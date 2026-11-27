package main

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"
	"time"

	"backend/internal/telemetry"
	"backend/migration"

	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/codes"
	"go.opentelemetry.io/otel/propagation"
	sdktrace "go.opentelemetry.io/otel/sdk/trace"
	"go.opentelemetry.io/otel/sdk/trace/tracetest"
	"go.opentelemetry.io/otel/trace"
)

// Use a fresh disposable platform database and a separate disposable target.
func TestActivityLoggingThroughWorker(t *testing.T) {
	platformDSN, targetDSN := os.Getenv("ACTIVITY_TEST_PLATFORM_DSN"), os.Getenv("ACTIVITY_TEST_TARGET_DSN")
	if platformDSN == "" || targetDSN == "" {
		t.Skip("set ACTIVITY_TEST_PLATFORM_DSN and ACTIVITY_TEST_TARGET_DSN to disposable PostgreSQL databases")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	var logs bytes.Buffer
	previous := slog.Default()
	slog.SetDefault(slog.New(telemetry.NewHandler(&logs)))
	defer slog.SetDefault(previous)
	exporter := tracetest.NewInMemoryExporter()
	provider := sdktrace.NewTracerProvider(sdktrace.WithSyncer(exporter))
	priorProvider, priorPropagator := otel.GetTracerProvider(), otel.GetTextMapPropagator()
	otel.SetTracerProvider(provider)
	otel.SetTextMapPropagator(propagation.TraceContext{})
	defer func() {
		_ = provider.Shutdown(context.Background())
		otel.SetTracerProvider(priorProvider)
		otel.SetTextMapPropagator(priorPropagator)
	}()
	config, err := pgxpool.ParseConfig(platformDSN)
	if err != nil {
		t.Fatal("invalid activity test platform DSN")
	}
	config.ConnConfig.Tracer = telemetry.PostgresTracer{Scope: "platform"}
	pool, err := pgxpool.NewWithConfig(ctx, config)
	if err != nil {
		t.Fatal("could not create activity test platform pool")
	}
	defer pool.Close()
	target, err := pgx.ParseConfig(targetDSN)
	if err != nil {
		t.Fatal("invalid activity test target DSN")
	}
	if target.Database == config.ConnConfig.Database {
		t.Fatal("activity test target and platform must have distinct database names")
	}
	var initialized bool
	err = pool.QueryRow(ctx, `SELECT to_regclass('public.users') IS NOT NULL`).Scan(&initialized)
	if err != nil || initialized {
		t.Fatal("activity test requires a fresh disposable platform database")
	}
	seed, err := os.ReadFile("../../database_schema/access-control/access-control-db.sql")
	if err != nil {
		t.Fatal(err)
	}
	if _, err = pool.Exec(ctx, string(seed)); err != nil {
		t.Fatalf("seed platform: %v", err)
	}
	repository := &migration.Repository{Pool: pool}
	if err = repository.Initialize(ctx); err != nil {
		t.Fatalf("initialize workflow: %v", err)
	}
	const actor = "11111111-1111-4111-8111-000000000001"
	const group = "22222222-2222-4222-8222-000000000001"
	if _, err = pool.Exec(ctx, `INSERT INTO group_leaders (group_id,leader_id) VALUES ($1,$2)`, group, actor); err != nil {
		t.Fatalf("seed group leader: %v", err)
	}

	t.Setenv("PLATFORM_DATABASE_DSN", platformDSN)
	t.Setenv("DB_CREDENTIALS_KEY", base64.StdEncoding.EncodeToString(make([]byte, 32)))
	t.Setenv("ALLOW_PRIVATE_TARGETS", "true")
	t.Setenv("TARGET_DB_ALLOWED_HOSTS", target.Host)
	t.Setenv("MIGRATION_FORBID_SELF_APPROVAL", "false")
	gin.SetMode(gin.TestMode)
	service := &migration.Service{Repository: repository}
	router, closeRouter, err := newRouter(service, t.TempDir(), nil)
	if err != nil {
		t.Fatal(err)
	}
	defer closeRouter()
	migration.RegisterReviewRoutes(router, migration.NewReview(service))

	var session *http.Cookie
	var csrf string
	request := func(method, path string, input any, want int) *httptest.ResponseRecorder {
		t.Helper()
		b, err := json.Marshal(input)
		if err != nil {
			t.Fatal(err)
		}
		requestCtx, cancel := context.WithCancel(ctx)
		defer cancel()
		req := httptest.NewRequestWithContext(requestCtx, method, "http://localhost"+path, bytes.NewReader(b))
		req.Header.Set("Content-Type", "application/json")
		req.Header.Set("Origin", "http://localhost")
		req.Header.Set("X-CSRF-Token", csrf)
		if session != nil {
			req.AddCookie(session)
		}
		res := httptest.NewRecorder()
		router.ServeHTTP(res, req)
		for _, cookie := range res.Result().Cookies() {
			if cookie.Name == "ssid" {
				session = cookie
			}
		}
		if res.Code != want {
			t.Fatalf("%s %s = %d, want %d: %s", method, path, res.Code, want, res.Body)
		}
		return res
	}
	read := func(response *httptest.ResponseRecorder, value any) {
		t.Helper()
		if err := json.Unmarshal(response.Body.Bytes(), value); err != nil {
			t.Fatal(err)
		}
	}
	login := request("POST", "/session-manager/login", map[string]string{
		"username": "admin", "password": "password123",
	}, http.StatusOK)
	if session == nil || session.Value == "" {
		t.Fatal("login did not create a session")
	}
	if session.Domain != "" || !session.HttpOnly || !session.Secure {
		t.Fatal("login must issue a host-only Secure, HttpOnly session cookie")
	}
	csrf = request("GET", "/session-manager/me", nil, http.StatusOK).Header().Get("X-CSRF-Token")

	targetPassword := target.Password
	if targetPassword == "" {
		targetPassword = "activity-test-secret-credential"
	}
	var database struct{ ID string }
	registration := request("POST", "/api/admin/databases", map[string]any{
		"name": "activity test", "host": target.Host, "port": target.Port, "database_name": target.Database,
		"username": target.User, "password": targetPassword, "ssl_mode": "disable",
	}, http.StatusCreated)
	read(registration, &database)
	request("PUT", "/api/admin/databases/"+database.ID+"/groups/"+group+"/policy",
		map[string]string{"group_id": group, "effect": "ViewEdit"}, http.StatusOK)
	csrf = request("GET", "/session-manager/me", nil, http.StatusOK).Header().Get("X-CSRF-Token")
	prefix := "/api/databases/" + database.ID
	viewer := request("GET", prefix+"/schemas", nil, http.StatusOK)
	table := pgx.Identifier{"activity_test_secret_table_" + strings.ReplaceAll(uuid.NewString(), "-", "")}.Sanitize()
	type job struct {
		submission, approval, id, status, query, transaction, sqlstate string
		published                                                      trace.SpanContext
	}
	jobs := []job{
		{status: "succeeded", query: "CREATE TABLE ? ( ? ? PRIMARY ? ) ; INSERT INTO ? VALUES ( ? )", transaction: "committed"},
		{status: "failed", query: "INSERT INTO ? VALUES ( ? )", transaction: "rolled_back", sqlstate: "23505"},
		{status: "succeeded", query: "SELECT ?", transaction: "committed"}, // Requeued after a worker exits before creating an attempt.
	}
	scripts := []string{
		"CREATE TABLE " + table + " (value text PRIMARY KEY); INSERT INTO " + table + " VALUES ('activity-test-secret-row')",
		"INSERT INTO " + table + " VALUES ('activity-test-secret-row')",
		"SELECT 1",
	}
	for i := range jobs {
		var draft struct{ ID string }
		read(request("POST", prefix+"/migrations", map[string]any{
			"name": "activity test", "sql_script": scripts[i], "irreversible": true, "irreversible_reason": "test",
		}, http.StatusCreated), &draft)
		submission := request("POST", prefix+"/change-requests",
			map[string]string{"migration_id": draft.ID, "operation": "apply"}, http.StatusCreated)
		var change struct {
			Request migration.ChangeRequest `json:"change_request"`
		}
		read(submission, &change)
		approval := request("POST", prefix+"/change-requests/"+change.Request.ID+"/approve",
			map[string]string{"comment": "activity-test-secret-comment"}, http.StatusOK)
		jobs[i].submission = submission.Header().Get("X-Correlation-ID")
		jobs[i].approval = approval.Header().Get("X-Correlation-ID")
		jobs[i].id = change.Request.ID
	}

	// Reload through new storage and provider connections after the HTTP contexts have ended.
	workerPool, err := pgxpool.NewWithConfig(ctx, config.Copy())
	if err != nil {
		t.Fatal(err)
	}
	defer workerPool.Close()
	workerService := &migration.Service{Repository: &migration.Repository{Pool: workerPool}}
	_, closeWorkerRouter, err := newRouter(workerService, t.TempDir(), nil)
	if err != nil {
		t.Fatal(err)
	}
	defer closeWorkerRouter()
	for i := range jobs {
		var state, correlation string
		var carrier telemetry.TraceContext
		err = workerPool.QueryRow(ctx, `SELECT status, submission_correlation_id, traceparent, tracestate
 FROM dmp_migration.change_requests WHERE id=$1`, jobs[i].id).
			Scan(&state, &correlation, &carrier.TraceParent, &carrier.TraceState)
		jobs[i].published = trace.SpanContextFromContext(carrier.Extract(ctx))
		if err != nil || state != "queued" || correlation != jobs[i].submission || !jobs[i].published.IsValid() || !jobs[i].published.IsRemote() {
			t.Fatalf("could not reload queue context for job %d: %v", i, err)
		}
	}
	recoveryJob := jobs[len(jobs)-1]
	_, err = workerPool.Exec(ctx, `UPDATE dmp_migration.change_requests
 SET status='running',started_at=now()-interval '10 minutes' WHERE id=$1`, recoveryJob.id)
	if err != nil {
		t.Fatal(err)
	}
	workerCtx, stop := context.WithCancel(ctx)
	done := migration.NewReview(workerService).StartWorker(workerCtx, migration.WorkerConfig{PollInterval: 20 * time.Millisecond})
	defer func() { stop(); <-done }()
	for _, j := range jobs {
		for {
			var status string
			err = pool.QueryRow(ctx, `SELECT status FROM dmp_migration.change_requests WHERE id=$1`, j.id).Scan(&status)
			if err != nil {
				t.Fatalf("wait for job %s: %v", j.id, err)
			}
			if status == j.status {
				break
			}
			if status == "failed" || status == "succeeded" || status == "cancelled" {
				t.Fatalf("job %s status %s, want %s", j.id, status, j.status)
			}
			time.Sleep(20 * time.Millisecond)
		}
	}
	stop()
	<-done

	request("PUT", "/api/admin/databases/"+database.ID+"/groups/"+group+"/policy",
		map[string]string{"group_id": group, "effect": "ViewOnly"}, http.StatusOK)
	csrf = request("GET", "/session-manager/me", nil, http.StatusOK).Header().Get("X-CSRF-Token")
	console := request("POST", prefix+"/query", map[string]string{"query": "SELECT 'activity-test-secret-console'"}, http.StatusOK)
	consoleFailure := request("POST", prefix+"/query", map[string]string{"query": "SELECT 1 / 0"}, http.StatusUnprocessableEntity)

	invalidationTrace := testPGXTraceAfterInvalidatedStatement(t, ctx, workerPool)
	spans := exporter.GetSpans()
	spanJSON, err := json.Marshal(spans)
	if err != nil {
		t.Fatal(err)
	}
	logText := logs.String()
	for _, secret := range []string{"password123", session.Value, csrf, targetPassword, "activity-test-secret-", "activity_test_secret_table_"} {
		if secret != "" && (strings.Contains(logText, secret) || bytes.Contains(spanJSON, []byte(secret))) {
			t.Fatal("sensitive data reached the activity log or exported trace")
		}
	}
	var records []map[string]any
	for line := range strings.SplitSeq(strings.TrimSpace(logText), "\n") {
		var record map[string]any
		if err := json.Unmarshal([]byte(line), &record); err != nil {
			t.Fatal(err)
		}
		records = append(records, record)
	}
	find := func(want map[string]any) (int, map[string]any) {
		t.Helper()
		for i, record := range records {
			matches := true
			for k, v := range want {
				if record[k] != v {
					matches = false
					break
				}
			}
			if matches {
				return i, record
			}
		}
		t.Fatalf("missing record matching %v", want)
		return -1, nil
	}
	spansByID := make(map[string]tracetest.SpanStub, len(spans))
	claims := 0
	for _, span := range spans {
		spansByID[span.SpanContext.SpanID().String()] = span
		if span.Name == "queue.claim" {
			claims++
		}
	}
	if claims != len(jobs) {
		t.Fatalf("got %d claim spans for %d runnable jobs", claims, len(jobs))
	}
	spanForLog := func(record map[string]any, name string) tracetest.SpanStub {
		t.Helper()
		id, _ := record["span_id"].(string)
		span, found := spansByID[id]
		if !found || span.Name != name || span.SpanContext.TraceID().String() != record["trace_id"] {
			t.Fatalf("missing %s for log %v", name, record)
		}
		return span
	}
	childOf := func(child, parent tracetest.SpanStub) {
		t.Helper()
		if child.Parent.SpanID() != parent.SpanContext.SpanID() ||
			child.SpanContext.TraceID() != parent.SpanContext.TraceID() || child.SpanContext.SpanID() == parent.SpanContext.SpanID() {
			t.Fatalf("%s is missing parent %s", child.Name, parent.Name)
		}
	}
	_, loginSQL := find(map[string]any{"event": "sql.finished", "database_scope": "platform",
		"correlation_id": login.Header().Get("X-Correlation-ID"), "query": "SELECT * FROM ? WHERE ? = $? LIMIT $? FOR UPDATE"})
	_, loginHTTP := find(map[string]any{"event": "http.request", "correlation_id": login.Header().Get("X-Correlation-ID")})
	childOf(spanForLog(loginSQL, "db.query"), spanForLog(loginHTTP, "POST /session-manager/login"))
	_, registrationTx := find(map[string]any{"event": "transaction.commit", "database_scope": "platform",
		"correlation_id": registration.Header().Get("X-Correlation-ID"), "status": "committed"})
	spanForLog(registrationTx, "db.commit")
	_, viewerSQL := find(map[string]any{"event": "sql.finished", "database_scope": "target",
		"correlation_id": viewer.Header().Get("X-Correlation-ID"), "actor_id": actor})
	spanForLog(viewerSQL, "db.query")
	_, consoleSQL := find(map[string]any{"event": "sql.finished", "database_scope": "target",
		"correlation_id": console.Header().Get("X-Correlation-ID"), "query": "SELECT ?"})
	spanForLog(consoleSQL, "db.query")
	_, consoleTx := find(map[string]any{"event": "transaction.rollback", "database_scope": "target",
		"correlation_id": console.Header().Get("X-Correlation-ID"), "status": "rolled_back"})
	spanForLog(consoleTx, "db.rollback")
	_, consoleError := find(map[string]any{"event": "sql.finished", "database_scope": "target",
		"correlation_id": consoleFailure.Header().Get("X-Correlation-ID"), "status": "failed", "sqlstate": "22012"})
	if spanForLog(consoleError, "db.query").Status.Code != codes.Error {
		t.Fatal("failed SQL console statement must have error span status")
	}
	_, invalidated := find(map[string]any{"event": "sql.finished", "trace_id": invalidationTrace,
		"status": "failed", "error": "cancelled", "parameter_count": float64(0)})
	if spanForLog(invalidated, "db.query").Status.Code != codes.Error {
		t.Fatal("pgx deallocation failure did not end its SQL span with an error")
	}
	for _, j := range jobs {
		_, submitted := find(map[string]any{"event": "queue.submitted", "correlation_id": j.approval,
			"submission_correlation_id": j.submission, "job_id": j.id})
		publish := spanForLog(submitted, "queue.publish")
		_, approvalHTTP := find(map[string]any{"event": "http.request", "correlation_id": j.approval})
		childOf(publish, spanForLog(approvalHTTP, "POST /api/databases/:id/change-requests/:requestId/approve"))
		_, submissionHTTP := find(map[string]any{"event": "http.request", "correlation_id": j.submission})
		if !j.published.Equal(publish.SpanContext.WithRemote(true)) || submissionHTTP["trace_id"] == submitted["trace_id"] {
			t.Fatal("persisted parent must belong to final approval")
		}
		if j.id == recoveryJob.id {
			_, recovery := find(map[string]any{"event": "queue.recovery", "job_id": j.id, "correlation_id": j.submission})
			_, outcome := find(map[string]any{"event": "queue.requeue", "job_id": j.id, "status": "queued"})
			recovered := spanForLog(recovery, "queue.recover")
			childOf(recovered, publish)
			if !recovered.Parent.IsRemote() || recovered.SpanContext.SpanID() != spanForLog(outcome, "queue.recover").SpanContext.SpanID() {
				t.Fatal("orphan recovery must use the durable publication context")
			}
		}
		_, claimed := find(map[string]any{"event": "queue.claim", "status": "running", "job_id": j.id})
		claim := spanForLog(claimed, "queue.claim")
		childOf(claim, publish)
		_, revalidated := find(map[string]any{"event": "authorization.database", "status": "allowed", "job_id": j.id, "span_id": claimed["span_id"]})
		spanForLog(revalidated, "queue.claim")
		_, started := find(map[string]any{"event": "queue.execution_started", "correlation_id": j.submission, "job_id": j.id})
		worker := spanForLog(started, "worker.process")
		childOf(worker, claim)
		_, executionLog := find(map[string]any{"event": "migration.execution", "job_id": j.id})
		execution := spanForLog(executionLog, "migration.execute")
		childOf(execution, worker)
		var lockSpanID string
		for _, span := range spans {
			if span.Name == "migration.lock" && span.Parent.SpanID() == execution.SpanContext.SpanID() {
				lockSpanID = span.SpanContext.SpanID().String()
				break
			}
		}
		_, lock := find(map[string]any{"event": "migration.lock", "status": "acquired", "job_id": j.id, "span_id": lockSpanID})
		childOf(spanForLog(lock, "migration.lock"), execution)

		start, sqlStart := find(map[string]any{"event": "sql.started", "correlation_id": j.submission, "query": j.query})
		finish, sql := find(map[string]any{"event": "sql.finished", "correlation_id": j.submission, "query": j.query,
			"database_scope": "target", "job_id": j.id, "actor_id": actor, "status": j.status})
		sqlSpan := spanForLog(sql, "db.query")
		childOf(sqlSpan, execution)
		if sqlStart["span_id"] != sql["span_id"] || (sqlSpan.Status.Code == codes.Error) != (j.sqlstate != "") {
			t.Fatal("SQL span and logs disagree on execution")
		}
		if sql["execution_id"] == nil || sql["attempt_id"] != sql["execution_id"] || sql["migration_id"] == nil ||
			sql["migration_version"] == nil || sql["operation"] != "apply" || sql["database_id"] != database.ID {
			t.Fatalf("missing execution context: %v", sql)
		}
		countKey := "rows_affected"
		if j.id == recoveryJob.id {
			countKey = "rows_returned"
			if sql["rows_affected"] != nil {
				t.Fatal("read statement reported affected rows")
			}
		}
		if j.sqlstate != "" && sql["sqlstate"] != j.sqlstate || j.sqlstate == "" && sql[countKey] != float64(1) {
			t.Fatalf("unexpected statement outcome: %v", sql)
		}
		transactionEvent := "transaction.commit"
		transactionSpan := "db.commit"
		if j.transaction == "rolled_back" {
			transactionEvent = "transaction.rollback"
			transactionSpan = "db.rollback"
		}
		tx, txLog := find(map[string]any{"event": transactionEvent, "correlation_id": j.submission,
			"database_scope": "target", "execution_id": sql["execution_id"], "status": j.transaction})
		txSpan := spanForLog(txLog, transactionSpan)
		childOf(txSpan, execution)
		attrs := attribute.NewSet(txSpan.Attributes...)
		if outcome, _ := attrs.Value("outcome"); outcome.AsString() != j.transaction {
			t.Fatal("transaction span has the wrong outcome")
		}
		end, _ := find(map[string]any{"event": "queue.completed", "correlation_id": j.submission, "status": j.status})
		if !(start < finish && finish < tx && tx < end) {
			t.Fatal("statement, transaction and job records are out of order")
		}
	}
}

func testPGXTraceAfterInvalidatedStatement(t *testing.T, ctx context.Context, pool *pgxpool.Pool) string {
	t.Helper()
	ctx, span := telemetry.Start(ctx, "test.invalidated_statement")
	defer span.End()
	conn, err := pool.Acquire(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Release()
	var value int
	const cached = "SELECT $1::integer AS slice2_invalidation_probe"
	if err = conn.QueryRow(ctx, cached, 1).Scan(&value); err != nil {
		t.Fatal(err)
	}
	if err = conn.Conn().PgConn().Exec(ctx, "DEALLOCATE ALL").Close(); err != nil {
		t.Fatal(err)
	}
	if err = conn.QueryRow(ctx, cached, 1).Scan(&value); err == nil {
		t.Fatal("expected the cached prepared statement to be invalid")
	}
	cancelled, cancel := context.WithCancel(ctx)
	cancel()
	_, err = conn.Exec(cancelled, "SELECT 2 AS slice2_deallocation_probe")
	if !errors.Is(err, context.Canceled) || !strings.Contains(err.Error(), "deallocate cached statement") {
		t.Fatal("expected Exec to fail during pgx's invalidated-statement cleanup")
	}
	return span.SpanContext().TraceID().String()
}
