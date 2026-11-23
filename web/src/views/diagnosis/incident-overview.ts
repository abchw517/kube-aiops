import { apiClient } from "../../api/client.ts";
import { ApiError, type IncidentOverview, type IncidentSeverity, type ListIncidentsParams } from "../../api/generated.ts";
import { escapeHtml } from "../../components/html.ts";

const severityLevels = [
  { level: "P1", label: "P1 高风险", tone: "critical" },
  { level: "P2", label: "P2 中风险", tone: "warning" },
  { level: "P3", label: "P3 低风险", tone: "info" },
] as const;

type SeverityCounts = Record<IncidentSeverity, number | undefined>;

/** 接口返回的计数应为非负整数，缺失时留空。 */
function countOf(value: number | undefined): number | undefined {
  return value !== undefined && Number.isSafeInteger(value) && value >= 0 ? value : undefined;
}

/** 按服务端权限与当前筛选取得各级别总数，避免以最近列表推算分布。 */
async function severityCounts(params: ListIncidentsParams, summary: IncidentOverview): Promise<SeverityCounts> {
  const total = countOf(summary.counts.total);
  const counts = { P1: undefined, P2: undefined, P3: undefined } as SeverityCounts;
  await Promise.all(severityLevels.map(async ({ level }) => {
    if (total === 0 || params.severity) {
      counts[level] = !params.severity || level === params.severity ? total : 0;
      return;
    }
    try {
      const result = await apiClient.getIncidentOverview({ ...params, severity: level });
      counts[level] = countOf(result.counts.total);
    } catch (error) {
      if (error instanceof ApiError && (error.status === 401 || error.status === 403)) throw error;
      counts[level] = undefined;
    }
  }));
  return counts;
}

/** 统一展示计数与不可用状态，避免把缺失数据当成零。 */
function countLabel(count: number | undefined): string {
  return count === undefined ? '<span class="incident-overview-unavailable">暂不可用</span>' : `${count}<span>件</span>`;
}

/** 渲染来自当前权限范围的事件级别分布。 */
function renderSeverity(counts: SeverityCounts): string {
  const p1 = counts.P1;
  const p2 = counts.P2;
  const p3 = counts.P3;
  const total = p1 !== undefined && p2 !== undefined && p3 !== undefined ? p1 + p2 + p3 : undefined;
  const chart = total && p1 !== undefined && p2 !== undefined
    ? ` style="--incident-chart-p1:${p1 / total * 100}%;--incident-chart-p2:${(p1 + p2) / total * 100}%;--incident-chart-end:100%"` : "";
  return `<section class="panel incident-overview-panel">
    <header class="incident-overview-heading"><h2>事件级别分布</h2></header>
    <div class="incident-severity-layout">
      <div class="incident-severity-chart"${chart} aria-hidden="true"><div>${total ?? "—"}<span>事件</span></div></div>
      <ul class="incident-severity-legend">${severityLevels.map(({ level, label, tone }) =>
        `<li><span class="incident-plot-key incident-plot-${tone}"></span><span>${label}</span><span>${countLabel(counts[level])}</span></li>`
      ).join("")}</ul>
    </div>
  </section>`;
}

/** 使用接口返回的 UTC 日期及计数构建趋势图，数字在键盘聚焦时同样可见。 */
function renderTrend(trend: IncidentOverview["trend"]): string {
  const days = Object.entries(trend)
    .filter(([day, count]) => /^\d{4}-\d{2}-\d{2}$/.test(day) && countOf(count) !== undefined)
    .sort(([a], [b]) => a.localeCompare(b));
  const highest = Math.max(0, ...days.map(([, count]) => count));
  const step = Math.max(1, Math.ceil(highest / 4));
  const chartMax = step * 4;
  const total = days.reduce((sum, [, count]) => sum + count, 0);
  return `<section class="panel incident-overview-panel">
    <header class="incident-overview-heading"><h2>近 7 天事件趋势</h2><span>UTC</span></header>
    <figure class="incident-trend-chart">
      ${days.length ? `<div class="incident-trend-plot">
        <div class="incident-trend-axis" aria-hidden="true">${[4, 3, 2, 1, 0].map((mark) => `<span>${mark * step}</span>`).join("")}</div>
        <div class="incident-trend-bars" style="--incident-trend-days:${days.length}">${days.map(([day, count]) => {
          const label = day.slice(5).replace("-", "/");
          const description = `${day}（UTC）共 ${count} 件事件`;
          return `<button class="incident-trend-bar" type="button" style="--incident-trend-height:${count / chartMax * 100}%"
            data-incident-trend-point="${description}" aria-label="${description}" title="${description}">
            <span aria-hidden="true">${label}</span>
          </button>`;
        }).join("")}</div>
      </div>` : ""}
      <figcaption data-incident-trend-note role="status">${days.length ? `近 7 天新发 ${total} 件事件` : "趋势数据暂不可用"}</figcaption>
    </figure>
  </section>`;
}

/** 渲染诊断总览的四项指标与图表。 */
export async function renderIncidentOverview(summary: IncidentOverview, params: ListIncidentsParams): Promise<string> {
  const counts = await severityCounts(params, summary);
  const metrics = [
    ["故障事件", countOf(summary.counts.total), "total"],
    ["处理中", countOf(summary.counts.investigating), "warning"],
    ["已恢复", countOf(summary.counts.recovered), "recovered"],
    ["高风险（P1）", counts.P1, "critical"],
  ] as const;
  return `<dl class="incident-overview-metrics" aria-label="故障汇总">
    ${metrics.map(([label, count, tone]) => `<div class="panel incident-overview-metric incident-plot-${tone}">
      <dt>${escapeHtml(label)}</dt><dd>${countLabel(count)}</dd>
    </div>`).join("")}
  </dl>
  <div class="incident-overview-plots">${renderTrend(summary.trend)}${renderSeverity(counts)}</div>`;
}

/** 让选中的日期在图表下方持续可读。 */
export function bindIncidentOverview(root: HTMLElement): void {
  const note = root.querySelector<HTMLElement>("[data-incident-trend-note]");
  if (!note) return;
  for (const point of root.querySelectorAll<HTMLButtonElement>("[data-incident-trend-point]")) {
    point.addEventListener("click", () => {
      const description = point.dataset.incidentTrendPoint;
      if (description) note.textContent = description;
    });
  }
}
