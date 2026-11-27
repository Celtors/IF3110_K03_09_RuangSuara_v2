package telemetry

import (
	"context"
	"errors"
	"io"
	"log/slog"
	"os"

	"go.opentelemetry.io/contrib/bridges/otelslog"
	"go.opentelemetry.io/otel/exporters/otlp/otlplog/otlploghttp"
	"go.opentelemetry.io/otel/exporters/otlp/otlptrace/otlptracehttp"
	"go.opentelemetry.io/otel/exporters/stdout/stdouttrace"
	sdklog "go.opentelemetry.io/otel/sdk/log"
	sdktrace "go.opentelemetry.io/otel/sdk/trace"
)

// Init installs tracing and returns a logger with the same metadata policy for
// stdout and OTLP. The caller drains application work before shutting it down.
func Init(ctx context.Context, w io.Writer) (*slog.Logger, func(context.Context) error, error) {
	local := newLocalHandler(w)
	diagnostics := slog.New(local)
	setExporterDiagnostics(diagnostics)
	res := telemetryResource()

	var traceExporter sdktrace.SpanExporter
	var err error
	if otlpEnabled("OTEL_EXPORTER_OTLP_TRACES_ENDPOINT") {
		traceExporter, err = otlptracehttp.New(ctx)
		if err != nil {
			diagnostics.LogAttrs(ctx, slog.LevelWarn, "telemetry.trace_exporter", ErrorAttrs(err)...)
		}
	}
	if traceExporter == nil {
		traceExporter, err = stdouttrace.New(stdouttrace.WithWriter(w))
		if err != nil {
			return diagnostics, nil, err
		}
	}
	tracing := installTracing(res, traceExporter)

	var output slog.Handler = local
	shutdown := tracing.Shutdown
	if otlpEnabled("OTEL_EXPORTER_OTLP_LOGS_ENDPOINT") {
		exporter, err := otlploghttp.New(ctx)
		if err != nil {
			diagnostics.LogAttrs(ctx, slog.LevelWarn, "telemetry.log_exporter", ErrorAttrs(err)...)
		} else {
			logs := sdklog.NewLoggerProvider(sdklog.WithResource(res),
				sdklog.WithProcessor(sdklog.NewBatchProcessor(exporter)))
			bridge := otelslog.NewHandler("backend", otelslog.WithLoggerProvider(logs))
			output = slog.NewMultiHandler(local, bridge)
			shutdown = func(ctx context.Context) error {
				return errors.Join(tracing.Shutdown(ctx), logs.Shutdown(ctx))
			}
		}
	}
	return slog.New(handler{output, addMetadata}), func(ctx context.Context) error {
		err := shutdown(ctx)
		if err != nil {
			diagnostics.LogAttrs(ctx, slog.LevelWarn, "telemetry.shutdown", ErrorAttrs(err)...)
		}
		return err
	}, nil
}

func otlpEnabled(signalEndpoint string) bool {
	return os.Getenv(signalEndpoint) != "" || os.Getenv("OTEL_EXPORTER_OTLP_ENDPOINT") != ""
}
