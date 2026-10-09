# CodX

CodX runs an isolated, per-user [code-server](https://github.com/coder/code-server)
workspace on Kubernetes: users sign in with their OIDC provider, pick a
workspace profile, and get their own pod, their own TLS-terminated endpoint
and their own persistent home directory - without ever seeing the
Kubernetes API or each other.

## What CodX is for

**Uniform environments for computer science classes** (the original use
case): students get a ready-to-use, identical environment for their labs -
same image, same tools, same pre-configured workspace - reachable from a
plain browser. Nothing to install, nothing to update, wiped when the class
is over.

**A bastion for infrastructure teams**, especially Kubernetes-based
environments: every administrator connects to *their own* isolated
workspace instead of sharing a jump host. There is no session recording -
isolation between users is the boundary, not surveillance. The per-user
mTLS client certificates (codx-to-workspace, and the admin mTLS fallback
login) make it a good fit for restricted environments.

**A home for personal AI agents**: each user can host long-running personal
agents (and their tools) in a workspace with persistent storage, network
egress, and a stable URL behind the user's own TLS certificate.

**Plain developer workspaces**: code-server, a PVC that survives restarts,
logs, live CPU/RAM usage and inactivity-based auto-stop to keep idle
workspaces from costing resources.

## Why not JupyterHub?

The project started as a JupyterHub deployment. CodX was written to get the
same "one workspace per user" result with a lot less moving parts, and a few
things JupyterHub made hard:

- **Dynamic `Profile` CRDs**: workspace templates are plain Kubernetes
  custom resources - create, update or delete them live, no reconfiguration
  or restart of the hub.
- **mTLS made easy**: per-workspace TLS with an Envoy sidecar, cert-manager
  certificates and mutual TLS between CodX and every workspace, enabled by
  a single Helm value.
- **OpenTelemetry**: traces for the CodX server and the workspace sidecars,
  wired to any OTLP collector.
- **More workspace customization**: profiles carry the full pod spec -
  image, command, args, env, resources, sidecars, init containers,
  tolerations, node selectors, volumes.
- **Workspace logs**: stream the code-server logs live from the user UI or
  the admin UI.
- **A simpler, Kubernetes-only codebase**: one Go binary and a Helm chart,
  no spawner zoo, no Python dependency chain, no config pyramid.
- **Extendable PVCs**: grow a user's home directory from the admin UI
  without recreating anything.
- **CPU/RAM usage**: live per-workspace resource usage (requests, limits)
  in the user UI, the admin UI and a bundled Grafana dashboard.
- **mTLS fallback authentication**: admins can still sign in with a client
  certificate when the OIDC provider is down.
- **OIDC group gating per workspace**: profiles declare which OIDC groups
  may use them; users only see the workspaces they are entitled to.

## Feature overview

- OIDC login (authorization code flow), admin group, per-user slugged
  Kubernetes objects
- mTLS admin fallback login via Traefik client-certificate forwarding
  (`passTLSClientCert`), with a CN allow list
- Per-workspace: dedicated pod, Service, cert-manager certificate and PVC;
  automatic inactivity stop with a per-profile delay
- Live workspace logs, restart/stop, proxying with mTLS to the Envoy
  sidecar
- Admin UI: list users, online status, creation and last-login dates,
  CPU/RAM usage, workspace logs, extend PVC, stop, delete
- Prometheus metrics (running/pending workspaces, health) and a bundled
  Grafana dashboard
- Helm chart: Traefik mTLS objects (TLSOption, passTLSClientCert middleware,
  CA secret), extra middlewares, NetworkPolicies, cert-manager CA, Valkey

## Documentation

- [Helm chart README](charts/codx/README.md) - architecture and
  installation instructions
- [Authentication](docs/authentication.md) - OIDC, sessions, mTLS fallback
- [Workspace creation](docs/workspace-creation.md) - per-user objects, TLS
  model, lifecycle
- [Workspace inactivity management](docs/inactivity.md) - activity
  detection and auto-stop
- [Profile CRD reference](docs/profile-crd.md) - workspace templates
- [Contributing](CONTRIBUTING.md)

## License

CodX is released under the [MIT License](LICENSE),
Copyright (c) 2026 Benoit Pourre.

## Credits

CodX is vibe-coded with love using [Mistral Vibe GLM5.3](https://mistral.ai)
under the author's supervision.
