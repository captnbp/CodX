package k8s

import (
	"context"
	"testing"
	"time"

	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

func TestGetWorkspacePodAge(t *testing.T) {
	cs := newTestClientset()
	cfg := testConfig()
	mgr := NewWorkspaceManager(cs, cfg)
	ctx := context.Background()

	// No pod: not running, zero age.
	age, running := mgr.GetWorkspacePodAge(ctx, "john-doe")
	if running {
		t.Error("GetWorkspacePodAge running = true, want false (no pod)")
	}
	if age != 0 {
		t.Errorf("GetWorkspacePodAge = %v, want 0 (no pod)", age)
	}

	// Running pod started 15 minutes ago.
	pod := &corev1.Pod{
		ObjectMeta: metav1.ObjectMeta{
			Name:              "codx-john-doe",
			Namespace:         cfg.Namespace,
			CreationTimestamp: metav1.Time{Time: time.Now().Add(-15 * time.Minute)},
		},
		Status: corev1.PodStatus{Phase: corev1.PodRunning},
	}
	if _, err := cs.CoreV1.Pods(cfg.Namespace).Create(ctx, pod, metav1.CreateOptions{}); err != nil {
		t.Fatalf("create pod: %v", err)
	}

	age, running = mgr.GetWorkspacePodAge(ctx, "john-doe")
	if !running {
		t.Fatal("GetWorkspacePodAge running = false, want true")
	}
	if age < 14*time.Minute || age > 16*time.Minute {
		t.Errorf("GetWorkspacePodAge = %v, want about 15m", age)
	}

	// Status.StartTime takes precedence over CreationTimestamp when set.
	started := metav1.Time{Time: time.Now().Add(-5 * time.Minute)}
	if err := cs.CoreV1.Pods(cfg.Namespace).Delete(ctx, "codx-john-doe", metav1.DeleteOptions{}); err != nil {
		t.Fatalf("delete pod: %v", err)
	}
	pod.Status.StartTime = &started
	if _, err := cs.CoreV1.Pods(cfg.Namespace).Create(ctx, pod, metav1.CreateOptions{}); err != nil {
		t.Fatalf("create pod: %v", err)
	}

	age, running = mgr.GetWorkspacePodAge(ctx, "john-doe")
	if !running {
		t.Fatal("GetWorkspacePodAge running = false, want true")
	}
	if age < 4*time.Minute || age > 6*time.Minute {
		t.Errorf("GetWorkspacePodAge = %v, want about 5m (from StartTime)", age)
	}
}
