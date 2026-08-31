# Phase 1.1 验收清单

## 1. Operator

```bash
kubectl get pod -n k8sgpt-operator-system
kubectl get crd | grep k8sgpt
```

要求：

- Operator Pod Running
- `k8sgpts.core.k8sgpt.ai` 存在
- `results.core.k8sgpt.ai` 存在
- `mutations.core.k8sgpt.ai` 存在，但 Phase 1.1 不使用 Mutation

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
- anonymized=true
- analysis interval=5m
- 不包含 Log filter
- 不启用 Auto Remediation

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

- 至少能够针对测试故障生成 Result
- Result 可通过 Kubernetes API 查询
- Portal 后续只依赖 Result CR，不解析 K8sGPT 日志

## 6. Phase 1.1 完成定义

当前阶段定义为：**基础生命周期完成、生产事务闭环待修复**。下面所有项目
通过后，才可将“生产事务闭环待修复”更新为“生产事务闭环完成”。

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
| Kubernetes v1.34+ Kind 生命周期 E2E | Required Check 通过 |
| Provider Functional E2E | 三类本次新鲜 Result、AI details、analysis health 全部通过 |

以上项目全部满足、GitHub Actions 的三个 Job 均通过且 main 分支启用
Required Status Check 后，Phase 1.1 才视为完成，并进入 Phase 1.2 Portal Backend API。
