# Phase 1.2.4 实现需求：API Contract Closure

## 1. 阶段目标

Phase 1.2.4 是 Phase 1.2 Portal Backend 的收口阶段。目标不是继续扩展业务 API，而是把 Phase 1.2.1～1.2.3 已经实现的只读 HTTP API 固化为可验证、可生成、不可静默漂移的稳定契约，为 Phase 1.3 Web Portal 提供唯一可信接口来源。

```text
Go HTTP Handler / DTO
        ↓
api/openapi.yaml
        ↓
Contract Validation
        ├── Route Drift Guard
        ├── Go DTO Schema Drift Guard
        ├── Sensitive Field Guard
        └── OpenAPI Structural Validation
        ↓
Deterministic TypeScript Client Generator
        ↓
clients/typescript/generated.ts
        ↓
Generated Client Drift Check
        ↓
Existing Required Check: Preflight / Lint / RBAC
```

只有 Phase 1.2.4 PR 合并且合并后的 `main` 三个 Required Checks 再次全绿，Phase 1.2 才允许标记为 Completed。

## 2. OpenAPI 作为外部契约基线

新增：

```text
api/openapi.yaml
```

要求：

- 使用 OpenAPI 3.1；
- 覆盖当前全部 8 个 GET 路由；
- 不新增写接口；
- 明确定义 path/query 参数、成功响应和安全错误响应；
- Finding List 的 `items` 必须是 required array，空集合语义固定为 `[]`；
- Severity 固定声明 `critical | warning | info`；
- 当前 cluster 仍只有 `local`；
- 不在本阶段引入 OIDC/OAuth2 security scheme，认证授权由 Phase 1.4 实现。

## 3. 当前契约范围

必须覆盖：

```http
GET /healthz
GET /readyz
GET /api/v1/clusters
GET /api/v1/clusters/{cluster}/namespaces
GET /api/v1/clusters/{cluster}/resources/{kind}/{namespace}/{name}
GET /api/v1/findings
GET /api/v1/findings/summary
GET /api/v1/findings/{id}
```

本阶段禁止为了“完善 OpenAPI”而新增 Portal Backend 业务能力。

## 4. Contract Validation

新增：

```text
tools/openapi/contract.py
tools/openapi/requirements.txt
```

校验必须同时覆盖：

1. 使用 `openapi-spec-validator` 对 OpenAPI 3.1 文档做结构校验；
2. 从 `internal/httpapi/server.go` 自动提取 `METHOD + PATH`；
3. OpenAPI route set 与 Go Handler route set 必须完全一致；
4. 所有 operation 必须有唯一 `operationId`；
5. 关键 Go DTO 的 JSON 字段与 OpenAPI Schema property set 必须完全一致；
6. Go `omitempty` 与 OpenAPI `required` 必须一致；
7. Go Severity 常量与 OpenAPI Severity enum 必须一致；
8. FindingPage `items` 必须保持 required array。

关键 DTO 映射至少包括：

- `kubernetes.Cluster` → `Cluster`；
- `kubernetes.Namespace` → `Namespace`；
- `kubernetes.ResourceStatus` → `ResourceStatus`；
- `kubernetes.ResourceDetail` → `ResourceDetail`；
- `finding.ResourceRef` → `ResourceRef`；
- `finding.Finding` → `Finding`；
- `finding.Pagination` → `Pagination`；
- `finding.Page` → `FindingPage`；
- `finding.Summary` → `FindingSummary`。

## 5. Sensitive Field Guard

OpenAPI 与生成的客户端都不得暴露 K8sGPT Result 原始敏感字段或原始对象入口。

至少禁止以下 schema property：

```text
sensitive
unmasked
masked
rawResult
rawObject
serviceAccountToken
providerToken
kubeconfig
```

注意：该 Guard 保护的是 Portal 外部契约，不改变 Kubernetes 内部 K8sGPT CRD 结构。

## 6. TypeScript Client Generation

生成物固定为：

```text
clients/typescript/generated.ts
```

生成原则：

- 只从 `api/openapi.yaml` 生成；
- 生成过程确定性；
- 文件头记录 OpenAPI SHA256；
- 生成 DTO 类型；
- 生成 path/query 参数类型；
- 生成只读 `KubeAIOpsApiClient`；
- 所有 path segment 使用 `encodeURIComponent`；
- 所有 query 参数通过 `URLSearchParams` 构造；
- 非 2xx 响应统一抛出 `ApiError`；
- 生成文件禁止人工修改。

本阶段不创建 React hook、状态管理、UI 组件或业务页面。

## 7. Contract Drift Check

CI 必须重新根据 OpenAPI 在内存中生成 TypeScript Client，并与 Git 中提交的 `generated.ts` 做逐字节比较。

出现以下任一情况必须失败：

- OpenAPI 改了但客户端未重新生成；
- 客户端被人工修改；
- Go route 与 OpenAPI route 不一致；
- Go DTO JSON 字段与 OpenAPI Schema 不一致；
- `omitempty` 与 `required` 不一致；
- Severity enum 漂移；
- 敏感字段进入外部契约。

## 8. Makefile 工程入口

新增：

```bash
make api-contract-validate
make api-client-generate
make api-client-drift-check
make api-contract-check
```

其中：

- `api-contract-validate`：结构 + route + DTO + sensitive 校验；
- `api-client-generate`：重新生成 TypeScript Client；
- `api-client-drift-check`：验证生成物未漂移；
- `api-contract-check`：Phase 1.2.4 完整 Contract Gate。

## 9. CI 门禁

不新增绕过现有 Ruleset 的独立弱检查。Contract Gate 直接加入现有 Required Check：

```text
Preflight / Lint / RBAC
```

因此 Phase 1.2.4 一旦出现契约漂移，现有 Required Check 必须变红，PR 不能合并。

CI 需固定安装：

```text
PyYAML==6.0.2
openapi-spec-validator==0.7.2
```

## 10. 安全边界

Phase 1.2.4 不扩大任何 Kubernetes RBAC：

- Secret：继续 DENY；
- `pods/log`：继续 DENY；
- Pod list/watch：继续按 Phase 1.2.2 既有边界；
- create/update/patch/delete：继续 DENY；
- 不增加 Mutation；
- 不增加 Auto Remediation；
- 不把 Raw Result CR/Raw Kubernetes Object 加入 OpenAPI。

## 11. 回归要求

Phase 1.2.4 不能破坏既有行为：

- Go format/vet/test/build 继续 PASS；
- Docker backend smoke 继续 PASS；
- Secret Scan 继续 PASS；
- RBAC exact-set 继续 PASS；
- Kubernetes v1.34 Kind E2E 继续 PASS；
- Phase 1.2.3 Finding List/Detail/Summary E2E 继续 PASS。

## 12. 验收矩阵

| 检查项 | 要求 |
|---|---|
| OpenAPI 3.1 structural validation | PASS |
| 8 个 GET 路由完整覆盖 | PASS |
| Server ↔ OpenAPI route drift | NONE |
| Go DTO ↔ Schema field drift | NONE |
| Go omitempty ↔ required drift | NONE |
| Severity enum drift | NONE |
| Sensitive field guard | PASS |
| Deterministic TypeScript generation | PASS |
| Generated client drift | NONE |
| Finding empty items contract | `[]` |
| Go fmt/vet/test/build | PASS |
| Docker smoke | PASS |
| Gitleaks | PASS |
| RBAC exact-set | PASS |
| Kubernetes v1.34 Kind E2E | PASS |
| Kubernetes write | DENY |
| Secret / pods/log | DENY |

## 13. Phase 1.2 完成门禁

Phase 1.2.4 完成后仍不能立即宣布 Phase 1.2 Completed。必须按顺序满足：

```text
phase-1.2.4-api-contract implementation complete
        ↓
PR three Required Checks green
        ↓
Merge to main
        ↓
main three Required Checks green again
        ↓
Phase 1.2 = Completed
        ↓
Start Phase 1.3 Web Portal
```

## 14. 非本阶段范围

- React / Web Portal 页面；
- OIDC / SSO；
- 用户、角色、Namespace 授权；
- Prometheus / Loki / Alertmanager RCA；
- Severity 智能推断；
- Pod Logs；
- Mutation / Auto Remediation；
- 写操作 API。
