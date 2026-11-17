# Phase 1.4 Security Boundary

Phase 1.4 adds identity, authorization, audit and sanitization controls without expanding the Kubernetes privilege surface.

Mandatory invariants:

- Secret read remains denied.
- `pods/log` remains denied.
- create/update/patch/delete/deletecollection remain denied.
- Mutation and Auto Remediation remain disabled.
- Raw Kubernetes objects remain outside the Portal contract.
- Raw K8sGPT Result CR payloads remain outside the Portal contract.
- Authorization is enforced server-side and defaults to deny.
- Audit events must not contain credentials or sensitive/raw payloads.
- Sanitization is defense in depth and must not replace the OpenAPI allowlist boundary.
