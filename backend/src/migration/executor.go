package migration

import (
	"context"
	"errors"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgxpool"
)

type executionResult struct{ Status, Code, Message, FaultCode string }

// Record the transaction ID so interrupted commits can be checked later.
func executePostgres(ctx context.Context, pool *pgxpool.Pool, sql string, recordXID func(string) error) executionResult {
	tx, err := pool.BeginTx(ctx, pgx.TxOptions{})
	if err != nil {
		return executionResult{Status: "failed", Message: "Target connection unavailable before SQL execution", FaultCode: "database_unavailable"}
	}
	defer rollback(tx)
	if _, err = tx.Exec(ctx, `SET LOCAL statement_timeout = '30s'; SET LOCAL lock_timeout = '5s'; SET LOCAL idle_in_transaction_session_timeout = '40s'`); err != nil {
		return executionResult{Status: "failed", Message: "Could not configure target transaction"}
	}
	var xid string
	if err = tx.QueryRow(ctx, `SELECT pg_current_xact_id()::text`).Scan(&xid); err != nil {
		return executionResult{Status: "failed", Message: "Could not identify target transaction"}
	}
	if err = recordXID(xid); err != nil {
		return executionResult{Status: "failed", Message: "Could not persist transaction identity; SQL was not submitted"}
	}
	// Run the entire SQL script and check for errors.
	err = tx.Conn().PgConn().Exec(ctx, sql).Close()
	if err != nil {
		return executionResult{Status: "failed", Code: postgresCode(err), Message: "PostgreSQL rejected the migration; transaction rolled back"}
	}
	err = tx.Commit(ctx)
	if err == nil {
		return executionResult{Status: "succeeded"}
	}
	// A connection failure during COMMIT may leave the result uncertain.
	var pgerr *pgconn.PgError
	if errors.As(err, &pgerr) || errors.Is(err, pgx.ErrTxCommitRollback) {
		return executionResult{Status: "failed", Code: postgresCode(err), Message: "PostgreSQL rejected COMMIT; transaction rolled back"}
	}
	return executionResult{Status: "unknown", Message: "COMMIT acknowledgement was lost; reconciliation is required"}
}
func postgresCode(err error) string {
	var pgerr *pgconn.PgError
	if errors.As(err, &pgerr) {
		return pgerr.Code
	}
	return ""
}

func cleanupContext() (context.Context, context.CancelFunc) {
	return context.WithTimeout(context.Background(), 5*time.Second)
}
