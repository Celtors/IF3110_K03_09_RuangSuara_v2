CREATE SCHEMA IF NOT EXISTS dmp_migration;

CREATE TABLE IF NOT EXISTS dmp_migration.definitions (
    id text PRIMARY KEY, database_id text NOT NULL, name text NOT NULL,
    description text NOT NULL DEFAULT '', sql_script text NOT NULL,
    release_label text NOT NULL DEFAULT '', author_user_id text NOT NULL,
    created_at timestamptz NOT NULL DEFAULT now(), updated_at timestamptz NOT NULL DEFAULT now(),
    state text NOT NULL DEFAULT 'draft' CHECK (state IN ('draft','running','failed','applied','unknown')),
    version bigint CHECK (version > 0),
    CHECK ((state = 'applied') = (version IS NOT NULL)),
    UNIQUE(database_id, version)
);

CREATE TABLE IF NOT EXISTS dmp_migration.attempts (
    id text PRIMARY KEY, migration_id text NOT NULL REFERENCES dmp_migration.definitions(id),
    database_id text NOT NULL, sql_script_snapshot text NOT NULL, executor_user_id text NOT NULL,
    started_at timestamptz NOT NULL DEFAULT now(), completed_at timestamptz,
    status text NOT NULL CHECK (status IN ('running','succeeded','failed','unknown')),
    version bigint CHECK (version > 0), target_xid text,
    postgres_error_code text NOT NULL DEFAULT '', error_message text NOT NULL DEFAULT '',
    CHECK ((status = 'succeeded') = (version IS NOT NULL)),
    UNIQUE(database_id,version)
);

CREATE UNIQUE INDEX IF NOT EXISTS one_unresolved_per_database
ON dmp_migration.attempts(database_id) WHERE status IN ('running','unknown');

-- Existing unresolved attempts have no trustworthy target identity. They remain
-- blocked for operator review rather than being reconciled on a guessed target.
ALTER TABLE dmp_migration.attempts ADD COLUMN IF NOT EXISTS target_identity text NOT NULL DEFAULT '';

CREATE UNIQUE INDEX IF NOT EXISTS one_success_per_migration
ON dmp_migration.attempts(migration_id) WHERE status = 'succeeded';

CREATE INDEX IF NOT EXISTS definitions_database ON dmp_migration.definitions(database_id,created_at);

CREATE INDEX IF NOT EXISTS attempts_migration ON dmp_migration.attempts(migration_id,started_at DESC,id DESC);
