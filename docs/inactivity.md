# Workspace inactivity management

CodX stops idle user workspaces automatically: running a code-server pod per
user 24/7 wastes resources, so each workspace is monitored at the network
level and its pod is stopped after a period without HTTPS activity. This
document describes how activity is detected, how workspaces are stopped, and
how to configure the delays.

## Overview

```
+--------------+   lists pods   +------------------------+
|   CodX       |--------------->|  Kubernetes API        |
|              |                +------------------------+
| inactivity   |   poll /stats  +------------------------+
| loops        |--------------->|  workspace Envoy       |
|              |                |  sidecar (admin :9901) |
|  - activity  |                +------------------------+
|    source    |
|  - watcher   |   stop         +------------------------+
|  - reconciler|--------------->|  workspace Pod         |
+--------------+   (delete)    +------------------------+
                     Service, PVC and Certificate are kept: the workspace
                     is restarted later with the user's data intact.
```

Three loops run inside the CodX server, all ticking at the same
`inactivity.checkInterval` (default `60s`):

| Loop | Role |
|------|------|
| Activity source (`ConnectionCountActivity`) | Polls every running workspace pod's Envoy admin interface and records the last activity timestamp per workspace. |
| Watcher | Compares the last activity timestamp of every registered workspace against its delay and stops the pod when it has been idle too long. |
| Registration reconciler | Keeps the watcher's registrations in sync with the workspaces that actually exist and their profile-configured delays. |

## Leader election: a single inactivity monitor

CodX is a stateless server and can run with several replicas (sessions are
shared via Redis), but the inactivity monitor must run on **exactly one
replica at a time**: two concurrent watchers would poll every workspace
twice and race to stop the same idle pods. When
`inactivity.leaderElection.enabled` is `true` (the Helm chart default), the
replicas therefore compete for a Kubernetes `Lease`
(`coordination.k8s.io/v1`):

- Each replica runs a leader election loop (`internal/leader`) using the
  standard client-go `leaderelection` package with a `LeaseLock` on the
  object `<leaseNamespace>/<leaseName>` (defaults: the release namespace
  and `<instanceName>-inactivity`).
- The replica holding the lease becomes the leader: it starts the activity
  source, the watcher and the registration reconciler. Its leadership is
  renewed in the background (`retryPeriod`, default `2s`).
- The other replicas stand by and re-check the lease; they do not monitor
  anything.
- If the leader dies or loses API connectivity, the lease expires after
  `leaseDuration` (default `15s`) and another replica acquires it, then
  starts its own inactivity loops. A failover therefore adds up to roughly
  `leaseDuration` of dead time to the inactivity checks - workspaces are
  never stopped early, at most a little late.
- On graceful shutdown the leader releases the lease immediately
  (`releaseOnCancel`, default `true`) so the failover is fast during
  rolling deployments.

The leader identity is `<pod name>_<pod UID>` (exposed to the container by
the downward API), and the election records `LeaderElection` events on the
Lease object for observability. The RBAC rules for `leases` (get, create,
update) and `events` (create) are included in the chart's Role.

The rest of the server (HTTPS API, admin UI, metrics) keeps running on
**every** replica; only the inactivity loops are elected.

### Configuration

| Config key | Default | Description |
|------------|---------|-------------|
| `inactivity.leaderElection.enabled` | `true` (chart) | Elect the inactivity monitor via a Lease. Must be enabled when running more than one replica. |
| `inactivity.leaderElection.leaseName` | `<instanceName>-inactivity` | Name of the Lease object. |
| `inactivity.leaderElection.leaseNamespace` | pod namespace | Namespace of the Lease object. |
| `inactivity.leaderElection.leaseDuration` | `15s` | How long a non-leader waits before taking over an unresponsive leader. |
| `inactivity.leaderElection.renewDeadline` | `10s` | How long the leader retries renewing the lease before giving up. |
| `inactivity.leaderElection.retryPeriod` | `2s` | Interval between lease (re)acquisition attempts. |
| `inactivity.leaderElection.releaseOnCancel` | `true` | Release the lease immediately on graceful shutdown. |

The server refuses to start when leader election is enabled with an invalid
configuration (missing lease name, malformed durations, or
`leaseDuration <= renewDeadline`).

With a single replica (`replicas: 1`), leader election can be disabled
(`inactivity.leaderElection.enabled: false`): the inactivity loops then run
directly, like before.

## How activity is detected

Activity is defined at the network level, not by code-server usage: a
workspace is considered **active when its Envoy TLS termination sidecar
reports more than `10` active downstream HTTPS connections** (the constant
`DefaultMinActiveConnections`). The activity source polls, for every running
workspace pod:

```
GET http://<pod IP>:9901/stats?format=json&filter=http.codx_workspace.downstream_cx_active
```

with a 5-second per-pod timeout. When the reported count is strictly greater
than `10`, the current time is recorded as the workspace's last activity.

Notes on the threshold:

- The polled stat counts **downstream** connections: browsers, IDE tabs,
  proxies - anything connected to the workspace through Envoy.
- The strict `> 10` threshold is a deliberate hysteresis: a handful of
  lingering connections (an idle tab, a monitoring probe, a half-closed
  socket) does not count as activity. A workload or an interactive session
  easily exceeds it.
- Timestamps only move forward: the recorded time is the latest poll at
  which activity was seen, so a workspace must be idle (below the threshold)
  on **every** poll for a full delay before it is stopped.
- The Envoy admin port (9901) and the connection-manager stat prefix
  (`codx_workspace`) are set by the chart's Envoy ConfigMap and must stay in
  sync with the Go constants `DefaultEnvoyAdminPort` and
  `DefaultConnectionStatName` (`internal/inactivity/connectioncount.go`).
- A single unreachable pod is logged (debug level) and skipped; it does not
  block the other pods. Workspaces whose pod no longer exists are forgotten
  by the source.

## What "stop" means

When a workspace exceeds its inactivity delay, the watcher calls
`StopWorkspace`, which **deletes the workspace pod**. Everything else is
kept:

| Object | On stop |
|--------|---------|
| Pod | Deleted |
| Service | Kept |
| cert-manager Certificate | Kept |
| PVC (home directory) | Kept |

The user restarts the workspace at any time from the CodX UI (or it is
restarted by an admin); the pod is recreated with the existing PVC mounted,
so no user data is lost. This is the same operation as the user-facing
"Stop my workspace" button.

A stopped workspace is not stopped repeatedly: once its pod is gone, the
activity source forgets it, and the watcher only stops workspaces for which
activity has been recorded. When the workspace is started again, the new
pod is picked up by the poll and the cycle restarts.

## Watcher semantics

- **Delay per workspace**: each workspace is registered with the
  `inactivityStopDelaySeconds` of its profile.
- **Zero delay means never stop**: a profile with
  `inactivityStopDelaySeconds: 0` (or unset) is never stopped
  automatically.
- **Fresh workspaces are not stopped**: if no activity has ever been
  recorded (a workspace that just started, or whose Envoy has never
  reported more than 10 connections), the watcher skips it. Activity must
  have been seen at least once for the idle clock to apply.
- **Stop condition**: `now - lastActivity >= delay`, evaluated on every
  tick. Because both the poll and the check run at `checkInterval`, the
  effective stop time is accurate to within roughly one interval.
- **Delay changes are picked up**: the registration reconciler re-reads
  the delays on every tick, so editing `inactivityStopDelaySeconds` in a
  `Profile` CR applies to existing workspaces within one interval
  (a shorter delay can stop a workspace sooner, a longer one pushes the
  deadline out). Pods whose profile is not (yet) in the CodX profile cache
  are skipped and retried on the next tick.

## Configuration

### Poll and check interval

`inactivity.checkInterval` (ConfigMap, chart value `inactivity.checkInterval`)
is the common tick of the three loops: how often the Envoy stats are
polled, how often idle workspaces are checked, and how often registrations
are reconciled.

| Config key | Default | Description |
|------------|---------|-------------|
| `inactivity.checkInterval` | `60s` | Go duration. Must be valid, otherwise the server refuses to start; an unset or zero value falls back to `60s`. A shorter interval stops workspaces sooner (and polls more often); a longer one adds up to one interval of slack. |

### Per-profile delay

The delay lives in the `Profile` CRD, so different workspace profiles can
have different idle policies:

```yaml
apiVersion: codx.io/v1
kind: Profile
metadata:
  name: default
spec:
  title: Default
  description: Standard code-server workspace
  inactivityStopDelaySeconds: 1800   # 30 minutes; 0 = never stop
  ...
```

| Field | Default | Description |
|-------|---------|-------------|
| `spec.inactivityStopDelaySeconds` | `1800` | Seconds of inactivity (no poll with more than 10 active connections) after which the workspace pod is stopped. `0` disables automatic stop. |

See the [Profile CRD reference](profile-crd.md) for the full spec.

### Helm chart

```yaml
inactivity:
  checkInterval: 60s

profiles:
  default:
    inactivityStopDelaySeconds: 1800
    ...
```

## Audit events

The watcher reports through the audit logger (same sink as logins and admin
actions):

| Event | Fields | Meaning |
|-------|--------|---------|
| `workspace_stop` | `slug`, `reason=inactivity`, `idle`, `delay` | Workspace pod stopped for inactivity. |
| `workspace_stop_failed` | `slug`, `reason=inactivity`, `idle`, `delay`, `error` | The stop call failed; retried on the next tick. |

## Troubleshooting

| Symptom | Likely cause and check |
|---------|------------------------|
| Workspace stopped although the user was working | The connection count fell to 10 or fewer on every poll: long computations without HTTP traffic, or a thin client with few connections. Consider lowering `checkInterval` to see activity more finely, or raising the threshold trade-off (see `DefaultMinActiveConnections`). |
| Workspace never stopped, although idle | `inactivityStopDelaySeconds: 0` in the profile, no activity ever recorded (fresh workspace), or the Envoy stat name / admin port out of sync with the chart ConfigMap. |
| Stop happens much later than the delay | Expected slack: activity is polled and checked at `checkInterval`, so a workspace can run up to one interval past its deadline. |
| `workspace_stop_failed` in the audit log | The pod deletion failed (Kubernetes API error in the event); the watcher retries every tick. |
| Pod recreated right after being stopped | A user (or admin) restarted the workspace; the watcher resets its stopped flag on the new registration. |

## Related documents

- [Profile CRD reference](profile-crd.md) - `inactivityStopDelaySeconds`
  and the rest of the Profile spec.
- [Documentation index](README.md)
