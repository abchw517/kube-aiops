import type { Finding, Incident, IncidentSeverity, Severity } from "../api/generated.ts";

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

export function overviewSearch(query: URLSearchParams): URLSearchParams {
  const result = new URLSearchParams();
  const cluster = query.get("clusterId")?.trim() || query.get("cluster")?.trim();
  const severity = overviewLevel(query.get("severity"));
  const days = overviewDays(query.get("trendDays"));
  if (cluster && cluster !== "*") result.set("clusterId", cluster);
  if (severity) result.set("severity", severity);
  if (days !== 7) result.set("trendDays", String(days));
  return result;
}

type DiagnosticRecord = {
  id: string;
  cluster: string;
  title: string;
  severity: Severity;
  source: "incident" | "finding";
  origin: string;
  status?: Incident["status"];
  observed?: string;
};

type Day = { date: string; incident: number; finding: number };
const dayMs = 86_400_000;

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

export function aggregateDiagnostics(incidents: Incident[], findings: Finding[], severity?: Severity, now = Date.now(), days: OverviewDays = 7) {
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

  const end = Math.floor(now / dayMs) * dayMs;
  const daily = new Map<string, Day>();
  for (let offset = days - 1; offset >= 0; offset--) {
    const date = new Date(end - offset * dayMs).toISOString().slice(0, 10);
    daily.set(date, { date, incident: 0, finding: 0 });
  }
  const counts: Record<Severity, number> = { critical: 0, warning: 0, info: 0 };
  const records = rows.filter((item) => !severity || item.severity === severity)
    .map((item) => ({ ...item, time: timestamp(item.observed) }))
    .sort((a, b) => (b.time ?? -Infinity) - (a.time ?? -Infinity) ||
      a.source.localeCompare(b.source) || a.cluster.localeCompare(b.cluster) || a.id.localeCompare(b.id));
  let undated = 0;
  for (const record of records) {
    counts[record.severity]++;
    if (record.time === undefined) {
      undated++;
      continue;
    }
    const bucket = daily.get(new Date(record.time).toISOString().slice(0, 10));
    if (bucket && record.time <= now) bucket[record.source]++;
  }
  return { total: records.length, counts, daily: [...daily.values()], undated, recent: records.slice(0, 5) };
}
