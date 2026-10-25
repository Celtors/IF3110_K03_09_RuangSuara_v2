package migration

import (
	"context"
	"crypto/sha256"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

type Service struct {
	Repository  *Repository
	Access      Access
	Connections Connections
}

func isNoRows(err error) bool { return errors.Is(err, pgx.ErrNoRows) }
func (s *Service) authorize(ctx context.Context, user, db string, want Permission) error {
	if user == "" || s.Access == nil {
		return fault("unauthenticated", "A validated session is required")
	}
	if db == "" || len(db) > 200 {
		return fault("invalid_input", "Invalid managed database ID")
	}
	permission, err := s.Access.Permission(ctx, user, db)
	if err != nil {
		var f *Fault
		if errors.As(err, &f) && f.Code == "invalid_input" {
			return f
		}
		return fault("authorization_unavailable", "Database authorization is unavailable")
	}
	if permission < want || permission > Edit {
		return fault("forbidden", "You do not have the required database permission")
	}
	if s.Repository == nil || s.Repository.Pool == nil {
		return fault("database_unavailable", "Migration storage is unavailable")
	}
	return nil
}

// This identifies configured endpoints, without persisting credentials. A
// registration retarget must never make recovery inspect an unrelated XID.
func targetIdentity(ctx context.Context, conn *pgx.Conn) (string, error) {
	c := conn.Config()
	var cluster, database string
	if err := conn.QueryRow(ctx, `SELECT system_identifier::text, (SELECT oid::text FROM pg_catalog.pg_database WHERE datname=pg_catalog.current_database()) FROM pg_catalog.pg_control_system()`).Scan(&cluster, &database); err != nil {
		if postgresCode(err) == "42501" {
			return "", fault("database_unavailable", "Target identity inspection is not permitted; an administrator must check EXECUTE on pg_catalog.pg_control_system()")
		}
		return "", fault("database_unavailable", "Target identity could not be verified")
	}
	return fmt.Sprintf("%x", sha256.Sum256([]byte(fmt.Sprintf("%s\x00%d\x00%s\x00%s\x00%s", c.Host, c.Port, c.Database, cluster, database)))), nil
}
func (s *Service) connection(ctx context.Context, db string) (*pgxpool.Pool, error) {
	if s.Connections == nil {
		return nil, fault("database_unavailable", "Database connection provider is unavailable")
	}
	pool, err := s.Connections.PostgreSQL(ctx, db)
	if errors.Is(err, ErrDatabaseNotFound) {
		return nil, fault("not_found", "Managed database not found")
	}
	if err != nil || pool == nil {
		return nil, fault("database_unavailable", "Target database connection unavailable")
	}
	return pool, nil
}
func (s *Service) History(ctx context.Context, user, db string) ([]Migration, Permission, error) {
	db = canonicalDatabaseID(db)
	if err := s.authorize(ctx, user, db, View); err != nil {
		return nil, NoAccess, err
	}
	if _, err := s.connection(ctx, db); err != nil {
		return nil, NoAccess, err
	}
	p, err := s.Access.Permission(ctx, user, db)
	if err != nil {
		return nil, NoAccess, fault("authorization_unavailable", "Database authorization is unavailable")
	}
	ms, err := s.Repository.List(ctx, db)
	return ms, p, err
}
func (s *Service) Details(ctx context.Context, user, db, id string) (Migration, error) {
	db = canonicalDatabaseID(db)
	if err := s.authorize(ctx, user, db, View); err != nil {
		return Migration{}, err
	}
	m, err := s.Repository.Get(ctx, db, id)
	if err != nil {
		return m, err
	}
	m.Attempts, err = s.Repository.Attempts(ctx, db, id)
	return m, err
}
func (s *Service) Save(ctx context.Context, user, db, id string, d Draft) (Migration, error) {
	db = canonicalDatabaseID(db)
	if err := s.authorize(ctx, user, db, Edit); err != nil {
		return Migration{}, err
	}
	d.Name = strings.TrimSpace(d.Name)
	if len(d.Name) < 1 || len(d.Name) > 200 || len(d.Description) > 4000 || len(d.ReleaseLabel) > 100 {
		return Migration{}, fault("invalid_input", "Name is required (maximum 200 bytes); description maximum 4000 and release label maximum 100")
	}
	if _, err := parseSQL(d.SQL, false); err != nil {
		return Migration{}, err
	}
	if _, err := s.connection(ctx, db); err != nil {
		return Migration{}, err
	}
	return s.Repository.save(ctx, db, id, user, d)
}
func (s *Service) Execute(ctx context.Context, user, db, id string) (Attempt, error) {
	db = canonicalDatabaseID(db)
	if err := s.authorize(ctx, user, db, Edit); err != nil {
		return Attempt{}, err
	}
	ctx, cancel := context.WithTimeout(ctx, 40*time.Second)
	defer cancel()
	pool, err := s.connection(ctx, db)
	if err != nil {
		return Attempt{}, err
	}
	platformConn, release, err := s.Repository.lock(ctx, db)
	if err != nil {
		return Attempt{}, err
	}
	defer release()
	// Resolve unfinished executions before assigning another migration version.
	if err = s.reconcileLocked(ctx, platformConn, pool, db); err != nil {
		return Attempt{}, err
	}
	definition, err := scanMigration(platformConn.QueryRow(ctx, `SELECT `+definitionColumns+` FROM dmp_migration.definitions WHERE database_id=$1 AND id=$2`, db, id), false)
	if err != nil {
		return Attempt{}, err
	}
	if definition.State == "applied" {
		return Attempt{}, fault("already_applied", "This migration has already been applied")
	}
	if definition.State != "draft" && definition.State != "failed" {
		return Attempt{}, fault("conflict", "This migration has an unresolved execution")
	}
	if _, err = parseSQL(definition.SQL, false); err != nil {
		return Attempt{}, err
	}
	attempt, err := beginAttempt(ctx, platformConn, definition, user, "")
	if err != nil {
		return attempt, err
	}
	result := executePostgres(ctx, pool, definition.SQL, func(xid, identity string) error {
		_, recordErr := platformConn.Exec(ctx, `UPDATE dmp_migration.attempts SET target_xid=$2,target_identity=$3 WHERE id=$1`, attempt.ID, xid, identity)
		if recordErr == nil {
			attempt.XID = &xid
			attempt.TargetIdentity = identity
		}
		return recordErr
	})
	clean, done := cleanupContext()
	defer done()
	if err = finishAttempt(clean, platformConn, &attempt, result.Status, result.Code, result.Message); err != nil {
		return attempt, fault("indeterminate", "Target outcome could not be recorded; execution is blocked until reconciliation")
	}
	if result.Status == "failed" {
		if result.FaultCode != "" {
			return attempt, fault(result.FaultCode, result.Message)
		}
		return attempt, fault("execution_failed", result.Message)
	}
	if result.Status == "unknown" {
		return attempt, fault("indeterminate", result.Message)
	}
	return attempt, nil
}

// Reconcile checks the outcome of interrupted migrations.
// Only one recovery operation can run per database at a time.
func (s *Service) Reconcile(ctx context.Context, user, db string) error {
	db = canonicalDatabaseID(db)
	if err := s.authorize(ctx, user, db, Edit); err != nil {
		return err
	}
	pool, err := s.connection(ctx, db)
	if err != nil {
		return err
	}
	c, release, err := s.Repository.lock(ctx, db)
	if err != nil {
		return err
	}
	defer release()
	return s.reconcileLocked(ctx, c, pool, db)
}
func (s *Service) reconcileLocked(ctx context.Context, c *pgxpool.Conn, pool *pgxpool.Pool, db string) error {
	var a Attempt
	err := c.QueryRow(ctx, `SELECT id,migration_id,database_id,target_xid,target_identity FROM dmp_migration.attempts WHERE database_id=$1 AND status IN ('running','unknown')`, db).Scan(&a.ID, &a.MigrationID, &a.DatabaseID, &a.XID, &a.TargetIdentity)
	if isNoRows(err) {
		return nil
	}
	if err != nil {
		return err
	}
	if a.TargetIdentity == "" {
		return fault("indeterminate", "The execution has no recorded target identity; operator investigation is required")
	}
	probe, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	target, err := pool.Acquire(probe)
	if err != nil {
		return fault("database_unavailable", "Target connection unavailable during reconciliation")
	}
	defer target.Release()
	identity, err := targetIdentity(probe, target.Conn())
	if err != nil {
		return err
	}
	if a.TargetIdentity != identity {
		return fault("indeterminate", "The target configuration differs from the recorded execution; restore the original target before reconciliation")
	}
	if a.XID != nil {
		var status *string
		// Identity and outcome must come from the same physical connection.
		e := target.QueryRow(probe, `SELECT pg_catalog.pg_xact_status($1::xid8)`, *a.XID).Scan(&status)
		if e == nil && status != nil && (*status == "committed" || *status == "aborted") {
			resolved := "failed"
			if *status == "committed" {
				resolved = "succeeded"
			}
			return finishAttempt(ctx, c, &a, resolved, "", "Outcome recovered from PostgreSQL transaction status")
		}
	}
	// Do not retry automatically because the previous execution may have succeeded.
	if err = finishAttempt(ctx, c, &a, "unknown", "", "Target outcome remains unresolved; operator investigation required"); err != nil {
		return err
	}
	return fault("indeterminate", "An unresolved execution blocks this database; reconcile after the target transaction finishes")
}
