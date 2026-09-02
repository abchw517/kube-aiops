# kube-aiops Architecture

## Current Phase Model

```text
Phase 1.1 K8sGPT Engine        — Completed
Phase 1.2 Portal Backend API   — Completed
Phase 1.3 Web Portal           — In Development
```

## End-to-End Architecture

```text
User / SRE
    |
    v
Phase 1.3 Web Portal
    |
    | generated TypeScript Client only
    v
Phase 1.2 Portal Backend API
    |
    +-----------------------------+
    |                             |
    v                             v
Safe Kubernetes Projection    Finding Model
                                  ^
                                  |
                           Result Adapter
                                  ^
                                  |
                           K8sGPT Result CR
                                  ^
                                  |
                           K8sGPT Engine
                                  ^
                                  |
                           K8sGPT Operator
```

## Phase 1.3 Contract Boundary

OpenAPI remains the single external contract source:

```text
api/openapi.yaml
      ↓
tools/openapi/contract.py
      ↓
clients/typescript/generated.ts
      ↓
web/src/api/client.ts
      ↓
Portal views
```

Portal code must not define a second copy of Finding, Summary, Cluster, Namespace or error DTOs and must not issue direct handwritten API requests.

## Finding Domain Flow

```text
Route / Filter change
        ↓
Generated KubeAIOpsApiClient
        ├── listClusters
        ├── listNamespaces
        ├── listFindings
        ├── summarizeFindings
        └── getFinding
        ↓
Loading / Empty / Error / Retry state machine
        ↓
Finding List / Summary / Detail rendering
```

A render generation counter prevents an older asynchronous response from replacing a newer filter or route result.

## Deployment Model

The Portal is built into static assets and served by a non-root Nginx container on port 8080. The production routing model should keep the Portal and Backend behind the same origin, with `/api/` routed to Portal Backend and all other Portal paths routed to the static service.

The static container does not contain Kubernetes credentials and does not communicate with Kubernetes directly.

## Phase Boundaries

Current supported capabilities:

- read-only Kubernetes safe projections
- normalized Finding List / Detail / Summary
- cluster / namespace / severity / kind filtering
- K8sGPT advisory diagnostics

Not part of Phase 1.3:

- Pod Logs
- Secrets
- raw Kubernetes objects
- raw Result CR
- mutation
- auto remediation
- arbitrary GVR passthrough
- Prometheus/Loki correlation
