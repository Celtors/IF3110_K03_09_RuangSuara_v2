package migration

import (
	"context"
	"regexp"
	"strings"

	"github.com/jackc/pgx/v5"
)

// The desired schema is the complete target. 
// Missing objects may be removed.
type Schema struct {
	Name        string   `json:"name"`
	Tables      []Table  `json:"tables"`
	Unsupported []string `json:"unsupported,omitempty"`
}
type Table struct {
	Name        string       `json:"name"`
	Columns     []Column     `json:"columns"`
	Constraints []Constraint `json:"constraints"`
	Indexes     []Index      `json:"indexes"`
}
type Column struct {
	Name        string  `json:"name"`
	Type        string  `json:"type"`
	Nullable    bool    `json:"nullable"`
	Default     *string `json:"default,omitempty"`
	Unsupported bool    `json:"unsupported,omitempty"`
}
type Constraint struct {
	Name       string `json:"name"`
	Definition string `json:"definition"`
}
type Index struct {
	Name        string   `json:"name"`
	Columns     []string `json:"columns"`
	Unique      bool     `json:"unique"`
	Unsupported bool     `json:"unsupported,omitempty"`
}
type Difference struct {
	Kind        string `json:"kind"`
	Object      string `json:"object"`
	Destructive bool   `json:"destructive"`
	SQL         string `json:"sql"`
}
type Diff struct {
	Current     Schema       `json:"current"`
	Differences []Difference `json:"differences"`
	SQL         string       `json:"sql_script"`
	Warnings    []string     `json:"warnings"`
}

func quote(s string) string { return pgx.Identifier{s}.Sanitize() }

var typePattern = regexp.MustCompile(`^(smallint|integer|bigint|int2|int4|int8|int|text|boolean|bool|uuid|date|json|jsonb|bytea|real|double precision|numeric(\([0-9]+(,[0-9]+)?\))?|decimal(\([0-9]+(,[0-9]+)?\))?|character varying(\([0-9]+\))?|varchar(\([0-9]+\))?|character(\([0-9]+\))?|timestamp(\([0-6]\))?( with(out)? time zone)?|time(\([0-6]\))?( with(out)? time zone)?)(\[\])?$`)

func validName(s string) bool { return len(s) > 0 && len(s) <= 63 && !strings.ContainsRune(s, 0) }
func validateSchema(s Schema) error {
	if !validName(s.Name) || strings.HasPrefix(s.Name, "pg_") || s.Name == "information_schema" {
		return fault("invalid_input", "Supply a non-system PostgreSQL schema name")
	}
	if len(s.Tables) > 100 {
		return fault("invalid_input", "At most 100 tables per comparison")
	}
	names := map[string]bool{}
	indexes := map[string]bool{}
	for _, t := range s.Tables {
		if !validName(t.Name) || names[t.Name] || len(t.Columns) == 0 || len(t.Columns) > 100 {
			return fault("invalid_input", "Tables require unique valid names and 1 to 100 columns")
		}
		names[t.Name] = true
		columns := map[string]bool{}
		for _, c := range t.Columns {
			if !validName(c.Name) || columns[c.Name] {
				return fault("invalid_input", "Columns require unique valid names")
			}
			columns[c.Name] = true
			if !typePattern.MatchString(c.Type) {
				return fault("unsupported_sql", "Unsupported column type: "+c.Type)
			}
			if c.Default != nil {
				stmts, e := parseSQL("SELECT "+*c.Default, false)
				if e != nil || len(stmts) != 1 {
					return fault("unsupported_sql", "Default must be a single supported SQL expression")
				}
				node, ok := stmts[0]["SelectStmt"].(map[string]any)
				if !ok {
					return fault("unsupported_sql", "Default must be a scalar SQL expression")
				}
				targets, _ := node["targetList"].([]any)
				if len(targets) != 1 || node["fromClause"] != nil || node["withClause"] != nil || node["larg"] != nil || node["rarg"] != nil || node["intoClause"] != nil || node["sortClause"] != nil || node["limitCount"] != nil {
					return fault("unsupported_sql", "Default must be a scalar literal or supported expression")
				}
			}
		}
		constraints := map[string]bool{}
		for _, c := range t.Constraints {
			if !validName(c.Name) || constraints[c.Name] {
				return fault("invalid_input", "Constraints require unique valid names")
			}
			constraints[c.Name] = true
			if _, err := parseConstraint(c); err != nil {
				return err
			}
		}
		for _, i := range t.Indexes {
			if !validName(i.Name) || indexes[i.Name] || len(i.Columns) == 0 {
				return fault("invalid_input", "Indexes require unique schema-wide names and columns")
			}
			indexes[i.Name] = true
			for _, c := range i.Columns {
				if !columns[c] {
					return fault("invalid_input", "Index refers to a missing column")
				}
			}
		}
	}
	return nil
}

// Parse the SQL to ensure it defines exactly one table constraint.
func parseConstraint(c Constraint) (map[string]any, error) {
	parsed, err := parseSQL("CREATE TABLE __dmp_validation ("+quote("__placeholder")+" integer, CONSTRAINT "+quote(c.Name)+" "+c.Definition+")", false)
	if err != nil || len(parsed) != 1 || parsed[0]["CreateStmt"] == nil {
		return nil, fault("unsupported_sql", "Constraint definition must contain one PostgreSQL table constraint")
	}
	node := parsed[0]["CreateStmt"].(map[string]any)
	elements, _ := node["tableElts"].([]any)
	if len(elements) != 2 {
		return nil, fault("unsupported_sql", "Constraint definition must contain exactly one constraint")
	}
	last, _ := elements[1].(map[string]any)
	constraint, ok := last["Constraint"].(map[string]any)
	if !ok {
		return nil, fault("unsupported_sql", "Expected a table constraint")
	}
	return constraint, nil
}

func (s *Service) Schema(ctx context.Context, user, db, name string) (Schema, error) {
	db = canonicalDatabaseID(db)
	if err := s.authorize(ctx, user, db, View); err != nil {
		return Schema{}, err
	}
	if !validName(name) || strings.HasPrefix(name, "pg_") || name == "information_schema" {
		return Schema{}, fault("invalid_input", "Invalid schema name")
	}
	pool, err := s.connection(ctx, db)
	if err != nil {
		return Schema{}, err
	}
	return inspectSchema(ctx, pool, name)
}
func (s *Service) Diff(ctx context.Context, user, db string, desired Schema) (Diff, error) {
	db = canonicalDatabaseID(db)
	if err := s.authorize(ctx, user, db, View); err != nil {
		return Diff{}, err
	}
	if err := validateSchema(desired); err != nil {
		return Diff{}, err
	}
	current, err := s.Schema(ctx, user, db, desired.Name)
	if err != nil {
		return Diff{}, err
	}
	return CompareSchemas(current, desired)
}
func (s *Service) Plan(ctx context.Context, user, db, sql string) (Plan, error) {
	db = canonicalDatabaseID(db)
	if err := s.authorize(ctx, user, db, View); err != nil {
		return Plan{}, err
	}
	if _, err := parseSQL(sql, true); err != nil {
		return Plan{}, err
	}
	pool, err := s.connection(ctx, db)
	if err != nil {
		return Plan{}, err
	}
	return explainPostgres(ctx, pool, sql)
}
