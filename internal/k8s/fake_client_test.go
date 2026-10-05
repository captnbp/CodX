package k8s

import (
	"context"
	"io"
	"strings"
	"sync"

	profilev1 "github.com/captnbp/CodX/api/profile/v1"
	cmv1 "github.com/cert-manager/cert-manager/pkg/apis/certmanager/v1"
	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/labels"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/apimachinery/pkg/watch"
)

// fakeCoreV1Client implements CoreV1Client for testing.
type fakeCoreV1Client struct {
	mu         sync.Mutex
	podMetrics map[string]*PodMetrics
	services   map[string]*corev1.Service
	pvcs       map[string]*corev1.PersistentVolumeClaim
	pods       map[string]*corev1.Pod
	configMaps map[string]*corev1.ConfigMap
	secrets    map[string]*corev1.Secret
}

func newFakeCoreV1Client() *fakeCoreV1Client {
	return &fakeCoreV1Client{
		podMetrics: make(map[string]*PodMetrics),
		services:   make(map[string]*corev1.Service),
		pvcs:       make(map[string]*corev1.PersistentVolumeClaim),
		pods:       make(map[string]*corev1.Pod),
		configMaps: make(map[string]*corev1.ConfigMap),
		secrets:    make(map[string]*corev1.Secret),
	}
}

func (c *fakeCoreV1Client) Services(namespace string) ServiceInterface {
	return &fakeServiceInterface{client: c}
}

func (c *fakeCoreV1Client) PersistentVolumeClaims(namespace string) PVCInterface {
	return &fakePVCInterface{client: c}
}

func (c *fakeCoreV1Client) Pods(namespace string) PodInterface {
	return &fakePodInterface{client: c}
}

func (c *fakeCoreV1Client) ConfigMaps(namespace string) ConfigMapInterface {
	return &fakeConfigMapInterface{client: c}
}

func (c *fakeCoreV1Client) Secrets(namespace string) SecretInterface {
	return &fakeSecretInterface{client: c}
}

func (c *fakeCoreV1Client) MetricsV1() MetricsV1Client {
	return &fakeMetricsV1Client{metrics: c.podMetrics}
}

type fakeMetricsV1Client struct {
	metrics map[string]*PodMetrics
}

func (c *fakeMetricsV1Client) PodMetrics(namespace string) PodMetricsInterface {
	return &fakePodMetricsInterface{metrics: c.metrics, namespace: namespace}
}

type fakePodMetricsInterface struct {
	metrics   map[string]*PodMetrics
	namespace string
}

func (f *fakePodMetricsInterface) Get(ctx context.Context, name string, opts metav1.GetOptions) (*PodMetrics, error) {
	metrics, ok := f.metrics[f.namespace+"/"+name]
	if !ok {
		return nil, apierrors.NewNotFound(schema.GroupResource{Group: "metrics.k8s.io", Resource: "pods"}, name)
	}
	return metrics, nil
}

type fakeServiceInterface struct {
	client *fakeCoreV1Client
}

func (f *fakeServiceInterface) Get(ctx context.Context, name string, opts metav1.GetOptions) (*corev1.Service, error) {
	f.client.mu.Lock()
	defer f.client.mu.Unlock()
	svc, ok := f.client.services[name]
	if !ok {
		return nil, apierrors.NewNotFound(schema.GroupResource{Resource: "services"}, name)
	}
	return svc.DeepCopy(), nil
}

func (f *fakeServiceInterface) Create(ctx context.Context, svc *corev1.Service, opts metav1.CreateOptions) (*corev1.Service, error) {
	f.client.mu.Lock()
	defer f.client.mu.Unlock()
	if _, exists := f.client.services[svc.Name]; exists {
		return nil, apierrors.NewAlreadyExists(schema.GroupResource{Resource: "services"}, svc.Name)
	}
	f.client.services[svc.Name] = svc.DeepCopy()
	return svc.DeepCopy(), nil
}

func (f *fakeServiceInterface) Delete(ctx context.Context, name string, opts metav1.DeleteOptions) error {
	f.client.mu.Lock()
	defer f.client.mu.Unlock()
	if _, exists := f.client.services[name]; !exists {
		return apierrors.NewNotFound(schema.GroupResource{Resource: "services"}, name)
	}
	delete(f.client.services, name)
	return nil
}

func (f *fakeServiceInterface) List(ctx context.Context, opts metav1.ListOptions) (*corev1.ServiceList, error) {
	f.client.mu.Lock()
	defer f.client.mu.Unlock()
	items := make([]corev1.Service, 0, len(f.client.services))
	for _, svc := range f.client.services {
		items = append(items, *svc.DeepCopy())
	}
	return &corev1.ServiceList{Items: items}, nil
}

type fakePVCInterface struct {
	client *fakeCoreV1Client
}

func (f *fakePVCInterface) Get(ctx context.Context, name string, opts metav1.GetOptions) (*corev1.PersistentVolumeClaim, error) {
	f.client.mu.Lock()
	defer f.client.mu.Unlock()
	pvc, ok := f.client.pvcs[name]
	if !ok {
		return nil, apierrors.NewNotFound(schema.GroupResource{Resource: "persistentvolumeclaims"}, name)
	}
	return pvc.DeepCopy(), nil
}

func (f *fakePVCInterface) Create(ctx context.Context, pvc *corev1.PersistentVolumeClaim, opts metav1.CreateOptions) (*corev1.PersistentVolumeClaim, error) {
	f.client.mu.Lock()
	defer f.client.mu.Unlock()
	if _, exists := f.client.pvcs[pvc.Name]; exists {
		return nil, apierrors.NewAlreadyExists(schema.GroupResource{Resource: "persistentvolumeclaims"}, pvc.Name)
	}
	f.client.pvcs[pvc.Name] = pvc.DeepCopy()
	return pvc.DeepCopy(), nil
}

func (f *fakePVCInterface) Update(ctx context.Context, pvc *corev1.PersistentVolumeClaim, opts metav1.UpdateOptions) (*corev1.PersistentVolumeClaim, error) {
	f.client.mu.Lock()
	defer f.client.mu.Unlock()
	f.client.pvcs[pvc.Name] = pvc.DeepCopy()
	return pvc.DeepCopy(), nil
}

func (f *fakePVCInterface) Delete(ctx context.Context, name string, opts metav1.DeleteOptions) error {
	f.client.mu.Lock()
	defer f.client.mu.Unlock()
	if _, exists := f.client.pvcs[name]; !exists {
		return apierrors.NewNotFound(schema.GroupResource{Resource: "persistentvolumeclaims"}, name)
	}
	delete(f.client.pvcs, name)
	return nil
}

type fakePodInterface struct {
	client *fakeCoreV1Client
}

func (f *fakePodInterface) Get(ctx context.Context, name string, opts metav1.GetOptions) (*corev1.Pod, error) {
	f.client.mu.Lock()
	defer f.client.mu.Unlock()
	pod, ok := f.client.pods[name]
	if !ok {
		return nil, apierrors.NewNotFound(schema.GroupResource{Resource: "pods"}, name)
	}
	return pod.DeepCopy(), nil
}

func (f *fakePodInterface) Create(ctx context.Context, pod *corev1.Pod, opts metav1.CreateOptions) (*corev1.Pod, error) {
	f.client.mu.Lock()
	defer f.client.mu.Unlock()
	if _, exists := f.client.pods[pod.Name]; exists {
		return nil, apierrors.NewAlreadyExists(schema.GroupResource{Resource: "pods"}, pod.Name)
	}
	f.client.pods[pod.Name] = pod.DeepCopy()
	return pod.DeepCopy(), nil
}

func (f *fakePodInterface) Delete(ctx context.Context, name string, opts metav1.DeleteOptions) error {
	f.client.mu.Lock()
	defer f.client.mu.Unlock()
	if _, exists := f.client.pods[name]; !exists {
		return apierrors.NewNotFound(schema.GroupResource{Resource: "pods"}, name)
	}
	delete(f.client.pods, name)
	return nil
}

func (f *fakePodInterface) Logs(ctx context.Context, name string, opts corev1.PodLogOptions) (io.ReadCloser, error) {
	return io.NopCloser(strings.NewReader("fake pod logs")), nil
}

func (f *fakePodInterface) List(ctx context.Context, opts metav1.ListOptions) (*corev1.PodList, error) {
	f.client.mu.Lock()
	defer f.client.mu.Unlock()

	selector := labels.Everything()
	if opts.LabelSelector != "" {
		var err error
		selector, err = labels.Parse(opts.LabelSelector)
		if err != nil {
			return nil, err
		}
	}

	items := make([]corev1.Pod, 0, len(f.client.pods))
	for _, pod := range f.client.pods {
		if !selector.Matches(labels.Set(pod.Labels)) {
			continue
		}
		items = append(items, *pod.DeepCopy())
	}
	return &corev1.PodList{Items: items}, nil
}

type fakeConfigMapInterface struct {
	client *fakeCoreV1Client
}

func (f *fakeConfigMapInterface) Get(ctx context.Context, name string, opts metav1.GetOptions) (*corev1.ConfigMap, error) {
	f.client.mu.Lock()
	defer f.client.mu.Unlock()
	cm, ok := f.client.configMaps[name]
	if !ok {
		return nil, apierrors.NewNotFound(schema.GroupResource{Resource: "configmaps"}, name)
	}
	return cm.DeepCopy(), nil
}

type fakeSecretInterface struct {
	client *fakeCoreV1Client
}

func (f *fakeSecretInterface) Get(ctx context.Context, name string, opts metav1.GetOptions) (*corev1.Secret, error) {
	f.client.mu.Lock()
	defer f.client.mu.Unlock()
	sec, ok := f.client.secrets[name]
	if !ok {
		return nil, apierrors.NewNotFound(schema.GroupResource{Resource: "secrets"}, name)
	}
	return sec.DeepCopy(), nil
}

func (f *fakeSecretInterface) Delete(ctx context.Context, name string, opts metav1.DeleteOptions) error {
	f.client.mu.Lock()
	defer f.client.mu.Unlock()
	if _, exists := f.client.secrets[name]; !exists {
		return apierrors.NewNotFound(schema.GroupResource{Resource: "secrets"}, name)
	}
	delete(f.client.secrets, name)
	return nil
}

// fakeCertManagerClient implements CertManagerClient for testing.
type fakeCertManagerClient struct {
	mu    sync.Mutex
	certs map[string]*cmv1.Certificate
}

func newFakeCertManagerClient() *fakeCertManagerClient {
	return &fakeCertManagerClient{
		certs: make(map[string]*cmv1.Certificate),
	}
}

func (c *fakeCertManagerClient) Certificates(namespace string) CertificateInterface {
	return &fakeCertificateInterface{client: c}
}

type fakeCertificateInterface struct {
	client *fakeCertManagerClient
}

func (f *fakeCertificateInterface) Get(ctx context.Context, name string, opts metav1.GetOptions) (*cmv1.Certificate, error) {
	f.client.mu.Lock()
	defer f.client.mu.Unlock()
	cert, ok := f.client.certs[name]
	if !ok {
		return nil, apierrors.NewNotFound(schema.GroupResource{Group: "cert-manager.io", Resource: "certificates"}, name)
	}
	return cert.DeepCopy(), nil
}

func (f *fakeCertificateInterface) Create(ctx context.Context, cert *cmv1.Certificate, opts metav1.CreateOptions) (*cmv1.Certificate, error) {
	f.client.mu.Lock()
	defer f.client.mu.Unlock()
	if _, exists := f.client.certs[cert.Name]; exists {
		return nil, apierrors.NewAlreadyExists(schema.GroupResource{Group: "cert-manager.io", Resource: "certificates"}, cert.Name)
	}
	f.client.certs[cert.Name] = cert.DeepCopy()
	return cert.DeepCopy(), nil
}

func (f *fakeCertificateInterface) Delete(ctx context.Context, name string, opts metav1.DeleteOptions) error {
	f.client.mu.Lock()
	defer f.client.mu.Unlock()
	if _, exists := f.client.certs[name]; !exists {
		return apierrors.NewNotFound(schema.GroupResource{Group: "cert-manager.io", Resource: "certificates"}, name)
	}
	delete(f.client.certs, name)
	return nil
}

func (f *fakeCertificateInterface) List(ctx context.Context, opts metav1.ListOptions) (*cmv1.CertificateList, error) {
	f.client.mu.Lock()
	defer f.client.mu.Unlock()
	items := make([]cmv1.Certificate, 0, len(f.client.certs))
	for _, cert := range f.client.certs {
		items = append(items, *cert.DeepCopy())
	}
	return &cmv1.CertificateList{Items: items}, nil
}

// fakeProfileClient implements ProfileClient for testing.
type fakeProfileClient struct {
	profiles []*profilev1.Profile
}

func (f *fakeProfileClient) List(ctx context.Context, namespace string, opts metav1.ListOptions) (*profilev1.ProfileList, error) {
	items := make([]profilev1.Profile, 0, len(f.profiles))
	for _, p := range f.profiles {
		items = append(items, *p.DeepCopy())
	}
	return &profilev1.ProfileList{Items: items}, nil
}

func (f *fakeProfileClient) Get(ctx context.Context, namespace, name string, opts metav1.GetOptions) (*profilev1.Profile, error) {
	for _, p := range f.profiles {
		if p.Name == name {
			return p.DeepCopy(), nil
		}
	}
	return nil, apierrors.NewNotFound(schema.GroupResource{Group: "codx.io", Resource: "profiles"}, name)
}

func (f *fakeProfileClient) Watch(ctx context.Context, namespace string, opts metav1.ListOptions) (ProfileWatch, error) {
	ch := make(chan ProfileWatchEvent)
	w := &fakeProfileWatch{ch: ch}
	go func() {
		defer close(ch)
		for _, p := range f.profiles {
			ch <- ProfileWatchEvent{Type: watch.Added, Profile: p.DeepCopy()}
		}
	}()
	return w, nil
}

// fakeProfileWatch implements ProfileWatch for tests. It emits the current
// set of profiles as Added events then closes the channel.
type fakeProfileWatch struct {
	ch chan ProfileWatchEvent
}

func (w *fakeProfileWatch) Stop() {}

func (w *fakeProfileWatch) ResultChan() <-chan ProfileWatchEvent { return w.ch }
