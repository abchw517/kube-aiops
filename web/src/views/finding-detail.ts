import type { Finding } from "../../../clients/typescript/generated.ts";
import { escapeHtml, formatTimestamp } from "../components/html.ts";

export function renderFindingDetail(finding: Finding): string {
  return `
    <article class="detail-grid" data-testid="finding-detail">
      <section class="panel detail-main">
        <a class="back-link" href="#/findings">← Back to findings</a>
        <div class="detail-title">
          <span class="severity severity-${escapeHtml(finding.severity)}">${escapeHtml(finding.severity)}</span>
          <h1>${escapeHtml(finding.problem)}</h1>
        </div>
        <dl class="detail-list">
          ${detailRow("Finding ID", finding.id)}
          ${detailRow("Cluster", finding.cluster)}
          ${detailRow("Namespace", finding.namespace ?? finding.resource.namespace ?? "—")}
          ${detailRow("Kind", finding.resource.kind ?? "—")}
          ${detailRow("Resource", finding.resource.name ?? "—")}
          ${detailRow("Source", finding.source)}
          ${detailRow("Created", formatTimestamp(finding.createdAt), true)}
        </dl>
      </section>
      <section class="panel detail-copy">
        <p class="eyebrow">AI diagnostic detail</p>
        <h2>Explanation</h2>
        <p>${escapeHtml(finding.details ?? "No additional diagnostic details were provided.")}</p>
      </section>
    </article>`;
}

function detailRow(label: string, value: string, trusted = false): string {
  return `<div><dt>${escapeHtml(label)}</dt><dd>${trusted ? value : escapeHtml(value)}</dd></div>`;
}
