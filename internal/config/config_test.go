package config

import (
	"os"
	"testing"
)

func TestLoadDefaults(t *testing.T) {
	yaml := `
instanceName: "codx-prod"
http:
  tlsCertFile: "/tls/tls.crt"
  tlsKeyFile: "/tls/tls.key"
oidc:
  issuer: "https://keycloak.example.com/realms/myrealm"
  clientId: "codx"
  redirectUrl: "https://codx.example.com/auth/callback"
  adminGroup: "codx-admins"
redis:
  host: "valkey:6379"
certManager:
  issuerName: "codx-workspace-issuer"
`
	cfg, err := Load([]byte(yaml))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	// Explicit values
	if cfg.InstanceName != "codx-prod" {
		t.Errorf("InstanceName = %q, want %q", cfg.InstanceName, "codx-prod")
	}
	if cfg.OIDC.Issuer != "https://keycloak.example.com/realms/myrealm" {
		t.Errorf("OIDC.Issuer = %q", cfg.OIDC.Issuer)
	}
	if cfg.OIDC.AdminGroup != "codx-admins" {
		t.Errorf("OIDC.AdminGroup = %q", cfg.OIDC.AdminGroup)
	}

	// Defaults
	if cfg.HTTP.ListenAddr != "[::]:8443" {
		t.Errorf("HTTP.ListenAddr default = %q, want [::]:8443", cfg.HTTP.ListenAddr)
	}
	if cfg.OIDC.GroupClaimName != "groups" {
		t.Errorf("OIDC.GroupClaimName default = %q, want groups", cfg.OIDC.GroupClaimName)
	}
	if cfg.OIDC.UsernameClaimName != "preferred_username" {
		t.Errorf("OIDC.UsernameClaimName default = %q, want preferred_username", cfg.OIDC.UsernameClaimName)
	}
	if cfg.CertManager.IssuerType != "Issuer" {
		t.Errorf("CertManager.IssuerType default = %q, want Issuer", cfg.CertManager.IssuerType)
	}
	if cfg.CertManager.IssuerGroup != "cert-manager.io" {
		t.Errorf("CertManager.IssuerGroup default = %q, want cert-manager.io", cfg.CertManager.IssuerGroup)
	}
	if cfg.CertManager.Renewal != "720h" {
		t.Errorf("CertManager.Renewal default = %q, want 720h", cfg.CertManager.Renewal)
	}
	if cfg.CertManager.Validity != "2160h" {
		t.Errorf("CertManager.Validity default = %q, want 2160h", cfg.CertManager.Validity)
	}
	if cfg.TLS.ServerCertDir != "/tls" {
		t.Errorf("TLS.ServerCertDir default = %q, want /tls", cfg.TLS.ServerCertDir)
	}
	if cfg.TLS.ClientCertDir != "/tls/client" {
		t.Errorf("TLS.ClientCertDir default = %q, want /tls/client", cfg.TLS.ClientCertDir)
	}
	if cfg.Slug.MaxLength != 63 {
		t.Errorf("Slug.MaxLength default = %d, want 63", cfg.Slug.MaxLength)
	}
	if cfg.Inactivity.Signal != "log-tail" {
		t.Errorf("Inactivity.Signal default = %q, want log-tail", cfg.Inactivity.Signal)
	}
	if cfg.Inactivity.CheckInterval != "60s" {
		t.Errorf("Inactivity.CheckInterval default = %q, want 60s", cfg.Inactivity.CheckInterval)
	}
}

func TestRedisTLSEnabledByCASecret(t *testing.T) {
	yaml := `
instanceName: "codx"
http:
  tlsCertFile: "/tls/tls.crt"
  tlsKeyFile: "/tls/tls.key"
oidc:
  issuer: "https://keycloak.example.com/realms/myrealm"
  clientId: "codx"
  redirectUrl: "https://codx.example.com/auth/callback"
  adminGroup: "codx-admins"
redis:
  host: "valkey:6379"
  caSecretName: "redis-ca"
certManager:
  issuerName: "codx-workspace-issuer"
`
	cfg, err := Load([]byte(yaml))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !cfg.Redis.TLS {
		t.Error("Redis.TLS should be auto-enabled when caSecretName is set")
	}
}

func TestValidationMissingRequired(t *testing.T) {
	yaml := `
instanceName: ""
oidc:
  issuer: ""
  clientId: ""
  adminGroup: ""
redis:
  host: ""
certManager:
  issuerName: ""
http: {}
`
	_, err := Load([]byte(yaml))
	if err == nil {
		t.Fatal("expected validation error, got nil")
	}
	for _, field := range []string{"instanceName", "oidc.issuer", "oidc.clientId", "oidc.redirectUrl", "oidc.adminGroup", "redis.host", "certManager.issuerName", "http.tlsCertFile", "http.tlsKeyFile"} {
		if !contains(err.Error(), field) {
			t.Errorf("validation error should mention %q: %v", field, err)
		}
	}
}

func TestValidationInvalidInactivitySignal(t *testing.T) {
	yaml := `
instanceName: "codx"
http:
  tlsCertFile: "/tls/tls.crt"
  tlsKeyFile: "/tls/tls.key"
oidc:
  issuer: "https://kc.example.com/realm"
  clientId: "codx"
  redirectUrl: "https://codx.example.com/auth/callback"
  adminGroup: "admins"
redis:
  host: "valkey:6379"
certManager:
  issuerName: "issuer"
inactivity:
  signal: "invalid-method"
`
	_, err := Load([]byte(yaml))
	if err == nil {
		t.Fatal("expected validation error for invalid signal")
	}
	if !contains(err.Error(), "inactivity.signal") {
		t.Errorf("error should mention inactivity.signal: %v", err)
	}
}

func TestEnvOverrides(t *testing.T) {
	t.Setenv("CODX_OIDC_CLIENT_SECRET", "secret-from-env")
	t.Setenv("CODX_REDIS_PASSWORD", "redis-pass-from-env")

	yaml := `
instanceName: "codx"
http:
  tlsCertFile: "/tls/tls.crt"
  tlsKeyFile: "/tls/tls.key"
oidc:
  issuer: "https://kc.example.com/realm"
  clientId: "codx"
  redirectUrl: "https://codx.example.com/auth/callback"
  adminGroup: "admins"
redis:
  host: "valkey:6379"
certManager:
  issuerName: "issuer"
`
	cfg, err := Load([]byte(yaml))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if cfg.OIDC.ClientSecret != "secret-from-env" {
		t.Errorf("OIDC.ClientSecret = %q, want secret-from-env", cfg.OIDC.ClientSecret)
	}
	if cfg.Redis.Password != "redis-pass-from-env" {
		t.Errorf("Redis.Password = %q, want redis-pass-from-env", cfg.Redis.Password)
	}

	// Clean up for other tests
	os.Unsetenv("CODX_OIDC_CLIENT_SECRET")
	os.Unsetenv("CODX_REDIS_PASSWORD")
}

func TestInvalidYAML(t *testing.T) {
	yaml := `
instanceName: "codx"
http:
  tlsCertFile: "/tls/tls.crt"
  tlsKeyFile: "/tls/tls.key"
oidc:
  issuer: "https://kc.example.com/realm"
  clientId: "codx"
  redirectUrl: "https://codx.example.com/auth/callback"
  adminGroup: "admins"
redis:
  host: "valkey:6379"
certManager:
  issuerName: "issuer"
inactivity:
  checkInterval: "not-a-duration"
`
	_, err := Load([]byte(yaml))
	if err == nil {
		t.Fatal("expected validation error for invalid duration")
	}
	if !contains(err.Error(), "checkInterval") {
		t.Errorf("error should mention checkInterval: %v", err)
	}
}

func contains(s, substr string) bool {
	return len(s) >= len(substr) && (s == substr || len(substr) == 0 ||
		(len(s) > 0 && len(substr) > 0 && indexOf(s, substr) >= 0))
}

func indexOf(s, sub string) int {
	for i := 0; i <= len(s)-len(sub); i++ {
		if s[i:i+len(sub)] == sub {
			return i
		}
	}
	return -1
}
