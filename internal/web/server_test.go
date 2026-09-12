package web

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	profilev1 "github.com/captnbp/CodX/api/profile/v1"
	"github.com/captnbp/CodX/internal/config"
	"github.com/captnbp/CodX/internal/k8s"
	"github.com/captnbp/CodX/internal/k8s/fake"
	"github.com/captnbp/CodX/internal/session"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

// testServer builds a web.Server with in-memory fakes for testing.
func testServer(t *testing.T) (*Server, *session.MemoryStore, *k8s.ProfileStore) {
	t.Helper()

	cfg := &config.Config{
		InstanceName: "codx",
		Namespace:    "codx-system",
		HTTP: config.HTTPConfig{
			ListenAddr:   ":0",
			TLSCertFile:  "/tls/tls.crt",
			TLSKeyFile:   "/tls/tls.key",
		},
		Slug: config.SlugConfig{MaxLength: 63},
		CertManager: config.CertManagerConfig{
			IssuerType:  "Issuer",
			IssuerGroup: "cert-manager.io",
			IssuerName:  "codx-issuer",
			Renewal:     "720h",
			Validity:    "2160h",
		},
	}

	store := session.NewMemoryStore()
	profiles := k8s.NewProfileStore()

	// Populate profiles.
	profiles.Upsert(&profilev1.Profile{
		ObjectMeta: metav1.ObjectMeta{Name: "python-dev"},
		Spec: profilev1.ProfileSpec{
			Title:       "Python Developer",
			Description: "Python 3.12 workspace",
			OIDCGroups:  []string{"developers"},
			PodSpec:     profilev1.ProfilePodSpec{Image: "codercom/code-server:latest"},
		},
	})
	profiles.Upsert(&profilev1.Profile{
		ObjectMeta: metav1.ObjectMeta{Name: "default"},
		Spec: profilev1.ProfileSpec{
			Title:       "Default",
			Description: "Default workspace",
			OIDCGroups:  nil, // all users
			PodSpec:     profilev1.ProfilePodSpec{Image: "codercom/code-server:latest"},
		},
	})

	// Create a workspace manager with fake clients.
	cs := &k8s.Clientset{
		CoreV1:      fake.NewCoreV1Client(),
		CertManager: fake.NewCertManagerClient(),
	}
	wm := k8s.NewWorkspaceManager(cs, cfg)

	// Use a mock proxy factory that doesn't need TLS files.
	proxyFactory := func(fqdn string) (http.Handler, error) {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			w.WriteHeader(http.StatusOK)
			w.Write([]byte("proxied to " + fqdn))
		}), nil
	}

	// We don't use a real OIDC authenticator in tests; the auth field is
	// only needed for login/callback which we test separately.
	srv := New(cfg, nil, store, profiles, wm, proxyFactory)

	return srv, store, profiles
}

// createSessionCookie creates a test session in the store and returns the
// cookie value to use in requests.
func createSessionCookie(t *testing.T, store session.Store, username, slug string, groups []string, isAdmin bool) string {
	t.Helper()
	sess := &session.Session{
		ID:        "test-session-" + slug,
		Subject:   "sub-" + slug,
		Username:  username,
		Slug:      slug,
		Groups:    groups,
		IsAdmin:   isAdmin,
		ExpiresAt: time.Now().Add(1 * time.Hour),
	}
	if err := store.Save(context.Background(), sess); err != nil {
		t.Fatalf("save session: %v", err)
	}
	return sess.ID
}

func doRequest(t *testing.T, handler http.Handler, method, path string, cookieValue string) *httptest.ResponseRecorder {
	t.Helper()
	req := httptest.NewRequest(method, path, nil)
	if cookieValue != "" {
		req.AddCookie(&http.Cookie{Name: SessionCookieName, Value: cookieValue})
	}
	rr := httptest.NewRecorder()
	handler.ServeHTTP(rr, req)
	return rr
}

func TestHealthEndpoint(t *testing.T) {
	srv, _, _ := testServer(t)
	handler := srv.Handler()

	rr := doRequest(t, handler, "GET", "/healthz", "")
	if rr.Code != http.StatusOK {
		t.Errorf("healthz: got %d, want %d", rr.Code, http.StatusOK)
	}
	if !strings.Contains(rr.Body.String(), "ok") {
		t.Errorf("healthz body = %q, want to contain 'ok'", rr.Body.String())
	}
}

func TestRedirectToLoginWithoutSession(t *testing.T) {
	srv, _, _ := testServer(t)
	handler := srv.Handler()

	rr := doRequest(t, handler, "GET", "/", "")
	if rr.Code != http.StatusSeeOther {
		t.Errorf("no session: got %d, want %d (redirect)", rr.Code, http.StatusSeeOther)
	}
	if !strings.Contains(rr.Header().Get("Location"), "/auth/login") {
		t.Errorf("no session redirect: Location = %q, want /auth/login", rr.Header().Get("Location"))
	}
}

func TestIndexWithSession(t *testing.T) {
	srv, store, _ := testServer(t)
	handler := srv.Handler()

	cookie := createSessionCookie(t, store, "john.doe", "john-doe", []string{"developers"}, false)
	rr := doRequest(t, handler, "GET", "/", cookie)

	if rr.Code != http.StatusOK {
		t.Fatalf("index with session: got %d, want %d", rr.Code, http.StatusOK)
	}
	if !strings.Contains(rr.Body.String(), "john.doe") {
		t.Errorf("index should contain username: %q", rr.Body.String())
	}
	if !strings.Contains(rr.Body.String(), "Python Developer") {
		t.Errorf("index should list the python-dev profile: %q", rr.Body.String())
	}
	if !strings.Contains(rr.Body.String(), "Default") {
		t.Errorf("index should list the default profile: %q", rr.Body.String())
	}
}

func TestIndexWithSessionNoMatchingProfiles(t *testing.T) {
	srv, store, _ := testServer(t)
	handler := srv.Handler()

	cookie := createSessionCookie(t, store, "nobody", "nobody", []string{"nonexistent-group"}, false)
	rr := doRequest(t, handler, "GET", "/", cookie)

	if rr.Code != http.StatusOK {
		t.Fatalf("index: got %d, want %d", rr.Code, http.StatusOK)
	}
	// Should only see the "default" profile (empty OIDCGroups = all users).
	if !strings.Contains(rr.Body.String(), "Default") {
		t.Errorf("index should list the default profile: %q", rr.Body.String())
	}
	if strings.Contains(rr.Body.String(), "Python Developer") {
		t.Errorf("index should not list python-dev for non-matching user: %q", rr.Body.String())
	}
}

func TestListProfilesAPI(t *testing.T) {
	srv, store, _ := testServer(t)
	handler := srv.Handler()

	cookie := createSessionCookie(t, store, "john.doe", "john-doe", []string{"developers"}, false)
	rr := doRequest(t, handler, "GET", "/api/profiles", cookie)

	if rr.Code != http.StatusOK {
		t.Fatalf("list profiles: got %d, want %d", rr.Code, http.StatusOK)
	}

	var infos []struct {
		Name  string `json:"name"`
		Title string `json:"title"`
	}
	if err := json.Unmarshal(rr.Body.Bytes(), &infos); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}

	if len(infos) != 2 {
		t.Errorf("profiles count = %d, want 2", len(infos))
	}
}

func TestListProfilesAPINoSession(t *testing.T) {
	srv, _, _ := testServer(t)
	handler := srv.Handler()

	rr := doRequest(t, handler, "GET", "/api/profiles", "")
	if rr.Code != http.StatusSeeOther {
		t.Errorf("no session: got %d, want redirect", rr.Code)
	}
}

func TestStopWorkspaceAPI(t *testing.T) {
	srv, store, _ := testServer(t)
	handler := srv.Handler()

	cookie := createSessionCookie(t, store, "john.doe", "john-doe", []string{"developers"}, false)

	// First create the workspace objects via EnsureWorkspace directly.
	// (The stop endpoint calls StopWorkspace which deletes the pod.)

	rr := doRequest(t, handler, "POST", "/api/workspace/stop", cookie)
	if rr.Code != http.StatusOK {
		t.Fatalf("stop workspace: got %d, want %d, body: %s", rr.Code, http.StatusOK, rr.Body.String())
	}

	var result map[string]string
	if err := json.Unmarshal(rr.Body.Bytes(), &result); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if result["status"] != "stopped" {
		t.Errorf("status = %q, want stopped", result["status"])
	}
}

func TestStopWorkspaceWrongMethod(t *testing.T) {
	srv, store, _ := testServer(t)
	handler := srv.Handler()

	cookie := createSessionCookie(t, store, "john.doe", "john-doe", []string{"developers"}, false)
	rr := doRequest(t, handler, "GET", "/api/workspace/stop", cookie)
	if rr.Code != http.StatusMethodNotAllowed {
		t.Errorf("GET stop: got %d, want %d", rr.Code, http.StatusMethodNotAllowed)
	}
}

func TestWorkspaceStatusAPI(t *testing.T) {
	srv, store, _ := testServer(t)
	handler := srv.Handler()

	cookie := createSessionCookie(t, store, "john.doe", "john-doe", []string{"developers"}, false)
	rr := doRequest(t, handler, "GET", "/api/workspace/status", cookie)

	if rr.Code != http.StatusOK {
		t.Fatalf("status: got %d, want %d", rr.Code, http.StatusOK)
	}

	var result map[string]any
	if err := json.Unmarshal(rr.Body.Bytes(), &result); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if result["slug"] != "john-doe" {
		t.Errorf("slug = %v, want john-doe", result["slug"])
	}
}

func TestProxyForbidsOtherUser(t *testing.T) {
	srv, store, _ := testServer(t)
	handler := srv.Handler()

	cookie := createSessionCookie(t, store, "john.doe", "john-doe", []string{"developers"}, false)
	rr := doRequest(t, handler, "GET", "/user/jane-smith/", cookie)

	if rr.Code != http.StatusForbidden {
		t.Errorf("proxy to other user: got %d, want %d", rr.Code, http.StatusForbidden)
	}
}

func TestProxyOwnWorkspace(t *testing.T) {
	srv, store, _ := testServer(t)
	handler := srv.Handler()

	cookie := createSessionCookie(t, store, "john.doe", "john-doe", []string{"developers"}, false)
	rr := doRequest(t, handler, "GET", "/user/john-doe/", cookie)

	if rr.Code != http.StatusOK {
		t.Fatalf("proxy to own workspace: got %d, want %d", rr.Code, http.StatusOK)
	}
	if !strings.Contains(rr.Body.String(), "proxied") {
		t.Errorf("proxy body = %q, want to contain 'proxied'", rr.Body.String())
	}
}

func TestProxyPathRouting(t *testing.T) {
	srv, store, _ := testServer(t)
	handler := srv.Handler()

	cookie := createSessionCookie(t, store, "john.doe", "john-doe", []string{"developers"}, false)
	rr := doRequest(t, handler, "GET", "/user/john-doe/some/path", cookie)

	if rr.Code != http.StatusOK {
		t.Fatalf("proxy with path: got %d, want %d", rr.Code, http.StatusOK)
	}
}

func TestIsProfileAllowed(t *testing.T) {
	tests := []struct {
		name     string
		profile  *profilev1.Profile
		groups   []string
		expected bool
	}{
		{"empty groups = all users", &profilev1.Profile{Spec: profilev1.ProfileSpec{}}, []string{"any"}, true},
		{"matching group", &profilev1.Profile{Spec: profilev1.ProfileSpec{OIDCGroups: []string{"dev"}}}, []string{"dev"}, true},
		{"non-matching group", &profilev1.Profile{Spec: profilev1.ProfileSpec{OIDCGroups: []string{"admin"}}}, []string{"dev"}, false},
		{"one of many matches", &profilev1.Profile{Spec: profilev1.ProfileSpec{OIDCGroups: []string{"admin", "dev"}}}, []string{"dev"}, true},
		{"no user groups", &profilev1.Profile{Spec: profilev1.ProfileSpec{OIDCGroups: []string{"dev"}}}, []string{}, false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := isProfileAllowed(tt.profile, tt.groups); got != tt.expected {
				t.Errorf("isProfileAllowed = %v, want %v", got, tt.expected)
			}
		})
	}
}

func TestExpiredSessionRedirects(t *testing.T) {
	srv, store, _ := testServer(t)
	handler := srv.Handler()

	// Create an expired session.
	sess := &session.Session{
		ID:        "expired-session",
		Username:  "john.doe",
		Slug:      "john-doe",
		ExpiresAt: time.Now().Add(-1 * time.Hour), // expired
	}
	if err := store.Save(context.Background(), sess); err != nil {
		t.Fatalf("save session: %v", err)
	}

	rr := doRequest(t, handler, "GET", "/", "expired-session")
	if rr.Code != http.StatusSeeOther {
		t.Errorf("expired session: got %d, want redirect", rr.Code)
	}
}

func TestLogout(t *testing.T) {
	srv, store, _ := testServer(t)
	handler := srv.Handler()

	cookie := createSessionCookie(t, store, "john.doe", "john-doe", []string{"developers"}, false)
	rr := doRequest(t, handler, "GET", "/auth/logout", cookie)

	if rr.Code != http.StatusSeeOther {
		t.Fatalf("logout: got %d, want redirect", rr.Code)
	}
	if !strings.Contains(rr.Header().Get("Location"), "/auth/login") {
		t.Errorf("logout redirect = %q, want /auth/login", rr.Header().Get("Location"))
	}

	// Session should be deleted from store.
	_, err := store.Get(context.Background(), "test-session-john-doe")
	if err == nil {
		t.Error("session should be deleted after logout")
	}
}
