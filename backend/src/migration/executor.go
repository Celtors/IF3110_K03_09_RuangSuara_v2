package migration

import (
	"context"
	"errors"
	"strings"
	"time"

	"backend/internal/telemetry"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgxpool"
)

type executionResult struct{ Status, Code, Message, FaultCode string }

// Record the transaction ID so interrupted commits can be checked later.
func executePostgres(ctx context.Context, pool *pgxpool.Pool, sql, expectedTarget string, recordXID func(string, string) error) executionResult {
	tx, err := pool.BeginTx(ctx, pgx.TxOptions{})
	if err != nil {
		return executionResult{Status: "failed", Message: "Target connection unavailable before SQL execution", FaultCode: "database_unavailable"}
	}
	defer rollback(ctx, tx)
	if _, err = tx.Exec(ctx, `SET LOCAL statement_timeout = '30s'; SET LOCAL lock_timeout = '5s'; SET LOCAL idle_in_transaction_session_timeout = '40s'`); err != nil {
		return executionResult{Status: "failed", Message: "Could not configure target transaction"}
	}
	// Inspect the connection that will actually execute SQL, not another pool
	// member that might belong to a different server behind the same endpoint.
	identity, err := targetIdentity(ctx, tx.Conn())
	if err != nil {
		return executionResult{Status: "failed", Message: err.Error(), FaultCode: "database_unavailable"}
	}
	if expectedTarget != "" && identity != expectedTarget {
		return executionResult{Status: "failed", Message: "The target differs from the original migration target", FaultCode: "conflict"}
	}
	var xid string
	if err = tx.QueryRow(ctx, `SELECT pg_catalog.pg_current_xact_id()::text`).Scan(&xid); err != nil {
		return executionResult{Status: "failed", Message: "Could not identify target transaction"}
	}
	if err = recordXID(xid, identity); err != nil {
		return executionResult{Status: "failed", Message: "Could not persist transaction identity; SQL was not submitted"}
	}
	// Drain each result without materializing returned rows. Command tags belong
	// to statement execution and say nothing about the later commit.
	sqlCtx, activity := telemetry.StartSQL(ctx, "target", sql, 0)
	results := tx.Conn().PgConn().Exec(sqlCtx, sql)
	var total int64
	var rows *int64
	for results.NextResult() {
		tag, _ := results.ResultReader().Close()
		if count := telemetry.RowCount(tag); count != nil {
			total += *count
			rows = &total
		}
	}
	err = results.Close()
	activity.Finish(sqlCtx, rows, err)
	if err != nil {
		return executionResult{Status: "failed", Code: postgresCode(err), Message: "PostgreSQL rejected the migration; transaction rolled back"}
	}
	return commitResult(tx.Commit(ctx))
}

func commitResult(err error) executionResult {
	if err == nil {
		return executionResult{Status: "succeeded"}
	}

	if errors.Is(err, pgx.ErrTxCommitRollback) {
		return executionResult{Status: "failed", Message: "PostgreSQL rejected COMMIT; transaction rolled back"}
	}

	// A server error can also report an unknown outcome or a lost connection.
	var pgerr *pgconn.PgError
	if errors.As(err, &pgerr) && pgerr.SeverityUnlocalized != "FATAL" && pgerr.SeverityUnlocalized != "PANIC" &&
		!strings.HasPrefix(pgerr.Code, "08") && pgerr.Code != "40003" {
		return executionResult{Status: "failed", Code: pgerr.Code, Message: "PostgreSQL rejected COMMIT; transaction rolled back"}
	}
	return executionResult{Status: "unknown", Code: postgresCode(err), Message: "COMMIT outcome could not be confirmed; reconciliation is required"}
}
func postgresCode(err error) string {
	var pgerr *pgconn.PgError
	if errors.As(err, &pgerr) {
		return pgerr.Code
	}
	return ""
}

func cleanupContext(ctx context.Context) (context.Context, context.CancelFunc) {
	return context.WithTimeout(context.WithoutCancel(ctx), 5*time.Second)
}
