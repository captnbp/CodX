package inactivity

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strconv"
	"sync"
	"testing"
	"time"
)

// fakePodLister is a mutable WorkspacePodLister for tests.
type fakePodLister struct {
	mu   sync.Mutex
	pods []WorkspacePod
	err  error
}

func (f *fakePodLister) ListWorkspacePods(ctx context.Context) ([]WorkspacePod, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.pods, f.err
}

func (f *fakePodLister) set(pods []WorkspacePod) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.pods = pods
}

// envoyStatsServer returns an httptest server serving the Envoy admin
// /stats JSON payload for the given active connection count.
func envoyStatsServer(t *testing.T, count int64) *httptest.Server {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/stats" {
			http.NotFound(w, r)
			return
		}
		fmt.Fprintf(w, `{"stats":[`+
			`{"name":"http.codx_workspace.downstream_cx_active","value":%d,"type":"GAUGE"},`+
			`{"name":"http.codx_workspace.downstream_rq_total","value":42,"type":"COUNTER"}`+
			`]}`, count)
	}))
	t.Cleanup(srv.Close)
	return srv
}

// testEndpoint returns the PodIP and Envoy admin port matching an httptest
// server, so tests can poll it through ConnectionCountActivity.
func testEndpoint(t *testing.T, srvURL string) (string, int) {
	t.Helper()
	u, err := url.Parse(srvURL)
	if err != nil {
		t.Fatalf("failed to parse url %q: %v", srvURL, err)
	}
	port, err := strconv.Atoi(u.Port())
	if err != nil {
		t.Fatalf("failed to parse port of %q: %v", srvURL, err)
	}
	return u.Hostname(), port
}

// newTestActivity builds a ConnectionCountActivity polling the given test
// server for a single workspace slug.
func newTestActivity(t *testing.T, srvURL, slug string) (*ConnectionCountActivity, *fakePodLister) {
	t.Helper()
	ip, port := testEndpoint(t, srvURL)
	lister := &fakePodLister{pods: []WorkspacePod{{Slug: slug, PodIP: ip}}}
	return NewConnectionCountActivity(lister, discardLogger(), &ConnectionCountOptions{AdminPort: port}), lister
}

func TestConnectionCountRecordsActivityWhenConnectionsActive(t *testing.T) {
	srv := envoyStatsServer(t, 11)
	a, _ := newTestActivity(t, srv.URL, "john-doe")

	a.Poll(context.Background())

	ts := a.LastActivity("john-doe")
	if ts.IsZero() {
		t.Fatal("LastActivity is zero, want activity recorded for active connections")
	}
	if since := time.Since(ts); since > 5*time.Second {
		t.Errorf("LastActivity = %v, want a recent timestamp", ts)
	}
}

func TestConnectionCountNoActivityWhenNoConnections(t *testing.T) {
	srv := envoyStatsServer(t, 0)
	a, _ := newTestActivity(t, srv.URL, "john-doe")

	a.Poll(context.Background())

	if ts := a.LastActivity("john-doe"); !ts.IsZero() {
		t.Errorf("LastActivity = %v, want zero for no active connections", ts)
	}
}

func TestConnectionCountNoActivityAtThreshold(t *testing.T) {
	srv := envoyStatsServer(t, DefaultMinActiveConnections)
	a, _ := newTestActivity(t, srv.URL, "john-doe")

	a.Poll(context.Background())

	if ts := a.LastActivity("john-doe"); !ts.IsZero() {
		t.Errorf("LastActivity = %v, want zero for %d active connections", ts, DefaultMinActiveConnections)
	}
}

func TestConnectionCountUnreachablePodDoesNotBlockOthers(t *testing.T) {
	srv := envoyStatsServer(t, 11)
	ip, port := testEndpoint(t, srv.URL)
	lister := &fakePodLister{pods: []WorkspacePod{
		{Slug: "gone", PodIP: "127.0.0.2"}, // loopback too, but the admin port is closed
		{Slug: "john-doe", PodIP: ip},
	}}

	a := NewConnectionCountActivity(lister, discardLogger(), &ConnectionCountOptions{AdminPort: port})
	a.Poll(context.Background())

	if ts := a.LastActivity("john-doe"); ts.IsZero() {
		t.Error("LastActivity for john-doe is zero, want activity recorded despite unreachable pod")
	}
	if ts := a.LastActivity("gone"); !ts.IsZero() {
		t.Errorf("LastActivity for gone = %v, want zero", ts)
	}
}

func TestConnectionCountForgetsRemovedPods(t *testing.T) {
	srv := envoyStatsServer(t, 11)
	a, lister := newTestActivity(t, srv.URL, "john-doe")

	a.Poll(context.Background())
	if ts := a.LastActivity("john-doe"); ts.IsZero() {
		t.Fatal("expected activity recorded on first poll")
	}

	// The pod disappears: activity must be forgotten.
	lister.set(nil)
	a.Poll(context.Background())

	if ts := a.LastActivity("john-doe"); !ts.IsZero() {
		t.Errorf("LastActivity after pod removal = %v, want zero", ts)
	}
}

func TestConnectionCountListErrorKeepsRecords(t *testing.T) {
	srv := envoyStatsServer(t, 11)
	a, lister := newTestActivity(t, srv.URL, "john-doe")

	a.Poll(context.Background())

	// A listing error must not wipe the recorded activity.
	lister.mu.Lock()
	lister.err = fmt.Errorf("api unavailable")
	lister.mu.Unlock()
	a.Poll(context.Background())

	if ts := a.LastActivity("john-doe"); ts.IsZero() {
		t.Error("LastActivity is zero after list error, want the previous record kept")
	}
}

func TestConnectionCountCustomStatName(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		fmt.Fprintf(w, `{"stats":[{"name":"listener.0.0.0.0_9443.downstream_cx_active","value":11,"type":"GAUGE"}]}`)
	}))
	t.Cleanup(srv.Close)

	ip, port := testEndpoint(t, srv.URL)
	lister := &fakePodLister{pods: []WorkspacePod{{Slug: "john-doe", PodIP: ip}}}
	a := NewConnectionCountActivity(lister, discardLogger(), &ConnectionCountOptions{
		AdminPort: port,
		StatName:  "listener.0.0.0.0_9443.downstream_cx_active",
	})
	a.Poll(context.Background())

	if ts := a.LastActivity("john-doe"); ts.IsZero() {
		t.Error("LastActivity is zero, want activity recorded for custom stat name")
	}
}

func TestConnectionCountDefaultOptions(t *testing.T) {
	lister := &fakePodLister{}
	a := NewConnectionCountActivity(lister, discardLogger(), nil)

	if a.adminPort != DefaultEnvoyAdminPort {
		t.Errorf("adminPort = %d, want %d", a.adminPort, DefaultEnvoyAdminPort)
	}
	if a.statName != DefaultConnectionStatName {
		t.Errorf("statName = %q, want %q", a.statName, DefaultConnectionStatName)
	}
	if a.client.Timeout != DefaultConnectionTimeout {
		t.Errorf("timeout = %v, want %v", a.client.Timeout, DefaultConnectionTimeout)
	}
}

// fakeActivityStore is an in-memory ActivityStore for tests.
type fakeActivityStore struct {
	mu      sync.Mutex
	records map[string]time.Time
	err     error
}

func newFakeActivityStore() *fakeActivityStore {
	return &fakeActivityStore{records: make(map[string]time.Time)}
}

func (f *fakeActivityStore) Save(ctx context.Context, slug string, ts time.Time) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.err != nil {
		return f.err
	}
	f.records[slug] = ts
	return nil
}

func (f *fakeActivityStore) Delete(ctx context.Context, slug string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.err != nil {
		return f.err
	}
	delete(f.records, slug)
	return nil
}

func (f *fakeActivityStore) Load(ctx context.Context) (map[string]time.Time, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.err != nil {
		return nil, f.err
	}
	out := make(map[string]time.Time, len(f.records))
	for slug, ts := range f.records {
		out[slug] = ts
	}
	return out, nil
}

func TestConnectionCountPersistsActivityRecords(t *testing.T) {
	srv := envoyStatsServer(t, 11)
	a, lister := newTestActivity(t, srv.URL, "john-doe")
	store := newFakeActivityStore()
	a.WithStore(store)

	before := time.Now().Truncate(time.Second)
	a.Poll(context.Background())

	saved, ok := store.records["john-doe"]
	if !ok {
		t.Fatal("activity record not persisted to the store")
	}
	if saved.Before(before) || time.Since(saved) > time.Minute {
		t.Errorf("persisted record = %v, want a recent timestamp", saved)
	}

	// A workspace whose pod disappears is forgotten from the store too.
	lister.set(nil)
	a.Poll(context.Background())
	if _, ok := store.records["john-doe"]; ok {
		t.Error("activity record should be deleted from the store when the pod is gone")
	}
}

func TestConnectionCountRestoreResumesTracking(t *testing.T) {
	srv := envoyStatsServer(t, 11)
	a, _ := newTestActivity(t, srv.URL, "john-doe")
	store := newFakeActivityStore()
	a.WithStore(store)

	// The previous leader recorded activity 10 minutes ago.
	previous := time.Now().Add(-10 * time.Minute)
	store.records["john-doe"] = previous
	// The new leader also has a stale in-memory record that the restore
	// must replace.
	a.RecordActivity("stale", time.Now())

	a.Restore(context.Background())

	if ts := a.LastActivity("john-doe"); !ts.Equal(previous) {
		t.Errorf("restored last activity = %v, want %v", ts, previous)
	}
	if ts := a.LastActivity("stale"); !ts.IsZero() {
		t.Errorf("restore should replace the in-memory records, stale record = %v", ts)
	}
}

func TestConnectionCountRestoreFromEmptyStoreStartsFromZero(t *testing.T) {
	srv := envoyStatsServer(t, 11)
	a, _ := newTestActivity(t, srv.URL, "john-doe")
	a.WithStore(newFakeActivityStore())

	// Empty store (Valkey restarted and lost its data).
	a.Restore(context.Background())
	if ts := a.LastActivity("john-doe"); !ts.IsZero() {
		t.Errorf("last activity = %v, want zero after an empty restore", ts)
	}

	// A failing store also restarts the tracking from zero.
	store := newFakeActivityStore()
	store.err = fmt.Errorf("connection refused")
	a.WithStore(store)
	a.RecordActivity("john-doe", time.Now())
	a.Restore(context.Background())
	if ts := a.LastActivity("john-doe"); !ts.IsZero() {
		t.Errorf("last activity = %v, want zero after a failed restore", ts)
	}
}

func TestConnectionCountRestoreWithoutStore(t *testing.T) {
	// No store attached (unit tests, custom deployments): Restore is a
	// no-op and must not clear the in-memory records.
	srv := envoyStatsServer(t, 11)
	a, _ := newTestActivity(t, srv.URL, "john-doe")
	ts := time.Now()
	a.RecordActivity("john-doe", ts)

	a.Restore(context.Background())

	if got := a.LastActivity("john-doe"); !got.Equal(ts) {
		t.Errorf("last activity = %v, want %v (no store: no-op)", got, ts)
	}
}
