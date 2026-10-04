package k8s

import (
	"context"
	"encoding/json"
	"fmt"
	"io"

	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

// WorkspaceUsage holds the current resource usage of a workspace: CPU and
// memory of the code-server container relative to its requests and limits,
// ephemeral storage (rootfs + logs) of that container, and the pod-level
// network counters (cumulative since pod start).
type WorkspaceUsage struct {
	// CPU usage of the code-server container, in cores.
	CPUUsedCores float64

	// CPU request and limit of the code-server container, in cores.
	// Zero when not set in the profile.
	CPURequestCores float64
	CPULimitCores   float64

	// Memory working set of the code-server container, in bytes.
	MemoryUsedBytes int64

	// Memory request and limit of the code-server container, in bytes.
	// Zero when not set in the profile.
	MemoryRequestBytes int64
	MemoryLimitBytes    int64

	// Ephemeral storage used by the code-server container (rootfs and
	// logs), in bytes.
	StorageUsedBytes int64

	// Cumulative network counters of the workspace pod, in bytes.
	NetworkRxBytes int64
	NetworkTxBytes int64
}

// nodeStatsSummary holds the subset of the kubelet stats summary API used by
// CodX (GET /api/v1/nodes/<name>/proxy/stats/summary).
type nodeStatsSummary struct {
	Pods []podStats `json:"pods"`
}

type podStats struct {
	PodRef struct {
		Name      string `json:"name"`
		Namespace string `json:"namespace"`
	} `json:"podRef"`
	Network    *networkStats     `json:"network"`
	Containers []containerStats `json:"containers"`
}

type networkStats struct {
	RxBytes int64 `json:"rxBytes"`
	TxBytes int64 `json:"txBytes"`
}

type containerStats struct {
	Name    string     `json:"name"`
	CPU     *cpuStats  `json:"cpu"`
	Memory  *memStats  `json:"memory"`
	Rootfs  *fsStats   `json:"rootfs"`
	Logs    *fsStats   `json:"logs"`
}

type cpuStats struct {
	UsageNanoCores int64 `json:"usageNanoCores"`
}

type memStats struct {
	WorkingSetBytes int64 `json:"workingSetBytes"`
}

type fsStats struct {
	UsedBytes int64 `json:"usedBytes"`
}

// GetWorkspaceUsage returns the resource usage of the user's workspace. The
// second return value reports whether the workspace pod exists and is
// running; usage is only meaningful then. The requests and limits come from
// the pod spec, the live usage from the kubelet stats summary of the node
// the pod runs on.
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

	// Live usage from the kubelet stats summary of the pod's node.
	if pod.Spec.NodeName == "" || m.clients.Nodes == nil {
		return usage, true, nil
	}

	reader, err := m.clients.Nodes.StatsSummary(ctx, pod.Spec.NodeName)
	if err != nil {
		return usage, true, fmt.Errorf("get node stats summary: %w", err)
	}
	defer reader.Close()

	var summary nodeStatsSummary
	if err := json.NewDecoder(reader).Decode(&summary); err != nil {
		return usage, true, fmt.Errorf("decode node stats summary: %w", err)
	}

	for i := range summary.Pods {
		p := summary.Pods[i]
		if p.PodRef.Name != objName || p.PodRef.Namespace != namespace {
			continue
		}
		if p.Network != nil {
			usage.NetworkRxBytes = p.Network.RxBytes
			usage.NetworkTxBytes = p.Network.TxBytes
		}
		for j := range p.Containers {
			c := p.Containers[j]
			if c.Name != CodeServerContainerName {
				continue
			}
			if c.CPU != nil {
				usage.CPUUsedCores = float64(c.CPU.UsageNanoCores) / 1e9
			}
			if c.Memory != nil {
				usage.MemoryUsedBytes = c.Memory.WorkingSetBytes
			}
			if c.Rootfs != nil {
				usage.StorageUsedBytes += c.Rootfs.UsedBytes
			}
			if c.Logs != nil {
				usage.StorageUsedBytes += c.Logs.UsedBytes
			}
		}
		return usage, true, nil
	}

	return usage, true, nil
}
