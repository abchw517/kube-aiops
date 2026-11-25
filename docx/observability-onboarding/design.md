# kube-aiops 可观测数据源接入设计

交付对象为「资源管理 → 数据源管理」。沿用截图里的 Zabbix 折叠区、右侧接入按钮和「连接 → 范围 → 预览 → 保存并启用」路径，新增 Prometheus、VictoriaLogs（VLS）、Alertmanager、Grafana。原型为独立 HTML，使用演示数据。

[可点击原型](https://chatgpt.com/library?file_id=libfile_c809123ec82c8191a67dfde7dd10726c&account_id=personal) · [PRD 和交付索引](00-prd.md)

## 一、接入后在哪里使用

| 数据源 | 首期用途 | 在诊断里出现的位置 | 接入验证 |
| --- | --- | --- | --- |
| Prometheus | 读取指标与时序，复用现有 PromQL 诊断查询 | 故障和 K8sGPT 诊断详情的指标证据 | `/api/v1/query` 有界读取。选定范围后预览 `up`，后续由诊断详情的固定模板判断具体指标覆盖。 |
| VictoriaLogs | 读取有界 LogsQL 聚合，支持后续按权限扩展脱敏样本 | 诊断详情的日志证据 | `/select/logsql/stats_query_range` 预览最近一段时间的日志统计。可配置集群字段和租户信息。 |
| **Alertmanager** | 同步当前告警，保留来源和处置状态，供总览与关联证据使用 | 诊断总览增加 Alertmanager 来源。详情保留告警名称、实例、级别、首次触发和上游状态 | `GET /api/v2/alerts` 按已授权范围拉取，预览当前告警和可选的静默／抑制告警。 |
| Grafana | 发现授权的 Dashboard，保存 Dashboard UID，按诊断上下文生成跳转链接 | 诊断详情的「查看 Grafana 仪表盘」 | 使用只读 Service Account Token 查询可见仪表盘。兼容适配器可使用 `GET /api/search`，并按目标 Grafana 版本验证。 |
| Zabbix | 继续同步已选主机组的问题 | 诊断总览与问题详情 | 延用现有接入流程和权限。 |

指标预览验证访问和数据覆盖，`up` 返回空数组时显示「连接正常 · 范围内暂无序列」。日志预览同样区分空窗口与上游不可用。Grafana 跳转沿用用户自身会话和 Grafana 权限，首期链接传 `from` 和 `to`，按诊断首次发现前 30 分钟组装。Dashboard 变量映射留作后续扩展。Grafana 中指向 Prometheus 或 VictoriaLogs 的数据源仍由对应连接器提供证据。项目当前已经通过环境变量运行前三类证据适配器，页面接入将复用查询逻辑和限额。

## 二、页面与操作

| 区域 | 行为 |
| --- | --- |
| 数据源列表 | 按类型展示五个可折叠区块。标题显示连接数，右侧接入按钮始终可见。全局可按环境和状态筛选；保存后展开对应区块并显示新连接。资产数据源入口继续位于下方。 |
| 步骤一 · 连接 | 输入名称、环境、站点地址，选择认证方式。用户填写站点根地址或带反向代理前缀的基地址，平台拼接固定 API 路径。Grafana 使用 Service Account Token，其他 HTTP 接入按部署支持 Bearer、Basic 或免认证。 |
| 步骤二 · 范围 | 从授权目录选择业务和集群。集群独享端点显式绑定实例，共用端点要求配置并验证精确标签或字段等于某个值。VictoriaLogs 可附加固定租户，Grafana 授权后选择一个或多个 Dashboard。未建立明确映射的记录不会进入业务总览。 |
| 步骤三 · 预览 | 测试连接后可预览当前范围。提示权限不足、超时、证书不可信、标签不匹配。预览成功且样本为零时仍可启用，有覆盖提示的预览须明确确认或修正。 |
| 保存与维护 | 连接状态和采集状态分别显示。列表展示名称、环境／业务／实例、上次测试或成功同步时间。支持重测、编辑、轮换凭据、暂停、恢复。Alertmanager 同步后才更新总览。 |

普通路径使用一个弹窗完成。实现要求先选择环境、业务和集群，再发起连接测试，从第一次请求开始验证授权范围。连接和采集范围桌面并排，窄屏顺序显示。变更地址、凭据、范围映射或仪表盘选择后，重新验证受影响的连接或预览。测试从 API 服务所在网络发起，页面不会把浏览器可访问误报成 API 已可访问。

## 三、Alertmanager 首期方案

1. **轮询接入，先接当前告警。** 预览和同步使用只读 API v2，范围由已授权的平台环境、业务、集群和精确上游 matcher 共同确定。每 60 秒进行一次有界轮询，由后台 worker 执行。手动同步进入同一队列。静默和抑制按上游 suppressed 状态合并标注，未处理单独标记。默认总览只展示活动告警。列表保留上游状态与原始严重度。
2. **按告警实例去重。** 同一逻辑 Alertmanager 使用具备 HA 能力的 HTTPS 入口。对同一范围内的规范化标签计算指纹，以指纹加 `startsAt` 标识一次告警。组名只用于展示。使用 `severity` 到平台级别的显式映射，未命中的保留原值并显示未映射状态。重试或主备切换不会重复计入。
3. **保留轮询边界。** 成功轮询中不再出现的告警标记「已离开当前告警列表」，无需从列表消失推断恢复。同步失败保留最后成功快照并标注时间和过期，避免误报 0 或批量恢复。趋势按平台已经采到的有效首次触发时间统计。Alertmanager 来源可切换查看已离开列表的近期记录，快照保留窗口为 30 天，单连接上限 1000 条。来源间的诊断与告警总览保持各自的记录身份，详情使用既有关联证据。
4. **后续按需接 Webhook。** 若需要明确恢复时间或覆盖短时告警，再配置 Alertmanager Webhook receiver 和 `send_resolved: true`，依靠受信凭据、范围校验、幂等落库和乱序保护接收通知。Webhook 和轮询共享事件身份，管理页单独提示通知覆盖范围。静默、抑制、路由和分组频率会影响通知，成功接收 Webhook 也无法自动补齐配置前历史。

## 四、路由、状态和权限

| 主题 | 设计 |
| --- | --- |
| 数据源配置 | 主记录保存类型、名称、基地址、凭据引用、认证方式、启用状态和修订号。范围绑定另存环境／业务／实例、上游映射及可选租户。同步状态保存最近尝试、成功时间、覆盖或失败类别。首期保存前由 API 再次验证同一范围，浏览器表单仅在弹窗内保留输入凭据，关闭后清理。 |
| 凭据和网络 | 用户在页面录入凭据，服务端使用部署已有的密钥设施加密保存，详情接口只返回是否已配置。兼容旧文件凭据。连接目标须由平台预先允许，生产默认 HTTPS，私有 CA 由平台信任设施提供。平台按类型设置精确 HTTPS 基地址白名单，禁用出站代理和重定向。连接层核对解析后的 IP，拦截回环、链路本地和非单播地址。内部部署可继续用集群出站网络策略限制可信网段。私有 CA 通过 API 进程的系统信任库配置。凭据轮换、修改范围、启停均留审计。 |
| 权限与查询 | 复用数据源查看、维护、测试能力，并校验每个绑定的环境／业务／集群权限。使用白名单查询模板、有界时间窗、并发和响应大小限制。对共享上游先验证映射，返回结果仍做范围复核。记录、日志片段或完整标签按授权和脱敏规则呈现。 |
| 多连接选择 | 同一类型按当前诊断的环境／业务／集群解析一个启用的证据绑定。冲突时阻止启用或提示先解除旧绑定，避免跨范围回退。不同观测集群可配置独立连接，共享后端可配置多个明确绑定。 |
| 存量迁移 | 读取现有 `PROMETHEUS_*`、`VICTORIALOGS_*` 和 `ALERTMANAGER_*` 配置，作为只读的「部署配置」显示。已验证的同范围平台连接可显式接管；暂停平台连接后，诊断证据恢复该集群原有部署配置。启用连接的读取失败按诊断不可用处理。切换时校对查询结果与权限是否一致。 |
| 停用与过期 | 暂停后停止新查询／采集，并从默认实时视图移除。恢复后立即验证或同步。失败快照按授权和时效标识展示。初次同步进行中、已验证零结果和上游失败各有独立状态。 |

## 五、交付顺序和验收

| 阶段 | 验收 |
| --- | --- |
| P0 · 连接与证据 | 同一弹窗完成四类连接的测试、绑定、预览和保存。Prometheus 与 VictoriaLogs 复用诊断证据位置。Grafana 可选仪表盘并生成带时间的跳转链接。验证跨范围无访问及旧部署配置可继续查询。 |
| P1 · Alertmanager 总览 | 同一共享后端用不同 matcher 绑定多个集群。轮询失败保留已授权的旧快照并标记过期，空结果与失败状态可区分。试验重复轮询、静默抑制、HA、权限变化和首次入库时间。 |
| P2 · Alertmanager Webhook | 验收标准包含短时告警、明确恢复、重复通知、乱序、上游路由覆盖和超限处理。按需推进。 |

原型可演示所有四类接入、范围预览、保存、列表筛选和启停。Alertmanager 展示轮询模式及静默／抑制预览，Grafana 展示仪表盘勾选。实际接入能力以上线实现和各环境联调结果为准。

## 部署配置

配置允许接入的 HTTPS 基地址。可按需部署单个类型；从旧部署参数读取的连接继续在原集群作用域使用。每个业务、环境、集群与类型组合支持一个平台绑定，编辑可以轮换凭据或更换服务地址。

| 类型 | API 环境变量示例 |
| --- | --- |
| Prometheus | `DIAGNOSTIC_PROMETHEUS_ENDPOINTS=https://obs.example.com/prometheus` |
| VictoriaLogs | `DIAGNOSTIC_VICTORIALOGS_ENDPOINTS=https://obs.example.com/logs` |
| Alertmanager | `DIAGNOSTIC_ALERTMANAGER_ENDPOINTS=https://obs.example.com/alertmanager` |
| Grafana | `DIAGNOSTIC_GRAFANA_ENDPOINTS=https://obs.example.com/grafana` |

每个变量可用逗号分隔多个基地址。持久化身份 SQLite 和同目录的 `.observability-credentials.key` 文件，备份时两者须一并保留。生产启用前确认登录用户具备数据源查看、维护或测试权限，非管理员同时需要环境／业务与集群范围授权。在共享上游使用精确集群标签；Alertmanager 带 namespace 的记录还需匹配平台命名空间业务归属。共享命名空间缺少唯一业务归属时跳过相应告警。

## 接口参考

- [Prometheus HTTP API](https://prometheus.io/docs/prometheus/latest/querying/api/)
- [VictoriaLogs 查询接口](https://docs.victoriametrics.com/victorialogs/querying/)
- [Alertmanager API v2](https://github.com/prometheus/alertmanager/blob/main/api/v2/openapi.yaml) · [Webhook receiver 配置](https://prometheus.io/docs/alerting/latest/configuration/#webhook_config)
- [Grafana Dashboard 搜索](https://grafana.com/docs/grafana/latest/developer-resources/api-reference/http-api/api-legacy/folder_dashboard_search/) · [Service Account](https://grafana.com/docs/grafana/latest/administration/service-accounts/) · [Dashboard 链接参数](https://grafana.com/docs/grafana/latest/visualizations/dashboards/build-dashboards/create-dashboard-url-variables/)
