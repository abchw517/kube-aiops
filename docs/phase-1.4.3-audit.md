# Phase 1.4.3 — Audit

## Status

**Active / Implementation.**

Entry evidence:

- previous stage: Phase 1.4.2 Authorization — Completed
- transition PR: `#21 docs: close Phase 1.4.2 and enter Phase 1.4.3 Audit`
- verified entry baseline: `main@1129dc786a3de4400a1ab65cfe3948115c90fd30`
- entry baseline workflow: `kube-aiops CI #72` — `success`
- Kubernetes baseline: `v1.36.4`
- implementation branch: `phase-1.4.3-audit`
- implementation PR: `#22 feat: Phase 1.4.3 structured audit pipeline`

Phase 1.4.3 remains **Active**, not Completed, until PR #22 Required Checks are green, the implementation is merged to `main`, and the resulting `main` Required Checks are green.

## Goal

Add a structured, provider-neutral and security-safe audit pipeline around the existing authenticated and authorized read-only API path.

Audit must answer:

```text
Who attempted which Portal capability,
for which cluster/namespace scope,
what security/result outcome occurred,
and how long the request took?
```

It must do this without recording credentials, request/response bodies, raw Kubernetes objects, raw K8sGPT Result CR payloads, Finding diagnostic payloads, raw URL/query strings, or provider/sink error payloads.

## Implemented Runtime Architecture

```text
Request
  ↓
Request / Correlation ID
  ↓
Audit Lifecycle Recorder
  │   canonical route + centralized capability
  ↓
Authentication
  │   validated subject/provider only
  ↓
Authorization
  │   normalized cluster/namespace scope + decision
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

The audit lifecycle sits outside AuthN/AuthZ so `401`, `403`, security-provider `503`, safe `404`, invalid requests, backend errors and successful handler responses can all produce a final event when an Audit Sink is configured.

Canonical route metadata is obtained from the real `http.ServeMux` matcher before AuthN. This allows an unauthenticated request to be audited as a route template such as:

```text
GET /api/v1/findings/{id}
```

without persisting the opaque Finding ID, raw URL path or query string.

## Implemented Components

```text
internal/audit/
├── event.go          # fixed Event / Outcome schema + validation
├── event_test.go     # bounded schema safety tests
├── sink.go           # Sink / SinkFunc provider-neutral boundary
└── recorder.go       # request-local bounded lifecycle state

internal/httpapi/
├── audit.go          # route match, response status, outcome, latency, sink delivery
├── audit_test.go     # security/outcome/leakage regression coverage
├── authn.go          # principal + AuthN outcome annotation
├── authz.go          # capability/scope + AuthZ outcome annotation
└── server.go         # request metadata -> audit -> AuthN -> AuthZ -> handler pipeline
```

## Audit Event Contract

The event model is a fixed Go struct, not an arbitrary map.

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

### Outcomes

Implemented bounded outcomes:

- `success`
- `unauthenticated`
- `denied`
- `security_unavailable`
- `not_found`
- `invalid_request`
- `backend_error`

### Validation rules

- timestamp is server generated;
- request/correlation IDs are required and bounded;
- route pattern is required, bounded and control-character safe;
- capability must be one of the centralized Phase 1.4.2 capabilities;
- principal subject/provider must appear together and remain bounded;
- namespace cannot exist without cluster;
- scope values remain bounded and control-character safe;
- HTTP status must be in the valid status range;
- latency is bounded to a finite server-side duration;
- unknown outcomes are rejected.

## Authentication Boundary

On successful AuthN, Audit receives only:

```text
principal.Subject
principal.Provider
```

Audit does not receive:

- display name;
- groups;
- bearer/session/provider credentials;
- Authorization header;
- Cookie values.

AuthN outcomes:

| Request result | Audit outcome |
| --- | --- |
| no valid identity | `unauthenticated` |
| provider unavailable/error | `security_unavailable` |
| provider returns invalid Principal | `security_unavailable` |

Raw provider errors are not copied into the event.

## Authorization Boundary

Audit reuses the centralized Phase 1.4.2 route capability map and normalized `authorization.Scope`.

It does not accept browser-provided capability names or arbitrary policy metadata.

AuthZ outcomes:

| Request result | Audit outcome |
| --- | --- |
| policy allows | final handler outcome |
| policy denies | `denied` |
| Authorizer missing/unavailable/error | `security_unavailable` |

Only scope values that pass authorization scope validation are copied into the recorder. Unsafe hostile scope input is omitted rather than copied into an event.

## Finding Detail Boundary

Finding Detail preserves the anti-bypass sequence:

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

The final event may contain the normalized cluster/namespace and `findings:read` capability, but never contains:

- the opaque Finding ID;
- Finding description/explanation text;
- raw Result CR payload.

If the Finding does not exist, Audit records `not_found` without fabricating cluster/namespace scope.

## Handler Result Mapping

Default HTTP result mapping after AuthN/AuthZ:

| HTTP result | Audit outcome |
| --- | --- |
| 2xx / 3xx | `success` |
| 400-class validation/client error | `invalid_request` |
| 404 | `not_found` |
| 5xx backend failure | `backend_error` |

Explicit AuthN/AuthZ outcomes take precedence over generic status mapping, so an authorization-service `503` remains `security_unavailable`, not `backend_error`.

## Sink Abstraction

The backend depends on a provider-neutral interface:

```go
type Sink interface {
    Record(context.Context, Event) error
}
```

A `SinkFunc` adapter is also available.

The HTTP/security pipeline does not depend on Loki, Elasticsearch, Kafka, a database or a particular SIEM.

A Sink receives an already validated bounded Event only.

## Sink Failure Semantics

Audit delivery failure must not rewrite an existing security or handler result.

Examples:

```text
AuthN deny + audit sink failure -> request remains denied
AuthZ deny + audit sink failure -> request remains denied
Authorizer error + audit sink failure -> request remains fail-closed
Allowed request + audit sink failure -> original handler response remains unchanged
```

Safe operational logging records only bounded metadata such as:

- stable reason: `invalid_event` or `sink_error`;
- request ID;
- correlation ID;
- capability.

Raw sink errors and full events are not dumped to application logs.

## Data Explicitly Excluded

Audit events must not contain:

- `Authorization` headers;
- `Cookie` / `Set-Cookie` values;
- bearer, session or provider tokens;
- passwords, API keys or private keys;
- kubeconfig or ServiceAccount tokens;
- request body;
- response body;
- raw URL/query string;
- raw Kubernetes object payload;
- raw K8sGPT Result CR payload;
- Finding detail text or diagnostic explanations;
- opaque Finding IDs;
- display name or group lists;
- Pod Logs or Secret values;
- user-agent or client IP by default;
- raw provider/sink error strings.

The implementation uses an **allowlisted typed event** rather than broad redaction of arbitrary payloads.

## Health / Readiness Boundary

`/healthz` and `/readyz` stay outside the user-access audit stream. They do not generate misleading user security events.

## Test Coverage

Current implementation tests cover:

```text
Schema safety
  bounded valid event -> accepted
  control chars / oversized fields -> rejected
  unknown outcome -> rejected
  invalid status/latency -> rejected

Request metadata
  request ID propagated
  correlation ID propagated

Authentication
  401 -> unauthenticated
  AuthN provider error -> security_unavailable
  valid Principal -> subject/provider only

Authorization
  allow -> capability/scope captured
  deny -> denied
  Authorizer error -> security_unavailable

Handlers
  2xx -> success
  400 -> invalid_request
  safe 404 -> not_found
  backend 5xx -> backend_error

Finding Detail
  normalized real scope captured after resolution
  opaque Finding ID absent
  missing Finding does not fabricate scope

Sensitive-data regression
  Authorization absent
  Cookie/token absent
  query payload absent
  Finding ID absent
  displayName/groups absent

Sink failure
  existing deny remains deny
  raw sink error absent from client response and application logs

Operational boundary
  health endpoint does not emit a user audit event
```

Existing AuthN, AuthZ, OpenAPI, Go/Web, Docker, browser and Kubernetes v1.36 Kind tests remain mandatory.

## Security Boundary

Phase 1.4.3 does **not** expand Kubernetes privileges and does not add new business-data reads.

Still forbidden:

- Pod Logs;
- Secret reads;
- raw Kubernetes YAML/JSON;
- raw K8sGPT Result CR content;
- create/update/patch/delete/deletecollection;
- Mutation;
- Auto Remediation;
- arbitrary Kubernetes API proxying;
- browser identity impersonation of Kubernetes users;
- Events/Prometheus/Loki/Alertmanager correlation.

The last item remains Phase 2. Audit is a security/accountability pipeline, not the observability-correlation phase.

## CI / Acceptance

Phase 1.4.3 is complete only when:

- fixed typed audit event schema exists;
- request/correlation IDs are recorded safely;
- authenticated principal identity is recorded without credential material;
- centralized capability and normalized scope are recorded;
- AuthN/AuthZ deny/unavailable outcomes are covered;
- final handler result/status/latency are covered;
- Sink abstraction remains provider-neutral;
- sensitive-data leakage regression tests are green;
- audit sink failures do not weaken AuthN/AuthZ;
- no Kubernetes privilege expansion occurs;
- API Contract Gate is green;
- Go/Web/Docker/browser checks are green;
- Secret Scan is green;
- Kubernetes v1.36 Kind E2E is green;
- implementation PR #22 Required Checks are all green;
- implementation is merged to `main`;
- resulting `main` Required Checks are all green.

Until the repository-level merge and post-merge conditions are satisfied, Phase 1.4.3 remains **Active**, not Completed.

## Non-Goals

Not part of Phase 1.4.3:

- selecting a concrete enterprise SIEM/backend;
- Loki/Elasticsearch/Kafka-specific coupling;
- Events/Prometheus/Loki/Alertmanager correlation;
- distributed tracing implementation;
- response sanitizer implementation;
- Pod Logs or Secret access;
- write/remediation actions.

The next stage after Audit acceptance is **Phase 1.4.4 — Sanitizer**.
