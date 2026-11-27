package migration

import (
	"context"
	"fmt"
	"log/slog"
	"math/big"
	"regexp"
	"strings"
	"time"

	"backend/internal/targetdb"
	"backend/internal/telemetry"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/trace"
)

// Batch SQL is deliberately narrow: one integer-column UPDATE with an explicit
// cursor parameter. No automatic rewriting of arbitrary migration SQL.
type BatchSpec struct {
	Attribute string `json:"batch_attribute"`
	Size      int    `json:"batch_size"`
}
type batchStatement struct{ schema, table, column, cursor string }

var batchPattern = regexp.MustCompile(`(?i)^\s*UPDATE\s+([a-z_][a-z0-9_]*)\.([a-z_][a-z0-9_]*)\s+SET\s+([a-z_][a-z0-9_]*)\s*=\s*(?:([a-z_][a-z0-9_]*)\s*([+-])\s*)?(-?[0-9]+)\s+WHERE\s+([a-z_][a-z0-9_]*)\s*=\s*ANY\s*\(\s*\$1\s*::\s*bigint\s*\[\s*\]\s*\)\s*;?\s*$`)

func parseBatch(sql string, spec BatchSpec) (batchStatement, error) {
	bad := fault("invalid_input", "Batch requires UPDATE schema.table SET integer_column = integer_column +/- integer (or integer literal) WHERE primary_key = ANY($1::bigint[]); batch size 1..10000")
	if spec.Size < 1 || spec.Size > 10000 || strings.Contains(sql, "--") {
		return batchStatement{}, bad
	}
	m := batchPattern.FindStringSubmatch(strings.ToLower(sql))
	if m == nil || len(m[1]) > 63 || len(m[2]) > 63 || len(m[3]) > 63 || len(m[7]) > 63 || m[7] != spec.Attribute || m[3] == m[7] || (m[4] != "" && m[4] != m[3]) || strings.HasPrefix(m[1], "pg_") || m[1] == "information_schema" || m[1] == "dmp_batch" || m[1] == "dmp_migration" {
		return batchStatement{}, bad
	}
	if _, err := parseSQL(sql, false); err != nil {
		return batchStatement{}, err
	}
	return batchStatement{m[1], m[2], m[3], m[7]}, nil
}

// Only an exact inverse integer adjustment is eligible for compensation.
func inverseBatchSQL(up, down string, spec BatchSpec) bool {
	a, e := parseBatch(up, spec)
	if e != nil {
		return false
	}
	b, e := parseBatch(down, spec)
	if e != nil || a != b {
		return false
	}
	delta := func(sql string) *big.Int {
		m := batchPattern.FindStringSubmatch(strings.ToLower(sql))
		if m == nil || m[4] == "" {
			return nil
		}
		v, ok := new(big.Int).SetString(m[6], 10)
		if !ok {
			return nil
		}
		if m[5] == "-" {
			v.Neg(v)
		}
		return v
	}
	x, y := delta(up), delta(down)
	return x != nil && y != nil && new(big.Int).Add(x, y).Sign() == 0
}
func (b batchStatement) relation() string { return pgx.Identifier{b.schema, b.table}.Sanitize() }

// Reject relations with effects outside the recorded rows (including rules,
// triggers, inheritance, RLS, generated columns and foreign keys).
func (b batchStatement) validate(ctx context.Context, q interface {
	QueryRow(context.Context, string, ...any) pgx.Row
}) error {
	var ok bool
	err := q.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM pg_catalog.pg_class c JOIN pg_catalog.pg_namespace n ON n.oid=c.relnamespace
 JOIN pg_catalog.pg_attribute k ON k.attrelid=c.oid AND k.attname=$3 AND NOT k.attisdropped
 JOIN pg_catalog.pg_attribute v ON v.attrelid=c.oid AND v.attname=$4 AND NOT v.attisdropped
 WHERE n.nspname=$1 AND c.relname=$2 AND c.relkind='r' AND c.relpersistence='p' AND NOT c.relrowsecurity
 AND k.atttypid IN (20,21,23) AND v.atttypid IN (20,21,23) AND k.attidentity='a'
 AND EXISTS(SELECT 1 FROM pg_catalog.pg_index i WHERE i.indrelid=c.oid AND i.indisprimary AND i.indisvalid AND i.indnkeyatts=1 AND i.indkey[0]=k.attnum)
 AND NOT EXISTS(SELECT 1 FROM pg_catalog.pg_trigger t WHERE t.tgrelid=c.oid)
 AND NOT EXISTS(SELECT 1 FROM pg_catalog.pg_rewrite r WHERE r.ev_class=c.oid)
 AND NOT EXISTS(SELECT 1 FROM pg_catalog.pg_inherits i WHERE i.inhrelid=c.oid OR i.inhparent=c.oid)
 AND NOT EXISTS(SELECT 1 FROM pg_catalog.pg_attribute a WHERE a.attrelid=c.oid AND a.attgenerated<>'')
 AND NOT EXISTS(SELECT 1 FROM pg_catalog.pg_constraint f WHERE f.contype='f' AND (f.conrelid=c.oid OR f.confrelid=c.oid)))`, b.schema, b.table, b.cursor, b.column).Scan(&ok)
	if err != nil {
		return err
	}
	if !ok {
		return fault("invalid_input", "Batch requires a permanent ordinary table with a single integer GENERATED ALWAYS identity primary key, integer update column, and no triggers, rules, inheritance, RLS, generated columns or foreign keys")
	}
	return nil
}

// The target owns the authoritative checkpoint and row images. The platform
// stores intent before the target transaction, then mirrors verified progress.
const targetBatchDDL = `CREATE SCHEMA IF NOT EXISTS dmp_batch;
CREATE TABLE IF NOT EXISTS dmp_batch.jobs(id text PRIMARY KEY, relation_oid oid NOT NULL, completed bigint NOT NULL DEFAULT 0, committed_rows bigint NOT NULL DEFAULT 0, cursor bigint, reverted bigint NOT NULL DEFAULT 0, scope_count bigint NOT NULL DEFAULT 0, replay_state text NOT NULL DEFAULT 'applied', done boolean NOT NULL DEFAULT false);
CREATE TABLE IF NOT EXISTS dmp_batch.scope(job_id text NOT NULL REFERENCES dmp_batch.jobs(id), key bigint NOT NULL, before_row jsonb NOT NULL, before_xmin text NOT NULL, after_row jsonb, after_xmin text, replay_xmin text, batch_no bigint, PRIMARY KEY(job_id,key));
CREATE INDEX IF NOT EXISTS scope_batch ON dmp_batch.scope(job_id,batch_no,key);`

type batchProgress struct {
	State      string    `json:"status"`
	Attribute  string    `json:"batch_attribute"`
	Size       int       `json:"batch_size"`
	DownHash   string    `json:"-"`
	Identity   string    `json:"-"`
	Batches    int64     `json:"committed_batches"`
	Rows       int64     `json:"committed_rows"`
	Cursor     *int64    `json:"checkpoint"`
	Reverted   int64     `json:"reverted_batches"`
	Intent     string    `json:"intent,omitempty"`
	Action     string    `json:"-"`
	Error      string    `json:"error,omitempty"`
	Done       bool      `json:"-"`
	ScopeReady bool      `json:"-"`
	ScopeCount int64     `json:"scope_rows"`
	Updated    time.Time `json:"updated_at"`
}

// Batches fill Size except for the last one, and compensation walks backward.
func (p batchProgress) compensatedRows() int64 {
	if p.Reverted == 0 {
		return 0
	}
	return p.Rows - (p.Batches-p.Reverted)*int64(p.Size)
}

const batchColumns = `state,attribute,batch_size,down_hash,target_identity,committed_batches,committed_rows,cursor,reverted_batches,intent,action,error_message,scope_ready,scope_count,updated_at`

func loadBatch(ctx context.Context, q interface {
	QueryRow(context.Context, string, ...any) pgx.Row
}, id string) (p batchProgress, err error) {
	err = q.QueryRow(ctx, `SELECT `+batchColumns+` FROM dmp_migration.batch_jobs WHERE id=$1`, id).Scan(&p.State, &p.Attribute, &p.Size, &p.DownHash, &p.Identity, &p.Batches, &p.Rows, &p.Cursor, &p.Reverted, &p.Intent, &p.Action, &p.Error, &p.ScopeReady, &p.ScopeCount, &p.Updated)
	return
}
func mirrorBatch(ctx context.Context, c *pgxpool.Conn, id string, p batchProgress, state, message string) error {
	_, err := c.Exec(ctx, `UPDATE dmp_migration.batch_jobs SET state=$2,committed_batches=$3,committed_rows=$4,cursor=$5,reverted_batches=$6,intent='',error_message=$7,scope_ready=$8,scope_count=$9,updated_at=now() WHERE id=$1`, id, state, p.Batches, p.Rows, p.Cursor, p.Reverted, message, p.ScopeReady, p.ScopeCount)
	return err
}
func batchIntent(ctx context.Context, c *pgxpool.Conn, id, intent string) error {
	_, err := c.Exec(ctx, `UPDATE dmp_migration.batch_jobs SET intent=$2,updated_at=now() WHERE id=$1`, id, intent)
	return err
}
func targetProgress(ctx context.Context, c *pgxpool.Conn, id string, p *batchProgress) error {
	progress := *p
	err := c.QueryRow(ctx, `SELECT completed,committed_rows,cursor,reverted,done,scope_count FROM dmp_batch.jobs WHERE id=$1`, id).
		Scan(&progress.Batches, &progress.Rows, &progress.Cursor, &progress.Reverted, &progress.Done, &progress.ScopeCount)
	if err == nil {
		progress.ScopeReady = true
		*p = progress
	}
	return err
}

func batchCheckpoint(ctx context.Context, p batchProgress, kind, status string, started time.Time) {
	elapsed := time.Since(started).Milliseconds()
	trace.SpanFromContext(ctx).AddEvent("batch.checkpoint_verified", trace.WithAttributes(
		attribute.String("checkpoint_kind", kind), attribute.String("outcome", status),
		attribute.Int64("committed_batches", p.Batches), attribute.Int64("committed_rows", p.Rows),
		attribute.Int64("reverted_batches", p.Reverted), attribute.Int64("duration_ms", elapsed)))
	slog.InfoContext(ctx, "batch.checkpoint_verified", "checkpoint_kind", kind, "status", status,
		"committed_batches", p.Batches, "committed_rows", p.Rows, "reverted_batches", p.Reverted,
		"duration_ms", elapsed)
}
func configureBatchTx(ctx context.Context, tx pgx.Tx, b batchStatement) error {
	if _, err := tx.Exec(ctx, `SET LOCAL statement_timeout='30s'; SET LOCAL lock_timeout='5s'; SET LOCAL search_path=pg_catalog`); err != nil {
		return err
	}
	if _, err := tx.Exec(ctx, `LOCK TABLE `+b.relation()+` IN SHARE ROW EXCLUSIVE MODE`); err != nil {
		return err
	}
	return b.validate(ctx, tx)
}
func initBatchScope(ctx context.Context, c *pgxpool.Conn, id string, b batchStatement) error {
	tx, err := c.Begin(ctx)
	if err != nil {
		return err
	}
	defer rollback(ctx, tx)
	if err = configureBatchTx(ctx, tx, b); err != nil {
		return err
	}
	if _, err = tx.Exec(ctx, targetBatchDDL); err != nil {
		return err
	}
	if _, err = tx.Exec(ctx, `INSERT INTO dmp_batch.jobs(id,relation_oid) VALUES($1,$2::text::regclass)`, id, b.relation()); err != nil {
		return err
	}
	tag, err := tx.Exec(ctx, `INSERT INTO dmp_batch.scope(job_id,key,before_row,before_xmin) SELECT $1,t.`+quote(b.cursor)+`,jsonb_build_array(t.`+quote(b.column)+`),t.xmin::text FROM `+b.relation()+` t`, id)
	if err != nil {
		return err
	}
	if _, err = tx.Exec(ctx, `UPDATE dmp_batch.jobs SET scope_count=$2 WHERE id=$1`, id, tag.RowsAffected()); err != nil {
		return err
	}
	return tx.Commit(ctx)
}

// Run one transaction, including the durable checkpoint. Only the selected keys
// enter Go memory. Only the changed column image and row version are kept in PostgreSQL for conflict detection.
func runBatchTx(ctx context.Context, c *pgxpool.Conn, id, sql string, b batchStatement, p batchProgress, undo bool) (resultErr error) {
	ctx, span := telemetry.Start(ctx, "batch.transaction")
	defer telemetry.EndSpan(span)
	outcome := "failed"
	defer func() {
		span.SetAttributes(attribute.String("outcome", outcome))
		traceMigrationError(span, resultErr)
	}()
	tx, err := c.Begin(ctx)
	if err != nil {
		return err
	}
	defer rollback(ctx, tx)
	if err = configureBatchTx(ctx, tx, b); err != nil {
		return err
	}
	var same bool
	if err = tx.QueryRow(ctx, `SELECT relation_oid=$2::text::regclass AND completed=$3 AND reverted=$4 AND cursor IS NOT DISTINCT FROM $5::bigint AND (NOT done OR $6::boolean) FROM dmp_batch.jobs WHERE id=$1 FOR UPDATE`, id, b.relation(), p.Batches, p.Reverted, p.Cursor, undo).Scan(&same); err != nil {
		return err
	}
	if !same {
		return fault("conflict", "Batch target or checkpoint changed; reload verified evidence")
	}
	var keys []int64
	var rows pgx.Rows
	if undo {
		rows, err = tx.Query(ctx, `SELECT key FROM dmp_batch.scope WHERE job_id=$1 AND batch_no=$2 ORDER BY key`, id, p.Batches-p.Reverted)
	} else if p.Cursor == nil {
		rows, err = tx.Query(ctx, `SELECT key FROM dmp_batch.scope WHERE job_id=$1 ORDER BY key LIMIT $2`, id, p.Size)
	} else {
		rows, err = tx.Query(ctx, `SELECT key FROM dmp_batch.scope WHERE job_id=$1 AND key>$2 ORDER BY key LIMIT $3`, id, p.Cursor, p.Size)
	}
	if err != nil {
		return err
	}
	for rows.Next() {
		var k int64
		if err = rows.Scan(&k); err != nil {
			rows.Close()
			return err
		}
		keys = append(keys, k)
	}
	rows.Close()
	if err = rows.Err(); err != nil {
		return err
	}
	if undo {
		expected := min(int64(p.Size), p.Rows-(p.Batches-p.Reverted-1)*int64(p.Size))
		if int64(len(keys)) != expected || expected <= 0 {
			return fault("indeterminate", "Compensation scope is incomplete")
		}
	}
	if len(keys) == 0 {
		if p.Rows != p.ScopeCount {
			return fault("indeterminate", "Recorded target scope is incomplete")
		}
		if undo {
			return fault("conflict", "Missing recorded compensation scope")
		}
		_, err = tx.Exec(ctx, `UPDATE dmp_batch.jobs SET done=true WHERE id=$1`, id)
		if err != nil {
			return err
		}
	} else {
		join := ` FROM ` + b.relation() + ` t JOIN dmp_batch.scope s ON t.` + quote(b.cursor) + `=s.key WHERE s.job_id=$1 AND s.key=ANY($2::bigint[])`
		image, xmin := "before_row", "before_xmin"
		if undo {
			image, xmin = "after_row", "after_xmin"
		}
		var count int64
		if err = tx.QueryRow(ctx, `SELECT count(*)`+join+` AND jsonb_build_array(t.`+quote(b.column)+`)=s.`+image+` AND t.xmin::text=s.`+xmin, id, keys).Scan(&count); err != nil {
			return err
		}
		if count != int64(len(keys)) {
			return fault("conflict", "Original rows changed or disappeared; batch cannot safely proceed")
		}
		tag, err := tx.Exec(ctx, sql, keys)
		if err != nil {
			return err
		}
		span.SetAttributes(attribute.Int64("rows_affected", tag.RowsAffected()))
		if tag.RowsAffected() != int64(len(keys)) {
			return fault("conflict", "Batch affected unexpected number of rows")
		}
		if undo {
			if err = tx.QueryRow(ctx, `SELECT count(*)`+join+` AND jsonb_build_array(t.`+quote(b.column)+`)=s.before_row`, id, keys).Scan(&count); err != nil {
				return err
			}
			if count != int64(len(keys)) {
				return fault("conflict", "Approved down SQL did not restore the original rows")
			}
			_, err = tx.Exec(ctx, `UPDATE dmp_batch.jobs SET reverted=reverted+1 WHERE id=$1`, id)
		} else {
			_, err = tx.Exec(ctx, `UPDATE dmp_batch.scope s SET batch_no=$3,after_row=jsonb_build_array(t.`+quote(b.column)+`),after_xmin=t.xmin::text FROM `+b.relation()+` t WHERE s.job_id=$1 AND s.key=ANY($2::bigint[]) AND t.`+quote(b.cursor)+`=s.key`, id, keys, p.Batches+1)
			if err == nil {
				_, err = tx.Exec(ctx, `UPDATE dmp_batch.jobs SET completed=completed+1,committed_rows=committed_rows+$2,cursor=$3 WHERE id=$1`, id, len(keys), keys[len(keys)-1])
			}
		}
		if err != nil {
			return err
		}
	}
	span.AddEvent("checkpoint_written")
	err = tx.Commit(ctx)
	outcome = telemetry.CommitOutcome(err)
	return err
}
func retryBatchTransaction(ctx context.Context, run func() error, verified func() bool, delay func(time.Duration)) error {
	var err error
	for tries := 0; tries < 3; tries++ {
		if tries > 0 {
			telemetry.RecordRetry("batch_transaction")
		}
		err = run()
		if err == nil || !transientBatch(err) || tries == 2 {
			return err
		}
		if !verified() {
			return fault("indeterminate", "Checkpoint requires reconciliation before retry")
		}
		backoff := time.Duration(tries+1) * 250 * time.Millisecond
		slog.LogAttrs(ctx, slog.LevelWarn, "batch.retry", append(telemetry.ErrorAttrs(err),
			slog.Int("retry_number", tries+1), slog.Int64("delay_ms", backoff.Milliseconds()))...)
		delay(backoff)
	}
	return err
}
func transientBatch(err error) bool {
	switch postgresCode(err) {
	case "40001", "40P01", "55P03":
		return true
	}
	return false
}

// Both locks are session locks. The platform request stays running even when
// paused, so a process crash does not free the logical database reservation.
// The target lock must be acquired before trusting a missing checkpoint.
func (r *Review) processBatch(ctx context.Context, q QueuedRequest, dispatched batchProgress, lockWaits *batchLockWaits) (outcome string, err error) {
	ctx, span := telemetry.Start(ctx, "batch.execute")
	defer telemetry.EndSpan(span)
	start := time.Now()
	var p batchProgress
	defer func() {
		span.SetAttributes(attribute.String("outcome", outcome),
			attribute.Int64("committed_batches", p.Batches), attribute.Int64("committed_rows", p.Rows),
			attribute.Int64("reverted_batches", p.Reverted))
		traceMigrationError(span, err)
		if outcome == "stale" || outcome == "lock_unavailable" {
			return
		}
		level := slog.LevelInfo
		if err != nil || outcome == "failed" || outcome == "paused" || outcome == "partially_reverted" || outcome == "unresolved" {
			level = slog.LevelWarn
		}
		slog.LogAttrs(ctx, level, "batch.execution", append(telemetry.ErrorAttrs(err),
			slog.String("status", outcome), slog.Int64("duration_ms", time.Since(start).Milliseconds()),
			slog.Int64("committed_batches", p.Batches), slog.Int64("committed_rows", p.Rows),
			slog.Int64("reverted_batches", p.Reverted))...)
	}()

	c, release, err := r.repo().lock(ctx, q.DatabaseID)
	platformProbedAt := time.Now()
	if err != nil {
		if err == errPlatformLockBusy && dispatched.acceptsLockTiming() {
			lockWaits.probe(q, dispatched, "platform", "", platformProbedAt, false)
		}
		return "lock_unavailable", err
	}
	defer release()
	p, err = loadBatch(ctx, c, q.ID)
	if err != nil {
		return "record_failed", err
	}
	var status string
	var attemptID *string
	var startedAt *time.Time
	if err = c.QueryRow(ctx, `SELECT status,attempt_id,started_at FROM dmp_migration.change_requests WHERE id=$1`,
		q.ID).Scan(&status, &attemptID, &startedAt); err != nil {
		return "record_failed", err
	}
	if status != "running" || startedAt == nil || !startedAt.Equal(q.startedAt) || p.State == "paused" {
		return "stale", nil
	}
	measureLockWait := p.acceptsLockTiming()
	if measureLockWait {
		lockWaits.probe(q, p, "platform", "", platformProbedAt, true)
	}
	ctx = telemetry.With(ctx, slog.String("batch_action", p.Action))
	span.SetAttributes(attribute.String("batch_action", p.Action))
	if attemptID != nil {
		ctx = attemptContext(ctx, Attempt{ID: *attemptID})
		span.SetAttributes(attribute.String("attempt_id", *attemptID))
	}
	slog.InfoContext(ctx, "batch.started", "status", p.State, "reloaded", q.reloaded)
	// Any bookkeeping error after an intent leaves the durable request locked.
	pause := func(state string, e error) (string, error) {
		if state == "paused" && p.Intent != "" {
			state = "unresolved"
		}
		code, msg := faultInfo(e)
		if code == "internal_error" {
			msg = "Batch transaction or bookkeeping failed"
		}
		if _, writeErr := c.Exec(ctx, `UPDATE dmp_migration.batch_jobs SET state=$2,error_message=$3,updated_at=now() WHERE id=$1`,
			q.ID, state, msg); writeErr != nil {
			return "record_failed", writeErr
		}
		return state, e
	}
	m, err := r.repo().Get(ctx, q.DatabaseID, q.MigrationID)
	if err != nil {
		return pause("unresolved", err)
	}
	b, err := parseBatch(m.SQLUp, BatchSpec{p.Attribute, p.Size})
	if err != nil {
		return pause("paused", err)
	}
	undo := p.Action == "compensate"
	sql := m.SQLUp
	if undo {
		sql = m.SQLDown
		down, e := parseBatch(sql, BatchSpec{p.Attribute, p.Size})
		if e != nil || down != b || m.Irreversible || sqlHash(sql) != p.DownHash || !inverseBatchSQL(m.SQLUp, sql, BatchSpec{p.Attribute, p.Size}) {
			return pause("paused", fault("conflict", "No approved compatible down SQL"))
		}
	}
	pool, err := r.Service.connection(ctx, q.DatabaseID)
	if err != nil {
		return pause("unresolved", err)
	}
	target, err := pool.Acquire(ctx)
	if err != nil {
		return pause("unresolved", err)
	}
	defer target.Release()
	var locked bool
	probeStart := time.Now()
	err = target.QueryRow(ctx, `SELECT pg_try_advisory_lock(781349,3)`).Scan(&locked)
	targetProbedAt := time.Now()
	telemetry.ObserveLockProbe("target", targetProbedAt.Sub(probeStart), locked, err)
	if err != nil || !locked {
		if err != nil {
			_ = target.Conn().Close(ctx)
		} else if measureLockWait {
			// Confirm the physical database on this connection before timing across passes.
			probeCtx, cancel := context.WithTimeout(telemetry.WithoutQueryTelemetry(ctx), 100*time.Millisecond)
			identity, identityErr := targetIdentity(probeCtx, target.Conn())
			cancel()
			if identityErr == nil && (p.Identity == "" || p.Identity == identity) {
				lockWaits.probe(q, p, "target", identity, targetProbedAt, false)
			}
		}
		return "lock_unavailable", err
	}
	defer func() {
		clean, cancel := cleanupContext(ctx)
		defer cancel()
		var ok bool
		if e := target.QueryRow(clean, `SELECT pg_advisory_unlock(781349,3)`).Scan(&ok); e != nil || !ok {
			_ = target.Conn().Close(clean)
		}
	}()
	fingerprint, err := checkTarget(ctx, target, c)
	if err != nil {
		return pause("unresolved", err)
	}
	identity := targetdb.RecoveryIdentity(target.Conn().Config(), fingerprint)
	if p.Identity != "" && p.Identity != identity {
		return pause("unresolved", fault("indeterminate", "Target identity changed"))
	}
	if measureLockWait {
		lockWaits.probe(q, p, "target", identity, targetProbedAt, true)
	}
	if p.Identity == "" {
		if _, err = c.Exec(ctx, `UPDATE dmp_migration.batch_jobs SET target_identity=$2 WHERE id=$1`, q.ID, identity); err != nil {
			return "record_failed", err
		}
	}
	// Create the ordinary attempt before touching the target; recover a crash
	// between beginAttempt and linking by using the unique running attempt.
	var a Attempt
	if attemptID == nil {
		var old string
		err = c.QueryRow(ctx, `SELECT id FROM dmp_migration.attempts
 WHERE database_id=$1 AND migration_id=$2 AND operation='apply' AND status='running' AND started_at >= $3`,
			q.DatabaseID, q.MigrationID, q.startedAt).Scan(&old)
		if isNoRows(err) {
			if e := r.authorizeExecution(ctx, q); e != nil {
				code, message := faultInfo(e)
				if code == "authorization_unavailable" {
					return "authorization_unavailable", e
				}
				if err = mirrorBatch(ctx, c, q.ID, p, "failed", message); err != nil {
					return "record_failed", err
				}
				if err = r.repo().finishRequest(ctx, q.ID, RequestFailed, code, message, "", q.startedAt); err != nil {
					return "record_failed", err
				}
				return "failed", e
			}
			if _, _, err = prepareExecution(ctx, c, m, OperationApply); err != nil {
				_, message := faultInfo(err)
				if e := mirrorBatch(ctx, c, q.ID, p, "failed", message); e != nil {
					return "record_failed", e
				}
				if e := r.repo().finishRequest(ctx, q.ID, RequestFailed, "execution_failed", message, "", q.startedAt); e != nil {
					return "record_failed", e
				}
				return "failed", err
			}
			a, err = beginAttempt(ctx, c, m, q.RequestedBy, OperationApply, m.SQLUp, nil)
			old = a.ID
		}
		if err != nil {
			return pause("unresolved", err)
		}
		attemptID = &old
		if _, err = c.Exec(ctx, `UPDATE dmp_migration.change_requests SET attempt_id=$2 WHERE id=$1`, q.ID, old); err != nil {
			return "record_failed", err
		}
		ctx = attemptContext(ctx, Attempt{ID: old, migrationVersion: a.migrationVersion})
		span.SetAttributes(attribute.String("attempt_id", old))
		slog.InfoContext(ctx, "migration.attempt_started", "status", "running")
		if a.ID != "" && m.State == "failed" {
			telemetry.RecordRetry("failed_apply")
		}
	}
	a = Attempt{ID: *attemptID, MigrationID: q.MigrationID, DatabaseID: q.DatabaseID, Operation: OperationApply}
	if err = c.QueryRow(ctx, `SELECT status,postgres_error_code,error_message FROM dmp_migration.attempts WHERE id=$1`, a.ID).
		Scan(&a.Status, &a.ErrorCode, &a.Error); err != nil {
		return "record_failed", err
	}
	// If platform finalization committed before the request status was saved.
	if a.Status == "succeeded" || a.Status == "failed" {
		final := RequestSucceeded
		state := "completed"
		code := ""
		if a.Status == "failed" {
			final = RequestFailed
			state = "reverted"
			if !undo {
				state = "failed"
				code = "execution_failed"
			}
		}
		if err = mirrorBatch(ctx, c, q.ID, p, state, a.Error); err != nil {
			return "record_failed", err
		}
		if err = r.repo().finishRequest(ctx, q.ID, final, code, a.Error, a.ID, q.startedAt); err != nil {
			return "record_failed", err
		}
		return state, nil
	}
	if q.reloaded && p.State != "ready" {
		telemetry.RecordRetry("batch_reentry")
	}
	finish := func(state string, cause error, message string) (string, error) {
		result, attemptStatus, code := RequestFailed, "failed", "execution_failed"
		if state == "completed" {
			result, attemptStatus, code = RequestSucceeded, "succeeded", ""
			if _, e := c.Exec(ctx, `UPDATE dmp_migration.attempts SET prepared_rows=$2 WHERE id=$1`, a.ID, p.Rows); e != nil {
				return "record_failed", e
			}
		} else if state == "reverted" {
			code = ""
		}
		if e := finishAttempt(ctx, c, &a, "batch", attemptStatus, postgresCode(cause), message); e != nil {
			return "record_failed", e
		}
		if e := mirrorBatch(ctx, c, q.ID, p, state, message); e != nil {
			return "record_failed", e
		}
		if e := r.repo().finishRequest(ctx, q.ID, result, code, message, a.ID, q.startedAt); e != nil {
			return "record_failed", e
		}
		return state, cause
	}
	if _, err = c.Exec(ctx, `UPDATE dmp_migration.attempts SET target_identity=$2 WHERE id=$1`, a.ID, identity); err != nil {
		return "record_failed", err
	}
	// Reconcile before authorization: reading evidence cannot execute new SQL.
	verifiedBefore := p
	recoveryStart := time.Now()
	err = targetProgress(ctx, target, q.ID, &p)
	if err == nil && (p.Batches < verifiedBefore.Batches || p.Rows < verifiedBefore.Rows || p.Reverted < verifiedBefore.Reverted || (verifiedBefore.ScopeReady && p.ScopeCount != verifiedBefore.ScopeCount)) {
		p = verifiedBefore
		return pause("unresolved", fault("indeterminate", "Target checkpoint regressed"))
	}
	if isNoRows(err) || postgresCode(err) == "42P01" {
		if verifiedBefore.ScopeReady || verifiedBefore.Batches > 0 || (p.Intent != "" && p.Intent != "scope") {
			return pause("unresolved", fault("indeterminate", "Target checkpoint is missing"))
		}
		if e := r.authorizeExecution(ctx, q); e != nil {
			code, message := faultInfo(e)
			if code == "authorization_unavailable" {
				return pause("unresolved", e)
			}
			// A missing checkpoint under the target lock settles any prior scope intent.
			if writeErr := mirrorBatch(ctx, c, q.ID, p, "paused", message); writeErr != nil {
				return "record_failed", writeErr
			}
			return "paused", e
		}
		if err = batchIntent(ctx, c, q.ID, "scope"); err != nil {
			return "record_failed", err
		}
		p.Intent = "scope"
		scopeStart := time.Now()
		initErr := initBatchScope(ctx, target, q.ID, b)
		err = targetProgress(ctx, target, q.ID, &p)
		if initErr != nil && (isNoRows(err) || postgresCode(err) == "42P01") {
			return finish("failed", initErr, "Scope creation failed before data execution")
		}
		if err == nil {
			state := "committed"
			if initErr != nil {
				state = "recovered_commit"
			}
			batchCheckpoint(ctx, p, "scope", state, scopeStart)
		}
	} else if err == nil && (p.Intent != "" || q.reloaded) {
		state := "reconciled"
		if p.Batches > verifiedBefore.Batches || p.Reverted > verifiedBefore.Reverted || !verifiedBefore.ScopeReady {
			state = "recovered_commit"
		}
		batchCheckpoint(ctx, p, "reconciliation", state, recoveryStart)
	}
	if err != nil {
		return pause("unresolved", err)
	}
	lastMirrored := verifiedBefore
	for {
		state := "running"
		if p.Batches > 0 {
			state = "partial"
		}
		if undo {
			state = "compensating"
		}
		if err = mirrorBatch(ctx, c, q.ID, p, state, ""); err != nil {
			return "record_failed", err
		}
		// Record new target-verified work only after the platform mirror confirms
		// it. A reload compares against that mirror, including after an uncertain commit.
		telemetry.RecordBatchProgress("apply", p.Batches-lastMirrored.Batches, p.Rows-lastMirrored.Rows)
		telemetry.RecordBatchProgress("compensate", p.Reverted-lastMirrored.Reverted, p.compensatedRows()-lastMirrored.compensatedRows())
		lastMirrored = p
		p.Intent = ""
		if (!undo && p.Done) || (undo && p.Reverted == p.Batches) {
			if undo {
				return finish("reverted", nil, "Committed batches compensated")
			}
			return finish("completed", nil, "")
		}
		number := p.Batches + 1
		if undo {
			number = p.Batches - p.Reverted
		}
		batchCtx := telemetry.With(ctx, slog.Int64("batch_number", number))
		intent := fmt.Sprintf("%s:%d", p.Action, number)
		if err = batchIntent(batchCtx, c, q.ID, intent); err != nil {
			return "record_failed", err
		}
		p.Intent = intent
		batchStart := time.Now()
		var authorizationErr error
		err = retryBatchTransaction(batchCtx, func() error {
			authorizationErr = r.authorizeExecution(batchCtx, q)
			if authorizationErr != nil {
				return authorizationErr
			}
			return runBatchTx(batchCtx, target, q.ID, sql, b, p, undo)
		}, func() bool {
			var verified batchProgress
			return targetProgress(batchCtx, target, q.ID, &verified) == nil &&
				verified.Batches == p.Batches && verified.Rows == p.Rows && verified.Reverted == p.Reverted && verified.Done == p.Done
		}, time.Sleep)
		previous := p
		verifyErr := targetProgress(batchCtx, target, q.ID, &p)
		if verifyErr != nil {
			return pause("unresolved", verifyErr)
		}
		kind := "batch"
		if p.Done && !previous.Done {
			kind = "completion"
		}
		if err != nil {
			// A live connection with a verified unchanged checkpoint proves no commit;
			// if the connection was lost leave intent intact for the next recovery.
			if p.Batches != previous.Batches || p.Reverted != previous.Reverted || p.Done != previous.Done {
				batchCheckpoint(batchCtx, p, kind, "recovered_commit", batchStart)
				continue
			}
			code, message := faultInfo(err)
			if code == "indeterminate" || code == "authorization_unavailable" {
				return pause("unresolved", err)
			}
			if code == "internal_error" {
				message = "Batch failed (SQLSTATE " + postgresCode(err) + "); resume or compensate after correcting the cause"
			}
			if authorizationErr == nil && p.Batches == 0 && !undo {
				return finish("failed", err, message)
			}
			if e := mirrorBatch(ctx, c, q.ID, p, "paused", message); e != nil {
				return "record_failed", e
			}
			if undo && p.Reverted > 0 {
				return "partially_reverted", err
			}
			return "paused", err
		}
		batchCheckpoint(batchCtx, p, kind, "committed", batchStart)
	}
}

// Running requests themselves are durable reservations. Recover them directly,
// including after restart, without relying on an in-memory lease or clock.
func (r *Review) nextBatch(ctx context.Context, attempted map[string]bool) (*QueuedRequest, error) {
	rows, err := r.repo().Pool.Query(ctx, `SELECT r.id,r.database_id,r.migration_id,r.operation,r.sql_hash,r.requested_by,
 r.submission_correlation_id,r.traceparent,r.tracestate,r.started_at
 FROM dmp_migration.change_requests r JOIN dmp_migration.batch_jobs b ON b.id=r.id
 WHERE r.status='running' AND b.state<>'paused' ORDER BY r.started_at`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	for rows.Next() {
		var q QueuedRequest
		if err = rows.Scan(&q.ID, &q.DatabaseID, &q.MigrationID, &q.Operation, &q.SQLHash, &q.RequestedBy,
			&q.submissionCorrelationID, &q.queueTrace.TraceParent, &q.queueTrace.TraceState, &q.startedAt); err != nil {
			return nil, err
		}
		if _, live := r.inflight.Load(q.ID); !live && !attempted[q.ID] {
			q.reloaded = true
			return &q, nil
		}
	}
	return nil, rows.Err()
}

// Reuse the original immutable approval (including down hash), and require the
// original requester to retain edit permission. Never resume a compensation
// as an apply after any reversal has begun.
func (r *Review) batchAction(ctx context.Context, user, db, id, action string) error {
	db = canonicalDatabaseID(db)
	ctx = telemetry.With(migrationContext(ctx, user, db, ""), telemetry.ID("job_id", id), slog.String("batch_action", action))
	if err := r.Service.authorize(ctx, user, db, Edit); err != nil {
		return err
	}
	c, release, err := r.repo().lock(ctx, db)
	if err != nil {
		return err
	}
	defer release()
	var q QueuedRequest
	var status string
	err = c.QueryRow(ctx, `SELECT id,database_id,migration_id,operation,sql_hash,requested_by,status FROM dmp_migration.change_requests WHERE database_id=$1 AND id=$2`, db, id).Scan(&q.ID, &q.DatabaseID, &q.MigrationID, &q.Operation, &q.SQLHash, &q.RequestedBy, &status)
	if isNoRows(err) {
		return fault("not_found", "Job not found")
	}
	if err != nil {
		return err
	}
	if user != q.RequestedBy {
		return fault("forbidden", "Only the approved requester can recover this job")
	}
	p, err := loadBatch(ctx, c, id)
	if isNoRows(err) {
		return fault("conflict", "Job is not a batch migration")
	}
	if err != nil {
		return err
	}
	if status != "running" || p.State != "paused" {
		return fault("conflict", "Only a paused, verified batch job can be resumed or compensated")
	}
	if err := r.authorizeExecution(ctx, q); err != nil {
		return err
	}
	if action == "compensate" {
		if p.Batches == 0 || p.DownHash == "" {
			return fault("conflict", "No committed batches or compatible approved down SQL; a corrective migration is required")
		}
		m, e := r.repo().Get(ctx, db, q.MigrationID)
		if e != nil {
			return e
		}
		if m.Irreversible || sqlHash(m.SQLDown) != p.DownHash {
			return fault("conflict", "Approved down SQL is unavailable; a corrective migration is required")
		}
		p.Action = "compensate"
	}
	tx, err := c.Begin(ctx)
	if err != nil {
		return err
	}
	defer rollback(ctx, tx)
	if _, err = tx.Exec(ctx, `UPDATE dmp_migration.batch_jobs SET state='ready',action=$2,error_message='',updated_at=now() WHERE id=$1`, id, p.Action); err != nil {
		return err
	}
	if err = addEvent(ctx, tx, id, action, user, nil); err != nil {
		return err
	}
	if err = tx.Commit(ctx); err != nil {
		return err
	}
	if action == "resume" {
		telemetry.RecordRetry("batch_resume_accepted")
	}
	slog.InfoContext(ctx, "batch.action", "status", "accepted", "committed_batches", p.Batches, "reverted_batches", p.Reverted)
	r.notify()
	return nil
}
