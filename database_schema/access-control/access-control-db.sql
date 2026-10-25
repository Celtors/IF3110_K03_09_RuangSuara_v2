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
    permission VARCHAR(20) NOT NULL DEFAULT 'VIEW_ONLY' CHECK (permission IN ('VIEW_ONLY','VIEW_EDIT')),
    effect VARCHAR(10) NOT NULL DEFAULT 'ALLOW' CHECK (effect IN ('ALLOW','DENY')),
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
    created_at TIMESTAMPTZ NOT NULL DEFAULT CURRENT_TIMESTAMP
);
CREATE INDEX IF NOT EXISTS idx_user_groups_user ON user_groups(user_id);
CREATE INDEX IF NOT EXISTS idx_user_groups_group ON user_groups(group_id);
CREATE INDEX IF NOT EXISTS idx_group_policies_database ON group_policies(database_id);
CREATE INDEX IF NOT EXISTS idx_sessions_token_status ON sso_sessions(session_token_hash, status);
