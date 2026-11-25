# 资源管理 · 开发任务

[设计方案](design.md) · [PRD](00-prd.md) · [页面拆解](01-pages.md) · [功能拆解](02-features.md) · [用户故事](03-user-stories.md) · [测试用例](05-unit-test-cases.md) · [交互原型](prototype.html) · [文档维护规则](../development-standard.md)

覆盖环境管理、业务线管理、Kubernetes 集群与命名空间，以及监控诊断和资产数据源。环境与业务线采用当前平台目录交互，集群与数据源采用当前工作区及专项编辑流程。

## 环境与业务线目录

| 任务 | 涉及文件与范围 |
| --- | --- |
| RCT-01 | `catalog-model.ts` 抽取目录定义、过滤、排序、校验和引用类型。 |
| RCT-02 | `catalog-workspace.ts` 实现共用标题、导航、搜索、列表与空态。 |
| RCT-03 | `catalog-dialog.ts` 接入详情、复制、ID 解锁、写入请求、草稿确认和删除冲突。 |
| RCT-04 | `catalog.css` 适配桌面与窄屏，复用资源管理色板、列表与弹窗样式。 |
| RCT-05 | `resource-management.ts` 与 Portal 路由加载目录，仍按原权限与页面注册项渲染。 |
| RCT-06 | 更新目录单元测试与浏览器模拟接口，验证名称和 ID 提交、引用冲突、只读、390px、失败恢复。 |

代码、文档和验证用例随当前功能分支交付，浏览器端通过既有资源管理入口访问。

## 集群与数据源工作区

| 任务 | 交付项 | 代码入口 |
| --- | --- | --- |
| T01 共用布局 | 复用主题、筛选、状态标记、类型分组、详情和固定底栏 | `web/src/views/resources/resource-workspace.ts`、`resources.css` |
| T02 数据源列表 | 来源并行读取、独立重试、资产游标、记录更新和权限派生 | `datasource-management.ts`、`datasource-rows.ts` |
| T03 接入适配 | 保留各连接器测试和预览，统一新增入口；资产编辑、库存 | `zabbix-sources.ts`、`observability-sources.ts`、`asset-source-editor.ts` |
| T04 集群目录 | 共享环境与分视图查询、历史节点及采集详情、状态统计 | `cluster-workspace.ts`、`cluster-records.ts` |
| T05 集群接入与维护 | 选择方式、分区配置、环境锁定、凭据与删除反馈 | `cluster-onboarding.ts`、原有 `namespace-ownership.ts` |
| T06 入口与回归 | 旧路由、权限、焦点、响应式、实际接口变更兼容 | `app.ts`、`portal-shell.ts`、`browser-test.mjs`、`web/tests` |

开发顺序为真实数据和路由、列表与详情、写入与恢复、测试构建。上线前用 [测试用例](05-unit-test-cases.md#集群与数据源工作区) 逐项核对。

## 可观测数据源接入

连接器契约和部署配置见本节。列表布局与新增入口以集群与数据源工作区为准。

| 编号 | 实现 |
| --- | --- |
| D1 | `api/internal/diagnosis/incident` 增加管理路由、SQLite 配置、探测和启停。 |
| D2 | `api/internal/portal` 配置端点、加密、范围解析及故障／Finding 证据替换。 |
| D3 | 后台 Alertmanager 轮询、快照保留和总览来源接入。 |
| D4 | Grafana 仪表盘发现、UID 保存及诊断详情跳转。 |
| D5 | OpenAPI 定义和自动生成客户端，Portal 管理页按原型实现弹窗和类型区。 |
| D6 | 单元、端到端、接口契约及生产 CSP 回归。 |
| 后续 | Alertmanager Webhook 接收、明确恢复事件和 Grafana Dashboard 变量映射。 |

## Zabbix 接入与同步

数据源在监控与诊断页签中配置。诊断结果、趋势和授权可见性见[诊断控制台](../diagnosis-console/design.md)。

| 任务 | 代码位置 | 对应需求 |
| --- | --- | --- |
| DEV-01 Zabbix 6.x 只读适配器 | `api/internal/diagnosis/assetmeta/zabbix_problems.go` | PRD-01、02 |
| DEV-02 凭据、目录与主机授权 | `api/internal/portal/zabbix.go`、`api/internal/permissions/interface_grants.go` | PRD-01、04、06 |
| DEV-03 SQLite 快照与租约调度 | `api/internal/diagnosis/incident/zabbix_store.go` | PRD-03、04、06 |
| DEV-04 API 契约与受权查询 | `api/internal/diagnosis/incident/zabbix.go`、`api/openapi.yaml` | PRD-01 至 06 |
| DEV-05 接入、预览、运维管理页 | `web/src/views/diagnosis/zabbix-sources.ts` | PRD-01 至 04 |
| DEV-06 汇总、详情和状态口径 | `web/src/views/diagnosis/unified-overview.ts` | PRD-05、06 |
| DEV-07 角色面板主机范围 | `web/src/views/permissions/platform-admin.ts` | PRD-06 |
| DEV-08 端到端试点 | 可读取的企业 Zabbix 6.x 实例和试点业务 | 现场验收，涉及真实环境网络、授权与数据规模 |
| DEV-09 名称持久化和兼容迁移 | 更新 `api/internal/diagnosis/incident/` 的 SQLite 迁移、创建校验与 API 测试，更新 `api/openapi.yaml` 并生成 Web 客户端类型。 | PRD-07 |
| DEV-10 环境联动与折叠交互 | 更新 `web/src/views/diagnosis/zabbix-sources.ts` 及样式，使用 `web/e2e/browser-test.mjs` 验证范围联动、折叠与保存。 | PRD-07、08 |

## 采集、归属与维护基线

本节保留集群接入、节点及命名空间等业务规则。列表、编辑入口和目录删除反馈采用前面的工作区规则。

需求基线 资源管理首版 PRD v1.0（2026-11-17 评审稿）<br>
开发规范 [kube-aiops 模块开发规范](../development-standard.md)<br>
模块文档 [页面拆解](01-pages.md#采集归属与维护基线) · [功能拆解](02-features.md#采集归属与维护基线) · [用户故事](03-user-stories.md#采集归属与维护基线) · [开发任务](04-development-tasks.md#采集归属与维护基线) · [单元测试用例](05-unit-test-cases.md#采集归属与维护基线)


Kubernetes 集群管理交互参见 [资源管理原型](cluster-onboarding.html)。直连凭据支持内嵌 CA 与静态 Token 或客户端证书的 kubeconfig、单独提交 Token 或客户端证书。直连配置和凭据密文保存在 `managed_clusters`。首次保存时服务自动生成 32 字节随机密钥，以权限 `0600` 存放在 SQLite 同目录的 `<数据库文件>.cluster-credentials.key`。备份和迁移需同时保留数据库和密钥文件。升级前已加密的记录继续使用原 `CLUSTER_CREDENTIAL_KEY_FILE` 或 `AUTH_LDAP_ENCRYPTION_KEY_FILE` 解密，下次轮换切换到自动管理的密钥。轮换候选凭据通过节点读取验证后才替换原连接。外部删除后的实例 ID 退役，本地重新连接沿用原归属并重新采集。

### Kubernetes 集群管理当前实现

- 系统管理员在「资源管理 → Kubernetes 集群管理」查看本地、手动接入和采集上报实例，从环境及业务线目录选择新实例归属。原环境、业务线及集群名称继续保留。资源管理三个页面统一由系统管理员操作。
- 本地与手动接入需要 Kubernetes 节点与命名空间的 list 权限，以及 K8sGPT Result 读取权限。测试新提交的内联凭据时，使用请求内存中的凭据检查 TLS、API 连通性和节点读取权限。测试已保存的加密凭据时，使用原密钥解密。旧挂载型记录保留在目录并等待补录，补录前不注册为可采集连接。首次校验失败的连接保留为待接入，可由管理员重新校验。
- 节点状态或真实诊断任一到达即锁定环境，空上报只更新上报时间。采集状态存放在 SQLite 中，节点采集连续失败两轮后展示最后成功的结果并标记过期。
- 本批覆盖 RM-T01、RM-T04 至 RM-T06 及首采前的环境修正。本地归属补录、凭据类型及轮换、命名空间分配与业务线历史诊断、删除及重连另行验收。

`web/src/views/clusters/cluster-onboarding.ts` 提供集群管理名称，资源侧栏、集群页面标题和返回链接复用该文案。`#/resources/clusters` 和旧地址跳转沿用现有路由。

### 开发顺序与代码落点

1. 先接通环境、业务线及授权底座（RM-T09 至 RM-T12），并复用现有的 Go 授权、HTTP API 和 Kubernetes 客户端。底座可先完成存取及策略，再接通界面。
2. 实现接入、校验及采集（RM-T01 至 RM-T06）。本地启动识别与删除标记共用持久化契约，标记字段先随模型落地，完整删除和恢复在 RM-T17、RM-T18 闭环。
3. 实现业务线归属和诊断入口（RM-T07、RM-T08、RM-T13、RM-T14），随后接通迁移、轮换、删除及重新连接（RM-T15 至 RM-T18）。
4. API 契约涉及到的改动同步到 `api/openapi.yaml` 和生成客户端。Web 路由、组件及测试沿用 `web/src`、`web/tests` 约定。后端优先复用 `api/internal/httpapi`、`api/internal/permissions/authorization`、`api/internal/kubernetes` 及现有注入方式。具体包、字段和 API 路径以 Coding 分支实际结构为准。

### 开发任务

每组依次包含服务端和 Web 两个可独立检视的子任务。每组的 Web 子任务依赖同组服务端契约。完成条件为对应故事通过、用例纳入现有测试入口，以及涉及的接口契约通过校验。

#### RM-T01

**导航与集群列表** · 用户故事 [RM-US01](03-user-stories.md#rm-us01) · 测试 [RM-UT01](05-unit-test-cases.md#rm-ut01)

依赖 RM-T10

- `RM-T01-A` 服务端 · 服务端实现集群列表及单条资源的共用可见性过滤，按当前授权查询。
- `RM-T01-B` Web · Web 添加一级入口和默认路由，渲染列表、空态和错误；刷新及直链维持权限约束。

#### RM-T02

**本地识别与归属补录** · 用户故事 [RM-US02](03-user-stories.md#rm-us02) · 测试 [RM-UT02](05-unit-test-cases.md#rm-ut02)

依赖 RM-T09、RM-T10 及预先约定的本地删除标记读模型

- `RM-T02-A` 服务端 · 实现本地识别、稳定连接标识及启动恢复入口；启动时查询删除状态。
- `RM-T02-B` Web · 增加补录接口和表单，校验选择项，保存后刷新列表和状态区。

#### RM-T03

**外部集群接入** · 用户故事 [RM-US03](03-user-stories.md#rm-us03) · 测试 [RM-UT03](05-unit-test-cases.md#rm-ut03)

依赖 RM-T09、RM-T10、RM-T11、RM-T12

- `RM-T03-A` 服务端 · 复用 Kubernetes 客户端构造路径，接入三种配置校验；定义稳定身份去重和凭据存储边界，连接及创建者授权一致提交。
- `RM-T03-A` 回归验收 · `POST/PUT ?test=true` 收到新内联凭据时，在请求内存中解析和校验，直接执行节点读取探测。首次保存自动创建密钥，已有密文但密钥丢失时保存失败并保留原记录。测试成功返回 204，SQLite 和集群注册表写入数均为 0。保存及轮换仅持久化加密后的凭据。
- `RM-T03-B` Web · 实现方式切换和字段联动，接通创建接口及响应；错误回显避免包含凭据。

#### RM-T04

**校验失败与手动重试** · 用户故事 [RM-US04](03-user-stories.md#rm-us04) · 测试 [RM-UT04](05-unit-test-cases.md#rm-ut04)

依赖 RM-T03、RM-T11

- `RM-T04-A` 服务端 · 映射网络、认证和节点读取错误；持久化待接入状态及最近失败原因，手动通过后记录首采起点。手动重新校验失败时立即更新最近检查时间与 `nodeError`，保留最后一次成功的节点快照及采样时间。同一秒内发起的多次校验按发起时间排序，较早发起的结果即使晚返回也不会覆盖较新结果。
- `RM-T04-B` Web · 接通保存待接入及重新校验按钮，呈现状态和脱敏原因。对于已接入实例，列表先判断节点数据是否过期，再判断节点校验是否失败，最后展示最新成功快照对应的健康状态。详情明确区分最近检查和上一次成功快照。

#### RM-T05

**首采及状态转换** · 用户故事 [RM-US05](03-user-stories.md#rm-us05) · 测试 [RM-UT05](05-unit-test-cases.md#rm-ut05)

依赖 RM-T02、RM-T04

- `RM-T05-A` 服务端 · 实现独立首采状态计算及有效样本判断，注入时钟，分别记录节点和诊断到达，用状态码表达结果。
- `RM-T05-B` Web · 采集状态区绑定状态码及首批真实数据，呈现各区块的等待、部分数据及超时文案。

#### RM-T06

**节点健康、诊断提示及过期** · 用户故事 [RM-US06](03-user-stories.md#rm-us06) · 测试 [RM-UT06](05-unit-test-cases.md#rm-ut06)

依赖 RM-T05

- `RM-T06-A` 服务端 · 实现节点采集轮次、快照及失败计数，隔离节点健康和诊断提示；采集调度仅处理活跃连接。周期内重复手动校验沿用原有 5 分钟失败计数规则。连续两轮周期采集失败后将快照标记为过期，后续成功读取节点时刷新快照并清除读取错误和失败计数。
- `RM-T06-A` RBAC 验收 · 本地节点采集收到 Kubernetes 403 时，提示核对 API Deployment 实际使用的 ServiceAccount。默认 API ClusterRole 对 Nodes 授予 `get`、`list`、`watch`，对 Namespaces 仅授予 `list`。安装及升级后执行 `python3 scripts/check-api-node-rbac.py --context CONTEXT` 检查实际账号的 `list nodes` 权限。自定义部署可指定 `--namespace` 和 `--deployment`。命令默认只读，显式指定 `--repair` 时为实际账号创建专用 ClusterRole 和 ClusterRoleBinding，仅补授 `list nodes`。
- `RM-T06-B` Web · 展示节点异常、最后成功时间及过期提示，并独立呈现 K8sGPT 未安装、无权读取或工作负载故障。已启用的本地集群可「重新校验」，权限修复后成功采集节点即清除提示。

#### RM-T07

**追加集群业务线** · 用户故事 [RM-US07](03-user-stories.md#rm-us07) · 测试 [RM-UT07](05-unit-test-cases.md#rm-ut07)

依赖 RM-T11、RM-T12

- `RM-T07-A` 服务端 · 新增原连接关联业务线的写操作，校验业务线存在并对关联去重。
- `RM-T07-B` Web · 在分配视图提供追加操作，重复刷新继续指向同一集群记录。

#### RM-T08

**命名空间多对多分配** · 用户故事 [RM-US08](03-user-stories.md#rm-us08) · 测试 [RM-UT08](05-unit-test-cases.md#rm-ut08)

依赖 RM-T07、RM-T11

- `RM-T08-A` 服务端 · 在现有命名空间归属接口接受按业务线提交的 `{businessId, add, remove}`，事务内应用当前集群、当前业务线的增量，保留其他业务线、其他集群和本次未编辑的归属。兼容按命名空间提交的旧请求。写接口校验权限及业务线目录，独立业务集群仅接受所属业务线。
- `RM-T08-B` Web · 共用集群先选业务线，独立业务集群直接使用所属业务线。按当前业务线回填已关联命名空间，切换业务线时保留弹窗内各自的勾选草稿。命名空间列表独立纵向滚动，取消和保存按钮位于列表之外。保存仅提交当前业务线的勾选变化，有其他待保存草稿时保留弹窗，失败时保留草稿及错误反馈。无操作权限时隐藏分配入口。

#### RM-T09

**环境维护与删除保护** · 用户故事 [RM-US09](03-user-stories.md#rm-us09) · 测试 [RM-UT09](05-unit-test-cases.md#rm-ut09)

依赖 RM-T10 的角色判断

- `RM-T09-A` 服务端 · 实现幂等预置、环境写操作和活跃引用的事务保护，环境 ID 修改与旧 ID 别名和名称索引一同提交，保持环境权限与集群引用。
- `RM-T09-B` Web · 编辑表单提交原 ID 和新 ID，显示校验结果，成功后刷新列表。

#### RM-T10

**环境管理员委派及环境授权** · 用户故事 [RM-US10](03-user-stories.md#rm-us10) · 测试 [RM-UT10](05-unit-test-cases.md#rm-ut10)

依赖 现有认证和授权机制

- `RM-T10-A` 服务端 · 实现委派和撤销权限矩阵；环境撤权同步清理关联集群授权，保留其他环境授权。
- `RM-T10-B` Web · 实现管理员及成员授权操作，显示当前授权和操作失败反馈。

#### RM-T11

**集群授权** · 用户故事 [RM-US11](03-user-stories.md#rm-us11) · 测试 [RM-UT11](05-unit-test-cases.md#rm-ut11)

依赖 RM-T10

- `RM-T11-A` 服务端 · 实现集群授权用例，写入前校验操作者和接收人权限，与环境撤权及迁移保持一致。
- `RM-T11-B` Web · 在集群页展示可管理的授权操作及失败原因。

#### RM-T12

**业务线维护与删除保护** · 用户故事 [RM-US12](03-user-stories.md#rm-us12) · 测试 [RM-UT12](05-unit-test-cases.md#rm-ut12)

依赖 现有认证和授权机制

- `RM-T12-A` 服务端 · 实现业务线 ID 编辑，保留稳定引用键和历史别名。删除检查集群、命名空间和角色引用。
- `RM-T12-B` Web · 提供可编辑的业务线 ID 与原 ID 的更新请求，显示引用冲突和格式错误。

#### RM-T13

**环境历史诊断** · 用户故事 [RM-US13](03-user-stories.md#rm-us13) · 测试 [RM-UT13](05-unit-test-cases.md#rm-ut13)

依赖 RM-T08、RM-T10

- `RM-T13-A` 服务端 · 共用诊断查询授权，按当前环境、集群及命名空间关联筛选，保留历史时间。
- `RM-T13-B` Web · 接入环境导航和集群/命名空间历史视图，详情仅渲染诊断，不请求节点或工作负载列表。

#### RM-T14

**业务线历史诊断** · 用户故事 [RM-US14](03-user-stories.md#rm-us14) · 测试 [RM-UT14](05-unit-test-cases.md#rm-ut14)

依赖 RM-T08、RM-T10、RM-T13

- `RM-T14-A` 服务端 · 实现按当前业务线关联集合和环境权限查询，集群级记录单独排除，并复用环境校验。
- `RM-T14-B` Web · 接入业务线导航及历史诊断视图，处理移除关系后原详情直链。

#### RM-T15

**首采前修正集群环境**

- 服务端校验管理员身份与持久化首采标记。首采完成后的环境修改返回冲突。
- 首采前提供环境目录选择，首采成功后显示只读环境和原因。

#### RM-T16

**外部凭据轮换** · 用户故事 [RM-US16](03-user-stories.md#rm-us16) · 测试 [RM-UT16](05-unit-test-cases.md#rm-ut16)

依赖 RM-T03、RM-T04、RM-T11

- `RM-T16-A` 服务端 · 实现候选凭据校验、通过后切换和运行中采集配置刷新，保留失败前有效配置；避免并发旧配置覆盖新配置。
- `RM-T16-B` Web · 实现轮换表单和状态更新，响应及提示使用脱敏原因。

#### RM-T17

**删除集群连接** · 用户故事 [RM-US17](03-user-stories.md#rm-us17) · 测试 [RM-UT17](05-unit-test-cases.md#rm-ut17)

依赖 RM-T06、RM-T11

- `RM-T17-A` 服务端 · 连接删除与历史清理保持一致，停止调度并在采集写入时检查连接状态，防止迟到回写。
- `RM-T17-B` Web · 提供删除确认和结果反馈；外部连接从活跃页移除，本地连接以已删除记录保留。

#### RM-T18

**本地删除状态与重新连接** · 用户故事 [RM-US18](03-user-stories.md#rm-us18) · 测试 [RM-UT18](05-unit-test-cases.md#rm-ut18)

依赖 RM-T02、RM-T05、RM-T17

- `RM-T18-A` 服务端 · 持久化删除标记和三项配置；启动时尊重删除标记；显式重连接口以新的采集状态恢复，避免重复调度。
- `RM-T18-B` Web · 已删除本地记录旁显示重新连接按钮，仅管理员可操作；成功后进入新一轮采集状态。

### 评审项与受影响任务

| 编号 | 待确定项 | 涉及任务 | 当前处理 |
| --- | --- | --- | --- |
| RM-R01 | 三页导航、默认列表、待分配入口位置 | RM-T01、RM-T08 | 按 PRD 评审布局拆解，路由和命名在 Coding 分支确认。 |
| RM-R02 | 已删除的本地记录是否继续占用原环境和业务线的引用。原归属已删除时怎么重连 | RM-T09、RM-T12、RM-T18 | 活跃引用保护及原归属完整时的重连按已确认行为推进。缺失归属分支待定。 |
| RM-R03 | 首采超时后仅诊断先到时的恢复文案 | RM-T05 | 按有效诊断可以进入已接入、无节点提示采集异常拆解。复用状态码，最终文案待定。 |

### 验证命令

按修改范围运行 `make api-test`、`make api-contract-check`、`make web-check`。以 2 环境和 2 集群试点验收真实采集、凭据和迁移。
