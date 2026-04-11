package monitor

import (
	"context"
	"log/slog"
	"sync"
	"time"

	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/kubernetes"

	"github.com/josephtknight/k8s-amt-node-watchdog/internal/config"
	"github.com/josephtknight/k8s-amt-node-watchdog/internal/metrics"
)

// Restarter is called when a node has been NotReady long enough.
type Restarter interface {
	MaybeRestart(ctx context.Context, node *corev1.Node, notReadySince time.Time) error
}

// Monitor polls the Kubernetes API for node status and tracks NotReady durations.
type Monitor struct {
	cfg       *config.Config
	clientset kubernetes.Interface
	restarter Restarter

	mu             sync.Mutex
	notReadySince  map[string]time.Time
}

func New(cfg *config.Config, clientset kubernetes.Interface, restarter Restarter) *Monitor {
	return &Monitor{
		cfg:           cfg,
		clientset:     clientset,
		restarter:     restarter,
		notReadySince: make(map[string]time.Time),
	}
}

// Run starts the polling loop. It blocks until ctx is cancelled.
func (m *Monitor) Run(ctx context.Context) {
	slog.Info("monitor started", "poll_interval", m.cfg.PollInterval, "threshold", m.cfg.NotReadyThreshold)

	ticker := time.NewTicker(m.cfg.PollInterval)
	defer ticker.Stop()

	// Poll immediately on start, then on each tick
	m.poll(ctx)
	for {
		select {
		case <-ctx.Done():
			slog.Info("monitor stopped")
			return
		case <-ticker.C:
			m.poll(ctx)
		}
	}
}

func (m *Monitor) poll(ctx context.Context) {
	nodes, err := m.clientset.CoreV1().Nodes().List(ctx, metav1.ListOptions{})
	if err != nil {
		slog.Error("failed to list nodes, skipping tick", "error", err)
		return
	}

	m.mu.Lock()
	defer m.mu.Unlock()

	now := time.Now()
	currentNotReady := make(map[string]bool)

	for i := range nodes.Items {
		node := &nodes.Items[i]
		ready := isNodeReady(node)

		if !ready {
			currentNotReady[node.Name] = true
			if _, tracked := m.notReadySince[node.Name]; !tracked {
				m.notReadySince[node.Name] = now
				slog.Warn("node became NotReady", "node", node.Name)
			}

			since := m.notReadySince[node.Name]
			duration := now.Sub(since)

			if duration >= m.cfg.NotReadyThreshold {
				slog.Warn("node exceeded NotReady threshold",
					"node", node.Name,
					"not_ready_for", duration.Round(time.Second),
					"threshold", m.cfg.NotReadyThreshold,
				)
				if err := m.restarter.MaybeRestart(ctx, node, since); err != nil {
					slog.Error("restart attempt failed", "node", node.Name, "error", err)
				}
			} else {
				slog.Info("node NotReady, waiting for threshold",
					"node", node.Name,
					"not_ready_for", duration.Round(time.Second),
					"remaining", (m.cfg.NotReadyThreshold - duration).Round(time.Second),
				)
			}
		}
	}

	// Clear nodes that returned to Ready
	for name := range m.notReadySince {
		if !currentNotReady[name] {
			slog.Info("node returned to Ready", "node", name)
			delete(m.notReadySince, name)
		}
	}

	metrics.NodesNotReady.Set(float64(len(currentNotReady)))
}

func isNodeReady(node *corev1.Node) bool {
	for _, cond := range node.Status.Conditions {
		if cond.Type == corev1.NodeReady {
			return cond.Status == corev1.ConditionTrue
		}
	}
	return false
}

// NotReadySince returns the tracked NotReady times (for testing).
func (m *Monitor) NotReadySince() map[string]time.Time {
	m.mu.Lock()
	defer m.mu.Unlock()
	result := make(map[string]time.Time, len(m.notReadySince))
	for k, v := range m.notReadySince {
		result[k] = v
	}
	return result
}
