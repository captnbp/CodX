// mTLS fallback authentication: when the OIDC provider is unavailable, an
// administrator holding a client certificate accepted by the ingress is
// granted an admin session based on the certificate Common Name forwarded by
// the Traefik passTLSClientCert middleware (X-Forwarded-Tls-Client-Cert-Info
// header). Certificate validation (chain, expiry) is done by the ingress
// against its TLSOption CA; CodX only matches the forwarded Common Name
// against the configured allow list.
package web

import (
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/captnbp/CodX/internal/session"
	"github.com/captnbp/CodX/internal/slug"
)

// fallbackSession attempts mTLS fallback authentication. It returns an
// admin session when the fallback is enabled, the request carries the
// client certificate info header, and the certificate Common Name is in the
// allow list. It returns nil otherwise.
func (s *Server) fallbackSession(r *http.Request) *session.Session {
	if !s.cfg.AuthFallback.Enabled || len(s.cfg.AuthFallback.AdminCNs) == 0 {
		return nil
	}

	raw := r.Header.Get(s.cfg.AuthFallback.HeaderName)
	if raw == "" {
		return nil
	}

	cn := clientCertCN(raw)
	if cn == "" {
		return nil
	}

	if !containsString(s.cfg.AuthFallback.AdminCNs, cn) {
		s.audit.Info("login_failed",
			"method", "mtls-fallback",
			"reason", "certificate common name not allowed",
			"cn", cn,
			"remote", clientIP(r),
		)
		return nil
	}

	sessID, err := randomState()
	if err != nil {
		return nil
	}

	return &session.Session{
		ID:       sessID,
		Subject:  "mtls:" + cn,
		Username: cn,
		Slug:     slug.Make(cn, s.cfg.Slug.MaxLength),
		// No OIDC group is claimed for a certificate CN: access is granted
		// by IsAdmin (admins can use every profile), not by group
		// membership.
		Groups:    nil,
		IsAdmin:   true,
		ExpiresAt: time.Now().Add(s.sessionTTL()),
	}
}

// clientCertCN extracts the Common Name from a Traefik
// X-Forwarded-Tls-Client-Cert-Info header value, e.g.
// Subject="CN=alice,OU=example,O=example",Issuer="CN=ca,OU=example".
// The value may be percent-encoded (Traefik escapes the whole header value).
func clientCertCN(headerValue string) string {
	decoded := headerValue
	if strings.ContainsRune(headerValue, '%') {
		// Traefik percent-encodes with PathEscape; QueryUnescape is tried
		// as a fallback for proxies that escape spaces as '+'.
		if unescaped, err := url.PathUnescape(headerValue); err == nil {
			decoded = unescaped
		} else if unescaped, err := url.QueryUnescape(headerValue); err == nil {
			decoded = unescaped
		}
	}

	return commonName(headerField(decoded, "Subject"))
}

// headerField extracts the value of the given field (e.g. Subject) from a
// decoded certificate info header. Quoted values may contain commas.
func headerField(decoded, name string) string {
	prefix := name + "=\""
	i := strings.Index(decoded, prefix)
	if i >= 0 {
		rest := decoded[i+len(prefix):]
		if j := strings.IndexByte(rest, '"'); j >= 0 {
			return rest[:j]
		}
		return ""
	}
	// Unquoted field: value runs until the next comma or end of string.
	prefix = name + "="
	i = strings.Index(decoded, prefix)
	if i < 0 {
		return ""
	}
	rest := decoded[i+len(prefix):]
	if j := strings.IndexByte(rest, ','); j >= 0 {
		rest = rest[:j]
	}
	return rest
}

// commonName extracts the CN component from an X.509 distinguished name,
// e.g. "CN=alice,OU=example" -> "alice". Escaped commas within a component
// (\,) are preserved.
func commonName(dn string) string {
	for _, part := range strings.Split(dn, ",") {
		part = strings.TrimSpace(part)
		if after, ok := strings.CutPrefix(part, "CN="); ok {
			return after
		}
	}
	return ""
}

func containsString(list []string, target string) bool {
	for _, v := range list {
		if v == target {
			return true
		}
	}
	return false
}
