# Phase 2 Entry — Observability Correlation

## Status

**Entering. Runtime implementation has not started.**

Phase 2 must start only after this completion/entry transition is merged and the resulting `main` again passes all Required Checks.

Entry evidence from the completed security phase:

- previous phase: Phase 1.4 — Completed
- Phase 1.4 final implementation PR: `#26 feat: Phase 1.4.5 Production Gates`
- completed runtime baseline: `main@f3e794784fea1986f731191e4be4aa3153fe292d`
- post-merge CI: `kube-aiops CI #87` — `completed / success`
- Required Checks: all green
  - `Preflight / Lint / RBAC`
  - `Secret Scan`
  - `Kubernetes v1.36 Kind E2E`
- Kubernetes baseline: `v1.36.4`

## Goal

Phase 2 adds a bounded, read-only observability correlation layer around the existing safe Finding model.

The core question is:

```text
For this already-authorized Finding and its real cluster/namespace/resource scope,
what Kubernetes Events, metric signals, log signals and alerts occurred in the same time/resource context?
```

Target flow:

```text
Finding
   ↓ resolve real scope + authorize
Correlation Request
   ↓
┌───────────────────────────────────────────────┐
│ Read-only source adapters                     │
│                                               │
│ Kubernetes Events                            │
│ Prometheus metrics                           │
│ Loki log signals                             │
│ Alertmanager alerts                          │
└───────────────────────────────────────────────┘
   ↓
Normalization / budgets / source status
   ↓
Deterministic correlation
   ↓
Typed Correlation Bundle
   ↓
Sanitizer / Audit / OpenAPI
   ↓
Web Portal
```

## Phase Boundary

Phase 2 is **evidence correlation**, not automated reasoning or remediation.

Included:

- Kubernetes Event correlation
- Prometheus metric correlation
- Loki-derived log signal correlation
- Alertmanager alert correlation
- normalized time/resource scope
- deterministic source status and correlation metadata
- read-only OpenAPI DTOs and Portal visualization
- source timeout, cardinality and response-size budgets
- explicit partial-failure semantics

Explicitly not included:

- RCA Agent
- Runbook Agent
- write actions
- scale/restart/patch/delete
- Mutation
- Auto Remediation
- arbitrary PromQL proxy
- arbitrary LogQL proxy
- arbitrary Alertmanager API proxy
- arbitrary Kubernetes API proxy
- raw Kubernetes object passthrough
- raw K8sGPT Result CR passthrough
- raw Pod log streaming

Phase 3 may consume the safe Phase 2 Correlation Bundle as evidence for RCA, but Phase 3 must not be smuggled into Phase 2.

## Initial User/API Surface

The first protected correlation surface should be anchored to an existing Finding rather than an arbitrary browser-supplied observability query:

```text
GET /api/v1/findings/{id}/correlation
```

Security order:

```text
resolve safe Finding by opaque id
        ↓
derive real cluster / namespace / resource
        ↓
Authorize correlations:read for that real scope
        ↓
query approved source adapters with bounded templates
        ↓
normalize / correlate / sanitize
        ↓
return typed Correlation Bundle
```

The opaque Finding ID alone is never an authorization scope.

## Authorization Extension

Phase 2 should add one application-level capability:

```text
correlations:read
```

It remains an application policy capability, not a Kubernetes RBAC verb.

The existing deny-by-default route coverage gate must be extended so a new correlation route cannot exist without:

- authentication
- `correlations:read`
- normalized scope extraction
- Audit canonical route metadata
- typed Sanitizer boundary
- OpenAPI coverage

## Source Security Model

### Kubernetes Events

Use the Kubernetes API only through a dedicated read-only adapter.

If current Portal Backend RBAC does not already permit Events, Phase 2 may add only the minimal explicit read extension required for core `events`:

```text
get
list
watch
```

Any such change must be accompanied by live and static negative assertions that still deny:

- Secrets
- `pods/log`
- create/update/patch/delete/deletecollection

### Prometheus

The browser never submits arbitrary PromQL.

The backend owns a versioned query catalog keyed by approved signal names/resource types. Queries have bounded time range, step, series count and response size.

### Loki

The browser never submits arbitrary LogQL.

Phase 2 initially exposes only normalized log signals such as fingerprint/class/count/firstSeen/lastSeen and allowlisted resource labels. Raw log lines are not part of the Phase 2 contract.

### Alertmanager

Use read-only alert retrieval and normalize only allowlisted labels/state/time information. Silence creation, alert mutation and arbitrary API passthrough remain out of scope.

## Failure Semantics

One source being unavailable must not silently become an empty successful source.

Correlation responses should make source state explicit:

```text
available
unavailable
disabled
partial
```

Raw upstream errors, URLs, credentials, query strings and response payloads must not be returned to the browser or Audit events.

A useful correlation response may be returned when at least one configured source succeeds; if no configured evidence source can produce a safe result, return a stable service-unavailable error.

## Implementation Stages

Recommended Phase 2 implementation sequence:

1. **Phase 2.1 — Correlation Contract / Core**
   - typed Signal / SourceStatus / CorrelationBundle
   - `correlations:read`
   - route/OpenAPI/generated-client coverage
   - time/scope/budget primitives

2. **Phase 2.2 — Kubernetes Events Adapter**
   - minimal read-only Events integration
   - normalization and bounded event evidence

3. **Phase 2.3 — Prometheus Adapter**
   - fixed query catalog
   - bounded instant/range metric signals

4. **Phase 2.4 — Loki + Alertmanager Adapters**
   - log fingerprint/count signals without raw logs
   - normalized read-only alert evidence

5. **Phase 2.5 — Correlation Engine + Portal**
   - deterministic evidence assembly/scoring
   - partial-failure source state
   - Finding Detail correlation view
   - browser E2E

The exact PR decomposition may be smaller or larger, but every merge must preserve the existing Required Check identities and Phase 1.4 security invariants.

## Completion Conditions

Phase 2 is not complete until:

- all four evidence sources have production-safe adapters or are explicitly documented/configured as disabled
- the correlation API is backend-authorized by real Finding scope
- arbitrary observability queries are impossible from the browser
- source queries have tested budgets/timeouts/cardinality limits
- raw logs/raw upstream payloads do not cross the safe API boundary
- source partial failure is explicit and deterministic
- Correlation Bundle contract and generated client are stable
- Portal correlation rendering is escaped and read-only
- Secret/pods-log/write RBAC negative gates remain green
- all three Required Checks pass on PR and post-merge `main`

Required Check names remain exact:

1. `Preflight / Lint / RBAC`
2. `Secret Scan`
3. `Kubernetes v1.36 Kind E2E`
