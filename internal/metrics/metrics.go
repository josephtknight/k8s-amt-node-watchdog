package metrics

import (
	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/promauto"
)

var (
	NodesNotReady = promauto.NewGauge(prometheus.GaugeOpts{
		Name: "watchdog_nodes_not_ready",
		Help: "Current number of NotReady nodes being tracked",
	})

	RestartsTotal = promauto.NewCounterVec(prometheus.CounterOpts{
		Name: "watchdog_restarts_total",
		Help: "Total number of restart attempts by outcome",
	}, []string{"node", "result"})

	RestartDuration = promauto.NewHistogramVec(prometheus.HistogramOpts{
		Name:    "watchdog_restart_duration_seconds",
		Help:    "Duration of AMT power cycle calls",
		Buckets: prometheus.DefBuckets,
	}, []string{"node"})

	QuorumBlockedTotal = promauto.NewCounter(prometheus.CounterOpts{
		Name: "watchdog_quorum_blocked_total",
		Help: "Number of restarts blocked by safety checks",
	})

	Leader = promauto.NewGauge(prometheus.GaugeOpts{
		Name: "watchdog_leader",
		Help: "1 if this replica is the current leader",
	})
)
