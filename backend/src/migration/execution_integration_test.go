package migration

import (
	"context"
	"testing"

	"backend/internal/telemetry"

	"github.com/jackc/pgx/v5"
	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/codes"
	sdktrace "go.opentelemetry.io/otel/sdk/trace"
	"go.opentelemetry.io/otel/sdk/trace/tracetest"
)

func testOperationTransitions(t *testing.T, ctx context.Context, service *Service) {
	const db = "slice2-transitions"
	pool, err := service.Connections.PostgreSQL(ctx, "slice2")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := pool.Exec(ctx, `
 CREATE TABLE slice2_items (id integer PRIMARY KEY);
 CREATE TABLE slice2_refs (id integer REFERENCES slice2_items(id));
 INSERT INTO slice2_items VALUES (1)`); err != nil {
		t.Fatal(err)
	}

	first := saveExecutionDraft(t, ctx, service, db, Draft{
		Name: "First", SQLUp: "INSERT INTO slice2_items VALUES (1)", SQLDown: "DELETE FROM slice2_items WHERE id=1",
	})
	if _, err := service.ExecuteOperation(ctx, "view", db, first.ID, OperationApply); faultCode(err) != "forbidden" {
		t.Fatalf("view user apply: %v", err)
	}
	if _, err := service.ExecuteOperation(ctx, "", db, first.ID, OperationRollback); faultCode(err) != "unauthenticated" {
		t.Fatalf("unauthenticated rollback: %v", err)
	}
	failedApply := runOperation(t, ctx, service, first, OperationApply, "execution_failed")
	checkExecutionState(t, ctx, service, first, "failed", 1)

	if _, err := pool.Exec(ctx, "DELETE FROM slice2_items WHERE id=1"); err != nil {
		t.Fatal(err)
	}
	applied := runOperation(t, ctx, service, first, OperationApply, "")
	checkExecutionState(t, ctx, service, first, "applied", 1)

	second := saveExecutionDraft(t, ctx, service, db, Draft{
		Name: "Second", SQLUp: "INSERT INTO slice2_items VALUES (2)", SQLDown: "DELETE FROM slice2_items WHERE id=2",
	})
	secondApply := runOperation(t, ctx, service, second, OperationApply, "")
	runOperation(t, ctx, service, first, OperationRollback, "migration_order_conflict")
	secondRollback := runOperation(t, ctx, service, second, OperationRollback, "")
	checkExecutionState(t, ctx, service, second, "rolled_back", 2)

	if _, err := pool.Exec(ctx, "INSERT INTO slice2_refs VALUES (1)"); err != nil {
		t.Fatal(err)
	}
	failedRollback := runOperation(t, ctx, service, first, OperationRollback, "execution_failed")
	checkExecutionState(t, ctx, service, first, "applied", 1)
	checkReversedExecution(t, failedRollback, applied)

	if _, err := pool.Exec(ctx, "DELETE FROM slice2_refs"); err != nil {
		t.Fatal(err)
	}
	rolledBack := runOperation(t, ctx, service, first, OperationRollback, "")
	checkExecutionState(t, ctx, service, first, "rolled_back", 1)
	checkReversedExecution(t, rolledBack, applied)
	runOperation(t, ctx, service, second, OperationForward, "migration_order_conflict")

	if _, err := pool.Exec(ctx, "INSERT INTO slice2_items VALUES (1)"); err != nil {
		t.Fatal(err)
	}
	failedForward := runOperation(t, ctx, service, first, OperationForward, "execution_failed")
	checkExecutionState(t, ctx, service, first, "rolled_back", 1)

	if _, err := pool.Exec(ctx, "DELETE FROM slice2_items WHERE id=1"); err != nil {
		t.Fatal(err)
	}
	forwarded := runOperation(t, ctx, service, first, OperationForward, "")
	checkExecutionState(t, ctx, service, first, "applied", 1)
	var inserted bool
	if err := pool.QueryRow(ctx, "SELECT EXISTS(SELECT 1 FROM slice2_items WHERE id=1)").Scan(&inserted); err != nil || !inserted {
		t.Fatalf("forward did not run the original insert: %v", err)
	}
	rolledBackAgain := runOperation(t, ctx, service, first, OperationRollback, "")
	checkReversedExecution(t, rolledBackAgain, forwarded)
	checkExecutionState(t, ctx, service, first, "rolled_back", 1)

	for i, attempt := range []Attempt{applied, secondApply, secondRollback, rolledBack, forwarded, rolledBackAgain} {
		if attempt.SuccessOrder == nil || *attempt.SuccessOrder != int64(i+1) {
			t.Fatalf("success order for %s: %+v", attempt.ID, attempt)
		}
	}
	if rolledBack.SQL != first.SQLDown || forwarded.SQL != first.SQLUp {
		t.Fatalf("unexpected SQL snapshots for rollback and forward: %q, %q", rolledBack.SQL, forwarded.SQL)
	}

	if err := service.Repository.Initialize(ctx); err != nil {
		t.Fatalf("reinitialize after executing rollback and forward: %v", err)
	}
	attempts, err := service.Repository.Attempts(ctx, db, first.ID)
	if err != nil || len(attempts) != 7 {
		t.Fatalf("attempt history: %+v, error: %v", attempts, err)
	}
	byID := make(map[string]Attempt)
	for _, attempt := range attempts {
		byID[attempt.ID] = attempt
	}
	checkReversedExecution(t, byID[rolledBackAgain.ID], forwarded)
	for _, failed := range []Attempt{failedApply, failedRollback, failedForward} {
		stored, found := byID[failed.ID]
		if !found || stored.Status != "failed" || stored.SuccessOrder != nil || stored.SQL != failed.SQL {
			t.Fatalf("failed attempt %s changed or missing: %+v", failed.ID, stored)
		}
	}
}

func testExecutionPathConflicts(t *testing.T, ctx context.Context, service *Service) {
	const db = "slice2-path"
	failed := saveExecutionDraft(t, ctx, service, db, Draft{
		Name: "Failed", SQLUp: "SELECT * FROM slice2_missing_table",
	})
	runOperation(t, ctx, service, failed, OperationApply, "execution_failed")
	checkExecutionState(t, ctx, service, failed, "failed", 1)

	first := saveExecutionDraft(t, ctx, service, db, Draft{Name: "First path", SQLUp: "SELECT 1", SQLDown: "SELECT 2"})
	runOperation(t, ctx, service, first, OperationApply, "")
	runOperation(t, ctx, service, first, OperationRollback, "")

	next := saveExecutionDraft(t, ctx, service, db, Draft{Name: "New path", SQLUp: "SELECT 3", SQLDown: "SELECT 4"})
	runOperation(t, ctx, service, next, OperationApply, "")
	runOperation(t, ctx, service, next, OperationRollback, "")
	checkExecutionState(t, ctx, service, next, "rolled_back", 3)

	runOperation(t, ctx, service, first, OperationForward, "migration_order_conflict")
	runOperation(t, ctx, service, failed, OperationApply, "migration_order_conflict")
	runOperation(t, ctx, service, next, OperationForward, "")

	irreversible := saveExecutionDraft(t, ctx, service, db, Draft{
		Name: "Irreversible", SQLUp: "SELECT 5", Irreversible: true, IrreversibleReason: "No data to restore",
	})
	runOperation(t, ctx, service, irreversible, OperationApply, "")
	runOperation(t, ctx, service, irreversible, OperationRollback, "conflict")
}

func testMigrationRecovery(t *testing.T, ctx context.Context, service *Service) {
	recorder := tracetest.NewSpanRecorder()
	provider := sdktrace.NewTracerProvider(sdktrace.WithSpanProcessor(recorder))
	previous := otel.GetTracerProvider()
	otel.SetTracerProvider(provider)
	defer func() {
		_ = provider.Shutdown(context.Background())
		otel.SetTracerProvider(previous)
	}()
	ctx, recovery := telemetry.Start(ctx, "test.recovery")
	defer recovery.End()

	const db = "slice2-recovery"
	definition := saveExecutionDraft(t, ctx, service, db, Draft{
		Name: "Recoverable", SQLUp: "CREATE TABLE slice2_recovered (id integer)", SQLDown: "DROP TABLE slice2_recovered",
	})
	applied := beginInterruptedAttempt(t, ctx, service, definition, OperationApply)
	checkExecutionState(t, ctx, service, definition, "draft", 1)
	inProgress, err := service.Repository.Attempts(ctx, db, definition.ID)
	if err != nil || len(inProgress) != 1 || inProgress[0].ID != applied.ID || inProgress[0].Status != "running" {
		t.Fatalf("attempt before executing SQL: %+v, error: %v", inProgress, err)
	}
	if err := runInterruptedSQL(t, ctx, service, applied).Commit(ctx); err != nil {
		t.Fatal(err)
	}
	if err := service.Reconcile(ctx, "edit", db); err != nil {
		t.Fatal(err)
	}
	checkExecutionState(t, ctx, service, definition, "applied", 1)

	abortedRollback := beginInterruptedAttempt(t, ctx, service, definition, OperationRollback)
	if err := runInterruptedSQL(t, ctx, service, abortedRollback).Rollback(ctx); err != nil {
		t.Fatal(err)
	}
	if err := service.Reconcile(ctx, "edit", db); err != nil {
		t.Fatal(err)
	}
	checkExecutionState(t, ctx, service, definition, "applied", 1)

	rollbackAttempt := beginInterruptedAttempt(t, ctx, service, definition, OperationRollback)
	rollbackTx := runInterruptedSQL(t, ctx, service, rollbackAttempt)
	if err := service.Reconcile(ctx, "edit", db); faultCode(err) != "indeterminate" {
		t.Fatalf("reconcile in-progress rollback: %v", err)
	}
	checkExecutionState(t, ctx, service, definition, "applied", 1)
	pending := saveExecutionDraft(t, ctx, service, db, Draft{Name: "Pending", SQLUp: "SELECT 1"})
	runOperation(t, ctx, service, pending, OperationApply, "indeterminate")
	checkExecutionState(t, ctx, service, pending, "draft", 0)

	var unresolvedStatus string
	if err := service.Repository.Pool.QueryRow(ctx, "SELECT status FROM dmp_migration.attempts WHERE id=$1", rollbackAttempt.ID).
		Scan(&unresolvedStatus); err != nil || unresolvedStatus != "unknown" {
		t.Fatalf("unresolved rollback status: %q, error: %v", unresolvedStatus, err)
	}
	if err := rollbackTx.Commit(ctx); err != nil {
		t.Fatal(err)
	}
	if err := service.Reconcile(ctx, "edit", db); err != nil {
		t.Fatal(err)
	}
	checkExecutionState(t, ctx, service, definition, "rolled_back", 1)

	abortedForward := beginInterruptedAttempt(t, ctx, service, definition, OperationForward)
	if err := runInterruptedSQL(t, ctx, service, abortedForward).Rollback(ctx); err != nil {
		t.Fatal(err)
	}
	if err := service.Reconcile(ctx, "edit", db); err != nil {
		t.Fatal(err)
	}
	checkExecutionState(t, ctx, service, definition, "rolled_back", 1)

	forward := beginInterruptedAttempt(t, ctx, service, definition, OperationForward)
	if err := runInterruptedSQL(t, ctx, service, forward).Commit(ctx); err != nil {
		t.Fatal(err)
	}
	if err := service.Reconcile(ctx, "edit", db); err != nil {
		t.Fatal(err)
	}
	checkExecutionState(t, ctx, service, definition, "applied", 1)

	attempts, err := service.Repository.Attempts(ctx, db, definition.ID)
	if err != nil || len(attempts) != 5 {
		t.Fatalf("recovered attempts: %+v, error: %v", attempts, err)
	}
	byID := make(map[string]Attempt)
	for _, attempt := range attempts {
		byID[attempt.ID] = attempt
	}
	for _, attempt := range []Attempt{abortedRollback, abortedForward} {
		if stored := byID[attempt.ID]; stored.Status != "failed" || stored.SuccessOrder != nil {
			t.Fatalf("aborted %s was not recorded: %+v", attempt.Operation, stored)
		}
	}
	for i, attempt := range []Attempt{applied, rollbackAttempt, forward} {
		stored := byID[attempt.ID]
		if stored.Status != "succeeded" || stored.SuccessOrder == nil || *stored.SuccessOrder != int64(i+1) {
			t.Fatalf("committed attempt was not finalized once: %+v", stored)
		}
	}
	checkReversedExecution(t, byID[rollbackAttempt.ID], applied)

	outcomes := map[string]int{}
	for _, span := range recorder.Ended() {
		if span.Name() != "migration.reconcile" {
			continue
		}
		attrs := attribute.NewSet(span.Attributes()...)
		value, _ := attrs.Value("transaction.outcome")
		outcome := value.AsString()
		outcomes[outcome]++
		if span.SpanContext().TraceID() != recovery.SpanContext().TraceID() ||
			(span.Status().Code == codes.Error) != (outcome == "unknown") {
			t.Fatalf("unexpected reconciliation span for %s", outcome)
		}
	}
	if outcomes["committed"] != 3 || outcomes["aborted"] != 2 || outcomes["unknown"] != 2 {
		t.Fatalf("unexpected reconciliation outcomes: %v", outcomes)
	}
}

func saveExecutionDraft(t *testing.T, ctx context.Context, service *Service, db string, draft Draft) Migration {
	t.Helper()
	definition, err := service.Save(ctx, "edit", db, "", draft)
	if err != nil {
		t.Fatal(err)
	}
	return definition
}

func runOperation(t *testing.T, ctx context.Context, service *Service, definition Migration, operation Operation, code string) Attempt {
	t.Helper()
	attempt, err := service.ExecuteOperation(ctx, "edit", definition.DatabaseID, definition.ID, operation)
	if faultCode(err) != code {
		t.Fatalf("%s %s: got %v, want fault code %q", operation, definition.Name, err, code)
	}
	return attempt
}

func checkExecutionState(t *testing.T, ctx context.Context, service *Service, definition Migration, state string, version int64) {
	t.Helper()
	stored, err := service.Repository.Get(ctx, definition.DatabaseID, definition.ID)
	if err != nil || stored.State != state {
		t.Fatalf("%s state: got %q, want %q, error: %v", definition.Name, stored.State, state, err)
	}
	if version == 0 {
		if stored.Version != nil {
			t.Fatalf("%s has a reserved version before execution", definition.Name)
		}
	} else if stored.Version == nil || *stored.Version != version {
		t.Fatalf("%s did not retain version %d", definition.Name, version)
	}
}

func checkReversedExecution(t *testing.T, rollbackAttempt, priorAttempt Attempt) {
	t.Helper()
	if rollbackAttempt.ReversesExecutionID == nil || *rollbackAttempt.ReversesExecutionID != priorAttempt.ID {
		t.Fatalf("rollback attempt %s did not link to %s", rollbackAttempt.ID, priorAttempt.ID)
	}
}

func beginInterruptedAttempt(t *testing.T, ctx context.Context, service *Service, definition Migration, operation Operation) Attempt {
	t.Helper()
	conn, release, err := service.Repository.lock(ctx, definition.DatabaseID)
	if err != nil {
		t.Fatal(err)
	}
	defer release()
	current, err := service.Repository.Get(ctx, definition.DatabaseID, definition.ID)
	if err != nil {
		t.Fatal(err)
	}
	sql, reversesExecutionID, err := prepareExecution(ctx, conn, current, operation)
	if err != nil {
		t.Fatal(err)
	}
	attempt, err := beginAttempt(ctx, conn, current, "edit", operation, sql, reversesExecutionID)
	if err != nil {
		t.Fatal(err)
	}
	return attempt
}

// Leave platform finalization pending so reconciliation has to read the actual target outcome.
func runInterruptedSQL(t *testing.T, ctx context.Context, service *Service, attempt Attempt) pgx.Tx {
	t.Helper()
	pool, err := service.Connections.PostgreSQL(ctx, attempt.DatabaseID)
	if err != nil {
		t.Fatal(err)
	}
	tx, err := pool.Begin(ctx)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { rollback(ctx, tx) })

	identity, err := targetIdentity(ctx, tx.Conn())
	if err != nil {
		t.Fatal(err)
	}
	var xid string
	if err := tx.QueryRow(ctx, "SELECT pg_catalog.pg_current_xact_id()::text").Scan(&xid); err != nil {
		t.Fatal(err)
	}
	if _, err := service.Repository.Pool.Exec(ctx, "UPDATE dmp_migration.attempts SET target_xid=$2,target_identity=$3 WHERE id=$1", attempt.ID, xid, identity); err != nil {
		t.Fatal(err)
	}
	if _, err := tx.Exec(ctx, attempt.SQL); err != nil {
		t.Fatal(err)
	}
	return tx
}
