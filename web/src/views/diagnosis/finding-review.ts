import type { CorrelationSource, SourceState } from "../../api/generated.ts";
import { escapeHtml, formatTimestamp } from "../../components/html.ts";

const judgments = { supported: "证据支持", insufficient: "证据不足", conflicting: "证据冲突" } as const;
const sources: Record<CorrelationSource, string> = {
  "kubernetes-events": "Kubernetes 事件",
  prometheus: "Prometheus",
  victorialogs: "VictoriaLogs",
  alertmanager: "Alertmanager",
};
const availability: Record<SourceState, string> = {
  available: "可用", unavailable: "暂不可用", partial: "部分可用", disabled: "已禁用",
};

interface Revision { revision: number; conclusion: string }
interface Assessment {
  assessment: { state: keyof typeof judgments; judgment: string; gaps: string; next: string };
  evidence: { source: CorrelationSource | "K8sGPT"; signalId?: string; title: string; time?: string; historical: boolean }[];
  evidenceState: string;
  sources: { source: CorrelationSource; state: SourceState }[];
  citations: { knowledgeId: string; title: string; revision: number }[] | null;
  appliedSkills?: string[] | null;
}


export const findingReviewTrigger = () => '<button type="button" class="button secondary finding-review-trigger" data-review-action="toggle" aria-controls="finding-review" aria-expanded="false">AI 深度调查（AI 复核）</button>';

export function findingReviewPanel(embedded = false): string {
  return `<section id="finding-review" class="finding-review" aria-label="AI 深度调查" hidden>
    ${embedded ? "" : "<h3>AI 深度调查</h3>"}
    <p data-review-status ${embedded ? 'class="notice"' : ""} role="status"></p>
    <div data-review-result></div>
    <details data-review-input ${embedded ? 'class="card" open' : ""}>
      <summary>补充观察或追问</summary>
      <label>现场观察或问题（选填）<textarea data-review-observation rows="3" maxlength="4000" placeholder="例如：10 分钟前 PVC 仍处于 Pending"></textarea></label>
      <button type="button" class="button secondary" data-review-action="assess">${embedded ? "发起 AI 复核" : "重新复核"}</button>
      ${embedded ? '<button type="button" class="btn danger" data-review-action="cancel" hidden>取消调查</button>' : ""}
    </details>
    <div data-review-editor ${embedded ? 'class="card"' : ""} hidden>
      <label>复核结论<textarea data-review-conclusion rows="5" maxlength="8192"></textarea></label>
      <button type="button" class="button" data-review-action="save">保存复核结论</button>
    </div>
    <button type="button" class="button secondary" data-review-action="refresh" hidden>重新载入复核记录</button>
    <details data-review-history hidden><summary>复核历史</summary><div></div></details>
  </section>`;
}

function renderAssessment({ assessment, evidence, sources: states, evidenceState, citations, appliedSkills }: Assessment, embedded = false): string {
  const summary = evidenceState === "timeout" ? "关联证据查询超时。" :
    evidenceState === "disabled" ? "未启用关联证据采集。" : "本次未能取得关联证据。";
  const section = (title: string, content: string) =>
    embedded ? `<section class="card"><h3>${title}</h3>${content}</section>` : `<h3>${title}</h3>${content}`;
  const refs = evidence.length ? `<ul class="review-evidence">${evidence.map((item) => {
    const source = item.source === "K8sGPT" ? item.source : sources[item.source];
    const link = escapeHtml(JSON.stringify([item.source, item.signalId ?? ""]));
    return `<li><button class="${embedded ? "ref" : "link-button"}" type="button" data-review-jump="${link}">${escapeHtml(source)} · ${escapeHtml(item.title)}</button>
      <small>${item.time ? formatTimestamp(item.time) : "时间未知"}${item.historical ? " · 历史记录" : ""}</small></li>`;
  }).join("")}</ul>` : "<p>本次判断没有可定位的引用。</p>";
  const sourceCoverage = `<details><summary>证据来源状态</summary>${evidenceState === "available"
    ? `<ul>${states.map(({source,state}) => `<li>${sources[source]} · ${availability[state]}</li>`).join("")}</ul>`
    : `<p>${summary}</p>`}</details>`;
  const result = `${appliedSkills?.length ? `<p>已应用技能 ${escapeHtml(appliedSkills.join("、"))}</p>` : ""}
    ${section("当前判断",`<span class="review-state" data-state="${assessment.state}">${judgments[assessment.state]}</span>
      <p class="detail-text">${escapeHtml(assessment.judgment)}</p>`)}
    ${section(embedded ? "证据引用" : "证据依据", refs + sourceCoverage)}
    ${section(embedded ? "待验证" : "待验证项", `<p class="detail-text">${escapeHtml(assessment.gaps)}</p>`)}
    ${section("下一步", `<p class="detail-text">${escapeHtml(assessment.next)}</p>`)}
    ${citations?.length ? `<details><summary>Wiki 参考</summary><ul>${citations.map((cite) =>
      `<li><a href="#/wiki/${encodeURIComponent(cite.knowledgeId)}?revision=${cite.revision}">${escapeHtml(cite.title)}</a> · v${cite.revision}</li>`).join("")}</ul></details>` : ""}`;
  return embedded ? `<div class="result">${result}</div>` : result;
}

function reviewElement<T extends HTMLElement>(container: HTMLElement, selector: string): T {
  const element = container.querySelector<T>(selector);
  if (!element) throw new Error(`Missing review element: ${selector}`);
  return element;
}

export function bindFindingReview(root: HTMLElement, id: string, cluster: string, workspace?: {
  signal: AbortSignal; onEvidence: () => void;
}): void {
  const trigger = root.querySelector<HTMLButtonElement>('[data-review-action="toggle"]');
  const review = root.querySelector<HTMLElement>("#finding-review");
  if (!trigger || !review) return;
  const panel = review;
  const status = reviewElement<HTMLElement>(panel, "[data-review-status]");
  const result = reviewElement<HTMLElement>(panel, "[data-review-result]");
  const observation = reviewElement<HTMLTextAreaElement>(panel, "[data-review-observation]");
  const conclusion = reviewElement<HTMLTextAreaElement>(panel, "[data-review-conclusion]");
  const editor = reviewElement<HTMLElement>(panel, "[data-review-editor]");
  const history = reviewElement<HTMLDetailsElement>(panel, "[data-review-history]");
  const saved = reviewElement<HTMLElement>(root, "[data-review-saved]");
  const button = (action: string) => reviewElement<HTMLButtonElement>(panel, `[data-review-action="${action}"]`);
  const url = `/api/v1/admin/finding-analysis?${new URLSearchParams({ id, cluster })}`;
  let revisions: Revision[] = [];
  let revision = 0;
  let loaded = false;
  let busy = false;
  let assessment: { input: string; controller: AbortController } | undefined;
  let stale = false;
  let dirty = false;
  let attempted = false;
  const cancel = panel.querySelector<HTMLButtonElement>("[data-review-action=cancel]");

  function updateActions() {
    const reviewingCurrentInput = assessment?.input === observation.value.trim();
    const assessButton = button("assess");
    assessButton.disabled = stale || (busy && (!assessment || reviewingCurrentInput));
    assessButton.textContent = reviewingCurrentInput ? "复核中…" : workspace && !attempted ? "发起 AI 复核" : "重新复核";
    if (cancel) cancel.hidden = !assessment;
    for (const action of ["save", "refresh"]) {
      button(action).disabled = busy || (stale && action !== "refresh");
    }
    button("refresh").hidden = !stale;
    conclusion.readOnly = busy;
    button("save").hidden = !dirty;
    result.setAttribute("aria-busy", String(Boolean(assessment)));
  }

  async function request<T>(action?: string, fields = {}, signal?: AbortSignal): Promise<T> {
    if (workspace) signal = signal ? AbortSignal.any([signal,workspace.signal]) : workspace.signal;
    const response = await fetch(url, action ? {
      method: "POST", headers: { "Content-Type": "application/json" },
      body: JSON.stringify({ action, revision, ...fields }), signal,
    } : { signal });
    signal?.throwIfAborted();
    if (response.status === 409) stale = true;
    if (!response.ok) {
      if (response.headers.get("Content-Type")?.includes("application/json")) {
        const error = await response.json().catch(() => ({})) as { code?: string; upstreamStatus?: number };
        const messages: Record<string, string> = {
          AI_PROVIDER_TIMEOUT: "模型响应超时。观察内容已保留，可以重新复核。",
          AI_REVIEW_DEADLINE_EXCEEDED: "本次复核达到总等待时限。观察内容已保留，请重新复核。",
          AI_PROVIDER_RATE_LIMITED: "模型服务繁忙，请稍后重试。",
          AI_PROVIDER_RESPONSE_INVALID: "模型未返回完整的复核内容，请重试。",
          AI_ENGINE_PERMISSION_DENIED: "无法读取模型配置，请检查 API 的 Engine 和 Secret 读取权限。",
          AI_ENGINE_ENDPOINT_NOT_APPROVED: "模型地址未获批准，请检查 API 的允许地址。",
          AI_PROVIDER_ACCESS_DENIED: "模型服务拒绝访问，请检查模型授权。",
          AI_ENGINE_CONFIG_INVALID: "Engine 的 AI 配置无效，请检查模型、地址和 Secret 引用。",
          AI_ENGINE_UNAVAILABLE: "无法读取 Engine，请检查实例名称、命名空间和集群连接。",
          AI_ENGINE_SECRET_UNAVAILABLE: "无法读取 Secret 或指定密钥项，请检查 Engine 的 Secret 引用。",
          AI_PROVIDER_CONNECT_FAILED: "无法连接模型服务，请检查 API Pod 的出口、DNS 和 TLS。",
          AI_PROVIDER_REQUEST_REJECTED: "模型服务拒绝请求，请核对模型名称和接口地址。",
          AI_PROVIDER_UNAVAILABLE: "模型服务暂不可用，请稍后重试。",
        };
        if (error.code === "AI_PROVIDER_TIMEOUT" && error.upstreamStatus) {
          throw new Error(`模型服务返回超时（HTTP ${error.upstreamStatus}）。观察内容已保留，请重新复核。`);
        }
        if (error.code && Object.hasOwn(messages, error.code)) throw new Error(messages[error.code]);
      }
      if (response.status === 504 && action === "assess") {
        throw new Error("网关等待超时。观察内容已保留，请重试。若仍失败请检查入口代理和 API 的超时配置。");
      }
      const text = response.headers.get("Content-Type")?.includes("text/plain") ? (await response.text()).trim() : "";
      throw new Error(text || "服务暂时无法完成此操作，请重试。");
    }
    return response.json() as Promise<T>;
  }

  function showSaved(item?: Revision) {
    saved.hidden = !item;
    saved.innerHTML = item ? `<h3>已保存复核结论 <small>v${item.revision}</small></h3><p class="detail-text">${escapeHtml(item.conclusion)}</p>` : "";
  }

  function showHistory(entries: Revision[]) {
    revisions = entries;
    revision = entries[0]?.revision ?? 0;
    showSaved(entries[0]);
    history.hidden = entries.length === 0;
    reviewElement(history, "div").innerHTML = entries.map((entry) =>
      `<p class="detail-text"><strong>v${entry.revision}</strong> ${escapeHtml(entry.conclusion)}</p>`).join("");
  }

  async function loadHistory(signal?: AbortSignal) {
    const data = await request<{ history: Revision[]; canPublish: boolean }>(undefined, {}, signal);
    signal?.throwIfAborted();
    showHistory(data.history);
    loaded = true;
    stale = false;
  }

  /** 提交点击时的观察快照，取消的复核不再更新界面。 */
  async function assess(input: string, signal: AbortSignal) {
    if (!loaded) await loadHistory(signal);
    signal.throwIfAborted();
    const data = await request<Assessment>("assess", { observation: input }, signal);
    signal.throwIfAborted();
    result.innerHTML = renderAssessment(data,Boolean(workspace));
    conclusion.value = `${data.assessment.judgment}\n\n待验证项：${data.assessment.gaps}\n下一步：${data.assessment.next}`;
    editor.hidden = false;
    dirty = true;
    status.textContent = input !== observation.value.trim() ? "补充内容已保留，可点击「重新复核」提交。" : "";
  }

  async function run(action: string) {
    const input = observation.value.trim();
    if (stale && action !== "refresh") return;
    if (busy && (action !== "assess" || !assessment || assessment.input === input)) return;
    if (action === "assess" && new TextEncoder().encode(input).length > 4000) {
      status.textContent = "观察内容最多 4000 字节。";
      return;
    }
    const attempt = action === "assess" ? { input, controller: new AbortController() } : undefined;
    assessment?.controller.abort();
    assessment = attempt;
    busy = true;
    updateActions();
    try {
      if (attempt) {
        attempted = true;
        status.textContent = input ? "正在复核补充的信息…" : "正在复核关联证据…";
        await assess(attempt.input, attempt.controller.signal);
        return;
      }
      switch (action) {
        case "refresh":
          await loadHistory();
          status.textContent = "已载入最新版本，请核对后再操作。";
          break;
        case "save": {
          const value = conclusion.value.trim();
          if (!value || new TextEncoder().encode(value).length > 8192) {
            throw new Error("请填写复核结论，最多 8192 字节。");
          }
          const item = await request<Revision>("save", { observation: value });
          showHistory([item, ...revisions].slice(0, 5));
          dirty = false;
          root.querySelector("[data-finding-knowledge]")?.dispatchEvent(new CustomEvent("finding-review-saved", {detail: item.revision}));
          status.textContent = "已保存复核结论";
          break;
        }
      }
    } catch (error) {
      if (!attempt?.controller.signal.aborted && !workspace?.signal.aborted) {
        status.textContent = error instanceof Error ? error.message : "操作失败，请重试。";
      }
    } finally {
      if (!attempt?.controller.signal.aborted && !workspace?.signal.aborted) {
        assessment = undefined;
        busy = false;
        updateActions();
      }
    }
  }

  observation.addEventListener("input", updateActions);
  conclusion.addEventListener("input", () => { dirty = true; updateActions(); });
  trigger.addEventListener("click", () => {
    panel.hidden = !panel.hidden;
    trigger.setAttribute("aria-expanded", String(!panel.hidden));
    if (!panel.hidden && !loaded && !stale) void run("assess");
  });
  panel.addEventListener("click", (event) => {
    if (!(event.target instanceof HTMLElement)) return;
    const control = event.target.closest<HTMLButtonElement>("button");
    if (control?.dataset.reviewAction === "cancel" && assessment) {
      assessment.controller.abort();
      assessment = undefined;
      busy = false;
      status.textContent = "调查已取消，可重新发起。";
      updateActions();
      button("assess").focus();
      return;
    }
    if (control?.dataset.reviewAction) void run(control.dataset.reviewAction);
    if (!control?.dataset.reviewJump) return;
    workspace?.onEvidence();
    const [source, signal] = JSON.parse(control.dataset.reviewJump) as [string, string];
    const evidence = [...root.querySelectorAll<HTMLElement>("[data-evidence-signal]")]
      .find((row) => row.dataset.evidenceSignal === signal && row.dataset.evidenceSource === source);
    const sourceDetails = evidence?.closest<HTMLDetailsElement>("details.evidence-source") ??
      [...root.querySelectorAll<HTMLDetailsElement>("details.evidence-source[data-evidence-source]")]
        .find((details) => details.dataset.evidenceSource === source);
    if (sourceDetails) sourceDetails.open = true;
    const target = source === "K8sGPT" ? root.querySelector<HTMLElement>("[data-finding-explanation]") :
      evidence ?? sourceDetails?.querySelector<HTMLElement>("summary") ?? root.querySelector<HTMLElement>("#correlation-heading");
    target?.focus();
    target?.scrollIntoView({ block: "center" });
  });
}
