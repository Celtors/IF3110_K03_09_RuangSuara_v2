package migration

import (
	"context"
	"os"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
)

const milestoneOneStorage = `
CREATE SCHEMA dmp_migration;
CREATE TABLE dmp_migration.definitions (
    id text PRIMARY KEY, database_id text NOT NULL, name text NOT NULL,
    description text NOT NULL DEFAULT '', sql_script text NOT NULL,
    release_label text NOT NULL DEFAULT '', author_user_id text NOT NULL,
    created_at timestamptz NOT NULL DEFAULT now(), updated_at timestamptz NOT NULL DEFAULT now(),
    state text NOT NULL DEFAULT 'draft' CHECK (state IN ('draft','running','failed','applied','unknown')),
    version bigint CHECK (version > 0),
    CHECK ((state = 'applied') = (version IS NOT NULL)),
    UNIQUE(database_id, version)
);
CREATE TABLE dmp_migration.attempts (
    id text PRIMARY KEY, migration_id text NOT NULL REFERENCES dmp_migration.definitions(id),
    database_id text NOT NULL, sql_script_snapshot text NOT NULL, executor_user_id text NOT NULL,
    started_at timestamptz NOT NULL DEFAULT now(), completed_at timestamptz,
    status text NOT NULL CHECK (status IN ('running','succeeded','failed','unknown')),
    version bigint CHECK (version > 0), target_xid text,
    postgres_error_code text NOT NULL DEFAULT '', error_message text NOT NULL DEFAULT '',
    CHECK ((status = 'succeeded') = (version IS NOT NULL)),
    UNIQUE(database_id, version), target_identity text NOT NULL DEFAULT ''
);
CREATE UNIQUE INDEX one_unresolved_per_database
ON dmp_migration.attempts(database_id) WHERE status IN ('running','unknown');
CREATE UNIQUE INDEX one_success_per_migration
ON dmp_migration.attempts(migration_id) WHERE status = 'succeeded';
INSERT INTO dmp_migration.definitions (id,database_id,name,sql_script,author_user_id,state,version) VALUES
    ('applied','legacy','Applied','SELECT 1','edit','applied',1),
    ('edited','legacy','Edited after failure','SELECT 3','edit','draft',NULL),
    ('draft','legacy','Untouched draft','SELECT 4','edit','draft',NULL),
    ('unresolved','legacy','Unresolved','SELECT 5','edit','unknown',NULL),
    ('retry','legacy-retry','Legacy retry','SELECT 6','edit','failed',NULL);
INSERT INTO dmp_migration.attempts
    (id,migration_id,database_id,sql_script_snapshot,executor_user_id,status,version,target_xid,target_identity) VALUES
    ('success','applied','legacy','SELECT 1','edit','succeeded',1,'40','original-target'),
    ('failure','edited','legacy','SELECT 2','edit','failed',NULL,'41','original-target'),
    ('unknown','unresolved','legacy','SELECT 5','edit','unknown',NULL,'42','original-target'),
    ('retry-failed','retry','legacy-retry','SELECT 6','edit','failed',NULL,'43','original-target');`

// MIGRATION_TEST_DSN must point to a disposable PostgreSQL database without migration storage.
func TestMigrationDefinitionPersistence(t *testing.T) {
	dsn := os.Getenv("MIGRATION_TEST_DSN")
	if dsn == "" {
		t.Skip("set MIGRATION_TEST_DSN to a disposable PostgreSQL database")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	pool, err := pgxpool.New(ctx, dsn)
	if err != nil {
		t.Fatal(err)
	}
	defer pool.Close()

	var schemaExists bool
	if err := pool.QueryRow(ctx, `SELECT to_regnamespace('dmp_migration') IS NOT NULL`).Scan(&schemaExists); err != nil {
		t.Fatal(err)
	}
	if schemaExists {
		t.Fatal("MIGRATION_TEST_DSN already contains migration storage")
	}
	if _, err := pool.Exec(ctx, milestoneOneStorage); err != nil {
		t.Fatal(err)
	}

	repo := &Repository{Pool: pool}
	if err := repo.Initialize(ctx); err != nil {
		t.Fatalf("upgrade M1 storage: %v", err)
	}
	legacy, err := repo.Get(ctx, "legacy", "applied")
	if err != nil || legacy.State != "applied" || legacy.Version == nil || *legacy.Version != 1 ||
		legacy.SQLUp != "SELECT 1" || legacy.SQLDown != "" || !legacy.Irreversible || legacy.IrreversibleReason == "" {
		t.Fatalf("legacy applied definition: %+v, error: %v", legacy, err)
	}
	appliedAttempts, err := repo.Attempts(ctx, "legacy", legacy.ID)
	if err != nil || len(appliedAttempts) != 1 {
		t.Fatalf("legacy applied attempts: %+v, error: %v", appliedAttempts, err)
	}
	legacySuccess := appliedAttempts[0]
	if legacySuccess.ID != "success" || legacySuccess.SQL != "SELECT 1" || legacySuccess.Status != "succeeded" ||
		legacySuccess.Operation != OperationApply || legacySuccess.ReversesExecutionID != nil ||
		legacySuccess.SuccessOrder == nil || *legacySuccess.SuccessOrder != 1 {
		t.Fatalf("legacy successful attempt changed: %+v", legacySuccess)
	}
	untouched, err := repo.Get(ctx, "legacy", "draft")
	if err != nil || untouched.SQLUp != "SELECT 4" || untouched.Irreversible {
		t.Fatalf("legacy unattempted draft: %+v, error: %v", untouched, err)
	}
	edited, err := repo.Get(ctx, "legacy", "edited")
	if err != nil || edited.SQLUp != "SELECT 3" || !edited.Irreversible {
		t.Fatalf("legacy edited definition: %+v, error: %v", edited, err)
	}
	attempts, err := repo.Attempts(ctx, "legacy", "edited")
	if err != nil || len(attempts) != 1 || attempts[0].SQL != "SELECT 2" || attempts[0].Status != "failed" {
		t.Fatalf("legacy failed snapshot: %+v, error: %v", attempts, err)
	}
	var xid, identity, status string
	err = pool.QueryRow(ctx, `SELECT target_xid,target_identity,status FROM dmp_migration.attempts WHERE id='unknown'`).Scan(&xid, &identity, &status)
	if err != nil || xid != "42" || identity != "original-target" || status != "unknown" {
		t.Fatalf("unresolved attempt changed: %q, %q, %q, error: %v", xid, identity, status, err)
	}

	service := &Service{
		Repository:  repo,
		Access:      fakeAccess{perms: map[string]Permission{"edit": Edit, "view": View}},
		Connections: fakeConnections{pool: pool},
	}
	const db = "slice1"
	if _, err := service.Save(ctx, "edit", db, "", Draft{}); faultCode(err) != "invalid_input" {
		t.Fatalf("save empty draft: %v", err)
	}
	incomplete, err := service.Save(ctx, "edit", db, "", Draft{Name: "Up-only draft", SQLUp: "SELECT 1"})
	if err != nil || incomplete.State != "draft" {
		t.Fatalf("save up-only draft: %+v, error: %v", incomplete, err)
	}
	reversible := Draft{Name: "Reversible", SQLUp: "SELECT 1", SQLDown: "SELECT 2"}
	invalid := reversible
	invalid.SQLDown = "BEGIN"
	if _, err := service.Save(ctx, "edit", db, incomplete.ID, invalid); faultCode(err) != "unsupported_sql" {
		t.Fatalf("save invalid SQL down: %v", err)
	}
	if _, err := service.Save(ctx, "edit", "legacy", "edited", reversible); faultCode(err) != "conflict" {
		t.Fatalf("edit attempted legacy draft: %v", err)
	}
	if updated, err := service.Save(ctx, "edit", "legacy", untouched.ID, reversible); err != nil || updated.Draft != reversible {
		t.Fatalf("edit unattempted legacy draft: %+v, error: %v", updated, err)
	}
	complete, err := service.Save(ctx, "edit", db, incomplete.ID, reversible)
	if err != nil || complete.Draft != reversible {
		t.Fatalf("update unattempted draft: %+v, error: %v", complete, err)
	}
	irreversible := Draft{Name: "Irreversible", SQLUp: "SELECT 1", Irreversible: true, IrreversibleReason: "No original data remains"}
	created, err := service.Save(ctx, "edit", db, "", irreversible)
	if err != nil || created.Draft != irreversible {
		t.Fatalf("save irreversible definition: %+v, error: %v", created, err)
	}
	if err := repo.Initialize(ctx); err != nil {
		t.Fatalf("reinitialize M2 storage: %v", err)
	}
	loaded, err := service.Details(ctx, "edit", db, complete.ID)
	if err != nil || loaded.Draft != reversible {
		t.Fatalf("reloaded definition: %+v, error: %v", loaded, err)
	}
	failing := Draft{Name: "Failing", SQLUp: "SELECT * FROM slice1_absent_table", SQLDown: "SELECT 1"}
	failed, err := service.Save(ctx, "edit", db, "", failing)
	if err != nil {
		t.Fatal(err)
	}
	attempt, err := service.Execute(ctx, "edit", db, failed.ID)
	if faultCode(err) != "execution_failed" || attempt.Status != "failed" || attempt.SuccessOrder != nil || attempt.SQL != failing.SQLUp {
		t.Fatalf("failed apply: %+v, error: %v", attempt, err)
	}
	if _, err := service.Save(ctx, "edit", db, failed.ID, reversible); faultCode(err) != "conflict" {
		t.Fatalf("edit failed migration: %v", err)
	}
	for _, state := range []string{"failed", "rolled_back"} {
		if _, err := pool.Exec(ctx, `UPDATE dmp_migration.definitions SET state=$1,version=10 WHERE id=$2`, state, failed.ID); err != nil {
			t.Fatalf("store %s definition with a reserved version: %v", state, err)
		}
	}

	upOnly, err := service.Save(ctx, "edit", db, "", Draft{Name: "M1 apply", SQLUp: "SELECT 7"})
	if err != nil {
		t.Fatal(err)
	}
	applied, err := service.Execute(ctx, "edit", db, upOnly.ID)
	if err != nil || applied.Status != "succeeded" || applied.SuccessOrder == nil || *applied.SuccessOrder != 1 || applied.SQL != upOnly.SQLUp {
		t.Fatalf("M1 apply: %+v, error: %v", applied, err)
	}
	checkExecutionState(t, ctx, service, upOnly, "applied", 11)

	legacyRetry, err := service.Execute(ctx, "edit", "legacy-retry", "retry")
	if err != nil || legacyRetry.Status != "succeeded" || legacyRetry.ID == "retry-failed" || legacyRetry.SQL != "SELECT 6" {
		t.Fatalf("retry legacy failure with unchanged SQL: %+v, error: %v", legacyRetry, err)
	}

	t.Run("operation transitions", func(t *testing.T) { testOperationTransitions(t, ctx, service) })
	t.Run("path conflicts", func(t *testing.T) { testExecutionPathConflicts(t, ctx, service) })
	t.Run("recovery", func(t *testing.T) { testMigrationRecovery(t, ctx, service) })
	t.Run("execution history API", func(t *testing.T) { testExecutionHistoryAPI(t, ctx, service) })
	t.Run("definition state constraint cleanup", func(t *testing.T) { testDefinitionStateConstraintCleanup(t, ctx, repo) })
}
