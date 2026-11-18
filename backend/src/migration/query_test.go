package migration

import (
	"errors"
	"strings"
	"testing"

	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgtype"
)

func faultCode(err error) string {
	var f *Fault
	if err == nil {
		return ""
	}
	if errors.As(err, &f) {
		return f.Code
	}
	return "other"
}

func TestParseConsoleSQL(t *testing.T) {
	cases := []struct {
		name, sql string
		perm      Permission
		want      string // "" = accepted, otherwise the fault code
	}{
		// View only: accepted
		{"select", "SELECT 1", View, ""},
		{"where order", "SELECT * FROM students WHERE id = 1 ORDER BY name;", View, ""},
		{"join group", "SELECT d.name, COUNT(s.id) FROM departments d LEFT JOIN students s ON d.id = s.department_id GROUP BY d.name", View, ""},
		{"cte", "WITH x AS (SELECT 1 AS a) SELECT * FROM x", View, ""},
		{"subquery", "SELECT * FROM (SELECT 1 AS a) q WHERE a IN (SELECT 1)", View, ""},
		{"values", "VALUES (1),(2)", View, ""},
		{"safe function", "SELECT lower('A')", View, ""},
		// View only: rejected
		{"insert", "INSERT INTO t VALUES (1)", View, "forbidden"},
		{"update", "UPDATE t SET a = 1", View, "forbidden"},
		{"delete", "DELETE FROM t", View, "forbidden"},
		{"create", "CREATE TABLE t (a int)", View, "forbidden"},
		{"alter", "ALTER TABLE t ADD b int", View, "forbidden"},
		{"drop", "DROP TABLE t", View, "forbidden"},
		{"truncate", "TRUNCATE t", View, "forbidden"},
		{"grant", "GRANT SELECT ON t TO x", View, "unsupported_sql"},
		{"revoke", "REVOKE SELECT ON t FROM x", View, "unsupported_sql"},
		{"writable cte", "WITH d AS (DELETE FROM t RETURNING *) SELECT * FROM d", View, "forbidden"},
		{"select into", "SELECT * INTO n FROM t", View, "forbidden"},
		{"for update", "SELECT * FROM t FOR UPDATE", View, "forbidden"},
		{"set_config", "SELECT set_config('a','b',false)", View, "unsupported_sql"},
		{"terminate", "SELECT pg_terminate_backend(1)", View, "unsupported_sql"},
		{"query_to_xml", "SELECT query_to_xml('select 1', true, true, '')", View, "unsupported_sql"},
		{"begin", "BEGIN", View, "unsupported_sql"},
		{"commit", "COMMIT", View, "unsupported_sql"},
		{"set role", "SET ROLE x", View, "unsupported_sql"},
		{"do block", "DO $$ BEGIN END $$", View, "unsupported_sql"},
		{"explain analyze", "EXPLAIN ANALYZE DELETE FROM t", View, "unsupported_sql"},
		{"multiple", "SELECT 1; SELECT 2", View, "unsupported_sql"},
		{"empty", "", View, "invalid_sql"},
		{"blank", "   ", View, "invalid_sql"},
		{"only semicolon", ";", View, "invalid_sql"},
		{"syntax", "SELEC 1", View, "invalid_sql"},
		// View + Edit: accepted
		{"edit select", "SELECT 1", Edit, ""},
		{"insert returning", "INSERT INTO t (a) VALUES (1) RETURNING a", Edit, ""},
		{"update", "UPDATE t SET a = 2 WHERE a = 1", Edit, ""},
		{"delete returning", "DELETE FROM t WHERE a = 2 RETURNING *", Edit, ""},
		{"create table", "CREATE TABLE t2 (id integer PRIMARY KEY)", Edit, ""},
		{"alter table", "ALTER TABLE t2 ADD COLUMN b text", Edit, ""},
		{"drop table", "DROP TABLE t2", Edit, ""},
		{"truncate", "TRUNCATE t2", Edit, ""},
		{"create index", "CREATE INDEX i ON t (a)", Edit, ""},
		{"writable cte", "WITH x AS (UPDATE t SET a = 1 RETURNING a) SELECT * FROM x", Edit, ""},
		// View + Edit: still rejected
		{"edit grant", "GRANT ALL ON t TO x", Edit, "unsupported_sql"},
		{"concurrent index", "CREATE INDEX CONCURRENTLY i ON t (a)", Edit, "unsupported_sql"},
		{"alter database", "ALTER DATABASE d RENAME TO e", Edit, "unsupported_sql"},
		{"comment database", "COMMENT ON DATABASE d IS 'x'", Edit, "unsupported_sql"},
		{"dblink", "SELECT dblink('a','b')", Edit, "unsupported_sql"},
		{"drop role", "DROP ROLE x", Edit, "unsupported_sql"},
		{"create function", "CREATE FUNCTION f() RETURNS int AS 'select 1' LANGUAGE sql", Edit, "unsupported_sql"},
		{"edit multiple", "SELECT 1; DROP TABLE t", Edit, "unsupported_sql"},
		{"no access", "SELECT 1", NoAccess, "forbidden"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			_, err := parseConsoleSQL(c.sql, c.perm)
			if got := faultCode(err); got != c.want {
				t.Fatalf("parseConsoleSQL(%q) fault=%q (%v), want %q", c.sql, got, err, c.want)
			}
		})
	}
}

func TestParseConsoleSQLSizeLimit(t *testing.T) {
	_, err := parseConsoleSQL("SELECT 1;"+strings.Repeat(" ", maxConsoleSQLBytes), View)
	if faultCode(err) != "invalid_sql" {
		t.Fatalf("oversized SQL must be rejected, got %v", err)
	}
}

func TestSerializeValue(t *testing.T) {
	cases := []struct {
		name string
		oid  uint32
		raw  []byte
		want any
	}{
		{"null", pgtype.TextOID, nil, nil},
		{"int4", pgtype.Int4OID, []byte("42"), int64(42)},
		{"int8 small", pgtype.Int8OID, []byte("12"), int64(12)},
		{"int8 beyond 2^53", pgtype.Int8OID, []byte("9007199254740993"), "9007199254740993"},
		{"float8", pgtype.Float8OID, []byte("1.5"), 1.5},
		{"float NaN", pgtype.Float8OID, []byte("NaN"), "NaN"},
		{"bool true", pgtype.BoolOID, []byte("t"), true},
		{"bool false", pgtype.BoolOID, []byte("f"), false},
		{"numeric keeps precision", pgtype.NumericOID, []byte("12.340"), "12.340"},
		{"text", pgtype.TextOID, []byte("héllo"), "héllo"},
		{"jsonb", pgtype.JSONBOID, []byte(`{"a": 1}`), `{"a": 1}`},
		{"bytea", pgtype.ByteaOID, []byte(`\xdead`), `\xdead`},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got, cut := serializeValue(c.oid, c.raw, 1024)
			if got != c.want || cut {
				t.Fatalf("got %#v (cut=%v), want %#v", got, cut, c.want)
			}
		})
	}
}

func TestTruncateCell(t *testing.T) {
	got, cut := truncateCell(strings.Repeat("é", 10), 5) // 2-byte runes: must not split one
	if !cut || !strings.HasSuffix(got, "…") || strings.ContainsRune(got, '\uFFFD') {
		t.Fatalf("bad truncation: %q cut=%v", got, cut)
	}
	if got, cut = truncateCell("abc", 5); got != "abc" || cut {
		t.Fatal("short values must be unchanged")
	}
}

func TestLoadConsoleLimits(t *testing.T) {
	env := func(m map[string]string) func(string) string { return func(k string) string { return m[k] } }
	if l := loadConsoleLimits(env(nil)); l.MaxRows != 200 || l.Timeout.Seconds() != 10 {
		t.Fatalf("defaults wrong: %+v", l)
	}
	l := loadConsoleLimits(env(map[string]string{"SQL_CONSOLE_TIMEOUT": "1h", "SQL_CONSOLE_MAX_ROWS": "99999"}))
	if l.MaxRows != 1000 || l.Timeout.Seconds() != 30 {
		t.Fatalf("limits must be clamped: %+v", l)
	}
	l = loadConsoleLimits(env(map[string]string{"SQL_CONSOLE_TIMEOUT": "garbage", "SQL_CONSOLE_MAX_ROWS": "0"}))
	if l.MaxRows != 200 || l.Timeout.Seconds() != 10 {
		t.Fatalf("invalid values must fall back: %+v", l)
	}
}

func TestCommandName(t *testing.T) {
	for in, want := range map[string]string{"INSERT 0 5": "INSERT", "UPDATE 3": "UPDATE", "CREATE TABLE": "CREATE TABLE", "SELECT 2": "SELECT"} {
		if got := commandName(in); got != want {
			t.Errorf("commandName(%q)=%q want %q", in, got, want)
		}
	}
}

func TestConsoleFaultMapping(t *testing.T) {
	var r QueryResult
	cases := []struct {
		err  error
		want string
	}{
		{&pgconn.PgError{Code: "57014", Message: "canceling statement due to statement timeout"}, "query_timeout"},
		{&pgconn.PgError{Code: "25006", Message: "cannot execute INSERT in a read-only transaction"}, "forbidden"},
		{&pgconn.PgError{Code: "42P01", Message: `relation "x" does not exist`, Position: 15}, "query_failed"},
		{errors.New("connection reset"), "database_unavailable"},
	}
	for _, c := range cases {
		if got := faultCode(consoleFault(t.Context(), c.err, &r)); got != c.want {
			t.Errorf("%v -> %q want %q", c.err, got, c.want)
		}
	}
	if r.PostgresCode != "42P01" || r.Position != 15 {
		t.Fatalf("pg error code/position must be captured, got %q/%d", r.PostgresCode, r.Position)
	}
}