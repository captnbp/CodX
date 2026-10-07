package web

import (
	"fmt"
	"net/http"
	"strings"
	"time"

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
        <tr><th>Username</th><th>Slug</th><th>Online</th><th>Created</th><th>Last login</th><th>CPU</th><th>RAM</th><th>Actions</th></tr>
      </thead>
      <tbody id="users"></tbody>
    </table>
  </div>
</div>
<div class="modal fade" id="logsModal" tabindex="-1">
  <div class="modal-dialog modal-xl modal-dialog-scrollable">
    <div class="modal-content">
      <div class="modal-header">
        <h5 class="modal-title" id="logsTitle">Workspace logs</h5>
        <button type="button" class="btn-close" data-bs-dismiss="modal"></button>
      </div>
      <div class="modal-body">
        <pre id="logsPre" class="bg-dark text-light p-3 rounded" style="max-height: 60vh; overflow-y: scroll; font-size: 0.8rem;"></pre>
      </div>
    </div>
  </div>
</div>
<script>
function formatBytes(b) {
    if (b < 1024) { return b + " B"; }
    var units = ["KiB", "MiB", "GiB", "TiB"];
    var u = -1;
    do { b = b / 1024; u++; } while (b >= 1024 && u < units.length - 1);
    return b.toFixed(1) + " " + units[u];
}
function formatCores(c) {
    if (c === 0) { return "0"; }
    if (c < 1) { return Math.round(c * 1000) + "m"; }
    return c.toFixed(2);
}
function resourceBar(used, limit, request, fmt) {
    var pct = 0;
    var limitText = "no limit";
    if (limit > 0) {
        pct = Math.min(100, Math.round(used / limit * 100));
        limitText = fmt(limit);
    }
    var color = pct > 90 ? "bg-danger" : (pct > 75 ? "bg-warning" : "bg-success");
    var html = '<div style="min-width: 120px;"><div class="d-flex justify-content-between"><span>' + fmt(used) + '</span><span class="text-muted">' + limitText + '</span></div>';
    html += '<div class="progress" style="height: 6px;"><div class="progress-bar ' + color + '" style="width: ' + pct + '%%"></div></div>';
    if (request > 0) {
        html += '<div class="text-muted" style="font-size: 0.75rem;">request: ' + fmt(request) + '</div>';
    }
    html += '</div>';
    return html;
}
function loadUsers() {
    fetch("/api/admin/users").then(r => r.json()).then(users => {
        var tbody = document.querySelector("#users");
        tbody.innerHTML = "";
        users.forEach(u => {
            var tr = document.createElement("tr");
            var onlineBadge = u.online
                ? '<span class="badge text-bg-success">online</span>'
                : '<span class="badge text-bg-secondary">offline</span>';
            var created = u.createdAt
                ? new Date(u.createdAt).toLocaleString()
                : '<span class="text-muted">unknown</span>';
            var lastLogin = u.lastLogin
                ? new Date(u.lastLogin).toLocaleString()
                : '<span class="text-muted">never</span>';
            var usage = u.usage ? usageCell(u.usage) : '<span class="text-muted">&mdash;</span>';
            var logsBtn = u.online
                ? '<button class="btn btn-sm btn-outline-secondary me-1" onclick="openLogs(\'' + u.slug + '\')">Logs</button>'
                : '';
            tr.innerHTML = "<td>" + u.username + "</td>" +
                "<td><code>" + u.slug + "</code></td>" +
                "<td>" + onlineBadge + "</td>" +
                "<td>" + created + "</td>" +
                "<td>" + lastLogin + "</td>" +
                "<td>" + (u.usage ? resourceBar(u.usage.cpu.usedCores, u.usage.cpu.limitCores, u.usage.cpu.requestCores, formatCores) : '<span class="text-muted">&mdash;</span>') + "</td>" +
                "<td>" + (u.usage ? resourceBar(u.usage.memory.usedBytes, u.usage.memory.limitBytes, u.usage.memory.requestBytes, formatBytes) : '<span class="text-muted">&mdash;</span>') + "</td>" +
                "<td>" +
                '<button class="btn btn-sm btn-outline-primary me-1" onclick="extendPVC(\'' + u.slug + '\')">Extend PVC</button>' +
                logsBtn +
                '<button class="btn btn-sm btn-outline-warning me-1" onclick="stopWorkspace(\'' + u.slug + '\')">Stop</button>' +
                '<button class="btn btn-sm btn-outline-danger" onclick="deleteUser(\'' + u.slug + '\')">Delete</button>' +
                "</td>";
            tbody.appendChild(tr);
        });
    });
}
var logsEvtSource = null;
function closeLogs() {
    if (logsEvtSource) { logsEvtSource.close(); logsEvtSource = null; }
}
function openLogs(slug) {
    document.getElementById("logsTitle").textContent = "Workspace logs: " + slug;
    var pre = document.getElementById("logsPre");
    pre.textContent = "";
    closeLogs();
    logsEvtSource = new EventSource("/api/admin/users/" + encodeURIComponent(slug) + "/logs?follow=true&tail=200");
    logsEvtSource.addEventListener("log", function(e) {
        pre.textContent += e.data + "\n";
        pre.scrollTop = pre.scrollHeight;
    });
    logsEvtSource.addEventListener("end", function(e) {
        pre.textContent += "--- " + (e.data || "stream closed") + " ---\n";
        closeLogs();
    });
    logsEvtSource.addEventListener("error", function(e) {
        if (e.data) { pre.textContent += "Error: " + e.data + "\n"; }
        closeLogs();
    });
    new bootstrap.Modal(document.getElementById("logsModal")).show();
}
document.getElementById("logsModal").addEventListener("hidden.bs.modal", closeLogs);
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
setInterval(loadUsers, 10000);
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
	Username string `json:"username"`
	Slug     string `json:"slug"`
	Online   bool   `json:"online"`

	// CreatedAt is the creation time of the user's workspace (RFC3339),
	// read from the workspace PVC creationTimestamp.
	CreatedAt string `json:"createdAt,omitempty"`

	// LastLogin is the last login time of the user (RFC3339), read from
	// the last-login annotation on the workspace PVC.
	LastLogin string `json:"lastLogin,omitempty"`

	// Usage is the current CPU/RAM usage of the code-server container,
	// only reported for running workspaces.
	Usage *usageInfo `json:"usage,omitempty"`
}

// usageInfo is the JSON shape of a workspace's resource usage, matching the
// user status endpoint so the UI rendering code is shared.
type usageInfo struct {
	CPU struct {
		UsedCores    float64 `json:"usedCores"`
		RequestCores float64 `json:"requestCores"`
		LimitCores   float64 `json:"limitCores"`
	} `json:"cpu"`
	Memory struct {
		UsedBytes    int64 `json:"usedBytes"`
		RequestBytes int64 `json:"requestBytes"`
		LimitBytes   int64 `json:"limitBytes"`
	} `json:"memory"`
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

		info := userInfo{
			Username: slug,
			Slug:     slug,
			Online:   s.workspaces.IsWorkspaceRunning(r.Context(), slug),
		}

		// Creation date of the user's workspace: the PVC is created on
		// first start and survives stops and restarts.
		if createdAt, err := s.workspaces.GetWorkspaceCreationTime(r.Context(), slug); err == nil && !createdAt.IsZero() {
			info.CreatedAt = createdAt.UTC().Format(time.RFC3339)
		}

		// Last login, recorded on the PVC at every successful login.
		if lastLogin, err := s.workspaces.GetWorkspaceLastLogin(r.Context(), slug); err == nil && !lastLogin.IsZero() {
			info.LastLogin = lastLogin.UTC().Format(time.RFC3339)
		}

		// CPU/RAM usage with requests, like the user UI, for running
		// workspaces only.
		if info.Online {
			if usage, running, err := s.workspaces.GetWorkspaceUsage(r.Context(), slug); err != nil {
				s.log.V(1).Error(err, "failed to get workspace usage", "slug", slug)
			} else if usage != nil && running {
				info.Usage = &usageInfo{}
				info.Usage.CPU.UsedCores = usage.CPUUsedCores
				info.Usage.CPU.RequestCores = usage.CPURequestCores
				info.Usage.CPU.LimitCores = usage.CPULimitCores
				info.Usage.Memory.UsedBytes = usage.MemoryUsedBytes
				info.Usage.Memory.RequestBytes = usage.MemoryRequestBytes
				info.Usage.Memory.LimitBytes = usage.MemoryLimitBytes
			}
		}

		users = append(users, info)
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
	case "logs":
		if r.Method != http.MethodGet {
			http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
			return
		}
		s.serveWorkspaceLogs(w, r, slug)

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
