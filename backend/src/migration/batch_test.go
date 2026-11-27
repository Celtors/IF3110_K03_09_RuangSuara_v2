package migration

import (
	"context"
	"errors"
	"fmt"
	"os"
	"strings"
	"testing"
	"time"

	"backend/internal/telemetry"

	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgxpool"
)

func TestBatchSQLContract(t *testing.T) {
	const up = "UPDATE public.items SET score = score + 1 WHERE id = ANY($1::bigint[])"
	spec := BatchSpec{"id", 2}
	cases := []struct {
		sql   string
		spec  BatchSpec
		valid bool
	}{
		{up, spec, true}, {up, BatchSpec{"id", 0}, false}, {up, BatchSpec{"id", 10001}, false}, {up, BatchSpec{"score", 2}, false},
		{strings.Replace(up, "score = score + 1", "id = id + 1", 1), spec, false},
		{up + "; DELETE FROM public.items", spec, false},
		{strings.Replace(up, "WHERE id", "WHERE score", 1), spec, false},
		{strings.Replace(up, "score + 1", "score--1", 1), spec, false},
		{"UPDATE public.items SET score = 1 WHERE id = ANY($1::bigint[])", spec, true},
	}
	for _, c := range cases {
		_, err := parseBatch(c.sql, c.spec)
		if (err == nil) != c.valid {
			t.Errorf("parse %q = %v, expected valid=%v", c.sql, err, c.valid)
		}
	}
	down := strings.Replace(up, "+ 1", "- 1", 1)
	if !inverseBatchSQL(up, down, spec) || inverseBatchSQL(up, up, spec) || inverseBatchSQL(up, strings.Replace(up, "score + 1", "0", 1), spec) {
		t.Fatal("unsafe compensation contract")
	}
	if !transientBatch(&pgconn.PgError{Code: "40P01"}) {
		t.Fatal("deadlock must be retryable")
	}
}

// Target integration tests require a disposable PostgreSQL 16 database.
func batchTestTarget(t *testing.T) *pgxpool.Pool {
	t.Helper()
	dsn := os.Getenv("SQL_CONSOLE_TEST_TARGET_DSN")
	if dsn == "" {
		t.Skip("set SQL_CONSOLE_TEST_TARGET_DSN")
	}
	config, err := pgxpool.ParseConfig(dsn)
	if err != nil {
		t.Fatal(err)
	}
	config.ConnConfig.Tracer = telemetry.PostgresTracer{Scope: "target"}
	pool, err := pgxpool.NewWithConfig(context.Background(), config)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(pool.Close)
	return pool
}
func batchFixture(t *testing.T, pool *pgxpool.Pool, rows int, withLimit bool) (string, string, string) {
	t.Helper()
	ctx := context.Background()
	name := "batch_it_" + newID()[:12]
	rel := "public." + name
	mustExec(t, pool, `CREATE TABLE `+rel+`(id bigint GENERATED ALWAYS AS IDENTITY PRIMARY KEY, score integer NOT NULL, payload text NOT NULL DEFAULT '')`)
	t.Cleanup(func() { _, _ = pool.Exec(ctx, `DROP TABLE IF EXISTS `+rel) })
	mustExec(t, pool, fmt.Sprintf(`INSERT INTO %s(score) SELECT CASE WHEN g>2 THEN 9 ELSE 0 END FROM generate_series(1,%d) g`, rel, rows))
	if withLimit {
		mustExec(t, pool, `ALTER TABLE `+rel+` ADD CONSTRAINT limited_score CHECK(score<=9)`)
	}
	return rel, `UPDATE ` + rel + ` SET score=score+1 WHERE id=ANY($1::bigint[])`, `UPDATE ` + rel + ` SET score=score-1 WHERE id=ANY($1::bigint[])`
}
func mustExec(t *testing.T, pool *pgxpool.Pool, sql string) {
	t.Helper()
	if _, err := pool.Exec(context.Background(), sql); err != nil {
		t.Fatal(err)
	}
}
func score(t *testing.T, pool *pgxpool.Pool, rel string, id int64) int {
	t.Helper()
	var n int
	if err := pool.QueryRow(context.Background(), `SELECT score FROM `+rel+` WHERE id=$1`, id).Scan(&n); err != nil {
		t.Fatal(err)
	}
	return n
}
func TestBatchLedgerIntegration(t *testing.T) {
	ctx := context.Background()
	pool := batchTestTarget(t)
	rel, up, down := batchFixture(t, pool, 5, true)
	spec := BatchSpec{"id", 2}
	b, err := parseBatch(up, spec)
	if err != nil {
		t.Fatal(err)
	}
	c, err := pool.Acquire(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer c.Release()
	if _, err = c.Exec(ctx, targetBatchDDL); err != nil {
		t.Fatal(err)
	}
	id := newID()
	defer func() {
		_, _ = c.Exec(ctx, `DELETE FROM dmp_batch.scope WHERE job_id=$1`, id)
		_, _ = c.Exec(ctx, `DELETE FROM dmp_batch.jobs WHERE id=$1`, id)
	}()
	if err = initBatchScope(ctx, c, id, b); err != nil {
		t.Fatal(err)
	}
	p := batchProgress{Size: 2}
	if err = targetProgress(ctx, c, id, &p); err != nil {
		t.Fatal(err)
	}
	stale := p
	// Insert an out-of-scope row after snapshot, including a lower key.
	mustExec(t, pool, `INSERT INTO `+rel+`(id,score) OVERRIDING SYSTEM VALUE VALUES(-5,7),(100,8)`)
	if err = runBatchTx(ctx, c, id, up, b, p, false); err != nil {
		t.Fatal(err)
	}
	// Duplicate delivery with stale checkpoint must not change a committed row.
	if err = runBatchTx(ctx, c, id, up, b, stale, false); err == nil {
		t.Fatal("stale checkpoint accepted")
	}
	if err = targetProgress(ctx, c, id, &p); err != nil || p.Batches != 1 || p.Rows != 2 || p.Cursor == nil || *p.Cursor != 2 {
		t.Fatalf("checkpoint %+v err %v", p, err)
	}
	if err = runBatchTx(ctx, c, id, up, b, p, false); err == nil {
		t.Fatal("expected second-batch failure")
	}
	if score(t, pool, rel, 1) != 1 || score(t, pool, rel, 3) != 9 {
		t.Fatal("failed batch did not preserve earlier commit")
	}
	mustExec(t, pool, `ALTER TABLE `+rel+` DROP CONSTRAINT limited_score`)
	if err = runBatchTx(ctx, c, id, up, b, p, false); err != nil {
		t.Fatal(err)
	}
	if err = targetProgress(ctx, c, id, &p); err != nil || p.Batches != 2 || p.Rows != 4 {
		t.Fatalf("resume checkpoint %+v, %v", p, err)
	}
	if score(t, pool, rel, 1) != 1 || score(t, pool, rel, 3) != 10 || score(t, pool, rel, -5) != 7 || score(t, pool, rel, 100) != 8 {
		t.Fatal("resume or stable scope incorrect")
	}
	// Compensate latest batch first. Then force a failure reverting batch one.
	if err = runBatchTx(ctx, c, id, down, b, p, true); err != nil {
		t.Fatal(err)
	}
	if err = targetProgress(ctx, c, id, &p); err != nil || p.Reverted != 1 {
		t.Fatalf("reverted progress %+v, %v", p, err)
	}
	if score(t, pool, rel, 1) != 1 || score(t, pool, rel, 3) != 9 {
		t.Fatal("compensation order incorrect")
	}
	mustExec(t, pool, `ALTER TABLE `+rel+` ADD CONSTRAINT positive_score CHECK(score>0) NOT VALID`)
	if err = runBatchTx(ctx, c, id, down, b, p, true); err == nil {
		t.Fatal("expected compensation failure")
	}
	if err = targetProgress(ctx, c, id, &p); err != nil || p.Reverted != 1 {
		t.Fatal("failed compensation advanced checkpoint")
	}
	mustExec(t, pool, `ALTER TABLE `+rel+` DROP CONSTRAINT positive_score`)
	if err = runBatchTx(ctx, c, id, down, b, p, true); err != nil {
		t.Fatal(err)
	}
	if err = targetProgress(ctx, c, id, &p); err != nil || p.Reverted != 2 {
		t.Fatal("compensation resume failed")
	}
	if score(t, pool, rel, 1) != 0 || score(t, pool, rel, 3) != 9 || score(t, pool, rel, 100) != 8 {
		t.Fatal("compensation changed unrelated rows or failed to restore")
	}
	// Plain non-unique column and ordinary non-identity primary key are rejected.
	bad := b
	bad.cursor = "score"
	bad.column = "id"
	if err = bad.validate(ctx, c); err == nil {
		t.Fatal("non-unique cursor accepted")
	}
}

func TestBatchRetryBoundAndEvidence(t *testing.T) {
	calls, checks := 0, 0
	var delays []time.Duration
	err := retryBatchTransaction(context.Background(), func() error { calls++; return &pgconn.PgError{Code: "40P01"} }, func() bool { checks++; return true }, func(d time.Duration) { delays = append(delays, d) })
	if err == nil || calls != 3 || checks != 2 || len(delays) != 2 || delays[0] != 250*time.Millisecond || delays[1] != 500*time.Millisecond {
		t.Fatalf("retry calls=%d checks=%d delays=%v", calls, checks, delays)
	}
	calls = 0
	_ = retryBatchTransaction(context.Background(), func() error { calls++; return errors.New("lost COMMIT acknowledgement") }, func() bool { return false }, func(time.Duration) { t.Fatal("unknown failure retried") })
	if calls != 1 {
		t.Fatal("uncertain commit retried")
	}
}
