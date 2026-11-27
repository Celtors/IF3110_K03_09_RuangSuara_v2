package migration

import (
	"context"
	"time"

	"backend/internal/telemetry"

	"github.com/prometheus/client_golang/prometheus"
)

var batchStates = []string{"ready", "running", "partial", "paused", "unresolved", "completed", "failed", "compensating", "reverted"}

// RegisterStateMetrics reads both state machines from a single database snapshot.
func (r *Repository) RegisterStateMetrics(registry *prometheus.Registry) {
	registry.MustRegister(stateMetrics{
		repo: r,
		requests: prometheus.NewDesc(telemetry.Namespace+"_change_requests",
			"Current change requests by workflow status.", []string{"status"}, nil),
		batches: prometheus.NewDesc(telemetry.Namespace+"_batch_jobs",
			"Current batch jobs by durable execution state.", []string{"state"}, nil),
		health: prometheus.NewDesc(telemetry.Namespace+"_state_collection_success",
			"Whether the scrape could read request and batch state from the platform database.", nil, nil),
	})
}

type stateMetrics struct {
	repo                      *Repository
	requests, batches, health *prometheus.Desc
}

func (s stateMetrics) Describe(ch chan<- *prometheus.Desc) {
	ch <- s.requests
	ch <- s.batches
	ch <- s.health
}

func (s stateMetrics) Collect(ch chan<- prometheus.Metric) {
	requests, batches, err := s.counts()
	if err != nil {
		ch <- prometheus.MustNewConstMetric(s.health, prometheus.GaugeValue, 0)
		return
	}
	ch <- prometheus.MustNewConstMetric(s.health, prometheus.GaugeValue, 1)
	for status := range allRequestStatuses {
		ch <- prometheus.MustNewConstMetric(s.requests, prometheus.GaugeValue, float64(requests[string(status)]), string(status))
	}
	for _, state := range batchStates {
		ch <- prometheus.MustNewConstMetric(s.batches, prometheus.GaugeValue, float64(batches[state]), state)
	}
}

func (s stateMetrics) counts() (requests, batches map[string]int64, err error) {
	ctx, cancel := context.WithTimeout(telemetry.WithoutQueryTelemetry(context.Background()), time.Second)
	defer cancel()
	rows, err := s.repo.Pool.Query(ctx, `
 SELECT false, status, count(*) FROM dmp_migration.change_requests GROUP BY status
 UNION ALL
 SELECT true, state, count(*) FROM dmp_migration.batch_jobs GROUP BY state`)
	if err != nil {
		return nil, nil, err
	}
	defer rows.Close()
	requests, batches = make(map[string]int64), make(map[string]int64)
	for rows.Next() {
		var batch bool
		var state string
		var count int64
		if err = rows.Scan(&batch, &state, &count); err != nil {
			return nil, nil, err
		}
		if batch {
			batches[state] = count
		} else {
			requests[state] = count
		}
	}
	return requests, batches, rows.Err()
}
