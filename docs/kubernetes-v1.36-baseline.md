# kube-aiops Kubernetes v1.36 Platform Baseline

Kubernetes v1.36.4 是 kube-aiops 的部署验证基线。版本配置位于 `config/platform-versions.env`，使用 `make platform-check` 检查版本、镜像引用和 API 使用面。初次激活的验收记录见 [v1.36 基线激活记录](../docx/kubernetes-v1.36-activation.md)。

## 支持版本矩阵

| 组件 | 旧基线 | Kubernetes v1.36 基线 | 说明 |
|---|---:|---:|---|
| Kubernetes | v1.34.8 | **v1.36.4** | 目标集群部署验证基线 |
| kubeconform schema | 1.34.0 | **1.36.0** | 所有提交给 API Server 的内置资源严格 Schema 校验 |
| Go language baseline | 1.24 | **1.26.0** | `go.mod` language version |
| Go toolchain | 1.24.x | **1.26.5** | 与 Kubernetes release-1.36 `.go-version` 对齐 |
| K8sGPT Operator | v0.2.29 | **v0.2.29** | 当前稳定版本保持不变，镜像 digest 固定 |
| K8sGPT Engine | v0.4.37 | **v0.4.41** | 固定 GHCR 多架构镜像 digest |

K8sGPT Engine：

```text
ghcr.io/k8sgpt-ai/k8sgpt:v0.4.41@sha256:1bc581442ac9c3a9e2f9a2f818c2bd84ba476b445d46fcb168073799caa58640
```

K8sGPT Operator：

```text
ghcr.io/k8sgpt-ai/k8sgpt-operator:v0.2.29@sha256:82d0adcce816182bbfbef9f1d535db93ab0901f08d45a3a9e572c6e795c5bfa8
```

## Kubernetes v1.36 API 支持边界

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

## CI 与集群验收

仓库 CI 运行 `Preflight / Lint / RBAC` 和 `Secret Scan`。预检覆盖 Kubernetes 1.36 清单 Schema、API 契约、Go 与 Web 测试、浏览器测试和容器冒烟测试。

在目标集群验证 API discovery、Operator 和 Engine 就绪、只读 RBAC、Provider 分析结果及 Portal 访问。生命周期和回滚场景在现有测试集群验收。操作步骤见 [安装部署指南](install-deployment.md)。

## 安全性校验

升级必须满足：

- Secret 仍然 DENY；
- `pods/log` 仍然 DENY；
- Kubernetes `create/update/patch/delete/deletecollection` 仍然 DENY；
- K8sGPT Engine 和 Operator 默认上游镜像使用 digest 固定；
- GitHub Actions 使用 40 位 commit SHA 固定；
- K8sGPT `anonymized: true` 保持；
- Engine/Operator 容器保持 non-root、禁止 privilege escalation、read-only root filesystem、drop ALL capabilities；
- Gitleaks 全历史扫描继续为 Required Check；
- 升级不得扩大现有 RBAC exact-set。
