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
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

// adminTestServer builds a web.Server with an admin session and pre-populated
// workspace objects for testing admin endpoints.
func adminTestServer(t *testing.T) (*Server, *session.MemoryStore, *k8s.WorkspaceManager) {
	t.Helper()

	cfg := &config.Config{
		InstanceName: "codx",
		Namespace:    "codx-system",
		Slug:         config.SlugConfig{MaxLength: 63},
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
	profiles.Upsert(&profilev1.Profile{
		ObjectMeta: metav1.ObjectMeta{Name: "default"},
		Spec: profilev1.ProfileSpec{
			Title:   "Default",
			PodSpec: profilev1.ProfilePodSpec{Image: "codercom/code-server:latest"},
			PVC:     profilev1.ProfilePVC{Size: "10Gi"},
		},
	})

	coreClient := fake.NewCoreV1Client()
	certClient := fake.NewCertManagerClient()
	cs := &k8s.Clientset{
		CoreV1:      coreClient,
		CertManager: certClient,
	}
	wm := k8s.NewWorkspaceManager(cs, cfg)

	// Create workspace objects for two users.
	prof := profiles.Get("default")
	_, err := wm.EnsureWorkspace(context.Background(), prof, "john-doe")
	if err != nil {
		t.Fatalf("create workspace john-doe: %v", err)
	}
	_, err = wm.EnsureWorkspace(context.Background(), prof, "jane-smith")
	if err != nil {
		t.Fatalf("create workspace jane-smith: %v", err)
	}

	proxyFactory := func(fqdn string) (http.Handler, error) {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			w.WriteHeader(http.StatusOK)
		}), nil
	}

	srv := New(cfg, nil, store, profiles, wm, proxyFactory)
	_ = store

	// Create admin session.
	adminSess := &session.Session{
		ID:        "admin-session",
		Username:  "admin.user",
		Slug:      "admin-user",
		Groups:    []string{"codx-admins"},
		IsAdmin:   true,
		ExpiresAt: time.Now().Add(1 * time.Hour),
	}
	if err := store.Save(context.Background(), adminSess); err != nil {
		t.Fatalf("save admin session: %v", err)
	}

	// Create non-admin session.
	userSess := &session.Session{
		ID:        "user-session",
		Username:  "john.doe",
		Slug:      "john-doe",
		Groups:    []string{"developers"},
		IsAdmin:   false,
		ExpiresAt: time.Now().Add(1 * time.Hour),
	}
	if err := store.Save(context.Background(), userSess); err != nil {
		t.Fatalf("save user session: %v", err)
	}

	return srv, store, wm
}

func TestAdminUIRequiresAdmin(t *testing.T) {
	srv, _, _ := adminTestServer(t)
	handler := srv.Handler()

	// Non-admin should be forbidden.
	rr := doRequest(t, handler, "GET", "/admin", "user-session")
	if rr.Code != http.StatusForbidden {
		t.Errorf("non-admin /admin: got %d, want %d", rr.Code, http.StatusForbidden)
	}
}

func TestAdminUIRendersForAdmin(t *testing.T) {
	srv, _, _ := adminTestServer(t)
	handler := srv.Handler()

	rr := doRequest(t, handler, "GET", "/admin", "admin-session")
	if rr.Code != http.StatusOK {
		t.Fatalf("admin /admin: got %d, want %d", rr.Code, http.StatusOK)
	}
	if !strings.Contains(rr.Body.String(), "CodX Admin") {
		t.Errorf("admin UI should contain 'CodX Admin': %q", rr.Body.String())
	}
}

func TestAdminListUsers(t *testing.T) {
	srv, _, _ := adminTestServer(t)
	handler := srv.Handler()

	rr := doRequest(t, handler, "GET", "/api/admin/users", "admin-session")
	if rr.Code != http.StatusOK {
		t.Fatalf("list users: got %d, want %d", rr.Code, http.StatusOK)
	}

	var users []userInfo
	if err := json.Unmarshal(rr.Body.Bytes(), &users); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}

	if len(users) != 2 {
		t.Errorf("users count = %d, want 2", len(users))
	}
}

func TestAdminListUsersForbiddenForNonAdmin(t *testing.T) {
	srv, _, _ := adminTestServer(t)
	handler := srv.Handler()

	rr := doRequest(t, handler, "GET", "/api/admin/users", "user-session")
	if rr.Code != http.StatusForbidden {
		t.Errorf("non-admin list users: got %d, want %d", rr.Code, http.StatusForbidden)
	}
}

func TestAdminStopWorkspace(t *testing.T) {
	srv, _, _ := adminTestServer(t)
	handler := srv.Handler()

	rr := doRequest(t, handler, "POST", "/api/admin/users/john-doe/stop", "admin-session")
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

func TestAdminDeleteWorkspace(t *testing.T) {
	srv, _, _ := adminTestServer(t)
	handler := srv.Handler()

	rr := doRequest(t, handler, "POST", "/api/admin/users/john-doe/delete", "admin-session")
	if rr.Code != http.StatusOK {
		t.Fatalf("delete workspace: got %d, want %d, body: %s", rr.Code, http.StatusOK, rr.Body.String())
	}

	var result map[string]string
	if err := json.Unmarshal(rr.Body.Bytes(), &result); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if result["status"] != "deleted" {
		t.Errorf("status = %q, want deleted", result["status"])
	}
}

func TestAdminExtendPVC(t *testing.T) {
	srv, _, _ := adminTestServer(t)
	handler := srv.Handler()

	rr := doRequest(t, handler, "POST", "/api/admin/users/john-doe/extend?pvcSize=50Gi", "admin-session")
	if rr.Code != http.StatusOK {
		t.Fatalf("extend PVC: got %d, want %d, body: %s", rr.Code, http.StatusOK, rr.Body.String())
	}

	var result map[string]string
	if err := json.Unmarshal(rr.Body.Bytes(), &result); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if result["status"] != "extended" {
		t.Errorf("status = %q, want extended", result["status"])
	}
	if result["newSize"] != "50Gi" {
		t.Errorf("newSize = %q, want 50Gi", result["newSize"])
	}
}

func TestAdminExtendPVCMissingParam(t *testing.T) {
	srv, _, _ := adminTestServer(t)
	handler := srv.Handler()

	rr := doRequest(t, handler, "POST", "/api/admin/users/john-doe/extend", "admin-session")
	if rr.Code != http.StatusBadRequest {
		t.Errorf("extend PVC without size: got %d, want %d", rr.Code, http.StatusBadRequest)
	}
}

func TestAdminUnknownAction(t *testing.T) {
	srv, _, _ := adminTestServer(t)
	handler := srv.Handler()

	rr := doRequest(t, handler, "POST", "/api/admin/users/john-doe/unknown", "admin-session")
	if rr.Code != http.StatusBadRequest {
		t.Errorf("unknown action: got %d, want %d", rr.Code, http.StatusBadRequest)
	}
}

func TestAdminActionsForbiddenForNonAdmin(t *testing.T) {
	srv, _, _ := adminTestServer(t)
	handler := srv.Handler()

	rr := doRequest(t, handler, "POST", "/api/admin/users/john-doe/stop", "user-session")
	if rr.Code != http.StatusForbidden {
		t.Errorf("non-admin stop: got %d, want %d", rr.Code, http.StatusForbidden)
	}
}

func TestRestartWorkspaceEndpoint(t *testing.T) {
	srv, store, _ := adminTestServer(t)
	handler := srv.Handler()

	rr := doRequest(t, handler, "POST", "/api/workspace/restart", "user-session")
	if rr.Code != http.StatusOK {
		t.Fatalf("restart: got %d, want %d, body: %s", rr.Code, http.StatusOK, rr.Body.String())
	}

	var result map[string]string
	if err := json.Unmarshal(rr.Body.Bytes(), &result); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if result["status"] != "restarting" {
		t.Errorf("status = %q, want restarting", result["status"])
	}

	_ = store // keep store reference
}

func TestRestartWorkspaceWrongMethod(t *testing.T) {
	srv, _, _ := adminTestServer(t)
	handler := srv.Handler()

	rr := doRequest(t, handler, "GET", "/api/workspace/restart", "user-session")
	if rr.Code != http.StatusMethodNotAllowed {
		t.Errorf("GET restart: got %d, want %d", rr.Code, http.StatusMethodNotAllowed)
	}
}

// Ensure the fake clients compile with the expected types.
var (
	_ corev1.Service = corev1.Service{}
	_ metav1.ListOptions = metav1.ListOptions{}
)

// httptest.ResponseRecorder alias for readability.
var _ = httptest.NewRecorder
