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
kubectl auth can-i get pods/log \
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

以上项目全部满足后，Phase 1.1 才视为完成，并进入 Phase 1.2 Portal Backend API。
