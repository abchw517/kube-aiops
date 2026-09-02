# kube-aiops

基于 K8sGPT 构建的 Kubernetes AIOps 平台，提供安全只读的 AI 辅助诊断、根因分析、可观测性关联与 Web Portal 能力。

## 当前阶段

- **Phase 1.1：K8sGPT Engine — Completed**
- **Phase 1.2：Portal Backend API — Completed**
- **Platform Baseline：Kubernetes v1.36 升级与封版校验**
- **Phase 1.3：Web Portal — 等待 v1.36 平台基线合并后 main 全绿再进入**

Phase 1.2 的完成记录见 `docx/phase-1.2-completion.md`。当前平台基线说明见
`docs/kubernetes-v1.36-baseline.md`。

## 当前支持基线

```text
Kubernetes:       v1.36.4
Kind:             v0.33.0
kubectl:          v1.36.4
Go language:      1.26.0
Go toolchain:     1.26.5
K8sGPT Operator:  v0.2.29
K8sGPT Engine:    v0.4.37
```

统一版本源：

```text
config/platform-versions.env
```

Kubernetes、K8sGPT Engine、K8sGPT Operator 等运行时镜像使用 `tag@sha256`
不可变引用；GitHub Actions 使用完整 commit SHA 固定。平台基线一致性由
`tests/platform-baseline-test.py` 强制校验。

## 安全边界

当前阶段继续保持只读 AIOps 边界：

- 使用固定 `k8sgpt` ServiceAccount 承载 Engine 权限；
- 业务资源只允许 `get/list/watch`；
- Secret 读取禁止；
- `pods/log` 读取禁止；
- `create/update/patch/delete/deletecollection` 禁止；
- Mutation / Auto Remediation 禁用；
- Namespace egress 仅允许 DNS、HTTPS 和 Kubernetes API 端口；
- AI 分析默认启用匿名化；
- Result CR 作为 K8sGPT 与 Portal Backend 的结构化边界；
- Raw Result CR / Raw Kubernetes Object 不通过 Portal API 暴露。

## 架构

```text
User / SRE
    |
    v
Phase 1.3 Web Portal
    |
    v
Phase 1.2 Portal Backend API
    |
    +-------------------------------+
    |                               |
    v                               v
Kubernetes API                 Finding Model
    |                               ^
    |                               |
    +--> K8sGPT Result CR -----------+
            ^
            |
      K8sGPT Engine
            ^
            |
      K8sGPT Operator
```

K8sGPT Engine 仍执行：

```text
Kubernetes Resource
        ↓
Analyzer
        ↓
Anonymization
        ↓
AI Provider
        ↓
Result CR
```

## 目录结构

```text
.
├── api/
│   └── openapi.yaml
├── clients/
│   └── typescript/generated.ts
├── cmd/api/
├── config/
│   └── platform-versions.env
├── internal/
├── Makefile
├── preflight.sh
├── install.sh
├── verify.sh
├── uninstall.sh
├── Dockerfile
├── deploy/
│   └── k8sgpt/
├── tests/
│   ├── platform-baseline-test.py
│   ├── kubernetes-v1.36-smoke.sh
│   ├── e2e-kind.sh
│   └── e2e-api-kind.sh
├── docs/
│   ├── kubernetes-v1.36-baseline.md
│   ├── architecture.md
│   └── security.md
└── .github/workflows/
    ├── ci.yml
    └── provider-e2e.yml
```

# 快速开始

## 0. 平台基线与 Preflight

先验证版本配置、Kubernetes API 使用面、Go/K8sGPT/CI 是否一致：

```bash
make platform-check
```

再执行完整静态 Preflight：

```bash
make preflight
```

Preflight 包括：

- 所有 Shell 脚本 `bash -n`；
- ShellCheck；
- 所有 YAML/YML 语法解析；
- Kubernetes **v1.36** kubeconform Schema 校验；
- 高置信度 Secret/Token/Private Key 扫描；
- RBAC 静态安全检查；
- Python 语法检查；
- Kubernetes v1.36 平台版本一致性检查；
- Secret / Pod Logs / write verbs 等安全控制回归测试。

需要真实验证 Kubernetes v1.36.4 API Server 时：

```bash
make platform-smoke
```

该命令会创建临时 Kind 集群并精确验证：

```text
Kind CLI    = v0.33.0
kubectl     = v1.36.4
API Server  = v1.36.4
kubelet     = v1.36.4
```

同时探测 kube-aiops 依赖的 stable API：

```text
/api/v1
/apis/apps/v1
/apis/rbac.authorization.k8s.io/v1
/apis/batch/v1
/apis/networking.k8s.io/v1
/apis/autoscaling/v2
```

## 1. 创建 AI Provider Secret

Secret 不提交到 Git：

```bash
export OPENAI_TOKEN='replace-me'
make bootstrap-secret
```

固定 Namespace：

```text
k8sgpt-operator-system
```

Phase 1.1/1.2 不支持通过参数任意覆盖 Namespace、Release、ServiceAccount、
Secret 或 K8sGPT CR 名称，避免身份与 RBAC 基线漂移。

## 2. 安装 K8sGPT

```bash
make install
```

安装链路：

```text
检查 kubectl / helm / current-context
   ↓
检查 Namespace 与 AI Secret（不满足时不修改集群）
   ↓
获取 Kubernetes Lease 并记录升级前 revision / 资源快照
   ↓
使用 --atomic / --cleanup-on-fail 安装或升级 Operator
   ↓
Helm post-renderer 在资源进入 API Server 前收敛上游宽权限 RBAC
   ↓
Server-Side Apply 复验 RBAC 安全基线
   ↓
验证 Secret / Pod Log / Patch / Delete 权限均被拒绝
   ↓
部署 K8sGPT CR
   ↓
严格检查 Operator 与 K8sGPT Engine Ready
```

首次安装的后置步骤失败时会卸载本次 Release 并恢复安装前快照；已有 Release
发生 Helm 外后置失败时执行 `helm rollback` 并恢复 RBAC、ServiceAccount 和
K8sGPT CR 快照。Lease 覆盖安装/卸载全生命周期，heartbeat 续租并在变更点
执行 fencing 检查。回滚二次失败时保留 `0700` 取证目录和对象级恢复报告。

当前 K8sGPT 版本：

```text
K8sGPT Operator: v0.2.29
K8sGPT Engine:   v0.4.37
```

Operator 镜像：

```text
ghcr.io/k8sgpt-ai/k8sgpt-operator:v0.2.29@sha256:82d0adcce816182bbfbef9f1d535db93ab0901f08d45a3a9e572c6e795c5bfa8
```

Engine 镜像：

```text
ghcr.io/k8sgpt-ai/k8sgpt:v0.4.37@sha256:ebf0d1f5a8463190abdf1a9c84282cfbd9ce611e3dc1c490194b7aaf2676d088
```

本基线没有直接使用仅能确认源码 release、但无法独立确认语义 tag digest 的
更新 Engine tag。运行时镜像版本提升必须同时具备可信 digest 并通过完整
Kubernetes v1.36 E2E。

Operator 版本门禁：

```bash
OPERATOR_VERSION=0.2.29 make install
```

Legacy RBAC 首次迁移必须显式执行：

```bash
MIGRATE_LEGACY_OWNERSHIP=true make install
```

安装器会先检查 rules、`roleRef`、`subjects` 与所有权语义指纹，匹配后才迁移。

## 3. 安全与功能验收

```bash
make verify
```

核心验收：

- Kubernetes API 连通性；
- Helm Release 必须为 `deployed` 且 Chart 身份正确；
- K8sGPT / Result / Mutation CRD；
- ServiceAccount、ClusterRole/Binding、NetworkPolicy 与仓库 exact-set 基线一致；
- 业务资源 `get/list/watch`；
- Secret 读取必须为 `no`；
- Pod Log 读取必须为 `no`；
- create/update/patch/delete 必须为 `no`；
- `anonymized=true`；
- backend、分析周期、Secret 引用正确；
- Log Analyzer 未启用；
- Auto Remediation 未启用；
- Operator / Engine observedGeneration 收敛且完整 Ready；
- Result CR 查询错误不得降级为零结果。

严格 Result 模式：

```bash
STRICT_RESULTS=true make verify
```

## 4. 故障样例

```bash
make demo
```

覆盖：

```text
ImagePullBackOff
CrashLoopBackOff
PVC Pending
```

查看：

```bash
kubectl get pod,pvc -n k8sgpt-demo
make results
```

清理：

```bash
make clean-demo
```

## 5. 状态与卸载

```bash
make status
make uninstall
```

默认卸载策略：

```text
K8sGPT CR        删除
Helm Release     删除
Phase 1.1 RBAC   删除
AI Secret        保留
Namespace        保留
CRD              保留
```

可选：

```bash
PURGE_SECRET=true make uninstall
PURGE_SECRET=true PURGE_NAMESPACE=true make uninstall
PURGE_DEMO=true make uninstall
```

CRD 默认不自动删除，避免误伤其它实例或后续重新安装。

# Kubernetes v1.36 Kind E2E

```bash
make e2e
```

支持命令会从 `config/platform-versions.env` 注入固定基线：

```text
Kind:       v0.33.0
kubectl:    v1.36.4
Node image: kindest/node:v1.36.4@sha256:099e049362a1526b2db71494e1947aae99bd16290d7c895f2b7ea312e3cbfaed
```

Required E2E 在生命周期测试前先执行精确版本/API smoke，然后继续验证：

- 前置条件失败时零 Helm 变更；
- 外部/伪造身份同名 RBAC 拒绝接管；
- legacy 指纹迁移；
- 同名外部 Helm Release 拒绝操作；
- 首次安装与重复安装幂等；
- Helm 后置故障 rollback；
- rollback 二次失败取证状态保留；
- Lease heartbeat / fencing / CAS；
- Forbidden 查询不得假成功；
- trusted uninstall；
- Phase 1.2 readonly API E2E。

真实 Provider 功能 E2E 使用同一 Kubernetes v1.36.4 基线，通过手工工作流执行：

```bash
export OPENAI_TOKEN='restricted-test-token'
make e2e-provider
```

GitHub Environment：`phase-1.1-functional`；Secret：`OPENAI_E2E_TOKEN`。

# Phase 1.2 Portal Backend

Phase 1.2 已完成，提供只读 API：

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

OpenAPI 3.1 是 Portal 外部契约基线：

```bash
make api-contract-validate
make api-client-generate
make api-client-drift-check
make api-contract-check
```

CI 会检查：

- Server route ↔ OpenAPI route drift；
- Go DTO ↔ OpenAPI Schema drift；
- `omitempty` ↔ `required` drift；
- Severity enum drift；
- Sensitive/raw field guard；
- TypeScript generated client byte-for-byte drift。

Portal Backend 不使用 `client-go`，而使用受限 in-cluster HTTP client。当前 Kubernetes
built-in API 只依赖稳定的 `/api/v1` 和 `/apis/apps/v1`；K8sGPT Result 使用其自身
`core.k8sgpt.ai/v1alpha1` CRD API。平台一致性测试禁止已从 Kubernetes 删除的
built-in beta API 重新进入代码或 manifests。

# Go v1.36 对齐基线

当前 `go.mod`：

```go
module github.com/abchw517/kube-aiops

go 1.26.0

toolchain go1.26.5
```

Docker builder：

```text
golang:1.26.5-alpine3.23
```

该工具链与 Kubernetes `release-1.36` 的 Go toolchain 基线对齐。Go 升级仍必须通过：

```text
gofmt
vet
test
build
Docker smoke
```

# GitHub Actions CI

工作流：`.github/workflows/ci.yml`。

Required Checks：

```text
Preflight / Lint / RBAC
Secret Scan
Kubernetes v1.36 Kind E2E
```

其中：

```text
Preflight / Lint / RBAC
├── bash -n / ShellCheck
├── YAML lint
├── Kubernetes v1.36 kubeconform
├── Platform baseline consistency
├── OpenAPI Contract Gate
├── Go fmt / vet / test / build
├── Docker backend smoke
└── project preflight

Secret Scan
└── Gitleaks full history

Kubernetes v1.36 Kind E2E
├── Kind v0.33.0 / kubectl v1.36.4
├── API Server / kubelet exact v1.36.4 smoke
├── stable API discovery
├── lifecycle / rollback / concurrency / trusted uninstall
└── Phase 1.2 readonly API E2E
```

安全设计：

- Workflow 默认仅 `contents: read`；
- 第三方 GitHub Actions 固定到完整 commit SHA；
- Kind node / K8sGPT runtime 镜像固定 sha256 digest；
- Checkout 完整历史用于 Gitleaks；
- Provider E2E 仅 `main` 可请求 Environment Secret；
- AI Secret 通过 stdin 创建，不进入 `kubectl` argv；
- RBAC 出现 `*`、Secret、Pod Logs、危险写 verbs、外部 RoleRef 或宽泛 Group 时 CI 失败；
- 不通过 allow-failure 或放宽 Ruleset 绕过平台升级失败。

`main` Ruleset 应要求：

```text
Preflight / Lint / RBAC
Secret Scan
Kubernetes v1.36 Kind E2E
```

# RBAC Hardening

Operator `v0.2.29` 显式关闭 dynamicRBAC：

```yaml
dynamicRBAC:
  enabled: false
```

固定身份：

```text
ServiceAccount:     k8sgpt
ClusterRole:        k8sgpt-clusterrole
ClusterRoleBinding: k8sgpt-clusterrole-binding
```

Helm post-renderer 在对象进入 API Server 前替换上游静态宽权限，安装后再通过
Server-Side Apply 与 `kubectl auth can-i` 验证。Engine Role 移除：

```text
secrets
pods/log
create
update
patch
delete
deletecollection
```

ClusterRole / ClusterRoleBinding 必须同时具备精确所有权标识：

```text
kube-aiops.io/owner=phase-1.1
app.kubernetes.io/part-of=kube-aiops
app.kubernetes.io/instance=k8sgpt-operator
```

# Makefile 常用命令

```bash
make help
make platform-check
make platform-smoke
make preflight
make bootstrap-secret
make install
make verify
make demo
make clean-demo
make status
make results
make uninstall
make e2e
make e2e-provider
make api-contract-check
```

# 安全原则

```text
Detect      -> Allowed
Explain     -> Allowed
Recommend   -> Allowed
Result CR   -> Allowed

Secret Read -> Denied
Pod Logs    -> Denied
Patch       -> Denied
Update      -> Denied
Delete      -> Denied
Mutation    -> Disabled
Remediation -> Disabled
```

详细设计：

- `docs/kubernetes-v1.36-baseline.md`
- `docs/architecture.md`
- `docs/security.md`
- `docs/phase-1.1-acceptance.md`
- `docs/phase-1.1-hardening.md`
- `docx/phase-1.2-completion.md`

# Roadmap

- Phase 1.1：K8sGPT Engine — Completed
- Phase 1.2：Portal Backend API — Completed
- Platform Baseline：Kubernetes v1.36 — current gate
- Phase 1.3：Web Portal
- Phase 1.4：Authentication / Authorization / Audit / Sanitizer
- Phase 2：Events + Prometheus + Loki + Alertmanager 关联分析
- Phase 3：RCA Agent + Runbook
- Phase 4：HITL Remediation
- Phase 5：Controlled Auto Remediation
