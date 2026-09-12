package k8s

import (
	"context"
	"fmt"
	"time"

	cmv1 "github.com/cert-manager/cert-manager/pkg/apis/certmanager/v1"
	cmmeta "github.com/cert-manager/cert-manager/pkg/apis/meta/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

// IsCertificateReady checks whether a cert-manager Certificate is in the
// Ready state.
func IsCertificateReady(cert *cmv1.Certificate) bool {
	for _, cond := range cert.Status.Conditions {
		if cond.Type == certReadyConditionType && cond.Status == cmmeta.ConditionTrue {
			return true
		}
	}
	return false
}

// certReadyConditionType is the condition type cert-manager sets when a
// certificate is ready.
const certReadyConditionType cmv1.CertificateConditionType = "Ready"

// WaitForCertificate polls the certificate until it is ready or the context
// is cancelled.
func (m *WorkspaceManager) WaitForCertificate(ctx context.Context, userSlug string, pollInterval time.Duration) error {
	namespace := m.cfg.Namespace
	objName := objectName(m.cfg.InstanceName, userSlug)

	ticker := time.NewTicker(pollInterval)
	defer ticker.Stop()

	for {
		cert, err := m.clients.CertManager.Certificates(namespace).Get(ctx, objName, metav1.GetOptions{})
		if err != nil {
			if apierrors.IsNotFound(err) {
				return fmt.Errorf("certificate %s not found", objName)
			}
			return fmt.Errorf("get certificate: %w", err)
		}

		if IsCertificateReady(cert) {
			return nil
		}

		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-ticker.C:
		}
	}
}

// EnsureCertificateReady creates the certificate if it doesn't exist and waits
// for it to become ready.
func (m *WorkspaceManager) EnsureCertificateReady(ctx context.Context, userSlug, fqdn string, pollInterval time.Duration) error {
	namespace := m.cfg.Namespace
	objName := objectName(m.cfg.InstanceName, userSlug)

	// Check if the certificate already exists.
	_, err := m.clients.CertManager.Certificates(namespace).Get(ctx, objName, metav1.GetOptions{})
	if err != nil {
		if !apierrors.IsNotFound(err) {
			return fmt.Errorf("get certificate: %w", err)
		}
		// Create the certificate.
		cert := BuildCertificate(m.cfg.InstanceName, userSlug, namespace, fqdn, m.cfg.CertManager)
		if _, err := m.clients.CertManager.Certificates(namespace).Create(ctx, cert, metav1.CreateOptions{}); err != nil {
			if !apierrors.IsAlreadyExists(err) {
				return fmt.Errorf("create certificate: %w", err)
			}
		}
	}

	return m.WaitForCertificate(ctx, userSlug, pollInterval)
}
