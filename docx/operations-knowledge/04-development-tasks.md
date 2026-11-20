# 运维知识库 · 开发任务

[PRD v1.1](00-prd.md) · [页面](01-pages.md) · [功能](02-features.md) · [用户故事](03-user-stories.md) · [测试用例](05-unit-test-cases.md) · [P0/P1 接口与上线验收](api-contract.md)

## 依赖与交付批次

| 批次 | 工程任务 | 可验证交付 |
| --- | --- | --- |
| A 范围与兼容基础 | T07、T08 | 权限矩阵、兼容数据、迁移报告与回退演练。T07 和 T08 先冻结范围及数据契约，再分别编码。 |
| B 值班发现 | T01、T02、T03、T09 的 P0 | 同一已生效集合支持搜索、详情、推荐和 AI。关闭模型可验收。 |
| C 知识产生与治理 | T05、T06、T04、T10 的 P0 | 候选、审核、发布、撤回和关联。所有入口完成适配后切换生产读写。 |
| D 增强 | T09 与 T10 的 P1 | 章节引用、版本反馈、复核提醒、同步任务记录和逐项重试。 |

A 的迁移上线切换依赖 B、C 写入和读取兼容完成，编码期间用测试库和兼容数据验证。合并前统一核对所触达的 Go、API 契约、前端、浏览器和试点用例。

## OK-T01

**搜索 API、索引与首页交互** · P0 · [OK-US01](03-user-stories.md#ok-us01) · [OK-UT01](05-unit-test-cases.md#ok-ut01)

- 依赖 T07 的统一读集合、T08 的版本数据。扩展现有知识列表接口，定义关键词、范围、排序与游标契约，保留旧页码调用。返回条目、匹配原因、计数、筛选项和下一游标。
- 代码落点 `api/internal/wiki/wiki_documents.go`、知识检索包、`web/src/views/wiki/wiki.ts`。确认当前 SQLite 驱动与中文索引可用性，以试点查询集验证精确报错和中文召回。索引重建仍读取受约束的生效集合。
- 前端设置 URL 恢复、空态、重试与防止旧请求覆盖。后端测筛选与分页结果、接口兼容和暖态 p95。

## OK-T02

**条目详情、版本与来源读接口** · P0 · [OK-US02](03-user-stories.md#ok-us02) · [OK-UT02](05-unit-test-cases.md#ok-ut02)

- 依赖 T07、T08。扩展知识详情返回当前版或显式快照、模板字段、适用和可见范围、所有受权来源。沿用现有旧版深链，默认版本改读生效版本。
- 代码落点 `api/internal/wiki/wiki_documents.go`、Wiki 路由和详情视图。验证旧链接、失权、撤回、渲染转义及反向关联过滤。

## OK-T03

**Finding 推荐与独立加载** · P0 · [OK-US03](03-user-stories.md#ok-us03) · [OK-UT03](05-unit-test-cases.md#ok-ut03)

- 依赖 T01、T07、T08。复用 Finding 身份和资源目录解析，返回有效手册、案例、命中信号与版本，最多 3 条。查询结果独立于模型状态，严格区分解析、无匹配和失败。
- 代码落点 `api/internal/diagnosis`、`api/internal/portal`、`api/internal/wiki/wiki_ask.go` 的 Finding lookup、`web/src/views/diagnosis`。测试跨实例、跨目标授权与失败展示。

## OK-T04

**Finding 与知识关联** · P0 · [OK-US04](03-user-stories.md#ok-us04) · [OK-UT04](05-unit-test-cases.md#ok-ut04)

- 依赖 T02、T03、T05、T07。定义关系唯一键、固定复核与手册版本、插入或返回现有关系。写入时复核目标当前状态、真实范围和双方权限；读取按双方权限过滤。
- 代码落点 `api/internal/identitystore`、`api/internal/diagnosis`、`api/internal/wiki`、诊断沉淀入口与知识详情。测试双击、并发、跨实例同 ID 和撤回后访问。

## OK-T05

**统一草稿、脱敏与送审核验** · P0 · [OK-US05](03-user-stories.md#ok-us05) · [OK-UT05](05-unit-test-cases.md#ok-ut05)

- 依赖 T07、T08。实现手工建稿、现有条目修订、从已保存 Finding 复核建稿、模板预览及送审。候选写入校验版本，冲突时保留用户输入。复用既有数据脱敏并补齐模板字段校验。
- 代码落点 `api/internal/diagnosis/wiki_review_publish.go`、`api/internal/wiki/wiki_documents.go`、`api/internal/wiki/wiki_files.go`、Wiki 编辑页。更新 #101 原入口。旧直接发布请求返回 `409 WIKI_DRAFT_REQUIRED`，无写入；新建草稿接口返回候选 ID 和版本。测试旧请求、范围解析和脱敏。

## OK-T06

**审核、撤回、复核与审计** · P0 · [OK-US06](03-user-stories.md#ok-us06) · [OK-UT06](05-unit-test-cases.md#ok-ut06)

- 依赖 T05、T07。定义候选状态和生效版本指针。发布事务检查预览、提交者、审核者、基线及幂等参数，提交新版本与审计；撤回事务使状态与读集合同时生效。确定审计落点及提交一致性。
- 代码落点 `api/internal/wiki`、`api/internal/identitystore`、项目现有操作审计和知识治理视图。实现驳回、放弃、责任团队更新及复核日期，覆盖双人审核、冲突、重试、事务失败及跨入口撤回。

## OK-T07

**知识授权及资源范围解析** · P0 · [OK-US07](03-user-stories.md#ok-us07) · [OK-UT07](05-unit-test-cases.md#ok-ut07)

- 无编码前置。定义可见目标、适用条件、来源锚点、状态过滤及拒绝原因。沿用现有 Wiki 能力，以实例及命名空间接入资源目录，多业务归属作为集合。发布检查全部新旧目标，撤回校验存量目标；过期或丢失目录的条目经管理员治理。
- 代码落点 `api/internal/wiki`、`api/internal/portal/resource_catalog.go`、`api/internal/portal/namespace_business.go`、授权层。禁止通过缓存、筛选聚合、历史版本、关系或问答泄露。复用同一套允许和拒绝夹具。

## OK-T08

**SQLite 迁移、索引重建与回退** · P0 · [OK-US08](03-user-stories.md#ok-us08) · [OK-UT08](05-unit-test-cases.md#ok-ut08)

- 依赖 T07 的范围契约。增量迁移条目元数据、版本快照、有效指针、来源关系、候选及索引。旧文档仅当前表存在时补齐可验证的首个快照。保留旧 ID 和远端页映射，未知归属进入治理。
- 代码落点 `api/internal/identitystore/sqlite.go` 和 Wiki 迁移逻辑。迁移按稳定键可重跑。准备库备份、数量/状态对账报告、旧直链回归、切换和回退演练。适配全部入口后才切换，验证旧服务不会绕过新约束。

## OK-T09

**知识问答的授权检索及引用** · P0/P1 · [OK-US09](03-user-stories.md#ok-us09) · [OK-UT09](05-unit-test-cases.md#ok-ut09)

- P0 依赖 T01、T07、T08。更新 `api/internal/wiki/wiki_ask.go`，问答和 Finding AI 复核共用状态过滤。模型调用前和回答返回前复核引用权限和状态；失效命中时丢弃整段回答并提示重试。
- P1 依赖 T02、T06。添加格式受控的章节引用、固定版本链接和反馈。前端处理范围切换、模型限流和超时，测试非法引用以及生成中途撤权。

## OK-T10

**导入和远端同步候选化** · P0/P1 · [OK-US10](03-user-stories.md#ok-us10) · [OK-UT10](05-unit-test-cases.md#ok-ut10)

- P0 依赖 T05、T06、T07、T08。适配 `api/internal/wiki/wiki_files.go`、`wiki_sync.go`、`web/src/views/wiki/wiki-tools.ts`。来源 ID 加页 ID 定位条目，内容哈希防重；新页、更新、失败、疑似缺失与撤权有不同结果。保持既有连接和凭据约束。
- P1 将执行记录和行级失败保留在数据库，提供按源或单页重试；重新校验身份、来源凭据和目标范围。测试重复触发、半途失败、重启和撤权。

## 追踪索引

页面引用的功能在 [功能拆解](02-features.md) 中分别追踪到故事，故事再指向本页任务，任务逐项链接测试。以下是以交付批次核对的简表。

| 范围 | 页面 | 功能、故事、任务、测试的对应尾号 |
| --- | --- | --- |
| 值班阅读 | P01、P02 | 01、02 |
| 推荐和复用 | P02、P03 | 03、04 |
| 知识编辑与审核 | P03、P04、P05 | 05、06 |
| 统一授权与迁移 | 各页、后台 | 07、08 |
| AI 引用与反馈 | P01、P02 | 09 |
| 来源同步 | P06 | 10 |

## Coding 进入条件

1. 评审通过 PRD、页面状态、权限矩阵、存量迁移和 P0 验收条目。
2. 锁定试点授权、共享目标和生产审核人。从脱敏备份提取迁移样本并准备至少 15 条带预期答案的搜索夹具。
3. Coding 按 A → B/C → 验收的顺序执行。每个 PR 标明所覆盖的故事、任务和测试编号；接口变更同步契约。

## P1 任务交付与验证

| 开发任务 | 实现位置 | 验证 |
| --- | --- | --- |
| OK-T09 | `wiki_chapters.go`、`wiki_feedback.go`、`wiki_ask.go`、`governance.ts` | 模型夹杂编造引用、调用期间撤权、切换筛选范围、固定版本章节定位、反馈去重、完成复核。 |
| OK-T10 | `wiki_runs.go`、`wiki_sync.go`、`wiki-runs.ts` | 手动和定时记录、并发冲突、重启可读、过期租约恢复、失权重试拒绝、单页与整源重试、成功候选不重复。 |
