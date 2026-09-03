# Phase 1.4 — Authentication / Authorization / Audit / Sanitizer

## Status

**Phase 1.4 active — current stage: Phase 1.4.3 Audit entering.**

Phase 1.3 is formally completed on the green `main@0248de41466d3e4746f1f6e3b1035a95c63a7940` baseline.

Phase 1.4.1 Identity Contract is formally completed on `main@7b13b250f035657a1edf24e9590a6560488186bf` after PR #18 was merged and post-merge `main` CI run #63 passed all Required Checks.

Phase 1.4.2 Authorization is formally completed on `main@8186bfeff2682677b468ebc397cf248afd2f3213` after PR #20 was merged and post-merge `main` CI run #70 passed all Required Checks:

- `Preflight / Lint / RBAC`
- `Secret Scan`
- `Kubernetes v1.36 Kind E2E`

The post-merge Kind job also passed Kubernetes `v1.36.4` platform validation, lifecycle/rollback/concurrency/trusted-uninstall E2E, and the Phase 1.2 read-only API E2E.

The Phase 1.4.3 implementation branch must be created from the next green `main` after this completion/entry transition is merged.

## Goal

Add a production-grade identity and trust boundary around the existing read-only Portal without expanding Kubernetes mutation privileges.

```text
User / SRE
    ↓
Authentication
    ↓
Authorization Policy
    ↓
Phase 1.3 Web Portal
    ↓
Phase 1.2 Backend API
    ↓
Sanitizer / Safe Finding Projection
    ↓
Kubernetes + K8sGPT read-only data sources

Every accepted or rejected protected request
    ↓
Structured Audit Event
```

## Scope

### Authentication

- define the Portal identity boundary
- establish a provider-neutral authenticated principal model
- keep authentication configuration outside frontend source code
- do not place long-lived credentials in browser storage
- preserve same-origin deployment where practical

### Authorization

Initial authorization is read-only and capability based:

- `findings:list`
- `findings:read`
- `findings:summary`
- `clusters:list`
- `namespaces:list`
- `resources:read`

Authorization is enforced by the backend. Frontend hiding alone is never an authorization control.

### Audit

Record structured, security-safe audit events for Portal/API access:

- request ID / correlation ID
- authenticated principal identifier when available
- operation/capability
- cluster/namespace scope when applicable
- authorization/authentication/result outcome
- HTTP status class / result status
- timestamp
- latency

Audit records must not contain Secret values, raw Kubernetes objects, raw Result CR content, bearer/session tokens, authorization headers, cookies, provider credentials, or request/response bodies containing unbounded diagnostic data.

Audit emission must not weaken the request security path: an audit sink failure must be observable but must not silently change an authorization deny into an allow.

### Sanitizer

Add a reusable output sanitization layer before data leaves the backend trust boundary.

Minimum guards:

- Secret/token/password/private-key patterns
- authorization/cookie header material
- raw Kubernetes object payloads
- raw K8sGPT Result CR payloads
- unsafe HTML/script content
- oversized/unbounded diagnostic text

The sanitizer is defense in depth and must not replace the OpenAPI allowlist/DTO boundary.

## Explicit Non-Goals

Phase 1.4 does **not** add:

- Pod Logs
- Secret reads
- raw Kubernetes YAML/JSON explorer
- raw Result CR viewer
- create/update/patch/delete
- Mutation
- Auto Remediation
- arbitrary Kubernetes API proxying
- Events/Prometheus/Loki/Alertmanager correlation

The last item remains Phase 2.

## Proposed Architecture

```text
Browser
  │
  │ authenticated session / identity context
  ▼
Portal Edge / AuthN
  │
  ▼
Backend AuthN Adapter
  │
  ├── Principal
  ▼
Authorization Guard
  │
  ├── deny by default
  ├── capability + scope evaluation
  ▼
Read-only API Handler
  │
  ▼
Finding Service
  │
  ▼
Sanitizer
  │
  ▼
OpenAPI Response DTO

Request lifecycle ───────────────► Security-safe Audit Recorder ──► Audit Sink
```

## Implementation Stages

### Phase 1.4.1 — Identity Contract — Completed

- principal model
- auth middleware interface
- unauthenticated/forbidden response contract
- request/correlation ID
- OpenAPI contract updates
- generated TypeScript client regeneration

Completion record: `docs/phase-1.4.1-identity-contract.md`.

### Phase 1.4.2 — Authorization — Completed

- centralized capability model
- backend-enforced deny-by-default authorization
- cluster/namespace scope rules
- `resources:read` coverage for the existing safe resource projection
- Finding Detail real-scope authorization to prevent opaque-ID scope bypass
- backend allow/deny matrix tests
- frontend authentication/forbidden states without frontend authorization decisions

Completion record: `docs/phase-1.4.2-authorization.md`.

### Phase 1.4.3 — Audit — Entering

- structured audit event schema
- provider-neutral, security-safe audit recorder/sink abstraction
- request/correlation ID propagation
- principal/capability/scope/result/latency fields
- authentication/authorization denial and backend result coverage
- sink failure handling that never weakens AuthN/AuthZ
- tests preventing sensitive-data leakage

Stage design and acceptance boundary: `docs/phase-1.4.3-audit.md`.

### Phase 1.4.4 — Sanitizer

- centralized response sanitizer
- secret/token/header guards
- text length bounds
- HTML/script safety tests
- raw-object/raw-Result regression guards

### Phase 1.4.5 — Production Gates

Required pipeline additions should include:

- AuthN middleware unit/integration tests
- AuthZ matrix tests
- unauthenticated/forbidden browser E2E
- sanitizer security regression tests
- audit leakage tests
- OpenAPI/client drift gate
- Docker smoke
- Kubernetes v1.36 Kind E2E
- Secret Scan

## Acceptance Principle

Phase 1.4 is complete only when:

```text
AuthN implemented
    +
AuthZ deny-by-default proven
    +
Audit coverage proven
    +
Sanitizer regression suite proven
    +
No privilege expansion
    +
PR Required Checks all green
    +
Merge to main
    +
main Required Checks all green
```