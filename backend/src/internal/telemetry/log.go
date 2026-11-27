package telemetry

import (
	"context"
	"errors"
	"io"
	"log/slog"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"go.opentelemetry.io/otel/trace"
)

type metadataKey struct{}

func With(ctx context.Context, attrs ...slog.Attr) context.Context {
	current, _ := ctx.Value(metadataKey{}).([]slog.Attr)
	merged := append([]slog.Attr(nil), current...)
	for _, attr := range attrs {
		if attr.Key == "" {
			continue
		}
		replaced := false
		for i := range merged {
			if merged[i].Key == attr.Key {
				merged[i] = attr
				replaced = true
				break
			}
		}
		if !replaced {
			merged = append(merged, attr)
		}
	}
	return context.WithValue(ctx, metadataKey{}, merged)
}

// ID accepts the UUID and compact hex forms used for application identifiers.
func ID(key, value string) slog.Attr {
	if len(value) == 32 || len(value) == 36 {
		if _, err := uuid.Parse(value); err == nil {
			return slog.String(key, value)
		}
	}
	return slog.Attr{}
}

func CorrelationID(ctx context.Context) string {
	attrs, _ := ctx.Value(metadataKey{}).([]slog.Attr)
	for _, attr := range attrs {
		if attr.Key == "correlation_id" {
			return attr.Value.String()
		}
	}
	return ""
}

func NewHandler(w io.Writer) slog.Handler {
	return handler{slog.NewJSONHandler(w, &slog.HandlerOptions{
		ReplaceAttr: func(groups []string, attr slog.Attr) slog.Attr {
			if len(groups) == 0 && attr.Key == slog.MessageKey {
				attr.Key = "event"
			}
			return attr
		},
	})}
}

type handler struct{ slog.Handler }

func (h handler) Handle(ctx context.Context, record slog.Record) error {
	attrs, _ := ctx.Value(metadataKey{}).([]slog.Attr)
	record.AddAttrs(attrs...)
	if span := trace.SpanContextFromContext(ctx); span.IsValid() {
		record.AddAttrs(slog.String("trace_id", span.TraceID().String()), slog.String("span_id", span.SpanID().String()))
	}
	return h.Handler.Handle(ctx, record)
}

func (h handler) WithAttrs(attrs []slog.Attr) slog.Handler {
	return handler{h.Handler.WithAttrs(attrs)}
}

func (h handler) WithGroup(name string) slog.Handler {
	return handler{h.Handler.WithGroup(name)}
}

// ErrorAttrs reports fixed categories and SQLSTATE. Driver error text can contain
// bound values, SQL fragments or connection details.
func ErrorAttrs(err error) []slog.Attr {
	if err == nil {
		return nil
	}
	var postgres *pgconn.PgError
	switch {
	case errors.As(err, &postgres):
		attrs := []slog.Attr{slog.String("error", "postgres_error")}
		if state := SQLStateAttr(postgres.Code); state.Key != "" {
			attrs = append(attrs, state)
		}
		return attrs
	case errors.Is(err, context.Canceled):
		return []slog.Attr{slog.String("error", "cancelled")}
	case errors.Is(err, context.DeadlineExceeded):
		return []slog.Attr{slog.String("error", "deadline_exceeded")}
	case errors.Is(err, pgx.ErrTxCommitRollback):
		return []slog.Attr{slog.String("error", "commit_rolled_back")}
	default:
		return []slog.Attr{slog.String("error", "operation_failed")}
	}
}

func SQLStateAttr(value string) slog.Attr {
	if len(value) != 5 {
		return slog.Attr{}
	}
	for _, c := range value {
		if (c < '0' || c > '9') && (c < 'A' || c > 'Z') {
			return slog.Attr{}
		}
	}
	return slog.String("sqlstate", value)
}
