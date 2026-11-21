# 诊断中心 V2.0 P2–P3 交付

## 实现范围

| 任务 | 实现 |
| --- | --- |
| DC-T05 根因分析 | 可从故障详情发起独立 RCA，选择证据、补充人工观察、查看任务状态、取消或重试、跳转引用及保存修订版本。SQLite 保存输入、脱敏快照、租约和版本，复用集群已配置的模型。 |
| DC-T06 关联拓扑 | 展示上游、下游与主机监控映射。可登记或解除故障间关系及云资产关系。推断关系必须选择证据。跨实例查询检查两个节点的当前权限及证据所有权。 |
| DC-T07 数据源 | 配置、启停、服务端连接测试和资产同步。支持 Zabbix Host、阿里云 ECS、腾讯云 CVM。可从同步资产创建故障，或将资产关联到既有故障。 |
| DC-T08 策略 | 支持范围、故障级别、信号来源、冷却、并发和启停。策略模板为基于证据的只读 RCA。任务历史记录策略版本，经启用的环境才会触发。 |

## 部署配置

诊断中心沿用生产模式、SQLite、现有资源目录与身份体系。RCA 使用故障所属集群的模型配置。数据源密钥通过 API 容器内的只读 Secret 挂载提供，浏览器传递文件引用。

| API 环境变量 | 用途 |
| --- | --- |
| `DIAGNOSTIC_INCIDENT_ENVIRONMENTS` | P1–P3 的环境范围，沿用 P1 配置规则。 |
| `DIAGNOSTIC_POLICY_ENVIRONMENTS` | 逗号分隔的自动运行环境 ID。空值时策略可保存，调度保持停用。 |
| `DIAGNOSTIC_CREDENTIALS_DIR` | 已挂载的凭据目录绝对路径。文件引用使用该目录下的文件名。 |
| `DIAGNOSTIC_ZABBIX_ENDPOINTS` | 逗号分隔、精确匹配的 HTTPS JSON-RPC 地址。云厂商端点由适配器按地域生成。 |

凭据文件的 `scope` 使用 `environmentId/businessId/clusterId/namespace`，空业务保留分隔符。凭据中的 `kind`、`endpoint`、`region` 和 `assetIds` 约束可使用的数据源配置。`purpose` 为 `inventory` 或 `ingress`。Zabbix 使用 `token`，云资产盘点使用 `accessKeyId`、`accessKeySecret` 和可选的 `sessionToken`。连接器推送使用单独的 `token`。凭据自动重读，可在挂载内轮换同名文件。

```json
{
  "scope": "production/orders/ack-prod-01/prod-order",
  "kind": "zabbix",
  "endpoint": "https://monitor.example.test/api_jsonrpc.php",
  "region": "",
  "assetIds": ["10010"],
  "purpose": "inventory",
  "token": "<只读 Zabbix API Token>"
}
```

配置资产白名单后，数据源测试保存脱敏资产投影，并更新检查和同步时间。已登记资产可在拓扑中作为 `runs_on`、`depends_on` 等关系目标。资产推送使用 `POST /api/v1/diagnostics/ingress/{connectorId}/signals`，以连接器 Bearer 凭据和实例范围验证请求。调用方提交 `incidentId`、原生 `assetId`、每次告警生命周期稳定的 `externalId`、`state`、`observedAt` 和 `summary`。资产和故障必须已映射。重放及乱序输入沿用 P1 去重规则。

## 权限与故障恢复

- 权限目录中可分别授予 `diagnosis.rca.read/run/cancel/save`、`diagnosis.topology.read/write`、`diagnosis.connector.read/write/test` 及 `diagnosis.policy.read/write`。RCA 任务还需要原故障与证据可读。
- 模型输出的引用限于本次快照。只引用历史证据时结论降为证据不足。证据被移出原故障后不再向原故障公开旧分析内容。
- 服务重启后继续处理排队任务。已领取任务在租约过期后显示超时。取消、重放或策略版本变化后的迟到结果不能写入已完成结论。
- 策略只接受启用版本生效后新收集的活跃信号。变更或停用时取消旧版本的待运行任务，保留运行历史。自动诊断范围仅为模型分析。
- 拓扑的覆盖状态表示当前可访问的已登记关系，支持环路，并限制查询深度和节点数量。缺失的拓扑关系显示未知或覆盖不完整。

## 验收口径

自动化验证覆盖 RCA 去重、取消、超时、证据引用和历史版本，拓扑循环和权限裁剪，三种供应商的只读请求与凭据绑定，数据源权限错误和回放隔离，以及策略并发、版本和停用。浏览器回归覆盖七个诊断页面。

真实模型、生产数据源和凭据轮换仍通过对应环境试点验收。接口与前端类型以 [OpenAPI](../../api/openapi.yaml) 为准。

- [Zabbix Host API](https://www.zabbix.com/documentation/current/en/manual/api/reference/host/get)
- [阿里云 ECS DescribeInstances](https://www.alibabacloud.com/help/en/ecs/developer-reference/api-ecs-2014-05-26-describeinstances)
- [腾讯云 CVM 接口参考](https://main.qcloudimg.com/raw/document/intl/product/pdf/tencent-cloud_213_7217_en.pdf)
