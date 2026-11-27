package telemetry

import (
	"context"
	"io"
	"log/slog"

	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/codes"
	"go.opentelemetry.io/otel/exporters/stdout/stdouttrace"
	"go.opentelemetry.io/otel/propagation"
	"go.opentelemetry.io/otel/sdk/resource"
	sdktrace "go.opentelemetry.io/otel/sdk/trace"
	semconv "go.opentelemetry.io/otel/semconv/v1.37.0"
	"go.opentelemetry.io/otel/trace"
)

func InitTracing(w io.Writer) (func(context.Context) error, error) {
	exporter, err := stdouttrace.New(stdouttrace.WithWriter(w))
	if err != nil {
		return nil, err
	}
	provider := sdktrace.NewTracerProvider(
		sdktrace.WithResource(resource.NewWithAttributes(semconv.SchemaURL, semconv.ServiceName("ruangsuara-backend"))),
		sdktrace.WithSampler(sdktrace.AlwaysSample()),
		sdktrace.WithBatcher(exporter),
	)
	otel.SetTracerProvider(provider)
	otel.SetTextMapPropagator(propagation.TraceContext{})
	otel.SetErrorHandler(otel.ErrorHandlerFunc(func(err error) {
		slog.LogAttrs(context.Background(), slog.LevelWarn, "tracing.export", ErrorAttrs(err)...)
	}))
	return provider.Shutdown, nil
}

// Start carries the shared operation identifiers from structured log metadata.
func Start(ctx context.Context, name string, opts ...trace.SpanStartOption) (context.Context, trace.Span) {
	metadata, _ := ctx.Value(metadataKey{}).([]slog.Attr)
	for _, attr := range metadata {
		switch attr.Key {
		case "database_id", "migration_id", "job_id", "blocked_job_id", "attempt_id", "operation":
			opts = append(opts, trace.WithAttributes(attribute.String(attr.Key, attr.Value.String())))
		}
	}
	return otel.Tracer("backend").Start(ctx, name, opts...)
}

// SpanError shares the log redaction policy and avoids exporting raw exception text.
func SpanError(span trace.Span, err error) {
	for _, attr := range ErrorAttrs(err) {
		switch attr.Key {
		case "error":
			span.SetAttributes(attribute.String("error.type", attr.Value.String()))
			span.SetStatus(codes.Error, attr.Value.String())
		case "sqlstate":
			span.SetAttributes(attribute.String("db.response.status_code", attr.Value.String()))
		}
	}
}

func spanPanic(span trace.Span) {
	span.SetAttributes(attribute.String("error.type", "panic"))
	span.SetStatus(codes.Error, "panic")
}

// EndSpan is deferred in operations that let a panic reach their caller.
// A directly deferred SDK End would also export the raw panic value.
func EndSpan(span trace.Span) {
	if value := recover(); value != nil {
		spanPanic(span)
		defer panic(value)
	}
	span.End()
}

// TraceContext is the W3C carrier stored with a queue entry.
type TraceContext struct {
	TraceParent string
	TraceState  string
}

func CaptureTraceContext(ctx context.Context) TraceContext {
	carrier := propagation.MapCarrier{}
	propagation.TraceContext{}.Inject(ctx, carrier)
	return TraceContext{TraceParent: carrier.Get("traceparent"), TraceState: carrier.Get("tracestate")}
}

func (stored TraceContext) Extract(ctx context.Context) context.Context {
	return propagation.TraceContext{}.Extract(ctx, propagation.MapCarrier{
		"traceparent": stored.TraceParent, "tracestate": stored.TraceState,
	})
}
