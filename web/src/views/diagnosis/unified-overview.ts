import { createOverviewClient } from "../../api/client.ts";
import { ApiError, type Cluster, type Finding, type Incident, type Severity, type ZabbixHealth, type ZabbixProblems } from "../../api/generated.ts";
import { escapeHtml } from "../../components/html.ts";
import { diagnosticListPaths } from "../../domain/diagnostic-routes.ts";
import { aggregateDiagnostics, diagnosticLevels, incidentLevel, overviewDays, overviewLevel, overviewSearch, type OverviewDays } from "../../domain/unified-diagnostics.ts";
import { incidentHref, incidentStatusLabels } from "./incident-filters.ts";
import { bindOverviewTrend, renderOverviewTrend, type Trend } from "./unified-trend.ts";
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
      <div class="overview-filter-actions"><button class="button" type="submit">应用筛选</button>
      <a class="overview-text-link" href="${clearScope(query)}">清除筛选</a></div>
    </form>
    <p class="overview-current-scope">当前范围 <strong>${escapeHtml(scopeLabel)} · ${severity ? diagnosticLevels[severity] : "全部级别"}</strong> <span>${sourceLabel(query.get("source"), hasZabbix)}</span>
      <button type="button" class="overview-jump" data-overview-jump>跳到最近诊断 ↓</button></p>
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
  if (source === "zabbix" && zabbix && !zabbixSources.length) coverage.push("当前范围尚未接入 Zabbix 数据源");
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
  const now = Date.now();
  const data = aggregateDiagnostics(incident.items, findingRows, severity, now, 30, problems);
  const trend: Trend = { data, now, incomplete, available: hasData, sources: [] };
  if (incident.coverage === "complete" || incident.coverage === "partial") trend.sources.push("incident");
  if (finding.coverage === "complete" || finding.coverage === "partial") trend.sources.push("finding");
  if (problems.length || zabbixSources.some(item => item.hasSnapshot)) trend.sources.push("zabbix");
  const counts = hasData ? data : undefined;
  const renderWindow = (days: OverviewDays) => renderOverviewTrend(trend, days,
    window => overviewHref(query, { trendDays: String(window) }));
  const html = `<section class="unified-overview" aria-label="诊断概况">
    ${renderScope(catalog, query, severity, canReadZabbix, sourceScopes)}${notice}${renderMetrics(counts, query, severity)}
    <div class="panel overview-analytics">
      <section class="overview-trend" data-overview-trend aria-labelledby="overview-trend-title">${renderWindow(days)}</section>
      ${renderDistribution(counts, query, severity, incomplete)}
    </div>
    ${renderRecent(data.recent, query, canReadIncidents, incomplete, hasData)}
  </section>`;
  return { html, bind: (root: HTMLElement) => bindUnifiedOverview(root, trend, query, renderWindow) };
}

function severityHref(query: URLSearchParams, level: Severity | "", severity: Severity | undefined): string {
  return overviewHref(query, { severity: severity === level ? "" : level });
}

function renderMetrics(data: Overview | undefined, query: URLSearchParams, severity: Severity | undefined) {
  const metrics: [Severity | "", string, number | undefined][] = [["", "诊断记录总数", data?.total],
    ...levels.map(([level, label]): [Severity, string, number | undefined] => [level, label, data?.counts[level]])];
  return `<div class="overview-metrics" aria-label="诊断指标">${metrics.map(([level, label, count]) => {
    const classes = `panel overview-metric metric-${level || "total"}`;
    return data ? `<a class="${classes}" href="${severityHref(query, level, severity)}"
      ${(severity ?? "") === level ? 'aria-current="true"' : ""}><span>${label}</span><strong>${count}</strong></a>` :
      `<div class="${classes}"><span>${label}</span><strong>—</strong></div>`;
  }).join("")}</div>`;
}

function renderDistribution(data: Overview | undefined, query: URLSearchParams, severity: Severity | undefined, incomplete: boolean) {
  const clearLevel = severity ? `<a href="${severityHref(query, severity, severity)}">查看全部级别</a>` : "";
  return `<section class="overview-distribution-panel" aria-labelledby="overview-distribution-title">
    <header class="overview-heading"><div><h2 id="overview-distribution-title">严重度分布</h2>
    <p>${data ? `${incomplete ? "已获取记录" : "当前筛选范围"} · ${data.total} 条` : "暂不可用"}</p></div></header>
    <ul class="overview-distribution">${levels.map(([level, label]) => {
      const count = data?.counts[level];
      const percent = data?.total ? (count ?? 0) / data.total * 100 : 0;
      const row = `<span class="overview-distribution-row"><span>${label}</span>
        <span><strong>${count ?? "—"}</strong><small>${data?.total ? `${percent.toFixed(1)}%` : "—"}</small></span></span>
        <span class="overview-progress" aria-hidden="true"><span style="width:${percent}%"></span></span>`;
      return `<li class="overview-${level}">${data ? `<a href="${severityHref(query, level, severity)}"
        ${severity === level ? 'aria-current="true"' : ""}>${row}</a>` : row}</li>`;
    }).join("")}</ul>
    ${data ? `<p class="overview-distribution-foot"><span>点击级别筛选总览</span>${clearLevel}</p>` : ""}
  </section>`;
}

function renderRecent(rows: Overview["recent"], query: URLSearchParams, canReadIncidents: boolean, incomplete: boolean, available: boolean) {
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
      link: `<a data-overview-return href="${escapeHtml(target)}">${escapeHtml(row.title)}</a>`,
      level,
      status: row.status ? escapeHtml(incidentStatusLabels[row.status]) : "—",
      source: escapeHtml(row.source === "incident" ? `故障事件 · ${row.origin}` : row.origin),
      instance: escapeHtml(row.cluster || "—"),
      at,
    };
  });
  return `<section class="panel overview-records" aria-labelledby="overview-recent-title"><header class="overview-heading overview-record-heading">
    <div><h2 id="overview-recent-title" tabindex="-1">最近诊断</h2><p>按首次发现时间倒序，最多 5 条 · UTC+8</p></div><div class="overview-actions">
    <a class="button secondary" href="${escapeHtml(href(diagnosticListPaths.events, canReadIncidents ? eventQuery : findingsQuery))}">查看故障事件</a>
    <a class="button secondary" href="${escapeHtml(href(diagnosticListPaths.findings, findingsQuery))}">查看原有诊断结果</a></div></header>
    ${items.length ? `<div class="overview-table-wrap unified-recent" tabindex="0" role="region" aria-label="最近诊断记录"><table>
      <thead><tr><th scope="col">问题</th><th scope="col">级别</th><th scope="col">处置状态</th><th scope="col">来源</th><th scope="col">主机或实例</th><th scope="col">首次发现</th></tr></thead>
      <tbody>${items.map((item) => `<tr><td>${item.link}</td><td>${item.level}</td><td>${item.status}</td><td>${item.source}</td><td>${item.instance}</td><td>${item.at}</td></tr>`).join("")}</tbody>
    </table></div><ul class="overview-record-cards" aria-label="最近诊断记录">${items.map((item) => `<li>${item.link}<div class="overview-card-details">
      ${item.level}<span>${item.source}</span><span>${item.instance}</span>${item.status === "—" ? "" : `<span>${item.status}</span>`}<span>${item.at}</span>
      </div></li>`).join("")}</ul>`
    : `<div class="overview-empty"><strong>${!available ? "最近诊断暂不可用" : incomplete ? "当前范围尚未取得诊断记录" : "当前范围暂无诊断记录"}</strong>${available ? `<a href="${clearScope(query)}">清除筛选</a>` : ""}</div>`}
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

function bindUnifiedOverview(root: HTMLElement, trend: Trend, query: URLSearchParams,
  renderWindow: (days: OverviewDays) => string): void {
  const form = root.querySelector<HTMLFormElement>("[data-unified-filters]");
  const chart = root.querySelector<HTMLElement>("[data-overview-trend]");
  const overview = root.querySelector<HTMLElement>(".unified-overview");
  if (!form || !chart || !overview) return;
  form.addEventListener("submit", (event) => {
    event.preventDefault();
    const next = new URLSearchParams();
    for (const [key, value] of new FormData(form)) {
      if (typeof value === "string" && value) next.set(key, value);
    }
    window.location.hash = href(diagnosticListPaths.overview, overviewSearch(next));
  });
  root.querySelector("[data-overview-jump]")?.addEventListener("click", () => {
    const heading = root.querySelector<HTMLElement>("#overview-recent-title");
    heading?.focus({ preventScroll: true });
    heading?.scrollIntoView({ block: "start" });
  });
  bindOverviewTrend(chart, trend, overviewDays(query.get("trendDays")));
  chart.addEventListener("click", (event) => {
    if (!(event.target instanceof Element) || event.button || event.ctrlKey || event.metaKey || event.shiftKey || event.altKey) return;
    const link = event.target.closest<HTMLAnchorElement>("[data-overview-window]");
    if (!link) return;
    event.preventDefault();
    const days = overviewDays(link.dataset.overviewWindow ?? null);
    if (days === overviewDays(query.get("trendDays"))) return;
    setTrendWindow(query, days);
    history.pushState(history.state, "", href(diagnosticListPaths.overview, overviewSearch(query)));
    updateWindowLinks(overview, days);
    chart.innerHTML = renderWindow(days);
    bindOverviewTrend(chart, trend, days);
    chart.querySelector<HTMLElement>(`[data-overview-window="${days}"]`)?.focus({ preventScroll: true });
  });
}

function setTrendWindow(query: URLSearchParams, days: OverviewDays): void {
  if (days === 7) query.delete("trendDays");
  else query.set("trendDays", String(days));
}

function updateWindowLinks(root: HTMLElement, days: OverviewDays): void {
  const field = root.querySelector<HTMLInputElement>('[name="trendDays"]');
  if (field) field.value = String(days);
  for (const link of root.querySelectorAll<HTMLAnchorElement>('a[href^="#"]')) {
    const [path, rawQuery] = (link.getAttribute("href") ?? "").split("?", 2);
    if (!path) continue;
    const query = new URLSearchParams(rawQuery);
    if (path === diagnosticListPaths.overview) {
      setTrendWindow(query, days);
    } else if (link.hasAttribute("data-overview-return")) {
      const returnQuery = new URLSearchParams(query.get("return") ?? "");
      setTrendWindow(returnQuery, days);
      query.set("return", overviewSearch(returnQuery).toString());
    } else {
      continue;
    }
    link.setAttribute("href", href(path, query));
  }
}
