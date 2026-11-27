package migration

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"net/http/httptest"
	"os"
	"strings"
	"sync"
	"testing"
	"time"

	"backend/internal/telemetry"

	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/codes"
	sdktrace "go.opentelemetry.io/otel/sdk/trace"
	"go.opentelemetry.io/otel/sdk/trace/tracetest"
	"gorm.io/driver/postgres"
	"gorm.io/gorm"
)

const (
	jobEditor   = "77777777-7777-4777-8777-000000000001"
	jobLeader   = "77777777-7777-4777-8777-000000000002"
	jobReviewer = "77777777-7777-4777-8777-000000000003"
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
	case jobEditor:
		if !a.revoked {
			return Edit, nil
		}
	case jobLeader, jobReviewer:
		return View, nil
	}
	return NoAccess, nil
}

type jobHarness struct {
	r                *Review
	router           *gin.Engine
	target, platform *pgxpool.Pool
	db               string
	access           *jobTestAccess
	group            string
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
	config.ConnConfig.Tracer = telemetry.PostgresTracer{Scope: "platform"}
	platform, err := pgxpool.NewWithConfig(ctx, config)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(platform.Close)
	var hasAccessSchema bool
	if err = platform.QueryRow(ctx, `SELECT to_regclass('public.users') IS NOT NULL`).Scan(&hasAccessSchema); err != nil {
		t.Fatal(err)
	}
	if !hasAccessSchema {
		schema, err := os.ReadFile("../../../database_schema/access-control/access-control-db.sql")
		if err != nil {
			t.Fatal(err)
		}
		mustExec(t, platform, string(schema))
	}
	repo := &Repository{Pool: platform}
	if err = repo.Initialize(ctx); err != nil {
		t.Fatal(err)
	}
	access := &jobTestAccess{}
	r := NewReview(&Service{Repository: repo, Access: access, Connections: fakeConnections{pool: target}})
	gin.SetMode(gin.TestMode)
	router := gin.New()
	router.Use(telemetry.HTTP)
	RegisterReviewRoutes(router, r)
	RegisterRoutes(router, r.Service)
	db := uuid.NewString()
	h := &jobHarness{r: r, router: router, target: target, platform: platform, db: db, access: access}
	h.execPlatform(t, `INSERT INTO users (id,username,password_hash) VALUES
 ($1,'job-editor','fixture'), ($2,'job-leader','fixture'), ($3,'job-reviewer','fixture')
 ON CONFLICT (id) DO NOTHING`, jobEditor, jobLeader, jobReviewer)
	h.execPlatform(t, `INSERT INTO databases (id,name,host,port,database_name,username,password_ciphertext)
 VALUES ($1,$2,'localhost',5432,'fixture','fixture','fixture')`, db, db)
	t.Cleanup(func() { h.execPlatform(t, `DELETE FROM databases WHERE id=$1`, db) })
	h.group = h.addApprovalGroup(t, "ViewEdit", jobEditor, jobLeader)
	h.execPlatform(t, `INSERT INTO group_leaders (group_id,leader_id) VALUES ($1,$2)`, h.group, jobLeader)
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

func (h *jobHarness) execPlatform(t *testing.T, sql string, args ...any) {
	t.Helper()
	if _, err := h.platform.Exec(context.Background(), sql, args...); err != nil {
		t.Fatal(err)
	}
}

func (h *jobHarness) addApprovalGroup(t *testing.T, effect string, members ...string) string {
	t.Helper()
	group := uuid.NewString()
	h.execPlatform(t, `INSERT INTO groups_ (id,name) VALUES ($1,$2)`, group, group)
	t.Cleanup(func() { h.execPlatform(t, `DELETE FROM groups_ WHERE id=$1`, group) })
	h.execPlatform(t, `INSERT INTO group_policies (group_id,database_id,effect) VALUES ($1,$2,$3)`, group, h.db, effect)
	for _, member := range members {
		h.execPlatform(t, `INSERT INTO user_groups (group_id,user_id) VALUES ($1,$2)`, group, member)
	}
	return group
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
	m, err := h.r.Service.Save(context.Background(), jobEditor, h.db, "", Draft{Name: newID(), SQLUp: up, SQLDown: down})
	if err != nil {
		t.Fatal(err)
	}
	in := SubmitRequest{MigrationID: m.ID, Operation: OperationApply}
	if batch > 0 {
		in.Mode = "batch"
		in.BatchAttribute = "id"
		in.BatchSize = batch
	}
	out := h.request(t, jobEditor, "POST", "/change-requests", in, 201)
	id := out["job_id"].(string)
	return id
}
func (h *jobHarness) approve(t *testing.T, id string) {
	h.request(t, jobLeader, "POST", "/change-requests/"+id+"/approve", DecisionBody{}, 200)
}
func (h *jobHarness) status(t *testing.T, id string) map[string]any {
	return h.request(t, jobEditor, "GET", "/jobs/"+id, nil, 200)
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
func TestGroupApprovalsIntegration(t *testing.T) {
	h := newJobHarness(t)
	ctx := context.Background()
	group := h.addApprovalGroup(t, "ViewOnly", jobReviewer)
	id := h.submit(t, "CREATE TABLE public.group_approval_test (id integer)", "DROP TABLE public.group_approval_test", 0)
	approvePath := "/change-requests/" + id + "/approve"
	get := func(user string) ChangeRequest {
		t.Helper()
		q, err := h.r.Get(ctx, user, h.db, id)
		if err != nil {
			t.Fatal(err)
		}
		return q
	}
	approvalForGroup := func(q ChangeRequest) Approval {
		t.Helper()
		for _, a := range q.Approvals {
			if a.GroupID == group {
				return a
			}
		}
		t.Fatalf("required group %s missing from approvals: %+v", group, q.Approvals)
		return Approval{}
	}

	q := get(jobReviewer)
	h.request(t, jobEditor, "POST", "/migrations/"+q.MigrationID+"/execute", nil, 409)
	if a := approvalForGroup(q); len(q.Approvals) != 2 || a.RequiredLeaderID != "" || a.Mine || a.Decision != "pending" {
		t.Fatalf("leaderless group must await approval: %+v", q.Approvals)
	}
	h.request(t, jobEditor, "POST", approvePath, DecisionBody{}, 403)
	h.request(t, "outsider", "POST", approvePath, DecisionBody{}, 403)
	h.request(t, jobReviewer, "POST", approvePath, DecisionBody{}, 403)
	h.approve(t, id)
	if q = get(jobReviewer); q.Status != RequestPendingReview || q.QueuedAt != nil {
		t.Fatalf("queued before all groups approved: %+v", q)
	}

	h.execPlatform(t, `INSERT INTO group_leaders (group_id,leader_id) VALUES ($1,$2)`, group, jobReviewer)
	if a := approvalForGroup(get(jobReviewer)); !a.Mine || a.RequiredLeaderID != jobReviewer {
		t.Fatalf("new leader is missing from the pending approval: %+v", a)
	}
	h.execPlatform(t, `DELETE FROM user_groups WHERE group_id=$1 AND user_id=$2`, group, jobReviewer)
	h.request(t, jobReviewer, "POST", approvePath, DecisionBody{}, 403)
	h.execPlatform(t, `INSERT INTO user_groups (group_id,user_id) VALUES ($1,$2)`, group, jobReviewer)
	h.execPlatform(t, `UPDATE users SET status='Inactive' WHERE id=$1`, jobReviewer)
	t.Cleanup(func() { h.execPlatform(t, `UPDATE users SET status='Active' WHERE id=$1`, jobReviewer) })
	h.request(t, jobReviewer, "POST", approvePath, DecisionBody{}, 403)
	h.execPlatform(t, `UPDATE users SET status='Active' WHERE id=$1`, jobReviewer)
	inbox, err := h.r.PendingApprovals(ctx, jobReviewer)
	if err != nil || len(inbox) != 1 || inbox[0].ID != id || !approvalForGroup(inbox[0]).Mine {
		t.Fatalf("new leader's inbox: %+v, %v", inbox, err)
	}
	h.request(t, jobReviewer, "POST", approvePath, DecisionBody{}, 200)
	h.request(t, jobReviewer, "POST", approvePath, DecisionBody{}, 409)
	h.execPlatform(t, `INSERT INTO user_groups (group_id,user_id) VALUES ($1,$2)`, group, jobEditor)
	h.execPlatform(t, `UPDATE group_leaders SET leader_id=$2 WHERE group_id=$1`, group, jobEditor)

	q = get(jobEditor)
	a := approvalForGroup(q)
	queuedEvents := 0
	for _, event := range q.Events {
		if event.Event == "queued" {
			queuedEvents++
		}
	}
	if q.Status != RequestQueued || q.QueuedAt == nil || queuedEvents != 1 ||
		a.Decision != "approved" || a.DecidedBy == nil || *a.DecidedBy != jobReviewer ||
		a.RequiredLeaderID != jobReviewer || a.LeaderUsername != "job-reviewer" {
		t.Fatalf("final approval must queue once and retain the decision-maker: %+v", q)
	}
	h.request(t, jobEditor, "POST", "/change-requests/"+id+"/cancel", DecisionBody{}, 200)
}

func TestAsyncJobsAndRecoveryIntegration(t *testing.T) {
	h := newJobHarness(t)
	metrics := telemetry.NewMetricsRegistry()
	initialRows := workflowMetric(t, metrics, "batch_rows_total", "action", "apply")
	initialBatches := workflowMetric(t, metrics, "batch_commits_total", "action", "apply")
	initialCompletions := workflowMetric(t, metrics, "migration_completions_total", "mode", "batch", "operation", "apply", "outcome", "succeeded")
	initialFailed := workflowMetric(t, metrics, "migration_completions_total", "mode", "batch", "operation", "apply", "outcome", "failed")
	initialResume := workflowMetric(t, metrics, "migration_retries_total", "kind", "batch_resume_accepted")
	var logs bytes.Buffer
	slogBefore, tracingBefore := slog.Default(), otel.GetTracerProvider()
	exporter := tracetest.NewInMemoryExporter()
	provider := sdktrace.NewTracerProvider(sdktrace.WithSyncer(exporter))
	slog.SetDefault(slog.New(telemetry.NewHandler(&logs)))
	otel.SetTracerProvider(provider)
	t.Cleanup(func() {
		_ = provider.Shutdown(context.Background())
		slog.SetDefault(slogBefore)
		otel.SetTracerProvider(tracingBefore)
	})
	rel, up, down := batchFixture(t, h.target, 5, true)
	id := h.submit(t, up, down, 2)
	if p := h.status(t, id); p["status"] != "pending_review" {
		t.Fatal(p)
	}
	h.request(t, jobEditor, "POST", "/change-requests/"+id+"/approve", DecisionBody{}, 403)
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
	if workflowMetric(t, metrics, "batch_rows_total", "action", "apply") != initialRows+2 ||
		workflowMetric(t, metrics, "batch_commits_total", "action", "apply") != initialBatches+1 ||
		workflowMetric(t, metrics, "migration_completions_total", "mode", "batch", "operation", "apply", "outcome", "failed") != initialFailed {
		t.Fatal("paused batch counted rolled-back work or a terminal failure")
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
	h.request(t, jobEditor, "POST", "/jobs/"+id+"/resume", nil, 202)
	h.r = NewReview(h.r.Service) // Fresh worker state, same PostgreSQL storage.
	stop = h.worker(t)
	p = h.wait(t, id, "completed", false)
	if p["committed_batches"] != float64(3) || p["committed_rows"] != float64(5) {
		t.Fatal(p)
	}
	h.wait(t, later, "completed", false)
	stop()
	if workflowMetric(t, metrics, "batch_rows_total", "action", "apply") != initialRows+5 ||
		workflowMetric(t, metrics, "batch_commits_total", "action", "apply") != initialBatches+3 ||
		workflowMetric(t, metrics, "migration_completions_total", "mode", "batch", "operation", "apply", "outcome", "succeeded") != initialCompletions+1 ||
		workflowMetric(t, metrics, "migration_retries_total", "kind", "batch_resume_accepted") != initialResume+1 {
		t.Fatal("resume duplicated checkpoint progress or omitted a durable terminal outcome")
	}
	assertBatchResumeTelemetry(t, logs.String(), exporter.GetSpans(), id, strings.TrimPrefix(rel, "public."))
}

func assertBatchResumeTelemetry(t *testing.T, logs string, spans tracetest.SpanStubs, id, table string) {
	t.Helper()
	rawSpans, err := json.Marshal(spans)
	if err != nil || strings.Contains(logs, table) || bytes.Contains(rawSpans, []byte(table)) ||
		strings.Contains(logs, "limited_score") || bytes.Contains(rawSpans, []byte("limited_score")) {
		t.Fatal("batch query or error exposed a target identifier")
	}
	spanByID := make(map[string]tracetest.SpanStub, len(spans))
	for _, span := range spans {
		spanByID[span.SpanContext.SpanID().String()] = span
	}
	var records []map[string]any
	for line := range strings.SplitSeq(strings.TrimSpace(logs), "\n") {
		var record map[string]any
		if err = json.Unmarshal([]byte(line), &record); err != nil {
			t.Fatal(err)
		}
		if record["job_id"] == id {
			records = append(records, record)
		}
	}
	find := func(want map[string]any, spanName string) (map[string]any, tracetest.SpanStub) {
		t.Helper()
		for _, record := range records {
			matched := true
			for k, v := range want {
				matched = matched && record[k] == v
			}
			if !matched {
				continue
			}
			spanID, _ := record["span_id"].(string)
			span, exists := spanByID[spanID]
			if !exists || span.Name != spanName || record["trace_id"] != span.SpanContext.TraceID().String() || span.EndTime.Before(span.StartTime) {
				t.Fatalf("missing matching finished span for %v", want)
			}
			return record, span
		}
		t.Fatalf("missing batch log matching %v", want)
		return nil, tracetest.SpanStub{}
	}
	approval, publish := find(map[string]any{"event": "queue.submitted"}, "queue.publish")
	_, claim := find(map[string]any{"event": "queue.claim", "status": "running"}, "queue.claim")
	first, initial := find(map[string]any{"event": "batch.started", "reloaded": false}, "batch.execute")
	reloaded, resumed := find(map[string]any{"event": "batch.started", "reloaded": true}, "batch.execute")
	initialWorker := spanByID[initial.Parent.SpanID().String()]
	resumedWorker := spanByID[resumed.Parent.SpanID().String()]
	if claim.Parent.SpanID() != publish.SpanContext.SpanID() ||
		initialWorker.Name != "worker.process" || initialWorker.Parent.SpanID() != claim.SpanContext.SpanID() ||
		resumedWorker.Name != "worker.process" || !resumedWorker.Parent.Equal(publish.SpanContext.WithRemote(true)) ||
		first["trace_id"] != approval["trace_id"] || reloaded["trace_id"] != approval["trace_id"] ||
		first["correlation_id"] != approval["submission_correlation_id"] || reloaded["correlation_id"] != first["correlation_id"] {
		t.Fatal("initial claim or resumed worker lost its approval trace or submission correlation")
	}
	_, lock := find(map[string]any{"event": "migration.lock", "status": "acquired"}, "migration.lock")
	if lock.Parent.SpanID() != initial.SpanContext.SpanID() {
		t.Fatal("batch advisory lock is missing its execution parent")
	}
	paused, _ := find(map[string]any{"event": "batch.execution", "status": "paused", "committed_rows": float64(2)}, "batch.execute")
	failed, failedSQL := find(map[string]any{"event": "sql.finished", "database_scope": "target",
		"status": "failed", "sqlstate": "23514", "batch_number": float64(2)}, "db.query")
	_, rollback := find(map[string]any{"event": "transaction.rollback", "database_scope": "target",
		"status": "rolled_back", "batch_number": float64(2)}, "db.rollback")
	_, commit := find(map[string]any{"event": "transaction.commit", "database_scope": "target",
		"status": "committed", "batch_number": float64(2)}, "db.commit")
	failedBatch, committedBatch := spanByID[failedSQL.Parent.SpanID().String()], spanByID[commit.Parent.SpanID().String()]
	committedAttrs := attribute.NewSet(committedBatch.Attributes...)
	outcome, _ := committedAttrs.Value("outcome")
	batchNumber, _ := committedAttrs.Value("batch_number")
	if failedSQL.Status.Code != codes.Error || failedBatch.Name != "batch.transaction" ||
		rollback.Parent.SpanID() != failedBatch.SpanContext.SpanID() || failedBatch.Parent.SpanID() != initial.SpanContext.SpanID() ||
		committedBatch.Name != "batch.transaction" || committedBatch.Parent.SpanID() != resumed.SpanContext.SpanID() ||
		outcome.AsString() != "committed" || batchNumber.AsInt64() != 2 {
		t.Fatal("batch statement, rollback, or resumed commit span has the wrong outcome or parent")
	}
	verified, verifiedSpan := find(map[string]any{"event": "batch.checkpoint_verified", "checkpoint_kind": "batch",
		"status": "committed", "committed_batches": float64(2), "committed_rows": float64(4)}, "batch.execute")
	if !verifiedSpan.SpanContext.Equal(resumed.SpanContext) || paused["attempt_id"] == nil ||
		paused["attempt_id"] != failed["execution_id"] || paused["attempt_id"] != verified["attempt_id"] ||
		verified["trace_id"] != approval["trace_id"] {
		t.Fatal("verified progress is missing resumed execution metadata")
	}
	for _, record := range []map[string]any{failed, paused, verified} {
		data, _ := json.Marshal(record)
		t.Log(string(data))
	}
	t.Logf("exported %s trace_id=%s span_id=%s parent_span_id=%s batch_number=%d outcome=%s",
		committedBatch.Name, committedBatch.SpanContext.TraceID(), committedBatch.SpanContext.SpanID(),
		committedBatch.Parent.SpanID(), batchNumber.AsInt64(), outcome.AsString())
}
func TestAsyncPermissionRevalidationIntegration(t *testing.T) {
	h := newJobHarness(t)
	rel, _, _ := batchFixture(t, h.target, 2, false)
	id := h.submit(t, `UPDATE `+rel+` SET score=score+1`, `UPDATE `+rel+` SET score=score-1`, 0)
	h.approve(t, id)
	ctx := context.Background()
	accessDB, err := gorm.Open(postgres.Open(os.Getenv("SQL_CONSOLE_TEST_PLATFORM_DSN")))
	if err != nil {
		t.Fatal("could not open disposable authorization database")
	}
	sqlDB, err := accessDB.DB()
	if err != nil {
		t.Fatal("could not open disposable authorization pool")
	}
	defer sqlDB.Close()
	h.r.Service.Access = ApplicationAccess{Platform: accessDB}
	h.execPlatform(t, "UPDATE users SET status='Inactive' WHERE id=$1", jobEditor)
	defer h.execPlatform(t, "UPDATE users SET status='Active' WHERE id=$1", jobEditor)
	h.worker(t)
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		q, e := h.r.Get(ctx, jobLeader, h.db, id)
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

func TestAsyncRecoveryAfterAccessRevoked(t *testing.T) {
	h := newJobHarness(t)
	rel, _, _ := batchFixture(t, h.target, 1, false)
	up := `UPDATE ` + rel + ` SET score=score+1`
	id := h.submit(t, up, `UPDATE `+rel+` SET score=score-1`, 0)
	h.approve(t, id)
	ctx := context.Background()
	repo := h.r.repo()
	q, err := repo.claim(ctx, h.r.revalidate)
	if err != nil || q == nil || q.ID != id {
		t.Fatalf("claim %v %v", q, err)
	}
	m, err := repo.Get(ctx, h.db, q.MigrationID)
	if err != nil {
		t.Fatal(err)
	}
	var attempt Attempt
	func() {
		conn, release, e := repo.lock(ctx, h.db)
		if e != nil {
			t.Fatal(e)
		}
		defer release()
		attempt, e = beginAttempt(ctx, conn, m, q.RequestedBy, q.Operation, up, nil)
		if e != nil {
			t.Fatal(e)
		}
		if e = repo.linkAttempt(ctx, id, attempt.ID); e != nil {
			t.Fatal(e)
		}
		result := executePostgres(ctx, h.target, up, "", func(xid, identity string) error {
			_, e := conn.Exec(ctx, `UPDATE dmp_migration.attempts SET target_xid=$2,target_identity=$3 WHERE id=$1`, attempt.ID, xid, identity)
			return e
		}, func(int64) error { return nil }, nil)
		if result.Status != "succeeded" {
			t.Fatalf("interruption fixture did not commit: %+v", result)
		}
	}()
	h.access.mu.Lock()
	h.access.revoked = true
	h.access.mu.Unlock()
	if code, _ := faultInfo(h.r.Service.Reconcile(ctx, jobEditor, h.db)); code != "forbidden" {
		t.Fatalf("public reconciliation with revoked access returned %q", code)
	}
	candidate := InFlightRequest{ID: id, DatabaseID: h.db, MigrationID: m.ID, Operation: q.Operation,
		RequestedBy: q.RequestedBy, Status: RequestRunning, StartedAt: q.startedAt, queueTrace: q.queueTrace}
	stale := candidate
	stale.StartedAt = stale.StartedAt.Add(-time.Second)
	h.r.recoverOne(ctx, stale)
	stored, err := h.r.Get(ctx, jobLeader, h.db, id)
	if err != nil || stored.Status != RequestRunning {
		t.Fatalf("stale recovery changed the current claim: %v %v", stored.Status, err)
	}
	h.r.recoverOne(ctx, candidate)
	stored, err = h.r.Get(ctx, jobLeader, h.db, id)
	if err != nil || stored.Status != RequestSucceeded || stored.AttemptID == nil || *stored.AttemptID != attempt.ID {
		t.Fatalf("internal recovery did not settle the linked attempt: %v %v", stored.Status, err)
	}
	if got := score(t, h.target, rel, 1); got != 1 {
		t.Fatalf("recovery replayed committed SQL, score is %d", got)
	}
}
func TestBatchUnmirroredCommitRecoveryIntegration(t *testing.T) {
	h := newJobHarness(t)
	metrics := telemetry.NewMetricsRegistry()
	initialBatches := workflowMetric(t, metrics, "batch_commits_total", "action", "apply")
	initialRows := workflowMetric(t, metrics, "batch_rows_total", "action", "apply")
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
	if workflowMetric(t, metrics, "batch_rows_total", "action", "apply") != initialRows {
		t.Fatal("unverified platform progress was counted")
	}
	h.worker(t)
	p := h.wait(t, id, "completed", false)
	if p["committed_rows"] != float64(5) || score(t, h.target, rel, 1) != 1 || score(t, h.target, rel, 5) != 10 {
		t.Fatalf("recovery double-applied or skipped rows: %+v", p)
	}
	// Rollback and forward use the original completed batch scope through the queue.
	mustExec(t, h.target, `INSERT INTO `+rel+`(score) VALUES(20)`)
	for _, op := range []Operation{OperationRollback, OperationForward} {
		out := h.request(t, jobEditor, "POST", "/change-requests", SubmitRequest{MigrationID: q.MigrationID, Operation: op}, 201)
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
	if workflowMetric(t, metrics, "batch_commits_total", "action", "apply") != initialBatches+3 ||
		workflowMetric(t, metrics, "batch_rows_total", "action", "apply") != initialRows+5 {
		t.Fatal("recovery omitted the unmirrored checkpoint or counted it twice")
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
	metrics := telemetry.NewMetricsRegistry()
	initialBatches := workflowMetric(t, metrics, "batch_commits_total", "action", "compensate")
	initialRows := workflowMetric(t, metrics, "batch_rows_total", "action", "compensate")
	rel, up, down := batchFixture(t, h.target, 5, true)
	id := h.submit(t, up, down, 2)
	h.approve(t, id)
	stop := h.worker(t)
	h.wait(t, id, "partial", true)
	stop() // The paused state is durable before processBatch releases its lock.
	h.request(t, jobLeader, "POST", "/jobs/"+id+"/compensate", nil, 403)
	h.request(t, jobEditor, "POST", "/jobs/"+id+"/compensate", nil, 202)
	h.worker(t)
	p := h.wait(t, id, "reverted", false)
	if p["compensation_status"] != "reverted" || p["reverted_batches"] != float64(1) || score(t, h.target, rel, 1) != 0 || score(t, h.target, rel, 3) != 9 {
		t.Fatalf("compensation failed: %v", p)
	}
	if workflowMetric(t, metrics, "batch_commits_total", "action", "compensate") != initialBatches+1 ||
		workflowMetric(t, metrics, "batch_rows_total", "action", "compensate") != initialRows+2 {
		t.Fatal("verified compensation was omitted from its own counters")
	}
}

type batchRetryAccess struct {
	ApplicationAccess
	beforeCheck func()
}

func (a batchRetryAccess) Permission(ctx context.Context, user, db string) (Permission, error) {
	a.beforeCheck()
	return a.ApplicationAccess.Permission(ctx, user, db)
}

func TestBatchRetryRevalidatesRequester(t *testing.T) {
	h := newJobHarness(t)
	ctx := context.Background()
	rel, up, down := batchFixture(t, h.target, 3, false)
	id := h.submit(t, up, down, 1)
	h.approve(t, id)
	claimed, err := h.r.repo().claim(ctx, h.r.revalidate)
	if err != nil || claimed == nil || claimed.ID != id {
		t.Fatal("could not claim the approved fixture job")
	}

	mustExec(t, h.target, targetBatchDDL)
	mustExec(t, h.target, `CREATE SEQUENCE public.slice7b1_batch_retry;
		CREATE FUNCTION public.slice7b1_batch_retry() RETURNS trigger LANGUAGE plpgsql AS $$
		BEGIN
			IF NEW.id = TG_ARGV[0] AND NEW.completed = 2 THEN
				PERFORM nextval('public.slice7b1_batch_retry');
				RAISE EXCEPTION 'fixture serialization failure' USING ERRCODE = '40001';
			END IF;
			RETURN NEW;
		END $$;
		CREATE TRIGGER fixture_retry BEFORE UPDATE OF completed ON dmp_batch.jobs
		FOR EACH ROW EXECUTE FUNCTION public.slice7b1_batch_retry('`+id+`');`)
	defer mustExec(t, h.target, `DROP TRIGGER fixture_retry ON dmp_batch.jobs;
		DROP FUNCTION public.slice7b1_batch_retry(); DROP SEQUENCE public.slice7b1_batch_retry;`)
	accessDB, err := gorm.Open(postgres.Open(os.Getenv("SQL_CONSOLE_TEST_PLATFORM_DSN")))
	if err != nil {
		t.Fatal("could not open disposable authorization database")
	}
	sqlDB, err := accessDB.DB()
	if err != nil {
		t.Fatal("could not open disposable authorization pool")
	}
	defer sqlDB.Close()
	defer h.execPlatform(t, "UPDATE users SET status='Active' WHERE id=$1", jobEditor)

	deactivated := false
	h.r.Service.Access = batchRetryAccess{ApplicationAccess: ApplicationAccess{Platform: accessDB}, beforeCheck: func() {
		var ran bool
		if err := h.target.QueryRow(ctx, "SELECT is_called FROM public.slice7b1_batch_retry").Scan(&ran); err != nil {
			t.Fatal(err)
		}
		if ran && !deactivated {
			h.execPlatform(t, "UPDATE users SET status='Inactive' WHERE id=$1", jobEditor)
			deactivated = true
		}
	}}
	h.r.process(ctx, *claimed, &batchLockWaits{pending: make(map[batchLockKey]batchLockWait)})
	p, err := loadBatch(ctx, h.platform, id)
	if err != nil || !deactivated || p.State != "paused" || p.Batches != 1 || p.Rows != 1 {
		t.Fatalf("expected the first commit to be preserved with a paused batch, got state=%s batches=%d rows=%d error=%v",
			p.State, p.Batches, p.Rows, err)
	}
	var attempts int
	if err := h.target.QueryRow(ctx, "SELECT last_value FROM public.slice7b1_batch_retry").Scan(&attempts); err != nil || attempts != 1 {
		t.Fatalf("requester reached a new transaction after deactivation, transaction probes=%d error=%v", attempts, err)
	}
	if score(t, h.target, rel, 1) != 1 || score(t, h.target, rel, 2) != 0 || score(t, h.target, rel, 3) != 9 {
		t.Fatal("the target did not preserve exactly the first committed batch")
	}
}
