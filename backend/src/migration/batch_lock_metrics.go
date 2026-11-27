package migration

import (
	"context"
	"maps"
	"sync"
	"time"

	"backend/internal/telemetry"

	"github.com/jackc/pgx/v5/pgxpool"
)

// A worker keeps at most one open interval per batch and lock scope. The claim
// and last batch write identify the phase across passes, including after a resume.
type batchLockWaits struct {
	mu      sync.Mutex
	pending map[batchLockKey]batchLockWait
}

type batchLockKey struct {
	jobID, scope string
}

type batchLockWait struct {
	claimed, updated, since time.Time
	identity                string
}

func (p batchProgress) acceptsLockTiming() bool {
	switch p.State {
	case "ready", "running", "partial", "unresolved", "compensating":
		return true
	}
	return false
}

func (w *batchLockWaits) probe(q QueuedRequest, p batchProgress, scope, identity string, at time.Time, acquired bool) {
	key := batchLockKey{q.ID, scope}
	w.mu.Lock()
	wait, exists := w.pending[key]
	same := exists && wait.claimed.Equal(q.startedAt) && wait.updated.Equal(p.Updated) && wait.identity == identity
	if acquired {
		delete(w.pending, key)
	} else if !same {
		w.pending[key] = batchLockWait{q.startedAt, p.Updated, at, identity}
	}
	w.mu.Unlock()

	if acquired && same {
		telemetry.ObserveBatchLockContention(scope, at.Sub(wait.since))
	}
}

func (w *batchLockWaits) discard(jobID string) {
	w.mu.Lock()
	defer w.mu.Unlock()
	delete(w.pending, batchLockKey{jobID, "platform"})
	delete(w.pending, batchLockKey{jobID, "target"})
}

// Another process may finish or change a batch between passes. Reap those
// entries without touching an interval opened while the database read was in flight.
func (w *batchLockWaits) prune(ctx context.Context, pool *pgxpool.Pool) {
	w.mu.Lock()
	stale := maps.Clone(w.pending)
	w.mu.Unlock()
	if len(stale) == 0 {
		return
	}

	ids := make([]string, 0, len(stale))
	for key := range stale {
		ids = append(ids, key.jobID)
	}
	ctx, cancel := context.WithTimeout(telemetry.WithoutQueryTelemetry(ctx), time.Second)
	defer cancel()
	rows, err := pool.Query(ctx, `SELECT r.id,r.started_at,b.updated_at
 FROM dmp_migration.change_requests r JOIN dmp_migration.batch_jobs b ON b.id=r.id
 WHERE r.status='running' AND r.id=ANY($1)`, ids)
	if err != nil {
		return
	}
	defer rows.Close()
	for rows.Next() {
		var id string
		var claimed, updated time.Time
		if err = rows.Scan(&id, &claimed, &updated); err != nil {
			return
		}
		for _, scope := range []string{"platform", "target"} {
			key := batchLockKey{id, scope}
			if wait, exists := stale[key]; exists && wait.claimed.Equal(claimed) && wait.updated.Equal(updated) {
				delete(stale, key)
			}
		}
	}
	if rows.Err() != nil {
		return
	}
	w.mu.Lock()
	defer w.mu.Unlock()
	for key, wait := range stale {
		if w.pending[key] == wait {
			delete(w.pending, key)
		}
	}
}
