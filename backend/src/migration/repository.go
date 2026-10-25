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

type Repository struct{ Pool *pgxpool.Pool }

func (r *Repository) Initialize(ctx context.Context) error {
	_, err := r.Pool.Exec(ctx, storageSQL)
	return err
}
func newID() string {
	b := make([]byte, 16)
	if _, err := rand.Read(b); err != nil {
		panic(err)
	}
	return hex.EncodeToString(b)
}

const definitionColumns = `id,database_id,name,description,sql_script,release_label,author_user_id,created_at,updated_at,state,version`

func scanMigration(row pgx.Row, includeHistory bool) (Migration, error) {
	var m Migration
	fields := []any{&m.ID, &m.DatabaseID, &m.Name, &m.Description, &m.SQL, &m.ReleaseLabel, &m.Author, &m.Created, &m.Updated, &m.State, &m.Version}
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
	rows, err := r.Pool.Query(ctx, `SELECT id,migration_id,database_id,sql_script_snapshot,executor_user_id,started_at,completed_at,status,version,target_xid,postgres_error_code,error_message FROM dmp_migration.attempts WHERE database_id=$1 AND migration_id=$2 ORDER BY started_at,id`, db, id)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []Attempt{}
	for rows.Next() {
		var a Attempt
		if err = rows.Scan(&a.ID, &a.MigrationID, &a.DatabaseID, &a.SQL, &a.Executor, &a.Started, &a.Completed, &a.Status, &a.Version, &a.XID, &a.ErrorCode, &a.Error); err != nil {
			return nil, err
		}
		out = append(out, a)
	}
	return out, rows.Err()
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
		return scanMigration(c.QueryRow(ctx, `INSERT INTO dmp_migration.definitions(id,database_id,name,description,sql_script,release_label,author_user_id) VALUES($1,$2,$3,$4,$5,$6,$7) RETURNING `+definitionColumns, newID(), db, d.Name, d.Description, d.SQL, d.ReleaseLabel, user), false)
	}
	m, err := scanMigration(c.QueryRow(ctx, `SELECT `+definitionColumns+` FROM dmp_migration.definitions WHERE database_id=$1 AND id=$2`, db, id), false)
	if err != nil {
		return m, err
	}
	if m.State != "draft" && m.State != "failed" {
		return m, fault("conflict", "Only unapplied drafts with a resolved outcome can be edited")
	}
	return scanMigration(c.QueryRow(ctx, `UPDATE dmp_migration.definitions SET name=$3,description=$4,sql_script=$5,release_label=$6,state='draft',updated_at=now() WHERE database_id=$1 AND id=$2 RETURNING `+definitionColumns, db, id, d.Name, d.Description, d.SQL, d.ReleaseLabel), false)
}

func beginAttempt(ctx context.Context, c *pgxpool.Conn, m Migration, user string) (Attempt, error) {
	a := Attempt{ID: newID(), MigrationID: m.ID, DatabaseID: m.DatabaseID, SQL: m.SQL, Executor: user, Status: "running"}
	tx, err := c.Begin(ctx)
	if err != nil {
		return a, err
	}
	defer rollback(tx)
	err = tx.QueryRow(ctx, `INSERT INTO dmp_migration.attempts(id,migration_id,database_id,sql_script_snapshot,executor_user_id,status) VALUES($1,$2,$3,$4,$5,'running') RETURNING started_at`, a.ID, a.MigrationID, a.DatabaseID, a.SQL, user).Scan(&a.Started)
	if err != nil {
		return a, err
	}
	_, err = tx.Exec(ctx, `UPDATE dmp_migration.definitions SET state='running',updated_at=now() WHERE id=$1`, m.ID)
	if err != nil {
		return a, err
	}
	return a, tx.Commit(ctx)
}
func finishAttempt(ctx context.Context, c *pgxpool.Conn, a *Attempt, status, code, message string) error {
	tx, err := c.Begin(ctx)
	if err != nil {
		return err
	}
	defer rollback(tx)
	var version *int64
	if status == "succeeded" {
		var v int64
		err = tx.QueryRow(ctx, `SELECT COALESCE(MAX(version),0)+1 FROM dmp_migration.attempts WHERE database_id=$1`, a.DatabaseID).Scan(&v)
		if err != nil {
			return err
		}
		version = &v
	}
	var completed time.Time
	err = tx.QueryRow(ctx, `UPDATE dmp_migration.attempts SET status=$2,version=$3,completed_at=now(),postgres_error_code=$4,error_message=$5 WHERE id=$1 AND status IN ('running','unknown') RETURNING completed_at`, a.ID, status, version, code, message).Scan(&completed)
	if err != nil {
		return err
	}
	state := status
	if status == "succeeded" {
		state = "applied"
	}
	_, err = tx.Exec(ctx, `UPDATE dmp_migration.definitions SET state=$2,version=$3,updated_at=now() WHERE id=$1`, a.MigrationID, state, version)
	if err != nil {
		return err
	}
	if err = tx.Commit(ctx); err != nil {
		return err
	}
	a.Status = status
	a.Version = version
	a.Completed = &completed
	a.ErrorCode = code
	a.Error = message
	return nil
}
func rollback(tx pgx.Tx) {
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	_ = tx.Rollback(ctx)
}
