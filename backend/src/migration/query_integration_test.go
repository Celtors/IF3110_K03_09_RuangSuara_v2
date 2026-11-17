package migration

import (
	"context"
	"errors"
	"net/http"
	"os"
	"strings"
	"testing"

	"github.com/gin-gonic/gin"
	"github.com/jackc/pgx/v5/pgxpool"
)

const consoleTestDB = "33333333-3333-4333-8333-0000000000aa"

type fakeAccess struct{ perms map[string]Permission }

func (fakeAccess) Identity(*gin.Context) (string, error) { return "", nil }
func (f fakeAccess) Permission(_ context.Context, user, _ string) (Permission, error) {
	return f.perms[user], nil
}
func (fakeAccess) CSRF(*http.Request) error { return nil }

type fakeConnections struct {
	pool *pgxpool.Pool
	err  error
}

func (f fakeConnections) PostgreSQL(context.Context, string) (*pgxpool.Pool, error) {
	return f.pool, f.err
}

const consoleFixture = `
DROP TABLE IF EXISTS console_it_students, console_it_departments, console_it_types, console_it_created;
CREATE TABLE console_it_departments (id serial PRIMARY KEY, name text NOT NULL);
CREATE TABLE console_it_students (id serial PRIMARY KEY, name text NOT NULL, email text, department_id int REFERENCES console_it_departments(id));
INSERT INTO console_it_departments(name) VALUES ('Informatics'),('Systems');
INSERT INTO console_it_students(name,email,department_id) VALUES ('Alice','alice@example.com',1),('Bob',NULL,2),('Charlie','c@example.com',1);
CREATE TABLE console_it_types (i int, b bigint, n numeric(10,2), t text, f boolean, ts timestamptz, d date, j jsonb, u uuid, a int[], by bytea);
INSERT INTO console_it_types VALUES (1, 9007199254740993, 12.50, 'x', true, '2026-01-02 03:04:05+00', '2026-01-02', '{"k":1}', '11111111-1111-4111-8111-111111111111', '{1,2}', '\xdead');`

// Run with a disposable PostgreSQL (docker-compose.yaml provides both):
//   SQL_CONSOLE_TEST_TARGET_DSN=postgres://target_user:target_password@localhost:9001/target-test-db?sslmode=disable
//   SQL_CONSOLE_TEST_PLATFORM_DSN=postgres://myuser:mypassword@localhost:9000/access-control-db?sslmode=disable
func consoleTestService(t *testing.T) (*Service, *pgxpool.Pool) {
	t.Helper()
	targetDSN, platformDSN := os.Getenv("SQL_CONSOLE_TEST_TARGET_DSN"), os.Getenv("SQL_CONSOLE_TEST_PLATFORM_DSN")
	if targetDSN == "" || platformDSN == "" {
		t.Skip("set SQL_CONSOLE_TEST_TARGET_DSN and SQL_CONSOLE_TEST_PLATFORM_DSN to run SQL Console integration tests")
	}
	ctx := context.Background()
	target, err := pgxpool.New(ctx, targetDSN)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(target.Close)
	platform, err := pgxpool.New(ctx, platformDSN)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(platform.Close)
	if _, err = target.Exec(ctx, consoleFixture); err != nil {
		t.Fatalf("fixture: %v", err)
	}
	t.Cleanup(func() {
		_, _ = target.Exec(ctx, `DROP TABLE IF EXISTS console_it_students, console_it_departments, console_it_types, console_it_created`)
	})
	return &Service{
		Repository:  &Repository{Pool: platform},
		Access:      fakeAccess{perms: map[string]Permission{"view": View, "edit": Edit, "none": NoAccess}},
		Connections: fakeConnections{pool: target},
	}, target
}

func TestSQLConsoleIntegration(t *testing.T) {
	svc, target := consoleTestService(t)
	ctx := context.Background()
	query := func(user, sql string) (QueryResult, error) { return svc.Query(ctx, user, consoleTestDB, sql) }
	ok := func(t *testing.T, user, sql string) QueryResult {
		t.Helper()
		r, err := query(user, sql)
		if err != nil {
			t.Fatalf("%s: %v", sql, err)
		}
		return r
	}
	wantFault := func(t *testing.T, user, sql, code string) {
		t.Helper()
		if _, err := query(user, sql); faultCode(err) != code {
			t.Fatalf("%s: fault %q, want %q (%v)", sql, faultCode(err), code, err)
		}
	}
	count := func(t *testing.T, sql string) int {
		t.Helper()
		var n int
		if err := target.QueryRow(ctx, sql).Scan(&n); err != nil {
			t.Fatal(err)
		}
		return n
	}

	t.Run("1 select", func(t *testing.T) {
		r := ok(t, "view", "SELECT id, name, email FROM console_it_students ORDER BY id")
		if !r.HasResultSet || r.RowCount != 3 || len(r.Columns) != 3 || r.Columns[1] != "name" || r.Rows[0][0] != int64(1) || r.Rows[0][1] != "Alice" || r.Truncated {
			t.Fatalf("unexpected: %+v", r)
		}
	})
	t.Run("2 where", func(t *testing.T) {
		if r := ok(t, "view", "SELECT name FROM console_it_students WHERE name = 'Bob'"); r.RowCount != 1 || r.Rows[0][0] != "Bob" {
			t.Fatalf("%+v", r)
		}
	})
	t.Run("3 join and 4 aggregate", func(t *testing.T) {
		r := ok(t, "view", `SELECT d.name AS department, COUNT(s.id) AS total_students
			FROM console_it_departments d LEFT JOIN console_it_students s ON d.id = s.department_id
			GROUP BY d.name ORDER BY d.name`)
		if r.RowCount != 2 || r.Columns[0] != "department" || r.Rows[0][1] != int64(2) {
			t.Fatalf("%+v", r)
		}
	})
	t.Run("5 cte", func(t *testing.T) {
		if r := ok(t, "view", "WITH x AS (SELECT name FROM console_it_students WHERE id = 1) SELECT * FROM x"); r.RowCount != 1 {
			t.Fatalf("%+v", r)
		}
	})
	t.Run("6 empty result set", func(t *testing.T) {
		r := ok(t, "view", "SELECT id, name FROM console_it_students WHERE id < 0")
		if !r.HasResultSet || r.RowCount != 0 || len(r.Columns) != 2 || r.Rows == nil {
			t.Fatalf("an empty result set must still report columns: %+v", r)
		}
	})
	t.Run("7 null", func(t *testing.T) {
		if r := ok(t, "view", "SELECT email FROM console_it_students WHERE name = 'Bob'"); r.Rows[0][0] != nil {
			t.Fatalf("NULL must serialize as nil: %+v", r)
		}
	})
	t.Run("8 data types", func(t *testing.T) {
		r := ok(t, "view", "SELECT i,b,n,t,f,ts,d,j,u,a,by FROM console_it_types")
		row := r.Rows[0]
		if row[0] != int64(1) || row[1] != "9007199254740993" || row[2] != "12.50" || row[3] != "x" || row[4] != true ||
			!strings.Contains(row[5].(string), "2026-01-02") || row[6] != "2026-01-02" || !strings.Contains(row[7].(string), `"k"`) ||
			row[8] != "11111111-1111-4111-8111-111111111111" || row[9] != "{1,2}" || row[10] != `\xdead` {
			t.Fatalf("unexpected: %#v", row)
		}
		if r.ColumnTypes[0] != "int4" || r.ColumnTypes[7] != "jsonb" {
			t.Fatalf("column types: %v", r.ColumnTypes)
		}
	})
	t.Run("9 insert returning", func(t *testing.T) {
		r := ok(t, "edit", "INSERT INTO console_it_students(name) VALUES ('Dina') RETURNING id, name")
		if !r.HasResultSet || r.RowCount != 1 || r.Rows[0][1] != "Dina" || r.RowsAffected == nil || *r.RowsAffected != 1 || r.AccessMode != "read_write" {
			t.Fatalf("%+v", r)
		}
		if count(t, "SELECT count(*) FROM console_it_students WHERE name='Dina'") != 1 {
			t.Fatal("ViewEdit writes must be committed")
		}
	})
	t.Run("10 update returning", func(t *testing.T) {
		if r := ok(t, "edit", "UPDATE console_it_students SET email='d@example.com' WHERE name='Dina' RETURNING email"); r.Rows[0][0] != "d@example.com" {
			t.Fatalf("%+v", r)
		}
	})
	t.Run("11 delete returning", func(t *testing.T) {
		if r := ok(t, "edit", "DELETE FROM console_it_students WHERE name='Dina' RETURNING name"); r.RowCount != 1 {
			t.Fatalf("%+v", r)
		}
	})
	t.Run("12 update without returning", func(t *testing.T) {
		r := ok(t, "edit", "UPDATE console_it_students SET email = 'x@example.com' WHERE department_id = 1")
		if r.HasResultSet || r.RowsAffected == nil || *r.RowsAffected != 2 || r.Command != "UPDATE" || len(r.Columns) != 0 {
			t.Fatalf("%+v", r)
		}
	})
	t.Run("13 create table", func(t *testing.T) {
		r := ok(t, "edit", "CREATE TABLE console_it_created (id int)")
		if r.HasResultSet || r.Command != "CREATE TABLE" || r.RowsAffected != nil {
			t.Fatalf("%+v", r)
		}
	})
	t.Run("14 invalid syntax", func(t *testing.T) {
		wantFault(t, "edit", "SELEC * FROM console_it_students", "invalid_sql")
	})
	t.Run("14b postgres error is surfaced", func(t *testing.T) {
		_, err := query("edit", "SELECT * FROM console_it_missing")
		var f *Fault
		if faultCode(err) != "query_failed" || !errors.As(err, &f) || !strings.Contains(f.Message, "console_it_missing") {
			t.Fatalf("expected PostgreSQL message, got %v", err)
		}
	})
	t.Run("15 view only rejects writes", func(t *testing.T) {
		before := count(t, "SELECT count(*) FROM console_it_students")
		for _, sql := range []string{"INSERT INTO console_it_students(name) VALUES ('Eve')", "DROP TABLE console_it_students", "TRUNCATE console_it_students"} {
			wantFault(t, "view", sql, "forbidden")
		}
		if count(t, "SELECT count(*) FROM console_it_students") != before {
			t.Fatal("View only must not change data")
		}
		if r := ok(t, "view", "SELECT 1"); r.AccessMode != "read_only" {
			t.Fatalf("access mode %q", r.AccessMode)
		}
	})
	t.Run("15b read-only transaction backs up the validator", func(t *testing.T) {
		// A function the validator cannot see through: PostgreSQL must refuse it.
		wantFault(t, "view", "SELECT nextval(pg_get_serial_sequence('console_it_students','id'))", "forbidden")
	})
	t.Run("17 unauthorized", func(t *testing.T) {
		wantFault(t, "none", "SELECT 1", "forbidden")
		wantFault(t, "stranger", "SELECT 1", "forbidden")
	})
	t.Run("18 multiple statements", func(t *testing.T) {
		wantFault(t, "edit", "SELECT 1; DROP TABLE console_it_students", "unsupported_sql")
	})
	t.Run("19 timeout", func(t *testing.T) {
		t.Setenv("SQL_CONSOLE_TIMEOUT", "1s")
		wantFault(t, "view", "SELECT pg_sleep(5)", "query_timeout")
	})
	t.Run("20 row limit", func(t *testing.T) {
		t.Setenv("SQL_CONSOLE_MAX_ROWS", "10")
		r := ok(t, "view", "SELECT generate_series(1, 50)")
		if !r.Truncated || r.RowCount != 10 || r.MaxRows != 10 {
			t.Fatalf("%+v", r)
		}
	})
	t.Run("20b truncated write still commits fully", func(t *testing.T) {
		t.Setenv("SQL_CONSOLE_MAX_ROWS", "1")
		r := ok(t, "edit", "UPDATE console_it_students SET name = name RETURNING id")
		if !r.Truncated || r.RowCount != 1 || r.RowsAffected == nil || *r.RowsAffected != 3 {
			t.Fatalf("%+v", r)
		}
	})
	t.Run("21 connection failure", func(t *testing.T) {
		down := *svc
		down.Connections = fakeConnections{err: errors.New("dial tcp 10.0.0.5:5432: refused")}
		_, err := down.Query(ctx, "edit", consoleTestDB, "SELECT 1")
		var f *Fault
		if faultCode(err) != "database_unavailable" || !errors.As(err, &f) || strings.Contains(f.Message, "10.0.0.5") {
			t.Fatalf("connection details must not leak: %v", err)
		}
	})
	t.Run("platform database is refused", func(t *testing.T) {
		same := *svc
		same.Repository = &Repository{Pool: target} // "platform" == target
		if _, err := same.Query(ctx, "edit", consoleTestDB, "SELECT 1"); faultCode(err) != "forbidden" {
			t.Fatalf("expected forbidden, got %v", err)
		}
	})
	t.Run("no migration history is created", func(t *testing.T) {
		// Ad-hoc SQL touches only the target pool; the Repository pool is used for
		// the identity probe alone. Assert the platform schema is unchanged.
		var before, after int
		_ = svc.Repository.Pool.QueryRow(ctx, `SELECT count(*) FROM dmp_migration.attempts`).Scan(&before)
		ok(t, "edit", "SELECT 1")
		_ = svc.Repository.Pool.QueryRow(ctx, `SELECT count(*) FROM dmp_migration.attempts`).Scan(&after)
		if before != after {
			t.Fatal("ad-hoc SQL must not create migration attempts")
		}
	})
}