# Phase 1.2 Completion Record

## Status

**Phase 1.2: Portal Backend API — Completed**

Completion baseline:

- `main` merge commit: `de79b5536ee2a1e5bca7206d8b1268faab3fdc06`
- Phase 1.2.4 PR: `#12`
- Post-merge GitHub Actions run: `#45` (`33520705138`)
- Historical Kubernetes validation baseline: `v1.34 Kind E2E`

> `v1.34` is retained here as historical evidence of the exact Phase 1.2 completion run.
> The repository's current supported platform baseline is maintained separately in
> `docs/kubernetes-v1.36-baseline.md` and is upgraded to Kubernetes v1.36.4 only after
> its PR and post-merge `main` gates are green.

## Completion Gate

Phase 1.2 is only considered complete after the following sequence has been satisfied:

```text
Phase 1.2.4 API Contract Closure implemented
        ↓
PR Required Checks all green
        ↓
Merge to main
        ↓
main Required Checks all green again
        ↓
Phase 1.2 = Completed
```

All gates above were satisfied for the Phase 1.2 completion baseline.

## Required Checks Evidence

Post-merge `main` run #45 completed successfully with all required jobs green:

- `Secret Scan`: PASS
- `Preflight / Lint / RBAC`: PASS
- `Kubernetes v1.34 Kind E2E`: PASS

The `Preflight / Lint / RBAC` job also confirmed:

- OpenAPI Contract Gate: PASS
- Go backend checks: PASS
- Docker backend smoke test: PASS
- Project preflight: PASS

The historical Kind E2E job confirmed:

- lifecycle / rollback / concurrency / trusted uninstall E2E: PASS
- Phase 1.2 readonly API E2E: PASS

These entries intentionally preserve the original run #45 job names. They must not be
rewritten to v1.36 because that would falsify the audit trail.

## Phase 1.2 Delivered Scope

Phase 1.2 established the read-only Portal Backend contract and implementation required by the Web Portal layer:

- cluster discovery
- namespace discovery
- resource detail lookup
- Finding list
- Finding detail
- Finding summary
- health and readiness endpoints
- stable error model
- OpenAPI 3.1 contract
- deterministic TypeScript client generation
- Server route ↔ OpenAPI route drift guard
- Go DTO ↔ OpenAPI Schema drift guard
- Severity enum drift guard
- generated-client drift guard
- sensitive/raw-field contract guard

The Phase 1.2 API contract covers the following GET routes:

```http
GET /healthz
GET /readyz
GET /api/v1/clusters
GET /api/v1/clusters/{cluster}/namespaces
GET /api/v1/clusters/{cluster}/resources/{kind}/{namespace}/{name}
GET /api/v1/findings
GET /api/v1/findings/summary
GET /api/v1/findings/{id}
```

## Security Boundary Preserved

Phase 1.2 did not expand the production mutation boundary:

- Kubernetes Secret access remains DENY
- `pods/log` remains DENY
- Kubernetes `create/update/patch/delete` remains DENY
- Raw K8sGPT Result CR is not exposed through the Portal contract
- Raw Kubernetes objects are not exposed through the Portal contract
- Mutation is not enabled
- Auto Remediation is not enabled
- AI remains advisory/read-only

These constraints remain mandatory for Phase 1.3 unless a later security phase explicitly changes them.

## Kubernetes v1.36 Platform Supersession

After Phase 1.2 completion, kube-aiops performs a dedicated platform-baseline upgrade before
Phase 1.3. The target/current support contract is defined by:

```text
Kubernetes:      v1.36.4
Kind:            v0.33.0
kubectl:         v1.36.4
Go toolchain:    1.26.5
K8sGPT Operator: v0.2.29
K8sGPT Engine:   v0.4.37 (immutable digest)
```

This supersession does **not** reopen Phase 1.2 feature scope. It validates the already frozen
Phase 1.2 backend and Phase 1.1 lifecycle on Kubernetes v1.36.4. Activation requires the new
`Kubernetes v1.36 Kind E2E` Required Check to pass on both the upgrade PR and post-merge `main`.

## Phase 1.3 Entry Gate

Phase 1.3 Web Portal may start only from a green `main` after the Kubernetes v1.36 platform
baseline is merged and its post-merge Required Checks are green.

Phase 1.3 must consume the committed OpenAPI/TypeScript client contract rather than introducing a second handwritten API model.

Required sequence:

```text
Phase 1.2 Completed (historical v1.34 evidence retained)
        ↓
Kubernetes v1.36 platform upgrade PR all green
        ↓
Required Check switched to Kubernetes v1.36 Kind E2E
        ↓
Merge platform baseline to main
        ↓
main all green again on Kubernetes v1.36.4
        ↓
Start Phase 1.3 Web Portal
```

Phase 1.2 backend scope remains frozen as **Completed**.
