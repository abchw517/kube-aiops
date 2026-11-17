# Phase 1.2：Portal Backend API

## Phase 1.2.1：Backend Skeleton

Phase 1.2.1 已建立 Portal Backend 的最小可运行骨架：

- Go Backend Skeleton
- `GET /healthz`
- `GET /readyz`
- 最小 Dockerfile、Deployment、Service
- 独立 `kube-aiops-api` ServiceAccount
- Result-only 只读 RBAC
- Go build/test/vet/gofmt 与 Docker smoke test
- Kubernetes Manifest Schema / RBAC / Secret 静态门禁

## Phase 1.2.2：Kubernetes ReadOnly API

Phase 1.2.2 在 1.2.1 基础上增加统一 Kubernetes Client，并只开放当前 Portal 最小需要的只读查询能力。

### API

```text
GET /healthz
GET /readyz
GET /api/v1/clusters
GET /api/v1/clusters/{cluster}/namespaces
GET /api/v1/clusters/{cluster}/resources/{kind}/{namespace}/{name}
```

当前只支持单集群 ID：

```text
local
```

Resource Detail 当前只支持：

```text
Pod
Deployment
```

不支持任意 GVR 透传，避免客户端通过构造 URL 绕过 API 资源白名单。

### Cluster API

```http
GET /api/v1/clusters
```

示例：

```json
{
  "items": [
    {
      "id": "local",
      "name": "local",
      "status": "ready"
    }
  ]
}
```

Phase 1.2.2 暂不通过 `/version` 探测 Kubernetes Server Version，因此不需要增加 `nonResourceURLs` RBAC。

### Namespace API

```http
GET /api/v1/clusters/local/namespaces
```

返回经过裁剪的结构，仅暴露 Namespace 名称：

```json
{
  "items": [
    {"name": "default"},
    {"name": "kube-system"}
  ]
}
```

不会向 Portal 透传 Namespace labels / annotations / managedFields。

### Resource API

Pod：

```http
GET /api/v1/clusters/local/resources/pods/default/demo
```

Deployment：

```http
GET /api/v1/clusters/local/resources/deployments/default/demo
```

返回值不是 Kubernetes 原始对象，只包含 Portal 当前需要的最小字段：

```json
{
  "apiVersion": "v1",
  "kind": "Pod",
  "namespace": "default",
  "name": "demo",
  "createdAt": "2026-08-31T10:00:00Z",
  "status": {
    "phase": "Running"
  }
}
```

Deployment 的 status 只保留：

```text
replicas
readyReplicas
availableReplicas
```

明确不返回：

```text
Pod spec
container env
Secret / ConfigMap values
annotations
managedFields
Pod logs
ServiceAccount token
完整 Kubernetes 原始对象
```

## Kubernetes Client Boundary

统一链路：

```text
HTTP Handler
    ↓
Backend Interface
    ↓
Kubernetes Client
    ↓
Kubernetes API
```

`/readyz`、Namespace API 和 Resource API 共用同一客户端实现，避免不同 API 各自实现 Token、CA、TLS 和错误处理。

ServiceAccount Token 每次 Kubernetes 请求都从文件读取，因此兼容 Kubernetes projected ServiceAccount Token 轮换。

客户端响应读取设置上限，避免异常 API 响应造成无边界内存读取。

## 错误边界

Portal 不接收 client-go/Kubernetes 的原始错误文本。

示例：

```json
{
  "error": {
    "code": "RESOURCE_NOT_FOUND",
    "message": "resource not found"
  }
}
```

当前主要错误：

```text
CLUSTER_NOT_FOUND
UNSUPPORTED_RESOURCE_KIND
RESOURCE_NOT_FOUND
RESOURCE_READ_FAILED
NAMESPACE_LIST_FAILED
```

## RBAC

Phase 1.2.2 继续遵循：

> 代码能力和 Kubernetes 权限同步增长。

允许：

```text
core.k8sgpt.ai/results
  get/list/watch

core/namespaces
  list

core/pods
  get

apps/deployments
  get
```

禁止：

```text
secrets
pods/log
services
statefulsets
configmaps
create
update
patch
delete
deletecollection
impersonate
bind
escalate
*
```

即使 Resource API URL 被人为修改为 `secrets`，HTTP 层资源白名单也会先返回 `400 UNSUPPORTED_RESOURCE_KIND`，同时 Kubernetes RBAC 本身也没有 Secret 权限，形成双层限制。

## RBAC 验收

```bash
SA='system:serviceaccount:kube-aiops-system:kube-aiops-api'

# 必须 ALLOW
kubectl auth can-i list results.core.k8sgpt.ai --as="${SA}"
kubectl auth can-i list namespaces --as="${SA}"
kubectl auth can-i get pods --as="${SA}"
kubectl auth can-i get deployments.apps --as="${SA}"

# 必须 DENY
kubectl auth can-i list pods --as="${SA}"
kubectl auth can-i get secrets --as="${SA}"
kubectl auth can-i get pods/log --as="${SA}"
kubectl auth can-i get statefulsets.apps --as="${SA}"
kubectl auth can-i patch deployments.apps --as="${SA}"
kubectl auth can-i delete pods --as="${SA}"
```

## Phase 1.2.2 验收标准

| 检查项 | 要求 |
|---|---|
| Phase 1.2.1 回归 | PASS |
| `GET /api/v1/clusters` | 200 |
| local Cluster | PASS |
| unknown Cluster | 404 |
| Namespace list | PASS |
| Pod detail | PASS |
| Deployment detail | PASS |
| unsupported resource kind | 400 |
| missing Pod/Deployment | 404 |
| 原始 Pod/Deployment spec 不透传 | PASS |
| Namespace list RBAC | ALLOW |
| Pod get RBAC | ALLOW |
| Deployment get RBAC | ALLOW |
| Pod list RBAC | DENY |
| Secret | DENY |
| Pod Logs | DENY |
| Kubernetes write | DENY |
| gofmt / vet / test / build | PASS |
| Docker smoke test | PASS |
| kubeconform | PASS |
| RBAC lint | PASS |
| Gitleaks | PASS |
| Kubernetes v1.34 Kind E2E | PASS |

## 非目标

Phase 1.2.2 不实现：

- Findings API
- Result → Finding Adapter
- 任意 Kubernetes GVR 查询
- Pod Logs
- Secret / ConfigMap 内容
- OIDC / SSO
- Portal UI
- Prometheus / Loki RCA
- Kubernetes 写操作
- Mutation / Auto Remediation

## 下一阶段：Phase 1.2.3

Phase 1.2.3 实现核心诊断数据契约：

```text
K8sGPT Result CR
        ↓
Result Adapter
        ↓
Finding Model
        ↓
GET /api/v1/findings
GET /api/v1/findings/{id}
```

这一阶段完成后，Phase 1.3 Web Portal 就可以直接基于稳定的 Finding API 开始开发。
