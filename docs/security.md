# Phase 1.1 安全设计

## 安全目标

Phase 1.1 的原则是：

> AI 可以发现问题、解释问题、给出建议，但不能修改业务资源。

## RBAC 基线

K8sGPT workload 使用：

```text
ServiceAccount: k8sgpt
ClusterRole:    k8sgpt-clusterrole
```

仅授予：

```text
get
list
watch
```

明确禁止：

```text
create
update
patch
delete
deletecollection
```

## Secret 访问

Phase 1.1 不允许 K8sGPT 读取 Kubernetes Secret。

```bash
kubectl auth can-i get secrets \
  --as=system:serviceaccount:k8sgpt-operator-system:k8sgpt
```

预期：

```text
no
```

AI Provider Secret 只由 K8sGPT workload 在 Pod 启动过程中按 CR 引用使用，不应被 Analyzer 当作业务数据扫描。

## Log Analyzer

Phase 1.1 不启用 Log Analyzer，同时从业务读取 ClusterRole 中移除 `pods/log` 权限。

原因：日志中可能出现：

- Token
- Password
- Cookie
- Authorization Header
- AK/SK
- 数据库连接串
- 用户数据
- 业务敏感字段

日志关联分析放到 Phase 2，并增加独立 Sanitizer。

## dynamicRBAC

Operator v0.2.29 默认启用 dynamicRBAC。当前项目将其设置为：

```yaml
dynamicRBAC:
  enabled: false
```

关闭后，官方 Helm Chart 会创建静态 `k8sgpt` ServiceAccount、`k8sgpt-clusterrole` 和 `k8sgpt-clusterrole-binding`。

官方静态角色默认包含 `secrets` 和 `pods/log`，因此本项目必须在 Helm 安装/升级后重新应用：

```bash
kubectl apply -f deploy/k8sgpt/serviceaccount.yaml
kubectl apply -f deploy/k8sgpt/clusterrole.yaml
kubectl apply -f deploy/k8sgpt/clusterrolebinding.yaml
```

这会将 `k8sgpt-clusterrole` 收敛到 Phase 1.1 基线。

> 每次执行 `helm upgrade` 后必须再次执行上述 hardening apply，避免 Helm 将官方默认 RBAC 恢复回来。后续可通过 Helm post-renderer 或 GitOps policy 自动化该过程。

## 匿名化

K8sGPT CR 设置：

```yaml
ai:
  anonymized: true
```

匿名化是第一层保护，但不能替代 Phase 2 的数据清洗与脱敏组件。

## 禁止自动修复

Phase 1.1 不启用：

- Mutation
- Auto Remediation
- Tool Calling 执行
- kubectl patch/delete/scale

安全链路固定为：

```text
Detect
  ↓
Explain
  ↓
Recommend
  ↓
STOP
```

## 安全验收命令

允许：

```bash
kubectl auth can-i list pods \
  --as=system:serviceaccount:k8sgpt-operator-system:k8sgpt
```

拒绝：

```bash
kubectl auth can-i get secrets \
  --as=system:serviceaccount:k8sgpt-operator-system:k8sgpt

kubectl auth can-i get pods/log \
  --as=system:serviceaccount:k8sgpt-operator-system:k8sgpt

kubectl auth can-i patch deployments.apps \
  --as=system:serviceaccount:k8sgpt-operator-system:k8sgpt

kubectl auth can-i delete pods \
  --as=system:serviceaccount:k8sgpt-operator-system:k8sgpt
```

全部拒绝项应返回：

```text
no
```
