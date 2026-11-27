package telemetry

import (
	"time"

	"github.com/prometheus/client_golang/prometheus"
)

const Namespace = "ruangsuara"

var (
	httpRequests = counter("http_requests_total", "Completed application HTTP requests.", "method", "route", "status_class")
	httpDuration = histogram("http_request_duration_seconds", "Application HTTP request duration.", []float64{.001, .005, .01, .025, .05, .1, .25, .5, 1, 2.5, 5, 15, 45}, "method", "route", "status_class")
	queueWait    = histogram("queue_wait_duration_seconds", "Time from publication or recovery requeue to a committed claim.", []float64{.05, .1, .25, .5, 1, 2, 5, 10, 30, 60, 120, 300, 900}, "claim")
	lockProbe    = histogram("lock_acquisition_duration_seconds", "Duration of a single non-blocking advisory-lock query, excluding pool acquisition.", []float64{.0005, .001, .0025, .005, .01, .025, .05, .1, .25, .5, 1, 5}, "scope", "outcome")
	lockBusy     = counter("lock_contention_total", "Advisory-lock probes declined by PostgreSQL because the lock was held.", "scope")
	completions  = counter("migration_completions_total", "Execution attempts with durably finalized outcomes.", "mode", "operation", "outcome")
	attemptTime  = histogram("migration_execution_duration_seconds", "Time from durable attempt start to terminal finalization, including retries and pauses.", migrationBuckets, "mode", "outcome")
	jobTime      = histogram("job_duration_seconds", "Time from initial queue publication to terminal request finalization.", migrationBuckets, "outcome")
	retries      = counter("migration_retries_total", "Repeated execution actions by category.", "kind")
	batches      = counter("batch_commits_total", "Newly mirrored target-verified data batches.", "action")
	batchRows    = counter("batch_rows_total", "Rows in newly mirrored target-verified data batches.", "action")

	ordinaryLockWait = prometheus.NewHistogram(prometheus.HistogramOpts{
		Namespace: Namespace, Name: "ordinary_advisory_lock_retry_wait_seconds",
		Help:    "Accumulated elapsed retry backoff after confirmed busy target advisory locks, recorded after the ordinary attempt's retry loop, including failed attempts that performed such backoff.",
		Buckets: []float64{.125, .25, .3, .5, .6, .75, .85, 1, 2, 5},
	})
	batchLockWait = histogram("batch_advisory_lock_contention_interval_seconds",
		"Elapsed time from the first confirmed busy batch advisory-lock probe to a later acquisition in the same worker and batch execution phase, including delays between passes.",
		migrationBuckets, "scope")
)

var migrationBuckets = []float64{.01, .05, .1, .25, .5, 1, 2.5, 5, 10, 30, 60, 120, 300, 900, 3600}

func counter(name, help string, labels ...string) *prometheus.CounterVec {
	return prometheus.NewCounterVec(prometheus.CounterOpts{Namespace: Namespace, Name: name, Help: help}, labels)
}

func histogram(name, help string, buckets []float64, labels ...string) *prometheus.HistogramVec {
	return prometheus.NewHistogramVec(prometheus.HistogramOpts{Namespace: Namespace, Name: name, Help: help, Buckets: buckets}, labels)
}

// Event instruments follow the process lifetime. The registry owns exposition.
func NewMetricsRegistry() *prometheus.Registry {
	for _, operation := range []string{"apply", "rollback", "forward"} {
		for _, outcome := range []string{"succeeded", "failed"} {
			completions.WithLabelValues("ordinary", operation, outcome)
		}
	}
	for _, outcome := range []string{"succeeded", "failed"} {
		completions.WithLabelValues("batch", "apply", outcome)
	}
	for _, action := range []string{"apply", "compensate"} {
		batches.WithLabelValues(action)
		batchRows.WithLabelValues(action)
	}
	for _, scope := range []string{"platform", "target"} {
		lockBusy.WithLabelValues(scope)
		batchLockWait.WithLabelValues(scope)
	}
	for _, kind := range []string{"ordinary_transaction", "batch_transaction", "worker_reclaim", "batch_reentry", "failed_apply", "batch_resume_accepted"} {
		retries.WithLabelValues(kind)
	}

	registry := prometheus.NewRegistry()
	registry.MustRegister(httpRequests, httpDuration, queueWait, lockProbe, lockBusy,
		ordinaryLockWait, batchLockWait, completions, attemptTime, jobTime, retries, batches, batchRows)
	return registry
}

func ObserveQueueClaim(queued, updated, started time.Time) {
	claim := "initial"
	if updated.After(queued) {
		claim, queued = "reclaim", updated
		RecordRetry("worker_reclaim")
	}
	queueWait.WithLabelValues(claim).Observe(max(0, started.Sub(queued).Seconds()))
}

func ObserveLockProbe(scope string, elapsed time.Duration, acquired bool, err error) {
	outcome := "acquired"
	switch {
	case err != nil:
		outcome = "error"
	case !acquired:
		outcome = "busy"
		lockBusy.WithLabelValues(scope).Inc()
	}
	lockProbe.WithLabelValues(scope, outcome).Observe(elapsed.Seconds())
}

func ObserveOrdinaryLockRetryWait(elapsed time.Duration) {
	ordinaryLockWait.Observe(elapsed.Seconds())
}

func ObserveBatchLockContention(scope string, elapsed time.Duration) {
	batchLockWait.WithLabelValues(scope).Observe(elapsed.Seconds())
}

func ObserveCompletion(mode, operation, outcome string, elapsed time.Duration) {
	completions.WithLabelValues(mode, operation, outcome).Inc()
	attemptTime.WithLabelValues(mode, outcome).Observe(elapsed.Seconds())
}

func ObserveJobCompletion(outcome string, elapsed time.Duration) {
	jobTime.WithLabelValues(outcome).Observe(elapsed.Seconds())
}

func RecordRetry(kind string) {
	retries.WithLabelValues(kind).Inc()
}

func RecordBatchProgress(action string, committedBatches, rows int64) {
	if committedBatches > 0 {
		batches.WithLabelValues(action).Add(float64(committedBatches))
		batchRows.WithLabelValues(action).Add(float64(rows))
	}
}
