package migration

import (
	"context"
	"errors"
	"net/http"
	"time"

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
	Identity(*http.Request) (string, error)
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

var ErrDatabaseNotFound = errors.New("managed database not found")

type Draft struct {
	Name         string `json:"name"`
	Description  string `json:"description"`
	SQL          string `json:"sql_script"`
	ReleaseLabel string `json:"release_label"`
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
type Attempt struct {
	ID          string     `json:"id"`
	MigrationID string     `json:"migration_id"`
	DatabaseID  string     `json:"database_id"`
	SQL         string     `json:"sql_script_snapshot"`
	Executor    string     `json:"executor_user_id"`
	Started     time.Time  `json:"started_at"`
	Completed   *time.Time `json:"completed_at"`
	Status      string     `json:"status"`
	Version     *int64     `json:"version"`
	XID         *string    `json:"target_transaction_id,omitempty"`
	ErrorCode   string     `json:"postgres_error_code,omitempty"`
	Error       string     `json:"error,omitempty"`
}
