# Kubernetes v1.36 Platform Baseline Activation Record

## Status

**Active**

This record captures the activation evidence for the kube-aiops Kubernetes v1.36 platform baseline.

## Activated Baseline

```text
Kubernetes / kubectl / kubelet: v1.36.4
Kind:                         v0.33.0
kubeconform schema:           1.36.0
Go language:                  1.26.0
Go toolchain:                 1.26.5
K8sGPT Operator:              v0.2.29
K8sGPT Engine:                v0.4.37
```

Runtime images remain pinned with immutable `tag@sha256` references.

## Upgrade Evidence

- Upgrade PR: `#14` — `build: upgrade platform baseline to Kubernetes v1.36`
- PR head validated by CI run `#48` (`33586060152`)
- PR Required Checks:
  - `Preflight / Lint / RBAC`: PASS
  - `Secret Scan`: PASS
  - `Kubernetes v1.36 Kind E2E`: PASS
- PR merge commit: `f7e785b6f1d06c9cde2a49407ce49fc03381f941`
- Post-merge `main` CI run: `#49` (`33587446292`)
- Post-merge `main` Required Checks:
  - `Preflight / Lint / RBAC`: PASS
  - `Secret Scan`: PASS
  - `Kubernetes v1.36 Kind E2E`: PASS

## Ruleset Evidence

Repository Ruleset: `main-protection` (`21645143`)

Required Checks after migration:

```text
Preflight / Lint / RBAC
Secret Scan
Kubernetes v1.36 Kind E2E
```

`strict_required_status_checks_policy` remains enabled. The obsolete `Kubernetes v1.34 Kind E2E` context is no longer required.

## Runtime Validation Evidence

The Kubernetes v1.36 E2E gate verified:

- Kind CLI `v0.33.0`;
- kubectl client `v1.36.4`;
- Kubernetes API Server `v1.36.4`;
- kubelet `v1.36.4`;
- stable Kubernetes API discovery required by kube-aiops;
- lifecycle / reinstall idempotency;
- rollback / rollback-failure evidence retention;
- Lease heartbeat / fencing / CAS / concurrency;
- trusted uninstall;
- Phase 1.2 readonly API E2E;
- existing RBAC DENY boundaries for Secret, Pod logs and mutation operations.

## Security and Compatibility Decision

The baseline remains read-only and advisory. This activation does not introduce Kubernetes mutation capability, Secret access, Pod log access, Mutation CR enablement, or Auto Remediation.

K8sGPT Engine is pinned to `v0.4.37` because its semantic tag digest was independently verifiable at baseline formation time. A later Engine upgrade must preserve immutable digest pinning and pass the full Kubernetes v1.36 gate before adoption.

## Phase 1.3 Entry

The Kubernetes v1.36 platform baseline is now **Active**. Phase 1.3 Web Portal may start from the green `main` baseline at or after merge commit `f7e785b6f1d06c9cde2a49407ce49fc03381f941`, and must consume the existing Phase 1.2 OpenAPI/generated TypeScript contract rather than creating a second handwritten API model.
