package k8s

import (
	"context"
	"testing"

	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

func TestCountWorkspaces(t *testing.T) {
	cs := newTestClientset()
	cfg := testConfig()
	mgr := NewWorkspaceManager(cs, cfg)
	ctx := context.Background()

	makePod := func(name, managedBy, phase string) *corev1.Pod {
		return &corev1.Pod{
			ObjectMeta: metav1.ObjectMeta{
				Name:   name,
				Labels: workspaceLabels(managedBy, name),
			},
			Status: corev1.PodStatus{Phase: corev1.PodPhase(phase)},
		}
	}

	pods := cs.CoreV1.Pods(cfg.Namespace)
	for _, p := range []*corev1.Pod{
		makePod("codx-john-doe", cfg.InstanceName, string(corev1.PodRunning)),
		makePod("codx-jane-doe", cfg.InstanceName, string(corev1.PodRunning)),
		makePod("codx-alice", cfg.InstanceName, string(corev1.PodPending)),
		makePod("codx-failed", cfg.InstanceName, string(corev1.PodFailed)),
		// Managed by another instance; must not be counted.
		makePod("other-bob", "other", string(corev1.PodRunning)),
	} {
		if _, err := pods.Create(ctx, p, metav1.CreateOptions{}); err != nil {
			t.Fatalf("create pod %s: %v", p.Name, err)
		}
	}

	counts, err := mgr.CountWorkspaces(ctx)
	if err != nil {
		t.Fatalf("CountWorkspaces: %v", err)
	}
	if counts.Running != 2 {
		t.Errorf("counts.Running = %d, want 2", counts.Running)
	}
	if counts.Pending != 1 {
		t.Errorf("counts.Pending = %d, want 1", counts.Pending)
	}
}
