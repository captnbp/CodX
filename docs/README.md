# CodX documentation

- [Authentication](authentication.md) - OIDC login flow, sessions, and the
  mTLS client-certificate fallback for admins (Traefik TLSOption,
  passTLSClientCert Middleware, client CA management).
- [Workspace creation](workspace-creation.md) - per-user Kubernetes objects
  (Service, PVC, Certificate, Pod), naming, TLS model and workspace
  lifecycle.
- [Workspace inactivity management](inactivity.md) - activity detection via
  the Envoy sidecar, automatic stop of idle workspaces, per-profile delays
  and configuration.
- [Profile CRD reference](profile-crd.md) - `profiles.codx.io/v1` workspace
  templates and OIDC group gating.
