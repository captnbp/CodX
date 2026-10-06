package web

import (
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
	"time"

	"github.com/captnbp/CodX/internal/config"
)

// escapedCertInfoHeader builds a Traefik-style X-Forwarded-Tls-Client-Cert-Info
// header value: the raw info string is percent-escaped like Traefik does with
// PathEscape.
func escapedCertInfoHeader(raw string) string {
	return url.PathEscape(raw)
}

func TestClientCertCN(t *testing.T) {
	tests := []struct {
		name  string
		value string
		want  string
	}{
		{
			name:  "percent-encoded subject with CN and OUs",
			value: escapedCertInfoHeader(`Subject="CN=alice,OU=example,O=Example Org",Issuer="CN=Example Root CA,OU=example"`) + `,` + escapedCertInfoHeader(`NB=1600000000,NA=1700000000`),
			want:  "alice",
		},
		{
			name:  "unescaped subject with CN only",
			value: `Subject="CN=alice"`,
			want:  "alice",
		},
		{
			name:  "CN not first component",
			value: `Subject="O=Example Org,OU=example,CN=alice"`,
			want:  "alice",
		},
		{
			name:  "spaces around components",
			value: `Subject="CN = alice, OU=example"`,
			want:  "",
		},
		{
			name:  "CN with spaces and unicode",
			value: escapedCertInfoHeader(`Subject="CN=Alice Martin,OU=example"`),
			want:  "Alice Martin",
		},
		{
			name:  "no subject field",
			value: `Issuer="CN=Example Root CA",NB=1600000000`,
			want:  "",
		},
		{
			name:  "unquoted subject without commas",
			value: `Subject=CN=alice`,
			want:  "alice",
		},
		{
			name:  "empty header",
			value: ``,
			want:  "",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := clientCertCN(tt.value); got != tt.want {
				t.Errorf("clientCertCN(%q) = %q, want %q", tt.value, got, tt.want)
			}
		})
	}
}

// fallbackTestServer builds a web.Server with the mTLS fallback enabled and
// the given allow list.
func fallbackTestServer(t *testing.T, adminCNs []string) (*Server, *httptest.Server) {
	t.Helper()

	srv, store, _, _ := testServerWithClientset(t)
	srv.cfg.AuthFallback = config.AuthFallbackConfig{
		Enabled:    true,
		HeaderName: "X-Forwarded-Tls-Client-Cert-Info",
		AdminCNs:   adminCNs,
	}
	srv.cfg.OIDC.AdminGroup = "codx-admins"
	srv.cfg.Slug.MaxLength = 63

	ts := httptest.NewServer(srv.Handler())
	t.Cleanup(ts.Close)

	// Keep the store reference alive to avoid the unused-variable linter.
	_ = store
	return srv, ts
}

func doFallbackRequest(t *testing.T, ts *httptest.Server, path, certHeader string) *http.Response {
	t.Helper()
	req, err := http.NewRequest(http.MethodGet, ts.URL+path, nil)
	if err != nil {
		t.Fatalf("new request: %v", err)
	}
	if certHeader != "" {
		req.Header.Set("X-Forwarded-Tls-Client-Cert-Info", certHeader)
	}
	client := &http.Client{
		CheckRedirect: func(req *http.Request, via []*http.Request) error {
			return http.ErrUseLastResponse
		},
	}
	resp, err := client.Do(req)
	if err != nil {
		t.Fatalf("do request: %v", err)
	}
	t.Cleanup(func() { resp.Body.Close() })
	return resp
}

func TestFallbackAuthGrantsAdminSession(t *testing.T) {
	_, ts := fallbackTestServer(t, []string{"alice"})

	header := escapedCertInfoHeader(`Subject="CN=alice,OU=example,O=Example Org"`) + `,` + escapedCertInfoHeader(`Issuer="CN=Example Root CA"`)

	resp := doFallbackRequest(t, ts, "/admin", header)
	if resp.StatusCode != http.StatusOK {
		t.Errorf("fallback admin request: got %d, want %d", resp.StatusCode, http.StatusOK)
	}

	// The session cookie must be set so subsequent requests work without
	// the header (until the session expires).
	cookies := resp.Cookies()
	found := false
	for _, c := range cookies {
		if c.Name == SessionCookieName && c.Value != "" {
			found = true
		}
	}
	if !found {
		t.Errorf("fallback login should set the %s cookie, got %v", SessionCookieName, cookies)
	}
}

func TestFallbackAuthDeniedForUnknownCN(t *testing.T) {
	_, ts := fallbackTestServer(t, []string{"alice"})

	header := escapedCertInfoHeader(`Subject="CN=mallory,OU=evil"`)

	resp := doFallbackRequest(t, ts, "/", header)
	if resp.StatusCode != http.StatusSeeOther {
		t.Errorf("unknown CN: got %d, want %d (redirect to login)", resp.StatusCode, http.StatusSeeOther)
	}
	if loc := resp.Header.Get("Location"); !strings.Contains(loc, "/auth/login") {
		t.Errorf("unknown CN redirect: Location = %q, want /auth/login", loc)
	}
}

func TestFallbackAuthDisabledRedirectsToLogin(t *testing.T) {
	srv, _, _, _ := testServerWithClientset(t)
	srv.cfg.AuthFallback = config.AuthFallbackConfig{
		Enabled:    false,
		HeaderName: "X-Forwarded-Tls-Client-Cert-Info",
		AdminCNs:   []string{"alice"},
	}
	ts := httptest.NewServer(srv.Handler())
	t.Cleanup(ts.Close)

	resp := doFallbackRequest(t, ts, "/", escapedCertInfoHeader(`Subject="CN=alice"`))
	if resp.StatusCode != http.StatusSeeOther {
		t.Errorf("disabled fallback: got %d, want %d (redirect to login)", resp.StatusCode, http.StatusSeeOther)
	}
}

func TestFallbackSessionContent(t *testing.T) {
	srv, _, _ := testServer(t)
	srv.cfg.AuthFallback = config.AuthFallbackConfig{
		Enabled:    true,
		HeaderName: "X-Forwarded-Tls-Client-Cert-Info",
		AdminCNs:   []string{"alice"},
	}
	srv.cfg.OIDC.AdminGroup = "codx-admins"

	req := httptest.NewRequest(http.MethodGet, "/admin", nil)
	req.Header.Set("X-Forwarded-Tls-Client-Cert-Info", escapedCertInfoHeader(`Subject="CN=alice,OU=example"`))

	sess := srv.fallbackSession(req)
	if sess == nil {
		t.Fatal("fallbackSession returned nil for an allowed CN")
	}
	if !sess.IsAdmin {
		t.Error("fallback session should be admin")
	}
	if sess.Username != "alice" {
		t.Errorf("Username = %q, want alice", sess.Username)
	}
	if sess.Subject != "mtls:alice" {
		t.Errorf("Subject = %q, want mtls:alice", sess.Subject)
	}
	if sess.Slug != "alice" {
		t.Errorf("Slug = %q, want alice", sess.Slug)
	}
	if len(sess.Groups) != 1 || sess.Groups[0] != "codx-admins" {
		t.Errorf("Groups = %v, want [codx-admins]", sess.Groups)
	}
	if sess.ExpiresAt.Before(time.Now()) {
		t.Errorf("ExpiresAt = %v, should be in the future", sess.ExpiresAt)
	}
}

func TestSessionTTLConfigurable(t *testing.T) {
	srv, _, _ := testServer(t)
	srv.cfg.AuthFallback = config.AuthFallbackConfig{
		Enabled:    true,
		HeaderName: "X-Forwarded-Tls-Client-Cert-Info",
		AdminCNs:   []string{"alice"},
	}
	srv.cfg.Session.TTL = "8h"

	// The session expiry follows the configured TTL.
	req := httptest.NewRequest(http.MethodGet, "/", nil)
	req.Header.Set("X-Forwarded-Tls-Client-Cert-Info", escapedCertInfoHeader(`Subject="CN=alice"`))
	sess := srv.fallbackSession(req)
	if sess == nil {
		t.Fatal("fallbackSession returned nil for an allowed CN")
	}
	want := 8 * time.Hour
	got := time.Until(sess.ExpiresAt)
	if got < want-time.Minute || got > want+time.Minute {
		t.Errorf("ExpiresAt is %v from now, want ~%v (session.ttl)", got, want)
	}

	// The cookie MaxAge follows the configured TTL too.
	rec := httptest.NewRecorder()
	srv.setSessionCookie(rec, sess.ID)
	found := false
	for _, c := range rec.Result().Cookies() {
		if c.Name == SessionCookieName {
			found = true
			if c.MaxAge != int(want.Seconds()) {
				t.Errorf("cookie MaxAge = %d, want %d (session.ttl seconds)", c.MaxAge, int(want.Seconds()))
			}
		}
	}
	if !found {
		t.Fatalf("setSessionCookie did not set the %s cookie", SessionCookieName)
	}

	// The default TTL applies when session.ttl is not set or invalid.
	defaultTTL, err := time.ParseDuration(config.DefaultSessionTTL)
	if err != nil {
		t.Fatalf("parse config.DefaultSessionTTL %q: %v", config.DefaultSessionTTL, err)
	}
	srv.cfg.Session.TTL = ""
	if ttl := srv.sessionTTL(); ttl != defaultTTL {
		t.Errorf("sessionTTL() with empty config = %v, want %v (config.DefaultSessionTTL)", ttl, defaultTTL)
	}
	srv.cfg.Session.TTL = "bogus"
	if ttl := srv.sessionTTL(); ttl != defaultTTL {
		t.Errorf("sessionTTL() with invalid config = %v, want %v (config.DefaultSessionTTL)", ttl, defaultTTL)
	}
}
