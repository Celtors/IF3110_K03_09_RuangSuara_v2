package migration

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/jackc/pgx/v5/pgxpool"
)

type jobTestAccess struct {
	mu      sync.Mutex
	revoked bool
}

func (*jobTestAccess) Identity(c *gin.Context) (string, error) {
	return c.GetHeader("X-Test-User"), nil
}
func (a *jobTestAccess) Permission(_ context.Context, user, _ string) (Permission, error) {
	a.mu.Lock()
	defer a.mu.Unlock()
	switch user {
	case "editor":
		if !a.revoked {
			return Edit, nil
		}
	case "leader":
		return View, nil
	}
	return NoAccess, nil
}
func (*jobTestAccess) CSRF(*http.Request) error { return nil }

type jobTestApprovers struct{}

func (jobTestApprovers) RequiredApprovers(context.Context, string) ([]RequiredApprover, error) {
	return []RequiredApprover{{GroupID: "g1", GroupName: "test", LeaderID: "leader", LeaderActive: true}}, nil
}

type jobHarness struct {
	r                *Review
	router           *gin.Engine
	target, platform *pgxpool.Pool
	db               string
	access           *jobTestAccess
}

func newJobHarness(t *testing.T) *jobHarness {
	t.Helper()
	target := batchTestTarget(t)
	dsn := os.Getenv("SQL_CONSOLE_TEST_PLATFORM_DSN")
	if dsn == "" {
		t.Skip("set SQL_CONSOLE_TEST_PLATFORM_DSN")
	}
	ctx := context.Background()
	config, err := pgxpool.ParseConfig(dsn)
	if err != nil {
		t.Fatal(err)
	}
	config.MaxConns = 12
	platform, err := pgxpool.NewWithConfig(ctx, config)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(platform.Close)
	repo := &Repository{Pool: platform}
	if err = repo.Initialize(ctx); err != nil {
		t.Fatal(err)
	}
	access := &jobTestAccess{}
	r := NewReview(&Service{Repository: repo, Access: access, Connections: fakeConnections{pool: target}})
	r.Approvers = jobTestApprovers{}
	gin.SetMode(gin.TestMode)
	router := gin.New()
	RegisterReviewRoutes(router, r)
	db := newID()
	h := &jobHarness{r, router, target, platform, db, access}
	t.Cleanup(func() { // cleanup only this test database's records; audit triggers may reject DELETE, so require disposable platform
		jobs, e := platform.Query(ctx, `SELECT b.id FROM dmp_migration.batch_jobs b JOIN dmp_migration.change_requests r ON r.id=b.id WHERE r.database_id=$1`, db)
		if e == nil {
			for jobs.Next() {
				var id string
				if jobs.Scan(&id) == nil {
					_, _ = target.Exec(ctx, `DELETE FROM dmp_batch.scope WHERE job_id=$1`, id)
					_, _ = target.Exec(ctx, `DELETE FROM dmp_batch.jobs WHERE id=$1`, id)
				}
			}
			jobs.Close()
		}
		_, _ = platform.Exec(ctx, `DELETE FROM dmp_migration.batch_jobs WHERE id IN (SELECT id FROM dmp_migration.change_requests WHERE database_id=$1)`, db)
		_, _ = platform.Exec(ctx, `DELETE FROM dmp_migration.change_requests WHERE database_id=$1`, db)
		_, _ = platform.Exec(ctx, `DELETE FROM dmp_migration.attempts WHERE database_id=$1`, db)
		_, _ = platform.Exec(ctx, `DELETE FROM dmp_migration.definitions WHERE database_id=$1`, db)
	})
	return h
}
func (h *jobHarness) request(t *testing.T, user, method, path string, body any, want int) map[string]any {
	t.Helper()
	raw, _ := json.Marshal(body)
	req := httptest.NewRequest(method, "/api/databases/"+h.db+path, bytes.NewReader(raw))
	req.Header.Set("X-Test-User", user)
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	h.router.ServeHTTP(w, req)
	if w.Code != want {
		t.Fatalf("%s %s => %d expected %d: %s", method, path, w.Code, want, w.Body.String())
	}
	var out map[string]any
	if err := json.Unmarshal(w.Body.Bytes(), &out); err != nil {
		t.Fatal(err)
	}
	return out
}
func (h *jobHarness) submit(t *testing.T, up, down string, batch int) string {
	t.Helper()
	m, err := h.r.Service.Save(context.Background(), "editor", h.db, "", Draft{Name: newID(), SQLUp: up, SQLDown: down})
	if err != nil {
		t.Fatal(err)
	}
	in := SubmitRequest{MigrationID: m.ID, Operation: OperationApply}
	if batch > 0 {
		in.Mode = "batch"
		in.BatchAttribute = "id"
		in.BatchSize = batch
	}
	out := h.request(t, "editor", "POST", "/change-requests", in, 201)
	id := out["job_id"].(string)
	return id
}
func (h *jobHarness) approve(t *testing.T, id string) {
	h.request(t, "leader", "POST", "/change-requests/"+id+"/approve", DecisionBody{}, 200)
}
func (h *jobHarness) status(t *testing.T, id string) map[string]any {
	return h.request(t, "editor", "GET", "/jobs/"+id, nil, 200)
}
func (h *jobHarness) wait(t *testing.T, id, state string, paused bool) map[string]any {
	t.Helper()
	deadline := time.Now().Add(4 * time.Minute)
	for time.Now().Before(deadline) {
		p := h.status(t, id)
		if p["status"] == state && (!paused || p["paused"] == true) {
			return p
		}
		if state == "completed" && (p["status"] == "failed" || p["paused"] == true) {
			t.Fatalf("job did not complete: %+v", p)
		}
		time.Sleep(20 * time.Millisecond)
	}
	t.Fatalf("timed out waiting for %s: %+v", state, h.status(t, id))
	return nil
}
func (h *jobHarness) worker(t *testing.T) func() {
	t.Helper()
	ctx, cancel := context.WithCancel(context.Background())
	done := h.r.StartWorker(ctx, WorkerConfig{PollInterval: 25 * time.Millisecond, RecoveryInterval: 100 * time.Millisecond, OrphanAfter: 2 * time.Second, MaxConcurrent: 2})
	var once sync.Once
	stop := func() { once.Do(func() { cancel(); <-done }) }
	t.Cleanup(stop)
	return stop
}
func TestAsyncJobsAndRecoveryIntegration(t *testing.T) {
	h := newJobHarness(t)
	rel, up, down := batchFixture(t, h.target, 5, true)
	id := h.submit(t, up, down, 2)
	if p := h.status(t, id); p["status"] != "pending_review" {
		t.Fatal(p)
	}
	h.request(t, "editor", "POST", "/change-requests/"+id+"/approve", DecisionBody{}, 403)
	h.request(t, "none", "GET", "/jobs/"+id, nil, 403)
	h.approve(t, id)
	if h.status(t, id)["status"] != "queued" || score(t, h.target, rel, 1) != 0 {
		t.Fatal("approval waited for execution or ran SQL inline")
	}
	// Start after submission, as after a process restart.
	stop := h.worker(t)
	p := h.wait(t, id, "partial", true)
	if p["committed_batches"] != float64(1) || p["committed_rows"] != float64(2) {
		t.Fatal(p)
	}
	// Later same-database operation remains queued while partial.
	later := h.submit(t, `UPDATE `+rel+` SET score=score+2`, `UPDATE `+rel+` SET score=score-2`, 0)
	h.approve(t, later)
	time.Sleep(150 * time.Millisecond)
	if h.status(t, later)["status"] != "queued" {
		t.Fatal("partial did not hold database")
	}
	stop()
	mustExec(t, h.target, `ALTER TABLE `+rel+` DROP CONSTRAINT limited_score`)
	h.request(t, "editor", "POST", "/jobs/"+id+"/resume", nil, 202)
	h.worker(t)
	p = h.wait(t, id, "completed", false)
	if p["committed_batches"] != float64(3) || p["committed_rows"] != float64(5) {
		t.Fatal(p)
	}
	h.wait(t, later, "completed", false)
}
func TestAsyncPermissionRevalidationIntegration(t *testing.T) {
	h := newJobHarness(t)
	rel, _, _ := batchFixture(t, h.target, 2, false)
	id := h.submit(t, `UPDATE `+rel+` SET score=score+1`, `UPDATE `+rel+` SET score=score-1`, 0)
	h.approve(t, id)
	h.access.mu.Lock()
	h.access.revoked = true
	h.access.mu.Unlock()
	h.worker(t)
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		q, e := h.r.Get(context.Background(), "leader", h.db, id)
		if e != nil {
			t.Fatal(e)
		}
		if q.Status == RequestCancelled {
			if score(t, h.target, rel, 1) != 0 {
				t.Fatal("revoked requester executed")
			}
			return
		}
		time.Sleep(25 * time.Millisecond)
	}
	t.Fatal("worker did not cancel revoked requester")
}
func TestBatchUnmirroredCommitRecoveryIntegration(t *testing.T) {
	h := newJobHarness(t)
	rel, up, down := batchFixture(t, h.target, 5, false)
	id := h.submit(t, up, down, 2)
	h.approve(t, id)
	ctx := context.Background()
	q, err := h.r.repo().claim(ctx, h.r.revalidate)
	if err != nil || q == nil || q.ID != id {
		t.Fatalf("claim %v %v", q, err)
	}
	c, release, err := h.r.repo().lock(ctx, h.db)
	if err != nil {
		t.Fatal(err)
	}
	target, err := h.target.Acquire(ctx)
	if err != nil {
		release()
		t.Fatal(err)
	}
	func() {
		defer release()
		defer target.Release()
		m, e := h.r.repo().Get(ctx, h.db, q.MigrationID)
		if e != nil {
			t.Fatal(e)
		}
		a, e := beginAttempt(ctx, c, m, q.RequestedBy, OperationApply, up, nil)
		if e != nil {
			t.Fatal(e)
		}
		identity, e := targetIdentity(ctx, target.Conn())
		if e != nil {
			t.Fatal(e)
		}
		if _, e = c.Exec(ctx, `UPDATE dmp_migration.batch_jobs SET target_identity=$2,intent='scope' WHERE id=$1`, id, identity); e != nil {
			t.Fatal(e)
		}
		if _, e = c.Exec(ctx, `UPDATE dmp_migration.change_requests SET attempt_id=$2 WHERE id=$1`, id, a.ID); e != nil {
			t.Fatal(e)
		}
		if _, e = target.Exec(ctx, targetBatchDDL); e != nil {
			t.Fatal(e)
		}
		b, e := parseBatch(up, BatchSpec{"id", 2})
		if e != nil {
			t.Fatal(e)
		}
		if e = initBatchScope(ctx, target, id, b); e != nil {
			t.Fatal(e)
		}
		p := batchProgress{Size: 2}
		if e = targetProgress(ctx, target, id, &p); e != nil {
			t.Fatal(e)
		}
		if e = batchIntent(ctx, c, id, "apply:1"); e != nil {
			t.Fatal(e)
		}
		if e = runBatchTx(ctx, target, id, up, b, p, false); e != nil {
			t.Fatal(e)
		}
		// Simulated process death: no platform progress mirror after target COMMIT.
	}()
	if h.status(t, id)["committed_rows"] != float64(0) || score(t, h.target, rel, 1) != 1 {
		t.Fatal("interruption fixture invalid")
	}
	h.worker(t)
	p := h.wait(t, id, "completed", false)
	if p["committed_rows"] != float64(5) || score(t, h.target, rel, 1) != 1 || score(t, h.target, rel, 5) != 10 {
		t.Fatalf("recovery double-applied or skipped rows: %+v", p)
	}
	// Rollback and forward use the original completed batch scope through the queue.
	mustExec(t, h.target, `INSERT INTO `+rel+`(score) VALUES(20)`)
	for _, op := range []Operation{OperationRollback, OperationForward} {
		out := h.request(t, "editor", "POST", "/change-requests", SubmitRequest{MigrationID: q.MigrationID, Operation: op}, 201)
		jid := out["job_id"].(string)
		h.approve(t, jid)
		h.wait(t, jid, "completed", false)
		expected := 0
		if op == OperationForward {
			expected = 1
		}
		if score(t, h.target, rel, 1) != expected || score(t, h.target, rel, 6) != 20 {
			t.Fatalf("scoped %s failed", op)
		}
	}
}

// Mandatory acceptance test. Run explicitly on disposable PostgreSQL databases.
// The baseline uses real heap updates (no artificial pg_sleep).
func TestBatchPerformanceAcceptance(t *testing.T) {
	if os.Getenv("MIGRATION_RUN_PERFORMANCE") != "1" {
		t.Skip("set MIGRATION_RUN_PERFORMANCE=1")
	}
	h := newJobHarness(t)
	ctx := context.Background()
	rel, up, down := batchFixture(t, h.target, 0, false)
	rows := 1000000
	batchSize := 10000
	for {
		mustExec(t, h.target, `TRUNCATE `+rel+` RESTART IDENTITY`)
		mustExec(t, h.target, fmt.Sprintf(`INSERT INTO %s(score,payload) SELECT 0,repeat(md5(g::text),4) FROM generate_series(1,%d) g`, rel, rows))
		start := time.Now()
		mustExec(t, h.target, `UPDATE `+rel+` SET score=score+1`)
		elapsed := time.Since(start)
		t.Logf("direct baseline rows=%d duration=%s", rows, elapsed)
		mustExec(t, h.target, `UPDATE `+rel+` SET score=0`)
		if elapsed > time.Second || rows >= 4000000 {
			if elapsed <= time.Second {
				t.Log("environment did not reliably exceed one second; report this limitation")
			}
			break
		}
		rows *= 2
	}
	h.worker(t)
	normal := h.submit(t, strings.Split(up, " WHERE ")[0], strings.Split(down, " WHERE ")[0], 0)
	start := time.Now()
	h.approve(t, normal)
	normalStatus := h.wait(t, normal, "completed", false)
	normalDuration := time.Since(start)
	var normalCount, normalSum, normalWrong int64
	if err := h.target.QueryRow(ctx, `SELECT count(*),sum(score),count(*) FILTER(WHERE score<>1) FROM `+rel).Scan(&normalCount, &normalSum, &normalWrong); err != nil {
		t.Fatal(err)
	}
	if normalWrong != 0 || normalCount != int64(rows) {
		t.Fatalf("normal results invalid: count=%d wrong=%d", normalCount, normalWrong)
	}
	mustExec(t, h.target, `UPDATE `+rel+` SET score=0`)
	// Same physical fixture and managed database, with data reset between runs.
	batch := h.submit(t, up, down, batchSize)
	start = time.Now()
	h.approve(t, batch)
	var prev float64 = -1
	var final map[string]any
	deadline := time.Now().Add(15 * time.Minute)
	for time.Now().Before(deadline) {
		p := h.status(t, batch)
		if n, ok := p["committed_batches"].(float64); ok && n != prev {
			t.Logf("batch API progress: batches=%v rows=%v checkpoint=%v status=%v", n, p["committed_rows"], p["checkpoint"], p["status"])
			prev = n
		}
		if p["status"] == "completed" {
			final = p
			break
		}
		if p["status"] == "failed" || p["paused"] == true {
			t.Fatalf("batch failed: %+v", p)
		}
		time.Sleep(100 * time.Millisecond)
	}
	if final == nil {
		t.Fatal("performance batch timed out")
	}
	batchDuration := time.Since(start)
	var count, sum, wrong int64
	if err := h.target.QueryRow(ctx, `SELECT count(*),sum(score),count(*) FILTER(WHERE score<>1) FROM `+rel).Scan(&count, &sum, &wrong); err != nil {
		t.Fatal(err)
	}
	if count != normalCount || sum != normalSum || wrong != 0 || final["committed_rows"] != float64(rows) || final["committed_batches"] != float64((rows+batchSize-1)/batchSize) {
		t.Fatalf("non-equivalent dataset or counts: %+v", final)
	}
	var version string
	_ = h.target.QueryRow(ctx, `SELECT version()`).Scan(&version)
	t.Logf("environment=%s rows=%d batch_size=%d normal_duration=%s batch_duration=%s normal_job=%v final_batch=%v", version, rows, batchSize, normalDuration, batchDuration, normalStatus, final)
}

func TestCompensationJobIntegration(t *testing.T) {
	h := newJobHarness(t)
	rel, up, down := batchFixture(t, h.target, 5, true)
	id := h.submit(t, up, down, 2)
	h.approve(t, id)
	h.worker(t)
	h.wait(t, id, "partial", true)
	h.request(t, "leader", "POST", "/jobs/"+id+"/compensate", nil, 403)
	h.request(t, "editor", "POST", "/jobs/"+id+"/compensate", nil, 202)
	p := h.wait(t, id, "reverted", false)
	if p["compensation_status"] != "reverted" || p["reverted_batches"] != float64(1) || score(t, h.target, rel, 1) != 0 || score(t, h.target, rel, 3) != 9 {
		t.Fatalf("compensation failed: %v", p)
	}
}
