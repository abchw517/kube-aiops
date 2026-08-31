# Phase 1.2：Portal Backend API

## Phase 1.2.1 当前范围

本阶段只建立 Portal Backend 的最小可运行骨架，不提前实现 Findings、Resource API、认证或自动修复。

当前交付：

- Go Backend Skeleton
- `GET /healthz`
- `GET /readyz`
- 最小 Dockerfile
- 最小 Deployment / Service
- 独立 `kube-aiops-api` ServiceAccount
- Result-only 只读 ClusterRole / ClusterRoleBinding
- Go 单元测试与 CI 编译检查
- Kubernetes Manifest Schema / RBAC 静态门禁继续生效

明确不包含：

- Findings API
- Namespace API
- Pod/Deployment Resource API
- Portal UI
- OIDC / SSO
- Prometheus / Loki RCA
- Kubernetes 写操作
- Secret 读取
- Pod Logs 读取
- Mutation / Auto Remediation

## 健康检查语义

### `/healthz`

只判断 API 进程是否存活，不访问 Kubernetes。

成功：

```json
{"status":"ok"}
```

### `/readyz`

实际访问 Kubernetes 中的 K8sGPT Result API：

```text
/apis/core.k8sgpt.ai/v1alpha1/namespaces/k8sgpt-operator-system/results?limit=1
```

因此 readiness 同时验证：

1. Kubernetes API 可达；
2. ServiceAccount Token/CA 可用；
3. K8sGPT Result CRD 已存在；
4. `kube-aiops-api` 对 Result 具备 list 权限。

任一条件失败时返回 HTTP `503`，但不会把 Kubernetes 原始错误返回给客户端。

## RBAC

Phase 1.2.1 遵循“代码能力和权限同步增长”原则。

当前只允许：

```text
core.k8sgpt.ai/results
  get/list/watch
```

明确禁止：

```text
namespaces
pods
pods/log
secrets
services
deployments
statefulsets
create
update
patch
delete
deletecollection
impersonate
bind
escalate
```

Namespace、Pod、Deployment 等权限会等 Phase 1.2.2 对应 API 真正实现时再逐项增加。

## 本地开发

```bash
make api-fmt-check
make api-test
make api-build
make api-run
```

默认监听：

```text
:8080
```

可配置环境变量：

```text
HTTP_ADDR
READY_TIMEOUT
K8SGPT_NAMESPACE
KUBERNETES_API_URL
KUBERNETES_BEARER_TOKEN_FILE
KUBERNETES_CA_FILE
```

本地没有 Kubernetes ServiceAccount 时，`/healthz` 仍可返回 200，`/readyz` 会返回 503。

## Docker / Kind 本地运行

```bash
docker build -t kube-aiops-api:dev .
kind load docker-image kube-aiops-api:dev --name <kind-cluster-name>
```

然后应用最小运行资源：

```bash
kubectl apply -f deploy/api/namespace.yaml
kubectl apply -f deploy/api/serviceaccount.yaml
kubectl apply -f deploy/api/clusterrole.yaml
kubectl apply -f deploy/api/clusterrolebinding.yaml
kubectl apply -f deploy/api/deployment.yaml
kubectl apply -f deploy/api/service.yaml
```

当前 Deployment 使用：

```text
kube-aiops-api:dev
```

只用于 Phase 1.2.1 本地/Kind 验证；正式镜像构建、签名、digest 固定和发布流水线后续独立推进。

## RBAC 安全验收

```bash
SA='system:serviceaccount:kube-aiops-system:kube-aiops-api'

kubectl auth can-i list results.core.k8sgpt.ai --as="${SA}"

kubectl auth can-i list namespaces --as="${SA}"
kubectl auth can-i get pods --as="${SA}"
kubectl auth can-i get secrets --as="${SA}"
kubectl auth can-i get pods/log --as="${SA}"
kubectl auth can-i patch deployments.apps --as="${SA}"
kubectl auth can-i delete pods --as="${SA}"
```

预期：

```text
list results.core.k8sgpt.ai = yes
其它检查                    = no
```

## Phase 1.2.1 验收标准

| 检查项 | 要求 |
|---|---|
| Go build | PASS |
| Go unit test | PASS |
| go vet | PASS |
| gofmt | PASS |
| `/healthz` | 200 |
| `/readyz` Kubernetes/Result API 正常 | 200 |
| `/readyz` Kubernetes/Result API 异常 | 503 |
| Result get/list/watch | ALLOW |
| Namespace | DENY |
| Pod | DENY |
| Secret | DENY |
| Pod Logs | DENY |
| Kubernetes write | DENY |
| kubeconform | PASS |
| RBAC lint | PASS |
| Gitleaks | PASS |
| Kubernetes v1.34 Kind E2E | Phase 1.1 回归必须继续 PASS |

## 下一阶段

Phase 1.2.2 再增加：

```text
Kubernetes client abstraction
        ↓
Cluster / Namespace API
        ↓
只读 Resource API
        ↓
对应资源权限按需扩展
```

Phase 1.2.3 再实现核心链路：

```text
K8sGPT Result CR
        ↓
Result Adapter
        ↓
Finding Model
        ↓
GET /api/v1/findings
```
