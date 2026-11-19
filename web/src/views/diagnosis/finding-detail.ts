import type { Finding } from "../../api/generated.ts";
import { escapeHtml, formatSeverity, formatTimestamp } from "../../components/html.ts";

import { findingReviewPanel, findingReviewTrigger } from "./finding-review.ts";

const reviewPrefix = "AI 复核结论\n\n";

export function renderFindingDetail(finding: Finding, reviewEnabled = false, returnQuery: string | null = null): string {
  const query = returnQuery ?? new URLSearchParams({ cluster: finding.cluster }).toString();
  return `
    <article class="detail-grid" data-testid="finding-detail">
      <section class="panel detail-main">
        <a class="back-link" href="#/findings?${escapeHtml(query)}">← 返回诊断列表</a>
        <div class="detail-heading">
          <h2>故障信息</h2>
          <span class="severity severity-${escapeHtml(finding.severity)}">${escapeHtml(formatSeverity(finding.severity))}</span>
        </div>
        <p class="detail-text detail-problem">${escapeHtml(finding.problem)}</p>
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
        <div class="detail-heading"><h2>诊断说明</h2>${reviewEnabled ? findingReviewTrigger() : ""}</div>
        <h3>K8sGPT 原始说明</h3>
        <p class="detail-text" data-finding-explanation tabindex="-1">${escapeHtml((finding.originalDetails ?? finding.details) || "暂无补充诊断说明。")}</p>
        <div data-review-saved aria-live="polite"${finding.originalDetails === undefined ? " hidden" : ""}>${finding.originalDetails === undefined ? "" :
          `<h3>已保存复核结论${finding.reviewRevision ? ` <small>v${finding.reviewRevision}</small>` : ""}</h3><p class="detail-text">${escapeHtml(finding.details?.replace(reviewPrefix, "") ?? "")}</p>`}</div>
        ${reviewEnabled ? findingReviewPanel() : ""}
      </section>
    </article>`;
}

function detailRow(label: string, value: string, trusted = false): string {
  return `<div><dt>${escapeHtml(label)}</dt><dd>${trusted ? value : escapeHtml(value)}</dd></div>`;
}
