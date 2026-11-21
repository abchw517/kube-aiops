# API 与数据模型设计

[PRD](00-prd.md) · [任务](04-development-tasks.md)

## P0 复用契约

| HTTP | 用途 | 行为 |
| --- | --- | --- |
| GET `/api/v1/clusters`、`/api/v1/clusters/{cluster}/namespaces` | 授权范围与筛选项 | 沿用现有发现接口和权限。 |
| GET `/api/v1/findings`、`/api/v1/findings/summary` | 总览、列表 | `cluster=*` 表示授权范围聚合。复用 `environment/business/group/namespace/kind/severity/problem`，列表额外接受 `limit/continue`。 |
| GET `/api/v1/findings/{id}`、`/api/v1/findings/{id}/correlation` | 详情与关联证据 | 必须携带选中实例 `cluster`；旧直链保留本地实例默认项。 |
| 现有 Finding 复核及 Wiki 接口 | 结论生成、保存与发布 | 继续使用原授权和持久化。后续 RCA 任务与旧复核版本通过来源引用互通。 |

## P1–P3 目标契约

以下路径均以 `/api/v1/diagnostics` 为前缀。P1 可用契约以 [OpenAPI](../../api/openapi.yaml) 为准。采集端推送和云资产映射随相关连接器交付。

| 批次 | HTTP | 请求及响应 | 目录动作建议 |
| --- | --- | --- | --- |
| P1 | GET `/overview`、`/incidents` | `environmentId,businessId,clusterId,platform,namespace,severity,status,source,q,from,to,limit,continue`。汇总返回 `counts,trend,byPlatform,coverage`，列表返回 `items,pagination,coverage`。 | `diagnosis.incident.list` |
| P1 | POST `/incidents` | `title,scope,resourceId,severity,description`，`Idempotency-Key`，返回 201 和 `Location`。 | `diagnosis.incident.create` |
| P1 | GET `/incidents/{id}`、`/{id}/timeline`、`/{id}/evidence` | 稳定故障 ID。详情返回 `incident,counts,coverage,version`。时间线及证据返回游标分页；证据列表含摘要及状态。 | `diagnosis.incident.read`、`diagnosis.evidence.read` |
| P1 | GET `/incidents/{id}/evidence/{evidenceId}` | 校验故障范围与证据自身范围后返回已脱敏明细、来源、时间。 | `diagnosis.evidence.read` |
| P1 | PATCH `/incidents/{id}` | `If-Match`，`status,ownerGroupId,severity,reason`。成功返回版本递增后的故障。 | `diagnosis.incident.update` |
| P1 | POST `/incidents/{id}/collect` | 使用 `If-Match`。按故障资源锚点调用现有四类证据连接器，返回采集后的故障版本。 | `diagnosis.incident.correlate`、`diagnosis.evidence.read` |
| P1 | POST `/incidents/{id}/merge`、`/{id}/split` | 提交源/目标版本或成员 ID 与原因，事务内调整归属并审计。 | `diagnosis.incident.correlate` |
| P3 | POST `/ingress/{connectorId}/signals` | 采集端身份绑定至数据源。返回 202，重复和乱序可安全重放。 | 采集端专用接口权限 |
| P2 | POST/GET `/incidents/{id}/rca-runs`、GET `/incidents/{id}/rca-runs/{runId}` | 发起传 `observation,evidenceIds` 及幂等键，返回 202 和任务位置。轮询进度、正文和引用。 | `diagnosis.rca.run`、`diagnosis.rca.read` |
| P2 | POST `/incidents/{id}/rca-runs/{runId}/cancel`、PATCH `/incidents/{id}/rca-runs/{runId}/conclusion` | 取消排队或运行任务。已完成结论以 `If-Match` 编修并版本化。 | `diagnosis.rca.cancel`、`diagnosis.rca.save` |
| P2 | GET `/incidents/{id}/topology` | `depth` 默认 2，最大 4；`nodes,edges,coverage,truncated`。 | `diagnosis.topology.read` |
| P3 | GET/POST `/datasources`、PATCH `/datasources/{id}`、POST `/datasources/{id}/test` | 列表、创建、配置/启停和连接验证。读响应返回脱敏配置、健康和更新时间。更新使用 `If-Match`。 | `diagnosis.connector.read`、`diagnosis.connector.write`、`diagnosis.connector.test` |
| P3 | GET/POST `/policies`、PATCH `/policies/{id}` | 维护版本化条件、作用域、模板和启用状态。更新使用 `If-Match`。 | `diagnosis.policy.read`、`diagnosis.policy.write` |

表内简写的 `/{id}` 继承同一行的 `/incidents`。查询时间使用 RFC 3339，存 UTC，展示按用户时区。故障列表以 `lastSeen DESC, id DESC` 排序，游标与查询条件绑定，默认 50、最大 100。集合在授权后分页和统计。覆盖状态用 `complete,partial,unavailable`，受限范围的分母仅含当前用户可见来源。

错误继续使用 `{error:{code,message}}`。参数错误 400，缺少会话 401，无业务功能或目标范围权限 403，不可见的子资源统一返回 404，资源版本不匹配 412，业务状态冲突或幂等键冲突 409，缺少必要版本 428，限流 429，上游不可用或超时 502/504。响应错误不包含凭据与上游原文。

## 故障契约示例

```http
POST /api/v1/diagnostics/incidents
Content-Type: application/json
Idempotency-Key: example-1

{
  "title": "订单服务延迟升高",
  "scope": {"environmentId": "prod", "businessId": "orders", "clusterId": "ack-orders-01", "namespace": "prod-order"},
  "resource": {"kind": "Deployment", "name": "order-api"},
  "severity": "P1",
  "description": "网关及应用接口 P99 持续升高"
}
```

```http
HTTP/1.1 201 Created
Location: /api/v1/diagnostics/incidents/01INCDEMO01
ETag: "1"
Content-Type: application/json

{
  "id": "01INCDEMO01",
  "displayKey": "INC-2026-00128",
  "status": "pending",
  "severity": "P1",
  "source": "manual",
  "version": 1
}
```

更新时携带 `If-Match: "1"` 和 `{"status":"investigating","reason":"值班人员已确认告警"}`。响应返回 `ETag: "2"`。故障详情中的证据摘要用 `id,source,kind,observedAt,collectedAt,summary,state` 表示；客户端通过明细端点延迟读取正文。RCA 任务状态为 `queued,running,succeeded,failed,timed_out,canceled`，终态保存模型、完成时间和证据快照编号。

## 存储与索引

| 实体 | 主键及关键字段 | 约束 / 索引 |
| --- | --- | --- |
| `diagnostic_resource` | `id, environment_id,business_id,platform,provider_account,region,cluster_id,namespace,kind,external_id` | 平台、账号、区域和外部 ID 组成云资产自然键；K8s 使用实例 ID 和资源 UID。 |
| `diagnostic_connector` | `id, kind, name, scope_ref, config_json, secret_ref, enabled, version, health, last_sync_at` | 范围内连接器名字唯一。存 Secret 引用。 |
| `diagnostic_signal` | `id,connector_id,external_event_id,scope_ref,resource_id,event_type,state,severity,first_seen,last_seen,ingested_at,fingerprint` | 源事件 ID 存在时用 `(connector_id,external_event_id)` 防重；否则用源实例、资源身份、规则及原始时间组合的稳定指纹。按资源和时间建索引。 |
| `diagnostic_incident` | `id,display_key,scope_ref,primary_resource_id,title,severity,status,owner_group_id,first_seen,last_seen,version` | `display_key` 唯一，`(scope_ref,status,last_seen,id)` 建索引。手工故障允许零条信号。 |
| `diagnostic_incident_signal` | `incident_id,signal_id,linked_at,relation,confidence` | 活跃成员关系保证信号最多归属一个故障。合并、拆分及历史轨迹一起提交。 |
| `diagnostic_evidence` | `id,incident_id,signal_id,scope_ref,source,kind,observed_at,collected_at,digest,sanitized_content,expires_at` | 证据引用指向不可变快照。按故障和观测时间索引。明细过期后保留引用元数据。 |
| `diagnostic_timeline` | `id,incident_id,occurred_at,actor_ref,kind,payload` | 记录状态、归并、诊断和观察变化。按故障和时间分页，并对关联内容做同等授权。 |
| `diagnostic_rca_run` | `id,incident_id,run_no,requested_by,state,observation,evidence_set_ref,policy_version,started_at,ended_at,error_code` | `(incident_id,run_no)` 唯一，同一故障每次仅一项活动任务。队列按租约和 fencing token 提交。 |
| `diagnostic_rca_revision` | `id,run_id,revision,judgment,confidence,references,gaps,next_steps,created_by,created_at` | `(run_id,revision)` 唯一。保存结论时确认调用者仍可读引用。 |
| `diagnostic_dependency` | `src_id,dst_id,relation,source_ref,confidence,last_seen,expires_at` | `(src_id,dst_id,relation,source_ref)` 唯一，按两端资源键查询。 |
| `diagnostic_policy` | `id,scope_ref,version,match_json,template_ref,enabled,cooldown_seconds,max_concurrency` | 每轮执行引用当时策略版本。触发以策略 ID、资源 ID 与故障条件去重。 |

`scope_ref` 引用服务端资源目录解析出的环境、业务、集群或主机数据范围。P1 以不可变 ID 建关系。名称用于展示或兼容既有查询。跨平台依赖经过授权后单独记录，缺省禁止跨环境自动合并。

创建状态为 `pending`。允许 `pending → investigating → recovered → closed`，已恢复可以转回处理中并记录原因。已关闭为终态，复发创建新故障并引用前序记录。状态更新、时间线、成员变更与审计投递记录在同一事务提交。审计使用已有身份和事件格式，失败可重放。幂等键按调用方、动作、目标与输入摘要管理。模型任务完成写入时再次确认租约、当前任务状态和引用可见性。首轮上线只启用现有来源；策略上限和保留期由生产试点数据确定。
