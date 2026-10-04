package k8s

import (
	"context"
	"fmt"
	"io"
	"time"

	"github.com/captnbp/CodX/internal/slug"
	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

// IsPodReady checks whether all of a pod's containers are ready.
func IsPodReady(pod *corev1.Pod) bool {
	for _, cond := range pod.Status.Conditions {
		if cond.Type == corev1.PodReady && cond.Status == corev1.ConditionTrue {
			return true
		}
	}
	return false
}

// WaitForPodReady polls the workspace pod until it is ready or the context
// is cancelled.
func (m *WorkspaceManager) WaitForPodReady(ctx context.Context, userSlug string, pollInterval time.Duration) error {
	namespace := m.cfg.Namespace
	objName := objectName(m.cfg.InstanceName, userSlug)

	ticker := time.NewTicker(pollInterval)
	defer ticker.Stop()

	for {
		pod, err := m.clients.CoreV1.Pods(namespace).Get(ctx, objName, metav1.GetOptions{})
		if err != nil {
			if apierrors.IsNotFound(err) {
				return fmt.Errorf("pod %s not found", objName)
			}
			return fmt.Errorf("get pod: %w", err)
		}

		if IsPodReady(pod) {
			return nil
		}

		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-ticker.C:
		}
	}
}

// IsWorkspaceRunning checks whether the workspace pod exists and is running.
func (m *WorkspaceManager) IsWorkspaceRunning(ctx context.Context, userSlug string) bool {
	namespace := m.cfg.Namespace
	objName := objectName(m.cfg.InstanceName, userSlug)

	pod, err := m.clients.CoreV1.Pods(namespace).Get(ctx, objName, metav1.GetOptions{})
	if err != nil {
		return false
	}
	return pod.Status.Phase == corev1.PodRunning
}

// WorkspaceCounts holds the number of workspace pods by state, used by the
// Prometheus metrics endpoint.
type WorkspaceCounts struct {
	// Running is the number of workspace pods in the Running phase.
	Running int

	// Pending is the number of workspace pods created but not yet running
	// (Pending phase).
	Pending int
}

// CountWorkspaces counts the workspace pods managed by this CodX instance,
// split by running and pending state.
func (m *WorkspaceManager) CountWorkspaces(ctx context.Context) (WorkspaceCounts, error) {
	namespace := m.cfg.Namespace
	pods, err := m.clients.CoreV1.Pods(namespace).List(ctx, metav1.ListOptions{
		LabelSelector: fmt.Sprintf("%s=%s,%s=%s",
			LabelComponent, ComponentName,
			LabelManagedBy, m.cfg.InstanceName,
		),
	})
	if err != nil {
		return WorkspaceCounts{}, fmt.Errorf("list workspace pods: %w", err)
	}

	counts := WorkspaceCounts{}
	for i := range pods.Items {
		switch pods.Items[i].Status.Phase {
		case corev1.PodRunning:
			counts.Running++
		case corev1.PodPending:
			counts.Pending++
		}
	}
	return counts, nil
}

// ListWorkspacePods returns all workspace pods managed by this CodX instance
// (component=workspace, managed-by=<instance>) that are running and have a
// pod IP assigned. Used by the connection-count inactivity source to reach
// the Envoy admin interface of each workspace.
func (m *WorkspaceManager) ListWorkspacePods(ctx context.Context) ([]corev1.Pod, error) {
	namespace := m.cfg.Namespace
	pods, err := m.clients.CoreV1.Pods(namespace).List(ctx, metav1.ListOptions{
		LabelSelector: fmt.Sprintf("%s=%s,%s=%s",
			LabelComponent, ComponentName,
			LabelManagedBy, m.cfg.InstanceName,
		),
	})
	if err != nil {
		return nil, fmt.Errorf("list workspace pods: %w", err)
	}

	running := make([]corev1.Pod, 0, len(pods.Items))
	for i := range pods.Items {
		pod := pods.Items[i]
		if pod.Status.Phase != corev1.PodRunning || pod.Status.PodIP == "" {
			continue
		}
		running = append(running, pod)
	}
	return running, nil
}

// GetWorkspacePodAge returns the age of the workspace pod (based on its
// start time, falling back to its creation timestamp) and whether it exists
// and is running.
func (m *WorkspaceManager) GetWorkspacePodAge(ctx context.Context, userSlug string) (time.Duration, bool) {
	namespace := m.cfg.Namespace
	objName := objectName(m.cfg.InstanceName, userSlug)

	pod, err := m.clients.CoreV1.Pods(namespace).Get(ctx, objName, metav1.GetOptions{})
	if err != nil || pod.Status.Phase != corev1.PodRunning {
		return 0, false
	}

	start := pod.CreationTimestamp.Time
	if pod.Status.StartTime != nil {
		start = pod.Status.StartTime.Time
	}
	return time.Since(start), true
}

// GetWorkspaceLogs returns the logs of one container of the workspace pod,
// optionally following new lines as they are emitted (follow=true).
// The caller must close the returned ReadCloser.
func (m *WorkspaceManager) GetWorkspaceLogs(ctx context.Context, userSlug, container string, tailLines int64, follow bool) (io.ReadCloser, error) {
	namespace := m.cfg.Namespace
	objName := slug.ObjectName(m.cfg.InstanceName, userSlug, m.maxNameLen)
	return m.clients.CoreV1.Pods(namespace).Logs(ctx, objName, corev1.PodLogOptions{
		Container: container,
		TailLines: &tailLines,
		Follow:    follow,
	})
}

// RestartWorkspace deletes the workspace pod so that it gets recreated (the
// PVC, Service, and Certificate are preserved).
func (m *WorkspaceManager) RestartWorkspace(ctx context.Context, userSlug string) error {
	namespace := m.cfg.Namespace
	objName := objectName(m.cfg.InstanceName, userSlug)

	// Delete the pod; Kubernetes will recreate it if it was created by a
	// controller. For standalone pods, the caller must re-create it.
	if err := m.clients.CoreV1.Pods(namespace).Delete(ctx, objName, metav1.DeleteOptions{}); err != nil {
		if !apierrors.IsNotFound(err) {
			return fmt.Errorf("delete pod for restart: %w", err)
		}
	}
	return nil
}

// ListWorkspaceServices returns all services labeled with the CodX component
// and the instance name. Used by the admin UI to list all workspace users.
func (m *WorkspaceManager) ListWorkspaceServices(ctx context.Context) ([]corev1.Service, error) {
	namespace := m.cfg.Namespace
	services, err := m.clients.CoreV1.Services(namespace).List(ctx, metav1.ListOptions{
		LabelSelector: fmt.Sprintf("%s=%s,%s=%s",
			LabelComponent, ComponentName,
			LabelManagedBy, m.cfg.InstanceName,
		),
	})
	if err != nil {
		return nil, fmt.Errorf("list workspace services: %w", err)
	}
	return services.Items, nil
}
