package migration

import (
	"context"

	"github.com/jackc/pgx/v5"
)

// The platform mirror clears intent only after reading the locked target ledger.
// An unresolved checkpoint or a fully reverted batch cannot enter correction.
const verifiedPartial = `b.state='paused' AND b.intent='' AND b.scope_ready
 AND b.target_identity<>'' AND b.committed_rows>0 AND b.committed_batches>b.reverted_batches
 AND (b.down_hash='' OR b.compensation_unsafe) AND b.resolved_by IS NULL`

const eligibleCorrection = `SELECT 1 FROM dmp_migration.batch_jobs b
 JOIN dmp_migration.change_requests p ON p.id=b.id
 JOIN dmp_migration.attempts a ON a.id=p.attempt_id
 WHERE ` + verifiedPartial + `
 AND ((NOT b.correction_started AND p.status='running' AND a.status='running')
   OR (b.correction_started AND p.status='failed' AND a.status='failed'))`

// Under the same platform lock used by batch actions, move the reservation
// from the original running request to the batch record and claim its correction.
// The two existing unique running-request and unresolved-attempt indexes remain in force.
func transferCorrection(ctx context.Context, tx pgx.Tx, q QueuedRequest) (func(), error) {
	var locked bool
	if err := tx.QueryRow(ctx, `SELECT pg_try_advisory_xact_lock(hashtextextended($1,781349))`, q.DatabaseID).Scan(&locked); err != nil || !locked {
		return nil, err
	}
	var a Attempt
	var started bool
	err := tx.QueryRow(ctx, `SELECT p.migration_id,p.attempt_id,b.correction_started FROM dmp_migration.batch_jobs b
 JOIN dmp_migration.change_requests p ON p.id=b.id
 WHERE p.id=$1 AND p.database_id=$2 AND p.migration_id<>$3 AND EXISTS (`+eligibleCorrection+` AND p.id=$1)
 FOR UPDATE OF b,p`, *q.CorrectsBatchID, q.DatabaseID, q.MigrationID).Scan(&a.MigrationID, &a.ID, &started)
	if isNoRows(err) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	if started {
		return func() {}, nil
	}
	a.DatabaseID, a.Operation = q.DatabaseID, OperationApply
	const message = "Verified partial writes are retained for an approved corrective migration"
	attemptRecorded, err := recordAttemptOutcome(ctx, tx, &a, "batch", "failed", "", message)
	if err != nil {
		return nil, err
	}
	parentRecorded, err := recordRequestOutcome(ctx, tx, *q.CorrectsBatchID, RequestFailed, "partial_awaiting_correction", message, a.ID, nil)
	if err != nil || parentRecorded == nil {
		return nil, err
	}
	if _, err = tx.Exec(ctx, `UPDATE dmp_migration.batch_jobs SET correction_started=true,updated_at=now() WHERE id=$1`, *q.CorrectsBatchID); err != nil {
		return nil, err
	}
	return func() { attemptRecorded(); parentRecorded() }, nil
}

type correctionProof struct {
	id                   string
	identity             string
	batches, rows        int64
	cursor               *int64
	reverted, scopeCount int64
}

// Only a claimed, explicitly associated request can execute while a correction
// reservation is active. This also guards the direct execution entry point.
func correctionForExecution(ctx context.Context, conn rowQuerier, db, requestID string) (*correctionProof, error) {
	var proof correctionProof
	var permitted bool
	err := conn.QueryRow(ctx, `SELECT b.id,b.target_identity,b.committed_batches,b.committed_rows,
 b.cursor,b.reverted_batches,b.scope_count,
 (p.status='failed' AND `+verifiedPartial+` AND EXISTS(SELECT 1 FROM dmp_migration.change_requests c
 WHERE c.id=$2 AND c.corrects_batch_id=b.id AND c.status='running' AND c.database_id=$1 AND c.operation='apply'))
 FROM dmp_migration.batch_jobs b JOIN dmp_migration.change_requests p ON p.id=b.id
 WHERE p.database_id=$1 AND b.correction_started AND b.resolved_by IS NULL`, db, requestID).
		Scan(&proof.id, &proof.identity, &proof.batches, &proof.rows, &proof.cursor, &proof.reverted, &proof.scopeCount, &permitted)
	if isNoRows(err) && requestID == "" {
		return nil, nil
	}
	if (isNoRows(err) && requestID != "") || (err == nil && !permitted) {
		return nil, fault("conflict", "This database is reserved for an approved corrective migration")
	}
	if err != nil {
		return nil, err
	}
	return &proof, nil
}

func (p correctionProof) matches(ctx context.Context, tx pgx.Tx) bool {
	var same bool
	err := tx.QueryRow(ctx, `SELECT completed=$2 AND committed_rows=$3 AND cursor IS NOT DISTINCT FROM $4::bigint
 AND reverted=$5 AND scope_count=$6 AND NOT done FROM dmp_batch.jobs WHERE id=$1 FOR UPDATE`,
		p.id, p.batches, p.rows, p.cursor, p.reverted, p.scopeCount).Scan(&same)
	return err == nil && same
}
