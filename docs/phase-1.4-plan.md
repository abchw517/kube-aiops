# Phase 1.4 — Authentication / Authorization / Audit / Sanitizer

## Status

**Phase 1.4 active — current stage: Phase 1.4.4 Sanitizer entering.**

Completed stages:

- Phase 1.4.1 Identity Contract — completed on `main@7b13b250f035657a1edf24e9590a6560488186bf`; post-merge CI #63 green.
- Phase 1.4.2 Authorization — completed on `main@8186bfeff2682677b468ebc397cf248afd2f3213`; post-merge CI #70 green.
- Phase 1.4.3 Audit — completed on `main@10e152dd54b0f98d77bd0595e54c4b021d7679f8`; implementation PR #22 and post-merge CI #76 passed all Required Checks.

For Phase 1.4.3, post-merge CI #76 passed:

- `Preflight / Lint / RBAC`
- `Secret Scan`
- `Kubernetes v1.36 Kind E2E`

The Kind job also passed Kubernetes `v1.36.4` platform validation, lifecycle/rollback/concurrency/trusted-uninstall E2E, and the Phase 1.2 read-only API E2E.

The Phase 1.4.4 implementation branch must be created from the green `main` produced after the Phase 1.4.3 completion / Phase 1.4.4 entry transition is merged and its own Required Checks are green.

## Goal

Add a production-grade identity, authorization, accountability and response-safety boundary around the existing read-only Portal without expanding Kubernetes mutation or sensitive-data privileges.

```text
User / SRE
    ↓
Authentication
    ↓
Authorization Policy
    ↓
Read-only Portal API
    ↓
Safe Domain / DTO Projection
    ↓
Sanitizer
    ↓
OpenAPI Response

Every protected request
    ↓
Structured Audit Event
```

## Scope

### Authentication

- provider-neutral validated Principal
- request/correlation IDs
- no long-lived credentials in browser source/storage
- provider errors do not leak raw credentials or tokens

### Authorization

Backend-enforced, deny-by-default read-only capabilities:

- `findings:list`
- `findings:read`
- `findings:summary`
- `clusters:list`
- `namespaces:list`
- `resources:read`

Frontend hiding is never an authorization control.

### Audit

Phase 1.4.3 now provides structured, security-safe audit events containing bounded request metadata, validated principal identity, centralized capability, normalized scope, outcome, HTTP status and latency. Audit events exclude request/response bodies, credentials, raw Kubernetes objects, raw K8sGPT Result CR data and opaque Finding IDs.

Audit Sink failure never weakens AuthN/AuthZ or rewrites the original handler result.

### Sanitizer

Phase 1.4.4 adds a reusable response safety layer before data leaves the backend trust boundary.

Minimum guards:

- bounded diagnostic text and bounded aggregate response size;
- credential-like material such as bearer/session tokens, passwords, API keys and private-key blocks;
- authorization/cookie header material accidentally embedded in text;
- unsafe HTML/script content and active markup;
- raw Kubernetes object / raw K8sGPT Result CR regression guards;
- deterministic safe replacement / rejection behavior;
- structured tests proving no sensitive fixture crosses the response boundary.

Sanitizer is **defense in depth**. It must not replace typed DTO/OpenAPI allowlists, and it must never justify reading Secrets, Pod Logs, raw Kubernetes objects, raw Result CR payloads or broader Kubernetes resources.

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

## Architecture

```text
Browser
  │
  ▼
Portal Edge / AuthN
  │
  ▼
Backend Principal
  │
  ▼
Authorization Guard
  │ deny by default
  ▼
Read-only Handler / Service
  │
  ▼
Typed Safe Projection
  │
  ▼
Sanitizer
  │
  ▼
OpenAPI Response DTO

Request lifecycle ─────────► Audit Recorder ─────────► Audit Sink
```

## Implementation Stages

### Phase 1.4.1 — Identity Contract — Completed

- Principal model
- Authenticator boundary
- 401/403 response contract
- request/correlation IDs
- OpenAPI contract and generated client updates

Completion record: `docs/phase-1.4.1-identity-contract.md`.

### Phase 1.4.2 — Authorization — Completed

- centralized capabilities
- deny-by-default backend enforcement
- cluster/namespace scopes
- `resources:read`
- Finding Detail real-scope authorization
- allow/deny matrix tests
- frontend 401/403 states without frontend authorization decisions

Completion record: `docs/phase-1.4.2-authorization.md`.

### Phase 1.4.3 — Audit — Completed

- fixed typed Event and bounded Outcome model
- provider-neutral Sink / SinkFunc
- request-local Recorder
- canonical route pattern capture
- Principal subject/provider only
- normalized capability/scope capture
- AuthN/AuthZ/result outcome and latency capture
- sink-failure isolation
- sensitive-data leakage regression coverage
- implementation PR #22 merged
- post-merge `main` CI #76 green

Completion record: `docs/phase-1.4.3-audit.md`.

### Phase 1.4.4 — Sanitizer — Entering

- centralized sanitizer package and policy
- safe text normalization and maximum-length policy
- credential/header/private-key pattern guards
- HTML/script safety rules
- response-boundary integration without arbitrary-object reflection
- raw Kubernetes/raw Result regression tests
- finding/list/summary/resource response leakage tests
- deterministic sanitize/reject result contract
- no Kubernetes privilege expansion

Design and acceptance boundary: `docs/phase-1.4.4-sanitizer.md`.

### Phase 1.4.5 — Production Gates

Aggregate production gates include:

- AuthN tests
- AuthZ matrix tests
- Audit leakage tests
- Sanitizer security regression tests
- unauthenticated/forbidden browser E2E
- OpenAPI/client drift gate
- Docker smoke
- Kubernetes v1.36 Kind E2E
- Secret Scan

## Acceptance Principle

Phase 1.4 is complete only when:

```text
AuthN proven
    +
AuthZ deny-by-default proven
    +
Audit coverage proven
    +
Sanitizer response-safety proven
    +
No privilege expansion
    +
PR Required Checks green
    +
Merge to main
    +
main Required Checks green
```
