package telemetry

import (
	"bytes"
	"context"
	"errors"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgconn"
	"go.opentelemetry.io/otel"
	otlpLogs "go.opentelemetry.io/proto/otlp/collector/logs/v1"
	otlpTraces "go.opentelemetry.io/proto/otlp/collector/trace/v1"
	"google.golang.org/protobuf/proto"
)

func TestOTLPPreservesLoggingPolicyAndCorrelation(t *testing.T) {
	var mu sync.Mutex
	payload := map[string][]byte{}
	collector := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		b, err := io.ReadAll(r.Body)
		if err != nil {
			http.Error(w, "cannot read OTLP request", 400)
			return
		}
		mu.Lock()
		payload[r.URL.Path] = b
		mu.Unlock()
		w.Header().Set("Content-Type", "application/x-protobuf")
	}))
	defer collector.Close()
	t.Setenv("OTEL_EXPORTER_OTLP_ENDPOINT", collector.URL)
	t.Setenv("OTEL_EXPORTER_OTLP_LOGS_ENDPOINT", "")
	t.Setenv("OTEL_EXPORTER_OTLP_TRACES_ENDPOINT", "")
	t.Setenv("OTEL_EXPORTER_OTLP_COMPRESSION", "none")
	t.Setenv("OTEL_EXPORTER_OTLP_LOGS_COMPRESSION", "none")
	t.Setenv("OTEL_EXPORTER_OTLP_TRACES_COMPRESSION", "none")
	t.Setenv("OTEL_TRACES_SAMPLER", "")

	previousProvider, previousPropagator, previousErrors := otel.GetTracerProvider(), otel.GetTextMapPropagator(), otel.GetErrorHandler()
	t.Cleanup(func() {
		otel.SetTracerProvider(previousProvider)
		otel.SetTextMapPropagator(previousPropagator)
		otel.SetErrorHandler(previousErrors)
	})
	var stdout bytes.Buffer
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	logger, shutdown, err := Init(ctx, &stdout)
	if err != nil {
		t.Fatal(err)
	}
	ctx, span := Start(With(ctx, slog.String("correlation_id", "test-context")), "fixture")
	grouped := logger.With("component", "worker").WithGroup("details")
	grouped.DebugContext(ctx, "filtered")
	grouped.WarnContext(ctx, "warning", "outcome", "failed")
	secret := "unsafe-diagnostic-fixture"
	errorAttrs := ErrorAttrs(&pgconn.PgError{Code: "23505", Detail: secret})
	grouped.LogAttrs(ctx, slog.LevelError, "database.failure", errorAttrs...)
	span.End()
	otel.Handle(errors.New(secret))
	if err = shutdown(ctx); err != nil {
		t.Fatal(err)
	}

	mu.Lock()
	defer mu.Unlock()
	var logs otlpLogs.ExportLogsServiceRequest
	var traces otlpTraces.ExportTraceServiceRequest
	if err := proto.Unmarshal(payload["/v1/logs"], &logs); err != nil {
		t.Fatal(err)
	}
	if err := proto.Unmarshal(payload["/v1/traces"], &traces); err != nil {
		t.Fatal(err)
	}
	if len(logs.GetResourceLogs()) != 1 || len(traces.GetResourceSpans()) != 1 {
		t.Fatal("expected one resource in each OTLP request")
	}
	logScopes, traceScopes := logs.GetResourceLogs()[0].GetScopeLogs(), traces.GetResourceSpans()[0].GetScopeSpans()
	if len(logScopes) != 1 || len(traceScopes) != 1 || len(traceScopes[0].GetSpans()) != 1 {
		t.Fatal("expected one instrumentation scope per signal and one span")
	}
	recorded := logScopes[0].GetLogRecords()
	exported := traceScopes[0].GetSpans()[0]
	if len(recorded) != 2 || recorded[0].GetBody().GetStringValue() != "warning" ||
		recorded[0].GetSeverityNumber() != 13 || recorded[1].GetSeverityNumber() != 17 {
		t.Fatalf("unexpected exported logs: %v", recorded)
	}
	record := recorded[0]
	if !bytes.Equal(record.TraceId, exported.TraceId) || !bytes.Equal(record.SpanId, exported.SpanId) {
		t.Fatal("native log and span identifiers differ")
	}
	attrs := record.GetAttributes()
	if len(attrs) != 2 || attrs[0].GetKey() != "component" || attrs[0].GetValue().GetStringValue() != "worker" || attrs[1].GetKey() != "details" {
		t.Fatalf("bound attributes and group missing: %v", attrs)
	}
	details := attrs[1].GetValue().GetKvlistValue().GetValues()
	if len(details) != 2 || details[0].GetKey() != "outcome" || details[1].GetKey() != "correlation_id" {
		t.Fatalf("record and context enrichment differ: %v", details)
	}
	local := stdout.String()
	for _, part := range []string{`"event":"warning"`, `"component":"worker"`, `"correlation_id":"test-context"`, span.SpanContext().TraceID().String(), span.SpanContext().SpanID().String(), `"event":"telemetry.export"`} {
		if !strings.Contains(local, part) {
			t.Errorf("stdout missing %s", part)
		}
	}
	if strings.Contains(local, secret) || strings.Contains(local, "filtered") ||
		strings.Contains(logs.String(), secret) || strings.Contains(logs.String(), "telemetry.export") || strings.Contains(traces.String(), secret) {
		t.Fatal("telemetry exported filtered data or internal diagnostics")
	}
}
