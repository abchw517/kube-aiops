import type {
  Cluster,
  Finding,
  FindingPage,
  FindingSummary,
  Namespace,
  Severity,
} from "../../api/generated.ts";
import { escapeHtml, formatSeverity, formatTimestamp } from "../../components/html.ts";
import { emptyState } from "../../components/states.ts";
import {
  allClusters,
  displayFindingScope,
  type FindingFilters,
  scopeValue,
  serializeFindingFilters,
} from "../../domain/filters.ts";
import { renderSeverityHelp } from "./severity-help.ts";

interface FindingWorkspace {
  clusters: Cluster[];
  namespaces: Namespace[];
  knownKinds: string[];
  filters: FindingFilters;
  page: FindingPage;
  summary: FindingSummary;
  cursor?: string;
}

/** 组织诊断列表、筛选、汇总和沿用当前筛选条件的分页链接。 */
export function renderFindingWorkspace(workspace: FindingWorkspace): string {
  const { clusters, namespaces, knownKinds, filters, page, summary, cursor } = workspace;
  const query = serializeFindingFilters(filters);
  const nextCursor = page.pagination.continue;
  const nextQuery = new URLSearchParams(query);
  if (nextCursor) nextQuery.set("continue", nextCursor);
  const returnQuery = new URLSearchParams(query);
  if (cursor) returnQuery.set("continue", cursor);
  return `
    ${renderFilters(clusters, namespaces, knownKinds, filters)}
    ${renderSummary(summary)}
    <div class="finding-workspace">
      <div class="finding-results">
        ${page.items.length === 0 ? emptyState() : renderFindingTable(page.items, summary.total, clusters, returnQuery.toString())}
        ${cursor || nextCursor ? `<nav class="pagination" aria-label="诊断结果分页">
          ${cursor ? `<a class="button secondary" href="#/findings?${escapeHtml(query)}">返回首页</a>` : ""}
          ${nextCursor ? `<a class="button" href="#/findings?${escapeHtml(nextQuery.toString())}">下一页</a>` : ""}
        </nav>` : ""}
      </div>
      ${renderSummaryBreakdown(summary)}
    </div>`;
}

/** 将集群范围和结果条件分组，沿用原有筛选控件。 */
function renderFilters(
  clusters: Cluster[],
  namespaces: Namespace[],
  knownKinds: string[],
  filters: FindingFilters,
): string {
  const instance = clusters.find((item) => item.id === filters.cluster);
  const freshness = instance?.status === "waiting" ? "采集端尚未上报。" :
    instance?.status === "offline" && instance.lastReportAt ?
      `采集离线，显示最近一次上报的数据（${new Date(instance.lastReportAt * 1000).toLocaleString()}）。` : "";
  return `
    <section class="panel filters" aria-label="诊断结果筛选">
      <fieldset>
        <legend>集群范围</legend>
        ${renderClusterSelect(clusters, filters)}
      </fieldset>
      <fieldset>
        <legend>结果筛选</legend>
        ${renderSelect("namespace", "命名空间", namespaces.map((item) => item.name), filters.namespace)}
        ${renderSelect("severity", "严重级别", ["critical", "warning", "info"] satisfies Severity[], filters.severity, formatSeverity)}
        ${renderSelect("kind", "资源类型", knownKinds, filters.kind)}
        <button class="button secondary" type="button" data-action="clear-filters">清除筛选</button>
      </fieldset>
      ${freshness ? `<p class="evidence-notice" role="status">${escapeHtml(freshness)}</p>` : ""}
    </section>`;
}

/** 展示当前筛选范围内的总数与严重级别计数。 */
function renderSummary(summary: FindingSummary): string {
  const severity = summary.bySeverity;
  return `
    <section class="summary-grid" aria-label="诊断结果汇总" data-testid="finding-summary">
      ${metricCard(summary.total, "total")}
      ${metricCard(severity.critical ?? 0, "critical")}
      ${metricCard(severity.warning ?? 0, "warning")}
      ${metricCard(severity.info ?? 0, "info")}
    </section>`;
}

/** 展示与诊断表格对应的两个统计维度。 */
function renderSummaryBreakdown(summary: FindingSummary): string {
  return `
    <aside class="summary-breakdown panel" aria-label="诊断结果分布">
      <section>
        <h2>按资源类型</h2>
        ${renderBreakdown(summary.byKind)}
      </section>
      <section>
        <h2>按命名空间</h2>
        ${renderBreakdown(summary.byNamespace)}
      </section>
    </aside>`;
}

/** 渲染可通过键盘横向滚动的诊断结果表格。 */
function renderFindingTable(findings: Finding[], total: number, clusters: Cluster[], returnQuery: string): string {
  const clusterNames = new Map(clusters.map(({ id, name }) => [id, name]));
  return `
    <section class="panel table-panel" data-testid="finding-list">
      <div class="section-heading">
        <div>
          <h2 id="finding-results-heading">共 ${total} 条诊断结果${findings.length !== total ? `，本页 ${findings.length} 条` : ""}</h2>
        </div>
      </div>
      <div class="table-wrap" role="region" aria-labelledby="finding-results-heading" tabindex="0">
        <table>
          <thead>
            <tr>
              <th scope="col">严重级别</th>
              <th scope="col">问题</th>
              <th scope="col">资源</th>
              <th scope="col">集群实例</th>
              <th scope="col">命名空间</th>
              <th scope="col">发现时间</th>
            </tr>
          </thead>
          <tbody>
            ${findings.map((finding) => renderFindingRow(finding, clusterNames, returnQuery)).join("")}
          </tbody>
        </table>
      </div>
    </section>`;
}

function renderFindingRow(finding: Finding, clusterNames: Map<string, string>, returnQuery: string): string {
  const resource = [finding.resource.kind, finding.resource.name]
    .filter(Boolean)
    .join("/");
  const detailQuery = new URLSearchParams({ cluster: finding.cluster, return: returnQuery });
  return `
    <tr>
      <td><span class="severity severity-${escapeHtml(finding.severity)}">${escapeHtml(formatSeverity(finding.severity))}</span></td>
      <td class="finding-problem"><a href="#/findings/${encodeURIComponent(finding.id)}?${escapeHtml(detailQuery.toString())}" title="${escapeHtml(finding.problem)}">${escapeHtml(finding.problem)}</a></td>
      <td translate="no">${escapeHtml(resource || "—")}</td>
      <td class="finding-cluster" translate="no">${escapeHtml(clusterNames.get(finding.cluster) ?? finding.cluster)}</td>
      <td translate="no">${escapeHtml(finding.namespace ?? finding.resource.namespace ?? "—")}</td>
      <td class="finding-time">${formatTimestamp(finding.createdAt)}</td>
    </tr>`;
}

function renderSelect<Value extends string>(
  name: string,
  label: string,
  values: Value[],
  selected?: string,
  formatValue: (value: Value) => string = (value) => value,
  allLabel = "全部",
): string {
  const unique = [...new Set(values.filter(Boolean))].sort((a, b) => formatValue(a).localeCompare(formatValue(b), "zh-CN"));
  const missing = selected && !unique.some((value) => value === selected) ?
    `<option value="${escapeHtml(selected)}" selected>${escapeHtml(selected)}</option>` : "";
  return `
    <label>
      <span>${escapeHtml(label)}</span>
      <select name="${escapeHtml(name)}" data-filter="${escapeHtml(name)}">
        <option value="">${allLabel}</option>
        ${missing}
        ${unique
          .map(
            (value) =>
              `<option value="${escapeHtml(value)}"${selected === value ? " selected" : ""}>${escapeHtml(formatValue(value))}</option>`,
          )
          .join("")}
      </select>
    </label>`;
}

function metricCard(value: number, tone: Severity | "total"): string {
  return `<div class="metric-card metric-${tone}">${tone === "total" ? "<span>总数</span>" : renderSeverityHelp(tone)}<strong>${value}</strong></div>`;
}

function renderBreakdown(values: Record<string, number>): string {
  const entries = Object.entries(values).sort((a, b) => b[1] - a[1]);
  if (entries.length === 0) return '<p class="muted">暂无数据</p>';
  return `<ul class="breakdown">${entries
    .map(
      ([name, count]) =>
        `<li><span>${escapeHtml(name || "未知")}</span><strong>${count}</strong></li>`,
    )
    .join("")}</ul>`;
}

function renderClusterSelect(clusters: Cluster[], filters: FindingFilters): string {
  const selected = displayFindingScope(clusters, filters);
  const dimensions = [
    ["environment", "环境", "全部环境（ALL）"],
    ["business", "业务", "全部业务"],
    ["group", "集群名称", "全部集群名称"],
  ] as const;
  let available = clusters;
  const selectors = dimensions.map(([key, label, all]) => {
    const value = selected[key];
    const choices = available.map((item) => scopeValue(item, key));
    if (value) available = available.filter((item) => scopeValue(item, key) === value);
    return renderSelect(key, label, choices, value, undefined, all);
  });
  const instances = new Map(available.map(({ id, name }) => [id, name]));
  return selectors.join("") + renderSelect("cluster", "集群实例", [...instances.keys()],
    selected.cluster === allClusters ? undefined : selected.cluster, (id) => instances.get(id) ?? id, "全部集群实例");
}
