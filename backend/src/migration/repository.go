package migration

import (
	"context"
	"crypto/rand"
	_ "embed"
	"encoding/hex"
	"errors"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

//go:embed storage.sql
var storageSQL string

//go:embed review.sql
var reviewSQL string

type Repository struct{ Pool *pgxpool.Pool }

// Initialize creates the migration tables, then the change request workflow tables that depend on them.
func (r *Repository) Initialize(ctx context.Context) error {
	if _, err := r.Pool.Exec(ctx, storageSQL); err != nil {
		return err
	}
	_, err := r.Pool.Exec(ctx, reviewSQL)
	return err
}
func newID() string {
	b := make([]byte, 16)
	if _, err := rand.Read(b); err != nil {
		panic(err)
	}
	return hex.EncodeToString(b)
}

const definitionColumns = `id,database_id,name,description,sql_script,sql_down,irreversible,irreversible_reason,
 release_label,author_user_id,created_at,updated_at,state,version`

func scanMigration(row pgx.Row, includeHistory bool) (Migration, error) {
	var m Migration
	fields := []any{
		&m.ID, &m.DatabaseID, &m.Name, &m.Description, &m.SQLUp, &m.SQLDown, &m.Irreversible, &m.IrreversibleReason,
		&m.ReleaseLabel, &m.Author, &m.Created, &m.Updated, &m.State, &m.Version,
	}
	if includeHistory {
		fields = append(fields, &m.LastExecutor, &m.LastExecuted)
	}
	err := row.Scan(fields...)
	if errors.Is(err, pgx.ErrNoRows) {
		return m, fault("not_found", "Migration not found")
	}
	return m, err
}
func (r *Repository) List(ctx context.Context, db string) ([]Migration, error) {
	rows, err := r.Pool.Query(ctx, `SELECT `+definitionColumns+`,
 (SELECT executor_user_id FROM dmp_migration.attempts a WHERE a.migration_id=d.id ORDER BY started_at DESC,id DESC LIMIT 1),
 (SELECT started_at FROM dmp_migration.attempts a WHERE a.migration_id=d.id ORDER BY started_at DESC,id DESC LIMIT 1)
 FROM dmp_migration.definitions d WHERE database_id=$1 ORDER BY created_at DESC,id`, db)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []Migration{}
	for rows.Next() {
		m, e := scanMigration(rows, true)
		if e != nil {
			return nil, e
		}
		out = append(out, m)
	}
	return out, rows.Err()
}
func (r *Repository) Get(ctx context.Context, db, id string) (Migration, error) {
	return scanMigration(r.Pool.QueryRow(ctx, `SELECT `+definitionColumns+` FROM dmp_migration.definitions WHERE database_id=$1 AND id=$2`, db, id), false)
}
func (r *Repository) Attempts(ctx context.Context, db, id string) ([]Attempt, error) {
	rows, err := r.Pool.Query(ctx, `
 SELECT id,migration_id,database_id,operation,reverses_execution_id,sql_script_snapshot,
        executor_user_id,started_at,completed_at,status,version,target_xid,postgres_error_code,error_message
 FROM dmp_migration.attempts
 WHERE database_id=$1 AND migration_id=$2
 ORDER BY started_at,id`, db, id)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []Attempt{}
	for rows.Next() {
		var a Attempt
		err = rows.Scan(&a.ID, &a.MigrationID, &a.DatabaseID, &a.Operation, &a.ReversesExecutionID, &a.SQL,
			&a.Executor, &a.Started, &a.Completed, &a.Status, &a.SuccessOrder, &a.XID, &a.ErrorCode, &a.Error)
		if err != nil {
			return nil, err
		}
		out = append(out, a)
	}
	return out, rows.Err()
}

func (r *Repository) ExecutionHistory(ctx context.Context, db string) ([]ExecutionHistoryEntry, error) {
	rows, err := r.Pool.Query(ctx, `
 SELECT a.id,a.migration_id,d.name,d.version,a.version,a.operation,a.status,a.reverses_execution_id,
        a.executor_user_id,a.started_at,a.completed_at,a.sql_script_snapshot,a.postgres_error_code,a.error_message
 FROM dmp_migration.attempts a
 JOIN dmp_migration.definitions d ON d.id=a.migration_id AND d.database_id=a.database_id
 WHERE a.database_id=$1
 ORDER BY a.started_at,a.id`, db)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	history := []ExecutionHistoryEntry{}
	for rows.Next() {
		var entry ExecutionHistoryEntry
		err := rows.Scan(&entry.ExecutionID, &entry.MigrationID, &entry.MigrationName, &entry.MigrationVersion,
			&entry.SuccessOrder, &entry.Operation, &entry.Status, &entry.ReversesExecutionID,
			&entry.ExecutorUserID, &entry.StartedAt, &entry.CompletedAt, &entry.SQLSnapshot, &entry.PostgresErrorCode, &entry.Error)
		if err != nil {
			return nil, err
		}
		history = append(history, entry)
	}
	return history, rows.Err()
}

// Lock this database so migration edits and executions cannot overlap.
func (r *Repository) lock(ctx context.Context, db string) (*pgxpool.Conn, func(), error) {
	c, err := r.Pool.Acquire(ctx)
	if err != nil {
		return nil, nil, err
	}
	var ok bool
	err = c.QueryRow(ctx, `SELECT pg_try_advisory_lock(hashtextextended($1,781349))`, db).Scan(&ok)
	if err != nil || !ok {
		// If locking may have succeeded, close the connection to avoid leaving a lock.
		if err != nil {
			clean, cancel := cleanupContext()
			_ = c.Conn().Close(clean)
			cancel()
		}
		c.Release()
		if err != nil {
			return nil, nil, err
		}
		return nil, nil, fault("conflict", "Another migration operation is in progress")
	}
	release := func() {
		clean, cancel := context.WithTimeout(context.Background(), 3*time.Second)
		defer cancel()
		var unlocked bool
		if e := c.QueryRow(clean, `SELECT pg_advisory_unlock(hashtextextended($1,781349))`, db).Scan(&unlocked); e != nil || !unlocked {
			_ = c.Conn().Close(clean)
		}
		c.Release()
	}
	return c, release, nil
}

func (r *Repository) save(ctx context.Context, db, id, user string, d Draft) (Migration, error) {
	c, release, err := r.lock(ctx, db)
	if err != nil {
		return Migration{}, err
	}
	defer release()

	if id == "" {
		row := c.QueryRow(ctx, `INSERT INTO dmp_migration.definitions
 (id,database_id,name,description,sql_script,sql_down,irreversible,irreversible_reason,release_label,author_user_id)
 VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10) RETURNING `+definitionColumns,
			newID(), db, d.Name, d.Description, d.SQLUp, d.SQLDown, d.Irreversible, d.IrreversibleReason, d.ReleaseLabel, user)
		return scanMigration(row, false)
	}

	m, err := scanMigration(c.QueryRow(ctx, `SELECT `+definitionColumns+` FROM dmp_migration.definitions WHERE database_id=$1 AND id=$2`, db, id), false)
	if err != nil {
		return m, err
	}
	if m.State != "draft" {
		return m, fault("conflict", "Only unattempted draft migrations can be edited")
	}

	// Milestone 1 could reset a failed definition to draft without removing its attempts.
	var hasAttempt bool
	err = c.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM dmp_migration.attempts WHERE migration_id=$1)`, id).Scan(&hasAttempt)
	if err != nil {
		return m, err
	}
	if hasAttempt {
		return m, fault("conflict", "A migration that has been attempted cannot be edited. Create a new migration to change its SQL")
	}

	// Approval covers an exact SQL hash, so the SQL is frozen while a change request is open.
	var hasOpenRequest bool
	err = c.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM dmp_migration.change_requests
 WHERE migration_id=$1 AND status IN ('pending_review','approved','queued','running','unknown'))`, id).Scan(&hasOpenRequest)
	if err != nil {
		return m, err
	}
	if hasOpenRequest {
		return m, fault("conflict", "This migration has an open change request. Cancel it before editing the migration")
	}

	row := c.QueryRow(ctx, `UPDATE dmp_migration.definitions
 SET name=$3,description=$4,sql_script=$5,sql_down=$6,irreversible=$7,irreversible_reason=$8,release_label=$9,updated_at=now()
 WHERE database_id=$1 AND id=$2 RETURNING `+definitionColumns,
		db, id, d.Name, d.Description, d.SQLUp, d.SQLDown, d.Irreversible, d.IrreversibleReason, d.ReleaseLabel)
	return scanMigration(row, false)
}

func beginAttempt(ctx context.Context, c *pgxpool.Conn, definition Migration, user string, operation Operation, sql string, reversesExecutionID *string) (Attempt, error) {
	attempt := Attempt{
		ID: newID(), MigrationID: definition.ID, DatabaseID: definition.DatabaseID,
		Operation: operation, ReversesExecutionID: reversesExecutionID,
		SQL: sql, Executor: user, Status: "running",
	}
	tx, err := c.Begin(ctx)
	if err != nil {
		return attempt, err
	}
	defer rollback(tx)

	if definition.Version == nil {
		if err := reserveMigrationVersion(ctx, tx, definition.DatabaseID, definition.ID); err != nil {
			return attempt, err
		}
	}

	err = tx.QueryRow(ctx, `
 INSERT INTO dmp_migration.attempts
     (id,migration_id,database_id,operation,reverses_execution_id,sql_script_snapshot,executor_user_id,status)
 VALUES ($1,$2,$3,$4,$5,$6,$7,'running') RETURNING started_at`,
		attempt.ID, attempt.MigrationID, attempt.DatabaseID, operation, reversesExecutionID, sql, user,
	).Scan(&attempt.Started)
	if err != nil {
		return attempt, err
	}
	return attempt, tx.Commit(ctx)
}

// The caller holds the database lock while allocating this position.
func reserveMigrationVersion(ctx context.Context, tx pgx.Tx, databaseID, migrationID string) error {
	_, err := tx.Exec(ctx, `
 UPDATE dmp_migration.definitions
 SET version=(SELECT COALESCE(MAX(version),0)+1 FROM dmp_migration.definitions WHERE database_id=$1),
     updated_at=now()
 WHERE database_id=$1 AND id=$2 AND version IS NULL`, databaseID, migrationID)
	return err
}

func finishAttempt(ctx context.Context, c *pgxpool.Conn, attempt *Attempt, status, code, message string) error {
	tx, err := c.Begin(ctx)
	if err != nil {
		return err
	}
	defer rollback(tx)

	var successOrder *int64
	if status == "succeeded" {
		var nextOrder int64
		err = tx.QueryRow(ctx, `SELECT COALESCE(MAX(version),0)+1 FROM dmp_migration.attempts WHERE database_id=$1`,
			attempt.DatabaseID).Scan(&nextOrder)
		if err != nil {
			return err
		}
		successOrder = &nextOrder
	}

	var completed *time.Time
	err = tx.QueryRow(ctx, `
 UPDATE dmp_migration.attempts
 SET status=$2,version=$3,completed_at=CASE WHEN $2='unknown' THEN NULL ELSE now() END,
     postgres_error_code=$4,error_message=$5
 WHERE id=$1 AND status IN ('running','unknown')
 RETURNING completed_at`, attempt.ID, status, successOrder, code, message).Scan(&completed)
	if err != nil {
		return err
	}

	var state string
	if status == "succeeded" {
		state = "applied"
		if attempt.Operation == OperationRollback {
			state = "rolled_back"
		}
		if attempt.Operation == OperationApply {
			// A committed M1 attempt recovered after the upgrade may have no reserved version.
			if err := reserveMigrationVersion(ctx, tx, attempt.DatabaseID, attempt.MigrationID); err != nil {
				return err
			}
		}
	} else if status == "failed" && attempt.Operation == OperationApply {
		state = "failed"
	}

	if state != "" {
		_, err = tx.Exec(ctx, `UPDATE dmp_migration.definitions SET state=$3,updated_at=now() WHERE database_id=$1 AND id=$2`,
			attempt.DatabaseID, attempt.MigrationID, state)
		if err != nil {
			return err
		}
	}
	if err = tx.Commit(ctx); err != nil {
		return err
	}

	attempt.Status = status
	attempt.SuccessOrder = successOrder
	attempt.Completed = completed
	attempt.ErrorCode = code
	attempt.Error = message
	return nil
}
func rollback(tx pgx.Tx) {
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	_ = tx.Rollback(ctx)
}
