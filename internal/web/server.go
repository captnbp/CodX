// Package web implements the CodX HTTP server: OIDC login/callback,
// profile picker, workspace start (with SSE status streaming), workspace
// proxy, and workspace restart/stop endpoints.
package web

import (
	"crypto/rand"
	"encoding/base64"
	"fmt"
	"html"
	"net/http"
	"time"

	profilev1 "github.com/captnbp/CodX/api/profile/v1"
	"github.com/captnbp/CodX/internal/config"
	"github.com/captnbp/CodX/internal/k8s"
	"github.com/captnbp/CodX/internal/oidc"
	"github.com/captnbp/CodX/internal/proxy"
	"github.com/captnbp/CodX/internal/session"
	"github.com/go-logr/logr"
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
	log          logr.Logger
	audit        logr.Logger
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
		log:          logr.Discard(),
		audit:        logr.Discard(),
	}
}

// WithLogger sets the structured logger used by the server for request and
// profile resolution logging. The audit logger follows it under the name
// "audit"; use WithAuditLogger to send audit events to a dedicated sink.
func (s *Server) WithLogger(log logr.Logger) *Server {
	s.log = log.WithName("web")
	s.audit = log.WithName("audit")
	return s
}

// WithAuditLogger overrides the audit logger, e.g. to write security-relevant
// events (login, logout, workspace lifecycle, admin actions, denied access)
// to a dedicated output.
func (s *Server) WithAuditLogger(log logr.Logger) *Server {
	s.audit = log.WithName("audit")
	return s
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
	mux.HandleFunc("/api/workspace/restart", s.handleRestartWorkspace)
	mux.HandleFunc("/api/workspace/status", s.handleWorkspaceStatus)
	mux.HandleFunc("/api/admin/users", s.handleAdminListUsers)
	mux.HandleFunc("/api/admin/users/", s.handleAdminUserAction)
	mux.HandleFunc("/admin", s.handleAdminUI)
	mux.HandleFunc("/profile", s.handleProfile)
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

	s.log.Info("serving profile picker",
		"user", sess.Username,
		"groups", sess.Groups,
		"cachedProfiles", s.profiles.Count(),
		"allowedProfiles", len(profiles),
	)

	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	fmt.Fprintf(w, `<!DOCTYPE html>
<html lang="en">
<head>
<meta charset="utf-8">
<meta name="viewport" content="width=device-width, initial-scale=1">
<title>CodX</title>
<link href="https://cdn.jsdelivr.net/npm/bootstrap@5.3.3/dist/css/bootstrap.min.css" rel="stylesheet" integrity="sha384-QWTKZyjpPEjISv5WaRU9OFeRpok6YctnYmDr5pNlyT2bRjXh0JMhjY6hW+ALEwIH" crossorigin="anonymous">
</head>
<body class="bg-light">
<nav class="navbar navbar-expand-lg navbar-dark bg-dark mb-4">
  <div class="container">
    <span class="navbar-brand mb-0 h1">CodX</span>
    <div class="d-flex">
      <a href="/profile" class="navbar-text text-light me-3 text-decoration-none">Signed in as <strong>%s</strong></a>
%s      <a href="/auth/logout" class="btn btn-outline-light btn-sm">Logout</a>
    </div>
  </div>
</nav>
<div class="container">
  <div class="row g-4">
    <div class="col-lg-8">
      <h2 class="mb-3">Available Workspaces</h2>
      <div class="row row-cols-1 row-cols-md-2 g-3">`, html.EscapeString(sess.Username), adminNavLink(sess.IsAdmin))

	for _, p := range profiles {
		fmt.Fprintf(w, `<div class="col">
        <div class="card h-100">
          <div class="card-body">
            <h5 class="card-title">%s</h5>
            <p class="card-text text-muted">%s</p>
            <button class="btn btn-primary" onclick="startWorkspace('%s')">Start</button>
          </div>
        </div>
      </div>`,
			html.EscapeString(p.Spec.Title), html.EscapeString(p.Spec.Description), html.EscapeString(p.Name))
	}

	fmt.Fprintf(w, `</div>
    </div>
    <div class="col-lg-4">
      <h2 class="mb-3">Workspace</h2>
      <div class="d-grid gap-2">
        <button class="btn btn-danger" onclick="stopWorkspace()">Stop my workspace</button>
      </div>
      <div id="status" class="mt-3"></div>
    </div>
  </div>
</div>
<script>
function startWorkspace(profileName) {
    var evtSource = new EventSource("/api/workspace/start?profile=" + encodeURIComponent(profileName));
    var statusDiv = document.getElementById("status");
    statusDiv.innerHTML = '<div class="alert alert-info">Starting workspace...</div>';
    evtSource.addEventListener("step", function(e) {
        statusDiv.innerHTML += '<div class="alert alert-secondary">' + e.data + '</div>';
    });
    evtSource.addEventListener("ready", function(e) {
        statusDiv.innerHTML += '<div class="alert alert-success">Workspace ready! Redirecting...</div>';
        setTimeout(function() { window.location.href = e.data; }, 500);
        evtSource.close();
    });
    evtSource.addEventListener("error", function(e) {
        statusDiv.innerHTML += '<div class="alert alert-danger">Error: ' + e.data + '</div>';
        evtSource.close();
    });
}
function stopWorkspace() {
    var statusDiv = document.getElementById("status");
    statusDiv.innerHTML = '<div class="alert alert-info">Stopping workspace...</div>';
    fetch("/api/workspace/stop", { method: "POST" })
        .then(function(r) { return r.json(); })
        .then(function(data) {
            if (data.status === "stopped") {
                statusDiv.innerHTML += '<div class="alert alert-success">Workspace stopped.</div>';
            } else {
                statusDiv.innerHTML += '<div class="alert alert-danger">Error: ' + (data.error || "unknown error") + '</div>';
            }
        })
        .catch(function(err) {
            statusDiv.innerHTML += '<div class="alert alert-danger">Error: ' + err + '</div>';
        });
}
</script>
<script src="https://cdn.jsdelivr.net/npm/bootstrap@5.3.3/dist/js/bootstrap.bundle.min.js" integrity="sha384-YvpcrYf0tY3lHB60NNkmXc5s9fDVZLESaAA55NDzOxhy9GkcIdslK1eN7N6jIeHz" crossorigin="anonymous"></script>
</body>
</html>`)
}

func (s *Server) handleProfile(w http.ResponseWriter, r *http.Request) {
	sess := SessionFromContext(r.Context())
	if sess == nil {
		http.Redirect(w, r, "/auth/login", http.StatusSeeOther)
		return
	}

	s.log.V(1).Info("serving profile page",
		"user", sess.Username,
		"groups", sess.Groups,
		"admin", sess.IsAdmin,
	)

	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	fmt.Fprintf(w, `<!DOCTYPE html>
<html lang="en">
<head>
<meta charset="utf-8">
<meta name="viewport" content="width=device-width, initial-scale=1">
<title>CodX - Profile</title>
<link href="https://cdn.jsdelivr.net/npm/bootstrap@5.3.3/dist/css/bootstrap.min.css" rel="stylesheet" integrity="sha384-QWTKZyjpPEjISv5WaRU9OFeRpok6YctnYmDr5pNlyT2bRjXh0JMhjY6hW+ALEwIH" crossorigin="anonymous">
</head>
<body class="bg-light">
<nav class="navbar navbar-expand-lg navbar-dark bg-dark mb-4">
  <div class="container">
    <span class="navbar-brand mb-0 h1">CodX</span>
    <div class="d-flex">
      <a href="/profile" class="navbar-text text-light me-3 text-decoration-none">Signed in as <strong>%s</strong></a>
      <a href="/" class="btn btn-outline-light btn-sm me-2">Workspaces</a>
%s      <a href="/auth/logout" class="btn btn-outline-light btn-sm">Logout</a>
    </div>
  </div>
</nav>
<div class="container">
  <div class="row g-4">
    <div class="col-lg-8">
      <h2 class="mb-3">Profile</h2>
      <div class="card">
        <div class="card-body">
          <p class="mb-2"><strong>Username:</strong> %s</p>
          <p class="mb-2"><strong>Subject:</strong> <code>%s</code></p>
          <p class="mb-2"><strong>Workspace slug:</strong> <code>%s</code></p>
          <p class="mb-0"><strong>Admin:</strong> %s</p>
        </div>
      </div>
    </div>
    <div class="col-lg-4">
      <h2 class="mb-3">OIDC Groups</h2>
      <div class="card">
        <div class="card-body">
`, html.EscapeString(sess.Username),
		adminNavLink(sess.IsAdmin),
		html.EscapeString(sess.Username),
		html.EscapeString(sess.Subject),
		html.EscapeString(sess.Slug),
		groupBadge(sess.IsAdmin))

	for _, g := range sess.Groups {
		fmt.Fprintf(w, `          <span class="badge text-bg-primary me-1 mb-1">%s</span>`,
			html.EscapeString(g))
	}

	if len(sess.Groups) == 0 {
		fmt.Fprintf(w, `          <p class="text-muted mb-0">No groups assigned.</p>`)
	}

	fmt.Fprintf(w, `
        </div>
      </div>
    </div>
  </div>
</div>
<script src="https://cdn.jsdelivr.net/npm/bootstrap@5.3.3/dist/js/bootstrap.bundle.min.js" integrity="sha384-YvpcrYf0tY3lHB60NNkmXc5s9fDVZLESaAA55NDzOxhy9GkcIdslK1eN7N6jIeHz" crossorigin="anonymous"></script>
</body>
</html>`)
}

// groupBadge returns a Bootstrap badge indicating whether the user is an admin.
func groupBadge(isAdmin bool) string {
	if isAdmin {
		return `<span class="badge text-bg-success">yes</span>`
	}
	return `<span class="badge text-bg-secondary">no</span>`
}

// adminNavLink returns a navbar link to the admin page, shown only for admins.
func adminNavLink(isAdmin bool) string {
	if isAdmin {
		return `<a href="/admin" class="btn btn-outline-warning btn-sm me-2">Admin</a>`
	}
	return ""
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
		s.audit.Info("login_failed", "reason", "invalid state", "remote", clientIP(r))
		http.Error(w, "invalid state", http.StatusBadRequest)
		return
	}

	code := r.URL.Query().Get("code")
	if code == "" {
		s.audit.Info("login_failed", "reason", "missing code", "remote", clientIP(r))
		http.Error(w, "missing code", http.StatusBadRequest)
		return
	}

	sess, err := s.auth.Exchange(r.Context(), code)
	if err != nil {
		s.audit.Info("login_failed", "reason", "token exchange", "error", err, "remote", clientIP(r))
		http.Error(w, fmt.Sprintf("authentication failed: %v", err), http.StatusUnauthorized)
		return
	}

	// Save the session.
	if err := s.store.Save(r.Context(), sess); err != nil {
		s.audit.Info("session_save_failed", "user", sess.Username, "error", err, "remote", clientIP(r))
		http.Error(w, "session save failed", http.StatusInternalServerError)
		return
	}

	s.audit.Info("login",
		"user", sess.Username,
		"slug", sess.Slug,
		"groups", sess.Groups,
		"admin", sess.IsAdmin,
		"remote", clientIP(r),
	)

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
	var user string
	if cookie, err := r.Cookie(SessionCookieName); err == nil {
		// Best-effort lookup so the audit log records who logged out.
		if sess, err := s.store.Get(r.Context(), cookie.Value); err == nil {
			user = sess.Username
		}
		_ = s.store.Delete(r.Context(), cookie.Value)
	}
	clearSessionCookie(w, s.cfg.InstanceName)
	s.audit.Info("logout", "user", user, "remote", clientIP(r))
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

	s.log.V(1).Info("list profiles",
		"user", sess.Username,
		"groups", sess.Groups,
		"cachedProfiles", s.profiles.Count(),
		"allowedProfiles", len(profiles),
	)

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
		s.audit.Info("workspace_start_denied", "user", sess.Username, "profile", profileName, "reason", "profile not found", "remote", clientIP(r))
		http.Error(w, "profile not found", http.StatusNotFound)
		return
	}

	if !isProfileAllowed(profile, sess.Groups) {
		s.audit.Info("workspace_start_denied", "user", sess.Username, "profile", profileName, "reason", "profile not allowed", "remote", clientIP(r))
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
		s.audit.Info("workspace_start_failed", "user", sess.Username, "slug", sess.Slug, "profile", profileName, "error", err)
		sendError(fmt.Sprintf("Failed to create workspace: %v", err))
		return
	}

	for _, step := range steps {
		sendStep(step.Message)
	}

	// Wait for the certificate to be ready.
	sendStep("Waiting for TLS certificate...")
	if err := s.workspaces.WaitForCertificate(r.Context(), sess.Slug, 5*time.Second); err != nil {
		s.audit.Info("workspace_start_failed", "user", sess.Username, "slug", sess.Slug, "profile", profileName, "error", err)
		sendError(fmt.Sprintf("Certificate not ready: %v", err))
		return
	}
	sendStep("TLS certificate is ready.")

	// Wait for the pod to be ready.
	sendStep("Waiting for workspace pod to be ready...")
	if err := s.workspaces.WaitForPodReady(r.Context(), sess.Slug, 5*time.Second); err != nil {
		s.audit.Info("workspace_start_failed", "user", sess.Username, "slug", sess.Slug, "profile", profileName, "error", err)
		sendError(fmt.Sprintf("Pod not ready: %v", err))
		return
	}
	sendStep("Workspace pod is ready.")

	s.audit.Info("workspace_start", "user", sess.Username, "slug", sess.Slug, "profile", profileName)

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
		s.audit.Info("workspace_stop_failed", "user", sess.Username, "slug", sess.Slug, "error", err)
		writeJSON(w, http.StatusInternalServerError, map[string]string{
			"error": fmt.Sprintf("failed to stop workspace: %v", err),
		})
		return
	}

	s.audit.Info("workspace_stop", "user", sess.Username, "slug", sess.Slug)
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
		s.audit.Info("proxy_access_denied", "user", sess.Username, "target", pathSlug, "remote", clientIP(r))
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

// clientIP returns the client address for the audit log: the first
// X-Forwarded-For entry when set (CodX runs behind an ingress), otherwise the
// request RemoteAddr.
func clientIP(r *http.Request) string {
	if xff := r.Header.Get("X-Forwarded-For"); xff != "" {
		if i := indexByte(xff, ','); i >= 0 {
			return xff[:i]
		}
		return xff
	}
	return r.RemoteAddr
}
