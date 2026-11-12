import {
  ApiError,
  type CorrelationBundle,
  type CorrelationSignal,
  type CorrelationSource,
  type SignalUnit,
  type SourceState,
} from "../../../clients/typescript/generated.ts";
import { escapeHtml, formatSeverity, formatTimestamp } from "../components/html.ts";

const sourceNames: ReadonlyArray<readonly [CorrelationSource, string]> = [
  ["kubernetes-events", "Kubernetes 事件"],
  ["prometheus", "Prometheus"],
  ["victorialogs", "VictoriaLogs"],
  ["alertmanager", "Alertmanager"],
];

const stateLabels: Record<SourceState, string> = {
  available: "可用",
  partial: "部分可用",
  unavailable: "不可用",
  disabled: "已禁用",
};

const unitLabels: Record<SignalUnit, string> = {
  count: "",
  bytes: "B",
  seconds: "s",
  milliseconds: "ms",
  percent: "%",
  ratio: "",
  cores: "核",
  bytes_per_second: "B/s",
  requests_per_second: "请求/秒",
};

const numberFormat = new Intl.NumberFormat("zh-CN", { maximumSignificantDigits: 6 });
const alertSummaries = new Map([
  ["Alertmanager state active", "告警状态：触发中"],
  ["Alertmanager state suppressed", "告警状态：已抑制"],
  ["Alertmanager state unprocessed", "告警状态：待处理"],
]);

export function renderCorrelationSection(id: string): string {
  return `
    <section class="panel correlation-panel" aria-labelledby="correlation-heading">
      <h2 id="correlation-heading">关联证据</h2>
      <div data-correlation-finding="${escapeHtml(id)}" aria-live="polite" aria-busy="true">
        ${renderCorrelationLoading()}
      </div>
    </section>`;
}

export function renderCorrelationLoading(): string {
  return '<p class="evidence-notice">正在加载关联证据…</p>';
}

export function renderCorrelationError(error: unknown): string {
  const status = error instanceof ApiError ? error.status : undefined;
  const title = status === 401
    ? "查看证据需要身份认证"
    : status === 403
      ? "无权查看关联证据"
      : "加载关联证据失败";
  return `
    <div class="evidence-notice" role="alert">
      <p><strong>${title}</strong></p>
      ${status === 403 ? "" : '<button type="button" class="button secondary" data-action="retry-correlation">重新加载证据</button>'}
    </div>`;
}

export function renderCorrelationEvidence(bundle: CorrelationBundle): string {
  return `
    <p class="evidence-window">诊断时间窗口：${formatTimestamp(bundle.window.start)} – ${formatTimestamp(bundle.window.end)}</p>
    <div class="correlation-grid">
      ${sourceNames.map(([source, name]) => {
        const state = bundle.sources.find((status) => status.source === source)?.state ?? "unavailable";
        const signals = bundle.signals.filter((signal) => signal.source === source);
        return `
          <section class="evidence-source" aria-label="${name}">
            <div class="evidence-source-heading">
              <h3>${name}</h3>
              <span class="source-state source-state-${escapeHtml(state)}">${escapeHtml(stateLabels[state])}</span>
            </div>
            ${signals.length > 0
              ? `<ol class="evidence-signals">${signals.map(renderSignal).join("")}</ol>`
              : state === "available" ? '<p class="evidence-notice">此时间窗口内暂无证据。</p>' : ""}
          </section>`;
      }).join("")}
    </div>`;
}

function renderSignal(signal: CorrelationSignal): string {
  const resourceName = [signal.resource?.namespace, signal.resource?.name].filter(Boolean).join("/");
  const resource = [signal.resource?.kind, resourceName].filter(Boolean).join(" ");
  const first = signal.firstSeen ?? signal.lastSeen;
  const timeRange = first === signal.lastSeen || !signal.lastSeen
    ? formatTimestamp(first)
    : `${formatTimestamp(first)} – ${formatTimestamp(signal.lastSeen)}`;
  const metadata = [resource ? escapeHtml(resource) : "", first ? timeRange : ""].filter(Boolean).join(" · ");
  const value = signal.value === undefined
    ? ""
    : [numberFormat.format(signal.value), signal.unit ? unitLabels[signal.unit] : ""].filter(Boolean).join(" ");
  const count = signal.count && signal.type !== "alert" ? formatCount(signal.count, signal.type) : "";
  const summary = signal.source === "alertmanager" && signal.summary
    ? alertSummaries.get(signal.summary) ?? signal.summary
    : signal.summary;

  return `
    <li class="evidence-signal">
      <div class="evidence-signal-heading">
        <strong>${escapeHtml(signal.name ?? signal.id)}</strong>
        ${signal.severity ? `<span class="severity severity-${escapeHtml(signal.severity)}">${escapeHtml(formatSeverity(signal.severity))}</span>` : ""}
      </div>
      ${metadata ? `<p class="evidence-meta">${metadata}</p>` : ""}
      ${value || count ? `<p class="evidence-measurement">${escapeHtml([value, count].filter(Boolean).join(" · "))}</p>` : ""}
      ${summary ? `<p class="evidence-summary">${escapeHtml(summary)}</p>` : ""}
    </li>`;
}

function formatCount(count: number, type: CorrelationSignal["type"]): string {
  return `${numberFormat.format(count)} ${type === "metric" ? "个样本" : "次匹配"}`;
}
