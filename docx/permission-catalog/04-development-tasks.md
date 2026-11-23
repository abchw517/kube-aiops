# 权限目录 · 开发任务

[故事](03-user-stories.md) · [测试](05-unit-test-cases.md)

| 任务 ID | 故事 | 实现位置 | 完成条件 |
| --- | --- | --- | --- |
| PC-T01 归档设计和接口目录 | PC-US01 | `docx/permission-catalog/`、`api/internal/permissions/catalog/` | 七项评审交付可读，目录编码唯一并与原型矩阵对照。 |
| PC-T02 角色授权与兼容 | PC-US02、PC-US04、PC-US06 | `api/internal/permissions/` | SQLite 旧角色可开库，新授权支持范围与动作，角色版本及来源正确。 |
| PC-T03 诊断与证据裁决 | PC-US02 | `api/internal/httpapi/`、`api/internal/diagnosis/` | 列表、汇总、详情和证据复用授权，AI 用真实 Finding 范围。 |
| PC-T04 Wiki 业务动作裁决 | PC-US03 | `api/internal/wiki/` | 草稿、发布、反馈、问答、绑定、同步动作按业务语义和资源范围校验。 |
| PC-T05 管理能力与字段投影 | PC-US05、PC-US06 | `api/internal/portal/`、`api/internal/permissions/` | 独立读权限按字段或对象投影，管理专属写保留既有门槛，语义操作记录至审计。 |
| PC-T06 权限目录与角色配置 | PC-US01～05 | `web/src/views/permissions/` | 真实服务数据支撑矩阵、筛选、角色选点、预览和生效权限来源。 |
| PC-T07 回归与交付 | 全部 | Go 和 Web 单元测试、浏览器回归 | 验证拒绝请求、老角色回归、构建与桌面交互。 |

## 管理员组与权限点勾选开发任务

| 编号 | 交付范围 | 验收 |
| --- | --- | --- |
| PC-T11 | SQLite 管理员角色解析、LDAP 登录及会话复查 | 统一解析直属和组来源，按实际身份变化更新会话版本 |
| PC-T12 | 管理员组影响预览、关联、批量成员保护及审计 | 预览与提交检查角色和成员快照，普通管理员的所有写入口拒绝，审计可定位 |
| PC-T13 | 用户组表单、影响确认、账号有效权限来源 | 主流程可从现有入口进入，支持新建、已有关联和撤销 |
| PC-T14 | `permission-catalog.ts`、`platform-admin.ts`、`styles.css`。独立勾选、集合与筛选批量控件、范围分批入清单 | 从侧栏进入角色草稿，分别验证只读目录返回链路、可见结果半选、隐藏选择保留、不同范围类型分批添加和失败重试。沿用现有角色 API、权限编码和服务端范围校验 |
| PC-T15 | 更新 PRD、页面与功能拆解、HTML 原型和测试用例。扩展 `web/e2e/` 的真实页面操作和请求断言 | 验证浏览器点击位置、勾选状态、重试、分批入清单、预览、确认提交与打开角色复查。运行 Go 授权测试和前端检查 |
