# kube-aiops Security Design

## Security Principle

> AI and Portal may discover, summarize, correlate and explain problems, but they must not silently expand into sensitive-data access or mutation.

Defense in depth is enforced across:

```text
Web Portal capability boundary
        ↓
OpenAPI / Backend route boundary
        ↓
Authentication / Authorization
        ↓
Backend source adapter allowlists
        ↓
Typed Sanitizer / Audit
        ↓
Kubernetes RBAC / upstream read-only credentials
```

## Phase 1.4 Completed Security Boundary

Phase 1.4 completed the production security composition:

```text
Request metadata
    ↓
Structured Audit
    ↓
Trusted Authentication contract
    ↓
Deny-by-default Authorization
    ↓
Read-only Backend handler
    ↓
Typed safe projection
    ↓
Typed Sanitizer
    ↓
JSON response
```

Production mode requires a validated SecurityBundle and fails closed before serving protected traffic when mandatory security components are missing.

Authentication remains provider-neutral. The repository does not use fake allow-all production identity adapters, static browser tokens, unsigned `X-User`/`X-Groups`, browser kubeconfig or Kubernetes user-token proxying as substitutes for a trusted Authenticator.

## Kubernetes RBAC Baseline

K8sGPT workload remains read-only for approved business resources and explicitly denied Secret, Pod Log and write access.

Portal Backend uses a separate `kube-aiops-api` ServiceAccount with only permissions required by its safe read-only APIs.

Current forbidden Kubernetes capabilities remain:

```text
secrets
pods/log
create
update
patch
delete
deletecollection
```

Mutation and Auto Remediation remain disabled.

## Web Portal Boundary

The Web Portal:

- consumes only `clients/typescript/generated.ts`
- does not use Kubernetes credentials
- does not call Kubernetes API directly
- does not call Prometheus/Loki/Alertmanager directly
- does not expose Secret data
- does not expose raw Kubernetes objects
- does not expose raw K8sGPT Result CRs
- has no write, mutation, remediation, scale, patch or delete UI
- has no arbitrary resource/GVR URL construction
- has no arbitrary PromQL or LogQL query editor in Phase 2

Visible read-only UI state is never treated as authorization enforcement. Enforcement remains server-side and policy/RBAC-backed.

## Browser-Side Data Safety

All backend-controlled text inserted into HTML is escaped. The Portal does not intentionally render backend-provided HTML.

Static serving includes restrictive browser headers such as CSP, `nosniff`, no-referrer and restrictive permissions policy. The production static container runs non-root.

Phase 1.4 Sanitizer is a server-side defense-in-depth layer and remains active before JSON response emission.

## Audit Boundary

Structured Audit is limited to safe allowlisted metadata such as:

- request/correlation IDs
- Principal subject/provider
- canonical route
- application capability
- normalized cluster/namespace scope
- outcome/status/latency

Audit must not record Authorization/Cookie/token material, request/response bodies, raw Kubernetes objects, raw Result CR payloads, raw log lines, PromQL/LogQL strings or observability-provider credentials.

## Phase 2 Observability Source Boundary

Phase 2 adds read-only observability correlation while preserving the security layers above.

### Kubernetes Events

If Events permission is not already present, only the minimal core `events` read surface may be added:

```text
get
list
watch
```

Any RBAC change must retain negative tests proving Secrets, `pods/log` and all write verbs remain denied.

### Prometheus

The backend owns fixed, versioned PromQL templates and bounded query budgets. The browser cannot submit arbitrary PromQL, backend URLs or credentials.

Only normalized metric evidence crosses the API boundary; raw Prometheus response payloads and complete label sets do not.

### Loki

The backend owns fixed, versioned LogQL templates and bounded query budgets.

Phase 2 initially exposes normalized log signals only:

```text
fingerprint/class
count
firstSeen/lastSeen
allowlisted resource identity
```

Raw Pod log lines are not part of the Phase 2 external contract.

### Alertmanager

Phase 2 uses read-only alert retrieval and returns only allowlisted normalized alert metadata. Silence creation or other Alertmanager mutations are out of scope.

## API Error Boundary

Portal Backend converts Kubernetes and observability upstream failures into stable safe API errors/source states. The Web Portal must not reintroduce raw upstream error text.

A source timeout/error must not be silently converted into empty evidence. Correlation source state must distinguish available/unavailable/disabled/partial without leaking raw upstream responses.

## Forbidden Capabilities

The following remain forbidden at Phase 2 entry:

```text
create
update
patch
delete
deletecollection
pods/log raw streaming
secrets
raw Kubernetes object passthrough
raw Result CR passthrough
arbitrary PromQL proxy
arbitrary LogQL proxy
arbitrary Alertmanager mutation/proxy
Mutation
RCA-triggered action
Auto Remediation
```

Any future phase that needs mutation or a wider data boundary requires an explicit security review, new contract and new RBAC/upstream permission gates.

## Security Tests

Required regressions continue to include:

- Authentication/Authorization fail-closed behavior
- protected-route capability/scope coverage
- Audit leakage tests
- Sanitizer leakage/XSS tests
- API Contract Gate and generated client drift
- Secret Scan/Gitleaks
- production Docker fail-closed startup
- Kubernetes v1.36 Kind baseline
- Secret and `pods/log` denial
- create/update/patch/delete/deletecollection denial
- lifecycle/rollback/concurrency/trusted-uninstall E2E

Phase 2 adds source query-budget, partial-failure, correlation scope and observability-data leakage tests without weakening the existing Required Check identities.