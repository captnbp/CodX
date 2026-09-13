package oidc

import (
	"context"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/captnbp/CodX/internal/config"
	"github.com/coreos/go-oidc/v3/oidc"
	"github.com/go-jose/go-jose/v4"
)

// fakeOIDCServer stands up a minimal OIDC provider with discovery, JWKS,
// authorization, and token endpoints for testing.
type fakeOIDCServer struct {
	t        *testing.T
	server   *httptest.Server
	signer   jose.Signer
	jwk      jose.JSONWebKey
	clientID string
}

func newFakeOIDCServer(t *testing.T, clientID string) *fakeOIDCServer {
	t.Helper()

	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatalf("generate ECDSA key: %v", err)
	}

	jwk := jose.JSONWebKey{
		Key:       key,
		KeyID:     "test-key-1",
		Algorithm: string(jose.ES256),
		Use:       "sig",
	}

	signer, err := jose.NewSigner(jose.SigningKey{Algorithm: jose.ES256, Key: key}, &jose.SignerOptions{
		ExtraHeaders: map[jose.HeaderKey]any{jose.HeaderKey("kid"): "test-key-1"},
	})
	if err != nil {
		t.Fatalf("create signer: %v", err)
	}

	f := &fakeOIDCServer{
		t:        t,
		jwk:      jwk,
		signer:   signer,
		clientID: clientID,
	}

	mux := http.NewServeMux()
	mux.HandleFunc("/.well-known/openid-configuration", f.handleDiscovery)
	mux.HandleFunc("/auth", f.handleAuth)
	mux.HandleFunc("/token", f.handleToken)
	mux.HandleFunc("/jwks", f.handleJWKS)

	f.server = httptest.NewServer(mux)

	t.Cleanup(f.server.Close)

	return f
}

// newFakeOIDCServerWithClaims creates a fake OIDC server that issues ID tokens
// with the given custom claims.
func newFakeOIDCServerWithClaims(t *testing.T, clientID string, claims map[string]any) *fakeOIDCServer {
	t.Helper()

	f := newFakeOIDCServer(t, clientID)

	// Replace the token handler to use custom claims.
	origHandler := f.server.Config.Handler
	mux := http.NewServeMux()
	mux.HandleFunc("/.well-known/openid-configuration", f.handleDiscovery)
	mux.HandleFunc("/auth", f.handleAuth)
	mux.HandleFunc("/jwks", f.handleJWKS)
	mux.HandleFunc("/token", func(w http.ResponseWriter, r *http.Request) {
		claims["iss"] = f.server.URL
		claims["aud"] = clientID
		if _, ok := claims["exp"]; !ok {
			claims["exp"] = time.Now().Add(1 * time.Hour).Unix()
		}
		if _, ok := claims["iat"]; !ok {
			claims["iat"] = time.Now().Unix()
		}
		idToken := f.makeIDToken(claims)
		w.Header().Set("Content-Type", "application/json")
		if err := json.NewEncoder(w).Encode(map[string]any{
			"access_token": "fake-access-token",
			"token_type":   "Bearer",
			"expires_in":   3600,
			"id_token":     idToken,
		}); err != nil {
			f.t.Fatalf("encode token response: %v", err)
		}
	})
	_ = origHandler
	f.server.Config.Handler = mux

	return f
}

func (f *fakeOIDCServer) issuer() string {
	return f.server.URL
}

func (f *fakeOIDCServer) handleDiscovery(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")
	if err := json.NewEncoder(w).Encode(map[string]any{
		"issuer":                                f.server.URL,
		"authorization_endpoint":                 f.server.URL + "/auth",
		"token_endpoint":                         f.server.URL + "/token",
		"jwks_uri":                               f.server.URL + "/jwks",
		"id_token_signing_alg_values_supported": []string{"ES256"},
		"response_types_supported":               []string{"code"},
	}); err != nil {
		f.t.Fatalf("encode discovery: %v", err)
	}
}

func (f *fakeOIDCServer) handleAuth(w http.ResponseWriter, r *http.Request) {
	// Redirect back with a fixed code.
	redirect := r.FormValue("redirect_uri") + "?code=test-auth-code&state=" + r.FormValue("state")
	http.Redirect(w, r, redirect, http.StatusFound)
}

func (f *fakeOIDCServer) handleToken(w http.ResponseWriter, r *http.Request) {
	idToken := f.makeIDToken(map[string]any{
		"iss":                f.server.URL,
		"sub":                "user-subject-123",
		"aud":                f.clientID,
		"exp":                time.Now().Add(1 * time.Hour).Unix(),
		"iat":                time.Now().Unix(),
		"preferred_username": "john.doe",
		"groups":              []string{"developers", "codx-admins"},
	})

	w.Header().Set("Content-Type", "application/json")
	if err := json.NewEncoder(w).Encode(map[string]any{
		"access_token": "fake-access-token",
		"token_type":   "Bearer",
		"expires_in":   3600,
		"id_token":     idToken,
	}); err != nil {
		f.t.Fatalf("encode token response: %v", err)
	}
}

func (f *fakeOIDCServer) handleJWKS(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")
	if err := json.NewEncoder(w).Encode(jose.JSONWebKeySet{Keys: []jose.JSONWebKey{f.jwk.Public()}}); err != nil {
		f.t.Fatalf("encode JWKS: %v", err)
	}
}

func (f *fakeOIDCServer) makeIDToken(claims map[string]any) string {
	payload, err := json.Marshal(claims)
	if err != nil {
		f.t.Fatalf("marshal claims: %v", err)
	}

	signed, err := f.signer.Sign(payload)
	if err != nil {
		f.t.Fatalf("sign token: %v", err)
	}

	token, err := signed.CompactSerialize()
	if err != nil {
		f.t.Fatalf("serialize token: %v", err)
	}
	return token
}

func TestNewAuthenticator(t *testing.T) {
	f := newFakeOIDCServer(t, "codx-client")
	ctx := context.Background()

	auth, err := New(ctx, config.OIDCConfig{
		Issuer:              f.issuer(),
		ClientID:            "codx-client",
		ClientSecret:        "secret",
		RedirectURL:         f.server.URL + "/auth/callback",
		GroupClaimName:      "groups",
		AdminGroup:          "codx-admins",
		UsernameClaimName:   "preferred_username",
	})
	if err != nil {
		t.Fatalf("New: %v", err)
	}

	if auth == nil {
		t.Fatal("authenticator is nil")
	}
}

func TestExchange(t *testing.T) {
	f := newFakeOIDCServer(t, "codx-client")
	ctx := context.Background()

	auth, err := New(ctx, config.OIDCConfig{
		Issuer:              f.issuer(),
		ClientID:            "codx-client",
		ClientSecret:         "secret",
		RedirectURL:          f.server.URL + "/auth/callback",
		GroupClaimName:       "groups",
		AdminGroup:           "codx-admins",
		UsernameClaimName:    "preferred_username",
	})
	if err != nil {
		t.Fatalf("New: %v", err)
	}

	// Use a custom HTTP client that follows redirects to the fake server.
	ctx = oidc.ClientContext(ctx, f.server.Client())

	sess, err := auth.Exchange(ctx, "test-auth-code")
	if err != nil {
		t.Fatalf("Exchange: %v", err)
	}

	if sess.Subject != "user-subject-123" {
		t.Errorf("Subject = %q, want user-subject-123", sess.Subject)
	}
	if sess.Username != "john.doe" {
		t.Errorf("Username = %q, want john.doe", sess.Username)
	}
	if sess.Slug != "john-doe" {
		t.Errorf("Slug = %q, want john-doe", sess.Slug)
	}
	if len(sess.Groups) != 2 {
		t.Errorf("Groups = %v, want 2 groups", sess.Groups)
	}
	if !containsString(sess.Groups, "developers") {
		t.Errorf("Groups should contain developers: %v", sess.Groups)
	}
	if !sess.IsAdmin {
		t.Error("IsAdmin should be true for codx-admins member")
	}
	if sess.AccessToken != "fake-access-token" {
		t.Errorf("AccessToken = %q", sess.AccessToken)
	}
	if sess.IDToken == "" {
		t.Error("IDToken should not be empty")
	}
}

func TestExchangeNonAdmin(t *testing.T) {
	f := newFakeOIDCServerWithClaims(t, "codx-client", map[string]any{
		"iss":                "", // filled in by newFakeOIDCServerWithClaims
		"sub":                "user-subject-456",
		"aud":                "codx-client",
		"exp":                time.Now().Add(1 * time.Hour).Unix(),
		"iat":                time.Now().Unix(),
		"preferred_username": "jane.smith",
		"groups":              []string{"developers"},
	})
	ctx := context.Background()

	auth, err := New(ctx, config.OIDCConfig{
		Issuer:              f.issuer(),
		ClientID:            "codx-client",
		ClientSecret:         "secret",
		RedirectURL:          f.server.URL + "/auth/callback",
		GroupClaimName:       "groups",
		AdminGroup:           "codx-admins",
		UsernameClaimName:    "preferred_username",
	})
	if err != nil {
		t.Fatalf("New: %v", err)
	}

	ctx = oidc.ClientContext(ctx, f.server.Client())

	sess, err := auth.Exchange(ctx, "test-auth-code")
	if err != nil {
		t.Fatalf("Exchange: %v", err)
	}

	if sess.IsAdmin {
		t.Error("IsAdmin should be false for non-admin user")
	}
	if sess.Username != "jane.smith" {
		t.Errorf("Username = %q, want jane.smith", sess.Username)
	}
	if sess.Slug != "jane-smith" {
		t.Errorf("Slug = %q, want jane-smith", sess.Slug)
	}
}

func TestExtractGroups(t *testing.T) {
	tests := []struct {
		name    string
		claim   map[string]any
		key     string
		want    []string
	}{
		{"string slice", map[string]any{"groups": []string{"a", "b"}}, "groups", []string{"a", "b"}},
		{"any slice", map[string]any{"groups": []any{"a", "b"}}, "groups", []string{"a", "b"}},
		{"single string", map[string]any{"groups": "single"}, "groups", []string{"single"}},
		{"missing key", map[string]any{}, "groups", nil},
		{"mixed types", map[string]any{"groups": []any{"a", 42, "b"}}, "groups", []string{"a", "b"}},
		{"custom claim name", map[string]any{"memberOf": []string{"x", "y"}}, "memberOf", []string{"x", "y"}},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := extractGroups(tt.claim, tt.key)
			if len(got) != len(tt.want) {
				t.Errorf("extractGroups got %v, want %v", got, tt.want)
				return
			}
			for i, g := range got {
				if g != tt.want[i] {
					t.Errorf("extractGroups[%d] = %q, want %q", i, g, tt.want[i])
				}
			}
		})
	}
}

func TestContainsGroup(t *testing.T) {
	groups := []string{"a", "b", "c"}
	if !containsGroup(groups, "b") {
		t.Error("should find b")
	}
	if containsGroup(groups, "z") {
		t.Error("should not find z")
	}
	if containsGroup(nil, "a") {
		t.Error("nil groups should not find anything")
	}
}

func TestAuthURL(t *testing.T) {
	f := newFakeOIDCServer(t, "codx-client")
	ctx := context.Background()

	auth, err := New(ctx, config.OIDCConfig{
		Issuer:              f.issuer(),
		ClientID:            "codx-client",
		ClientSecret:         "secret",
		RedirectURL:          "https://codx.example.com/auth/callback",
		GroupClaimName:       "groups",
		AdminGroup:           "admins",
		UsernameClaimName:    "preferred_username",
	})
	if err != nil {
		t.Fatalf("New: %v", err)
	}

	url := auth.AuthURL("random-state")
	if url == "" {
		t.Fatal("AuthURL returned empty")
	}
	if !strings.Contains(url, "state=random-state") {
		t.Errorf("AuthURL should contain state: %s", url)
	}
	if !strings.Contains(url, "client_id=codx-client") {
		t.Errorf("AuthURL should contain client_id: %s", url)
	}
}

func TestRandomID(t *testing.T) {
	id1, err := randomID(32)
	if err != nil {
		t.Fatalf("randomID: %v", err)
	}
	id2, err := randomID(32)
	if err != nil {
		t.Fatalf("randomID: %v", err)
	}
	if id1 == id2 {
		t.Error("two random IDs should be different")
	}
	if len(id1) < 32 {
		t.Errorf("random ID too short: %d", len(id1))
	}
}

func TestRandomState(t *testing.T) {
	state, err := RandomState()
	if err != nil {
		t.Fatalf("RandomState: %v", err)
	}
	if state == "" {
		t.Error("RandomState returned empty")
	}
}

func TestIsAdmin(t *testing.T) {
	f := newFakeOIDCServer(t, "codx-client")
	ctx := context.Background()

	auth, err := New(ctx, config.OIDCConfig{
		Issuer:    f.issuer(),
		ClientID:  "codx-client",
		AdminGroup: "codx-admins",
	})
	if err != nil {
		t.Fatalf("New: %v", err)
	}

	if !auth.IsAdmin([]string{"codx-admins"}) {
		t.Error("IsAdmin should be true for admin group member")
	}
	if auth.IsAdmin([]string{"developers"}) {
		t.Error("IsAdmin should be false for non-admin")
	}
}

func TestExtractClaimsMissingUsername(t *testing.T) {
	f := newFakeOIDCServer(t, "codx-client")

	// Create an ID token without the username claim.
	// We'll use a signed JWT directly.
	payload, _ := json.Marshal(map[string]any{
		"iss": f.server.URL,
		"sub": "no-username",
		"aud": "codx-client",
		"exp": time.Now().Add(1 * time.Hour).Unix(),
	})
	signed, err := f.signer.Sign(payload)
	if err != nil {
		t.Fatalf("sign: %v", err)
	}
	rawToken, err := signed.CompactSerialize()
	if err != nil {
		t.Fatalf("serialize: %v", err)
	}

	// Parse it into an IDToken-like structure for testing extractClaims.
	// We need to use the verifier path; instead, test extractClaims directly.
	// Build a fake IDToken by parsing the JWT.
	// Since extractClaims needs *oidc.IDToken, we'll verify it.
	verifier := f.providerVerifier("codx-client")
	idToken, err := verifier.Verify(context.Background(), rawToken)
	if err != nil {
		t.Fatalf("Verify: %v", err)
	}

	_, err = extractClaims(idToken, "groups", "preferred_username")
	if err == nil {
		t.Fatal("extractClaims should fail when username is missing")
	}
}

func (f *fakeOIDCServer) providerVerifier(clientID string) *oidc.IDTokenVerifier {
	provider, err := oidc.NewProvider(oidc.ClientContext(context.Background(), f.server.Client()), f.issuer())
	if err != nil {
		f.t.Fatalf("NewProvider: %v", err)
	}
	return provider.Verifier(&oidc.Config{ClientID: clientID})
}

// OAuth2 token endpoint helpers

func containsString(slice []string, s string) bool {
	for _, v := range slice {
		if v == s {
			return true
		}
	}
	return false
}
