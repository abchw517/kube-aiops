# 全站 UI · 开发文档

## 实施顺序

1. 将认证配置已采用的外框推广为共享外框。使用当前路由和现有能力派生模块、栏目与面包屑，保留登录和首次改密门禁。
2. 补齐诊断总览，复用事件查询、筛选序列化和详情组件。按真实响应范围统计并说明口径。
3. 在 Kubernetes 集群管理工作区实现范围与视图切换。复用授权及集群、命名空间数据，保留现有管理操作。
4. 将知识库及系统管理迁移到共享工作区。把模块导航交给外框，保留角色组等业务子视图切换。
5. 验证路由、权限、交互和桌面及窄屏效果。

| 现有入口 | 本期职责 |
| --- | --- |
| `web/src/components/portal-shell.ts`、`portal-shell.css` | 顶栏、侧栏、面包屑、工作区及响应式规则。 |
| `web/src/app.ts` | 提供路由和权限能力，处理移动导航、焦点、退出及刷新。 |
| `web/src/domain/diagnostic-routes.ts`、`web/src/views/diagnosis/` | 保留 URL 与筛选，生成趋势和指标，联动事件详情。 |
| `web/src/views/resources/`、`web/src/views/clusters/` | 由 `clusterManagementTitle` 提供集群导航、标题和返回入口文案。环境标题通过 `summary` 选择环境范围，并展开该环境的集群。集群和命名空间列表共用当前范围。 |
| `web/src/views/wiki/`、`web/src/views/permissions/` | 沿用业务请求和表单，适配共享外框。 |

`platform-admin.ts` 在 `#/admin/access` 初始化可勾选的角色草稿。`permission-catalog.ts` 共用服务端权限目录数据并按当前可编辑状态渲染控件。

`identity-admin.ts` 与 `authentication.css` 负责 LDAP 表单分组、响应式布局、四步状态和保存门禁。明文连接使用同一个 `label` 包裹复选框和文本，布局采用 Flex 垂直居中。测试和启用分别使用现有 `ldap-test` 与 `ldap` 接口。

菜单按应用现有能力显示，读写由服务端校验。URL 参数沿用已有规则。业务模块维护自身表单、请求和派生状态。若为图表或视图新增接口查询，同步契约并验证其聚合与授权范围。演示和测试可使用夹具，部署构建从现有接口读取真实数据。

运行 `make web-check` 及适用的浏览器用例。涉及接口时运行 `make api-test` 和 `make api-contract-check`。对照原型检查 1440px、390px 和 320px。详见[测试用例](04-test-cases.md)。
