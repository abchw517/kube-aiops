# kube-aiops

基于 K8sGPT 构建的 Kubernetes AIOps 平台，提供安全只读的 AI 辅助诊断、根因分析、可观测性关联与 Web Portal 能力。

## 当前阶段

当前仓库首先落地 **Phase 1.1：K8sGPT Engine**。

Phase 1.1 的目标是建立一套安全、可重复部署、可验证的 Kubernetes 原生 AI 诊断底座：

- 使用 K8sGPT Operator 管理 K8sGPT Engine
- 使用固定 `k8sgpt` ServiceAccount 承载 Engine 权限
- 业务资源只允许 `get/list/watch`
- 禁止读取 Secret
- Phase 1.1 禁止读取 `pods/log`
- 禁止 `create/update/patch/delete`
- 禁止 Mutation / Auto Remediation
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
- `k8sgpt-clusterrole` 静态 RBAC 安全检查

本地 YAML/RBAC 检查需要：

```bash
python3 -m pip install pyyaml
```

ShellCheck 可选安装；GitHub Actions 中为强制检查项。

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
Preflight
   ↓
检查 kubectl / helm / current-context
   ↓
检查 Namespace 与 AI Secret（不满足时不修改集群）
   ↓
使用 --atomic / --cleanup-on-fail 安装或升级 Operator
   ↓
应用 RBAC Hardening
   ↓
验证 Secret / Pod Log / Patch / Delete 权限均被拒绝
   ↓
部署 K8sGPT CR
   ↓
严格检查 Operator Ready
```

首次安装的后置步骤失败时，安装器会清理本次创建的 CR、Helm Release
和项目 RBAC；已有 Release 升级失败时由 Helm `--atomic` 回滚。

默认版本：

```text
K8sGPT Operator: 0.2.27
K8sGPT Engine:   v0.4.32
```

可覆盖 Operator 版本：

```bash
OPERATOR_VERSION=0.2.27 make install
```

## 3. 安全与功能验收

```bash
make verify
```

验证内容包括：

- Kubernetes Context
- Kubernetes API 连通性
- Helm Release 必须为 `deployed`
- Namespace
- K8sGPT / Result / Mutation CRD
- `k8sgpt` ServiceAccount
- `get/list` 业务资源权限
- Secret 读取必须为 `no`
- Pod Log 读取必须为 `no`
- create/update/patch/delete 必须为 `no`
- AI Provider Secret 存在且包含 `openai-api-key`
- K8sGPT CR 存在
- `anonymized=true`
- backend、分析周期与 Secret 引用正确
- Log Analyzer 未启用
- Auto Remediation 未启用
- Result CR API 可读取
- Operator Ready（未就绪直接失败）

无 Result 时默认只告警，不判定 Phase 1.1 失败。部署故障样例后可使用严格模式：

```bash
STRICT_RESULTS=true make verify
```

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

E2E 使用 `kindest/node:v1.34.0`，验证前置条件失败时零 Helm 变更、
首次安装、重复安装、基础验证、完全卸载、重复卸载、残留资源与 CRD 保留策略。

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
└── install → reinstall → verify → uninstall → uninstall
```

安全设计：

- Workflow 默认仅授予 `contents: read`
- Checkout 使用完整历史，Secret Scanner 可以覆盖历史提交
- Secret 输出启用 redact，避免在 CI 日志再次暴露凭据
- RBAC 若出现 `*`、Secret、Pod Logs 或危险写 verbs，CI 直接失败
- `create/update/patch/delete/deletecollection/impersonate/bind/escalate` 均属于禁止权限

建议将 `Phase 1.1 CI` 配置为 `main` 分支的 Required Status Check，避免绕过质量门禁直接合并。

# RBAC Hardening

Operator `v0.2.27` 默认启用 dynamicRBAC。本项目显式设置：

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

官方静态 ClusterRole 默认仍包含 `secrets` 与 `pods/log` 读取权限，因此 **每次 Helm install/upgrade 后都必须重新应用本仓库的 RBAC Hardening**。

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

# Roadmap

- Phase 1.1：K8sGPT Engine
- Phase 1.2：Portal Backend API
- Phase 1.3：Web Portal
- Phase 1.4：Authentication / Authorization / Audit / Sanitizer
- Phase 2：Events + Prometheus + Loki + Alertmanager 关联分析
- Phase 3：RCA Agent + Runbook
- Phase 4：HITL Remediation
- Phase 5：Controlled Auto Remediation
