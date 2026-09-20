package k8s

import (
	"context"
	"testing"

	cmv1 "github.com/cert-manager/cert-manager/pkg/apis/certmanager/v1"
	cmmeta "github.com/cert-manager/cert-manager/pkg/apis/meta/v1"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

func newTestClientset() *Clientset {
	return &Clientset{
		CoreV1:      newFakeCoreV1Client(),
		CertManager: newFakeCertManagerClient(),
	}
}

func TestEnsureWorkspaceCreatesAllObjects(t *testing.T) {
	cs := newTestClientset()
	cfg := testConfig()
	mgr := NewWorkspaceManager(cs, cfg)
	profile := testProfile("python-dev", "Python Dev", nil)

	steps, err := mgr.EnsureWorkspace(context.Background(), profile, "john-doe")
	if err != nil {
		t.Fatalf("EnsureWorkspace: %v", err)
	}

	if len(steps) != 4 {
		t.Fatalf("got %d steps, want 4", len(steps))
	}

	objName := "codx-john-doe"
	coreV1 := cs.CoreV1.(*fakeCoreV1Client)
	certMgr := cs.CertManager.(*fakeCertManagerClient)

	// Verify Service.
	svc, err := coreV1.Services("").Get(context.Background(), objName, metav1.GetOptions{})
	if err != nil {
		t.Fatalf("Service not found: %v", err)
	}
	if svc.Labels[LabelInstance] != "john-doe" {
		t.Errorf("Service instance label = %q, want john-doe", svc.Labels[LabelInstance])
	}
	if svc.Labels[LabelManagedBy] != "codx" {
		t.Errorf("Service managed-by label = %q, want codx", svc.Labels[LabelManagedBy])
	}
	if svc.Labels[LabelComponent] != "workspace" {
		t.Errorf("Service component label = %q, want workspace", svc.Labels[LabelComponent])
	}

	// Verify PVC.
	pvc, err := coreV1.PersistentVolumeClaims("").Get(context.Background(), objName, metav1.GetOptions{})
	if err != nil {
		t.Fatalf("PVC not found: %v", err)
	}
	storage := pvc.Spec.Resources.Requests[corev1.ResourceStorage]
	if storage.String() != "10Gi" {
		t.Errorf("PVC size = %s, want 10Gi", storage.String())
	}

	// Verify Certificate.
	cert, err := certMgr.Certificates("").Get(context.Background(), objName, metav1.GetOptions{})
	if err != nil {
		t.Fatalf("Certificate not found: %v", err)
	}
	if cert.Spec.SecretName != objName+"-tls" {
		t.Errorf("Certificate secret name = %q, want %s-tls", cert.Spec.SecretName, objName)
	}
	if len(cert.Spec.DNSNames) != 1 {
		t.Fatalf("Certificate DNSNames = %v, want 1 entry", cert.Spec.DNSNames)
	}
	if cert.Spec.DNSNames[0] != "codx-john-doe.codx-system.svc.cluster.local" {
		t.Errorf("Certificate DNSName = %q, want codx-john-doe.codx-system.svc.cluster.local", cert.Spec.DNSNames[0])
	}
	if cert.Spec.IssuerRef.Name != "codx-issuer" {
		t.Errorf("Certificate issuer name = %q, want codx-issuer", cert.Spec.IssuerRef.Name)
	}

	// Verify Pod.
	pod, err := coreV1.Pods("").Get(context.Background(), objName, metav1.GetOptions{})
	if err != nil {
		t.Fatalf("Pod not found: %v", err)
	}
	if len(pod.Spec.Containers) < 2 {
		t.Fatalf("Pod has %d containers, want >= 2", len(pod.Spec.Containers))
	}
	if pod.Spec.Containers[0].Name != "code-server" {
		t.Errorf("First container = %q, want code-server", pod.Spec.Containers[0].Name)
	}
	if pod.Spec.Containers[1].Name != "nginx-tls" {
		t.Errorf("Second container = %q, want nginx-tls", pod.Spec.Containers[1].Name)
	}
	if pod.Spec.EnableServiceLinks == nil {
		t.Error("EnableServiceLinks is nil, want false by default")
	} else if *pod.Spec.EnableServiceLinks {
		t.Error("EnableServiceLinks = true, want false by default")
	}
}

func TestEnsureWorkspaceEnableServiceLinksTrue(t *testing.T) {
	cs := newTestClientset()
	cfg := testConfig()
	mgr := NewWorkspaceManager(cs, cfg)
	profile := testProfile("python-dev", "Python Dev", nil)
	trueVal := true
	profile.Spec.PodSpec.EnableServiceLinks = &trueVal

	_, err := mgr.EnsureWorkspace(context.Background(), profile, "john-doe")
	if err != nil {
		t.Fatalf("EnsureWorkspace: %v", err)
	}

	coreV1 := cs.CoreV1.(*fakeCoreV1Client)
	objName := "codx-john-doe"
	pod, err := coreV1.Pods("").Get(context.Background(), objName, metav1.GetOptions{})
	if err != nil {
		t.Fatalf("Pod not found: %v", err)
	}
	if pod.Spec.EnableServiceLinks == nil {
		t.Fatal("EnableServiceLinks is nil, want true")
	}
	if !*pod.Spec.EnableServiceLinks {
		t.Error("EnableServiceLinks = false, want true")
	}
}

func TestEnsureWorkspaceIdempotent(t *testing.T) {
	cs := newTestClientset()
	cfg := testConfig()
	mgr := NewWorkspaceManager(cs, cfg)
	profile := testProfile("python-dev", "Python Dev", nil)

	// First call creates everything.
	_, err := mgr.EnsureWorkspace(context.Background(), profile, "jane-doe")
	if err != nil {
		t.Fatalf("first EnsureWorkspace: %v", err)
	}

	// Second call should not error.
	_, err = mgr.EnsureWorkspace(context.Background(), profile, "jane-doe")
	if err != nil {
		t.Fatalf("second EnsureWorkspace (idempotent): %v", err)
	}

	coreV1 := cs.CoreV1.(*fakeCoreV1Client)
	certMgr := cs.CertManager.(*fakeCertManagerClient)
	if len(coreV1.services) != 1 {
		t.Errorf("services count = %d, want 1", len(coreV1.services))
	}
	if len(coreV1.pvcs) != 1 {
		t.Errorf("pvcs count = %d, want 1", len(coreV1.pvcs))
	}
	if len(coreV1.pods) != 1 {
		t.Errorf("pods count = %d, want 1", len(coreV1.pods))
	}
	if len(certMgr.certs) != 1 {
		t.Errorf("certs count = %d, want 1", len(certMgr.certs))
	}
}

func TestStopWorkspace(t *testing.T) {
	cs := newTestClientset()
	cfg := testConfig()
	mgr := NewWorkspaceManager(cs, cfg)
	profile := testProfile("python-dev", "Python Dev", nil)

	if _, err := mgr.EnsureWorkspace(context.Background(), profile, "john-doe"); err != nil {
		t.Fatalf("EnsureWorkspace: %v", err)
	}

	if err := mgr.StopWorkspace(context.Background(), "john-doe"); err != nil {
		t.Fatalf("StopWorkspace: %v", err)
	}

	coreV1 := cs.CoreV1.(*fakeCoreV1Client)
	if len(coreV1.pods) != 0 {
		t.Errorf("pods count = %d, want 0 after stop", len(coreV1.pods))
	}
	if len(coreV1.pvcs) != 1 {
		t.Errorf("pvcs count = %d, want 1 after stop", len(coreV1.pvcs))
	}
	if len(coreV1.services) != 1 {
		t.Errorf("services count = %d, want 1 after stop", len(coreV1.services))
	}
}

func TestDeleteWorkspace(t *testing.T) {
	cs := newTestClientset()
	cfg := testConfig()
	mgr := NewWorkspaceManager(cs, cfg)
	profile := testProfile("python-dev", "Python Dev", nil)

	if _, err := mgr.EnsureWorkspace(context.Background(), profile, "john-doe"); err != nil {
		t.Fatalf("EnsureWorkspace: %v", err)
	}

	if err := mgr.DeleteWorkspace(context.Background(), "john-doe"); err != nil {
		t.Fatalf("DeleteWorkspace: %v", err)
	}

	coreV1 := cs.CoreV1.(*fakeCoreV1Client)
	certMgr := cs.CertManager.(*fakeCertManagerClient)
	if len(coreV1.pods) != 0 {
		t.Errorf("pods count = %d, want 0 after delete", len(coreV1.pods))
	}
	if len(coreV1.pvcs) != 0 {
		t.Errorf("pvcs count = %d, want 0 after delete", len(coreV1.pvcs))
	}
	if len(coreV1.services) != 0 {
		t.Errorf("services count = %d, want 0 after delete", len(coreV1.services))
	}
	if len(coreV1.secrets) != 0 {
		t.Errorf("secrets count = %d, want 0 after delete", len(coreV1.secrets))
	}
	if len(certMgr.certs) != 0 {
		t.Errorf("certs count = %d, want 0 after delete", len(certMgr.certs))
	}
}

func TestDeleteWorkspaceIdempotent(t *testing.T) {
	cs := newTestClientset()
	cfg := testConfig()
	mgr := NewWorkspaceManager(cs, cfg)

	if err := mgr.DeleteWorkspace(context.Background(), "nonexistent"); err != nil {
		t.Fatalf("DeleteWorkspace on non-existent: %v", err)
	}
}

func TestExtendPVC(t *testing.T) {
	cs := newTestClientset()
	cfg := testConfig()
	mgr := NewWorkspaceManager(cs, cfg)
	profile := testProfile("python-dev", "Python Dev", nil)

	if _, err := mgr.EnsureWorkspace(context.Background(), profile, "john-doe"); err != nil {
		t.Fatalf("EnsureWorkspace: %v", err)
	}

	if err := mgr.ExtendPVC(context.Background(), "john-doe", "50Gi"); err != nil {
		t.Fatalf("ExtendPVC: %v", err)
	}

	coreV1 := cs.CoreV1.(*fakeCoreV1Client)
	objName := "codx-john-doe"
	pvc, err := coreV1.PersistentVolumeClaims("").Get(context.Background(), objName, metav1.GetOptions{})
	if err != nil {
		t.Fatalf("Get PVC: %v", err)
	}
	storage := pvc.Spec.Resources.Requests[corev1.ResourceStorage]
	if storage.String() != "50Gi" {
		t.Errorf("PVC size = %s, want 50Gi", storage.String())
	}
}

func TestIsCertificateReady(t *testing.T) {
	cert := &cmv1.Certificate{
		Status: cmv1.CertificateStatus{
			Conditions: []cmv1.CertificateCondition{
				{Type: "Ready", Status: cmmeta.ConditionTrue},
			},
		},
	}
	if !IsCertificateReady(cert) {
		t.Error("certificate with Ready=True should be ready")
	}

	certNotReady := &cmv1.Certificate{
		Status: cmv1.CertificateStatus{
			Conditions: []cmv1.CertificateCondition{
				{Type: "Ready", Status: cmmeta.ConditionFalse},
			},
		},
	}
	if IsCertificateReady(certNotReady) {
		t.Error("certificate with Ready=False should not be ready")
	}

	certNoConditions := &cmv1.Certificate{}
	if IsCertificateReady(certNoConditions) {
		t.Error("certificate with no conditions should not be ready")
	}
}

func TestServiceFQDN(t *testing.T) {
	fqdn := ServiceFQDN("codx", "john-doe", "codx-system")
	want := "codx-john-doe.codx-system.svc.cluster.local"
	if fqdn != want {
		t.Errorf("ServiceFQDN = %q, want %q", fqdn, want)
	}
}

func TestBuildServiceIPFamilies(t *testing.T) {
	cfg := testConfig()
	cfg.WorkspaceService.IPFamilies = []string{"IPv4"}
	cfg.WorkspaceService.IPFamilyPolicy = "SingleStack"
	cfg.WorkspaceService.Annotations = map[string]string{"test": "value"}

	svc := BuildService("codx", "john-doe", "codx-system", nil, cfg)

	if len(svc.Spec.IPFamilies) != 1 {
		t.Errorf("IPFamilies length = %d, want 1", len(svc.Spec.IPFamilies))
	}
	if svc.Spec.IPFamilies[0] != corev1.IPv4Protocol {
		t.Errorf("IPFamilies[0] = %q, want %q", svc.Spec.IPFamilies[0], corev1.IPv4Protocol)
	}

	if svc.Spec.IPFamilyPolicy == nil {
		t.Fatal("IPFamilyPolicy is nil")
	}
	if *svc.Spec.IPFamilyPolicy != corev1.IPFamilyPolicySingleStack {
		t.Errorf("IPFamilyPolicy = %q, want %q", *svc.Spec.IPFamilyPolicy, corev1.IPFamilyPolicySingleStack)
	}

	if svc.Annotations["test"] != "value" {
		t.Errorf("Annotations[test] = %q, want value", svc.Annotations["test"])
	}
}

func TestBuildPodEnvUsername(t *testing.T) {
	profile := testProfile("python-dev", "Python Dev", nil)
	pod := BuildPod("codx", "john-doe", "codx-system", "codx-john-doe.codx-system.svc.cluster.local", profile)

	// Find the code-server container.
	var codeServer *corev1.Container
	for i := range pod.Spec.Containers {
		if pod.Spec.Containers[i].Name == "code-server" {
			codeServer = &pod.Spec.Containers[i]
			break
		}
	}
	if codeServer == nil {
		t.Fatal("code-server container not found")
	}

	// Check CODX_USERNAME env var.
	found := false
	for _, env := range codeServer.Env {
		if env.Name == "CODX_USERNAME" && env.Value == "john-doe" {
			found = true
			break
		}
	}
	if !found {
		t.Error("CODX_USERNAME env var not found or incorrect in code-server container")
	}
}
