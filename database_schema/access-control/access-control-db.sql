CREATE TABLE users(
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    username VARCHAR(20) NOT NULL UNIQUE,
    password_hash VARCHAR(64) NOT NULL,
    status VARCHAR(10) NOT NULL,
    created_at TIMESTAMP DEFAULT CURRENT_TIMESTAMP NOT NULL,
    updated_at TIMESTAMP DEFAULT CURRENT_TIMESTAMP NOT NULL
);

CREATE TABLE groups_(
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    name VARCHAR(50) NOT NULL UNIQUE,
    description TEXT,
    created_at TIMESTAMP DEFAULT CURRENT_TIMESTAMP NOT NULL,
    updated_at TIMESTAMP DEFAULT CURRENT_TIMESTAMP NOT NULL
);

CREATE TABLE user_groups(
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    user_id UUID NOT NULL,
    group_id UUID NOT NULL,
    created_at TIMESTAMP DEFAULT CURRENT_TIMESTAMP NOT NULL,
    CONSTRAINT fk_ug_user
        FOREIGN KEY (user_id)
        REFERENCES users(id)
        ON DELETE CASCADE,
    CONSTRAINT fk_ug_group
        FOREIGN KEY (group_id)
        REFERENCES groups_(id)
        ON DELETE CASCADE,
    UNIQUE (user_id, group_id) 
);

CREATE TABLE databases(
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    name string NOT NULL,
    created_at TIMESTAMP DEFAULT CURRENT_TIMESTAMP NOT NULL,
    updated_at TIMESTAMP DEFAULT CURRENT_TIMESTAMP NOT NULL
);

CREATE TABLE group_policies(
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    group_id VARCHAR(50) NOT NULL UNIQUE NOT NULL,
    database_id UUID NOT NULL,
    effect VARCHAR(10) NOT NULL,
    created_at TIMESTAMP DEFAULT CURRENT_TIMESTAMP NOT NULL,
    CONSTRAINT fk_gp_group
        FOREIGN KEY (group_id)
        REFERENCES groups_(id)
        ON DELETE CASCADE,
    CONSTRAINT fk_gp_db
        FOREIGN KEY (database_id)
        REFERENCES databases(id)
        ON DELETE CASCADE,
    UNIQUE (policy, group_id) 
);


CREATE TABLE sso_sessions(
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    user_id UUID NOT NULL,
    session_token_hash VARCHAR(64) NOT NULL,
    status VARCHAR(10) NOT NULL,
    created_at TIMESTAMP DEFAULT CURRENT_TIMESTAMP NOT NULL,
    expires_at TIMESTAMP NOT NULL,
    last_activity_at TIMESTAMP, 
    revoked_at TIMESTAMP,
    revoked_reason VARCHAR(100),
    ip_address VARCHAR(50),
    user_agent TEXT,
    CONSTRAINT fk_sso_user
        FOREIGN KEY (user_id)
        REFERENCES users(id)
        ON DELETE CASCADE
);

CREATE TABLE access_logs(
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    event_type VARCHAR(50) NOT NULL,
    database_id UUID NOT NULL,
    user_id UUID NOT NULL,
    session_id UUID NOT NULL,
    result VARCHAR(50) NOT NULL,
    created_at DATETIME DEFAULT CURRENT_TIMESTAMP NOT NULL,
    CONSTRAINT fk_audit_actor
        FOREIGN KEY (actor_id)
        REFERENCES users(id)
        ON DELETE CASCADE,
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
)