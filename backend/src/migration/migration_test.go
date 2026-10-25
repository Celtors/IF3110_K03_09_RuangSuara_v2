package migration

import (
	"strings"
	"testing"

	fx "backend/access_control/effect"
	"github.com/google/uuid"
)

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
