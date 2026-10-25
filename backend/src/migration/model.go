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

// Access Control/Database Management provide validated identity and the final
// database-specific permission. The adapter owns ALLOW/DENY precedence; Migration
// never queries memberships. PermissionFromEffective translates its resolved value.
// Requests that change data must also pass session-bound CSRF protection.
type Access interface {
	Identity(*http.Request) (string, error)
	Permission(context.Context, string, string) (Permission, error)
	CSRF(*http.Request) error
}

// Connections is supplied by Database Management. Pools remain provider-owned.
// Database IDs are canonical registered UUID strings (opaque IDs may be used by
// isolated test providers). Registration, credentials and decryption stay there.
// The provider creates/reuses pgx pools and closes them only after active users
// drain; Migration closes its transactions, never the borrowed pool. GORM's
// per-request *gorm.DB is not this pool and must not be cast or closed here.
// An ID must retain the same database/cluster lineage for XID recovery. Coordinate
// registry retargeting and pool replacement with unresolved attempts first.
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
