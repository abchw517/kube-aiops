import type { DiagnosticDatasource, ObservabilitySource, ZabbixScopeChoice, ZabbixSource } from "../../api/generated.ts";
import { escapeHtml as h } from "../../components/html.ts";

export const cloudNames = { "aliyun-ecs": "阿里云 ECS", "tencent-cvm": "腾讯云 CVM" };
const cloudHealth: Record<string, string> = {
  untested: "待验证", available: "可用", partial: "部分可用", unavailable: "不可用", timeout: "超时",
  unauthorized: "上游拒绝授权", credentials_unavailable: "凭据不可用", configuration_error: "配置错误",
};
export const zabbixHealth: Record<string, string> = {
  pending: "等待首次同步", syncing: "正在同步", available: "同步正常", stale: "数据已过期",
  unavailable: "首次同步失败", restricted: "采集权限不可用", paused: "已暂停",
};
export type SourceRecord =
  | { family: "zabbix"; source: ZabbixSource }
  | { family: "observability"; source: ObservabilitySource }
  | { family: "assets"; source: DiagnosticDatasource };

export const sourceKey = ({ family, source }: SourceRecord): string => `${family}:${source.id}`;
export const sourceKind = (row: SourceRecord): string => row.family === "assets" ? row.source.config.kind :
  row.family === "observability" ? row.source.kind : "zabbix";
export const sourceName = (row: SourceRecord): string => row.source.name || `Zabbix · ${row.family === "zabbix" ? row.source.environmentName || row.source.scope.environmentId : row.source.id}`;
export const isSync = (row: SourceRecord): boolean => row.family === "zabbix" || (row.family === "observability" && row.source.kind === "alertmanager");

export function sourceStatus(row: SourceRecord, failedAttempt = false): { label: string; state: string; snapshot?: boolean } {
  if (!row.source.enabled) return { label: row.family === "assets" ? "已停用" : "已暂停", state: "paused" };
  if (row.family === "assets") {
    const { health, syncedAt } = row.source;
    if (health === "available" && !failedAttempt) return { label: "可用", state: "good" };
    if (health === "untested" && !failedAttempt) return { label: "待验证", state: "untested" };
    return { label: failedAttempt ? "测试并同步失败" : cloudHealth[health] ?? "状态未知", state: "issue", snapshot: Boolean(syncedAt) };
  }
  const { source } = row;
  if (row.family === "zabbix") {
    const { health, syncedAt } = row.source;
    const state = ["stale", "unavailable", "restricted"].includes(health) || failedAttempt ? "issue"
      : ["pending", "syncing"].includes(health) ? "waiting" : health === "available" ? "good" : "enabled";
    return { label: failedAttempt ? "同步请求失败" : zabbixHealth[health] ?? "已启用", state, snapshot: state === "issue" && Boolean(syncedAt) };
  }
  if (row.source.failed || failedAttempt) return { label: isSync(row) ? "同步失败" : "验证失败", state: "issue", snapshot: isSync(row) && Boolean(source.syncedAt) };
  if (isSync(row)) return source.syncedAt ? { label: "同步正常", state: "good" } : { label: "等待首次同步", state: "waiting" };
  return { label: "已启用", state: "enabled" };
}

export function sourceScope(row: SourceRecord, scopes: ZabbixScopeChoice[]): string {
  const { scope } = row.source;
  const env = scopes.find(item => item.id === scope.environmentId);
  const business = env?.businesses.find(item => item.id === scope.businessId);
  const zabbix = row.family === "zabbix" ? row.source : undefined;
  return [zabbix?.environmentName || env?.name || scope.environmentId || "未指定环境",
    zabbix?.businessName || business?.name || scope.businessId,
    "clusterId" in scope ? scope.clusterId : "", "namespace" in scope ? scope.namespace : ""].filter(Boolean).join(" / ");
}

export function sourceTime(value?: string | number): string {
  if (!value) return "—";
  const time = new Date(typeof value === "number" ? value * 1000 : value);
  return Number.isNaN(time.valueOf()) ? h(value) : h(time.toLocaleString("zh-CN", { timeZone: "Asia/Shanghai" }));
}

export function sourceActivity(row: SourceRecord): { label: string; time: string } {
  return isSync(row) || row.family === "assets"
    ? { label: "上次成功同步", time: sourceTime(row.source.syncedAt) }
    : { label: "上次验证", time: sourceTime(row.source.checkedAt) };
}
