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

`make bootstrap-secret` 通过 stdin 向 `kubectl` 传递 Token，避免值出现在进程
argv；安装和验证只查询 `openai-api-key` 是否存在，不把 Secret 值读入 Shell。

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

官方静态角色默认包含 `secrets` 和 `pods/log`。本项目使用 Helm post-renderer，
在资源提交 API Server 之前替换 ClusterRole 规则、ClusterRoleBinding 的
`roleRef/subjects` 以及 ServiceAccount 安全字段；安装后再使用 Server-Side Apply
复验并收敛 Git 基线：

```bash
kubectl apply -f deploy/k8sgpt/serviceaccount.yaml
kubectl apply -f deploy/k8sgpt/clusterrole.yaml
kubectl apply -f deploy/k8sgpt/clusterrolebinding.yaml
```

这会将 `k8sgpt-clusterrole` 收敛到 Phase 1.1 基线。

集群级 RBAC 必须同时带有 owner、part-of、instance 三元身份。同名 Helm
Release 必须来自 `k8sgpt-operator` Chart。旧双标识资源仅能在安装时通过
`MIGRATE_LEGACY_OWNERSHIP=true` 显式迁移，且权限与 Binding 指纹必须匹配；
卸载器永远不对 legacy 身份执行删除。

Namespace 使用 PSA `baseline` enforce，并以 Kubernetes v1.34 `restricted`
策略执行 audit/warn。Operator 和 Engine 使用 non-root、RuntimeDefault seccomp、
禁止提权、drop ALL capabilities 与只读根文件系统。

Namespace 还应用 egress NetworkPolicy：仅放行 DNS（TCP/UDP 53）、HTTPS（443）
和 Kubernetes API 常用端口（6443）。如果 Provider、代理或私有 API 使用其它
端口，必须先评审并显式扩展清单，不能临时放开全量 egress。

> 不要绕过 `install.sh` 直接执行裸 `helm upgrade`。标准入口会同时执行
> post-render、Server-Side Apply、安全断言和失败回滚。

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
