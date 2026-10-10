// Package leader implements Lease-based Kubernetes leader election so that
// only one CodX replica runs the inactivity monitor at a time.
package leader

import (
	"context"
	"fmt"
	"os"
	"time"

	"github.com/go-logr/logr"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/tools/leaderelection"
	"k8s.io/client-go/tools/leaderelection/resourcelock"
	"k8s.io/client-go/tools/record"
)

// Election coordinates leader election between the CodX replicas. The
// winning replica runs the inactivity activity source, watcher and
// registration reconciler; the other replicas stand by.
type Election struct {
	leaseNamespace  string
	leaseName       string
	identity        string
	leaseDuration   time.Duration
	renewDeadline   time.Duration
	retryPeriod     time.Duration
	releaseOnCancel bool
	log             logr.Logger
}

// NewElection builds an Election. The identity defaults to the pod name and
// UID (unique per replica, and stable across container restarts of the same
// pod).
func NewElection(leaseNamespace, leaseName, identity string, leaseDuration, renewDeadline, retryPeriod time.Duration, releaseOnCancel bool, log logr.Logger) *Election {
	return &Election{
		leaseNamespace:  leaseNamespace,
		leaseName:       leaseName,
		identity:        identity,
		leaseDuration:   leaseDuration,
		renewDeadline:   renewDeadline,
		retryPeriod:     retryPeriod,
		releaseOnCancel: releaseOnCancel,
		log:             log,
	}
}

// PodIdentity returns a leader election identity unique to this replica,
// built from the pod name and UID exposed by the downward API. It falls back
// to the hostname when the pod environment variables are not set (for
// example outside a cluster).
func PodIdentity() string {
	podName := os.Getenv("POD_NAME")
	podUID := os.Getenv("POD_UID")
	if podName == "" {
		podName = os.Getenv("HOSTNAME")
	}
	if podUID == "" {
		return podName
	}
	return podName + "_" + podUID
}

// Run blocks running the leader election loop until ctx is cancelled or the
// lease cannot be renewed. OnStartedLeading is invoked once this replica
// acquires the lease, with a context cancelled as soon as leadership is
// lost; the caller must start the leader-only loops from it and stop them
// when that context is cancelled. OnStoppedLeading is invoked after
// leadership is lost (or on shutdown), always after OnStartedLeading's
// context has been cancelled.
func (e *Election) Run(ctx context.Context, coreClient kubernetes.Interface, recorder record.EventRecorder, onStartedLeading func(ctx context.Context), onStoppedLeading func()) error {
	lock := &resourcelock.LeaseLock{
		LeaseMeta: metav1.ObjectMeta{
			Namespace: e.leaseNamespace,
			Name:      e.leaseName,
		},
		Client:     coreClient.CoordinationV1(),
		LockConfig: resourcelock.ResourceLockConfig{Identity: e.identity, EventRecorder: recorder},
	}

	le, err := leaderelection.NewLeaderElector(leaderelection.LeaderElectionConfig{
		Lock:            lock,
		LeaseDuration:   e.leaseDuration,
		RenewDeadline:   e.renewDeadline,
		RetryPeriod:     e.retryPeriod,
		ReleaseOnCancel: e.releaseOnCancel,
		Name:            e.leaseName,
		Callbacks: leaderelection.LeaderCallbacks{
			OnStartedLeading: func(ctx context.Context) {
				e.log.Info("acquired inactivity leader lease",
					"lease", fmt.Sprintf("%s/%s", e.leaseNamespace, e.leaseName),
					"identity", e.identity,
				)
				onStartedLeading(ctx)
			},
			OnStoppedLeading: func() {
				e.log.Info("lost or released inactivity leader lease",
					"lease", fmt.Sprintf("%s/%s", e.leaseNamespace, e.leaseName),
					"identity", e.identity,
				)
				onStoppedLeading()
			},
			OnNewLeader: func(identity string) {
				if identity != e.identity {
					e.log.Info("another replica leads the inactivity monitor", "leader", identity)
				}
			},
		},
	})
	if err != nil {
		return fmt.Errorf("build leader elector: %w", err)
	}

	e.log.Info("starting inactivity leader election",
		"lease", fmt.Sprintf("%s/%s", e.leaseNamespace, e.leaseName),
		"identity", e.identity,
		"leaseDuration", e.leaseDuration,
		"renewDeadline", e.renewDeadline,
		"retryPeriod", e.retryPeriod,
	)
	le.Run(ctx)
	return nil
}

// EnsureLeaseNamespaceDefault returns leaseNamespace when set, and the
// namespace of the pod CodX runs in otherwise (from the downward API).
func EnsureLeaseNamespaceDefault(leaseNamespace string) string {
	if leaseNamespace != "" {
		return leaseNamespace
	}
	if ns := os.Getenv("POD_NAMESPACE"); ns != "" {
		return ns
	}
	return "default"
}
