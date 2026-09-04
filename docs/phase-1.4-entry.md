# Phase 1.4 Entry

Phase 1.4 is the active security-hardening phase after Phase 1.3 Web Portal completed on a green `main` baseline.

Active scope: **Authentication / Authorization / Audit / Sanitizer / Production Gates**.

## Stage Status

- Phase 1.4.1 — Identity Contract: **Completed**
  - implementation PR: #18
  - completion baseline: `main@7b13b250f035657a1edf24e9590a6560488186bf`
  - post-merge CI #63: all Required Checks green
- Phase 1.4.2 — Authorization: **Completed**
  - completion baseline: `main@8186bfeff2682677b468ebc397cf248afd2f3213`
  - post-merge CI #70: all Required Checks green
- Phase 1.4.3 — Audit: **Completed**
  - implementation PR: #22
  - completion baseline: `main@10e152dd54b0f98d77bd0595e54c4b021d7679f8`
  - post-merge CI #76: all Required Checks green
- Phase 1.4.4 — Sanitizer: **Completed**
  - implementation PR: #24
  - completion baseline: `main@ad1845c97ec783e80763655fd94227e228661871`
  - post-merge CI #81: all Required Checks green
- Phase 1.4.5 — Production Gates: **Entering**
  - implementation must start from the final green `main` produced after this completion/entry transition is merged and revalidated

## Current Production-Gate Focus

The Phase 1.4 security components are implemented and individually proven, but the real `cmd/api` production composition still uses the compatibility `httpapi.NewHandler(...)` constructor. That constructor intentionally preserves the Phase 1.3 unauthenticated runtime until a trusted AuthN provider is wired.

Phase 1.4.5 therefore must prove that a production deployment cannot silently start with AuthN/AuthZ/Audit omitted. Missing trusted security components must fail closed. The stage must not introduce fake static browser tokens, unsigned `X-User` / `X-Groups` trust, Kubernetes user-token proxying, or any other shortcut that weakens the established identity boundary.

The read-only Kubernetes security boundary remains unchanged. Phase 2 Events / Prometheus / Loki / Alertmanager correlation is explicitly out of scope for Phase 1.4.
