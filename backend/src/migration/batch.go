package migration

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"math/big"
	"regexp"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
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
	err := c.QueryRow(ctx, `SELECT completed,committed_rows,cursor,reverted,done,scope_count FROM dmp_batch.jobs WHERE id=$1`, id).Scan(&p.Batches, &p.Rows, &p.Cursor, &p.Reverted, &p.Done, &p.ScopeCount)
	if err == nil {
		p.ScopeReady = true
	}
	return err
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
func runBatchTx(ctx context.Context, c *pgxpool.Conn, id, sql string, b batchStatement, p batchProgress, undo bool) error {
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
		return tx.Commit(ctx)
	}
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
	return tx.Commit(ctx)
}
func retryBatchTransaction(run func() error, verified func() bool, delay func(time.Duration)) error {
	var err error
	for tries := 0; tries < 3; tries++ {
		err = run()
		if err == nil || !transientBatch(err) || tries == 2 {
			return err
		}
		if !verified() {
			return errors.New("checkpoint requires reconciliation")
		}
		delay(time.Duration(tries+1) * 250 * time.Millisecond)
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
func (r *Review) processBatch(ctx context.Context, q QueuedRequest) {
	c, release, err := r.repo().lock(ctx, q.DatabaseID)
	if err != nil {
		return
	}
	defer release()
	p, err := loadBatch(ctx, c, q.ID)
	if err != nil {
		return
	}
	var status string
	var attemptID *string
	if err = c.QueryRow(ctx, `SELECT status,attempt_id FROM dmp_migration.change_requests WHERE id=$1`, q.ID).Scan(&status, &attemptID); err != nil || status != "running" || p.State == "paused" {
		return
	}
	// Any bookkeeping error after an intent leaves the durable request locked.
	pause := func(state string, e error) {
		if state == "paused" && p.Intent != "" {
			state = "unresolved"
		}
		code, msg := faultInfo(e)
		if code == "internal_error" {
			msg = "Batch transaction or bookkeeping failed; inspect target logs"
			slog.WarnContext(ctx, "batch.failure", "job_id", q.ID, "sqlstate", postgresCode(e))
		}
		_, _ = c.Exec(ctx, `UPDATE dmp_migration.batch_jobs SET state=$2,error_message=$3,updated_at=now() WHERE id=$1`, q.ID, state, msg)
	}
	m, err := r.repo().Get(ctx, q.DatabaseID, q.MigrationID)
	if err != nil {
		pause("unresolved", err)
		return
	}
	b, err := parseBatch(m.SQLUp, BatchSpec{p.Attribute, p.Size})
	if err != nil {
		pause("paused", err)
		return
	}
	undo := p.Action == "compensate"
	sql := m.SQLUp
	if undo {
		sql = m.SQLDown
		down, e := parseBatch(sql, BatchSpec{p.Attribute, p.Size})
		if e != nil || down != b || m.Irreversible || sqlHash(sql) != p.DownHash || !inverseBatchSQL(m.SQLUp, sql, BatchSpec{p.Attribute, p.Size}) {
			pause("paused", fault("conflict", "No approved compatible down SQL"))
			return
		}
	}
	pool, err := r.Service.connection(ctx, q.DatabaseID)
	if err != nil {
		pause("unresolved", err)
		return
	}
	target, err := pool.Acquire(ctx)
	if err != nil {
		pause("unresolved", err)
		return
	}
	defer target.Release()
	var locked bool
	if err = target.QueryRow(ctx, `SELECT pg_try_advisory_lock(781349,3)`).Scan(&locked); err != nil || !locked {
		if err != nil {
			_ = target.Conn().Close(ctx)
		}
		return
	}
	defer func() {
		clean, cancel := cleanupContext(ctx)
		defer cancel()
		var ok bool
		if e := target.QueryRow(clean, `SELECT pg_advisory_unlock(781349,3)`).Scan(&ok); e != nil || !ok {
			_ = target.Conn().Close(clean)
		}
	}()
	if err = ensureTargetIsNotPlatform(ctx, target, c); err != nil {
		pause("unresolved", err)
		return
	}
	identity, err := targetIdentity(ctx, target.Conn())
	if err != nil {
		pause("unresolved", err)
		return
	}
	if p.Identity != "" && p.Identity != identity {
		pause("unresolved", fault("indeterminate", "Target identity changed"))
		return
	}
	if p.Identity == "" {
		if _, err = c.Exec(ctx, `UPDATE dmp_migration.batch_jobs SET target_identity=$2 WHERE id=$1`, q.ID, identity); err != nil {
			return
		}
	}
	// Create the ordinary attempt before touching the target; recover a crash
	// between beginAttempt and linking by using the unique running attempt.
	var a Attempt
	if attemptID == nil {
		var old string
		err = c.QueryRow(ctx, `SELECT id FROM dmp_migration.attempts WHERE database_id=$1 AND migration_id=$2 AND status='running'`, q.DatabaseID, q.MigrationID).Scan(&old)
		if isNoRows(err) {
			ok, code, msg, e := r.revalidate(ctx, q)
			if e != nil || !ok {
				if e == nil {
					e = fault(code, msg)
				}
				if mirrorBatch(ctx, c, q.ID, p, "failed", e.Error()) == nil {
					_ = r.repo().finishRequest(ctx, q.ID, RequestFailed, "authorization_failed", e.Error(), "")
				}
				return
			}
			if _, _, err = prepareExecution(ctx, c, m, OperationApply); err != nil {
				if mirrorBatch(ctx, c, q.ID, p, "failed", err.Error()) == nil {
					_ = r.repo().finishRequest(ctx, q.ID, RequestFailed, "execution_failed", err.Error(), "")
				}
				return
			}
			a, err = beginAttempt(ctx, c, m, q.RequestedBy, OperationApply, m.SQLUp, nil)
			old = a.ID
		}
		if err != nil {
			pause("unresolved", err)
			return
		}
		attemptID = &old
		if _, err = c.Exec(ctx, `UPDATE dmp_migration.change_requests SET attempt_id=$2 WHERE id=$1`, q.ID, old); err != nil {
			return
		}
	}
	a = Attempt{ID: *attemptID, MigrationID: q.MigrationID, DatabaseID: q.DatabaseID, Operation: OperationApply}
	var attemptStatus string
	if err = c.QueryRow(ctx, `SELECT status FROM dmp_migration.attempts WHERE id=$1`, a.ID).Scan(&attemptStatus); err != nil {
		return
	}
	// If platform finalization committed before the request status was saved.
	if attemptStatus == "succeeded" || attemptStatus == "failed" {
		final := RequestSucceeded
		state := "completed"
		if attemptStatus == "failed" {
			final = RequestFailed
			state = "reverted"
			if !undo {
				state = "failed"
			}
		}
		if mirrorBatch(ctx, c, q.ID, p, state, p.Error) == nil {
			_ = r.repo().finishRequest(ctx, q.ID, final, "", p.Error, a.ID)
		}
		return
	}
	if _, err = c.Exec(ctx, `UPDATE dmp_migration.attempts SET target_identity=$2 WHERE id=$1`, a.ID, identity); err != nil {
		return
	}
	// Reconcile before authorization: reading evidence cannot execute new SQL.
	if p.Intent == "" && !p.ScopeReady {
		if _, err = target.Exec(ctx, targetBatchDDL); err != nil {
			pause("unresolved", err)
			return
		}
	}
	verifiedBefore := p
	err = targetProgress(ctx, target, q.ID, &p)
	if err == nil && (p.Batches < verifiedBefore.Batches || p.Rows < verifiedBefore.Rows || p.Reverted < verifiedBefore.Reverted || (verifiedBefore.ScopeReady && p.ScopeCount != verifiedBefore.ScopeCount)) {
		pause("unresolved", fault("indeterminate", "Target checkpoint regressed"))
		return
	}
	if isNoRows(err) {
		if verifiedBefore.ScopeReady || verifiedBefore.Batches > 0 || (p.Intent != "" && p.Intent != "scope") {
			pause("unresolved", fault("indeterminate", "Target checkpoint is missing"))
			return
		}
		ok, code, msg, e := r.revalidate(ctx, q)
		if e != nil || !ok {
			if e == nil {
				e = fault(code, msg)
			}
			pause("paused", e)
			return
		}
		if batchIntent(ctx, c, q.ID, "scope") != nil {
			return
		}
		if err = initBatchScope(ctx, target, q.ID, b); err != nil {
			if e = targetProgress(ctx, target, q.ID, &p); isNoRows(e) {
				message := "Scope creation failed before data execution"
				if finishAttempt(ctx, c, &a, "failed", postgresCode(err), message) == nil && mirrorBatch(ctx, c, q.ID, p, "failed", message) == nil {
					_ = r.repo().finishRequest(ctx, q.ID, RequestFailed, "execution_failed", message, a.ID)
				}
			} else {
				pause("unresolved", err)
			}
			return
		}
		err = targetProgress(ctx, target, q.ID, &p)
	}
	if err != nil {
		pause("unresolved", err)
		return
	}
	for {
		state := "running"
		if p.Batches > 0 {
			state = "partial"
		}
		if undo {
			state = "compensating"
		}
		if err = mirrorBatch(ctx, c, q.ID, p, state, ""); err != nil {
			return
		}
		p.Intent = ""
		if (!undo && p.Done) || (undo && p.Reverted == p.Batches) {
			result := RequestSucceeded
			attemptResult := "succeeded"
			state = "completed"
			message := ""
			if undo {
				result = RequestFailed
				attemptResult = "failed"
				state = "reverted"
				message = "Committed batches compensated"
			}
			if err = finishAttempt(ctx, c, &a, attemptResult, "", message); err != nil {
				return
			}
			if err = mirrorBatch(ctx, c, q.ID, p, state, message); err != nil {
				return
			}
			_ = r.repo().finishRequest(ctx, q.ID, result, "", message, a.ID)
			return
		}
		ok, code, msg, e := r.revalidate(ctx, q)
		if e != nil || !ok {
			if e == nil {
				e = fault(code, msg)
			}
			pause("paused", e)
			return
		}
		intent := fmt.Sprintf("apply:%d", p.Batches+1)
		if undo {
			intent = fmt.Sprintf("compensate:%d", p.Batches-p.Reverted)
		}
		if batchIntent(ctx, c, q.ID, intent) != nil {
			return
		}
		err = retryBatchTransaction(func() error { return runBatchTx(ctx, target, q.ID, sql, b, p, undo) }, func() bool {
			var verified batchProgress
			return targetProgress(ctx, target, q.ID, &verified) == nil && verified.Batches == p.Batches && verified.Reverted == p.Reverted && verified.Done == p.Done
		}, time.Sleep)
		if err != nil {
			// A live connection with a verified unchanged checkpoint proves no commit;
			// if the connection was lost leave intent intact for the next recovery.
			previous := p
			if e = targetProgress(ctx, target, q.ID, &p); e != nil {
				pause("unresolved", err)
				return
			}
			if p.Batches != previous.Batches || p.Reverted != previous.Reverted || p.Done != previous.Done {
				continue
			}
			code, message := faultInfo(err)
			if code == "indeterminate" {
				pause("unresolved", err)
				return
			}
			if code == "internal_error" {
				message = "Batch failed (SQLSTATE " + postgresCode(err) + "); resume or compensate after correcting the cause"
			}
			if p.Batches == 0 && !undo {
				if finishAttempt(ctx, c, &a, "failed", postgresCode(err), message) == nil && mirrorBatch(ctx, c, q.ID, p, "failed", message) == nil {
					_ = r.repo().finishRequest(ctx, q.ID, RequestFailed, "execution_failed", message, a.ID)
				}
				return
			}
			if mirrorBatch(ctx, c, q.ID, p, "paused", message) != nil {
				return
			}
			return
		}
		if err = targetProgress(ctx, target, q.ID, &p); err != nil {
			pause("unresolved", err)
			return
		}
	}
}

// Running requests themselves are durable reservations. Recover them directly,
// including after restart, without relying on an in-memory lease or clock.
func (r *Review) nextBatch(ctx context.Context, attempted map[string]bool) (*QueuedRequest, error) {
	rows, err := r.repo().Pool.Query(ctx, `SELECT r.id,r.database_id,r.migration_id,r.operation,r.sql_hash,r.requested_by,r.submission_correlation_id FROM dmp_migration.change_requests r JOIN dmp_migration.batch_jobs b ON b.id=r.id WHERE r.status='running' AND b.state<>'paused' ORDER BY r.started_at`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	for rows.Next() {
		var q QueuedRequest
		if err = rows.Scan(&q.ID, &q.DatabaseID, &q.MigrationID, &q.Operation, &q.SQLHash, &q.RequestedBy, &q.submissionCorrelationID); err != nil {
			return nil, err
		}
		if _, live := r.inflight.Load(q.ID); !live && !attempted[q.ID] {
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
	ok, code, msg, err := r.revalidate(ctx, q)
	if err != nil {
		return err
	}
	if !ok {
		return fault(code, msg)
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
	r.notify()
	return nil
}
