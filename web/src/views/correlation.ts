import {
  ApiError,
  type CorrelationBundle,
  type CorrelationSignal,
  type CorrelationSource,
  type SignalUnit,
  type SourceState,
} from "../../../clients/typescript/generated.ts";
import { escapeHtml, formatTimestamp } from "../components/html.ts";

const sourceNames: ReadonlyArray<readonly [CorrelationSource, string]> = [
  ["kubernetes-events", "Kubernetes Events"],
  ["prometheus", "Prometheus"],
  ["victorialogs", "VictoriaLogs"],
  ["alertmanager", "Alertmanager"],
];

const stateLabels: Record<SourceState, string> = {
  available: "Available",
  partial: "Partial",
  unavailable: "Unavailable",
  disabled: "Disabled",
};

const unitLabels: Record<SignalUnit, string> = {
  count: "",
  bytes: "B",
  seconds: "s",
  milliseconds: "ms",
  percent: "%",
  ratio: "",
  cores: "cores",
  bytes_per_second: "B/s",
  requests_per_second: "req/s",
};

const numberFormat = new Intl.NumberFormat(undefined, { maximumSignificantDigits: 6 });

export function renderCorrelationSection(id: string): string {
  return `
    <section class="panel correlation-panel" aria-labelledby="correlation-heading">
      <h2 id="correlation-heading">Correlated evidence</h2>
      <div data-correlation-finding="${escapeHtml(id)}" aria-live="polite" aria-busy="true">
        ${renderCorrelationLoading()}
      </div>
    </section>`;
}

export function renderCorrelationLoading(): string {
  return '<p class="evidence-notice">Loading correlation evidence…</p>';
}

export function renderCorrelationError(error: unknown): string {
  const status = error instanceof ApiError ? error.status : undefined;
  const title = status === 401
    ? "Authentication required for evidence"
    : status === 403
      ? "Evidence access denied"
      : "Unable to load evidence";
  return `
    <div class="evidence-notice" role="alert">
      <p><strong>${title}</strong></p>
      ${status === 403 ? "" : '<button type="button" class="button secondary" data-action="retry-correlation">Retry evidence</button>'}
    </div>`;
}

export function renderCorrelationEvidence(bundle: CorrelationBundle): string {
  return `
    <p class="evidence-window">Finding window · ${formatTimestamp(bundle.window.start)} – ${formatTimestamp(bundle.window.end)}</p>
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
              : state === "available" ? '<p class="evidence-notice">No evidence in this window.</p>' : ""}
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

  return `
    <li class="evidence-signal">
      <div class="evidence-signal-heading">
        <strong>${escapeHtml(signal.name ?? signal.id)}</strong>
        ${signal.severity ? `<span class="severity severity-${escapeHtml(signal.severity)}">${escapeHtml(signal.severity)}</span>` : ""}
      </div>
      ${metadata ? `<p class="evidence-meta">${metadata}</p>` : ""}
      ${value || count ? `<p class="evidence-measurement">${escapeHtml([value, count].filter(Boolean).join(" · "))}</p>` : ""}
      ${signal.summary ? `<p class="evidence-summary">${escapeHtml(signal.summary)}</p>` : ""}
    </li>`;
}

function formatCount(count: number, type: CorrelationSignal["type"]): string {
  const units = type === "metric" ? ["sample", "samples"] : ["match", "matches"];
  return `${numberFormat.format(count)} ${count === 1 ? units[0] : units[1]}`;
}
