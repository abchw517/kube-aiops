# kube-aiops Security Design

## Security Principle

> AI and Portal may discover, summarize and explain problems, but Phase 1.x must not mutate business resources.

Defense in depth is enforced at four layers:

```text
Web Portal capability boundary
        ↓
OpenAPI / Backend route boundary
        ↓
Backend Kubernetes client whitelist
        ↓
Kubernetes RBAC
```

## Kubernetes RBAC Baseline

K8sGPT workload remains read-only for approved business resources and is explicitly denied Secret, Pod Log and write access.

Portal Backend uses a separate `kube-aiops-api` ServiceAccount with only the permissions required by its safe read-only APIs. Kubernetes write verbs, Secret access and `pods/log` remain denied.

## Phase 1.3 Web Boundary

The Web Portal:

- consumes only `clients/typescript/generated.ts`
- does not use Kubernetes credentials
- does not call Kubernetes API directly
- does not expose Pod Logs
- does not expose Secret data
- does not expose raw Kubernetes objects
- does not expose raw K8sGPT Result CRs
- has no write, mutation, remediation, scale, patch or delete UI
- has no arbitrary resource/GVR URL construction

The visible `READ ONLY` badge is not treated as an authorization mechanism. Enforcement remains server-side and RBAC-backed.

## Browser-Side Data Safety

All backend-controlled text inserted into HTML is escaped. The Portal does not intentionally render backend-provided HTML.

Static serving adds:

- Content-Security-Policy: self-only scripts/styles/connections; no objects; no framing
- `X-Content-Type-Options: nosniff`
- `Referrer-Policy: no-referrer`
- restrictive `Permissions-Policy`

The production static container runs as non-root UID/GID 101.

## API Error Boundary

Portal Backend already converts Kubernetes/upstream failures into stable safe API errors. The Web Portal shows generic generated-client request failures and must not reintroduce raw Kubernetes error text.

## Forbidden Capabilities

The following remain forbidden in Phase 1.3:

```text
create
update
patch
delete
deletecollection
pods/log
secrets
raw Kubernetes object passthrough
raw Result CR passthrough
Mutation
Auto Remediation
```

Any future phase that needs one of these capabilities requires an explicit security review and a new contract/RBAC change. Phase 1.3 must not prepare hidden write hooks for later use.

## Security Tests

Phase 1.3 adds frontend tests for HTML escaping and read-only state behavior while retaining all existing Phase 1.1/1.2 RBAC, Secret, OpenAPI sensitive-field and Kubernetes E2E gates.
