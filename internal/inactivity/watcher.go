// Package inactivity monitors workspace HTTPS activity and stops pods
// after a configurable period of inactivity.
//
// The watcher supports two signal sources (configurable in the ConfigMap):
//   - "log-tail" (default): CodX tails the Envoy sidecar access log lines
//     to detect the last HTTPS activity timestamp.
//   - "connection-count": CodX periodically checks the number of active
//     HTTPS connections to the workspace.
//
// In both cases, when no activity is detected for longer than the profile's
// inactivityStopDelaySeconds, the workspace pod is stopped.
package inactivity

import (
	"context"
	"sync"
	"time"

	"github.com/go-logr/logr"
)

// ActivitySource is the interface for detecting workspace activity.
type ActivitySource interface {
	// LastActivity returns the timestamp of the last HTTPS activity for the
	// given workspace slug, or the zero time if no activity has been recorded.
	LastActivity(slug string) time.Time
}

// StopFunc is called to stop a workspace pod.
type StopFunc func(ctx context.Context, slug string) error

// Watcher monitors workspace activity and stops idle pods.
type Watcher struct {
	mu           sync.Mutex
	source       ActivitySource
	stopFunc     StopFunc
	checkInterval time.Duration
	log          logr.Logger

	// delays maps slug to the inactivity stop delay for that workspace.
	delays map[string]time.Duration

	// lastCheck maps slug to the last time we checked/acted on this workspace.
	lastCheck map[string]time.Time

	// stopped tracks which workspaces have already been stopped (to avoid
	// repeated stop calls).
	stopped map[string]bool
}

// NewWatcher creates a new inactivity Watcher.
func NewWatcher(source ActivitySource, stopFunc StopFunc, checkInterval time.Duration, log logr.Logger) *Watcher {
	return &Watcher{
		source:        source,
		stopFunc:      stopFunc,
		checkInterval: checkInterval,
		log:           log,
		delays:        make(map[string]time.Duration),
		lastCheck:     make(map[string]time.Time),
		stopped:       make(map[string]bool),
	}
}

// Register adds a workspace to the watcher with the given inactivity delay.
// If delay is zero, the workspace is never stopped.
func (w *Watcher) Register(slug string, delay time.Duration) {
	w.mu.Lock()
	defer w.mu.Unlock()
	w.delays[slug] = delay
	w.stopped[slug] = false
}

// Unregister removes a workspace from the watcher.
func (w *Watcher) Unregister(slug string) {
	w.mu.Lock()
	defer w.mu.Unlock()
	delete(w.delays, slug)
	delete(w.lastCheck, slug)
	delete(w.stopped, slug)
}

// Run starts the periodic check loop. It blocks until the context is cancelled.
func (w *Watcher) Run(ctx context.Context) {
	ticker := time.NewTicker(w.checkInterval)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			w.checkAll(ctx)
		}
	}
}

// checkAll iterates over all registered workspaces and stops those that have
// been inactive for longer than their configured delay.
func (w *Watcher) checkAll(ctx context.Context) {
	w.mu.Lock()
	slugs := make([]string, 0, len(w.delays))
	for slug := range w.delays {
		slugs = append(slugs, slug)
	}
	w.mu.Unlock()

	for _, slug := range slugs {
		w.mu.Lock()
		delay := w.delays[slug]
		alreadyStopped := w.stopped[slug]
		w.mu.Unlock()

		// Skip if delay is zero (never stop) or already stopped.
		if delay == 0 || alreadyStopped {
			continue
		}

		lastActivity := w.source.LastActivity(slug)
		if lastActivity.IsZero() {
			// No activity recorded yet — don't stop (workspace might just
			// have started).
			continue
		}

		idleDuration := time.Since(lastActivity)
		if idleDuration >= delay {
			w.log.Info("stopping inactive workspace", "slug", slug, "idle", idleDuration, "delay", delay)
			if err := w.stopFunc(ctx, slug); err != nil {
				w.log.Error(err, "failed to stop inactive workspace", "slug", slug)
				continue
			}
			w.mu.Lock()
			w.stopped[slug] = true
			w.mu.Unlock()
		}
	}
}

// IsStopped returns true if the watcher has already stopped the workspace
// due to inactivity.
func (w *Watcher) IsStopped(slug string) bool {
	w.mu.Lock()
	defer w.mu.Unlock()
	return w.stopped[slug]
}

// Reset clears the stopped flag for a workspace (e.g. when it's restarted).
func (w *Watcher) Reset(slug string) {
	w.mu.Lock()
	defer w.mu.Unlock()
	w.stopped[slug] = false
}
