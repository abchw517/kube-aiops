# kube-aiops

基于 K8sGPT 构建的 Kubernetes AIOps 平台，提供安全只读的 AI 辅助诊断、根因分析、可观测性关联与 Web Portal 能力。

## 当前阶段

当前仓库首先落地 **Phase 1.1：K8sGPT Engine**。

当前阶段定义：**基础生命周期完成、生产事务闭环待修复**。生产事务闭环以
Lease 互斥、可信回滚/卸载和 Provider 功能 E2E 全部通过为完成条件。

Phase 1.1 的目标是建立一套安全、可重复部署、可验证的 Kubernetes 原生 AI 诊断底座：

- 使用 K8sGPT Operator 管理 K8sGPT Engine
- 使用固定 `k8sgpt` ServiceAccount 承载 Engine 权限
- 业务资源只允许 `get/list/watch`
- 禁止读取 Secret
- Phase 1.1 禁止读取 `pods/log`
- 禁止 `create/update/patch/delete`
- 禁止 Mutation / Auto Remediation
- Namespace egress 仅允许 DNS、HTTPS 和 Kubernetes API 端口
- AI 分析默认启用匿名化
- 通过 Result CR 输出结构化诊断结果
- 提供 ImagePullBackOff、CrashLoopBackOff、PVC Pending 三类故障样例
- 提供本地 Preflight 与 GitHub Actions CI 安全质量门禁

## Phase 1.1 架构

```text
User / SRE
    |
    v
Kubernetes API
    |
    +---------------------------+
    |                           |
    v                           v
K8sGPT Operator            K8sGPT CR
    |                           |
    +------------+--------------+
                 |
                 v
          K8sGPT Engine
                 |
          get/list/watch only
                 |
     +-----------+-----------+
     |           |           |
     v           v           v
    Pod      Deployment     PVC ...
     |           |           |
     +-----------+-----------+
                 |
                 v
             Analyzer
                 |
                 v
           Anonymization
                 |
                 v
            AI Provider
                 |
                 v
             Result CR
                 |
                 v
      Phase 1.2 Portal Backend
```

## 目录结构

```text
.
├── Makefile
├── preflight.sh
├── install.sh
├── verify.sh
├── uninstall.sh
├── README.md
├── .gitignore
├── .github/
│   └── workflows/
│       └── ci.yml
├── deploy/
│   └── k8sgpt/
│       ├── namespace.yaml
│       ├── serviceaccount.yaml
│       ├── clusterrole.yaml
│       ├── clusterrolebinding.yaml
│       ├── k8sgpt.yaml
│       ├── values-production.yaml
│       └── demo/
│           ├── namespace.yaml
│           ├── imagepullbackoff.yaml
│           ├── crashloopbackoff.yaml
│           └── pvc-pending.yaml
└── docs/
    ├── architecture.md
    ├── security.md
    └── phase-1.1-acceptance.md
```

# 快速开始

## 0. 本地 Preflight

提交代码或执行安装前建议先运行：

```bash
make preflight
```

本地检查包括：

- 所有 `.sh` 执行 `bash -n`
- 本机存在 ShellCheck 时执行 ShellCheck
- 所有 YAML/YML 使用 PyYAML 解析
- Helm post-renderer 在资源进入 API Server 前替换上游宽权限 RBAC
- 高置信度 Secret/Token/Private Key 模式扫描
- 仓库内所有 Role、ClusterRole 及其 Binding 的静态 RBAC 安全检查
- post-renderer、Make 参数注入和 Secret argv 回归测试

本地 YAML/RBAC 检查需要：

```bash
python3 -m pip install pyyaml
```

ShellCheck 可选安装；GitHub Actions 中为强制检查项。

安装/卸载命令依赖 `kubectl`、`helm`、`python3`；安装器还要求 `curl` 与
`sha256sum`，用于下载并校验固定的 Operator Chart。

## 1. 创建 AI Provider Secret

Secret 不提交到 Git 仓库。首次安装前创建：

```bash
export OPENAI_TOKEN='replace-me'
make bootstrap-secret
```

Phase 1.1 是固定资源身份的单实例基线：Namespace 固定为
`k8sgpt-operator-system`。本阶段不支持覆盖 Namespace、Release、
ServiceAccount、Secret 或 K8sGPT CR 名称。

## 2. 安装 Phase 1.1

```bash
make install
```

`make install` 会依次执行：

```text
检查 kubectl / helm / current-context
   ↓
检查 Namespace 与 AI Secret（不满足时不修改集群）
   ↓
获取 Kubernetes Lease 并记录升级前 revision / 资源快照
   ↓
使用 --atomic / --cleanup-on-fail 安装或升级 Operator
   ↓
应用 RBAC Hardening
   ↓
验证 Secret / Pod Log / Patch / Delete 权限均被拒绝
   ↓
部署 K8sGPT CR
   ↓
严格检查 Operator 与 K8sGPT Engine Ready
```

首次安装的后置步骤失败时，安装器会卸载本次 Release 并恢复安装前资源快照；
已有 Release 的 Helm 外后置步骤失败时，会执行 `helm rollback` 回到升级前
revision，并恢复 RBAC、ServiceAccount 与 K8sGPT CR 快照。Lease 覆盖安装和
卸载全生命周期，后台 heartbeat 持续续租，并在每个变更点执行 fencing 检查，
避免 Jenkins、GitHub Actions 与人工操作并发修改同一 Release。回滚发生二次
失败时会保留权限为 `0700` 的快照目录和对象级恢复报告，不会销毁取证状态。

默认版本：

```text
K8sGPT Operator: 0.2.29
K8sGPT Engine:   v0.4.32
```

Operator、kube-rbac-proxy 与 Engine 镜像均使用 `tag@sha256` 固定多架构 manifest
digest；Operator Chart 也通过固定 URL 下载并校验仓库内置 SHA256，不再信任
运行时可变的 Helm index。版本变更必须同时更新 digest 并通过 Kind E2E。

Phase 1.1 只允许经过 CI 验证的 Operator 版本：

```bash
OPERATOR_VERSION=0.2.29 make install
```

从旧版双标识 RBAC 首次升级时，必须显式迁移；安装器会先校验权限规则、
`roleRef` 和 `subjects` 的语义指纹，匹配后才补齐实例身份：

```bash
MIGRATE_LEGACY_OWNERSHIP=true make install
```

卸载器不接受 legacy 所有权，必须先完成上述安全迁移。

## 3. 安全与功能验收

```bash
make verify
```

验证内容包括：

- Kubernetes Context
- Kubernetes API 连通性
- Helm Release 必须为 `deployed`，且 Chart 身份必须为固定的 `k8sgpt-operator-0.2.29`
- Namespace
- K8sGPT / Result / Mutation CRD
- ServiceAccount、ClusterRole/Binding、NetworkPolicy 必须与仓库安全基线一致
- `get/list` 业务资源权限
- Secret 读取必须为 `no`
- Pod Log 读取必须为 `no`
- create/update/patch/delete 必须为 `no`
- 未启用 Analyzer 对应的 DaemonSet、NetworkPolicy、Webhook 权限必须为 `no`
- AI Provider Secret 存在且包含 `openai-api-key`
- K8sGPT CR 存在
- `anonymized=true`
- backend、分析周期与 Secret 引用正确
- Log Analyzer 未启用
- Auto Remediation 未启用
- Result CR API 可读取
- Operator Ready、observedGeneration 收敛且期望副本数至少为 1
- K8sGPT Engine Deployment Ready、observedGeneration 收敛且期望副本数至少为 1
- Result 查询错误不得降级为零结果

无 Result 时默认只告警，不判定 Phase 1.1 失败。部署故障样例后可使用严格模式：

```bash
STRICT_RESULTS=true make verify
```

严格模式可使用 `RESULT_SINCE` 和 `EXPECTED_RESULT_KINDS`，只接受当前 K8sGPT
实例在指定时间后生成、且包含 AI details 的 Result。

## 4. 部署故障样例

```bash
make demo
```

会创建：

```text
ImagePullBackOff
CrashLoopBackOff
PVC Pending
```

查看故障：

```bash
kubectl get pod,pvc -n k8sgpt-demo
```

查看 K8sGPT Result：

```bash
make results
```

等待 K8sGPT 完成一个分析周期后，可执行：

```bash
STRICT_RESULTS=true make verify
```

清理测试故障：

```bash
make clean-demo
```

## 5. 查看状态

```bash
make status
```

该命令检查 API、Helm revision 与 Chart 身份、Lease、Operator/Engine 非零副本完整
Ready、最近分析错误和当前 K8sGPT 实例的 Result 数量；关键查询失败、Release
身份异常或工作负载未收敛时返回非零状态。

## 6. 卸载

```bash
make uninstall
```

卸载会等待 K8sGPT CR 删除完成，再卸载 Operator，并检查 Helm Release、
K8sGPT CR、ClusterRole 和 ClusterRoleBinding 残留。API、权限、
finalizer 或超时错误会返回非零状态。

默认策略：

```text
K8sGPT CR        删除
Helm Release     删除
Phase 1.1 RBAC   删除
AI Secret        保留
Namespace        保留
CRD              保留
```

需要同时删除 AI Secret：

```bash
PURGE_SECRET=true make uninstall
```

需要同时删除 Namespace：

```bash
PURGE_SECRET=true PURGE_NAMESPACE=true make uninstall
```

同时清理 Demo：

```bash
PURGE_DEMO=true make uninstall
```

CRD 默认不自动删除，避免误伤其它 K8sGPT 实例或后续重新安装。

## 7. Kubernetes v1.34+ Kind E2E

```bash
make e2e
```

E2E 使用固定 digest 的 `kindest/node:v1.34.8`，验证前置条件失败时零 Helm
变更、外部或伪造身份的同名 RBAC 拒绝接管、legacy 指纹迁移、同名外部 Helm
Release 拒绝操作、首次安装、重复安装、升级后置失败回滚、回滚状态保留、Lease
heartbeat/fencing/CAS 释放、Forbidden 查询不得假成功、完全卸载、重复卸载和
残留资源策略。

该 Required Check 是确定性的**生命周期 E2E**，不使用假 Token 冒充 AI 功能
验收。真实 Provider 功能闭环通过独立的手工工作流执行：

```bash
export OPENAI_TOKEN='restricted-test-token'
make e2e-provider
```

它会部署三类 Demo，并只接受本次运行生成、属于当前 K8sGPT 实例的 Pod、
Deployment、PersistentVolumeClaim Result；所有 Result 必须包含 AI details，且
K8sGPT `lastAnalysisError` 必须为空。GitHub 工作流需要 Environment
`phase-1.1-functional` 中的 `OPENAI_E2E_TOKEN` Secret。

# Makefile 命令

```bash
make help
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
```

# GitHub Actions CI

工作流：

```text
.github/workflows/ci.yml
```

在 `main` 分支 Push 和面向 `main` 的 Pull Request 时自动执行。

CI 分为三类 Job：

```text
Preflight / Lint / RBAC
├── bash -n
├── ShellCheck
├── YAML lint
└── preflight.sh

Secret Scan
└── Gitleaks 全 Git 历史扫描

Kubernetes v1.34 Kind E2E
└── install → reinstall → rollback fault → Lease contention → trusted uninstall
```

安全设计：

- Workflow 默认仅授予 `contents: read`
- 所有第三方 GitHub Actions 均固定到完整 commit SHA
- Kind node 镜像固定到上游发布的 sha256 digest
- Checkout 使用完整历史，Secret Scanner 可以覆盖历史提交
- Secret 输出启用 redact，避免在 CI 日志再次暴露凭据
- Provider E2E 只有 `main` ref 才能请求 Environment Secret
- Secret 通过 stdin 创建，不进入 `kubectl` argv；验证脚本只读取 key 存在状态
- RBAC 若出现 `*`、Secret、Pod Logs、危险写 verbs、外部 RoleRef 或宽泛 Group，CI 直接失败
- `create/update/patch/delete/deletecollection/impersonate/bind/escalate` 均属于禁止权限

`main` Ruleset 应分别要求 `Preflight / Lint / RBAC`、`Secret Scan` 和
`Kubernetes v1.34 Kind E2E` 三个 Required Status Checks。

# RBAC Hardening

Operator `v0.2.29` 默认启用 dynamicRBAC。本项目显式设置：

```yaml
dynamicRBAC:
  enabled: false
```

关闭后，官方 Helm Chart 会创建静态：

```text
ServiceAccount:     k8sgpt
ClusterRole:        k8sgpt-clusterrole
ClusterRoleBinding: k8sgpt-clusterrole-binding
```

官方静态 ClusterRole 默认仍包含 `secrets` 与 `pods/log` 读取权限。post-renderer
会在资源进入 API Server 前收敛 ServiceAccount、ClusterRole rules 以及
ClusterRoleBinding 的 `roleRef/subjects`；安装后再用 Server-Side Apply 复验基线。

本项目的 `install.sh` 已自动执行该动作：

```text
helm upgrade --install
        ↓
kubectl apply clusterrole.yaml
        ↓
kubectl apply clusterrolebinding.yaml
        ↓
kubectl auth can-i 安全校验
```

本仓库的同名 `k8sgpt-clusterrole` 会移除：

```text
secrets
pods/log
create
update
patch
delete
```

ClusterRole 与 ClusterRoleBinding 必须同时具备以下精确身份，缺少或伪造任意
一项都会拒绝接管或删除：

```text
kube-aiops.io/owner=phase-1.1
app.kubernetes.io/part-of=kube-aiops
app.kubernetes.io/instance=k8sgpt-operator
```

同名 Helm Release 还必须验证 Chart 身份为 `k8sgpt-operator`。

Operator `v0.2.28` 引入 gRPC 依赖安全更新、跨 Namespace Prompt Injection
防护和分析错误状态上报，`v0.2.29` 增加策略门控 Auto Remediation。本阶段虽然
采用 `v0.2.29`，但 Mutation 与 Auto Remediation 仍保持禁用，并由 `make verify`
强制验收。

# 安全原则

Phase 1.1 明确遵循以下边界：

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

详细设计见：

- `docs/architecture.md`
- `docs/security.md`
- `docs/phase-1.1-acceptance.md`
- `docs/phase-1.1-hardening.md`

# Roadmap

- Phase 1.1：K8sGPT Engine
- Phase 1.2：Portal Backend API
- Phase 1.3：Web Portal
- Phase 1.4：Authentication / Authorization / Audit / Sanitizer
- Phase 2：Events + Prometheus + Loki + Alertmanager 关联分析
- Phase 3：RCA Agent + Runbook
- Phase 4：HITL Remediation
- Phase 5：Controlled Auto Remediation
