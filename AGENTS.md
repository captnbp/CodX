# AGENTS.md

Guidance for coding agents (and humans) working on CodX. The project runs
an isolated, per-user code-server workspace on Kubernetes: one Go binary, one
Helm chart, one CRD. Keep it that way - see [CONTRIBUTING.md](CONTRIBUTING.md)
for the ground rules and [docs/](docs/) for the feature documentation.

## Repository layout

| Path | Content |
|------|---------|
| `cmd/codx` | server entrypoint and wiring (leader election, loops, metrics) |
| `api/profile/v1` | the `Profile` CRD types |
| `internal/web` | HTTP server: auth, embedded HTML/JS UIs, admin, SSE endpoints |
| `internal/k8s` | workspace objects, usage, metrics clients, Kubernetes fakes |
| `internal/inactivity` | activity detection, watcher, leader-elected monitor |
| `internal/leader` | Lease-based leader election |
| `internal/oidc`, `internal/session` | OIDC login, Redis/Valkey session + activity stores |
| `internal/proxy` | mTLS reverse proxy to workspace Envoy sidecars |
| `internal/config` | ConfigMap configuration model (YAML + defaults + validation) |
| `internal/metrics`, `internal/tracing` | Prometheus gauges, OpenTelemetry setup |
| `charts/codx` | Helm chart (values, templates, `dashboards/`, README) |
| `hack/` | doc and metadata check scripts |
| `docs/` | feature documentation (keep it factual, update with the code) |

## Language and style

- **English everywhere**: code, comments, commit messages, docs, chart values.
- `gofmt` clean; comments explain *why*, not *what*; minimal diffs that match
  the surrounding style.
- Conventional commits (`feat:`, `fix:`, `docs:`, `chore:`), as used in the
  history.

## Validation suite

Run the relevant subset after every change; run the full suite before
declaring anything done. These are the exact commands used during
development of the current codebase.

### Go (any change under `cmd/`, `api/`, `internal/`)

```sh
go build ./...
go vet ./...
go test -race -covermode=atomic ./...
gofmt -l internal/ cmd/ api/        # empty output = clean
```

- Tests must pass under `-race`. New behavior comes with table-driven tests
  using the in-memory fakes (`internal/k8s/fake` for the web package,
  `fake_client_test.go` inside `internal/k8s`).
- `gofmt -l` must come back empty for the files you touched
  (`internal/oidc/*` are pre-existing exceptions - do not reformat them
  without reason).
- If golangci-lint is installed: `golangci-lint run --timeout=4m ./...`
  (0 issues expected; it is a CI gate).

### SAST (gosec) - security-sensitive or Go changes

```sh
go install github.com/securego/gosec/v2/cmd/gosec@v2.29.0   # version pinned in CI
"$(go env GOPATH)/bin/gosec" -fmt=sarif -out=results.sarif ./...   # exit 0 = clean
```

- Zero findings is the baseline. Do not exclude rules globally: fix the
  finding, or annotate with `// #nosec GXXX -- justification` like the
  existing annotations in `cmd/codx/main.go` and `internal/session`.
- **Never combine `-quiet` with `-out`**: `-quiet` suppresses the report
  output entirely, the SARIF file is never written, and the CI upload step
  fails with "Path does not exist" (this broke the CI once).

### CVE scan (Go module changes)

```sh
make scan-cves
```

Runs `trivy filesystem --scanners vuln --severity HIGH,CRITICAL
--ignore-unfixed --exit-code 1 .` over `go.mod`/`go.sum` - the same policy
as the CI image scan. Fix findings by bumping the module
(`go get module@version && go mod tidy`), then re-run the Go suite.
Do not add entries to a trivyignore without saying so.

### Helm chart (any change under `charts/codx/`)

```sh
helm lint charts/codx
helm lint charts/codx -f charts/codx/values-test.yaml
helm template codx charts/codx > /dev/null
helm template codx charts/codx -f charts/codx/values-test.yaml > /dev/null
make check-values-docs
```

- `helm template` must render for the default values AND values-test.
  When adding a conditional template, also render its enabled/disabled
  variants (the CI "Helm template" step uses its own `--set` values - keep
  that step in sync when adding required values).
- `make check-values-docs` (hack/check_values_docs.py) enforces the Bitnami
  readme-generator contract on `values.yaml`: every **leaf** key must have a
  `## @param <key>` comment, every `@param` must match an existing key, and a
  `@param ... [object]`/`[array]` hint covers a whole subtree. Run it after
  any values.yaml change.
- The dashboard JSON lives in `charts/codx/dashboards/` and is injected via
  `.Files.Get` (so `{{pod}}` legends are not parsed as Helm expressions);
  validate the rendered ConfigMap parses as JSON.
- When the CRD (`charts/codx/crds/`) changes, regenerate the docs with
  `make docs-crd`.

### Workflows (changes under `.github/`)

Validate the YAML parses (`python3 -c "import yaml; yaml.safe_load(open(...))"`)
and check the step order and permissions of the modified job.

## CI gates

A change is only done when these pass - they mirror the local commands above:

- **go**: `go vet` + `go test -race` with coverage
- **lint**: golangci-lint
- **security**: gosec v2.29.0, SARIF uploaded to code scanning (needs
  `security-events: write` + `actions: read`)
- **helm**: dependency update, lint, template render check, package, OCI push
  + cosign sign
- **docker**: amd64 build -> Trivy CVE gate (HIGH/CRITICAL, ignore-unfixed)
  -> CycloneDX SBOM (artifact + cosign attest) -> multi-arch build/push ->
  cosign sign. The image is scanned **before** it is pushed.

Pull requests are reviewed by the code owners (`.github/CODEOWNERS`).

## Project gotchas

- **Embedded HTML/JS is rendered with `fmt.Fprintf`**: every literal `%` in
  the JS must be escaped as `%%`, otherwise `go vet` fails on the format
  string. `TestAdminUIDefinesReferencedFunctions` (internal/web) guards
  against calling an undefined JS function in the admin UI - a
  `ReferenceError` in `loadUsers` breaks the whole users table (that shipped
  once).
- **PVC annotations store RFC3339 timestamps (second precision)**: tests
  comparing them against `time.Now()` must truncate to the second, and must
  not assert that two writes milliseconds apart produce increasing values.
- **The Kubernetes fake PVC Create stamps `CreationTimestamp`** (like the API
  server): tests relying on the workspace creation date work out of the box.
- **Admin sessions bypass profile group gating** (`IsAdmin` in
  `internal/web`): mTLS fallback sessions claim no OIDC group on purpose
  (no fake `oidc.adminGroup` membership).
- **Session lifetime** comes from `session.ttl` (default in
  `internal/config.DefaultSessionTTL`): never hardcode `24h`/`12h` in
  tests or code, use the constant.
- **Workspace object names** are `<instanceName>-<slug>`, truncated to
  `slug.maxLength`: name collisions are possible for very long usernames
  (documented in docs/workspace-creation.md).
- **No database**: per-user facts that must survive restarts live on the
  workspace PVC (annotations), shared facts in Redis/Valkey (sessions,
  inactivity tracking context). Keep it that way.
