package web

import (
	"fmt"
	"net/http"
	"sync"
	"testing"

	"github.com/go-logr/logr"
)

// auditSink is a logr.LogSink that captures entries for test assertions.
type auditSink struct {
	mu      sync.Mutex
	entries []map[string]any
}

func (s *auditSink) Init(info logr.RuntimeInfo) {}
func (s *auditSink) Enabled(level int) bool     { return true }

func (s *auditSink) Info(level int, msg string, kv ...any) {
	s.record(msg, kv...)
}

func (s *auditSink) Error(err error, msg string, kv ...any) {
	s.record(msg, kv...)
}

func (s *auditSink) WithValues(kv ...any) logr.LogSink { return s }
func (s *auditSink) WithName(name string) logr.LogSink { return s }

func (s *auditSink) record(msg string, kv ...any) {
	entry := map[string]any{"msg": msg}
	for i := 0; i+1 < len(kv); i += 2 {
		entry[fmt.Sprint(kv[i])] = kv[i+1]
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	s.entries = append(s.entries, entry)
}

// find returns the last entry with the given message, or nil.
func (s *auditSink) find(msg string) map[string]any {
	s.mu.Lock()
	defer s.mu.Unlock()
	for i := len(s.entries) - 1; i >= 0; i-- {
		if s.entries[i]["msg"] == msg {
			return s.entries[i]
		}
	}
	return nil
}

// newAuditServer returns a test server whose audit logger writes to a
// capturable sink.
func newAuditServer(t *testing.T) (*Server, *auditSink) {
	t.Helper()
	srv, _, _ := testServer(t)
	sink := &auditSink{}
	srv.WithLogger(logr.Discard())
	srv.WithAuditLogger(logr.New(sink))
	return srv, sink
}

func TestAuditLogout(t *testing.T) {
	srv, sink := newAuditServer(t)
	cookie := createSessionCookie(t, srv.store, "john", "john-doe", nil, false)

	doRequest(t, srv.Handler(), "GET", "/auth/logout", cookie)

	entry := sink.find("logout")
	if entry == nil {
		t.Fatal("no audit entry for logout")
	}
	if entry["user"] != "john" {
		t.Errorf("logout audit user = %v, want john", entry["user"])
	}
}

func TestAuditWorkspaceStop(t *testing.T) {
	srv, sink := newAuditServer(t)
	cookie := createSessionCookie(t, srv.store, "john", "john-doe", nil, false)

	rr := doRequest(t, srv.Handler(), "POST", "/api/workspace/stop", cookie)
	if rr.Code != http.StatusOK {
		t.Fatalf("stop workspace: got %d, want %d", rr.Code, http.StatusOK)
	}

	entry := sink.find("workspace_stop")
	if entry == nil {
		t.Fatal("no audit entry for workspace_stop")
	}
	if entry["user"] != "john" || entry["slug"] != "john-doe" {
		t.Errorf("workspace_stop audit = %v, want user john / slug john-doe", entry)
	}
}

func TestAuditWorkspaceStartDenied(t *testing.T) {
	srv, sink := newAuditServer(t)
	// john is not in the "developers" group required by the python-dev profile.
	cookie := createSessionCookie(t, srv.store, "john", "john-doe", nil, false)

	rr := doRequest(t, srv.Handler(), "GET", "/api/workspace/start?profile=python-dev", cookie)
	if rr.Code != http.StatusForbidden {
		t.Fatalf("start workspace: got %d, want %d", rr.Code, http.StatusForbidden)
	}

	entry := sink.find("workspace_start_denied")
	if entry == nil {
		t.Fatal("no audit entry for workspace_start_denied")
	}
	if entry["user"] != "john" || entry["profile"] != "python-dev" {
		t.Errorf("workspace_start_denied audit = %v, want user john / profile python-dev", entry)
	}

	// Unknown profile.
	rr = doRequest(t, srv.Handler(), "GET", "/api/workspace/start?profile=ghost", cookie)
	if rr.Code != http.StatusNotFound {
		t.Fatalf("start workspace: got %d, want %d", rr.Code, http.StatusNotFound)
	}
	entry = sink.find("workspace_start_denied")
	if entry == nil || entry["profile"] != "ghost" {
		t.Errorf("workspace_start_denied audit = %v, want profile ghost", entry)
	}
}

func TestAuditProxyAccessDenied(t *testing.T) {
	srv, sink := newAuditServer(t)
	cookie := createSessionCookie(t, srv.store, "john", "john-doe", nil, false)

	rr := doRequest(t, srv.Handler(), "GET", "/user/jane-doe/", cookie)
	if rr.Code != http.StatusForbidden {
		t.Fatalf("proxy: got %d, want %d", rr.Code, http.StatusForbidden)
	}

	entry := sink.find("proxy_access_denied")
	if entry == nil {
		t.Fatal("no audit entry for proxy_access_denied")
	}
	if entry["user"] != "john" || entry["target"] != "jane-doe" {
		t.Errorf("proxy_access_denied audit = %v, want user john / target jane-doe", entry)
	}
}

func TestAuditAdminStopWorkspace(t *testing.T) {
	srv, sink := newAuditServer(t)
	cookie := createSessionCookie(t, srv.store, "admin", "admin", nil, true)

	rr := doRequest(t, srv.Handler(), "POST", "/api/admin/users/jane-doe/stop", cookie)
	if rr.Code != http.StatusOK {
		t.Fatalf("admin stop workspace: got %d, want %d", rr.Code, http.StatusOK)
	}

	entry := sink.find("workspace_stop")
	if entry == nil {
		t.Fatal("no audit entry for workspace_stop")
	}
	if entry["user"] != "admin" || entry["target"] != "jane-doe" {
		t.Errorf("workspace_stop audit = %v, want user admin / target jane-doe", entry)
	}
}

func TestAuditAdminAccessDenied(t *testing.T) {
	srv, sink := newAuditServer(t)
	cookie := createSessionCookie(t, srv.store, "john", "john-doe", nil, false)

	rr := doRequest(t, srv.Handler(), "GET", "/api/admin/users", cookie)
	if rr.Code != http.StatusForbidden {
		t.Fatalf("admin list users: got %d, want %d", rr.Code, http.StatusForbidden)
	}

	entry := sink.find("admin_access_denied")
	if entry == nil {
		t.Fatal("no audit entry for admin_access_denied")
	}
	if entry["user"] != "john" {
		t.Errorf("admin_access_denied audit = %v, want user john", entry)
	}
}

func TestClientIP(t *testing.T) {
	req := &http.Request{Header: http.Header{}, RemoteAddr: "10.0.0.1:1234"}
	if got := clientIP(req); got != "10.0.0.1:1234" {
		t.Errorf("clientIP without XFF = %q, want RemoteAddr", got)
	}

	req.Header.Set("X-Forwarded-For", "203.0.113.5, 10.0.0.2")
	if got := clientIP(req); got != "203.0.113.5" {
		t.Errorf("clientIP with XFF list = %q, want 203.0.113.5", got)
	}

	req.Header.Set("X-Forwarded-For", "203.0.113.7")
	if got := clientIP(req); got != "203.0.113.7" {
		t.Errorf("clientIP with single XFF = %q, want 203.0.113.7", got)
	}
}
