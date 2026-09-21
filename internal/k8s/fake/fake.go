// Package fake provides in-memory implementations of the k8s client
// interfaces for use in tests across packages.
package fake

import (
	"context"
	"sync"

	profilev1 "github.com/captnbp/CodX/api/profile/v1"
	"github.com/captnbp/CodX/internal/k8s"
	cmv1 "github.com/cert-manager/cert-manager/pkg/apis/certmanager/v1"
	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/apimachinery/pkg/watch"
)

// CoreV1Client is an in-memory implementation of k8s.CoreV1Client.
type CoreV1Client struct {
	mu           sync.Mutex
	ServiceMap   map[string]*corev1.Service
	PVCMap       map[string]*corev1.PersistentVolumeClaim
	PodMap       map[string]*corev1.Pod
	ConfigMapMap map[string]*corev1.ConfigMap
	SecretMap    map[string]*corev1.Secret
}

// NewCoreV1Client creates a new in-memory CoreV1Client.
func NewCoreV1Client() *CoreV1Client {
	return &CoreV1Client{
		ServiceMap:   make(map[string]*corev1.Service),
		PVCMap:       make(map[string]*corev1.PersistentVolumeClaim),
		PodMap:       make(map[string]*corev1.Pod),
		ConfigMapMap: make(map[string]*corev1.ConfigMap),
		SecretMap:    make(map[string]*corev1.Secret),
	}
}

func (c *CoreV1Client) Services(namespace string) k8s.ServiceInterface {
	return &serviceInterface{client: c}
}

func (c *CoreV1Client) PersistentVolumeClaims(namespace string) k8s.PVCInterface {
	return &pvcInterface{client: c}
}

func (c *CoreV1Client) Pods(namespace string) k8s.PodInterface {
	return &podInterface{client: c}
}

func (c *CoreV1Client) ConfigMaps(namespace string) k8s.ConfigMapInterface {
	return &configMapInterface{client: c}
}

func (c *CoreV1Client) Secrets(namespace string) k8s.SecretInterface {
	return &secretInterface{client: c}
}

type serviceInterface struct{ client *CoreV1Client }

func (f *serviceInterface) Get(ctx context.Context, name string, opts metav1.GetOptions) (*corev1.Service, error) {
	f.client.mu.Lock()
	defer f.client.mu.Unlock()
	svc, ok := f.client.ServiceMap[name]
	if !ok {
		return nil, apierrors.NewNotFound(schema.GroupResource{Resource: "services"}, name)
	}
	return svc.DeepCopy(), nil
}

func (f *serviceInterface) Create(ctx context.Context, svc *corev1.Service, opts metav1.CreateOptions) (*corev1.Service, error) {
	f.client.mu.Lock()
	defer f.client.mu.Unlock()
	if _, exists := f.client.ServiceMap[svc.Name]; exists {
		return nil, apierrors.NewAlreadyExists(schema.GroupResource{Resource: "services"}, svc.Name)
	}
	f.client.ServiceMap[svc.Name] = svc.DeepCopy()
	return svc.DeepCopy(), nil
}

func (f *serviceInterface) Delete(ctx context.Context, name string, opts metav1.DeleteOptions) error {
	f.client.mu.Lock()
	defer f.client.mu.Unlock()
	if _, exists := f.client.ServiceMap[name]; !exists {
		return apierrors.NewNotFound(schema.GroupResource{Resource: "services"}, name)
	}
	delete(f.client.ServiceMap, name)
	return nil
}

func (f *serviceInterface) List(ctx context.Context, opts metav1.ListOptions) (*corev1.ServiceList, error) {
	f.client.mu.Lock()
	defer f.client.mu.Unlock()
	items := make([]corev1.Service, 0, len(f.client.ServiceMap))
	for _, svc := range f.client.ServiceMap {
		items = append(items, *svc.DeepCopy())
	}
	return &corev1.ServiceList{Items: items}, nil
}

type pvcInterface struct{ client *CoreV1Client }

func (f *pvcInterface) Get(ctx context.Context, name string, opts metav1.GetOptions) (*corev1.PersistentVolumeClaim, error) {
	f.client.mu.Lock()
	defer f.client.mu.Unlock()
	pvc, ok := f.client.PVCMap[name]
	if !ok {
		return nil, apierrors.NewNotFound(schema.GroupResource{Resource: "persistentvolumeclaims"}, name)
	}
	return pvc.DeepCopy(), nil
}

func (f *pvcInterface) Create(ctx context.Context, pvc *corev1.PersistentVolumeClaim, opts metav1.CreateOptions) (*corev1.PersistentVolumeClaim, error) {
	f.client.mu.Lock()
	defer f.client.mu.Unlock()
	if _, exists := f.client.PVCMap[pvc.Name]; exists {
		return nil, apierrors.NewAlreadyExists(schema.GroupResource{Resource: "persistentvolumeclaims"}, pvc.Name)
	}
	f.client.PVCMap[pvc.Name] = pvc.DeepCopy()
	return pvc.DeepCopy(), nil
}

func (f *pvcInterface) Update(ctx context.Context, pvc *corev1.PersistentVolumeClaim, opts metav1.UpdateOptions) (*corev1.PersistentVolumeClaim, error) {
	f.client.mu.Lock()
	defer f.client.mu.Unlock()
	f.client.PVCMap[pvc.Name] = pvc.DeepCopy()
	return pvc.DeepCopy(), nil
}

func (f *pvcInterface) Delete(ctx context.Context, name string, opts metav1.DeleteOptions) error {
	f.client.mu.Lock()
	defer f.client.mu.Unlock()
	if _, exists := f.client.PVCMap[name]; !exists {
		return apierrors.NewNotFound(schema.GroupResource{Resource: "persistentvolumeclaims"}, name)
	}
	delete(f.client.PVCMap, name)
	return nil
}

type podInterface struct{ client *CoreV1Client }

func (f *podInterface) Get(ctx context.Context, name string, opts metav1.GetOptions) (*corev1.Pod, error) {
	f.client.mu.Lock()
	defer f.client.mu.Unlock()
	pod, ok := f.client.PodMap[name]
	if !ok {
		return nil, apierrors.NewNotFound(schema.GroupResource{Resource: "pods"}, name)
	}
	return pod.DeepCopy(), nil
}

func (f *podInterface) Create(ctx context.Context, pod *corev1.Pod, opts metav1.CreateOptions) (*corev1.Pod, error) {
	f.client.mu.Lock()
	defer f.client.mu.Unlock()
	if _, exists := f.client.PodMap[pod.Name]; exists {
		return nil, apierrors.NewAlreadyExists(schema.GroupResource{Resource: "pods"}, pod.Name)
	}
	f.client.PodMap[pod.Name] = pod.DeepCopy()
	return pod.DeepCopy(), nil
}

func (f *podInterface) Delete(ctx context.Context, name string, opts metav1.DeleteOptions) error {
	f.client.mu.Lock()
	defer f.client.mu.Unlock()
	if _, exists := f.client.PodMap[name]; !exists {
		return apierrors.NewNotFound(schema.GroupResource{Resource: "pods"}, name)
	}
	delete(f.client.PodMap, name)
	return nil
}

func (f *podInterface) List(ctx context.Context, opts metav1.ListOptions) (*corev1.PodList, error) {
	f.client.mu.Lock()
	defer f.client.mu.Unlock()
	items := make([]corev1.Pod, 0, len(f.client.PodMap))
	for _, pod := range f.client.PodMap {
		items = append(items, *pod.DeepCopy())
	}
	return &corev1.PodList{Items: items}, nil
}

type configMapInterface struct{ client *CoreV1Client }

func (f *configMapInterface) Get(ctx context.Context, name string, opts metav1.GetOptions) (*corev1.ConfigMap, error) {
	f.client.mu.Lock()
	defer f.client.mu.Unlock()
	cm, ok := f.client.ConfigMapMap[name]
	if !ok {
		return nil, apierrors.NewNotFound(schema.GroupResource{Resource: "configmaps"}, name)
	}
	return cm.DeepCopy(), nil
}

type secretInterface struct{ client *CoreV1Client }

func (f *secretInterface) Get(ctx context.Context, name string, opts metav1.GetOptions) (*corev1.Secret, error) {
	f.client.mu.Lock()
	defer f.client.mu.Unlock()
	sec, ok := f.client.SecretMap[name]
	if !ok {
		return nil, apierrors.NewNotFound(schema.GroupResource{Resource: "secrets"}, name)
	}
	return sec.DeepCopy(), nil
}

func (f *secretInterface) Delete(ctx context.Context, name string, opts metav1.DeleteOptions) error {
	f.client.mu.Lock()
	defer f.client.mu.Unlock()
	if _, exists := f.client.SecretMap[name]; !exists {
		return apierrors.NewNotFound(schema.GroupResource{Resource: "secrets"}, name)
	}
	delete(f.client.SecretMap, name)
	return nil
}

// CertManagerClient is an in-memory implementation of k8s.CertManagerClient.
type CertManagerClient struct {
	mu      sync.Mutex
	CertMap map[string]*cmv1.Certificate
}

// NewCertManagerClient creates a new in-memory CertManagerClient.
func NewCertManagerClient() *CertManagerClient {
	return &CertManagerClient{CertMap: make(map[string]*cmv1.Certificate)}
}

func (c *CertManagerClient) Certificates(namespace string) k8s.CertificateInterface {
	return &certificateInterface{client: c}
}

type certificateInterface struct{ client *CertManagerClient }

func (f *certificateInterface) Get(ctx context.Context, name string, opts metav1.GetOptions) (*cmv1.Certificate, error) {
	f.client.mu.Lock()
	defer f.client.mu.Unlock()
	cert, ok := f.client.CertMap[name]
	if !ok {
		return nil, apierrors.NewNotFound(schema.GroupResource{Group: "cert-manager.io", Resource: "certificates"}, name)
	}
	return cert.DeepCopy(), nil
}

func (f *certificateInterface) Create(ctx context.Context, cert *cmv1.Certificate, opts metav1.CreateOptions) (*cmv1.Certificate, error) {
	f.client.mu.Lock()
	defer f.client.mu.Unlock()
	if _, exists := f.client.CertMap[cert.Name]; exists {
		return nil, apierrors.NewAlreadyExists(schema.GroupResource{Group: "cert-manager.io", Resource: "certificates"}, cert.Name)
	}
	f.client.CertMap[cert.Name] = cert.DeepCopy()
	return cert.DeepCopy(), nil
}

func (f *certificateInterface) Delete(ctx context.Context, name string, opts metav1.DeleteOptions) error {
	f.client.mu.Lock()
	defer f.client.mu.Unlock()
	if _, exists := f.client.CertMap[name]; !exists {
		return apierrors.NewNotFound(schema.GroupResource{Group: "cert-manager.io", Resource: "certificates"}, name)
	}
	delete(f.client.CertMap, name)
	return nil
}

func (f *certificateInterface) List(ctx context.Context, opts metav1.ListOptions) (*cmv1.CertificateList, error) {
	f.client.mu.Lock()
	defer f.client.mu.Unlock()
	items := make([]cmv1.Certificate, 0, len(f.client.CertMap))
	for _, cert := range f.client.CertMap {
		items = append(items, *cert.DeepCopy())
	}
	return &cmv1.CertificateList{Items: items}, nil
}

// ProfileClient is an in-memory implementation of k8s.ProfileClient.
type ProfileClient struct {
	mu       sync.Mutex
	Profiles []*profilev1.Profile
}

// NewProfileClient creates a new in-memory ProfileClient.
func NewProfileClient(profiles []*profilev1.Profile) *ProfileClient {
	return &ProfileClient{Profiles: profiles}
}

func (f *ProfileClient) List(ctx context.Context, namespace string, opts metav1.ListOptions) (*profilev1.ProfileList, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	items := make([]profilev1.Profile, 0, len(f.Profiles))
	for _, p := range f.Profiles {
		items = append(items, *p.DeepCopy())
	}
	return &profilev1.ProfileList{Items: items}, nil
}

func (f *ProfileClient) Get(ctx context.Context, namespace, name string, opts metav1.GetOptions) (*profilev1.Profile, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	for _, p := range f.Profiles {
		if p.Name == name {
			return p.DeepCopy(), nil
		}
	}
	return nil, apierrors.NewNotFound(schema.GroupResource{Group: "codx.io", Resource: "profiles"}, name)
}

// Watch returns a ProfileWatch that emits the current profiles as Added
// events then closes. It is a simple snapshot watch suitable for tests that
// do not need live updates.
func (f *ProfileClient) Watch(ctx context.Context, namespace string, opts metav1.ListOptions) (k8s.ProfileWatch, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	ch := make(chan k8s.ProfileWatchEvent)
	go func() {
		defer close(ch)
		for _, p := range f.Profiles {
			ch <- k8s.ProfileWatchEvent{Type: watch.Added, Profile: p.DeepCopy()}
		}
	}()
	return &profileWatch{ch: ch}, nil
}

// profileWatch implements k8s.ProfileWatch for the in-memory fake.
type profileWatch struct {
	ch chan k8s.ProfileWatchEvent
}

func (w *profileWatch) Stop() {}

func (w *profileWatch) ResultChan() <-chan k8s.ProfileWatchEvent { return w.ch }
