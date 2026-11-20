# 平台访问控制 · 开发任务

[PRD](00-prd.md) · [用户故事](03-user-stories.md) · [测试](05-unit-test-cases.md)

P0 先冻结 T04 的裁决契约及 T07 的数据格式，可并行开发前端原型接口。优先完成统一裁决、兼容存储与迁移，再接入 T01/T03 和 T02/T05/T06。P0 回归通过后单独部署 P1 的 T08。每个开发 PR 列出对应故事和测试编号。

## AC-T01

**账号与组角色关系** · [AC-US01](03-user-stories.md#ac-us01) · [AC-UT01](05-unit-test-cases.md#ac-ut01)

依赖 T04 的主体校验。复用 `platform_http.go` 的批量处理、`platform_state.go` 的组与直接角色关系；更新 `platform-admin.ts` 的逐项结果及禁止状态。验证新旧负载均拒绝把管理员角色绑定到组。

## AC-T02

**生效权限与模拟检查** · [AC-US02](03-user-stories.md#ac-us02) · [AC-UT02](05-unit-test-cases.md#ac-ut02)

依赖 T04 的统一判权、T03 的权限条目。扩展账号详情 DTO，并新增管理员只读模拟接口。复用 `authorization` 裁决，前端改进生效权限视图及模拟面板。

## AC-T03

**动作范围与角色编辑** · [AC-US03](03-user-stories.md#ac-us03) · [AC-UT03](05-unit-test-cases.md#ac-ut03)

依赖 T04 的范围规范和 T07 的存储契约。扩展角色 DTO、范围校验及前端角色编辑。保存请求携带期望版本，提交时重新校验目标归属。接口变更同步 OpenAPI 与前端类型。

## AC-T04

**统一裁决与资源范围** · [AC-US04](03-user-stories.md#ac-us04) · [AC-UT04](05-unit-test-cases.md#ac-ut04)

先冻结动作目录、范围结构和错误返回。统一 `authorization/model.go`、`platform_authorization.go` 及 Portal / Wiki 边界解析。依据资源目录识别实例与命名空间；会话、归属与角色变更在下一次请求重新校验。

## AC-T05

**模块入口与数据输出一致性** · [AC-US05](03-user-stories.md#ac-us05) · [AC-UT05](05-unit-test-cases.md#ac-ut05)

依赖 T04。逐一登记 Portal 路由、诊断、知识检索和同步的动作映射。以同一个裁决结果驱动菜单、API 和任务。连接事件、缓存和模型回答使用当时有效授权。

## AC-T06

**操作审计与身份保护** · [AC-US06](03-user-stories.md#ac-us06) · [AC-UT06](05-unit-test-cases.md#ac-ut06)

依赖 T01、T03、T04。复用现有操作审计持久化及列表。为角色和组变更设计可追溯差异及事务边界。审计记录遵守既定保留策略；后台失败原因只输出脱敏摘要。

## AC-T07

**等价迁移与上线回退** · [AC-US07](03-user-stories.md#ac-us07) · [AC-UT07](05-unit-test-cases.md#ac-ut07)

依赖 T04 的契约，实现在 `api/internal/identitystore` 和已有权限存储。准备脱敏样本、历史决策回放、备份核验、部署顺序及回退演练。发现无法等价者记录差异并由指定人员处置。

## AC-T08

**Wiki 只读试点** · [AC-US08](03-user-stories.md#ac-us08) · [AC-UT08](05-unit-test-cases.md#ac-ut08)

P1，依赖 T04、T05、T07。更新平台动作目录和 Wiki 读入口，分别启用组与资源目标。与知识库 PRD 的可见版本规则联调，记录试点范围、回退操作及验证结果。
