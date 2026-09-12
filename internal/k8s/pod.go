package k8s

import (
	"context"
	"fmt"
	"time"

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
