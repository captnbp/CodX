package inactivity

import (
	"sync"
	"time"
)

// LogTailActivity tracks the last HTTPS activity timestamp per workspace
// by processing Envoy access log lines.
//
// In production, CodX tails the Envoy sidecar access log (via kubectl logs
// or a shared log volume). Each line that matches an HTTPS request updates
// the last activity timestamp for the corresponding workspace slug.
type LogTailActivity struct {
	mu      sync.Mutex
	records map[string]time.Time
}

// NewLogTailActivity creates a new LogTailActivity.
func NewLogTailActivity() *LogTailActivity {
	return &LogTailActivity{
		records: make(map[string]time.Time),
	}
}

// RecordActivity updates the last activity timestamp for the given slug.
func (a *LogTailActivity) RecordActivity(slug string, ts time.Time) {
	a.mu.Lock()
	defer a.mu.Unlock()
	current, ok := a.records[slug]
	if !ok || ts.After(current) {
		a.records[slug] = ts
	}
}

// LastActivity returns the last activity timestamp for the given slug,
// or the zero time if no activity has been recorded.
func (a *LogTailActivity) LastActivity(slug string) time.Time {
	a.mu.Lock()
	defer a.mu.Unlock()
	return a.records[slug]
}

// Forget removes the activity record for a slug.
func (a *LogTailActivity) Forget(slug string) {
	a.mu.Lock()
	defer a.mu.Unlock()
	delete(a.records, slug)
}

// TrackedSlugs returns the list of slugs currently being tracked.
func (a *LogTailActivity) TrackedSlugs() []string {
	a.mu.Lock()
	defer a.mu.Unlock()
	slugs := make([]string, 0, len(a.records))
	for slug := range a.records {
		slugs = append(slugs, slug)
	}
	return slugs
}
