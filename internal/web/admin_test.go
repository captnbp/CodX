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
	"k8s.io/apimachinery/pkg/api/resource"
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

	// john-doe's workspace is running (usage and logs are available);
	// jane-smith's is stopped. The PVC creation timestamp of john-doe is
	// fixed so the reported creation date is deterministic.
	coreClient.PodMap["codx-john-doe"].Status.Phase = corev1.PodRunning
	coreClient.PodMap["codx-john-doe"].Spec.Containers[0].Resources = corev1.ResourceRequirements{
		Requests: corev1.ResourceList{
			corev1.ResourceCPU:    resource.MustParse("250m"),
			corev1.ResourceMemory: resource.MustParse("256Mi"),
		},
		Limits: corev1.ResourceList{
			corev1.ResourceCPU:    resource.MustParse("2"),
			corev1.ResourceMemory: resource.MustParse("4Gi"),
		},
	}
	coreClient.PVCMap["codx-john-doe"].CreationTimestamp = metav1.NewTime(time.Date(2026, 1, 15, 10, 30, 0, 0, time.UTC))
	// Last login of john-doe, recorded by the login flow on the PVC
	// annotation; jane-smith never logged in since her workspace exists.
	coreClient.PVCMap["codx-john-doe"].Annotations = map[string]string{
		k8s.LastLoginAnnotation: time.Date(2026, 10, 7, 15, 43, 58, 0, time.UTC).Format(time.RFC3339),
	}
	coreClient.PodLogs["codx-john-doe"] = "code-server starting\nlistening on 8080\n"

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
	_ corev1.Service     = corev1.Service{}
	_ metav1.ListOptions = metav1.ListOptions{}
)

// httptest.ResponseRecorder alias for readability.
var _ = httptest.NewRecorder

func TestAdminListUsersReportsCreationAndUsage(t *testing.T) {
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
		t.Fatalf("users count = %d, want 2", len(users))
	}

	bySlug := map[string]userInfo{}
	for _, u := range users {
		bySlug[u.Slug] = u
	}

	// john-doe: running, so usage with requests is reported.
	john := bySlug["john-doe"]
	if !john.Online {
		t.Error("john-doe should be online")
	}
	if john.Usage == nil {
		t.Fatal("john-doe usage is nil, want usage for a running workspace")
	}
	if john.Usage.CPU.RequestCores != 0.25 {
		t.Errorf("CPU request = %v, want 0.25", john.Usage.CPU.RequestCores)
	}
	if john.Usage.CPU.LimitCores != 2 {
		t.Errorf("CPU limit = %v, want 2", john.Usage.CPU.LimitCores)
	}
	if john.Usage.Memory.RequestBytes != 256*1024*1024 {
		t.Errorf("memory request = %v, want 256Mi", john.Usage.Memory.RequestBytes)
	}
	if john.Usage.Memory.LimitBytes != 4*1024*1024*1024 {
		t.Errorf("memory limit = %v, want 4Gi", john.Usage.Memory.LimitBytes)
	}

	// Creation date from the PVC creationTimestamp.
	createdAt, err := time.Parse(time.RFC3339, john.CreatedAt)
	if err != nil {
		t.Fatalf("createdAt %q is not RFC3339: %v", john.CreatedAt, err)
	}
	want := time.Date(2026, 1, 15, 10, 30, 0, 0, time.UTC)
	if !createdAt.Equal(want) {
		t.Errorf("createdAt = %v, want %v", createdAt, want)
	}

	// Last login from the PVC annotation.
	lastLogin, err := time.Parse(time.RFC3339, john.LastLogin)
	if err != nil {
		t.Fatalf("lastLogin %q is not RFC3339: %v", john.LastLogin, err)
	}
	wantLogin := time.Date(2026, 10, 7, 15, 43, 58, 0, time.UTC)
	if !lastLogin.Equal(wantLogin) {
		t.Errorf("lastLogin = %v, want %v", lastLogin, wantLogin)
	}

	// jane-smith: stopped, no usage but still a creation date.
	jane := bySlug["jane-smith"]
	if jane.Online {
		t.Error("jane-smith should be offline")
	}
	if jane.Usage != nil {
		t.Errorf("jane-smith usage = %v, want nil for a stopped workspace", jane.Usage)
	}
	if jane.CreatedAt == "" {
		t.Error("jane-smith createdAt is empty, want the PVC creation date")
	}
	if jane.LastLogin != "" {
		t.Errorf("jane-smith lastLogin = %q, want empty (never logged in)", jane.LastLogin)
	}
}

func TestAdminLogsRunningWorkspace(t *testing.T) {
	srv, _, _ := adminTestServer(t)
	handler := srv.Handler()

	rr := doRequest(t, handler, "GET", "/api/admin/users/john-doe/logs?tail=100", "admin-session")
	if rr.Code != http.StatusOK {
		t.Fatalf("admin logs: got %d, want %d, body: %s", rr.Code, http.StatusOK, rr.Body.String())
	}
	if got := rr.Header().Get("Content-Type"); !strings.Contains(got, "text/plain") {
		t.Errorf("admin logs content type = %q, want text/plain", got)
	}
	if !strings.Contains(rr.Body.String(), "listening on 8080") {
		t.Errorf("admin logs body should contain the pod logs: %q", rr.Body.String())
	}
}

func TestAdminLogsWorkspaceNotRunning(t *testing.T) {
	srv, _, _ := adminTestServer(t)
	handler := srv.Handler()

	rr := doRequest(t, handler, "GET", "/api/admin/users/jane-smith/logs", "admin-session")
	if rr.Code != http.StatusConflict {
		t.Errorf("logs of stopped workspace: got %d, want %d", rr.Code, http.StatusConflict)
	}
}

func TestAdminLogsForbiddenForNonAdmin(t *testing.T) {
	srv, _, _ := adminTestServer(t)
	handler := srv.Handler()

	rr := doRequest(t, handler, "GET", "/api/admin/users/john-doe/logs", "user-session")
	if rr.Code != http.StatusForbidden {
		t.Errorf("non-admin logs: got %d, want %d", rr.Code, http.StatusForbidden)
	}
}

// fakeActivityStore is a web.ActivityStore for tests: the last-activity map
// persisted by the inactivity leader.
type fakeActivityStore struct {
	records map[string]time.Time
}

func (f *fakeActivityStore) Load(ctx context.Context) (map[string]time.Time, error) {
	return f.records, nil
}

func TestAdminListUsersReportsIdleTime(t *testing.T) {
	srv, _, _ := adminTestServer(t)
	handler := srv.Handler()

	// The inactivity context persisted by the leader: john-doe has been
	// idle for 25 minutes, jane-smith (stopped) has no record.
	idleSince := time.Now().Add(-25 * time.Minute)
	srv.WithActivityStore(&fakeActivityStore{records: map[string]time.Time{
		"john-doe": idleSince,
	}})

	rr := doRequest(t, handler, "GET", "/api/admin/users", "admin-session")
	if rr.Code != http.StatusOK {
		t.Fatalf("list users: got %d, want %d", rr.Code, http.StatusOK)
	}

	var users []userInfo
	if err := json.Unmarshal(rr.Body.Bytes(), &users); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	bySlug := map[string]userInfo{}
	for _, u := range users {
		bySlug[u.Slug] = u
	}

	john := bySlug["john-doe"]
	if john.IdleSeconds == nil {
		t.Fatal("john-doe idleSeconds is nil, want the persisted idle time")
	}
	if *john.IdleSeconds < 24*60 || *john.IdleSeconds > 26*60 {
		t.Errorf("john-doe idleSeconds = %d, want ~1500 (25 minutes)", *john.IdleSeconds)
	}

	// No recorded activity (never active or lost context): idle unknown.
	jane := bySlug["jane-smith"]
	if jane.IdleSeconds != nil {
		t.Errorf("jane-smith idleSeconds = %d, want nil (no activity record)", *jane.IdleSeconds)
	}
}

func TestAdminListUsersWithoutActivityStore(t *testing.T) {
	srv, _, _ := adminTestServer(t)
	handler := srv.Handler()

	// No activity store attached: the admin UI works, idle simply unknown.
	rr := doRequest(t, handler, "GET", "/api/admin/users", "admin-session")
	if rr.Code != http.StatusOK {
		t.Fatalf("list users: got %d, want %d", rr.Code, http.StatusOK)
	}
	var users []userInfo
	if err := json.Unmarshal(rr.Body.Bytes(), &users); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	for _, u := range users {
		if u.IdleSeconds != nil {
			t.Errorf("%s idleSeconds = %d, want nil without an activity store", u.Slug, *u.IdleSeconds)
		}
	}
}
