// Package k8s provides Kubernetes client setup and workspace object
// management for CodX.
package k8s

import (
	"fmt"

	profilev1 "github.com/captnbp/CodX/api/profile/v1"
	"github.com/captnbp/CodX/internal/config"
	cmv1 "github.com/cert-manager/cert-manager/pkg/apis/certmanager/v1"
	cmmeta "github.com/cert-manager/cert-manager/pkg/apis/meta/v1"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/resource"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/util/intstr"
)

// Common labels for all workspace objects.
const (
	LabelInstance  = "app.kubernetes.io/instance"
	LabelManagedBy = "app.kubernetes.io/managed-by"
	LabelName      = "app.kubernetes.io/name"
	LabelComponent = "app.kubernetes.io/component"
	ComponentName  = "workspace"
)

// workspaceLabels returns the standard label set for a workspace object.
func workspaceLabels(instance, slug string) map[string]string {
	return map[string]string{
		LabelInstance:  slug,
		LabelManagedBy: instance,
		LabelName:      instance,
		LabelComponent: ComponentName,
	}
}

// ServiceFQDN returns the fully-qualified DNS name for a workspace service:
// <release>-<slug>.<namespace>.svc.cluster.local
func ServiceFQDN(instance, slug, namespace string) string {
	return fmt.Sprintf("%s-%s.%s.svc.cluster.local", instance, slug, namespace)
}

// BuildService creates a Service object for a workspace.
func BuildService(instance, slug, namespace string, profile *profilev1.Profile, cfg *config.Config) *corev1.Service {
	svc := &corev1.Service{
		ObjectMeta: metav1.ObjectMeta{
			Name:        objectName(instance, slug),
			Namespace:   namespace,
			Labels:      workspaceLabels(instance, slug),
			Annotations: mergeAnnotations(nil, cfg.WorkspaceService.Annotations),
		},
		Spec: corev1.ServiceSpec{
			Selector: map[string]string{
				LabelInstance:  slug,
				LabelComponent: ComponentName,
			},
			Ports: []corev1.ServicePort{
				{
					Name:       "https",
					Port:       9443,
					TargetPort: intstr.FromInt(9443),
					Protocol:   corev1.ProtocolTCP,
				},
			},
		},
	}

	// Apply IP family settings from config.
	if len(cfg.WorkspaceService.IPFamilies) > 0 {
		families := make([]corev1.IPFamily, len(cfg.WorkspaceService.IPFamilies))
		for i, f := range cfg.WorkspaceService.IPFamilies {
			families[i] = corev1.IPFamily(f)
		}
		svc.Spec.IPFamilies = families
	}
	if cfg.WorkspaceService.IPFamilyPolicy != "" {
		policy := corev1.IPFamilyPolicy(cfg.WorkspaceService.IPFamilyPolicy)
		svc.Spec.IPFamilyPolicy = &policy
	}

	return svc
}

// BuildPVC creates a PVC for the user's home directory.
func BuildPVC(instance, slug, namespace string, profile *profilev1.Profile) (*corev1.PersistentVolumeClaim, error) {
	size := profile.Spec.PVC.Size
	if size == "" {
		size = "10Gi"
	}

	quantity, err := resource.ParseQuantity(size)
	if err != nil {
		return nil, fmt.Errorf("parse PVC size %q: %w", size, err)
	}

	pvc := &corev1.PersistentVolumeClaim{
		ObjectMeta: metav1.ObjectMeta{
			Name:      objectName(instance, slug),
			Namespace: namespace,
			Labels:    mergeLabels(workspaceLabels(instance, slug), profile.Spec.PVC.Labels),
		},
		Spec: corev1.PersistentVolumeClaimSpec{
			AccessModes: []corev1.PersistentVolumeAccessMode{
				corev1.ReadWriteOnce,
			},
			Resources: corev1.VolumeResourceRequirements{
				Requests: corev1.ResourceList{
					corev1.ResourceStorage: quantity,
				},
			},
		},
	}

	if profile.Spec.PVC.StorageClassName != nil && *profile.Spec.PVC.StorageClassName != "" {
		pvc.Spec.StorageClassName = profile.Spec.PVC.StorageClassName
	}

	if len(profile.Spec.PVC.Annotations) > 0 {
		pvc.Annotations = make(map[string]string, len(profile.Spec.PVC.Annotations))
		for k, v := range profile.Spec.PVC.Annotations {
			pvc.Annotations[k] = v
		}
	}

	return pvc, nil
}

// BuildCertificate creates a cert-manager Certificate for the workspace TLS.
func BuildCertificate(instance, slug, namespace, fqdn string, cfg config.CertManagerConfig) *cmv1.Certificate {
	secretName := objectName(instance, slug) + "-tls"

	validity, _ := cfg.ParseValidity()
	renewal, _ := cfg.ParseRenewal()

	cert := &cmv1.Certificate{
		ObjectMeta: metav1.ObjectMeta{
			Name:      objectName(instance, slug),
			Namespace: namespace,
			Labels:    workspaceLabels(instance, slug),
		},
		Spec: cmv1.CertificateSpec{
			SecretName: secretName,
			DNSNames:   []string{fqdn},
			IssuerRef: cmmeta.IssuerReference{
				Name:  cfg.IssuerName,
				Kind:  cfg.IssuerType,
				Group: cfg.IssuerGroup,
			},
			Usages: []cmv1.KeyUsage{
				cmv1.UsageClientAuth,
				cmv1.UsageServerAuth,
			},
		},
	}

	if validity > 0 {
		cert.Spec.Duration = &metav1.Duration{Duration: validity}
	}
	if renewal > 0 {
		cert.Spec.RenewBefore = &metav1.Duration{Duration: renewal}
	}

	return cert
}

// BuildPod creates the workspace Pod with the code-server container, nginx
// sidecar, and PVC mount.
func BuildPod(instance, slug, namespace, fqdn string, profile *profilev1.Profile) *corev1.Pod {
	objName := objectName(instance, slug)
	labels := mergeLabels(workspaceLabels(instance, slug), profile.Spec.PodSpec.Labels)

	pod := &corev1.Pod{
		ObjectMeta: metav1.ObjectMeta{
			Name:        objName,
			Namespace:   namespace,
			Labels:      labels,
			Annotations: profile.Spec.PodSpec.Annotations,
		},
		Spec: corev1.PodSpec{
			SecurityContext:    profile.Spec.PodSpec.SecurityContext,
			InitContainers:     profile.Spec.PodSpec.InitContainers,
			Containers:         buildContainers(profile, objName, slug),
			Volumes:             buildVolumes(profile, objName),
			EnableServiceLinks: resolveEnableServiceLinks(profile),
		},
	}

	return pod
}

// resolveEnableServiceLinks returns the value for PodSpec.EnableServiceLinks
// from the profile. It defaults to false so the workspace environment stays
// clean and the nginx-tls sidecar never receives service-linked env vars.
func resolveEnableServiceLinks(profile *profilev1.Profile) *bool {
	if profile.Spec.PodSpec.EnableServiceLinks != nil {
		return profile.Spec.PodSpec.EnableServiceLinks
	}
	falseVal := false
	return &falseVal
}

// buildContainers builds the container list: the code-server main container,
// the nginx sidecar, plus any extra sidecars from the profile.
func buildContainers(profile *profilev1.Profile, objName, userSlug string) []corev1.Container {
	containers := make([]corev1.Container, 0, 2+len(profile.Spec.PodSpec.Sidecars))

	// Main code-server container.
	main := corev1.Container{
		Name:         "code-server",
		Image:        profile.Spec.PodSpec.Image,
		Resources:    profile.Spec.PodSpec.Resources,
		VolumeMounts: profile.Spec.PodSpec.VolumeMounts,
		Env:          profile.Spec.PodSpec.Env,
	}
	if len(profile.Spec.PodSpec.Command) > 0 {
		main.Command = profile.Spec.PodSpec.Command
	}
	if len(profile.Spec.PodSpec.Args) > 0 {
		main.Args = profile.Spec.PodSpec.Args
	}
	// Add CODX_USERNAME env var so code-server knows the user.
	main.Env = append(main.Env, corev1.EnvVar{
		Name:  "CODX_USERNAME",
		Value: userSlug,
	})
	// Mount PVC at /home/coder.
	main.VolumeMounts = append(main.VolumeMounts, corev1.VolumeMount{
		Name:      "home",
		MountPath: "/home/coder",
	})

	containers = append(containers, main)

	// Nginx TLS termination sidecar.
	containers = append(containers, corev1.Container{
		Name:  "nginx-tls",
		Image: "nginx:1.31-alpine",
		Ports: []corev1.ContainerPort{
			{ContainerPort: 9443, Name: "https", Protocol: corev1.ProtocolTCP},
		},
		VolumeMounts: []corev1.VolumeMount{
			{Name: "tls", MountPath: "/tls", ReadOnly: true},
			{Name: "nginx-config", MountPath: "/etc/nginx/nginx.conf", SubPath: "nginx.conf", ReadOnly: true},
			{Name: "nginx-tmp", MountPath: "/tmp"},
		},
		SecurityContext: &corev1.SecurityContext{
			RunAsUser:                int64Ptr(101),
			RunAsNonRoot:             boolPtr(true),
			ReadOnlyRootFilesystem:   boolPtr(true),
			AllowPrivilegeEscalation: boolPtr(false),
			Capabilities: &corev1.Capabilities{
				Drop: []corev1.Capability{"ALL"},
			},
		},
	})

	// Extra sidecars from profile.
	containers = append(containers, profile.Spec.PodSpec.Sidecars...)

	return containers
}

// buildVolumes builds the volume list: the PVC volume, the TLS secret volume,
// the nginx config volume, the nginx tmp volume, plus any extra volumes from
// the profile.
func buildVolumes(profile *profilev1.Profile, objName string) []corev1.Volume {
	volumes := make([]corev1.Volume, 0, 4+len(profile.Spec.PodSpec.Volumes))

	// PVC for /home/coder.
	volumes = append(volumes, corev1.Volume{
		Name: "home",
		VolumeSource: corev1.VolumeSource{
			PersistentVolumeClaim: &corev1.PersistentVolumeClaimVolumeSource{
				ClaimName: objName,
			},
		},
	})

	// TLS certificate secret (from cert-manager).
	volumes = append(volumes, corev1.Volume{
		Name: "tls",
		VolumeSource: corev1.VolumeSource{
			Secret: &corev1.SecretVolumeSource{
				SecretName: objName + "-tls",
			},
		},
	})

	// Nginx config from ConfigMap.
	volumes = append(volumes, corev1.Volume{
		Name: "nginx-config",
		VolumeSource: corev1.VolumeSource{
			ConfigMap: &corev1.ConfigMapVolumeSource{
				LocalObjectReference: corev1.LocalObjectReference{
					Name: "codx-nginx",
				},
			},
		},
	})

	// Writable /tmp for the nginx sidecar (pid + temp paths) so it runs under
	// a read-only root filesystem.
	volumes = append(volumes, corev1.Volume{
		Name: "nginx-tmp",
		VolumeSource: corev1.VolumeSource{
			EmptyDir: &corev1.EmptyDirVolumeSource{},
		},
	})

	// Extra volumes from profile.
	volumes = append(volumes, profile.Spec.PodSpec.Volumes...)

	return volumes
}

// objectName returns <instance>-<slug>.
func objectName(instance, slug string) string {
	return instance + "-" + slug
}

// mergeLabels merges base labels with extra labels (extra wins on conflict).
func mergeLabels(base, extra map[string]string) map[string]string {
	result := make(map[string]string, len(base)+len(extra))
	for k, v := range base {
		result[k] = v
	}
	for k, v := range extra {
		result[k] = v
	}
	return result
}

// mergeAnnotations merges base annotations with extra annotations (extra wins on conflict).
func mergeAnnotations(base, extra map[string]string) map[string]string {
	result := make(map[string]string, len(base)+len(extra))
	for k, v := range base {
		result[k] = v
	}
	for k, v := range extra {
		result[k] = v
	}
	return result
}

// EnsureClientset groups the Kubernetes typed clientsets needed by CodX.
type Clientset struct {
	CoreV1      CoreV1Client
	CertManager CertManagerClient
	Profile     ProfileClient
}

func int64Ptr(v int64) *int64 { return &v }
func boolPtr(v bool) *bool    { return &v }
