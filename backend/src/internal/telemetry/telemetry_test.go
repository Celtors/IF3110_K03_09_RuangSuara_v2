package telemetry

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/codes"
	"go.opentelemetry.io/otel/propagation"
	sdktrace "go.opentelemetry.io/otel/sdk/trace"
	"go.opentelemetry.io/otel/sdk/trace/tracetest"
	"go.opentelemetry.io/otel/trace"
)

func recordSpans(t *testing.T) *tracetest.SpanRecorder {
	t.Helper()
	recorder := tracetest.NewSpanRecorder()
	previous, propagator := otel.GetTracerProvider(), otel.GetTextMapPropagator()
	provider := sdktrace.NewTracerProvider(sdktrace.WithSampler(sdktrace.AlwaysSample()), sdktrace.WithSpanProcessor(recorder))
	otel.SetTracerProvider(provider)
	otel.SetTextMapPropagator(propagation.TraceContext{})
	t.Cleanup(func() {
		_ = provider.Shutdown(context.Background())
		otel.SetTracerProvider(previous)
		otel.SetTextMapPropagator(propagator)
	})
	return recorder
}

func capture(t *testing.T) *bytes.Buffer {
	t.Helper()
	var out bytes.Buffer
	previous := slog.Default()
	slog.SetDefault(slog.New(NewHandler(&out)))
	t.Cleanup(func() { slog.SetDefault(previous) })
	return &out
}

func records(t *testing.T, out *bytes.Buffer) []map[string]any {
	t.Helper()
	var entries []map[string]any
	for line := range strings.SplitSeq(strings.TrimSpace(out.String()), "\n") {
		var entry map[string]any
		if err := json.Unmarshal([]byte(line), &entry); err != nil {
			t.Fatal(err)
		}
		entries = append(entries, entry)
	}
	return entries
}

func TestHTTPLogging(t *testing.T) {
	out := capture(t)
	recorder := recordSpans(t)
	router := gin.New()
	router.Use(HTTP)
	databaseID, actorID := uuid.NewString(), uuid.NewString()
	traceID := trace.TraceID{1}
	spanID := trace.SpanID{2}
	router.GET("/databases/:id", func(c *gin.Context) {
		ctx := With(c.Request.Context(), ID("actor_id", actorID))
		c.Request = c.Request.WithContext(ctx)
		slog.Default().With("test", true).InfoContext(ctx, "handler.event", "status", "succeeded")
		c.Status(http.StatusNoContent)
	})
	router.GET("/panic", func(c *gin.Context) {
		_, span := Start(c.Request.Context(), "test.operation")
		defer EndSpan(span)
		panic("private-panic-value")
	})
	req := httptest.NewRequest(http.MethodGet, "/databases/"+databaseID+"?x=private-url-value", nil)
	req.Header.Set("X-Correlation-ID", "private-header-value")
	req.AddCookie(&http.Cookie{Name: "ssid", Value: "private-session-value"})
	req.Header.Set("Traceparent", "00-"+traceID.String()+"-"+spanID.String()+"-00")
	req.Header.Set("Tracestate", "vendor=private-tracestate-value")
	response := httptest.NewRecorder()
	router.ServeHTTP(response, req)
	correlationID := response.Header().Get("X-Correlation-ID")
	if _, err := uuid.Parse(correlationID); err != nil || response.Code != http.StatusNoContent {
		t.Fatalf("unexpected HTTP response %d with correlation %q", response.Code, correlationID)
	}
	panicResponse := httptest.NewRecorder()
	router.ServeHTTP(panicResponse, httptest.NewRequest(http.MethodGet, "/panic", nil))
	if panicResponse.Code != http.StatusInternalServerError {
		t.Fatalf("panic status = %d", panicResponse.Code)
	}
	rejected := httptest.NewRequest(http.MethodGet, "/private-unmatched-path", nil)
	rejected.Header.Set("Traceparent", "00-invalid")
	router.ServeHTTP(httptest.NewRecorder(), rejected)
	spans := recorder.Ended()
	if len(spans) != 4 || spans[0].Name() != "GET /databases/:id" || spans[0].SpanKind() != trace.SpanKindServer ||
		spans[0].Parent().SpanID() != spanID || !spans[0].Parent().IsRemote() ||
		spans[0].SpanContext().TraceID() != traceID || !spans[0].SpanContext().IsSampled() ||
		spans[0].Parent().TraceState().Len() != 0 || spans[0].Status().Code != codes.Unset ||
		spans[1].Status().Code != codes.Error || spans[1].Status().Description != "panic" || len(spans[1].Events()) != 0 ||
		spans[2].Status().Code != codes.Error || spans[2].Status().Description != "panic" || len(spans[2].Events()) != 0 ||
		spans[3].Parent().IsValid() || spans[3].Name() != "GET" || spans[3].Status().Code != codes.Unset {
		t.Fatal("unexpected HTTP spans")
	}
	entries := records(t, out)
	if len(entries) != 5 || entries[0]["event"] != "handler.event" || entries[1]["event"] != "http.request" ||
		entries[1]["route"] != "/databases/:id" || entries[2]["event"] != "http.panic" {
		t.Fatalf("unexpected events: %v", entries)
	}
	for _, entry := range entries[:2] {
		if entry["time"] == nil || entry["level"] != "INFO" || entry["status"] != "succeeded" ||
			entry["correlation_id"] != correlationID || entry["database_id"] != databaseID ||
			entry["actor_id"] != actorID || entry["trace_id"] != traceID.String() ||
			entry["span_id"] != spans[0].SpanContext().SpanID().String() {
			t.Fatalf("missing context: %v", entry)
		}
	}
	if strings.Contains(out.String(), "private-") {
		t.Fatal("sensitive HTTP data reached the logs")
	}
}

func TestSQLLogging(t *testing.T) {
	out := capture(t)
	recorder := recordSpans(t)
	ctx := With(context.Background(), slog.String("correlation_id", uuid.NewString()))
	ctx, operation := Start(ctx, "test.operation")
	defer operation.End()
	tracer := PostgresTracer{Scope: "target"}
	sql := `SELECT "private-identifier", 'private-literal', $body$private-body$body$, $1, 982126 /* private-comment */`
	query := tracer.TraceQueryStart(ctx, nil, pgx.TraceQueryStartData{
		SQL: sql, Args: []any{pgx.QueryExecModeExec, pgx.QueryResultFormats{}, "private-parameter"},
	})
	tracer.TraceQueryEnd(query, nil, pgx.TraceQueryEndData{CommandTag: pgconn.NewCommandTag("SELECT 1")})
	queryCtx, activity := StartSQL(ctx, "target", "SELECT 'private-unterminated", 0)
	activity.Finish(queryCtx, SQLRows{},
		&pgconn.PgError{Code: "22012", Message: "private-message", Detail: "private-detail", Hint: "private-hint"})
	queryCtx, activity = StartSQL(ctx, "target", "commit", 0)
	activity.Finish(queryCtx, SQLRows{}, errors.New("private-connection"))
	queryCtx, activity = StartSQL(ctx, "target", "rollback", 0)
	activity.Finish(queryCtx, SQLRows{}, nil)
	query = tracer.TraceQueryStart(ctx, nil, pgx.TraceQueryStartData{SQL: "commit"})
	tracer.TraceQueryEnd(query, nil, pgx.TraceQueryEndData{CommandTag: pgconn.NewCommandTag("ROLLBACK")})

	entries := records(t, out)
	if len(entries) != 10 {
		t.Fatalf("got %d SQL records", len(entries))
	}
	if entries[0]["event"] != "sql.started" || entries[0]["query"] != "SELECT ? , ? , ? , $? , ?" ||
		entries[0]["parameter_count"] != float64(1) || entries[1]["event"] != "sql.finished" ||
		entries[1]["database_scope"] != "target" || entries[1]["status"] != "succeeded" ||
		entries[1]["rows_returned"] != float64(1) || entries[1]["rows_affected"] != nil || entries[1]["duration_ms"] == nil {
		t.Fatalf("unexpected successful query records: %v", entries[:2])
	}
	types, ok := entries[0]["parameter_types"].([]any)
	if !ok || len(types) != 1 || types[0] != "text" {
		t.Fatalf("expected only the bind type, got %v", entries[0]["parameter_types"])
	}
	if entries[3]["status"] != "failed" || entries[3]["sqlstate"] != "22012" ||
		entries[3]["error"] != "postgres_error" || entries[3]["query"] != "[omitted: unavailable shape]" {
		t.Fatalf("unexpected failed query record: %v", entries[3])
	}
	if entries[5]["event"] != "transaction.commit" || entries[5]["status"] != "unknown" ||
		entries[7]["event"] != "transaction.rollback" || entries[7]["status"] != "rolled_back" ||
		entries[9]["event"] != "transaction.commit" || entries[9]["status"] != "rolled_back" {
		t.Fatalf("unexpected transaction records: %v", entries[4:])
	}
	if strings.Contains(out.String(), "private-") || strings.Contains(out.String(), "982126") {
		t.Fatal("sensitive SQL data reached the logs")
	}
	spans := recorder.Ended()
	if len(spans) != 5 {
		t.Fatalf("got %d SQL spans", len(spans))
	}
	names := []string{"db.query", "db.query", "db.commit", "db.rollback", "db.commit"}
	for i, span := range spans {
		if span.Name() != names[i] || !span.Parent().Equal(operation.SpanContext()) ||
			entries[2*i]["span_id"] != span.SpanContext().SpanID().String() ||
			entries[2*i+1]["span_id"] != entries[2*i]["span_id"] {
			t.Fatalf("unexpected span/log relationship for %s", span.Name())
		}
	}
	attributes := map[string]string{}
	for _, attr := range spans[1].Attributes() {
		attributes[string(attr.Key)] = attr.Value.Emit()
	}
	if spans[1].Status().Code != codes.Error || attributes["db.response.status_code"] != "22012" ||
		spans[2].Status().Code != codes.Error || spans[3].Status().Code != codes.Unset || len(spans[1].Events()) != 0 {
		t.Fatalf("unexpected SQL error and transaction spans: %v", attributes)
	}
}

func TestQueueTraceContext(t *testing.T) {
	recorder := recordSpans(t)
	state, err := trace.ParseTraceState("vendor=valid")
	if err != nil {
		t.Fatal(err)
	}
	remote := trace.NewSpanContext(trace.SpanContextConfig{
		TraceID: trace.TraceID{1}, SpanID: trace.SpanID{2}, TraceState: state, Remote: true,
	})
	source, cancel := context.WithCancel(trace.ContextWithRemoteSpanContext(context.Background(), remote))
	source, publish := Start(source, "queue.publish")
	stored := CaptureTraceContext(source)
	publish.End()
	cancel()
	worker := stored.Extract(context.Background())
	parent := trace.SpanContextFromContext(worker)
	if !parent.IsRemote() || !parent.Equal(publish.SpanContext().WithRemote(true)) || worker.Err() != nil {
		t.Fatal("persisted context did not preserve the publication span independently of request cancellation")
	}
	for _, legacy := range []TraceContext{{}, {TraceParent: "malformed", TraceState: "vendor=ignored"}} {
		_, claim := Start(legacy.Extract(context.Background()), "queue.claim")
		claim.End()
	}
	for _, span := range recorder.Ended()[1:] {
		if span.Parent().IsValid() || !span.SpanContext().IsValid() {
			t.Fatal("missing or invalid context must start a new trace")
		}
	}
}

func TestConsoleTracingShutdown(t *testing.T) {
	logs := capture(t)
	previous, propagator, handler := otel.GetTracerProvider(), otel.GetTextMapPropagator(), otel.GetErrorHandler()
	t.Cleanup(func() {
		otel.SetTracerProvider(previous)
		otel.SetTextMapPropagator(propagator)
		otel.SetErrorHandler(handler)
	})
	var console bytes.Buffer
	shutdown, err := InitTracing(&console)
	if err != nil {
		t.Fatal(err)
	}
	ctx, span := Start(context.Background(), "test.operation")
	ctx, query := StartSQL(ctx, "target", "SELECT 'private-literal'", 0)
	query.Finish(ctx, SQLRows{}, &pgconn.PgError{Code: "22012", Message: "private-database-error"})
	span.End()
	if err := shutdown(context.Background()); err != nil {
		t.Fatal(err)
	}
	entries := records(t, &console)
	if len(entries) != 2 || entries[0]["Name"] != "db.query" ||
		!strings.Contains(console.String(), "ruangsuara-backend") || strings.Contains(console.String(), "private-") {
		t.Fatal("console exporter did not flush the expected sanitized spans")
	}
	for _, output := range []map[string]any{
		{"Name": entries[0]["Name"], "SpanContext": entries[0]["SpanContext"], "Parent": entries[0]["Parent"], "Status": entries[0]["Status"]},
		records(t, logs)[1],
	} {
		raw, err := json.Marshal(output)
		if err != nil {
			t.Fatal(err)
		}
		t.Log(string(raw))
	}
}
