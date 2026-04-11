package restarter

import (
	"context"
	"fmt"
	"testing"
	"time"

	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/kubernetes/fake"
	"k8s.io/client-go/tools/record"

	"github.com/josephtknight/k8s-amt-node-watchdog/internal/config"
)

type mockCycler struct {
	calls []string
	err   error
}

func (m *mockCycler) PowerCycle(ip string) error {
	m.calls = append(m.calls, ip)
	return m.err
}

func baseCfg() *config.Config {
	return &config.Config{
		NotReadyThreshold:    15 * time.Minute,
		CooldownPeriod:       1 * time.Hour,
		MaxConcurrentRestarts: 1,
		AMTPort:              16992,
		AMTAnnotation:        "watchdog.example.com/amt-ip",
		DryRun:               false,
	}
}

func nodeWithIP(name, ip string) *corev1.Node {
	return &corev1.Node{
		ObjectMeta: metav1.ObjectMeta{Name: name},
		Status: corev1.NodeStatus{
			Conditions: []corev1.NodeCondition{
				{Type: corev1.NodeReady, Status: corev1.ConditionFalse},
			},
			Addresses: []corev1.NodeAddress{
				{Type: corev1.NodeInternalIP, Address: ip},
			},
		},
	}
}

func nodeWithAnnotation(name, ip, annotation, amtIP string) *corev1.Node {
	n := nodeWithIP(name, ip)
	n.Annotations = map[string]string{annotation: amtIP}
	return n
}

func controlPlaneNode(name, ip string) *corev1.Node {
	n := nodeWithIP(name, ip)
	n.Labels = map[string]string{"node-role.kubernetes.io/control-plane": ""}
	return n
}

func readyControlPlaneNode(name, ip string) *corev1.Node {
	n := controlPlaneNode(name, ip)
	n.Status.Conditions[0].Status = corev1.ConditionTrue
	return n
}

func TestMaybeRestart_DryRun(t *testing.T) {
	cfg := baseCfg()
	cfg.DryRun = true

	node := nodeWithIP("node-1", "10.0.0.1")
	clientset := fake.NewSimpleClientset(node)
	cycler := &mockCycler{}
	recorder := record.NewFakeRecorder(10)
	r := NewWithRecorder(cfg, clientset, cycler, recorder)

	err := r.MaybeRestart(context.Background(), node, time.Now().Add(-20*time.Minute))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(cycler.calls) != 0 {
		t.Error("should not call PowerCycle in dry run mode")
	}
}

func TestMaybeRestart_Success(t *testing.T) {
	cfg := baseCfg()
	node := nodeWithIP("node-1", "10.0.0.1")
	clientset := fake.NewSimpleClientset(node)
	cycler := &mockCycler{}
	recorder := record.NewFakeRecorder(10)
	r := NewWithRecorder(cfg, clientset, cycler, recorder)

	err := r.MaybeRestart(context.Background(), node, time.Now().Add(-20*time.Minute))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(cycler.calls) != 1 || cycler.calls[0] != "10.0.0.1" {
		t.Errorf("expected PowerCycle call to 10.0.0.1, got: %v", cycler.calls)
	}
}

func TestMaybeRestart_AMTAnnotationOverride(t *testing.T) {
	cfg := baseCfg()
	node := nodeWithAnnotation("node-1", "10.0.0.1", cfg.AMTAnnotation, "192.168.1.100")
	clientset := fake.NewSimpleClientset(node)
	cycler := &mockCycler{}
	recorder := record.NewFakeRecorder(10)
	r := NewWithRecorder(cfg, clientset, cycler, recorder)

	err := r.MaybeRestart(context.Background(), node, time.Now().Add(-20*time.Minute))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(cycler.calls) != 1 || cycler.calls[0] != "192.168.1.100" {
		t.Errorf("expected PowerCycle call to annotation IP 192.168.1.100, got: %v", cycler.calls)
	}
}

func TestMaybeRestart_CooldownPreventsRestart(t *testing.T) {
	cfg := baseCfg()
	node := nodeWithIP("node-1", "10.0.0.1")
	clientset := fake.NewSimpleClientset(node)
	cycler := &mockCycler{}
	recorder := record.NewFakeRecorder(10)
	r := NewWithRecorder(cfg, clientset, cycler, recorder)

	// First restart succeeds
	r.MaybeRestart(context.Background(), node, time.Now().Add(-20*time.Minute))
	if len(cycler.calls) != 1 {
		t.Fatal("first restart should succeed")
	}

	// Second restart should be blocked by cooldown
	r.MaybeRestart(context.Background(), node, time.Now().Add(-20*time.Minute))
	if len(cycler.calls) != 1 {
		t.Error("second restart should be blocked by cooldown")
	}
}

func TestMaybeRestart_ConcurrencyLimit(t *testing.T) {
	cfg := baseCfg()
	cfg.MaxConcurrentRestarts = 1

	node1 := nodeWithIP("node-1", "10.0.0.1")
	node2 := nodeWithIP("node-2", "10.0.0.2")
	clientset := fake.NewSimpleClientset(node1, node2)
	cycler := &mockCycler{}
	recorder := record.NewFakeRecorder(10)
	r := NewWithRecorder(cfg, clientset, cycler, recorder)

	// Restart node-1 (within 5min window = in-flight)
	r.MaybeRestart(context.Background(), node1, time.Now().Add(-20*time.Minute))
	if len(cycler.calls) != 1 {
		t.Fatal("first restart should succeed")
	}

	// Node-2 should be blocked by concurrency limit since node-1 was just restarted
	r.MaybeRestart(context.Background(), node2, time.Now().Add(-20*time.Minute))
	if len(cycler.calls) != 1 {
		t.Error("second restart should be blocked by concurrency limit")
	}
}

func TestMaybeRestart_PowerCycleFailure_NoCooldown(t *testing.T) {
	cfg := baseCfg()
	node := nodeWithIP("node-1", "10.0.0.1")
	clientset := fake.NewSimpleClientset(node)
	cycler := &mockCycler{err: fmt.Errorf("connection refused")}
	recorder := record.NewFakeRecorder(10)
	r := NewWithRecorder(cfg, clientset, cycler, recorder)

	err := r.MaybeRestart(context.Background(), node, time.Now().Add(-20*time.Minute))
	if err == nil {
		t.Fatal("expected error")
	}

	// Cooldown should NOT be set on failure, so a retry should be allowed
	// Reset the error so next call succeeds
	cycler.err = nil
	err = r.MaybeRestart(context.Background(), node, time.Now().Add(-20*time.Minute))
	if err != nil {
		t.Fatalf("retry should succeed: %v", err)
	}
	if len(cycler.calls) != 2 {
		t.Errorf("expected 2 PowerCycle calls (1 failed + 1 retry), got %d", len(cycler.calls))
	}
}

func TestMaybeRestart_ControlPlaneQuorum(t *testing.T) {
	cfg := baseCfg()

	// 3 control-plane nodes: 2 ready, 1 not ready (the target)
	target := controlPlaneNode("cp-1", "10.0.0.1")
	cp2 := readyControlPlaneNode("cp-2", "10.0.0.2")
	cp3 := readyControlPlaneNode("cp-3", "10.0.0.3")
	clientset := fake.NewSimpleClientset(target, cp2, cp3)
	cycler := &mockCycler{}
	recorder := record.NewFakeRecorder(10)
	r := NewWithRecorder(cfg, clientset, cycler, recorder)

	// With 2 ready out of 3, restarting would leave 1 ready < majority(2)
	// So it should be blocked
	err := r.MaybeRestart(context.Background(), target, time.Now().Add(-20*time.Minute))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(cycler.calls) != 0 {
		t.Error("should block restart to protect etcd quorum")
	}
}

func TestResolveAMTIP(t *testing.T) {
	tests := []struct {
		name       string
		node       *corev1.Node
		annotation string
		expected   string
	}{
		{
			"annotation override",
			nodeWithAnnotation("n", "10.0.0.1", "watchdog.example.com/amt-ip", "192.168.1.1"),
			"watchdog.example.com/amt-ip",
			"192.168.1.1",
		},
		{
			"fallback to InternalIP",
			nodeWithIP("n", "10.0.0.1"),
			"watchdog.example.com/amt-ip",
			"10.0.0.1",
		},
		{
			"no IP available",
			&corev1.Node{ObjectMeta: metav1.ObjectMeta{Name: "n"}},
			"watchdog.example.com/amt-ip",
			"",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := resolveAMTIP(tt.node, tt.annotation)
			if got != tt.expected {
				t.Errorf("resolveAMTIP() = %q, want %q", got, tt.expected)
			}
		})
	}
}

func TestIsControlPlane(t *testing.T) {
	tests := []struct {
		name     string
		labels   map[string]string
		expected bool
	}{
		{"control-plane label", map[string]string{"node-role.kubernetes.io/control-plane": ""}, true},
		{"master label", map[string]string{"node-role.kubernetes.io/master": ""}, true},
		{"worker", map[string]string{}, false},
		{"nil labels", nil, false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			node := &corev1.Node{ObjectMeta: metav1.ObjectMeta{Labels: tt.labels}}
			if got := isControlPlane(node); got != tt.expected {
				t.Errorf("isControlPlane() = %v, want %v", got, tt.expected)
			}
		})
	}
}
