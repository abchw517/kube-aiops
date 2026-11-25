# 开发任务

| 任务 | 实施内容 | 代码入口 | 验收用例 |
| --- | --- | --- | --- |
| DEV-01 | 收敛全局色板、字号、圆角和阴影；建立白色导航与当前选中态 | styles.css、components/portal-shell.css | TC-01、TC-02 |
| DEV-02 | 登录卡片、LDAP 选择与状态提示按操作顺序重排 | components/states.ts、components/portal-shell.css | TC-03 |
| DEV-03 | 总览指标卡、独立趋势与严重度环图；用 CSSOM 应用比例 | views/diagnosis/unified-overview.ts、unified-overview.css | TC-04 至 TC-06 |
| DEV-04 | 资源列表、切换标签、配置弹窗与详情侧栏对齐；集成集群复选框和 Zabbix 关闭修复 | views/resources、views/clusters、views/diagnosis/zabbix-sources.ts | TC-07 至 TC-09 |
| DEV-05 | 知识检索和系统工作区接入公共视觉变量 | views/wiki/wiki.css、views/permissions 中的对应样式 | TC-10、TC-11 |
| DEV-06 | 执行静态检查、单元测试和浏览器回归，覆盖窄屏、CSP 与真实路由 | web/e2e/browser-test.mjs、Makefile | TC-01 至 TC-12 |

集成顺序为公共外框、工作区样式与总览、端到端回归。已有集群复选框和 Zabbix 关闭修复进入同一开发分支，用于完整验证交互。
