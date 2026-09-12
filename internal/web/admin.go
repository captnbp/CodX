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
		writeJSON(w, http.StatusInternalServerError, map[string]string{
			"error": fmt.Sprintf("failed to restart workspace: %v", err),
		})
		return
	}

	writeJSON(w, http.StatusOK, map[string]string{"status": "restarting"})
}

func (s *Server) handleAdminUI(w http.ResponseWriter, r *http.Request) {
	if !s.requireAdmin(w, r) {
		return
	}

	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	fmt.Fprintf(w, `<!DOCTYPE html>
<html>
<head><title>CodX Admin</title></head>
<body>
<h1>CodX Admin</h1>
<h2>User Workspaces</h2>
<table id="users" border="1">
<tr><th>Username</th><th>Slug</th><th>Online</th><th>Actions</th></tr>
</table>
<script>
function loadUsers() {
    fetch("/api/admin/users").then(r => r.json()).then(users => {
        var tbody = document.querySelector("#users");
        users.forEach(u => {
            var tr = document.createElement("tr");
            tr.innerHTML = "<td>" + u.username + "</td>" +
                "<td>" + u.slug + "</td>" +
                "<td>" + (u.online ? "yes" : "no") + "</td>" +
                "<td>" +
                "<button onclick=\"extendPVC('" + u.slug + "')\">Extend PVC</button> " +
                "<button onclick=\"stopWorkspace('" + u.slug + "')\">Stop</button> " +
                "<button onclick=\"deleteUser('" + u.slug + "')\">Delete</button>" +
                "</td>";
            tbody.appendChild(tr);
        });
    });
}
function extendPVC(slug) {
    var size = prompt("New PVC size (e.g. 50Gi):");
    if (!size) return;
    fetch("/api/admin/users/" + slug + "/extend?pvcSize=" + encodeURIComponent(size), {method: "POST"})
        .then(r => r.json()).then(alert(JSON.stringify(r))).then(loadUsers);
}
function stopWorkspace(slug) {
    fetch("/api/admin/users/" + slug + "/stop", {method: "POST"})
        .then(r => r.json()).then(alert(JSON.stringify(r))).then(loadUsers);
}
function deleteUser(slug) {
    if (!confirm("Delete workspace for " + slug + "? This removes all data.")) return;
    fetch("/api/admin/users/" + slug + "/delete", {method: "POST"})
        .then(r => r.json()).then(alert(JSON.stringify(r))).then(loadUsers);
}
loadUsers();
</script>
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

	switch action {
	case "stop":
		if r.Method != http.MethodPost {
			http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
			return
		}
		if err := s.workspaces.StopWorkspace(r.Context(), slug); err != nil {
			writeJSON(w, http.StatusInternalServerError, map[string]string{
				"error": fmt.Sprintf("failed to stop workspace: %v", err),
			})
			return
		}
		writeJSON(w, http.StatusOK, map[string]string{"status": "stopped"})

	case "delete":
		if r.Method != http.MethodPost {
			http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
			return
		}
		if err := s.workspaces.DeleteWorkspace(r.Context(), slug); err != nil {
			writeJSON(w, http.StatusInternalServerError, map[string]string{
				"error": fmt.Sprintf("failed to delete workspace: %v", err),
			})
			return
		}
		// Also delete the user's session if it exists.
		_ = s.store.Delete(r.Context(), "session-"+slug)
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
			writeJSON(w, http.StatusInternalServerError, map[string]string{
				"error": fmt.Sprintf("failed to extend PVC: %v", err),
			})
			return
		}
		writeJSON(w, http.StatusOK, map[string]string{"status": "extended", "newSize": newSize})

	default:
		http.Error(w, "unknown action", http.StatusBadRequest)
	}
}

// Ensure session is used for admin handlers.
var _ = session.Session{}
