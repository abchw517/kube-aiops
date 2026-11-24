import { createOverviewClient } from "../../api/client.ts";
import { ApiError, type Cluster, type Finding, type Incident, type Severity } from "../../api/generated.ts";
import { escapeHtml } from "../../components/html.ts";
import { diagnosticListPaths } from "../../domain/diagnostic-routes.ts";
import { aggregateDiagnostics, diagnosticLevels, incidentLevel, overviewDays, overviewLevel, overviewSearch, overviewWindows } from "../../domain/unified-diagnostics.ts";
import { incidentHref, incidentStatusLabels } from "./incident-filters.ts";

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

type Overview = ReturnType<typeof aggregateDiagnostics>;
const observedTime = new Intl.DateTimeFormat("zh-CN", {
  timeZone: "Asia/Shanghai", dateStyle: "short", timeStyle: "medium", hourCycle: "h23",
});

function overviewHref(query: URLSearchParams, changes: Record<string, string>): string {
  const next = new URLSearchParams(query);
  for (const [key, value] of Object.entries(changes)) {
    if (value) next.set(key, value);
    else next.delete(key);
  }
  return escapeHtml(href(diagnosticListPaths.overview, overviewSearch(next)));
}

function clearScope(query: URLSearchParams): string {
  return overviewHref(query, { clusterId: "", severity: "" });
}

function renderScope(clusters: Cluster[], query: URLSearchParams, severity?: Severity) {
  const instance = query.get("clusterId") ?? "";
  const entries: [string, string][] = clusters.map(({ id, name }) => [id, name === id ? id : `${name} · ${id}`]);
  if (instance && !entries.some(([id]) => id === instance)) entries.push([instance, instance]);
  const scopeLabel = entries.find(([id]) => id === instance)?.[1] ?? "全部可见实例";
  return `<section class="panel overview-panel overview-scope">
    <form class="overview-toolbar" data-unified-filters aria-label="诊断总览筛选">
      <label>集群实例<select name="clusterId">${choices([["", "全部可见实例"], ...entries], instance)}</select></label>
      <label>诊断级别<select name="severity">${choices([["", "全部级别"], ...levels], severity ?? "")}</select></label>
      <input type="hidden" name="trendDays" value="${overviewDays(query.get("trendDays"))}">
      <button class="button" type="submit">应用筛选</button>
      <a class="overview-text-link" href="${clearScope(query)}">清除筛选</a>
    </form>
    <p class="overview-current-scope">当前范围 <strong>${escapeHtml(scopeLabel)} · ${severity ? diagnosticLevels[severity] : "全部级别"}</strong> <span>故障事件与 K8sGPT</span></p>
  </section>`;
}

const coverageLabels: Record<Coverage, string> = {
  complete: "", partial: "覆盖不完整", unavailable: "暂不可用", restricted: "当前权限无法读取",
};

export async function loadUnifiedOverview(hash: string, canReadIncidents: boolean, signal: AbortSignal) {
  const query = overviewSearch(new URLSearchParams(hash.split("?")[1]));
  const cluster = query.get("clusterId");
  const severity = overviewLevel(query.get("severity"));
  const days = overviewDays(query.get("trendDays"));
  const client = createOverviewClient(signal);
  const [incident, finding, catalog] = await Promise.all([
    canReadIncidents ? readPages<Incident>((cursor) => client.listIncidents({ clusterId: cluster ?? undefined, limit: 100, continue: cursor }))
      : { items: [], coverage: "restricted" as const },
    readPages<Finding>((cursor) => client.listFindings({ cluster: cluster || "*", limit: 100, continue: cursor })),
    client.listClusters().then(({ items }) => items, (error: unknown) => {
      if (error instanceof ApiError && error.status === 401) throw error;
      return [];
    }),
  ]);
  const sources = [["故障事件", incident.coverage], ["K8sGPT", finding.coverage]] as const;
  const coverage = sources.filter(([, state]) => state !== "complete").map(([name, state]) => `${name}：${coverageLabels[state]}`);
  const hasData = sources.some(([, state]) => state === "complete" || state === "partial");
  const incomplete = sources.some(([, state]) => state === "partial" || state === "unavailable");
  const notice = coverage.length ? `<div class="overview-notice" role="status"><span>${escapeHtml(coverage.join("；"))}。${hasData && incomplete ? "仅统计已获取记录。" : ""}</span>
    ${incomplete ? '<button type="button" class="button secondary" data-action="retry">重试</button>' : ""}</div>` : "";
  const data = aggregateDiagnostics(incident.items, finding.items, severity, Date.now(), days);
  return `<section class="unified-overview" aria-label="诊断概况">
    ${renderScope(catalog, query, severity)}${notice}${renderMetrics(hasData ? data : undefined, query, severity)}
    ${hasData ? renderRecent(data.recent, query, canReadIncidents, incomplete) +
      `<div class="overview-analytics">${renderTrend(data, query, incomplete)}${renderDistribution(data, incomplete)}</div>`
      : '<section class="panel overview-panel overview-empty"><strong>最近诊断与图表暂不可用</strong></section>'}
  </section>`;
}

function renderMetrics(data: Overview | undefined, query: URLSearchParams, severity: Severity | undefined) {
  const metrics: [string, string, number | undefined][] = [["", "诊断记录总数", data?.total],
    ...levels.map(([level, label]): [string, string, number | undefined] => [level, label, data?.counts[level]])];
  return `<div class="overview-metrics" aria-label="诊断指标">${metrics.map(([level, label, count]) =>
    `<a class="panel overview-metric metric-${level || "total"}" href="${overviewHref(query, { severity: level })}"
      ${(severity ?? "") === level ? 'aria-current="true"' : ""}><span>${label}</span><strong>${count ?? "—"}</strong></a>`).join("")}</div>`;
}

function renderTrend(data: Overview, query: URLSearchParams, incomplete: boolean) {
  const days = data.daily.length;
  const total = data.daily.reduce((sum, { incident, finding }) => sum + incident + finding, 0);
  const max = Math.max(1, ...data.daily.map((day) => day.incident + day.finding));
  const toggle = `<nav class="overview-segment" aria-label="趋势时间窗">${overviewWindows.map((windowDays) =>
    `<a href="${overviewHref(query, { trendDays: String(windowDays) })}"${windowDays === days ? ' aria-current="true"' : ""}>${windowDays} 天</a>`).join("")}</nav>`;
  const hint = incomplete ? "当前窗口未取得诊断记录。" : data.undated ? "已取得的带有效时间记录未落在当前时间窗。" :
    data.total ? "当前范围内有记录，本时间窗内没有首次发现的记录。" : "当前筛选范围没有匹配的诊断记录。";
  const emptyTitle = incomplete ? "数据覆盖不完整" : data.undated ? "未取得当前窗口的带时间记录" : `近 ${days} 天无新增`;
  return `<section class="panel overview-panel" aria-label="诊断趋势">
    <header class="overview-heading"><div><h2>诊断趋势</h2><p>${data.daily[0]?.date} 至 ${data.daily.at(-1)?.date}（UTC）</p></div>${toggle}</header>
    ${total ? `<div class="overview-plot" style="--overview-days:${days}">${data.daily.map(({ date, incident, finding }, index) => {
      const label = `${date}（UTC）共 ${incident + finding} 条，故障 ${incident} 条，K8sGPT ${finding} 条`;
      const showDate = index === 0 || (index !== days - 2 && index % Math.ceil(days / 5) === 0) || index === days - 1;
      return `<button class="overview-trend-day" type="button" data-overview-point="${label}" aria-pressed="false" aria-label="${label}" title="${label}">
        <span class="overview-trend-bars" aria-hidden="true"><span style="height:${incident / max * 100}%"></span><span style="height:${finding / max * 100}%"></span></span>
        <span class="overview-day-label" aria-hidden="true">${showDate ? date.slice(5) : ""}</span></button>`;
    }).join("")}</div>
    <p class="overview-trend-legend"><span>故障事件</span><span>K8sGPT</span></p>
    <p data-overview-note class="overview-chart-note" role="status">${incomplete ? "已获取" : "窗口内"} ${total} 条</p>`
    : `<div class="overview-trend-empty"><strong>${emptyTitle}</strong><p>${hint}</p>
      ${days === 7 && data.total ? `<a href="${overviewHref(query, { trendDays: "14" })}">查看近 14 天</a>` : ""}</div>`}
    ${data.undated ? `<p class="overview-chart-note">${data.undated} 条记录缺少有效时间，未计入趋势。</p>` : ""}
  </section>`;
}

function renderDistribution(data: Overview, incomplete: boolean) {
  return `<section class="panel overview-panel"><header class="overview-heading"><div><h2>严重度分布</h2>
    <p>${incomplete ? "已获取记录" : "当前范围"} · ${data.total} 条</p></div></header>
    <ul class="overview-distribution">${levels.map(([level, label]) => {
      const count = data.counts[level];
      return `<li class="overview-${level}"><span>${label}</span>
        <div class="overview-progress" aria-hidden="true"><span style="width:${data.total ? count / data.total * 100 : 0}%"></span></div>
        <strong>${count}</strong></li>`;
    }).join("")}</ul>
  </section>`;
}

function renderRecent(rows: Overview["recent"], query: URLSearchParams, canReadIncidents: boolean, incomplete: boolean) {
  const eventQuery = new URLSearchParams(query);
  eventQuery.delete("trendDays");
  const findingsQuery = new URLSearchParams();
  const cluster = query.get("clusterId");
  const severity = overviewLevel(query.get("severity"));
  if (cluster) findingsQuery.set("cluster", cluster);
  if (severity) {
    findingsQuery.set("severity", severity);
    const entry = Object.entries(incidentLevel).find(([, level]) => level === severity);
    if (entry) eventQuery.set("severity", entry[0]);
  }
  const items = rows.map((row) => {
    const params = new URLSearchParams({ cluster: row.cluster, from: "overview", return: query.toString() });
    const target = row.source === "incident" ? incidentHref(row.id, query.toString(), true) :
      href(`${diagnosticListPaths.findings}/${encodeURIComponent(row.id)}`, params);
    return {
      link: `<a href="${escapeHtml(target)}">${escapeHtml(row.title)}</a>`,
      level: `<span class="overview-badge overview-${row.severity}">${diagnosticLevels[row.severity]}</span>`,
      status: row.status ? escapeHtml(incidentStatusLabels[row.status]) : "—",
      source: escapeHtml(row.source === "incident" ? `故障事件 · ${row.origin}` : row.origin),
      instance: escapeHtml(row.cluster || "—"),
      at: row.time === undefined ? "—" : `<time datetime="${escapeHtml(row.observed)}">${escapeHtml(observedTime.format(row.time))}</time>`,
    };
  });
  return `<section class="panel overview-records" aria-label="最近诊断"><header class="overview-heading overview-record-heading">
    <div><h2>最近诊断</h2><p>按首次发现时间倒序，最多 5 条 · UTC+8</p></div><div class="overview-actions">
    <a class="button secondary" href="${escapeHtml(href(diagnosticListPaths.events, canReadIncidents ? eventQuery : findingsQuery))}">查看故障事件</a>
    <a class="button secondary" href="${escapeHtml(href(diagnosticListPaths.findings, findingsQuery))}">查看原有诊断结果</a></div></header>
    ${items.length ? `<div class="overview-table-wrap unified-recent" tabindex="0" role="region" aria-label="最近诊断记录"><table>
      <thead><tr><th scope="col">问题</th><th scope="col">级别</th><th scope="col">处置状态</th><th scope="col">来源</th><th scope="col">实例</th><th scope="col">首次发现</th></tr></thead>
      <tbody>${items.map((item) => `<tr><td>${item.link}</td><td>${item.level}</td><td>${item.status}</td><td>${item.source}</td><td>${item.instance}</td><td>${item.at}</td></tr>`).join("")}</tbody>
    </table></div><ul class="overview-record-cards" aria-label="最近诊断记录">${items.map((item) => `<li>${item.link}<div class="overview-card-details">
      ${item.level}<span>${item.source}</span><span>${item.instance}</span>${item.status === "—" ? "" : `<span>${item.status}</span>`}<span>${item.at}</span>
      </div></li>`).join("")}</ul>`
    : `<div class="overview-empty"><strong>${incomplete ? "当前范围尚未取得诊断记录" : "当前范围暂无诊断记录"}</strong><a href="${clearScope(query)}">清除筛选</a></div>`}
  </section>`;
}

export function bindUnifiedOverview(root: HTMLElement): void {
  root.querySelector<HTMLFormElement>("[data-unified-filters]")?.addEventListener("submit", (event) => {
    event.preventDefault();
    const form = event.currentTarget as HTMLFormElement;
    const query = new URLSearchParams();
    for (const [key, value] of new FormData(form)) {
      if (typeof value === "string" && value) query.set(key, value);
    }
    window.location.hash = href(diagnosticListPaths.overview, overviewSearch(query));
  });
  const note = root.querySelector<HTMLElement>("[data-overview-note]");
  if (!note) return;
  for (const point of root.querySelectorAll<HTMLButtonElement>("[data-overview-point]")) {
    point.addEventListener("click", () => {
      root.querySelector('[data-overview-point][aria-pressed="true"]')?.setAttribute("aria-pressed", "false");
      point.setAttribute("aria-pressed", "true");
      note.textContent = point.dataset.overviewPoint ?? "";
    });
  }
}
