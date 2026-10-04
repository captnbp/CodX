package web

import (
	"fmt"
	"net/http"
	"strings"

	"github.com/captnbp/CodX/internal/k8s"
	"github.com/captnbp/CodX/internal/session"
)

// requireAdmin checks if the session user is an admin. Returns false and writes
// an error response if not.
func (s *Server) requireAdmin(w http.ResponseWriter, r *http.Request) bool {
	sess := SessionFromContext(r.Context())
	if sess == nil {
		http.Error(w, "unauthorized", http.StatusUnauthorized)
		return false
	}
	if !sess.IsAdmin {
		s.audit.Info("admin_access_denied", "user", sess.Username, "path", r.URL.Path, "remote", clientIP(r))
		http.Error(w, "forbidden: admin role required", http.StatusForbidden)
		return false
	}
	return true
}

func (s *Server) handleRestartWorkspace(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}

	sess := SessionFromContext(r.Context())
	if sess == nil {
		http.Error(w, "unauthorized", http.StatusUnauthorized)
		return
	}

	if err := s.workspaces.RestartWorkspace(r.Context(), sess.Slug); err != nil {
		s.audit.Info("workspace_restart_failed", "user", sess.Username, "slug", sess.Slug, "error", err)
		writeJSON(w, http.StatusInternalServerError, map[string]string{
			"error": fmt.Sprintf("failed to restart workspace: %v", err),
		})
		return
	}

	s.audit.Info("workspace_restart", "user", sess.Username, "slug", sess.Slug)
	writeJSON(w, http.StatusOK, map[string]string{"status": "restarting"})
}

func (s *Server) handleAdminUI(w http.ResponseWriter, r *http.Request) {
	if !s.requireAdmin(w, r) {
		return
	}

	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	fmt.Fprintf(w, `<!DOCTYPE html>
<html lang="en">
<head>
<meta charset="utf-8">
<meta name="viewport" content="width=device-width, initial-scale=1">
<title>CodX Admin</title>
<link href="https://cdn.jsdelivr.net/npm/bootstrap@5.3.3/dist/css/bootstrap.min.css" rel="stylesheet" integrity="sha384-QWTKZyjpPEjISv5WaRU9OFeRpok6YctnYmDr5pNlyT2bRjXh0JMhjY6hW+ALEwIH" crossorigin="anonymous">
</head>
<body class="bg-light">
<nav class="navbar navbar-expand-lg navbar-dark bg-dark mb-4">
  <div class="container">
    <span class="navbar-brand mb-0 h1">CodX Admin</span>
    <div class="d-flex">
      <a href="/" class="btn btn-outline-light btn-sm">Back</a>
    </div>
  </div>
</nav>
<div class="container">
  <h2 class="mb-3">User Workspaces</h2>
  <div class="table-responsive">
    <table class="table table-striped table-hover align-middle">
      <thead class="table-dark">
        <tr><th>Username</th><th>Slug</th><th>Online</th><th>Actions</th></tr>
      </thead>
      <tbody id="users"></tbody>
    </table>
  </div>
</div>
<script>
function loadUsers() {
    fetch("/api/admin/users").then(r => r.json()).then(users => {
        var tbody = document.querySelector("#users");
        tbody.innerHTML = "";
        users.forEach(u => {
            var tr = document.createElement("tr");
            var onlineBadge = u.online
                ? '<span class="badge text-bg-success">online</span>'
                : '<span class="badge text-bg-secondary">offline</span>';
            tr.innerHTML = "<td>" + u.username + "</td>" +
                "<td><code>" + u.slug + "</code></td>" +
                "<td>" + onlineBadge + "</td>" +
                "<td>" +
                '<button class="btn btn-sm btn-outline-primary me-1" onclick="extendPVC(\'' + u.slug + '\')">Extend PVC</button>' +
                '<button class="btn btn-sm btn-outline-warning me-1" onclick="stopWorkspace(\'' + u.slug + '\')">Stop</button>' +
                '<button class="btn btn-sm btn-outline-danger" onclick="deleteUser(\'' + u.slug + '\')">Delete</button>' +
                "</td>";
            tbody.appendChild(tr);
        });
    });
}
function extendPVC(slug) {
    var size = prompt("New PVC size (e.g. 50Gi):");
    if (!size) return;
    fetch("/api/admin/users/" + slug + "/extend?pvcSize=" + encodeURIComponent(size), {method: "POST"})
        .then(r => r.json()).then(function(d){ alert(JSON.stringify(d)); }).then(loadUsers);
}
function stopWorkspace(slug) {
    fetch("/api/admin/users/" + slug + "/stop", {method: "POST"})
        .then(r => r.json()).then(function(d){ alert(JSON.stringify(d)); }).then(loadUsers);
}
function deleteUser(slug) {
    if (!confirm("Delete workspace for " + slug + "? This removes all data.")) return;
    fetch("/api/admin/users/" + slug + "/delete", {method: "POST"})
        .then(r => r.json()).then(function(d){ alert(JSON.stringify(d)); }).then(loadUsers);
}
loadUsers();
</script>
<script src="https://cdn.jsdelivr.net/npm/bootstrap@5.3.3/dist/js/bootstrap.bundle.min.js" integrity="sha384-YvpcrYf0tY3lHB60NNkmXc5s9fDVZLESaAA55NDzOxhy9GkcIdslK1eN7N6jIeHz" crossorigin="anonymous"></script>
</body>
</html>`)
}

func (s *Server) handleAdminListUsers(w http.ResponseWriter, r *http.Request) {
	if !s.requireAdmin(w, r) {
		return
	}

	if r.Method != http.MethodGet {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}

	// List all sessions from the store to discover known users.
	// In production this would query the K8s API for all workspace objects
	// (pods, PVCs, certificates) labeled with the CodX component=workspace.
	// For now, we list pods labeled by the instance.
	users := s.listWorkspaceUsers(r)

	writeJSON(w, http.StatusOK, users)
}

// userInfo represents a workspace user in the admin list.
type userInfo struct {
	Username     string `json:"username"`
	Slug         string `json:"slug"`
	Online       bool   `json:"online"`
	LastActivity string `json:"lastActivity,omitempty"`
}

func (s *Server) listWorkspaceUsers(r *http.Request) []userInfo {
	// Query K8s for all pods labeled with component=workspace and managed-by=<instance>.
	svcList, err := s.workspaces.ListWorkspaceServices(r.Context())
	if err != nil {
		return []userInfo{}
	}

	users := make([]userInfo, 0, len(svcList))
	for _, svc := range svcList {
		slug := svc.Labels[k8s.LabelInstance]
		if slug == "" {
			continue
		}
		online := s.workspaces.IsWorkspaceRunning(r.Context(), slug)
		users = append(users, userInfo{
			Username: slug,
			Slug:     slug,
			Online:   online,
		})
	}
	return users
}

func (s *Server) handleAdminUserAction(w http.ResponseWriter, r *http.Request) {
	if !s.requireAdmin(w, r) {
		return
	}

	// Path: /api/admin/users/<slug>/<action>
	path := strings.TrimPrefix(r.URL.Path, "/api/admin/users/")
	parts := strings.SplitN(path, "/", 2)
	if len(parts) != 2 {
		http.Error(w, "invalid path", http.StatusBadRequest)
		return
	}
	slug := parts[0]
	action := parts[1]
	adminUser := SessionFromContext(r.Context()).Username

	switch action {
	case "stop":
		if r.Method != http.MethodPost {
			http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
			return
		}
		if err := s.workspaces.StopWorkspace(r.Context(), slug); err != nil {
			s.audit.Info("workspace_stop_failed", "user", adminUser, "target", slug, "error", err)
			writeJSON(w, http.StatusInternalServerError, map[string]string{
				"error": fmt.Sprintf("failed to stop workspace: %v", err),
			})
			return
		}
		s.audit.Info("workspace_stop", "user", adminUser, "target", slug)
		writeJSON(w, http.StatusOK, map[string]string{"status": "stopped"})

	case "delete":
		if r.Method != http.MethodPost {
			http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
			return
		}
		if err := s.workspaces.DeleteWorkspace(r.Context(), slug); err != nil {
			s.audit.Info("workspace_delete_failed", "user", adminUser, "target", slug, "error", err)
			writeJSON(w, http.StatusInternalServerError, map[string]string{
				"error": fmt.Sprintf("failed to delete workspace: %v", err),
			})
			return
		}
		// Also delete the user's session if it exists.
		_ = s.store.Delete(r.Context(), "session-"+slug)
		s.audit.Info("workspace_delete", "user", adminUser, "target", slug)
		writeJSON(w, http.StatusOK, map[string]string{"status": "deleted"})

	case "extend":
		if r.Method != http.MethodPost {
			http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
			return
		}
		newSize := r.URL.Query().Get("pvcSize")
		if newSize == "" {
			http.Error(w, "missing pvcSize parameter", http.StatusBadRequest)
			return
		}
		if err := s.workspaces.ExtendPVC(r.Context(), slug, newSize); err != nil {
			s.audit.Info("pvc_extend_failed", "user", adminUser, "target", slug, "size", newSize, "error", err)
			writeJSON(w, http.StatusInternalServerError, map[string]string{
				"error": fmt.Sprintf("failed to extend PVC: %v", err),
			})
			return
		}
		s.audit.Info("pvc_extend", "user", adminUser, "target", slug, "size", newSize)
		writeJSON(w, http.StatusOK, map[string]string{"status": "extended", "newSize": newSize})

	default:
		http.Error(w, "unknown action", http.StatusBadRequest)
	}
}

// Ensure session is used for admin handlers.
var _ = session.Session{}
