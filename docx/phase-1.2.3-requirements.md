# Phase 1.2.3 实现需求：Finding Domain

## 1. 阶段目标

Phase 1.2.3 在 Phase 1.2.2 Kubernetes ReadOnly API 基础上建立稳定的诊断领域模型，将 K8sGPT `Result` CR 转换为 Portal 可直接消费的 `Finding`，形成以下闭环：

```text
K8sGPT Result CR
        ↓
Result Adapter
        ↓
Finding Domain Model
        ├── GET /api/v1/findings
        ├── GET /api/v1/findings/{id}
        └── GET /api/v1/findings/summary
```

本阶段是 Phase 1.3 Web Portal 的核心数据前置层。Portal 不得解析 K8sGPT CRD，也不得依赖 K8sGPT 原始字段结构。

## 2. 数据源边界

### 2.1 只读取当前 K8sGPT 实例

Backend 只接受同时具备以下标签的 Result：

```text
k8sgpts.k8sgpt.ai/name=k8sgpt-engine
k8sgpts.k8sgpt.ai/namespace=k8sgpt-operator-system
```

其它 K8sGPT 实例生成的 Result 必须隔离，不能通过 List、Detail 或 Summary 泄露。

### 2.2 Result 字段兼容策略

当前 K8sGPT Result `spec` 同时存在 legacy 字段与 `targetRef`。Adapter 必须：

1. 优先使用 `spec.targetRef.apiVersion/kind/namespace/name`；
2. 当 `targetRef` 缺失时使用 `spec.kind` 与 `spec.name`；
3. legacy `spec.name` 若为 `namespace/name`，拆分为资源 namespace/name；
4. 字段缺失时安全降级，不得 panic；
5. 不把 K8sGPT Result 原始对象透传给 HTTP 客户端。

## 3. 安全要求

K8sGPT `Failure` 中存在 `Sensitive` 信息，包括 masked/unmasked 数据。Phase 1.2.3 必须遵守：

```text
Failure.Text        可进入 Finding.problem
Failure.Sensitive   禁止进入内部 Finding DTO
Sensitive.unmasked  禁止读取/返回/记录
Sensitive.masked    本阶段也不对 Portal 暴露
```

同时继续保持：

- 不读取 Kubernetes Secret；
- 不读取 `pods/log`；
- 不增加任何 create/update/patch/delete 权限；
- 不返回 Raw Result CR；
- 不返回 Raw Kubernetes Object；
- 不记录 ServiceAccount Token、kubeconfig 或 Provider Token；
- Backend 仍然只使用 Phase 1.2.2 已有 Result `get/list/watch` 权限，本阶段不扩大 RBAC。

## 4. Finding Model

```json
{
  "id": "result-object-name",
  "cluster": "local",
  "namespace": "dev-base",
  "severity": "warning",
  "resource": {
    "apiVersion": "v1",
    "kind": "Pod",
    "namespace": "dev-base",
    "name": "java-demo-xxx"
  },
  "problem": "CrashLoopBackOff",
  "details": "AI analysis text",
  "source": "k8sgpt",
  "createdAt": "2026-09-01T08:00:00Z"
}
```

字段语义：

- `id`：Result `metadata.name`，作为 Finding Detail 的稳定 API 标识；
- `cluster`：Phase 1 当前固定为 `local`；
- `namespace`：目标 Kubernetes 资源 Namespace；
- `resource`：由 `targetRef` 优先映射；
- `problem`：第一个非空 `spec.error[].text`，缺失时使用中性默认描述；
- `details`：`spec.details`；
- `source`：固定 `k8sgpt`；
- `createdAt`：Result `metadata.creationTimestamp`。

## 5. Severity 策略

K8sGPT Result 当前没有权威 Severity 字段。Phase 1.2.3 不允许根据字符串、Kind 或 AI 文本自行推断 `critical/info`。

因此本阶段采用稳定基线：

```text
K8sGPT Finding severity = warning
```

API Schema 预留 `critical/warning/info`，后续 Phase 2 在 Prometheus、Alertmanager、事件和日志关联后再做风险等级增强。

## 6. Finding List API

接口：

```http
GET /api/v1/findings
```

支持 Query：

```text
cluster
namespace
kind
severity
problem
limit
continue
```

规则：

- 默认 `limit=50`；
- 最大 `limit=200`；
- `continue` 为 opaque cursor；
- 排序固定为 `createdAt DESC, id ASC`；
- 分页 cursor 使用 keyset 语义，避免新 Finding 插入导致 offset 分页明显漂移；
- `problem` 使用大小写不敏感的子串过滤；
- `kind`、`severity` 使用大小写不敏感匹配；
- `namespace` 使用精确匹配；
- 未知 cluster 返回 404。

为了控制 Backend 内存和 Kubernetes API 压力，单次请求最多扫描 5000 个当前实例 Result，超过上限返回明确的服务错误，不允许无限制加载。

## 7. Finding Detail API

接口：

```http
GET /api/v1/findings/{id}
```

要求：

- 使用 Result `metadata.name` 查询；
- 查询后再次验证 K8sGPT instance 标签；
- foreign Result 即使 ID 可猜测，也必须表现为 404；
- 不返回 Result labels/spec/status 原始结构；
- Kubernetes 原始错误不得返回浏览器。

## 8. Finding Summary API

接口：

```http
GET /api/v1/findings/summary
```

支持与 Finding List 完全一致的业务 Filter：

```text
cluster
namespace
kind
severity
problem
```

Summary 不接受 List pagination 语义。

响应至少包含：

```json
{
  "total": 2,
  "bySeverity": {
    "critical": 0,
    "warning": 2,
    "info": 0
  },
  "byKind": {
    "Pod": 1,
    "Deployment": 1
  },
  "byNamespace": {
    "dev-base": 1,
    "kube-aiops-system": 1
  }
}
```

List 和 Summary 必须调用同一套 Result Adapter 和 Filter 判断，禁止实现第二套 Result Parser。

## 9. HTTP 错误模型

继续使用统一安全错误结构：

```json
{
  "error": {
    "code": "FINDING_NOT_FOUND",
    "message": "finding not found"
  }
}
```

至少覆盖：

- `CLUSTER_NOT_FOUND` → 404；
- `FINDING_NOT_FOUND` → 404；
- `INVALID_LIMIT` → 400；
- `INVALID_CONTINUE_TOKEN` → 400；
- `FINDING_SET_TOO_LARGE` → 503；
- Kubernetes/Result API 非预期错误 → 502，且不得透传原始错误正文。

## 10. Kubernetes Client 要求

- 继续使用 Phase 1.2.2 统一 TLS/CA/ServiceAccount Token Client；
- Result List 使用 Kubernetes 原生 `metadata.continue` 分页读取；
- 每批 Result List 建议 `limit=200`；
- 对外 Finding pagination 与内部 Kubernetes pagination 解耦；
- 只允许当前 K8sGPT instance labelSelector；
- Kubernetes 响应读取必须存在显式大小上限。

## 11. E2E 验收

Phase 1.2.3 必须接入现有 `Kubernetes v1.34 Kind E2E` Required Check。

在独立 Kind 集群创建：

1. 当前实例 Pod Result；
2. 当前实例 Deployment Result；
3. foreign K8sGPT instance Result；
4. Result Failure 中放入测试用 `sensitive.unmasked` 字段。

必须验证：

- List 只返回当前实例的 2 条 Finding；
- foreign Result 不可见；
- `Sensitive/unmasked` 内容绝不出现在响应；
- Raw `spec/labels/status` 不出现在 Finding 响应；
- namespace/kind/severity/problem Filter 正确；
- `limit=1` 返回 continue cursor；
- continue 能获取下一页且无重复；
- Detail 可查询当前实例 Finding；
- foreign Result Detail 返回 404；
- Summary total/bySeverity/byKind/byNamespace 正确；
- 带 Filter 的 Summary 与 List 语义一致；
- 原有 Cluster/Namespace/Resource API 回归继续通过；
- Secret、Pod Logs、Kubernetes write 仍保持 DENY。

## 12. 验收矩阵

| 检查项 | 要求 |
|---|---|
| Result → Finding Adapter | PASS |
| targetRef 优先 | PASS |
| legacy fallback | PASS |
| Sensitive 不进入 DTO | PASS |
| Current instance 隔离 | PASS |
| Finding List | PASS |
| Finding Detail | PASS |
| Finding Summary | PASS |
| Filter 一致性 | PASS |
| Keyset continue | PASS |
| Result scan 上限 | PASS |
| Go format/vet/test/build | PASS |
| Docker smoke | PASS |
| Gitleaks | PASS |
| RBAC exact-set | PASS |
| Kubernetes v1.34 Kind E2E | PASS |
| Secret / pods/log | DENY |
| Kubernetes write | DENY |

## 13. 非本阶段范围

以下内容明确不进入 Phase 1.2.3：

- OpenAPI Contract；
- TypeScript Client Generator；
- React/Web Portal；
- OIDC/SSO；
- Namespace 用户级授权；
- Prometheus/Loki/Alertmanager 关联；
- Severity 智能推断；
- Pod Logs；
- Mutation/Auto Remediation。

OpenAPI + TypeScript Client Contract Gate 由 Phase 1.2.4 独立实现。
