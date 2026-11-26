# 前端 UI · 开发任务

[设计方案](design.md) · [PRD](00-prd.md) · [页面拆解](01-pages.md) · [功能拆解](02-features.md) · [用户故事](03-user-stories.md) · [测试用例](05-unit-test-cases.md) · [交互原型](prototype.html) · [文档维护规则](../development-standard.md)

全站外框、导航、视觉和登录是本模块的维护范围。业务页面流程分别维护在对应模块中。当前以全站白色布局与最新登录背景为准。

## 登录页

| 任务 | 改动 | 验收 |
| --- | --- | --- |
| DWL-DEV-06 | 接入登录背景和响应式布局，清理画布观察器 | DWL-TC-09、DWL-TC-10 |

## 全站布局与共享工作区

| 任务 | 实施内容 | 代码入口 | 验收用例 |
| --- | --- | --- | --- |
| DEV-01 | 收敛全局色板、字号、圆角和阴影；建立白色导航与当前选中态 | styles.css、components/portal-shell.css | TC-01、TC-02 |
| DEV-02 | 登录卡片、LDAP 选择与状态提示按操作顺序重排 | components/states.ts、components/portal-shell.css | TC-03 |
| DEV-03 | 总览指标卡、独立趋势与严重度环图；用 CSSOM 应用比例 | views/diagnosis/unified-overview.ts、unified-overview.css | TC-04 至 TC-06 |
| DEV-04 | 资源列表、切换标签、配置弹窗与详情侧栏对齐；集成集群复选框和 Zabbix 关闭修复 | views/resources、views/clusters、views/diagnosis/zabbix-sources.ts | TC-07 至 TC-09 |
| DEV-05 | 知识检索和系统工作区接入公共视觉变量 | views/wiki/wiki.css、views/permissions 中的对应样式 | TC-10、TC-11 |
| DEV-06 | 执行静态检查、单元测试和浏览器回归，覆盖窄屏、CSP 与真实路由 | web/e2e/browser-test.mjs、Makefile | TC-01 至 TC-12 |

集成顺序为公共外框、工作区样式与总览、端到端回归。已有集群复选框和 Zabbix 关闭修复进入同一开发分支，用于完整验证交互。

## 共享外框实现边界

导航模型位于 `web/src/domain/portal-navigation.ts`，壳组件位于 `web/src/components/portal-shell.ts` 与 `portal-shell.css`。`web/src/app.ts` 管理路由、授权入口、移动侧栏焦点及页面切换时的渲染代次。各业务模块维护自己的请求、表单及派生状态。会话、LDAP、权限及审计的实现边界见[系统管理](../system-management/04-development-tasks.md#认证用户管理与审计)。

## AI 服务导航与诊断助手外框

**PC-T90**

`web/src/domain/page-registry.ts` 与 `portal-navigation.ts` 定义菜单、栏目和路由投影。`web/src/app.ts` 挂载助手、处理加载与鉴权。弹层样式在 `web/src/views/ai/ai.css`，问答交互复用 `wiki-tools.ts`。保持键盘、焦点和窄屏回归。

[测试用例](05-unit-test-cases.md#ai-服务导航与诊断助手外框)
