# Phase 1.4.2 — Authorization

## Status

**Implementation in progress / CI acceptance pending.**

Entry baseline:

- `main`: `a648c50e650f18313e30e054051e3dd2f07b738b`
- previous stage: Phase 1.4.1 Identity Contract — Completed
- implementation branch: `phase-1.4.2-authorization`
- Kubernetes baseline: `v1.36.4`

## Goal

Add backend-enforced, deny-by-default authorization on top of the provider-neutral `Principal` established in Phase 1.4.1, while preserving the existing read-only Kubernetes security boundary.

Authorization answers only:

```text
Can this authenticated Principal perform this read-only capability
for this requested cluster/namespace scope?
```

It must not turn the Portal into a Kubernetes API proxy and must not derive authorization from frontend visibility.

## Implementation Progress

Implemented on the Phase 1.4.2 branch:

- centralized `internal/authorization` package;
- provider-neutral `Capability`, `Scope`, `DecisionRequest`, `Decision`, and `Authorizer` contracts;
- versioned `v1` deterministic policy model with subject/group selectors;
- explicit allow/deny effects with deny taking precedence over allow;
- no matching policy rule means deny;
- malformed/unknown policy data fails during policy construction rather than becoming an allow;
- centralized mapping for every existing protected read-only API route;
- authorization failures and authorizer errors fail closed;
- authenticated requests with no Authorizer fail closed;
- cluster and namespace scope extraction uses normalized request values;
- Finding Detail resolves the normalized Finding first and authorizes against its real cluster/namespace before returning it;
- deferred Finding authorization checks Principal/Authorizer prerequisites before resolving the Finding, preventing data-source reads under an incomplete security pipeline;
- Web Portal renders explicit `401` authentication-required and `403` authorization-denied states using the generated client `ApiError`;
- frontend remains presentation-only and does not make authorization decisions;
- table-driven backend route/capability/scope tests and deterministic policy tests added.

Still pending before Phase 1.4.2 can be marked Completed:

- PR Required Checks acceptance;
- post-merge `main` Required Checks acceptance.

## Capability Model

Initial capabilities are deliberately small and map to the existing read-only API surface:

| Capability | API intent |
| --- | --- |
| `clusters:list` | enumerate configured Portal clusters |
| `namespaces:list` | enumerate namespaces within an allowed cluster |
| `findings:list` | list safe normalized findings |
| `findings:summary` | read aggregate finding summaries |
| `findings:read` | read one safe normalized finding |
| `resources:read` | read the existing safe Pod/Deployment projection |

Capabilities are backend policy inputs, not Kubernetes RBAC verbs and not OpenAPI roles.

## Scope Model

Initial scope dimensions:

```text
Scope
├── cluster
└── namespace (optional)
```

Rules:

- cluster scope is explicit;
- namespace scope may narrow a cluster grant;
- absent namespace grant does not imply access to every namespace;
- wildcard behavior is explicit as `*` in policy data rather than inferred;
- cluster-only grants do not authorize namespaced requests;
- the current single configured cluster `local` does not justify hardcoding allow-all behavior;
- scope evaluation uses normalized request parameters, not raw URLs.

Examples:

```text
{cluster: local}                 -> cluster-level request only
{cluster: local, namespace: dev} -> local/dev only
{cluster: local, namespace: *}   -> explicitly all namespaces in local
{cluster: *, namespace: *}       -> explicitly all clusters/namespaces
```

## Deny-by-Default

The authorization guard is fail-closed.

```text
Request
  ↓
Request / Correlation ID
  ↓
Authentication
  ↓
Validated Principal
  ↓
Route -> capability mapping
  ↓
Scope extraction / normalization
  ↓
Authorizer.Authorize(...)
  ├── Allow -> existing read-only handler
  ├── Deny  -> 403 AUTHORIZATION_DENIED
  └── Error -> 503 AUTHORIZATION_UNAVAILABLE
```

No policy entry means deny. Authorization provider/internal errors never downgrade to allow.

When AuthN is active but no Authorizer is configured, protected API routes fail closed with `AUTHORIZATION_UNAVAILABLE` rather than silently reverting to the Phase 1.3 anonymous behavior.

## Backend Interfaces

The implementation uses a provider-neutral decision seam:

```go
type DecisionRequest struct {
    Principal  identity.Principal
    Capability Capability
    Scope      Scope
}

type Decision struct {
    Allowed bool
}

type Authorizer interface {
    Authorize(context.Context, DecisionRequest) (Decision, error)
}
```

Properties:

- HTTP handlers do not parse policy documents;
- `Principal` remains identity-only and does not grow authorization state;
- policy evaluation is independently unit-testable;
- route/capability mapping is centralized and exhaustively tested;
- scope normalization is centralized;
- Kubernetes RBAC permissions are unchanged.

## Route / Capability Mapping

| HTTP route | Capability | Scope source |
| --- | --- | --- |
| `GET /api/v1/clusters` | `clusters:list` | explicit global configured-cluster scope |
| `GET /api/v1/clusters/{cluster}/namespaces` | `namespaces:list` | path `cluster` |
| `GET /api/v1/clusters/{cluster}/resources/{kind}/{namespace}/{name}` | `resources:read` | path `cluster`, `namespace` |
| `GET /api/v1/findings` | `findings:list` | normalized query `cluster`, optional `namespace` |
| `GET /api/v1/findings/summary` | `findings:summary` | normalized query `cluster`, optional `namespace` |
| `GET /api/v1/findings/{id}` | `findings:read` | actual normalized Finding `cluster` / `namespace` |

### Finding Detail anti-bypass path

An opaque Finding ID is never treated as authorization scope.

```text
Authenticated request
  ↓
Principal + Authorizer prerequisite check
  ↓
Resolve safe normalized Finding by opaque ID
  ├── not found -> safe 404
  ↓
Extract actual Finding.cluster / Finding.namespace
  ↓
Authorize findings:read on that real scope
  ├── deny -> generic 403, no Finding payload
  └── allow -> return normalized Finding
```

This prevents a principal allowed in one namespace from reading an opaque Finding ID belonging to another namespace.

## Policy Source Boundary

The repository-managed policy evaluator is deterministic and versioned as `v1`.

Policy properties:

- policy contains no credentials or secrets;
- subject and group selectors operate only on the already authenticated `Principal`;
- capabilities must be known centralized capabilities;
- scopes must be explicit;
- empty policy denies;
- malformed policy fails to construct;
- unknown JSON fields are rejected;
- duplicate/conflicting matches are deterministic because matching `deny` overrides matching `allow`;
- browser-supplied unsigned identity headers are never treated as trusted identity.

A concrete enterprise OIDC/SSO/session provider remains separable from authorization policy and is not introduced by this stage.

## Frontend Boundary

Frontend behavior is usability only, not a security control.

Implemented states:

- `401` -> explicit authentication/session-required state;
- `403` -> explicit authenticated-but-forbidden state;
- other backend/transport errors -> existing safe error/retry state.

The Portal uses the generated TypeScript client's `ApiError`; it does not duplicate API DTOs or HTTP status contracts. No long-lived credential is added to local storage or session storage.

## OpenAPI Boundary

The standardized `401` / `403` contract established by Phase 1.4.1 remains stable.

Internal role names, policy-file structure, subject/group mappings, and authorization decisions are not exposed as public API schemas. No raw policy dump endpoint is introduced.

## Security Properties

Still forbidden:

- Pod Logs;
- Secret reads;
- raw Kubernetes YAML/JSON;
- raw K8sGPT Result CR payloads;
- create/update/patch/delete/deletecollection;
- Mutation;
- Auto Remediation;
- arbitrary Kubernetes API proxying;
- impersonating Kubernetes users from browser identity;
- accepting unsigned `X-User` / `X-Groups` identity headers from untrusted clients.

Authorization only narrows access inside the existing safe projection. It does not expand Kubernetes ServiceAccount permissions.

## Test Matrix

Implemented backend coverage includes:

```text
Authentication
  unauthenticated -> 401 before Authorizer
  authenticated   -> Authorizer evaluated

Capability
  exact capability -> allow when policy permits
  missing/unrelated capability -> deny
  centralized mapping covers all 6 protected routes

Scope
  cluster scope
  namespace scope
  cluster-only grant vs namespaced request -> deny
  explicit namespace wildcard

Policy
  empty policy -> deny
  malformed/unknown policy -> construction failure
  conflicting allow + deny -> deny

Finding detail
  real allowed namespace -> allow
  real denied namespace -> generic 403
  denied response does not expose Finding ID/scope payload
  non-existent ID -> safe 404 without fabricated scope decision

Failure handling
  missing Authorizer -> fail closed
  Authorizer error -> fail closed
```

Frontend unit coverage includes explicit 401 and 403 states while preserving loading/empty/error/retry behavior.

## CI / Acceptance

Phase 1.4.2 is complete only when:

- capability model is centralized;
- route-to-capability mapping is exhaustive;
- Authorizer abstraction exists;
- deny-by-default enforcement is proven;
- cluster/namespace scope checks are proven;
- Finding Detail real-scope authorization is proven;
- frontend 401/403 states are handled without becoming an authorization control;
- AuthZ matrix tests are green;
- existing API Contract Gate is green;
- existing Go/Web/Docker/browser checks are green;
- Secret Scan is green;
- Kubernetes v1.36 Kind E2E is green;
- no Kubernetes privilege expansion occurs;
- implementation PR is merged to `main`;
- resulting `main` Required Checks are green.

Until the last two conditions are true, Phase 1.4.2 remains **Active**, not Completed.

## Non-Goals for Phase 1.4.2

Not part of this stage:

- enterprise OIDC/SSO provider selection;
- login/logout UX beyond safe 401/403 states;
- structured audit sink implementation;
- response sanitizer implementation;
- Events/Prometheus/Loki/Alertmanager correlation;
- writes or remediation.

The next stage after Authorization acceptance is **Phase 1.4.3 — Audit**.
