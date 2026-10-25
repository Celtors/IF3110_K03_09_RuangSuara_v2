BEGIN;
ALTER TABLE databases ADD COLUMN IF NOT EXISTS dbms VARCHAR(16) NOT NULL DEFAULT 'postgres';
ALTER TABLE databases DROP CONSTRAINT IF EXISTS chk_databases_dbms;
ALTER TABLE databases ADD CONSTRAINT chk_databases_dbms CHECK (dbms IN ('postgres','mariadb'));
COMMIT;
