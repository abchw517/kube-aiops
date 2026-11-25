# 诊断控制台 · 开发任务

[设计方案](design.md) · [PRD](00-prd.md) · [页面拆解](01-pages.md) · [功能拆解](02-features.md) · [用户故事](03-user-stories.md) · [测试用例](05-unit-test-cases.md) · [交互原型](prototype.html) · [文档维护规则](../development-standard.md)

覆盖诊断总览、故障事件、原有诊断结果、证据与复核、拓扑、AI 分析和诊断策略。总览采用聚合后的全量统计与最近诊断分页，数据源配置归[资源管理](../resource-management/design.md)。

## 最近诊断分页

| 任务 | 改动 | 验收 |
| --- | --- | --- |
| DWL-DEV-01 | 诊断聚合暴露完整有序记录，解析页和页容量 | DWL-TC-01、DWL-TC-02 |
| DWL-DEV-02 | 总览接入审计式页脚，局部翻页、URL 与详情返回同步 | DWL-TC-03、DWL-TC-04 |

## 统一诊断总览

| 任务 | 实施位置 | 依赖 |
| --- | --- | --- |
| D1 数据源路由迁移 | resource-routes、portal-navigation、app、diagnostic-settings | 复用管理组件与能力信号 |
| D2 统一记录集和有界分页 | domain/unified-diagnostics、views/diagnosis/unified-overview | 四类诊断来源的受权列表或快照 |
| D3 渲染与导航恢复 | unified-overview、app、诊断样式 | D2 |
| D4 回归 | web/tests、web/e2e 与可点击原型 | D1–D3 |
| D5 页面重构 | 总览模板与独立样式 | 分析卡前置，校对桌面、窄屏、空态和失败态 |
| D6 视图参数与回链 | unified-diagnostics、详情回链 | 校验趋势日数，集群、级别及趋势窗均可恢复 |
| D7 布局和交互回归 | 单元测试、Portal E2E、HTML 原型 | 验证历史记录可见、图表时间窗、日期读数与窄屏记录卡 |
| D8 UTC+8 聚合 | `domain/unified-diagnostics.ts` | 调整日界线，验证首日、下一日、无时间戳和未来时间 |
| D9 趋势局部更新 | `unified-trend.ts`、`unified-overview.ts` | 共享轴、比例堆叠、日期键盘、范围切片和回链同步 |
| D10 分布与响应式 | `unified-overview.css`、Portal E2E | 以真实像素校验柱体基线、环图分段比例和移动端图内滚动 |

## 诊断结果与详情

[PRD v1.2](00-prd.md#诊断结果与详情) · [用户故事](03-user-stories.md#诊断结果与详情) · [测试用例](05-unit-test-cases.md#诊断结果与详情) · [HTML 原型](finding-review.html)

DC-T01 至 DC-T04 保留最初设计与验收记录。DC-T05 基于已具备跨实例范围查询、资源范围权限和详情内复核的现行实现。

### DC-T01

**环境默认范围与筛选联动** · [DC-US01](03-user-stories.md#dc-us01) · 页面 [DC-P01](01-pages.md#dc-p01)

- 前端在 `web/src/domain/filters.ts` 维护 URL 与查询参数的映射，在诊断列表入口和现有筛选组件接入环境、业务、集群名称、实例。无参数显示「全部环境（ALL）」。仅某个具体实例被显式选中时，列表限定到它。变更上游后保留仍有效的筛选，并将已经无效的下游范围和分页游标清除。「清除筛选」清除结果条件，保留集群范围。
- API 对列表和汇总新增 `cluster=*` 聚合范围，可同时传入 `environment`、`business`、`group`。维护 API 契约和生成的客户端类型。省略 `cluster` 的原 API 请求仍进入 `local`，显式实例继续按原方式授权。后端以已有集群与命名空间读取权限约束选项和结果，不能通过传入非法条件扩大范围。
- 完成条件：默认、具体环境与具体实例三种范围均能刷新复现，空条件回到首页，显式未知实例或无权范围沿用错误反馈。[DC-UT01](05-unit-test-cases.md#dc-ut01)

### DC-T02

**范围内的列表、汇总、分页和详情** · [DC-US02](03-user-stories.md#dc-us02) · 页面 [DC-P01](01-pages.md#dc-p01)、[DC-P02](01-pages.md#dc-p02)

- 依赖 DC-T01 的范围契约。复用现有诊断数据及权限检查，给列表与汇总应用相同的范围、命名空间、资源类型和严重级别条件；汇总统计全部匹配记录。保留现有时间与 ID 排序规则，跨实例增加稳定的实例比较条件，避免相同 ID、相同时间跨页遗漏或重复。
- 表格显示记录所属实例。按实例和 ID 生成详情链接，继续使用既有详情权限和后续操作。命名空间选项合并当前范围可读名称，资源类型选项保持与范围一致。
- 查询范围内无符合项时显示空状态；来源失败时沿用可重试错误，不显示看似完整的部分汇总。保留现有单实例采集状态表述和登录入口。前端忽略过期请求响应。
- 完成条件：两实例同名记录可分别进入详情，流程通过筛选、聚合、跨页和授权回归。[DC-UT02](05-unit-test-cases.md#dc-ut02)

### DC-T03

**三级说明交互** · [DC-US03](03-user-stories.md#dc-us03) · 页面 [DC-P01](01-pages.md#dc-p01)

- 在现有诊断汇总条中保留总数及颜色编码，将严重、警告、信息的标签设为可聚焦的说明入口，使用 PRD 文案和现有级别键。保持读数、标签和表格范围的关系。
- 悬停和聚焦显示说明，点按可展开或关闭，Escape 关闭后焦点留在当前入口。提供语义按钮、可辨认焦点及屏幕阅读器可读的说明；在窄屏上保持文字可完整阅读。
- 完成条件：三个标签的说明及示例和 PRD 一致，鼠标、键盘、触屏操作均可验证，操作不触发诊断筛选。[DC-UT03](05-unit-test-cases.md#dc-ut03)

### 开发检查点

先确认本页与 [PRD](00-prd.md#诊断结果与详情)、[页面](01-pages.md#诊断结果与详情)、[功能](02-features.md#诊断结果与详情)、[用户故事](03-user-stories.md#诊断结果与详情) 和 [原型](finding-review.html) 一致。编码结束后执行 Go 相关授权和诊断 API 测试、前端单元测试、类型检查、构建与浏览器回归。

### DC-T04

**详情内证据复核与独立发布** · [DC-US04](03-user-stories.md#dc-us04) · [DC-UT04](05-unit-test-cases.md#dc-ut04)

1. 更新详情原型，覆盖未启动、生成中、结构化结果、补充后重试、保存和发布预览。原始说明与证据区可同时阅读。
2. 复用当前诊断的关联证据采集和脱敏。后端为模型证据分配引用编号，验证返回结构和引用；只允许引用本次提供的证据。区分历史窗口、来源状态、人工输入及 Wiki 经验。模型异常独立返回。
3. 新增无写入的复核操作和带版本的保存操作；同一诊断跨实例隔离。详情以独立字段返回已保存的复核文本，保留原始说明及旧 API 的说明语义。
4. 将知识预览和发布接入现有 Wiki 权限、脱敏、版本及稳定编号。发布读取服务端已保存版本，页面与 API 分别校验权限，不修改诊断版本。
5. 替换弹窗为详情内复核面板。实现引用定位、原地重试、补充观察、可编辑保存和独立发布反馈。补充区从请求开始即可编辑。为同一复核面板保留一个有效请求，显式提交新的观察时取消旧请求，延迟到达的旧响应不更新页面。沿用现有复核接口。验证 API 测试、前端检查、浏览器端到端测试和文档契约。

### DC-T05

**统一证据折叠并完善复核超时恢复** · [DC-US05](03-user-stories.md#dc-us05) · [DC-UT05](05-unit-test-cases.md#dc-ut05)

1. 更新独立 HTML 原型，覆盖混合状态来源、四个折叠标题、引用定位和模拟超时后的重试。
2. 在 `web/src/views/diagnosis/correlation.ts` 为所有来源渲染原生折叠项和零结果说明。在 `finding-review.ts` 保留已有的信号定位，并处理信号缺失时的来源标题回退。
3. 复用现有 API 的配置及错误契约。模型等待默认 90 秒，允许设置 `FINDING_AI_PROVIDER_TIMEOUT=5s…120s`。复核总时限 135 秒，API 响应写入时限 140 秒。专用 HTTPRoute 请求时限 150 秒，后端请求时限 145 秒。
4. 浏览器覆盖四个来源及空态、精确和回退引用定位、三类超时反馈、观察保留及重试。Go 测试覆盖配置上下限、上游超时状态与同一观察重试。部署环境用真实上游模型验收。

## 故障事件、拓扑、AI 分析与策略

这里保留 V2 的分期范围、故障处置和相关验收。总览口径与入口以本文件上方的统一诊断总览为准。

[故事](03-user-stories.md#故障事件拓扑ai-分析与策略) · [测试](05-unit-test-cases.md#故障事件拓扑ai-分析与策略) · [契约](design.md#api-与数据模型)

| 编号 | 批次 | 实施与依赖 | 完成条件 |
| --- | --- | --- | --- |
| DC-T01 | P0 | 在 `web/src/views/diagnosis` 与 Portal 壳层增加总览、故障事件及详情路由。复用 Finding API、权限、聚合筛选和详情组件。 | 注册入口可访问。查询与返回链接保留范围。确认无模拟故障或状态冒充真实数据。 |
| DC-T02 | P0 依赖 | 核验已有 PR #108。关联证据统一默认折叠、AI 引用定位以及模型请求超时并入部署验收。 | 在最终集成分支回归全部工具、无证据、权限和部分可用场景。 |
| DC-T03 | P1 | 新建 `api/internal/diagnosis/incident`，SQLite 加法迁移、领域状态机及审计，目录注册权限。依赖 T01。 | 人工创建、读取、流转及事务用例通过，既有 Finding API 兼容。 |
| DC-T04 | P1 | 抽出来源适配器接入现有 correlation/采集，定义资源映射和重放机制，开发归并队列与人工拆分。依赖 T03。 | 重复/乱序、跨环境、活跃故障关联及部分失败用例通过。 |
| DC-T05 | P2 | 在 incident 下编排只读 RCA 任务，复用现有 Provider 和审查器，保存证据快照与版本。依赖 T02/T04。 | 取消、超时、并发和引用校验通过，旧版复核兼容。 |
| DC-T06 | P2 | 增加资源关系查询和图形工作区。依赖已授权的资源映射和 T04。 | 关系来源及置信度可追溯，可用/未知/部分覆盖清楚。 |
| DC-T07 | P3 | 接入 Zabbix 与云元数据，开发数据源状态和配置。依赖 T04 和资源映射。 | 使用测试环境真实账号验证只读查询、按范围接入和凭据轮换。 |
| DC-T08 | P3 | 策略配置、运行记录、冷却去重及自动触发。依赖 T05/T07。 | 并发更新冲突、禁用、冷却及审计通过。分环境启用试点。 |

T01、T03 与 T04 在 `feat/diagnostic-center-v2` 分支交付。T02 已随 [PR #108](https://github.com/abchw517/kube-aiops/pull/108) 合入开发基线。P1 的模块入口、数据源范围和试点验收见 [P1 交付说明](04-development-tasks.md#故障事件-p1-交付)。T05–T08 的代码与自动化验收见 [P2–P3 交付说明](04-development-tasks.md#诊断中心-p2p3-交付)。

## 故障事件 P1 交付

### 运维人员操作

1. 在「故障事件」打开「人工创建故障」，选择已授权的集群实例和命名空间，填写标题、级别和描述。可选填 Pod 或 Deployment。也可从现有 Finding 详情创建故障并保留来源链接。
2. 共享命名空间需要指定一个已登记且获授权的业务 ID。单一业务归属由服务端解析。生产和预发命名空间须完成业务归属。
3. 在详情展开「更新故障」，填写变更说明并提交。状态从待确认进入处理中、已恢复和已关闭。已恢复可转回处理中。详情时间线记录操作者、状态和原因。
4. 点击「采集证据」，系统按现有连接配置调用 Kubernetes 事件、Prometheus、Alertmanager 和 VictoriaLogs。资源一致且处在采集窗口内的信号由当前故障接收。已有归属的信号保留原关联。每类来源显示可用、部分可用、不可用或未启用。
5. 在详情选择同资源故障并归并，或选中证据快照拆分出新故障。拆分会移动该信号及其已有版本。归并后的来源故障可跳转至目标。归并要求来源与目标的资源身份和授权范围一致。

### 部署与授权

- 部署沿用持久化的 `AUTH_SQLITE_PATH`。启动时事务性创建 P1 的表与索引。数据库目录与 SQLite 写入权限按现有 Portal 部署管理。
- 按需设置 API 进程的 `DIAGNOSTIC_INCIDENT_ENVIRONMENTS`，值为以逗号分隔的稳定环境 ID，例如 `development,testing`。空值允许所有已登记环境。移出环境后，相关故障保留在数据库中，可在重新启用后读取。
- 平台管理员使用现有权限目录授予 `diagnosis.incident.list`、`diagnosis.incident.read`、`diagnosis.incident.create`、`diagnosis.incident.update` 和 `diagnosis.incident.correlate`。证据查询和采集另需 `diagnosis.evidence.read`。角色范围与故障实际的实例、命名空间、环境和业务归属匹配。
- 采集使用既有只读连接器。P1 适用已有登记的 Kubernetes 资源，资产型数据源和独立连接器推送认证仍按后续批次安排。真实来源的连通性与字段映射在试点环境验收。

### 写入与追溯

| 路径 | 约束 |
| --- | --- |
| POST `/api/v1/diagnostics/incidents` | 首次创建返回 201。同一调用方、相同键和请求体重试返回 200 及原故障 ID。键相同且输入变化返回 409。 |
| PATCH `/api/v1/diagnostics/incidents/{id}` | 使用当前版本的 `If-Match`。过期返回 412，非法流转返回 409。 |
| POST `/api/v1/diagnostics/incidents/{id}/collect` | 使用 `If-Match`。服务端按资源锚点调用原有相关性服务，经脱敏和权限复查后持久化。 |
| POST `/api/v1/diagnostics/incidents/{id}/merge` 与 `/split` | 使用目标故障版本，归并另传源版本。移动信号、快照与记录变更在一个 SQLite 事务中提交。 |

故障更新、时间线与操作审计同提交。操作审计遵循平台现有保留配置。相同来源记录重放按提供方标识及资源范围去重。Prometheus 采样按观测时间区分，Alertmanager 按指纹与本轮告警开始时间区分。较旧观测保留已写入的较新信号状态。

### 验收范围

契约、行为与权限由 Go 测试覆盖。浏览器使用测试数据验证人工创建、版本提交、状态更新、时间线和证据折叠。生产试点仍需验证四个连接器的真实端点、凭据权限及命名空间映射。

[PRD](00-prd.md#故障事件拓扑ai-分析与策略) · [开发任务](04-development-tasks.md#故障事件拓扑ai-分析与策略) · [测试用例](05-unit-test-cases.md#故障事件拓扑ai-分析与策略) · [OpenAPI](../../api/openapi.yaml)

## 诊断中心 P2–P3 交付

### 实现范围

| 任务 | 实现 |
| --- | --- |
| DC-T05 根因分析 | 可从故障详情发起独立 RCA，选择证据、补充人工观察、查看任务状态、取消或重试、跳转引用及保存修订版本。SQLite 保存输入、脱敏快照、租约和版本，复用集群已配置的模型。 |
| DC-T06 关联拓扑 | 展示上游、下游与主机监控映射。可登记或解除故障间关系及云资产关系。推断关系必须选择证据。跨实例查询检查两个节点的当前权限及证据所有权。 |
| DC-T07 数据源 | 配置、启停、服务端连接测试和资产同步。支持 Zabbix Host、阿里云 ECS、腾讯云 CVM。可从同步资产创建故障，或将资产关联到既有故障。 |
| DC-T08 策略 | 支持范围、故障级别、信号来源、冷却、并发和启停。策略模板为基于证据的只读 RCA。任务历史记录策略版本，经启用的环境才会触发。 |

### 部署配置

诊断中心沿用生产模式、SQLite、现有资源目录与身份体系。RCA 使用故障所属集群的模型配置。数据源密钥通过 API 容器内的只读 Secret 挂载提供，浏览器传递文件引用。

| API 环境变量 | 用途 |
| --- | --- |
| `DIAGNOSTIC_INCIDENT_ENVIRONMENTS` | P1–P3 的环境范围，沿用 P1 配置规则。 |
| `DIAGNOSTIC_POLICY_ENVIRONMENTS` | 逗号分隔的自动运行环境 ID。空值时策略可保存，调度保持停用。 |
| `DIAGNOSTIC_CREDENTIALS_DIR` | 已挂载的凭据目录绝对路径。文件引用使用该目录下的文件名。 |
| `DIAGNOSTIC_ZABBIX_ENDPOINTS` | 逗号分隔、精确匹配的 HTTPS JSON-RPC 地址。云厂商端点由适配器按地域生成。 |

凭据文件的 `scope` 使用 `environmentId/businessId/clusterId/namespace`，空业务保留分隔符。凭据中的 `kind`、`endpoint`、`region` 和 `assetIds` 约束可使用的数据源配置。`purpose` 为 `inventory` 或 `ingress`。Zabbix 使用 `token`，云资产盘点使用 `accessKeyId`、`accessKeySecret` 和可选的 `sessionToken`。连接器推送使用单独的 `token`。凭据自动重读，可在挂载内轮换同名文件。

```json
{
  "scope": "production/orders/ack-prod-01/prod-order",
  "kind": "zabbix",
  "endpoint": "https://monitor.example.test/api_jsonrpc.php",
  "region": "",
  "assetIds": ["10010"],
  "purpose": "inventory",
  "token": "<只读 Zabbix API Token>"
}
```

配置资产白名单后，数据源测试保存脱敏资产投影，并更新检查和同步时间。已登记资产可在拓扑中作为 `runs_on`、`depends_on` 等关系目标。资产推送使用 `POST /api/v1/diagnostics/ingress/{connectorId}/signals`，以连接器 Bearer 凭据和实例范围验证请求。调用方提交 `incidentId`、原生 `assetId`、每次告警生命周期稳定的 `externalId`、`state`、`observedAt` 和 `summary`。资产和故障必须已映射。重放及乱序输入沿用 P1 去重规则。

### 权限与故障恢复

- 权限目录中可分别授予 `diagnosis.rca.read/run/cancel/save`、`diagnosis.topology.read/write`、`diagnosis.connector.read/write/test` 及 `diagnosis.policy.read/write`。RCA 任务还需要原故障与证据可读。
- 模型输出的引用限于本次快照。只引用历史证据时结论降为证据不足。证据被移出原故障后不再向原故障公开旧分析内容。
- 服务重启后继续处理排队任务。已领取任务在租约过期后显示超时。取消、重放或策略版本变化后的迟到结果不能写入已完成结论。
- 策略只接受启用版本生效后新收集的活跃信号。变更或停用时取消旧版本的待运行任务，保留运行历史。自动诊断范围仅为模型分析。
- 拓扑的覆盖状态表示当前可访问的已登记关系，支持环路，并限制查询深度和节点数量。缺失的拓扑关系显示未知或覆盖不完整。

### 验收口径

自动化验证覆盖 RCA 去重、取消、超时、证据引用和历史版本，拓扑循环和权限裁剪，三种供应商的只读请求与凭据绑定，数据源权限错误和回放隔离，以及策略并发、版本和停用。浏览器回归覆盖七个诊断页面。

真实模型、生产数据源和凭据轮换仍通过对应环境试点验收。接口与前端类型以 [OpenAPI](../../api/openapi.yaml) 为准。

- [Zabbix Host API](https://www.zabbix.com/documentation/current/en/manual/api/reference/host/get)
- [阿里云 ECS DescribeInstances](https://www.alibabacloud.com/help/en/ecs/developer-reference/api-ecs-2014-05-26-describeinstances)
- [腾讯云 CVM 接口参考](https://main.qcloudimg.com/raw/document/intl/product/pdf/tencent-cloud_213_7217_en.pdf)

## Zabbix 问题与来源覆盖

数据源配置、连接校验和采集管理见[资源管理](../resource-management/design.md#zabbix-接入与同步)。

| 任务 | 代码位置 | 对应需求 |
| --- | --- | --- |
| DEV-04 API 契约与受权查询 | `api/internal/diagnosis/incident/zabbix.go`、`api/openapi.yaml` | PRD-01 至 06 |
| DEV-06 汇总、详情和状态口径 | `web/src/views/diagnosis/unified-overview.ts` | PRD-05、06 |

### Holmes 健康告警调查接入

总览保留原始统计和数据源链接。Zabbix 与 Alertmanager 记录可在详情内发起一次同步调查。API 根据数据源 ID 和记录 ID 重新读取授权快照。Zabbix 以环境和业务授权，Alertmanager 还验证集群和命名空间。角色需要数据源读取和 `diagnosis.health.investigate`。关闭弹窗会终止浏览器请求。

在 Portal API 容器中通过 Secret 只读挂载 JSON 文件，将 `HOLMES_BINDINGS_FILE` 设为其绝对路径。每行绑定的 Holmes 服务应部署在客户环境中，工具凭据的读取权限须与该行范围一致。下例使用虚构的实例 ID，接入时取自对应告警的 sourceId。可选 apiKey 通过 X-API-Key 传给内部网关。可选 model 使用 Holmes 的模型配置名。

```json
[
  {"source":"zabbix","sourceId":"zbx-prod","environmentId":"prod","businessId":"orders","endpoint":"https://holmes-orders.internal"},
  {"source":"alertmanager","sourceId":"am-prod","environmentId":"prod","businessId":"orders","clusterId":"prod-sh-01","namespace":"orders","endpoint":"https://holmes-orders-ns.internal"}
]
```

Go 客户端调用 POST /api/chat，读取 analysis，超时 45 秒。缺少精确匹配时返回 HOLMES_SCOPE_UNCONFIGURED。移除绑定可关闭对应范围的新调查。首轮交付采用同步文本结果，调查历史、SSE 工具轨迹、人工保存和知识草稿见后续任务。

现有 Finding 的 assess 和 enhance 共用同一适配器。Finding 绑定使用 source=finding、sourceId=clusterId 和实际 namespace，environmentId/businessId 取空字符串。assess 使用结构化输出及已有引用检查。现有事件 RCA 使用 source=incident、sourceId=clusterId 及事件的环境、业务、集群、命名空间。已有模型配置仍作为复核和 RCA 的可用性前提，未绑定或上游请求失败时调用既有模型。

在已有内部 Holmes 服务及测试绑定文件就绪后，可在试点环境挂载绑定。保留目录挂载可接收 Secret 更新。更新绑定与 API 部署可独立回滚。

```bash
kubectl -n kube-aiops-system create secret generic kube-aiops-holmes-bindings --from-file=bindings.json=./bindings.json --dry-run=client -o yaml | kubectl apply -f -
kubectl -n kube-aiops-system patch deployment kube-aiops-api --type=strategic --patch-file deploy/examples/holmes/api-bindings.patch
kubectl -n kube-aiops-system rollout status deployment/kube-aiops-api
```
