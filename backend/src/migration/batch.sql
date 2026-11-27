SELECT    pg_advisory_xact_lock(781349, 2);
CREATE    TABLE IF NOT EXISTS dmp_migration.batch_jobs (
    id text PRIMARY KEY REFERENCES dmp_migration.change_requests(id),
    attribute text NOT NULL, batch_size integer NOT NULL CHECK(batch_size BETWEEN 1 AND 10000),
    down_hash text NOT NULL DEFAULT '', target_identity text NOT NULL DEFAULT '',
    state text NOT NULL DEFAULT 'ready' CHECK(state IN ('ready','running','partial','paused','unresolved','completed','failed','compensating','reverted')), action text NOT NULL DEFAULT 'apply' CHECK(action IN ('apply','compensate')),
    committed_batches bigint NOT NULL DEFAULT 0, committed_rows bigint NOT NULL DEFAULT 0,
    cursor bigint, reverted_batches bigint NOT NULL DEFAULT 0,
    intent text NOT NULL DEFAULT '', error_message text NOT NULL DEFAULT '',
    scope_ready boolean NOT NULL DEFAULT false, scope_count bigint NOT NULL DEFAULT 0, updated_at timestamptz NOT NULL DEFAULT now()
);

ALTER TABLE dmp_migration.batch_jobs
    ADD COLUMN IF NOT EXISTS compensation_unsafe boolean NOT NULL DEFAULT false,
    ADD COLUMN IF NOT EXISTS correction_started boolean NOT NULL DEFAULT false,
    ADD COLUMN IF NOT EXISTS resolved_by text REFERENCES dmp_migration.change_requests(id);
