// Package proxy implements the mTLS reverse proxy that forwards requests
// from the CodX server to a user's workspace pod.
package proxy

import (
	"crypto/tls"
	"crypto/x509"
	"fmt"
	"net"
	"net/http"
	"net/http/httputil"
	"net/url"
	"os"
	"path/filepath"
)

// WorkspaceProxy is a reverse proxy that forwards requests to a workspace
// pod's nginx sidecar using mTLS with the CodX client certificate.
type WorkspaceProxy struct {
	// reverseProxy is the underlying httputil.ReverseProxy.
	reverseProxy *httputil.ReverseProxy

	// targetFQDN is the workspace service FQDN.
	targetFQDN string
}

// Config holds the TLS paths for the proxy.
type Config struct {
	// ClientCertFile is the path to the CodX client certificate (tls.crt).
	ClientCertFile string

	// ClientKeyFile is the path to the CodX client key (tls.key).
	ClientKeyFile string

	// CAFile is the path to the shared CA certificate (ca.crt).
	CAFile string
}

// New creates a WorkspaceProxy for the given workspace service FQDN.
// The workspace's nginx sidecar listens on :9443.
func New(workspaceFQDN string, cfg Config) (*WorkspaceProxy, error) {
	target := &url.URL{
		Scheme: "https",
		Host:   workspaceFQDN + ":9443",
	}

	tlsConfig, err := buildTLSConfig(cfg)
	if err != nil {
		return nil, fmt.Errorf("build TLS config: %w", err)
	}

	transport := &http.Transport{
		TLSClientConfig: tlsConfig,
	}

	rp := &httputil.ReverseProxy{
		Transport: transport,
		Director: func(r *http.Request) {
			// Preserve the original Host header so nginx sees the expected FQDN.
			r.Host = workspaceFQDN
			r.URL.Scheme = target.Scheme
			r.URL.Host = target.Host
			r.URL.Path = target.Path
			// Add standard X-Forwarded-* headers for the downstream nginx.
			if clientIP := r.Header.Get("X-Forwarded-For"); clientIP != "" {
				r.Header.Set("X-Forwarded-For", clientIP)
			} else {
				r.Header.Set("X-Forwarded-For", r.RemoteAddr)
			}
			if realIP := r.Header.Get("X-Real-IP"); realIP == "" {
				r.Header.Set("X-Real-IP", r.RemoteAddr)
			}
			if host := r.Header.Get("X-Forwarded-Host"); host == "" {
				r.Header.Set("X-Forwarded-Host", r.Host)
			}
			if port := r.Header.Get("X-Forwarded-Port"); port == "" {
				// Try to extract port from Host header (host:port)
				if _, portStr, err := net.SplitHostPort(r.Host); err == nil {
					r.Header.Set("X-Forwarded-Port", portStr)
				} else {
					// Default to 443 for HTTPS
					r.Header.Set("X-Forwarded-Port", "443")
				}
			}
			if proto := r.Header.Get("X-Forwarded-Proto"); proto == "" {
				r.Header.Set("X-Forwarded-Proto", "https")
			}
			if server := r.Header.Get("X-Forwarded-Server"); server == "" {
				r.Header.Set("X-Forwarded-Server", workspaceFQDN)
			}
		},
	}

	return &WorkspaceProxy{
		reverseProxy: rp,
		targetFQDN:   workspaceFQDN,
	}, nil
}

// ServeHTTP forwards the request to the workspace pod.
func (p *WorkspaceProxy) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	p.reverseProxy.ServeHTTP(w, r)
}

// TargetFQDN returns the workspace service FQDN.
func (p *WorkspaceProxy) TargetFQDN() string {
	return p.targetFQDN
}

// buildTLSConfig creates a *tls.Config that:
//   - presents the CodX client certificate (mTLS)
//   - verifies the workspace certificate against the shared CA
//   - requires TLS 1.3
func buildTLSConfig(cfg Config) (*tls.Config, error) {
	// Load the client certificate.
	cert, err := tls.LoadX509KeyPair(cfg.ClientCertFile, cfg.ClientKeyFile)
	if err != nil {
		return nil, fmt.Errorf("load client certificate: %w", err)
	}

	// Load the CA certificate.
	caData, err := os.ReadFile(cfg.CAFile)
	if err != nil {
		return nil, fmt.Errorf("read CA certificate: %w", err)
	}

	caPool := x509.NewCertPool()
	if !caPool.AppendCertsFromPEM(caData) {
		return nil, fmt.Errorf("failed to parse CA certificate from %s", cfg.CAFile)
	}

	return &tls.Config{
		Certificates: []tls.Certificate{cert},
		RootCAs:      caPool,
		MinVersion:   tls.VersionTLS13,
	}, nil
}

// DefaultTLSConfig returns a Config with the default paths from the
// configuration struct.
func DefaultTLSConfig(serverCertDir, clientCertDir, caDir string) Config {
	return Config{
		ClientCertFile: filepath.Join(clientCertDir, "tls.crt"),
		ClientKeyFile:  filepath.Join(clientCertDir, "tls.key"),
		CAFile:         filepath.Join(caDir, "ca.crt"),
	}
}
