-- Migration workflow & approval
--
-- Draft -> Pending Review -> Approved -> Pending (Queue) -> Running -> Success / Failed
--
-- Runs in the PLATFORM database, after backend/src/migration/storage.sql
-- (it references dmp_migration.definitions and dmp_migration.attempts).
-- Idempotent: safe to run on every startup.
--
-- Model
--   * "Draft" is dmp_migration.definitions.state = 'draft' (unchanged).
--   * Every execution (apply, rollback, forward) needs its own change request,
--     because rollback/forward are operations on a migration, not new definitions.
--   * A request needs one approval from the leader of EACH group that has
--     ViewOnly or ViewEdit access to the target database (a single group means
--     a single approval). The approver set is snapshotted at submit time by the
--     application; this schema stores and enforces it.
--   * The queue is the set of requests with status = 'queued', ordered per
--     target database. It lives here, not in a separate database, so state
--     changes, constraints and locks share one transaction.
--
-- Not enforced here (application responsibility):
--   * Computing the required groups/leaders at submit time.
--   * Re-validating permissions, leaders and sql_hash right before running.
--   * Self-approval rules (an author who is also a leader).
--   * The per-database advisory lock used by Repository.lock().

SELECT pg_advisory_xact_lock(781349, 2);

CREATE SCHEMA IF NOT EXISTS dmp_migration;

-- ---------------------------------------------------------------------------
-- Change requests: one per requested execution of a migration
-- ---------------------------------------------------------------------------
CREATE TABLE IF NOT EXISTS dmp_migration.change_requests (
    id            text PRIMARY KEY,
    seq           bigint GENERATED ALWAYS AS IDENTITY,          -- stable FIFO tiebreaker
    database_id   text NOT NULL,
    migration_id  text NOT NULL REFERENCES dmp_migration.definitions(id),
    operation     text NOT NULL CHECK (operation IN ('apply','rollback','forward')),
    -- SHA-256 (hex) of the SQL this request will run (sql_script for apply/forward,
    -- sql_down for rollback). Approval is only valid for exactly this SQL.
    sql_hash      text NOT NULL CHECK (sql_hash ~ '^[0-9a-f]{64}$'),
    requested_by  text NOT NULL,
    status        text NOT NULL DEFAULT 'pending_review' CHECK (status IN (
                      'pending_review',  -- waiting for group leaders
                      'approved',        -- every required leader approved
                      'queued',          -- waiting for its turn on the target database
                      'running',
                      'succeeded',
                      'failed',
                      'unknown',         -- outcome unresolved; mirrors attempts.status
                      'rejected',        -- a leader rejected it
                      'cancelled')),     -- withdrawn before it started running
    attempt_id    text REFERENCES dmp_migration.attempts(id),   -- set once execution begins
    reject_reason text NOT NULL DEFAULT '',   -- reviewer's reason (rejected) or requester's note (cancelled)
    result_code   text NOT NULL DEFAULT '',   -- machine code of the outcome, e.g. execution_failed
    result_message text NOT NULL DEFAULT '',  -- human-readable outcome
    created_at    timestamptz NOT NULL DEFAULT now(),
    updated_at    timestamptz NOT NULL DEFAULT now(),
    approved_at   timestamptz,
    queued_at     timestamptz,
    started_at    timestamptz,
    completed_at  timestamptz,
    CHECK (status NOT IN ('queued','running','succeeded','failed','unknown') OR approved_at IS NOT NULL),
    CHECK (status NOT IN ('queued','running','succeeded','failed','unknown') OR queued_at IS NOT NULL),
    CHECK (status NOT IN ('running','succeeded','failed','unknown') OR started_at IS NOT NULL),
    -- attempt_id is linked as soon as ExecuteOperation creates the attempt; a request can
    -- fail before that point (revalidation, ordering, unavailable target), so only
    -- outcomes that ran SQL require it.
    CHECK (status NOT IN ('succeeded','unknown') OR attempt_id IS NOT NULL),
    CHECK (status NOT IN ('succeeded','failed','rejected','cancelled') OR completed_at IS NOT NULL)
);

-- Older requests use their job ID as the correlation ID when read by a worker.
ALTER TABLE dmp_migration.change_requests
ADD COLUMN IF NOT EXISTS submission_correlation_id text NOT NULL DEFAULT '';

-- A migration can have only one open request per operation.
CREATE UNIQUE INDEX IF NOT EXISTS one_open_request_per_operation
ON dmp_migration.change_requests(migration_id, operation)
WHERE status IN ('pending_review','approved','queued','running','unknown');

-- At most one request runs (or is unresolved) per target database.
CREATE UNIQUE INDEX IF NOT EXISTS one_running_request_per_database
ON dmp_migration.change_requests(database_id)
WHERE status IN ('running','unknown');

-- Worker claim: oldest queued request per database.
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
    id                 text PRIMARY KEY,
    request_id         text NOT NULL REFERENCES dmp_migration.change_requests(id) ON DELETE CASCADE,
    group_id           text NOT NULL,
    group_name         text NOT NULL DEFAULT '',   -- snapshot, groups can be renamed or deleted
    required_leader_id text NOT NULL,        -- leader of the group when the request was submitted
    leader_username    text NOT NULL DEFAULT '',   -- snapshot for display
    decision           text NOT NULL DEFAULT 'pending' CHECK (decision IN ('pending','approved','rejected')),
    decided_by         text,                 -- normally = required_leader_id; differs if an admin overrides
    decided_at         timestamptz,
    comment            text NOT NULL DEFAULT '',
    created_at         timestamptz NOT NULL DEFAULT now(),
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
    OR NEW.requested_by IS DISTINCT FROM OLD.requested_by
    OR NEW.submission_correlation_id IS DISTINCT FROM OLD.submission_correlation_id THEN
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
