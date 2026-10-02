package inactivity

import (
	"context"
	"fmt"
	"sync"
	"testing"
	"time"
)

// fakeDelayLister is a mutable DelayLister for tests.
type fakeDelayLister struct {
	mu     sync.Mutex
	delays map[string]time.Duration
	err    error
}

func (f *fakeDelayLister) ListWorkspaceDelays(ctx context.Context) (map[string]time.Duration, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.delays, f.err
}

func (f *fakeDelayLister) set(delays map[string]time.Duration) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.delays = delays
}

func newReconciler() (*Watcher, *LogTailActivity, *RegistrationReconciler, *fakeDelayLister, *mockStopFunc) {
	lister := &fakeDelayLister{}
	activity := NewLogTailActivity()
	stopMock := &mockStopFunc{}
	w := NewWatcher(activity, stopMock.stop, time.Hour, discardLogger())
	r := NewRegistrationReconciler(w, lister, discardLogger())
	return w, activity, r, lister, stopMock
}

func TestReconcileRegistersWorkspaces(t *testing.T) {
	w, _, r, lister, _ := newReconciler()
	lister.set(map[string]time.Duration{
		"john-doe": time.Hour,
		"jane":     0, // zero delay: never stop
	})

	r.Reconcile(context.Background())

	if slugs := w.Slugs(); len(slugs) != 2 {
		t.Fatalf("Slugs count = %d, want 2: %v", len(slugs), slugs)
	}
}

func TestReconcileUnregistersRemovedWorkspaces(t *testing.T) {
	w, _, r, lister, _ := newReconciler()
	lister.set(map[string]time.Duration{"john-doe": time.Hour})
	r.Reconcile(context.Background())
	if len(w.Slugs()) != 1 {
		t.Fatalf("Slugs count = %d, want 1", len(w.Slugs()))
	}

	// The workspace pod disappears.
	lister.set(map[string]time.Duration{})
	r.Reconcile(context.Background())

	if slugs := w.Slugs(); len(slugs) != 0 {
		t.Errorf("Slugs after removal = %v, want empty", slugs)
	}
}

func TestReconcileListErrorKeepsRegistrations(t *testing.T) {
	w, _, r, lister, _ := newReconciler()
	lister.set(map[string]time.Duration{"john-doe": time.Hour})
	r.Reconcile(context.Background())

	lister.mu.Lock()
	lister.err = fmt.Errorf("api unavailable")
	lister.mu.Unlock()
	r.Reconcile(context.Background())

	if slugs := w.Slugs(); len(slugs) != 1 || slugs[0] != "john-doe" {
		t.Errorf("Slugs after list error = %v, want [john-doe]", slugs)
	}
}

func TestReconcileAppliesProfileDelay(t *testing.T) {
	w, activity, r, lister, stopMock := newReconciler()

	// Last activity 2 hours ago, workspace delay 1 hour: checkAll must stop it.
	activity.RecordActivity("john-doe", time.Now().Add(-2*time.Hour))
	lister.set(map[string]time.Duration{"john-doe": time.Hour})
	r.Reconcile(context.Background())
	w.checkAll(context.Background())

	if len(stopMock.stopped) != 1 || stopMock.stopped[0] != "john-doe" {
		t.Fatalf("stopped = %v, want [john-doe]", stopMock.stopped)
	}
}

func TestReconcileZeroDelayNeverStops(t *testing.T) {
	w, activity, r, lister, stopMock := newReconciler()

	// Zero delay (profile with InactivityStopDelaySeconds=0) means never stop.
	activity.RecordActivity("john-doe", time.Now().Add(-24*time.Hour))
	lister.set(map[string]time.Duration{"john-doe": 0})
	r.Reconcile(context.Background())
	w.checkAll(context.Background())

	if len(stopMock.stopped) != 0 {
		t.Errorf("stopped = %v, want none for zero delay", stopMock.stopped)
	}
}
