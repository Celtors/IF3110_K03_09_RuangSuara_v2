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
	const maxParameterTypes = 24
	count := 0
	var types []string
	for _, arg := range data.Args {
		switch arg.(type) {
		case pgx.QueryExecMode, pgx.QueryRewriter, pgx.QueryResultFormats, pgx.QueryResultFormatsByOID:
			continue
		}
		count++
		if len(types) < maxParameterTypes {
			types = append(types, parameterType(arg))
		}
	}
	ctx, query := startSQL(ctx, t.Scope, data.SQL, count, types)
	return context.WithValue(ctx, queryKey{}, query)
}

// A fixed vocabulary avoids leaking values or user-defined type names.
func parameterType(value any) string {
	switch value.(type) {
	case nil:
		return "null"
	case string, *string:
		return "text"
	case bool, *bool:
		return "boolean"
	case int, int8, int16, int32, int64, uint, uint8, uint16, uint32, uint64, *int64:
		return "integer"
	case float32, float64:
		return "float"
	case []byte:
		return "bytes"
	case []int64, []string:
		return "array"
	case time.Time, *time.Time:
		return "time"
	default:
		return "other"
	}
}

func (t PostgresTracer) TraceQueryEnd(ctx context.Context, _ *pgx.Conn, data pgx.TraceQueryEndData) {
	query := ctx.Value(queryKey{}).(SQLExecution)
	err := data.Err
	// pgx turns a ROLLBACK command tag into ErrTxCommitRollback after this hook.
	if query.transaction == "commit" && data.CommandTag.String() == "ROLLBACK" && err == nil {
		err = pgx.ErrTxCommitRollback
	}
	var rows SQLRows
	rows.Add(data.CommandTag)
	query.Finish(ctx, rows, err)
}

type SQLExecution struct {
	start       time.Time
	attrs       []slog.Attr
	scope       string
	transaction string
	span        trace.Span
}

// StartSQL is also used by the migration script executor, which calls PgConn
// directly and bypasses pgx's query hooks.
func StartSQL(ctx context.Context, scope, sql string, parameterCount int) (context.Context, SQLExecution) {
	return startSQL(ctx, scope, sql, parameterCount, nil)
}

func startSQL(ctx context.Context, scope, sql string, parameterCount int, parameterTypes []string) (context.Context, SQLExecution) {
	shape := sqlShape(sql)
	q := SQLExecution{
		scope: scope,
		attrs: []slog.Attr{
			slog.String("database_scope", scope),
			slog.String("query", shape),
			slog.Int("parameter_count", parameterCount),
		},
	}
	if len(parameterTypes) > 0 {
		q.attrs = append(q.attrs, slog.Any("parameter_types", parameterTypes))
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
		if len(parameterTypes) > 0 {
			q.span.SetAttributes(attribute.StringSlice("parameter_types", parameterTypes))
		}
	}
	slog.LogAttrs(ctx, slog.LevelInfo, "sql.started", append(q.attrs, slog.String("status", "started"))...)
	q.start = time.Now()
	return ctx, q
}

// StatementFinished records the server's completed command in a script. The
// script and its transaction have separate outcomes.
func (q SQLExecution) StatementFinished(ctx context.Context, position int, tag pgconn.CommandTag) {
	if tag.String() == "" {
		return
	}
	command, _, _ := strings.Cut(tag.String(), " ")
	attrs := []slog.Attr{slog.String("database_scope", q.scope), slog.Int("statement_index", position), slog.String("status", "executed")}
	switch command {
	case "INSERT", "UPDATE", "DELETE", "MERGE", "COPY":
		attrs = append(attrs, slog.String("command", command), slog.Int64("rows_affected", tag.RowsAffected()))
	case "SELECT", "FETCH":
		attrs = append(attrs, slog.String("command", command), slog.Int64("rows_returned", tag.RowsAffected()))
	case "ALTER", "CREATE", "DROP", "TRUNCATE", "COMMENT", "SET", "RESET", "DO", "GRANT", "REVOKE":
		attrs = append(attrs, slog.String("command", command))
	}
	slog.LogAttrs(ctx, slog.LevelInfo, "sql.statement_finished", attrs...)
}

func (q SQLExecution) Finish(ctx context.Context, rows SQLRows, err error) {
	event, status, level := "sql.finished", "succeeded", slog.LevelInfo
	if err != nil {
		status, level = "failed", slog.LevelWarn
	}
	switch q.transaction {
	case "commit":
		event, status = "transaction.commit", CommitOutcome(err)
	case "rollback":
		event, status = "transaction.rollback", "rolled_back"
		if err != nil {
			status = "unknown"
		}
	}
	attrs := append(q.attrs, slog.String("status", status),
		slog.Float64("duration_ms", float64(time.Since(q.start).Microseconds())/1000))
	if rows.Affected != nil {
		attrs = append(attrs, slog.Int64("rows_affected", *rows.Affected))
	}
	if rows.Returned != nil {
		attrs = append(attrs, slog.Int64("rows_returned", *rows.Returned))
	}
	slog.LogAttrs(ctx, level, event, append(attrs, ErrorAttrs(err)...)...)
	if q.span != nil {
		q.span.SetAttributes(attribute.String("outcome", status))
		if rows.Affected != nil {
			q.span.SetAttributes(attribute.Int64("rows_affected", *rows.Affected))
		}
		if rows.Returned != nil {
			q.span.SetAttributes(attribute.Int64("rows_returned", *rows.Returned))
		}
		SpanError(q.span, err)
		q.span.End()
	}
}

func CommitOutcome(err error) string {
	if err == nil {
		return "committed"
	}
	if errors.Is(err, pgx.ErrTxCommitRollback) {
		return "rolled_back"
	}
	var pgErr *pgconn.PgError
	if errors.As(err, &pgErr) && pgErr.SeverityUnlocalized != "FATAL" && pgErr.SeverityUnlocalized != "PANIC" &&
		(strings.HasPrefix(pgErr.Code, "23") || pgErr.Code == "40001" || pgErr.Code == "40P01" || pgErr.Code == "55P03") {
		return "rolled_back"
	}
	return "unknown"
}

// SQLRows holds statement counts. A multi-statement call can count the same
// row multiple times, and the enclosing transaction may still roll back.
type SQLRows struct {
	Affected, Returned *int64
}

func (rows *SQLRows) Add(tag pgconn.CommandTag) {
	verb, _, _ := strings.Cut(tag.String(), " ")
	var total **int64
	switch verb {
	case "INSERT", "UPDATE", "DELETE", "MERGE", "COPY":
		total = &rows.Affected
	case "SELECT", "FETCH":
		total = &rows.Returned
	default:
		return
	}
	if *total == nil {
		*total = new(int64)
	}
	**total += tag.RowsAffected()
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
