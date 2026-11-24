# 开发任务

| 任务 | 实施位置 | 依赖 |
| --- | --- | --- |
| D1 数据源路由迁移 | resource-routes、portal-navigation、app、diagnostic-settings | 复用管理组件与能力信号 |
| D2 统一记录集和有界分页 | domain/unified-diagnostics、views/diagnosis/unified-overview | 两个已授权列表 |
| D3 渲染与导航恢复 | unified-overview、app、诊断样式 | D2 |
| D4 回归 | web/tests、web/e2e 与可点击原型 | D1–D3 |
