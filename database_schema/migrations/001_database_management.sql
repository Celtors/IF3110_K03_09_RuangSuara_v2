-- Review and back up the platform database before applying this migration.
-- Legacy rows in databases that lack connection metadata cannot be used as target
-- connections until their metadata and encrypted credentials are populated.
BEGIN;
ALTER TABLE databases ALTER COLUMN name TYPE VARCHAR(100) USING name::text;
ALTER TABLE databases ADD COLUMN IF NOT EXISTS host VARCHAR(253);
ALTER TABLE databases ADD COLUMN IF NOT EXISTS port INTEGER;
ALTER TABLE databases ADD COLUMN IF NOT EXISTS database_name VARCHAR(128);
ALTER TABLE databases ADD COLUMN IF NOT EXISTS username VARCHAR(128);
ALTER TABLE databases ADD COLUMN IF NOT EXISTS password_ciphertext TEXT;
ALTER TABLE databases ADD COLUMN IF NOT EXISTS ssl_mode VARCHAR(32) NOT NULL DEFAULT 'verify-full';

-- Drop legacy group-policy constraints that conflict with the normalized model.
ALTER TABLE group_policies DROP CONSTRAINT IF EXISTS fk_gp_group;
ALTER TABLE group_policies DROP CONSTRAINT IF EXISTS fk_gp_db;
ALTER TABLE group_policies DROP CONSTRAINT IF EXISTS group_policies_group_id_key;
ALTER TABLE group_policies DROP CONSTRAINT IF EXISTS group_policies_policy_group_id_key;
ALTER TABLE group_policies DROP CONSTRAINT IF EXISTS group_policies_group_id_database_id_key;
DO $$ BEGIN
  IF EXISTS (SELECT 1 FROM information_schema.columns WHERE table_name='group_policies' AND column_name='group_id' AND data_type='character varying') THEN
    ALTER TABLE group_policies ALTER COLUMN group_id TYPE UUID USING group_id::uuid;
  END IF;
END $$;
ALTER TABLE group_policies ADD COLUMN IF NOT EXISTS permission VARCHAR(20) NOT NULL DEFAULT 'VIEW_ONLY';
UPDATE group_policies SET effect = CASE WHEN upper(effect) IN ('BLOCKED','DENY') THEN 'DENY' ELSE 'ALLOW' END;
ALTER TABLE group_policies ALTER COLUMN effect TYPE VARCHAR(10);
ALTER TABLE group_policies ALTER COLUMN effect SET DEFAULT 'ALLOW';
ALTER TABLE group_policies ADD CONSTRAINT chk_group_policies_permission CHECK (permission IN ('VIEW_ONLY','VIEW_EDIT'));
ALTER TABLE group_policies ADD CONSTRAINT chk_group_policies_effect CHECK (effect IN ('ALLOW','DENY'));
ALTER TABLE group_policies ADD CONSTRAINT fk_gp_group FOREIGN KEY (group_id) REFERENCES groups_(id) ON DELETE CASCADE;
ALTER TABLE group_policies ADD CONSTRAINT fk_gp_db FOREIGN KEY (database_id) REFERENCES databases(id) ON DELETE CASCADE;
ALTER TABLE group_policies ADD CONSTRAINT uq_group_database_policy UNIQUE (group_id, database_id);
CREATE INDEX IF NOT EXISTS idx_group_policies_database ON group_policies(database_id);
COMMIT;

-- Manual follow-up required: populate host, port, database_name, username, and
-- password_ciphertext for each pre-existing databases row. Do not copy plaintext
-- passwords into this table; use the application's encrypted credential workflow.
