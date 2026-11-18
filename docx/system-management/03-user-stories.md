# 系统管理 · 用户故事

- 需求基线 [首版 PRD](kube-aiops-system-management-prd.md)（2026-11-17 评审稿）
- 交互参考 [HTML 原型](kube-aiops-system-management-prototype.html)
- [模块开发规范](../development-standard.md)
- [页面拆解](01-pages.md) · [功能拆解](02-features.md) · [用户故事](03-user-stories.md) · [开发任务](04-development-tasks.md) · [单元测试用例](05-unit-test-cases.md)

## 故事与验收

### SM-US01

**系统初始化和管理员管理** · 功能 [SM-F01](02-features.md#sm-f01) · 任务 [SM-T01](04-development-tasks.md#sm-t01) · 用例 [SM-UT01](05-unit-test-cases.md#sm-ut01)

作为超级管理员，我希望初始化时获得唯一账号，并管理多个普通管理员。

**验收标准**

- 并发初始化仍只有一个超级管理员。
- 只有超级管理员可创建或删除普通管理员。
- 删除后立即拒绝旧会话且产生审计。
- 超级管理员不可删除。

### SM-US02

**访客创建、停用和恢复** · 功能 [SM-F02](02-features.md#sm-f02) · 任务 [SM-T02](04-development-tasks.md#sm-t02) · 用例 [SM-UT02](05-unit-test-cases.md#sm-ut02)

作为系统管理员，我希望发放临时密码并管理访客的访问生命周期。

**验收标准**

- 新访客关联固定只读角色。
- 停用立即阻断登录和旧会话。
- 恢复沿用原角色且不复活旧会话。
- 选中一个可删除的用户时可在列表发起删除。确认后账号移出列表、旧会话失效并写入审计。删除 LDAP 员工后，同一目录身份不能再次自动登记。

### SM-US03

**搜索用户与账号详情** · 功能 [SM-F03](02-features.md#sm-f03) · 任务 [SM-T03](04-development-tasks.md#sm-t03) · 用例 [SM-UT03](05-unit-test-cases.md#sm-ut03)

作为系统管理员，我希望定位用户和查看管理入口。

**验收标准**

- 列表可按账号及名称搜索并按来源、状态筛选。
- 详情展示关系与实际状态。
- 按钮和接口均按操作权限校验。

### SM-US04

**本地临时密码与改密门禁** · 功能 [SM-F04](02-features.md#sm-f04) · 任务 [SM-T04](04-development-tasks.md#sm-t04) · 用例 [SM-UT04](05-unit-test-cases.md#sm-ut04)

作为本地账号持有人及系统管理员，我希望使用临时密码安全完成首次登录或访客密码重置。

**验收标准**

- 待改密会话仅能执行改密相关操作。
- 重置访客密码立即撤销旧会话。
- LDAP 密码在目录侧处理。

### SM-US05

**超级管理员服务器恢复** · 功能 [SM-F05](02-features.md#sm-f05) · 任务 [SM-T05](04-development-tasks.md#sm-t05) · 用例 [SM-UT05](05-unit-test-cases.md#sm-ut05)

作为部署运维，我希望在遗忘密码时恢复唯一超级管理员。

**验收标准**

- 专用脚本只重置超级管理员并发放临时密码。
- 旧会话立即失效。
- 完成改密后才允许管理。
- 记录恢复事件且无密码明文。

### SM-US06

**LDAP 草稿四阶段测试和启用** · 功能 [SM-F06](02-features.md#sm-f06) · 任务 [SM-T06](04-development-tasks.md#sm-t06) · 用例 [SM-UT06](05-unit-test-cases.md#sm-ut06)

作为系统管理员，我希望验证新配置，同时保留原线上登录配置。

**验收标准**

- 连接、绑定、查找指定员工、本人认证依次通过才可启用。
- 测试不创建用户、会话或角色。
- 草稿改动需复测。
- 失败继续使用原配置。

### SM-US07

**LDAP 首次登记与默认角色** · 功能 [SM-F07](02-features.md#sm-f07) · 任务 [SM-T07](04-development-tasks.md#sm-t07) · 用例 [SM-UT07](05-unit-test-cases.md#sm-ut07)

作为LDAP 员工，我希望首次登录即可只读查看研发、测试诊断。

**验收标准**

- 首次正式登录建立用户和默认角色。
- 重复或并发登录不重复登记。
- 仍有其他角色时保留默认角色撤销结果。
- 失去最后一个角色时下次正式登录恢复默认角色。

### SM-US08

**平台停用与 LDAP 目录状态** · 功能 [SM-F08](02-features.md#sm-f08) · 任务 [SM-T08](04-development-tasks.md#sm-t08) · 用例 [SM-UT08](05-unit-test-cases.md#sm-ut08)

作为系统管理员或系统调度，我希望及时收回被停用员工的已有访问。

**验收标准**

- 平台停用立即失效。
- 目录可用时停用后 5 分钟内撤销全部会话。
- 目录故障时保留会话原定到期时间，恢复后补查。
- 双侧启用才允许登录并沿用原授权。

### SM-US09

**预置角色保护** · 功能 [SM-F09](02-features.md#sm-f09) · 任务 [SM-T09](04-development-tasks.md#sm-t09) · 用例 [SM-UT09](05-unit-test-cases.md#sm-ut09)

作为系统管理员，我希望查看预置角色且避免误改或误删。

**验收标准**

- 预置权限固定，不为 LDAP 员工授予系统管理员角色。
- 即使没有任何账号关联也禁止删除两个预置角色。

### SM-US10

**自定义角色与历史诊断范围** · 功能 [SM-F10](02-features.md#sm-f10) · 任务 [SM-T10](04-development-tasks.md#sm-t10) · 用例 [SM-UT10](05-unit-test-cases.md#sm-ut10)

作为系统管理员，我希望按环境和资源归属授予诊断查看。

**验收标准**

- 只支持历史诊断查看。
- 研测可选整个环境、集群或指定命名空间。
- 预发生产要求明确业务命名空间。
- 同角色可组合多个环境权限项。

### SM-US11

**平台用户组和批量角色绑定** · 功能 [SM-F11](02-features.md#sm-f11) · 任务 [SM-T11](04-development-tasks.md#sm-t11) · 用例 [SM-UT11](05-unit-test-cases.md#sm-ut11)

作为系统管理员，我希望为 LDAP 员工建立平台分组并维护关联角色。

**验收标准**

- 平台组由管理员维护，LDAP 目录组变更不自动加组。
- 可对一个组批量添加或移除角色。
- 组员仅 LDAP 员工。

### SM-US12

**自定义角色和用户组删除保护** · 功能 [SM-F12](02-features.md#sm-f12) · 任务 [SM-T12](04-development-tasks.md#sm-t12) · 用例 [SM-UT12](05-unit-test-cases.md#sm-ut12)

作为系统管理员，我希望知道需要解除哪些关联才能删除。

**验收标准**

- 角色有直接用户或用户组引用则拒绝，用户组有成员或角色则拒绝。
- 解除全部引用才可删除。
- 列出阻塞对象。

### SM-US13

**单人和批量授权或撤销** · 功能 [SM-F13](02-features.md#sm-f13) · 任务 [SM-T13](04-development-tasks.md#sm-t13) · 用例 [SM-UT13](05-unit-test-cases.md#sm-ut13)

作为系统管理员，我希望从账号列表一次维护多人的直接角色与组关系。

**验收标准**

- 支持加入、移出、授予和撤销，符合条件的项目独立成功。
- 每项可追踪。
- 不可撤销唯一默认角色，也不能给本地用户分配平台组。

### SM-US14

**权限并集与访问来源** · 功能 [SM-F14](02-features.md#sm-f14) · 任务 [SM-T14](04-development-tasks.md#sm-t14) · 用例 [SM-UT14](05-unit-test-cases.md#sm-ut14)

作为LDAP 员工和系统管理员，我希望查看授权范围并解释为何当前可以访问。

**验收标准**

- 对有效权限求并集，每项列出直接角色和各组继承来源。
- 撤权即时生效，仍有一条独立有效路径时继续可访问。

### SM-US15

**诊断隔离与业务线改派** · 功能 [SM-F15](02-features.md#sm-f15) · 任务 [SM-T15](04-development-tasks.md#sm-t15) · 用例 [SM-UT15](05-unit-test-cases.md#sm-ut15)

作为历史诊断查看者，我希望查询权限范围内的诊断。

**验收标准**

- 列表和单条读取均验证范围。
- 生产或预发集群级诊断仅管理员可见。
- 共享空间允许经任一授权路径查看完整历史。
- 归属变化即时重判。

### SM-US16

**操作审计写入与检索** · 功能 [SM-F16](02-features.md#sm-f16) · 任务 [SM-T16](04-development-tasks.md#sm-t16) · 用例 [SM-UT16](05-unit-test-cases.md#sm-ut16)

作为系统管理员，我希望查找谁、何时、对哪个对象完成何种操作。

**验收标准**

- 记录状态或配置变化、登录成功失败、系统撤权、脚本恢复和批量结果。
- 普通管理员可查超管管理员生命周期事件。
- 风险规则内置、输出脱敏。

### SM-US17

**资源管理权限与首采环境锁定** · 功能 [SM-F17](02-features.md#sm-f17) · 任务 [SM-T17](04-development-tasks.md#sm-t17) · 用例 [SM-UT17](05-unit-test-cases.md#sm-ut17)

作为系统管理员，我希望在首采前修正集群环境，防止接入后迁移。

**验收标准**

- 仅管理员可操作资源管理。
- 真实节点状态或首条真实 K8sGPT 诊断任一成功即锁定。
- 首采后环境不可变更。
