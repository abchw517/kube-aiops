# Phase 1.4 — Authentication / Authorization / Audit / Sanitizer

## Status

**Phase 1.4 active — current stage: Phase 1.4.5 Production Gates entering.**

Completed stages:

- Phase 1.4.1 Identity Contract — completed on `main@7b13b250f035657a1edf24e9590a6560488186bf`; post-merge CI #63 green.
- Phase 1.4.2 Authorization — completed on `main@8186bfeff2682677b468ebc397cf248afd2f3213`; post-merge CI #70 green.
- Phase 1.4.3 Audit — completed on `main@10e152dd54b0f98d77bd0595e54c4b021d7679f8`; implementation PR #22 and post-merge CI #76 passed all Required Checks.
- Phase 1.4.4 Sanitizer — completed on `main@ad1845c97ec783e80763655fd94227e228661871`; implementation PR #24 and post-merge CI #81 passed all Required Checks.

For Phase 1.4.4, post-merge CI #81 passed:

- `Preflight / Lint / RBAC`
- `Secret Scan`
- `Kubernetes v1.36 Kind E2E`

The Kind job also passed Kubernetes `v1.36.4` platform validation, lifecycle/rollback/concurrency/trusted-uninstall E2E, and the Phase 1.2 read-only API E2E.

The Phase 1.4.5 implementation branch must be created from the green `main` produced after this Phase 1.4.4 completion / Phase 1.4.5 entry transition is merged and its own Required Checks are green.

## Goal

Add a production-grade identity, authorization, accountability and response-safety boundary around the existing read-only Portal without expanding Kubernetes mutation or sensitive-data privileges, and prove that the complete security composition cannot be silently bypassed in the production startup path.

```text
User / SRE
    ↓
Trusted Authentication
    ↓
Deny-by-default Authorization
    ↓
Read-only Portal API
    ↓
Typed Safe Projection
    ↓
Sanitizer
    ↓
OpenAPI Response

Every protected request
    ↓
Structured Audit Event
```

## Completed Security Layers

### Authentication — Phase 1.4.1

- provider-neutral validated `Principal`
- request/correlation IDs
- injectable `Authenticator`
- stable 401/503 fail-closed behavior when AuthN is configured
- no static browser token, unsigned identity header, Kubernetes user-token proxy, or frontend-managed long-lived credential shortcut

### Authorization — Phase 1.4.2

Backend-enforced, deny-by-default read-only capabilities:

- `clusters:list`
- `namespaces:list`
- `findings:list`
- `findings:summary`
- `findings:read`
- `resources:read`

Authorization is cluster/namespace scoped. Finding Detail resolves the safe Finding and authorizes its real normalized scope before response emission.

### Audit — Phase 1.4.3

Structured bounded audit events include:

- request/correlation IDs
- Principal subject/provider only
- canonical route pattern
- capability
- normalized cluster/namespace scope
- outcome
- HTTP status
- latency

Audit excludes Authorization/Cookie/token material, request/response bodies, raw Kubernetes objects, raw K8sGPT Result CR data and opaque Finding IDs. Sink delivery failure never weakens AuthN/AuthZ or changes the handler response.

### Sanitizer — Phase 1.4.4

A typed, deterministic defense-in-depth response Sanitizer is active for the safe Finding/Resource response boundary.

It provides:

- centralized immutable validated policy defaults
- `problem <= 2 KiB`
- `details <= 8 KiB`
- bounded aggregate Finding page diagnostic text
- UTF-8-safe deterministic truncation
- high-confidence credential/header/private-key/JWT redaction
- active script/event-handler/javascript neutralization
- identifier/status/summary structural validation
- stable fail-closed `RESPONSE_SANITIZATION_FAILED`
- browser XSS regression proving unsafe diagnostic text cannot become active DOM

Sanitizer does not justify Secret reads, Pod Logs, raw Kubernetes objects, raw Result CR payloads or arbitrary-object passthrough.

## Phase 1.4.5 — Production Gates

Phase 1.4.5 is not a new feature surface. It is the final composition and release gate that proves the Phase 1.4 security layers are actually enforced by the production runtime and cannot be accidentally omitted when the code evolves.

### P0 — Production runtime composition gate

Current repository reality at Phase 1.4.5 entry:

```text
cmd/api/main.go
    ↓
httpapi.NewHandler(...)
```

`NewHandler(...)` is intentionally the Phase 1.3 compatibility constructor and does not inject Authenticator, Authorizer or AuditSink. Phase 1.4.5 must therefore prevent the production startup path from silently using that compatibility mode.

Required production property:

```text
Production startup
    ↓
Trusted security composition
    ├── Authenticator required
    ├── Authorizer required
    ├── Audit Sink required
    └── Sanitizer always active

missing/invalid mandatory security component
    ↓
FAIL CLOSED before serving protected traffic
```

A development/test compatibility mode may remain explicit and isolated, but it must not be the implicit production path.

The gate must not invent insecure substitutes such as:

- static browser bearer tokens
- unsigned `X-User` / `X-Groups`
- fake production identity providers
- browser-supplied Kubernetes credentials
- Kubernetes user impersonation shortcuts

Concrete enterprise OIDC/SSO/session-provider selection remains a separate integration decision. If no trusted provider is available, production mode must remain intentionally unavailable rather than downgrade to anonymous access.

### P1 — Protected-route coverage gate

CI must fail when a new `/api/v1/*` route is added without all required security metadata.

For every protected route, prove:

```text
ServeMux route
  = OpenAPI operation
  = AuthN-required API surface
  = centralized capability mapping
  = normalized scope rule
  = Audit canonical-route coverage
  = typed Sanitizer coverage where response data crosses the safe projection boundary
```

No handler-specific ad hoc bypass should be possible.

### P1 — Integrated security composition tests

Add deterministic test-only composition proving the complete order:

```text
Request Metadata
    ↓
Audit Recorder
    ↓
Authentication
    ↓
Authorization
    ↓
Read-only Handler
    ↓
Sanitizer
    ↓
JSON response
```

Minimum scenarios:

- unauthenticated protected request -> 401 before AuthZ/backend
- AuthN provider unavailable -> safe 503
- authenticated but denied -> 403 before backend response emission
- allowed scoped request -> 200
- Finding Detail -> real Finding scope authorized before Sanitizer
- Sanitizer structural block -> stable 502, no unsafe payload
- Audit event emitted for 401/403/2xx/5xx without sensitive material
- Audit sink failure does not alter security decision or response
- health/readiness boundary remains explicit and independently tested

### P1 — Browser security gates

Browser E2E must continue proving:

- 401 renders unauthenticated/session-required state
- 403 renders authenticated-but-forbidden state
- Finding diagnostic script-like payload cannot execute or create unexpected DOM
- no `innerHTML`-style unsafe Finding rendering is introduced
- frontend state hiding is never treated as authorization enforcement

### P1 — Kubernetes and RBAC negative gates

Required Checks must continue proving:

- Kubernetes baseline `v1.36.4`
- fixed ServiceAccount/RBAC model
- Secret read denied
- pods/log denied
- create/update/patch/delete/deletecollection denied
- lifecycle / rollback / concurrency / trusted uninstall E2E green
- Phase 1.2 readonly API E2E green

Production Gates must not expand RBAC to make a security test pass.

### P1 — Contract / leakage / supply-chain gates

Keep mandatory:

- API Contract Gate
- generated TypeScript client drift check
- Go vet/tests/build
- Web lint/typecheck/tests/build
- Docker backend smoke
- Docker web smoke
- browser E2E
- project preflight
- Secret Scan / Gitleaks

Security fixtures must not require disabling or allowlisting Secret Scan rules.

### P1 — Required Check identity

These Required Check names are repository governance contracts and must remain exact:

1. `Preflight / Lint / RBAC`
2. `Secret Scan`
3. `Kubernetes v1.36 Kind E2E`

Do not rename them without an explicit ruleset migration.

## Explicit Non-Goals

Phase 1.4 does **not** add:

- Pod Logs
- Secret reads
- raw Kubernetes YAML/JSON explorer
- raw Result CR viewer
- create/update/patch/delete/deletecollection
- Mutation
- Auto Remediation
- arbitrary Kubernetes API proxying
- browser identity impersonation of Kubernetes users
- Events/Prometheus/Loki/Alertmanager correlation

Observability correlation remains Phase 2.

## Phase 1.4.5 Acceptance

Phase 1.4.5 is complete only when all of the following are true:

- production runtime cannot silently use the unauthenticated compatibility constructor
- production security composition fails closed when mandatory trusted components are absent/invalid
- no insecure placeholder AuthN mechanism is introduced
- protected-route coverage is exhaustive and CI-enforced
- integrated AuthN -> AuthZ -> handler -> Sanitizer -> Audit behavior is proven
- browser 401/403/XSS security regressions are proven
- no Kubernetes privilege expansion occurs
- API Contract Gate green
- Go/Web/Docker/browser/project preflight green
- Secret Scan green
- Kubernetes v1.36 Kind E2E green
- implementation PR Required Checks all green
- implementation merged to `main`
- resulting `main` Required Checks all green

At that point **Phase 1.4 as a whole may be marked Completed**.

## Acceptance Principle

```text
AuthN contract proven
    +
AuthZ deny-by-default proven
    +
Audit leakage safety proven
    +
Sanitizer response safety proven
    +
Production composition cannot silently bypass them
    +
No privilege expansion
    +
PR Required Checks green
    +
Merge to main
    +
main Required Checks green
```
