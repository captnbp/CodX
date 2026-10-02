package inactivity

import (
	"context"
	"time"

	"github.com/go-logr/logr"
)

// DelayLister lists the workspaces that exist, mapped to their configured
// inactivity stop delay. A zero delay means the workspace is never stopped.
// It is implemented by the caller (see cmd/codx) so this package stays
// independent of Kubernetes.
type DelayLister interface {
	ListWorkspaceDelays(ctx context.Context) (map[string]time.Duration, error)
}

// RegistrationReconciler keeps a Watcher's registrations in sync with the
// workspaces that actually exist: new workspaces are registered with their
// delay, removed workspaces are unregistered, and updated delays (e.g. after
// a Profile CR change) are refreshed.
type RegistrationReconciler struct {
	watcher *Watcher
	lister  DelayLister
	log     logr.Logger
}

// NewRegistrationReconciler creates a new RegistrationReconciler.
func NewRegistrationReconciler(watcher *Watcher, lister DelayLister, log logr.Logger) *RegistrationReconciler {
	return &RegistrationReconciler{
		watcher: watcher,
		lister:  lister,
		log:     log,
	}
}

// Run starts the reconcile loop. It blocks until the context is cancelled.
func (r *RegistrationReconciler) Run(ctx context.Context, interval time.Duration) {
	ticker := time.NewTicker(interval)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			r.Reconcile(ctx)
		}
	}
}

// Reconcile registers, updates, and unregisters the watcher's workspaces
// based on the latest listing.
func (r *RegistrationReconciler) Reconcile(ctx context.Context) {
	delays, err := r.lister.ListWorkspaceDelays(ctx)
	if err != nil {
		// Keep the current registrations and retry on the next tick.
		r.log.Error(err, "failed to list workspace delays")
		return
	}

	for slug, delay := range delays {
		r.watcher.Register(slug, delay)
	}
	for _, slug := range r.watcher.Slugs() {
		if _, ok := delays[slug]; !ok {
			r.watcher.Unregister(slug)
		}
	}
}
