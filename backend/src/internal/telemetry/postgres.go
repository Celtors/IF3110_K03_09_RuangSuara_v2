package telemetry

import (
	"context"
	"errors"
	"log/slog"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/stdlib"
	pg "github.com/pganalyze/pg_query_go/v6"
	pgquery "github.com/wasilibs/go-pgquery"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/trace"
)

// TraceStdlib installs the same query hook on GORM's database/sql driver.
func TraceStdlib(scope string) stdlib.OptionOpenDB {
	return stdlib.OptionBeforeConnect(func(_ context.Context, config *pgx.ConnConfig) error {
		config.Tracer = PostgresTracer{Scope: scope}
		return nil
	})
}

type PostgresTracer struct{ Scope string }

type queryKey struct{}

func (t PostgresTracer) TraceQueryStart(ctx context.Context, _ *pgx.Conn, data pgx.TraceQueryStartData) context.Context {
	count := 0
	for _, arg := range data.Args {
		switch arg.(type) {
		case pgx.QueryExecMode, pgx.QueryRewriter, pgx.QueryResultFormats, pgx.QueryResultFormatsByOID:
			continue
		}
		count++
	}
	ctx, query := StartSQL(ctx, t.Scope, data.SQL, count)
	return context.WithValue(ctx, queryKey{}, query)
}

func (t PostgresTracer) TraceQueryEnd(ctx context.Context, _ *pgx.Conn, data pgx.TraceQueryEndData) {
	query := ctx.Value(queryKey{}).(SQLExecution)
	err := data.Err
	// pgx turns a ROLLBACK command tag into ErrTxCommitRollback after this hook.
	if query.transaction == "commit" && data.CommandTag.String() == "ROLLBACK" && err == nil {
		err = pgx.ErrTxCommitRollback
	}
	query.Finish(ctx, RowCount(data.CommandTag), err)
}

type SQLExecution struct {
	start       time.Time
	attrs       []slog.Attr
	transaction string
	span        trace.Span
}

// StartSQL is also used by the migration script executor, which calls PgConn
// directly and bypasses pgx's query hooks.
func StartSQL(ctx context.Context, scope, sql string, parameterCount int) (context.Context, SQLExecution) {
	shape := sqlShape(sql)
	q := SQLExecution{
		attrs: []slog.Attr{
			slog.String("database_scope", scope),
			slog.String("query", shape),
			slog.Int("parameter_count", parameterCount),
		},
	}
	switch sql := strings.TrimSpace(sql); {
	case strings.EqualFold(sql, "commit"):
		q.transaction = "commit"
	case strings.EqualFold(sql, "rollback"):
		q.transaction = "rollback"
	}
	// Startup and idle queue polling keep their SQL logs without producing root spans.
	if trace.SpanContextFromContext(ctx).IsValid() {
		name := "db.query"
		if q.transaction != "" {
			name = "db." + q.transaction
		}
		ctx, q.span = Start(ctx, name, trace.WithSpanKind(trace.SpanKindClient), trace.WithAttributes(
			attribute.String("db.system.name", "postgresql"),
			attribute.String("database_scope", scope),
			attribute.String("db.query.text", shape),
			attribute.Int("parameter_count", parameterCount),
		))
	}
	slog.LogAttrs(ctx, slog.LevelInfo, "sql.started", append(q.attrs, slog.String("status", "started"))...)
	q.start = time.Now()
	return ctx, q
}

func (q SQLExecution) Finish(ctx context.Context, rows *int64, err error) {
	event, status, level := "sql.finished", "succeeded", slog.LevelInfo
	if err != nil {
		status, level = "failed", slog.LevelWarn
	}
	switch q.transaction {
	case "commit":
		event, status = "transaction.commit", commitStatus(err)
	case "rollback":
		event, status = "transaction.rollback", "rolled_back"
		if err != nil {
			status = "unknown"
		}
	}
	attrs := append(q.attrs, slog.String("status", status),
		slog.Float64("duration_ms", float64(time.Since(q.start).Microseconds())/1000))
	if rows != nil && err == nil {
		attrs = append(attrs, slog.Int64("rows_affected", *rows))
	}
	slog.LogAttrs(ctx, level, event, append(attrs, ErrorAttrs(err)...)...)
	if q.span != nil {
		q.span.SetAttributes(attribute.String("outcome", status))
		if rows != nil && err == nil {
			q.span.SetAttributes(attribute.Int64("rows_affected", *rows))
		}
		SpanError(q.span, err)
		q.span.End()
	}
}

func commitStatus(err error) string {
	if err == nil {
		return "committed"
	}
	if errors.Is(err, pgx.ErrTxCommitRollback) {
		return "rolled_back"
	}
	var pgErr *pgconn.PgError
	if errors.As(err, &pgErr) && pgErr.SeverityUnlocalized != "FATAL" && pgErr.SeverityUnlocalized != "PANIC" &&
		!strings.HasPrefix(pgErr.Code, "08") && pgErr.Code != "40003" {
		return "rolled_back"
	}
	return "unknown"
}

// RowCount reports counts only for commands that provide one in their tag.
func RowCount(tag pgconn.CommandTag) *int64 {
	verb, _, _ := strings.Cut(tag.String(), " ")
	switch verb {
	case "SELECT", "INSERT", "UPDATE", "DELETE", "MERGE", "COPY", "FETCH", "MOVE":
		rows := tag.RowsAffected()
		return &rows
	}
	return nil
}

func sqlShape(sql string) string {
	const maxInput = 64 * 1024
	const maxTokens = 96
	if len(sql) > maxInput {
		return "[omitted: size limit]"
	}
	scan, err := pgquery.Scan(sql)
	if err != nil || len(scan.Tokens) == 0 {
		return "[omitted: unavailable shape]"
	}

	// Emit only shared SQL syntax from the lexer vocabulary. Never copy source
	// slices, including identifiers, literals, comments and dollar-quoted bodies.
	parts := make([]string, 0, min(len(scan.Tokens), maxTokens))
	for _, token := range scan.Tokens {
		if len(parts) == maxTokens {
			parts = append(parts, "…")
			break
		}
		switch kind := token.Token; kind {
		case pg.Token_SQL_COMMENT, pg.Token_C_COMMENT:
			continue
		case pg.Token_PARAM:
			parts = append(parts, "$?")
		case pg.Token_ASCII_40, pg.Token_ASCII_41, pg.Token_ASCII_42, pg.Token_ASCII_44,
			pg.Token_ASCII_46, pg.Token_ASCII_59, pg.Token_ASCII_61:
			parts = append(parts, string(rune(kind)))
		case pg.Token_ALTER, pg.Token_CREATE, pg.Token_DELETE_P, pg.Token_DROP, pg.Token_INSERT,
			pg.Token_MERGE, pg.Token_TRUNCATE, pg.Token_UPDATE, pg.Token_BEGIN_P, pg.Token_COMMIT,
			pg.Token_ROLLBACK, pg.Token_SET, pg.Token_VALUES, pg.Token_EXPLAIN:
			parts = append(parts, strings.TrimSuffix(kind.String(), "_P"))
		default:
			if token.KeywordKind == pg.KeywordKind_RESERVED_KEYWORD {
				parts = append(parts, strings.TrimSuffix(kind.String(), "_P"))
			} else {
				parts = append(parts, "?")
			}
		}
	}
	if len(parts) == 0 {
		return "[omitted: unavailable shape]"
	}
	return strings.Join(parts, " ")
}
