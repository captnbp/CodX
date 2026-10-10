package k8s

import (
	"context"
	"encoding/json"
	"fmt"
	"io"

	profilev1 "github.com/captnbp/CodX/api/profile/v1"
	cmv1versioned "github.com/cert-manager/cert-manager/pkg/client/clientset/versioned"
	cmv1typed "github.com/cert-manager/cert-manager/pkg/client/clientset/versioned/typed/certmanager/v1"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/resource"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/apimachinery/pkg/watch"
	"k8s.io/client-go/dynamic"
	"k8s.io/client-go/kubernetes"
	kscheme "k8s.io/client-go/kubernetes/scheme"
	corev1client "k8s.io/client-go/kubernetes/typed/core/v1"
	"k8s.io/client-go/rest"
	"k8s.io/client-go/tools/record"
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

	// The leader election uses the standard typed clientset and a
	// broadcaster-backed EventRecorder to record Events on the Lease.
	recorder := record.NewBroadcaster().NewRecorder(
		kscheme.Scheme,
		corev1.EventSource{Component: "codx-leader-election"},
	)

	return &Clientset{
		leaderElectionClient:   coreClient,
		leaderElectionRecorder: recorder,
		CoreV1:                 &realCoreV1{inner: coreClient.CoreV1()},
		CertManager: &realCertManager{inner: cmClient.CertmanagerV1()},
		Profile:     &realProfileClient{client: dynClient},
		// Pod metrics are fetched with raw requests against the
		// metrics.k8s.io API (served by metrics-server); reuse the corev1
		// REST client of the typed clientset (it already carries auth,
		// TLS, and the negotiated serializer).
		MetricsV1: &realMetricsV1{rest: coreClient.CoreV1().RESTClient()},
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

// realMetricsV1 reads pod metrics from the metrics.k8s.io API served by
// metrics-server through the API server
// (GET /apis/metrics.k8s.io/v1beta1/namespaces/<ns>/pods/<name>).
type realMetricsV1 struct {
	rest rest.Interface
}

func (m *realMetricsV1) PodMetrics(namespace string) PodMetricsInterface {
	return &realPodMetricsInterface{rest: m.rest, namespace: namespace}
}

// podMetricsJSON mirrors the subset of the metrics.k8s.io PodMetrics object
// used by CodX. Quantities are JSON strings ("250m", "536870912").
type podMetricsJSON struct {
	Containers []struct {
		Name  string `json:"name"`
		Usage struct {
			CPU    resource.Quantity `json:"cpu"`
			Memory resource.Quantity `json:"memory"`
		} `json:"usage"`
	} `json:"containers"`
}

type realPodMetricsInterface struct {
	rest      rest.Interface
	namespace string
}

func (p *realPodMetricsInterface) Get(ctx context.Context, name string, opts metav1.GetOptions) (*PodMetrics, error) {
	raw, err := p.rest.Get().
		AbsPath("/apis/metrics.k8s.io/v1beta1/namespaces", p.namespace, "pods", name).
		Do(ctx).Raw()
	if err != nil {
		return nil, err
	}

	var doc podMetricsJSON
	if err := json.Unmarshal(raw, &doc); err != nil {
		return nil, fmt.Errorf("decode pod metrics %s/%s: %w", p.namespace, name, err)
	}

	out := &PodMetrics{Containers: make([]ContainerMetrics, 0, len(doc.Containers))}
	for i := range doc.Containers {
		c := doc.Containers[i]
		out.Containers = append(out.Containers, ContainerMetrics{
			Name:                  c.Name,
			CPUUsedCores:          float64(c.Usage.CPU.MilliValue()) / 1000,
			MemoryWorkingSetBytes: c.Usage.Memory.Value(),
		})
	}
	return out, nil
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
