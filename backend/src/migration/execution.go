package migration

import (
	"context"
	"strings"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
)

// prepareExecution reads the current path while the caller holds the database lock.
func prepareExecution(ctx context.Context, conn *pgxpool.Conn, definition Migration, operation Operation) (string, *string, error) {
	var sql string
	var reversesExecutionID *string

	switch operation {
	case OperationApply:
		if definition.State == "applied" {
			return "", nil, fault("already_applied", "This migration has already been applied")
		}
		if definition.State != "draft" && definition.State != "failed" {
			return "", nil, fault("conflict", "Only draft or failed migrations can be applied")
		}
		if strings.TrimSpace(definition.Name) == "" {
			return "", nil, fault("invalid_input", "A migration name is required before execution")
		}
		if err := checkApplyOrder(ctx, conn, definition); err != nil {
			return "", nil, err
		}
		sql = definition.SQLUp

	case OperationRollback:
		if definition.State != "applied" {
			return "", nil, fault("conflict", "Only an applied migration can be rolled back")
		}
		if definition.Irreversible || strings.TrimSpace(definition.SQLDown) == "" {
			return "", nil, fault("conflict", "This migration has no reversible SQL down")
		}

		executionID, err := rollbackExecutionID(ctx, conn, definition)
		if err != nil {
			return "", nil, err
		}
		sql = definition.SQLDown
		reversesExecutionID = &executionID

	case OperationForward:
		if definition.State != "rolled_back" {
			return "", nil, fault("conflict", "Only a successfully rolled-back migration can be forwarded")
		}
		if err := checkForwardOrder(ctx, conn, definition); err != nil {
			return "", nil, err
		}
		sql = definition.SQLUp

	default:
		return "", nil, fault("invalid_input", "Invalid migration operation")
	}

	if _, err := parseSQL(sql, false); err != nil {
		return "", nil, err
	}
	return sql, reversesExecutionID, nil
}

func rollbackExecutionID(ctx context.Context, conn *pgxpool.Conn, definition Migration) (string, error) {
	var executionID string
	err := conn.QueryRow(ctx, `
 SELECT a.id
 FROM dmp_migration.attempts a
 JOIN dmp_migration.definitions d ON d.id=a.migration_id
 WHERE a.database_id=$1 AND d.id=$2 AND a.status='succeeded' AND a.operation IN ('apply','forward')
   AND d.version=(SELECT MAX(version) FROM dmp_migration.definitions WHERE database_id=$1 AND state='applied')
 ORDER BY a.version DESC
 LIMIT 1`, definition.DatabaseID, definition.ID).Scan(&executionID)
	if isNoRows(err) {
		return "", fault("migration_order_conflict", "Rollback requires the latest active migration and its successful execution")
	}
	return executionID, err
}

func checkApplyOrder(ctx context.Context, conn *pgxpool.Conn, definition Migration) error {
	if definition.State == "failed" {
		var partial bool
		err := conn.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM dmp_migration.batch_jobs b
		 JOIN dmp_migration.change_requests p ON p.id=b.id
		 WHERE p.migration_id=$1 AND b.correction_started)`, definition.ID).Scan(&partial)
		if err != nil {
			return err
		}
		if partial {
			return fault("conflict", "This migration retains partial writes and cannot be replayed")
		}
	}
	var legacyStartedAt *time.Time
	if definition.Version == nil {
		var lastAttemptSQL string
		err := conn.QueryRow(ctx, `
 SELECT MIN(started_at) OVER (), sql_script_snapshot
 FROM dmp_migration.attempts
 WHERE database_id=$1 AND migration_id=$2 AND operation='apply'
 ORDER BY started_at DESC,id DESC LIMIT 1`, definition.DatabaseID, definition.ID).
			Scan(&legacyStartedAt, &lastAttemptSQL)
		if isNoRows(err) {
			return nil
		}
		if err != nil {
			return err
		}
		if lastAttemptSQL != definition.SQLUp {
			return fault("conflict", "Stored SQL differs from the previous attempt. Create a new migration to apply it")
		}
	}

	// Legacy failures have no version. Use their recorded first start and reject ties.
	var newerApply bool
	err := conn.QueryRow(ctx, `
 SELECT EXISTS (
     SELECT 1
     FROM dmp_migration.attempts a
     JOIN dmp_migration.definitions d ON d.id=a.migration_id
     WHERE a.database_id=$1 AND a.migration_id<>$2 AND a.operation='apply' AND a.status='succeeded'
       AND (d.version>$3::bigint OR ($3 IS NULL AND a.started_at>=$4::timestamptz))
 )`, definition.DatabaseID, definition.ID, definition.Version, legacyStartedAt).Scan(&newerApply)
	if err != nil {
		return err
	}
	if newerApply {
		return fault("migration_order_conflict", "A newer migration has already been applied. Create a new migration instead of retrying this one")
	}
	return nil
}

func checkForwardOrder(ctx context.Context, conn *pgxpool.Conn, definition Migration) error {
	nextID, err := nextForwardID(ctx, conn, definition.DatabaseID)
	if err != nil {
		return err
	}
	if nextID != definition.ID {
		return fault("migration_order_conflict", "Only the next rolled-back migration on the current path can be forwarded")
	}
	return nil
}

func nextForwardID(ctx context.Context, conn *pgxpool.Conn, databaseID string) (string, error) {
	// A later initial apply closes an older rollback's forward path, even if
	// the later migration is eventually rolled back too.
	var nextID string
	err := conn.QueryRow(ctx, `
 WITH last_rollbacks AS (
     SELECT migration_id, MAX(version) AS success_order
     FROM dmp_migration.attempts
     WHERE database_id=$1 AND operation='rollback' AND status='succeeded'
     GROUP BY migration_id
 )
 SELECT d.id
 FROM dmp_migration.definitions d
 JOIN last_rollbacks r ON r.migration_id=d.id
 WHERE d.database_id=$1 AND d.state='rolled_back'
   AND d.version>COALESCE((SELECT MAX(version) FROM dmp_migration.definitions WHERE database_id=$1 AND state='applied'), 0)
   AND NOT EXISTS (
       SELECT 1
       FROM dmp_migration.attempts a
       JOIN dmp_migration.definitions newer ON newer.id=a.migration_id
       WHERE a.database_id=$1 AND a.operation='apply' AND a.status='succeeded'
         AND newer.version>d.version AND a.version>r.success_order
   )
 ORDER BY d.version
 LIMIT 1`, databaseID).Scan(&nextID)
	if isNoRows(err) {
		return "", nil
	}
	return nextID, err
}
