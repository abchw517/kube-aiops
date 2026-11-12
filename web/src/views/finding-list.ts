import type {
  Cluster,
  Finding,
  FindingSummary,
  Namespace,
  Severity,
} from "../../../clients/typescript/generated.ts";
import { escapeHtml, formatSeverity, formatTimestamp } from "../components/html.ts";
import type { FindingFilters } from "../domain/filters.ts";

export function renderFilters(
  clusters: Cluster[],
  namespaces: Namespace[],
  knownKinds: string[],
  filters: FindingFilters,
): string {
  return `
    <section class="panel filters" aria-label="诊断结果筛选">
      ${renderSelect("cluster", "集群", clusters.map((item) => item.id), filters.cluster ?? "local")}
      ${renderSelect("namespace", "命名空间", namespaces.map((item) => item.name), filters.namespace)}
      ${renderSelect("severity", "严重级别", ["critical", "warning", "info"] satisfies Severity[], filters.severity, formatSeverity)}
      ${renderSelect("kind", "资源类型", knownKinds, filters.kind)}
      <button class="button secondary" type="button" data-action="clear-filters">清除筛选</button>
    </section>`;
}

export function renderSummary(summary: FindingSummary): string {
  const severity = summary.bySeverity;
  return `
    <section class="summary-grid" aria-label="诊断结果汇总" data-testid="finding-summary">
      ${metricCard("总数", summary.total)}
      ${metricCard(formatSeverity("critical"), severity.critical ?? 0)}
      ${metricCard(formatSeverity("warning"), severity.warning ?? 0)}
      ${metricCard(formatSeverity("info"), severity.info ?? 0)}
    </section>
    <section class="summary-breakdown panel">
      <div>
        <h3>按资源类型</h3>
        ${renderBreakdown(summary.byKind)}
      </div>
      <div>
        <h3>按命名空间</h3>
        ${renderBreakdown(summary.byNamespace)}
      </div>
    </section>`;
}

export function renderFindingTable(findings: Finding[]): string {
  return `
    <section class="panel table-panel" data-testid="finding-list">
      <div class="section-heading">
        <div>
          <h2>共 ${findings.length} 条诊断结果</h2>
        </div>
      </div>
      <div class="table-wrap">
        <table>
          <thead>
            <tr>
              <th>严重级别</th>
              <th>资源</th>
              <th>命名空间</th>
              <th>问题</th>
              <th>发现时间</th>
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
      <td>${escapeHtml(resource || "—")}</td>
      <td>${escapeHtml(finding.namespace ?? finding.resource.namespace ?? "—")}</td>
      <td><a href="#/findings/${encodeURIComponent(finding.id)}">${escapeHtml(finding.problem)}</a></td>
      <td>${formatTimestamp(finding.createdAt)}</td>
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

function metricCard(label: string, value: number): string {
  return `<article class="metric-card"><span>${escapeHtml(label)}</span><strong>${value}</strong></article>`;
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
