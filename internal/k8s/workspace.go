package k8s

import (
	"context"
	"fmt"
	"time"

	profilev1 "github.com/captnbp/CodX/api/profile/v1"
	"github.com/captnbp/CodX/internal/config"
	"github.com/captnbp/CodX/internal/slug"
	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/api/resource"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

// WorkspaceManager creates and manages workspace objects (Service, PVC,
// Certificate, Pod) for a user.
type WorkspaceManager struct {
	clients    *Clientset
	cfg        *config.Config
	maxNameLen int
}

// NewWorkspaceManager creates a WorkspaceManager.
func NewWorkspaceManager(clients *Clientset, cfg *config.Config) *WorkspaceManager {
	return &WorkspaceManager{
		clients:    clients,
		cfg:        cfg,
		maxNameLen: cfg.Slug.MaxLength,
	}
}

// WorkspaceStep represents a step in the workspace creation process.
type WorkspaceStep struct {
	Name    string
	Message string
}

// EnsureWorkspace idempotently creates all workspace objects for a user and
// returns the created steps. It does not wait for the pod to be ready.
func (m *WorkspaceManager) EnsureWorkspace(ctx context.Context, profile *profilev1.Profile, userSlug string) ([]WorkspaceStep, error) {
	instance := m.cfg.InstanceName
	namespace := m.cfg.Namespace
	steps := make([]WorkspaceStep, 0, 4)

	// 1. Service.
	svc := BuildService(instance, userSlug, namespace, profile, m.cfg)
	if _, err := m.clients.CoreV1.Services(namespace).Create(ctx, svc, metav1.CreateOptions{}); err != nil {
		if !apierrors.IsAlreadyExists(err) {
			return steps, fmt.Errorf("create service: %w", err)
		}
	}
	steps = append(steps, WorkspaceStep{Name: "service", Message: "Service created"})

	// 2. PVC.
	pvc, err := BuildPVC(instance, userSlug, namespace, profile)
	if err != nil {
		return steps, fmt.Errorf("build PVC: %w", err)
	}
	if _, err := m.clients.CoreV1.PersistentVolumeClaims(namespace).Create(ctx, pvc, metav1.CreateOptions{}); err != nil {
		if !apierrors.IsAlreadyExists(err) {
			return steps, fmt.Errorf("create PVC: %w", err)
		}
	}
	steps = append(steps, WorkspaceStep{Name: "pvc", Message: "PVC created"})

	// 3. Certificate.
	fqdn := ServiceFQDN(instance, userSlug, namespace)
	cert := BuildCertificate(instance, userSlug, namespace, fqdn, m.cfg.CertManager)
	if _, err := m.clients.CertManager.Certificates(namespace).Create(ctx, cert, metav1.CreateOptions{}); err != nil {
		if !apierrors.IsAlreadyExists(err) {
			return steps, fmt.Errorf("create certificate: %w", err)
		}
	}
	steps = append(steps, WorkspaceStep{Name: "certificate", Message: "Certificate created"})

	// 4. Pod.
	pod := BuildPod(instance, userSlug, namespace, fqdn, profile, m.cfg)
	if _, err := m.clients.CoreV1.Pods(namespace).Create(ctx, pod, metav1.CreateOptions{}); err != nil {
		if !apierrors.IsAlreadyExists(err) {
			return steps, fmt.Errorf("create pod: %w", err)
		}
	}
	steps = append(steps, WorkspaceStep{Name: "pod", Message: "Pod created"})

	return steps, nil
}

// StopWorkspace deletes the workspace pod but leaves the PVC, Service, and
// Certificate so the user can restart.
func (m *WorkspaceManager) StopWorkspace(ctx context.Context, userSlug string) error {
	namespace := m.cfg.Namespace
	objName := slug.ObjectName(m.cfg.InstanceName, userSlug, m.maxNameLen)

	if err := m.clients.CoreV1.Pods(namespace).Delete(ctx, objName, metav1.DeleteOptions{}); err != nil {
		if !apierrors.IsNotFound(err) {
			return fmt.Errorf("delete pod: %w", err)
		}
	}
	return nil
}

// DeleteWorkspace permanently deletes all workspace objects: Pod, Certificate,
// Service, PVC, and TLS secret.
func (m *WorkspaceManager) DeleteWorkspace(ctx context.Context, userSlug string) error {
	namespace := m.cfg.Namespace
	objName := slug.ObjectName(m.cfg.InstanceName, userSlug, m.maxNameLen)
	secretName := objName + "-tls"

	// Delete pod.
	if err := m.clients.CoreV1.Pods(namespace).Delete(ctx, objName, metav1.DeleteOptions{}); err != nil {
		if !apierrors.IsNotFound(err) {
			return fmt.Errorf("delete pod: %w", err)
		}
	}

	// Delete certificate.
	if err := m.clients.CertManager.Certificates(namespace).Delete(ctx, objName, metav1.DeleteOptions{}); err != nil {
		if !apierrors.IsNotFound(err) {
			return fmt.Errorf("delete certificate: %w", err)
		}
	}

	// Delete service.
	if err := m.clients.CoreV1.Services(namespace).Delete(ctx, objName, metav1.DeleteOptions{}); err != nil {
		if !apierrors.IsNotFound(err) {
			return fmt.Errorf("delete service: %w", err)
		}
	}

	// Delete PVC.
	if err := m.clients.CoreV1.PersistentVolumeClaims(namespace).Delete(ctx, objName, metav1.DeleteOptions{}); err != nil {
		if !apierrors.IsNotFound(err) {
			return fmt.Errorf("delete PVC: %w", err)
		}
	}

	// Delete TLS secret.
	if err := m.clients.CoreV1.Secrets(namespace).Delete(ctx, secretName, metav1.DeleteOptions{}); err != nil {
		if !apierrors.IsNotFound(err) {
			return fmt.Errorf("delete TLS secret: %w", err)
		}
	}

	return nil
}

// LastLoginAnnotation is the PVC annotation tracking the user's last login,
// formatted as RFC3339. CodX has no database: the workspace PVC carries the
// few per-user facts that must survive pod restarts.
const LastLoginAnnotation = "codx.captnbp.io/last-login"

// TouchWorkspaceLastLogin stamps the current time on the workspace PVC's
// LastLoginAnnotation. Called on every successful login; callers tolerate
// the error (users without a workspace yet have no PVC to annotate).
func (m *WorkspaceManager) TouchWorkspaceLastLogin(ctx context.Context, userSlug string) error {
	namespace := m.cfg.Namespace
	objName := slug.ObjectName(m.cfg.InstanceName, userSlug, m.maxNameLen)

	pvc, err := m.clients.CoreV1.PersistentVolumeClaims(namespace).Get(ctx, objName, metav1.GetOptions{})
	if err != nil {
		return fmt.Errorf("get PVC: %w", err)
	}

	if pvc.Annotations == nil {
		pvc.Annotations = make(map[string]string, 1)
	}
	pvc.Annotations[LastLoginAnnotation] = time.Now().UTC().Format(time.RFC3339)

	if _, err := m.clients.CoreV1.PersistentVolumeClaims(namespace).Update(ctx, pvc, metav1.UpdateOptions{}); err != nil {
		return fmt.Errorf("update PVC: %w", err)
	}
	return nil
}

// GetWorkspaceLastLogin returns the user's last login time recorded on the
// workspace PVC (zero when never logged in or no workspace yet).
func (m *WorkspaceManager) GetWorkspaceLastLogin(ctx context.Context, userSlug string) (time.Time, error) {
	namespace := m.cfg.Namespace
	objName := slug.ObjectName(m.cfg.InstanceName, userSlug, m.maxNameLen)

	pvc, err := m.clients.CoreV1.PersistentVolumeClaims(namespace).Get(ctx, objName, metav1.GetOptions{})
	if err != nil {
		return time.Time{}, fmt.Errorf("get PVC: %w", err)
	}

	raw, ok := pvc.Annotations[LastLoginAnnotation]
	if !ok {
		return time.Time{}, nil
	}
	lastLogin, err := time.Parse(time.RFC3339, raw)
	if err != nil {
		return time.Time{}, fmt.Errorf("parse %s annotation %q: %w", LastLoginAnnotation, raw, err)
	}
	return lastLogin, nil
}

// GetWorkspaceCreationTime returns the creation time of a user's workspace,
// read from the creationTimestamp of the workspace PVC. The PVC is created on
// first start and survives stops and restarts, so it tracks the user's
// arrival better than the pod.
func (m *WorkspaceManager) GetWorkspaceCreationTime(ctx context.Context, userSlug string) (time.Time, error) {
	namespace := m.cfg.Namespace
	objName := slug.ObjectName(m.cfg.InstanceName, userSlug, m.maxNameLen)

	pvc, err := m.clients.CoreV1.PersistentVolumeClaims(namespace).Get(ctx, objName, metav1.GetOptions{})
	if err != nil {
		return time.Time{}, fmt.Errorf("get PVC: %w", err)
	}
	return pvc.CreationTimestamp.Time, nil
}

// ExtendPVC updates the PVC storage size.
func (m *WorkspaceManager) ExtendPVC(ctx context.Context, userSlug, newSize string) error {
	namespace := m.cfg.Namespace
	objName := slug.ObjectName(m.cfg.InstanceName, userSlug, m.maxNameLen)

	pvc, err := m.clients.CoreV1.PersistentVolumeClaims(namespace).Get(ctx, objName, metav1.GetOptions{})
	if err != nil {
		return fmt.Errorf("get PVC: %w", err)
	}

	pvc.Spec.Resources.Requests[corev1.ResourceStorage] = mustParseQuantity(newSize)

	if _, err := m.clients.CoreV1.PersistentVolumeClaims(namespace).Update(ctx, pvc, metav1.UpdateOptions{}); err != nil {
		return fmt.Errorf("update PVC: %w", err)
	}
	return nil
}

// mustParseQuantity parses a quantity string, panicking on error.
// This is only used internally for ExtendPVC where the caller validates input.
func mustParseQuantity(s string) resource.Quantity {
	q, err := resource.ParseQuantity(s)
	if err != nil {
		panic(fmt.Sprintf("invalid quantity %q: %v", s, err))
	}
	return q
}
