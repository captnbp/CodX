// Package web implements the CodX HTTP server: OIDC login/callback,
// profile picker, workspace start (with SSE status streaming), workspace
// proxy, and workspace restart/stop endpoints.
package web

import (
	"crypto/rand"
	"encoding/base64"
	"fmt"
	"net/http"
	"time"

	"github.com/captnbp/CodX/internal/config"
	"github.com/captnbp/CodX/internal/k8s"
	"github.com/captnbp/CodX/internal/oidc"
	"github.com/captnbp/CodX/internal/proxy"
	"github.com/captnbp/CodX/internal/session"
	profilev1 "github.com/captnbp/CodX/api/profile/v1"
)

// SessionCookieName is the name of the cookie storing the session ID.
const SessionCookieName = "codx-session"

// Server is the CodX HTTP server.
type Server struct {
	cfg          *config.Config
	auth         *oidc.Authenticator
	store        session.Store
	profiles     *k8s.ProfileStore
	workspaces   *k8s.WorkspaceManager
	proxyFactory ProxyFactory
}

// ProxyFactory creates a WorkspaceProxy for a given workspace FQDN.
type ProxyFactory func(workspaceFQDN string) (http.Handler, error)

// New creates a new web Server.
func New(
	cfg *config.Config,
	auth *oidc.Authenticator,
	store session.Store,
	profiles *k8s.ProfileStore,
	workspaces *k8s.WorkspaceManager,
	proxyFactory ProxyFactory,
) *Server {
	if proxyFactory == nil {
		proxyFactory = defaultProxyFactory(cfg)
	}

	return &Server{
		cfg:          cfg,
		auth:         auth,
		store:        store,
		profiles:     profiles,
		workspaces:   workspaces,
		proxyFactory: proxyFactory,
	}
}

// Handler returns an http.Handler with all routes registered.
func (s *Server) Handler() http.Handler {
	mux := http.NewServeMux()

	mux.HandleFunc("/healthz", s.handleHealth)
	mux.HandleFunc("/auth/login", s.handleLogin)
	mux.HandleFunc("/auth/callback", s.handleCallback)
	mux.HandleFunc("/auth/logout", s.handleLogout)
	mux.HandleFunc("/api/profiles", s.handleListProfiles)
	mux.HandleFunc("/api/workspace/start", s.handleStartWorkspace)
	mux.HandleFunc("/api/workspace/stop", s.handleStopWorkspace)
	mux.HandleFunc("/api/workspace/status", s.handleWorkspaceStatus)
	mux.HandleFunc("/user/", s.handleProxy)

	// Root serves the profile picker page.
	mux.HandleFunc("/", s.handleIndex)

	return s.withMiddleware(mux)
}

// withMiddleware wraps the handler with session retrieval and logging.
func (s *Server) withMiddleware(h http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// Skip auth for health check.
		if r.URL.Path == "/healthz" {
			h.ServeHTTP(w, r)
			return
		}

		// Skip auth for login and callback.
		if r.URL.Path == "/auth/login" || r.URL.Path == "/auth/callback" {
			h.ServeHTTP(w, r)
			return
		}

		// All other routes require a valid session.
		cookie, err := r.Cookie(SessionCookieName)
		if err != nil {
			http.Redirect(w, r, "/auth/login", http.StatusSeeOther)
			return
		}

		sess, err := s.store.Get(r.Context(), cookie.Value)
		if err != nil || sess.IsExpired(time.Now()) {
			// Clear the cookie and redirect to login.
			clearSessionCookie(w, s.cfg.InstanceName)
			http.Redirect(w, r, "/auth/login", http.StatusSeeOther)
			return
		}

		// Attach the session to the request context.
		r = r.WithContext(WithSession(r.Context(), sess))
		h.ServeHTTP(w, r)
	})
}

func (s *Server) handleHealth(w http.ResponseWriter, r *http.Request) {
	w.WriteHeader(http.StatusOK)
	fmt.Fprintln(w, "ok")
}

func (s *Server) handleIndex(w http.ResponseWriter, r *http.Request) {
	if r.URL.Path != "/" {
		http.NotFound(w, r)
		return
	}

	sess := SessionFromContext(r.Context())
	if sess == nil {
		http.Redirect(w, r, "/auth/login", http.StatusSeeOther)
		return
	}

	profiles := s.profiles.AllowedForGroups(sess.Groups)

	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	fmt.Fprintf(w, `<!DOCTYPE html>
<html>
<head><title>CodX</title></head>
<body>
<h1>CodX</h1>
<p>Welcome, %s!</p>
<h2>Available Workspaces</h2>
<ul>`, sess.Username)

	for _, p := range profiles {
		fmt.Fprintf(w, `<li><a href="#" onclick="startWorkspace('%s')">%s</a> — %s</li>`,
			p.Name, p.Spec.Title, p.Spec.Description)
	}

	fmt.Fprintf(w, `</ul>
<div id="status"></div>
<script>
function startWorkspace(profileName) {
    var evtSource = new EventSource("/api/workspace/start?profile=" + encodeURIComponent(profileName));
    var statusDiv = document.getElementById("status");
    statusDiv.innerHTML = "<p>Starting workspace...</p>";
    evtSource.addEventListener("step", function(e) {
        statusDiv.innerHTML += "<p>" + e.data + "</p>";
    });
    evtSource.addEventListener("ready", function(e) {
        statusDiv.innerHTML += "<p>Workspace ready! Redirecting...</p>";
        setTimeout(function() { window.location.href = e.data; }, 500);
        evtSource.close();
    });
    evtSource.addEventListener("error", function(e) {
        statusDiv.innerHTML += "<p style='color:red'>Error: " + e.data + "</p>";
        evtSource.close();
    });
}
</script>
</body>
</html>`)
}

func (s *Server) handleLogin(w http.ResponseWriter, r *http.Request) {
	state, err := randomState()
	if err != nil {
		http.Error(w, "internal error", http.StatusInternalServerError)
		return
	}

	// Store state in a short-lived cookie.
	http.SetCookie(w, &http.Cookie{
		Name:     "codx-state",
		Value:    state,
		Path:     "/",
		MaxAge:   300,
		HttpOnly: true,
		Secure:   true,
		SameSite: http.SameSiteLaxMode,
	})

	url := s.auth.AuthURL(state)
	http.Redirect(w, r, url, http.StatusSeeOther)
}

func (s *Server) handleCallback(w http.ResponseWriter, r *http.Request) {
	// Verify state.
	stateCookie, err := r.Cookie("codx-state")
	if err != nil {
		http.Error(w, "missing state cookie", http.StatusBadRequest)
		return
	}

	state := r.URL.Query().Get("state")
	if state == "" || state != stateCookie.Value {
		http.Error(w, "invalid state", http.StatusBadRequest)
		return
	}

	code := r.URL.Query().Get("code")
	if code == "" {
		http.Error(w, "missing code", http.StatusBadRequest)
		return
	}

	sess, err := s.auth.Exchange(r.Context(), code)
	if err != nil {
		http.Error(w, fmt.Sprintf("authentication failed: %v", err), http.StatusUnauthorized)
		return
	}

	// Save the session.
	if err := s.store.Save(r.Context(), sess); err != nil {
		http.Error(w, "session save failed", http.StatusInternalServerError)
		return
	}

	// Set the session cookie.
	setSessionCookie(w, sess.ID, s.cfg.InstanceName)

	// Clear the state cookie.
	http.SetCookie(w, &http.Cookie{
		Name:   "codx-state",
		Value:  "",
		Path:   "/",
		MaxAge: -1,
	})

	http.Redirect(w, r, "/", http.StatusSeeOther)
}

func (s *Server) handleLogout(w http.ResponseWriter, r *http.Request) {
	if cookie, err := r.Cookie(SessionCookieName); err == nil {
		_ = s.store.Delete(r.Context(), cookie.Value)
	}
	clearSessionCookie(w, s.cfg.InstanceName)
	http.Redirect(w, r, "/auth/login", http.StatusSeeOther)
}

func (s *Server) handleListProfiles(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}

	sess := SessionFromContext(r.Context())
	if sess == nil {
		http.Error(w, "unauthorized", http.StatusUnauthorized)
		return
	}

	profiles := s.profiles.AllowedForGroups(sess.Groups)

	type profileInfo struct {
		Name        string `json:"name"`
		Title       string `json:"title"`
		Description string `json:"description"`
	}

	infos := make([]profileInfo, 0, len(profiles))
	for _, p := range profiles {
		infos = append(infos, profileInfo{
			Name:        p.Name,
			Title:       p.Spec.Title,
			Description: p.Spec.Description,
		})
	}

	writeJSON(w, http.StatusOK, infos)
}

func (s *Server) handleStartWorkspace(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}

	sess := SessionFromContext(r.Context())
	if sess == nil {
		http.Error(w, "unauthorized", http.StatusUnauthorized)
		return
	}

	profileName := r.URL.Query().Get("profile")
	if profileName == "" {
		http.Error(w, "missing profile parameter", http.StatusBadRequest)
		return
	}

	// Verify the profile exists and is allowed for the user.
	profile := s.profiles.Get(profileName)
	if profile == nil {
		http.Error(w, "profile not found", http.StatusNotFound)
		return
	}

	if !isProfileAllowed(profile, sess.Groups) {
		http.Error(w, "profile not allowed", http.StatusForbidden)
		return
	}

	// Set up SSE.
	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-cache")
	w.Header().Set("Connection", "keep-alive")

	flusher, ok := w.(http.Flusher)
	if !ok {
		http.Error(w, "streaming not supported", http.StatusInternalServerError)
		return
	}

	sendStep := func(msg string) {
		fmt.Fprintf(w, "event: step\ndata: %s\n\n", msg)
		flusher.Flush()
	}

	sendError := func(msg string) {
		fmt.Fprintf(w, "event: error\ndata: %s\n\n", msg)
		flusher.Flush()
	}

	sendReady := func(url string) {
		fmt.Fprintf(w, "event: ready\ndata: %s\n\n", url)
		flusher.Flush()
	}

	// Create workspace objects.
	sendStep("Creating workspace objects...")
	steps, err := s.workspaces.EnsureWorkspace(r.Context(), profile, sess.Slug)
	if err != nil {
		sendError(fmt.Sprintf("Failed to create workspace: %v", err))
		return
	}

	for _, step := range steps {
		sendStep(step.Message)
	}

	// Wait for the certificate to be ready.
	sendStep("Waiting for TLS certificate...")
	if err := s.workspaces.WaitForCertificate(r.Context(), sess.Slug, 5*time.Second); err != nil {
		sendError(fmt.Sprintf("Certificate not ready: %v", err))
		return
	}
	sendStep("TLS certificate is ready.")

	// Wait for the pod to be ready.
	sendStep("Waiting for workspace pod to be ready...")
	// TODO(Phase 5): poll pod readiness. For now, we assume the pod is
	// ready once the certificate is issued and the objects are created.
	// In Phase 5 we'll add a WaitForPodReady method.

	workspaceURL := fmt.Sprintf("/user/%s/", sess.Slug)
	sendStep("Workspace is ready!")
	sendReady(workspaceURL)
}

func (s *Server) handleStopWorkspace(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}

	sess := SessionFromContext(r.Context())
	if sess == nil {
		http.Error(w, "unauthorized", http.StatusUnauthorized)
		return
	}

	if err := s.workspaces.StopWorkspace(r.Context(), sess.Slug); err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{
			"error": fmt.Sprintf("failed to stop workspace: %v", err),
		})
		return
	}

	writeJSON(w, http.StatusOK, map[string]string{"status": "stopped"})
}

func (s *Server) handleWorkspaceStatus(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}

	sess := SessionFromContext(r.Context())
	if sess == nil {
		http.Error(w, "unauthorized", http.StatusUnauthorized)
		return
	}

	// Check if the workspace pod exists.
	// This is a simplified status check; in Phase 5 we'll expand it.
	writeJSON(w, http.StatusOK, map[string]any{
		"username": sess.Username,
		"slug":     sess.Slug,
	})
}

func (s *Server) handleProxy(w http.ResponseWriter, r *http.Request) {
	sess := SessionFromContext(r.Context())
	if sess == nil {
		http.Error(w, "unauthorized", http.StatusUnauthorized)
		return
	}

	// Extract the slug from the path: /user/<slug>/...
	path := r.URL.Path
	if len(path) < len("/user/") {
		http.NotFound(w, r)
		return
	}
	rest := path[len("/user/"):]
	slash := indexByte(rest, '/')
	if slash < 0 {
		http.NotFound(w, r)
		return
	}
	pathSlug := rest[:slash]

	// Only allow the user to access their own workspace.
	if pathSlug != sess.Slug {
		http.Error(w, "forbidden", http.StatusForbidden)
		return
	}

	// Strip the /user/<slug>/ prefix and forward to the workspace.
	r.URL.Path = "/" + rest[slash+1:]
	if r.URL.Path == "/" {
		r.URL.Path = "/"
	}

	fqdn := k8s.ServiceFQDN(s.cfg.InstanceName, sess.Slug, s.cfg.Namespace)
	p, err := s.proxyFactory(fqdn)
	if err != nil {
		http.Error(w, fmt.Sprintf("proxy error: %v", err), http.StatusBadGateway)
		return
	}

	p.ServeHTTP(w, r)
}

// defaultProxyFactory returns a ProxyFactory that creates WorkspaceProxy
// instances using the TLS configuration from cfg.
func defaultProxyFactory(cfg *config.Config) ProxyFactory {
	tlsCfg := proxy.DefaultTLSConfig(cfg.TLS.ServerCertDir, cfg.TLS.ClientCertDir, cfg.TLS.CADir)
	return func(workspaceFQDN string) (http.Handler, error) {
		return proxy.New(workspaceFQDN, tlsCfg)
	}
}

// isProfileAllowed checks whether any of the user's groups match the profile's
// allowed groups. An empty OIDCGroups list means all authenticated users.
func isProfileAllowed(profile *profilev1.Profile, userGroups []string) bool {
	if len(profile.Spec.OIDCGroups) == 0 {
		return true
	}
	for _, allowed := range profile.Spec.OIDCGroups {
		for _, user := range userGroups {
			if allowed == user {
				return true
			}
		}
	}
	return false
}

func randomState() (string, error) {
	b := make([]byte, 32)
	if _, err := rand.Read(b); err != nil {
		return "", err
	}
	return base64.RawURLEncoding.EncodeToString(b), nil
}

func setSessionCookie(w http.ResponseWriter, sessionID, instance string) {
	http.SetCookie(w, &http.Cookie{
		Name:     SessionCookieName,
		Value:    sessionID,
		Path:     "/",
		MaxAge:   86400, // 24 hours
		HttpOnly: true,
		Secure:   true,
		SameSite: http.SameSiteLaxMode,
	})
}

func clearSessionCookie(w http.ResponseWriter, instance string) {
	http.SetCookie(w, &http.Cookie{
		Name:   SessionCookieName,
		Value:  "",
		Path:   "/",
		MaxAge: -1,
	})
}

func indexByte(s string, b byte) int {
	for i := 0; i < len(s); i++ {
		if s[i] == b {
			return i
		}
	}
	return -1
}
