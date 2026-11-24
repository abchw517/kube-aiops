# 开发任务

| 任务 | 实施位置 | 依赖 |
| --- | --- | --- |
| D1 数据源路由迁移 | resource-routes、portal-navigation、app、diagnostic-settings | 复用管理组件与能力信号 |
| D2 统一记录集和有界分页 | domain/unified-diagnostics、views/diagnosis/unified-overview | 现有三类获授权来源 |
| D3 渲染与导航恢复 | unified-overview、app、诊断样式 | D2 |
| D4 回归 | web/tests、web/e2e 与可点击原型 | D1–D3 |
| D5 页面重构 | 总览模板与独立样式 | 分析卡前置，校对桌面、窄屏、空态和失败态 |
| D6 视图参数与回链 | unified-diagnostics、详情回链 | 校验趋势日数，集群、级别及趋势窗均可恢复 |
| D7 布局和交互回归 | 单元测试、Portal E2E、HTML 原型 | 验证历史记录可见、图表时间窗、日期读数与窄屏记录卡 |
| D8 UTC+8 聚合 | `domain/unified-diagnostics.ts` | 调整日界线，验证首日、下一日、无时间戳和未来时间 |
| D9 趋势局部更新 | `unified-trend.ts`、`unified-overview.ts` | 共享轴、比例堆叠、日期键盘、范围切片和回链同步 |
| D10 分布与响应式 | `unified-overview.css`、Portal E2E | 以真实像素校验柱体基线、级别条宽度和移动端图内滚动 |
