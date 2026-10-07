# Workspace creation

This document describes how a new user gets from login to a running
code-server workspace: the per-user Kubernetes objects CodX creates, their
naming, the TLS model, and the lifecycle of a workspace.

## Overview

```
  User            CodX server                         Kubernetes
  ----            ----------                          ----------
  login  ------>  OIDC session (username -> slug)
  pick a profile
  Start  ------>  /api/workspace/start?profile=<name>  (SSE stream)
                    |  EnsureWorkspace (idempotent):
                    |    1. Service     <instance>-<slug>
                    |    2. PVC        <instance>-<slug>
                    |    3. cert-manager Certificate
                    |    4. Pod        <instance>-<slug>
                    |  Wait for the certificate to be Ready
                    |  Wait for the pod to be Ready
  <------ ready   redirect to /user/<slug>/
                    |
  browser  <----->  CodX proxy /user/<slug>/...  --mTLS-->  Envoy sidecar
                                                              (TLS :9443)
                                                        --> code-server
                                                            (localhost:8080)
```

The browser never talks to the workspace pod directly: all workspace traffic
goes through the CodX server, which authenticates the session and proxies to
the workspace over mutual TLS.

## From username to object names

At login, the username claim (`slug.usernameField`, default
`preferred_username`) is turned into a Kubernetes-safe slug:

- lowercased,
- every character outside `[a-z0-9-]` replaced with `-`,
- leading/trailing `-` trimmed,
- truncated to `slug.maxLength` (default 63).

All per-user objects share the name `<instance>-<slug>`, truncated again to
`slug.maxLength`:

| Object | Name |
|--------|------|
| Service | `<instance>-<slug>` |
| PVC | `<instance>-<slug>` |
| cert-manager Certificate | `<instance>-<slug>` |
| Pod | `<instance>-<slug>` |
| TLS Secret (created by cert-manager) | `<instance>-<slug>-tls` |

Caveat: slugging is not injective. Two very long usernames sharing a prefix
can produce the same truncated slug (or the same object name when the
`<instance>-` prefix consumes most of the budget), which makes them share a
workspace. Keep `slug.maxLength` comfortable (the default 63 leaves roughly
`63 - len(instance) - 1` characters for the user part) and watch the
`workspace_start` audit events if you suspect a collision.

The workspace Service FQDN - the DNS name the certificate is issued for and
the CodX proxy dials - is:

```
<instance>-<slug>.<namespace>.svc.cluster.local
```

## The start flow

The user picks a profile in the UI and clicks Start, which opens a Server-Sent
Events stream on `GET /api/workspace/start?profile=<name>`:

1. **Profile check**: the profile must exist and be allowed for the user
   (any of the user's OIDC groups in `spec.oidcGroups`; empty list = all
   authenticated users; admins can use every profile). Otherwise the request
   is denied with an audit `workspace_start_denied`.
2. **EnsureWorkspace**: the four objects below are created in order. The
   operation is idempotent - an existing object is left untouched, so
   restarting a stopped workspace or retrying after a failure is safe.
3. **Wait for the certificate**: the cert-manager Certificate is polled
   (every 5 s) until its `Ready` condition is true. cert-manager issues the
   certificate from the configured issuer; the TLS secret
   `<instance>-<slug>-tls` appears when it is ready.
4. **Wait for the pod**: the Pod is polled (every 5 s) until it is Ready -
   that is, until both readiness probes pass (code-server `/healthz` and
   Envoy `/ready`).
5. A `ready` SSE event carries the workspace URL `/user/<slug>/`.

Each phase streams a `step` event to the UI; failures stream an `error`
event and an audit `workspace_start_failed` is recorded.

## Created objects

### Service

| Property | Value |
|----------|-------|
| Name | `<instance>-<slug>` |
| Ports | `https` 9443 (Envoy TLS), `envoy-admin` 9901 (Envoy stats/readiness) |
| Selector | `app.kubernetes.io/instance=<slug>`, `app.kubernetes.io/component=workspace` |
| IP families / policy | from `workspaceService.ipFamilies` / `workspaceService.ipFamilyPolicy` |
| Annotations | `workspaceService.annotations` |

### PVC (home directory)

| Property | Value |
|----------|-------|
| Name | `<instance>-<slug>` |
| Access mode | `ReadWriteOnce` |
| Size | `spec.pvc.size` of the profile, default `10Gi` |
| StorageClass, labels, annotations | from the profile (`spec.pvc.*`) |

The PVC is mounted at `/home/coder` in the code-server container. It is kept
across stops and restarts; only deleting the workspace (admin action) removes
it. An admin can grow it later from the admin UI (`workspace_pvc_extend`).

### Certificate (cert-manager)

| Property | Value |
|----------|-------|
| Name | `<instance>-<slug>` |
| Secret | `<instance>-<slug>-tls` |
| DNS names | the workspace Service FQDN |
| Usages | `server auth`, `client auth` |
| Issuer | `certManager.issuerType` / `issuerGroup` / `issuerName` (chart: the internal CA issuer, or your own via `tls.issuerRef.existingIssuerName`) |
| Duration / renewal | `certManager.validity` (default `2160h` = 90 d) / `certManager.renewal` (default `720h` = 30 d) |

The certificate is the workspace's server certificate, presented by the Envoy
sidecar. cert-manager renews it before expiry; no user action is needed.

### Pod

The Pod runs the code-server container plus an Envoy TLS termination sidecar:

| Container | Details |
|-----------|---------|
| `code-server` | Image, command, args, env, resources, extra volume mounts from the profile (`spec.podSpec`). The `CODX_USERNAME` env var carries the user slug. The PVC is always mounted at `/home/coder`. Readiness: profile override (`spec.podSpec.codeServerReadinessProbe`) or default HTTP probe on `/healthz:8080`. |
| `envoy-tls` | Image `workspace.envoyImage` (default `envoyproxy/envoy:distroless-v1.39-latest`). Listens on 9443, terminates TLS with the workspace certificate, forwards plain HTTP to code-server on `localhost:8080`. Readiness: profile override (`envoyReadinessProbe`) or default HTTP probe on `/ready:9901`. Runs non-root, read-only root filesystem, all capabilities dropped. |
| extra sidecars | `spec.podSpec.sidecars`, verbatim. |

Volumes: the PVC (`home`), the TLS secret (`tls`, at `/tls`), the Envoy
configuration ConfigMap `codx-envoy` (`envoy-config`), a writable `/tmp`
EmptyDir for Envoy (`envoy-tmp`), plus any extra volumes from the profile.

The rest of the PodSpec - security context, init containers, affinity, node
selector, tolerations, annotations, labels - comes from
`spec.podSpec`. Labels are merged with the managed labels (the profile label
`codx.captnbp.io/profile=<name>` always wins, so the inactivity watcher can
resolve each workspace's stop delay). Image pull secrets default to the
profile's, with `global.imagePullSecrets` as fallback. Service links are
disabled by default so the Envoy sidecar never sees service-linked env vars.

## TLS model

- **Browser to CodX**: regular HTTPS at the ingress, terminated by Traefik;
  the user is authenticated by the CodX session cookie.
- **CodX to workspace**: mutual TLS. The CodX proxy presents its client
  certificate (mounted at `/tls/client`, issued by the chart) and verifies
  the workspace certificate against the internal CA (`/tls/ca.crt`), with
  TLS 1.3 as the minimum version. The workspace identity is the Service FQDN,
  which matches the certificate's DNS names.
- **Envoy to code-server**: plain HTTP inside the pod, on the loopback
  interface only (the code-server args bind it to `[::1]:8080` in the
  chart's default profile).

## Workspace lifecycle

| Action | Who | Effect | Audit event |
|--------|-----|--------|-------------|
| Start | user (UI) | idempotent creation of the four objects + waits | `workspace_start` |
| Restart | user (UI) | pod deleted; the next start recreates it (standalone pods are not auto-recreated by Kubernetes) | `workspace_restart` |
| Stop | user, admin, or inactivity watcher | pod deleted, Service/PVC/Certificate kept | `workspace_stop` (reason `inactivity` for the watcher) |
| Delete | admin | Pod, Certificate, Service, PVC and TLS secret all deleted | `workspace_delete` |
| Extend storage | admin | PVC `storage` request increased | `pvc_extend` |
| Stop for inactivity | watcher | see [Workspace inactivity management](inactivity.md) | `workspace_stop` |

Every failed variant (`workspace_start_failed`, `workspace_stop_failed`, ...)
is recorded with the error.

## Configuration reference

| Config key | Default | Description |
|------------|---------|-------------|
| `slug.usernameField` | `preferred_username` | OIDC claim the slug is derived from. |
| `slug.maxLength` | `63` | Slug and object name length budget. |
| `certManager.issuerType` / `issuerGroup` / `issuerName` | `Issuer` / `cert-manager.io` / - | cert-manager issuer signing workspace certificates. Required. |
| `certManager.validity` / `renewal` | `2160h` / `720h` | Certificate lifetime and renewal window. |
| `workspace.envoyImage` | `envoyproxy/envoy:distroless-v1.39-latest` | Envoy sidecar image. |
| `workspaceService.ipFamilies` / `ipFamilyPolicy` / `annotations` | `[IPv6, IPv4]` / `PreferDualStack` / - | Workspace Service settings. |
| `workspace.imagePullSecrets` | - | Fallback pull secrets for workspace pods (chart: `global.imagePullSecrets`). |

Profile-level settings (image, resources, PVC size, probes, sidecars, ...)
are documented in the [Profile CRD reference](profile-crd.md).

## Troubleshooting

| Symptom | Likely cause and check |
|---------|------------------------|
| Start stuck at `Waiting for TLS certificate...` | cert-manager cannot issue: check the Certificate status (`kubectl describe certificate <instance>-<slug>`), the issuer (`certManager.issuerName`) and the CA issuer health. |
| Start stuck at `Waiting for workspace pod to be ready...` | A readiness probe fails: check pod events and container logs; the default probes require code-server on `:8080` (`/healthz`) and Envoy admin on `:9901` (`/ready`). |
| `Failed to create workspace: create PVC: ...` | The profile's `pvc.size` or the StorageClass is invalid, or the storage class does not exist. |
| Two users end up on the same workspace | Slug collision (see the naming caveat above): lower `slug.maxLength` pressure or shorten usernames. |
| Proxy returns 502 for a ready workspace | The Envoy sidecar is not listening (check the `envoy-tls` container) or the TLS secret is missing (`<instance>-<slug>-tls`). |
| Certificate expires without renewal | cert-manager renewal window (`certManager.renewal`) shorter than the issuer's latency, or cert-manager down; check the Certificate `Ready` condition and renewal events. |

## Related documents

- [Profile CRD reference](profile-crd.md) - the full profile spec.
- [Workspace inactivity management](inactivity.md) - automatic stop of idle
  workspaces.
- [Authentication](authentication.md) - the login that produces the slug.
- [Documentation index](README.md)
