# kube-aiops Architecture

## Current Phase Model

```text
Phase 1.1 K8sGPT Engine            — Completed
Phase 1.2 Portal Backend API       — Completed
Phase 1.3 Web Portal               — Completed
Phase 1.4 Security Hardening       — Completed
Phase 2   Observability Correlation — In Development
Phase 3   RCA Agent + Runbook      — Planned
Phase 4   HITL Remediation         — Planned
Phase 5   Controlled Auto Remediation — Planned
```

## End-to-End Architecture

```text
User / SRE
    |
    v
Web Portal
    |
    | generated TypeScript Client only
    v
Portal Backend API
    |
    +----------------------+--------------------------+
    |                      |                          |
    v                      v                          v
Safe Kubernetes        Finding Model          Phase 2 Correlation
Projection                 ^                         Engine
                           |                          |
                    Result Adapter        +----------+----------------+
                           ^               |          |                |
                           |               v          v                v
                    K8sGPT Result CR   Events     Prometheus      VictoriaLogs
                           ^                                        |
                           |                                        v
                    K8sGPT Engine                             Alertmanager
                           ^
                           |
                    K8sGPT Operator
```

Phase 2 source adapters remain server-side. The browser never talks directly to Kubernetes, Prometheus, VictoriaLogs or Alertmanager.

## Security Composition

Phase 1.4 completed the Portal security boundary:

```text
Request / Correlation Metadata
        ↓
Structured Audit
        ↓
Trusted Authentication contract
        ↓
Deny-by-default Authorization
        ↓
Read-only handler
        ↓
Typed safe projection
        ↓
Typed Sanitizer
        ↓
OpenAPI response
```

Production startup fails closed when mandatory security components are missing. Development compatibility is explicit and cannot silently become the production path.

## Contract Boundary

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

Portal code must not define a second copy of Finding, Summary, CorrelationBundle, Cluster, Namespace or error DTOs and must not issue direct handwritten API requests.

## Finding Domain Flow

```text
Route / Filter change
        ↓
Generated KubeAIOpsApiClient
        ├── listClusters
        ├── listNamespaces
        ├── listFindings
        ├── summarizeFindings
        ├── getFinding
        └── Phase 2: getFindingCorrelation
        ↓
Loading / Empty / Partial / Error / Retry state machine
        ↓
Finding List / Summary / Detail / Correlation rendering
```

A render generation counter prevents an older asynchronous response from replacing a newer filter or route result.

## Phase 2 Correlation Boundary

Phase 2 enriches an already-authorized Finding with bounded observability evidence:

```text
Finding
   ↓ resolve real scope
correlations:read authorization
   ↓
Correlation Engine
   ├── Kubernetes EventSource
   ├── Prometheus MetricSource
   ├── VictoriaLogs LogSignalSource
   └── Alertmanager AlertSource
   ↓
Normalize / Budget / Correlate
   ↓
Typed CorrelationBundle
   ↓
Sanitizer / Audit / OpenAPI
```

Phase 2 does not expose arbitrary PromQL, LogsQL, Alertmanager API calls or Kubernetes API passthrough. VictoriaLogs initially produces normalized log fingerprints/counts/time ranges rather than raw log lines.

## Deployment Model

The Portal is built into static assets and served by a non-root Nginx container on port 8080. The production routing model should keep the Portal and Backend behind the same origin, with `/api/` routed to Portal Backend and all other Portal paths routed to the static service.

The static container does not contain Kubernetes credentials and does not communicate with Kubernetes or observability backends directly.

## Phase Boundaries

Currently supported capabilities:

- read-only Kubernetes safe projections
- normalized Finding List / Detail / Summary
- cluster / namespace / severity / kind filtering
- K8sGPT advisory diagnostics
- Phase 1.4 AuthN/AuthZ/Audit/Sanitizer/Production Gates
- Phase 2.1 bounded correlation contract/core
- Phase 2.2 Kubernetes Events correlation

Phase 2 adds only read-only observability correlation.

Not part of Phase 2:

- raw Pod Logs
- Secrets
- raw Kubernetes objects
- raw Result CR
- arbitrary PromQL/LogsQL query editors
- mutation
- RCA Agent
- Runbook execution
- HITL remediation
- Auto Remediation
- arbitrary GVR passthrough
