# 系统管理 · 开发任务

- 需求基线 [首版 PRD](00-prd.md)
- 交互参考 [HTML 原型](prototype.html)
- [模块开发规范](../development-standard.md)
- [页面拆解](01-pages.md) · [功能拆解](02-features.md) · [用户故事](03-user-stories.md) · [开发任务](04-development-tasks.md) · [单元测试用例](05-unit-test-cases.md)

## 实施顺序

1. 身份、会话与审计底座（SM-T01、SM-T04、SM-T16），接通本地与 LDAP 账号流程（SM-T02、SM-T03、SM-T05 至 SM-T08）。引导初始化的角色常量先由底座提供，SM-T09 再接入写操作的保护。
2. 固定角色与数据范围，完成组管理、批量授权和权限解释（SM-T09 至 SM-T14）。
3. 接通集群首采后的环境锁定及历史诊断权限（SM-T17、SM-T15）。

对外接口变更同步 `api/openapi.yaml` 和生成客户端。复用现有权限、会话和资源模块，具体文件以 Coding 分支为准。每项任务以故事验收、单测和适用的接口契约校验为完成条件。

### SM-T01

**系统初始化和管理员管理** · 故事 [SM-US01](03-user-stories.md#sm-us01) · 测试 [SM-UT01](05-unit-test-cases.md#sm-ut01)

- 依赖 现有存储、身份或审计契约。
- 代码落点 identitystore + permissions。
- `SM-T01-A` 服务端 · 初始化与管理员写服务，原子维护唯一身份和会话撤销，区分普通与超级管理员。
- `SM-T01-B` Web 或运维 · 在列表和详情按权限显示新建、删除及确认，成功后刷新；超管删除在 API 层拒绝。
- 验收输出 并发初始化仍只有一个超级管理员；只有超级管理员可创建或删除普通管理员；删除后立即拒绝旧会话且产生审计；超级管理员不可删除。

### SM-T02

**访客创建、停用和恢复** · 故事 [SM-US02](03-user-stories.md#sm-us02) · 测试 [SM-UT02](05-unit-test-cases.md#sm-ut02)

- 依赖 [SM-T01](04-development-tasks.md#sm-t01)、[SM-T04](04-development-tasks.md#sm-t04)。
- 代码落点 identity_admin + identitystore。
- `SM-T02-A` 服务端 · 访客创建及状态更新服务，保留关联角色；用统一撤销机制处理停用。
- `SM-T02-B` Web 或运维 · 新增访客表单、临时密码发放结果、停用和恢复确认及状态反馈。
- `SM-T02-C` 删除复用账号变更接口。事务内按身份校验、清理平台账号和关联，记录高风险审计。LDAP 登录登记拒绝已删除的目录身份。
- 验收输出 新访客关联固定只读角色；停用立即阻断登录和旧会话；恢复沿用原角色且不复活旧会话；选中单个可删除的用户后可发起删除，确认后刷新列表并保留审计。

### SM-T03

- 布局验收（PR #93 待合并）长 LDAP DN 换行，状态与操作同排，用户组和直接角色分行，关联标签可换行，分配按钮在右下角。用 1440px 和 390px 验证停用确认及授权弹窗。

**搜索用户与账号详情** · 故事 [SM-US03](03-user-stories.md#sm-us03) · 测试 [SM-UT03](05-unit-test-cases.md#sm-ut03)

- 依赖 [SM-T01](04-development-tasks.md#sm-t01)。
- 代码落点 portal/routes + identity-admin.ts。
- `SM-T03-A` 服务端 · 提供账号列表、详情读接口和统一查询投影；权限判断复用管理员身份。
- `SM-T03-B` Web 或运维 · 实现筛选、空态、加载、错误和详情导航；切换筛选时明确批量选中范围。
- 验收输出 列表可按账号及名称搜索并按来源、状态筛选；详情展示关系与实际状态；按钮和接口均按操作权限校验。

### SM-T04

**本地临时密码与改密门禁** · 故事 [SM-US04](03-user-stories.md#sm-us04) · 测试 [SM-UT04](05-unit-test-cases.md#sm-ut04)

- 依赖 [SM-T01](04-development-tasks.md#sm-t01)。
- 代码落点 identity/password.go + identity/session.go。
- `SM-T04-A` 服务端 · 实现待改密状态、密码哈希更新、会话限制和旧凭据撤销。普通管理员建号和重置策略待评审。
- `SM-T04-B` Web 或运维 · 实现临时密码改密表单、错误反馈、身份适配与成功后跳转。
- 验收输出 待改密会话仅能执行改密相关操作；重置访客密码立即撤销旧会话；LDAP 密码在目录侧处理。

### SM-T05

**超级管理员服务器恢复** · 故事 [SM-US05](03-user-stories.md#sm-us05) · 测试 [SM-UT05](05-unit-test-cases.md#sm-ut05)

- 依赖 [SM-T01](04-development-tasks.md#sm-t01)、[SM-T04](04-development-tasks.md#sm-t04)、[SM-T16](04-development-tasks.md#sm-t16)。
- 代码落点 部署脚本目录 + identity/password.go。
- `SM-T05-A` 服务端 · 提供受服务器权限控制的 CLI/脚本入口，调用密码服务与审计；确认失败不能产生半成功状态。
- `SM-T05-B` Web 或运维 · 复用待改密页面，恢复流程在服务端运维入口执行。
- 验收输出 专用脚本只重置超级管理员并发放临时密码；旧会话立即失效；完成改密后才允许管理；记录恢复事件且无密码明文。

### SM-T06

**LDAP 草稿四阶段测试和启用** · 故事 [SM-US06](03-user-stories.md#sm-us06) · 测试 [SM-UT06](05-unit-test-cases.md#sm-ut06)

- 依赖 [SM-T01](04-development-tasks.md#sm-t01)。
- 代码落点 permissions/identity.go + ldap_credentials.go。
- `SM-T06-A` 服务端 · 复用 LDAP 客户端和凭据存储，引入草稿版本绑定及四阶段测试输出；校验测试和启用操作并发。
- `SM-T06-B` Web 或运维 · 展示四阶段及 DNS、TCP、LDAPS 握手和证书诊断。测试失败保留当前表单的员工凭据，成功后清空；保存成功后清空搜索密码输入框。保存反馈显示于保存按钮旁。
- `SM-T06-C` 传输由 LDAP URL 选择。明文需显式允许，LDAPS 校验证书。五分钟验证凭据绑定配置和操作者。SQLite 默认直接保存搜索密码，兼容有旧密钥时读取密文，缺少密钥时引导重填搜索密码。
- 验收输出 连接、绑定、查找指定员工、本人认证依次通过才可启用；测试不创建用户、会话或角色；草稿改动需复测；失败继续使用原配置。

### SM-T07

**LDAP 首次登记与默认角色** · 故事 [SM-US07](03-user-stories.md#sm-us07) · 测试 [SM-UT07](05-unit-test-cases.md#sm-ut07)

- 依赖 [SM-T06](04-development-tasks.md#sm-t06)、[SM-T09](04-development-tasks.md#sm-t09)。
- 代码落点 identity/session.go + identitystore。
- `SM-T07-A` 服务端 · 正式登录分支原子完成账号登记、判断有效角色与补发默认角色，测试登录共用认证但跳过持久化。
- `SM-T07-B` Web 或运维 · 复用登录和用户列表，展示 LDAP 来源及默认角色。
- 验收输出 首次正式登录建立用户和默认角色；重复或并发登录不重复登记；仍有其他角色时保留默认角色撤销结果；失去最后一个角色时下次正式登录恢复默认角色。

### SM-T08

**平台停用与 LDAP 目录状态** · 故事 [SM-US08](03-user-stories.md#sm-us08) · 测试 [SM-UT08](05-unit-test-cases.md#sm-ut08)

- 依赖 [SM-T07](04-development-tasks.md#sm-t07)、[SM-T16](04-development-tasks.md#sm-t16)。
- 代码落点 identity/session.go + LDAP 状态调度。
- `SM-T08-A` 服务端 · 实现两侧独立状态和调度探针；区分已确认停用与目录不可用，注入时钟，禁止滑动延长故障期间会话。
- `SM-T08-B` Web 或运维 · 显示停用与恢复原因；按账号状态呈现后续登录结果，自动撤权在审计可查。
- 验收输出 平台停用立即失效；目录可用时停用后 5 分钟内撤销全部会话；目录故障时保留会话原定到期时间，恢复后补查；双侧启用才允许登录并沿用原授权。

### SM-T09

**预置角色保护** · 故事 [SM-US09](03-user-stories.md#sm-us09) · 测试 [SM-UT09](05-unit-test-cases.md#sm-ut09)

- 依赖 [SM-T01](04-development-tasks.md#sm-t01)。
- 代码落点 authorization/roles.go + roles.ts。
- `SM-T09-A` 服务端 · 在写服务和持久化入口保持保留角色标识，拒绝删除、编辑和跨身份授权。
- `SM-T09-B` Web 或运维 · 预置角色提供只读详情，隐藏编辑和删除按钮，展示禁止删除状态。
- 验收输出 预置权限固定，不为 LDAP 员工授予系统管理员角色；即使没有任何账号关联也禁止删除两个预置角色。

### SM-T10

**自定义角色与历史诊断范围** · 故事 [SM-US10](03-user-stories.md#sm-us10) · 测试 [SM-UT10](05-unit-test-cases.md#sm-ut10)

- 依赖 [SM-T09](04-development-tasks.md#sm-t09)。
- 代码落点 authorization/model.go + roles.ts + resource_catalog.go。
- `SM-T10-A` 服务端 · 构建授权范围类型与验证器，使用资源目录稳定标识和当前归属，验证独立与共用集群。
- `SM-T10-B` Web 或运维 · 实现按环境、集群类型动态选项及授权预览，选择业务线后勾选具体命名空间。
- 验收输出 只支持历史诊断查看；研测可选整个环境、集群或指定命名空间；预发生产要求明确业务命名空间；同角色可组合多个环境权限项。

### SM-T11

**平台用户组和批量角色绑定** · 故事 [SM-US11](03-user-stories.md#sm-us11) · 测试 [SM-UT11](05-unit-test-cases.md#sm-ut11)

- 依赖 [SM-T07](04-development-tasks.md#sm-t07)、[SM-T10](04-development-tasks.md#sm-t10)。
- 代码落点 identity_roles.go + identitystore + 权限配置 Web。
- `SM-T11-A` 服务端 · 实现分组及多角色关系写入和变更校验，分离 LDAP 目录组信息与平台组。
- `SM-T11-B` Web 或运维 · 实现用户组列表、编辑、关联角色多选和逐项结果。
- 验收输出 平台组由管理员维护，LDAP 目录组变更不自动加组；可对一个组批量添加或移除角色；组员仅 LDAP 员工。

### SM-T12

**自定义角色和用户组删除保护** · 故事 [SM-US12](03-user-stories.md#sm-us12) · 测试 [SM-UT12](05-unit-test-cases.md#sm-ut12)

- 依赖 [SM-T10](04-development-tasks.md#sm-t10)、[SM-T11](04-development-tasks.md#sm-t11)、[SM-T13](04-development-tasks.md#sm-t13)。
- 代码落点 identitystore + 角色和组写服务。
- `SM-T12-A` 服务端 · 删除检查与删除在同一一致性边界，防止并发加组导致悬挂引用。
- `SM-T12-B` Web 或运维 · 显示删除确认及具体阻塞对象，失败保持原详情。
- 验收输出 角色有直接用户或用户组引用则拒绝，用户组有成员或角色则拒绝；解除全部引用才可删除；列出阻塞对象。

### SM-T13

**单人和批量授权或撤销** · 故事 [SM-US13](03-user-stories.md#sm-us13) · 测试 [SM-UT13](05-unit-test-cases.md#sm-ut13)

- 依赖 [SM-T07](04-development-tasks.md#sm-t07)、[SM-T09](04-development-tasks.md#sm-t09)、[SM-T10](04-development-tasks.md#sm-t10)、[SM-T11](04-development-tasks.md#sm-t11)。
- 代码落点 identity_roles.go + adminhttp + 用户管理 Web。
- `SM-T13-A` 服务端 · 实现批量逐项验证和结果，默认角色保护按操作完成后剩余角色判定；绑定持久化和审计遵循评审规则。
- `SM-T13-B` Web 或运维 · 实现多选、选择目标、逐项成功或失败汇总及审计定位。
- 验收输出 支持加入、移出、授予和撤销，符合条件的项目独立成功；每项可追踪；不可撤销唯一默认角色，也不能给本地用户分配平台组。

### SM-T14

**权限并集与访问来源** · 故事 [SM-US14](03-user-stories.md#sm-us14) · 测试 [SM-UT14](05-unit-test-cases.md#sm-ut14)

- 依赖 [SM-T08](04-development-tasks.md#sm-t08)、[SM-T10](04-development-tasks.md#sm-t10)、[SM-T13](04-development-tasks.md#sm-t13)。
- 代码落点 authorization/policy.go + httpapi + 用户详情 Web。
- `SM-T14-A` 服务端 · 以同一授权判定支撑诊断访问与可解释来源，依赖当前身份状态和最新角色关系。
- `SM-T14-B` Web 或运维 · 详情呈现范围与全部来源，撤销后重新读取实际授权。
- 验收输出 对有效权限求并集，每项列出直接角色和各组继承来源；撤权即时生效，仍有一条独立有效路径时继续可访问。

### SM-T15

**诊断隔离与业务线改派** · 故事 [SM-US15](03-user-stories.md#sm-us15) · 测试 [SM-UT15](05-unit-test-cases.md#sm-ut15)

- 依赖 [SM-T14](04-development-tasks.md#sm-t14)、[SM-T17](04-development-tasks.md#sm-t17)。
- 代码落点 api/internal/httpapi + authorization + 诊断 Web。
- `SM-T15-A` 服务端 · 为列表、汇总、详情及可用范围提供一致服务端过滤，按资源最新归属重判并防止旁路。
- `SM-T15-B` Web 或运维 · 按授权返回范围渲染筛选器，处理旧链接失效和无数据提示。
- 验收输出 列表和单条读取均验证范围；生产或预发集群级诊断仅管理员可见；共享空间允许经任一授权路径查看完整历史；归属变化即时重判。

### SM-T16

**操作审计写入与检索** · 故事 [SM-US16](03-user-stories.md#sm-us16) · 测试 [SM-UT16](05-unit-test-cases.md#sm-ut16)

- 依赖 现有存储、身份或审计契约。
- 代码落点 api/internal/audit + identitystore + 操作审计 Web。
- `SM-T16-A` 服务端 · 复用现有 audit 事件模型，扩展持久化查询与事件分级，按账号、操作类型、风险过滤并分页。
- `SM-T16-B` Web 或运维 · 提供审计列表、批次逐项详情和查询失败状态，批量记录可按目标账号查找。时间统一显示北京时间到秒。
- `SM-T16-C`（PR #91 待合并）SQLite 持久化低、中、高的独立正整数天数，默认 3 / 5 / 7 天。仅超管可保存。事务中清理超期记录和追加高风险审计。启动及每小时清理，查询直接排除超期记录。最近登录时间独立持久化。
- 验收输出 记录状态或配置变化、登录成功失败、系统撤权、脚本恢复和批量结果；普通管理员可查超管管理员生命周期事件；风险规则内置、输出脱敏。

### SM-T17

**资源管理权限与首采环境锁定** · 故事 [SM-US17](03-user-stories.md#sm-us17) · 测试 [SM-UT17](05-unit-test-cases.md#sm-ut17)

- 依赖 [SM-T01](04-development-tasks.md#sm-t01)。
- 代码落点 portal/managed_clusters.go + resource_catalog.go + 资源管理 Web。
- `SM-T17-A` 服务端 · 在资源写接口收敛权限，环境修改与首采状态使用同一持久化条件，回收跨环境迁移能力。
- `SM-T17-B` Web 或运维 · 接入详情按首采状态显示可编辑或锁定，提示因并发首采导致改派失败。
- 验收输出 仅管理员可操作资源管理；真实节点状态或首条真实 K8sGPT 诊断任一成功即锁定；首采后环境不可变更。

## 评审待确认

- [SM-T01](04-development-tasks.md#sm-t01)、[SM-T04](04-development-tasks.md#sm-t04) · 日常改密入口及普通管理员建号和重置流程。先实现已确认的访客和超管改密门禁。
- [SM-T13](04-development-tasks.md#sm-t13)、[SM-T16](04-development-tasks.md#sm-t16) · 整批失败审计是否持久化和详细风险等级映射。先实现部分成功及逐项结果。

## 验证入口

按改动范围运行 `make api-test`、`make api-contract-check`、`make web-check`。真实 LDAP、SQLite 事务约束和浏览器会话联动另做集成与端到端验证。

## 服务端恢复超级管理员密码

在可访问身份 SQLite 数据库的服务器或容器内，将临时密码通过标准输入交给 `kube-aiops-api admin-reset-password`。命令读取部署的 `AUTH_SQLITE_PATH`。重置后原会话立即失效，超级管理员使用临时密码登录并完成改密。重置事件写入操作审计。

## 授权与资源目录联动

生产和预发布权限关联实时命名空间归属。独立业务集群只接受所属业务线，共用集群允许多条业务线。系统管理员在「资源管理 → 集群接入 → 命名空间归属」配置业务线归属，随后在「系统管理 → 权限配置」选择命名空间。修改业务线目录 ID 后，历史关联通过别名定位相同业务线。
