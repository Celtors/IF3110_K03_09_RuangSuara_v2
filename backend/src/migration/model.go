package migration

import (
	databasemanagement "backend/database_management"
	"context"
	"net/http"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/jackc/pgx/v5/pgxpool"
)

type Permission int

const (
	NoAccess Permission = iota
	View
	Edit
)

// Access provides the current user's identity and database permissions.
// Access Control handles group permission rules. Migration uses the result.
// Requests that modify data must also pass CSRF validation.
type Access interface {
	Identity(*gin.Context) (string, error)
	Permission(context.Context, string, string) (Permission, error)
	CSRF(*http.Request) error
}

// Connections provides PostgreSQL connections for managed databases.
// Database Management owns the connection pools and credentials.
// Migration uses these pools without closing them.
// Database connections must remain consistent during migration recovery.
type Connections interface {
	PostgreSQL(context.Context, string) (*pgxpool.Pool, error)
}

type Fault struct {
	Code    string `json:"code"`
	Message string `json:"message"`
}

func (e *Fault) Error() string         { return e.Message }
func fault(code, message string) error { return &Fault{code, message} }

var ErrDatabaseNotFound = databasemanagement.ErrDatabaseNotFound

type Draft struct {
	Name               string `json:"name"`
	Description        string `json:"description"`
	SQLUp              string `json:"sql_script"`
	SQLDown            string `json:"sql_down"`
	Irreversible       bool   `json:"irreversible"`
	IrreversibleReason string `json:"irreversible_reason"`
	ReleaseLabel       string `json:"release_label"`
}
type Migration struct {
	ID         string `json:"id"`
	DatabaseID string `json:"database_id"`
	Draft
	Author       string     `json:"author_user_id"`
	Created      time.Time  `json:"created_at"`
	Updated      time.Time  `json:"updated_at"`
	State        string     `json:"state"`
	Version      *int64     `json:"version"`
	Attempts     []Attempt  `json:"attempts,omitempty"`
	LastExecutor *string    `json:"last_executor_user_id"`
	LastExecuted *time.Time `json:"last_executed_at"`
}
type Operation string

const (
	OperationApply    Operation = "apply"
	OperationRollback Operation = "rollback"
	OperationForward  Operation = "forward"
)

type Attempt struct {
	ID                  string     `json:"id"`
	MigrationID         string     `json:"migration_id"`
	DatabaseID          string     `json:"database_id"`
	Operation           Operation  `json:"operation"`
	ReversesExecutionID *string    `json:"reverses_execution_id,omitempty"`
	SQL                 string     `json:"sql_script_snapshot"`
	Executor            string     `json:"executor_user_id"`
	Started             time.Time  `json:"started_at"`
	Completed           *time.Time `json:"completed_at"`
	Status              string     `json:"status"`
	SuccessOrder        *int64     `json:"version"`
	XID                 *string    `json:"target_transaction_id,omitempty"`
	TargetIdentity      string     `json:"-"`
	ErrorCode           string     `json:"postgres_error_code,omitempty"`
	Error               string     `json:"error,omitempty"`
}

type ExecutionHistoryEntry struct {
	ExecutionID         string     `json:"execution_id"`
	MigrationID         string     `json:"migration_id"`
	MigrationName       string     `json:"migration_name"`
	MigrationVersion    *int64     `json:"migration_version"`
	SuccessOrder        *int64     `json:"success_order"`
	Operation           Operation  `json:"operation"`
	Status              string     `json:"status"`
	ReversesExecutionID *string    `json:"reverses_execution_id"`
	ExecutorUserID      string     `json:"executor_user_id"`
	StartedAt           time.Time  `json:"started_at"`
	CompletedAt         *time.Time `json:"completed_at"`
	SQLSnapshot         string     `json:"sql_script_snapshot"`
	PostgresErrorCode   string     `json:"postgres_error_code,omitempty"`
	Error               string     `json:"error,omitempty"`
}
