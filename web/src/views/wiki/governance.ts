import { escapeHtml } from "../../components/html.ts";
import { authorizationDeniedState } from "../../components/states.ts";

export interface Target { cluster: string; namespace: string; environment?: string }
export interface Source { cluster: string; namespace: string; id: string; revision: number }
export interface WikiDocument {
  knowledgeId?: string;
  revision?: number;
  currentRevision?: number;
  title: string;
  cluster: string;
  namespace: string;
  environment: string;
  business: string;
  documentType: string;
  body?: string;
  createdAt?: string;
  matchReason?: string;
  withdrawn?: boolean;
  scopePending?: boolean;
  links?: Source[];
  metadata?: {
    team?: string; reviewBy?: string; reviewedAt?: string; applicability?: string;
    resourceType?: string; targets?: Target[]; signals?: string[]; sections?: Record<string, string>;
    finding?: Source; source?: string;
  };
}
interface Candidate { id: string; knowledgeId: string; version: number; baseRevision: number; status: string; document: WikiDocument; reason?: string }
const url = "/api/v1/wiki";
const h = escapeHtml;
const sections: Record<string, Record<string, string>> = {
  排障手册: { conditions: "适用与排除条件", signals: "匹配信号", checks: "按序检查及预期", recovery: "恢复验证", escalation: "升级路径" },
  故障复盘: { impact: "故障现象与影响", evidence: "已核实证据", conclusion: "根因或待确认项", recovery: "处置与恢复验证" },
  操作规程: { conditions: "前置条件与影响范围", steps: "操作步骤", rollback: "停止条件与回滚", recovery: "验收" },
  常见问题: { question: "问题", answer: "答案", conditions: "适用条件与来源" },
};
const statusLabels: Record<string, string> = { draft: "草稿", review: "待审核", rejected: "已驳回", discarded: "已放弃", published: "已发布" };

export async function wikiJSON<T>(path: string, body?: unknown): Promise<T> {
  const response = await fetch(path, body === undefined ? undefined : {
    method: "POST", headers: { "Content-Type": "application/json" }, body: JSON.stringify(body),
  });
  if (!response.ok) throw new Error((await response.text()).trim() || "知识请求失败");
  return response.json() as Promise<T>;
}

export function knowledgeLink(id: string, revision?: number): string {
  return `#/wiki/${encodeURIComponent(id)}${revision ? `?revision=${revision}` : ""}`;
}

export function renderWikiSections(doc: WikiDocument): string {
  const fields = doc.metadata?.sections ?? {};
  const entries = Object.entries(sections[doc.documentType] ?? {}).filter(([key]) => fields[key]);
  return entries.length ? entries.map(([key, title]) =>
    `<section><h3>${h(title)}</h3><p class="wiki-document-body">${h(fields[key])}</p></section>`).join("") :
    `<p class="wiki-document-body">${h(doc.body ?? "")}</p>`;
}

function chapterFields(type: string, values: Record<string, string> = {}): string {
  return Object.entries(sections[type] ?? {}).map(([key, title]) =>
    `<label>${h(title)}<textarea name="section-${key}" rows="3">${h(values[key] ?? "")}</textarea></label>`).join("");
}

function editor(doc: WikiDocument, candidate?: Candidate, existing?: string): string {
  const metadata = doc.metadata ?? {};
  const targets = metadata.targets?.map((entry) => `${entry.cluster}/${entry.namespace}`).join("\n") ??
    (doc.namespace ? `${doc.cluster}/${doc.namespace}` : "");
  const field = (label: string, name: string, value = "", type = "text") =>
    `<label>${h(label)}<input name="${name}" type="${type}" value="${h(value)}"></label>`;
  return `<section class="panel wiki-card"><a href="#/wiki/governance">返回知识治理</a><h2>${candidate ? "编辑知识草稿" : "新建知识草稿"}</h2>
    ${candidate?.reason ? `<p class="system-notice">${h(candidate.reason)}</p>` : ""}
    <form data-wiki-candidate class="wiki-import-form" data-id="${h(candidate?.id ?? "")}" data-existing="${h(existing ?? "")}" data-version="${candidate?.version ?? 0}" data-base="${doc.revision ?? 0}">
      ${field("标题", "title", doc.title)}<label>类型<select name="documentType"${metadata.finding ? " disabled" : ""}>${Object.keys(sections).map((kind) =>
        `<option${kind === doc.documentType ? " selected" : ""}>${h(kind)}</option>`).join("")}</select></label>
      ${field("资源类型", "resourceType", metadata.resourceType)}${field("责任团队", "team", metadata.team)}${field("下次复核日期", "reviewBy", metadata.reviewBy, "date")}
      ${field("适用与排除条件摘要", "applicability", metadata.applicability)}${field("匹配信号（用顿号分隔）", "signals", metadata.signals?.join("、"))}
      <label>可见目标（每行一个 实例/命名空间）<textarea name="targets" rows="2"${metadata.finding ? " readonly" : ""}>${h(targets)}</textarea></label>
      ${metadata.finding ? `<p>来源诊断 ${h(metadata.finding.id)} · 复核 v${metadata.finding.revision}</p>` : field("来源", "source", metadata.source)}
      <div class="wiki-import-form" data-wiki-chapters>${chapterFields(doc.documentType, metadata.sections)}</div>
      ${doc.body ? `<details><summary>导入或原始正文</summary><textarea name="body" rows="4">${h(doc.body)}</textarea></details>` : ""}
      <div class="system-actions"><button class="button secondary" value="save">保存草稿</button>
        <button class="button secondary" value="preview"${candidate ? "" : " disabled"}>脱敏预览</button>
        <button class="button" value="submit"${candidate ? "" : " disabled"}>提交审核</button>
        ${candidate ? '<button class="button secondary" value="discard">放弃草稿</button>' : ""}</div>
      <p role="status"></p><div data-wiki-preview></div>
    </form></section>`;
}

const emptyDoc: WikiDocument = { title: "", cluster: "local", namespace: "", documentType: "排障手册", environment: "", business: "" };

export async function renderWikiGovernance(path: string, params: URLSearchParams): Promise<string | null> {
  if (path === "#/wiki/governance") {
    const response = await fetch(`${url}/candidates`);
    if (response.status === 401) return null;
    if (!response.ok) return authorizationDeniedState();
    const candidates = await response.json() as Candidate[];
    const published = await wikiJSON<WikiDocument[]>(`${url}/governance`);
    const statuses = ["review", "draft", "rejected", "published", "overdue", "withdrawn", "scopePending"];
    const state = params.get("status") || "review";
    const text: Record<string, string> = { ...statusLabels, overdue: "待复核", withdrawn: "已撤回", scopePending: "范围待确认" };
    const listing = ["published", "overdue", "withdrawn", "scopePending"].includes(state)
      ? published.filter((doc) => state === "withdrawn" ? doc.withdrawn : state === "scopePending" ? doc.scopePending : !doc.withdrawn &&
        (state === "published" || !doc.metadata?.reviewBy || doc.metadata.reviewBy < new Date().toISOString().slice(0, 10)))
        .map((doc) => `<li><a href="#/wiki/manage/${h(doc.knowledgeId)}">${h(doc.title)}</a> · v${doc.revision}${doc.metadata?.team ? ` · ${h(doc.metadata.team)}` : ""}</li>`)
      : candidates.filter((entry) => entry.status === state).map((entry) =>
        `<li><a href="#/wiki/draft/${h(entry.id)}">${h(entry.document.title || "未命名草稿")}</a> · ${statusLabels[entry.status]}${!entry.document.environment ? " · 范围待确认" : ""}</li>`);
    return `<section class="panel wiki-card"><div class="wiki-toolbar"><h2>知识治理</h2><div class="system-actions"><a href="#/wiki">返回知识库</a><a href="#/wiki/draft/new" class="button">新建知识</a></div></div>
      <nav class="system-subtabs" aria-label="知识状态">${statuses.map((item) =>
        `<a href="#/wiki/governance?status=${item}"${state === item ? ' aria-current="page"' : ""}>${text[item]}</a>`).join("")}</nav>
      ${listing.length ? `<ul class="wiki-governance-list">${listing.join("")}</ul>` : "<p>所选状态暂无条目。</p>"}</section>`;
  }
  if (path === "#/wiki/draft/new") {
    const id = params.get("knowledgeId");
    return id ? editor(await wikiJSON<WikiDocument>(`${url}/knowledge/${encodeURIComponent(id)}/governance`), undefined, id) : editor(emptyDoc);
  }
  const id = path.split("/").at(-1) ?? "";
  if (!/^[a-f0-9]{64}$/.test(id)) return authorizationDeniedState();
  if (path.startsWith("#/wiki/manage/")) {
    const doc = await wikiJSON<WikiDocument>(`${url}/knowledge/${id}/governance`);
    return `<section class="panel wiki-card"><a href="#/wiki/governance">返回知识治理</a><h2>${h(doc.title)} · v${doc.revision}</h2>
      ${doc.withdrawn ? "<p>已撤回。仅治理人员可查看历史。</p>" : ""}
      ${renderWikiSections(doc)}
      <a class="button secondary" href="#/wiki/draft/new?knowledgeId=${id}">创建修订</a>
      ${doc.withdrawn ? "" : `<form data-wiki-manage class="wiki-import-form" data-id="${id}" data-revision="${doc.revision}">
        <label>责任团队<input name="team" value="${h(doc.metadata?.team ?? "")}"></label>
        <label>下次复核日期<input name="reviewBy" type="date" value="${h(doc.metadata?.reviewBy ?? "")}"></label>
        <div class="system-actions"><button class="button secondary" value="review">完成复核或移交</button><button class="button catalog-danger secondary" value="withdraw">撤回知识</button></div>
        <p role="status"></p></form>`}</section>`;
  }
  const candidate = await wikiJSON<Candidate>(`${url}/candidates/${id}`);
  if (["draft", "rejected"].includes(candidate.status)) return editor(candidate.document, candidate);
  const doc = candidate.document;
  const old = candidate.baseRevision ? await wikiJSON<WikiDocument>(`${url}/knowledge/${candidate.knowledgeId}/governance`) : undefined;
  return `<section class="panel wiki-card"><a href="#/wiki/governance">返回知识治理</a><h2>${h(doc.title)} · ${statusLabels[candidate.status] ?? ""}</h2>
    <p>责任团队 ${h(doc.metadata?.team)} · ${h(doc.metadata?.applicability)}</p>
    <p>目标范围 ${h(doc.metadata?.targets?.map((target) => `${target.cluster}/${target.namespace}`).join("、"))}</p>
    ${doc.metadata?.targets?.some((target) => target.environment === "production") ? "<p>生产目标须由另一位有权管理员审核。</p>" : ""}
    <div class="wiki-governance-diff">${old ? `<div><h3>当前版本 v${old.revision}</h3>${renderWikiSections(old)}</div>` : ""}
      <div><h3>候选版本</h3>${renderWikiSections(doc)}</div></div>
    ${candidate.status === "review" ? `<form data-wiki-review-candidate class="wiki-import-form" data-id="${id}" data-version="${candidate.version}">
      <label>驳回原因（驳回时必填）<input name="reason"></label>
      <div class="system-actions"><button class="button secondary" value="reject">驳回</button><button class="button" value="publish">确认发布</button></div>
      <p role="status"></p></form>` : ""}</section>`;
}

export function bindWikiGovernance(root: HTMLElement, reload: () => void): void {
  const edit = root.querySelector<HTMLFormElement>("[data-wiki-candidate]");
  edit?.querySelector("select[name=documentType]")?.addEventListener("change", (event) => {
    const select = event.target as HTMLSelectElement;
    const area = edit.querySelector("[data-wiki-chapters]");
    if (area) area.innerHTML = chapterFields(select.value);
  });
  const bind = (selector: string, run: (form: HTMLFormElement, fields: FormData, action: string) => Promise<void>) => {
    const form = root.querySelector<HTMLFormElement>(selector);
    form?.addEventListener("submit", async (event) => {
      event.preventDefault();
      if (form.inert) return;
      const status = form.querySelector("[role=status]");
      const action = event.submitter?.getAttribute("value") ?? "save";
      form.inert = true;
      if (status) status.textContent = "正在处理…";
      try {
        await run(form, new FormData(form), action);
        if (status) status.textContent = "";
      } catch (error) {
        if (status) status.textContent = error instanceof Error ? error.message : "请求失败，可保留内容后重试。";
      } finally { form.inert = false; }
    });
  };
  bind("[data-wiki-candidate]", async (form, fields, action) => {
    const get = (key: string) => String(fields.get(key) ?? "");
    const targets = get("targets").split("\n").map((value) => value.trim()).filter(Boolean)
      .map((value) => { const [cluster, namespace = "", ...rest] = value.split("/"); return {cluster, namespace: rest.length ? "" : namespace}; });
    const document = {
      title: get("title"), documentType: get("documentType") || "故障复盘", body: get("body"), cluster: targets[0]?.cluster ?? "",
      namespace: targets[0]?.namespace ?? "", environment: "", business: "",
      metadata: { targets, resourceType: get("resourceType"), team: get("team"), reviewBy: get("reviewBy"),
        applicability: get("applicability"), signals: get("signals").split(/[、,，]/), source: get("source"),
        sections: Object.fromEntries([...fields].filter(([key]) => key.startsWith("section-")).map(([key, value]) => [key.slice(8), value])) },
    };
    const id = form.dataset.id;
    const result = id ? await wikiJSON<Candidate>(`${url}/candidates/${id}`, { action, document, version: Number(form.dataset.version) }) :
      await wikiJSON<Candidate>(`${url}/candidates`, { document, knowledgeId: form.dataset.existing, baseRevision: Number(form.dataset.base) });
    if (action === "preview") {
      const preview = form.querySelector("[data-wiki-preview]");
      if (preview) preview.innerHTML = `<h3>脱敏预览</h3><p>${h(result.document.title)}</p>${renderWikiSections(result.document)}`;
    } else if (action === "submit" || action === "discard") window.location.hash = "#/wiki/governance";
    else if (!id) window.location.hash = `#/wiki/draft/${result.id}`;
    else reload();
  });
  bind("[data-wiki-review-candidate]", async (form, fields, action) => {
    await wikiJSON(`${url}/candidates/${form.dataset.id}`, { action, version: Number(form.dataset.version), reason: fields.get("reason"), document: {} });
    window.location.hash = "#/wiki/governance";
  });
  bind("[data-wiki-manage]", async (form, fields, action) => {
    if (action === "withdraw" && !window.confirm("确认撤回该版本？撤回后将退出普通阅读、推荐和问答。")) return;
    await wikiJSON(`${url}/knowledge/${form.dataset.id}/governance`, { action, revision: Number(form.dataset.revision), ...Object.fromEntries(fields) });
    reload();
  });
}
