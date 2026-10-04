package k8s

import (
	"context"
	"fmt"
	"io"

	profilev1 "github.com/captnbp/CodX/api/profile/v1"
	cmv1versioned "github.com/cert-manager/cert-manager/pkg/client/clientset/versioned"
	cmv1typed "github.com/cert-manager/cert-manager/pkg/client/clientset/versioned/typed/certmanager/v1"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/apimachinery/pkg/watch"
	"k8s.io/client-go/dynamic"
	"k8s.io/client-go/kubernetes"
	corev1client "k8s.io/client-go/kubernetes/typed/core/v1"
	"k8s.io/client-go/rest"
)

// profileGVR is the GroupVersionResource for the Profile CRD.
var profileGVR = schema.GroupVersionResource{
	Group:    profilev1.GroupName,
	Version:  profilev1.Version,
	Resource: "profiles",
}

// NewInClusterClientset builds a *Clientset from the in-cluster service account
// configuration. It wires up the corev1, cert-manager, and Profile (dynamic)
// clients so that WorkspaceManager and ProfileStore can talk to a real cluster.
func NewInClusterClientset() (*Clientset, error) {
	config, err := rest.InClusterConfig()
	if err != nil {
		return nil, fmt.Errorf("get in-cluster config: %w", err)
	}

	coreClient, err := kubernetes.NewForConfig(config)
	if err != nil {
		return nil, fmt.Errorf("build corev1 clientset: %w", err)
	}

	cmClient, err := cmv1versioned.NewForConfig(config)
	if err != nil {
		return nil, fmt.Errorf("build cert-manager clientset: %w", err)
	}

	dynClient, err := dynamic.NewForConfig(config)
	if err != nil {
		return nil, fmt.Errorf("build dynamic clientset: %w", err)
	}

	return &Clientset{
		CoreV1:      &realCoreV1{inner: coreClient.CoreV1()},
		CertManager: &realCertManager{inner: cmClient.CertmanagerV1()},
		Profile:     &realProfileClient{client: dynClient},
	}, nil
}

// realCoreV1 adapts the standard corev1 client to the CoreV1Client interface.
// The standard typed interfaces are supersets of the ones defined in
// interfaces.go, so they satisfy them directly.
type realCoreV1 struct {
	inner corev1client.CoreV1Interface
}

func (c *realCoreV1) Services(namespace string) ServiceInterface {
	return c.inner.Services(namespace)
}

func (c *realCoreV1) PersistentVolumeClaims(namespace string) PVCInterface {
	return c.inner.PersistentVolumeClaims(namespace)
}

func (c *realCoreV1) Pods(namespace string) PodInterface {
	return &realPodInterface{inner: c.inner.Pods(namespace)}
}

// realPodInterface adapts the typed pod interface to PodInterface. The typed
// interface is a superset except for Logs, which is adapted from GetLogs.
type realPodInterface struct {
	inner corev1client.PodInterface
}

func (p *realPodInterface) Get(ctx context.Context, name string, opts metav1.GetOptions) (*corev1.Pod, error) {
	return p.inner.Get(ctx, name, opts)
}

func (p *realPodInterface) Create(ctx context.Context, pod *corev1.Pod, opts metav1.CreateOptions) (*corev1.Pod, error) {
	return p.inner.Create(ctx, pod, opts)
}

func (p *realPodInterface) Delete(ctx context.Context, name string, opts metav1.DeleteOptions) error {
	return p.inner.Delete(ctx, name, opts)
}

func (p *realPodInterface) List(ctx context.Context, opts metav1.ListOptions) (*corev1.PodList, error) {
	return p.inner.List(ctx, opts)
}

func (p *realPodInterface) Logs(ctx context.Context, name string, opts corev1.PodLogOptions) (io.ReadCloser, error) {
	return p.inner.GetLogs(name, &opts).Stream(ctx)
}

func (c *realCoreV1) ConfigMaps(namespace string) ConfigMapInterface {
	return c.inner.ConfigMaps(namespace)
}

func (c *realCoreV1) Secrets(namespace string) SecretInterface {
	return c.inner.Secrets(namespace)
}

// realCertManager adapts the cert-manager client to the CertManagerClient
// interface.
type realCertManager struct {
	inner cmv1typed.CertmanagerV1Interface
}

func (c *realCertManager) Certificates(namespace string) CertificateInterface {
	return c.inner.Certificates(namespace)
}

// realProfileClient implements ProfileClient using a dynamic client. The
// dynamic client returns unstructured objects, which are converted to typed
// *profilev1.Profile values via the default unstructured converter.
type realProfileClient struct {
	client dynamic.Interface
}

func (c *realProfileClient) List(ctx context.Context, namespace string, opts metav1.ListOptions) (*profilev1.ProfileList, error) {
	list, err := c.client.Resource(profileGVR).Namespace(namespace).List(ctx, opts)
	if err != nil {
		return nil, err
	}

	profiles := make([]profilev1.Profile, 0, len(list.Items))
	for i := range list.Items {
		var p profilev1.Profile
		if err := runtime.DefaultUnstructuredConverter.FromUnstructured(list.Items[i].Object, &p); err != nil {
			return nil, fmt.Errorf("convert profile %s: %w", list.Items[i].GetName(), err)
		}
		profiles = append(profiles, p)
	}

	return &profilev1.ProfileList{Items: profiles}, nil
}

func (c *realProfileClient) Get(ctx context.Context, namespace, name string, opts metav1.GetOptions) (*profilev1.Profile, error) {
	obj, err := c.client.Resource(profileGVR).Namespace(namespace).Get(ctx, name, opts)
	if err != nil {
		return nil, err
	}

	var p profilev1.Profile
	if err := runtime.DefaultUnstructuredConverter.FromUnstructured(obj.Object, &p); err != nil {
		return nil, fmt.Errorf("convert profile %s: %w", name, err)
	}
	return &p, nil
}

func (c *realProfileClient) Watch(ctx context.Context, namespace string, opts metav1.ListOptions) (ProfileWatch, error) {
	raw, err := c.client.Resource(profileGVR).Namespace(namespace).Watch(ctx, opts)
	if err != nil {
		return nil, err
	}
	w := &realProfileWatch{
		raw: raw,
		out: make(chan ProfileWatchEvent),
	}
	go w.pump()
	return w, nil
}

// realProfileWatch adapts a raw watch.Interface from the dynamic client into
// a typed ProfileWatch by decoding each unstructured event into a
// *profilev1.Profile.
type realProfileWatch struct {
	raw watch.Interface
	out chan ProfileWatchEvent
}

// pump reads raw events from the dynamic client watch, converts each object
// to a typed *profilev1.Profile, and forwards it to the typed channel. When
// the raw stream ends (server closes or Stop is called), the typed channel is
// closed.
func (w *realProfileWatch) pump() {
	defer close(w.out)
	for event := range w.raw.ResultChan() {
		obj, ok := event.Object.(*unstructured.Unstructured)
		if !ok {
			continue
		}
		var p profilev1.Profile
		if err := runtime.DefaultUnstructuredConverter.FromUnstructured(obj.Object, &p); err != nil {
			continue
		}
		select {
		case w.out <- ProfileWatchEvent{Type: event.Type, Profile: &p}:
		default:
			// Drop the event if no consumer is reading; the watch contract does
			// not guarantee delivery to a slow consumer.
		}
	}
}

func (w *realProfileWatch) Stop() {
	w.raw.Stop()
}

func (w *realProfileWatch) ResultChan() <-chan ProfileWatchEvent {
	return w.out
}
