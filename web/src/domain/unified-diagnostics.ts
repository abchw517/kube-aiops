import type { Finding, Incident, IncidentSeverity, ObservabilityAlert, Severity, ZabbixProblem } from "../api/generated.ts";

export const diagnosticLevels = { critical: "严重", warning: "警告", info: "信息" } as const;
export const incidentLevel: Record<IncidentSeverity, Severity> = { P1: "critical", P2: "warning", P3: "info" };

export function overviewLevel(value: string | null): Severity | undefined {
  if (value === "P1" || value === "P2" || value === "P3") return incidentLevel[value];
  return value === "critical" || value === "warning" || value === "info" ? value : undefined;
}

export const overviewWindows = [7, 14, 30] as const;
export type OverviewDays = typeof overviewWindows[number];

export function overviewDays(value: string | null): OverviewDays {
  return overviewWindows.find((days) => String(days) === value) ?? 7;
}

export const overviewPageSizes = [5, 10, 15] as const;
export const maxOverviewPageSize = 100;

export function overviewPageSize(value: string | null): number | undefined {
  if (!value || !/^\d{1,3}$/.test(value)) return undefined;
  const size = Number(value);
  if (size > 0 && size <= maxOverviewPageSize) return size;
}

export function overviewRecordPage(query: URLSearchParams, total: number) {
  const size = overviewPageSize(query.get("recentPageSize")) ?? overviewPageSizes[0];
  const pages = Math.max(1, Math.ceil(total / size));
  const requested = Number(query.get("recentPage"));
  const page = Number.isSafeInteger(requested) && requested > 1 ? Math.min(requested, pages) : 1;
  return { page, pages, size, start: (page - 1) * size, end: Math.min(page * size, total) };
}

export function overviewSearch(query: URLSearchParams): URLSearchParams {
  const result = new URLSearchParams();
  const cluster = query.get("clusterId")?.trim() || query.get("cluster")?.trim();
  const severity = overviewLevel(query.get("severity"));
  const days = overviewDays(query.get("trendDays") ?? query.get("days"));
  if (cluster && cluster !== "*") result.set("clusterId", cluster);
  if (severity) result.set("severity", severity);
  for (const key of ["environmentId", "businessId"]) {
    const value = query.get(key)?.trim();
    if (value) result.set(key, value);
  }
  const source = query.get("source");
  if (source === "incident" || source === "finding" || source === "zabbix" || source === "alertmanager") result.set("source", source);
  if (query.get("recent") === "true") result.set("recent", "true");
  if (query.get("alertRecent") === "true") result.set("alertRecent", "true");
  if (days !== 7) result.set("trendDays", String(days));
  const page = query.get("recentPage");
  if (page && /^[1-9]\d*$/.test(page) && Number.isSafeInteger(Number(page)) && Number(page) > 1) result.set("recentPage", page);
  const size = overviewPageSize(query.get("recentPageSize"));
  if (size && size !== overviewPageSizes[0]) result.set("recentPageSize", String(size));
  return result;
}

type DiagnosticRecord = {
  id: string;
  cluster: string;
  title: string;
  severity: Severity | "";
  source: "incident" | "finding" | "zabbix" | "alertmanager";
  alert?: ObservabilityAlert;
  problem?: ZabbixProblem;
  origin: string;
  status?: Incident["status"];
  observed?: string;
};

type Day = { date: string; incident: number; finding: number; zabbix: number; alertmanager: number };
const dayMs = 86_400_000;
const utc8OffsetMs = 8 * 3_600_000;

function utc8Date(time: number): string {
  return new Date(time + utc8OffsetMs).toISOString().slice(0, 10);
}

function findingKey(cluster: string, id: string): string {
  return JSON.stringify([cluster, id]);
}

/** Resolve only redirects that end in a visible incident in the same instance. */
function visibleMergeTarget(item: Incident, byID: ReadonlyMap<string, Incident>): boolean {
  const visited = new Set([item.id]);
  let target = item;
  while (target.mergedInto) {
    const next = byID.get(target.mergedInto);
    if (!next || visited.has(next.id) || next.scope.clusterId !== item.scope.clusterId) return false;
    visited.add(next.id);
    target = next;
  }
  return target !== item;
}

function timestamp(value: string | undefined): number | undefined {
  const time = value ? Date.parse(value) : NaN;
  return Number.isFinite(time) ? time : undefined;
}

export function aggregateDiagnostics(incidents: Incident[], findings: Finding[], severity?: Severity, now = Date.now(),
  days: OverviewDays = 7, problems: ZabbixProblem[] = [], alerts: ObservabilityAlert[] = []) {
  const incidentByID = new Map(incidents.map((item) => [item.id, item]));
  const linked = new Set(incidents.flatMap(({ scope, findingId }) =>
    findingId ? [findingKey(scope.clusterId, findingId)] : []));
  const uniqueFindings = new Map(findings.map((item) => [findingKey(item.cluster, item.id), item]));
  const rows: DiagnosticRecord[] = [];
  for (const item of incidentByID.values()) {
    if (visibleMergeTarget(item, incidentByID)) continue;
    rows.push({ id: item.id, cluster: item.scope.clusterId, title: item.title,
      severity: incidentLevel[item.severity], source: "incident", origin: item.source,
      status: item.status, observed: item.firstSeen });
  }
  for (const [key, item] of uniqueFindings) {
    if (!linked.has(key)) rows.push({ id: item.id, cluster: item.cluster, title: item.problem,
      severity: item.severity, source: "finding", origin: "K8sGPT", observed: item.createdAt });
  }

  for (const problem of problems) {
    rows.push({ id: problem.id, cluster: "", title: problem.title, severity: problem.severity,
      source: "zabbix", origin: "Zabbix", observed: problem.startedAt, problem });
  }
  for (const alert of alerts) {
    rows.push({ id: alert.id, cluster: alert.scope.clusterId, title: alert.title, severity: alert.severity,
      source: "alertmanager", origin: "Alertmanager", observed: alert.startedAt, alert });
  }
  const daily = new Map<string, Day>();
  for (let offset = days - 1; offset >= 0; offset--) {
    const date = utc8Date(now - offset * dayMs);
    daily.set(date, { date, incident: 0, finding: 0, zabbix: 0, alertmanager: 0 });
  }
  const counts: Record<Severity, number> = { critical: 0, warning: 0, info: 0 };
  const records = rows.filter((item) => !severity || item.severity === severity)
    .map((item) => ({ ...item, time: timestamp(item.observed) }))
    .sort((a, b) => (b.time ?? -Infinity) - (a.time ?? -Infinity) ||
      a.source.localeCompare(b.source) || a.cluster.localeCompare(b.cluster) || a.id.localeCompare(b.id));
  let undated = 0;
  for (const record of records) {
    if (record.severity) counts[record.severity]++;
    if (record.time === undefined) {
      undated++;
      continue;
    }
    const bucket = daily.get(utc8Date(record.time));
    if (bucket && record.time <= now) bucket[record.source]++;
  }
  return { total: records.length, counts, daily: [...daily.values()], undated, records };
}
