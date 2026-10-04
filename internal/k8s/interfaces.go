package k8s

import (
	"context"
	"io"

	profilev1 "github.com/captnbp/CodX/api/profile/v1"
	cmv1 "github.com/cert-manager/cert-manager/pkg/apis/certmanager/v1"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/watch"
)

// CoreV1Client is the subset of the Kubernetes corev1 clientset used by CodX.
type CoreV1Client interface {
	Services(namespace string) ServiceInterface
	PersistentVolumeClaims(namespace string) PVCInterface
	Pods(namespace string) PodInterface
	ConfigMaps(namespace string) ConfigMapInterface
	Secrets(namespace string) SecretInterface
}

// NodeInterface wraps node proxy operations.
type NodeInterface interface {
	// StatsSummary returns the kubelet stats summary of a node
	// (per-container CPU/memory, per-pod network). The caller must close
	// the returned ReadCloser.
	StatsSummary(ctx context.Context, name string) (io.ReadCloser, error)
}

// ServiceInterface wraps corev1 Service operations.
type ServiceInterface interface {
	Get(ctx context.Context, name string, opts metav1.GetOptions) (*corev1.Service, error)
	Create(ctx context.Context, svc *corev1.Service, opts metav1.CreateOptions) (*corev1.Service, error)
	Delete(ctx context.Context, name string, opts metav1.DeleteOptions) error
	List(ctx context.Context, opts metav1.ListOptions) (*corev1.ServiceList, error)
}

// PVCInterface wraps corev1 PVC operations.
type PVCInterface interface {
	Get(ctx context.Context, name string, opts metav1.GetOptions) (*corev1.PersistentVolumeClaim, error)
	Create(ctx context.Context, pvc *corev1.PersistentVolumeClaim, opts metav1.CreateOptions) (*corev1.PersistentVolumeClaim, error)
	Update(ctx context.Context, pvc *corev1.PersistentVolumeClaim, opts metav1.UpdateOptions) (*corev1.PersistentVolumeClaim, error)
	Delete(ctx context.Context, name string, opts metav1.DeleteOptions) error
}

// PodInterface wraps corev1 Pod operations.
type PodInterface interface {
	Get(ctx context.Context, name string, opts metav1.GetOptions) (*corev1.Pod, error)
	Create(ctx context.Context, pod *corev1.Pod, opts metav1.CreateOptions) (*corev1.Pod, error)
	Delete(ctx context.Context, name string, opts metav1.DeleteOptions) error
	List(ctx context.Context, opts metav1.ListOptions) (*corev1.PodList, error)
	// Logs returns a ReadCloser streaming the logs of one container of a
	// pod. The caller must close it.
	Logs(ctx context.Context, name string, opts corev1.PodLogOptions) (io.ReadCloser, error)
}

// ConfigMapInterface wraps corev1 ConfigMap operations.
type ConfigMapInterface interface {
	Get(ctx context.Context, name string, opts metav1.GetOptions) (*corev1.ConfigMap, error)
}

// SecretInterface wraps corev1 Secret operations.
type SecretInterface interface {
	Get(ctx context.Context, name string, opts metav1.GetOptions) (*corev1.Secret, error)
	Delete(ctx context.Context, name string, opts metav1.DeleteOptions) error
}

// CertManagerClient is the subset of the cert-manager clientset used by CodX.
type CertManagerClient interface {
	Certificates(namespace string) CertificateInterface
}

// CertificateInterface wraps cert-manager Certificate operations.
type CertificateInterface interface {
	Get(ctx context.Context, name string, opts metav1.GetOptions) (*cmv1.Certificate, error)
	Create(ctx context.Context, cert *cmv1.Certificate, opts metav1.CreateOptions) (*cmv1.Certificate, error)
	Delete(ctx context.Context, name string, opts metav1.DeleteOptions) error
	List(ctx context.Context, opts metav1.ListOptions) (*cmv1.CertificateList, error)
}

// ProfileWatchEvent is a typed watch event carrying a Profile resource.
type ProfileWatchEvent struct {
	Type    watch.EventType
	Profile *profilev1.Profile
}

// ProfileWatch is a typed watch channel for Profile resources. It mirrors
// k8s.io/apimachinery/pkg/watch.Interface but delivers already-decoded
// *profilev1.Profile values instead of raw runtime.Object.
type ProfileWatch interface {
	// Stop stops the watch and closes the result channel. The consumer must
	// call Stop once it no longer reads events.
	Stop()
	// ResultChan returns the channel of typed watch events. The channel is
	// closed when the watch is stopped or the server ends the stream.
	ResultChan() <-chan ProfileWatchEvent
}

// ProfileClient is the interface for reading Profile CRs.
type ProfileClient interface {
	List(ctx context.Context, namespace string, opts metav1.ListOptions) (*profilev1.ProfileList, error)
	Get(ctx context.Context, namespace, name string, opts metav1.GetOptions) (*profilev1.Profile, error)
	Watch(ctx context.Context, namespace string, opts metav1.ListOptions) (ProfileWatch, error)
}
