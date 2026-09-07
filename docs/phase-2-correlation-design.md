# Phase 2 Correlation Design — Events / Prometheus / Loki / Alertmanager

## Purpose

Phase 2 introduces a deterministic, read-only observability correlation layer that enriches an already-authorized Finding with bounded evidence from Kubernetes Events, Prometheus, Loki and Alertmanager.

It must preserve the Phase 1.4 security model and must not expose raw observability backends as browser-accessible query proxies.

## Architectural Principle

Correlation is a backend-owned evidence workflow:

```text
Web Portal
    ↓ generated TypeScript client
Portal Backend
    ↓
Resolve Finding
    ↓
Authorize real cluster/namespace scope
    ↓
Correlation Engine
    ├── Kubernetes EventSource
    ├── Prometheus MetricSource
    ├── Loki LogSignalSource
    └── Alertmanager AlertSource
    ↓
Normalize / Bound / Correlate
    ↓
Typed CorrelationBundle
    ↓
Sanitizer
    ↓
JSON / Audit
```

The frontend supplies neither Kubernetes credentials nor raw PromQL/LogQL.

## Initial Protected Route

```text
GET /api/v1/findings/{id}/correlation
```

Recommended request parameters are intentionally narrow:

- optional bounded `window` selected from server-approved durations
- no arbitrary query expression
- no arbitrary backend URL
- no arbitrary label selector

The server resolves the Finding first and uses its normalized real scope as the correlation target.

## Capability

Add a centralized application capability:

```text
correlations:read
```

Expected route mapping:

| Route | Capability | Scope |
| --- | --- | --- |
| `GET /api/v1/findings/{id}/correlation` | `correlations:read` | resolved Finding cluster + optional namespace |

Finding ID is never itself a security scope.

## Core Domain Model

The exact Go/OpenAPI names may evolve, but the contract should remain typed and narrow.

Conceptual model:

```go
type Source string

const (
    SourceKubernetesEvents Source = "kubernetes-events"
    SourcePrometheus       Source = "prometheus"
    SourceLoki             Source = "loki"
    SourceAlertmanager     Source = "alertmanager"
)

type SourceState string

const (
    SourceAvailable   SourceState = "available"
    SourceUnavailable SourceState = "unavailable"
    SourceDisabled    SourceState = "disabled"
    SourcePartial     SourceState = "partial"
)

type TimeWindow struct {
    Start string `json:"start"`
    End   string `json:"end"`
}

type CorrelationTarget struct {
    Cluster   string              `json:"cluster"`
    Namespace string              `json:"namespace,omitempty"`
    Resource  finding.ResourceRef `json:"resource"`
}

type SourceStatus struct {
    Source Source      `json:"source"`
    State  SourceState `json:"state"`
}

type Signal struct {
    ID         string              `json:"id"`
    Source     Source              `json:"source"`
    Type       string              `json:"type"`
    Severity   string              `json:"severity,omitempty"`
    Resource   finding.ResourceRef `json:"resource,omitempty"`
    FirstSeen  string              `json:"firstSeen,omitempty"`
    LastSeen   string              `json:"lastSeen,omitempty"`
    Count      int                 `json:"count,omitempty"`
    Name       string              `json:"name,omitempty"`
    Value      *float64            `json:"value,omitempty"`
    Unit       string              `json:"unit,omitempty"`
    Fingerprint string             `json:"fingerprint,omitempty"`
}

type CorrelationBundle struct {
    FindingID string            `json:"findingId"`
    Target    CorrelationTarget `json:"target"`
    Window    TimeWindow        `json:"window"`
    Sources   []SourceStatus    `json:"sources"`
    Signals   []Signal          `json:"signals"`
}
```

The actual public DTO should not contain backend query strings, raw labels, raw upstream response bodies or provider credentials.

## Source Adapter Interfaces

Each observability system is isolated behind a read-only provider interface.

Conceptual shape:

```go
type Query struct {
    Target CorrelationTarget
    Window TimeWindow
    Budget Budget
}

type EventSource interface {
    Events(context.Context, Query) ([]EventSignal, error)
}

type MetricSource interface {
    Metrics(context.Context, Query) ([]MetricSignal, error)
}

type LogSignalSource interface {
    LogSignals(context.Context, Query) ([]LogSignal, error)
}

type AlertSource interface {
    Alerts(context.Context, Query) ([]AlertSignal, error)
}
```

Handlers must never know PromQL, LogQL or backend authentication details.

## Query Budgets

Every adapter must enforce a server-owned budget independent of frontend input.

Recommended first bounds:

```text
Default window:       30m
Maximum window:       2h
Per-source timeout:   3-5s
Maximum total signals per source: bounded
Maximum series:       bounded
Maximum samples:      bounded
Maximum Event objects scanned: bounded
Maximum alert objects scanned: bounded
Maximum Loki result entries scanned: bounded before aggregation
```

Concrete values should be configuration constants with tests and may be tuned after measured production load.

No adapter may issue an unbounded query because a user supplied a broad time or label selector.

## Kubernetes Events Adapter

### Purpose

Correlate scheduling, image, volume, probe, eviction and controller events with the Finding target.

### Matching

Prefer exact object identity where available:

```text
namespace
kind
name
```

Allow a small explicit related-object strategy only where it can be derived safely, for example a Deployment and its selected Pods. Do not introduce an arbitrary GVR graph crawler in Phase 2.

### Output

Normalize only bounded fields such as:

- reason
- type
- involved resource reference
- first/last timestamp
- count
- source component if allowlisted

Do not return the entire Event object.

### RBAC

If needed, extend only the Portal Backend ServiceAccount with core Event `get/list/watch`. Keep explicit tests proving Secrets, `pods/log` and write verbs remain denied.

## Prometheus Adapter

### Query Catalog

PromQL is owned by a versioned backend catalog, for example:

```text
pod_cpu_usage
pod_memory_working_set
pod_restart_rate
pod_network_receive
pod_network_transmit
workload_ready_replicas
node_pressure_indicator
```

The browser sends a semantic request through the correlation route, never raw PromQL.

Each catalog entry defines:

- supported resource kinds
- label mapping strategy
- instant or range query
- server-owned range/step
- maximum series/samples
- normalization function
- unit

### Output

Return bounded metric evidence, not the full Prometheus API response.

Possible safe fields:

- metric signal name
- current/peak value
- baseline/reference value where deterministic
- unit
- first/last breach time
- sample count

Do not expose Prometheus credentials, backend URLs, complete label sets or raw query strings.

## Loki Adapter

### Security Boundary

Phase 2 does not expose raw Pod logs.

The backend may query Loki with fixed LogQL templates but initially emits only normalized log signals:

```text
fingerprint / class
count
firstSeen
lastSeen
allowlisted resource identity
```

Raw message text is not part of the initial API contract.

### Query Catalog

Examples of server-owned patterns may classify:

- panic / fatal
- OOM-related application messages
- connection timeout
- upstream 5xx
- JVM OOM / GC pressure signatures
- application restart signatures

Any pattern must be versioned and tested for query cost. The browser cannot submit regex or LogQL expressions.

## Alertmanager Adapter

### Read-only Scope

Normalize active/recent alerts relevant to the target cluster/namespace/resource.

Output only allowlisted data such as:

- alert name
- state
- severity if present
- startsAt / endsAt
- selected resource identity labels
- stable fingerprint

Do not expose all labels/annotations by default because they may contain sensitive or high-cardinality content.

The adapter must not create/update silences or mutate Alertmanager state.

## Correlation Algorithm

Phase 2 correlation is deterministic and explainable; it is not an LLM RCA engine.

Initial matching dimensions:

1. **Scope match**
   - same cluster
   - same namespace when namespaced
   - same resource identity when available

2. **Time overlap**
   - signal falls inside the approved correlation window
   - optionally weight signals closer to `Finding.CreatedAt`

3. **Source semantics**
   - Event reasons known to relate to the resource/problem class
   - metric threshold/breach produced by a fixed catalog rule
   - log fingerprint class
   - alert identity/severity

4. **Deduplication**
   - stable source-specific fingerprint
   - aggregate repeated signals rather than emitting unbounded items

A future `score` may be added only if its calculation is deterministic, documented and tested. Phase 2 must not present correlation as causal proof.

## Partial Failure Model

Adapters execute with independent contexts/timeouts and can run concurrently within a global request budget.

Recommended semantics:

```text
Prometheus succeeds
Events succeeds
Loki times out
Alertmanager disabled
        ↓
HTTP 200
sources:
  events       available
  prometheus   available
  loki         unavailable
  alertmanager disabled
signals: successful bounded evidence only
```

If every configured source is unavailable and no safe evidence can be produced, return a stable `503` such as:

```text
CORRELATION_UNAVAILABLE
```

Never convert source failures into a fake empty-success state.

Raw source errors must remain server-side and must be logged/audited only through stable safe reason codes.

## Sanitizer Boundary

All public correlation DTOs pass a typed sanitizer before JSON emission.

Sanitizer responsibilities include:

- enum/status validation
- resource identifier bounds
- signal count bounds
- string length/control-character validation
- allowlist validation for source/type/unit
- no unexpected arbitrary maps

Sanitization does not justify ingesting or returning forbidden raw data.

## Audit Boundary

Audit should record the correlation request as a protected operation with:

- canonical route
- `correlations:read`
- Principal subject/provider
- resolved cluster/namespace scope
- outcome/status/latency

Audit must not record:

- PromQL
- LogQL
- source credentials
- raw log lines
- raw Event objects
- raw Prometheus/Loki/Alertmanager payloads
- Finding diagnostic text

## OpenAPI / Client Boundary

OpenAPI remains the only external contract source:

```text
api/openapi.yaml
        ↓
API Contract Gate
        ↓
clients/typescript/generated.ts
        ↓
web/src/api/client.ts
        ↓
Correlation view
```

The Web Portal must not define a second hand-written CorrelationBundle contract.

## Portal UX

Finding Detail may add a read-only correlation section grouped by source:

```text
Finding Detail
    ├── Kubernetes Events
    ├── Metric Signals
    ├── Log Signals
    └── Alerts
```

The UI must distinguish:

- no evidence
- source disabled
- source unavailable
- partial result
- authorization denied
- authentication required

It must not render source text as HTML and must not offer arbitrary query editors in Phase 2.

## CI / E2E Gates

Phase 2 must preserve and extend the existing governance checks.

Required static/unit gates:

- source query catalog validation
- no raw PromQL/LogQL request parameter in OpenAPI
- no arbitrary upstream URL parameter
- route → capability/scope/audit/sanitizer coverage
- adapter timeout/budget tests
- deterministic partial failure tests
- sanitizer leakage tests
- generated TypeScript drift check

Kind / integration gates should prove where practical:

- Kubernetes Events can be read with only the intended permission
- Secret read remains denied
- `pods/log` remains denied
- create/update/patch/delete/deletecollection remain denied
- correlation route enforces AuthN/AuthZ before source work
- source failure returns stable safe status

External Prometheus/Loki/Alertmanager provider E2E may use deterministic local fixtures in CI, but test adapters must never become production defaults.

## Phase 3 Contract

Phase 3 RCA consumes the safe `CorrelationBundle` produced by Phase 2.

```text
Phase 2
Finding + bounded observability evidence
        ↓
CorrelationBundle
        ↓
Phase 3 RCA Agent
```

Phase 3 should not require browser-side direct access to Prometheus/Loki/Alertmanager and should not require reopening the raw-data boundaries closed by Phase 2.