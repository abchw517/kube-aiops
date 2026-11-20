import { escapeHtml as h } from "../../components/html.ts";
import { knowledgeLink, type WikiDocument, wikiJSON } from "../wiki/governance.ts";

interface Link { knowledgeId: string; title: string; wikiRevision: number }
interface Knowledge { items: WikiDocument[]; links: Link[]; status: string; canDraft: boolean }

export function findingKnowledgePanel(): string {
  return '<section class="panel correlation-panel" data-finding-knowledge aria-label="相关知识"><h2>相关知识</h2><div data-knowledge-content aria-live="polite">正在查找相关知识…</div></section>';
}

export function bindFindingKnowledge(root: HTMLElement, id: string, cluster: string, savedRevision = 0): void {
  const panel = root.querySelector<HTMLElement>("[data-finding-knowledge]");
  const area = panel?.querySelector<HTMLElement>("[data-knowledge-content]");
  if (!area) return;
  const content = area;
  const endpoint = `/api/v1/wiki/finding?${new URLSearchParams({ id, cluster })}`;
  let revision = savedRevision;
  let current: Knowledge | undefined;
  let pending = false;
  let query = "";
  const render = () => {
    if (!current) return;
    const docs = current.items;
    const handbooks = docs.filter((doc) => doc.documentType === "排障手册");
    const links = current.links;
    content.innerHTML = docs.length ? `<ul class="wiki-governance-list">${docs.map((doc) =>
      `<li><a href="${knowledgeLink(doc.knowledgeId ?? "", doc.revision)}">${h(doc.title)}</a> · ${h(doc.matchReason ?? "")}
        <small>${h(doc.metadata?.applicability ?? "")}</small></li>`).join("")}</ul>` :
      `<p>${current.status === "unresolved" ? "诊断范围待确认。" : "当前无匹配知识。"} <a href="#/wiki">搜索知识库</a></p>`;
    if (links.length) content.insertAdjacentHTML("beforeend", `<p>已关联 ${links.map((link) =>
      `<a href="${knowledgeLink(link.knowledgeId, link.wikiRevision)}">${h(link.title)} v${link.wikiRevision}</a>`).join("、")}</p>`);
    if (!current.canDraft) return;
    content.insertAdjacentHTML("beforeend", revision < 1 ? "<p>保存复核结论后可沉淀知识。</p>" :
      `<details><summary>沉淀知识</summary><div class="system-actions">
       <button type="button" class="button" data-knowledge-action="draft">创建故障案例草稿</button>
       ${handbooks.map((doc) => `<button type="button" class="button secondary" data-knowledge-action="link" data-id="${h(doc.knowledgeId)}">关联 ${h(doc.title)}</button>`).join("")}</div>
       <form data-link-search class="wiki-filters"><label>查找其他可关联手册<input name="q" type="search" maxlength="512" value="${h(query)}"></label>
       <button class="button secondary">搜索手册</button></form></details>`);
    content.insertAdjacentHTML("beforeend", '<p data-knowledge-status role="status"></p>');
  };
  const load = async () => {
    try {
      const data = await wikiJSON<Knowledge>(query ? `${endpoint}&q=${encodeURIComponent(query)}` : endpoint);
      if (!content.isConnected) return;
      current = data;
      render();
    } catch {
      if (content.isConnected) content.innerHTML = '<p>暂时无法获取相关知识。 <button class="button secondary" type="button" data-knowledge-action="retry">重试</button></p>';
    }
  };
  panel?.addEventListener("finding-review-saved", (event) => {
    if (!content.isConnected || !(event instanceof CustomEvent)) return;
    revision = Number(event.detail);
    render();
  });
  content.addEventListener("submit", async (event) => {
    if (!(event.target instanceof HTMLFormElement) || !event.target.hasAttribute("data-link-search")) return;
    event.preventDefault();
    query = String(new FormData(event.target).get("q") ?? "").trim();
    await load();
    const tools = content.querySelector("details");
    if (tools) tools.open = true;
  });
  content.addEventListener("click", async (event) => {
    if (!(event.target instanceof HTMLButtonElement) || pending) return;
    const button = event.target;
    const action = button.dataset.knowledgeAction;
    if (action === "retry") { await load(); return; }
    if (action !== "draft" && action !== "link") return;
    const doc = current?.items.find((item) => item.knowledgeId === button.dataset.id);
    const status = content.querySelector("[data-knowledge-status]");
    pending = true;
    button.disabled = true;
    try {
      const result = await wikiJSON<{ id: string }>(endpoint, {action, reviewRevision: revision, knowledgeId: doc?.knowledgeId, wikiRevision: doc?.revision});
      if (!content.isConnected) return;
      if (action === "draft") window.location.hash = `#/wiki/draft/${encodeURIComponent(result.id)}`;
      else await load();
    } catch (error) {
      if (status) status.textContent = error instanceof Error ? error.message : "操作失败，请重试。";
    } finally { button.disabled = false; pending = false; }
  });
  void load();
}
