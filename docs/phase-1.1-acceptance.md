# Phase 1.1 验收清单

> Phase 1.1 已完成。本文保留其安全/生命周期验收项，并将当前持续回归环境升级为
> Kubernetes v1.36.4。历史完成证据与后续平台升级证据应分别保留。

## 1. Operator

```bash
kubectl get pod -n k8sgpt-operator-system
kubectl get crd | grep k8sgpt
```

要求：

- Operator Pod Running
- `k8sgpts.core.k8sgpt.ai` 存在
- `results.core.k8sgpt.ai` 存在
- `mutations.core.k8sgpt.ai` 存在，但当前不使用 Mutation

## 2. RBAC

```bash
kubectl auth can-i list pods \
  --as=system:serviceaccount:k8sgpt-operator-system:k8sgpt
```

预期：`yes`

```bash
kubectl auth can-i get secrets \
  --as=system:serviceaccount:k8sgpt-operator-system:k8sgpt
```

预期：`no`

```bash
kubectl auth can-i get pods --subresource=log \
  --as=system:serviceaccount:k8sgpt-operator-system:k8sgpt
```

预期：`no`

```bash
kubectl auth can-i patch deployments.apps \
  --as=system:serviceaccount:k8sgpt-operator-system:k8sgpt
```

预期：`no`

```bash
kubectl auth can-i delete pods \
  --as=system:serviceaccount:k8sgpt-operator-system:k8sgpt
```

预期：`no`

## 3. K8sGPT CR

```bash
kubectl get k8sgpt -n k8sgpt-operator-system
kubectl get k8sgpt k8sgpt-engine -n k8sgpt-operator-system -o yaml
```

要求：

- AI enabled
- `anonymized=true`
- analysis interval=5m
- 不包含 Log filter
- 不启用 Auto Remediation
- Engine 使用仓库固定的 immutable `tag@sha256`

当前持续验证版本：

```text
K8sGPT Operator: v0.2.29
K8sGPT Engine:   v0.4.37
```

## 4. Demo 故障

```bash
kubectl apply -f deploy/k8sgpt/demo/namespace.yaml
kubectl apply -f deploy/k8sgpt/demo/imagepullbackoff.yaml
kubectl apply -f deploy/k8sgpt/demo/crashloopbackoff.yaml
kubectl apply -f deploy/k8sgpt/demo/pvc-pending.yaml
kubectl get pods,pvc -n k8sgpt-demo
```

期望观察到：

- ImagePullBackOff / ErrImagePull
- CrashLoopBackOff
- PVC Pending

## 5. Result CR

```bash
kubectl get results -n k8sgpt-operator-system
kubectl get results -n k8sgpt-operator-system -o yaml
```

要求：

- 能够针对测试故障生成 Result
- Result 可通过 Kubernetes API 查询
- Portal 只依赖 Result CR，不解析 K8sGPT 日志

## 6. Kubernetes v1.36 持续兼容验收

当前 Required E2E 必须使用：

```text
Kubernetes API Server: v1.36.4
kubectl:                v1.36.4
kubelet:                v1.36.4
Kind:                   v0.33.0
```

在完整生命周期 E2E 前先运行 `tests/kubernetes-v1.36-smoke.sh`，精确验证版本和
kube-aiops 依赖的 stable API discovery。

## 7. 持续验收矩阵

| 检查项 | 要求 |
|---|---|
| Operator Running | 通过 |
| K8sGPT CR 可用 | 通过 |
| Result CR 可生成 | 通过 |
| Pod Analyzer | 通过 |
| Deployment Analyzer | 通过 |
| PVC Analyzer | 通过 |
| AI Explain | 通过 |
| anonymized=true | 通过 |
| Secret 不进入 Git | 通过 |
| Secret Read | 禁止 |
| pods/log Read | 禁止 |
| Patch/Update/Delete | 禁止 |
| Mutation | 禁止 |
| Auto Remediation | 禁止 |
| 缺少 Namespace/Secret | 安装失败且不创建 Helm Release |
| 重复安装 | 通过且无资源漂移 |
| 安装失败回滚 | 新装恢复快照；已有 Release 的 Helm 后置失败回滚旧 revision |
| 并发生命周期操作 | Kubernetes Lease 拒绝并发 install/uninstall |
| 长事务锁 | heartbeat 续租、变更前 fencing、CAS 释放均通过 |
| 资源所有权 | owner/part-of/instance 三元身份、Helm Chart 身份及运行时安全清单语义通过 |
| Secret 传递 | Token 不进入 argv，脚本不读取 Secret 值 |
| 状态检查 | API/权限/Chart 身份/非零副本 Ready 查询失败时 `make status` 返回非零 |
| Pod 安全 | PSA baseline enforce、restricted audit/warn，Operator/Engine 安全上下文通过 |
| 卸载与重复卸载 | 通过 |
| 卸载残留检查 | 查询错误必须 FAIL；CRD 按策略保留 |
| Kubernetes v1.36 Kind 生命周期 E2E | Required Check 通过 |
| Provider Functional E2E | 三类本次新鲜 Result、AI details、analysis health 全部通过 |

当前持续 Required Status Checks：

```text
Preflight / Lint / RBAC
Secret Scan
Kubernetes v1.36 Kind E2E
```

Phase 1.1 的功能范围保持冻结；平台版本升级不得借机扩大 RBAC、启用日志读取或
Mutation/Auto Remediation。
