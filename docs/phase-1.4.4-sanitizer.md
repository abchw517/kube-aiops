# Phase 1.4.4 — Sanitizer

## Status

**Entering. Runtime implementation has not started.**

Entry evidence:

- previous stage: Phase 1.4.3 Audit — Completed
- Audit implementation PR: `#22 feat: Phase 1.4.3 structured audit pipeline`
- Audit merged baseline: `main@10e152dd54b0f98d77bd0595e54c4b021d7679f8`
- post-merge CI: `kube-aiops CI #76` — `success`
- Kubernetes baseline: `v1.36.4`

The Phase 1.4.4 implementation branch must be created from the green `main` produced after this completion/entry transition is merged and its own Required Checks are green.

## Goal

Add a reusable, typed and deterministic response Sanitizer before data crosses the backend trust boundary to the Portal client.

The Sanitizer must answer:

```text
Is this already-safe typed response still safe to emit,
and can bounded diagnostic text be normalized/redacted
without exposing credentials, active markup or unbounded content?
```

It is **defense in depth**. It does not replace:

- the existing read-only Kubernetes RBAC boundary;
- typed safe projections such as `finding.Finding` and `kubernetes.ResourceDetail`;
- OpenAPI response DTO allowlists;
- backend AuthN/AuthZ;
- frontend safe text rendering.

Most importantly, Sanitizer must never become a reason to read more sensitive data and then attempt to redact it later.

## Existing Safe Projection Baseline

The current Finding response is already typed:

```text
Finding
├── id
├── cluster
├── namespace
├── severity
├── resource
│   ├── apiVersion
│   ├── kind
│   ├── namespace
│   └── name
├── problem
├── details
├── source
└── createdAt
```

The highest-risk unbounded/external text is `problem` / `details` from the K8sGPT finding path.

The current `kubernetes.ResourceDetail` is already a narrow projection:

```text
ResourceDetail
├── apiVersion
├── kind
├── namespace
├── name
├── createdAt
└── status
    ├── phase
    ├── replicas
    ├── readyReplicas
    └── availableReplicas
```

It does not expose raw metadata, labels, annotations, env, command, volume content, Secret data or arbitrary object JSON.

Phase 1.4.4 must preserve these projection boundaries.

## Security Boundary

Still forbidden:

- Secret reads;
- Pod Logs;
- raw Kubernetes YAML/JSON;
- raw K8sGPT Result CR payloads;
- arbitrary labels/annotations/env/config dumps;
- request or response body logging;
- Kubernetes create/update/patch/delete/deletecollection;
- Mutation;
- Auto Remediation;
- arbitrary Kubernetes API proxying;
- browser identity impersonation of Kubernetes users;
- Events/Prometheus/Loki/Alertmanager correlation.

Observability correlation remains Phase 2.

## Sanitizer Principles

### 1. Typed field-by-field sanitization

Do **not** serialize an arbitrary object and run global regex replacement over JSON.

Reasons:

- it can corrupt IDs, cursors or JSON structure;
- it hides projection regressions instead of preventing them;
- it creates unpredictable false positives;
- it encourages passing arbitrary payloads through a generic redactor.

Sanitization should be explicit for known DTO/domain fields.

### 2. Allowlist before redaction

The safety order is:

```text
Restricted data source
  ↓
Typed safe projection
  ↓
Field validation
  ↓
Diagnostic-text Sanitizer
  ↓
Typed OpenAPI response
```

Never:

```text
Raw Secret / raw Kubernetes object / raw Result CR
  ↓
Generic regex redaction
  ↓
Browser
```

### 3. Deterministic behavior

For the same input and policy, Sanitizer output must be deterministic and idempotent.

```text
sanitize(sanitize(x)) == sanitize(x)
```

### 4. Fail closed on projection regression

Credential-like fragments inside an allowed diagnostic text field may be replaced safely.

A response that appears to contain a raw Kubernetes object, raw Result CR, Secret payload or another forbidden structure is a **projection-boundary failure** and should be rejected rather than heuristically rewritten into something that looks safe.

## Proposed Package Boundary

```text
internal/sanitizer/
├── policy.go          # bounded policy constants / defaults
├── text.go            # diagnostic text normalization + redaction
├── finding.go         # Finding / Page / Summary typed sanitization
├── resource.go        # ResourceDetail / identifier validation
├── error.go           # stable errors / reason codes
└── *_test.go
```

No reflection-based arbitrary-object walker should be introduced.

## Proposed API Shape

Conceptual interface:

```go
type Sanitizer interface {
    Finding(finding.Finding) (finding.Finding, error)
    FindingPage(finding.Page) (finding.Page, error)
    FindingSummary(finding.Summary) (finding.Summary, error)
    Resource(kubernetes.ResourceDetail) (kubernetes.ResourceDetail, error)
}
```

A concrete implementation should hold an immutable validated Policy.

A smaller functional boundary is also acceptable if it remains typed and independently testable.

## Field Classes

### Identifier / metadata fields

Examples:

- cluster;
- namespace;
- kind;
- resource name;
- API version;
- severity;
- source;
- createdAt;
- Finding ID;
- pagination cursor.

These should be **validated and bounded**, not blindly regex-redacted.

Rules should include:

- valid UTF-8;
- no C0/DEL control characters;
- bounded lengths;
- preserve existing Kubernetes identifier semantics where applicable;
- reject malformed values that indicate a broken projection contract.

### Diagnostic text fields

Primary fields:

- `finding.Problem`;
- `finding.Details`.

These are candidates for bounded normalization/redaction.

## Diagnostic Text Policy

Recommended initial bounds:

```text
problem: <= 2 KiB UTF-8 text
details: <= 8 KiB UTF-8 text
page cumulative diagnostic text: bounded independently
```

Exact constants may be adjusted after tests, but must be centralized in Policy and not duplicated across handlers.

Truncation must:

- preserve valid UTF-8;
- be deterministic;
- use a fixed safe marker;
- never include the removed suffix in logs/audit/error text.

## Credential / Header Guards

The diagnostic-text sanitizer should detect narrowly scoped high-confidence patterns including:

- `Authorization: Bearer ...` material;
- bearer-token fragments;
- `Cookie:` / `Set-Cookie:` header material;
- password / passwd / secret / token / api-key assignments when followed by credential-like values;
- PEM private-key blocks;
- JWT-shaped credential tokens where confidence is high;
- kubeconfig / ServiceAccount token material when represented as credential text.

Replacement must use fixed markers such as:

```text
[REDACTED:credential]
[REDACTED:private-key]
[REDACTED:header]
```

Do not place the original matched value in an error, application log or Audit event.

Pattern design must include false-positive tests. Ordinary Kubernetes names, image tags, hashes, timestamps and diagnostic identifiers must not be destroyed merely because they contain words such as `token` or `secret` in a non-credential context.

## HTML / Script Safety

The API is JSON, not HTML. Frontend safe text rendering remains mandatory.

Sanitizer nevertheless adds defense-in-depth tests and handling for active markup in diagnostic text, including:

- `<script ...>` / `</script>`;
- inline event-handler style payloads such as `onerror=`;
- `javascript:` active URL schemes;
- other clearly executable markup fragments.

Do not rely on a generic HTML parser to transform arbitrary rich HTML because rich HTML is not part of the API contract.

Preferred behavior is deterministic neutralization/redaction of active content while preserving normal plain diagnostic text.

The Web Portal must continue to use safe text insertion rather than `innerHTML` for Finding content.

## Raw Kubernetes / Result Regression Guard

The strongest control remains the existing typed projection. Phase 1.4.4 should add regression tests that fail if forbidden raw structures become reachable.

Examples of forbidden response characteristics:

```text
kind: Secret + data/stringData payload
metadata + spec + status arbitrary object dump
managedFields
annotations/labels map passthrough
containers[].env[].valueFrom.secretKeyRef payload dump
raw core.k8sgpt.ai Result spec/status payload
```

These checks should focus on preventing code/projection regression, not trying to redact arbitrary raw objects at runtime.

## Finding Integration

Recommended flow:

```text
K8sGPT Result source
  ↓
existing adapter / safe finding projection
  ↓
finding.Finding
  ↓
Sanitizer.Finding
  ↓
HTTP handler
  ↓
JSON response
```

For list:

```text
finding.Page
  ↓
per-item typed sanitization
  ↓
aggregate diagnostic-size guard
  ↓
response
```

For detail:

```text
resolve safe Finding
  ↓
AuthZ real scope
  ↓
Sanitizer.Finding
  ↓
response
```

AuthZ must still occur against the real normalized Finding scope. Sanitization must not change authorization identity/scope semantics.

## Resource Integration

`kubernetes.ResourceDetail` is already a narrow projection. Sanitizer should validate its bounded identifier/status fields and reject malformed control-character/unbounded values.

It must not change `GetResource` to fetch or expose full Kubernetes objects merely so they can be sanitized.

## Summary Integration

`finding.Summary` contains counts and bounded category keys.

Sanitizer should validate category/map keys and aggregate cardinality without converting Summary into an arbitrary map sanitizer.

## Failure Semantics

### Recoverable diagnostic-text issue

Examples:

- high-confidence credential fragment;
- active script fragment;
- diagnostic text exceeds configured bound.

Expected behavior:

```text
sanitize / redact / truncate deterministically
  ↓
return safe typed response
```

### Projection-boundary / structural violation

Examples:

- forbidden raw object becomes reachable;
- malformed identifier with unsafe control characters;
- aggregate response violates hard safety bound;
- Sanitizer policy/config is invalid.

Expected behavior:

```text
block response
  ↓
stable safe backend error
  ↓
no unsafe payload emitted
```

The client must not receive the original sanitizer error or matched unsafe content.

## HTTP Error Boundary

If Sanitizer rejects an otherwise authorized backend response, return a stable generic response such as:

```text
RESPONSE_SANITIZATION_FAILED
response could not be emitted safely
```

The exact HTTP status must be reviewed against the existing API contract. The implementation must update OpenAPI/contract tests if a new status is introduced.

Never return the matched credential, raw object fragment or sanitizer regex/debug information.

## Audit / Logging Boundary

Phase 1.4.3 Audit remains unchanged in principle.

If Sanitizer rejects a response:

- the Audit event may use the existing bounded `backend_error` outcome unless a new outcome is explicitly reviewed;
- the event must not contain the unsafe text;
- application logging may include only stable reason code, request ID, correlation ID and capability;
- raw matched values and full response objects must never be logged.

Example safe log metadata:

```text
reason=sanitizer_blocked
request_id=...
correlation_id=...
capability=findings:read
```

## Policy Configuration

Initial Phase 1.4.4 should prefer code-defined immutable defaults over a dynamically editable regex policy.

Why:

- easier security review;
- deterministic CI;
- prevents operators from accidentally disabling critical patterns;
- avoids introducing a new policy-distribution/control-plane problem.

If configurable overrides are later needed, they require validation, versioning and safe defaults.

## Test Matrix

Minimum backend coverage:

```text
Normal text
  ordinary Problem/Details unchanged
  Unicode preserved
  Kubernetes identifiers preserved

Bounds
  problem exact boundary accepted
  problem over boundary truncated safely
  details exact boundary accepted
  details over boundary truncated safely
  UTF-8 rune never split
  page cumulative limit enforced

Credentials
  Authorization Bearer value removed
  Cookie/Set-Cookie value removed
  password/token/api-key assignment value removed
  PEM private key removed
  high-confidence JWT credential removed
  original credential absent from output/error/log/audit

False positives
  namespace containing "secret" is preserved
  normal phrase containing "token" without credential value is preserved
  image digest/hash preserved
  resource name preserved

Active content
  script payload neutralized
  onerror payload neutralized
  javascript scheme neutralized
  normal angle-bracket-free diagnostic text preserved

Idempotence
  sanitize(sanitize(x)) == sanitize(x)

Finding
  list item Problem/Details sanitized
  detail Problem/Details sanitized
  real cluster/namespace/ID semantics preserved
  pagination semantics preserved

Summary / Resource
  safe typed responses preserved
  malformed unsafe identifiers rejected

Projection regression
  raw Secret fixture rejected
  raw Kubernetes object fixture cannot be emitted
  raw Result CR fixture cannot be emitted
  arbitrary labels/annotations maps cannot be emitted

Failure safety
  stable client error only
  unsafe matched content absent from response
  unsafe matched content absent from logs
  Audit contains no unsafe content

Existing gates
  AuthN/AuthZ/Audit tests remain green
  API Contract Gate green
  Go/Web/Docker/browser green
  Secret Scan green
  Kind v1.36 green
```

## Frontend Regression

Browser E2E should include a Finding whose diagnostic text contains script-like payloads and verify:

- content cannot execute;
- no unexpected DOM nodes are created;
- no `innerHTML`-style rendering is introduced;
- sanitized/plain text is displayed safely.

Frontend safety is a second boundary, not a replacement for backend Sanitizer.

## Proposed Initial Implementation Order

1. `internal/sanitizer/policy.go` with immutable defaults and validation.
2. diagnostic text sanitizer with bounds + high-confidence credential/active-content guards.
3. typed `Finding` / `Page` integration.
4. typed `ResourceDetail` / `Summary` validation.
5. HTTP response integration after backend + AuthZ and before `writeJSON`.
6. stable error/log/Audit behavior.
7. security regression tests and browser XSS regression.
8. full Required Checks / Kind v1.36 regression.

## CI / Acceptance

Phase 1.4.4 is complete only when:

- centralized typed Sanitizer exists;
- policy constants are validated and centrally owned;
- diagnostic text is bounded and valid UTF-8;
- credential/header/private-key guards are proven;
- active HTML/script payloads cannot cross the response boundary unsafely;
- Finding list/detail are sanitized without changing authorization scope semantics;
- ResourceDetail/Summary remain typed and safe;
- projection regression tests prevent raw Secret/Kubernetes/Result payloads;
- Sanitizer failure never leaks original unsafe material;
- Audit/logging do not contain sanitizer input payloads;
- no Kubernetes privilege expansion occurs;
- existing AuthN/AuthZ/Audit tests stay green;
- API Contract Gate is green;
- Go/Web/Docker/browser checks are green;
- Secret Scan is green;
- Kubernetes v1.36 Kind E2E is green;
- implementation PR Required Checks are all green;
- implementation is merged to `main`;
- resulting `main` Required Checks are all green.

Until the final repository-level conditions are satisfied, Phase 1.4.4 remains **Active**, not Completed.

## Non-Goals

Not part of Phase 1.4.4:

- reading Secrets and redacting them for display;
- Pod Logs;
- arbitrary Kubernetes object sanitization;
- rich HTML support;
- selecting a DLP/SIEM vendor;
- observability correlation;
- distributed tracing;
- write/remediation operations;
- Auto Remediation.

The next stage after Sanitizer acceptance is **Phase 1.4.5 — Production Gates**.
