package migration

import (
	"context"
	"time"

	"backend/internal/telemetry"

	"github.com/prometheus/client_golang/prometheus"
)

var batchStates = []string{"ready", "running", "partial", "paused", "unresolved", "completed", "failed", "compensating", "reverted"}

func registerWorkerMetrics(registry *prometheus.Registry, slots <-chan struct{}) func() {
	if registry == nil {
		return func() {}
	}
	occupied := prometheus.NewGaugeFunc(prometheus.GaugeOpts{
		Namespace: telemetry.Namespace, Name: "worker_slots_occupied",
		Help: "Worker slots occupied by claim processing or in-flight migration jobs.",
	}, func() float64 { return float64(len(slots)) })
	capacity := prometheus.NewGaugeFunc(prometheus.GaugeOpts{
		Namespace: telemetry.Namespace, Name: "worker_slots_max",
		Help: "Maximum migration worker slots.",
	}, func() float64 { return float64(cap(slots)) })
	registry.MustRegister(occupied, capacity)
	return func() {
		registry.Unregister(occupied)
		registry.Unregister(capacity)
	}
}

// RegisterStateMetrics reads workflow state and queue diagnostics from one database snapshot.
func (r *Repository) RegisterStateMetrics(registry *prometheus.Registry) {
	registry.MustRegister(stateMetrics{
		repo: r,
		requests: prometheus.NewDesc(telemetry.Namespace+"_change_requests",
			"Current change requests by workflow status.", []string{"status"}, nil),
		batches: prometheus.NewDesc(telemetry.Namespace+"_batch_jobs",
			"Current batch jobs by durable execution state.", []string{"state"}, nil),
		oldest: prometheus.NewDesc(telemetry.Namespace+"_oldest_queued_timestamp_seconds",
			"Unix timestamp of the oldest currently queued request's latest queue entry, or zero if the queue is empty.", nil, nil),
		blocked: prometheus.NewDesc(telemetry.Namespace+"_database_blocked_queued_requests",
			"Queued requests whose database has a running or unresolved request or attempt.", nil, nil),
		health: prometheus.NewDesc(telemetry.Namespace+"_state_collection_success",
			"Whether the scrape could read workflow state and queue diagnostics from the platform database.", nil, nil),
	})
}

type stateMetrics struct {
	repo                                       *Repository
	requests, batches, oldest, blocked, health *prometheus.Desc
}

func (s stateMetrics) Describe(ch chan<- *prometheus.Desc) {
	ch <- s.requests
	ch <- s.batches
	ch <- s.oldest
	ch <- s.blocked
	ch <- s.health
}

func (s stateMetrics) Collect(ch chan<- prometheus.Metric) {
	ctx, cancel := context.WithTimeout(telemetry.WithoutQueryTelemetry(context.Background()), time.Second)
	defer cancel()

	var requests, batches map[string]int64
	var oldest float64
	var blocked int64
	// updated_at records the latest requeue, as used by the claim wait metric.
	err := s.repo.Pool.QueryRow(ctx, `
 SELECT
   (SELECT jsonb_object_agg(status, n) FROM
     (SELECT status, count(*) AS n FROM dmp_migration.change_requests GROUP BY status) counts),
   (SELECT jsonb_object_agg(state, n) FROM
     (SELECT state, count(*) AS n FROM dmp_migration.batch_jobs GROUP BY state) counts),
   COALESCE(EXTRACT(EPOCH FROM min(GREATEST(r.queued_at,r.updated_at))),0)::double precision,
   count(*) FILTER (WHERE `+queuedDatabaseBlocked+`)
 FROM dmp_migration.change_requests r WHERE r.status='queued'`).Scan(&requests, &batches, &oldest, &blocked)
	if err != nil {
		ch <- prometheus.MustNewConstMetric(s.health, prometheus.GaugeValue, 0)
		return
	}
	ch <- prometheus.MustNewConstMetric(s.health, prometheus.GaugeValue, 1)
	ch <- prometheus.MustNewConstMetric(s.oldest, prometheus.GaugeValue, oldest)
	ch <- prometheus.MustNewConstMetric(s.blocked, prometheus.GaugeValue, float64(blocked))
	for status := range allRequestStatuses {
		ch <- prometheus.MustNewConstMetric(s.requests, prometheus.GaugeValue, float64(requests[string(status)]), string(status))
	}
	for _, state := range batchStates {
		ch <- prometheus.MustNewConstMetric(s.batches, prometheus.GaugeValue, float64(batches[state]), state)
	}
}
