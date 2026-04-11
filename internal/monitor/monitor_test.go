package monitor

import (
	"context"
	"testing"
	"time"

	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/kubernetes/fake"

	"github.com/josephtknight/k8s-amt-node-watchdog/internal/config"
)

type mockRestarter struct {
	calls []string
}

func (m *mockRestarter) MaybeRestart(_ context.Context, node *corev1.Node, _ time.Time) error {
	m.calls = append(m.calls, node.Name)
	return nil
}

func newReadyNode(name string) *corev1.Node {
	return &corev1.Node{
		ObjectMeta: metav1.ObjectMeta{Name: name},
		Status: corev1.NodeStatus{
			Conditions: []corev1.NodeCondition{
				{Type: corev1.NodeReady, Status: corev1.ConditionTrue},
			},
		},
	}
}

func newNotReadyNode(name string) *corev1.Node {
	return &corev1.Node{
		ObjectMeta: metav1.ObjectMeta{Name: name},
		Status: corev1.NodeStatus{
			Conditions: []corev1.NodeCondition{
				{Type: corev1.NodeReady, Status: corev1.ConditionFalse},
			},
		},
	}
}

func TestMonitor_TracksNotReadyNodes(t *testing.T) {
	clientset := fake.NewSimpleClientset(
		newReadyNode("node-1"),
		newNotReadyNode("node-2"),
	)

	cfg := &config.Config{
		PollInterval:      30 * time.Second,
		NotReadyThreshold: 15 * time.Minute,
	}
	rst := &mockRestarter{}
	mon := New(cfg, clientset, rst)

	ctx := context.Background()
	mon.poll(ctx)

	tracked := mon.NotReadySince()
	if _, ok := tracked["node-2"]; !ok {
		t.Error("expected node-2 to be tracked as NotReady")
	}
	if _, ok := tracked["node-1"]; ok {
		t.Error("node-1 should not be tracked (it's Ready)")
	}
}

func TestMonitor_ClearsRecoveredNodes(t *testing.T) {
	node2 := newNotReadyNode("node-2")
	clientset := fake.NewSimpleClientset(
		newReadyNode("node-1"),
		node2,
	)

	cfg := &config.Config{
		PollInterval:      30 * time.Second,
		NotReadyThreshold: 15 * time.Minute,
	}
	rst := &mockRestarter{}
	mon := New(cfg, clientset, rst)

	ctx := context.Background()

	// First poll: node-2 is NotReady
	mon.poll(ctx)
	if _, ok := mon.NotReadySince()["node-2"]; !ok {
		t.Fatal("expected node-2 to be tracked")
	}

	// Simulate node-2 recovering
	node2.Status.Conditions[0].Status = corev1.ConditionTrue
	clientset.Tracker().Update(corev1.SchemeGroupVersion.WithResource("nodes"), node2, "")

	// Second poll: node-2 should be cleared
	mon.poll(ctx)
	if _, ok := mon.NotReadySince()["node-2"]; ok {
		t.Error("node-2 should have been cleared after recovery")
	}
}

func TestMonitor_TriggersRestartAfterThreshold(t *testing.T) {
	clientset := fake.NewSimpleClientset(
		newNotReadyNode("node-1"),
	)

	cfg := &config.Config{
		PollInterval:      30 * time.Second,
		NotReadyThreshold: 1 * time.Second, // very short for testing
	}
	rst := &mockRestarter{}
	mon := New(cfg, clientset, rst)

	ctx := context.Background()

	// First poll: starts tracking
	mon.poll(ctx)
	if len(rst.calls) != 0 {
		t.Fatal("should not restart immediately")
	}

	// Wait for threshold
	time.Sleep(1100 * time.Millisecond)

	// Second poll: should trigger restart
	mon.poll(ctx)
	if len(rst.calls) != 1 || rst.calls[0] != "node-1" {
		t.Errorf("expected restart of node-1, got: %v", rst.calls)
	}
}

func TestIsNodeReady(t *testing.T) {
	tests := []struct {
		name     string
		node     *corev1.Node
		expected bool
	}{
		{"ready", newReadyNode("a"), true},
		{"not ready", newNotReadyNode("b"), false},
		{"no conditions", &corev1.Node{ObjectMeta: metav1.ObjectMeta{Name: "c"}}, false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := isNodeReady(tt.node); got != tt.expected {
				t.Errorf("isNodeReady() = %v, want %v", got, tt.expected)
			}
		})
	}
}
