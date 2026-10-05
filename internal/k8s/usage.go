package k8s

import (
	"context"
	"fmt"

	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

// WorkspaceUsage holds the current resource usage of a workspace: CPU and
// memory of the code-server container relative to its requests and limits.
type WorkspaceUsage struct {
	// CPU usage of the code-server container, in cores.
	CPUUsedCores float64

	// CPU request and limit of the code-server container, in cores.
	// Zero when not set in the profile.
	CPURequestCores float64
	CPULimitCores   float64

	// Memory usage of the code-server container, in bytes.
	MemoryUsedBytes int64

	// Memory request and limit of the code-server container, in bytes.
	// Zero when not set in the profile.
	MemoryRequestBytes int64
	MemoryLimitBytes   int64
}

// GetWorkspaceUsage returns the resource usage of the user's workspace. The
// second return value reports whether the workspace pod exists and is
// running; usage is only meaningful then. The requests and limits come from
// the pod spec, the live usage from metrics-server (metrics.k8s.io).
func (m *WorkspaceManager) GetWorkspaceUsage(ctx context.Context, userSlug string) (*WorkspaceUsage, bool, error) {
	namespace := m.cfg.Namespace
	objName := objectName(m.cfg.InstanceName, userSlug)

	pod, err := m.clients.CoreV1.Pods(namespace).Get(ctx, objName, metav1.GetOptions{})
	if err != nil || pod.Status.Phase != corev1.PodRunning {
		return nil, false, nil
	}

	usage := &WorkspaceUsage{}

	// Requests and limits of the code-server container.
	for i := range pod.Spec.Containers {
		c := pod.Spec.Containers[i]
		if c.Name != CodeServerContainerName {
			continue
		}
		if q, ok := c.Resources.Requests[corev1.ResourceCPU]; ok {
			usage.CPURequestCores = float64(q.MilliValue()) / 1000
		}
		if q, ok := c.Resources.Limits[corev1.ResourceCPU]; ok {
			usage.CPULimitCores = float64(q.MilliValue()) / 1000
		}
		if q, ok := c.Resources.Requests[corev1.ResourceMemory]; ok {
			usage.MemoryRequestBytes = q.Value()
		}
		if q, ok := c.Resources.Limits[corev1.ResourceMemory]; ok {
			usage.MemoryLimitBytes = q.Value()
		}
	}

	// Live usage from metrics-server. No metrics client wired: report the
	// requests and limits with zero live usage.
	if m.clients.MetricsV1 == nil {
		return usage, true, nil
	}

	metrics, err := m.clients.MetricsV1.PodMetrics(namespace).Get(ctx, objName, metav1.GetOptions{})
	if err != nil {
		// metrics-server has not scraped the pod yet (it starts shortly
		// after the pod does): report zero live usage, not an error.
		if apierrors.IsNotFound(err) {
			return usage, true, nil
		}
		return usage, true, fmt.Errorf("get pod metrics: %w", err)
	}

	for i := range metrics.Containers {
		c := metrics.Containers[i]
		if c.Name != CodeServerContainerName {
			continue
		}
		usage.CPUUsedCores = c.CPUUsedCores
		usage.MemoryUsedBytes = c.MemoryWorkingSetBytes
	}

	return usage, true, nil
}
