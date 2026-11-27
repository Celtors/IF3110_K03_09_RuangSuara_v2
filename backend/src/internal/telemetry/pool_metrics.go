package telemetry

import (
	"database/sql"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/prometheus/client_golang/prometheus"
)

func RegisterPoolMetrics(registry *prometheus.Registry, platform *pgxpool.Pool, platformSQL *sql.DB, targets func() []*pgxpool.Stat) {
	desc := func(name, help string) *prometheus.Desc {
		return prometheus.NewDesc(Namespace+"_db_pool_"+name, help, []string{"role"}, nil)
	}
	registry.MustRegister(poolMetrics{
		platform: platform, platformSQL: platformSQL, targets: targets,
		active: desc("active_connections", "Acquired or in-use database connections."),
		idle:   desc("idle_connections", "Idle database connections."),
		max:    desc("max_connections", "Configured database pool capacity."),
		waits:  desc("waits_total", "Connection waits reported by the pool driver."),
		wait:   desc("wait_duration_seconds_total", "Cumulative connection wait time reported by the pool driver."),
	})
}

type poolMetrics struct {
	platform    *pgxpool.Pool
	platformSQL *sql.DB
	targets     func() []*pgxpool.Stat

	active, idle, max, waits, wait *prometheus.Desc
}

func (p poolMetrics) Describe(ch chan<- *prometheus.Desc) {
	ch <- p.active
	ch <- p.idle
	ch <- p.max
	ch <- p.waits
	ch <- p.wait
}

type poolSnapshot struct {
	active, idle, max, waits int64
	waitDuration             time.Duration
}

func (p poolMetrics) Collect(ch chan<- prometheus.Metric) {
	if p.platform != nil {
		p.publish(ch, "platform_migration", summarizePGX([]*pgxpool.Stat{p.platform.Stat()}))
	}
	if p.platformSQL != nil {
		stat := p.platformSQL.Stats()
		p.publish(ch, "platform_sql", poolSnapshot{
			active: int64(stat.InUse), idle: int64(stat.Idle), max: int64(stat.MaxOpenConnections),
			waits: stat.WaitCount, waitDuration: stat.WaitDuration,
		})
	}
	if p.targets != nil {
		p.publish(ch, "target_migration", summarizePGX(p.targets()))
	}
}

func summarizePGX(stats []*pgxpool.Stat) (snapshot poolSnapshot) {
	for _, stat := range stats {
		snapshot.active += int64(stat.AcquiredConns())
		snapshot.idle += int64(stat.IdleConns())
		snapshot.max += int64(stat.MaxConns())
		snapshot.waits += stat.EmptyAcquireCount()
		snapshot.waitDuration += stat.EmptyAcquireWaitTime()
	}
	return snapshot
}

func (p poolMetrics) publish(ch chan<- prometheus.Metric, role string, s poolSnapshot) {
	ch <- prometheus.MustNewConstMetric(p.active, prometheus.GaugeValue, float64(s.active), role)
	ch <- prometheus.MustNewConstMetric(p.idle, prometheus.GaugeValue, float64(s.idle), role)
	ch <- prometheus.MustNewConstMetric(p.max, prometheus.GaugeValue, float64(s.max), role)
	ch <- prometheus.MustNewConstMetric(p.waits, prometheus.CounterValue, float64(s.waits), role)
	ch <- prometheus.MustNewConstMetric(p.wait, prometheus.CounterValue, s.waitDuration.Seconds(), role)
}
