# 开发任务

| 任务 | 交付项 | 代码入口 |
| --- | --- | --- |
| T01 共用布局 | 复用主题、筛选、状态标记、类型分组、详情和固定底栏 | `web/src/views/resources/resource-workspace.ts`、`resources.css` |
| T02 数据源列表 | 来源并行读取、独立重试、资产游标、记录更新和权限派生 | `datasource-management.ts`、`datasource-rows.ts` |
| T03 接入适配 | 保留各连接器测试和预览，统一新增入口；资产编辑、库存 | `zabbix-sources.ts`、`observability-sources.ts`、`asset-source-editor.ts` |
| T04 集群目录 | 共享环境与分视图查询、历史节点及采集详情、状态统计 | `cluster-workspace.ts`、`cluster-records.ts` |
| T05 集群接入与维护 | 选择方式、分区配置、环境锁定、凭据与删除反馈 | `cluster-onboarding.ts`、原有 `namespace-ownership.ts` |
| T06 入口与回归 | 旧路由、权限、焦点、响应式、实际接口变更兼容 | `app.ts`、`portal-shell.ts`、`browser-test.mjs`、`web/tests` |

开发顺序为真实数据和路由、列表与详情、写入与恢复、测试构建。上线前用 [测试用例](06-test-cases.md) 逐项核对。
