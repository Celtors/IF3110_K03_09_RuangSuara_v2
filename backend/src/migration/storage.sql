-- Initialize runs this file in one transaction. Serialize startup schema changes.
SELECT pg_advisory_xact_lock(781349, 1);

CREATE SCHEMA IF NOT EXISTS dmp_migration;

CREATE TABLE IF NOT EXISTS dmp_migration.definitions (
    id text PRIMARY KEY, database_id text NOT NULL, name text NOT NULL,
    description text NOT NULL DEFAULT '', sql_script text NOT NULL,
    release_label text NOT NULL DEFAULT '', author_user_id text NOT NULL,
    created_at timestamptz NOT NULL DEFAULT now(), updated_at timestamptz NOT NULL DEFAULT now(),
    state text NOT NULL DEFAULT 'draft',
    version bigint CHECK (version > 0),
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

DO $$
BEGIN
    IF NOT EXISTS (
        SELECT 1 FROM pg_constraint
        WHERE conrelid = 'dmp_migration.definitions'::regclass
          AND conname = 'definitions_version_required'
    ) THEN
        -- The M1 state/version equivalence would reject a reserved version on failure.
        ALTER TABLE dmp_migration.definitions
            DROP CONSTRAINT IF EXISTS definitions_check,
            ADD COLUMN sql_down text NOT NULL DEFAULT '',
            ADD COLUMN irreversible boolean NOT NULL DEFAULT false,
            ADD COLUMN irreversible_reason text NOT NULL DEFAULT '',
            ADD CONSTRAINT definitions_version_required CHECK (
                state NOT IN ('applied','rolled_back') OR version IS NOT NULL
            );

        -- An M1 execution has no original SQL down. Leave unattempted drafts editable.
        UPDATE dmp_migration.definitions d
        SET irreversible = true,
            irreversible_reason = 'Milestone 1 migration has no recorded SQL down'
        WHERE d.state <> 'draft'
           OR EXISTS (SELECT 1 FROM dmp_migration.attempts a WHERE a.migration_id = d.id);
    END IF;
END $$;

ALTER TABLE dmp_migration.definitions
    DROP CONSTRAINT IF EXISTS definitions_execution_state_check,
    DROP CONSTRAINT IF EXISTS definitions_state_check;

CREATE UNIQUE INDEX IF NOT EXISTS one_unresolved_per_database
ON dmp_migration.attempts(database_id) WHERE status IN ('running','unknown');

-- Existing unresolved attempts have no trustworthy target identity. They remain
-- blocked for operator review rather than being reconciled on a guessed target.
ALTER TABLE dmp_migration.attempts ADD COLUMN IF NOT EXISTS target_identity text NOT NULL DEFAULT '';

DO $$
BEGIN
    IF NOT EXISTS (
        SELECT 1 FROM pg_constraint
        WHERE conrelid = 'dmp_migration.attempts'::regclass
          AND conname = 'attempts_reversal_check'
    ) THEN
        ALTER TABLE dmp_migration.attempts
            ADD COLUMN operation text NOT NULL DEFAULT 'apply'
                CHECK (operation IN ('apply','rollback','forward')),
            ADD COLUMN reverses_execution_id text REFERENCES dmp_migration.attempts(id),
            ADD CONSTRAINT attempts_reversal_check CHECK (
                (operation = 'rollback') = (reverses_execution_id IS NOT NULL)
            );

        DROP INDEX IF EXISTS dmp_migration.one_success_per_migration;
    END IF;
END $$;

CREATE UNIQUE INDEX IF NOT EXISTS one_success_per_migration
ON dmp_migration.attempts(migration_id) WHERE status = 'succeeded' AND operation = 'apply';

CREATE INDEX IF NOT EXISTS definitions_database ON dmp_migration.definitions(database_id,created_at);

CREATE INDEX IF NOT EXISTS attempts_migration ON dmp_migration.attempts(migration_id,started_at DESC,id DESC);
