# Phase 2 Completion — Observability Correlation

## Status

**Completed on 2026-11-11.**

Verified runtime baseline `main@a5a8843e41c25ff2896095437e0684cf24a5cb4d`.

| Verification | Commit | Workflow |
| --- | --- | --- |
| Final implementation PR [#33](https://github.com/abchw517/kube-aiops/pull/33) | `c968f47a92efc2ed97a73d2e80235a7ea98daf17` | [kube-aiops CI #114](https://github.com/abchw517/kube-aiops/actions/runs/42992488828) — success |
| Post-merge `main` | `a5a8843e41c25ff2896095437e0684cf24a5cb4d` | [kube-aiops CI #115](https://github.com/abchw517/kube-aiops/actions/runs/42994549711) — success |

All three required checks passed on both runs.

- `Preflight / Lint / RBAC`
- `Secret Scan`
- `Kubernetes v1.36 Kind E2E`

## Delivered Stages

| Stage | Delivered scope | Implementation |
| --- | --- | --- |
| 2.1 Correlation contract and core | Finding-scoped authorization, typed evidence, bounded query window and source budgets, generated TypeScript client | [#28](https://github.com/abchw517/kube-aiops/pull/28) |
| 2.2 Kubernetes Events | Read-only, resource-scoped event evidence | [#29](https://github.com/abchw517/kube-aiops/pull/29) |
| 2.3 Prometheus | Bounded metric queries for supported Pod, Deployment and Node signals | [#31](https://github.com/abchw517/kube-aiops/pull/31) |
| 2.4 VictoriaLogs and Alertmanager | Six Pod log classifications, resource-scoped alerts, optional source configuration | [#30](https://github.com/abchw517/kube-aiops/pull/30), [#32](https://github.com/abchw517/kube-aiops/pull/32) |
| 2.5 Correlation engine and Portal | Deterministic evidence ranking and deduplication, read-only Finding Detail with source states and section-level retry | [#33](https://github.com/abchw517/kube-aiops/pull/33) |

## Acceptance Coverage

- The correlation endpoint resolves the Finding and authorizes its actual scope before querying sources.
- Adapters use fixed queries, bounded time and response budgets, and normalized evidence. The API exposes source availability and preserves evidence from successful sources during partial failures.
- The OpenAPI contract and generated client pass drift checks. Browser tests cover grouped evidence, ranking, mobile layout, empty and degraded states, authentication and authorization errors, retry, and HTML escaping.
- Kubernetes v1.36.4 Kind checks exercise the platform baseline, lifecycle and rollback, read-only API and Events correlation. Negative RBAC assertions cover Secrets, `pods/log` and write verbs.
- Local checks with Prometheus, VictoriaLogs and Alertmanager verified the configured adapter paths. The Kubernetes Events adapter was exercised against Kind.

External sources are opt-in through backend configuration. See the [correlation design](phase-2-correlation-design.md) for scopes, budgets and deployment settings. Production startup requires the trusted authentication configuration established in [Phase 1.4](phase-1.4-completion.md).

The bounded `CorrelationBundle` is the evidence input for the planned Phase 3 RCA Agent and Runbook.
