package migration

import (
	"context"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"backend/internal/telemetry"

	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/promhttp"
	dto "github.com/prometheus/client_model/go"
)

func workflowMetric(t *testing.T, registry *prometheus.Registry, name string, labels ...string) float64 {
	t.Helper()
	sample := workflowMetricSample(t, registry, name, labels...)
	if sample == nil {
		return -1
	}
	switch {
	case sample.Gauge != nil:
		return sample.Gauge.GetValue()
	case sample.Counter != nil:
		return sample.Counter.GetValue()
	default:
		return float64(sample.Histogram.GetSampleCount())
	}
}

func workflowMetricSample(t *testing.T, registry *prometheus.Registry, name string, labels ...string) *dto.Metric {
	t.Helper()
	families, err := registry.Gather()
	if err != nil {
		t.Fatal(err)
	}
	for _, family := range families {
		if family.GetName() != "ruangsuara_"+name {
			continue
		}
		for _, sample := range family.Metric {
			if len(labels) != 2*len(sample.Label) {
				continue
			}
			matched := 0
			for _, label := range sample.Label {
				for i := 0; i < len(labels); i += 2 {
					if label.GetName() == labels[i] && label.GetValue() == labels[i+1] {
						matched++
					}
				}
			}
			if matched == len(sample.Label) {
				return sample
			}
		}
	}
	return nil
}

func TestCompensatedRowsIncludesShortLastBatch(t *testing.T) {
	progress := batchProgress{Size: 2, Batches: 3, Rows: 5}
	for reverted, rows := range []int64{0, 1, 3, 5} {
		progress.Reverted = int64(reverted)
		if got := progress.compensatedRows(); got != rows {
			t.Fatalf("after %d reverted batches got %d rows, want %d", reverted, got, rows)
		}
	}
}

func TestStateMetricsDistinguishEmptyAndUnavailable(t *testing.T) {
	h := newJobHarness(t)
	ctx := context.Background()
	pool, err := pgxpool.NewWithConfig(ctx, h.platform.Config())
	if err != nil {
		t.Fatal(err)
	}
	defer pool.Close()
	probeRepository := &Repository{Pool: pool}
	registry := telemetry.NewMetricsRegistry()
	probeRepository.RegisterStateMetrics(registry)

	readyBefore := workflowMetric(t, registry, "batch_jobs", "state", "ready")
	runningBefore := workflowMetric(t, registry, "change_requests", "status", "running")
	queuedBefore := workflowMetric(t, registry, "change_requests", "status", "queued")
	pendingBefore := workflowMetric(t, registry, "change_requests", "status", "pending_review")
	if workflowMetric(t, registry, "state_collection_success") != 1 ||
		workflowMetric(t, registry, "batch_jobs", "state", "compensating") != 0 ||
		workflowMetric(t, registry, "oldest_queued_timestamp_seconds") != 0 ||
		workflowMetric(t, registry, "database_blocked_queued_requests") != 0 {
		t.Fatal("expected healthy collection with a zero for the absent compensating state")
	}
	_, up, down := batchFixture(t, h.target, 5, false)
	id := h.submit(t, up, down, 2)
	t.Cleanup(func() {
		h.execPlatform(t, `UPDATE dmp_migration.change_requests
 SET status=CASE WHEN status='running' THEN 'failed' ELSE 'cancelled' END,completed_at=now()
 WHERE id=$1 AND status IN ('pending_review','queued','running')`, id)
	})
	if workflowMetric(t, registry, "change_requests", "status", "pending_review") != pendingBefore+1 ||
		workflowMetric(t, registry, "batch_jobs", "state", "ready") != readyBefore+1 ||
		workflowMetric(t, registry, "oldest_queued_timestamp_seconds") != 0 {
		t.Fatal("pending review and batch readiness should be visible independently")
	}
	h.approve(t, id)
	if workflowMetric(t, registry, "change_requests", "status", "queued") != queuedBefore+1 {
		t.Fatal("approval did not expose queued execution")
	}
	q, err := h.r.repo().claim(ctx, h.r.revalidate)
	if err != nil || q == nil || q.ID != id {
		t.Fatalf("could not claim fixture: %v", err)
	}
	for _, state := range []string{"partial", "paused", "unresolved"} {
		before := workflowMetric(t, registry, "batch_jobs", "state", state)
		if _, err = pool.Exec(ctx, `UPDATE dmp_migration.batch_jobs SET state=$2 WHERE id=$1`, id, state); err != nil {
			t.Fatal(err)
		}
		if workflowMetric(t, registry, "batch_jobs", "state", state) != before+1 ||
			workflowMetric(t, registry, "change_requests", "status", "running") != runningBefore+1 {
			t.Fatalf("request status masked the batch's %s state", state)
		}
	}
	pool.Close()
	if workflowMetric(t, registry, "state_collection_success") != 0 ||
		workflowMetric(t, registry, "change_requests", "status", "running") != -1 ||
		workflowMetric(t, registry, "batch_jobs", "state", "unresolved") != -1 ||
		workflowMetric(t, registry, "oldest_queued_timestamp_seconds") != -1 ||
		workflowMetric(t, registry, "database_blocked_queued_requests") != -1 {
		t.Fatal("unavailable state collection should omit counts and report failure")
	}
}

func TestQueueDiagnostics(t *testing.T) {
	h, independent := newJobHarness(t), newJobHarness(t)
	ctx := t.Context()
	registry := telemetry.NewMetricsRegistry()
	h.r.repo().RegisterStateMetrics(registry)

	_, up, down := batchFixture(t, h.target, 5, false)
	batch := h.submit(t, up, down, 2)
	first := h.submit(t, "SELECT 1", "SELECT 2", 0)
	second := h.submit(t, "SELECT 3", "SELECT 4", 0)
	runnable := independent.submit(t, "SELECT 5", "SELECT 6", 0)
	h.approve(t, batch)
	q, err := h.r.repo().claim(ctx, h.r.revalidate)
	if err != nil || q == nil || q.ID != batch {
		t.Fatalf("claim batch: %v, %v", q, err)
	}
	h.execPlatform(t, `UPDATE dmp_migration.batch_jobs SET state='paused',committed_batches=1,committed_rows=2 WHERE id=$1`, batch)
	h.approve(t, first)
	oldest := workflowMetric(t, registry, "oldest_queued_timestamp_seconds")
	age := float64(time.Now().UnixNano())/1e9 - oldest
	h.approve(t, second)
	independent.approve(t, runnable)
	if later := workflowMetric(t, registry, "oldest_queued_timestamp_seconds"); later != oldest || oldest <= 0 ||
		float64(time.Now().UnixNano())/1e9-later <= age {
		t.Fatalf("oldest timestamp should stay fixed while age grows: %f, %f", oldest, later)
	}
	if got := workflowMetric(t, registry, "database_blocked_queued_requests"); got != 2 {
		t.Fatalf("paused batch should block two of the three jobs: got %g", got)
	}
	logDiagnosticSamples(t, registry, "database_blocked_queued_requests", "oldest_queued_timestamp_seconds")

	attemptID := newID()
	h.execPlatform(t, `INSERT INTO dmp_migration.attempts
 (id,migration_id,database_id,sql_script_snapshot,executor_user_id,status)
 VALUES ($1,$2,$3,'SELECT 1',$4,'unknown')`, attemptID, q.MigrationID, h.db, jobEditor)
	if got := workflowMetric(t, registry, "database_blocked_queued_requests"); got != 2 {
		t.Fatalf("a request and an attempt must not double-count jobs: got %g", got)
	}
	h.execPlatform(t, `UPDATE dmp_migration.change_requests SET status='failed',completed_at=now() WHERE id=$1`, batch)
	if got := workflowMetric(t, registry, "database_blocked_queued_requests"); got != 2 {
		t.Fatalf("an unresolved attempt alone should block both jobs: got %g", got)
	}
	q, err = h.r.repo().claim(ctx, independent.r.revalidate)
	if err != nil || q == nil || q.ID != runnable {
		t.Fatalf("scheduler should still claim the independent database: %v, %v", q, err)
	}
	h.execPlatform(t, `UPDATE dmp_migration.change_requests SET status='cancelled',completed_at=now() WHERE id=ANY($1)`, []string{first, second})
	independent.r.recoverOne(ctx, InFlightRequest{
		ID: q.ID, DatabaseID: q.DatabaseID, MigrationID: q.MigrationID, RequestedBy: q.RequestedBy,
		Operation: q.Operation, Status: RequestRunning, StartedAt: q.startedAt,
	})
	if got := workflowMetric(t, registry, "oldest_queued_timestamp_seconds"); got <= float64(q.startedAt.UnixNano())/1e9 {
		t.Fatalf("recovered request should age from its requeue: timestamp %f, claim %s", got, q.startedAt)
	}
	independent.execPlatform(t, `UPDATE dmp_migration.change_requests SET status='cancelled',completed_at=now() WHERE id=$1`, runnable)
}

func TestOrdinaryAdvisoryRetryWait(t *testing.T) {
	h := newJobHarness(t)
	registry := telemetry.NewMetricsRegistry()
	const metric = "ordinary_advisory_lock_retry_wait_seconds"
	before := workflowMetricSample(t, registry, metric).GetHistogram()
	id := h.submit(t, "SELECT 1", "SELECT 2", 0)
	h.approve(t, id)
	unlock := holdTargetAdvisoryLock(t, h.target)
	defer unlock()
	busy := workflowMetric(t, registry, "lock_contention_total", "scope", "target")

	ctx, cancel := context.WithCancel(t.Context())
	done := h.r.StartWorker(ctx, WorkerConfig{PollInterval: 25 * time.Millisecond, MaxConcurrent: 1, Metrics: registry})
	defer func() { cancel(); <-done }()
	waitForDiagnostic(t, func() bool { return workflowMetric(t, registry, "lock_contention_total", "scope", "target") > busy })
	if workflowMetric(t, registry, "worker_slots_occupied") != 1 || workflowMetric(t, registry, "worker_slots_max") != 1 {
		t.Fatal("expected the running attempt to occupy the only worker slot")
	}
	logDiagnosticSamples(t, registry, "worker_slots_occupied", "worker_slots_max")
	h.wait(t, id, "failed", false)
	unlock()
	after := workflowMetricSample(t, registry, metric).GetHistogram()
	if after.GetSampleCount() != before.GetSampleCount()+1 || after.GetSampleSum()-before.GetSampleSum() < .75 ||
		workflowMetric(t, registry, "lock_contention_total", "scope", "target") != busy+3 {
		t.Fatalf("three busy probes should yield one sample with both retry sleeps: count=%d sum=%f", after.GetSampleCount(), after.GetSampleSum())
	}
	logDiagnosticSamples(t, registry, metric+"_count", metric+"_sum")

	rel, _, _ := batchFixture(t, h.target, 1, false)
	// A real SQLSTATE 55P03 after advisory lock acquisition still retries, but contributes no advisory wait.
	id = h.submit(t, "SELECT id FROM "+rel+" FOR UPDATE NOWAIT", "SELECT 2", 0)
	tx, err := h.target.Begin(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = tx.Rollback(t.Context()) }()
	if _, err = tx.Exec(t.Context(), "SELECT id FROM "+rel+" FOR UPDATE"); err != nil {
		t.Fatal(err)
	}
	retries := workflowMetric(t, registry, "migration_retries_total", "kind", "ordinary_transaction")
	h.approve(t, id)
	h.wait(t, id, "failed", false)
	if workflowMetric(t, registry, metric) != float64(after.GetSampleCount()) ||
		workflowMetric(t, registry, "migration_retries_total", "kind", "ordinary_transaction") != retries+2 {
		t.Fatal("a PostgreSQL row-lock error should retry without contributing advisory wait")
	}
	waitForDiagnostic(t, func() bool { return workflowMetric(t, registry, "worker_slots_occupied") == 0 })
	cancel()
	<-done
	if workflowMetric(t, registry, "worker_slots_occupied") != -1 || workflowMetric(t, registry, "worker_slots_max") != -1 {
		t.Fatal("worker gauges should be absent after shutdown")
	}
}

func TestBatchAdvisoryContentionIntervals(t *testing.T) {
	h := newJobHarness(t)
	registry := telemetry.NewMetricsRegistry()
	const metric = "batch_advisory_lock_contention_interval_seconds"
	_, up, down := batchFixture(t, h.target, 5, false)
	id := h.submit(t, up, down, 2)
	h.approve(t, id)
	_, releasePlatform, err := h.r.repo().lock(t.Context(), h.db)
	if err != nil {
		t.Fatal(err)
	}
	releasePlatform = sync.OnceFunc(releasePlatform)
	defer releasePlatform()
	releaseTarget := holdTargetAdvisoryLock(t, h.target)
	defer releaseTarget()
	h.worker(t)

	for _, scope := range []struct {
		name    string
		release func()
	}{{"platform", releasePlatform}, {"target", releaseTarget}} {
		before := workflowMetricSample(t, registry, metric, "scope", scope.name).GetHistogram()
		busy := workflowMetric(t, registry, "lock_contention_total", "scope", scope.name)
		waitForDiagnostic(t, func() bool { return workflowMetric(t, registry, "lock_contention_total", "scope", scope.name) > busy })
		firstSeen := time.Now()
		waitForDiagnostic(t, func() bool {
			return workflowMetric(t, registry, "lock_contention_total", "scope", scope.name) >= busy+3
		})
		minimum := time.Since(firstSeen).Seconds()
		if workflowMetric(t, registry, metric, "scope", scope.name) != float64(before.GetSampleCount()) {
			t.Fatalf("%s interval should be open while the lock is held", scope.name)
		}
		scope.release()
		waitForDiagnostic(t, func() bool {
			return workflowMetric(t, registry, metric, "scope", scope.name) == float64(before.GetSampleCount()+1)
		})
		after := workflowMetricSample(t, registry, metric, "scope", scope.name).GetHistogram()
		if elapsed := after.GetSampleSum() - before.GetSampleSum(); elapsed < minimum {
			t.Fatalf("%s interval lost the first busy probe: got %fs, need at least %fs", scope.name, elapsed, minimum)
		}
	}
	h.wait(t, id, "completed", false)
	logDiagnosticSamples(t, registry, metric+"_count", metric+"_sum")
}

func holdTargetAdvisoryLock(t *testing.T, pool *pgxpool.Pool) func() {
	t.Helper()
	conn, err := pool.Acquire(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	if _, err = conn.Exec(t.Context(), `SELECT pg_advisory_lock(781349,3)`); err != nil {
		conn.Release()
		t.Fatal(err)
	}
	return sync.OnceFunc(func() {
		defer conn.Release()
		if _, err := conn.Exec(t.Context(), `SELECT pg_advisory_unlock(781349,3)`); err != nil {
			_ = conn.Conn().Close(context.Background())
			t.Error(err)
		}
	})
}

func waitForDiagnostic(t *testing.T, ready func() bool) {
	t.Helper()
	for deadline := time.Now().Add(5 * time.Second); time.Now().Before(deadline); time.Sleep(5 * time.Millisecond) {
		if ready() {
			return
		}
	}
	t.Fatal("timed out waiting for a diagnostic metric")
}

func logDiagnosticSamples(t *testing.T, registry *prometheus.Registry, names ...string) {
	t.Helper()
	w := httptest.NewRecorder()
	promhttp.HandlerFor(registry, promhttp.HandlerOpts{}).ServeHTTP(w, httptest.NewRequest("GET", "/metrics", nil))
	if w.Code != 200 {
		t.Fatalf("metric scrape returned %d", w.Code)
	}
	for _, line := range strings.Split(w.Body.String(), "\n") {
		for _, name := range names {
			if strings.HasPrefix(line, "ruangsuara_"+name+" ") || strings.HasPrefix(line, "ruangsuara_"+name+"{") {
				t.Log(line)
			}
		}
	}
}
