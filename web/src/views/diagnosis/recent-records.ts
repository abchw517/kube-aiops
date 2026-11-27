import { escapeHtml as h } from "../../components/html.ts";
import type { aggregateDiagnostics } from "../../domain/unified-diagnostics.ts";
import { incidentStatusLabels } from "./incident-filters.ts";

export type RecentRecord = ReturnType<typeof aggregateDiagnostics>["records"][number];
export const recentFilters = ["recentText", "recentSource", "recentLevel", "recentStatus"] as const;
export const recentSources = { zabbix: "Zabbix", alertmanager: "Alertmanager", finding: "K8sGPT", incident: "故障事件" } as const;
export const recentLevels = { critical: "严重", warning: "警告", info: "信息" } as const;
const alertStates = { active: "活动", suppressed: "静默或抑制", unprocessed: "待处理", departed: "已离开当前列表" };

export function recordStatus(row: RecentRecord): string {
  if (row.problem) return row.problem.recovered ? "已恢复" : "未恢复";
  if (row.alert) return alertStates[row.alert.state];
  return row.status ? incidentStatusLabels[row.status] : "未提供";
}

export function recordTarget(row: RecentRecord): string {
  return row.problem?.hosts.map(host => host.name).filter(Boolean).join("、") || row.cluster || "—";
}

export function recordTags(row: RecentRecord): string {
  const status = recordStatus(row);
  return `<span class="pill ${row.severity === "critical" ? "bad" : row.severity === "warning" ? "warn" : ""}">${row.severity ? recentLevels[row.severity] : "未映射"}</span>
    <span class="pill ${status === "已恢复" ? "good" : ""}">${h(status)}</span>`;
}

export function filterRecent(rows: RecentRecord[], query: URLSearchParams) {
  const [text, source, level, status] = recentFilters.map(key => query.get(key) || "");
  return rows.flatMap((row, index) => (!source || row.source === source) &&
    (!level || row.severity === level) && (!status || recordStatus(row) === status) &&
    [row.title, recordTarget(row), row.id, row.problem?.eventId].join(" ").toLowerCase().includes((text ?? "").trim().toLowerCase()) ? [{row, index}] : []);
}

export function recordDate(row: RecentRecord): string {
  return row.time === undefined ? "—" : new Intl.DateTimeFormat("zh-CN", {
    timeZone: "Asia/Shanghai", year: "numeric", month: "2-digit", day: "2-digit",
    hour: "2-digit", minute: "2-digit", second: "2-digit", hourCycle: "h23",
  }).format(row.time);
}
