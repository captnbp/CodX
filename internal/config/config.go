// Package config defines the CodX configuration model and parsing.
//
// CodX reads its configuration from a Kubernetes ConfigMap rendered as YAML.
// The Config struct is the in-memory representation used across the codebase.
package config

import (
	"fmt"
	"os"
	"strings"
	"time"

	"gopkg.in/yaml.v3"
)

// Config is the root configuration object for CodX.
type Config struct {
	// InstanceName identifies this CodX deployment. It defaults to the Helm
	// release name and is used to prefix workspace object names
	// (<instance>-<slug>) and in the managed-by/name labels.
	InstanceName string `yaml:"instanceName"`

	// Namespace is the Kubernetes namespace where CodX creates workspace
	// objects. Defaults to the namespace the CodX pod runs in.
	Namespace string `yaml:"namespace"`

	// HTTP configures the CodX HTTP server.
	HTTP HTTPConfig `yaml:"http"`

	// Metrics configures the Prometheus metrics endpoint.
	Metrics MetricsConfig `yaml:"metrics"`

	// OIDC configures the OIDC authentication provider.
	OIDC OIDCConfig `yaml:"oidc"`

	// Redis configures the Redis/Valkey session store.
	Redis RedisConfig `yaml:"redis"`

	// CertManager configures how per-user workspace certificates are issued.
	CertManager CertManagerConfig `yaml:"certManager"`

	// TLS configures the TLS settings for CodX's own server and the client
	// certificate used to authenticate to workspace pods.
	TLS TLSConfig `yaml:"tls"`

	// Slug configures how usernames are turned into Kubernetes-safe slugs.
	Slug SlugConfig `yaml:"slug"`

	// Inactivity configures how workspace inactivity is detected.
	Inactivity InactivityConfig `yaml:"inactivity"`

	// WorkspaceService configures the per-user workspace Service objects.
	WorkspaceService WorkspaceServiceConfig `yaml:"workspaceService"`

	// Workspace configures the per-user workspace Pod objects.
	Workspace WorkspaceConfig `yaml:"workspace"`

	// Tracing configures OpenTelemetry tracing for the CodX server itself.
	// +optional
	Tracing TracingConfig `yaml:"tracing,omitempty"`
}

// DefaultEnvoyImage is the container image used by the Envoy TLS termination
// sidecar in workspace pods when workspace.envoyImage is not set. The
// distroless variant runs as a non-root user by default.
const DefaultEnvoyImage = "envoyproxy/envoy:distroless-v1.39-latest"

// HTTPConfig configures the CodX HTTP server.
type HTTPConfig struct {
	// ListenAddr is the address the HTTP server binds to.
	// Supports IPv4 (e.g. "0.0.0.0:8443"), IPv6 (e.g. "[::]:8443"),
	// and dual-stack (e.g. "[::]:8443" on most systems).
	// +optional
	// +default="[::]:8443"
	ListenAddr string `yaml:"listenAddr"`

	// TLSCertFile is the path to the TLS certificate for the CodX server.
	// TLS is mandatory.
	TLSCertFile string `yaml:"tlsCertFile"`

	// TLSKeyFile is the path to the TLS key for the CodX server.
	// TLS is mandatory.
	TLSKeyFile string `yaml:"tlsKeyFile"`
}

// MetricsConfig configures the dedicated Prometheus metrics endpoint.
type MetricsConfig struct {
	// Enabled enables the dedicated /metrics endpoint.
	// +optional
	// +default=false
	Enabled bool `yaml:"enabled"`

	// ListenAddr is the address the metrics server binds to. Defaults to
	// "[::]:9443".
	// +optional
	// +default="[::]:9443"
	ListenAddr string `yaml:"listenAddr"`

	// TLS enables TLS on the metrics endpoint using the CodX server
	// certificate (http.tlsCertFile / http.tlsKeyFile).
	// +optional
	// +default=false
	TLS bool `yaml:"tls"`
}

// OIDCConfig configures the OIDC authentication provider.
type OIDCConfig struct {
	// Issuer is the OIDC issuer URL (e.g. https://keycloak.example.com/realms/myrealm).
	Issuer string `yaml:"issuer"`

	// ClientID is the OAuth2 client ID registered with the OIDC provider.
	ClientID string `yaml:"clientId"`

	// ClientSecret is the OAuth2 client secret. Can also be set via the
	// CODX_OIDC_CLIENT_SECRET environment variable.
	ClientSecret string `yaml:"clientSecret,omitempty"`

	// RedirectURL is the callback URL the OIDC provider redirects to after
	// login (e.g. https://codx.example.com/auth/callback).
	RedirectURL string `yaml:"redirectUrl"`

	// GroupClaimName is the name of the custom claim in the ID token that
	// contains the user's group memberships. Defaults to "groups".
	// +optional
	// +default="groups"
	GroupClaimName string `yaml:"groupClaimName"`

	// AdminGroup is the OIDC group whose members get admin privileges.
	AdminGroup string `yaml:"adminGroup"`

	// UsernameClaimName is the ID token claim used as the username source
	// for slug generation. Defaults to "preferred_username".
	// +optional
	// +default="preferred_username"
	UsernameClaimName string `yaml:"usernameClaimName"`
}

// RedisConfig configures the Redis/Valkey session store.
type RedisConfig struct {
	// Host is the Redis hostname (host:port).
	Host string `yaml:"host"`

	// Password is the optional Redis password. Can also be set via the
	// CODX_REDIS_PASSWORD environment variable.
	// +optional
	Password string `yaml:"password,omitempty"`

	// DB is the Redis logical database index.
	// +optional
	// +default=0
	DB int `yaml:"db"`

	// TLS enables TLS to Redis when true. Automatically true when	// +optional
	TLS bool `yaml:"tls"`

	// CAFilePath is the path to a PEM-encoded CA certificate file used to
	// verify the Redis TLS certificate. Typically mounted from a Kubernetes
	// secret. When set, TLS is automatically enabled.
	// +optional
	CAFilePath string `yaml:"caFilePath,omitempty"`
}

// CertManagerConfig configures how per-user workspace certificates are issued.
type CertManagerConfig struct {
	// IssuerType is the cert-manager issuer type: "Issuer" or "ClusterIssuer".
	// Defaults to "Issuer".
	// +optional
	// +default="Issuer"
	IssuerType string `yaml:"issuerType"`

	// IssuerGroup is the API group of the issuer resource. Defaults to
	// "cert-manager.io".
	// +optional
	// +default="cert-manager.io"
	IssuerGroup string `yaml:"issuerGroup"`

	// IssuerName is the name of the cert-manager issuer/ClusterIssuer used to
	// issue workspace certificates.
	IssuerName string `yaml:"issuerName"`

	// Renewal is the certificate renewal period before expiry.
	// Defaults to "720h" (30 days).
	// +optional
	// +default="720h"
	Renewal string `yaml:"renewal"`

	// Validity is the certificate validity duration.
	// Defaults to "2160h" (90 days).
	// +optional
	// +default="2160h"
	Validity string `yaml:"validity"`
}

// ParseValidity parses the Validity string into a time.Duration.
func (c *CertManagerConfig) ParseValidity() (time.Duration, error) {
	return time.ParseDuration(c.Validity)
}

// ParseRenewal parses the Renewal string into a time.Duration.
func (c *CertManagerConfig) ParseRenewal() (time.Duration, error) {
	return time.ParseDuration(c.Renewal)
}

// TLSConfig configures the TLS settings for CodX's own server and the client
// certificate used to authenticate to workspace pods.
type TLSConfig struct {
	// ServerCertDir is the directory where the CodX server TLS certificate
	// is mounted (from cert-manager). Expected files: tls.crt, tls.key.
	// Defaults to "/tls".
	// +optional
	// +default="/tls"
	ServerCertDir string `yaml:"serverCertDir"`

	// ClientCertDir is the directory where the CodX client certificate (used
	// for mTLS to workspace pods) is mounted. Expected files: tls.crt, tls.key.
	// Defaults to "/tls/client".
	// +optional
	// +default="/tls/client"
	ClientCertDir string `yaml:"clientCertDir"`

	// CADir is the directory where the shared CA certificate is mounted.
	// Expected file: ca.crt. Defaults to "/tls".
	// +optional
	// +default="/tls"
	CADir string `yaml:"caDir"`
}

// SlugConfig configures how usernames are turned into Kubernetes-safe slugs.
type SlugConfig struct {
	// UsernameField is the OIDC claim used as the username source.
	// Defaults to "preferred_username".
	// +optional
	// +default="preferred_username"
	UsernameField string `yaml:"usernameField"`

	// MaxLength is the maximum length of the generated slug. Defaults to 63
	// (Kubernetes label/name limit).
	// +optional
	// +default=63
	MaxLength int `yaml:"maxLength"`
}

// InactivityConfig configures how workspace inactivity is detected.
type InactivityConfig struct {
	// CheckInterval is how often the inactivity watcher checks for idle
	// workspaces. Defaults to "60s".
	// +optional
	// +default="60s"
	CheckInterval string `yaml:"checkInterval"`
}

// WorkspaceServiceConfig configures the per-user workspace Service objects.
type WorkspaceServiceConfig struct {
	// Annotations are extra annotations to add to every workspace Service.
	// +optional
	Annotations map[string]string `yaml:"annotations,omitempty"`

	// IPFamilies is the list of IP families (e.g. IPv4, IPv6) assigned to
	// workspace Services. Defaults to ["IPv6", "IPv4"].
	// +optional
	// +default=["IPv6", "IPv4"]
	IPFamilies []string `yaml:"ipFamilies,omitempty"`

	// IPFamilyPolicy represents the dual-stack-ness requested or required by
	// workspace Services. Defaults to "PreferDualStack".
	// +optional
	// +default="PreferDualStack"
	IPFamilyPolicy string `yaml:"ipFamilyPolicy,omitempty"`
}

// WorkspaceConfig configures the per-user workspace Pod objects.
type WorkspaceConfig struct {
	// EnvoyImage is the container image used by the Envoy TLS termination
	// sidecar in workspace pods. Defaults to DefaultEnvoyImage.
	// +optional
	// +default="envoyproxy/envoy:distroless-v1.39-latest"
	EnvoyImage string `yaml:"envoyImage,omitempty"`

	// ImagePullSecrets is the fallback list of image pull secret names
	// applied to workspace pods when the profile does not specify its own
	// (the Helm chart feeds it from global.imagePullSecrets).
	// +optional
	ImagePullSecrets []string `yaml:"imagePullSecrets,omitempty"`

	// Tracing configures OpenTelemetry tracing in the Envoy sidecar.
	// +optional
	Tracing TracingConfig `yaml:"tracing,omitempty"`
}

// TracingConfig configures OpenTelemetry tracing with an OTLP exporter.
//
// It is used in two places:
//   - top-level "tracing": the CodX server itself (OTLP/HTTP exporter, the
//     endpoint usually uses port 4318),
//   - "workspace.tracing": the workspace Envoy sidecar (envoy.tracers
//     .opentelemetry, OTLP gRPC exporter, the endpoint usually uses port 4317).
type TracingConfig struct {
	// Enabled enables OpenTelemetry tracing.
	// +optional
	// +default=false
	Enabled bool `yaml:"enabled"`

	// OTLPEndpoint is the host:port of the OpenTelemetry collector.
	// Required when enabled is true.
	// +optional
	OTLPEndpoint string `yaml:"otlpEndpoint,omitempty"`

	// ServiceName is the OpenTelemetry service name reported for requests.
	// Defaults to "codx" for the server and "codx-workspace" for the
	// workspace Envoy sidecar (the Envoy configuration is shared by all
	// workspaces, so that name is global).
	// +optional
	// +default="codx"
	ServiceName string `yaml:"serviceName,omitempty"`
}

// Load reads configuration from the given YAML data, applies defaults, and
// validates required fields. Environment variables CODX_OIDC_CLIENT_SECRET and
// CODX_REDIS_PASSWORD override the corresponding YAML fields.
func Load(data []byte) (*Config, error) {
	cfg := &Config{}
	if err := yaml.Unmarshal(data, cfg); err != nil {
		return nil, fmt.Errorf("parse config YAML: %w", err)
	}

	applyDefaults(cfg)

	if secret := os.Getenv("CODX_OIDC_CLIENT_SECRET"); secret != "" {
		cfg.OIDC.ClientSecret = secret
	}
	if secret := os.Getenv("CODX_REDIS_PASSWORD"); secret != "" {
		cfg.Redis.Password = secret
	}

	if err := Validate(cfg); err != nil {
		return nil, err
	}
	return cfg, nil
}

// applyDefaults fills in default values for unset fields.
func applyDefaults(cfg *Config) {
	if cfg.HTTP.ListenAddr == "" {
		cfg.HTTP.ListenAddr = "[::]:8443"
	}

	if cfg.Metrics.ListenAddr == "" {
		cfg.Metrics.ListenAddr = "[::]:9443"
	}

	if cfg.OIDC.GroupClaimName == "" {
		cfg.OIDC.GroupClaimName = "groups"
	}
	if cfg.OIDC.UsernameClaimName == "" {
		cfg.OIDC.UsernameClaimName = "preferred_username"
	}

	if cfg.Redis.CAFilePath == "" {
		cfg.Redis.CAFilePath = "/tls/ca.crt"
	}

	if cfg.CertManager.IssuerType == "" {
		cfg.CertManager.IssuerType = "Issuer"
	}
	if cfg.CertManager.IssuerGroup == "" {
		cfg.CertManager.IssuerGroup = "cert-manager.io"
	}
	if cfg.CertManager.Renewal == "" {
		cfg.CertManager.Renewal = "720h"
	}
	if cfg.CertManager.Validity == "" {
		cfg.CertManager.Validity = "2160h"
	}

	if cfg.TLS.ServerCertDir == "" {
		cfg.TLS.ServerCertDir = "/tls"
	}
	if cfg.TLS.ClientCertDir == "" {
		cfg.TLS.ClientCertDir = "/tls/client"
	}
	if cfg.TLS.CADir == "" {
		cfg.TLS.CADir = "/tls"
	}

	if cfg.Slug.UsernameField == "" {
		cfg.Slug.UsernameField = "preferred_username"
	}
	if cfg.Slug.MaxLength == 0 {
		cfg.Slug.MaxLength = 63
	}

	if cfg.Inactivity.CheckInterval == "" {
		cfg.Inactivity.CheckInterval = "60s"
	}

	// WorkspaceService defaults.
	if len(cfg.WorkspaceService.IPFamilies) == 0 {
		cfg.WorkspaceService.IPFamilies = []string{"IPv6", "IPv4"}
	}
	if cfg.WorkspaceService.IPFamilyPolicy == "" {
		cfg.WorkspaceService.IPFamilyPolicy = "PreferDualStack"
	}

	// Workspace defaults.
	if cfg.Workspace.EnvoyImage == "" {
		cfg.Workspace.EnvoyImage = DefaultEnvoyImage
	}
	if cfg.Workspace.Tracing.ServiceName == "" {
		cfg.Workspace.Tracing.ServiceName = "codx-workspace"
	}

	// Server tracing defaults.
	if cfg.Tracing.ServiceName == "" {
		cfg.Tracing.ServiceName = "codx"
	}
}

// Validate checks that required configuration fields are set and consistent.
func Validate(cfg *Config) error {
	var errs []string

	if cfg.InstanceName == "" {
		errs = append(errs, "instanceName is required")
	}

	if cfg.OIDC.Issuer == "" {
		errs = append(errs, "oidc.issuer is required")
	}
	if cfg.OIDC.ClientID == "" {
		errs = append(errs, "oidc.clientId is required")
	}
	if cfg.OIDC.RedirectURL == "" {
		errs = append(errs, "oidc.redirectUrl is required")
	}
	if cfg.OIDC.AdminGroup == "" {
		errs = append(errs, "oidc.adminGroup is required")
	}

	if cfg.Redis.Host == "" {
		errs = append(errs, "redis.host is required")
	}

	if cfg.CertManager.IssuerName == "" {
		errs = append(errs, "certManager.issuerName is required")
	}

	if cfg.HTTP.TLSCertFile == "" {
		errs = append(errs, "http.tlsCertFile is required (TLS is mandatory)")
	}
	if cfg.HTTP.TLSKeyFile == "" {
		errs = append(errs, "http.tlsKeyFile is required (TLS is mandatory)")
	}

	if cfg.Metrics.Enabled && cfg.Metrics.ListenAddr == cfg.HTTP.ListenAddr {
		errs = append(errs, fmt.Sprintf("metrics.listenAddr %q must differ from http.listenAddr when the metrics endpoint is enabled", cfg.Metrics.ListenAddr))
	}

	if _, err := time.ParseDuration(cfg.Inactivity.CheckInterval); err != nil {
		errs = append(errs, fmt.Sprintf("inactivity.checkInterval %q is not a valid duration: %v", cfg.Inactivity.CheckInterval, err))
	}

	if _, err := time.ParseDuration(cfg.CertManager.Renewal); err != nil {
		errs = append(errs, fmt.Sprintf("certManager.renewal %q is not a valid duration: %v", cfg.CertManager.Renewal, err))
	}
	if _, err := time.ParseDuration(cfg.CertManager.Validity); err != nil {
		errs = append(errs, fmt.Sprintf("certManager.validity %q is not a valid duration: %v", cfg.CertManager.Validity, err))
	}

	if cfg.Workspace.Tracing.Enabled && cfg.Workspace.Tracing.OTLPEndpoint == "" {
		errs = append(errs, "workspace.tracing.otlpEndpoint is required when workspace.tracing.enabled is true")
	}

	if cfg.Tracing.Enabled && cfg.Tracing.OTLPEndpoint == "" {
		errs = append(errs, "tracing.otlpEndpoint is required when tracing.enabled is true")
	}

	if len(errs) > 0 {
		return fmt.Errorf("invalid configuration:\n  - %s", strings.Join(errs, "\n  - "))
	}
	return nil
}
