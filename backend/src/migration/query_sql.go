package migration

import (
	"encoding/json"
	"strings"

	pgquery "github.com/wasilibs/go-pgquery"
)

const maxConsoleSQLBytes = 256 * 1024

var consoleWriteKinds = map[string]bool{
	"SelectStmt": true, "InsertStmt": true, "UpdateStmt": true, "DeleteStmt": true,
	"CreateStmt": true, "AlterTableStmt": true, "IndexStmt": true, "DropStmt": true,
	"TruncateStmt": true, "CreateSchemaStmt": true, "RenameStmt": true, "CommentStmt": true,
}

// Rejected anywhere in the tree for every permission.
var consoleForbiddenNodes = map[string]bool{
	"GrantStmt": true, "GrantRoleStmt": true, "AlterDefaultPrivilegesStmt": true,
}

// Data-modifying nodes: rejected anywhere (CTEs, subqueries) for View only.
var consoleWriteNodes = map[string]bool{
	"InsertStmt": true, "UpdateStmt": true, "DeleteStmt": true, "MergeStmt": true,
}

// ALTER/COMMENT on server-level objects is outside "ViewEdit".
var consoleForbiddenObjects = map[string]bool{
	"OBJECT_DATABASE": true, "OBJECT_ROLE": true, "OBJECT_TABLESPACE": true,
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

// parseConsoleSQL validates one ad-hoc statement for the user's permission and
// returns its pg_query node kind (used only for audit logging).
func parseConsoleSQL(sql string, permission Permission) (string, error) {
	if permission < View || permission > Edit {
		return "", fault("forbidden", "You do not have the required database permission")
	}
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
	switch {
	case kind == "SelectStmt":
	case consoleWriteKinds[kind] && permission < Edit:
		return kind, fault("forbidden", "View only access allows read-only SELECT statements; this statement requires View + Edit")
	case !consoleWriteKinds[kind]:
		return kind, fault("unsupported_sql", kind+" is outside the supported SQL subset")
	}
	if err = checkConsoleObjectType(kind, statement); err != nil {
		return kind, err
	}
	return kind, inspectConsoleNode(statement, permission)
}

func checkConsoleObjectType(kind string, statement map[string]any) error {
	field := map[string]string{"RenameStmt": "renameType", "CommentStmt": "objtype"}[kind]
	if field == "" {
		return nil
	}
	body, _ := statement[kind].(map[string]any)
	if t, _ := body[field].(string); consoleForbiddenObjects[t] {
		return fault("unsupported_sql", "Server-level objects (database, role, tablespace) cannot be changed here")
	}
	return nil
}

func inspectConsoleNode(value any, permission Permission) error {
	switch v := value.(type) {
	case map[string]any:
		for key, child := range v {
			switch {
			case consoleForbiddenNodes[key]:
				return fault("unsupported_sql", "GRANT, REVOKE and privilege changes are not supported")
			case permission < Edit && consoleWriteNodes[key]:
				return fault("forbidden", "Data-modifying statements require View + Edit access")
			case permission < Edit && key == "intoClause":
				return fault("forbidden", "SELECT INTO creates a table and requires View + Edit access")
			case permission < Edit && key == "lockingClause":
				return fault("forbidden", "Row locking (FOR UPDATE/SHARE) requires View + Edit access")
			case key == "concurrent" && child == true:
				return fault("unsupported_sql", "Concurrent index operations cannot run inside a transaction")
			case key == "FuncCall":
				if err := checkConsoleFunction(child); err != nil {
					return err
				}
			}
			if err := inspectConsoleNode(child, permission); err != nil {
				return err
			}
		}
	case []any:
		for _, item := range v {
			if err := inspectConsoleNode(item, permission); err != nil {
				return err
			}
		}
	}
	return nil
}

func checkConsoleFunction(call any) error {
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
		return fault("unsupported_sql", "Function "+name+" is not available in the SQL Console")
	}
	return nil
}
