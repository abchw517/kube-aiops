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
import { type FindingFilters, serializeFindingFilters } from "../../domain/filters.ts";

interface FindingWorkspace {
  clusters: Cluster[];
  namespaces: Namespace[];
  knownKinds: string[];
  filters: FindingFilters;
  page: FindingPage;
  summary: FindingSummary;
  hasCursor: boolean;
}

/** 组织诊断列表、筛选、汇总和沿用当前筛选条件的分页链接。 */
export function renderFindingWorkspace(workspace: FindingWorkspace): string {
  const { clusters, namespaces, knownKinds, filters, page, summary, hasCursor } = workspace;
  const query = serializeFindingFilters(filters);
  const nextCursor = page.pagination.continue;
  const nextQuery = new URLSearchParams(query);
  if (nextCursor) nextQuery.set("continue", nextCursor);
  return `
    ${renderFilters(clusters, namespaces, knownKinds, filters)}
    ${renderSummary(summary)}
    <div class="finding-workspace">
      <div class="finding-results">
        ${page.items.length === 0 ? emptyState() : renderFindingTable(page.items, summary.total)}
        ${hasCursor || nextCursor ? `<nav class="pagination" aria-label="诊断结果分页">
          ${hasCursor ? `<a class="button secondary" href="#/findings?${escapeHtml(query)}">返回首页</a>` : ""}
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
  const instance = clusters.find((item) => item.id === (filters.cluster ?? "local"));
  const freshness = instance?.status === "waiting" ? "采集端尚未上报。" :
    instance?.status === "offline" && instance.lastReportAt ?
      `采集离线，显示最近一次上报的数据（${new Date(instance.lastReportAt * 1000).toLocaleString()}）。` : "";
  return `
    <section class="panel filters" aria-label="诊断结果筛选">
      <fieldset>
        <legend>集群范围</legend>
        ${renderClusterSelect(clusters, filters.cluster ?? "local")}
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
      ${metricCard("总数", summary.total, "total")}
      ${metricCard(formatSeverity("critical"), severity.critical ?? 0, "critical")}
      ${metricCard(formatSeverity("warning"), severity.warning ?? 0, "warning")}
      ${metricCard(formatSeverity("info"), severity.info ?? 0, "info")}
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
function renderFindingTable(findings: Finding[], total: number): string {
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
              <th scope="col">命名空间</th>
              <th scope="col">发现时间</th>
            </tr>
          </thead>
          <tbody>
            ${findings.map(renderFindingRow).join("")}
          </tbody>
        </table>
      </div>
    </section>`;
}

function renderFindingRow(finding: Finding): string {
  const resource = [finding.resource.kind, finding.resource.name]
    .filter(Boolean)
    .join("/");
  return `
    <tr>
      <td><span class="severity severity-${escapeHtml(finding.severity)}">${escapeHtml(formatSeverity(finding.severity))}</span></td>
      <td class="finding-problem"><a href="#/findings/${encodeURIComponent(finding.id)}${finding.cluster === "local" ? "" : `?cluster=${encodeURIComponent(finding.cluster)}`}" title="${escapeHtml(finding.problem)}">${escapeHtml(finding.problem)}</a></td>
      <td translate="no">${escapeHtml(resource || "—")}</td>
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
): string {
  const unique = [...new Set(values.filter(Boolean))].sort((a, b) => a.localeCompare(b));
  const allOption = name === "cluster" ? "" : '<option value="">全部</option>';
  return `
    <label>
      <span>${escapeHtml(label)}</span>
      <select name="${escapeHtml(name)}" data-filter="${escapeHtml(name)}">
        ${allOption}
        ${unique
          .map(
            (value) =>
              `<option value="${escapeHtml(value)}"${selected === value ? " selected" : ""}>${escapeHtml(formatValue(value))}</option>`,
          )
          .join("")}
      </select>
    </label>`;
}

function metricCard(label: string, value: number, tone: Severity | "total"): string {
  return `<div class="metric-card metric-${tone}"><span>${escapeHtml(label)}</span><strong>${value}</strong></div>`;
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

function renderClusterSelect(clusters: Cluster[], selected: string): string {
  const current = clusters.find((cluster) => cluster.id === selected) ?? clusters[0];
  if (!current) return "";
  const dimensions = [["environment", "环境"], ["business", "业务"], ["group", "集群名称"]] as const;
  let available = clusters;
  const selectors = dimensions.map(([key, label]) => {
    const value = current[key] || "未分组";
    const choices = [...new Set(available.map((item) => item[key] || "未分组"))];
    available = available.filter((item) => (item[key] || "未分组") === value);
    return `<label><span>${label}</span><select name="${key}" data-filter="${key}">
      ${choices.map((choice) => `<option value="${escapeHtml(choice)}"${choice === value ? " selected" : ""}>${escapeHtml(choice)}</option>`).join("")}
    </select></label>`;
  });
  return `${selectors.join("")}<label><span>集群实例</span><select name="cluster" data-filter="cluster">
    ${available.map((item) => `<option value="${escapeHtml(item.id)}"${item.id === current.id ? " selected" : ""}>${escapeHtml(item.name)}</option>`).join("")}
    </select></label>`;
}
