package migration

import (
	"context"
	"log/slog"

	"backend/internal/telemetry"
)

func migrationContext(ctx context.Context, user, database, migration string) context.Context {
	return telemetry.With(ctx, telemetry.ID("actor_id", user), telemetry.ID("database_id", database),
		telemetry.ID("migration_id", migration))
}

func attemptContext(ctx context.Context, a Attempt) context.Context {
	ctx = telemetry.With(ctx, telemetry.ID("execution_id", a.ID), telemetry.ID("attempt_id", a.ID))
	if a.migrationVersion != nil {
		ctx = telemetry.With(ctx, slog.Int64("migration_version", *a.migrationVersion))
	}
	return ctx
}

func authorizationOutcome(ctx context.Context, want Permission, err error) {
	required := "view"
	if want == Edit {
		required = "edit"
	}
	if err != nil {
		code, _ := faultInfo(err)
		slog.WarnContext(ctx, "authorization.database", "status", "denied", "required_permission", required, "reason", code)
		return
	}
	slog.InfoContext(ctx, "authorization.database", "status", "allowed", "required_permission", required)
}

func jobCorrelation(stored, jobID string) string {
	if telemetry.ID("correlation_id", stored).Key == "" {
		return jobID // Rows from before the correlation column was added.
	}
	return stored
}

func (q QueuedRequest) logContext(ctx context.Context) context.Context {
	return jobContext(ctx, q.ID, q.submissionCorrelationID, q.RequestedBy, q.DatabaseID, q.MigrationID, q.Operation)
}

func (q InFlightRequest) logContext(ctx context.Context) context.Context {
	return jobContext(ctx, q.ID, q.submissionCorrelationID, q.RequestedBy, q.DatabaseID, q.MigrationID, q.Operation)
}

func jobContext(ctx context.Context, id, correlation, user, database, migration string, operation Operation) context.Context {
	return telemetry.With(migrationContext(ctx, user, database, migration),
		telemetry.ID("correlation_id", jobCorrelation(correlation, id)),
		telemetry.ID("job_id", id), slog.String("operation", string(operation)))
}
