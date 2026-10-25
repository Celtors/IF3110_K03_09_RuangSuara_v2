CREATE EXTENSION IF NOT EXISTS pgcrypto;

CREATE TABLE IF NOT EXISTS users (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    username VARCHAR(20) NOT NULL UNIQUE,
    password_hash VARCHAR(100) NOT NULL,
    status VARCHAR(10) NOT NULL DEFAULT 'Active',
    created_at TIMESTAMPTZ NOT NULL DEFAULT CURRENT_TIMESTAMP,
    updated_at TIMESTAMPTZ NOT NULL DEFAULT CURRENT_TIMESTAMP
);
CREATE TABLE IF NOT EXISTS groups_ (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    name VARCHAR(50) NOT NULL UNIQUE,
    description TEXT,
    created_at TIMESTAMPTZ NOT NULL DEFAULT CURRENT_TIMESTAMP,
    updated_at TIMESTAMPTZ NOT NULL DEFAULT CURRENT_TIMESTAMP
);
CREATE TABLE IF NOT EXISTS user_groups (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    user_id UUID NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    group_id UUID NOT NULL REFERENCES groups_(id) ON DELETE CASCADE,
    created_at TIMESTAMPTZ NOT NULL DEFAULT CURRENT_TIMESTAMP,
    UNIQUE (user_id, group_id)
);
CREATE TABLE IF NOT EXISTS databases (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    name VARCHAR(100) NOT NULL,
    host VARCHAR(253) NOT NULL,
    port INTEGER NOT NULL CHECK (port BETWEEN 1 AND 65535),
    database_name VARCHAR(128) NOT NULL,
    username VARCHAR(128) NOT NULL,
    password_ciphertext TEXT NOT NULL,
    ssl_mode VARCHAR(32) NOT NULL DEFAULT 'verify-full',
    created_at TIMESTAMPTZ NOT NULL DEFAULT CURRENT_TIMESTAMP,
    updated_at TIMESTAMPTZ NOT NULL DEFAULT CURRENT_TIMESTAMP
);
CREATE TABLE IF NOT EXISTS group_policies (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    group_id UUID NOT NULL REFERENCES groups_(id) ON DELETE CASCADE,
    database_id UUID NOT NULL REFERENCES databases(id) ON DELETE CASCADE,
    effect VARCHAR(10) NOT NULL DEFAULT 'ViewOnly' CHECK (effect IN ('ViewEdit','ViewOnly','Blocked')),
    created_at TIMESTAMPTZ NOT NULL DEFAULT CURRENT_TIMESTAMP,
    CONSTRAINT uq_group_database_policy UNIQUE (group_id, database_id)
);
CREATE TABLE IF NOT EXISTS sso_sessions (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    user_id UUID NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    session_token_hash VARCHAR(64) NOT NULL UNIQUE,
    status VARCHAR(10) NOT NULL,
    created_at TIMESTAMPTZ NOT NULL DEFAULT CURRENT_TIMESTAMP,
    expires_at TIMESTAMPTZ NOT NULL,
    last_activity_at TIMESTAMPTZ,
    revoked_at TIMESTAMPTZ,
    revoked_reason VARCHAR(100),
    ip_address VARCHAR(50),
    user_agent TEXT
);
CREATE TABLE IF NOT EXISTS access_logs (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    event_type VARCHAR(50) NOT NULL,
    database_id UUID REFERENCES databases(id) ON DELETE SET NULL,
    user_id UUID REFERENCES users(id) ON DELETE SET NULL,
    session_id UUID REFERENCES sso_sessions(id) ON DELETE SET NULL,
    result VARCHAR(50) NOT NULL,
    created_at TIMESTAMP DEFAULT CURRENT_TIMESTAMP NOT NULL,
    CONSTRAINT fk_audit_db
        FOREIGN KEY (database_id)
        REFERENCES databases(id)
        ON DELETE CASCADE,
    CONSTRAINT fk_audit_user
        FOREIGN KEY (user_id)
        REFERENCES users(id)
        ON DELETE CASCADE,
    CONSTRAINT fk_audit_sso
        FOREIGN KEY (session_id)
        REFERENCES sso_sessions(id)
        ON DELETE CASCADE
);

CREATE INDEX IF NOT EXISTS idx_user_groups_user ON user_groups(user_id);
CREATE INDEX IF NOT EXISTS idx_user_groups_group ON user_groups(group_id);
CREATE INDEX IF NOT EXISTS idx_group_policies_database ON group_policies(database_id);
CREATE INDEX IF NOT EXISTS idx_sessions_token_status ON sso_sessions(session_token_hash, status);

-- =====================================================================
-- Seed data (development only)
-- Every seeded user has the password: password123
-- (bcrypt hash below, same format the backend's HashPassword produces)
-- Fixed UUIDs keep the rows below readable and the script re-runnable by hand.
-- =====================================================================

INSERT INTO users (id, username, password_hash, status) VALUES
    ('11111111-1111-4111-8111-000000000001', 'admin', '$2a$10$nAMioM3saQ8..2oxIApiiudD4z3oJcyH0TKWE4pwZTDCrEkClCjVq', 'Active'),
    ('11111111-1111-4111-8111-000000000002', 'alice', '$2a$10$nAMioM3saQ8..2oxIApiiudD4z3oJcyH0TKWE4pwZTDCrEkClCjVq', 'Active'),
    ('11111111-1111-4111-8111-000000000003', 'bob',   '$2a$10$nAMioM3saQ8..2oxIApiiudD4z3oJcyH0TKWE4pwZTDCrEkClCjVq', 'Active'),
    ('11111111-1111-4111-8111-000000000004', 'carol', '$2a$10$nAMioM3saQ8..2oxIApiiudD4z3oJcyH0TKWE4pwZTDCrEkClCjVq', 'Active'),
    ('11111111-1111-4111-8111-000000000005', 'dave',  '$2a$10$nAMioM3saQ8..2oxIApiiudD4z3oJcyH0TKWE4pwZTDCrEkClCjVq', 'Inactive');

INSERT INTO groups_ (id, name, description) VALUES
    ('22222222-2222-4222-8222-000000000001', 'platform-admins', 'Manage users, groups and database access'),
    ('22222222-2222-4222-8222-000000000002', 'data-engineers',  'Build and run migrations on production data'),
    ('22222222-2222-4222-8222-000000000003', 'analysts',        'Read-only access to reporting data'),
    ('22222222-2222-4222-8222-000000000004', 'auditors',        'Review schema and migration history'),
    ('22222222-2222-4222-8222-000000000005', 'interns',         'Not granted any database yet');

INSERT INTO user_groups (user_id, group_id) VALUES
    ('11111111-1111-4111-8111-000000000001', '22222222-2222-4222-8222-000000000001'), -- admin  -> platform-admins
    ('11111111-1111-4111-8111-000000000002', '22222222-2222-4222-8222-000000000002'), -- alice  -> data-engineers
    ('11111111-1111-4111-8111-000000000002', '22222222-2222-4222-8222-000000000003'), -- alice  -> analysts
    ('11111111-1111-4111-8111-000000000003', '22222222-2222-4222-8222-000000000003'), -- bob    -> analysts
    ('11111111-1111-4111-8111-000000000004', '22222222-2222-4222-8222-000000000004'), -- carol  -> auditors
    ('11111111-1111-4111-8111-000000000005', '22222222-2222-4222-8222-000000000005'); -- dave   -> interns

-- Connection details are placeholders so the rows satisfy the NOT NULL columns.
-- password_ciphertext is NOT a real encrypted credential: set a real host and password
-- through PATCH /api/admin/databases/:id (which encrypts it) before using "test connection"
-- or the database viewer on these rows.
INSERT INTO databases (id, name, host, port, database_name, username, password_ciphertext) VALUES
    ('33333333-3333-4333-8333-000000000001', 'orders_db',    'db.example.test', 5432, 'orders',    'reader', 'seed-placeholder'),
    ('33333333-3333-4333-8333-000000000002', 'analytics_db', 'db.example.test', 5432, 'analytics', 'reader', 'seed-placeholder'),
    ('33333333-3333-4333-8333-000000000003', 'users_db',     'db.example.test', 5432, 'users',     'reader', 'seed-placeholder');


INSERT INTO group_policies (group_id, database_id, effect) VALUES
    ('22222222-2222-4222-8222-000000000001', '33333333-3333-4333-8333-000000000003', 'ViewEdit'), -- platform-admins -> users_db
    ('22222222-2222-4222-8222-000000000002', '33333333-3333-4333-8333-000000000001', 'ViewEdit'), -- data-engineers  -> orders_db
    ('22222222-2222-4222-8222-000000000003', '33333333-3333-4333-8333-000000000002', 'ViewOnly'), -- analysts        -> analytics_db
    ('22222222-2222-4222-8222-000000000004', '33333333-3333-4333-8333-000000000001', 'ViewOnly'); -- auditors        -> orders_db
