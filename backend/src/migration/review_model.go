package migration

import (
	"encoding/json"
	"time"
)

// RequestStatus is the position of a change request in the workflow:
//
//	Draft -> Pending Review -> Approved -> Pending (Queue) -> Running -> Success / Failed
//
// "Draft" is the migration definition's own state. Approved is immediate and
// transient: the request moves to queued in the same transaction as the final
// approval, so API clients normally see pending_review, queued, running, then a
// final status.
type RequestStatus string

const (
	RequestPendingReview RequestStatus = "pending_review"
	RequestApproved      RequestStatus = "approved"
	RequestQueued        RequestStatus = "queued"
	RequestRunning       RequestStatus = "running"
	RequestSucceeded     RequestStatus = "succeeded"
	RequestFailed        RequestStatus = "failed"
	RequestUnknown       RequestStatus = "unknown" // outcome unresolved; mirrors attempts.status
	RequestRejected      RequestStatus = "rejected"
	RequestCancelled     RequestStatus = "cancelled"
)

var allRequestStatuses = map[RequestStatus]bool{
	RequestPendingReview: true, RequestApproved: true, RequestQueued: true, RequestRunning: true,
	RequestSucceeded: true, RequestFailed: true, RequestUnknown: true, RequestRejected: true, RequestCancelled: true,
}

// Approval is one group's required decision on a change request. The leader and
// group names are snapshots taken when the request was submitted.
type Approval struct {
	ID               string     `json:"id"`
	RequestID        string     `json:"-"`
	GroupID          string     `json:"group_id"`
	GroupName        string     `json:"group_name"`
	RequiredLeaderID string     `json:"required_leader_id"`
	LeaderUsername   string     `json:"leader_username"`
	Decision         string     `json:"decision"` // pending, approved or rejected
	DecidedBy        *string    `json:"decided_by"`
	DecidedAt        *time.Time `json:"decided_at"`
	Comment          string     `json:"comment"`
}

// RequestEvent is one entry of a request's append-only audit trail.
type RequestEvent struct {
	ID        int64           `json:"id"`
	Event     string          `json:"event"`
	Actor     string          `json:"actor"` // user id, or empty for the system/worker
	Detail    json.RawMessage `json:"detail"`
	CreatedAt time.Time       `json:"created_at"`
}

// ChangeRequest asks to run one operation (apply, rollback or forward) of a
// migration. It must be approved by the leader of every group with access to the
// database before it is queued and executed.
type ChangeRequest struct {
	submissionCorrelationID string
	ID                      string        `json:"id"`
	DatabaseID              string        `json:"database_id"`
	MigrationID             string        `json:"migration_id"`
	MigrationName           string        `json:"migration_name"`
	Operation               Operation     `json:"operation"`
	SQLHash                 string        `json:"sql_hash"`
	RequestedBy             string        `json:"requested_by"`
	Status                  RequestStatus `json:"status"`
	AttemptID               *string       `json:"attempt_id"`
	RejectReason            string        `json:"reject_reason,omitempty"`
	ResultCode              string        `json:"result_code,omitempty"`
	ResultMessage           string        `json:"result_message,omitempty"`
	CreatedAt               time.Time     `json:"created_at"`
	UpdatedAt               time.Time     `json:"updated_at"`
	ApprovedAt              *time.Time    `json:"approved_at"`
	QueuedAt                *time.Time    `json:"queued_at"`
	StartedAt               *time.Time    `json:"started_at"`
	CompletedAt             *time.Time    `json:"completed_at"`
	// QueuePosition is 1 for the request that runs next on its database; nil unless queued.
	QueuePosition *int64         `json:"queue_position"`
	Approvals     []Approval     `json:"approvals"`
	Events        []RequestEvent `json:"events,omitempty"`
}

// SubmitRequest is the body of POST /api/databases/:id/change-requests.
type SubmitRequest struct {
	MigrationID string    `json:"migration_id"`
	Operation   Operation `json:"operation"`
}

// DecisionBody is the optional body of approve/reject/cancel. Reject requires a comment.
type DecisionBody struct {
	Comment string `json:"comment"`
}

// RequiredApprover is a group that has access to a database and the leader who must
// approve on its behalf.
type RequiredApprover struct {
	GroupID        string
	GroupName      string
	LeaderID       string // empty when the group has no leader
	LeaderUsername string
	LeaderActive   bool
}

// QueuedRequest is the part of a request the worker needs to run it.
type QueuedRequest struct {
	submissionCorrelationID string
	ID                      string
	DatabaseID              string
	MigrationID             string
	Operation               Operation
	SQLHash                 string
	RequestedBy             string
}
