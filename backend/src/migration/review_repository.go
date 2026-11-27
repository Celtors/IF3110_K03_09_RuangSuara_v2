package migration

import (
	"context"
	"encoding/json"
	"log/slog"
	"time"

	"backend/internal/telemetry"

	"github.com/jackc/pgx/v5"
)


const requestSelect = `SELECT r.id,r.database_id,r.migration_id,d.name,r.operation,r.sql_hash,r.requested_by,r.status,
 r.attempt_id,r.reject_reason,r.result_code,r.result_message,r.created_at,r.updated_at,r.approved_at,r.queued_at,
 r.started_at,r.completed_at,r.submission_correlation_id,
 CASE WHEN r.status='queued' THEN (SELECT count(*)+1 FROM dmp_migration.change_requests q
   WHERE q.database_id=r.database_id AND q.status='queued' AND (q.queued_at,q.seq)<(r.queued_at,r.seq)) END
 FROM dmp_migration.change_requests r
 JOIN dmp_migration.definitions d ON d.id=r.migration_id `

func scanRequest(row pgx.Row) (ChangeRequest, error) {
	var q ChangeRequest
	err := row.Scan(&q.ID, &q.DatabaseID, &q.MigrationID, &q.MigrationName, &q.Operation, &q.SQLHash, &q.RequestedBy, &q.Status,
		&q.AttemptID, &q.RejectReason, &q.ResultCode, &q.ResultMessage, &q.CreatedAt, &q.UpdatedAt, &q.ApprovedAt, &q.QueuedAt,
		&q.StartedAt, &q.CompletedAt, &q.submissionCorrelationID, &q.QueuePosition)
	return q, err
}

func addEvent(ctx context.Context, tx pgx.Tx, requestID, event, actor string, detail map[string]any) error {
	if detail == nil {
		detail = map[string]any{}
	}
	raw, err := json.Marshal(detail)
	if err != nil {
		return err
	}
	_, err = tx.Exec(ctx, `INSERT INTO dmp_migration.change_request_events(request_id,event,actor,detail)
 VALUES ($1,$2,$3,$4::text::jsonb)`, requestID, event, actor, string(raw))
	return err
}

func (r *Repository) insertRequest(ctx context.Context, q ChangeRequest, approvers []RequiredApprover) (ChangeRequest, error) {
	ctx = telemetry.With(ctx, telemetry.ID("job_id", q.ID))
	tx, err := r.Pool.Begin(ctx)
	if err != nil {
		return q, err
	}
	defer rollback(ctx, tx)

	_, err = tx.Exec(ctx, `INSERT INTO dmp_migration.change_requests
 (id,database_id,migration_id,operation,sql_hash,requested_by,submission_correlation_id)
 VALUES ($1,$2,$3,$4,$5,$6,$7)`,
		q.ID, q.DatabaseID, q.MigrationID, q.Operation, q.SQLHash, q.RequestedBy, q.submissionCorrelationID)
	if err != nil {
		if postgresCode(err) == "23505" {
			return q, fault("conflict", "This migration already has an open change request for the same operation")
		}
		return q, err
	}
	if q.Batch != nil {
		if _, err = tx.Exec(ctx, `INSERT INTO dmp_migration.batch_jobs(id,attribute,batch_size,down_hash) VALUES($1,$2,$3,$4)`, q.ID, q.Batch.Attribute, q.Batch.Size, q.CompensationSQLHash); err != nil {
			return q, err
		}
	}
	for _, a := range approvers {
		_, err = tx.Exec(ctx, `INSERT INTO dmp_migration.change_request_approvals
 (id,request_id,group_id,group_name,required_leader_id,leader_username) VALUES ($1,$2,$3,$4,$5,$6)`,
			newID(), q.ID, a.GroupID, a.GroupName, a.LeaderID, a.LeaderUsername)
		if err != nil {
			return q, err
		}
	}
	if err = addEvent(ctx, tx, q.ID, "submitted", q.RequestedBy, map[string]any{
		"operation": q.Operation, "sql_hash": q.SQLHash, "required_groups": len(approvers),
	}); err != nil {
		return q, err
	}
	if err = tx.Commit(ctx); err != nil {
		return q, err
	}
	slog.InfoContext(ctx, "approval.submitted", "status", "pending_review", "required_groups", len(approvers))
	return r.GetRequest(ctx, q.DatabaseID, q.ID)
}

// queryRequests runs requestSelect + where and attaches each request's approvals.
func (r *Repository) queryRequests(ctx context.Context, where string, args ...any) ([]ChangeRequest, error) {
	rows, err := r.Pool.Query(ctx, requestSelect+where, args...)
	if err != nil {
		return nil, err
	}
	out := []ChangeRequest{}
	for rows.Next() {
		q, e := scanRequest(rows)
		if e != nil {
			rows.Close()
			return nil, e
		}
		out = append(out, q)
	}
	rows.Close()
	if err = rows.Err(); err != nil {
		return nil, err
	}
	if len(out) == 0 {
		return out, nil
	}
	ids := make([]string, len(out))
	for i := range out {
		ids[i] = out[i].ID
	}
	byRequest, err := r.approvalsFor(ctx, ids)
	if err != nil {
		return nil, err
	}
	for i := range out {
		out[i].Approvals = byRequest[out[i].ID]
		p, e := loadBatch(ctx, r.Pool, out[i].ID)
		if e == nil {
			out[i].Batch = &BatchSpec{p.Attribute, p.Size}
			out[i].CompensationSQLHash = p.DownHash
		} else if !isNoRows(e) {
			return nil, e
		}
		if out[i].Approvals == nil {
			out[i].Approvals = []Approval{}
		}
	}
	return out, nil
}

func (r *Repository) approvalsFor(ctx context.Context, requestIDs []string) (map[string][]Approval, error) {
	// The leader columns stored on an approval are a snapshot from submit time and go stale
	// when a group's leadership changes. A pending approval is shown with the group's
	// current leader, who is the one that may decide it; a decided approval is shown with
	// the person who actually decided, so the record stays accurate.
	rows, err := r.Pool.Query(ctx, `SELECT a.id,a.request_id,a.group_id,a.group_name,
 CASE WHEN a.decision='pending' THEN COALESCE(gl.leader_id::text,'') ELSE COALESCE(a.decided_by,a.required_leader_id) END,
 CASE WHEN a.decision='pending' THEN COALESCE(lu.username,'') ELSE COALESCE(du.username,a.leader_username) END,
 a.decision,a.decided_by,a.decided_at,a.comment
 FROM dmp_migration.change_request_approvals a
 LEFT JOIN group_leaders gl ON gl.group_id::text=a.group_id
 LEFT JOIN users lu ON lu.id=gl.leader_id
 LEFT JOIN users du ON du.id::text=a.decided_by
 WHERE a.request_id = ANY($1) ORDER BY a.group_name,a.id`, requestIDs)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := map[string][]Approval{}
	for rows.Next() {
		var a Approval
		if err = rows.Scan(&a.ID, &a.RequestID, &a.GroupID, &a.GroupName, &a.RequiredLeaderID, &a.LeaderUsername,
			&a.Decision, &a.DecidedBy, &a.DecidedAt, &a.Comment); err != nil {
			return nil, err
		}
		out[a.RequestID] = append(out[a.RequestID], a)
	}
	return out, rows.Err()
}

func (r *Repository) events(ctx context.Context, requestID string) ([]RequestEvent, error) {
	rows, err := r.Pool.Query(ctx, `SELECT id,event,actor,detail,created_at
 FROM dmp_migration.change_request_events WHERE request_id=$1 ORDER BY id`, requestID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []RequestEvent{}
	for rows.Next() {
		var e RequestEvent
		var raw []byte
		if err = rows.Scan(&e.ID, &e.Event, &e.Actor, &raw, &e.CreatedAt); err != nil {
			return nil, err
		}
		e.Detail = json.RawMessage(raw)
		out = append(out, e)
	}
	return out, rows.Err()
}

// GetRequest returns one request with its approvals and audit events.
func (r *Repository) GetRequest(ctx context.Context, db, id string) (ChangeRequest, error) {
	list, err := r.queryRequests(ctx, `WHERE r.database_id=$1 AND r.id=$2`, db, id)
	if err != nil {
		return ChangeRequest{}, err
	}
	if len(list) == 0 {
		return ChangeRequest{}, fault("not_found", "Change request not found")
	}
	q := list[0]
	q.Events, err = r.events(ctx, id)
	return q, err
}

// leaderOfGroup is a SQL condition, over an approval aliased "a", that holds when the
// user in the parameter currently leads the approval's group. Only a group's leader may
// decide for it; ordinary members may not. Leadership is read live, so a leader appointed
// after the request was submitted may decide it, and a leader who was replaced may not.
func leaderOfGroup(param string) string {
	return `EXISTS (SELECT 1 FROM group_leaders gl WHERE gl.group_id::text=a.group_id AND gl.leader_id::text=` + param + `)`
}

// groupsOf returns the ids of the groups a user leads, i.e. the groups they may decide for.
func (r *Repository) groupsOf(ctx context.Context, user string) (map[string]bool, error) {
	rows, err := r.Pool.Query(ctx, `SELECT group_id::text FROM group_leaders WHERE leader_id::text=$1`, user)
	if err != nil {
		return nil, fault("authorization_unavailable", "Group leadership could not be determined")
	}
	defer rows.Close()
	out := map[string]bool{}
	for rows.Next() {
		var id string
		if err = rows.Scan(&id); err != nil {
			return nil, err
		}
		out[id] = true
	}
	return out, rows.Err()
}

// decide records the caller's decision on every approval addressed to a group they lead. It returns true when this decision completed the review and moved the
// request into the queue.
func (r *Repository) decide(ctx context.Context, db, id, user string, approve bool, comment string, forbidSelf bool) (bool, error) {
	tx, err := r.Pool.Begin(ctx)
	if err != nil {
		return false, err
	}
	defer func() { rollback(ctx, tx) }()

	var status, requester, migrationID, correlationID string
	var operation Operation
	err = tx.QueryRow(ctx, `SELECT status,requested_by,migration_id,operation,submission_correlation_id
 FROM dmp_migration.change_requests WHERE id=$1 AND database_id=$2 FOR UPDATE`, id, db).
		Scan(&status, &requester, &migrationID, &operation, &correlationID)
	if isNoRows(err) {
		return false, fault("not_found", "Change request not found")
	}
	if err != nil {
		return false, err
	}
	if status != string(RequestPendingReview) {
		return false, fault("conflict", "This change request is no longer awaiting review")
	}
	if forbidSelf && requester == user {
		return false, fault("forbidden", "You cannot decide on a change request you submitted")
	}

	ctx = telemetry.With(ctx, telemetry.ID("migration_id", migrationID), slog.String("operation", string(operation)),
		telemetry.ID("submission_correlation_id", jobCorrelation(correlationID, id)))
	decision := "approved"
	if !approve {
		decision = "rejected"
	}
	tag, err := tx.Exec(ctx, `UPDATE dmp_migration.change_request_approvals a
 SET decision=$3,decided_by=$2,decided_at=now(),comment=$4
 WHERE a.request_id=$1 AND a.decision='pending' AND `+leaderOfGroup("$2"), id, user, decision, comment)
	if err != nil {
		return false, err
	}
	if tag.RowsAffected() == 0 {
		var owes bool
		if err = tx.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM dmp_migration.change_request_approvals a
 WHERE a.request_id=$1 AND `+leaderOfGroup("$2")+`)`, id, user).Scan(&owes); err != nil {
			return false, err
		}
		if owes {
			return false, fault("conflict", "Your group's approval for this change request has already been decided")
		}
		return false, fault("forbidden", "Only the leader of a group this request is waiting on can decide it")
	}

	if !approve {
		_, err = tx.Exec(ctx, `UPDATE dmp_migration.change_requests
 SET status='rejected',reject_reason=$2,completed_at=now() WHERE id=$1`, id, comment)
		if err != nil {
			return false, err
		}
		if err = addEvent(ctx, tx, id, "rejected", user, map[string]any{"comment": comment, "groups": tag.RowsAffected()}); err != nil {
			return false, err
		}
		if err = tx.Commit(ctx); err != nil {
			return false, err
		}
		slog.InfoContext(ctx, "approval.decided", "status", decision, "groups", tag.RowsAffected())
		return false, nil
	}

	if err = addEvent(ctx, tx, id, "approved", user, map[string]any{"comment": comment, "groups": tag.RowsAffected()}); err != nil {
		return false, err
	}
	var remaining int64
	if err = tx.QueryRow(ctx, `SELECT count(*) FROM dmp_migration.change_request_approvals
 WHERE request_id=$1 AND decision<>'approved'`, id).Scan(&remaining); err != nil {
		return false, err
	}
	queued := remaining == 0
	if queued {
		// Approved is a transient status: the final approval queues the request in the same transaction.
		if _, err = tx.Exec(ctx, `UPDATE dmp_migration.change_requests SET status='approved',approved_at=now() WHERE id=$1`, id); err != nil {
			return false, err
		}
		if _, err = tx.Exec(ctx, `UPDATE dmp_migration.change_requests SET status='queued',queued_at=now() WHERE id=$1`, id); err != nil {
			return false, err
		}
		if err = addEvent(ctx, tx, id, "queued", "", nil); err != nil {
			return false, err
		}
	}
	if err = tx.Commit(ctx); err != nil {
		return false, err
	}
	slog.InfoContext(ctx, "approval.decided", "status", decision, "groups", tag.RowsAffected())
	if queued {
		slog.InfoContext(ctx, "queue.submitted", "status", "queued")
	}
	return queued, nil
}

// cancel withdraws a request the caller submitted, before it starts running.
func (r *Repository) cancel(ctx context.Context, db, id, user, reason string) error {
	tx, err := r.Pool.Begin(ctx)
	if err != nil {
		return err
	}
	defer rollback(ctx, tx)

	var status, requester string
	err = tx.QueryRow(ctx, `SELECT status,requested_by FROM dmp_migration.change_requests
 WHERE id=$1 AND database_id=$2 FOR UPDATE`, id, db).Scan(&status, &requester)
	if isNoRows(err) {
		return fault("not_found", "Change request not found")
	}
	if err != nil {
		return err
	}
	if requester != user {
		return fault("forbidden", "Only the person who submitted a change request can cancel it")
	}
	switch RequestStatus(status) {
	case RequestPendingReview, RequestApproved, RequestQueued:
	default:
		return fault("conflict", "A change request that is running or finished cannot be cancelled")
	}
	if _, err = tx.Exec(ctx, `UPDATE dmp_migration.change_requests
 SET status='cancelled',reject_reason=$2,completed_at=now() WHERE id=$1`, id, reason); err != nil {
		return err
	}
	if err = addEvent(ctx, tx, id, "cancelled", user, map[string]any{"comment": reason}); err != nil {
		return err
	}
	return tx.Commit(ctx)
}


const claimSQL = `SELECT r.id,r.database_id,r.migration_id,r.operation,r.sql_hash,r.requested_by,r.submission_correlation_id
 FROM dmp_migration.change_requests r
 WHERE r.status='queued'
   AND r.id=(SELECT q.id FROM dmp_migration.change_requests q
             WHERE q.database_id=r.database_id AND q.status='queued' ORDER BY q.queued_at,q.seq LIMIT 1)
   AND NOT EXISTS (SELECT 1 FROM dmp_migration.change_requests x
                   WHERE x.database_id=r.database_id AND x.status IN ('running','unknown'))
   AND NOT EXISTS (SELECT 1 FROM dmp_migration.attempts t
                   WHERE t.database_id=r.database_id AND t.status IN ('running','unknown'))
 ORDER BY r.queued_at,r.seq
 LIMIT 1
 FOR UPDATE OF r SKIP LOCKED`


func (r *Repository) claim(ctx context.Context, validate func(context.Context, QueuedRequest) (bool, string, string, error)) (*QueuedRequest, error) {
	tx, err := r.Pool.Begin(ctx)
	if err != nil {
		return nil, err
	}
	defer func() { rollback(ctx, tx) }()

	var q QueuedRequest
	err = tx.QueryRow(ctx, claimSQL).Scan(&q.ID, &q.DatabaseID, &q.MigrationID, &q.Operation, &q.SQLHash, &q.RequestedBy, &q.submissionCorrelationID)
	if isNoRows(err) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}

	ctx = q.logContext(ctx)
	ok, code, message, err := validate(ctx, q)
	if err != nil {
		slog.LogAttrs(ctx, slog.LevelWarn, "queue.claim", append(telemetry.ErrorAttrs(err), slog.String("status", "retry_pending"))...)
		return nil, err
	}
	if !ok {
		if _, err = tx.Exec(ctx, `UPDATE dmp_migration.change_requests
 SET status='cancelled',result_code=$2,result_message=$3,completed_at=now() WHERE id=$1`, q.ID, code, message); err != nil {
			return nil, err
		}
		if err = addEvent(ctx, tx, q.ID, "cancelled", "", map[string]any{"code": code, "message": message}); err != nil {
			return nil, err
		}
		if err = tx.Commit(ctx); err != nil {
			return nil, err
		}
		slog.WarnContext(ctx, "queue.claim", "status", "cancelled", "reason", code)
		return nil, nil
	}

	if err = tx.QueryRow(ctx, `UPDATE dmp_migration.change_requests SET status='running',started_at=now() WHERE id=$1 RETURNING started_at`, q.ID).Scan(&q.startedAt); err != nil {
		if postgresCode(err) == "23505" { // another worker started this database first
			return nil, nil
		}
		return nil, err
	}
	if err = addEvent(ctx, tx, q.ID, "started", "", nil); err != nil {
		return nil, err
	}
	if err = tx.Commit(ctx); err != nil {
		return nil, err
	}
	slog.InfoContext(ctx, "queue.claim", "status", "running")
	return &q, nil
}

// linkAttempt records which execution attempt belongs to a request.
func (r *Repository) linkAttempt(ctx context.Context, requestID, attemptID string) error {
	_, err := r.Pool.Exec(ctx, `UPDATE dmp_migration.change_requests SET attempt_id=$2 WHERE id=$1 AND attempt_id IS NULL`, requestID, attemptID)
	return err
}

// finishRequest records the outcome of a running or unresolved request.
func (r *Repository) finishRequest(ctx context.Context, id string, status RequestStatus, code, message, attemptID string, claimTime ...time.Time) error {
	tx, err := r.Pool.Begin(ctx)
	if err != nil {
		return err
	}
	defer rollback(ctx, tx)

	var attempt *string
	if attemptID != "" {
		attempt = &attemptID
	}
	var generation *time.Time
	if len(claimTime) > 0 {
		generation = &claimTime[0]
	}
	tag, err := tx.Exec(ctx, `UPDATE dmp_migration.change_requests
 SET status=$2,result_code=$3,result_message=$4,attempt_id=COALESCE(attempt_id,$5),
     completed_at=CASE WHEN $2='unknown' THEN NULL ELSE now() END
 WHERE id=$1 AND status IN ('running','unknown') AND ($6::timestamptz IS NULL OR started_at=$6)`, id, status, code, message, attempt, generation)
	if err != nil {
		return err
	}
	if tag.RowsAffected() == 0 {
		return nil // already finished by another worker
	}
	if err = addEvent(ctx, tx, id, "finished", "", map[string]any{"status": status, "code": code, "message": message}); err != nil {
		return err
	}
	if err = tx.Commit(ctx); err != nil {
		return err
	}
	slog.InfoContext(ctx, "queue.completed", "status", status, "reason", code)
	return nil
}


func (r *Repository) attemptFor(ctx context.Context, q InFlightRequest) (id, status, code, message string, err error) {
	err = r.Pool.QueryRow(ctx, `SELECT id,status,postgres_error_code,error_message
 FROM dmp_migration.attempts
 WHERE migration_id=$1 AND operation=$2 AND started_at >= $3
 ORDER BY started_at DESC,id DESC LIMIT 1`, q.MigrationID, q.Operation, q.StartedAt).Scan(&id, &status, &code, &message)
	if isNoRows(err) {
		return "", "", "", "", nil
	}
	return
}


type InFlightRequest struct {
	submissionCorrelationID string
	ID                      string
	DatabaseID              string
	MigrationID             string
	Operation               Operation
	RequestedBy             string
	Status                  RequestStatus
	StartedAt               time.Time
}


func (r *Repository) inFlight(ctx context.Context, orphanAfter time.Duration) ([]InFlightRequest, error) {
	rows, err := r.Pool.Query(ctx, `SELECT id,database_id,migration_id,operation,requested_by,status,started_at,submission_correlation_id
 FROM dmp_migration.change_requests
 WHERE (status='unknown' OR (status='running' AND started_at < now() - make_interval(secs => $1::float8))) AND NOT EXISTS(SELECT 1 FROM dmp_migration.batch_jobs b WHERE b.id=change_requests.id)
 ORDER BY started_at`, orphanAfter.Seconds())
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []InFlightRequest{}
	for rows.Next() {
		var q InFlightRequest
		if err = rows.Scan(&q.ID, &q.DatabaseID, &q.MigrationID, &q.Operation, &q.RequestedBy, &q.Status, &q.StartedAt, &q.submissionCorrelationID); err != nil {
			return nil, err
		}
		out = append(out, q)
	}
	return out, rows.Err()
}


func (r *Repository) blockedQueues(ctx context.Context) ([]QueuedRequest, error) {
	rows, err := r.Pool.Query(ctx, `SELECT DISTINCT ON (r.database_id) r.id,r.database_id,r.migration_id,r.operation,r.sql_hash,r.requested_by,r.submission_correlation_id
 FROM dmp_migration.change_requests r
 WHERE r.status='queued'
   AND EXISTS (SELECT 1 FROM dmp_migration.attempts t WHERE t.database_id=r.database_id AND t.status IN ('running','unknown')) AND NOT EXISTS(SELECT 1 FROM dmp_migration.change_requests x JOIN dmp_migration.batch_jobs b ON b.id=x.id WHERE x.database_id=r.database_id AND x.status='running')
 ORDER BY r.database_id,r.queued_at,r.seq`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []QueuedRequest{}
	for rows.Next() {
		var q QueuedRequest
		if err = rows.Scan(&q.ID, &q.DatabaseID, &q.MigrationID, &q.Operation, &q.SQLHash, &q.RequestedBy, &q.submissionCorrelationID); err != nil {
			return nil, err
		}
		out = append(out, q)
	}
	return out, rows.Err()
}

// approvalDecisions returns the decisions stored for one request.
func (r *Repository) approvalDecisions(ctx context.Context, requestID string) ([]Approval, error) {
	m, err := r.approvalsFor(ctx, []string{requestID})
	return m[requestID], err
}
