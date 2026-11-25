import { type aggregateDiagnostics, type OverviewDays, overviewWindows } from "../../domain/unified-diagnostics.ts";

type Overview = ReturnType<typeof aggregateDiagnostics>;
type Day = Overview["daily"][number];
export const trendLabels = { incident: "故障事件", finding: "K8sGPT", zabbix: "Zabbix", alertmanager: "Alertmanager" } as const;
type Source = keyof typeof trendLabels;
export type Trend = {
  data: Overview;
  sources: Source[];
  incomplete: boolean;
  available: boolean;
  now: number;
};

const cutoffTime = new Intl.DateTimeFormat("zh-CN", {
  timeZone: "Asia/Shanghai", hour: "2-digit", minute: "2-digit", hourCycle: "h23",
});

function dayTotal(day: Day): number {
  return day.incident + day.finding + day.zabbix + day.alertmanager;
}

function legend(sources: Source[], day?: Day): string {
  return sources.map(source =>
    `<span class="overview-series-${source}">${trendLabels[source]}${day ? ` ${day[source]}` : ""}</span>`).join("");
}

function renderReadout(day: Day, today: string, sources: Source[]): string {
  return `<span><strong>${day.date}${day.date === today ? " · 今日" : ""}</strong> · ${dayTotal(day)} 条</span>
    <span class="overview-trend-legend">${legend(sources, day)}</span>`;
}

export function renderOverviewTrend(trend: Trend, days: OverviewDays, windowHref: (window: OverviewDays) => string): string {
  const points = trend.data.daily.slice(-days);
  const today = points.at(-1)?.date ?? "";
  const total = points.reduce((sum, day) => sum + dayTotal(day), 0);
  const { sources } = trend;
  const count = trend.available && (total || !trend.incomplete) ? `${total} 条` : "—";
  const toggle = overviewWindows.map(window =>
    `<a href="${windowHref(window)}" data-overview-window="${window}"${window === days ? ' aria-current="true"' : ""}>${window} 天</a>`).join("");
  return `<header class="overview-heading"><div><h2 id="overview-trend-title">诊断趋势</h2>
    <p>${points[0]?.date} 至 ${today} · UTC+8 · 按首次发现日统计</p></div>
    <nav class="overview-segment" aria-label="趋势时间窗">${toggle}</nav></header>
    <div class="overview-trend-meta"><span>${trend.incomplete && trend.available ? "窗口内已获取" : "窗口内"} <strong>${count}</strong></span>
      <span class="overview-trend-legend" aria-label="来源图例">${legend(sources)}</span></div>
    ${total ? renderPlot(points, today, sources) : renderEmpty(trend, days, windowHref)}
    ${trend.available && total ? `<p class="overview-chart-note">${today.slice(5).replace("-", "/")} 当日统计截至 ${cutoffTime.format(trend.now)}。</p>` : ""}
    ${trend.available && trend.data.undated ? `<p class="overview-chart-note">${trend.data.undated} 条记录缺少有效时间，未计入趋势。</p>` : ""}`;
}

function renderEmpty(trend: Trend, days: OverviewDays, windowHref: (window: OverviewDays) => string): string {
  const title = !trend.available ? "诊断趋势暂不可用" : trend.incomplete ? `近 ${days} 天未取得记录` :
    trend.data.undated ? "未取得当前窗口的带时间记录" : `近 ${days} 天无新增`;
  const detail = !trend.available ? "当前筛选范围未取得可用数据。" :
    trend.incomplete ? "数据覆盖不完整，当前结果无法确认是否存在新增。" :
    trend.data.undated ? "已取得的带有效时间记录未落在当前时间窗。" :
    trend.data.total ? "当前范围有较早的诊断记录。" : "当前筛选范围没有匹配的诊断记录。";
  const wider = days === 7 ? 14 : 30;
  const canWiden = trend.available && days < 30 && trend.data.daily.slice(0, -days).some(dayTotal);
  return `<div class="overview-trend-empty"><strong>${title}</strong><p>${detail}</p>
    ${canWiden ? `<a href="${windowHref(wider)}" data-overview-window="${wider}">查看近 ${wider} 天</a>` : ""}</div>`;
}

function renderPlot(points: Day[], today: string, sources: Source[]): string {
  const scaleStep = Math.max(1, Math.ceil(Math.max(...points.map(dayTotal)) / 4));
  const scale = scaleStep * 4;
  const latest = points.reduce((selected, day, index) => dayTotal(day) ? index : selected, 0);
  const buttons = points.map((day, index) => {
    const label = `${day.date}（UTC+8）共 ${dayTotal(day)} 条${sources.map(key => `，${trendLabels[key]} ${day[key]} 条`).join("")}`;
    const segments = sources.filter(source => day[source] > 0).map(source =>
      `<span class="overview-series-${source}" data-overview-bar-height="${day[source] / scale * 100}"></span>`).join("");
    return `<button type="button" class="overview-trend-day" tabindex="${index === latest ? 0 : -1}"
      data-overview-point="${index}" aria-pressed="${index === latest}" aria-label="${label}" title="${label}">
      <span class="overview-trend-bars" aria-hidden="true">${segments}</span></button>`;
  }).join("");
  const dates = points.map(({ date }, index) => {
    const last = index === points.length - 1;
    const stride = points.length === 14 ? 3 : 5;
    const visible = points.length === 7 || last || (index < points.length - 2 && index % stride === 0);
    return `<span>${visible ? date.slice(5).replace("-", "/") : ""}</span>`;
  }).join("");
  const firstDay = points[latest];
  return `<div class="overview-plot" data-overview-days="${points.length}">
    <div class="overview-ticks" aria-hidden="true">${[4, 3, 2, 1, 0].map(tick => `<span>${tick * scaleStep}</span>`).join("")}</div>
    <div class="overview-plot-scroll"><div class="overview-plot-content">
      <div class="overview-plot-grid" role="group" aria-label="每日新增诊断，使用左右方向键切换日期">${buttons}</div>
      <div class="overview-dates" aria-hidden="true">${dates}</div>
    </div></div>
  </div><div data-overview-note class="overview-day-readout" role="status">${firstDay ? renderReadout(firstDay, today, sources) : ""}</div>`;
}

export function bindOverviewTrend(root: HTMLElement, trend: Trend, days: OverviewDays): void {
  const note = root.querySelector<HTMLElement>("[data-overview-note]");
  const scroll = root.querySelector<HTMLElement>(".overview-plot-scroll");
  if (!note || !scroll) return;
  // The Portal CSP blocks style attributes in markup. Apply computed dimensions through CSSOM.
  for (const bar of scroll.querySelectorAll<HTMLElement>("[data-overview-bar-height]")) {
    bar.style.height = `${bar.dataset.overviewBarHeight}%`;
  }
  const points = trend.data.daily.slice(-days);
  const today = points.at(-1)?.date ?? "";
  const buttons = [...scroll.querySelectorAll<HTMLButtonElement>("[data-overview-point]")];
  const reveal = (button: HTMLButtonElement) => {
    const point = button.getBoundingClientRect();
    const viewport = scroll.getBoundingClientRect();
    if (point.left < viewport.left) scroll.scrollLeft += point.left - viewport.left;
    else if (point.right > viewport.right) scroll.scrollLeft += point.right - viewport.right;
  };
  const select = (index: number, focus = false) => {
    const button = buttons[index];
    const day = points[index];
    if (!button || !day) return;
    for (const candidate of buttons) {
      const active = candidate === button;
      candidate.tabIndex = active ? 0 : -1;
      candidate.setAttribute("aria-pressed", String(active));
    }
    note.innerHTML = renderReadout(day, today, trend.sources);
    reveal(button);
    if (focus) button.focus({ preventScroll: true });
  };
  const selected = buttons.find(button => button.tabIndex === 0);
  if (selected) reveal(selected);
  for (const [index, button] of buttons.entries()) {
    button.addEventListener("click", () => select(index));
    button.addEventListener("keydown", (event) => {
      const destinations: Record<string, number> = {
        ArrowLeft: Math.max(index - 1, 0), ArrowRight: Math.min(index + 1, buttons.length - 1),
        Home: 0, End: buttons.length - 1,
      };
      const destination = destinations[event.key];
      if (destination === undefined) return;
      event.preventDefault();
      select(destination, true);
    });
  }
}
