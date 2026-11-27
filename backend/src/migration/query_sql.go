package migration

import (
	"encoding/json"
	"strings"

	"backend/internal/targetdb"

	pgquery "github.com/wasilibs/go-pgquery"
)

const maxConsoleSQLBytes = 256 * 1024

var consoleKnownWrites = map[string]bool{
	"InsertStmt": true, "UpdateStmt": true, "DeleteStmt": true,
	"CreateStmt": true, "AlterTableStmt": true, "IndexStmt": true, "DropStmt": true,
	"TruncateStmt": true, "CreateSchemaStmt": true, "RenameStmt": true, "CommentStmt": true,
}

// Data-modifying nodes: rejected anywhere (CTEs, subqueries) for both access tiers.
var consoleWriteNodes = map[string]bool{
	"InsertStmt": true, "UpdateStmt": true, "DeleteStmt": true, "MergeStmt": true,
}

var consoleBlockedFunctions = map[string]bool{
	"set_config": true, "pg_terminate_backend": true, "pg_cancel_backend": true,
	"pg_reload_conf": true, "pg_rotate_logfile": true, "pg_notify": true,
	"pg_switch_wal": true, "pg_create_restore_point": true,
}

var consoleBlockedPrefixes = []string{
	"pg_advisory_", "dblink", "lo_", "pg_read_", "pg_ls_", "pg_stat_file",
	"query_to_xml", "cursor_to_xml", "table_to_xml", "schema_to_xml", "database_to_xml",
	"pg_logical_", "pg_replication_",
}

// parseConsoleSQL validates one read-only ad-hoc statement and
// returns its pg_query node kind (used only for audit logging).
func parseConsoleSQL(sql string) (string, error) {
	if strings.TrimSpace(sql) == "" || len(sql) > maxConsoleSQLBytes {
		return "", fault("invalid_sql", "SQL must contain 1 to 262144 bytes")
	}
	raw, err := pgquery.ParseToJSON(sql)
	if err != nil {
		return "", fault("invalid_sql", "SQL syntax error: "+err.Error())
	}
	var tree struct {
		Statements []struct {
			Statement map[string]any `json:"stmt"`
		} `json:"stmts"`
	}
	if err = json.Unmarshal([]byte(raw), &tree); err != nil {
		return "", fault("invalid_sql", "The SQL parse tree could not be read")
	}
	switch {
	case len(tree.Statements) == 0:
		return "", fault("invalid_sql", "No SQL statement was supplied")
	case len(tree.Statements) > 1:
		return "", fault("unsupported_sql", "Only one SQL statement can be executed per request")
	}
	statement := tree.Statements[0].Statement
	if len(statement) != 1 {
		return "", fault("invalid_sql", "The SQL statement could not be classified")
	}
	var kind string
	for k := range statement {
		kind = k
	}
	if kind != "SelectStmt" {
		if consoleKnownWrites[kind] {
			return kind, fault("forbidden", "The SQL Console is read-only; submit a migration to make changes")
		}
		return kind, fault("unsupported_sql", kind+" is outside the supported read-only SQL subset")
	}
	return kind, inspectConsoleNode(statement)
}

func inspectConsoleNode(value any) error {
	switch v := value.(type) {
	case map[string]any:
		for key, child := range v {
			switch {
			case consoleWriteNodes[key], key == "intoClause", key == "lockingClause":
				return fault("forbidden", "The SQL Console is read-only; submit a migration to make changes")
			case key == "FuncCall":
				if err := checkTargetFunction(child); err != nil {
					return err
				}
			}
			if err := inspectConsoleNode(child); err != nil {
				return err
			}
		}
	case string:
		if strings.EqualFold(v, targetdb.LedgerSchema) {
			return fault("unsupported_sql", "The internal batch ledger is reserved")
		}
	case []any:
		for _, item := range v {
			if err := inspectConsoleNode(item); err != nil {
				return err
			}
		}
	}
	return nil
}

func checkTargetFunction(call any) error {
	node, _ := call.(map[string]any)
	names, _ := node["funcname"].([]any)
	name := ""
	for _, part := range names {
		p, _ := part.(map[string]any)
		s, _ := p["String"].(map[string]any)
		str, _ := s["sval"].(string)
		name = strings.ToLower(str)
	}
	blocked := consoleBlockedFunctions[name]
	for _, prefix := range consoleBlockedPrefixes {
		blocked = blocked || strings.HasPrefix(name, prefix)
	}
	if blocked {
		return fault("unsupported_sql", "Function "+name+" is not available in managed SQL")
	}
	return nil
}
