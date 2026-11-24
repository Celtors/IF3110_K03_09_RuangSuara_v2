package migration

import (
	"context"
	"encoding/json"
	"time"

	"github.com/jackc/pgx/v5"
)

// Every write to the workflow tables goes through this file. Status changes are
// additionally checked by triggers in review.sql, so an application bug cannot,
// for example, queue a request that is missing an approval.

const requestSelect = `SELECT r.id,r.database_id,r.migration_id,d.name,r.operation,r.sql_hash,r.requested_by,r.status,
 r.attempt_id,r.reject_reason,r.result_code,r.result_message,r.created_at,r.updated_at,r.approved_at,r.queued_at,
 r.started_at,r.completed_at,
 CASE WHEN r.status='queued' THEN (SELECT count(*)+1 FROM dmp_migration.change_requests q
   WHERE q.database_id=r.database_id AND q.status='queued' AND (q.queued_at,q.seq)<(r.queued_at,r.seq)) END
 FROM dmp_migration.change_requests r
 JOIN dmp_migration.definitions d ON d.id=r.migration_id `

func scanRequest(row pgx.Row) (ChangeRequest, error) {
	var q ChangeRequest
	err := row.Scan(&q.ID, &q.DatabaseID, &q.MigrationID, &q.MigrationName, &q.Operation, &q.SQLHash, &q.RequestedBy, &q.Status,
		&q.AttemptID, &q.RejectReason, &q.ResultCode, &q.ResultMessage, &q.CreatedAt, &q.UpdatedAt, &q.ApprovedAt, &q.QueuedAt,
		&q.StartedAt, &q.CompletedAt, &q.QueuePosition)
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

// insertRequest stores the request, its required approvals and the "submitted" event atomically.
func (r *Repository) insertRequest(ctx context.Context, q ChangeRequest, approvers []RequiredApprover) (ChangeRequest, error) {
	tx, err := r.Pool.Begin(ctx)
	if err != nil {
		return q, err
	}
	defer rollback(tx)

	_, err = tx.Exec(ctx, `INSERT INTO dmp_migration.change_requests
 (id,database_id,migration_id,operation,sql_hash,requested_by) VALUES ($1,$2,$3,$4,$5,$6)`,
		q.ID, q.DatabaseID, q.MigrationID, q.Operation, q.SQLHash, q.RequestedBy)
	if err != nil {
		if postgresCode(err) == "23505" {
			return q, fault("conflict", "This migration already has an open change request for the same operation")
		}
		return q, err
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
		if out[i].Approvals == nil {
			out[i].Approvals = []Approval{}
		}
	}
	return out, nil
}

func (r *Repository) approvalsFor(ctx context.Context, requestIDs []string) (map[string][]Approval, error) {
	rows, err := r.Pool.Query(ctx, `SELECT id,request_id,group_id,group_name,required_leader_id,leader_username,
 decision,decided_by,decided_at,comment
 FROM dmp_migration.change_request_approvals WHERE request_id = ANY($1) ORDER BY group_name,id`, requestIDs)
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

// decide records the caller's decision on every approval they owe. It returns true
// when this decision completed the review and moved the request into the queue.
func (r *Repository) decide(ctx context.Context, db, id, user string, approve bool, comment string, forbidSelf bool) (bool, error) {
	tx, err := r.Pool.Begin(ctx)
	if err != nil {
		return false, err
	}
	defer rollback(tx)

	var status, requester string
	err = tx.QueryRow(ctx, `SELECT status,requested_by FROM dmp_migration.change_requests
 WHERE id=$1 AND database_id=$2 FOR UPDATE`, id, db).Scan(&status, &requester)
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

	decision := "approved"
	if !approve {
		decision = "rejected"
	}
	tag, err := tx.Exec(ctx, `UPDATE dmp_migration.change_request_approvals
 SET decision=$3,decided_by=$2,decided_at=now(),comment=$4
 WHERE request_id=$1 AND required_leader_id=$2 AND decision='pending'`, id, user, decision, comment)
	if err != nil {
		return false, err
	}
	if tag.RowsAffected() == 0 {
		var owes bool
		if err = tx.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM dmp_migration.change_request_approvals
 WHERE request_id=$1 AND required_leader_id=$2)`, id, user).Scan(&owes); err != nil {
			return false, err
		}
		if owes {
			return false, fault("conflict", "You have already decided on this change request")
		}
		return false, fault("forbidden", "You are not a required approver for this change request")
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
		return false, tx.Commit(ctx)
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
	return queued, nil
}

// cancel withdraws a request the caller submitted, before it starts running.
func (r *Repository) cancel(ctx context.Context, db, id, user, reason string) error {
	tx, err := r.Pool.Begin(ctx)
	if err != nil {
		return err
	}
	defer rollback(tx)

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

// claimSQL picks the head of the queue of a database that has nothing running and no
// unresolved attempt. Skipping locked rows lets several workers poll safely.
const claimSQL = `SELECT r.id,r.database_id,r.migration_id,r.operation,r.sql_hash,r.requested_by
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

// claim takes the next runnable request and marks it running. validate runs while the
// row is locked: a returned error leaves the request queued to be retried, and
// ok=false cancels it with the given code and message.
func (r *Repository) claim(ctx context.Context, validate func(context.Context, QueuedRequest) (bool, string, string, error)) (*QueuedRequest, error) {
	tx, err := r.Pool.Begin(ctx)
	if err != nil {
		return nil, err
	}
	defer rollback(tx)

	var q QueuedRequest
	err = tx.QueryRow(ctx, claimSQL).Scan(&q.ID, &q.DatabaseID, &q.MigrationID, &q.Operation, &q.SQLHash, &q.RequestedBy)
	if isNoRows(err) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}

	ok, code, message, err := validate(ctx, q)
	if err != nil {
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
		return nil, tx.Commit(ctx)
	}

	if _, err = tx.Exec(ctx, `UPDATE dmp_migration.change_requests SET status='running',started_at=now() WHERE id=$1`, q.ID); err != nil {
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
	return &q, nil
}

// linkAttempt records which execution attempt belongs to a request.
func (r *Repository) linkAttempt(ctx context.Context, requestID, attemptID string) error {
	_, err := r.Pool.Exec(ctx, `UPDATE dmp_migration.change_requests SET attempt_id=$2 WHERE id=$1 AND attempt_id IS NULL`, requestID, attemptID)
	return err
}

// finishRequest records the outcome of a running or unresolved request.
func (r *Repository) finishRequest(ctx context.Context, id string, status RequestStatus, code, message, attemptID string) error {
	tx, err := r.Pool.Begin(ctx)
	if err != nil {
		return err
	}
	defer rollback(tx)

	var attempt *string
	if attemptID != "" {
		attempt = &attemptID
	}
	tag, err := tx.Exec(ctx, `UPDATE dmp_migration.change_requests
 SET status=$2,result_code=$3,result_message=$4,attempt_id=COALESCE(attempt_id,$5),
     completed_at=CASE WHEN $2='unknown' THEN NULL ELSE now() END
 WHERE id=$1 AND status IN ('running','unknown')`, id, status, code, message, attempt)
	if err != nil {
		return err
	}
	if tag.RowsAffected() == 0 {
		return nil // already finished by another worker
	}
	if err = addEvent(ctx, tx, id, "finished", "", map[string]any{"status": status, "code": code, "message": message}); err != nil {
		return err
	}
	return tx.Commit(ctx)
}

// attemptFor finds the execution attempt of a request that started at startedAt.
// Only one attempt can be unresolved per database, so the match is unambiguous.
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

// InFlightRequest is a request the worker had started but not finished.
type InFlightRequest struct {
	ID          string
	DatabaseID  string
	MigrationID string
	Operation   Operation
	RequestedBy string
	Status      RequestStatus
	StartedAt   time.Time
}

// inFlight lists requests that are unresolved or have been running for longer than any
// live execution could (so their worker must have stopped).
func (r *Repository) inFlight(ctx context.Context, orphanAfter time.Duration) ([]InFlightRequest, error) {
	rows, err := r.Pool.Query(ctx, `SELECT id,database_id,migration_id,operation,requested_by,status,started_at
 FROM dmp_migration.change_requests
 WHERE status='unknown' OR (status='running' AND started_at < now() - make_interval(secs => $1::float8))
 ORDER BY started_at`, orphanAfter.Seconds())
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []InFlightRequest{}
	for rows.Next() {
		var q InFlightRequest
		if err = rows.Scan(&q.ID, &q.DatabaseID, &q.MigrationID, &q.Operation, &q.RequestedBy, &q.Status, &q.StartedAt); err != nil {
			return nil, err
		}
		out = append(out, q)
	}
	return out, rows.Err()
}

// blockedQueues lists databases whose queue head waits on an unresolved attempt,
// with the user to reconcile as.
func (r *Repository) blockedQueues(ctx context.Context) ([]QueuedRequest, error) {
	rows, err := r.Pool.Query(ctx, `SELECT DISTINCT ON (r.database_id) r.id,r.database_id,r.migration_id,r.operation,r.sql_hash,r.requested_by
 FROM dmp_migration.change_requests r
 WHERE r.status='queued'
   AND EXISTS (SELECT 1 FROM dmp_migration.attempts t WHERE t.database_id=r.database_id AND t.status IN ('running','unknown'))
 ORDER BY r.database_id,r.queued_at,r.seq`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []QueuedRequest{}
	for rows.Next() {
		var q QueuedRequest
		if err = rows.Scan(&q.ID, &q.DatabaseID, &q.MigrationID, &q.Operation, &q.SQLHash, &q.RequestedBy); err != nil {
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
