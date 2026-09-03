# Phase 1.4.3 — Audit

## Status

**Entering. Implementation has not started.**

Entry evidence:

- previous stage: Phase 1.4.2 Authorization — Completed
- Phase 1.4.2 implementation PR: `#20`
- Phase 1.4.2 merged baseline: `main@8186bfeff2682677b468ebc397cf248afd2f3213`
- post-merge CI: `kube-aiops CI #70` — `success`
- Kubernetes baseline: `v1.36.4`

The Phase 1.4.3 implementation branch must be created from the green `main` produced after this completion/entry transition is merged and its own Required Checks are green.

## Goal

Add a structured, provider-neutral and security-safe audit pipeline around the existing authenticated and authorized read-only API path.

Audit must answer:

```text
Who attempted which Portal capability,
for which cluster/namespace scope,
what security/result outcome occurred,
and how long the request took?
```

It must do this without recording credentials, request/response bodies, raw Kubernetes objects, raw K8sGPT Result CR payloads, or unbounded diagnostic content.

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

## Proposed Request Pipeline

```text
Request
  ↓
Request / Correlation ID
  ↓
Audit Lifecycle Recorder
  │   creates request-local bounded audit state
  ↓
Authentication
  │   annotates principal or safe AuthN outcome
  ↓
Authorization
  │   annotates capability + normalized scope + decision
  ↓
Read-only Handler
  │
  ↓
Response Status Capture
  ↓
Finalize Audit Event
  ↓
Audit Sink
```

The lifecycle recorder should be outside AuthN/AuthZ so `401`, `403`, security-provider `503`, handler success, safe `404`, and backend failures can all produce a final event when auditing is configured.

A request-local recorder object may be placed in context before AuthN. Inner middleware can annotate that same bounded object without exposing mutable global state.

## Audit Event Schema

The event model should be a fixed Go struct, not an arbitrary map.

Proposed fields:

```text
AuditEvent
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

### Field rules

`timestamp`
- server-generated UTC timestamp;
- never client supplied.

`requestID` / `correlationID`
- reuse Phase 1.4.1 validated metadata;
- bounded safe ASCII only.

`routePattern`
- use the canonical route pattern such as `GET /api/v1/findings/{id}`;
- never record the raw URL path when it contains object names or opaque IDs;
- never record query strings.

`capability`
- use the centralized Phase 1.4.2 capability enum;
- never accept an arbitrary browser-supplied operation string.

`principalSubject` / `principalProvider`
- populated only from the validated authenticated `Principal`;
- do not record display name or group lists by default;
- never record bearer/session material.

`cluster` / `namespace`
- use normalized authorization scope values;
- do not derive scope from raw URL text when a normalized scope already exists.

`outcome`
- a bounded enum, for example:
  - `success`
  - `unauthenticated`
  - `denied`
  - `security_unavailable`
  - `not_found`
  - `backend_error`

`httpStatus`
- final response status code only;
- no response body.

`latencyMs`
- server-measured bounded integer duration;
- no tracing payload or stack dump.

## Data Explicitly Excluded

Audit events must not contain:

- `Authorization` headers;
- `Cookie` / `Set-Cookie` values;
- bearer, session or provider tokens;
- passwords, API keys or private keys;
- kubeconfig or ServiceAccount tokens;
- request body;
- response body;
- raw Kubernetes object payload;
- raw K8sGPT Result CR payload;
- Finding detail text or diagnostic explanations;
- arbitrary query strings;
- raw object/resource names unless a later explicit security review adds a bounded requirement;
- user-agent or client IP by default;
- raw provider/sink error strings in the client response.

This stage should prefer **allowlisted event fields** over broad redaction of arbitrary payloads.

## Sink Abstraction

The backend should depend on a small provider-neutral interface, for example:

```go
type Sink interface {
    Record(context.Context, Event) error
}
```

Properties:

- the HTTP/security pipeline does not depend on Loki, Elasticsearch, Kafka, a database, or a specific SIEM;
- tests can use an in-memory sink;
- concrete persistence can be wired later without changing the event contract;
- no sink receives request/response bodies;
- sink implementations receive an already validated bounded event.

A `SinkFunc` adapter may be provided for tests and simple integrations.

## Failure Semantics

Audit failure must never weaken authentication or authorization.

Required behavior:

```text
AuthN deny + audit sink failure -> request remains denied
AuthZ deny + audit sink failure -> request remains denied
Authorizer error + audit failure -> request remains fail-closed
Allowed request + audit sink failure -> security decision is not rewritten to allow/deny by the sink
```

The initial stage should keep audit persistence separate from authorization semantics. Sink failure must be surfaced through a safe operational signal without leaking event bodies, credentials, or raw sink error payloads to the user.

If a later compliance mode requires audit-write fail-closed behavior for otherwise allowed requests, that must be an explicit reviewed policy rather than an accidental side effect of the sink implementation.

## Outcome Capture

The recorder must cover the protected API lifecycle, including:

| Request result | Expected audit outcome |
| --- | --- |
| no valid identity | `unauthenticated` |
| AuthN provider unavailable | `security_unavailable` |
| authenticated but policy denies | `denied` |
| Authorizer unavailable/error | `security_unavailable` |
| allowed and handler succeeds | `success` |
| allowed but safe object not found | `not_found` |
| allowed but backend/read-only handler fails | `backend_error` |

`/healthz` and `/readyz` should remain outside the user security audit stream unless there is a separate operational-audit requirement; they must not create misleading user-access records.

## Finding Detail Boundary

Finding Detail keeps the Phase 1.4.2 anti-bypass sequence:

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

The audit event may record the resulting normalized cluster/namespace and capability but must not record the opaque Finding ID or Finding payload.

## Logging Boundary

Audit events and application logs are related but not interchangeable.

Application logs may report that audit delivery failed, but they must not dump the full event or sink payload. Safe failure logging should be limited to bounded metadata such as request ID, capability, and a stable failure code.

Phase 1.4.3 does not introduce Loki or log-correlation architecture. That remains outside this stage.

## Proposed Backend Components

```text
internal/audit/
├── event.go          # fixed event/outcome schema + validation
├── sink.go           # Sink / SinkFunc abstraction
├── recorder.go       # request-local lifecycle state + finalize
└── *_test.go

internal/httpapi/
├── audit.go          # middleware / response status capture
├── authn.go          # annotate safe AuthN outcome
├── authz.go          # annotate capability/scope/decision
└── server.go         # preserve real route registration / contract gate
```

The exact file split may vary, but the boundaries must remain provider-neutral and must preserve the existing API Contract Gate's real-route verification.

## Test Matrix

Minimum backend coverage:

```text
Schema safety
  bounded valid event -> accepted
  control characters / oversized fields -> rejected
  unknown arbitrary payload fields -> impossible by typed schema

Request metadata
  request ID propagated
  correlation ID propagated

Authentication
  401 -> unauthenticated event
  AuthN 503 -> security_unavailable event
  valid Principal -> subject/provider only, no credential material

Authorization
  allow -> capability/scope captured
  deny -> denied event
  Authorizer error -> security_unavailable event
  no raw policy document in event

Handlers
  2xx -> success
  safe 404 -> not_found
  backend 5xx -> backend_error
  final HTTP status captured

Finding Detail
  real normalized cluster/namespace captured after resolution
  opaque Finding ID never emitted
  denied Finding payload never emitted

Sensitive-data regression
  Authorization header absent
  Cookie absent
  bearer token absent
  Secret/private-key fixture absent
  raw Kubernetes object fixture absent
  raw Result CR fixture absent
  request/response body fixture absent

Sink failures
  deny remains deny
  AuthN/AuthZ fail-closed semantics unchanged
  no raw sink error returned to client
```

Existing AuthN, AuthZ, OpenAPI, Go/Web, Docker, browser and Kubernetes v1.36 Kind tests remain mandatory.

## CI / Acceptance

Phase 1.4.3 is complete only when:

- fixed typed audit event schema exists;
- request/correlation IDs are recorded safely;
- authenticated principal identity is recorded without credential material;
- centralized capability and normalized scope are recorded;
- AuthN/AuthZ deny/unavailable outcomes are covered;
- final handler result/status/latency are covered;
- sink abstraction is provider-neutral;
- sensitive-data leakage regression tests are green;
- audit sink failures do not weaken AuthN/AuthZ;
- no Kubernetes privilege expansion occurs;
- existing API Contract Gate is green;
- Go/Web/Docker/browser checks are green;
- Secret Scan is green;
- Kubernetes v1.36 Kind E2E is green;
- implementation PR Required Checks are all green;
- implementation is merged to `main`;
- resulting `main` Required Checks are all green.

Until the last three repository-level conditions are satisfied, Phase 1.4.3 remains **Active**, not Completed.

## Non-Goals for Phase 1.4.3

Not part of this stage:

- selecting a concrete enterprise SIEM/backend;
- Loki/Elasticsearch/Kafka-specific coupling;
- Events/Prometheus/Loki/Alertmanager correlation;
- distributed tracing implementation;
- response sanitizer implementation;
- Pod Logs or Secret access;
- write/remediation actions.

The next stage after Audit acceptance is **Phase 1.4.4 — Sanitizer**.