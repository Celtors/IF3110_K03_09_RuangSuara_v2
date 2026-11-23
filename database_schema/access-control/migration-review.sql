CREATE TABLE IF NOT EXISTS dmp_migration.change_requests (
    id            TEXT PRIMARY KEY,
    seq           BIGINT GENERATED ALWAYS AS IDENTITY,
    database_id   TEXT NOT NULL,
    migration_id  TEXT NOT NULL REFERENCES dmp_migration.definitions(id),
    operation     TEXT NOT NULL CHECK (operation IN ('apply','rollback','forward')),
    sql_hash      TEXT NOT NULL CHECK (sql_hash ~ '^[0-9a-f]{64}$'),
    requested_by  TEXT NOT NULL,
    status        TEXT NOT NULL DEFAULT 'pending_review' CHECK (status IN (
                      'pending_review',
                      'approved',
                      'queued',          
                      'running',
                      'succeeded',
                      'failed',
                      'unknown',        
                      'rejected', 
                      'cancelled')),
    attempt_id    TEXT REFERENCES dmp_migration.attempts(id),
    reject_reason TEXT NOT NULL DEFAULT '',
    created_at    TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at    TIMESTAMPTZ NOT NULL DEFAULT now(),
    approved_at   TIMESTAMPTZ,
    queued_at     TIMESTAMPTZ,
    started_at    TIMESTAMPTZ,
    completed_at  TIMESTAMPTZ,
    CHECK (status NOT IN ('queued','running','succeeded','failed','unknown') OR approved_at IS NOT NULL),
    CHECK (status NOT IN ('queued','running','succeeded','failed','unknown') OR queued_at IS NOT NULL),
    CHECK (status NOT IN ('running','succeeded','failed','unknown') OR (started_at IS NOT NULL AND attempt_id IS NOT NULL)),
    CHECK (status NOT IN ('succeeded','failed','rejected','cancelled') OR completed_at IS NOT NULL)
);


CREATE UNIQUE INDEX IF NOT EXISTS one_open_request_per_operation
ON dmp_migration.change_requests(migration_id, operation)
WHERE status IN ('pending_review','approved','queued','running','unknown');

CREATE UNIQUE INDEX IF NOT EXISTS one_running_request_per_database
ON dmp_migration.change_requests(database_id)
WHERE status IN ('running','unknown');


CREATE INDEX IF NOT EXISTS change_requests_queue
ON dmp_migration.change_requests(database_id, queued_at, seq)
WHERE status = 'queued';

CREATE INDEX IF NOT EXISTS change_requests_database
ON dmp_migration.change_requests(database_id, created_at DESC);

CREATE INDEX IF NOT EXISTS change_requests_migration
ON dmp_migration.change_requests(migration_id, created_at DESC);

-- ---------------------------------------------------------------------------
-- Approvals: one row per required group, naming the leader who must decide
-- ---------------------------------------------------------------------------
-- group_id / leader ids are plain text (like author_user_id), with no foreign
-- keys into the access-control tables, so this schema stays independent of them.
CREATE TABLE IF NOT EXISTS dmp_migration.change_request_approvals (
    id                 TEXT PRIMARY KEY,
    request_id         TEXT NOT NULL REFERENCES dmp_migration.change_requests(id) ON DELETE CASCADE,
    group_id           TEXT NOT NULL,
    required_leader_id TEXT NOT NULL,
    decision           TEXT NOT NULL DEFAULT 'pending' CHECK (decision IN ('pending','approved','rejected')),
    decided_by         TEXT,                 
    decided_at         TIMESTAMPTZ,
    comment            TEXT NOT NULL DEFAULT '',
    created_at         TIMESTAMPTZ NOT NULL DEFAULT now(),
    UNIQUE (request_id, group_id),
    CHECK ((decision = 'pending') = (decided_at IS NULL AND decided_by IS NULL))
);

-- "My pending approvals" inbox for a leader.
CREATE INDEX IF NOT EXISTS approvals_leader_pending
ON dmp_migration.change_request_approvals(required_leader_id)
WHERE decision = 'pending';

-- ---------------------------------------------------------------------------
-- Append-only audit trail
-- ---------------------------------------------------------------------------
CREATE TABLE IF NOT EXISTS dmp_migration.change_request_events (
    id          bigint GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
    request_id  text NOT NULL REFERENCES dmp_migration.change_requests(id) ON DELETE CASCADE,
    event       text NOT NULL,               -- e.g. submitted, approved, rejected, queued, started, finished
    actor       text NOT NULL DEFAULT '',    -- user id, or '' for the worker
    detail      jsonb NOT NULL DEFAULT '{}'::jsonb,
    created_at  timestamptz NOT NULL DEFAULT now()
);

CREATE INDEX IF NOT EXISTS change_request_events_request
ON dmp_migration.change_request_events(request_id, id);

-- ---------------------------------------------------------------------------
-- Guards
-- ---------------------------------------------------------------------------

-- Allowed status transitions, and "approved" only once every approval is in.
CREATE OR REPLACE FUNCTION dmp_migration.guard_change_request() RETURNS trigger AS $$
BEGIN
    IF NEW.database_id  IS DISTINCT FROM OLD.database_id
    OR NEW.migration_id IS DISTINCT FROM OLD.migration_id
    OR NEW.operation    IS DISTINCT FROM OLD.operation
    OR NEW.sql_hash     IS DISTINCT FROM OLD.sql_hash
    OR NEW.requested_by IS DISTINCT FROM OLD.requested_by THEN
        RAISE EXCEPTION 'change request % is immutable except for its status fields', OLD.id
            USING ERRCODE = 'check_violation';
    END IF;

    IF NEW.status IS DISTINCT FROM OLD.status THEN
        IF NOT (
               (OLD.status = 'pending_review' AND NEW.status IN ('approved','rejected','cancelled'))
            OR (OLD.status = 'approved'       AND NEW.status IN ('queued','cancelled'))
            OR (OLD.status = 'queued'         AND NEW.status IN ('running','cancelled'))
            OR (OLD.status = 'running'        AND NEW.status IN ('succeeded','failed','unknown'))
            OR (OLD.status = 'unknown'        AND NEW.status IN ('succeeded','failed'))
        ) THEN
            RAISE EXCEPTION 'invalid change request transition % -> %', OLD.status, NEW.status
                USING ERRCODE = 'check_violation';
        END IF;

        IF NEW.status = 'approved' AND (
               NOT EXISTS (SELECT 1 FROM dmp_migration.change_request_approvals a WHERE a.request_id = OLD.id)
            OR EXISTS (SELECT 1 FROM dmp_migration.change_request_approvals a
                       WHERE a.request_id = OLD.id AND a.decision <> 'approved')
        ) THEN
            RAISE EXCEPTION 'change request % needs an approval from every required group leader', OLD.id
                USING ERRCODE = 'check_violation';
        END IF;
    END IF;

    NEW.updated_at := now();
    RETURN NEW;
END $$ LANGUAGE plpgsql;

DROP TRIGGER IF EXISTS change_requests_guard ON dmp_migration.change_requests;
CREATE TRIGGER change_requests_guard
BEFORE UPDATE ON dmp_migration.change_requests
FOR EACH ROW EXECUTE FUNCTION dmp_migration.guard_change_request();

-- A decision is final, and can only be made while the request awaits review.
CREATE OR REPLACE FUNCTION dmp_migration.guard_approval() RETURNS trigger AS $$
DECLARE parent_status text;
BEGIN
    IF NEW.request_id IS DISTINCT FROM OLD.request_id
    OR NEW.group_id IS DISTINCT FROM OLD.group_id
    OR NEW.required_leader_id IS DISTINCT FROM OLD.required_leader_id THEN
        RAISE EXCEPTION 'approval % cannot change its request, group or required leader', OLD.id
            USING ERRCODE = 'check_violation';
    END IF;
    IF OLD.decision <> 'pending' THEN
        RAISE EXCEPTION 'approval % has already been decided', OLD.id
            USING ERRCODE = 'check_violation';
    END IF;
    SELECT status INTO parent_status FROM dmp_migration.change_requests WHERE id = OLD.request_id;
    IF parent_status <> 'pending_review' THEN
        RAISE EXCEPTION 'change request % is no longer awaiting review', OLD.request_id
            USING ERRCODE = 'check_violation';
    END IF;
    RETURN NEW;
END $$ LANGUAGE plpgsql;

DROP TRIGGER IF EXISTS change_request_approvals_guard ON dmp_migration.change_request_approvals;
CREATE TRIGGER change_request_approvals_guard
BEFORE UPDATE ON dmp_migration.change_request_approvals
FOR EACH ROW EXECUTE FUNCTION dmp_migration.guard_approval();

-- Approvals are only created together with the request, while it awaits review.
CREATE OR REPLACE FUNCTION dmp_migration.guard_approval_insert() RETURNS trigger AS $$
DECLARE parent_status text;
BEGIN
    SELECT status INTO parent_status FROM dmp_migration.change_requests WHERE id = NEW.request_id;
    IF parent_status IS DISTINCT FROM 'pending_review' THEN
        RAISE EXCEPTION 'approvers can only be added while change request % awaits review', NEW.request_id
            USING ERRCODE = 'check_violation';
    END IF;
    RETURN NEW;
END $$ LANGUAGE plpgsql;

DROP TRIGGER IF EXISTS change_request_approvals_insert_guard ON dmp_migration.change_request_approvals;
CREATE TRIGGER change_request_approvals_insert_guard
BEFORE INSERT ON dmp_migration.change_request_approvals
FOR EACH ROW EXECUTE FUNCTION dmp_migration.guard_approval_insert();

-- Audit rows are append-only.
CREATE OR REPLACE FUNCTION dmp_migration.forbid_event_change() RETURNS trigger AS $$
BEGIN
    RAISE EXCEPTION 'change_request_events is append-only' USING ERRCODE = 'check_violation';
END $$ LANGUAGE plpgsql;

DROP TRIGGER IF EXISTS change_request_events_append_only ON dmp_migration.change_request_events;
CREATE TRIGGER change_request_events_append_only
BEFORE UPDATE OR DELETE ON dmp_migration.change_request_events
FOR EACH ROW EXECUTE FUNCTION dmp_migration.forbid_event_change();