import { bindWikiFeedback, renderFeedback } from "./wiki-feedback.ts";
import { escapeHtml as h, formatTimestamp } from "../../components/html.ts";
import { authorizationDeniedState } from "../../components/states.ts";
import { bindWikiGovernance, knowledgeLink, renderWikiGovernance, renderWikiSections, reviewReminder, wikiSectionKeys, wikiSectionLabel, type WikiDocument } from "./governance.ts";
import { bindWikiTools, renderWikiTools } from "./wiki-tools.ts";

const endpoint = "/api/v1/wiki/knowledge";
const filters = [["environment", "环境"], ["business", "业务线"], ["resourceType", "资源类型"], ["documentType", "知识类型"]] as const;
const typeMarks: Record<string, string> = { 排障手册: "排", 故障复盘: "复", 操作规程: "程", 常见问题: "问" };
const keywords = ["CrashLoopBackOff", "内存", "FailedScheduling", "证书"];
const searchFields = ["q", ...filters.map(([key]) => key), "sort", "page", "cursor"];

export type WikiAccess = { draft: boolean; candidates: boolean; governance: boolean; ask: boolean };
type WikiPage = { items: WikiDocument[]; filters: Record<string, string[]>; page: number; pageSize: number; total: number; nextCursor?: string };

function listQuery(query: URLSearchParams): URLSearchParams {
  const result = new URLSearchParams();
  for (const key of searchFields) {
    const value = query.get(key);
    if (value && (key !== "sort" || value === "recent")) result.set(key, value);
  }
  return result;
}

function resultsHref(query: URLSearchParams): string {
  const value = listQuery(query);
  return value.size ? `#/wiki?${value}` : "#/wiki";
}

function startAtFirstPage(query: URLSearchParams): URLSearchParams {
  const result = new URLSearchParams(query);
  result.delete("cursor");
  result.delete("page");
  return result;
}

function documentHref(id: string, revision?: number, section?: string, back?: URLSearchParams): string {
  const target = knowledgeLink(id, revision, section);
  if (!back?.size) return target;
  return target + (target.includes("?") ? "&" : "?") + new URLSearchParams({ return: back.toString() });
}

function scopeLabel(doc: WikiDocument): string {
  return [doc.environment, doc.business, doc.metadata?.resourceType].filter(Boolean).map(h).join(" / ") || "待补齐";
}

function metadataLine(doc: WikiDocument): string {
  return `<span><strong>适用</strong> ${scopeLabel(doc)}</span>
    <span>责任团队 ${h(doc.metadata?.team || "待补齐")}</span><span>v${h(doc.revision ?? 1)}</span>
    <span class="wiki-document-time">更新于 ${formatTimestamp(doc.createdAt)}</span>`;
}

function quickSections(doc: WikiDocument): string {
  const metadata = doc.metadata;
  const sections = metadata?.sections;
  const blocks = [
    ["适用条件", metadata?.applicability || sections?.conditions],
    ["先查什么", sections?.checks || sections?.steps || sections?.evidence],
    ["恢复判断", sections?.recovery || sections?.rollback],
  ];
  return blocks.filter(([, text]) => text?.trim()).map(([label, text]) =>
    `<section class="wiki-preview-block"><h3>${label}</h3><p class="wiki-document-body">${h(text)}</p></section>`).join("");
}

function renderDocument(id: string, doc: WikiDocument, query: URLSearchParams, canDraft: boolean): string {
  const metadata = doc.metadata;
  const keys = wikiSectionKeys(doc);
  const requested = query.get("section") ?? "";
  const active = keys.includes(requested) ? requested : "";
  const reminder = reviewReminder(metadata?.reviewBy);
  const sources = [metadata?.finding, ...(doc.links ?? [])].filter((src) => src !== undefined);
  const back = listQuery(new URLSearchParams(query.get("return") ?? ""));
  return `<article class="panel wiki-card wiki-reading"><div class="wiki-toolbar">
    <a class="button secondary" href="${h(resultsHref(back))}">返回检索结果</a>
    ${canDraft ? `<a class="button secondary" href="#/wiki/draft/new?knowledgeId=${h(id)}">创建修订</a>` : ""}</div>
    <header class="wiki-reading-head"><h2>${h(doc.title)}</h2><div class="wiki-result-meta">${metadataLine(doc)}</div></header>
    ${doc.currentRevision && doc.currentRevision !== doc.revision ? `<p class="system-notice">这是历史版本。<a href="${h(documentHref(id, undefined, undefined, back))}">查看当前版本</a></p>` : ""}
    ${metadata?.reviewBy ? `<p class="muted">下次复核 ${h(metadata.reviewBy)}</p>` : ""}
    ${reminder ? `<p class="system-notice">${h(reminder)}，使用前请核对适用范围。</p>` : ""}
    ${metadata?.applicability ? `<section class="wiki-quick-check"><h3>适用条件</h3><p>${h(metadata.applicability)}</p>
      ${metadata.sections?.checks ? `<h3>先查什么</h3><p class="wiki-document-body">${h(metadata.sections.checks)}</p>` : ""}
      ${metadata.sections?.recovery ? `<h3>恢复判断</h3><p class="wiki-document-body">${h(metadata.sections.recovery)}</p>` : ""}</section>` : ""}
    ${metadata?.targets?.length ? `<p>可见目标 ${metadata.targets.map(target => h(`${target.cluster}/${target.namespace}`)).join("、")}</p>` : ""}
    ${requested && !active ? '<p role="status">此版本没有所选章节，已显示全文。</p>' : ""}
    ${keys.length > 1 ? `<nav class="system-subtabs" aria-label="知识章节">${keys.map(key =>
      `<a href="${h(documentHref(id, doc.revision, key, back))}"${active === key ? ' aria-current="location"' : ""}>${h(wikiSectionLabel(key, doc.documentType))}</a>`).join("")}</nav>` : ""}
    ${renderWikiSections(doc)}
    ${renderFeedback(id, doc.revision ?? 1, active)}
    ${sources.length ? `<section><h3>关联诊断</h3><ul>${sources.map(source =>
      `<li><a href="#/findings/${encodeURIComponent(source.id)}?cluster=${encodeURIComponent(source.cluster)}">${h(source.id)}</a> · 复核 v${h(source.revision)}</li>`).join("")}</ul></section>` : ""}
  </article>`;
}

function highlighted(value: string, query: string): string {
  const term = query.trim();
  if (!term) return h(value);
  const escaped = term.replace(/[.*+?^\u0024{}()|[\]\\]/g, "\\$&");
  return value.split(new RegExp(`(${escaped})`, "gi")).map((part, index) =>
    index % 2 ? `<mark>${h(part)}</mark>` : h(part)).join("");
}

function renderResult(doc: WikiDocument, query: URLSearchParams): string {
  const id = doc.knowledgeId ?? "";
  const term = query.get("q") ?? "";
  const reminder = reviewReminder(doc.metadata?.reviewBy);
  const heading = highlighted(doc.title, term);
  const typeMark = typeMarks[doc.documentType] ?? "文";
  return `<li class="wiki-result">
    <span class="wiki-type-mark" aria-hidden="true">${typeMark}</span>
    <div class="wiki-result-body"><div class="wiki-result-title"><h3>${id ? `<a href="${h(documentHref(id, doc.revision, undefined, query))}">${heading}</a>` : heading}</h3>
      <span class="wiki-result-tag">${h(doc.documentType)}</span>
      ${reminder ? `<span class="wiki-result-tag wiki-review-tag">${h(reminder)}</span>` : ""}</div>
      ${doc.metadata?.applicability ? `<p>${highlighted(doc.metadata.applicability, term)}</p>` : ""}
      <div class="wiki-result-meta">${metadataLine(doc)}</div>
      ${doc.matchReason ? `<p class="wiki-match">${highlighted(doc.matchReason, term)}</p>` : ""}</div>
    ${id ? `<button type="button" class="button secondary wiki-preview-button" data-wiki-preview-id="${h(id)}"
      data-wiki-revision="${h(doc.revision ?? "")}" data-wiki-title="${h(doc.title)}" aria-label="快速查看 ${h(doc.title)}">快速查看</button>` : ""}</li>`;
}

function renderSearch(data: WikiPage, query: URLSearchParams, admin: boolean, access?: WikiAccess): string {
  const pageHref = (page: number) => {
    const next = startAtFirstPage(query);
    if (page === data.page + 1 && data.nextCursor) next.set("cursor", data.nextCursor);
    else if (page > 1) next.set("page", String(page));
    return h(resultsHref(next));
  };
  const active = [["q", "关键词"], ...filters];
  const term = query.get("q") ?? "";
  const filterOnly = startAtFirstPage(query);
  for (const [key] of filters) filterOnly.delete(key);
  const chips = active.flatMap(([key, label]) => {
    const value = query.get(key);
    if (!value) return [];
    const next = startAtFirstPage(query);
    next.delete(key);
    return [`<a class="wiki-chip" href="${h(resultsHref(next))}" aria-label="移除 ${label} ${h(value)}">${label} · ${h(value)} <span aria-hidden="true">×</span></a>`];
  });
  return `<div class="wiki-workspace" data-wiki-search-workspace>
    <section class="panel wiki-search" aria-label="检索知识">
      <div class="wiki-toolbar"><label for="wiki-search-term">按症状、报错或事件 Reason 查找排障知识</label>
        <div class="system-actions">
          ${admin || access?.ask !== false ? '<button type="button" class="button secondary" data-open-wiki-ask>AI 知识问答</button>' : ""}
          ${admin || access?.draft ? '<a class="button secondary" href="#/wiki/draft/new">新建知识</a>' : ""}
          ${admin || access?.candidates || access?.governance ? '<a class="button secondary" href="#/wiki/governance">知识治理</a>' : ""}
        </div>
      </div>
      <form data-wiki-search class="wiki-search-form" role="search">
        <input type="search" id="wiki-search-term" aria-label="搜索知识" name="q" maxlength="512" value="${h(term)}" placeholder="例如 CrashLoopBackOff、内存、网关 5xx">
        <button type="submit" class="button">搜索</button>
      </form>
      <div class="wiki-suggestions"><small>常用关键词</small>${keywords.map(keyword => {
        const next = startAtFirstPage(query);
        next.set("q", keyword);
        return `<a class="wiki-chip" href="${h(resultsHref(next))}">${h(keyword)}</a>`;
      }).join("")}</div>
    </section>
    <div class="wiki-results-layout">
      <details class="panel wiki-facet-card" data-wiki-facet-card open><summary>筛选条件</summary>
        <form class="wiki-filters" data-wiki-filters>
          ${filters.map(([key,label]) => {
            const value = query.get(key) ?? "";
            const options = [...new Set([...(data.filters[key] ?? []), ...(value ? [value] : [])])];
            return `<label>${label}<select name="${key}"><option value="">全部${label}</option>${options.map(option =>
              `<option value="${h(option)}"${value === option ? " selected" : ""}>${h(option)}</option>`).join("")}</select></label>`;
          }).join("")}
          <div class="system-actions"><button class="button" type="submit">应用</button><a href="${h(resultsHref(filterOnly))}">清除筛选</a></div>
        </form>
      </details>
      <section class="panel wiki-list-card" aria-labelledby="wiki-list-title">
        <header class="wiki-toolbar"><div><h2 id="wiki-list-title" tabindex="-1">知识文档</h2><p class="wiki-result-count">共 ${data.total} 篇</p></div>
          <label class="wiki-sort">结果排序<select data-wiki-sort>
            <option value="">${term ? "相关度优先" : "默认排序"}</option>
            <option value="recent"${query.get("sort") === "recent" ? " selected" : ""}>最近更新</option>
          </select></label></header>
        ${chips.length ? `<div class="wiki-applied" aria-label="已应用筛选">${chips.join("")}</div>` : ""}
        ${data.items.length ? `<ul class="wiki-document-list">${data.items.map(doc => renderResult(doc, query)).join("")}</ul>` :
          `<div class="wiki-empty"><strong>${chips.length ? "暂无符合筛选条件的文档" : "知识库暂无文档"}</strong>
            <p>${chips.length ? "尝试缩短关键词或移除筛选条件。" : "已发布的知识会显示在此处。"}</p>
            ${chips.length ? '<a href="#/wiki">清除全部条件</a>' : ""}</div>`}
        ${data.page > 1 || data.page * data.pageSize < data.total ? `<nav class="pagination" aria-label="文档分页">
          ${data.page > 1 ? `<a class="button secondary" href="${pageHref(data.page - 1)}">上一页</a>` : ""}
          <span class="wiki-result-count">第 ${data.page} / ${Math.max(1, Math.ceil(data.total / data.pageSize))} 页</span>
          ${data.page * data.pageSize < data.total ? `<a class="button secondary" href="${pageHref(data.page + 1)}">下一页</a>` : ""}</nav>` : ""}
      </section>
    </div>
    <dialog class="wiki-preview" data-wiki-preview-drawer aria-labelledby="wiki-preview-title">
      <header><h2 id="wiki-preview-title" tabindex="-1">知识预览</h2>
        <button type="button" class="zabbix-close" aria-label="关闭" data-wiki-preview-close>×</button></header>
      <div class="wiki-preview-body" data-wiki-preview-body></div>
      <footer><button type="button" class="button secondary" data-wiki-preview-close>关闭</button>
        <a class="button" data-wiki-preview-read hidden>阅读全文</a></footer>
    </dialog>
    <section class="wiki-management" aria-label="知识工具">${renderWikiTools(admin)}</section>
  </div>`;
}

/** 返回 null 时由 Portal 处理身份认证。 */
export async function renderWiki(hash: string, admin: boolean, access?: WikiAccess): Promise<string | null> {
  const allowed = admin ? undefined : access;
  const [path, search] = hash.split("?", 2);
  const query = new URLSearchParams(search);
  if (/^#\/wiki\/(governance|draft\/|manage\/)/.test(path ?? "")) return renderWikiGovernance(path ?? "", query, allowed);
  const id = path?.match(/^#\/wiki\/([a-f0-9]{64})$/)?.[1];
  const revision = Number(query.get("revision")) || 0;
  const active = listQuery(query);
  const back = listQuery(new URLSearchParams(query.get("return") ?? ""));
  const response = await fetch(id ? `${endpoint}/${id}${revision ? `/revisions/${revision}` : ""}` : `${endpoint}?${active}`);
  if (response.status === 401) return null;
  if (response.status === 403) return authorizationDeniedState();
  if (!response.ok) return `<section class="panel wiki-card" role="alert">无法读取知识库内容。<a href="${h(resultsHref(id ? back : startAtFirstPage(active)))}">返回检索结果</a></section>`;
  return id ? renderDocument(id, await response.json() as WikiDocument, query, admin || access?.draft === true) :
    renderSearch(await response.json() as WikiPage, active, admin, access);
}

function bindPreview(workspace: HTMLElement, query: URLSearchParams, reload: () => void): void {
  const drawer = workspace.querySelector<HTMLDialogElement>("[data-wiki-preview-drawer]");
  const heading = drawer?.querySelector<HTMLElement>("h2");
  const body = drawer?.querySelector<HTMLElement>("[data-wiki-preview-body]");
  const read = drawer?.querySelector<HTMLAnchorElement>("[data-wiki-preview-read]");
  if (!drawer || !heading || !body || !read) return;
  let controller: AbortController | undefined;
  let opener: HTMLButtonElement | undefined;

  async function open(button: HTMLButtonElement) {
    const id = button.dataset.wikiPreviewId ?? "";
    if (!/^[a-f0-9]{64}$/.test(id) || !drawer || !heading || !body || !read) return;
    controller?.abort();
    const request = new AbortController();
    controller = request;
    opener = button;
    const version = Number(button.dataset.wikiRevision) || undefined;
    heading.textContent = button.dataset.wikiTitle ?? "知识预览";
    read.hidden = true;
    body.innerHTML = '<p role="status">正在读取知识预览…</p>';
    if (!drawer.open) drawer.showModal();
    heading.focus();
    const current = () => !request.signal.aborted && drawer.isConnected && drawer.open;
    try {
      const response = await fetch(`${endpoint}/${id}${version ? `/revisions/${version}` : ""}`, { signal: request.signal });
      if (!current()) return;
      if (response.status === 401) { reload(); return; }
      if (response.status === 403 || response.status === 404) {
        body.innerHTML = '<p role="status">文档暂不可查看，请关闭预览后刷新结果。</p>';
        return;
      }
      if (!response.ok) throw new Error("知识预览加载失败");
      const doc = await response.json() as WikiDocument;
      if (!current()) return;
      heading.textContent = doc.title;
      const warning = reviewReminder(doc.metadata?.reviewBy);
      body.innerHTML = `<div class="wiki-result-meta">${metadataLine(doc)}</div>
        ${warning ? `<p class="system-notice">${h(warning)}，使用前请核对适用范围。</p>` : ""}
        ${quickSections(doc) || `<section class="wiki-preview-block"><h3>正文摘录</h3><p class="wiki-document-body">${h(doc.body?.slice(0, 420) || "打开全文查看文档内容。")}${(doc.body?.length ?? 0) > 420 ? "…" : ""}</p></section>`}`;
      read.href = documentHref(id, doc.revision ?? version, undefined, query);
      read.hidden = false;
    } catch {
      if (current()) body.innerHTML = '<p role="status">无法读取预览，请重试。</p><button class="button secondary" type="button" data-wiki-preview-retry>重试</button>';
    }
  }

  workspace.addEventListener("click", event => {
    if (!(event.target instanceof Element)) return;
    const button = event.target.closest<HTMLButtonElement>("button");
    if (button?.hasAttribute("data-wiki-preview-id")) void open(button);
    else if (button?.hasAttribute("data-wiki-preview-close")) drawer.close();
    else if (button?.hasAttribute("data-wiki-preview-retry") && opener) void open(opener);
  });
  drawer.addEventListener("close", () => {
    if (drawer.open) return;
    controller?.abort();
    if (opener?.isConnected) opener.focus({ preventScroll: true });
  });
}

export function bindWiki(root: HTMLElement, reload: () => void): void {
  bindWikiTools(root);
  bindWikiFeedback(root);
  const query = new URLSearchParams(window.location.hash.split("?", 2)[1]);
  const section = query.get("section") ?? "";
  const target = /^[a-z]+$/.test(section) ? document.getElementById(`wiki-section-${section}`) : null;
  if (target && root.contains(target)) {
    target.focus({ preventScroll: true });
    target.scrollIntoView({ block: "start" });
  }
  bindWikiGovernance(root, reload);
  const workspace = root.querySelector<HTMLElement>("[data-wiki-search-workspace]");
  if (!workspace) return;
  const active = listQuery(query);
  bindPreview(workspace, active, reload);
  const facets = workspace.querySelector<HTMLDetailsElement>("[data-wiki-facet-card]");
  if (facets && window.matchMedia("(max-width: 720px)").matches) facets.open = false;

  const search = workspace.querySelector<HTMLFormElement>("[data-wiki-search]");
  const input = search?.querySelector<HTMLInputElement>('[name="q"]');
  input?.addEventListener("input", () => input.setCustomValidity(""));
  search?.addEventListener("submit", event => {
    event.preventDefault();
    const term = input?.value.trim() ?? "";
    if (input && new TextEncoder().encode(term).length > 512) {
      input.setCustomValidity("搜索词过长，请缩短后重试。");
      input.reportValidity();
      return;
    }
    const next = startAtFirstPage(active);
    if (term) next.set("q", term);
    else next.delete("q");
    window.location.hash = resultsHref(next);
  });
  workspace.querySelector<HTMLFormElement>("[data-wiki-filters]")?.addEventListener("submit", event => {
    event.preventDefault();
    if (!(event.currentTarget instanceof HTMLFormElement)) return;
    const next = startAtFirstPage(active);
    const values = new FormData(event.currentTarget);
    for (const [key] of filters) {
      const value = values.get(key);
      if (typeof value === "string" && value) next.set(key, value);
      else next.delete(key);
    }
    window.location.hash = resultsHref(next);
  });
  workspace.querySelector<HTMLSelectElement>("[data-wiki-sort]")?.addEventListener("change", event => {
    if (!(event.currentTarget instanceof HTMLSelectElement)) return;
    const next = startAtFirstPage(active);
    if (event.currentTarget.value === "recent") next.set("sort", "recent");
    else next.delete("sort");
    window.location.hash = resultsHref(next);
  });
}
