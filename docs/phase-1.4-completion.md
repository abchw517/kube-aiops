# Phase 1.4 Completion — Authentication / Authorization / Audit / Sanitizer / Production Gates

## Status

**Completed.**

Phase 1.4 is complete on the verified green runtime baseline:

```text
main@f3e794784fea1986f731191e4be4aa3153fe292d
```

Completion evidence:

- Phase 1.4.5 implementation PR: `#26 feat: Phase 1.4.5 Production Gates`
- PR head validated by CI: `c2fd13c0f26b8830ad1764e833bfed826c926d80`
- merged main baseline: `f3e794784fea1986f731191e4be4aa3153fe292d`
- post-merge workflow: `kube-aiops CI #87`
- workflow result: `completed / success`
- Required Checks:
  - `Preflight / Lint / RBAC` — success
  - `Secret Scan` — success
  - `Kubernetes v1.36 Kind E2E` — success
- Kind validation:
  - Kubernetes `v1.36.4` platform baseline — success
  - lifecycle / rollback / concurrency / trusted uninstall E2E — success
  - Phase 1.2 read-only API E2E — success

## Completed Stages

| Stage | Result | Completion evidence |
| --- | --- | --- |
| Phase 1.4.1 Identity Contract | Completed | `main@7b13b250f035657a1edf24e9590a6560488186bf`, CI #63 |
| Phase 1.4.2 Authorization | Completed | `main@8186bfeff2682677b468ebc397cf248afd2f3213`, CI #70 |
| Phase 1.4.3 Audit | Completed | `main@10e152dd54b0f98d77bd0595e54c4b021d7679f8`, CI #76 |
| Phase 1.4.4 Sanitizer | Completed | `main@ad1845c97ec783e80763655fd94227e228661871`, CI #81 |
| Phase 1.4.5 Production Gates | Completed | `main@f3e794784fea1986f731191e4be4aa3153fe292d`, CI #87 |

## Security Properties Now Enforced

The Portal security chain is now explicitly composed as:

```text
Request / Correlation Metadata
        ↓
Structured Audit Recorder
        ↓
Trusted Authentication contract
        ↓
Deny-by-default Authorization
        ↓
Read-only Backend handlers
        ↓
Typed safe projection
        ↓
Typed Sanitizer
        ↓
JSON response
```

Production startup is fail closed:

```text
SECURITY_MODE=production
        ↓
SecurityBundle.ValidateForProduction()
        ├── Authenticator required
        ├── Authorizer required
        ├── AuditSink required
        └── Sanitizer required
        ↓
missing / invalid component
        ↓
process startup fails before protected traffic is served
```

Development compatibility is explicit rather than implicit. The production path does not silently fall back to the Phase 1.3 compatibility constructor.

## Important Authentication Reality

Phase 1.4 intentionally remains provider-neutral. The repository does not invent a fake production identity provider simply to make `SECURITY_MODE=production` start successfully.

Until a trusted enterprise Authenticator is wired, production mode is intentionally unavailable and fails closed. This is the expected security property, not an incomplete fallback to anonymous access.

Forbidden shortcuts remain forbidden:

- static browser bearer tokens
- unsigned `X-User` / `X-Groups`
- browser kubeconfig or Kubernetes user-token proxying
- allow-all production Authenticator
- frontend-only authorization

## Read-only Kubernetes Boundary

Phase 1.4 completed without expanding the existing sensitive-data or mutation surface:

- Secret reads remain denied
- `pods/log` remains denied
- create/update/patch/delete/deletecollection remain denied
- raw Kubernetes object passthrough remains forbidden
- raw K8sGPT Result CR passthrough remains forbidden
- Mutation remains disabled
- Auto Remediation remains disabled

## Production Regression Gates

The repository now continuously verifies:

- API Contract Gate
- generated TypeScript client drift
- Go format/vet/tests/build
- Web lint/typecheck/tests/build
- production Docker fail-closed startup
- explicit development Docker compatibility smoke
- browser 401/403/XSS regressions
- protected ServeMux route coverage
- AuthN → AuthZ → handler → Sanitizer → Audit integration
- Secret Scan / Gitleaks
- Kubernetes v1.36.4 Kind baseline
- negative RBAC assertions including create/update/patch/delete/deletecollection
- lifecycle / rollback / concurrency / trusted uninstall
- Phase 1.2 read-only API E2E

Required Check names remain governance contracts:

1. `Preflight / Lint / RBAC`
2. `Secret Scan`
3. `Kubernetes v1.36 Kind E2E`

## Next Phase

The next phase is **Phase 2 — Observability Correlation**:

```text
Kubernetes Events
+ Prometheus
+ Loki
+ Alertmanager
        ↓
normalized bounded evidence
        ↓
Finding / resource correlation
        ↓
Correlation Bundle
```

Phase 2 does not introduce RCA Agent, Runbook execution, remediation or automatic mutation. Those remain later phases.