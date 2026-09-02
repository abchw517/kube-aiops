import type {
  Cluster,
  Finding,
  FindingSummary,
  Namespace,
  Severity,
} from "../../../clients/typescript/generated.ts";
import { escapeHtml, formatTimestamp } from "../components/html.ts";
import type { FindingFilters } from "../domain/filters.ts";

export function renderFilters(
  clusters: Cluster[],
  namespaces: Namespace[],
  knownKinds: string[],
  filters: FindingFilters,
): string {
  return `
    <section class="panel filters" aria-label="Finding filters">
      ${renderSelect("cluster", "Cluster", clusters.map((item) => item.id), filters.cluster ?? "local")}
      ${renderSelect("namespace", "Namespace", namespaces.map((item) => item.name), filters.namespace)}
      ${renderSelect("severity", "Severity", ["critical", "warning", "info"] satisfies Severity[], filters.severity)}
      ${renderSelect("kind", "Kind", knownKinds, filters.kind)}
      <button class="button secondary" type="button" data-action="clear-filters">Clear filters</button>
    </section>`;
}

export function renderSummary(summary: FindingSummary): string {
  const severity = summary.bySeverity;
  return `
    <section class="summary-grid" aria-label="Finding summary" data-testid="finding-summary">
      ${metricCard("Total", summary.total)}
      ${metricCard("Critical", severity.critical ?? 0)}
      ${metricCard("Warning", severity.warning ?? 0)}
      ${metricCard("Info", severity.info ?? 0)}
    </section>
    <section class="summary-breakdown panel">
      <div>
        <h3>By kind</h3>
        ${renderBreakdown(summary.byKind)}
      </div>
      <div>
        <h3>By namespace</h3>
        ${renderBreakdown(summary.byNamespace)}
      </div>
    </section>`;
}

export function renderFindingTable(findings: Finding[]): string {
  return `
    <section class="panel table-panel" data-testid="finding-list">
      <div class="section-heading">
        <div>
          <p class="eyebrow">Normalized diagnostics</p>
          <h2>${findings.length} ${findings.length === 1 ? "finding" : "findings"}</h2>
        </div>
      </div>
      <div class="table-wrap">
        <table>
          <thead>
            <tr>
              <th>Severity</th>
              <th>Resource</th>
              <th>Namespace</th>
              <th>Problem</th>
              <th>Created</th>
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
      <td><span class="severity severity-${escapeHtml(finding.severity)}">${escapeHtml(finding.severity)}</span></td>
      <td>${escapeHtml(resource || "—")}</td>
      <td>${escapeHtml(finding.namespace ?? finding.resource.namespace ?? "—")}</td>
      <td><a href="#/findings/${encodeURIComponent(finding.id)}">${escapeHtml(finding.problem)}</a></td>
      <td>${formatTimestamp(finding.createdAt)}</td>
    </tr>`;
}

function renderSelect(
  name: string,
  label: string,
  values: string[],
  selected?: string,
): string {
  const unique = [...new Set(values.filter(Boolean))].sort((a, b) => a.localeCompare(b));
  const allOption = name === "cluster" ? "" : '<option value="">All</option>';
  return `
    <label>
      <span>${escapeHtml(label)}</span>
      <select name="${escapeHtml(name)}" data-filter="${escapeHtml(name)}">
        ${allOption}
        ${unique
          .map(
            (value) =>
              `<option value="${escapeHtml(value)}"${selected === value ? " selected" : ""}>${escapeHtml(value)}</option>`,
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
  if (entries.length === 0) return '<p class="muted">No data</p>';
  return `<ul class="breakdown">${entries
    .map(
      ([name, count]) =>
        `<li><span>${escapeHtml(name || "unknown")}</span><strong>${count}</strong></li>`,
    )
    .join("")}</ul>`;
}
