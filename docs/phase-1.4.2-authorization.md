# Phase 1.4.2 — Authorization

## Status

**Active design / implementation stage.**

Entry baseline:

- `main`: `7b13b250f035657a1edf24e9590a6560488186bf`
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
- absent namespace grant must not imply access to every namespace;
- wildcard behavior, if supported, must be explicit in policy data rather than inferred;
- the current single configured cluster `local` does not justify hardcoding allow-all behavior;
- scope evaluation must use normalized request parameters, not raw URLs.

## Deny-by-Default

The authorization guard is fail-closed.

Expected flow:

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
Authorizer.Decide(...)
  ├── Allow  -> existing read-only handler
  ├── Deny   -> 403 AUTHORIZATION_DENIED
  └── Error  -> fail closed; stable safe error response
```

No policy entry means deny.

Authorization provider/internal errors must never downgrade to allow.

## Backend Interfaces

The implementation should introduce a provider-neutral decision seam rather than embedding group-name checks in HTTP handlers.

Target shape:

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

Exact package names may evolve during implementation, but these boundaries are mandatory:

- handler code does not parse policy files directly;
- Principal remains identity-only and does not grow mutable authorization state;
- policy evaluation is independently unit-testable;
- route/capability mapping is centralized and exhaustively tested;
- scope normalization is centralized and exhaustively tested.

## Route / Capability Mapping

Initial expected mapping:

| HTTP route | Capability | Scope source |
| --- | --- | --- |
| `GET /api/v1/clusters` | `clusters:list` | global configured-cluster list |
| `GET /api/v1/clusters/{cluster}/namespaces` | `namespaces:list` | path `cluster` |
| `GET /api/v1/clusters/{cluster}/resources/{kind}/{namespace}/{name}` | `resources:read` | path `cluster`, `namespace` |
| `GET /api/v1/findings` | `findings:list` | normalized query `cluster`, optional `namespace` |
| `GET /api/v1/findings/summary` | `findings:summary` | normalized query `cluster`, optional `namespace` |
| `GET /api/v1/findings/{id}` | `findings:read` | scope derived from the normalized finding before response |

The `getFinding` case must not authorize solely on an opaque ID. The backend must determine the finding's actual cluster/namespace scope and apply authorization before returning the object.

## Policy Source Boundary

Phase 1.4.2 should support a deterministic policy source suitable for local/CI use without claiming a final enterprise IAM integration.

Requirements:

- policy lives outside frontend source code;
- no secrets are required in policy documents;
- groups/subjects may be used as selectors, but browser-supplied unsigned identity headers are not trusted;
- policy loading errors fail closed;
- duplicate/conflicting policy behavior is deterministic;
- policy format is versioned if a repository-managed format is introduced.

A concrete OIDC/SSO provider remains separable from authorization policy.

## Frontend Boundary

Frontend behavior may improve usability but is not a security control.

Required states:

- `401` -> unauthenticated/session-required state
- `403` -> authenticated-but-forbidden state
- existing retry/error behavior remains for transport/backend failures

The frontend may hide unavailable navigation after receiving trusted capability information in a future contract, but backend authorization must still be evaluated on every protected request.

Phase 1.4.2 should not introduce long-lived credentials into local storage/session storage.

## OpenAPI Boundary

The 401/403 response contract established by Phase 1.4.1 remains stable.

Phase 1.4.2 should add contract material only when it is a true API response/input contract. Internal role names, policy-file structure, or group-to-capability mapping do not belong in public OpenAPI schemas unless exposed intentionally by a dedicated safe endpoint.

No raw policy dump endpoint is planned.

## Security Properties

Must remain forbidden:

- Pod Logs
- Secret reads
- raw Kubernetes YAML/JSON
- raw K8sGPT Result CR payloads
- create/update/patch/delete/deletecollection
- Mutation
- Auto Remediation
- arbitrary Kubernetes API proxying
- impersonating Kubernetes users from browser identity
- accepting unsigned `X-User`/`X-Groups` style headers from untrusted clients

Authorization must not expand Kubernetes ServiceAccount permissions. Application-level AuthZ narrows access inside the existing safe projection; it does not grant new cluster privileges.

## Test Matrix

Backend tests must cover at least:

```text
Authentication state
  unauthenticated -> 401 before AuthZ
  authenticated   -> AuthZ evaluated

Capability
  exact allow
  missing capability -> deny
  unrelated capability -> deny

Scope
  allowed cluster
  denied cluster
  allowed namespace
  denied namespace
  cluster-only grant vs namespaced request

Policy behavior
  empty policy -> deny
  malformed policy -> fail closed
  conflicting entries -> deterministic result

Finding detail
  opaque ID resolving to allowed namespace -> allow
  opaque ID resolving to denied namespace -> 403
  non-existent ID -> safe not-found behavior without scope leakage
```

An allow/deny matrix should be table-driven and cover every protected API route.

## CI / Acceptance

Phase 1.4.2 is complete only when:

- capability enum/model is centralized;
- route-to-capability mapping is exhaustive;
- Authorizer abstraction exists;
- deny-by-default middleware is enabled for authenticated protected routes;
- cluster/namespace scope checks are proven;
- `getFinding` scope is enforced after safe finding resolution;
- frontend 401/403 states are handled without becoming an authorization control;
- AuthZ matrix tests are green;
- existing API Contract Gate is green;
- existing Go/Web/Docker/browser checks are green;
- Secret Scan is green;
- Kubernetes v1.36 Kind E2E is green;
- no Kubernetes privilege expansion occurs;
- PR is merged to `main`;
- resulting `main` Required Checks are green.

## Non-Goals for Phase 1.4.2

Not part of this stage:

- enterprise OIDC/SSO provider selection
- login/logout UX beyond safe 401/403 states
- structured audit sink implementation
- response sanitizer implementation
- Events/Prometheus/Loki/Alertmanager correlation
- writes or remediation

The next stage after Authorization acceptance is **Phase 1.4.3 — Audit**.
