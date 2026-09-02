# kube-aiops Kubernetes v1.36 Platform Baseline

## 1. 目标

本基线在 Phase 1.2 Portal Backend 完成后、Phase 1.3 Web Portal 开始前执行一次平台升级，把 kube-aiops 的受支持 Kubernetes 验证环境从 v1.34 提升到 v1.36。

升级不是只修改 CI Job 名称，而是同时收敛：

- Kubernetes API Server / kubectl / kubelet 验证版本；
- Kind 版本与不可变 node image；
- kubeconform Kubernetes Schema 基线；
- Go 编译工具链；
- K8sGPT Operator / Engine 运行时版本；
- Kubernetes built-in API 使用面；
- CI Required Check 名称；
- 安全、健壮性和逻辑一致性门禁。

统一版本源：`config/platform-versions.env`。

## 2. 支持版本矩阵

| 组件 | 旧基线 | Kubernetes v1.36 基线 | 说明 |
|---|---:|---:|---|
| Kubernetes | v1.34.8 | **v1.36.4** | Kind/API Server/kubectl/kubelet 统一验证 |
| Kind | 旧 workflow action 默认 | **v0.33.0** | 显式固定 Kind CLI 版本 |
| Kind node image | v1.34.8 digest | **v1.36.4 digest** | `tag@sha256` 不可变引用 |
| kubeconform schema | 1.34.0 | **1.36.0** | 所有提交给 API Server 的内置资源严格 Schema 校验 |
| Go language baseline | 1.24 | **1.26.0** | `go.mod` language version |
| Go toolchain | 1.24.x | **1.26.5** | 与 Kubernetes release-1.36 `.go-version` 对齐 |
| K8sGPT Operator | v0.2.29 | **v0.2.29** | 当前稳定版本保持不变，镜像 digest 固定 |
| K8sGPT Engine | v0.4.32 | **v0.4.37** | 升级到已独立确认语义 tag digest 的稳定镜像 |

Kubernetes v1.36.4 Kind node image：

```text
kindest/node:v1.36.4@sha256:099e049362a1526b2db71494e1947aae99bd16290d7c895f2b7ea312e3cbfaed
```

K8sGPT Engine：

```text
ghcr.io/k8sgpt-ai/k8sgpt:v0.4.37@sha256:ebf0d1f5a8463190abdf1a9c84282cfbd9ce611e3dc1c490194b7aaf2676d088
```

K8sGPT Operator：

```text
ghcr.io/k8sgpt-ai/k8sgpt-operator:v0.2.29@sha256:82d0adcce816182bbfbef9f1d535db93ab0901f08d45a3a9e572c6e795c5bfa8
```

### 为什么 Engine 不直接使用 v0.4.38

升级时上游最新源码 release 已为 v0.4.38，但本项目的生产约束要求运行时镜像必须使用不可变 `tag@sha256`。在本次基线形成时，能够从独立语义 tag 元数据确认的最新稳定镜像为 v0.4.37，因此选择 v0.4.37，而不是退化为仅使用可变 tag。

后续只有在 v0.4.38 或更高版本的语义 tag digest 可独立验证、并通过本仓库完整 v1.36 E2E 后，才允许提升该版本。

## 3. Kubernetes v1.36 API 支持边界

Portal Backend 当前没有引入 `client-go`，而是通过受限的 in-cluster HTTP client 调用 Kubernetes API。支持 v1.36 的判断因此不能简化成“把 k8s.io/client-go 改成 v0.36”。

当前依赖的 Kubernetes built-in API 均为稳定版本：

```text
/api/v1
/apis/apps/v1
/apis/rbac.authorization.k8s.io/v1
/apis/batch/v1
/apis/networking.k8s.io/v1
/apis/autoscaling/v2
```

K8sGPT Result 使用：

```text
/apis/core.k8sgpt.ai/v1alpha1
```

这是 K8sGPT 自身 CRD API，不是 Kubernetes 已废弃的 built-in beta API。

CI 的 `tests/platform-baseline-test.py` 会拒绝以下已移除 built-in API 重新进入 manifests/backend：

- `extensions/v1beta1`；
- `apps/v1beta1` / `apps/v1beta2`；
- `networking.k8s.io/v1beta1`；
- `policy/v1beta1`；
- `batch/v1beta1`；
- `autoscaling/v2beta1` / `autoscaling/v2beta2`；
- `admissionregistration.k8s.io/v1beta1`；
- `apiextensions.k8s.io/v1beta1`。

## 4. CI / E2E 基线

Required Check 名称升级为：

```text
Kubernetes v1.36 Kind E2E
```

CI 固定：

```text
Kind:       v0.33.0
kubectl:    v1.36.4
API Server: v1.36.4
kubelet:    v1.36.4
```

在完整生命周期 E2E 前先运行 `tests/kubernetes-v1.36-smoke.sh`，它必须验证：

1. Kind CLI 版本精确等于 v0.33.0；
2. kubectl client 精确等于 v1.36.4；
3. Kind API Server 精确等于 v1.36.4；
4. Node kubelet 精确等于 v1.36.4；
5. kube-aiops 依赖的 stable API discovery 全部可用。

随后继续运行原有高价值回归：

- install / reinstall 幂等；
- Helm rollback；
- rollback 二次失败状态保留；
- Lease CAS / heartbeat / concurrency；
- trusted uninstall；
- RBAC ownership；
- Phase 1.2 readonly API E2E。

## 5. 安全性校验

升级必须满足：

- Secret 仍然 DENY；
- `pods/log` 仍然 DENY；
- Kubernetes `create/update/patch/delete/deletecollection` 仍然 DENY；
- K8sGPT Engine / Operator / Kind node 使用 digest 固定；
- GitHub Actions 使用 40 位 commit SHA 固定；
- K8sGPT `anonymized: true` 保持；
- Engine/Operator 容器保持 non-root、禁止 privilege escalation、read-only root filesystem、drop ALL capabilities；
- Gitleaks 全历史扫描继续为 Required Check；
- 升级不得扩大现有 RBAC exact-set。

## 6. 代码健壮性校验

必须通过：

```text
Shell syntax
ShellCheck
YAML lint
kubeconform Kubernetes 1.36 schema
platform baseline consistency
OpenAPI Contract Gate
Go fmt / vet / test / build (Go 1.26.5)
Docker backend smoke
project preflight
Kubernetes v1.36 runtime API smoke
lifecycle / rollback / concurrency E2E
Phase 1.2 readonly API E2E
```

任何一个失败均不得以跳过、allow-failure 或降低 Ruleset 的方式合并。

## 7. 逻辑严谨性校验

`tests/platform-baseline-test.py` 将以下重复配置视为一个一致性事务：

```text
config/platform-versions.env
        ↓
.github/workflows/ci.yml
.github/workflows/provider-e2e.yml
        ↓
go.mod / Dockerfile
        ↓
preflight.sh kubeconform schema
        ↓
deploy/k8sgpt/*
        ↓
internal/kubernetes/client.go API surface
```

如果只修改其中一处，CI 必须失败。这避免：

- Job 名称升级但实际 node 仍是旧版本；
- kubectl 与 API Server minor version 不一致；
- Go CI 与 Docker builder 工具链漂移；
- K8sGPT 文档版本与实际部署镜像漂移；
- Kubernetes Manifest 又引入已删除 API；
- Provider E2E 与 Required E2E 使用不同 Kubernetes 基线。

## 8. Phase 1.2 历史证据处理

`docx/phase-1.2-completion.md` 中 run #45 的 Kubernetes v1.34 E2E 是 Phase 1.2 当时真实发生的历史验收证据，不应改写为 v1.36。

本升级完成并合并后，文档会增加“当前平台基线已由 v1.34 supersede 为 v1.36.4”的说明，但保留原始 run #45 记录，确保审计可追溯。

## 9. 验收门禁

```text
platform/kubernetes-v1.36-baseline
        ↓
静态三维校验 PASS
        ↓
PR: Secret Scan PASS
        ↓
PR: Preflight / Lint / RBAC PASS
        ↓
PR: Kubernetes v1.36 Kind E2E PASS
        ↓
Ruleset Required Check 切换到 v1.36 名称
        ↓
Merge to main
        ↓
main 三项 Required Checks 再次全绿
        ↓
Kubernetes v1.36 platform baseline = Active
        ↓
进入 Phase 1.3 Web Portal
```

在最后一项 `main` E2E 全绿之前，不把 v1.36 基线标记为 Active。
