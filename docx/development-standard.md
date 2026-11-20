# kube-aiops 模块开发规范

## 本地开发与验证

从仓库根目录运行。Go 工具链基线为 1.26.5，Web 需要 Node.js 22+（含 npm、npx），契约检查需要 Python 3 和 [OpenAPI 依赖](../tools/openapi/requirements.txt)，浏览器回归需要 Chromium 或 Chrome。

```bash
make portal-deps
export PATH="${KUBE_AIOPS_TOOLS_DIR:-$HOME/.local/share/kube-aiops/tools}/bin:$PATH"
python3 -m venv .build/doc-venv
source .build/doc-venv/bin/activate
python -m pip install -r tools/openapi/requirements.txt

make platform-check
make api-test
make api-contract-check
make web-check
make web-e2e
make portal-demo-check
```

`make portal-deps` 优先使用现有工具链，可在受支持的 Linux 主机下载缺失版本。首次构建需获取 Go 模块和 npm 工具依赖。可用 `CHROME_BIN=/path/to/chromium make web-e2e` 指定浏览器。E2E 使用本地 mock API 和固定的 4173 端口，运行前先停止占用该端口的 Demo。

| 开发场景 | 命令与核对项 |
| --- | --- |
| 诊断查询与证据 | `make portal-demo` 运行真实 Go API 和样例数据。在浏览器验证列表、详情和四类证据。 |
| 系统管理与知识库 | `make web-e2e` 覆盖浏览器流程。真实登录、权限收敛、生产双人审核、模型和远端 Wiki 在预发验证。 |
| API 与 SQLite | `go test ./api/internal/identitystore ./api/internal/permissions ./api/internal/portal ./api/internal/diagnosis ./api/internal/wiki`。针对已有部署，以脱敏数据库副本验证迁移、权限和版本行为。 |
| 诊断查询接口变更 | 更新 `api/openapi.yaml`，运行 `make api-client-generate` 和 `make api-contract-check`。运维知识库会话接口同时更新[模块契约](operations-knowledge/api-contract.md)。 |
| 发布前 | 执行 `make preflight`，按[部署指南](install-deployment.md)从同一源码提交构建 API 与 Web 并验证目标镜像。 |

## 模块代码与验收入口

| 模块 | API / Web 代码 | 功能验收 |
| --- | --- | --- |
| 诊断控制台 | `api/internal/diagnosis/`、`web/src/views/diagnosis/` | [诊断验收用例](diagnosis-console/05-unit-test-cases.md) |
| 运维知识库 | `api/internal/wiki/`、`web/src/views/wiki/` | [知识库验收用例](operations-knowledge/05-unit-test-cases.md)、[生产试点](install-deployment.md#运维知识库生产试点验收) |
| 资源管理 | `api/internal/portal/`、`web/src/views/resources/` 与 `web/src/views/clusters/` | [资源管理验收用例](resource-management/05-unit-test-cases.md) |
| 系统管理 | `api/internal/permissions/`、`web/src/views/permissions/` | [系统管理验收用例](system-management/05-unit-test-cases.md) |
| SQLite 迁移 | `api/internal/identitystore/` | [存量数据库升级](install-deployment.md#存量部署升级) |

## 目录与文档

每个功能模块在仓库 `docx/<module>/` 下建立独立目录。目录使用小写英文及连字符。沿用项目 Markdown 格式，模块内文档按顺序编号。

| 文件 | 输入 | 交付要求 |
| --- | --- | --- |
| `01-pages.md` | 评审过的 PRD | 逐页记录入口、用户、页面内容、权限、加载/空/错误状态及关联功能。 |
| `02-features.md` | 页面与 PRD | 描述触发、输入、输出、核心规则、权限及对应故事。跨页功能复用同一个功能 ID。 |
| `03-user-stories.md` | 功能 | 写明角色、目标与可观察的验收标准，覆盖成功、失败和必要边界。 |
| `04-development-tasks.md` | 用户故事 | 拆分可交付任务，标注依赖、代码落点、输入输出、改动层和完成条件。 |
| `05-unit-test-cases.md` | 任务及验收标准 | 记录被测对象、夹具、触发和精确断言，列出时序、权限、错误与回归用例。 |

模块文档链接到对应 PRD 或记录所用版本。跨模块共用规则写在本规范，具体功能规则由模块文档维护。

## 编号和追踪

使用 `<模块前缀>-Pxx → Fxx → USxx → Txx → UTxx`。允许一对多及多对多关联。编号在模块内保持稳定。工程子任务可加 `-A`、`-B`，细化测试可加 `-01` 等序号。跨文档使用相对链接定位条目。

## Coding 执行顺序

1. 页面拆解以当前 PRD 为输入，梳理导航、数据范围和状态。功能拆解补齐各页动作及跨页业务规则。
2. 每个功能写出用户故事和可观察的验收标准。每个故事对应开发任务，列清依赖和编码前需要确认的边界。
3. 开发前识别要覆盖的单测并记录预期。新增或变更 API 时同步契约及使用方。开发时优先沿用目标分支的服务、授权、持久化及前端入口。
4. 完成任务后运行其单测和受影响回归。外部系统的真实调用、存储事务和浏览器链路另做对应的集成或端到端验证。记录执行结果，并在 PR 描述中列出覆盖的故事和任务编号。
5. 需求变更时追踪下游受影响页面、功能、故事、任务及用例。评审项写明具体影响和当前约定，明确未决分支后推进可独立交付的任务。

## 提交前检查

- 需求有对应页面或后台能力，页面有对应功能，功能的故事有任务和测试。
- 每个测试能追到可观察的验收标准。成功、无权限、无数据、失败恢复和已知边界按实际功能覆盖。
- 测试结果与试点验收状态如实记录。文档和实现使用相同术语及状态含义。

## 资源管理示例

- [页面拆解](resource-management/01-pages.md)
- [功能拆解](resource-management/02-features.md)
- [用户故事](resource-management/03-user-stories.md)
- [开发任务](resource-management/04-development-tasks.md)
- [单元测试用例](resource-management/05-unit-test-cases.md)
