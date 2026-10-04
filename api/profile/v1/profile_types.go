// Package profilev1 contains the Profile CRD types for CodX.
// A Profile describes how a user workspace pod and its supporting objects
// (Service, PVC, cert-manager Certificate) should be created.
package profilev1

import (
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

const (
	// GroupName is the API group for CodX custom resources.
	GroupName = "codx.io"
	// Version is the API version of the Profile CRD.
	Version = "v1"
)

// Profile represents a workspace template that admins can assign to users.
//
// +kubebuilder:object:root=true
// +kubebuilder:resource:shortName=profile;profiles
// +kubebuilder:subresource:status
// +genclient
type Profile struct {
	metav1.TypeMeta   `json:",inline"`
	metav1.ObjectMeta `json:"metadata,omitempty"`

	// Spec describes the desired workspace configuration.
	Spec ProfileSpec `json:"spec"`

	// Status represents the observed state of the Profile.
	// +optional
	Status ProfileStatus `json:"status,omitempty"`
}

// ProfileSpec holds the configuration for a workspace profile.
//
// +kubebuilder:object:generate=true
type ProfileSpec struct {
	// Title is the human-readable name shown to users in the profile picker.
	Title string `json:"title"`

	// Description is a short summary shown to users in the profile picker.
	// +optional
	Description string `json:"description,omitempty"`

	// OIDCGroups is the list of OIDC group names allowed to use this profile.
	// If empty, the profile is allowed for all authenticated users.
	// +optional
	OIDCGroups []string `json:"oidcGroups,omitempty"`

	// PodSpec describes the workspace pod and its containers.
	PodSpec ProfilePodSpec `json:"podSpec"`

	// PVC defines the default settings for the user's home directory PVC.
	// +optional
	PVC ProfilePVC `json:"pvc,omitempty"`

	// InactivityStopDelaySeconds is the delay after which a workspace pod
	// is stopped when no HTTPS connection is active. Zero means never stop.
	// +optional
	// +kubebuilder:default:=1800
	InactivityStopDelaySeconds int32 `json:"inactivityStopDelaySeconds,omitempty"`
}

// ProfilePodSpec describes the pod spec for a workspace. It is a subset of
// corev1.PodSpec that lets admins customize the image, command, args, env,
// volumes, sidecars and init containers, security context, labels and
// annotations.
//
// +kubebuilder:object:generate=true
type ProfilePodSpec struct {
	// Image is the container image for the code-server main container.
	Image string `json:"image"`

	// Command overrides the container entrypoint.
	// +optional
	Command []string `json:"command,omitempty"`

	// Args overrides the container command arguments.
	// +optional
	Args []string `json:"args,omitempty"`

	// Env adds extra environment variables to the main container.
	// +optional
	Env []corev1.EnvVar `json:"env,omitempty"`

	// Volumes adds extra volumes to the pod.
	// +optional
	Volumes []corev1.Volume `json:"volumes,omitempty"`

	// VolumeMounts adds extra volume mounts to the main container.
	// +optional
	VolumeMounts []corev1.VolumeMount `json:"volumeMounts,omitempty"`

	// Sidecars adds extra sidecar containers to the pod (in addition to the
	// mandatory Envoy TLS termination sidecar injected by CodX).
	// +optional
	Sidecars []corev1.Container `json:"sidecars,omitempty"`

	// InitContainers adds extra init containers to the pod.
	// +optional
	InitContainers []corev1.Container `json:"initContainers,omitempty"`

	// ImagePullSecrets sets the image pull secrets of the workspace pod, to
	// pull images (code-server, sidecars) from private registries.
	// +optional
	ImagePullSecrets []corev1.LocalObjectReference `json:"imagePullSecrets,omitempty"`

	// SecurityContext sets the pod-level security context.
	// +optional
	SecurityContext *corev1.PodSecurityContext `json:"securityContext,omitempty"`

	// Labels adds extra labels to the workspace pod.
	// +optional
	Labels map[string]string `json:"labels,omitempty"`

	// Annotations adds extra annotations to the workspace pod.
	// +optional
	Annotations map[string]string `json:"annotations,omitempty"`

	// Resources sets resource requests and limits for the main container.
	// +optional
	Resources corev1.ResourceRequirements `json:"resources,omitempty"`

	// EnableServiceLinks controls whether Kubernetes injects service-linked
	// environment variables (e.g. *_SERVICE_HOST, *_SERVICE_PORT) into the
	// code-server container. Defaults to false to keep the workspace
	// environment clean and avoid leaking cluster service discovery into
	// user workspaces. Set to true to enable the legacy injection behavior.
	// +optional
	// +kubebuilder:default:=false
	EnableServiceLinks *bool `json:"enableServiceLinks,omitempty"`

	// CodeServerReadinessProbe overrides the readiness probe of the
	// code-server main container. When unset, a default HTTP GET probe
	// on /healthz port 8080 is applied.
	// +optional
	CodeServerReadinessProbe *corev1.Probe `json:"codeServerReadinessProbe,omitempty"`

	// EnvoyReadinessProbe overrides the readiness probe of the Envoy TLS
	// sidecar. When unset, a default HTTP GET probe on the Envoy admin
	// endpoint /ready port 9901 is applied.
	// +optional
	EnvoyReadinessProbe *corev1.Probe `json:"envoyReadinessProbe,omitempty"`
}

// ProfilePVC defines the default settings for the user's home directory PVC.
//
// +kubebuilder:object:generate=true
type ProfilePVC struct {
	// Size is the default size of the PVC (e.g. "10Gi").
	// +optional
	// +kubebuilder:default:="10Gi"
	Size string `json:"size,omitempty"`

	// StorageClassName is the name of the StorageClass to use. If empty the
	// cluster default storage class is used.
	// +optional
	StorageClassName *string `json:"storageClassName,omitempty"`

	// Labels adds extra labels to the PVC.
	// +optional
	Labels map[string]string `json:"labels,omitempty"`

	// Annotations adds extra annotations to the PVC.
	// +optional
	Annotations map[string]string `json:"annotations,omitempty"`
}

// ProfileStatus represents the observed state of a Profile.
//
// +kubebuilder:object:generate=true
type ProfileStatus struct {
	// ObservedGeneration is the most recent generation observed for the Profile.
	// +optional
	ObservedGeneration int64 `json:"observedGeneration,omitempty"`

	// Conditions represents the latest available observations of the Profile state.
	// +optional
	Conditions []metav1.Condition `json:"conditions,omitempty"`
}

// ProfileList is a list of Profile resources.
//
// +kubebuilder:object:root=true
type ProfileList struct {
	metav1.TypeMeta `json:",inline"`
	metav1.ListMeta `json:"metadata,omitempty"`

	Items []Profile `json:"items"`
}
