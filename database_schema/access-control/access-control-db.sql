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
    name VARCHAR(50) NOT NULL,
    created_at TIMESTAMP DEFAULT CURRENT_TIMESTAMP NOT NULL,
    updated_at TIMESTAMP DEFAULT CURRENT_TIMESTAMP NOT NULL
);

CREATE TABLE group_policies(
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    group_id UUID NOT NULL UNIQUE NOT NULL,
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
    UNIQUE (database_id, group_id) 
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

INSERT INTO databases (id, name) VALUES
    ('33333333-3333-4333-8333-000000000001', 'orders_db'),
    ('33333333-3333-4333-8333-000000000002', 'analytics_db'),
    ('33333333-3333-4333-8333-000000000003', 'users_db');


INSERT INTO group_policies (group_id, database_id, effect) VALUES
    ('22222222-2222-4222-8222-000000000001', '33333333-3333-4333-8333-000000000003', 'ViewEdit'), -- platform-admins -> users_db
    ('22222222-2222-4222-8222-000000000002', '33333333-3333-4333-8333-000000000001', 'ViewEdit'), -- data-engineers  -> orders_db
    ('22222222-2222-4222-8222-000000000003', '33333333-3333-4333-8333-000000000002', 'ViewOnly'), -- analysts        -> analytics_db
    ('22222222-2222-4222-8222-000000000004', '33333333-3333-4333-8333-000000000001', 'ViewOnly'); -- auditors        -> orders_db