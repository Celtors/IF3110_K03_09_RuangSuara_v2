package migration

import (
	"encoding/json"
	"time"

	"backend/internal/telemetry"

	"go.opentelemetry.io/otel/trace"
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

type Approval struct {
	currentLeaderID  string
	ID               string     `json:"id"`
	RequestID        string     `json:"-"`
	GroupID          string     `json:"group_id"`
	GroupName        string     `json:"group_name"`
	RequiredLeaderID string     `json:"required_leader_id"`
	LeaderUsername   string     `json:"leader_username"`
	Mine             bool       `json:"mine"`
	Decision         string     `json:"decision"`
	DecidedBy        *string    `json:"decided_by"`
	DecidedAt        *time.Time `json:"decided_at"`
	Comment          string     `json:"comment"`
}

type RequestEvent struct {
	ID        int64           `json:"id"`
	Event     string          `json:"event"`
	Actor     string          `json:"actor"` // user id, or empty for the system/worker
	Detail    json.RawMessage `json:"detail"`
	CreatedAt time.Time       `json:"created_at"`
}

type ChangeRequest struct {
	Batch                   *BatchSpec `json:"batch,omitempty"`
	CompensationSQLHash     string     `json:"compensation_sql_hash,omitempty"`
	CorrectsBatchID         *string    `json:"corrects_batch_id,omitempty"`
	submissionCorrelationID string
	ID                      string         `json:"id"`
	DatabaseID              string         `json:"database_id"`
	MigrationID             string         `json:"migration_id"`
	MigrationName           string         `json:"migration_name"`
	Operation               Operation      `json:"operation"`
	SQLHash                 string         `json:"sql_hash"`
	RequestedBy             string         `json:"requested_by"`
	Status                  RequestStatus  `json:"status"`
	AttemptID               *string        `json:"attempt_id"`
	RejectReason            string         `json:"reject_reason,omitempty"`
	ResultCode              string         `json:"result_code,omitempty"`
	ResultMessage           string         `json:"result_message,omitempty"`
	CreatedAt               time.Time      `json:"created_at"`
	UpdatedAt               time.Time      `json:"updated_at"`
	ApprovedAt              *time.Time     `json:"approved_at"`
	QueuedAt                *time.Time     `json:"queued_at"`
	StartedAt               *time.Time     `json:"started_at"`
	CompletedAt             *time.Time     `json:"completed_at"`
	QueuePosition           *int64         `json:"queue_position"`
	Approvals               []Approval     `json:"approvals"`
	Events                  []RequestEvent `json:"events,omitempty"`
}

type SubmitRequest struct {
	CorrectsBatchID string    `json:"corrects_batch_id,omitempty"`
	BatchAttribute  string    `json:"batch_attribute,omitempty"`
	BatchSize       int       `json:"batch_size,omitempty"`
	Mode            string    `json:"mode,omitempty"`
	MigrationID     string    `json:"migration_id"`
	Operation       Operation `json:"operation"`
}

type DecisionBody struct {
	Comment string `json:"comment"`
}

type RequiredApprover struct {
	GroupID        string
	GroupName      string
	LeaderID       string
	LeaderUsername string
}

type QueuedRequest struct {
	startedAt               time.Time
	submissionCorrelationID string
	queueTrace              telemetry.TraceContext
	claimSpan               trace.SpanContext
	reloaded                bool
	ID                      string
	DatabaseID              string
	MigrationID             string
	Operation               Operation
	SQLHash                 string
	RequestedBy             string
	CorrectsBatchID         *string
}
