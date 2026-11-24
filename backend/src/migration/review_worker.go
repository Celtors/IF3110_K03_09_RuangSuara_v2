package migration

import (
	"context"
	"log"
	"sync"
	"time"
)

// WorkerConfig tunes the queue worker.
type WorkerConfig struct {
	PollInterval     time.Duration // how often to look for queued requests
	RecoveryInterval time.Duration // how often to resolve interrupted requests
	// OrphanAfter is how long a request may stay running before its worker is presumed
	// dead. It must exceed the 40s execution timeout plus bookkeeping.
	OrphanAfter time.Duration
	// MaxConcurrent bounds requests running at once across different databases. A single
	// database never runs more than one. Keep it below the platform pool size, because
	// every running request holds a pooled connection for its advisory lock.
	MaxConcurrent int
}

func DefaultWorkerConfig() WorkerConfig {
	return WorkerConfig{PollInterval: 2 * time.Second, RecoveryInterval: 30 * time.Second, OrphanAfter: 2 * time.Minute, MaxConcurrent: 2}
}

// StartWorker runs the queue until ctx is cancelled. The returned channel closes once
// the worker and every request it started have finished, so shut down in this order:
// cancel ctx, wait for the channel, then close database pools.
func (r *Review) StartWorker(ctx context.Context, cfg WorkerConfig) <-chan struct{} {
	done := make(chan struct{})
	if r.Service == nil || r.Service.Repository == nil || r.Service.Repository.Pool == nil {
		log.Print("Migration worker disabled: platform storage is unavailable")
		close(done)
		return done
	}
	d := DefaultWorkerConfig()
	if cfg.PollInterval <= 0 {
		cfg.PollInterval = d.PollInterval
	}
	if cfg.RecoveryInterval <= 0 {
		cfg.RecoveryInterval = d.RecoveryInterval
	}
	if cfg.OrphanAfter <= 0 {
		cfg.OrphanAfter = d.OrphanAfter
	}
	if cfg.MaxConcurrent <= 0 {
		cfg.MaxConcurrent = d.MaxConcurrent
	}
	go func() {
		defer close(done)
		r.runWorker(ctx, cfg)
	}()
	return done
}

func (r *Review) runWorker(ctx context.Context, cfg WorkerConfig) {
	sem := make(chan struct{}, cfg.MaxConcurrent)
	var running sync.WaitGroup
	poll := time.NewTicker(cfg.PollInterval)
	defer poll.Stop()
	recovery := time.NewTicker(cfg.RecoveryInterval)
	defer recovery.Stop()

	r.recoverInterrupted(ctx, cfg)
	for {
		r.drain(ctx, sem, &running)
		select {
		case <-ctx.Done():
			running.Wait() // in-flight requests finish and record their outcome
			return
		case <-poll.C:
		case <-r.wake:
		case <-recovery.C:
			r.recoverInterrupted(ctx, cfg)
		}
	}
}

// drain starts requests until the queue has nothing runnable or the worker is full.
func (r *Review) drain(ctx context.Context, sem chan struct{}, running *sync.WaitGroup) {
	for ctx.Err() == nil {
		select {
		case sem <- struct{}{}:
		default:
			return
		}
		q, err := r.repo().claim(ctx, r.revalidate)
		if err != nil {
			<-sem
			if ctx.Err() == nil {
				log.Printf("migration worker: claiming a request failed: %v", err)
			}
			return
		}
		if q == nil {
			<-sem
			return
		}
		running.Add(1)
		go func(q QueuedRequest) {
			defer running.Done()
			defer func() { <-sem }()
			// Shutdown must not abort SQL halfway: the request gets its own context and
			// the worker waits for it. A killed process is handled by recoverInterrupted.
			r.process(context.WithoutCancel(ctx), q)
		}(*q)
	}
}

// revalidate re-checks a queued request at the moment it would run, because
// permissions, leaders and migrations can change between approval and execution.
// ok=false cancels the request; an error leaves it queued to retry.
func (r *Review) revalidate(ctx context.Context, q QueuedRequest) (bool, string, string, error) {
	s := r.Service
	if s.Access == nil || r.Approvers == nil {
		return false, "", "", fault("authorization_unavailable", "Database authorization is unavailable")
	}

	permission, err := s.Access.Permission(ctx, q.RequestedBy, q.DatabaseID)
	if err != nil {
		return false, "", "", err
	}
	if permission != Edit {
		return false, "requester_lost_access", "The person who submitted this request no longer has edit access to the database", nil
	}

	m, err := r.repo().Get(ctx, q.DatabaseID, q.MigrationID)
	if err != nil {
		if code, _ := faultInfo(err); code == "not_found" {
			return false, "migration_missing", "The migration no longer exists", nil
		}
		return false, "", "", err
	}
	if sqlHash(rawSQL(m, q.Operation)) != q.SQLHash {
		return false, "sql_changed", "The migration SQL changed after it was approved", nil
	}

	decisions, err := r.repo().approvalDecisions(ctx, q.ID)
	if err != nil {
		return false, "", "", err
	}
	approvedGroups := map[string]bool{}
	for _, a := range decisions {
		if a.Decision != "approved" {
			return false, "approval_missing", "The change request is missing an approval", nil
		}
		approvedGroups[a.GroupID] = true
	}
	if len(approvedGroups) == 0 {
		return false, "approval_missing", "The change request has no approvals", nil
	}

	// A group that gained access after approval has not reviewed this change. Groups that
	// lost access simply need no approval any more.
	current, err := r.Approvers.RequiredApprovers(ctx, q.DatabaseID)
	if err != nil {
		return false, "", "", err
	}
	for _, a := range current {
		if !approvedGroups[a.GroupID] {
			return false, "approvers_changed", "Group " + a.GroupName + " gained access to the database after approval. Submit a new change request", nil
		}
	}
	return true, "", "", nil
}

// process executes one claimed request and records its outcome.
func (r *Review) process(ctx context.Context, q QueuedRequest) {
	r.inflight.Store(q.ID, struct{}{})
	defer r.inflight.Delete(q.ID)

	var attemptID string
	began := false
	_, err := r.Service.executeOperation(ctx, q.RequestedBy, q.DatabaseID, q.MigrationID, q.Operation, func(a Attempt) {
		began = true
		attemptID = a.ID
		clean, cancel := cleanupContext()
		defer cancel()
		if e := r.repo().linkAttempt(clean, q.ID, a.ID); e != nil {
			log.Printf("migration worker: could not link attempt to request %s: %v", q.ID, e)
		}
	})

	status, code, message := RequestSucceeded, "", ""
	if err != nil {
		code, message = faultInfo(err)
		status = RequestFailed
		// The attempt exists but its outcome could not be confirmed: keep the request
		// unresolved until recovery learns what the target did.
		if began && code == "indeterminate" {
			status = RequestUnknown
		}
		if code == "internal_error" {
			log.Printf("migration worker: request %s failed unexpectedly: %v", q.ID, err)
		}
	}

	clean, cancel := cleanupContext()
	defer cancel()
	if err = r.repo().finishRequest(clean, q.ID, status, code, message, attemptID); err != nil {
		// The request stays running; recoverInterrupted settles it from the attempt record.
		log.Printf("migration worker: could not record outcome of request %s: %v", q.ID, err)
	}
}

// recoverInterrupted settles requests whose worker stopped or whose outcome was unknown,
// and reconciles databases whose queue is blocked by an unresolved attempt.
func (r *Review) recoverInterrupted(ctx context.Context, cfg WorkerConfig) {
	stuck, err := r.repo().inFlight(ctx, cfg.OrphanAfter)
	if err != nil {
		if ctx.Err() == nil {
			log.Printf("migration worker: listing interrupted requests failed: %v", err)
		}
		return
	}
	for _, q := range stuck {
		if _, live := r.inflight.Load(q.ID); live {
			continue
		}
		r.recoverOne(ctx, q)
	}

	blocked, err := r.repo().blockedQueues(ctx)
	if err != nil {
		if ctx.Err() == nil {
			log.Printf("migration worker: listing blocked queues failed: %v", err)
		}
		return
	}
	for _, q := range blocked {
		if e := r.Service.Reconcile(ctx, q.RequestedBy, q.DatabaseID); e != nil {
			if code, _ := faultInfo(e); code != "indeterminate" && code != "conflict" {
				log.Printf("migration worker: reconcile of database %s failed: %v", q.DatabaseID, e)
			}
		}
	}
}

func (r *Review) recoverOne(ctx context.Context, q InFlightRequest) {
	attemptID, status, code, message, err := r.repo().attemptFor(ctx, q)
	if err != nil {
		log.Printf("migration worker: looking up attempt of request %s failed: %v", q.ID, err)
		return
	}
	if status == "running" || status == "unknown" {
		// Ask the target database what happened to the interrupted transaction.
		if e := r.Service.Reconcile(ctx, q.RequestedBy, q.DatabaseID); e != nil {
			if c, _ := faultInfo(e); c != "indeterminate" && c != "conflict" {
				log.Printf("migration worker: reconcile for request %s failed: %v", q.ID, e)
			}
		}
		attemptID, status, code, message, err = r.repo().attemptFor(ctx, q)
		if err != nil {
			log.Printf("migration worker: looking up attempt of request %s failed: %v", q.ID, err)
			return
		}
	}

	clean, cancel := cleanupContext()
	defer cancel()
	var finalStatus RequestStatus
	switch status {
	case "succeeded":
		finalStatus, code, message = RequestSucceeded, "", ""
	case "failed":
		finalStatus = RequestFailed
		if code == "" {
			code = "execution_failed"
		}
	case "":
		// No attempt exists, so the target never saw any SQL.
		if q.Status != RequestRunning {
			return
		}
		finalStatus, code, message = RequestFailed, "interrupted", "The worker stopped before the migration started. Submit a new change request"
	default: // still running or unknown on the target
		if q.Status == RequestUnknown {
			return
		}
		finalStatus, code, message = RequestUnknown, "indeterminate", "The outcome on the target database is unresolved; an operator must reconcile it"
	}
	if err = r.repo().finishRequest(clean, q.ID, finalStatus, code, message, attemptID); err != nil {
		log.Printf("migration worker: could not settle request %s: %v", q.ID, err)
	}
}
