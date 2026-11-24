# kube-aiops 权限目录产品设计

## 权限管理工作区重构 · 2026-11-24

[重构设计](workspace-redesign.md) · [可点击 HTML 原型](workspace-redesign.html)

权限管理默认打开角色工作区。管理员用实际业务页面、现名、旧名、授权编码、接口及动作筛选权限，然后在同一界面指定范围、检查授权清单和预览保存。只读目录、用户组和预置角色保留现有授权限制。导航位置、展示名称与顺序由同一页面注册表派生。既有权限编码、审计、可委派能力和数据范围契约继续使用。

下文的 CRUD 矩阵描述历史实现，动作列表以新版设计为准。

**开发基线 · 2026-11-20 · 基线 `main@a18717e`（含 PR #106）**

**权限点勾选修订 · 2026-11-23 · 基线 `main@9e3753b`**

[页面拆解](01-pages.md) · [管理员组与勾选原型](group-admin-prototype.html) · [权限目录原型](prototype.html) · [功能拆解](02-features.md) · [用户故事](03-user-stories.md) · [开发任务](04-development-tasks.md) · [测试用例](05-unit-test-cases.md)

## 1. 产品决策

把现有「权限目录」入口展开为独立工作区。管理员按模块和工具集合查找功能，在增、删、改、查四列看到可用动作，点击单元格即可展开接口及授权条件。角色编辑复用同一份目录，勾选最小必要的接口权限点，明确数据范围，预览变更再保存。

| 层级 | 设计 | 示例 |
| --- | --- | --- |
| 权限模块 | 诊断控制台、运维知识库、资源管理、系统管理四个独立模块 | 诊断控制台 |
| 工具集合 | 按运维工作归并同类功能，支持批量勾选本集合中当前可见的可授权动作 | AI 诊断复核 |
| 功能点 | 业务对象或一项独立工作能力，每行都有增、删、改、查四列 | AI 复核结论 |
| 接口权限点 | 最小授权单元。接口方法、路由及经过服务端校验的动作共同确定权限 | 保存复核结论 · `diagnosis.review.save` |
| 数据范围 | 与一个权限点绑定的同一条授权，每次请求重新核验资源归属 | 测试、`test-east-01`、门店业务线、`payments-test` |

每个一级模块拥有稳定标识和独立入口。工具集合只在各自模块内组织业务功能。后续增加模块时，在目录注册表登记新的一级分类，角色配置没有跨模块隐式勾选。

**四列的业务含义**

| 列 | 归类标准 | 典型动作 |
| --- | --- | --- |
| 增 | 创建业务对象、关联或启动一次计算／同步任务 | 新建草稿、绑定用户组、发起 AI 复核 |
| 删 | 撤销关联、放弃、停用或撤回对象 | 撤销用户角色、撤回知识、停用来源 |
| 改 | 变更对象字段、状态、版本或凭据 | 保存复核、提交审核、发布、轮换凭据 |
| 查 | 查看列表、详情、汇总，或执行无状态变更的受控检查 | 查看证据、脱敏预览、LDAP 配置测试 |

一个格可以包含多个独立权限点。列头和集合名只用于导航与批量操作。某个功能缺少对应动作时显示「—」，提供原因并保持不可授权。例如 Finding 只有读取，审计事件系统写入，知识撤回保留版本。风险标签由目录维护，分类取决于业务效果，使用 POST 的预览可以属于「查」，测试连接仍单独标为敏感并限制目的地。

## 2. 权限目录与授权流程

| 界面 | 交互 | 管理员决策 |
| --- | --- | --- |
| 权限目录 | 保留「系统管理 → 权限管理 → 权限目录」入口。四个独立模块页签，模块内按工具集合分组。支持按权限名称、编码、接口路径搜索并筛选高风险动作 | 先找到业务按钮，再查看实际授权点 |
| 接口详情 | 展示权限点编码、方法、路由、分流条件、风险、数据范围和接口状态。单元格中独立展示每个可授权动作 | 判断同一接口的哪个动作需要开放 |
| 角色编辑 | 选择数据范围，按接口权限点勾选。支持按集合或当前筛选结果显式批量勾选。管理专属动作不可配置，高风险在授权清单和确认页标记 | 减少重复勾选，保留写权限控制 |
| 变更预览 | 展示新增、撤销、范围变化和高风险项。按用户直授和组继承预估影响，对账号去重。确认后展示审核或操作审计入口 | 看清本次改变了哪些接口、哪些环境和哪些用户 |
| 生效权限 | 用户详情沿用现有入口，列出同一权限的全部来源，可按账号定位审计 | 撤掉一个来源后，解释其他来源是否仍然授权 |

目录为空、无匹配、不可授权、数据范围解析失败和目录版本变化均有对应状态。仅展示已授予和待保存项时，不隐藏同一权限跨范围的其他已授权项。已知版本冲突阻止覆盖，并允许重新加载、核对差异和重新预览。生产扩大范围、知识发布等现有业务约束继续由已注册的动作处理器校验。原型的账号、资源范围及保存过程使用示例数据。

## 3. 授权与接口契约

**保留四类关系。** 用户关联角色、用户关联用户组、用户组关联角色、角色关联权限点列表。用户管理、LDAP 四阶段验证、单人及批量授权、操作审计保留各自现行入口和业务规则。超级管理员可直接授予系统管理员，也可为用户组关联该预置角色。访客继续使用预置角色。首期将现有账号管理、LDAP、权限维护、审计保留等管理专属边界作为额外校验。

**授权存储。** 一条授权包含 `roleId`、`permissionCode`、`scopeSelector`、`effect` 和版本。范围使用目录稳定标识，支持全局、按环境／实例／业务线／命名空间、仅本人等类型，每类能力只接受已声明的范围。研发和测试沿用现有环境级及实例级诊断范围。预发布与生产的诊断和知识目标应明确实例、业务线、命名空间。集群接入按实例校验，新增实例用服务端验证拟登记的目录归属。账号、角色、LDAP、审计策略使用平台全局范围。“仅本人”由会话决定，对凭据还要检查来源资格。

**请求校验。** 验证身份与会话 → 用已注册的方法、规范化路由和白名单动作识别权限点 → 从可信存储载入目标资源和现行范围 → 判断当前角色及范围 → 执行状态约束和业务闸门 → 记录审计。权限和范围在同一条授予上匹配，直接和组继承只对完整授予取并集。缺少匹配就拒绝，存量显式拒绝优先。列表和汇总直接按权限过滤，详情对不可见对象按模块策略返回 404；已知对象的动作无权返回 403。来源或目标范围无法解析时停止写入。内部报错返回通用信息与可关联的请求 ID。

**复用路由。** `POST /api/v1/wiki/candidates/{id}` 根据有效 `action` 确定编辑、预览、送审、驳回、放弃或发布。通过 `POST /api/v1/admin/access` 编辑组时，角色差异需要逐项校验授予与撤销。复合读取按权限投影 `roles`、`groups`、授权来源和审计保留配置。未注册路由、未知或歧义动作默认拒绝。注册表禁止重复编码或相同分流条件映射到不同权限，新增路由必须声明人类授权点、本人操作、服务身份或公开入口之一。

**跨范围与衍生数据。** 创建跨集合关联同时检查来源和目标；知识问答、推荐、引用、计数和反馈沿用对象和版本可见性。生产发布、范围扩大或首次发布到多个目标保留双人审核。关系变更、AI 结果写入、同步落库和发布提交前重查身份、权限、版本及范围。后台同步按计划所有者权限执行；上报采集走独立服务身份与实例绑定，成员无法通过角色获得上报凭据。登录、退出、本人改密和探活沿用固定规则。

**管理操作和审计。** 授权操作者只有具备该管理能力且在可委派的权限和范围内才可保存变更。预置角色、超级管理员保护和角色引用保护继续生效。批量授权保持现有逐项结果，对失败项不写关联。记录操作者、权限编码、路由模板、动作、目标范围、授权前后差异、结果与请求 ID。访问审计和到期清理沿用现有审计规则，审计记录的人工作废或删除无权限点。凭据、模型正文和原始证据不入审计。

## 4. 迁移与交付

1. **目录与对照。** 从 OpenAPI 和会话路由生成方法与路径清单，人工确认动作语义、复合响应字段和范围规则。优先交付目录浏览及接口详情。当前代码的现有能力仍按原入口工作。
2. **兼容与影子判定。** 按旧授权逐类转换并对照真实请求。访客历史诊断只映射旧有发现、列表、汇总与详情点；既有细分角色及显式拒绝按实测逐条迁移。Wiki、AI 和平台管理权限按历史身份实际允许范围对照。发现判定差异先核对，新增权限默认独立授权。
3. **分模块执行。** 先启用可验证的诊断查询，再启用知识写入与发布，最后启用平台高风险操作。保留独立恢复身份、策略版本和审计。回滚到上一份经过校验的授权快照，并保留在发布窗口内的撤权记录。
4. **验证指标。** 用代表性岗位走完“目录选点 → 范围 → 预览 → 验证”，记录管理操作数、任务完成时间和越权拒绝结果。后端维护未分类路由数、影子判定差异和跨范围拒绝审计覆盖。上线门槛为已纳入路由全部分类、已验证的旧授权无未确认扩大、关键越权回归全部通过。

### 岗位示例

| 岗位 | 授权点示例 | 范围 |
| --- | --- | --- |
| 值班 SRE | 发现实例、读 Finding、读证据、发起复核和保存结论 | 分配到的测试或生产实例，生产明确业务线与命名空间 |
| 知识运营 | 搜索文档、新建和编辑草稿、送审 | 指定业务线及目标命名空间 |
| 知识审阅 | 读待审队列、候选、指定版本，驳回及发布 | 指定生产知识目标，发布时核验第二人 |
| 审计员 | 查询操作审计列表、读取详情和批次 | 平台全局的审计读取 |

### 验收用例

| 场景 | 预期 |
| --- | --- |
| 仅授权 `knowledge.candidate.save`，调用同一个 POST 的 publish | 返回 403，候选状态及正式文档保持原状 |
| 测试有保存复核权限，生产只有读取权限 | 测试可保存，生产保存被拒绝，生产读取按范围生效 |
| 仅有角色读取，访问共用的 `GET /admin/access` | 返回有权字段，用户组数据不可见 |
| 仅有用户组编辑，直接提交带新增 RoleIDs 的表单 | 缺少组角色授予时拒绝相应变更 |
| 按空动作授权运行 AI 复核，提交未知 action | 请求被拒绝且不发起模型调用 |
| 生产知识草稿同一人送审和确认发布 | 业务闸门拒绝，第二位有权管理员可完成 |
| 组角色被撤销，但用户仍有直接角色 | 权限继续生效，来源说明只剩直接角色 |
| 问答开始后知识被撤回或权限丢失 | 响应前复查版本和授权，无泄露引用 |
| 只查询审计，调用修改保留策略 | 403，保留配置不变 |
| 新接口漏配权限分类或复用路由动作重叠 | 契约检查失败 |

### 当前实现边界

- 实现按四个一级模块配置权限。模块标识分别为 `diagnosis`、`knowledge`、`resources`、`system`。前端从后端版本化目录取模块和功能点，新增分类和操作需同时接入业务处理器与测试。
- 角色保存支持新接口授权，同时兼容原有历史诊断范围。旧范围在读取时映射原先五类诊断能力，升级无需重建角色或改写 SQLite。新版 Web 和 API 应配套发布。
- 目录中的「现有管理身份」保留既有业务门槛。涉及环境和业务线维护、集群连接与凭据变更、账号与绑定维护、LDAP 激活及审计保留、远端 Wiki 来源注册等写操作按现行管理身份处理。管理员角色保持原有能力。其他点支持按声明的范围配置。
- 角色保存记录接口授权增减，并可定位操作审计。诊断及 Wiki 工作流仍使用各自的既有审计路径。已发布知识撤回、生产双人审核、后台 Wiki 任务所有者和指定模型的限制继续由对应业务处理器校验。
- 参考站点可确认的入口为登录页。Portal 使用上传截图所示的后台结构和 kube-aiops 标识，采用深蓝顶栏、浅蓝灰导航、紧凑矩阵及右侧详情。交互原型中的账号与授权结果为示例。

## 5. 接口权限清单

以下除标注「新增路由」外，方法和路径以当前仓库路由为依据。权限编码、业务分流和授权规则属于本次评审稿。未分配业务动作的列在原型中固定显示「—」。引用如下。

- [诊断查询 OpenAPI](https://github.com/abchw517/kube-aiops/blob/8f1cdc864442d1bc8b02d0fd07d4eb5339352a1e/api/openapi.yaml)
- [Portal 会话路由](https://github.com/abchw517/kube-aiops/blob/8f1cdc864442d1bc8b02d0fd07d4eb5339352a1e/api/internal/portal/routes.go)
- [系统管理动作](https://github.com/abchw517/kube-aiops/blob/8f1cdc864442d1bc8b02d0fd07d4eb5339352a1e/api/internal/permissions/platform_http.go)
- [知识库接口与上线验收](https://github.com/abchw517/kube-aiops/blob/8f1cdc864442d1bc8b02d0fd07d4eb5339352a1e/docx/operations-knowledge/api-contract.md)

### 诊断控制台

| 工具集合／功能点 | 权限列与动作 | 接口与分流 | 权限编码 | 风险 |
| --- | --- | --- | --- | --- |
| 历史诊断／诊断结果 | 查 · 查询诊断列表 | `GET /api/v1/findings` | `diagnosis.finding.list` | 常规 |
| 历史诊断／诊断结果 | 查 · 读取诊断汇总 | `GET /api/v1/findings/summary` | `diagnosis.finding.summary` | 常规 |
| 历史诊断／诊断结果 | 查 · 读取单条诊断 | `GET /api/v1/findings/{id}` | `diagnosis.finding.read` | 常规 |
| 历史诊断／资源发现 | 查 · 查询可见实例 | `GET /api/v1/clusters` | `diagnosis.discovery.clusters` | 常规 |
| 历史诊断／资源发现 | 查 · 查询可见命名空间 | `GET /api/v1/clusters/{cluster}/namespaces` | `diagnosis.discovery.namespaces` | 常规 |
| 历史诊断／资源发现 | 查 · 读取资源详情 | `GET /api/v1/clusters/{cluster}/resources/{kind}/{namespace}/{name}` | `diagnosis.discovery.resource` | 常规 |
| 历史诊断／关联证据 | 查 · 读取关联证据 | `GET /api/v1/findings/{id}/correlation` | `diagnosis.evidence.read` | 常规 |
| AI 诊断复核／AI 复核结论 | 增 · 生成 AI 复核 | `POST /api/v1/admin/finding-analysis` · action=assess、enhance 或空值 | `diagnosis.review.run` | 敏感 |
| AI 诊断复核／AI 复核结论 | 增 · 追问与生成总结 | `POST /api/v1/admin/finding-analysis` · action=chat / finalize / summarize | `diagnosis.review.chat` | 敏感 |
| AI 诊断复核／AI 复核结论 | 改 · 保存复核结论 | `POST /api/v1/admin/finding-analysis` · action=save，验证当前复核版本 | `diagnosis.review.save` | 敏感 |
| AI 诊断复核／AI 复核结论 | 查 · 读取复核历史 | `GET /api/v1/admin/finding-analysis` | `diagnosis.review.read` | 常规 |
| AI 诊断复核／诊断关联知识 | 增 · 从复核创建案例草稿 | `POST /api/v1/wiki/finding` · action=draft，另校验知识草稿创建权限 | `diagnosis.related.draft` | 敏感 |
| AI 诊断复核／诊断关联知识 | 增 · 关联手册版本 | `POST /api/v1/wiki/finding` · action=link，另校验目标知识版本 | `diagnosis.related.link` | 敏感 |
| AI 诊断复核／诊断关联知识 | 查 · 查看相关知识 | `GET /api/v1/wiki/finding` · 按知识权限过滤候选及关联结果 | `diagnosis.related.read` | 常规 |

### 运维知识库

| 工具集合／功能点 | 权限列与动作 | 接口与分流 | 权限编码 | 风险 |
| --- | --- | --- | --- | --- |
| 知识查询／知识文档与版本 | 查 · 检索知识文档 | `GET /api/v1/wiki/knowledge` | `knowledge.document.search` | 常规 |
| 知识查询／知识文档与版本 | 查 · 查看知识文档 | `GET /api/v1/wiki/knowledge/{id}` | `knowledge.document.read` | 常规 |
| 知识查询／知识文档与版本 | 查 · 查看指定版本 | `GET /api/v1/wiki/knowledge/{id}/revisions/{revision}` | `knowledge.document.version` | 常规 |
| 知识查询／AI 知识问答 | 增 · 发起知识问答 | `POST /api/v1/wiki/ask` · 同时验证各引用的读取权限 | `knowledge.qa.ask` | 敏感 |
| 知识查询／版本反馈 | 增 · 提交版本反馈 | `POST /api/v1/wiki/knowledge/{id}/feedback` · 另校验当前版本可读 | `knowledge.feedback.create` | 常规 |
| 知识查询／版本反馈 | 查 · 读取版本反馈 | `GET /api/v1/wiki/knowledge/{id}/feedback` | `knowledge.feedback.list` | 常规 |
| 知识编审／知识草稿 | 增 · 新建或修订草稿 | `POST /api/v1/wiki/candidates` | `knowledge.candidate.create` | 常规 |
| 知识编审／知识草稿 | 增 · 解析附件预览 | `POST /api/v1/wiki/imports/files` · 附件处理，转写草稿另需新增权限 | `knowledge.candidate.import` | 敏感 |
| 知识编审／知识草稿 | 删 · 放弃草稿 | `POST /api/v1/wiki/candidates/{id}` · action=discard，保留记录 | `knowledge.candidate.discard` | 常规 |
| 知识编审／知识草稿 | 改 · 编辑草稿 | `POST /api/v1/wiki/candidates/{id}` · action=save | `knowledge.candidate.save` | 常规 |
| 知识编审／知识草稿 | 查 · 读取候选清单 | `GET /api/v1/wiki/candidates` | `knowledge.candidate.list` | 常规 |
| 知识编审／知识草稿 | 查 · 读取草稿详情 | `GET /api/v1/wiki/candidates/{id}` | `knowledge.candidate.read` | 常规 |
| 知识编审／知识草稿 | 查 · 生成脱敏预览 | `POST /api/v1/wiki/candidates/{id}` · action=preview，无持久化变更 | `knowledge.candidate.preview` | 常规 |
| 知识编审／送审与发布 | 改 · 提交审核 | `POST /api/v1/wiki/candidates/{id}` · action=submit | `knowledge.workflow.submit` | 常规 |
| 知识编审／送审与发布 | 改 · 驳回候选 | `POST /api/v1/wiki/candidates/{id}` · action=reject | `knowledge.workflow.reject` | 敏感 |
| 知识编审／送审与发布 | 改 · 确认发布 | `POST /api/v1/wiki/candidates/{id}` · action=publish，生产或扩大范围复核另一位有权管理员 | `knowledge.workflow.publish` | 高风险 |
| 知识编审／知识治理 | 删 · 撤回生效知识 | `POST /api/v1/wiki/knowledge/{id}/governance` · action=withdraw，保留历史 | `knowledge.governance.withdraw` | 高风险 |
| 知识编审／知识治理 | 改 · 登记知识复核 | `POST /api/v1/wiki/knowledge/{id}/governance` · action=review | `knowledge.governance.review` | 敏感 |
| 知识编审／知识治理 | 查 · 查看治理队列 | `GET /api/v1/wiki/governance` | `knowledge.governance.list` | 常规 |
| 知识编审／知识治理 | 查 · 查看治理详情 | `GET /api/v1/wiki/knowledge/{id}/governance` | `knowledge.governance.read` | 常规 |
| 远端 Wiki／同步来源 | 增 · 注册来源 | `PUT /api/v1/wiki/sources/{id}` · 注册全新来源，存在时禁止借此能力改写 | `knowledge.source.register` | 高风险 |
| 远端 Wiki／同步来源 | 删 · 停用来源 | `DELETE /api/v1/wiki/sources/{id}` · 保留执行记录和知识版本 | `knowledge.source.disable` | 高风险 |
| 远端 Wiki／同步来源 | 查 · 测试来源连接 | `POST /api/v1/wiki/sources/{id}/check` · 只测试连接 | `knowledge.source.check` | 敏感 |
| 远端 Wiki／同步来源 | 查 · 查看来源配置 | `GET /api/v1/wiki/sources/{id}` · 返回脱敏配置 · 新增路由 | `knowledge.source.read` | 常规 |
| 远端 Wiki／本人 Wiki 凭据 | 增 · 新建本人绑定 | `PUT /api/v1/wiki/sources/{id}/credential` · 服务端确认此前未绑定 | `knowledge.credential.bind` | 敏感 |
| 远端 Wiki／本人 Wiki 凭据 | 删 · 解除本人绑定 | `DELETE /api/v1/wiki/sources/{id}/credential` · 仅本人 | `knowledge.credential.unbind` | 敏感 |
| 远端 Wiki／本人 Wiki 凭据 | 改 · 更换本人绑定 | `PUT /api/v1/wiki/sources/{id}/credential` · 服务端确认已有绑定 | `knowledge.credential.rotate` | 敏感 |
| 远端 Wiki／本人 Wiki 凭据 | 查 · 查看本人绑定状态 | `GET /api/v1/wiki/sources/{id}/credential` · 返回账号和配置状态 | `knowledge.credential.status` | 常规 |
| 远端 Wiki／同步任务 | 增 · 启动同步 | `POST /api/v1/wiki/sources/{id}/sync` · 使用执行人的来源绑定 | `knowledge.sync.run` | 敏感 |
| 远端 Wiki／同步任务 | 增 · 重试任务或失败页 | `POST /api/v1/wiki/sources/{id}/runs/{run}/retry` · 验证来源范围、运行归属及失败状态 | `knowledge.sync.retry` | 敏感 |
| 远端 Wiki／同步任务 | 查 · 查询执行列表 | `GET /api/v1/wiki/sources/{id}/runs` | `knowledge.sync.runs` | 常规 |
| 远端 Wiki／同步任务 | 查 · 查询执行详情 | `GET /api/v1/wiki/sources/{id}/runs/{run}` | `knowledge.sync.run-read` | 常规 |
| 远端 Wiki／同步计划 | 改 · 保存同步计划 | `PUT /api/v1/wiki/sources/{id}/schedule` · 开关及周期，执行时复验配置人权限 | `knowledge.schedule.save` | 敏感 |
| 远端 Wiki／同步计划 | 查 · 查看同步计划 | `GET /api/v1/wiki/sources/{id}/schedule` | `knowledge.schedule.read` | 常规 |

### 资源管理

| 工具集合／功能点 | 权限列与动作 | 接口与分流 | 权限编码 | 风险 |
| --- | --- | --- | --- | --- |
| 资源目录／环境目录 | 增 · 添加环境 | `POST /api/v1/admin/environments` | `resources.environment.create` | 常规 |
| 资源目录／环境目录 | 删 · 删除环境 | `DELETE /api/v1/admin/environments` · 删除前检查引用 | `resources.environment.delete` | 高风险 |
| 资源目录／环境目录 | 改 · 修改环境 | `PUT /api/v1/admin/environments` | `resources.environment.update` | 敏感 |
| 资源目录／环境目录 | 查 · 查询环境 | `GET /api/v1/admin/environments` | `resources.environment.list` | 常规 |
| 资源目录／业务线目录 | 增 · 添加业务线 | `POST /api/v1/admin/business-lines` | `resources.business.create` | 常规 |
| 资源目录／业务线目录 | 删 · 删除业务线 | `DELETE /api/v1/admin/business-lines` · 删除前检查引用 | `resources.business.delete` | 高风险 |
| 资源目录／业务线目录 | 改 · 修改业务线 | `PUT /api/v1/admin/business-lines` | `resources.business.update` | 敏感 |
| 资源目录／业务线目录 | 查 · 查询业务线 | `GET /api/v1/admin/business-lines` | `resources.business.list` | 常规 |
| 集群管理／集群接入 | 增 · 登记新实例 | `POST /api/v1/admin/cluster-onboarding` · 无 action 且 test 不为 true，校验拟登记归属 | `resources.cluster.create` | 高风险 |
| 集群管理／集群接入 | 删 · 删除连接 | `DELETE /api/v1/admin/cluster-onboarding` · 按 id 解析现有实例 | `resources.cluster.disconnect` | 高风险 |
| 集群管理／集群接入 | 改 · 修改接入配置 | `PUT /api/v1/admin/cluster-onboarding` · test 不为 true，提交新凭据追加轮换权限 | `resources.cluster.update` | 高风险 |
| 集群管理／集群接入 | 改 · 重新采集校验 | `POST /api/v1/admin/cluster-onboarding` · query action=recheck | `resources.cluster.recheck` | 敏感 |
| 集群管理／集群接入 | 改 · 恢复本地连接 | `POST /api/v1/admin/cluster-onboarding` · query action=reconnect | `resources.cluster.reconnect` | 高风险 |
| 集群管理／集群接入 | 改 · 轮换接入凭据 | `POST /api/v1/admin/cluster-onboarding` · query action=rotate-token。PUT 提交新直连凭据也校验此点 | `resources.cluster.rotate` | 高风险 |
| 集群管理／集群接入 | 查 · 查看集群接入状态 | `GET /api/v1/admin/cluster-onboarding` | `resources.cluster.list` | 常规 |
| 集群管理／集群接入 | 查 · 测试直连配置 | `POST /api/v1/admin/cluster-onboarding` · query test=true，POST 和 PUT 均适用 | `resources.cluster.test` | 敏感 |
| 集群管理／命名空间归属 | 改 · 维护归属关系 | `PUT /api/v1/admin/namespace-business` · 逐项校验真实实例、命名空间及变更前后业务线 | `resources.ownership.replace` | 高风险 |
| 集群管理／命名空间归属 | 查 · 查看归属关系 | `GET /api/v1/admin/namespace-business` | `resources.ownership.list` | 常规 |

### 系统管理

| 工具集合／功能点 | 权限列与动作 | 接口与分流 | 权限编码 | 风险 |
| --- | --- | --- | --- | --- |
| 身份与角色／用户账号 | 增 · 创建账号 | `POST /api/v1/admin/accounts` · action=create，创建管理员受身份约束 | `system.account.create` | 高风险 |
| 身份与角色／用户账号 | 删 · 删除受管账号 | `POST /api/v1/admin/accounts` · action=delete | `system.account.delete` | 高风险 |
| 身份与角色／用户账号 | 改 · 停用或启用账号 | `POST /api/v1/admin/accounts` · action=disable / enable | `system.account.state` | 高风险 |
| 身份与角色／用户账号 | 改 · 重置账号密码 | `POST /api/v1/admin/accounts` · action=reset | `system.account.reset` | 高风险 |
| 身份与角色／用户账号 | 查 · 查询用户和状态 | `GET /api/v1/admin/accounts` | `system.account.list` | 常规 |
| 身份与角色／角色配置 | 增 · 创建角色 | `POST /api/v1/admin/access` · action=role-save，且未提交角色 ID | `system.role.create` | 高风险 |
| 身份与角色／角色配置 | 删 · 删除自定义角色 | `POST /api/v1/admin/access` · action=role-delete，保留引用保护 | `system.role.delete` | 高风险 |
| 身份与角色／角色配置 | 改 · 修改角色授权 | `POST /api/v1/admin/access` · action=role-save，加载已有角色及版本 | `system.role.update` | 高风险 |
| 身份与角色／角色配置 | 查 · 查询角色定义 | `GET /api/v1/admin/access` · 独立控制 roles 投影 | `system.role.list` | 常规 |
| 身份与角色／用户组 | 增 · 创建用户组 | `POST /api/v1/admin/access` · action=group-save，且未提交组 ID | `system.group.create` | 高风险 |
| 身份与角色／用户组 | 删 · 删除用户组 | `POST /api/v1/admin/access` · action=group-delete，保留引用保护 | `system.group.delete` | 高风险 |
| 身份与角色／用户组 | 改 · 编辑用户组信息 | `POST /api/v1/admin/access` · action=group-save，加载已有组 | `system.group.update` | 敏感 |
| 身份与角色／用户组 | 查 · 查询用户组定义 | `GET /api/v1/admin/access` · 独立控制 groups 投影 | `system.group.list` | 常规 |
| 身份与角色／授权关联 | 增 · 授予用户直接角色 | `POST /api/v1/admin/accounts` · action=grant | `system.binding.user-role-grant` | 高风险 |
| 身份与角色／授权关联 | 增 · 将用户加入用户组 | `POST /api/v1/admin/accounts` · action=join | `system.binding.user-join` | 高风险 |
| 身份与角色／授权关联 | 增 · 为用户组新增角色 | `POST /api/v1/admin/access` · action=group-roles-add，或按 group-save 差异校验 | `system.binding.group-role-grant` | 高风险 |
| 身份与角色／授权关联 | 删 · 撤销用户直接角色 | `POST /api/v1/admin/accounts` · action=revoke | `system.binding.user-role-revoke` | 高风险 |
| 身份与角色／授权关联 | 删 · 将用户移出用户组 | `POST /api/v1/admin/accounts` · action=leave | `system.binding.user-leave` | 高风险 |
| 身份与角色／授权关联 | 删 · 撤销用户组角色 | `POST /api/v1/admin/access` · action=group-roles-remove，或按 group-save 差异校验 | `system.binding.group-role-revoke` | 高风险 |
| 身份与角色／授权关联 | 查 · 查询绑定与生效来源 | `GET /api/v1/admin/accounts` · 独立控制授权关系和来源字段的投影 | `system.binding.read` | 常规 |
| 身份与角色／LDAP 认证配置 | 改 · 保存 LDAP 配置 | `PUT /api/v1/admin/ldap` · 绑定当前验证结果 | `system.ldap.save` | 高风险 |
| 身份与角色／LDAP 认证配置 | 查 · 查看 LDAP 配置 | `GET /api/v1/admin/ldap` · 只返回配置及密码状态 | `system.ldap.read` | 敏感 |
| 身份与角色／LDAP 认证配置 | 查 · 测试 LDAP 配置 | `POST /api/v1/admin/ldap-test` · 独立检查测试权限 | `system.ldap.test` | 敏感 |
| 身份与角色／权限目录 | 查 · 查看权限目录 | `GET /api/v1/admin/permission-catalog` · 返回版本化目录 · 新增路由 | `system.catalog.read` | 常规 |
| 操作审计／审计记录 | 查 · 查询审计列表 | `GET /api/v1/admin/operations` · 列表和分页 | `system.audit.list` | 敏感 |
| 操作审计／审计记录 | 查 · 读取审计明细 | `GET /api/v1/admin/operations` · query record 或 batch | `system.audit.record` | 敏感 |
| 操作审计／审计保留策略 | 改 · 调整审计保留 | `PUT /api/v1/admin/operations` · 保留超级管理员限制 | `system.retention.update` | 高风险 |
| 操作审计／审计保留策略 | 查 · 读取保留设置 | `GET /api/v1/admin/operations` · 独立控制 retention 字段投影 | `system.retention.read` | 敏感 |

### 兼容路由处理

历史角色和策略入口 `/api/v1/admin/users`、`/api/v1/admin/roles`、`/api/v1/admin/policy` 维持原有超级管理员门槛，迁移期不纳入可委派目录。旧知识直发入口，包括复核的旧 `confirm/publish` 分支、`POST /api/v1/wiki/knowledge/{id}/revisions` 和带发布标记的导入请求，沿用草稿必经门槛。原有文本预览 `POST /api/v1/wiki/imports` 迁移到附件／草稿预览流程后按现有兼容期处理。这些入口纳入契约分类检查。

## 5. 用户组关联「系统管理员」

### 管理人、成员与角色

- 超级管理员可在新建及编辑用户组时关联或取消「系统管理员」。新建组默认未勾选此角色，现有组的角色需要由超级管理员主动调整。
- 普通系统管理员可以查看管理员组的角色和成员。管理员组的角色调整、成员加减、删除及其他能改变有效管理员身份的入口由超级管理员操作。现有直接授予和预置角色的维护规则继续沿用。
- 保留现有 LDAP 员工参与组授权的规则。本地账号继续使用现有固定角色。停用、目录停用或待改密账号按其当前状态处理。
- 「系统管理员」是全局管理角色。继承后的能力与同一个角色直接授予时一致，超级管理员身份和专属权限继续由既有机制决定。

### 页面交互与生效

1. 在「关联角色」列表单独展示「系统管理员」并标注「全局管理」。已有组跟随原来的编辑表单，新建组直接显示该选项。
2. 选择或取消该角色时，表单下方显示影响摘要。已有组显示成员人数。保存前的确认步骤展示会获得或失去管理员权限的人数和仍有其他管理员来源的人数。空组显示当前 0 人，并说明后续加入的员工会继承管理员权限。
3. 点「保存用户组」后提交差异。管理员角色有变化时，进入同一对话框的确认步骤并展示核对摘要。无变化时沿用一次保存。
4. 生效权限按「直接授予 + 用户组继承」取并集。取消其中一个来源时，来自直授或其他组的同一角色仍有效。真正发生管理员身份变化的账号结束旧会话，重新登录后读取有效角色。普通权限的增减按下一次受保护请求生效。
5. 用户组保存成功可查看本次审计。在用户详情中显示「用户组名称 → 系统管理员」及其他仍有效的来源。

### 后端要求

- 在用户组保存、批量加减角色、用户加入或退出组、批量成员调整和删除组时，统一计算变更前后管理员能力。管理人校验以服务端当前会话为准，不能靠 UI 禁用按钮替代。
- 在事务中校验管理员角色保存预览的组与成员版本。组成员或角色在确认前变化时返回冲突，刷新影响范围后重新确认。角色变更、会话版本变更和审计同事务提交。
- 审计记录操作者、组、角色增减、影响账号、保留来源及可定位的审计记录标识。批量路径沿用既有逐项结果；越权项不改变任何关系。

## 6. 权限点单选、批量选择与保存

管理员从「系统管理 → 权限管理」直接进入可勾选的角色草稿，从「角色列表」编辑已有自定义角色，也可从用户组的角色名称进入同一编辑器。旧策略页提供前往角色配置的入口。表单按「选择接口权限 → 选择授权范围 → 授权清单 → 核对角色变更」操作。预置角色与独立权限目录只读。接口目录请求失败时，保留角色表单和已保存清单，提供原地重试。

| 控件 | 行为 |
| --- | --- |
| 权限点右侧复选框 | 勾选或取消该动作。权限名称独立打开接口详情。保留现行管理身份约束的动作展示禁用状态与原因。 |
| 工具集合复选框 | 批量选择或取消该集合内**当前可见且可配置**的权限点。部分选中显示半选。 |
| 批量勾选当前结果 | 仅操作当前模块、工具集合及搜索／筛选结果中的可配置权限点。隐藏项和其他模块的选择保持原状态。界面分别显示当前结果已选数与总待添加数。 |
| 添加到授权清单 | 同时选中多种适用范围时，明确选择本次添加的范围类型，再填写所需数据范围。只处理该类型的勾选，筛选隐藏的同类已选项也计入本次。其他类型保留待添加。按「接口动作 + 范围」去重。 |
| 保存角色 | 仍有待添加选择时，提示先添加或清空。清单支持移除单条授权。点击「保存角色」预览增减和风险，点击「确认保存」提交，成功后对应角色授权生效。 |
| 已配置权限与详情 | 通过筛选查看草稿清单中的动作。详情列出各个范围，并用清单状态区分新增、已保存与待移除。 |

批量勾选覆盖当前结果中可配置的增、删、改、查。高风险动作参与显式批量选择，先在清单标注风险，保存时列出权限和范围增减供确认。生产与预发布继续要求实际实例、业务线及命名空间。平台全局和本人能力继续使用其类型约束。
