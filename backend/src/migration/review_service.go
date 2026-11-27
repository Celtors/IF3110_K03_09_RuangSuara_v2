package migration

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"log/slog"
	"os"
	"strings"
	"sync"

	"backend/internal/telemetry"
)

// Review adds the approval workflow and the execution queue on top of Service.
//
//	Draft -> Pending Review -> Approved -> Pending (Queue) -> Running -> Success / Failed
//
// The queue lives in the platform database next to the migration storage, so state
// changes, constraints and the per-database lock share one transactional boundary.
type Review struct {
	Service            *Service
	Approvers          Approvers
	ForbidSelfApproval bool

	wake     chan struct{}
	inflight sync.Map
}

func NewReview(s *Service) *Review {
	r := &Review{
		Service:            s,
		wake:               make(chan struct{}, 1),
		ForbidSelfApproval: os.Getenv("MIGRATION_FORBID_SELF_APPROVAL") == "true",
	}
	if s != nil && s.Repository != nil && s.Repository.Pool != nil {
		r.Approvers = PlatformApprovers{Pool: s.Repository.Pool}
	}
	return r
}

func (r *Review) notify() {
	select {
	case r.wake <- struct{}{}:
	default:
	}
}

func (r *Review) repo() *Repository { return r.Service.Repository }

func validOperation(op Operation) bool {
	return op == OperationApply || op == OperationRollback || op == OperationForward
}

func sqlHash(sql string) string {
	sum := sha256.Sum256([]byte(sql))
	return hex.EncodeToString(sum[:])
}

func rawSQL(m Migration, op Operation) string {
	if op == OperationRollback {
		return m.SQLDown
	}
	return m.SQLUp
}

func requestSQL(m Migration, op Operation) (string, error) {
	switch op {
	case OperationApply:
		if m.State == "applied" {
			return "", fault("already_applied", "This migration has already been applied")
		}
		if m.State != "draft" && m.State != "failed" {
			return "", fault("conflict", "Only draft or failed migrations can be applied")
		}
		if err := m.Draft.ValidateForReview(); err != nil {
			return "", err
		}
	case OperationRollback:
		if m.State != "applied" {
			return "", fault("conflict", "Only an applied migration can be rolled back")
		}
		if m.Irreversible || strings.TrimSpace(m.SQLDown) == "" {
			return "", fault("conflict", "This migration has no reversible SQL down")
		}
	case OperationForward:
		if m.State != "rolled_back" {
			return "", fault("conflict", "Only a successfully rolled-back migration can be forwarded")
		}
	default:
		return "", fault("invalid_input", "Operation must be apply, rollback or forward")
	}
	sql := rawSQL(m, op)
	if _, err := parseSQL(sql, false); err != nil {
		return "", err
	}
	return sql, nil
}

func checkApprovers(approvers []RequiredApprover) error {
	if len(approvers) == 0 {
		return fault("conflict", "No group has access to this database, so nobody can approve a change to it")
	}
	// A group without a leader still gets a pending approval. Only a leader may decide it,
	// so it stays pending until one is assigned and approves; leadership is read live.
	return nil
}

func (r *Review) markMine(ctx context.Context, user string, requests ...*ChangeRequest) error {
	groups, err := r.repo().groupsOf(ctx, user)
	if err != nil {
		return err
	}
	for _, q := range requests {
		for i := range q.Approvals {
			q.Approvals[i].Mine = groups[q.Approvals[i].GroupID]
		}
	}
	return nil
}

// one reads a request back and marks the viewer's approvals.
func (r *Review) one(ctx context.Context, user, db, id string) (ChangeRequest, error) {
	q, err := r.repo().GetRequest(ctx, db, id)
	if err != nil {
		return q, err
	}
	return q, r.markMine(ctx, user, &q)
}

func faultInfo(err error) (code, message string) {
	var f *Fault
	if errors.As(err, &f) {
		return f.Code, f.Message
	}
	return "internal_error", "The migration operation could not be completed"
}

// Submit creates a change request and asks every group with access to approve it.
// It needs Edit permission, like running the migration directly did.
func (r *Review) Submit(ctx context.Context, user, db string, in SubmitRequest) (ChangeRequest, error) {
	db = canonicalDatabaseID(db)
	ctx = migrationContext(ctx, user, db, "")
	if err := r.Service.authorize(ctx, user, db, Edit); err != nil {
		return ChangeRequest{}, err
	}
	if !validOperation(in.Operation) {
		return ChangeRequest{}, fault("invalid_input", "Operation must be apply, rollback or forward")
	}
	if id := strings.TrimSpace(in.MigrationID); id == "" || len(id) > 200 {
		return ChangeRequest{}, fault("invalid_input", "A migration ID is required")
	}
	if r.Approvers == nil {
		return ChangeRequest{}, fault("database_unavailable", "Approver lookup is unavailable")
	}
	pool, err := r.Service.connection(ctx, db)
	if err != nil {
		return ChangeRequest{}, err
	}

	m, err := r.repo().Get(ctx, db, strings.TrimSpace(in.MigrationID))
	if err != nil {
		return ChangeRequest{}, err
	}
	ctx = telemetry.With(ctx, telemetry.ID("migration_id", m.ID), slog.String("operation", string(in.Operation)))
	sql, err := requestSQL(m, in.Operation)
	if err != nil {
		return ChangeRequest{}, err
	}
	var batch *BatchSpec
	var downHash string
	if in.Mode != "" && in.Mode != "normal" && in.Mode != "batch" {
		return ChangeRequest{}, fault("invalid_input", "mode must be normal or batch")
	}
	if in.Mode == "batch" || in.BatchAttribute != "" || in.BatchSize != 0 {
		if in.Operation != OperationApply || in.Mode == "normal" {
			return ChangeRequest{}, fault("invalid_input", "Batch mode supports apply only")
		}
		spec := BatchSpec{in.BatchAttribute, in.BatchSize}
		b, e := parseBatch(sql, spec)
		if e != nil {
			return ChangeRequest{}, e
		}
		if e = b.validate(ctx, pool); e != nil {
			return ChangeRequest{}, e
		}
		batch = &spec
		if !m.Irreversible {
			if inverseBatchSQL(m.SQLUp, m.SQLDown, spec) {
				downHash = sqlHash(m.SQLDown)
			}
		}
	} else if in.Operation != OperationApply {
		replay, e := completedBatchReplay(ctx, r.repo().Pool, m, in.Operation)
		if e != nil {
			return ChangeRequest{}, e
		}
		if replay == nil && batchPattern.MatchString(sql) {
			return ChangeRequest{}, fault("invalid_input", "Parameterized SQL requires a recorded batch scope")
		}
	} else if batchPattern.MatchString(sql) {
		return ChangeRequest{}, fault("invalid_input", "Parameterized batch SQL requires batch mode")
	}
	approvers, err := r.Approvers.RequiredApprovers(ctx, db)
	if err != nil {
		return ChangeRequest{}, err
	}
	if err = checkApprovers(approvers); err != nil {
		return ChangeRequest{}, err
	}

	q, err := r.repo().insertRequest(ctx, ChangeRequest{
		ID:                      newID(),
		DatabaseID:              db,
		MigrationID:             m.ID,
		Operation:               in.Operation,
		Batch:                   batch,
		CompensationSQLHash:     downHash,
		SQLHash:                 sqlHash(sql),
		RequestedBy:             user,
		submissionCorrelationID: telemetry.CorrelationID(ctx),
	}, approvers)
	if err != nil {
		return q, err
	}
	return q, r.markMine(ctx, user, &q)
}

func (r *Review) Decide(ctx context.Context, user, db, id string, approve bool, comment string) (ChangeRequest, error) {
	db = canonicalDatabaseID(db)
	ctx = migrationContext(ctx, user, db, "")
	ctx = telemetry.With(ctx, telemetry.ID("job_id", id))
	if err := r.Service.authorize(ctx, user, db, View); err != nil {
		return ChangeRequest{}, err
	}
	comment = strings.TrimSpace(comment)
	if len(comment) > 2000 {
		return ChangeRequest{}, fault("invalid_input", "A comment can be at most 2000 bytes")
	}
	if !approve && comment == "" {
		return ChangeRequest{}, fault("invalid_input", "A reason is required to reject a change request")
	}

	if approve {
		q, err := r.repo().GetRequest(ctx, db, id)
		if err != nil {
			return ChangeRequest{}, err
		}
		m, err := r.repo().Get(ctx, db, q.MigrationID)
		if err != nil {
			return ChangeRequest{}, err
		}
		if sqlHash(rawSQL(m, q.Operation)) != q.SQLHash || (q.CompensationSQLHash != "" && (m.Irreversible || sqlHash(m.SQLDown) != q.CompensationSQLHash)) {
			return ChangeRequest{}, fault("conflict", "The approved SQL changed; cancel and submit a new request")
		}
	}

	queued, err := r.repo().decide(ctx, db, id, user, approve, comment, r.ForbidSelfApproval)
	if err != nil {
		return ChangeRequest{}, err
	}
	if queued {
		r.notify()
	}
	return r.one(ctx, user, db, id)
}

// Cancel withdraws a request before it starts running. Only its submitter can cancel it.
func (r *Review) Cancel(ctx context.Context, user, db, id, reason string) (ChangeRequest, error) {
	db = canonicalDatabaseID(db)
	ctx = migrationContext(ctx, user, db, "")
	ctx = telemetry.With(ctx, telemetry.ID("job_id", id))
	if err := r.Service.authorize(ctx, user, db, View); err != nil {
		return ChangeRequest{}, err
	}
	reason = strings.TrimSpace(reason)
	if len(reason) > 2000 {
		return ChangeRequest{}, fault("invalid_input", "A comment can be at most 2000 bytes")
	}
	if err := r.repo().cancel(ctx, db, id, user, reason); err != nil {
		return ChangeRequest{}, err
	}
	slog.InfoContext(ctx, "approval.cancelled", "status", "cancelled")
	return r.one(ctx, user, db, id)
}

// Get returns one request with approvals and audit events.
func (r *Review) Get(ctx context.Context, user, db, id string) (ChangeRequest, error) {
	db = canonicalDatabaseID(db)
	if err := r.Service.authorize(ctx, user, db, View); err != nil {
		return ChangeRequest{}, err
	}
	return r.one(ctx, user, db, id)
}

// List returns a database's requests, newest first, optionally filtered.
func (r *Review) List(ctx context.Context, user, db, migrationID, status string, limit int) ([]ChangeRequest, error) {
	db = canonicalDatabaseID(db)
	if err := r.Service.authorize(ctx, user, db, View); err != nil {
		return nil, err
	}
	if status != "" && !allRequestStatuses[RequestStatus(status)] {
		return nil, fault("invalid_input", "Unknown change request status")
	}
	if limit <= 0 || limit > 200 {
		limit = 100
	}
	out, err := r.repo().queryRequests(ctx, `WHERE r.database_id=$1 AND ($2::text='' OR r.migration_id=$2::text)
 AND ($3::text='' OR r.status=$3::text) ORDER BY r.created_at DESC,r.seq DESC LIMIT $4`, db, migrationID, status, limit)
	if err != nil {
		return nil, err
	}
	ptrs := make([]*ChangeRequest, len(out))
	for i := range out {
		ptrs[i] = &out[i]
	}
	return out, r.markMine(ctx, user, ptrs...)
}

// PendingApprovals lists the requests, across all databases, that are waiting on a group
// this user leads, and that they may still see.
func (r *Review) PendingApprovals(ctx context.Context, user string) ([]ChangeRequest, error) {
	if user == "" || r.Service.Access == nil {
		return nil, fault("unauthenticated", "A validated session is required")
	}
	if r.Service.Repository == nil || r.Service.Repository.Pool == nil {
		return nil, fault("database_unavailable", "Migration storage is unavailable")
	}
	all, err := r.repo().queryRequests(ctx, `WHERE r.status='pending_review' AND EXISTS (
 SELECT 1 FROM dmp_migration.change_request_approvals a
 WHERE a.request_id=r.id AND a.decision='pending' AND `+leaderOfGroup("$1")+`)
 ORDER BY r.created_at LIMIT 200`, user)
	if err != nil {
		return nil, err
	}
	allowed := map[string]bool{}
	out := []ChangeRequest{}
	for _, q := range all {
		ok, seen := allowed[q.DatabaseID]
		if !seen {
			p, e := r.Service.Access.Permission(ctx, user, q.DatabaseID)
			if e != nil {
				return nil, fault("authorization_unavailable", "Database authorization is unavailable")
			}
			ok = p >= View && p <= Edit
			allowed[q.DatabaseID] = ok
		}
		if ok {
			out = append(out, q)
		}
	}
	ptrs := make([]*ChangeRequest, len(out))
	for i := range out {
		ptrs[i] = &out[i]
	}
	return out, r.markMine(ctx, user, ptrs...)
}
