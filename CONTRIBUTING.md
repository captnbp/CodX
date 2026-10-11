# Contributing to CodX

Thanks for considering a contribution. CodX is a small, focused codebase:
one Go binary, one Helm chart, one CRD. Keep it that way.

## Getting started

Requirements:

- Go 1.27+
- Helm 4 (for chart work)

```sh
git clone https://github.com/captnbp/codx
cd codx

# Build and test the Go code
go build ./...
go test -race -covermode=atomic ./...

# Scan the Go modules for known CVEs (same gate as the CI image scan;
# requires trivy on PATH)
make scan-cves

# Render and lint the Helm chart
helm template codx charts/codx
helm lint charts/codx -f charts/codx/values-test.yaml
```

## Project layout

| Path | Content |
|------|---------|
| `cmd/codx` | server entrypoint and wiring |
| `api/profile/v1` | the `Profile` CRD types |
| `internal/web` | HTTP server: auth, UI, admin, SSE endpoints |
| `internal/k8s` | workspace objects, usage, metrics clients |
| `internal/inactivity` | activity detection and idle workspace stop |
| `internal/leader` | Lease-based leader election of the inactivity monitor |
| `internal/oidc`, `internal/session` | OIDC login and the Redis session store |
| `internal/proxy` | mTLS reverse proxy to workspace Envoy sidecars |
| `internal/config` | ConfigMap configuration model |
| `charts/codx` | Helm chart (values, templates, dashboard) |
| `docs/` | feature documentation |

## Ground rules

- **Small and specialized.** CodX only targets Kubernetes and only does
  workspaces. Features that need an abstract plugin layer, a second
  runtime or a database will be rejected.
- **No breaking changes to running workspaces.** The per-user objects
  (Service, PVC, Certificate, Pod) are the user's data; a CodX upgrade must
  never delete or rename them.
- **Security defaults.** TLS is mandatory, the mTLS fallback header is only
  as trustworthy as the ingress - keep NetworkPolicy guidance and audit
  events up to date when touching auth code.
- **Everything gets tests.** New behavior comes with Go tests
  (`go test -race`). Chart changes must keep `helm template` and
  `helm lint` green, and new values must be documented with `@param`
  comments in `values.yaml`.
- **Docs follow code.** Features under `docs/` are factual: update them in
  the same change, not later.

## Coding conventions

- `gofmt` clean; comments explain *why*, not *what*.
- Match the existing style: table-driven tests, logr structured logging,
  explicit error wrapping.
- HTML/JS embedded in `internal/web` is plain Bootstrap and vanilla JS;
  no bundler, no framework.
- The Helm chart follows the Bitnami conventions (common library helpers,
  `common.tplvalues.render` passthroughs, `@param` documentation).

## Commit messages

Conventional commits, as used throughout the history:

```
feat: add configurable session TTL for OIDC and mTLS fallback logins
fix: ...
docs: ...
chore: ...
```

## Making a change

1. Open an issue first for anything non-trivial, so the approach can be
   discussed.
2. Branch from `main`, keep the diff minimal, add tests and doc updates.
3. Make sure `go test -race ./...`, `make scan-cves`, `helm lint` and
   `helm template` pass.
4. Open a pull request with a description of *what* changed and *why*.

Pull requests are automatically assigned to the repository's code owners
for review (see [.github/CODEOWNERS](.github/CODEOWNERS)); no change
merges without their approval.

## License

By contributing, you agree that your contributions are licensed under the
[MIT License](LICENSE).
