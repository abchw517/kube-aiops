import { bindHealthInvestigation } from "./health-investigation.ts";
import { createOverviewClient } from "../../api/client.ts";
import { ApiError, type Cluster, type Finding, type Incident, type Severity, type ZabbixHealth, type ZabbixProblems } from "../../api/generated.ts";
import { escapeHtml } from "../../components/html.ts";
import { diagnosticListPaths } from "../../domain/diagnostic-routes.ts";
import { aggregateDiagnostics, diagnosticLevels, incidentLevel, maxOverviewPageSize, overviewDays, overviewLevel, overviewPageSize, overviewPageSizes,
  overviewRecordPage, overviewSearch, type OverviewDays } from "../../domain/unified-diagnostics.ts";
import { incidentHref } from "./incident-filters.ts";
import { filterRecent, recentFilters, recentLevels, recentSources, recordDate, recordStatus, recordTags, recordTarget, type RecentRecord } from "./recent-records.ts";
import { bindOverviewTrend, renderOverviewTrend, type Trend } from "./unified-trend.ts";
import { zabbixHealth } from "../resources/datasource-rows.ts";

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
function overviewHref(query: URLSearchParams, changes: Record<string, string>): string {
  const next = new URLSearchParams(query);
  for (const [key, value] of Object.entries(changes)) {
    if (value) next.set(key, value);
    else next.delete(key);
  }
  return escapeHtml(href(diagnosticListPaths.overview, overviewSearch(next)));
}

function clearScope(query: URLSearchParams): string {
  return overviewHref(query, { clusterId: "", severity: "", recentPage: "", source: "", environmentId: "", businessId: "", recent: "", alertRecent: "" });
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
      ...(hasZabbix ? [["zabbix", "Zabbix"] as [string, string], ["alertmanager", "Alertmanager"] as [string, string]] : [])], query.get("source") ?? "")}</select></label>
    <label>环境<select name="environmentId">${scopeChoices("environment")}</select></label>
    <label>业务<select name="businessId">${scopeChoices("business")}</select></label>
      <label>集群实例<select name="clusterId">${choices([["", "全部可见实例"], ...entries], instance)}</select></label>
      <label>诊断级别<select name="severity">${choices([["", "全部级别"], ...levels], severity ?? "")}</select></label>
      ${hasZabbix ? `<label>Zabbix 状态<select name="recent">${choices([["", "当前记录"], ["true", "含最近恢复"]], query.get("recent") ?? "")}</select></label>` : ""}
      ${hasZabbix && query.get("source") === "alertmanager" ? `<label>告警状态<select name="alertRecent">${choices([["", "活动告警"], ["true", "包含非活动告警"]], query.get("alertRecent") ?? "")}</select></label>` : ""}
      <input type="hidden" name="trendDays" value="${overviewDays(query.get("trendDays"))}">
      <input type="hidden" name="recentPageSize" value="${query.get("recentPageSize") ?? overviewPageSizes[0]}">
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
  if (source === "alertmanager") return "Alertmanager";
  if (source === "zabbix") return "Zabbix";
  return hasZabbix ? "故障事件、K8sGPT、Zabbix 与 Alertmanager" : "故障事件与 K8sGPT";
}

const coverageLabels: Record<Coverage, string> = {
  complete: "", partial: "覆盖不完整", unavailable: "暂不可用", restricted: "当前权限无法读取",
};

export async function loadUnifiedOverview(hash: string, canReadIncidents: boolean, signal: AbortSignal, canReadZabbix = false, findingAI = false, reviewAccess = false) {
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
  const tryAlerts = canReadZabbix && (!source || source === "alertmanager");
  const tolerateZabbix = (error: unknown): ZabbixProblems | undefined => {
    if (error instanceof ApiError && error.status === 401) throw error;
    return undefined;
  };
  const [incident, finding, catalog, zabbix, zabbixCatalog, alerts] = await Promise.all([
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
    tryAlerts ? client.listObservabilityAlerts({ clusterId: cluster ?? undefined, environmentId, businessId,
      recent: source === "alertmanager" ? query.get("alertRecent") ?? undefined : undefined }).catch(error => {
        if (error instanceof ApiError && error.status === 401) throw error;
        return undefined;
      }) : undefined,
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
  const alertSources = alerts?.sources ?? [];
  for (const item of alertSources) {
    if (!item.syncedAt || item.failed) coverage.push(`Alertmanager ${item.name}：${item.failed ? "同步失败" : "等待首次同步"}${item.syncedAt ? ` · 上次成功同步 ${new Date(item.syncedAt * 1000).toLocaleString("zh-CN")}` : ""}`);
  }
  if (source === "alertmanager" && !alerts?.sources.length) coverage.push("当前范围暂无可用的 Alertmanager 数据源");
  if (alerts?.truncated) coverage.push("Alertmanager：结果超过总览范围");
  const alertCoverage = alertSources.flatMap(item => item.coverageFrom ? [item.coverageFrom] : []);
  const hasData = alertSources.some(item => item.syncedAt) || sources.some(([, state]) => state === "complete" || state === "partial") ||
    zabbixSources.some(item => item.hasSnapshot);
  const incomplete = Boolean(alerts?.truncated || alertSources.some(item => item.failed || !item.syncedAt) ||
      tryAlerts && !alerts) || sources.some(([, state]) => state === "partial" || state === "unavailable") ||
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
  const data = aggregateDiagnostics(incident.items, findingRows, severity, now, 30, problems, alerts?.items);
  const trend: Trend = { data, now, incomplete, available: hasData, sources: [] };
  if (incident.coverage === "complete" || incident.coverage === "partial") trend.sources.push("incident");
  if (finding.coverage === "complete" || finding.coverage === "partial") trend.sources.push("finding");
  if (problems.length || zabbixSources.some(item => item.hasSnapshot)) trend.sources.push("zabbix");
  if (alertSources.some(item => item.syncedAt)) trend.sources.push("alertmanager");
  const counts = hasData ? data : undefined;
  const renderWindow = (days: OverviewDays) => renderOverviewTrend(trend, days,
    window => overviewHref(query, { trendDays: String(window) })) + (alertCoverage.length
    ? `<p class="overview-chart-note">Alertmanager 从 ${new Date(Math.min(...alertCoverage) * 1000).toLocaleString("zh-CN")} 起有平台采集记录。</p>` : "");
  const canReview = (row: RecentRecord) => row.cluster === "local" ? findingAI : reviewAccess && byCluster.get(row.cluster)?.reviewEnabled === true;
  const recentTotal = () => filterRecent(data.records, query).length;
  const renderRecords = () => renderRecent(data.records, query, canReadIncidents, incomplete, hasData, canReview);
  const html = `<section class="unified-overview" aria-label="诊断概况">
    ${renderScope(catalog, query, severity, canReadZabbix, sourceScopes)}${notice}${renderMetrics(counts, query, severity)}
    <div class="overview-analytics">
      <section class="panel overview-trend" data-overview-trend aria-labelledby="overview-trend-title">${renderWindow(days)}</section>
      ${renderDistribution(counts, query, severity, incomplete)}
    </div>
    <div data-overview-record-page>${renderRecords()}</div>
  </section>`;
  return { html, bind: (root: HTMLElement, lifetime: AbortSignal) => {
    bindUnifiedOverview(root, trend, query, renderWindow, renderRecords, recentTotal);
    const section = root.querySelector<HTMLElement>(".unified-overview");
    if (section) bindHealthInvestigation(section, data.records, lifetime, query, canReview);
  } };
}

function severityHref(query: URLSearchParams, level: Severity | "", severity: Severity | undefined): string {
  return overviewHref(query, { severity: severity === level ? "" : level, recentPage: "" });
}

function renderMetrics(data: Overview | undefined, query: URLSearchParams, severity: Severity | undefined) {
  const metrics: [Severity | "", string, number | undefined][] = [["", "诊断记录总数", data?.total],
    ...levels.map(([level, label]): [Severity, string, number | undefined] => [level, label, data?.counts[level]])];
  return `<div class="overview-metrics" aria-label="诊断指标">${metrics.map(([level, label, count]) => {
    const classes = `panel overview-metric metric-${level || "total"}`;
    const icon = level ? { critical: "!", warning: "△", info: "i" }[level] : "▤";
    const body = `<span class="overview-metric-label"><span aria-hidden="true">${icon}</span>${label}</span><strong>${count ?? "—"}</strong>`;
    return data ? `<a class="${classes}" href="${severityHref(query, level, severity)}"
      ${(severity ?? "") === level ? 'aria-current="true"' : ""}>${body}</a>` : `<div class="${classes}">${body}</div>`;
  }).join("")}</div>`;
}

function renderDistribution(data: Overview | undefined, query: URLSearchParams, severity: Severity | undefined, incomplete: boolean) {
  const clearLevel = severity ? `<a href="${severityHref(query, severity, severity)}">查看全部级别</a>` : "";
  const percent = (count: number) => data?.total ? count / data.total * 100 : 0;
  const critical = data?.counts.critical ?? 0;
  const warnings = critical + (data?.counts.warning ?? 0);
  const mapped = warnings + (data?.counts.info ?? 0);
  return `<section class="panel overview-distribution-panel" aria-labelledby="overview-distribution-title">
    <header class="overview-heading"><div><h2 id="overview-distribution-title">严重度分布</h2>
    <p>${data ? `${incomplete ? "已获取记录" : "当前筛选范围"} · ${data.total} 条` : "暂不可用"}</p></div></header>
    <div class="overview-distribution-body">
    ${data ? `<div class="overview-ring" aria-hidden="true" data-overview-ring="${percent(critical)} ${percent(warnings)} ${percent(mapped)}"><div><strong>${data.total}</strong><small>诊断记录</small></div></div>` : ""}
    <ul class="overview-distribution">${levels.map(([level, label]) => {
      const count = data?.counts[level];
      const row = `<span class="overview-distribution-row"><span class="overview-distribution-label">${label}</span>
        <span><strong>${count ?? "—"}</strong><small>${data?.total ? `${percent(count ?? 0).toFixed(1)}%` : "—"}</small></span></span>`;
      return `<li class="overview-${level}">${data ? `<a href="${severityHref(query, level, severity)}"
        ${severity === level ? 'aria-current="true"' : ""}>${row}</a>` : row}</li>`;
    }).join("")}</ul></div>
    ${data && data.total > mapped ? `<p class="overview-chart-note">未映射级别 · ${data.total - mapped} 条</p>` : ""}
    ${data ? `<p class="overview-distribution-foot"><span>点击级别筛选总览</span>${clearLevel}</p>` : ""}
  </section>`;
}

function renderRecent(rows: Overview["records"], query: URLSearchParams, canReadIncidents: boolean, incomplete: boolean, available: boolean,
  canReview: (row: RecentRecord) => boolean) {
  const eventQuery = new URLSearchParams(query);
  for (const key of ["trendDays", "source", "recent", "alertRecent", "recentPage", "recentPageSize", ...recentFilters]) eventQuery.delete(key);
  const findingsQuery = new URLSearchParams();
  const cluster = query.get("clusterId");
  const severity = overviewLevel(query.get("severity"));
  if (cluster) findingsQuery.set("cluster", cluster);
  if (severity) {
    findingsQuery.set("severity", severity);
    const entry = Object.entries(incidentLevel).find(([, level]) => level === severity);
    if (entry) eventQuery.set("severity", entry[0]);
  }
  const filtered = filterRecent(rows, query);
  const { page, pages, size, start, end } = overviewRecordPage(query, filtered.length);
  const select = (key: string, label: string, choices: Record<string,string>) =>
    `<select data-recent-filter="${key}" aria-label="${label}"><option value="">全部${label}</option>${Object.entries(choices).map(([value, text]) =>
      `<option value="${escapeHtml(value)}"${query.get(key) === value ? " selected" : ""}>${escapeHtml(text)}</option>`).join("")}</select>`;
  const results = filtered.slice(start, end).map(({row,index}) => {
    const date = recordDate(row).split(" ");
    const origin = recentSources[row.source];
    const full = row.source === "incident" ? incidentHref(row.id, query.toString(), true) : row.source === "finding" ?
      href(`${diagnosticListPaths.findings}/${encodeURIComponent(row.id)}`, new URLSearchParams({cluster:row.cluster,from:"overview",return:query.toString()})) : "";
    const title = full ? `<a href="${escapeHtml(full)}" data-overview-return class="problem" data-recent-record="${index}">${escapeHtml(row.title)}</a>` :
      `<button type="button" class="problem" data-recent-record="${index}" aria-label="${escapeHtml(row.title)}，${escapeHtml(recordTarget(row))}">${escapeHtml(row.title)}</button>`;
    const ai = row.source === "finding" ? "AI 复核" : row.source === "incident" ? "事件 RCA" : "AI 调查";
    return `<div class="record" role="listitem">
      <div>${title}<div class="chips">${recordTags(row)}</div></div>
      <div><span class="origin">${origin}</span><small>${escapeHtml(recordTarget(row))}</small></div>
      <div class="time"><time datetime="${escapeHtml(row.observed)}">${escapeHtml(date[0])}<small>${escapeHtml(date[1])}</small></time></div>
      <div class="actions"><button type="button" class="btn" data-recent-record="${index}">查看详情</button>
        ${row.source !== "finding" || canReview(row) ? `<button type="button" class="btn ai" data-recent-record="${index}" data-recent-ai>${ai}</button>` : ""}</div>
    </div>`;
  }).join("");
  return `<section class="overview-records recent-workspace" aria-labelledby="overview-recent-title">
    <header class="head overview-record-heading row between"><div><h2 id="overview-recent-title" tabindex="-1">最近诊断</h2><p class="subtitle">按首次发现时间倒序 · UTC+8</p></div><div class="actions overview-actions">
      <a class="btn" href="${escapeHtml(href(diagnosticListPaths.events, canReadIncidents ? eventQuery : findingsQuery))}">查看故障事件</a>
      <a class="btn" href="${escapeHtml(href(diagnosticListPaths.findings, findingsQuery))}">查看原有诊断结果</a></div></header>
    <div class="hero row between"><div><strong>先核对告警与证据，再发起 AI 调查</strong><p>选择记录查看原始问题、所属范围和关联证据。</p></div>
      <div class="row" data-recent-count aria-live="polite">${recentCount(filtered, available, incomplete)}</div></div>
    <section class="panel recent-list" aria-label="最近诊断记录">
      <div class="filters"><input type="search" data-recent-filter="recentText" aria-label="搜索诊断" placeholder="搜索问题、主机或实例" maxlength="120" value="${escapeHtml(query.get("recentText"))}">
        ${select("recentSource","来源",recentSources)}${select("recentLevel","级别",recentLevels)}
        ${select("recentStatus","状态",Object.fromEntries([...new Set(rows.map(recordStatus))].map(label => [label,label])))}
        <button class="btn quiet reset" type="button" data-recent-clear>清除筛选</button></div>
      <div data-recent-items>${results ? `<div class="table-head" aria-hidden="true"><span>问题 / 级别与状态</span><span>来源 / 主机或实例</span><span>首次发现</span><span>操作</span></div>
        <div class="unified-recent" role="list" aria-label="按首次发现倒序的诊断记录">${results}</div>` : `<div class="empty"><p>${!available ? "最近诊断暂不可用" : rows.length ? "没有符合当前筛选的诊断记录。" : incomplete ? "当前范围尚未取得诊断记录" : "当前范围暂无诊断记录"}</p>
        ${available && rows.length ? '<button class="btn" type="button" data-recent-clear>清除筛选</button>' : available ? `<a href="${clearScope(query)}">清除总览筛选</a>` : ""}</div>`}</div>
      <div data-recent-pager>${results ? renderRecordPager(page, pages, size, start, end, filtered.length, incomplete) : ""}</div>
    </section></section>`;
}

function recentCount(filtered: ReturnType<typeof filterRecent>, available: boolean, incomplete: boolean) {
  return available ? `<span class="summary-num"><b>${filtered.length}</b> 条${incomplete ? "已获取" : ""}记录</span>
    <span class="summary-num"><b>${filtered.filter(({row}) => row.severity === "critical").length}</b> 条严重</span>` : '<span class="summary-num">数据暂不可用</span>';
}

function renderRecordPager(page: number, pages: number, size: number, start: number, end: number, total: number, incomplete: boolean): string {
  const choices = [...new Set<number>([...overviewPageSizes, size])].sort((a, b) => a - b);
  return `<footer class="pager row between">
    <span class="muted">显示第 ${start + 1}–${end} 条 · ${incomplete ? "已获取" : "共"} ${total} 条</span>
    <nav class="audit-pagination" aria-label="最近诊断分页">
      <button class="btn" type="button" aria-label="上一页" data-overview-page="${page - 1}"${page <= 1 ? " disabled" : ""}>上一页</button>
      <span class="audit-page-number">第 ${page} / ${pages} 页</span>
      <button class="btn" type="button" aria-label="下一页" data-overview-page="${page + 1}"${page >= pages ? " disabled" : ""}>下一页</button>
      <select data-overview-limit aria-label="每页条数" aria-controls="overview-custom-limit">${choices.map(count =>
        `<option value="${count}"${count === size ? " selected" : ""}>${count} 条/页</option>`).join("")}
        <option value="custom">自定义…</option></select>
    </nav>
    <form class="audit-limit-editor" id="overview-custom-limit" hidden novalidate>
      <label>自定义每页条数<input name="limit" type="number" min="1" max="${maxOverviewPageSize}" step="1" required
        aria-describedby="overview-limit-hint overview-limit-error"></label>
      <small id="overview-limit-hint">1–${maxOverviewPageSize} 条</small>
      <button class="button" type="submit">应用</button>
      <button class="button secondary" type="button" data-overview-cancel-limit>取消</button>
      <p class="overview-limit-error" id="overview-limit-error" role="alert"></p>
    </form>
  </footer>`;
}

function bindUnifiedOverview(root: HTMLElement, trend: Trend, query: URLSearchParams,
  renderWindow: (days: OverviewDays) => string, renderRecords: () => string, recentTotal: () => number): void {
  const form = root.querySelector<HTMLFormElement>("[data-unified-filters]");
  const chart = root.querySelector<HTMLElement>("[data-overview-trend]");
  const overview = root.querySelector<HTMLElement>(".unified-overview");
  if (!form || !chart || !overview) return;
  const ring = overview.querySelector<HTMLElement>("[data-overview-ring]");
  if (ring) {
    // Set the stops through CSSOM so they also render under the Portal CSP.
    const [critical, warning, mapped] = (ring.dataset.overviewRing ?? "0 0 0").split(" ");
    ring.style.setProperty("--overview-ring-critical", `${critical}%`);
    ring.style.setProperty("--overview-ring-warning", `${warning}%`);
    ring.style.setProperty("--overview-ring-mapped", `${mapped}%`);
  }
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
  bindOverviewRecords(overview, recentTotal, query, renderRecords);
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

function bindOverviewRecords(root: HTMLElement, total: () => number, query: URLSearchParams, renderRecords: () => string): void {
  const section = root.querySelector<HTMLElement>("[data-overview-record-page]");
  if (!section) return;
  const current = () => overviewRecordPage(query, total());
  const setPage = (page: number) => {
    if (page > 1) query.set("recentPage", String(page));
    else query.delete("recentPage");
  };
  const address = () => href(diagnosticListPaths.overview, overviewSearch(query));
  if (Number(query.get("recentPage") ?? 1) !== current().page) {
    setPage(current().page);
    history.replaceState(history.state, "", address());
  }

  function showPage(page: number, size: number, focus: string) {
    setPage(page);
    if (size === overviewPageSizes[0]) query.delete("recentPageSize");
    else query.set("recentPageSize", String(size));
    const nextAddress = address();
    if (window.location.hash !== nextAddress) history.pushState(history.state, "", nextAddress);
    const field = root.querySelector<HTMLInputElement>('[name="recentPageSize"]');
    if (field) field.value = String(size);
    for (const link of root.querySelectorAll<HTMLAnchorElement>('a[href^="#/diagnostics"]')) {
      const [path, raw] = link.getAttribute("href")?.split("?", 2) ?? [];
      if (path !== diagnosticListPaths.overview) continue;
      const target = new URLSearchParams(raw);
      if (link.hasAttribute("data-overview-window") && page > 1) target.set("recentPage", String(page));
      else target.delete("recentPage");
      target.set("recentPageSize", String(size));
      link.setAttribute("href", href(path, overviewSearch(target)));
    }
    if (!section) return;
    section.innerHTML = renderRecords();
    const control = section.querySelector<HTMLElement>(focus) ??
      section.querySelector<HTMLElement>("[data-overview-page]:not(:disabled)") ??
      section.querySelector<HTMLElement>("#overview-recent-title");
    control?.focus({ preventScroll: true });
  }

  const cancelLimit = () => {
    const select = section.querySelector<HTMLSelectElement>("[data-overview-limit]");
    const form = section.querySelector<HTMLFormElement>("#overview-custom-limit");
    if (form) form.hidden = true;
    if (select) {
      select.value = String(current().size);
      select.focus();
    }
  };
  function refreshFiltered() {
    setPage(1);
    history.replaceState(history.state, "", address());
    const template = document.createElement("template");
    template.innerHTML = renderRecords();
    for (const selector of ["[data-recent-count]", "[data-recent-items]", "[data-recent-pager]"]) {
      const target = section?.querySelector(selector);
      const update = template.content.querySelector(selector);
      if (target && update) target.replaceWith(update);
    }
  }

  function setFilter(input: HTMLInputElement | HTMLSelectElement) {
    const key = recentFilters.find(field => field === input.dataset.recentFilter);
    if (!key) return;
    if (input.value) query.set(key, input.value);
    else query.delete(key);
    refreshFiltered();
  }
  section.addEventListener("input", event => {
    if (event.target instanceof HTMLInputElement && event.target.hasAttribute("data-recent-filter")) setFilter(event.target);
  });
  section.addEventListener("change", event => {
    const select = event.target;
    if (!(select instanceof HTMLSelectElement)) return;
    if (select.hasAttribute("data-recent-filter")) { setFilter(select); return; }
    if (!select.hasAttribute("data-overview-limit")) return;
    const size = overviewPageSize(select.value);
    if (size) {
      showPage(1, size, "[data-overview-limit]");
      return;
    }
    const form = section.querySelector<HTMLFormElement>("#overview-custom-limit");
    const input = form?.querySelector<HTMLInputElement>('[name="limit"]');
    if (!form || !input) return;
    form.hidden = false;
    input.value = String(current().size);
    input.removeAttribute("aria-invalid");
    const error = form.querySelector("#overview-limit-error");
    if (error) error.textContent = "";
    input.select();
    input.focus();
  });
  section.addEventListener("click", event => {
    if (!(event.target instanceof HTMLButtonElement)) return;
    if (event.target.hasAttribute("data-recent-clear")) {
      for (const key of recentFilters) query.delete(key);
      for (const input of section.querySelectorAll<HTMLInputElement | HTMLSelectElement>("[data-recent-filter]")) input.value = "";
      refreshFiltered();
      section.querySelector<HTMLElement>('[data-recent-filter="recentText"]')?.focus();
      return;
    }
    if (event.target.hasAttribute("data-overview-cancel-limit")) cancelLimit();
    const page = Number(event.target.dataset.overviewPage);
    if (page >= 1 && page <= current().pages && !event.target.disabled) {
      const target = page + (page > current().page ? 1 : -1);
      showPage(page, current().size, `[data-overview-page="${target}"]:not(:disabled)`);
    }
  });
  section.addEventListener("keydown", event => {
    if (event.key === "Escape" && event.target instanceof Element && event.target.closest("#overview-custom-limit")) {
      event.preventDefault();
      cancelLimit();
    }
  });
  section.addEventListener("submit", event => {
    if (!(event.target instanceof HTMLFormElement) || event.target.id !== "overview-custom-limit") return;
    event.preventDefault();
    const input = event.target.querySelector<HTMLInputElement>('[name="limit"]');
    const size = input && overviewPageSize(input.value);
    if (size) showPage(1, size, "[data-overview-limit]");
    else {
      const error = event.target.querySelector("#overview-limit-error");
      if (error) error.textContent = `请输入 1–${maxOverviewPageSize} 之间的整数。`;
      input?.setAttribute("aria-invalid", "true");
      input?.focus();
    }
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
