# Phase 1.4.5 — Production Gates

## Status

**Entering. Runtime implementation has not started.**

Entry evidence:

- previous stage: Phase 1.4.4 Sanitizer — Completed
- Sanitizer implementation PR: `#24 feat: Phase 1.4.4 typed response sanitizer`
- Sanitizer merged baseline: `main@ad1845c97ec783e80763655fd94227e228661871`
- post-merge CI: `kube-aiops CI #81` — `completed / success`
- Required Checks: all green
  - `Preflight / Lint / RBAC`
  - `Secret Scan`
  - `Kubernetes v1.36 Kind E2E`
- Kubernetes baseline: `v1.36.4`

The Phase 1.4.5 implementation branch must be created from the final green `main` produced after this Phase 1.4.4 completion / Phase 1.4.5 entry transition is merged and its own Required Checks are green.

## Goal

Turn the Phase 1.4 security components from individually proven libraries/middleware into a **production-enforceable composition and release gate**.

Phase 1.4.5 must answer:

```text
Can the real production startup path serve protected Portal traffic
only when the required Phase 1.4 security controls are actually wired,
and will CI fail if a future route/configuration change bypasses them?
```

This stage does not add a new user feature surface. It proves that the existing security architecture cannot silently degrade.

## P0 Finding at Entry

The current real binary composition is:

```text
cmd/api/main.go
    ↓
httpapi.NewHandler(logger, backend, readyTimeout)
```

`NewHandler(...)` is intentionally a compatibility constructor from Phase 1.4.1. It preserves the Phase 1.3 runtime until a trusted AuthN provider is selected and wired.

Consequently, on the current production binary path:

- Sanitizer is active through the server default;
- Authenticator is not injected;
- Authorizer is not injected;
- AuditSink is not injected.

This is acceptable as an earlier compatibility boundary but is **not acceptable as a final Phase 1.4 production state**.

Phase 1.4.5 must close this gap without pretending that an insecure placeholder is production authentication.

## Production Composition Invariant

Target invariant:

```text
Production process startup
        ↓
Validated production security composition
        ├── trusted Authenticator present
        ├── Authorizer present
        ├── Audit Sink present
        └── Sanitizer active
        ↓
NewHandlerWithOptions(...)
        ↓
serve protected /api/v1/* traffic
```

If a mandatory production security component is missing or invalid:

```text
startup / readiness
    ↓
FAIL CLOSED
    ↓
protected traffic is not served anonymously
```

A development/test compatibility mode may exist only when it is explicit, isolated and impossible to confuse with production.

## Authentication Boundary

Phase 1.4.5 must **not** weaken the Phase 1.4.1 provider-neutral contract merely to make production wiring convenient.

Forbidden shortcuts:

- static browser bearer token
- credentials hard-coded in frontend source
- long-lived token stored in browser localStorage/sessionStorage
- unsigned `X-User` / `X-Groups` trust
- arbitrary reverse-proxy headers without a cryptographically trusted boundary
- Kubernetes user-token proxying
- browser-provided kubeconfig
- fake production Authenticator that always allows

Concrete enterprise OIDC/SSO/session-provider selection remains a separate integration decision. The Production Gate must prefer an intentionally unavailable production mode over anonymous downgrade when no trusted provider exists.

## Proposed Runtime Boundary

The exact type names may change during implementation, but the architecture should make secure/insecure composition explicit rather than implicit.

Conceptual shape:

```go
type SecurityMode string

const (
    SecurityModeDevelopment SecurityMode = "development"
    SecurityModeProduction  SecurityMode = "production"
)

type SecurityBundle struct {
    Authenticator identity.Authenticator
    Authorizer     authorization.Authorizer
    AuditSink      audit.Sink
    Sanitizer      sanitizer.Sanitizer
}

func (b SecurityBundle) ValidateForProduction() error
```

The production handler factory should require a validated bundle before returning a handler.

The compatibility `NewHandler(...)` may remain for tests/development if required, but `cmd/api` production composition and deployment manifests must not silently rely on it.

## Startup / Readiness Gate

Production configuration must be validated before protected traffic is served.

Minimum failure conditions:

- production security mode selected but Authenticator missing
- Authorizer missing
- AuditSink missing
- invalid sanitizer policy
- contradictory security configuration
- unsupported/insecure provider mode

The failure response/logging contract must not contain tokens, cookies, secrets or raw provider configuration.

If startup validation fails, prefer process startup failure. If a component is intentionally initialized asynchronously, readiness must remain false until the security composition is valid; anonymous protected traffic must never be the fallback.

## Route Coverage Gate

CI must fail if a new protected route exists without complete security metadata.

For every `/api/v1/*` operation, prove alignment among:

```text
OpenAPI operation
    =
ServeMux canonical route
    =
requiresAuthentication surface
    =
central protectedRoute mapping
    =
Capability
    =
normalized Scope strategy
    =
Audit canonical-route coverage
    =
typed Sanitizer boundary where applicable
```

The test should be table-driven or generated from centralized metadata rather than relying on manual reviewer memory.

Adding a route without a capability/scope decision must fail CI.

## Integrated Security Pipeline Gate

The complete protected-request order must be proven as a composition, not only as isolated middleware tests:

```text
Request / Correlation metadata
        ↓
Audit Recorder
        ↓
Authentication
        ↓
Authorization
        ↓
Read-only Handler
        ↓
Typed safe projection
        ↓
Sanitizer
        ↓
JSON response
```

Minimum test matrix:

### Authentication

- no identity -> 401
- provider unavailable -> safe 503
- invalid Principal -> safe 503
- AuthZ/backend not invoked before successful AuthN

### Authorization

- authenticated + explicit allow -> handler may run
- authenticated + deny -> 403
- missing/failed Authorizer -> fail closed
- namespace/cluster scope remains normalized
- Finding Detail uses resolved real scope

### Sanitizer

- successful allowed response is sanitized before emission
- structural violation -> stable 502
- original unsafe value absent from response/log/Audit
- Sanitizer must run only after Finding Detail real-scope AuthZ

### Audit

- 401 recorded safely
- 403 recorded safely
- 2xx recorded safely
- 5xx/sanitizer block recorded safely
- Audit sink failure does not alter security decision or response
- no Authorization/Cookie/body/raw Finding payload is captured

## Browser Security Gate

Production Gates should promote the existing browser checks into explicit security regressions:

- unauthenticated API -> Authentication Required state
- authenticated but forbidden -> Access Denied state
- transport/backend error remains distinct from 401/403
- script-like Finding content cannot execute
- no unexpected DOM probe node is created
- no Finding `innerHTML` regression
- frontend hiding alone never grants or denies authorization

## Kubernetes / RBAC Gate

Phase 1.4.5 must keep the existing Kubernetes safety boundary immutable.

Required negative assertions:

- Secret reads denied
- pods/log denied
- create denied
- update denied
- patch denied
- delete denied
- deletecollection denied
- Mutation disabled
- Auto Remediation disabled

Required positive assertions remain limited to the existing safe read-only resources needed by the Portal/K8sGPT integration.

Do not widen ServiceAccount permissions to make a security E2E pass.

## API / DTO / Leakage Gate

Keep the existing Contract Gate and strengthen cross-layer coverage where needed:

- OpenAPI / Go DTO mapping remains exact
- generated TypeScript client drift check remains mandatory
- raw Kubernetes object fields cannot appear in safe response DTOs
- raw K8sGPT Result CR cannot appear in safe response DTOs
- Secret fields cannot enter Portal DTOs
- request/response bodies do not enter Audit
- sanitizer failures return only stable generic errors
- authentication/authorization provider errors remain generic

## Secret Scan Gate

Gitleaks remains mandatory with no security-test-specific bypass.

Security tests must construct high-confidence credential fixtures in ways that do not commit real-looking secrets to repository history. The Phase 1.4.4 JWT-fixture incident is the precedent: fix the fixture/history, not the Secret Scan rule.

## Existing CI Gates

These checks remain mandatory:

### `Preflight / Lint / RBAC`

Includes:

- Bash syntax
- ShellCheck
- YAML lint
- platform baseline consistency
- API Contract Gate
- Go fmt/vet/tests/build
- Web lint/typecheck/tests/build
- Docker backend smoke
- Docker web smoke
- browser E2E
- project preflight

### `Secret Scan`

- full repository/PR-range Gitleaks enforcement

### `Kubernetes v1.36 Kind E2E`

- Kubernetes `v1.36.4` baseline validation
- lifecycle / rollback / concurrency / trusted uninstall
- Phase 1.2 readonly API E2E

The Required Check names are governance contracts and must stay exactly:

1. `Preflight / Lint / RBAC`
2. `Secret Scan`
3. `Kubernetes v1.36 Kind E2E`

Do not rename them without an explicit GitHub ruleset migration.

## Production-Gate Test Artifacts

Recommended initial implementation structure:

```text
internal/runtime/ or internal/security/
├── mode.go
├── bundle.go
├── validate.go
└── *_test.go

internal/httpapi/
├── security_composition_test.go
└── route_security_gate_test.go

cmd/api/
└── main / composition wiring updates

tests/
├── production-security-gate-test.*
└── secure-api-e2e* (only if a trusted deterministic test adapter is cleanly isolated)
```

Exact directories are secondary to the invariants: production composition must be explicit, independently testable and fail closed.

## Test-Only Adapters

Deterministic test adapters are acceptable only inside test code/test binaries to prove middleware composition.

They must not become selectable production providers through normal runtime configuration.

CI must make it difficult to accidentally compile or enable an `allow-all` identity mechanism in production wiring.

## Logging / Audit Boundary

Production gate failures may log only bounded stable metadata such as:

```text
reason=security_bundle_invalid
component=authenticator
mode=production
```

Do not log:

- Authorization headers
- Cookie headers
- session tokens
- provider secrets
- kubeconfig
- ServiceAccount bearer token
- request/response bodies
- raw sanitizer input

## No Privilege Expansion

Phase 1.4.5 is a composition/release-hardening stage.

It must not add:

- Secret reads
- Pod Logs
- raw Kubernetes explorer
- raw K8sGPT Result viewer
- arbitrary Kubernetes API proxy
- create/update/patch/delete/deletecollection
- Mutation
- Auto Remediation

Events / Prometheus / Loki / Alertmanager correlation remains Phase 2.

## Acceptance Criteria

Phase 1.4.5 is complete only when:

- real production startup path does not silently use the Phase 1.3 compatibility handler
- missing mandatory production AuthN/AuthZ/Audit components fail closed before protected traffic
- Sanitizer remains mandatory
- no insecure placeholder identity mechanism is introduced
- protected-route security coverage is exhaustive and CI-enforced
- integrated AuthN -> AuthZ -> handler -> Sanitizer behavior is proven
- Audit coverage and failure isolation are proven in the integrated path
- browser 401/403/XSS regressions are green
- API Contract / generated-client drift gates are green
- Secret Scan is green without security-test bypasses
- Kubernetes v1.36 Kind E2E is green
- no Kubernetes privilege expansion occurs
- implementation PR passes all three Required Checks
- implementation is merged to `main`
- resulting `main` passes all three Required Checks

Only after these conditions are satisfied may **Phase 1.4 as a whole be marked Completed**.

## Non-Goals

Not part of Phase 1.4.5:

- choosing the enterprise OIDC/SSO vendor
- implementing fake production SSO
- full login/logout UX for a specific IdP
- Events/Prometheus/Loki/Alertmanager correlation
- distributed tracing
- Pod Logs
- Secret viewer
- write/remediation operations
- Auto Remediation

The next roadmap phase after Phase 1.4 completion remains **Phase 2 — Events + Prometheus + Loki + Alertmanager correlation**.
