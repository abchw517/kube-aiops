# kube-aiops

基于 K8sGPT 构建的 Kubernetes AIOps 平台，提供安全只读的 AI 辅助诊断、根因分析、可观测性关联与 Web Portal 能力。

## 当前阶段

当前仓库首先落地 **Phase 1.1：K8sGPT Engine**。

Phase 1.1 的目标是建立一套安全、可重复部署、可验证的 Kubernetes 原生 AI 诊断底座：

- 使用 K8sGPT Operator 管理 K8sGPT Engine
- 通过独立 ServiceAccount 控制权限
- 业务资源只允许 `get/list/watch`
- 禁止读取 Secret
- 禁止 `create/update/patch/delete`
- 禁止 Mutation / Auto Remediation
- AI 分析默认启用匿名化
- 通过 Result CR 输出结构化诊断结果
- 提供 ImagePullBackOff、CrashLoopBackOff、PVC Pending 三类故障样例

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
├── README.md
├── .gitignore
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

## 1. 安装 K8sGPT Operator

```bash
helm repo add k8sgpt https://charts.k8sgpt.ai/
helm repo update

helm upgrade --install k8sgpt-operator \
  k8sgpt/k8sgpt-operator \
  -n k8sgpt-operator-system \
  --create-namespace \
  --version 0.2.27 \
  -f deploy/k8sgpt/values-production.yaml \
  --wait \
  --timeout 5m
```

> 生产环境建议固定 Operator 版本，不跟随 latest/main 镜像。

## 2. 安装只读 RBAC

```bash
kubectl apply -f deploy/k8sgpt/namespace.yaml
kubectl apply -f deploy/k8sgpt/serviceaccount.yaml
kubectl apply -f deploy/k8sgpt/clusterrole.yaml
kubectl apply -f deploy/k8sgpt/clusterrolebinding.yaml
```

## 3. 创建 AI Provider Secret

Secret 不提交到 Git 仓库。

```bash
export OPENAI_TOKEN='replace-me'

kubectl create secret generic k8sgpt-openai-secret \
  -n k8sgpt-operator-system \
  --from-literal=openai-api-key="${OPENAI_TOKEN}"
```

## 4. 部署 K8sGPT CR

```bash
kubectl apply -f deploy/k8sgpt/k8sgpt.yaml
```

检查：

```bash
kubectl get k8sgpt -n k8sgpt-operator-system
kubectl get pod -n k8sgpt-operator-system
kubectl get results -n k8sgpt-operator-system
```

## 5. RBAC 安全验证

应该允许：

```bash
kubectl auth can-i get pods \
  --as=system:serviceaccount:k8sgpt-operator-system:k8sgpt-engine
```

预期：

```text
yes
```

应该拒绝：

```bash
kubectl auth can-i get secrets \
  --as=system:serviceaccount:k8sgpt-operator-system:k8sgpt-engine

kubectl auth can-i patch deployments \
  --as=system:serviceaccount:k8sgpt-operator-system:k8sgpt-engine

kubectl auth can-i delete pods \
  --as=system:serviceaccount:k8sgpt-operator-system:k8sgpt-engine
```

预期均为：

```text
no
```

## 6. 故障样例验证

```bash
kubectl apply -f deploy/k8sgpt/demo/namespace.yaml
kubectl apply -f deploy/k8sgpt/demo/imagepullbackoff.yaml
kubectl apply -f deploy/k8sgpt/demo/crashloopbackoff.yaml
kubectl apply -f deploy/k8sgpt/demo/pvc-pending.yaml
```

查看故障：

```bash
kubectl get pod,pvc -n k8sgpt-demo
```

查看诊断结果：

```bash
kubectl get results -n k8sgpt-operator-system
kubectl get results -n k8sgpt-operator-system -o yaml
```

## 安全原则

Phase 1.1 明确遵循以下边界：

```text
Detect      -> Allowed
Explain     -> Allowed
Recommend   -> Allowed
Result CR   -> Allowed

Secret Read -> Denied
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

## Roadmap

- Phase 1.1：K8sGPT Engine
- Phase 1.2：Portal Backend API
- Phase 1.3：Web Portal
- Phase 1.4：Authentication / Authorization / Audit / Sanitizer
- Phase 2：Events + Prometheus + Loki + Alertmanager 关联分析
- Phase 3：RCA Agent + Runbook
- Phase 4：HITL Remediation
- Phase 5：Controlled Auto Remediation
