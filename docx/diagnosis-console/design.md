# 诊断控制台 · 设计方案

[PRD](00-prd.md) · [页面拆解](01-pages.md) · [功能拆解](02-features.md) · [用户故事](03-user-stories.md) · [开发任务](04-development-tasks.md) · [测试用例](05-unit-test-cases.md) · [交互原型](prototype.html) · [文档维护规则](../development-standard.md)

覆盖诊断总览、故障事件、原有诊断结果、证据与复核、拓扑、AI 分析和诊断策略。总览采用聚合后的全量统计与最近诊断分页，数据源配置归[资源管理](../resource-management/design.md)。

## 最近诊断分页

[详情与 AI 调查交互原型](recent-diagnosis-investigation.html)

最近诊断使用同一组紧凑记录布局，窄屏自动换行。问题标题、查看详情与调查操作一致地打开右侧工作台。原始详情、完整页面链接及真实权限继续使用原有接口和路由。

[分页可点击原型](recent-diagnostics.html#/diagnostics)

保持默认 5 条，以授权范围内已获取、合并去重和排序后的完整记录为分页输入。桌面记录和窄屏堆叠布局共用同一数据切片。底栏沿用页码、前后翻页和自定义每页条数能力，使用该工作区的圆角控件样式。切换页码更新地址并局部刷新记录，趋势与严重度统计继续使用完整数据。筛选和页容量变化回第一页，趋势窗口变化保留当前记录页。关闭侧边工作台时保留筛选、页码和触发按钮焦点，打开完整详情的链接携带返回查询。

排序以首次发现时间倒序，时间相同依次按来源、集群和记录 ID 排序。源端覆盖受限时说明「已获取」，并沿用现有覆盖与重试提示。分页范围为该次总览已获取的记录，来源端的拉取边界由现有诊断服务管理。

## 统一记录集与统计口径

实施版 · 2026-11-24

### 1. 页面与导航

| 入口 | 设计 | 兼容 |
| --- | --- | --- |
| 诊断控制台 → 诊断总览 | 故障事件、K8sGPT Finding、Zabbix 问题与 Alertmanager 告警共同进入总览的指标、趋势、级别分布和最近记录。一套筛选控制整页。 | 沿用 `#/diagnostics`。 |
| 诊断控制台 → 原有诊断结果 | 保留完整列表、统计、筛选、分页和详情。总览中的 K8sGPT 记录先打开工作台，工作台提供完整诊断结果入口。 | 沿用 `#/findings`、`#/findings/:id`。故障事件工作台提供现有故障详情入口。 |
| 资源管理 → 平台目录 → 数据源管理 | 顶栏、侧栏和面包屑归属资源管理。复用配置、编辑与启停、测试同步、可见资产、分页和资产转故障。 | 新地址 `#/resources/datasources`，旧地址 `#/diagnostics/datasources` 以替换导航跳转，保留查询参数和分页游标。 |
| 诊断控制台 → 诊断策略 | 仍在诊断配置分组。 | 沿用 `#/diagnostics/policies`。 |

### 2. 诊断总览页面与统计口径

[总览 UI 重构设计](design.md#诊断趋势与级别分布)定义现行布局。分析卡在指标下方并先于最近诊断。切换 7、14、30 天时，仅趋势从同一数据快照中选取日桶。

| 原有区域 | 集成方式 |
| --- | --- |
| 顶部范围 | 统一提供来源、环境、业务线、集群实例、诊断级别及适用的来源状态筛选。默认用各来源当前授权范围。指定实例时，故障查询对应 `clusterId`，Finding 查询对应 `cluster`。错误或未授权实例按受限处理。故障状态等专属筛选仍在故障事件页。 |
| 指标 | 一组「诊断记录总数、严重、警告、信息」卡片。故障 P1/P2/P3 分别归入严重/警告/信息，Finding 使用原级别。已关联记录采用故障级别。原故障业务状态计数可保留在故障事件页，并在总览以简洁计数辅助阅读。 |
| 趋势与分布 | 诊断趋势按首次发现时间合桶，每日柱体用颜色显示故障记录、K8sGPT、Zabbix 与 Alertmanager 的贡献，柱体高度代表合计。级别分布与指标采用同一记录集合；统计以查询时可见数据为准。趋势缺少历史覆盖时明确标注，缺失时间的记录可进入总数并单独提示未计入趋势。 |
| 最近诊断 | 故障、K8sGPT、Zabbix 和 Alertmanager 使用同一记录列表，按首次发现倒序分页，默认每页 5 条。来源列标明每条记录的来源，保留实例和状态。独立 Finding 显示「—」作为处置状态；故障继续使用真实状态。链接用记录自身的实例 ID 打开对应详情，返回总览恢复筛选。 |
| 空态与失败 | 有成功来源时指标、趋势与最近记录呈现可用结果，并说明覆盖范围。所有已授权来源都成功且没有记录时显示 0。全部请求失败时显示错误和重试。仅有单一来源权限时按授权范围展示；401 走统一登录流程。 |

#### 归并规则

1. 各来源沿用服务端授权。故障事件与 Finding 先按集群实例核对显式关联。Zabbix 问题和 Alertmanager 告警保留各自的来源身份。故障使用 `scope.clusterId + findingId` 匹配 Finding 的 `cluster + id`。同名问题、同名资源、跨实例同 ID 均保留独立记录。
2. 有明确关联且故障代表记录可见时，关联 Finding 并入故障。最近列表只呈现故障行；故障级别、状态及首次发现时间作为其统一统计值。已合并的故障指向当前可见的归并目标。多个仍独立的故障共用 Finding 时，各故障按自身计数，Finding 只被扣除一次。关联对象不可见时只计当前已授权记录。
3. 按选定实例完成归并后再应用统一级别过滤。指标、级别分布、趋势和最近列表引用归并后的同一集合。最近列表按首次发现时间倒序，时间相同依次按来源、集群和稳定 ID 排序。指标统计当前可见的全部符合记录，趋势是所选 7、14 或 30 天的子集。日桶统一使用 UTC+8，包含当天且排除快照时刻之后的记录。缺失时间的记录保留在范围总数中。

同一实例有 1 条警告级故障和 3 条独立的 K8sGPT 警告时，总览显示 4 条诊断记录。新版原型默认用研发实例的 3 条历史 Finding 验证时间窗空态；测试实例可以体验两种来源混合。

### 3. API、权限与失败处理

- 数据源只迁移 Web 入口。沿用 `/api/v1/diagnostics/datasources` 及 `diagnosis.connector.read/write/test`、服务端范围约束和现有审计。资源模块可见条件包含数据源读取权。只有该权限时，资源菜单默认打开数据源管理。
- 本次使用现有受权分页接口 `GET /api/v1/diagnostics/incidents` 与 `GET /api/v1/findings`。现有 `GET /api/v1/diagnostics/overview` 服务于故障统计，契约保持兼容。故障和 Finding 分页各最多 20 页、每页 100 条，达到上限或超时时明确显示覆盖不完整。Zabbix 和 Alertmanager 走各自的受权快照查询并显示来源状态。接口已有的权限与脱敏处理继续生效。
- 只有摘要权限时，无法取得用于归并的记录，本次将其标为不可访问来源。详情继续由相应详情接口单独鉴权。现有汇总、列表和详情页面保持兼容。
- 第一版趋势定义为「当前仍可查询的诊断记录按首次发现时间分布」。若来源只提供当前快照，已消失的历史 Finding 不在该数据集内，页面说明该口径。完整历史趋势需要另行确认快照留存。接口超时、分页截断或授权不足时避免把缺失数据画成零。
- 查询变化时清除旧结果，丢弃已过期响应。总览详情返回地址只接受应用内受控路由；各详情页继续独立执行原有鉴权。首版可复用已有数据模型，无需迁移存量数据。

### 4. 开发与验收

| 场景 | 验收 |
| --- | --- |
| 总览正常 | 一组指标、一个趋势、一个级别分布和一张最近记录表。测试实例 1 条故障与 3 条 Finding 共同显示 4 条警告。 |
| 显式归并 | 同实例的关联 Finding 不重复计数，跨实例同 ID 或同名资源不误合并；排序和级别过滤基于归并结果。 |
| 分页与时间 | 大于 5 条时汇总维持全量，最近诊断默认每页 5 条；覆盖不足或无发现时间时口径说明准确。 |
| 筛选与下钻 | 切换实例与级别时各区同步；分别进入故障和 Finding 详情后恢复筛选；保留原有诊断结果页入口。 |
| 授权与故障 | 验证仅一来源可读、汇总与明细权限不一致、单来源失败、全部失败和重试，确保不泄露数据。 |
| 数据源迁移 | 新旧路由指向同一资源页，参数保留，权限、表单和测试同步交互可用。 |

HTML 使用示例数据，可验证融合、过滤、关联去重、详情返回及数据源迁移交互。原型使用示例。实际页面按本模块当前规则验收。

## 诊断趋势与级别分布

设计对应当前诊断总览路由。原型使用 2026-11-24 的脱敏样例。

[诊断趋势交互原型](trend.html)

### 现状与设计决策

| 视角 | 截图和现有实现暴露的问题 | 本次设计 |
| --- | --- | --- |
| 产品 | 最近诊断的五行记录压在趋势上方。趋势位于首屏下缘，阅读总数后很难立即判断何时开始增加。趋势与严重度分布的统计范围也容易混淆。 | 按「全局筛选 → 四项指标 → 趋势与严重度分布 → 最近诊断」排列。筛选卡右下保留跳到最近诊断的快捷入口，完整列表入口留在最近诊断区。趋势标注窗口内条数，严重度分布标注当前筛选范围和分母。 |
| 前端 | 当前趋势日项用重复的小网格承载柱体和日期，横线随日期项重复。截图中日期与横线松散，图与右侧分布视觉上像两个高度无关的区域。分布条在截图里几乎等长，无法直观比较。 | 将趋势和分布放进同一张分析卡。趋势共享数量轴、日期轴和网格，堆叠段按固定来源着色。分布区域按真实百分比绘制环图，同时写明数量和占比。检查实际页面的日项定位、通用样式覆盖和百分比渲染，以生产构建做浏览器验收。 |
| 运维 | 最近诊断显示 UTC+8，趋势按 UTC 日历日分桶。夜间跨日问题可能落在不同日期。缺数、真零和窗口外的存量记录也需要不同提示。 | 趋势分桶和最近诊断统一为 UTC+8，标注今日数据截至当前时刻。按首次发现日统计新增，显示来源覆盖状态，并单独处理零新增、权限或来源不可用以及缺少时间戳。 |

### 页面布局

| 区域 | 内容与布局 |
| --- | --- |
| 标题与全局筛选 | 保留原导航与「诊断总览」位置。六项筛选为来源、环境、业务、集群实例、诊断级别和 Zabbix 状态。筛选工具栏用紧凑网格，窄屏自然换行。 |
| 指标区 | 桌面并排显示诊断总数、严重、警告、信息。窄屏改为两列。级别卡和分布条使用同一个全局级别筛选。 |
| 分析卡 | 桌面趋势约占 75%，右侧分布约占 25%，分布最小宽度 238 px。两区顶部对齐，共用外框，中间用分隔线。主要绘图区在宽屏约 164 px 高，在中等桌面内容宽度约 144 px 高。日期刻度与图例均处于卡片内。 |
| 分析卡的窄屏布局 | 可用内容宽度约 760 px 以下改为上下排列。小屏的绘图区约 130 px 高。14 天和 30 天按空间减少日期标签，始末日期保留。密集模式只允许绘图区内部横滑，页面整体保持在视窗宽度内。 |
| 最近诊断 | 分析卡下方默认每页五条，支持调整页容量。桌面将级别与状态并入问题列，主机或实例并入来源列。窄屏换行展示同一记录。问题和 AI 入口打开侧边工作台，证据按来源折叠。保留故障事件及原有诊断结果入口。 |

### 交互与统计口径

| 操作或状态 | 结果 |
| --- | --- |
| 切换 7、14、30 天 | 使用同次查询的 30 天日桶切片，仅重绘趋势区域。全局指标、分布和最近诊断仍显示当前筛选范围。默认近 7 天，延用 `trendDays` 参数支持刷新和分享。 |
| 点击柱体、点击零值日或用方向键移动 | 高亮该日，在固定信息栏展示日期、当日合计和来源明细。最近诊断继续按全局筛选与首次发现时间展示。悬浮提示为补充，键盘和触屏均能读取明细。 |
| 选定某天后更换窗口或全局筛选 | 刷新图内的选中日期，优先定位到新窗口内最新有新增的日期。 |
| 点击严重度明细或指标卡 | 应用全局级别筛选，同步刷新指标、趋势、分布和最近诊断。被选中的级别再次点击可清除级别筛选，其他筛选项保留。 |
| 近 7 天为零，但当前范围有早期记录 | 图内显示「近 7 天无新增」。最近 30 天内有更早记录时，提供扩大到 14 天的入口。当前范围总数保留。 |
| 全范围零记录 | 确认数据可用时显示 0。严重度图为空环，占比显示「—」。 |
| 部分来源不可用或结果截断 | 展示来源级提示，有可用记录时以「已获取」标记统计；零结果的时间窗显示「未取得记录」。全不可用时指标使用「—」，趋势和最近诊断使用不可用态。 |
| 个别记录缺少首次发现时间 | 继续计入范围指标与严重度分布，在图下显示未计入趋势的条数。手工重试和权限提示沿用现有来源状态。 |

趋势按首次发现时间计数，沿用现有来源映射、去重和归并。故障事件使用 `firstSeen`，K8sGPT 使用 `createdAt`，Zabbix 使用 `startedAt`。UTC+8 计算按本地日历从 00:00 分桶，包含今天，排除晚于当前时刻的记录。归并后有效时间位于时间窗外的记录仍可计入范围总数。若用户开启「含最近恢复」，问题仍按首次发现日归属，恢复时间保留在详情。趋势基于当前可查询记录，时间窗扩大只调整日期范围。

严重度环图占比按该级别数量除以当前筛选范围内的已获取记录总数计算。原型默认总数为 112，严重 24（21.4%）、警告 76（67.9%）、信息 12（10.7%），近 7 天首次发现 51 条。柱体颜色编码来源，分布颜色编码级别，各自有可读文本。

### 实现与验收

页面编排位于 `web/src/views/diagnosis/unified-overview.ts`，图表渲染和键盘选择位于同目录 `unified-trend.ts`，样式位于 `unified-overview.css`。分桶由 `web/src/domain/unified-diagnostics.ts` 完成。扩大时间窗复用当前查询数据，更新 URL、筛选表单和详情返回链接。两类列表入口沿用现有路由和权限。

| 验收项 | 通过条件 |
| --- | --- |
| 桌面 1920 × 900 与 1280 × 800 | 先看到完整分析卡，再看到最近诊断标题和表格入口。柱体、数量轴和日期对齐，严重度环图和数值使用相同比例。 |
| 手机宽度 390 px | 分布位于趋势下方。切换 30 天后图表可在卡内滑动，页面无横向溢出，首尾日期均可定位。 |
| 时间窗与级别交互 | 切换窗口，范围总数和最近诊断保持；点击级别，所有区块使用同一筛选；点击日期或按方向键，单日明细准确。 |
| UTC+8 日界线 | `2026-11-23T15:59:59Z` 计入 11/23，`2026-11-23T16:00:00Z` 计入 11/24。当天晚于查询时刻的记录不计入趋势。 |
| 来源异常与真零 | 检查近 7 天无新增、部分来源不可用、全部不可用，以及原型中的筛选无匹配场景。全不可用不显示 0。 |
| 可访问性 | Tab 可定位窗口切换、严重度明细和日期控件。日期控件支持方向键，数值更新由屏幕阅读器可识别的状态区域读取。 |

验收使用同一 UTC+8 分桶函数的单元测试与通过 Portal 构建运行的 Chromium 端到端测试。企业现场数据采用发布后的环境验收。

## API 与数据模型

[PRD](00-prd.md#故障事件拓扑ai-分析与策略) · [任务](04-development-tasks.md#故障事件拓扑ai-分析与策略)

### P0 复用契约

| HTTP | 用途 | 行为 |
| --- | --- | --- |
| GET `/api/v1/clusters`、`/api/v1/clusters/{cluster}/namespaces` | 授权范围与筛选项 | 沿用现有发现接口和权限。 |
| GET `/api/v1/findings`、`/api/v1/findings/summary` | 总览、列表 | `cluster=*` 表示授权范围聚合。复用 `environment/business/group/namespace/kind/severity/problem`，列表额外接受 `limit/continue`。 |
| GET `/api/v1/findings/{id}`、`/api/v1/findings/{id}/correlation` | 详情与关联证据 | 必须携带选中实例 `cluster`；旧直链保留本地实例默认项。 |
| 现有 Finding 复核及 Wiki 接口 | 结论生成、保存与发布 | 继续使用原授权和持久化。后续 RCA 任务与旧复核版本通过来源引用互通。 |

### P1–P3 目标契约

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

### 故障契约示例

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

### 存储与索引

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

## 诊断关联与只读证据

现有 Kubernetes 事件、时序、日志与告警通过固定查询模板提供关联证据。按 Finding 的实际范围先授权，再以独立时间窗与请求预算读取。单源超时保留其他来源及明确状态。部署兼容与早期交付记录见[Phase 2 关联设计](../phase-2-correlation-design.md)。

## 诊断助手与 AI 深度调查

诊断总览继续聚合 Finding、故障事件及监控告警。健康告警调用受范围限制的 Holmes 调查，Finding 沿用诊断复核，故障事件进入已有 RCA 工作区。助手优先使用当前已授权 Finding 的追问能力，清除上下文后可使用[授权知识问答](../operations-knowledge/design.md#ai-知识文档与问答)。

[需求](00-prd.md#诊断助手与-ai-深度调查) · [页面](01-pages.md#诊断助手与-ai-深度调查) · [验收](05-unit-test-cases.md#诊断助手与-ai-深度调查)
