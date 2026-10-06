# Authentication

CodX authenticates users through an OIDC provider (authorization code flow)
and, as a fallback for administrators, through client certificates terminated
at the ingress (mTLS). This document describes both mechanisms, the session
management they share, the Traefik objects the Helm chart can create for the
mTLS fallback, and the security model.

## Overview

```
                    +-----------------------+        +---------------------+
  Browser           |  Traefik ingress      |        |  OIDC provider      |
  (no client cert)  |  TLSOption (optional) |        |  (e.g. Forgejo)     |
       -------------|                       |--------|                     |
       |            |  passTLSClientCert    |        +---------------------+
       |            |  Middleware           |
       |            +-----------+-----------+
       |                        |
       |            +-----------v-----------+
       +----------->|  CodX server           |
                    |  /auth/login          |  OIDC flow
                    |  /auth/callback        |
                    |  session middleware    |  mTLS fallback (admins)
                    +-----------+-----------+
                                |
                    +-----------v-----------+
                    |  Redis/Valkey          |
                    |  session store         |
                    +-----------------------+

  Admin browser     Traefik validates the client certificate against the
  (client cert)     TLSOption CA, forwards the Common Name in the
                    X-Forwarded-Tls-Client-Cert-Info header, and CodX grants
                    an admin session if the CN is in the allow list.
```

Both mechanisms produce the same artifact: a session stored in Redis/Valkey
and referenced by the `codx-session` cookie.

## OIDC authentication

CodX uses the OAuth2 authorization code flow with the OIDC provider
configured in the ConfigMap. All routes except `/healthz`, `/auth/login` and
`/auth/callback` require a valid session (`/auth/logout` without a session
simply redirects to the login page).

### Login flow

1. `GET /auth/login` - CodX generates a random state parameter, stores it in
   a short-lived `codx-state` cookie (5 minutes, HttpOnly, Secure) and
   redirects to the provider's authorization endpoint.
2. The user authenticates at the provider and is redirected to
   `GET /auth/callback?code=...&state=...`.
3. CodX verifies the state against the cookie, exchanges the code, verifies
   the ID token signature and audience (`clientId`), and extracts the claims.
4. A session is created and stored in Redis/Valkey, and the `codx-session`
   cookie is set.

### Claim extraction

| Config key | Default | Description |
|------------|---------|-------------|
| `oidc.issuer` | - | OIDC issuer URL, used for provider discovery. Required. |
| `oidc.clientId` | - | OAuth2 client ID. Required. |
| `oidc.clientSecret` | - | OAuth2 client secret. Can be overridden by the `CODX_OIDC_CLIENT_SECRET` environment variable. |
| `oidc.redirectUrl` | - | Callback URL (`https://<host>/auth/callback`). Required. |
| `oidc.usernameClaimName` | `preferred_username` | Claim used as the username and as the source of the Kubernetes-safe workspace slug. |
| `oidc.groupClaimName` | `groups` | Claim containing the user's group memberships. Accepted shapes: `[]string`, `[]any` of strings, or a single string. |
| `oidc.adminGroup` | - | Group whose members get admin privileges. Required. |

The username is turned into a slug (`slug.usernameField`, `slug.maxLength`,
default 63 characters) which names all the per-user Kubernetes objects:
Service, Certificate, PVC, Pod.

### Authorization

- **Admin**: a user is an admin when the group claim contains
  `oidc.adminGroup`. Admins see the Admin link in the UI and can use
  `/admin` and `/api/admin/*` (list users, extend storage, stop or delete
  workspaces).
- **Profiles**: workspaces are gated by the profile's `oidcGroups` list; a
  user can start a workspace when any of their groups matches. An empty list
  means all authenticated users. See the
  [Profile CRD reference](profile-crd.md).

## Session management

Sessions are created by both authentication mechanisms and carry the same
information. Their lifetime is set by the `session.ttl` configuration key
(Go duration, default `12h`), which drives three things consistently: the
server-side session expiry (`ExpiresAt`), the session store key expiry in
Redis/Valkey, and the session cookie `MaxAge`.

| Property | Value |
|----------|-------|
| Cookie name | `codx-session` |
| Cookie flags | `HttpOnly`, `Secure`, `SameSite=Lax`, `Path=/` |
| Cookie lifetime | `session.ttl` (default 12 h) |
| Session expiry | `session.ttl` (default 12 h), for OIDC and mTLS sessions alike |
| Store | Redis/Valkey, keys `<instance>:session:<id>`, key TTL = `session.ttl` |
| Session contents | subject, username, slug, groups, admin flag, tokens, expiry |

The OIDC session lives for `session.ttl` from login, regardless of the
OAuth2 token expiry: the tokens are kept in the session but never
re-validated after login.

| Config key | Default | Description |
|------------|---------|-------------|
| `session.ttl` | `12h` | Lifetime of a user session (OIDC login and mTLS fallback alike), applied to the session expiry, the store key expiry and the cookie MaxAge. Must be a positive Go duration (e.g. `8h`, `30m`); the server refuses to start otherwise. |

Sessions are shared between CodX replicas, so the deployment scales
horizontally. Sessions expire server-side; an expired or unknown session
cookie is cleared and the request is redirected to `/auth/login`.

`GET /auth/logout` deletes the session from the store, clears the cookie and
redirects to the login page.

Login, logout, and failed logins are recorded by the audit logger
(`login`, `logout`, `login_failed`, `session_save_failed`) with the remote
client IP.

## mTLS admin fallback

When the OIDC provider is unavailable, administrators can still reach CodX
with a client certificate. The ingress terminates and validates the client
certificate, forwards the certificate Common Name (CN) to CodX in a header,
and CodX grants an admin session when the CN is in the configured allow
list.

### Requirements

- The ingress is Traefik with the CRD provider.
- A Traefik `TLSOption` bound to the ingress route requests and validates
  client certificates against a CA.
- A Traefik `passTLSClientCert` Middleware forwards the certificate
  information to CodX.
- The chart can create all three objects; see
  [Traefik objects in the Helm chart](#traefik-objects-in-the-helm-chart).

### Flow

1. Traefik validates the client certificate against the TLSOption CA chain
   (validity, trust, usage). CodX never sees the raw certificate.
2. The `passTLSClientCert` Middleware sets the header with the selected
   certificate fields. With `info.subject.commonName: true`:

   ```
   X-Forwarded-Tls-Client-Cert-Info: Subject=%22CN%3dalice%2cOU%3dexample%22
   ```

   The value is percent-encoded; after decoding it reads
   `Subject="CN=alice,OU=example"`.
3. CodX's session middleware, when the request has no valid session cookie,
   decodes the header, extracts the CN, and compares it against
   `authFallback.adminCns`.
4. On a match, CodX creates an admin session (valid for `session.ttl`,
   default 24 h) with:
   - `Username` = certificate CN
   - `Slug` = slug derived from the CN
   - `Subject` = `mtls:<CN>`
   - `Groups` = `[oidc.adminGroup]`
   - `IsAdmin` = true

   The session is saved to the store, the `codx-session` cookie is set, and
   the login is audited as `login` with `method=mtls-fallback`.
5. A certificate CN not in the allow list is audited as `login_failed`
   (`reason=certificate common name not allowed`) and the request falls back
   to the normal OIDC login redirect.

CNs are matched exactly (case-sensitive, no wildcards). The header is also
parsed when it is not percent-encoded or when the Subject value is not
quoted; the CN component is found wherever it appears in the distinguished
name (`Subject="O=Example,CN=alice"`).

### Server configuration

```yaml
authFallback:
  enabled: true
  headerName: X-Forwarded-Tls-Client-Cert-Info
  adminCns:
    - alice
    - bob
```

| Config key | Default | Description |
|------------|---------|-------------|
| `authFallback.enabled` | `false` | Enable the mTLS fallback. |
| `authFallback.headerName` | `X-Forwarded-Tls-Client-Cert-Info` | Header carrying the escaped certificate information. |
| `authFallback.adminCns` | - | Client certificate Common Names allowed as admins. At least one is required when enabled, otherwise the server refuses to start. |

### Operational semantics

- **The fallback is always active when enabled**, not only when the OIDC
  provider is down: any request bearing the header with an allowed CN gets
  an admin session. This is deliberate: detecting OIDC availability
  per-request would be fragile, and presenting a valid client certificate is
  a sufficient credential on its own.
- Fallback sessions are full admin sessions: the admin UI and API work as
  with an OIDC admin, and the session survives the outage for its full
  `session.ttl` even if
  the client certificate is not presented again.
- `/auth/login` still redirects to the OIDC provider when it is used.

## Traefik objects in the Helm chart

The `traefikMtls` values create the objects the fallback needs, in the
release namespace:

```yaml
traefikMtls:
  enabled: true
  apiVersion: traefik.io/v1alpha1      # traefik.containo.us/v1alpha1 for Traefik v2
  existingCaSecret: ""                 # or caCertificates via --set-file
  caCertificates: ""
  tlsOption:
    name: ""                          # default: <fullname>-client-mtls
    annotations: {}
    clientAuthType: VerifyClientCertIfGiven
  middleware:
    name: ""                          # default: <fullname>-passtlsclientcert
    annotations: {}
    pem: false
    info:
      subject:
        commonName: true
```

### Created objects

| Object | Name | Purpose |
|--------|------|---------|
| `Secret` | `<fullname>-client-ca` (or `traefikMtls.existingCaSecret`) | CA certificate(s) (key `ca.crt`) used by Traefik to validate client certificates. |
| `TLSOption` | `<fullname>-client-mtls` | Requests and validates the client certificate on the ingress route (`spec.clientAuth`). |
| `Middleware` | `<fullname>-passtlsclientcert` | Forwards the certificate CN to CodX in `X-Forwarded-Tls-Client-Cert-Info` (`passTLSClientCert`, `pem: false`). |

When `traefikMtls.enabled` is true and `ingress.ingressControllerType` is
`traefik`, the chart also wires the ingress annotations automatically:

- appends `<fullname>-passtlsclientcert@kubernetescrd` to
  `traefik.ingress.kubernetes.io/router.middlewares` (your existing list is
  preserved);
- sets `traefik.ingress.kubernetes.io/router.tls.options` to
  `<fullname>-client-mtls@kubernetescrd`, unless you set it yourself.

Both objects are created in the same namespace as the Ingress, so Traefik
resolves the plain-name references. When enabled, the render fails with an
explicit error if neither `existingCaSecret` nor `caCertificates` is set.

### Choosing the client CA

- **Own PKI**: set `traefikMtls.existingCaSecret` to a secret containing the
  CA certificate under key `ca.crt`. The value supports Helm templating, e.g.
  `existingCaSecret: "{{ include \"common.names.fullname\" . }}-ca-crt"` for
  the chart-internal CA.
- **Chart-internal CA**: the chart creates a self-signed CA
  (`<fullname>-ca-crt`). Client certificates signed by the `<fullname>-http`
  issuer (CA based on that secret) are then accepted by the TLSOption. This
  is convenient for testing; production deployments should prefer a dedicated
  PKI so that admin client certificates cannot be confused with
  workspace/server certificates.
- **Inline**: `traefikMtls.caCertificates` with
  `--set-file traefikMtls.caCertificates=client-ca.pem` creates the secret.

### Client certificate policy

`traefikMtls.tlsOption.clientAuthType` controls how Traefik treats clients
without a certificate:

| Value | Browser without client cert | Notes |
|-------|------------------------------|-------|
| `VerifyClientCertIfGiven` (default) | Allowed, OIDC flow works | Recommended for the fallback use case. |
| `RequireAndVerifyClientCert` | TLS handshake fails | Only for deployments where every client has a certificate. |
| `RequireClientCert` | TLS handshake fails | Certificate requested but not verified against the CA. |
| `RequestClientCert` | Allowed, OIDC flow works | Certificate requested but not verified against the CA. |

With the default, users without a client certificate continue through the
normal OIDC flow.

### Issuing an admin client certificate

With the chart-internal CA, a cert-manager `Certificate` produces the
material:

```yaml
apiVersion: cert-manager.io/v1
kind: Certificate
metadata:
  name: admin-alice
  namespace: <codx namespace>
spec:
  commonName: alice            # must match authFallback.adminCns
  secretName: admin-alice-tls
  usages:
    - client auth
  issuerRef:
    name: <fullname>-http      # the chart CA issuer
    kind: Issuer
    group: cert-manager.io
```

Convert the secret to a PKCS#12 importable in the browser:

```sh
kubectl get secret admin-alice-tls -o jsonpath='{.data.tls\.crt}' | base64 -d > admin.crt
kubectl get secret admin-alice-tls -o jsonpath='{.data.tls\.key}' | base64 -d > admin.key
openssl pkcs12 -export -in admin.crt -inkey admin.key -out admin.p12
```

## Security model

- **Certificate validation happens at Traefik.** The TLSOption verifies the
  chain and expiry of the client certificate against the CA (Traefik does
  not check revocation: CRL/OCSP). CodX only matches the forwarded CN
  against the allow list.
- **The fallback header is trusted blindly.** CodX has no way to
  authenticate Traefik at the HTTP layer; the security of the fallback
  therefore relies on the pod not being reachable except through the
  ingress. Enable `networkPolicy.codx.enabled` to restrict traffic to the
  ingress controller pods - otherwise any client able to reach the CodX
  pod directly can forge the header and gain admin access.
- **Allow list granularity.** Access is granted per CN; the certificate
  subject is not otherwise verified. Compromise of an allowed client
  certificate is equivalent to compromise of an admin OIDC account.
- **Sessions outlive the certificate presentation.** A fallback session
  stays valid for its full `session.ttl` even if the certificate is
  revoked; the store TTL is
  the only bound. Rotate the allow list (a ConfigMap rollout) to revoke.
- **Audit trail.** Fallback logins (`login`, `method=mtls-fallback`) and
  denials (`login_failed`, `reason=certificate common name not allowed`)
  are recorded with the client IP; keep the audit log sink enabled.

## Troubleshooting

| Symptom | Likely cause and check |
|---------|------------------------|
| Request with a client certificate still redirects to `/auth/login` | CN not in `authFallback.adminCns` (check the audit log for `login_failed`), `authFallback.enabled` false in the rendered ConfigMap, or the middleware not attached to the router. |
| Traefik logs `router ... has a TLS options reference ... that does not exist` | The TLSOption is not created (`traefikMtls.enabled`) or the `router.tls.options` annotation points to a non-existent object. |
| Header missing on the CodX side | The `passTLSClientCert` Middleware is not in the router's middleware chain, or `info.subject.commonName` is not selected (`traefikMtls.middleware.info`). |
| CN parsed as empty | The forwarded Subject does not contain a `CN=` component; inspect the header with `curl -v --cert admin.crt --key admin.key https://<host>/healthz`. |
| Helm render fails with `traefikMtls: set ... existingCaSecret or ... caCertificates` | Expected guard: provide a CA source before enabling `traefikMtls.enabled`. |
| Browsers fail the TLS handshake with `RequireAndVerifyClientCert` | Use `VerifyClientCertIfGiven` to keep the OIDC flow available. |

## Related documents

- [Profile CRD reference](profile-crd.md) - profile-based workspace templates
  and OIDC group gating.
- [Authentication index](README.md)
