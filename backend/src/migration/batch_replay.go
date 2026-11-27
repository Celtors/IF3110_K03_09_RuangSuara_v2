package migration

import (
	"context"
	"log/slog"
	"regexp"

	"backend/internal/telemetry"

	"github.com/jackc/pgx/v5"
)

// A completed batch's rollback and forward use the existing single-transaction
// operation path, binding the original immutable scope instead of all current rows.
type batchReplay struct {
	id        string
	statement batchStatement
	operation Operation
}

func completedBatchReplay(ctx context.Context, q rowQuerier, m Migration, op Operation) (*batchReplay, error) {
	var id, attribute, downHash string
	var size int
	err := q.QueryRow(ctx, `SELECT b.id,b.attribute,b.batch_size,b.down_hash FROM dmp_migration.batch_jobs b JOIN dmp_migration.change_requests r ON r.id=b.id WHERE r.migration_id=$1 AND r.database_id=$2 AND r.status='succeeded' AND b.state='completed'`, m.ID, m.DatabaseID).Scan(&id, &attribute, &size, &downHash)
	if isNoRows(err) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	spec := BatchSpec{attribute, size}
	b, e := parseBatch(rawSQL(m, op), spec)
	if e != nil || m.Irreversible || downHash == "" || sqlHash(m.SQLDown) != downHash || !inverseBatchSQL(m.SQLUp, m.SQLDown, spec) {
		return nil, fault("conflict", "Completed batch has no safe approved inverse; create a corrective migration")
	}
	return &batchReplay{id, b, op}, nil
}
func runBatchReplay(ctx context.Context, tx pgx.Tx, sql string, r batchReplay) (int64, error) {
	b := r.statement
	if err := configureBatchTx(ctx, tx, b); err != nil {
		return 0, err
	}
	before, after, expected, next := "after_row", "before_row", "applied", "rolled_back"
	originalXmin := "after_xmin"
	if r.operation == OperationForward {
		before, after, expected, next = "before_row", "after_row", "rolled_back", "applied"
		originalXmin = "before_xmin"
	}
	var total int64
	var valid bool
	err := tx.QueryRow(ctx, `SELECT committed_rows,relation_oid=$2::text::regclass AND done AND reverted=0 AND replay_state=$3 AND committed_rows=scope_count FROM dmp_batch.jobs WHERE id=$1 FOR UPDATE`, r.id, b.relation(), expected).Scan(&total, &valid)
	if err != nil {
		return 0, err
	}
	if !valid {
		return 0, fault("conflict", "Original batch scope is unavailable or in the wrong state")
	}
	join := ` FROM ` + b.relation() + ` t JOIN dmp_batch.scope s ON t.` + quote(b.cursor) + `=s.key WHERE s.job_id=$1 AND s.batch_no IS NOT NULL`
	var count int64
	if err = tx.QueryRow(ctx, `SELECT count(*)`+join+` AND jsonb_build_array(t.`+quote(b.column)+`)=s.`+before+` AND t.xmin::text=COALESCE(s.replay_xmin,s.`+originalXmin+`)`, r.id).Scan(&count); err != nil {
		return 0, err
	}
	if count != total {
		return 0, fault("conflict", "Recorded rows changed; scoped rollback/forward would be unsafe")
	}
	// parseBatch has already accepted the exact restricted grammar. Bind the
	// recorded scope server-side, avoiding a Go slice or an oversized SQL string.
	pos := regexp.MustCompile(`(?i)\s+WHERE\s+`).FindStringIndex(sql)
	if pos == nil {
		return 0, fault("invalid_sql", "Batch cursor predicate missing")
	}
	statement := sql[:pos[0]] + ` WHERE ` + quote(b.cursor) + ` IN (SELECT key FROM dmp_batch.scope WHERE job_id=$1 AND batch_no IS NOT NULL)`
	sqlCtx := telemetry.With(ctx, telemetry.ID("batch_scope_id", r.id), slog.Int64("batch_key_count", total))
	tag, err := tx.Exec(sqlCtx, statement, r.id)
	if err != nil {
		return 0, err
	}
	if tag.RowsAffected() != total {
		return 0, fault("conflict", "Replay row count differs from original scope")
	}
	if err = tx.QueryRow(ctx, `SELECT count(*)`+join+` AND jsonb_build_array(t.`+quote(b.column)+`)=s.`+after, r.id).Scan(&count); err != nil {
		return 0, err
	}
	if count != total {
		return 0, fault("conflict", "Replay did not produce the approved recorded values")
	}
	if _, err = tx.Exec(ctx, `UPDATE dmp_batch.scope s SET replay_xmin=t.xmin::text FROM `+b.relation()+` t WHERE s.job_id=$1 AND s.batch_no IS NOT NULL AND t.`+quote(b.cursor)+`=s.key`, r.id); err != nil {
		return 0, err
	}
	_, err = tx.Exec(ctx, `UPDATE dmp_batch.jobs SET replay_state=$2 WHERE id=$1`, r.id, next)
	return total, err
}
