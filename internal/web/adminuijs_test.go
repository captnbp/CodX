package web

import (
	"regexp"
	"testing"
)

// TestAdminUIDefinesReferencedFunctions guards against referencing an
// undefined JS function in the admin UI template: a ReferenceError inside
// loadUsers breaks the whole users list rendering (all rows fail at once).
func TestAdminUIDefinesReferencedFunctions(t *testing.T) {
	srv, _, _ := adminTestServer(t)
	handler := srv.Handler()

	rr := doRequest(t, handler, "GET", "/admin", "admin-session")
	if rr.Code != 200 {
		t.Fatalf("admin UI: %d", rr.Code)
	}
	body := rr.Body.String()

	// Functions defined in the page (any of its <script> blocks).
	defined := map[string]bool{}
	for _, m := range regexp.MustCompile(`function ([a-zA-Z_$][\w$]*)\(`).FindAllStringSubmatch(body, -1) {
		defined[m[1]] = true
	}

	// Browser and library APIs available in the page, plus the JS keyword
	// (anonymous functions) and the resourceBar formatter parameter.
	known := map[string]bool{
		"fetch": true, "encodeURIComponent": true, "prompt": true,
		"confirm": true, "alert": true, "Date": true, "setInterval": true,
		"EventSource": true, "String": true, "JSON": true, "Number": true,
		"function": true, "fmt": true,
	}

	// Every identifier called directly (not as a method, i.e. not preceded
	// by a dot) must be defined in the page or be a known API.
	called := regexp.MustCompile(`(?:^|[^.\w$])([a-zA-Z_$][\w$]*)\(`)
	for _, m := range called.FindAllStringSubmatch(body, -1) {
		name := m[1]
		if !defined[name] && !known[name] {
			t.Errorf("admin UI calls undefined function %q", name)
		}
	}

	// Sanity: the admin UI handlers are defined.
	for _, name := range []string{"loadUsers", "openLogs", "closeLogs", "extendPVC", "stopWorkspace", "deleteUser", "resourceBar", "formatBytes", "formatCores"} {
		if !defined[name] {
			t.Errorf("admin UI does not define function %q", name)
		}
	}
}
