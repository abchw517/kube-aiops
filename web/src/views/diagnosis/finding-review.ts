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
}
interface Preview {
  previewId: string;
  document: { title: string; body: string; cluster: string; namespace: string };
}

export const findingReviewTrigger = () => '<button type="button" class="button secondary finding-review-trigger" data-review-action="toggle" aria-controls="finding-review" aria-expanded="false">AI 复核</button>';

export function findingReviewPanel(): string {
  return `<section id="finding-review" class="finding-review" aria-label="AI 证据复核" hidden>
    <p data-review-status role="status"></p>
    <div data-review-result></div>
    <details data-review-input hidden>
      <summary>补充观察或追问</summary>
      <label>现场观察或问题（选填）<textarea data-review-observation rows="3" maxlength="4000" placeholder="例如：10 分钟前 PVC 仍处于 Pending"></textarea></label>
      <button type="button" class="button secondary" data-review-action="assess">重新复核</button>
    </details>
    <div data-review-editor hidden>
      <label>复核结论<textarea data-review-conclusion rows="5" maxlength="8192"></textarea></label>
      <button type="button" class="button" data-review-action="save">保存复核结论</button>
    </div>
    <button type="button" class="button secondary" data-review-action="refresh" hidden>重新载入复核记录</button>
    <button type="button" class="button secondary" data-review-action="preview" hidden>预览知识发布</button>
    <div data-review-preview hidden>
      <h3>知识发布预览</h3>
      <div data-review-document></div>
      <div class="finding-review-actions">
        <button type="button" class="button secondary" data-review-action="cancel-preview">取消</button>
        <button type="button" class="button" data-review-action="publish">确认发布到知识库</button>
      </div>
    </div>
    <details data-review-history hidden><summary>复核历史</summary><div></div></details>
  </section>`;
}

function renderAssessment({ assessment, evidence, sources: states, evidenceState, citations }: Assessment): string {
  const summary = evidenceState === "timeout" ? "关联证据查询超时。" :
    evidenceState === "disabled" ? "未启用关联证据采集。" : "本次未能取得关联证据。";
  return `<h3>当前判断</h3><span class="review-state" data-state="${assessment.state}">${judgments[assessment.state]}</span>
    <p class="detail-text">${escapeHtml(assessment.judgment)}</p>
    <h3>证据依据</h3>
    ${evidence.length ? `<ul class="review-evidence">${evidence.map((item) => {
      const source = item.source === "K8sGPT" ? item.source : sources[item.source];
      return `<li><button class="link-button" type="button" data-review-jump="${escapeHtml(JSON.stringify([item.source, item.signalId ?? ""]))}">${escapeHtml(source)} · ${escapeHtml(item.title)}</button>
        <small>${item.time ? formatTimestamp(item.time) : "时间未知"}${item.historical ? " · 历史记录" : ""}</small></li>`;
    }).join("")}</ul>` : "<p>本次判断没有可定位的引用。</p>"}
    <details><summary>证据来源状态</summary>${evidenceState === "available"
      ? `<ul>${states.map(({ source, state }) => `<li>${sources[source]} · ${availability[state]}</li>`).join("")}</ul>`
      : `<p>${summary}</p>`}</details>
    <h3>待验证项</h3><p class="detail-text">${escapeHtml(assessment.gaps)}</p>
    <h3>下一步</h3><p class="detail-text">${escapeHtml(assessment.next)}</p>
    ${citations?.length ? `<details><summary>Wiki 参考</summary><ul>${citations.map((cite) =>
      `<li><a href="#/wiki/${encodeURIComponent(cite.knowledgeId)}?revision=${cite.revision}">${escapeHtml(cite.title)}</a> · v${cite.revision}</li>`).join("")}</ul></details>` : ""}`;
}

function reviewElement<T extends HTMLElement>(container: HTMLElement, selector: string): T {
  const element = container.querySelector<T>(selector);
  if (!element) throw new Error(`Missing review element: ${selector}`);
  return element;
}

export function bindFindingReview(root: HTMLElement, id: string, cluster: string): void {
  const trigger = root.querySelector<HTMLButtonElement>('[data-review-action="toggle"]');
  const review = root.querySelector<HTMLElement>("#finding-review");
  if (!trigger || !review) return;
  const panel = review;
  const status = reviewElement<HTMLElement>(panel, "[data-review-status]");
  const result = reviewElement<HTMLElement>(panel, "[data-review-result]");
  const observation = reviewElement<HTMLTextAreaElement>(panel, "[data-review-observation]");
  const observationPanel = reviewElement<HTMLDetailsElement>(panel, "[data-review-input]");
  const conclusion = reviewElement<HTMLTextAreaElement>(panel, "[data-review-conclusion]");
  const editor = reviewElement<HTMLElement>(panel, "[data-review-editor]");
  const preview = reviewElement<HTMLElement>(panel, "[data-review-preview]");
  const history = reviewElement<HTMLDetailsElement>(panel, "[data-review-history]");
  const saved = reviewElement<HTMLElement>(root, "[data-review-saved]");
  const button = (action: string) => reviewElement<HTMLButtonElement>(panel, `[data-review-action="${action}"]`);
  const url = `/api/v1/admin/finding-analysis?${new URLSearchParams({ id, cluster })}`;
  let revisions: Revision[] = [];
  let revision = 0;
  let canPublish = false;
  let loaded = false;
  let busy = false;
  let stale = false;
  let dirty = false;
  let previewId = "";

  function updateActions() {
    for (const action of ["assess", "save", "refresh", "preview", "publish", "cancel-preview"]) {
      button(action).disabled = busy || (stale && action !== "refresh");
    }
    button("refresh").hidden = !stale;
    button("preview").hidden = !canPublish || revision === 0;
    observation.readOnly = busy;
    conclusion.readOnly = busy;
    button("save").hidden = !dirty;
    panel.setAttribute("aria-busy", String(busy));
  }

  async function request<T>(action?: string, fields = {}): Promise<T> {
    const response = await fetch(url, action ? {
      method: "POST", headers: { "Content-Type": "application/json" },
      body: JSON.stringify({ action, revision, ...fields }),
    } : undefined);
    if (response.status === 409) stale = true;
    if (!response.ok) {
      if (response.headers.get("Content-Type")?.includes("application/json")) {
        const error = await response.json() as { code?: string };
        const messages: Record<string, string> = {
          AI_PROVIDER_TIMEOUT: "模型响应超时，请重试。",
          AI_PROVIDER_RATE_LIMITED: "模型服务繁忙，请稍后重试。",
          AI_PROVIDER_RESPONSE_INVALID: "模型未返回完整的复核内容，请重试。",
          AI_ENGINE_PERMISSION_DENIED: "无法读取模型配置，请检查 API 的 Engine 和 Secret 读取权限。",
          AI_ENGINE_ENDPOINT_NOT_APPROVED: "模型地址未获批准，请检查 API 的允许地址。",
          AI_PROVIDER_ACCESS_DENIED: "模型服务拒绝访问，请检查模型授权。",
        };
        if (error.code && Object.hasOwn(messages, error.code)) throw new Error(messages[error.code]);
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

  async function loadHistory() {
    const data = await request<{ history: Revision[]; canPublish: boolean }>();
    canPublish = data.canPublish;
    showHistory(data.history);
    loaded = true;
    stale = false;
  }

  async function assess() {
    if (!loaded) await loadHistory();
    observationPanel.hidden = false;
    const input = observation.value.trim();
    if (new TextEncoder().encode(input).length > 4000) throw new Error("观察内容最多 4000 字节。");
    const data = await request<Assessment>("assess", { observation: input });
    result.innerHTML = renderAssessment(data);
    conclusion.value = `${data.assessment.judgment}\n\n待验证项：${data.assessment.gaps}\n下一步：${data.assessment.next}`;
    editor.hidden = false;
    dirty = true;
    status.textContent = "";
  }

  async function run(action: string) {
    if (busy || (stale && action !== "refresh")) return;
    if (action === "cancel-preview") {
      preview.hidden = true;
      button("preview").focus();
      return;
    }
    busy = true;
    updateActions();
    try {
      switch (action) {
        case "assess":
          status.textContent = "正在复核关联证据…";
          await assess();
          break;
        case "refresh":
          await loadHistory();
          preview.hidden = true;
          status.textContent = "已载入最新版本，请核对后再操作。";
          break;
        case "save": {
          const value = conclusion.value.trim();
          if (!value || new TextEncoder().encode(value).length > 8192) {
            throw new Error("请填写复核结论，最多 8192 字节。");
          }
          const item = await request<Revision>("save", { observation: value });
          showHistory([item, ...revisions].slice(0, 5));
          preview.hidden = true;
          dirty = false;
          status.textContent = "已保存复核结论";
          break;
        }
        case "preview": {
          const data = await request<Preview>("preview-publication");
          previewId = data.previewId;
          reviewElement(preview, "[data-review-document]").innerHTML =
            `<p><strong>${escapeHtml(data.document.title)}</strong></p><p>${escapeHtml(data.document.cluster)} / ${escapeHtml(data.document.namespace)}</p>
            <p class="detail-text">${escapeHtml(data.document.body)}</p>`;
          preview.hidden = false;
          status.textContent = "";
          break;
        }
        case "publish": {
          const doc = await request<{ knowledgeId: string }>("publish", { previewId });
          preview.hidden = true;
          status.replaceChildren("已发布到知识库。 ");
          const link = document.createElement("a");
          link.href = `#/wiki/${encodeURIComponent(doc.knowledgeId)}`;
          link.textContent = "查看知识";
          status.append(link);
          break;
        }
      }
    } catch (error) {
      status.textContent = error instanceof Error ? error.message : "操作失败，请重试。";
      if (action === "assess") observationPanel.hidden = false;
    } finally {
      busy = false;
      updateActions();
    }
  }

  conclusion.addEventListener("input", () => { dirty = true; updateActions(); });
  trigger.addEventListener("click", () => {
    panel.hidden = !panel.hidden;
    trigger.setAttribute("aria-expanded", String(!panel.hidden));
    if (!panel.hidden && !loaded && !stale) void run("assess");
  });
  panel.addEventListener("click", (event) => {
    if (!(event.target instanceof HTMLElement)) return;
    const control = event.target.closest<HTMLButtonElement>("button");
    if (control?.dataset.reviewAction) void run(control.dataset.reviewAction);
    if (!control?.dataset.reviewJump) return;
    const [source, signal] = JSON.parse(control.dataset.reviewJump) as [string, string];
    const evidence = [...root.querySelectorAll<HTMLElement>("[data-evidence-signal]")]
      .find((row) => row.dataset.evidenceSignal === signal && row.dataset.evidenceSource === source);
    const target = source === "K8sGPT" ? root.querySelector<HTMLElement>("[data-finding-explanation]") :
      evidence ?? root.querySelector<HTMLElement>("#correlation-heading");
    target?.focus();
    target?.scrollIntoView({ block: "center" });
  });
}
