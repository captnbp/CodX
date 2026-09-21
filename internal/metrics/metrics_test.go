package metrics

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestHandlerExposesGoAndCodxMetrics(t *testing.T) {
	m := New()
	m.SetWorkspaceCounts(3, 1)
	m.SetHealthy(true)

	req := httptest.NewRequest(http.MethodGet, "/metrics", nil)
	rec := httptest.NewRecorder()
	m.Handler().ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d", rec.Code, http.StatusOK)
	}

	body := rec.Body.String()
	for _, name := range []string{
		"go_goroutines",
		WorkspacesRunningName,
		WorkspacesPendingName,
		HealthName,
	} {
		if !strings.Contains(body, name) {
			t.Errorf("metrics output missing %q", name)
		}
	}
	if !strings.Contains(body, WorkspacesRunningName+" 3") {
		t.Errorf("metrics output missing %s 3", WorkspacesRunningName)
	}
	if !strings.Contains(body, WorkspacesPendingName+" 1") {
		t.Errorf("metrics output missing %s 1", WorkspacesPendingName)
	}
	if !strings.Contains(body, HealthName+" 1") {
		t.Errorf("metrics output missing %s 1", HealthName)
	}
}

func TestSetHealthyFalse(t *testing.T) {
	m := New()
	m.SetHealthy(false)

	req := httptest.NewRequest(http.MethodGet, "/metrics", nil)
	rec := httptest.NewRecorder()
	m.Handler().ServeHTTP(rec, req)

	if !strings.Contains(rec.Body.String(), HealthName+" 0") {
		t.Errorf("metrics output missing %s 0", HealthName)
	}
}
