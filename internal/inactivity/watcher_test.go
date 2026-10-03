package inactivity

import (
	"context"
	"sync"
	"testing"
	"time"

	"github.com/go-logr/logr"
)

// stubActivity is a simple in-memory ActivitySource for tests.
type stubActivity struct {
	mu      sync.Mutex
	records map[string]time.Time
}

func newStubActivity() *stubActivity {
	return &stubActivity{records: make(map[string]time.Time)}
}

func (a *stubActivity) RecordActivity(slug string, ts time.Time) {
	a.mu.Lock()
	defer a.mu.Unlock()
	current, ok := a.records[slug]
	if !ok || ts.After(current) {
		a.records[slug] = ts
	}
}

func (a *stubActivity) LastActivity(slug string) time.Time {
	a.mu.Lock()
	defer a.mu.Unlock()
	return a.records[slug]
}

// mockStopFunc tracks which slugs were stopped and in what order.
type mockStopFunc struct {
	mu      sync.Mutex
	stopped []string
	err     error
}

func (m *mockStopFunc) stop(ctx context.Context, slug string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.stopped = append(m.stopped, slug)
	return m.err
}

func TestWatcherStopsInactiveWorkspace(t *testing.T) {
	activity := newStubActivity()
	// Record activity 2 hours ago.
	activity.RecordActivity("john-doe", time.Now().Add(-2*time.Hour))

	stopMock := &mockStopFunc{}
	w := NewWatcher(activity, stopMock.stop, 100*time.Millisecond, discardLogger())
	w.Register("john-doe", 1*time.Hour) // 1 hour delay

	ctx, cancel := context.WithTimeout(context.Background(), 500*time.Millisecond)
	defer cancel()
	w.Run(ctx)

	if len(stopMock.stopped) != 1 {
		t.Fatalf("expected 1 stop, got %d: %v", len(stopMock.stopped), stopMock.stopped)
	}
	if stopMock.stopped[0] != "john-doe" {
		t.Errorf("stopped slug = %q, want john-doe", stopMock.stopped[0])
	}
}

func TestWatcherDoesNotStopActiveWorkspace(t *testing.T) {
	activity := newStubActivity()
	// Record activity just now.
	activity.RecordActivity("john-doe", time.Now())

	stopMock := &mockStopFunc{}
	w := NewWatcher(activity, stopMock.stop, 100*time.Millisecond, discardLogger())
	w.Register("john-doe", 1*time.Hour)

	ctx, cancel := context.WithTimeout(context.Background(), 300*time.Millisecond)
	defer cancel()
	w.Run(ctx)

	if len(stopMock.stopped) != 0 {
		t.Errorf("should not stop active workspace, stopped: %v", stopMock.stopped)
	}
}

func TestWatcherDoesNotStopZeroDelay(t *testing.T) {
	activity := newStubActivity()
	activity.RecordActivity("john-doe", time.Now().Add(-24*time.Hour))

	stopMock := &mockStopFunc{}
	w := NewWatcher(activity, stopMock.stop, 100*time.Millisecond, discardLogger())
	w.Register("john-doe", 0) // zero = never stop

	ctx, cancel := context.WithTimeout(context.Background(), 300*time.Millisecond)
	defer cancel()
	w.Run(ctx)

	if len(stopMock.stopped) != 0 {
		t.Errorf("should not stop workspace with zero delay, stopped: %v", stopMock.stopped)
	}
}

func TestWatcherDoesNotStopNoActivity(t *testing.T) {
	activity := newStubActivity()
	// No activity recorded for this slug.

	stopMock := &mockStopFunc{}
	w := NewWatcher(activity, stopMock.stop, 100*time.Millisecond, discardLogger())
	w.Register("john-doe", 1*time.Hour)

	ctx, cancel := context.WithTimeout(context.Background(), 300*time.Millisecond)
	defer cancel()
	w.Run(ctx)

	if len(stopMock.stopped) != 0 {
		t.Errorf("should not stop workspace with no activity, stopped: %v", stopMock.stopped)
	}
}

func TestWatcherStopsOnlyOnce(t *testing.T) {
	activity := newStubActivity()
	activity.RecordActivity("john-doe", time.Now().Add(-2*time.Hour))

	stopMock := &mockStopFunc{}
	w := NewWatcher(activity, stopMock.stop, 50*time.Millisecond, discardLogger())
	w.Register("john-doe", 1*time.Hour)

	ctx, cancel := context.WithTimeout(context.Background(), 500*time.Millisecond)
	defer cancel()
	w.Run(ctx)

	if len(stopMock.stopped) != 1 {
		t.Errorf("expected exactly 1 stop, got %d: %v", len(stopMock.stopped), stopMock.stopped)
	}
}

func TestWatcherUnregisterRemovesWorkspace(t *testing.T) {
	activity := newStubActivity()
	activity.RecordActivity("john-doe", time.Now().Add(-2*time.Hour))

	stopMock := &mockStopFunc{}
	w := NewWatcher(activity, stopMock.stop, 100*time.Millisecond, discardLogger())
	w.Register("john-doe", 1*time.Hour)
	w.Unregister("john-doe")

	ctx, cancel := context.WithTimeout(context.Background(), 300*time.Millisecond)
	defer cancel()
	w.Run(ctx)

	if len(stopMock.stopped) != 0 {
		t.Errorf("should not stop unregistered workspace, stopped: %v", stopMock.stopped)
	}
}

func TestWatcherResetAllowsRestart(t *testing.T) {
	activity := newStubActivity()
	activity.RecordActivity("john-doe", time.Now().Add(-2*time.Hour))

	stopMock := &mockStopFunc{}
	w := NewWatcher(activity, stopMock.stop, 50*time.Millisecond, discardLogger())
	w.Register("john-doe", 1*time.Hour)

	// First cycle: should stop.
	ctx1, cancel1 := context.WithTimeout(context.Background(), 200*time.Millisecond)
	w.Run(ctx1)
	cancel1()

	if len(stopMock.stopped) != 1 {
		t.Fatalf("first cycle: expected 1 stop, got %d", len(stopMock.stopped))
	}

	// Reset and record new activity.
	w.Reset("john-doe")
	activity.RecordActivity("john-doe", time.Now().Add(-2*time.Hour))

	// Second cycle: should stop again.
	ctx2, cancel2 := context.WithTimeout(context.Background(), 200*time.Millisecond)
	w.Run(ctx2)
	cancel2()

	if len(stopMock.stopped) != 2 {
		t.Errorf("second cycle: expected 2 stops, got %d", len(stopMock.stopped))
	}
}

func TestWatcherMultipleWorkspaces(t *testing.T) {
	activity := newStubActivity()
	// john-doe is inactive.
	activity.RecordActivity("john-doe", time.Now().Add(-2*time.Hour))
	// jane-smith is active.
	activity.RecordActivity("jane-smith", time.Now())

	stopMock := &mockStopFunc{}
	w := NewWatcher(activity, stopMock.stop, 100*time.Millisecond, discardLogger())
	w.Register("john-doe", 1*time.Hour)
	w.Register("jane-smith", 1*time.Hour)

	ctx, cancel := context.WithTimeout(context.Background(), 400*time.Millisecond)
	defer cancel()
	w.Run(ctx)

	if len(stopMock.stopped) != 1 {
		t.Fatalf("expected 1 stop, got %d: %v", len(stopMock.stopped), stopMock.stopped)
	}
	if stopMock.stopped[0] != "john-doe" {
		t.Errorf("stopped slug = %q, want john-doe", stopMock.stopped[0])
	}
}

func TestIsStopped(t *testing.T) {
	activity := newStubActivity()
	activity.RecordActivity("john-doe", time.Now().Add(-2*time.Hour))

	stopMock := &mockStopFunc{}
	w := NewWatcher(activity, stopMock.stop, 50*time.Millisecond, discardLogger())
	w.Register("john-doe", 1*time.Hour)

	if w.IsStopped("john-doe") {
		t.Error("should not be stopped before check")
	}

	ctx, cancel := context.WithTimeout(context.Background(), 200*time.Millisecond)
	w.Run(ctx)
	cancel()

	if !w.IsStopped("john-doe") {
		t.Error("should be stopped after check")
	}
	if w.IsStopped("jane-smith") {
		t.Error("unregistered workspace should not be stopped")
	}
}

func discardLogger() logr.Logger {
	return logr.Discard()
}
