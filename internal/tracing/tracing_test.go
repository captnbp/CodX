package tracing

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"

	"github.com/captnbp/CodX/internal/config"
)

func TestSetupDisabled(t *testing.T) {
	cfg := config.TracingConfig{Enabled: false}

	shutdown, err := Setup(context.Background(), cfg)
	if err != nil {
		t.Fatalf("Setup: %v", err)
	}

	// Shutdown must be safe when tracing is disabled.
	if err := Shutdown(shutdown); err != nil {
		t.Errorf("Shutdown with tracing disabled: %v", err)
	}

	// The handler wraps and serves requests without recording spans.
	h := Handler("codx", http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/healthz", nil))
	if rec.Code != http.StatusOK {
		t.Errorf("wrapped handler status = %d, want 200", rec.Code)
	}
}

// fakeCollector is a minimal OTLP/HTTP collector that records export requests.
type fakeCollector struct {
	server *httptest.Server
	mu     sync.Mutex
	got    int // number of POSTs to /v1/traces with a non-empty body
}

func newFakeCollector(t *testing.T) *fakeCollector {
	t.Helper()
	fc := &fakeCollector{}
	fc.server = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodPost && r.URL.Path == "/v1/traces" && r.ContentLength > 0 {
			fc.mu.Lock()
			fc.got++
			fc.mu.Unlock()
		}
		w.WriteHeader(http.StatusOK)
	}))
	t.Cleanup(fc.server.Close)
	return fc
}

func (fc *fakeCollector) endpoint() string {
	return strings.TrimPrefix(fc.server.URL, "http://")
}

func (fc *fakeCollector) exports() int {
	fc.mu.Lock()
	defer fc.mu.Unlock()
	return fc.got
}

func TestSetupEnabledExportsSpans(t *testing.T) {
	fc := newFakeCollector(t)

	cfg := config.TracingConfig{
		Enabled:      true,
		OTLPEndpoint: fc.endpoint(),
		ServiceName:  "codx-test",
	}

	shutdown, err := Setup(context.Background(), cfg)
	if err != nil {
		t.Fatalf("Setup: %v", err)
	}

	// Serve a request through the otelhttp middleware: this creates a span.
	h := Handler("codx-test", http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/healthz", nil))
	if rec.Code != http.StatusOK {
		t.Errorf("wrapped handler status = %d, want 200", rec.Code)
	}

	// Flush the batch processor.
	if err := Shutdown(shutdown); err != nil {
		t.Fatalf("Shutdown: %v", err)
	}

	if fc.exports() == 0 {
		t.Error("no traces exported to the collector")
	}
}

func TestTransportInjectsTraceContext(t *testing.T) {
	fc := newFakeCollector(t)

	shutdown, err := Setup(context.Background(), config.TracingConfig{
		Enabled:      true,
		OTLPEndpoint: fc.endpoint(),
		ServiceName:  "codx-test",
	})
	if err != nil {
		t.Fatalf("Setup: %v", err)
	}

	// Serve a request through the instrumented transport inside a server span
	// and check the outgoing request carries the W3C trace context.
	var gotTraceparent string
	downstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotTraceparent = r.Header.Get("traceparent")
	}))
	defer downstream.Close()

	h := Handler("codx-test", http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		req, err := http.NewRequestWithContext(r.Context(), http.MethodGet, downstream.URL, nil)
		if err != nil {
			t.Errorf("build request: %v", err)
			return
		}
		resp, err := Transport(http.DefaultTransport).RoundTrip(req)
		if err != nil {
			t.Errorf("round trip: %v", err)
			return
		}
		resp.Body.Close()
		w.WriteHeader(http.StatusOK)
	}))
	h.ServeHTTP(httptest.NewRecorder(), httptest.NewRequest(http.MethodGet, "/healthz", nil))

	if gotTraceparent == "" {
		t.Error("traceparent header not injected by the instrumented transport")
	}

	// Flush the batch processor.
	if err := Shutdown(shutdown); err != nil {
		t.Fatalf("Shutdown: %v", err)
	}
}
