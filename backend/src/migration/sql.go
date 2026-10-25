package migration

import (
	"encoding/json"
	"fmt"
	"strings"

	pgquery "github.com/wasilibs/go-pgquery"
)

// Parse SQL and reject unsupported or unsafe statements.
// Migration must control its own transactions and database settings.
func parseSQL(sql string, plan bool) ([]map[string]any, error) {
	if len(sql) > 256*1024 || strings.TrimSpace(sql) == "" {
		return nil, fault("invalid_sql", "SQL must contain 1 to 262144 bytes")
	}
	raw, err := pgquery.ParseToJSON(sql)
	if err != nil {
		return nil, fault("invalid_sql", "PostgreSQL could not parse the SQL")
	}
	var tree struct {
		Statements []struct {
			Statement map[string]any `json:"stmt"`
		} `json:"stmts"`
	}
	if err = json.Unmarshal([]byte(raw), &tree); err != nil {
		return nil, err
	}
	if len(tree.Statements) == 0 || len(tree.Statements) > 100 {
		return nil, fault("invalid_sql", "Supply between 1 and 100 SQL statements")
	}
	if plan && len(tree.Statements) != 1 {
		return nil, fault("unsupported_sql", "Plan analysis accepts exactly one SELECT, INSERT, UPDATE or DELETE")
	}
	allowed := map[string]bool{"CreateStmt": true, "AlterTableStmt": true, "IndexStmt": true, "DropStmt": true, "TruncateStmt": true, "InsertStmt": true, "UpdateStmt": true, "DeleteStmt": true, "SelectStmt": true, "CreateSchemaStmt": true, "RenameStmt": true, "CommentStmt": true}
	out := []map[string]any{}
	for _, s := range tree.Statements {
		for kind := range s.Statement {
			if !allowed[kind] || (plan && kind != "SelectStmt" && kind != "InsertStmt" && kind != "UpdateStmt" && kind != "DeleteStmt") {
				return nil, fault("unsupported_sql", fmt.Sprintf("%s is outside the supported SQL subset", kind))
			}
		}
		if err = checkNodes(s.Statement, plan); err != nil {
			return nil, err
		}
		out = append(out, s.Statement)
	}
	return out, nil
}
func checkNodes(value any, plan bool) error {
	switch v := value.(type) {
	case map[string]any:
		for k, x := range v {
			if k == "concurrent" && x == true {
				return fault("unsupported_sql", "Concurrent index operations cannot run in a migration transaction")
			}
			if k == "intoClause" && plan {
				return fault("unsupported_sql", "SELECT INTO is not supported by plan analysis")
			}
			if k == "FuncCall" {
				node, _ := x.(map[string]any)
				names, _ := node["funcname"].([]any)
				parts := []string{}
				for _, name := range names {
					n, _ := name.(map[string]any)
					s, _ := n["String"].(map[string]any)
					str, _ := s["sval"].(string)
					parts = append(parts, strings.ToLower(str))
				}
				name := ""
				if len(parts) > 0 {
					name = parts[len(parts)-1]
				}
				if name == "set_config" || strings.HasPrefix(name, "pg_advisory_") || strings.HasPrefix(name, "dblink") || strings.HasPrefix(name, "lo_") {
					return fault("unsupported_sql", "Session-changing and external-effect functions are unsupported")
				}
				if plan {
					safe := map[string]bool{"count": true, "sum": true, "avg": true, "min": true, "max": true, "lower": true, "upper": true, "length": true, "abs": true}
					if len(parts) != 2 || parts[0] != "pg_catalog" || !safe[name] {
						return fault("unsupported_sql", "Plan functions must be explicitly qualified supported pg_catalog aggregates or scalar functions")
					}
				}
			}
			if err := checkNodes(x, plan); err != nil {
				return err
			}
		}
	case []any:
		for _, x := range v {
			if err := checkNodes(x, plan); err != nil {
				return err
			}
		}
	}
	return nil
}
