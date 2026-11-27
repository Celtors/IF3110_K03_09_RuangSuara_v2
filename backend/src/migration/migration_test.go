package migration

import (
	"context"
	"strings"
	"testing"

	fx "backend/access_control/effect"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
)

func TestMigrationCommitResult(t *testing.T) {
	tests := []struct {
		name   string
		err    error
		status string
	}{
		{name: "commit", status: "succeeded"},
		{name: "rollback tag", err: pgx.ErrTxCommitRollback, status: "failed"},
		{name: "constraint violation", err: &pgconn.PgError{Code: "23503"}, status: "failed"},
		{name: "connection lost", err: context.DeadlineExceeded, status: "unknown"},
		{name: "transaction outcome unknown", err: &pgconn.PgError{Code: "08007"}, status: "unknown"},
		{name: "statement outcome unknown", err: &pgconn.PgError{Code: "40003"}, status: "unknown"},
		{name: "fatal shutdown", err: &pgconn.PgError{Code: "57P01", SeverityUnlocalized: "FATAL"}, status: "unknown"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			if result := commitResult(test.err); result.Status != test.status {
				t.Fatalf("commit status: got %q, want %q", result.Status, test.status)
			}
		})
	}
}

func TestPrepareExecutionRejectsIneligibleDefinitions(t *testing.T) {
	tests := []struct {
		name       string
		definition Migration
		operation  Operation
		code       string
	}{
		{name: "invalid operation", operation: "invalid", code: "invalid_input"},
		{name: "reapply", definition: Migration{State: "applied"}, operation: OperationApply, code: "already_applied"},
		{name: "apply in unrecognized state", definition: Migration{State: "unrecognized"}, operation: OperationApply, code: "conflict"},
		{name: "apply without name", definition: Migration{State: "draft"}, operation: OperationApply, code: "invalid_input"},
		{name: "rollback draft", definition: Migration{State: "draft"}, operation: OperationRollback, code: "conflict"},
		{name: "rollback without down SQL", definition: Migration{State: "applied"}, operation: OperationRollback, code: "conflict"},
		{name: "forward active", definition: Migration{State: "applied"}, operation: OperationForward, code: "conflict"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			sql, reversesExecutionID, err := prepareExecution(context.Background(), nil, test.definition, test.operation)
			if faultCode(err) != test.code || sql != "" || reversesExecutionID != nil {
				t.Fatalf("preparation returned %q, %v, %v. Want fault %q", sql, reversesExecutionID, err, test.code)
			}
		})
	}
}

func TestMigrationPermissionMapping(t *testing.T) {
	cases := []struct {
		value    string
		expected Permission
	}{
		{fx.ViewOnly, View}, {fx.ViewEdit, Edit}, {fx.Blocked, NoAccess}, {"", NoAccess}, {"admin", NoAccess},
	}
	for _, c := range cases {
		if got := PermissionFromEffective(c.value); got != c.expected {
			t.Errorf("%q: got %v want %v", c.value, got, c.expected)
		}
	}
}
func TestCanonicalDatabaseID(t *testing.T) {
	id := uuid.New()
	if got := canonicalDatabaseID(strings.ToUpper(id.String())); got != id.String() {
		t.Fatalf("canonical uuid = %q", got)
	}
	if got := canonicalDatabaseID("invalid"); got != "invalid" {
		t.Fatal("unexpected rewrite for invalid id")
	}
}
func TestMigrationSQLValidation(t *testing.T) {
	cases := []struct {
		name, sql string
		plan, ok  bool
	}{
		{"create table", "CREATE TABLE public.tests (id integer)", false, true},
		{"insert data", "INSERT INTO public.tests (id) VALUES (1)", false, true},
		{"empty", "   ", false, false},
		{"transaction control", "BEGIN", false, false},
		{"session configuration", "SELECT set_config('search_path','public',false)", false, false},
		{"multiple statements for plan", "SELECT 1; SELECT 2", true, false},
		{"DDL in plan", "CREATE TABLE x(id integer)", true, false},
		{"safe plan query", "SELECT id FROM public.tests", true, true},
		{"plan rejects unqualified function", "SELECT random()", true, false},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			_, err := parseSQL(c.sql, c.plan)
			if (err == nil) != c.ok {
				t.Fatalf("parseSQL(%q, plan=%t) error=%v, expected success=%t", c.sql, c.plan, err, c.ok)
			}
		})
	}
}
func TestMigrationSQLSizeLimit(t *testing.T) {
	_, err := parseSQL("SELECT 1;"+strings.Repeat(" ", 256*1024), false)
	if err == nil {
		t.Fatal("oversized SQL must fail")
	}
}

func TestMigrationDefinitionValidation(t *testing.T) {
	const up = "CREATE TABLE items (id integer)"
	const down = "DROP TABLE items"
	cases := []struct {
		name       string
		draft      Draft
		saveCode   string
		reviewCode string
	}{
		{name: "empty draft", saveCode: "invalid_input", reviewCode: "invalid_input"},
		{name: "blank name", draft: Draft{Name: " \t ", SQLUp: up, SQLDown: down}, saveCode: "invalid_input", reviewCode: "invalid_input"},
		{name: "missing SQL up", draft: Draft{Name: "Items", SQLDown: down}, saveCode: "invalid_sql", reviewCode: "invalid_sql"},
		{name: "blank SQL up", draft: Draft{Name: "Items", SQLUp: " \n ", SQLDown: down}, saveCode: "invalid_sql", reviewCode: "invalid_sql"},
		{name: "up only", draft: Draft{Name: "Items", SQLUp: up}, reviewCode: "invalid_input"},
		{name: "reversible", draft: Draft{Name: "Items", SQLUp: up, SQLDown: down}},
		{name: "irreversible", draft: Draft{Name: "Items", SQLUp: up, Irreversible: true, IrreversibleReason: "Deleted rows cannot be restored"}},
		{name: "irreversible without reason", draft: Draft{Name: "Items", SQLUp: up, Irreversible: true, IrreversibleReason: "  "}, reviewCode: "invalid_input"},
		{name: "irreversible with down SQL", draft: Draft{Name: "Items", SQLUp: up, SQLDown: down, Irreversible: true}, saveCode: "invalid_input", reviewCode: "invalid_input"},
		{name: "reason without irreversible designation", draft: Draft{Name: "Items", SQLUp: up, SQLDown: down, IrreversibleReason: "No recovery"}, saveCode: "invalid_input", reviewCode: "invalid_input"},
		{name: "invalid up SQL", draft: Draft{Name: "Items", SQLUp: "CREATE TABLE", SQLDown: down}, saveCode: "invalid_sql", reviewCode: "invalid_sql"},
		{name: "invalid down SQL", draft: Draft{Name: "Items", SQLUp: up, SQLDown: "BEGIN"}, saveCode: "unsupported_sql", reviewCode: "unsupported_sql"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if err := tc.draft.validate(false); faultCode(err) != tc.saveCode {
				t.Errorf("save validation returned %v, want code %q", err, tc.saveCode)
			}
			if err := tc.draft.ValidateForReview(); faultCode(err) != tc.reviewCode {
				t.Errorf("review validation returned %v, want code %q", err, tc.reviewCode)
			}
		})
	}
}

func TestSchemaValidation(t *testing.T) {
	cases := []struct {
		name   string
		schema Schema
		valid  bool
	}{
		{"empty public schema", Schema{Name: "public"}, true},
		{"system schema", Schema{Name: "pg_catalog"}, false},
		{"duplicate table", Schema{Name: "public", Tables: []Table{{Name: "items", Columns: []Column{{Name: "id", Type: "integer"}}}, {Name: "items", Columns: []Column{{Name: "id", Type: "integer"}}}}}, false},
		{"unsupported type", Schema{Name: "public", Tables: []Table{{Name: "items", Columns: []Column{{Name: "id", Type: "unsupported_type"}}}}}, false},
		{"valid table", Schema{Name: "public", Tables: []Table{{Name: "items", Columns: []Column{{Name: "id", Type: "integer"}, {Name: "note", Type: "text", Nullable: true}}}}}, true},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if got := validateSchema(c.schema) == nil; got != c.valid {
				t.Errorf("validation success=%t want %t", got, c.valid)
			}
		})
	}
}
func TestCompareSchemasAddsTable(t *testing.T) {
	current := Schema{Name: "public"}
	desired := Schema{Name: "public", Tables: []Table{{Name: "items", Columns: []Column{{Name: "id", Type: "integer"}}}}}
	diff, err := CompareSchemas(current, desired)
	if err != nil {
		t.Fatal(err)
	}
	if len(diff.Differences) != 1 || diff.Differences[0].Kind != "add_table" || diff.Differences[0].Destructive {
		t.Fatalf("unexpected diff: %+v", diff)
	}
	if !strings.Contains(diff.SQL, "CREATE TABLE") {
		t.Fatalf("missing generated SQL: %q", diff.SQL)
	}
	reverse, err := CompareSchemas(desired, current)
	if err != nil {
		t.Fatal(err)
	}
	if len(reverse.Differences) != 1 || reverse.Differences[0].Kind != "drop_table" || !reverse.Differences[0].Destructive || len(reverse.Warnings) == 0 {
		t.Fatalf("missing destructive warning: %+v", reverse)
	}
}
func TestCompareSchemasRejectsNameMismatch(t *testing.T) {
	_, err := CompareSchemas(Schema{Name: "public"}, Schema{Name: "other"})
	if err == nil {
		t.Fatal("schema mismatch must fail")
	}
}
