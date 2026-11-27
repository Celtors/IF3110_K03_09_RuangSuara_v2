package migration

import (
	"context"
	"crypto/sha256"
	"errors"
	"fmt"
	"log/slog"
	"strings"
	"time"

	"backend/internal/telemetry"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	"go.opentelemetry.io/otel/attribute"
)

type Service struct {
	Repository  *Repository
	Access      Access
	Connections Connections
}

func isNoRows(err error) bool { return errors.Is(err, pgx.ErrNoRows) }
func (s *Service) authorize(ctx context.Context, user, db string, want Permission) (resultErr error) {
	defer func() { authorizationOutcome(ctx, want, resultErr) }()
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

func (s *Service) ExecutionHistory(ctx context.Context, user, db string, hideRollback bool) ([]ExecutionHistoryEntry, error) {
	db = canonicalDatabaseID(db)
	if err := s.authorize(ctx, user, db, View); err != nil {
		return nil, err
	}

	history, err := s.Repository.ExecutionHistory(ctx, db)
	if err != nil || !hideRollback {
		return history, err
	}
	return hideRolledBackExecutions(history), nil
}

func hideRolledBackExecutions(history []ExecutionHistoryEntry) []ExecutionHistoryEntry {
	successfulByID := make(map[string]string)
	for _, entry := range history {
		if entry.Status == "succeeded" && (entry.Operation == OperationApply || entry.Operation == OperationForward) {
			successfulByID[entry.ExecutionID] = entry.MigrationID
		}
	}

	hidden := make(map[string]struct{})
	for _, entry := range history {
		if entry.Operation != OperationRollback || entry.Status != "succeeded" || entry.ReversesExecutionID == nil {
			continue
		}
		reversedID := *entry.ReversesExecutionID
		migrationID, exists := successfulByID[reversedID]
		if !exists || migrationID != entry.MigrationID {
			continue
		}
		hidden[entry.ExecutionID] = struct{}{}
		hidden[reversedID] = struct{}{}
	}

	visible := make([]ExecutionHistoryEntry, 0, len(history)-len(hidden))
	for _, entry := range history {
		if _, hide := hidden[entry.ExecutionID]; !hide {
			visible = append(visible, entry)
		}
	}
	return visible
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
	ctx = migrationContext(ctx, user, db, id)
	if err := s.authorize(ctx, user, db, Edit); err != nil {
		return Migration{}, err
	}

	d.Name = strings.TrimSpace(d.Name)
	d.IrreversibleReason = strings.TrimSpace(d.IrreversibleReason)
	if err := d.validate(false); err != nil {
		return Migration{}, err
	}

	if _, err := s.connection(ctx, db); err != nil {
		return Migration{}, err
	}
	m, err := s.Repository.save(ctx, db, id, user, d)
	if err == nil {
		action := "updated"
		if id == "" {
			action = "created"
		}
		ctx = telemetry.With(ctx, telemetry.ID("migration_id", m.ID))
		slog.InfoContext(ctx, "migration.definition_saved", "status", "succeeded", "action", action)
	}
	return m, err
}

// ValidateForReview checks the definition that Approval intends to submit.
func (d Draft) ValidateForReview() error {
	return d.validate(true)
}

func (d Draft) validate(forReview bool) error {
	name := strings.TrimSpace(d.Name)
	if name == "" {
		return fault("invalid_input", "A migration name is required")
	}
	if len(name) > 200 || len(d.Description) > 4000 || len(d.ReleaseLabel) > 100 {
		return fault("invalid_input", "The name, description, and release label can be at most 200, 4000, and 100 bytes respectively")
	}
	if _, err := parseSQL(d.SQLUp, false); err != nil {
		return err
	}

	hasDown := strings.TrimSpace(d.SQLDown) != ""
	hasReason := strings.TrimSpace(d.IrreversibleReason) != ""
	if d.Irreversible && hasDown {
		return fault("invalid_input", "An irreversible migration cannot include SQL down")
	}
	if !d.Irreversible && hasReason {
		return fault("invalid_input", "Mark the migration irreversible to supply an irreversible reason")
	}

	if hasDown {
		if _, err := parseSQL(d.SQLDown, false); err != nil {
			var validation *Fault
			if errors.As(err, &validation) {
				return fault(validation.Code, "SQL down failed validation. "+validation.Message)
			}
			return err
		}
	}

	if forReview {
		if d.Irreversible && !hasReason {
			return fault("invalid_input", "An irreversible migration needs a reason before review")
		}
		if !d.Irreversible && !hasDown {
			return fault("invalid_input", "Supply SQL down or designate the migration irreversible before review")
		}
	}

	return nil
}

func (s *Service) Execute(ctx context.Context, user, db, id string) (Attempt, error) {
	return s.ExecuteOperation(ctx, user, db, id, OperationApply)
}

// ExecuteOperation runs an operation with the caller's context, including from a worker.
// Approval and job delivery checks belong to the caller's integration.
func (s *Service) ExecuteOperation(ctx context.Context, user, db, id string, operation Operation) (Attempt, error) {
	return s.executeOperation(ctx, user, db, id, operation, nil, nil)
}

// executeOperation is ExecuteOperation with a hook. onBegin runs once the attempt has been
// stored and before any SQL reaches the target, so the queue worker can link the attempt to
// its change request even if the process dies mid-execution.
func (s *Service) executeOperation(ctx context.Context, user, db, id string, operation Operation, beforeBegin func(context.Context, *pgxpool.Conn) error, onBegin func(context.Context, Attempt) error) (_ Attempt, resultErr error) {
	db = canonicalDatabaseID(db)
	ctx = migrationContext(ctx, user, db, id)
	if validOperation(operation) {
		ctx = telemetry.With(ctx, slog.String("operation", string(operation)))
	}
	ctx, span := telemetry.Start(ctx, "migration.execute")
	defer telemetry.EndSpan(span)
	defer func() {
		if resultErr == nil {
			span.SetAttributes(attribute.String("outcome", "succeeded"))
			slog.InfoContext(ctx, "migration.execution", "status", "succeeded")
			return
		}
		code, _ := faultInfo(resultErr)
		status := "failed"
		if code == "indeterminate" {
			status = "unknown"
		}
		span.SetAttributes(attribute.String("outcome", status))
		traceMigrationError(span, resultErr)
		slog.WarnContext(ctx, "migration.execution", "status", status, "reason", code)
	}()
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
	if beforeBegin != nil {
		if err = beforeBegin(ctx, platformConn); err != nil {
			return Attempt{}, err
		}
	}

	// Resolve unfinished executions before assigning another migration version.
	if err = s.reconcileLocked(ctx, platformConn, pool, db); err != nil {
		return Attempt{}, err
	}
	definition, err := scanMigration(platformConn.QueryRow(ctx, `SELECT `+definitionColumns+` FROM dmp_migration.definitions WHERE database_id=$1 AND id=$2`, db, id), false)
	if err != nil {
		return Attempt{}, err
	}
	sql, reversesExecutionID, err := prepareExecution(ctx, platformConn, definition, operation)
	if err != nil {
		return Attempt{}, err
	}

	var expectedTarget string
	if operation != OperationApply {
		err = platformConn.QueryRow(ctx, `
 SELECT target_identity FROM dmp_migration.attempts
 WHERE database_id=$1 AND migration_id=$2 AND status='succeeded'
 ORDER BY version DESC LIMIT 1`, db, id).Scan(&expectedTarget)
		if err != nil && !isNoRows(err) {
			return Attempt{}, err
		}
		if expectedTarget == "" {
			return Attempt{}, fault("conflict", "The original target for this migration cannot be verified")
		}
	}

	attempt, err := beginAttempt(ctx, platformConn, definition, user, operation, sql, reversesExecutionID)
	if err != nil {
		return attempt, err
	}
	ctx = attemptContext(ctx, attempt)
	span.SetAttributes(attribute.String("attempt_id", attempt.ID))
	slog.InfoContext(ctx, "migration.attempt_started", "status", "running",
		"retry", operation == OperationApply && definition.State == "failed")
	if onBegin != nil {
		if err = onBegin(ctx, attempt); err != nil {
			clean, done := cleanupContext(ctx)
			defer done()
			if e := finishAttempt(clean, platformConn, &attempt, "failed", "", "Approval or attempt linking failed before SQL"); e != nil {
				return attempt, fault("indeterminate", "Attempt outcome could not be recorded")
			}
			return attempt, err
		}
	}
	var replay *batchReplay
	if operation != OperationApply {
		replay, err = completedBatchReplay(ctx, platformConn, definition, operation)
		if err != nil {
			clean, done := cleanupContext(ctx)
			defer done()
			if e := finishAttempt(clean, platformConn, &attempt, "failed", "", err.Error()); e != nil {
				return attempt, fault("indeterminate", "Outcome could not be recorded")
			}
			return attempt, err
		}
	}
	var result executionResult
	for tries := 0; tries < 3; tries++ {
		result = executePostgres(ctx, pool, attempt.SQL, expectedTarget, func(xid, identity string) error {
			_, recordErr := platformConn.Exec(ctx, `UPDATE dmp_migration.attempts SET target_xid=$2,target_identity=$3,prepared_rows=NULL WHERE id=$1`, attempt.ID, xid, identity)
			if recordErr == nil {
				attempt.XID = &xid
				attempt.TargetIdentity = identity
			}
			return recordErr
		}, func(rows int64) error {
			_, e := platformConn.Exec(ctx, `UPDATE dmp_migration.attempts SET prepared_rows=$2 WHERE id=$1`, attempt.ID, rows)
			return e
		}, replay)
		if result.Status != "failed" || (result.Code != "40001" && result.Code != "40P01" && result.Code != "55P03") || tries == 2 {
			break
		}
		if ctx.Err() != nil {
			break
		}
		time.Sleep(time.Duration(tries+1) * 250 * time.Millisecond)
		if onBegin != nil {
			if e := onBegin(ctx, attempt); e != nil {
				result = executionResult{Status: "failed", FaultCode: "forbidden", Message: "Execution authorization could not be revalidated before retry"}
				break
			}
		}
	}

	clean, done := cleanupContext(ctx)
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
	ctx = migrationContext(ctx, user, db, "")
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
func (s *Service) reconcileLocked(ctx context.Context, c *pgxpool.Conn, pool *pgxpool.Pool, db string) (resultErr error) {
	ctx, span := telemetry.Start(ctx, "migration.reconcile")
	defer telemetry.EndSpan(span)
	outcome := "unresolved"
	defer func() {
		span.SetAttributes(attribute.String("outcome", outcome))
		traceMigrationError(span, resultErr)
	}()

	var batchActive bool
	if err := c.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM dmp_migration.change_requests r JOIN dmp_migration.batch_jobs b ON b.id=r.id WHERE r.database_id=$1 AND r.status='running')`, db).Scan(&batchActive); err != nil {
		return err
	}
	if batchActive {
		outcome = "batch_active"
		slog.InfoContext(ctx, "migration.reconcile", "status", outcome)
		return fault("indeterminate", "Active batch job holds this database; use its recovery API")
	}
	var a Attempt
	err := c.QueryRow(ctx, `
 SELECT a.id,a.migration_id,a.database_id,a.operation,a.reverses_execution_id,a.target_xid,a.target_identity,d.version
 FROM dmp_migration.attempts a JOIN dmp_migration.definitions d ON d.id=a.migration_id
 WHERE a.database_id=$1 AND a.status IN ('running','unknown')`, db).
		Scan(&a.ID, &a.MigrationID, &a.DatabaseID, &a.Operation, &a.ReversesExecutionID, &a.XID, &a.TargetIdentity, &a.migrationVersion)
	if isNoRows(err) {
		outcome = "no_unresolved_attempt"
		return nil
	}
	if err != nil {
		return err
	}
	ctx = attemptContext(telemetry.With(ctx, telemetry.ID("migration_id", a.MigrationID),
		slog.String("operation", string(a.Operation))), a)
	span.SetAttributes(attribute.String("attempt_id", a.ID), attribute.String("migration_id", a.MigrationID),
		attribute.String("operation", string(a.Operation)))
	slog.InfoContext(ctx, "migration.reconcile", "status", "started")
	defer func() {
		if resultErr != nil {
			code, _ := faultInfo(resultErr)
			slog.WarnContext(ctx, "migration.reconcile", "status", "unresolved", "reason", code)
		}
	}()
	if a.TargetIdentity == "pending" && a.XID == nil {
		if err := finishAttempt(ctx, c, &a, "failed", "interrupted", "Worker stopped before target SQL was submitted"); err != nil {
			return err
		}
		outcome = "interrupted"
		return nil
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
			slog.InfoContext(ctx, "transaction.reconciled", "status", *status, "database_scope", "target")
			span.SetAttributes(attribute.String("transaction.outcome", *status))
			if err := finishAttempt(ctx, c, &a, resolved, "", "Outcome recovered from PostgreSQL transaction status"); err != nil {
				return err
			}
			outcome = "resolved"
			return nil
		}
	}
	slog.WarnContext(ctx, "transaction.reconciled", "status", "unknown", "database_scope", "target")
	span.SetAttributes(attribute.String("transaction.outcome", "unknown"))
	// Do not retry automatically because the previous execution may have succeeded.
	if err = finishAttempt(ctx, c, &a, "unknown", "", "Target outcome remains unresolved; operator investigation required"); err != nil {
		return err
	}
	return fault("indeterminate", "An unresolved execution blocks this database; reconcile after the target transaction finishes")
}
