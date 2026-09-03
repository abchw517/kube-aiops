# Phase 1.4.3 — Audit

## Status

**Completed.**

Completion evidence:

- previous stage: Phase 1.4.2 Authorization — Completed
- entry transition PR: `#21 docs: close Phase 1.4.2 and enter Phase 1.4.3 Audit`
- verified entry baseline: `main@1129dc786a3de4400a1ab65cfe3948115c90fd30`
- entry baseline workflow: `kube-aiops CI #72` — `success`
- implementation branch: `phase-1.4.3-audit`
- implementation PR: `#22 feat: Phase 1.4.3 structured audit pipeline`
- final implementation head: `7fdb3d5ec30f24fd027896bb20a83069225e51a0`
- PR Required Checks: all green
- merged main commit: `10e152dd54b0f98d77bd0595e54c4b021d7679f8`
- post-merge workflow: `kube-aiops CI #76` — `success`
- Kubernetes baseline: `v1.36.4`

Post-merge Required Checks all passed:

- `Preflight / Lint / RBAC`
- `Secret Scan`
- `Kubernetes v1.36 Kind E2E`

The Kind job also passed Kubernetes `v1.36.4` baseline validation, lifecycle/rollback/concurrency/trusted-uninstall E2E, and the Phase 1.2 read-only API E2E.

## Goal Achieved

Phase 1.4.3 added a structured, provider-neutral and security-safe audit pipeline around the authenticated and authorized read-only API path.

Audit can answer:

```text
Who attempted which Portal capability,
for which normalized cluster/namespace scope,
what security/result outcome occurred,
and how long the request took?
```

without recording credentials, request/response bodies, raw Kubernetes objects, raw K8sGPT Result CR payloads, Finding diagnostic payloads, raw URL/query strings or provider/sink error payloads.

## Runtime Architecture

```text
Request
  ↓
Request / Correlation ID
  ↓
Audit Lifecycle Recorder
  │ canonical route + centralized capability
  ↓
Authentication
  │ validated subject/provider only
  ↓
Authorization
  │ normalized cluster/namespace scope + decision
  ↓
Read-only Handler
  ↓
HTTP status capture
  ↓
Outcome + latency finalization
  ↓
Event validation
  ↓
Provider-neutral Audit Sink
```

The audit lifecycle is outside AuthN/AuthZ so `401`, `403`, security-provider `503`, safe `404`, invalid requests, backend failures and successful responses can all produce a final event when a Sink is configured.

Canonical route metadata comes from the real `http.ServeMux` matcher, so paths such as `GET /api/v1/findings/{id}` are recorded as templates rather than raw URLs or opaque IDs.

## Implemented Components

```text
internal/audit/
├── event.go          # fixed Event / Outcome schema + validation
├── event_test.go     # bounded schema safety tests
├── sink.go           # Sink / SinkFunc provider-neutral boundary
└── recorder.go       # request-local bounded lifecycle state

internal/httpapi/
├── audit.go          # route match, status, outcome, latency, sink delivery
├── audit_test.go     # outcome/security/leakage regression coverage
├── authn.go          # principal + AuthN outcome annotation
├── authz.go          # capability/scope + AuthZ outcome annotation
└── server.go         # request metadata -> audit -> AuthN -> AuthZ -> handler
```

## Audit Event Contract

The event is a fixed typed structure, not an arbitrary payload map.

```text
Event
├── timestamp
├── requestID
├── correlationID
├── routePattern
├── capability
├── principalSubject      optional
├── principalProvider     optional
├── cluster               optional
├── namespace             optional
├── outcome
├── httpStatus
└── latencyMs
```

Bounded outcomes:

- `success`
- `unauthenticated`
- `denied`
- `security_unavailable`
- `not_found`
- `invalid_request`
- `backend_error`

Validation rejects unknown outcomes, control characters, oversized fields, invalid status codes, invalid latency and malformed identity/scope combinations.

## Authentication Boundary

On successful authentication Audit receives only:

```text
principal.Subject
principal.Provider
```

It does not receive display name, groups, credentials, Authorization headers or Cookie values.

AuthN failure mapping:

| Result | Audit outcome |
| --- | --- |
| no valid identity | `unauthenticated` |
| provider unavailable/error | `security_unavailable` |
| invalid Principal | `security_unavailable` |

Raw provider errors are never copied into the event.

## Authorization Boundary

Audit reuses the centralized Phase 1.4.2 capability model and validated `authorization.Scope`.

| Result | Audit outcome |
| --- | --- |
| allow | final handler outcome |
| deny | `denied` |
| Authorizer unavailable/error | `security_unavailable` |

Only validated normalized scope is copied into Audit.

## Finding Detail Boundary

Finding Detail preserves the Phase 1.4.2 anti-bypass sequence:

```text
AuthN prerequisites
  ↓
resolve safe normalized Finding
  ↓
derive real cluster/namespace
  ↓
AuthZ findings:read
  ↓
handler result
  ↓
audit final event
```

The event can contain the normalized cluster/namespace and `findings:read`, but never the opaque Finding ID, Finding explanation text or raw Result CR payload. A missing Finding produces `not_found` without fabricating scope.

## Handler Outcome Mapping

| HTTP result | Audit outcome |
| --- | --- |
| 2xx / 3xx | `success` |
| validation/client error | `invalid_request` |
| 404 | `not_found` |
| backend 5xx | `backend_error` |

Explicit AuthN/AuthZ outcomes take precedence over generic HTTP mapping.

## Sink Failure Semantics

The provider-neutral Sink receives only a validated bounded Event.

Audit delivery failure does not rewrite security or handler behavior:

```text
AuthN deny + sink failure -> remains denied
AuthZ deny + sink failure -> remains denied
Authorizer failure + sink failure -> remains fail-closed
Allowed response + sink failure -> original handler result remains unchanged
```

Operational logging uses only stable reason codes and bounded metadata; raw sink errors or full events are not dumped.

## Explicitly Excluded Data

Audit events exclude:

- Authorization / Cookie / Set-Cookie material
- bearer/session/provider tokens
- passwords, API keys, private keys
- kubeconfig / ServiceAccount tokens
- request body / response body
- raw URL/query string
- raw Kubernetes objects
- raw K8sGPT Result CR payloads
- Finding detail text or opaque Finding IDs
- displayName/groups
- Pod Logs / Secret values
- client IP / user-agent by default
- raw provider/sink errors

The implementation uses an allowlisted typed event instead of broad redaction of arbitrary payloads.

## Regression Coverage

Tests prove:

- bounded valid events are accepted;
- unsafe/oversized fields and unknown outcomes are rejected;
- request/correlation IDs propagate;
- 401, AuthN 503, 403 and AuthZ 503 map correctly;
- 2xx, 400, 404 and backend 5xx map correctly;
- Finding Detail records only real normalized scope after resolution;
- missing Finding does not fabricate scope;
- Authorization, Cookie, tokens, query payloads, Finding IDs, displayName and groups do not appear in serialized events;
- sink failure does not alter an existing deny or handler response;
- health/readiness remain outside the user-access audit stream.

Existing AuthN, AuthZ, OpenAPI, Go/Web, Docker, browser and Kubernetes v1.36 Kind tests stayed green.

## Security Boundary Preserved

Phase 1.4.3 did **not** add:

- Pod Logs
- Secret reads
- raw Kubernetes YAML/JSON
- raw K8sGPT Result CR access
- create/update/patch/delete/deletecollection
- Mutation
- Auto Remediation
- arbitrary Kubernetes API proxying
- Kubernetes user impersonation
- Events/Prometheus/Loki/Alertmanager correlation

Observability correlation remains Phase 2.

## Acceptance Result

All Phase 1.4.3 acceptance conditions are satisfied:

- typed bounded Event exists;
- request/correlation identity is safe;
- validated Principal identity is recorded without credentials;
- centralized capability and normalized scope are recorded;
- AuthN/AuthZ outcomes are covered;
- handler status/outcome/latency are covered;
- Sink is provider-neutral;
- sensitive-data leakage regression tests pass;
- sink failure cannot weaken AuthN/AuthZ;
- no Kubernetes privilege expansion occurred;
- API Contract, Go/Web/Docker/browser, Secret Scan and Kind v1.36 checks pass;
- PR #22 merged;
- post-merge `main` CI #76 passes all Required Checks.

**Phase 1.4.3 Audit is therefore formally Completed.**

The next stage is **Phase 1.4.4 — Sanitizer**.
