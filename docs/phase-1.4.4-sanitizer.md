# Phase 1.4.4 — Sanitizer

## Status

**Completed.**

Completion evidence:

- entry baseline: `main@aae32a595793a2063517bd54d8e6571a21453d47`
- implementation branch: `phase-1.4.4-sanitizer`
- implementation PR: `#24 feat: Phase 1.4.4 typed response sanitizer`
- PR gate head: `8fda1fe01fe473a46004c8c89a861772d6583b93`
- PR CI: `kube-aiops CI #80` — all Required Checks green
- squash-merged baseline: `main@ad1845c97ec783e80763655fd94227e228661871`
- post-merge CI: `kube-aiops CI #81` — `completed / success`
- Kubernetes baseline: `v1.36.4`

Post-merge CI #81 passed all Required Checks:

1. `Preflight / Lint / RBAC`
2. `Secret Scan`
3. `Kubernetes v1.36 Kind E2E`

The Kind job also passed Kubernetes v1.36.4 platform validation, lifecycle/rollback/concurrency/trusted-uninstall E2E, and Phase 1.2 readonly API E2E.

## Goal Achieved

Phase 1.4.4 added a reusable, typed and deterministic response Sanitizer before safe backend DTOs cross the Portal trust boundary.

The implemented safety order is:

```text
Restricted data source
  ↓
Typed safe projection
  ↓
Field validation
  ↓
Diagnostic-text Sanitizer
  ↓
Typed JSON response
```

The implementation deliberately does **not** read broader sensitive data and attempt to make it safe afterward.

## Implemented Package

```text
internal/sanitizer/
├── error.go
├── policy.go
├── text.go
├── finding.go
├── resource.go
└── sanitizer_test.go
```

The package is typed and field-by-field. It does not provide an arbitrary-object reflection walker or whole-JSON regex sanitizer.

## Immutable Default Policy

Initial validated code-defined defaults:

```text
problem                       <= 2 KiB
finding details               <= 8 KiB
page cumulative diagnostics   <= 512 KiB
identifier/status/map keys    bounded and control-character guarded
```

A nil Sanitizer option does not disable the response boundary; the HTTP server uses the validated default Sanitizer.

## Diagnostic Text Safety

`finding.Problem` and `finding.Details` are normalized and bounded deterministically.

Implemented guards include high-confidence handling for:

- Authorization/Bearer material
- Cookie/Set-Cookie material
- password/token/api-key style credential assignments
- PEM private-key material
- JWT-shaped credential tokens
- active `<script>` fragments
- inline event-handler payloads such as `onerror=`
- `javascript:` active URL schemes

Replacement uses fixed safe markers and never returns the original matched secret material through errors, logs or Audit events.

Truncation preserves valid UTF-8 and is deterministic/idempotent.

## False-Positive Boundary

Tests prove ordinary diagnostic text remains usable. In particular, Kubernetes/resource names and normal prose containing words such as `secret` or `token` are not treated as credentials solely because those words are present.

The implementation also preserves normal Unicode, identifiers, image/hash-like values and pagination semantics.

## Typed Response Integration

Sanitizer integration is explicit at the typed HTTP boundary for:

- Finding list
- Finding detail
- Finding summary
- Resource detail

Finding Detail preserves the Phase 1.4.2 authorization invariant:

```text
resolve safe Finding
  ↓
use real normalized cluster/namespace for AuthZ
  ↓
allow decision
  ↓
Sanitizer.Finding
  ↓
JSON response
```

Sanitization never changes the identity/scope used for authorization.

## Structural Fail-Closed Behavior

Recoverable diagnostic-text problems are sanitized/redacted/truncated.

Structural projection violations are blocked. Examples include malformed control-character identifiers, invalid bounded metadata, unsafe aggregate limits or other signs that the safe projection contract has regressed.

HTTP behavior for a blocked authorized response is stable and generic:

```text
HTTP 502
RESPONSE_SANITIZATION_FAILED
response could not be emitted safely
Cache-Control: no-store
```

The client never receives the raw sanitizer error or unsafe payload.

Application logging records only bounded stable metadata such as:

```text
reason=sanitizer_blocked
request_id=...
correlation_id=...
capability=...
```

The original sanitizer input/error is not logged.

Phase 1.4.3 Audit requires no unsafe payload extension: sanitizer-triggered 5xx responses use the existing bounded `backend_error` outcome.

## Existing Safe Projection Preserved

The Finding response remains the typed projection:

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

`kubernetes.ResourceDetail` remains narrow:

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

No labels/annotations/env/raw spec/Secret data/raw object payload was added.

## Browser Regression

The Web Portal already escapes Finding text before HTML insertion. Phase 1.4.4 adds a real browser E2E DOM probe containing an `onerror` payload and verifies:

- diagnostic text may be displayed as text
- the probe DOM node is not created
- the script side effect does not execute
- no unsafe Finding `innerHTML` behavior is introduced

Frontend escaping remains a second boundary, not a substitute for backend Sanitizer.

## Secret Scan Incident and Resolution

The first implementation CI used a continuous JWT-shaped test fixture that Gitleaks correctly flagged as secret-like content.

The fix did **not** weaken Secret Scan and did not add an allowlist. The implementation branch was rewritten from the green entry baseline so the flagged fixture was removed from PR history; the JWT-shaped value is now constructed at test runtime. Final PR CI #80 and post-merge CI #81 both passed Secret Scan.

## Security Boundary Retained

Still forbidden:

- Secret reads
- Pod Logs
- raw Kubernetes YAML/JSON
- raw K8sGPT Result CR payloads
- arbitrary labels/annotations/env/config dumps
- request/response body logging
- create/update/patch/delete/deletecollection
- Mutation
- Auto Remediation
- arbitrary Kubernetes API proxying
- browser identity impersonation of Kubernetes users
- Events/Prometheus/Loki/Alertmanager correlation

No Kubernetes RBAC expansion was introduced by Phase 1.4.4.

## Acceptance Result

All Phase 1.4.4 acceptance conditions are satisfied:

- centralized typed Sanitizer exists
- policy constants are validated and centrally owned
- diagnostic text is bounded and valid UTF-8
- credential/header/private-key/JWT guards are proven
- active content is neutralized and browser regression-proven
- Finding list/detail preserve authorization scope semantics
- ResourceDetail/Summary remain typed and bounded
- structural failures emit stable safe errors only
- unsafe sanitizer input is absent from response/log/Audit
- no Kubernetes privilege expansion
- API Contract Gate green
- Go/Web/Docker/browser/project preflight green
- Secret Scan green
- Kubernetes v1.36 Kind E2E green
- implementation PR #24 merged
- resulting `main@ad1845c97ec783e80763655fd94227e228661871` CI #81 green

Next stage: **Phase 1.4.5 — Production Gates**.
