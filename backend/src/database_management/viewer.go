package databasemanagement

import (
	"context"
	"errors"
	"net/http"
	"net/url"
	"strconv"
	"time"

	"backend/internal/targetdb"

	"github.com/gin-gonic/gin"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
)

func viewerParam(c *gin.Context, key string) string {
	value := c.Param(key)
	if c.Request.URL.RawPath != "" {
		value, _ = url.PathUnescape(value)
	}
	return value
}

func (h *Handler) readViewer(c *gin.Context, read func(context.Context, pgx.Tx) (any, error)) {
	if viewerParam(c, "schema") == targetdb.LedgerSchema {
		fail(c, http.StatusForbidden, "RESERVED_SCHEMA", "The internal batch ledger is reserved")
		return
	}
	ctx, cancel := context.WithTimeout(c.Request.Context(), 8*time.Second)
	defer cancel()
	pool, err := h.provider.PostgreSQL(ctx, c.Param("id"))
	if err != nil {
		registrationError(c, err)
		return
	}
	tx, err := pool.BeginTx(ctx, pgx.TxOptions{AccessMode: pgx.ReadOnly})
	if err != nil {
		fail(c, 502, "TARGET_UNAVAILABLE", errTargetUnavailable.Error())
		return
	}
	defer func() {
		cleanup, cancel := context.WithTimeout(context.WithoutCancel(ctx), 2*time.Second)
		defer cancel()
		_ = tx.Rollback(cleanup)
	}()

	if _, err = tx.Exec(ctx, `SET LOCAL statement_timeout='5s'; SET LOCAL lock_timeout='2s'; SET LOCAL search_path=pg_catalog`); err == nil {
		var data any
		data, err = read(ctx, tx)
		if err == nil {
			c.Header("Cache-Control", "no-store")
			c.JSON(http.StatusOK, data)
			return
		}
	}
	var pgErr *pgconn.PgError
	switch {
	case errors.Is(err, pgx.ErrNoRows):
		fail(c, 404, "TABLE_NOT_FOUND", "Schema or table not found")
	case errors.Is(ctx.Err(), context.DeadlineExceeded), errors.As(err, &pgErr) && pgErr.Code == "57014":
		fail(c, 504, "QUERY_TIMEOUT", "The database took too long to return results")
	case errors.As(err, &pgErr) && pgErr.Code == "42501":
		fail(c, 403, "TARGET_ACCESS_DENIED", "The registered PostgreSQL user cannot read this object")
	default:
		fail(c, 502, "INTROSPECTION_FAILED", "Could not read the target database")
	}
}

func (h *Handler) schemas(c *gin.Context) {
	h.readViewer(c, func(ctx context.Context, tx pgx.Tx) (any, error) {
		_, data, err := textRows(ctx, tx, `SELECT schema_name AS name FROM information_schema.schemata
			WHERE schema_name NOT IN ('pg_catalog','information_schema',$1) AND schema_name NOT LIKE 'pg_toast%'
			ORDER BY schema_name`, targetdb.LedgerSchema)
		return data, err
	})
}

func (h *Handler) tables(c *gin.Context) {
	h.readViewer(c, func(ctx context.Context, tx pgx.Tx) (any, error) {
		_, data, err := textRows(ctx, tx, `SELECT table_name AS name, table_type AS type
			FROM information_schema.tables WHERE table_schema = $1 ORDER BY table_name`, viewerParam(c, "schema"))
		return data, err
	})
}

const viewerColumns = `SELECT column_name, ordinal_position, data_type, udt_name, is_nullable, column_default
	FROM information_schema.columns WHERE table_schema = $1 AND table_name = $2 ORDER BY ordinal_position`

const viewerConstraints = `SELECT con.conname AS constraint_name,
	CASE con.contype WHEN 'p' THEN 'PRIMARY KEY' WHEN 'f' THEN 'FOREIGN KEY' WHEN 'u' THEN 'UNIQUE'
	WHEN 'c' THEN 'CHECK' WHEN 'x' THEN 'EXCLUDE' ELSE con.contype::text END AS constraint_type,
	local_col.attname AS column_name, ref_ns.nspname AS foreign_table_schema, ref.relname AS foreign_table_name,
	ref_col.attname AS foreign_column_name, pg_get_constraintdef(con.oid) AS definition
	FROM pg_constraint con
	JOIN pg_class rel ON rel.oid = con.conrelid
	JOIN pg_namespace ns ON ns.oid = rel.relnamespace
	LEFT JOIN LATERAL unnest(con.conkey) WITH ORDINALITY AS key(attnum, position) ON true
	LEFT JOIN pg_attribute local_col ON local_col.attrelid = rel.oid AND local_col.attnum = key.attnum
	LEFT JOIN pg_class ref ON ref.oid = con.confrelid
	LEFT JOIN pg_namespace ref_ns ON ref_ns.oid = ref.relnamespace
	LEFT JOIN pg_attribute ref_col ON ref_col.attrelid = ref.oid AND ref_col.attnum = con.confkey[key.position::int]
	WHERE ns.nspname = $1 AND rel.relname = $2 ORDER BY con.conname, key.position`

const viewerIndexes = `SELECT indexname, indexdef FROM pg_indexes
	WHERE schemaname = $1 AND tablename = $2 ORDER BY indexname`

func (h *Handler) columns(c *gin.Context)     { h.metadata(c, viewerColumns) }
func (h *Handler) constraints(c *gin.Context) { h.metadata(c, viewerConstraints) }
func (h *Handler) indexes(c *gin.Context)     { h.metadata(c, viewerIndexes) }

func (h *Handler) metadata(c *gin.Context, sql string) {
	schema, table := viewerParam(c, "schema"), viewerParam(c, "table")
	h.readViewer(c, func(ctx context.Context, tx pgx.Tx) (any, error) {
		if _, err := viewerRelation(ctx, tx, schema, table); err != nil {
			return nil, err
		}
		rows, err := tx.Query(ctx, sql, schema, table)
		if err != nil {
			return nil, err
		}
		return pgx.CollectRows(rows, pgx.RowToMap)
	})
}

func viewerRelation(ctx context.Context, tx pgx.Tx, schema, table string) (string, error) {
	var kind string
	err := tx.QueryRow(ctx, `SELECT c.relkind::text FROM information_schema.tables t
		JOIN pg_namespace n ON n.nspname = t.table_schema
		JOIN pg_class c ON c.relnamespace = n.oid AND c.relname = t.table_name
		WHERE t.table_schema = $1 AND t.table_name = $2`, schema, table).Scan(&kind)
	return kind, err
}

func (h *Handler) rows(c *gin.Context) {
	limit, limitErr := strconv.Atoi(c.DefaultQuery("limit", "50"))
	offset, offsetErr := strconv.Atoi(c.DefaultQuery("offset", "0"))
	if limitErr != nil || offsetErr != nil || limit < 1 || limit > 200 || offset < 0 {
		fail(c, 400, "INVALID_PAGINATION", "Limit must be between 1 and 200 and offset must be nonnegative")
		return
	}
	schema, table := viewerParam(c, "schema"), viewerParam(c, "table")
	h.readViewer(c, func(ctx context.Context, tx pgx.Tx) (any, error) {
		kind, err := viewerRelation(ctx, tx, schema, table)
		if err != nil {
			return nil, err
		}
		relation := pgx.Identifier{schema, table}.Sanitize()
		var keys string
		err = tx.QueryRow(ctx, `SELECT COALESCE(string_agg(quote_ident(a.attname), ', ' ORDER BY k.position), '')
			FROM pg_index i
			JOIN LATERAL unnest(i.indkey) WITH ORDINALITY AS k(attnum, position) ON k.position <= i.indnkeyatts
			JOIN pg_attribute a ON a.attrelid = i.indrelid AND a.attnum = k.attnum
			WHERE i.indrelid = $1::text::regclass AND i.indisprimary AND i.indisvalid`, relation).Scan(&keys)
		if err != nil {
			return nil, err
		}
		if keys == "" && (kind == "r" || kind == "p") {
			keys = "tableoid, ctid"
		}
		sql := "SELECT * FROM " + relation
		if keys != "" {
			sql += " ORDER BY " + keys
		}
		columns, data, err := textRows(ctx, tx, sql+" LIMIT $1 OFFSET $2", limit+1, offset)
		if err != nil {
			return nil, err
		}
		hasMore := len(data) > limit
		if hasMore {
			data = data[:limit]
		}
		return gin.H{"columns": columns, "rows": data, "limit": limit, "offset": offset, "has_more": hasMore}, nil
	})
}

// PostgreSQL formats each non-NULL cell. JSON clients keep exact bigint and
// numeric values, as well as the distinction between NULL and empty text.
func textRows(ctx context.Context, tx pgx.Tx, sql string, args ...any) ([]string, []map[string]*string, error) {
	options := []any{pgx.QueryExecModeExec, pgx.QueryResultFormats{pgx.TextFormatCode}}
	rows, err := tx.Query(ctx, sql, append(options, args...)...)
	if err != nil {
		return nil, nil, err
	}
	defer rows.Close()
	fields := rows.FieldDescriptions()
	columns := make([]string, len(fields))
	for i, field := range fields {
		columns[i] = field.Name
	}
	data := make([]map[string]*string, 0)
	for rows.Next() {
		row := make(map[string]*string, len(columns))
		for i, raw := range rows.RawValues() {
			row[columns[i]] = nil
			if raw != nil {
				value := string(raw)
				row[columns[i]] = &value
			}
		}
		data = append(data, row)
	}
	return columns, data, rows.Err()
}
