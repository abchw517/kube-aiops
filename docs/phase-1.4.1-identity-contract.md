# Phase 1.4.1 — Identity Contract

## Status

**Completed.**

Completion evidence:

- entry baseline: `main@0248de41466d3e4746f1f6e3b1035a95c63a7940`
- implementation branch: `phase-1.4-authn-authz-audit-sanitizer`
- implementation PR: `#18 feat: Phase 1.4.1 Identity Contract`
- merged baseline: `main@7b13b250f035657a1edf24e9590a6560488186bf`
- post-merge CI: run `#63`
- Required Checks: all green
  - `Preflight / Lint / RBAC`
  - `Secret Scan`
  - `Kubernetes v1.36 Kind E2E`

The post-merge Kind job also passed Kubernetes `v1.36.4` platform validation, lifecycle/rollback/concurrency/trusted-uninstall E2E, and the Phase 1.2 read-only API E2E.

## Goal

Establish a provider-neutral identity contract before selecting or wiring a concrete SSO/session provider.

Phase 1.4.1 deliberately avoids temporary authentication shortcuts such as static browser tokens,
unsigned identity headers, Kubernetes user-token proxying, or frontend-managed long-lived credentials.

## Principal Contract

The backend identity model is `internal/identity.Principal`:

```text
Principal
├── subject       required, provider-stable identity key
├── provider      required, provider identifier
├── displayName   optional presentation value
└── groups        optional bounded group hints
```

Explicitly excluded:

- bearer tokens
- cookies/session secrets
- passwords
- API keys
- kubeconfig
- Kubernetes ServiceAccount tokens
- authorization decisions/capabilities

Principal values are bounded and reject empty, untrimmed or control-character input before entering
request context.

## Authentication Adapter Boundary

`identity.Authenticator` is the provider-neutral adapter interface:

```go
Authenticate(context.Context, *http.Request) (Principal, error)
```

`httpapi.NewHandlerWithOptions(..., HandlerOptions{Authenticator: ...})` activates authentication for
`/api/v1/*` routes. `/healthz` and `/readyz` remain outside the user authentication boundary.

The compatibility constructor `httpapi.NewHandler` intentionally keeps the Phase 1.3 runtime behavior
until a concrete trusted provider is selected and wired. This avoids pretending that a static token or
spoofable header is production authentication.

When an Authenticator is configured, behavior is fail-closed:

```text
No valid identity               -> 401 AUTHENTICATION_REQUIRED
Provider unavailable/error      -> 503 AUTHENTICATION_UNAVAILABLE
Invalid Principal from provider -> 503 AUTHENTICATION_UNAVAILABLE
Valid Principal                 -> context -> read-only handler
```

The authorization-denied contract is reserved as:

```text
403 AUTHORIZATION_DENIED
```

Actual capability/scope policy evaluation is Phase 1.4.2.

## Request / Correlation Identity

Every HTTP request passes through request metadata middleware before AuthN.

Response headers:

```text
X-Request-ID
X-Correlation-ID
```

Rules:

- safe inbound identifiers may be preserved;
- unsafe or missing request IDs are replaced with a server-generated opaque ID;
- missing/unsafe correlation ID defaults to the validated request ID;
- identifiers are bounded to 128 characters and restricted to a safe ASCII character set;
- AuthN errors therefore always carry traceable request/correlation headers.

These identifiers contain no credentials and are designed for the Phase 1.4.3 audit pipeline.

## OpenAPI Contract

`api/openapi.yaml` is contract version `1.4.1`.

Changes:

- `Principal` schema added and mapped to the Go JSON projection contract;
- reusable `RequestID` and `CorrelationID` response headers added;
- standardized `Unauthorized` and `Forbidden` responses added;
- every `/api/v1/*` operation declares `401` and `403` responses;
- API operations that can fail due to an unavailable AuthN provider expose `503`;
- health endpoints remain public;
- no provider-specific OpenAPI security scheme is claimed before a trusted provider is selected.

`tools/openapi/contract.py` fails CI if:

- `Principal` drifts between Go and OpenAPI;
- the two identity headers disappear from 401/403 responses;
- any `/api/v1/*` operation loses its 401/403 contract;
- the generated TypeScript client is stale.

## Generated TypeScript Client

The generated client remains sourced exclusively from `api/openapi.yaml`.

Phase 1.4.1 adds the generated `Principal` type. No handwritten duplicate identity DTO is permitted in
`web/`.

A concrete browser session mechanism is intentionally not embedded in generated client code at this
stage. The selected AuthN provider must preserve the Phase 1.4 rule that long-lived credentials are not
stored in frontend source code or browser local storage.

## Security Properties

Phase 1.4.1 does not expand Kubernetes privileges.

Still forbidden:

- Secret reads
- Pod Logs
- raw Kubernetes object passthrough
- raw K8sGPT Result CR passthrough
- create/update/patch/delete/deletecollection
- Mutation
- Auto Remediation
- arbitrary Kubernetes API proxying

Authentication provider failures do not downgrade to anonymous access when an Authenticator is active.
Authentication error bodies are generic and do not include provider errors, tokens, headers or session
material. Raw provider errors are not logged.

## Test Coverage

Go tests cover:

- valid Principal contract;
- malformed/oversized/control-character Principal rejection;
- Principal context propagation;
- request/correlation ID generation and propagation;
- safe inbound identifier preservation;
- unsafe identifier replacement;
- health endpoint AuthN bypass;
- unauthenticated API -> 401;
- provider failure -> fail-closed 503;
- invalid provider Principal -> fail-closed 503;
- authenticated Principal injection;
- reserved authorization-denied -> 403 contract.

Existing Phase 1.1/1.2/1.3 tests and Kubernetes v1.36 Kind E2E remain mandatory and passed on both PR and post-merge main verification.

## Phase Boundary

Not implemented in Phase 1.4.1:

- concrete OIDC/SSO/session provider
- capability evaluation
- namespace/cluster authorization scopes
- frontend login/logout/session UX
- audit sink
- sanitizer
- Events/Prometheus/Loki/Alertmanager correlation

Next active stage: **Phase 1.4.2 — Authorization** on `phase-1.4.2-authorization`.
