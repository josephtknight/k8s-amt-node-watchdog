package restarter

import (
	"context"
	"fmt"
	"log/slog"
	"sync"
	"time"

	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/kubernetes/scheme"
	typedcorev1 "k8s.io/client-go/kubernetes/typed/core/v1"
	"k8s.io/client-go/tools/record"

	"github.com/josephtknight/k8s-amt-node-watchdog/internal/amt"
	"github.com/josephtknight/k8s-amt-node-watchdog/internal/config"
	"github.com/josephtknight/k8s-amt-node-watchdog/internal/metrics"
)

// Restarter orchestrates node power-cycle operations with safety checks.
type Restarter struct {
	cfg       *config.Config
	clientset kubernetes.Interface
	cycler    amt.PowerCycler
	recorder  record.EventRecorder

	mu        sync.Mutex
	cooldowns map[string]time.Time // node name -> last restart time
}

func New(cfg *config.Config, clientset kubernetes.Interface, cycler amt.PowerCycler) *Restarter {
	eventBroadcaster := record.NewBroadcaster()
	eventBroadcaster.StartRecordingToSink(&typedcorev1.EventSinkImpl{
		Interface: clientset.CoreV1().Events(""),
	})
	recorder := eventBroadcaster.NewRecorder(scheme.Scheme, corev1.EventSource{Component: "node-watchdog"})

	return &Restarter{
		cfg:       cfg,
		clientset: clientset,
		cycler:    cycler,
		recorder:  recorder,
		cooldowns: make(map[string]time.Time),
	}
}

// NewWithRecorder creates a Restarter with a custom event recorder (for testing).
func NewWithRecorder(cfg *config.Config, clientset kubernetes.Interface, cycler amt.PowerCycler, recorder record.EventRecorder) *Restarter {
	return &Restarter{
		cfg:       cfg,
		clientset: clientset,
		cycler:    cycler,
		recorder:  recorder,
		cooldowns: make(map[string]time.Time),
	}
}

// MaybeRestart evaluates safety checks and power-cycles a node if all pass.
func (r *Restarter) MaybeRestart(ctx context.Context, node *corev1.Node, notReadySince time.Time) error {
	r.mu.Lock()
	defer r.mu.Unlock()

	name := node.Name

	// 1. Cooldown check
	if lastRestart, ok := r.cooldowns[name]; ok {
		if time.Since(lastRestart) < r.cfg.CooldownPeriod {
			slog.Info("skipping restart: cooldown active",
				"node", name,
				"last_restart", lastRestart,
				"cooldown_remaining", (r.cfg.CooldownPeriod - time.Since(lastRestart)).Round(time.Second),
			)
			metrics.QuorumBlockedTotal.Inc()
			return nil
		}
	}

	// 2. Concurrency check
	inFlight := r.countInFlight()
	if inFlight >= r.cfg.MaxConcurrentRestarts {
		slog.Info("skipping restart: max concurrent restarts reached",
			"node", name,
			"in_flight", inFlight,
			"max", r.cfg.MaxConcurrentRestarts,
		)
		metrics.QuorumBlockedTotal.Inc()
		return nil
	}

	// 3. Cluster health floor
	readyCount, totalCount, err := r.clusterHealth(ctx)
	if err != nil {
		return fmt.Errorf("checking cluster health: %w", err)
	}

	majority := (totalCount / 2) + 1
	if readyCount < majority {
		// Degraded cluster — allow only one restart at a time
		if inFlight > 0 {
			slog.Warn("skipping restart: cluster degraded and restart already in-flight",
				"node", name,
				"ready", readyCount,
				"total", totalCount,
			)
			metrics.QuorumBlockedTotal.Inc()
			return nil
		}
		slog.Warn("cluster is degraded, proceeding with single restart",
			"node", name,
			"ready", readyCount,
			"total", totalCount,
		)
	}

	// 4. Control-plane quorum check
	if isControlPlane(node) {
		cpReady, cpTotal := r.controlPlaneHealth(ctx)
		// Need majority of control-plane nodes to maintain etcd quorum
		cpMajority := (cpTotal / 2) + 1
		if cpReady-1 < cpMajority { // -1 because we'd be taking one down
			slog.Warn("skipping restart: would risk etcd quorum",
				"node", name,
				"cp_ready", cpReady,
				"cp_total", cpTotal,
				"cp_majority_needed", cpMajority,
			)
			metrics.QuorumBlockedTotal.Inc()
			return nil
		}
	}

	// 5. Resolve AMT IP
	amtIP := resolveAMTIP(node, r.cfg.AMTAnnotation)
	if amtIP == "" {
		return fmt.Errorf("no AMT IP found for node %s (no annotation %q and no InternalIP)", name, r.cfg.AMTAnnotation)
	}

	// 6. Record pre-restart event
	r.recorder.Eventf(node, corev1.EventTypeWarning, "PowerCycleInitiated",
		"Node has been NotReady since %s (%s), initiating AMT power cycle to %s (dry_run=%v)",
		notReadySince.Format(time.RFC3339), time.Since(notReadySince).Round(time.Second), amtIP, r.cfg.DryRun)

	// 7. Execute power cycle
	if r.cfg.DryRun {
		slog.Warn("DRY RUN: would power-cycle node",
			"node", name,
			"amt_ip", amtIP,
			"not_ready_since", notReadySince,
		)
		r.recorder.Eventf(node, corev1.EventTypeWarning, "PowerCycleDryRun",
			"DRY RUN: Would have power-cycled node via AMT at %s", amtIP)
		metrics.RestartsTotal.WithLabelValues(name, "dry_run").Inc()
		r.cooldowns[name] = time.Now()
		return nil
	}

	start := time.Now()
	err = r.cycler.PowerCycle(amtIP)
	duration := time.Since(start)
	metrics.RestartDuration.WithLabelValues(name).Observe(duration.Seconds())

	// 8. Record result
	if err != nil {
		r.recorder.Eventf(node, corev1.EventTypeWarning, "PowerCycleFailed",
			"AMT power cycle failed for %s: %v", amtIP, err)
		metrics.RestartsTotal.WithLabelValues(name, "error").Inc()
		// Do NOT record cooldown — allow retry on next cycle
		return fmt.Errorf("power cycle failed for %s: %w", name, err)
	}

	r.recorder.Eventf(node, corev1.EventTypeWarning, "PowerCycleSucceeded",
		"AMT power cycle succeeded for %s (took %s)", amtIP, duration.Round(time.Millisecond))
	metrics.RestartsTotal.WithLabelValues(name, "success").Inc()
	r.cooldowns[name] = time.Now()

	slog.Info("power cycle completed",
		"node", name,
		"amt_ip", amtIP,
		"duration", duration.Round(time.Millisecond),
	)

	return nil
}

// countInFlight returns how many nodes were restarted in the last 5 minutes
// and are still potentially coming back up.
func (r *Restarter) countInFlight() int {
	count := 0
	cutoff := time.Now().Add(-5 * time.Minute)
	for _, t := range r.cooldowns {
		if t.After(cutoff) {
			count++
		}
	}
	return count
}

func (r *Restarter) clusterHealth(ctx context.Context) (ready, total int, err error) {
	nodes, err := r.clientset.CoreV1().Nodes().List(ctx, metav1.ListOptions{})
	if err != nil {
		return 0, 0, err
	}
	total = len(nodes.Items)
	for i := range nodes.Items {
		if nodeIsReady(&nodes.Items[i]) {
			ready++
		}
	}
	return ready, total, nil
}

func (r *Restarter) controlPlaneHealth(ctx context.Context) (ready, total int) {
	nodes, err := r.clientset.CoreV1().Nodes().List(ctx, metav1.ListOptions{})
	if err != nil {
		return 0, 0
	}
	for i := range nodes.Items {
		if isControlPlane(&nodes.Items[i]) {
			total++
			if nodeIsReady(&nodes.Items[i]) {
				ready++
			}
		}
	}
	return ready, total
}

func isControlPlane(node *corev1.Node) bool {
	_, ok1 := node.Labels["node-role.kubernetes.io/control-plane"]
	_, ok2 := node.Labels["node-role.kubernetes.io/master"]
	return ok1 || ok2
}

func nodeIsReady(node *corev1.Node) bool {
	for _, cond := range node.Status.Conditions {
		if cond.Type == corev1.NodeReady {
			return cond.Status == corev1.ConditionTrue
		}
	}
	return false
}

func resolveAMTIP(node *corev1.Node, annotation string) string {
	if ip, ok := node.Annotations[annotation]; ok && ip != "" {
		return ip
	}
	for _, addr := range node.Status.Addresses {
		if addr.Type == corev1.NodeInternalIP {
			return addr.Address
		}
	}
	return ""
}
