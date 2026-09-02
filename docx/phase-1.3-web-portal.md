# Phase 1.3 Web Portal — Requirements, Architecture and Acceptance

## Status

Phase 1.3 implementation branch: `phase-1.3-web-portal`.

Entry baseline:

- Phase 1.1: K8sGPT Engine — Completed
- Phase 1.2: Portal Backend API — Completed
- Kubernetes: v1.36.4
- `release/phase-1.2` and `main`: `f7e785b6f1d06c9cde2a49407ce49fc03381f941`
- Phase 1.3 starts only from the green `main` baseline above.

## Goal

Deliver the first production-oriented read-only Web Portal around one normalized Finding domain loop:

```text
Finding List
    ↓
Finding Detail
    ↓
Finding Summary
    ↓
Cluster / Namespace / Severity / Kind filters
```

The Portal is a presentation and interaction layer over the frozen Phase 1.2 contract. It must not become a second API model, a Kubernetes passthrough, or a mutation surface.

## Directory Structure

```text
web/
├── Dockerfile
├── nginx.conf
├── index.html
├── package.json
├── tsconfig.json
├── scripts/
│   └── build.mjs
├── src/
│   ├── api/
│   │   └── client.ts
│   ├── components/
│   │   ├── html.ts
│   │   └── states.ts
│   ├── domain/
│   │   └── filters.ts
│   ├── views/
│   │   ├── finding-list.ts
│   │   └── finding-detail.ts
│   ├── app.ts
│   ├── main.ts
│   └── styles.css
├── tests/
└── e2e/
```

## Contract Rule

The only HTTP client for Portal Backend calls is:

```text
clients/typescript/generated.ts
```

The Web Portal imports and consumes generated types and generated operations directly:

- `KubeAIOpsApiClient`
- `Finding`
- `FindingPage`
- `FindingSummary`
- `ListFindingsParams`
- `SummarizeFindingsParams`
- `Cluster`
- `Namespace`
- `Severity`

Forbidden:

- copying API DTO interfaces into `web/`
- manually rebuilding `/api/v1/...` request URLs in Portal code
- bypassing OpenAPI with direct `fetch()` calls
- adding frontend-only interpretations of raw Result CR or raw Kubernetes objects

## Interface Mapping

| Portal capability | Generated client operation | Backend route |
|---|---|---|
| Cluster filter | `listClusters()` | `GET /api/v1/clusters` |
| Namespace filter | `listNamespaces()` | `GET /api/v1/clusters/{cluster}/namespaces` |
| Finding List | `listFindings()` | `GET /api/v1/findings` |
| Finding Summary | `summarizeFindings()` | `GET /api/v1/findings/summary` |
| Finding Detail | `getFinding()` | `GET /api/v1/findings/{id}` |

The initial list page sends only Phase 1.2 contract parameters: `cluster`, `namespace`, `severity`, `kind`, plus a bounded `limit=50`.

## UI State Model

Each asynchronous page must have explicit state coverage:

```text
Loading → Success
            ├── Data
            └── Empty
Loading → Error → Retry → Loading
```

Requirements:

- Loading state must be visible and accessible.
- Empty state must distinguish an empty successful response from an error.
- Error state must not expose raw Kubernetes errors or sensitive backend internals.
- Retry must repeat the same read-only generated-client request.
- Stale asynchronous responses must not overwrite a newer route/filter render.

## Security Boundary

Phase 1.3 inherits and tightens the Phase 1.2 boundary:

- no Pod Logs
- no Secret access
- no raw Kubernetes object view
- no raw K8sGPT Result CR view
- no create/update/patch/delete controls
- no Mutation
- no Auto Remediation
- no arbitrary URL/GVR passthrough
- backend-controlled strings are HTML escaped before rendering
- static runtime emits CSP, `nosniff`, no-referrer and restrictive Permissions-Policy headers
- Web container runs as non-root UID/GID 101

The Portal displays an explicit `READ ONLY` product-state marker. This is informational; the real enforcement remains Backend contract + Kubernetes RBAC.

## Technology Choice

Phase 1.3 uses a small TypeScript SPA without a UI framework. Rationale:

- minimizes third-party runtime dependencies
- keeps the generated Phase 1.2 TypeScript Client as the center of the data layer
- reduces supply-chain and bundle complexity for a read-only operational console
- supports deterministic static deployment behind the same origin as Portal Backend

Build: esbuild. Typecheck: TypeScript. Lint: Biome. Unit tests: Node test runner. Browser E2E: headless Chrome/Chromium against a contract-shaped mock server.

## CI / Quality Gates

The repository-level gate must include:

```text
web lint
    ↓
web typecheck
    ↓
web unit tests
    ↓
web production build
    ↓
web Docker smoke
    ↓
browser E2E
```

Existing Phase 1.1/1.2 gates remain mandatory and unchanged in meaning:

- Secret Scan
- Preflight / Lint / RBAC
- Kubernetes v1.36 Kind E2E
- OpenAPI Contract Gate
- Go backend checks
- backend Docker smoke

## Browser E2E Scope

The first browser gate verifies:

- List renders normalized findings.
- Severity filtering changes the rendered Finding set.
- Detail route renders the selected Finding.
- UI interacts only with contract-shaped read-only endpoints.

Unit tests additionally enforce explicit Loading / Empty / Error / Retry rendering and HTML escaping.

## Acceptance Criteria

Phase 1.3 is not considered complete merely because code exists on the feature branch.

Acceptance sequence:

```text
Implementation complete
        ↓
Frontend lint/typecheck/test/build PASS
        ↓
Frontend Docker smoke PASS
        ↓
Frontend browser E2E PASS
        ↓
Existing OpenAPI / backend / security / Kubernetes gates PASS
        ↓
PR Required Checks all green
        ↓
Merge to main
        ↓
main Required Checks all green again
        ↓
Phase 1.3 completion review
```

Functional acceptance:

- Finding List: PASS
- Finding Detail: PASS
- Finding Summary: PASS
- Cluster filter: PASS
- Namespace filter: PASS
- Severity filter: PASS
- Kind filter: PASS
- Loading state: PASS
- Empty state: PASS
- Error state: PASS
- Retry state: PASS
- Generated TypeScript Client only: PASS
- No handwritten API DTO: PASS
- No Pod Logs / Secret / Raw Kubernetes / Raw Result CR: PASS
- No Mutation / Auto Remediation: PASS

## Non-Goals

Phase 1.3 does not implement:

- authentication/SSO redesign
- chat UI
- log browsing
- Secret browsing
- raw YAML/JSON explorer
- arbitrary Kubernetes resource explorer
- remediation execution
- mutation workflows
- Prometheus/Loki RCA correlation
- alert/event ingestion

Those require separate security and architecture phases.
