import type { Finding } from "../../../clients/typescript/generated.ts";
import { escapeHtml, formatSeverity, formatTimestamp } from "../components/html.ts";

export function renderFindingDetail(finding: Finding): string {
  return `
    <article class="detail-grid" data-testid="finding-detail">
      <section class="panel detail-main">
        <a class="back-link" href="#/findings${finding.cluster === "local" ? "" : `?cluster=${encodeURIComponent(finding.cluster)}`}">← 返回诊断列表</a>
        <div class="detail-title">
          <span class="severity severity-${escapeHtml(finding.severity)}">${escapeHtml(formatSeverity(finding.severity))}</span>
          <h2>${escapeHtml(finding.problem)}</h2>
        </div>
        <dl class="detail-list">
          ${detailRow("诊断编号", finding.id)}
          ${detailRow("集群", finding.cluster)}
          ${detailRow("命名空间", finding.namespace ?? finding.resource.namespace ?? "—")}
          ${detailRow("资源类型", finding.resource.kind ?? "—")}
          ${detailRow("资源名称", finding.resource.name ?? "—")}
          ${detailRow("来源", finding.source)}
          ${detailRow("发现时间", formatTimestamp(finding.createdAt), true)}
        </dl>
      </section>
      <section class="panel detail-copy">
        <h2>诊断说明</h2>
        <p>${escapeHtml(finding.details ?? "暂无补充诊断说明。")}</p>
      </section>
    </article>`;
}

function detailRow(label: string, value: string, trusted = false): string {
  return `<div><dt>${escapeHtml(label)}</dt><dd>${trusted ? value : escapeHtml(value)}</dd></div>`;
}
