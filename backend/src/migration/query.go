package migration

import (
	"context"
	"errors"
	"fmt"
	"log"
	"math"
	"os"
	"strconv"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/jackc/pgx/v5/pgxpool"
)

const maxConsoleCellBytes = 16 * 1024

type QueryRequest struct {
	Query string `json:"query"`
}

type QueryResult struct {
	Columns         []string `json:"columns"`
	ColumnTypes     []string `json:"column_types"`
	Rows            [][]any  `json:"rows"`
	RowCount        int      `json:"row_count"`
	RowsAffected    *int64   `json:"rows_affected,omitempty"`
	Command         string   `json:"command"`
	ExecutionTimeMS float64  `json:"execution_time_ms"`
	HasResultSet    bool     `json:"has_result_set"`
	Truncated       bool     `json:"truncated"`
	MaxRows         int      `json:"max_rows"`
	ValuesTruncated bool     `json:"values_truncated,omitempty"`
	AccessMode      string   `json:"access_mode"`
	PostgresCode    string   `json:"-"`
	Position        int      `json:"-"`
}

type consoleLimits struct {
	Timeout time.Duration
	MaxRows int
}

// SQL_CONSOLE_TIMEOUT (Go duration, 1s..30s, default 10s) and
// SQL_CONSOLE_MAX_ROWS (1..1000, default 200).
func loadConsoleLimits(getenv func(string) string) consoleLimits {
	l := consoleLimits{Timeout: 10 * time.Second, MaxRows: 200}
	if d, err := time.ParseDuration(strings.TrimSpace(getenv("SQL_CONSOLE_TIMEOUT"))); err == nil && d >= time.Second {
		l.Timeout = min(d, 30*time.Second)
	}
	if n, err := strconv.Atoi(strings.TrimSpace(getenv("SQL_CONSOLE_MAX_ROWS"))); err == nil && n >= 1 {
		l.MaxRows = min(n, 1000)
	}
	return l
}

// consolePermission resolves the caller's real permission exactly once.
func (s *Service) consolePermission(ctx context.Context, user, db string) (Permission, error) {
	if user == "" || s.Access == nil {
		return NoAccess, fault("unauthenticated", "A validated session is required")
	}
	if db == "" || len(db) > 200 {
		return NoAccess, fault("invalid_input", "Invalid managed database ID")
	}
	permission, err := s.Access.Permission(ctx, user, db)
	if err != nil {
		var f *Fault
		if errors.As(err, &f) && f.Code == "invalid_input" {
			return NoAccess, f
		}
		return NoAccess, fault("authorization_unavailable", "Database authorization is unavailable")
	}
	if permission < View || permission > Edit {
		return NoAccess, fault("forbidden", "You do not have the required database permission")
	}
	if s.Repository == nil || s.Repository.Pool == nil {
		return NoAccess, fault("database_unavailable", "Platform storage is unavailable")
	}
	return permission, nil
}

// Query runs one ad-hoc statement. It never creates migration history.
// The result is always returned (partially filled on failure) so the HTTP layer
// can report execution time and the PostgreSQL error position.
func (s *Service) Query(ctx context.Context, user, db, sql string) (QueryResult, error) {
	result := QueryResult{Columns: []string{}, ColumnTypes: []string{}, Rows: [][]any{}}
	db = canonicalDatabaseID(db)
	permission, err := s.consolePermission(ctx, user, db)
	if err != nil {
		return result, err
	}
	kind, err := parseConsoleSQL(sql, permission)
	if err != nil {
		return result, err
	}
	limits := loadConsoleLimits(os.Getenv)
	ctx, cancel := context.WithTimeout(ctx, limits.Timeout+5*time.Second)
	defer cancel()
	pool, err := s.connection(ctx, db)
	if err != nil {
		return result, err
	}
	result, err = runConsoleQuery(ctx, pool, s.Repository.Pool, permission, sql, limits)
	outcome := "ok"
	if err != nil {
		outcome = "error"
		var f *Fault
		if errors.As(err, &f) {
			outcome = f.Code
		}
	}
	// Audit trail without SQL text or credentials.
	log.Printf("sql_console user=%s database=%s access=%s statement=%s outcome=%s ms=%.2f",
		user, db, result.AccessMode, kind, outcome, result.ExecutionTimeMS)
	return result, err
}

type rowQuerier interface {
	QueryRow(context.Context, string, ...any) pgx.Row
}

func databaseFingerprint(ctx context.Context, q rowQuerier) (string, error) {
	var cluster, database string
	err := q.QueryRow(ctx, `SELECT system_identifier::text, (SELECT oid::text FROM pg_catalog.pg_database WHERE datname=pg_catalog.current_database()) FROM pg_catalog.pg_control_system()`).Scan(&cluster, &database)
	return cluster + "/" + database, err
}

// Fails closed if a registration resolves to the platform database itself.
func ensureTargetIsNotPlatform(ctx context.Context, target, platform rowQuerier) error {
	targetID, err := databaseFingerprint(ctx, target)
	if err != nil {
		if postgresCode(err) == "42501" {
			return fault("database_unavailable", "Target identity inspection is not permitted; an administrator must check EXECUTE on pg_catalog.pg_control_system()")
		}
		return fault("database_unavailable", "Target identity could not be verified")
	}
	platformID, err := databaseFingerprint(ctx, platform)
	if err != nil {
		return fault("database_unavailable", "Platform identity could not be verified")
	}
	if targetID == platformID {
		return fault("forbidden", "This registration resolves to the platform database; the SQL Console is disabled for it")
	}
	return nil
}

func runConsoleQuery(ctx context.Context, pool, platform *pgxpool.Pool, permission Permission, sql string, limits consoleLimits) (result QueryResult, err error) {
	result = QueryResult{Columns: []string{}, ColumnTypes: []string{}, Rows: [][]any{}, MaxRows: limits.MaxRows, AccessMode: "read_only"}
	start := time.Now()
	defer func() {
		result.ExecutionTimeMS = math.Round(float64(time.Since(start).Microseconds())/10) / 100
	}()

	options := pgx.TxOptions{AccessMode: pgx.ReadOnly}

	if permission >= Edit {
		options.AccessMode = pgx.ReadWrite
		result.AccessMode = "read_write"
	}
	tx, err := pool.BeginTx(ctx, options)
	if err != nil {
		return result, fault("database_unavailable", "Target connection unavailable before SQL execution")
	}
	defer rollback(tx) // no-op after Commit; rolls back every failure path

	statementMS := strconv.FormatInt(limits.Timeout.Milliseconds(), 10)
	idleMS := strconv.FormatInt((limits.Timeout + 5*time.Second).Milliseconds(), 10)
	if _, err = tx.Exec(ctx, `SELECT pg_catalog.set_config('statement_timeout',$1,true), pg_catalog.set_config('lock_timeout','5000',true), pg_catalog.set_config('idle_in_transaction_session_timeout',$2,true)`, statementMS, idleMS); err != nil {
		return result, fault("database_unavailable", "Could not configure target transaction")
	}
	if err = ensureTargetIsNotPlatform(ctx, tx, platform); err != nil {
		return result, err
	}

	// Extended protocol + text results: the server formats every value, and a
	// multi-statement string is rejected by PostgreSQL even if validation missed it.
	rows, err := tx.Query(ctx, sql, pgx.QueryExecModeExec, pgx.QueryResultFormats{pgx.TextFormatCode})
	if err != nil {
		return result, consoleFault(ctx, err, &result)
	}
	defer rows.Close()

	fields := rows.FieldDescriptions()
	oids := make([]uint32, len(fields))
	typeMap := tx.Conn().TypeMap()
	for i, f := range fields {
		oids[i] = f.DataTypeOID
		typeName := "unknown"
		if t, ok := typeMap.TypeForOID(f.DataTypeOID); ok {
			typeName = t.Name
		}
		result.Columns = append(result.Columns, f.Name)
		result.ColumnTypes = append(result.ColumnTypes, typeName)
	}
	result.HasResultSet = len(fields) > 0

	for rows.Next() {
		if len(result.Rows) >= limits.MaxRows {
			result.Truncated = true
			break
		}
		raw := rows.RawValues()
		row := make([]any, len(raw))
		for i, cell := range raw {
			value, cut := serializeValue(oids[i], cell, maxConsoleCellBytes)
			row[i] = value
			result.ValuesTruncated = result.ValuesTruncated || cut
		}
		result.Rows = append(result.Rows, row)
	}
	rows.Close()
	if err = rows.Err(); err != nil {
		return result, consoleFault(ctx, err, &result)
	}
	result.RowCount = len(result.Rows)
	tag := rows.CommandTag()
	result.Command = commandName(tag.String())
	switch result.Command {
	case "INSERT", "UPDATE", "DELETE", "MERGE":
		n := tag.RowsAffected()
		result.RowsAffected = &n
	case "SELECT":
		if !result.HasResultSet { // SELECT INTO
			n := tag.RowsAffected()
			result.RowsAffected = &n
		}
	}

	if permission >= Edit {
		if err = tx.Commit(ctx); err != nil {
			var pgErr *pgconn.PgError
			switch {
			case errors.As(err, &pgErr):
				return result, consoleFault(ctx, err, &result)
			case errors.Is(err, pgx.ErrTxCommitRollback):
				return result, fault("query_failed", "PostgreSQL rolled the transaction back at COMMIT")
			default:
				return result, fault("indeterminate", "COMMIT acknowledgement was lost; verify the target data before retrying")
			}
		}
	}
	return result, nil
}

// consoleFault returns PostgreSQL's own message (user SQL feedback), never Detail
// and never connection information.
func consoleFault(ctx context.Context, err error, result *QueryResult) error {
	var pgErr *pgconn.PgError
	if errors.As(err, &pgErr) {
		result.PostgresCode, result.Position = pgErr.Code, int(pgErr.Position)
		switch {
		case pgErr.Code == "57014" && strings.Contains(pgErr.Message, "statement timeout"):
			return fault("query_timeout", "The statement exceeded the execution time limit and was cancelled")
		case pgErr.Code == "25006":
			return fault("forbidden", "This statement would modify data, which View only access does not allow")
		}
		message := pgErr.Message
		if pgErr.Hint != "" {
			message += " Hint: " + pgErr.Hint
		}
		return fault("query_failed", fmt.Sprintf("%s (SQLSTATE %s)", message, pgErr.Code))
	}
	if errors.Is(err, context.DeadlineExceeded) || errors.Is(ctx.Err(), context.DeadlineExceeded) {
		return fault("query_timeout", "The request exceeded the execution time limit")
	}
	if errors.Is(err, context.Canceled) {
		return fault("query_failed", "The request was cancelled")
	}
	return fault("database_unavailable", "The target database connection was lost during execution")
}

// "INSERT 0 5" -> "INSERT", "CREATE TABLE" -> "CREATE TABLE".
func commandName(tag string) string {
	words := strings.Fields(tag)
	for len(words) > 1 {
		if _, err := strconv.ParseInt(words[len(words)-1], 10, 64); err != nil {
			break
		}
		words = words[:len(words)-1]
	}
	return strings.Join(words, " ")
}

// serializeValue converts PostgreSQL text output to JSON-friendly values.
// NULL -> nil. Small ints/floats/bools become JSON numbers/booleans. int8 beyond
// 2^53, numeric, NaN/Infinity, dates, timestamps, json/jsonb, uuid, bytea
// (\x hex), arrays and unknown types stay strings so nothing loses precision.
func serializeValue(oid uint32, raw []byte, maxBytes int) (any, bool) {
	if raw == nil {
		return nil, false
	}
	s := string(raw)
	switch oid {
	case pgtype.BoolOID:
		return s == "t", false
	case pgtype.Int2OID, pgtype.Int4OID, pgtype.OIDOID:
		if n, err := strconv.ParseInt(s, 10, 64); err == nil {
			return n, false
		}
	case pgtype.Int8OID:
		if n, err := strconv.ParseInt(s, 10, 64); err == nil && n >= -(1<<53) && n <= 1<<53 {
			return n, false
		}
	case pgtype.Float4OID, pgtype.Float8OID:
		if f, err := strconv.ParseFloat(s, 64); err == nil && !math.IsNaN(f) && !math.IsInf(f, 0) {
			return f, false
		}
	}
	return truncateCell(s, maxBytes)
}

func truncateCell(s string, maxBytes int) (string, bool) {
	if len(s) <= maxBytes {
		return s, false
	}
	cut := maxBytes
	for cut > 0 && !utf8.RuneStart(s[cut]) {
		cut--
	}
	return s[:cut] + "…", true
}