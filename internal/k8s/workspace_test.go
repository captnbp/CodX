package k8s

import (
	"context"
	"testing"

	"github.com/captnbp/CodX/internal/config"
	cmv1 "github.com/cert-manager/cert-manager/pkg/apis/certmanager/v1"
	cmmeta "github.com/cert-manager/cert-manager/pkg/apis/meta/v1"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/util/intstr"
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
	if len(svc.Spec.Ports) != 2 {
		t.Fatalf("Service has %d ports, want 2", len(svc.Spec.Ports))
	}
	if svc.Spec.Ports[0].Name != "https" || svc.Spec.Ports[0].Port != 9443 {
		t.Errorf("Service port 0 = %s:%d, want https:9443", svc.Spec.Ports[0].Name, svc.Spec.Ports[0].Port)
	}
	if svc.Spec.Ports[1].Name != "envoy-admin" || svc.Spec.Ports[1].Port != 9901 {
		t.Errorf("Service port 1 = %s:%d, want envoy-admin:9901", svc.Spec.Ports[1].Name, svc.Spec.Ports[1].Port)
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
	if pod.Spec.Containers[1].Name != "envoy-tls" {
		t.Errorf("Second container = %q, want envoy-tls", pod.Spec.Containers[1].Name)
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
	pod := BuildPod("codx", "john-doe", "codx-system", "codx-john-doe.codx-system.svc.cluster.local", profile, testConfig())

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

func TestBuildPodEnvoyImage(t *testing.T) {
	profile := testProfile("python-dev", "Python Dev", nil)

	// Default image when workspace.envoyImage is not set.
	pod := BuildPod("codx", "john-doe", "codx-system", "codx-john-doe.codx-system.svc.cluster.local", profile, testConfig())
	if pod.Spec.Containers[1].Image != config.DefaultEnvoyImage {
		t.Errorf("envoy-tls image = %q, want default %q", pod.Spec.Containers[1].Image, config.DefaultEnvoyImage)
	}

	// Image from config.
	cfg := testConfig()
	cfg.Workspace.EnvoyImage = "envoyproxy/envoy:distroless-v1.40-latest"
	pod = BuildPod("codx", "john-doe", "codx-system", "codx-john-doe.codx-system.svc.cluster.local", profile, cfg)
	if pod.Spec.Containers[1].Image != "envoyproxy/envoy:distroless-v1.40-latest" {
		t.Errorf("envoy-tls image = %q, want envoyproxy/envoy:distroless-v1.40-latest", pod.Spec.Containers[1].Image)
	}
}

func TestBuildPodProfileLabel(t *testing.T) {
	profile := testProfile("python-dev", "Python Dev", nil)
	pod := BuildPod("codx", "john-doe", "codx-system", "codx-john-doe.codx-system.svc.cluster.local", profile, testConfig())

	if got := pod.Labels[LabelProfile]; got != "python-dev" {
		t.Errorf("profile label = %q, want python-dev", got)
	}

	// The managed profile label must win over profile podSpec labels.
	profile = testProfile("python-dev", "Python Dev", nil)
	profile.Spec.PodSpec.Labels = map[string]string{LabelProfile: "override-me"}
	pod = BuildPod("codx", "john-doe", "codx-system", "codx-john-doe.codx-system.svc.cluster.local", profile, testConfig())
	if got := pod.Labels[LabelProfile]; got != "python-dev" {
		t.Errorf("profile label with conflicting podSpec label = %q, want python-dev", got)
	}
}

func TestBuildPodImagePullSecrets(t *testing.T) {
	profile := testProfile("python-dev", "Python Dev", nil)
	cfg := testConfig()
	pod := BuildPod("codx", "john-doe", "codx-system", "codx-john-doe.codx-system.svc.cluster.local", profile, cfg)
	if len(pod.Spec.ImagePullSecrets) != 0 {
		t.Errorf("ImagePullSecrets = %v, want none by default", pod.Spec.ImagePullSecrets)
	}

	// No profile pull secrets: fall back to the configured global ones.
	cfg.Workspace.ImagePullSecrets = []string{"global-registry-pull"}
	pod = BuildPod("codx", "john-doe", "codx-system", "codx-john-doe.codx-system.svc.cluster.local", profile, cfg)
	if len(pod.Spec.ImagePullSecrets) != 1 || pod.Spec.ImagePullSecrets[0].Name != "global-registry-pull" {
		t.Errorf("ImagePullSecrets = %v, want [global-registry-pull]", pod.Spec.ImagePullSecrets)
	}

	// Profile pull secrets win over the global fallback.
	profile.Spec.PodSpec.ImagePullSecrets = []corev1.LocalObjectReference{
		{Name: "private-registry-pull"},
	}
	pod = BuildPod("codx", "john-doe", "codx-system", "codx-john-doe.codx-system.svc.cluster.local", profile, cfg)
	if len(pod.Spec.ImagePullSecrets) != 1 || pod.Spec.ImagePullSecrets[0].Name != "private-registry-pull" {
		t.Errorf("ImagePullSecrets = %v, want [private-registry-pull]", pod.Spec.ImagePullSecrets)
	}
}

func TestBuildPodAffinityAndTolerations(t *testing.T) {
	profile := testProfile("python-dev", "Python Dev", nil)
	pod := BuildPod("codx", "john-doe", "codx-system", "codx-john-doe.codx-system.svc.cluster.local", profile, testConfig())
	if pod.Spec.Affinity != nil {
		t.Errorf("Affinity = %+v, want nil by default", pod.Spec.Affinity)
	}
	if len(pod.Spec.NodeSelector) != 0 {
		t.Errorf("NodeSelector = %v, want none by default", pod.Spec.NodeSelector)
	}
	if len(pod.Spec.Tolerations) != 0 {
		t.Errorf("Tolerations = %v, want none by default", pod.Spec.Tolerations)
	}

	profile.Spec.PodSpec.NodeSelector = map[string]string{"codx.io/pool": "workspaces"}
	profile.Spec.PodSpec.Affinity = &corev1.Affinity{
		NodeAffinity: &corev1.NodeAffinity{
			RequiredDuringSchedulingIgnoredDuringExecution: &corev1.NodeSelector{
				NodeSelectorTerms: []corev1.NodeSelectorTerm{{
					MatchExpressions: []corev1.NodeSelectorRequirement{{
						Key:      "codx.io/pool",
						Operator: corev1.NodeSelectorOpIn,
						Values:   []string{"workspaces"},
					}},
				}},
			},
		},
	}
	profile.Spec.PodSpec.Tolerations = []corev1.Toleration{
		{Key: "workload", Operator: corev1.TolerationOpEqual, Value: "workspaces", Effect: corev1.TaintEffectNoSchedule},
	}
	pod = BuildPod("codx", "john-doe", "codx-system", "codx-john-doe.codx-system.svc.cluster.local", profile, testConfig())

	if pod.Spec.NodeSelector["codx.io/pool"] != "workspaces" {
		t.Errorf("NodeSelector = %v, want codx.io/pool=workspaces", pod.Spec.NodeSelector)
	}
	if pod.Spec.Affinity == nil || pod.Spec.Affinity.NodeAffinity == nil {
		t.Fatal("Affinity not propagated to the pod")
	}
	terms := pod.Spec.Affinity.NodeAffinity.RequiredDuringSchedulingIgnoredDuringExecution.NodeSelectorTerms
	if len(terms) != 1 || terms[0].MatchExpressions[0].Key != "codx.io/pool" {
		t.Errorf("Affinity node selector = %+v, want codx.io/pool in [workspaces]", terms)
	}
	if len(pod.Spec.Tolerations) != 1 || pod.Spec.Tolerations[0].Key != "workload" {
		t.Errorf("Tolerations = %v, want one toleration on workload", pod.Spec.Tolerations)
	}
}

func TestBuildPodEnvoySidecar(t *testing.T) {
	profile := testProfile("python-dev", "Python Dev", nil)
	pod := BuildPod("codx", "john-doe", "codx-system", "codx-john-doe.codx-system.svc.cluster.local", profile, testConfig())

	// Envoy sidecar volumes: TLS secret, config ConfigMap, writable /tmp.
	envoy := pod.Spec.Containers[1]
	mounts := map[string]corev1.VolumeMount{}
	for _, m := range envoy.VolumeMounts {
		mounts[m.Name] = m
	}
	if m, ok := mounts["envoy-config"]; !ok {
		t.Error("envoy-config volume mount not found on envoy-tls container")
	} else {
		if m.MountPath != "/etc/envoy/envoy.yaml" {
			t.Errorf("envoy-config mount path = %q, want /etc/envoy/envoy.yaml", m.MountPath)
		}
		if m.SubPath != "envoy.yaml" {
			t.Errorf("envoy-config subPath = %q, want envoy.yaml", m.SubPath)
		}
		if !m.ReadOnly {
			t.Error("envoy-config mount should be read-only")
		}
	}
	if _, ok := mounts["tls"]; !ok {
		t.Error("tls volume mount not found on envoy-tls container")
	}
	if _, ok := mounts["envoy-tmp"]; !ok {
		t.Error("envoy-tmp volume mount not found on envoy-tls container")
	}

	// Pod volumes reference the codx-envoy ConfigMap.
	found := false
	for _, v := range pod.Spec.Volumes {
		if v.Name == "envoy-config" {
			found = true
			if v.ConfigMap == nil || v.ConfigMap.Name != "codx-envoy" {
				t.Errorf("envoy-config volume ConfigMap = %+v, want codx-envoy", v.ConfigMap)
			}
		}
	}
	if !found {
		t.Error("envoy-config volume not found in pod volumes")
	}

	// Security context: non-root, read-only root filesystem, no privileges.
	sc := envoy.SecurityContext
	if sc == nil {
		t.Fatal("envoy-tls security context is nil")
	}
	if sc.RunAsNonRoot == nil || !*sc.RunAsNonRoot {
		t.Error("envoy-tls should run as non-root")
	}
	if sc.ReadOnlyRootFilesystem == nil || !*sc.ReadOnlyRootFilesystem {
		t.Error("envoy-tls should have a read-only root filesystem")
	}
	if sc.AllowPrivilegeEscalation != nil && *sc.AllowPrivilegeEscalation {
		t.Error("envoy-tls should not allow privilege escalation")
	}
}

func TestBuildPodReadinessProbeDefaults(t *testing.T) {
	profile := testProfile("python-dev", "Python Dev", nil)
	pod := BuildPod("codx", "john-doe", "codx-system", "codx-john-doe.codx-system.svc.cluster.local", profile, testConfig())

	codeServer := pod.Spec.Containers[0]
	if codeServer.ReadinessProbe == nil {
		t.Fatal("code-server readiness probe not set")
	}
	if httpGet := codeServer.ReadinessProbe.HTTPGet; httpGet == nil {
		t.Fatal("code-server readiness probe is not an HTTP probe")
	} else {
		if httpGet.Path != "/healthz" {
			t.Errorf("code-server probe path = %q, want /healthz", httpGet.Path)
		}
		if httpGet.Port.IntValue() != 8080 {
			t.Errorf("code-server probe port = %d, want 8080", httpGet.Port.IntValue())
		}
	}

	envoy := pod.Spec.Containers[1]
	if envoy.ReadinessProbe == nil {
		t.Fatal("envoy-tls readiness probe not set")
	}
	if httpGet := envoy.ReadinessProbe.HTTPGet; httpGet == nil {
		t.Fatal("envoy-tls readiness probe is not an HTTP probe")
	} else {
		if httpGet.Path != "/ready" {
			t.Errorf("envoy-tls probe path = %q, want /ready", httpGet.Path)
		}
		if httpGet.Port.IntValue() != 9901 {
			t.Errorf("envoy-tls probe port = %d, want 9901", httpGet.Port.IntValue())
		}
	}
}

func TestBuildPodReadinessProbeOverrides(t *testing.T) {
	profile := testProfile("python-dev", "Python Dev", nil)
	profile.Spec.PodSpec.CodeServerReadinessProbe = &corev1.Probe{
		ProbeHandler: corev1.ProbeHandler{
			TCPSocket: &corev1.TCPSocketAction{Port: intstr.FromInt(9999)},
		},
		PeriodSeconds: 15,
	}
	profile.Spec.PodSpec.EnvoyReadinessProbe = &corev1.Probe{
		ProbeHandler: corev1.ProbeHandler{
			TCPSocket: &corev1.TCPSocketAction{Port: intstr.FromInt(19999)},
		},
		PeriodSeconds: 20,
	}
	pod := BuildPod("codx", "john-doe", "codx-system", "codx-john-doe.codx-system.svc.cluster.local", profile, testConfig())

	if p := pod.Spec.Containers[0].ReadinessProbe; p == nil || p.TCPSocket == nil || p.TCPSocket.Port.IntValue() != 9999 || p.PeriodSeconds != 15 {
		t.Errorf("code-server readiness probe override not applied: %+v", pod.Spec.Containers[0].ReadinessProbe)
	}
	if p := pod.Spec.Containers[1].ReadinessProbe; p == nil || p.TCPSocket == nil || p.TCPSocket.Port.IntValue() != 19999 || p.PeriodSeconds != 20 {
		t.Errorf("envoy-tls readiness probe override not applied: %+v", pod.Spec.Containers[1].ReadinessProbe)
	}
}
