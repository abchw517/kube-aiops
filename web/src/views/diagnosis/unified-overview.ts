import { createOverviewClient } from "../../api/client.ts";
import { ApiError, type Cluster, type Finding, type Incident, type Severity, type ZabbixHealth, type ZabbixProblems } from "../../api/generated.ts";
import { escapeHtml } from "../../components/html.ts";
import { diagnosticListPaths } from "../../domain/diagnostic-routes.ts";
import { aggregateDiagnostics, diagnosticLevels, incidentLevel, overviewDays, overviewLevel, overviewSearch, overviewWindows } from "../../domain/unified-diagnostics.ts";
import { incidentHref, incidentStatusLabels } from "./incident-filters.ts";
import { zabbixHealth } from "./zabbix-sources.ts";

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
  return overviewHref(query, { clusterId: "", severity: "", source: "", environmentId: "", businessId: "", recent: "" });
}

function renderScope(clusters: Cluster[], query: URLSearchParams, severity: Severity | undefined, hasZabbix: boolean, sourceScopes: ZabbixHealth[]) {
  const instance = query.get("clusterId") ?? "";
  const entries: [string, string][] = clusters.map(({ id, name }) => [id, name === id ? id : `${name} · ${id}`]);
  if (instance && !entries.some(([id]) => id === instance)) entries.push([instance, instance]);
  const scopeChoices = (kind: "environment" | "business") => {
    const values = new Map<string, string>();
    for (const cluster of clusters) {
      const value = cluster[kind];
      const key = scopeValue(value, kind, sourceScopes);
      if (key && value) values.set(key, value);
    }
    for (const source of sourceScopes) {
      const value = kind === "environment" ? source.scope.environmentId : source.scope.businessId;
      const label = kind === "environment" ? source.environmentName : source.businessName;
      if (value) values.set(value, label || value);
    }
    const selected = query.get(`${kind}Id`) ?? "";
    if (selected && !values.has(selected)) values.set(selected, selected);
    return choices([["", kind === "environment" ? "全部环境" : "全部业务"], ...values], selected);
  };
  const scopeLabel = entries.find(([id]) => id === instance)?.[1] ?? "全部可见实例";
  return `<section class="panel overview-panel overview-scope">
    <form class="overview-toolbar" data-unified-filters aria-label="诊断总览筛选">
    <label>来源<select name="source">${choices([["", "全部来源"], ["incident", "故障事件"], ["finding", "K8sGPT"],
      ...(hasZabbix ? [["zabbix", "Zabbix"] as [string, string]] : [])], query.get("source") ?? "")}</select></label>
    <label>环境<select name="environmentId">${scopeChoices("environment")}</select></label>
    <label>业务<select name="businessId">${scopeChoices("business")}</select></label>
      <label>集群实例<select name="clusterId">${choices([["", "全部可见实例"], ...entries], instance)}</select></label>
      <label>诊断级别<select name="severity">${choices([["", "全部级别"], ...levels], severity ?? "")}</select></label>
      ${hasZabbix ? `<label>Zabbix 状态<select name="recent">${choices([["", "当前记录"], ["true", "含最近恢复"]], query.get("recent") ?? "")}</select></label>` : ""}
      <input type="hidden" name="trendDays" value="${overviewDays(query.get("trendDays"))}">
      <button class="button" type="submit">应用筛选</button>
      <a class="overview-text-link" href="${clearScope(query)}">清除筛选</a>
    </form>
    <p class="overview-current-scope">当前范围 <strong>${escapeHtml(scopeLabel)} · ${severity ? diagnosticLevels[severity] : "全部级别"}</strong> <span>${sourceLabel(query.get("source"), hasZabbix)}</span></p>
  </section>`;
}

const defaultEnvironmentIDs: Record<string, string> = { 研发: "development", 测试: "testing", 预发布: "staging", 生产: "production" };

function scopeValue(label: string | undefined, kind: "environment" | "business", sources: ZabbixHealth[]): string | undefined {
  if (!label) return undefined;
  if (kind === "environment") {
    return sources.find(source => source.environmentName === label)?.scope.environmentId ?? defaultEnvironmentIDs[label] ?? label;
  }
  return sources.find(source => source.businessName === label)?.scope.businessId ?? label;
}

function sourceLabel(source: string | null, hasZabbix: boolean): string {
  if (source === "incident") return "故障事件";
  if (source === "finding") return "K8sGPT";
  if (source === "zabbix") return "Zabbix";
  return hasZabbix ? "故障事件、K8sGPT 与 Zabbix" : "故障事件与 K8sGPT";
}

const coverageLabels: Record<Coverage, string> = {
  complete: "", partial: "覆盖不完整", unavailable: "暂不可用", restricted: "当前权限无法读取",
};

export async function loadUnifiedOverview(hash: string, canReadIncidents: boolean, signal: AbortSignal, canReadZabbix = false) {
  const query = overviewSearch(new URLSearchParams(hash.split("?")[1]));
  const cluster = query.get("clusterId");
  const severity = overviewLevel(query.get("severity"));
  const days = overviewDays(query.get("trendDays"));
  const environmentId = query.get("environmentId") ?? undefined;
  const businessId = query.get("businessId") ?? undefined;
  const source = query.get("source");
  const tryIncidents = !source || source === "incident";
  const tryFindings = !source || source === "finding";
  const tryZabbix = canReadZabbix && (!source || source === "zabbix");
  const client = createOverviewClient(signal);
  const tolerateZabbix = (error: unknown): ZabbixProblems | undefined => {
    if (error instanceof ApiError && error.status === 401) throw error;
    return undefined;
  };
  const [incident, finding, catalog, zabbix, zabbixCatalog] = await Promise.all([
    tryIncidents && canReadIncidents ? readPages<Incident>((cursor) =>
      client.listIncidents({ clusterId: cluster ?? undefined, environmentId, businessId, limit: 100, continue: cursor }))
      : { items: [], coverage: "restricted" as const },
    tryFindings ? readPages<Finding>((cursor) => client.listFindings({ cluster: cluster || "*", limit: 100, continue: cursor }))
      : { items: [], coverage: "restricted" as const },
    client.listClusters().then(({ items }) => items, (error: unknown) => {
      if (error instanceof ApiError && error.status === 401) throw error;
      return [];
    }),
    tryZabbix ? client.listZabbixProblems({ recent: query.get("recent") === "true", environmentId, businessId })
      .catch(tolerateZabbix) : undefined,
    canReadZabbix && (!tryZabbix || environmentId || businessId)
      ? client.listZabbixProblems({ metadataOnly: true }).catch(tolerateZabbix) : undefined,
  ]);
  const sources = [
    ...(tryIncidents ? [["故障事件", incident.coverage] as const] : []),
    ...(tryFindings ? [["K8sGPT", finding.coverage] as const] : []),
  ];
  const coverage = sources.filter(([, state]) => state !== "complete").map(([name, state]) => `${name}：${coverageLabels[state]}`);
  const withinZabbixScope = ({ scope }: { scope: ZabbixHealth["scope"] }) =>
    (!environmentId || scope.environmentId === environmentId) && (!businessId || scope.businessId === businessId);
  const zabbixSources = (zabbix?.sources ?? []).filter(withinZabbixScope);
  const problems = (zabbix?.items ?? []).filter(withinZabbixScope).flatMap(row => {
    if (!cluster) return [row];
    const hosts = row.hosts.filter(host => host.clusterId === cluster);
    return hosts.length ? [{ ...row, hosts }] : [];
  });
  if (tryZabbix && !zabbix) coverage.push("Zabbix：暂不可用");
  if (zabbix?.truncated) coverage.push("Zabbix：结果超过总览范围");
  if (source === "zabbix" && !canReadZabbix) coverage.push("Zabbix：当前权限无法读取");
  if (source === "zabbix" && zabbix && !zabbixSources.length) coverage.push("当前范围尚未接入 Zabbix 问题数据源");
  for (const item of zabbixSources) {
    if (item.health === "available") continue;
    const at = item.syncedAt && item.hasSnapshot ? ` · 上次成功同步 ${item.syncedAt}` : "";
    coverage.push(`Zabbix ${item.environmentName ?? item.scope.environmentId} / ${item.businessName ?? item.scope.businessId} · ${zabbixHealth[item.health] ?? item.health}${at}`);
  }
  const hasData = sources.some(([, state]) => state === "complete" || state === "partial") ||
    zabbixSources.some(item => item.hasSnapshot);
  const incomplete = sources.some(([, state]) => state === "partial" || state === "unavailable") ||
    (tryZabbix && !zabbix) || zabbix?.truncated === true || zabbixSources.some(item => item.health !== "available");
  const sourceScopes = zabbixCatalog?.sources ?? zabbix?.sources ?? [];
  const byCluster = new Map(catalog.map(item => [item.id, item]));
  const findingRows = finding.items.filter(item => {
    const metadata = byCluster.get(item.cluster);
    return (!environmentId || scopeValue(metadata?.environment, "environment", sourceScopes) === environmentId)
      && (!businessId || scopeValue(metadata?.business, "business", sourceScopes) === businessId);
  });
  const notice = coverage.length ? `<div class="overview-notice" role="status"><span>${escapeHtml(coverage.join("；"))}。${hasData && incomplete ? "仅统计已获取记录。" : ""}</span>
    ${incomplete ? '<button type="button" class="button secondary" data-action="retry">重试</button>' : ""}</div>` : "";
  const data = aggregateDiagnostics(incident.items, findingRows, severity, Date.now(), days, problems);
  return `<section class="unified-overview" aria-label="诊断概况">
    ${renderScope(catalog, query, severity, canReadZabbix, sourceScopes)}${notice}${renderMetrics(hasData ? data : undefined, query, severity)}
    ${hasData ? renderRecent(data.recent, query, canReadIncidents, incomplete) +
      `<div class="overview-analytics">${renderTrend(data, query, incomplete, tryZabbix && (zabbixSources.length > 0 || source === "zabbix"))}${renderDistribution(data, incomplete)}</div>`
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

function renderTrend(data: Overview, query: URLSearchParams, incomplete: boolean, showZabbix: boolean) {
  const days = data.daily.length;
  const total = data.daily.reduce((sum, { incident, finding, zabbix }) => sum + incident + finding + zabbix, 0);
  const max = Math.max(1, ...data.daily.map((day) => day.incident + day.finding + day.zabbix));
  const toggle = `<nav class="overview-segment" aria-label="趋势时间窗">${overviewWindows.map((windowDays) =>
    `<a href="${overviewHref(query, { trendDays: String(windowDays) })}"${windowDays === days ? ' aria-current="true"' : ""}>${windowDays} 天</a>`).join("")}</nav>`;
  const hint = incomplete ? "当前窗口未取得诊断记录。" : data.undated ? "已取得的带有效时间记录未落在当前时间窗。" :
    data.total ? "当前范围内有记录，本时间窗内没有首次发现的记录。" : "当前筛选范围没有匹配的诊断记录。";
  const emptyTitle = incomplete ? "数据覆盖不完整" : data.undated ? "未取得当前窗口的带时间记录" : `近 ${days} 天无新增`;
  return `<section class="panel overview-panel" aria-label="诊断趋势">
    <header class="overview-heading"><div><h2>诊断趋势</h2><p>${data.daily[0]?.date} 至 ${data.daily.at(-1)?.date}（UTC）</p></div>${toggle}</header>
    ${total ? `<div class="overview-plot" style="--overview-days:${days}">${data.daily.map(({ date, incident, finding, zabbix }, index) => {
      const label = `${date}（UTC）共 ${incident + finding + zabbix} 条，故障 ${incident} 条，K8sGPT ${finding} 条${showZabbix ? `，Zabbix ${zabbix} 条` : ""}`;
      const showDate = index === 0 || (index !== days - 2 && index % Math.ceil(days / 5) === 0) || index === days - 1;
      return `<button class="overview-trend-day" type="button" data-overview-point="${label}" aria-pressed="false" aria-label="${label}" title="${label}">
        <span class="overview-trend-bars" aria-hidden="true"><span style="height:${incident / max * 100}%"></span><span style="height:${finding / max * 100}%"></span>${showZabbix ? `<span class="zabbix-legend" style="height:${zabbix / max * 100}%"></span>` : ""}</span>
        <span class="overview-day-label" aria-hidden="true">${showDate ? date.slice(5) : ""}</span></button>`;
    }).join("")}</div>
    <p class="overview-trend-legend"><span>故障事件</span><span>K8sGPT</span>${showZabbix ? '<span class="zabbix-legend">Zabbix</span>' : ""}</p>
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
  for (const key of ["trendDays", "source", "recent"]) eventQuery.delete(key);
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
    const at = row.time === undefined ? "—" : `<time datetime="${escapeHtml(row.observed)}">${escapeHtml(observedTime.format(row.time))}</time>`;
    const level = `<span class="overview-badge overview-${row.severity}">${diagnosticLevels[row.severity]}</span>`;
    if (row.problem) return { ...renderZabbixProblem(row.problem), at, level };
    const params = new URLSearchParams({ cluster: row.cluster, from: "overview", return: query.toString() });
    const target = row.source === "incident" ? incidentHref(row.id, query.toString(), true) :
      href(`${diagnosticListPaths.findings}/${encodeURIComponent(row.id)}`, params);
    return {
      link: `<a href="${escapeHtml(target)}">${escapeHtml(row.title)}</a>`,
      level,
      status: row.status ? escapeHtml(incidentStatusLabels[row.status]) : "—",
      source: escapeHtml(row.source === "incident" ? `故障事件 · ${row.origin}` : row.origin),
      instance: escapeHtml(row.cluster || "—"),
      at,
    };
  });
  return `<section class="panel overview-records" aria-label="最近诊断"><header class="overview-heading overview-record-heading">
    <div><h2>最近诊断</h2><p>按首次发现时间倒序，最多 5 条 · UTC+8</p></div><div class="overview-actions">
    <a class="button secondary" href="${escapeHtml(href(diagnosticListPaths.events, canReadIncidents ? eventQuery : findingsQuery))}">查看故障事件</a>
    <a class="button secondary" href="${escapeHtml(href(diagnosticListPaths.findings, findingsQuery))}">查看原有诊断结果</a></div></header>
    ${items.length ? `<div class="overview-table-wrap unified-recent" tabindex="0" role="region" aria-label="最近诊断记录"><table>
      <thead><tr><th scope="col">问题</th><th scope="col">级别</th><th scope="col">处置状态</th><th scope="col">来源</th><th scope="col">主机或实例</th><th scope="col">首次发现</th></tr></thead>
      <tbody>${items.map((item) => `<tr><td>${item.link}</td><td>${item.level}</td><td>${item.status}</td><td>${item.source}</td><td>${item.instance}</td><td>${item.at}</td></tr>`).join("")}</tbody>
    </table></div><ul class="overview-record-cards" aria-label="最近诊断记录">${items.map((item) => `<li>${item.link}<div class="overview-card-details">
      ${item.level}<span>${item.source}</span><span>${item.instance}</span>${item.status === "—" ? "" : `<span>${item.status}</span>`}<span>${item.at}</span>
      </div></li>`).join("")}</ul>`
    : `<div class="overview-empty"><strong>${incomplete ? "当前范围尚未取得诊断记录" : "当前范围暂无诊断记录"}</strong><a href="${clearScope(query)}">清除筛选</a></div>`}
  </section>`;
}

function renderZabbixProblem(problem: NonNullable<Overview["recent"][number]["problem"]>) {
  const start = Date.parse(problem.startedAt);
  const end = problem.recoveredAt ? Date.parse(problem.recoveredAt) : Date.now();
  const duration = Number.isFinite(start) && Number.isFinite(end) ? ` · 持续 ${Math.max(0, Math.floor((end - start) / 60_000))} 分钟` : "";
  const labels = ["未分类", "信息", "警告", "一般", "严重", "灾难"];
  const tags = (problem.tags ?? []).map(({ tag, value }) => `${escapeHtml(tag)}＝${escapeHtml(value)}`).join("、");
  const originalURL = problem.originalURL && /^https?:\/\//i.test(problem.originalURL) ? problem.originalURL : undefined;
  const recovery = problem.recoveredAt ? Date.parse(problem.recoveredAt) : NaN;
  return {
    link: `<details><summary>${escapeHtml(problem.title)}</summary><p>事件编号 ${escapeHtml(problem.eventId)}</p>
      <p>Zabbix ${labels[problem.rawSeverity] ?? "未知级别"} · ${problem.acknowledged ? "已确认" : "未确认"}${duration}</p>
      ${Number.isFinite(recovery) ? `<p>恢复于 ${escapeHtml(observedTime.format(recovery))}</p>` : ""}
      ${tags ? `<p>${tags}</p>` : ""}
      ${originalURL ? `<a href="${escapeHtml(originalURL)}" target="_blank" rel="noopener noreferrer">查看 Zabbix 原始问题</a>` : ""}</details>`,
    status: problem.recovered ? "已恢复" : "未恢复",
    source: "Zabbix",
    instance: escapeHtml(problem.hosts.map(host => host.name).join("、") || "—"),
  };
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
