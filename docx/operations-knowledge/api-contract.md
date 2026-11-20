# 运维知识库 P0 接口与上线验收

[PRD](00-prd.md) · [开发任务](04-development-tasks.md) · [测试用例](05-unit-test-cases.md)

管理及 Wiki 接口沿用 Portal 会话。所有写请求遵循现有同源检查和范围授权。诊断证据公开 API 继续由仓库 OpenAPI gate 校验。以下接口由 Wiki 会话路由注册。

| 接口 | 请求与返回 |
| --- | --- |
| `GET /api/v1/wiki/knowledge` | `q`、环境、业务线、资源类型、知识类型、`page` 或 `cursor`。返回有权生效条目、匹配原因、总数、可见筛选项和下一页游标。 |
| `GET /api/v1/wiki/knowledge/{id}[/revisions/{revision}]` | 缺省读生效版；显式版本返回允许访问的快照。关联诊断及范围按当前用户权限投影。普通入口使用相同响应表示不存在、已撤回和不可见。 |
| `GET/POST /api/v1/wiki/candidates` | 查询范围内候选；用 `document` 创建草稿。修订另传 `knowledgeId`、`baseRevision`。返回候选 ID、基础版本、当前候选版本和状态。 |
| `GET/POST /api/v1/wiki/candidates/{id}` | 查询或提交 `action`、`version`、可选 `document`、`reason`。支持保存、脱敏预览、送审、驳回、放弃和发布。发布用候选 ID 和期望版本防重。 |
| `GET /api/v1/wiki/governance` | 返回操作者可治理的生效、待复核和撤回条目。 |
| `GET/POST /api/v1/wiki/knowledge/{id}/governance` | 读取治理视图；用 `action`、`revision` 执行撤回或完成复核。复核需提供新日期，可同时维护责任团队。 |
| `GET/POST /api/v1/wiki/finding?id=…&cluster=…` | 返回 Finding 的匹配知识、已关联手册和范围状态。用已保存的 `reviewRevision` 创建草稿，或用 `knowledgeId`、`wikiRevision` 指定要关联的手册。 |
| `DELETE /api/v1/wiki/sources/{id}` | 停用来源，通过状态屏蔽分发和后续同步，保留关联及版本。 |
| 旧直接发布请求 | 返回 HTTP 409 和 `WIKI_DRAFT_REQUIRED`，通过候选入口完成后续送审。 |

## 升级与验证

- 在变更窗口前备份 SQLite，使用升级副本先跑开库迁移和旧链接回归。新增候选、发布状态、关联等表与旧数据分开。已有条目保留原 ID 与授权范围，缺少复核记录的历史条目在浏览中可读，在模型与诊断推荐中排除。
- 发布和撤回在一个事务提交版本或状态及成功审计；失败后重查候选版本或生效状态，再重试。停用远端来源后检查相关条目的普通直链、问答与同步。
- 生产试点还需使用真实资源目录、两位生产审核人、已脱敏的存量数据库和真实 Wiki 来源完成验收。回退前核对新建版本、撤回和审计变更，防止旧服务重新分发已撤回知识。

## 本地验证记录

- Go 全量测试、OpenAPI 与生成客户端检查、前端 lint／类型检查／单测／构建及 Chromium 端到端测试通过。
- API 集成覆盖生产双人审核、幂等发布、Finding 复核版本绑定、知识关联去重、共享手册的目标投影和撤回。浏览器覆盖从诊断创建草稿、送审、查找文档和故障复核回归。
- 三类试点模拟故障各 5 条固定查询均命中首位。1 万条合成文档、100 次暖态检索，Intel Xeon Platinum 8573C 上检索核心 p95 为 169.6 ms。检索采用 SQLite 读取及应用层匹配排序。该结果使用本地授权与数据夹具，线上性能以真实目录及权限联调结果为准。
