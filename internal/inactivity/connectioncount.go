package inactivity

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"sync"
	"time"

	"github.com/go-logr/logr"
)

// Defaults for the Envoy admin interface of the workspace sidecar. They must
// stay in sync with charts/codx/templates/envoy-configmap.yaml (admin port
// 9901, http connection manager stat_prefix "codx_workspace").
const (
	// DefaultEnvoyAdminPort is the port the workspace Envoy sidecar binds
	// its admin interface to.
	DefaultEnvoyAdminPort = 9901

	// DefaultConnectionStatName is the Envoy stat polled by the
	// connection-count source: the number of active downstream HTTPS
	// connections to the workspace.
	DefaultConnectionStatName = "http.codx_workspace.downstream_cx_active"

	// DefaultConnectionTimeout is the per-pod timeout when polling the
	// Envoy admin interface.
	DefaultConnectionTimeout = 5 * time.Second
)

// WorkspacePod identifies a running workspace pod that can be reached on its
// Envoy admin interface.
type WorkspacePod struct {
	Slug  string
	PodIP string
}

// WorkspacePodLister lists the running workspace pods. It is implemented by
// the caller (see cmd/codx) so this package stays independent of Kubernetes.
type WorkspacePodLister interface {
	ListWorkspacePods(ctx context.Context) ([]WorkspacePod, error)
}

// ConnectionCountOptions customizes a ConnectionCountActivity.
type ConnectionCountOptions struct {
	// AdminPort is the Envoy admin interface port. Defaults to
	// DefaultEnvoyAdminPort.
	AdminPort int

	// StatName is the Envoy stat polled for the active connection count.
	// Defaults to DefaultConnectionStatName.
	StatName string

	// Timeout is the per-pod HTTP timeout. Defaults to
	// DefaultConnectionTimeout.
	Timeout time.Duration
}

func (o *ConnectionCountOptions) withDefaults() *ConnectionCountOptions {
	opts := *o
	if opts.AdminPort == 0 {
		opts.AdminPort = DefaultEnvoyAdminPort
	}
	if opts.StatName == "" {
		opts.StatName = DefaultConnectionStatName
	}
	if opts.Timeout == 0 {
		opts.Timeout = DefaultConnectionTimeout
	}
	return &opts
}

// ConnectionCountActivity implements ActivitySource by polling the Envoy admin
// /stats endpoint of every workspace pod. When the watched stat (the number of
// active downstream HTTPS connections) is greater than zero, the current time
// is recorded as the workspace's last activity.
//
// The poll loop must be started explicitly with Run; without it the source
// never records activity.
type ConnectionCountActivity struct {
	mu        sync.Mutex
	records   map[string]time.Time
	lister    WorkspacePodLister
	client    *http.Client
	log       logr.Logger
	adminPort int
	statName  string
}

// NewConnectionCountActivity creates a new ConnectionCountActivity. opts may
// be nil to use the defaults.
func NewConnectionCountActivity(lister WorkspacePodLister, log logr.Logger, opts *ConnectionCountOptions) *ConnectionCountActivity {
	if opts == nil {
		opts = &ConnectionCountOptions{}
	}
	o := opts.withDefaults()
	return &ConnectionCountActivity{
		records:   make(map[string]time.Time),
		lister:    lister,
		client:    &http.Client{Timeout: o.Timeout},
		log:       log,
		adminPort: o.AdminPort,
		statName:  o.StatName,
	}
}

// Run starts the poll loop. It blocks until the context is cancelled.
func (a *ConnectionCountActivity) Run(ctx context.Context, interval time.Duration) {
	ticker := time.NewTicker(interval)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			a.Poll(ctx)
		}
	}
}

// Poll lists the workspace pods once and records activity for every workspace
// whose Envoy reports active downstream connections.
func (a *ConnectionCountActivity) Poll(ctx context.Context) {
	pods, err := a.lister.ListWorkspacePods(ctx)
	if err != nil {
		a.log.Error(err, "failed to list workspace pods")
		return
	}

	seen := make(map[string]bool, len(pods))
	for _, pod := range pods {
		seen[pod.Slug] = true
		count, err := a.activeConnections(ctx, pod.PodIP)
		if err != nil {
			// A single unreachable pod must not block the others.
			a.log.V(1).Error(err, "failed to query envoy stats", "slug", pod.Slug)
			continue
		}
		if count > 0 {
			a.RecordActivity(pod.Slug, time.Now())
		}
	}

	// Forget workspaces whose pod no longer exists.
	for _, slug := range a.TrackedSlugs() {
		if !seen[slug] {
			a.Forget(slug)
		}
	}
}

// activeConnections queries the Envoy admin /stats endpoint of one workspace
// pod and returns the value of the watched stat.
func (a *ConnectionCountActivity) activeConnections(ctx context.Context, podIP string) (int64, error) {
	u := fmt.Sprintf("http://%s:%d/stats?format=json&filter=%s",
		podIP, a.adminPort, url.QueryEscape(a.statName))

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, u, nil)
	if err != nil {
		return 0, fmt.Errorf("build stats request: %w", err)
	}

	resp, err := a.client.Do(req)
	if err != nil {
		return 0, fmt.Errorf("query envoy stats: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return 0, fmt.Errorf("envoy stats endpoint returned %s", resp.Status)
	}

	var payload struct {
		Stats []struct {
			Name  string `json:"name"`
			Value int64  `json:"value"`
		} `json:"stats"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&payload); err != nil {
		return 0, fmt.Errorf("decode envoy stats: %w", err)
	}

	for _, stat := range payload.Stats {
		if stat.Name == a.statName {
			return stat.Value, nil
		}
	}
	return 0, fmt.Errorf("stat %q not found in envoy stats", a.statName)
}

// RecordActivity updates the last activity timestamp for the given slug.
func (a *ConnectionCountActivity) RecordActivity(slug string, ts time.Time) {
	a.mu.Lock()
	defer a.mu.Unlock()
	current, ok := a.records[slug]
	if !ok || ts.After(current) {
		a.records[slug] = ts
	}
}

// LastActivity returns the last activity timestamp for the given slug,
// or the zero time if no activity has been recorded.
func (a *ConnectionCountActivity) LastActivity(slug string) time.Time {
	a.mu.Lock()
	defer a.mu.Unlock()
	return a.records[slug]
}

// Forget removes the activity record for a slug.
func (a *ConnectionCountActivity) Forget(slug string) {
	a.mu.Lock()
	defer a.mu.Unlock()
	delete(a.records, slug)
}

// TrackedSlugs returns the list of slugs currently being tracked.
func (a *ConnectionCountActivity) TrackedSlugs() []string {
	a.mu.Lock()
	defer a.mu.Unlock()
	slugs := make([]string, 0, len(a.records))
	for slug := range a.records {
		slugs = append(slugs, slug)
	}
	return slugs
}
