package k8s

import (
	"context"
	"testing"
	"time"

	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/resource"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

const testStatsSummary = `{"pods":[
  {
    "podRef": {"name": "codx-john-doe", "namespace": "codx-system"},
    "network": {"rxBytes": 1048576, "txBytes": 524288},
    "containers": [
      {
        "name": "code-server",
        "cpu": {"usageNanoCores": 250000000},
        "memory": {"workingSetBytes": 536870912},
        "rootfs": {"usedBytes": 10485760},
        "logs": {"usedBytes": 1048576}
      },
      {
        "name": "envoy-tls",
        "cpu": {"usageNanoCores": 10000000},
        "memory": {"workingSetBytes": 33554432}
      }
    ]
  },
  {
    "podRef": {"name": "codx-other", "namespace": "codx-system"},
    "network": {"rxBytes": 999, "txBytes": 999},
    "containers": []
  }
]}`

func TestGetWorkspaceUsage(t *testing.T) {
	cs := newTestClientset()
	cfg := testConfig()
	mgr := NewWorkspaceManager(cs, cfg)
	ctx := context.Background()

	// No pod: not running.
	usage, running, err := mgr.GetWorkspaceUsage(ctx, "john-doe")
	if err != nil {
		t.Fatalf("GetWorkspaceUsage: %v", err)
	}
	if running || usage != nil {
		t.Errorf("GetWorkspaceUsage without pod = (%v, %v), want (nil, false)", usage, running)
	}

	// Running pod on node worker-1 with requests and limits.
	pod := &corev1.Pod{
		ObjectMeta: metav1.ObjectMeta{
			Name:              "codx-john-doe",
			Namespace:         cfg.Namespace,
			CreationTimestamp: metav1.Time{Time: time.Now().Add(-time.Minute)},
		},
		Spec: corev1.PodSpec{
			NodeName: "worker-1",
			Containers: []corev1.Container{
				{
					Name: CodeServerContainerName,
					Resources: corev1.ResourceRequirements{
						Requests: corev1.ResourceList{
							corev1.ResourceCPU:    resource.MustParse("250m"),
							corev1.ResourceMemory: resource.MustParse("256Mi"),
						},
						Limits: corev1.ResourceList{
							corev1.ResourceCPU:    resource.MustParse("2"),
							corev1.ResourceMemory: resource.MustParse("4Gi"),
						},
					},
				},
			},
		},
		Status: corev1.PodStatus{Phase: corev1.PodRunning},
	}
	if _, err := cs.CoreV1.Pods(cfg.Namespace).Create(ctx, pod, metav1.CreateOptions{}); err != nil {
		t.Fatalf("create pod: %v", err)
	}
	cs.CoreV1.(*fakeCoreV1Client).nodeStats["worker-1"] = testStatsSummary

	usage, running, err = mgr.GetWorkspaceUsage(ctx, "john-doe")
	if err != nil {
		t.Fatalf("GetWorkspaceUsage: %v", err)
	}
	if !running || usage == nil {
		t.Fatalf("GetWorkspaceUsage = (nil, %v), want a usage report", running)
	}

	// CPU: 0.25 cores used, 0.25 requested, 2 limited.
	if usage.CPUUsedCores < 0.249 || usage.CPUUsedCores > 0.251 {
		t.Errorf("CPUUsedCores = %v, want 0.25", usage.CPUUsedCores)
	}
	if usage.CPURequestCores != 0.25 {
		t.Errorf("CPURequestCores = %v, want 0.25", usage.CPURequestCores)
	}
	if usage.CPULimitCores != 2 {
		t.Errorf("CPULimitCores = %v, want 2", usage.CPULimitCores)
	}

	// Memory: 512MiB used, 256MiB requested, 4Gi limited.
	if usage.MemoryUsedBytes != 536870912 {
		t.Errorf("MemoryUsedBytes = %v, want 536870912", usage.MemoryUsedBytes)
	}
	if usage.MemoryRequestBytes != 268435456 {
		t.Errorf("MemoryRequestBytes = %v, want 268435456", usage.MemoryRequestBytes)
	}
	if usage.MemoryLimitBytes != 4294967296 {
		t.Errorf("MemoryLimitBytes = %v, want 4294967296", usage.MemoryLimitBytes)
	}

	// Storage: rootfs 10MiB + logs 1MiB.
	if usage.StorageUsedBytes != 11534336 {
		t.Errorf("StorageUsedBytes = %v, want 11534336", usage.StorageUsedBytes)
	}

	// Network: pod-level counters (envoy traffic included).
	if usage.NetworkRxBytes != 1048576 || usage.NetworkTxBytes != 524288 {
		t.Errorf("Network = rx %v / tx %v, want 1048576 / 524288", usage.NetworkRxBytes, usage.NetworkTxBytes)
	}
}

func TestGetWorkspaceUsageWithoutNodeStats(t *testing.T) {
	cs := newTestClientset()
	cfg := testConfig()
	mgr := NewWorkspaceManager(cs, cfg)
	ctx := context.Background()

	// Running pod with no node name (not scheduled yet): requests and limits
	// are still reported, live usage stays zero.
	pod := &corev1.Pod{
		ObjectMeta: metav1.ObjectMeta{Name: "codx-jane", Namespace: cfg.Namespace},
		Spec: corev1.PodSpec{
			Containers: []corev1.Container{{
				Name: CodeServerContainerName,
				Resources: corev1.ResourceRequirements{
					Requests: corev1.ResourceList{corev1.ResourceCPU: resource.MustParse("500m")},
				},
			}},
		},
		Status: corev1.PodStatus{Phase: corev1.PodRunning},
	}
	if _, err := cs.CoreV1.Pods(cfg.Namespace).Create(ctx, pod, metav1.CreateOptions{}); err != nil {
		t.Fatalf("create pod: %v", err)
	}

	usage, running, err := mgr.GetWorkspaceUsage(ctx, "jane")
	if err != nil {
		t.Fatalf("GetWorkspaceUsage: %v", err)
	}
	if !running || usage == nil {
		t.Fatalf("GetWorkspaceUsage = (nil, %v), want a usage report", running)
	}
	if usage.CPURequestCores != 0.5 {
		t.Errorf("CPURequestCores = %v, want 0.5", usage.CPURequestCores)
	}
	if usage.CPUUsedCores != 0 || usage.MemoryUsedBytes != 0 {
		t.Errorf("live usage should be zero without node stats, got %+v", usage)
	}
}
