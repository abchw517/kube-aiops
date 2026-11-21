# 诊断中心 V2.0 P1

## 运维人员操作

1. 在「故障事件」打开「人工创建故障」，选择已授权的集群实例和命名空间，填写标题、级别和描述。可选填 Pod 或 Deployment。也可从现有 Finding 详情创建故障并保留来源链接。
2. 共享命名空间需要指定一个已登记且获授权的业务 ID。单一业务归属由服务端解析。生产和预发命名空间须完成业务归属。
3. 在详情展开「更新故障」，填写变更说明并提交。状态从待确认进入处理中、已恢复和已关闭。已恢复可转回处理中。详情时间线记录操作者、状态和原因。
4. 点击「采集证据」，系统按现有连接配置调用 Kubernetes 事件、Prometheus、Alertmanager 和 VictoriaLogs。资源一致且处在采集窗口内的信号由当前故障接收。已有归属的信号保留原关联。每类来源显示可用、部分可用、不可用或未启用。
5. 在详情选择同资源故障并归并，或选中证据快照拆分出新故障。拆分会移动该信号及其已有版本。归并后的来源故障可跳转至目标。归并要求来源与目标的资源身份和授权范围一致。

## 部署与授权

- 部署沿用持久化的 `AUTH_SQLITE_PATH`。启动时事务性创建 P1 的表与索引。数据库目录与 SQLite 写入权限按现有 Portal 部署管理。
- 按需设置 API 进程的 `DIAGNOSTIC_INCIDENT_ENVIRONMENTS`，值为以逗号分隔的稳定环境 ID，例如 `development,testing`。空值允许所有已登记环境。移出环境后，相关故障保留在数据库中，可在重新启用后读取。
- 平台管理员使用现有权限目录授予 `diagnosis.incident.list`、`diagnosis.incident.read`、`diagnosis.incident.create`、`diagnosis.incident.update` 和 `diagnosis.incident.correlate`。证据查询和采集另需 `diagnosis.evidence.read`。角色范围与故障实际的实例、命名空间、环境和业务归属匹配。
- 采集使用既有只读连接器。P1 适用已有登记的 Kubernetes 资源，资产型数据源和独立连接器推送认证仍按后续批次安排。真实来源的连通性与字段映射在试点环境验收。

## 写入与追溯

| 路径 | 约束 |
| --- | --- |
| POST `/api/v1/diagnostics/incidents` | 首次创建返回 201。同一调用方、相同键和请求体重试返回 200 及原故障 ID。键相同且输入变化返回 409。 |
| PATCH `/api/v1/diagnostics/incidents/{id}` | 使用当前版本的 `If-Match`。过期返回 412，非法流转返回 409。 |
| POST `/api/v1/diagnostics/incidents/{id}/collect` | 使用 `If-Match`。服务端按资源锚点调用原有相关性服务，经脱敏和权限复查后持久化。 |
| POST `/api/v1/diagnostics/incidents/{id}/merge` 与 `/split` | 使用目标故障版本，归并另传源版本。移动信号、快照与记录变更在一个 SQLite 事务中提交。 |

故障更新、时间线与操作审计同提交。操作审计遵循平台现有保留配置。相同来源记录重放按提供方标识及资源范围去重。Prometheus 采样按观测时间区分，Alertmanager 按指纹与本轮告警开始时间区分。较旧观测保留已写入的较新信号状态。

## 验收范围

契约、行为与权限由 Go 测试覆盖。浏览器使用测试数据验证人工创建、版本提交、状态更新、时间线和证据折叠。生产试点仍需验证四个连接器的真实端点、凭据权限及命名空间映射。

[PRD](00-prd.md) · [开发任务](04-development-tasks.md) · [测试用例](05-unit-test-cases.md) · [OpenAPI](../../api/openapi.yaml)
