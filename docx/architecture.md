# kube-aiops Architecture

## Current Phase Model

```text
Phase 1.1 K8sGPT Engine            — Completed
Phase 1.2 Portal Backend API       — Completed
Phase 1.3 Web Portal               — Completed
Phase 1.4 Security Hardening       — Completed
Phase 2   Observability Correlation — Completed
Phase 3   RCA Agent + Runbook      — Planned
Phase 4   HITL Remediation         — Planned
Phase 5   Controlled Auto Remediation — Planned
```

See the [project phase status](phase-status.md) for delivered features and the [Phase 2 completion record](phase-2-completion.md) for the evidence correlation baseline.

## End-to-End Architecture

The [README topology](../README.md#架构拓扑) shows the deployment and service dependencies. The Portal API assembles the following flows.

| Flow | Source and processing | Result |
| --- | --- | --- |
| Cluster discovery and diagnosis | The local and remote direct modes use Kubernetes API reads. The reporting mode sends Result snapshots from a remote CronJob over HTTPS. The Portal applies the current identity and cluster scope. | Findings and read-only resource projections |
| Evidence and AI review | The correlation service queries Events, Prometheus, VictoriaLogs and Alertmanager within the Finding scope and a bounded time window. Optional AI review reads approved Engine model configuration and combines the available evidence and applicable knowledge. | Correlation bundle and versioned review saved in SQLite |
| Operations knowledge | A saved Finding review or an import can produce a candidate in SQLite. Reviewers confirm publication. Search and Q&A use visible published knowledge, and Q&A links to a version and chapter. Optional HTTPS Wiki synchronization produces candidates and run records. | Searchable knowledge, feedback and governance history |
| System and resource administration | Local or LDAP identity feeds the Portal session and authorization layer. Effective diagnostic scopes use direct and group-inherited roles and the resource catalog. | Scoped access, resource directory and operation audit |

The browser uses the same-origin Portal endpoint. Kubernetes, observability sources, LDAP, the AI provider and optional remote Wiki are backend connections.

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

OpenAPI defines the diagnostic query contract:

```text
api/openapi.yaml
      ↓
tools/openapi/contract.py
      ↓
web/src/api/generated.ts
      ↓
web/src/api/client.ts
      ↓
Portal views
```

Diagnostic views use the generated Finding, Summary, CorrelationBundle, Cluster, Namespace and error DTOs. Authenticated administrative and Wiki routes are registered under `api/internal/portal/` and `api/internal/wiki/`. The P0/P1 Wiki routes are documented in the [module contract](operations-knowledge/api-contract.md).

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

The Gateway routes same-origin API requests to the Portal Backend service and static requests to the Web service. For SQLite-backed production deployments, one API replica mounts the persistent volume and uses the Recreate deployment strategy. See [production deployment](install-deployment.md#生产部署) for configuration and upgrades.

## Phase Boundaries

Currently supported capabilities:

- read-only Kubernetes safe projections
- normalized Finding List / Detail / Summary
- cluster / namespace / severity / kind filtering
- K8sGPT advisory diagnostics
- Phase 1.4 AuthN/AuthZ/Audit/Sanitizer/Production Gates
- Phase 2.1 bounded correlation contract/core
- Phase 2.2 Kubernetes Events correlation
- Phase 2.3 Prometheus metric correlation
- Phase 2.4 VictoriaLogs log classification and Alertmanager evidence
- Phase 2.5 evidence ranking and read-only Finding Detail view

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
