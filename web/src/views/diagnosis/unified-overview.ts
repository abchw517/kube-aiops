import { createOverviewClient } from "../../api/client.ts";
import { ApiError, type Cluster, type Finding, type Incident, type Severity } from "../../api/generated.ts";
import { escapeHtml, formatTimestamp } from "../../components/html.ts";
import { diagnosticListPaths } from "../../domain/diagnostic-routes.ts";
import { aggregateDiagnostics, diagnosticLevels, incidentLevel, overviewLevel } from "../../domain/unified-diagnostics.ts";
import { incidentHref, incidentStatusLabels } from "./incident-filters.ts";
import { bindIncidentOverview } from "./incident-overview.ts";

const maxPages = 20;
const levels = Object.entries(diagnosticLevels) as [Severity, string][];
type Page<T> = { items: T[]; pagination: { continue?: string }; coverage?: string };
type Coverage = "complete" | "partial" | "unavailable" | "restricted";

async function readPages<T>(read: (cursor?: string) => Promise<Page<T>>) {
  const items: T[] = [];
  const seen = new Set<string>();
  let cursor: string | undefined;
  let coverage: Coverage = "complete";
  for (let index = 0; index < maxPages; index++) {
    try {
      const page = await read(cursor);
      items.push(...page.items);
      if (page.coverage === "partial") coverage = "partial";
      cursor = page.pagination.continue;
      if (!cursor) return { items, coverage };
      if (seen.has(cursor)) break;
      seen.add(cursor);
    } catch (error) {
      if (error instanceof ApiError && error.status === 401) throw error;
      if (error instanceof ApiError && error.status === 403) return { items: [], coverage: "restricted" as const };
      return { items, coverage: items.length ? "partial" as const : "unavailable" as const };
    }
  }
  return { items, coverage: "partial" as const };
}

function href(path: string, query: URLSearchParams): string {
  return path + (query.size ? `?${query}` : "");
}

function choices(entries: [string, string][], selected: string): string {
  return entries.map(([key, label]) =>
    `<option value="${escapeHtml(key)}"${key === selected ? " selected" : ""}>${escapeHtml(label)}</option>`).join("");
}

function renderScope(clusters: Cluster[], instance: string, severity?: Severity) {
  const entries: [string, string][] = clusters.map(({ id, name }) => [id, name === id ? id : `${name} · ${id}`]);
  if (instance && !entries.some(([id]) => id === instance)) entries.push([instance, instance]);
  return `<form class="panel incident-filters unified-overview-scope" data-unified-filters aria-label="诊断总览筛选">
    <label>集群实例<select name="clusterId">${choices([["", "全部可见实例"], ...entries], instance)}</select></label>
    <label>诊断级别<select name="severity">${choices([["", "全部级别"], ...levels], severity ?? "")}</select></label>
    <button class="button secondary" type="submit">应用筛选</button>
  </form>`;
}

const coverageLabels = { partial: "覆盖不完整", unavailable: "暂不可用", restricted: "当前权限无法读取" } as const;

export async function loadUnifiedOverview(hash: string, canReadIncidents: boolean, signal: AbortSignal) {
  const query = new URLSearchParams(hash.split("?")[1]);
  const scope = query.get("clusterId")?.trim() || query.get("cluster")?.trim() || "";
  const cluster = scope === "*" ? "" : scope;
  const severity = overviewLevel(query.get("severity"));
  const client = createOverviewClient(signal);
  const [incident, finding, catalog] = await Promise.all([
    canReadIncidents ? readPages<Incident>((cursor) => client.listIncidents({ clusterId: cluster || undefined, limit: 100, continue: cursor }))
      : { items: [], coverage: "restricted" as const },
    readPages<Finding>((cursor) => client.listFindings({ cluster: cluster || "*", limit: 100, continue: cursor })),
    client.listClusters().then(({ items }) => items, (error: unknown) => {
      if (error instanceof ApiError && error.status === 401) throw error;
      return undefined;
    }),
  ]);
  const controls = renderScope(catalog ?? [], cluster, severity);
  // Absence from a discovery catalog can coexist with a narrower direct grant.
  // The list endpoints decide the requested instance and never widen it.
  const sources = [["故障事件", incident.coverage], ["K8sGPT", finding.coverage]] as const;
  const coverage = sources.filter(([, state]) => state !== "complete").map(([name, state]) =>
    `${name}：${coverageLabels[state as keyof typeof coverageLabels]}`);
  const hasData = sources.some(([, state]) => state === "complete" || state === "partial");
  if (!hasData) {
    return controls + `<section class="panel incident-header"><p role="status">${escapeHtml(coverage.join("；"))}</p>
      <button type="button" class="button secondary" data-action="retry">重试</button></section>`;
  }
  const data = aggregateDiagnostics(incident.items, finding.items, severity);
  const returnQuery = new URLSearchParams();
  if (cluster) returnQuery.set("clusterId", cluster);
  if (severity) returnQuery.set("severity", severity);
  return controls + renderOverview(data, returnQuery, severity, coverage, canReadIncidents);
}

function renderOverview(data: ReturnType<typeof aggregateDiagnostics>, query: URLSearchParams, severity: Severity | undefined, coverage: string[], canReadIncidents: boolean) {
  const eventQuery = new URLSearchParams(query);
  const findingsQuery = new URLSearchParams();
  const cluster = query.get("clusterId");
  if (cluster) findingsQuery.set("cluster", cluster);
  if (severity) {
    findingsQuery.set("severity", severity);
    const entry = Object.entries(incidentLevel).find(([, level]) => level === severity);
    if (entry) eventQuery.set("severity", entry[0]);
  }
  const metrics: [string, string, number][] = [["", "诊断记录总数", data.total],
    ...levels.map(([level, label]): [string, string, number] => [level, label, data.counts[level]])];
  return `<section class="unified-overview" aria-label="诊断概况">
    ${coverage.length ? `<p class="system-notice" role="status">${escapeHtml(coverage.join("；"))}。仅统计当前可获得的记录。
      <button type="button" class="button secondary" data-action="retry">重试</button></p>` : ""}
    <div class="summary-grid" aria-label="诊断指标">${metrics.map(([level, label, count]) => {
      const filters = new URLSearchParams(query);
      if (level) filters.set("severity", level);
      else filters.delete("severity");
      return `<a class="metric-card metric-${level || "total"}" href="${escapeHtml(href(diagnosticListPaths.overview, filters))}"
        ${(severity ?? "") === level ? 'aria-current="true"' : ""}><span>${label}</span><strong>${count}</strong></a>`;
    }).join("")}</div>
    <div class="incident-overview-plots">${renderTrend(data)}
      <section class="panel incident-overview-panel"><h2>诊断级别分布</h2><ul class="incident-severity-legend">
        ${levels.map(([level, label]) => `<li><span class="incident-plot-key incident-plot-${level}"></span><span>${label}</span><strong>${data.counts[level]}</strong></li>`).join("")}
      </ul></section>
    </div>
    <section class="panel incident-header"><header class="diagnostic-toolbar"><h2>最近诊断</h2><div class="catalog-actions">
      <a class="button secondary" href="${escapeHtml(href(diagnosticListPaths.events, canReadIncidents ? eventQuery : findingsQuery))}">查看故障事件</a>
      <a class="button secondary" href="${escapeHtml(href(diagnosticListPaths.findings, findingsQuery))}">查看原有诊断结果</a></div></header>
      ${renderRecent(data.recent, query)}
    </section>
  </section>`;
}

function renderTrend(data: ReturnType<typeof aggregateDiagnostics>) {
  const max = Math.max(1, ...data.daily.map((day) => day.incident + day.finding));
  return `<section class="panel incident-overview-panel"><h2>近 7 天诊断趋势</h2>
    <div class="unified-trend">${data.daily.map(({ date, incident, finding }) => {
      const hint = `${date}（UTC）共 ${incident + finding} 条，故障 ${incident} 条，K8sGPT ${finding} 条`;
      return `<button class="unified-trend-day" type="button" data-incident-trend-point="${hint}" aria-label="${hint}" title="${hint}">
        <span class="unified-trend-bars" aria-hidden="true"><span style="height:${incident / max * 100}%"></span><span style="height:${finding / max * 100}%"></span></span>
        <span>${date.slice(5)}</span></button>`;
    }).join("")}</div>
    <p class="unified-trend-legend"><span>故障事件</span><span>K8sGPT</span></p>
    <p data-incident-trend-note role="status">按当前可见记录的首次发现时间统计（UTC）</p>
    ${data.undated ? `<p>${data.undated} 条记录缺少有效时间，未计入趋势。</p>` : ""}
  </section>`;
}

function renderRecent(rows: ReturnType<typeof aggregateDiagnostics>["recent"], query: URLSearchParams) {
  if (!rows.length) return "<p>当前范围暂无诊断记录。</p>";
  const items = rows.map((row) => {
    const params = new URLSearchParams({ cluster: row.cluster, from: "overview", return: query.toString() });
    const target = row.source === "incident" ? incidentHref(row.id, query.toString(), true) :
      href(`${diagnosticListPaths.findings}/${encodeURIComponent(row.id)}`, params);
    return `<tr><td><a href="${escapeHtml(target)}">${escapeHtml(row.title)}</a></td>
      <td>${diagnosticLevels[row.severity]}</td><td>${row.status ? incidentStatusLabels[row.status] : "—"}</td>
      <td>${escapeHtml(row.source === "incident" ? `故障事件 · ${row.origin}` : row.origin)}</td>
      <td>${escapeHtml(row.cluster || "—")}</td><td>${escapeHtml(formatTimestamp(row.observed))}</td></tr>`;
  });
  return `<div class="incident-table unified-recent" tabindex="0" role="region" aria-label="最近诊断记录"><table>
    <thead><tr><th>问题</th><th>级别</th><th>处置状态</th><th>来源</th><th>集群实例</th><th>首次发现</th></tr></thead>
    <tbody>${items.join("")}</tbody></table></div>`;
}

export function bindUnifiedOverview(root: HTMLElement): void {
  bindIncidentOverview(root);
  root.querySelector<HTMLFormElement>("[data-unified-filters]")?.addEventListener("submit", (event) => {
    event.preventDefault();
    const form = event.currentTarget as HTMLFormElement;
    const query = new URLSearchParams();
    for (const [key, value] of new FormData(form)) {
      if (typeof value === "string" && value) query.set(key, value);
    }
    window.location.hash = href(diagnosticListPaths.overview, query);
  });
}
