# 系统管理 · 开发任务

[设计方案](design.md) · [PRD](00-prd.md) · [页面拆解](01-pages.md) · [功能拆解](02-features.md) · [用户故事](03-user-stories.md) · [测试用例](05-unit-test-cases.md) · [交互原型](prototype.html) · [文档维护规则](../development-standard.md)

覆盖用户、用户组、角色与权限目录、LDAP/AD、登录会话、改密及操作审计。权限工作区按页面注册表派生名称、位置、顺序及旧名称搜索，接口权限编码与范围契约沿用服务端定义。

## 角色工作区与权限目录

### 工作区重构任务

| 任务 | 落点 |
| --- | --- |
| 提取页面元数据与稳定映射 | page-registry、portal-navigation、resource-routes |
| 将服务端授权目录按菜单元数据投影 | permission-catalog，保留方法、编码、风险和范围 |
| 优化角色列表、编辑和授权审核 | platform-admin、permission-workspace.css，复用现有接口与校验 |
| 兼容旧更新与验证 | 保留后端目录和旧原型用于契约回归，新增新版原型、单测和端到端用例 |

[故事](03-user-stories.md#角色工作区与权限目录) · [测试](05-unit-test-cases.md#角色工作区与权限目录)

| 任务 ID | 故事 | 实现位置 | 完成条件 |
| --- | --- | --- | --- |
| PC-T01 归档设计和接口目录 | PC-US01 | `docx/system-management/`、`api/internal/permissions/catalog/` | 七项评审交付可读，目录编码唯一并与原型矩阵对照。 |
| PC-T02 角色授权与兼容 | PC-US02、PC-US04、PC-US06 | `api/internal/permissions/` | SQLite 旧角色可开库，新授权支持范围与动作，角色版本及来源正确。 |
| PC-T03 诊断与证据裁决 | PC-US02 | `api/internal/httpapi/`、`api/internal/diagnosis/` | 列表、汇总、详情和证据复用授权，AI 用真实 Finding 范围。 |
| PC-T04 Wiki 业务动作裁决 | PC-US03 | `api/internal/wiki/` | 草稿、发布、反馈、问答、绑定、同步动作按业务语义和资源范围校验。 |
| PC-T05 管理能力与字段投影 | PC-US05、PC-US06 | `api/internal/portal/`、`api/internal/permissions/` | 独立读权限按字段或对象投影，管理专属写保留既有门槛，语义操作记录至审计。 |
| PC-T06 权限目录与角色配置 | PC-US01～05 | `web/src/views/permissions/` | 真实服务数据支撑矩阵、筛选、角色选点、预览和生效权限来源。 |
| PC-T07 回归与交付 | 全部 | Go 和 Web 单元测试、浏览器回归 | 验证拒绝请求、老角色回归、构建与桌面交互。 |

### 管理员组与权限点勾选开发任务

| 编号 | 交付范围 | 验收 |
| --- | --- | --- |
| PC-T11 | SQLite 管理员角色解析、LDAP 登录及会话复查 | 统一解析直属和组来源，按实际身份变化更新会话版本 |
| PC-T12 | 管理员组影响预览、关联、批量成员保护及审计 | 预览与提交检查角色和成员快照，普通管理员的所有写入口拒绝，审计可定位 |
| PC-T13 | 用户组表单、影响确认、账号有效权限来源 | 主流程可从现有入口进入，支持新建、已有关联和撤销 |
| PC-T14 | `permission-catalog.ts`、`platform-admin.ts`、`styles.css`。独立勾选、集合与筛选批量控件、范围分批入清单 | 从侧栏进入角色草稿，分别验证只读目录返回链路、可见结果半选、隐藏选择保留、不同范围类型分批添加和失败重试。沿用现有角色 API、权限编码和服务端范围校验 |
| PC-T15 | 更新 PRD、页面与功能拆解、HTML 原型和测试用例。扩展 `web/e2e/` 的真实页面操作和请求断言 | 验证浏览器点击位置、勾选状态、重试、分批入清单、预览、确认提交与打开角色复查。运行 Go 授权测试和前端检查 |

## 认证、用户管理与审计

- 需求基线 [系统管理 PRD](00-prd.md#认证用户管理与审计)
- 交互参考 [HTML 原型](admin-workspace.html)
- [模块开发规范](../development-standard.md)
- [页面拆解](01-pages.md#认证用户管理与审计) · [功能拆解](02-features.md#认证用户管理与审计) · [用户故事](03-user-stories.md#认证用户管理与审计) · [开发任务](04-development-tasks.md#认证用户管理与审计) · [单元测试用例](05-unit-test-cases.md#认证用户管理与审计)

### 实施顺序

1. 身份、会话与审计底座（SM-T01、SM-T04、SM-T16），接通本地与 LDAP 账号流程（SM-T02、SM-T03、SM-T05 至 SM-T08）。引导初始化的角色常量先由底座提供，SM-T09 再接入写操作的保护。
2. 固定角色与数据范围，完成组管理、批量授权和权限解释（SM-T09 至 SM-T14）。
3. 接通集群首采后的环境锁定及历史诊断权限（SM-T17、SM-T15）。

对外接口变更同步 `api/openapi.yaml` 和生成客户端。复用现有权限、会话和资源模块，具体文件以 Coding 分支为准。每项任务以故事验收、单测和适用的接口契约校验为完成条件。

#### SM-T01

**系统初始化和管理员管理** · 故事 [SM-US01](03-user-stories.md#sm-us01) · 测试 [SM-UT01](05-unit-test-cases.md#sm-ut01)

- 依赖 现有存储、身份或审计契约。
- 代码落点 identitystore + permissions。
- `SM-T01-A` 服务端 · 初始化与管理员写服务，原子维护唯一身份和会话撤销，区分普通与超级管理员。
- `SM-T01-B` Web 或运维 · 在列表和详情按权限显示新建、删除及确认，成功后刷新；超管删除在 API 层拒绝。
- 验收输出 并发初始化仍只有一个超级管理员；只有超级管理员可创建或删除普通管理员；删除后立即拒绝旧会话且产生审计；超级管理员不可删除。

#### SM-T02

**访客创建、停用和恢复** · 故事 [SM-US02](03-user-stories.md#sm-us02) · 测试 [SM-UT02](05-unit-test-cases.md#sm-ut02)

- 依赖 [SM-T01](04-development-tasks.md#sm-t01)、[SM-T04](04-development-tasks.md#sm-t04)。
- 代码落点 identity_admin + identitystore。
- `SM-T02-A` 服务端 · 访客创建及状态更新服务，保留关联角色；用统一撤销机制处理停用。
- `SM-T02-B` Web 或运维 · 新增访客表单、临时密码发放结果、停用和恢复确认及状态反馈。
- `SM-T02-C` 删除复用账号变更接口。事务内按身份校验、清理平台账号和关联，记录高风险审计。LDAP 登录登记拒绝已删除的目录身份。
- 验收输出 新访客关联固定只读角色；停用立即阻断登录和旧会话；恢复沿用原角色且不复活旧会话；选中单个可删除的用户后可发起删除，确认后刷新列表并保留审计。

#### SM-T03

- 布局验收长 LDAP DN 换行，状态与操作同排，用户组和直接角色分行，关联标签可换行，分配按钮在详情标题区。用 1440px 和 390px 验证停用确认及授权弹窗。

**搜索用户与账号详情** · 故事 [SM-US03](03-user-stories.md#sm-us03) · 测试 [SM-UT03](05-unit-test-cases.md#sm-ut03)

- 依赖 [SM-T01](04-development-tasks.md#sm-t01)。
- 代码落点 portal/routes + identity-admin.ts。
- `SM-T03-A` 服务端 · 提供账号列表、详情读接口和统一查询投影；权限判断复用管理员身份。
- `SM-T03-B` Web 或运维 · 实现筛选、空态、加载、错误和详情导航；切换筛选时明确批量选中范围。
- 验收输出 列表可按账号及名称搜索并按来源、状态筛选；详情展示关系与实际状态；按钮和接口均按操作权限校验。

#### SM-T04

**本地临时密码与改密门禁** · 故事 [SM-US04](03-user-stories.md#sm-us04) · 测试 [SM-UT04](05-unit-test-cases.md#sm-ut04)

- 依赖 [SM-T01](04-development-tasks.md#sm-t01)。
- 代码落点 identity/password.go + identity/session.go。
- `SM-T04-A` 服务端 · 实现待改密状态、密码哈希更新、会话限制和旧凭据撤销。普通管理员建号和重置策略待评审。
- `SM-T04-B` Web 或运维 · 实现临时密码改密表单、错误反馈、身份适配与成功后跳转。
- 验收输出 待改密会话仅能执行改密相关操作；重置访客密码立即撤销旧会话；LDAP 密码在目录侧处理。

#### SM-T05

**超级管理员服务器恢复** · 故事 [SM-US05](03-user-stories.md#sm-us05) · 测试 [SM-UT05](05-unit-test-cases.md#sm-ut05)

- 依赖 [SM-T01](04-development-tasks.md#sm-t01)、[SM-T04](04-development-tasks.md#sm-t04)、[SM-T16](04-development-tasks.md#sm-t16)。
- 代码落点 部署脚本目录 + identity/password.go。
- `SM-T05-A` 服务端 · 提供受服务器权限控制的 CLI/脚本入口，调用密码服务与审计；确认失败不能产生半成功状态。
- `SM-T05-B` Web 或运维 · 复用待改密页面，恢复流程在服务端运维入口执行。
- 验收输出 专用脚本只重置超级管理员并发放临时密码；旧会话立即失效；完成改密后才允许管理；记录恢复事件且无密码明文。

#### SM-T06

**LDAP 草稿四阶段测试和启用** · 故事 [SM-US06](03-user-stories.md#sm-us06) · 测试 [SM-UT06](05-unit-test-cases.md#sm-ut06)

- 依赖 [SM-T01](04-development-tasks.md#sm-t01)。
- 代码落点 permissions/identity.go + ldap_credentials.go。
- `SM-T06-A` 服务端 · 复用 LDAP 客户端和凭据存储，引入草稿版本绑定及四阶段测试输出；校验测试和启用操作并发。
- `SM-T06-B` Web 或运维 · 在 `identity-admin.ts` 与 `authentication.css` 实现独立输入行及响应式两区布局，展示四阶段及 DNS、TCP、LDAPS 握手和证书诊断。测试失败保留当前表单的员工凭据，成功后清空；保存成功后清空搜索密码输入框。保存反馈显示于保存按钮旁。
- `SM-T06-C` 传输由 LDAP URL 选择。明文需显式允许，LDAPS 校验证书。五分钟验证凭据绑定配置和操作者。SQLite 默认直接保存搜索密码，兼容有旧密钥时读取密文，缺少密钥时引导重填搜索密码。
- 验收输出 连接、绑定、查找指定员工、本人认证依次通过才可启用；测试不创建用户、会话或角色；草稿改动需复测；失败继续使用原配置。

#### SM-T07

**LDAP 首次登记与默认角色** · 故事 [SM-US07](03-user-stories.md#sm-us07) · 测试 [SM-UT07](05-unit-test-cases.md#sm-ut07)

- 依赖 [SM-T06](04-development-tasks.md#sm-t06)、[SM-T09](04-development-tasks.md#sm-t09)。
- 代码落点 identity/session.go + identitystore。
- `SM-T07-A` 服务端 · 正式登录分支原子完成账号登记、判断有效角色与补发默认角色，测试登录共用认证但跳过持久化。
- `SM-T07-B` Web 或运维 · 复用登录和用户列表，展示 LDAP 来源及默认角色。
- 验收输出 首次正式登录建立用户和默认角色；重复或并发登录不重复登记；仍有其他角色时保留默认角色撤销结果；失去最后一个角色时下次正式登录恢复默认角色。

#### SM-T08

**平台停用与 LDAP 目录状态** · 故事 [SM-US08](03-user-stories.md#sm-us08) · 测试 [SM-UT08](05-unit-test-cases.md#sm-ut08)

- 依赖 [SM-T07](04-development-tasks.md#sm-t07)、[SM-T16](04-development-tasks.md#sm-t16)。
- 代码落点 identity/session.go + LDAP 状态调度。
- `SM-T08-A` 服务端 · 实现两侧独立状态和调度探针；区分已确认停用与目录不可用，注入时钟，禁止滑动延长故障期间会话。
- `SM-T08-B` Web 或运维 · 显示停用与恢复原因；按账号状态呈现后续登录结果，自动撤权在审计可查。
- 验收输出 平台停用立即失效；目录可用时停用后 5 分钟内撤销全部会话；目录故障时保留会话原定到期时间，恢复后补查；双侧启用才允许登录并沿用原授权。

#### SM-T09

**预置角色保护** · 故事 [SM-US09](03-user-stories.md#sm-us09) · 测试 [SM-UT09](05-unit-test-cases.md#sm-ut09)

- 依赖 [SM-T01](04-development-tasks.md#sm-t01)。
- 代码落点 authorization/roles.go + roles.ts。
- `SM-T09-A` 服务端 · 在写服务和持久化入口保持保留角色标识，拒绝删除和编辑预置角色。仅允许超级管理员直接为 LDAP 员工授予或撤销 `system-admin`，组角色写入拒绝该标识。
- `SM-T09-B` Web 或运维 · 预置角色提供只读详情，隐藏编辑和删除按钮，展示禁止删除状态。
- 验收输出 预置权限固定，即使零关联也禁止删除。超级管理员直接授予后，员工保留 LDAP 身份及目录密码，重新登录取得普通管理员权限。

#### SM-T10

**自定义角色与历史诊断范围** · 故事 [SM-US10](03-user-stories.md#sm-us10) · 测试 [SM-UT10](05-unit-test-cases.md#sm-ut10)

- 依赖 [SM-T09](04-development-tasks.md#sm-t09)。
- 代码落点 authorization/model.go + roles.ts + resource_catalog.go。
- `SM-T10-A` 服务端 · 构建授权范围类型与验证器，使用资源目录稳定标识和当前归属，验证独立与共用集群。
- `SM-T10-B` Web 或运维 · 实现按环境、集群类型动态选项及授权预览，选择业务线后勾选具体命名空间。
- 验收输出 只支持历史诊断查看；研测可选整个环境、集群或指定命名空间；预发生产要求明确业务命名空间；同角色可组合多个环境权限项。

#### SM-T11

**平台用户组和批量角色绑定** · 故事 [SM-US11](03-user-stories.md#sm-us11) · 测试 [SM-UT11](05-unit-test-cases.md#sm-ut11)

- 依赖 [SM-T07](04-development-tasks.md#sm-t07)、[SM-T10](04-development-tasks.md#sm-t10)。
- 代码落点 identity_roles.go + identitystore + 权限配置 Web。
- `SM-T11-A` 服务端 · 实现分组及多角色关系写入和变更校验，分离 LDAP 目录组信息与平台组。平台组仅可关联数据角色。
- `SM-T11-B` Web 或运维 · 实现用户组列表、编辑、关联角色多选和逐项结果。
- 验收输出 平台组由管理员维护，LDAP 目录组变更不自动加组；可对一个组批量添加或移除角色；组员仅 LDAP 员工。

#### SM-T12

**自定义角色和用户组删除保护** · 故事 [SM-US12](03-user-stories.md#sm-us12) · 测试 [SM-UT12](05-unit-test-cases.md#sm-ut12)

- 依赖 [SM-T10](04-development-tasks.md#sm-t10)、[SM-T11](04-development-tasks.md#sm-t11)、[SM-T13](04-development-tasks.md#sm-t13)。
- 代码落点 identitystore + 角色和组写服务。
- `SM-T12-A` 服务端 · 删除检查与删除在同一一致性边界，防止并发加组导致悬挂引用。
- `SM-T12-B` Web 或运维 · 显示删除确认及具体阻塞对象，失败保持原详情。
- 验收输出 角色有直接用户或用户组引用则拒绝，用户组有成员或角色则拒绝；解除全部引用才可删除；列出阻塞对象。

#### SM-T13

**单人和批量授权或撤销** · 故事 [SM-US13](03-user-stories.md#sm-us13) · 测试 [SM-UT13](05-unit-test-cases.md#sm-ut13)

- 依赖 [SM-T07](04-development-tasks.md#sm-t07)、[SM-T09](04-development-tasks.md#sm-t09)、[SM-T10](04-development-tasks.md#sm-t10)、[SM-T11](04-development-tasks.md#sm-t11)。
- 代码落点 identity_roles.go + adminhttp + 用户管理 Web。
- `SM-T13-A` 服务端 · 按操作者权限和目标账号逐项校验。超级管理员可直接变更 LDAP 员工全部平台角色。普通系统管理员仅可为普通 LDAP 员工变更自定义角色或访客角色。LDAP 管理员仅由超级管理员管理。默认角色保护按操作后的剩余角色判定。
- `SM-T13-A` 会话联动 · LDAP 员工直接管理员角色实际发生变化时，角色关系与会话版本同步提交，旧会话失效。重复授予或撤销按幂等处理。数据角色变化继续由当前会话在后续请求中重新校验。
- `SM-T13-B` Web 或运维 · 实现多选、按当前操作者展示直接角色、逐项成功或失败汇总及审计定位。普通管理员可读 LDAP 管理员详情，选择和变更入口仅向超级管理员开放。超级管理员的直接角色选择区提示管理员角色变更后需要重新登录。
- 验收输出 支持加入、移出、授予和撤销，符合条件的项目独立成功，每项可追踪。系统管理员角色变更按高风险审计。现有默认角色和本地账号入组保护继续生效。

#### SM-T14

**权限并集与访问来源** · 故事 [SM-US14](03-user-stories.md#sm-us14) · 测试 [SM-UT14](05-unit-test-cases.md#sm-ut14)

- 依赖 [SM-T08](04-development-tasks.md#sm-t08)、[SM-T10](04-development-tasks.md#sm-t10)、[SM-T13](04-development-tasks.md#sm-t13)。
- 代码落点 authorization/policy.go + httpapi + 用户详情 Web。
- `SM-T14-A` 服务端 · 以同一授权判定支撑诊断访问与可解释来源，依赖当前身份状态和最新角色关系。
- `SM-T14-B` Web 或运维 · 详情呈现范围与全部来源，撤销后重新读取实际授权。
- 验收输出 对有效权限求并集，每项列出直接角色和各组继承来源；撤权即时生效，仍有一条独立有效路径时继续可访问。

#### SM-T15

**诊断隔离与业务线改派** · 故事 [SM-US15](03-user-stories.md#sm-us15) · 测试 [SM-UT15](05-unit-test-cases.md#sm-ut15)

- 依赖 [SM-T14](04-development-tasks.md#sm-t14)、[SM-T17](04-development-tasks.md#sm-t17)。
- 代码落点 api/internal/httpapi + authorization + 诊断 Web。
- `SM-T15-A` 服务端 · 为列表、汇总、详情及可用范围提供一致服务端过滤，按资源最新归属重判并防止旁路。
- `SM-T15-B` Web 或运维 · 按授权返回范围渲染筛选器，处理旧链接失效和无数据提示。
- 验收输出 列表和单条读取均验证范围；生产或预发集群级诊断仅管理员可见；共享空间允许经任一授权路径查看完整历史；归属变化即时重判。

#### SM-T16

**操作审计写入与检索** · 故事 [SM-US16](03-user-stories.md#sm-us16) · 测试 [SM-UT16](05-unit-test-cases.md#sm-ut16)

- 依赖 现有存储、身份或审计契约。
- 代码落点 `api/internal/identitystore/sqlite.go`、`api/internal/permissions`、`api/internal/portal/routes.go` 与 `web/src/views/permissions/audit*.ts`。
- `SM-T16-A` 服务端 · 复用 audit 事件模型，按账号、操作类型、风险和批次过滤并分页。写入时按当前策略跳过保留天数为 0 的等级，查询排除这些等级和已超期记录。
- `SM-T16-B` Web · 按[交互原型](admin-workspace.html#audit)展示三档策略、独立筛选和分页、可聚焦的横向滚动表格。详情显示操作标题、风险、结果、操作者、目标、北京时间及失败原因。关闭后还原焦点，打开同批次时切换为批次筛选。编辑策略时，正数改为 0 须确认清理范围。取消确认返回编辑器并保留输入。
- `SM-T16-B` 分页 · 审计列表用 `page` 和 `limit` 请求当前页，服务端返回筛选总数和实际页码。快捷选项为 10、20、50，默认 10。自定义仅接受 1–100 的整数。保留筛选、页码和页大小以支持刷新。首次加载、变更页大小、变更筛选和空结果均显示页脚。旧批次游标请求继续可用。
- `SM-T16-C` SQLite · 原子迁移保留策略的取值约束，保留已有配置及默认 3 / 5 / 7 天。保存须显式提供三个非负整数，仅超管可执行。事务内更新策略，删除 0 天等级的全部历史，清理其他超期记录。策略变更事件适用更新后的高风险规则。最近登录时间独立保存。
- `SM-T16-D` 会话与回归 · 读取、翻页、详情和保存遇 401 时移除当前记录与草稿，登录后只重新读取原地址和筛选。遇 403 时展示无访问权限。覆盖迁移、0 天确认及取消、恢复正数和会话回归。
- 验收输出 记录状态或配置变化、登录成功失败、系统撤权、脚本恢复和批量结果；普通管理员可查超管管理员生命周期事件；风险规则内置、输出脱敏。

#### SM-T17

**资源管理权限与首采环境锁定** · 故事 [SM-US17](03-user-stories.md#sm-us17) · 测试 [SM-UT17](05-unit-test-cases.md#sm-ut17)

- 依赖 [SM-T01](04-development-tasks.md#sm-t01)。
- 代码落点 portal/managed_clusters.go + resource_catalog.go + 资源管理 Web。
- `SM-T17-A` 服务端 · 在资源写接口收敛权限，环境修改与首采状态使用同一持久化条件，回收跨环境迁移能力。
- `SM-T17-B` Web 或运维 · 接入详情按首采状态显示可编辑或锁定，提示因并发首采导致改派失败。
- 验收输出 仅管理员可操作资源管理；真实节点状态或首条真实 K8sGPT 诊断任一成功即锁定；首采后环境不可变更。

### 评审待确认

- [SM-T01](04-development-tasks.md#sm-t01)、[SM-T04](04-development-tasks.md#sm-t04) · 日常改密入口及普通管理员建号和重置流程。先实现已确认的访客和超管改密门禁。
- [SM-T13](04-development-tasks.md#sm-t13)、[SM-T16](04-development-tasks.md#sm-t16) · 整批失败审计是否持久化和详细风险等级映射。先实现部分成功及逐项结果。

### 验证入口

按改动范围运行 `make api-test`、`make api-contract-check`、`make web-check`。真实 LDAP、SQLite 事务约束和浏览器会话联动另做集成与端到端验证。

### 服务端恢复超级管理员密码

在可访问身份 SQLite 数据库的服务器或容器内，将临时密码通过标准输入交给 `kube-aiops-api admin-reset-password`。命令读取部署的 `AUTH_SQLITE_PATH`。重置后原会话立即失效，超级管理员使用临时密码登录并完成改密。重置事件写入操作审计。

### 授权与资源目录联动

生产和预发布权限关联实时命名空间归属。独立业务集群只接受所属业务线，共用集群允许多条业务线。系统管理员在「资源管理 → 集群接入 → 命名空间归属」配置业务线归属，随后在「系统管理 → 权限配置」选择命名空间。修改业务线目录 ID 后，历史关联通过别名定位相同业务线。

#### SM-T18

依赖现有账号、用户组和角色接口。[SM-US18](03-user-stories.md#sm-us18) · [SM-UT18](05-unit-test-cases.md#sm-ut18)

- Web 在 `platform-admin.ts` 就地切换详情、保持筛选及键盘焦点，按当前选中项开启批量操作。
- 直接授权与批量弹窗共用搜索及结果视图。沿用服务端账号和管理员边界。

#### SM-T19

依赖已有权限配置与命名空间归属查询。[SM-US19](03-user-stories.md#sm-us19) · [SM-UT19](05-unit-test-cases.md#sm-ut19)

- Web 复用角色列表和用户组内的角色编辑器，加入多选、去重、回退、差异和引用预览。
- API 在现有事务内验证编辑时的角色快照，保留现有集群、业务线、命名空间校验与单次写入。环境、业务线、集群和账号标识沿用存量。

#### SM-T20

依赖现有操作审计、批次和分页。[SM-US20](03-user-stories.md#sm-us20) · [SM-UT20](05-unit-test-cases.md#sm-ut20)

- API 保留授权响应体，为确实写入且事务提交的事件返回标识。同源内部接口通过 `X-Operation-Audit-Ref` 返回稳定标识，可通过 `GET /api/v1/admin/operations?record=…` 精确查询。SQLite 增量迁移保留存量记录及原分页标识。
- Web 接通账号筛选和结果详情，复用审计详情及同批次查询。添加现有认证、改密及筛选回归。

#### SM-T21

**认证配置布局与登录会话策略** · [SM-P03](01-pages.md#sm-p03) · [SM-F21](02-features.md#sm-f21) · [SM-UT21](05-unit-test-cases.md#sm-ut21)

1. SQLite 使用幂等迁移为新库与存量库初始化 30 分钟策略，持久化范围为 5 至 480。变更策略和写入高风险审计共用事务，记录操作者、时间与新旧分钟值。失败时回滚。
2. 在管理入口提供 `GET /api/v1/admin/session-policy` 与 `PUT /api/v1/admin/session-policy`。GET 允许已认证且已完成改密的系统管理员。PUT 复用同源检查，仅允许本地超级管理员。请求体为 `{"sessionLifetimeMinutes":60}`，两种成功响应均为 200 和当前策略 JSON。GET 与 PUT 响应使用 `Cache-Control: no-store`。
3. 服务端返回 400 表示非法输入，401 表示未登录或会话失效，403 表示权限不足或写请求非同源，503 表示策略存储不可用。身份校验通过后读取策略，再以同一时长计算服务端到期时间和 Cookie `Max-Age`。读取失败按 503 处理，已有会话不受配置变更影响。
4. Web 走原有认证配置路由、权限门禁及表单处理入口。页面外框对照 [共享外框原型的认证配置页](../portal-ui/shell-regression.html#/admin/ldap)。使用原生 `details` 和 `summary` 展示默认收起的登录会话与 LDAP。折叠状态、标题数值、只读、错误反馈对照 [系统管理认证配置原型](admin-workspace.html#ldap)。保留 LDAP 草稿、测试凭据处理与验证通过才可启用的流程。
5. 分别验收 1440px 桌面与 390px 窄屏。对照原型核对顶栏、侧栏、高亮、面包屑、卡片顺序、分栏和折叠状态。运行 Go 会话、SQLite 和路由测试，运行 Web 测试、构建及浏览器认证配置场景。

## 平台访问控制兼容设计

本节记录授权模型、上线迁移与历史验收。页面布局、入口、授权按钮和动作分组以角色与权限管理工作区为准。

[PRD](00-prd.md#平台访问控制兼容设计) · [用户故事](03-user-stories.md#平台访问控制兼容设计) · [测试](05-unit-test-cases.md#平台访问控制兼容设计)

P0 先冻结 T04 的裁决契约及 T07 的数据格式，可并行开发前端原型接口。优先完成统一裁决、兼容存储与迁移，再接入 T01/T03 和 T02/T05/T06。P0 回归通过后单独部署 P1 的 T08。每个开发 PR 列出对应故事和测试编号。

### AC-T01

**账号与组角色关系** · [AC-US01](03-user-stories.md#ac-us01) · [AC-UT01](05-unit-test-cases.md#ac-ut01)

依赖 T04 的主体校验。复用 `platform_http.go` 的批量处理、`platform_state.go` 的组与直接角色关系；更新 `platform-admin.ts` 的逐项结果及禁止状态。验证新旧负载均拒绝把管理员角色绑定到组。

### AC-T02

**生效权限与模拟检查** · [AC-US02](03-user-stories.md#ac-us02) · [AC-UT02](05-unit-test-cases.md#ac-ut02)

依赖 T04 的统一判权、T03 的权限条目。扩展账号详情 DTO，并新增管理员只读模拟接口。复用 `authorization` 裁决，前端改进生效权限视图及模拟面板。

### AC-T03

**动作范围与角色编辑** · [AC-US03](03-user-stories.md#ac-us03) · [AC-UT03](05-unit-test-cases.md#ac-ut03)

依赖 T04 的范围规范和 T07 的存储契约。扩展角色 DTO、范围校验及前端角色编辑。保存请求携带期望版本，提交时重新校验目标归属。接口变更同步 OpenAPI 与前端类型。

### AC-T04

**统一裁决与资源范围** · [AC-US04](03-user-stories.md#ac-us04) · [AC-UT04](05-unit-test-cases.md#ac-ut04)

先冻结动作目录、范围结构和错误返回。统一 `authorization/model.go`、`platform_authorization.go` 及 Portal / Wiki 边界解析。依据资源目录识别实例与命名空间；会话、归属与角色变更在下一次请求重新校验。

### AC-T05

**模块入口与数据输出一致性** · [AC-US05](03-user-stories.md#ac-us05) · [AC-UT05](05-unit-test-cases.md#ac-ut05)

依赖 T04。逐一登记 Portal 路由、诊断、知识检索和同步的动作映射。以同一个裁决结果驱动菜单、API 和任务。连接事件、缓存和模型回答使用当时有效授权。

### AC-T06

**操作审计与身份保护** · [AC-US06](03-user-stories.md#ac-us06) · [AC-UT06](05-unit-test-cases.md#ac-ut06)

依赖 T01、T03、T04。复用现有操作审计持久化及列表。为角色和组变更设计可追溯差异及事务边界。审计记录遵守既定保留策略；后台失败原因只输出脱敏摘要。

### AC-T07

**等价迁移与上线回退** · [AC-US07](03-user-stories.md#ac-us07) · [AC-UT07](05-unit-test-cases.md#ac-ut07)

依赖 T04 的契约，实现在 `api/internal/identitystore` 和已有权限存储。准备脱敏样本、历史决策回放、备份核验、部署顺序及回退演练。发现无法等价者记录差异并由指定人员处置。

### AC-T08

**Wiki 只读试点** · [AC-US08](03-user-stories.md#ac-us08) · [AC-UT08](05-unit-test-cases.md#ac-ut08)

P1，依赖 T04、T05、T07。更新平台动作目录和 Wiki 读入口，分别启用组与资源目标。与知识库 PRD 的可见版本规则联调，记录试点范围、回退操作及验证结果。

## AI 服务管理

**SM-T90**

`api/internal/portal/ai_services.go` 提供状态与检查接口，`routes.go` 授权，`api/internal/holmes/client.go` 投影绑定和发起检查。`web/src/views/ai/services.ts` 渲染环境、服务和技能页。配置由现有 Secret 管理，现场操作见[部署指南](../install-deployment.md#部署-holmesgpt)。

[测试用例](05-unit-test-cases.md#ai-服务管理)
