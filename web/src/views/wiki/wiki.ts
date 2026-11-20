import { bindWikiFeedback, renderFeedback } from "./wiki-feedback.ts";
import { escapeHtml, formatTimestamp } from "../../components/html.ts";

import { authorizationDeniedState } from "../../components/states.ts";

import { bindWikiGovernance, knowledgeLink, renderWikiGovernance, renderWikiSections, reviewReminder, wikiSectionKeys, wikiSectionLabel, type WikiDocument } from "./governance.ts";
import { bindWikiTools, renderWikiTools } from "./wiki-tools.ts";

const endpoint = "/api/v1/wiki/knowledge";
const filters = [["environment", "环境"], ["business", "业务线"], ["resourceType", "资源类型"], ["documentType", "知识类型"]] as const;

/** 渲染知识库页面，返回 null 表示应用需要显示登录界面。 */
export type WikiAccess = { draft: boolean; candidates: boolean; governance: boolean; ask: boolean };

export async function renderWiki(hash: string, admin: boolean, access?: WikiAccess): Promise<string | null> {
  const allowed = admin ? undefined : access;
  const [path, search] = hash.split("?", 2);
  const id = path?.match(/^#\/wiki\/([a-f0-9]{64})$/)?.[1];
  const query = new URLSearchParams(search);
  if (/^#\/wiki\/(governance|draft\/|manage\/)/.test(path ?? "")) return renderWikiGovernance(path ?? "", query, allowed);
  const revision = Number(query.get("revision")) || 0;
  const response = await fetch(id ? `${endpoint}/${id}${revision ? `/revisions/${revision}` : ""}` : `${endpoint}?${query}`);
  if (response.status === 401) return null;
  if (response.status === 403) return authorizationDeniedState();
  if (!response.ok) return '<section class="panel" role="alert">无法读取知识库内容。<a href="#/wiki">返回知识库</a></section>';
  if (id) {
    const doc = await response.json() as WikiDocument;
    const metadata = doc.metadata;
    const keys = wikiSectionKeys(doc);
    const requested = query.get("section") ?? "";
    const active = keys.includes(requested) ? requested : "";
    const reminder = reviewReminder(metadata?.reviewBy);
    const sources = [metadata?.finding, ...(doc.links ?? [])].filter((src) => src !== undefined);
    return `<section class="panel wiki-card"><div class="wiki-toolbar"><a href="#/wiki">返回知识库</a>
      ${admin || access?.draft ? `<a class="button secondary" href="#/wiki/draft/new?knowledgeId=${id}">创建修订</a>` : ""}</div>
      <h2>${escapeHtml(doc.title)}</h2><p>${escapeHtml(doc.environment)} / ${escapeHtml(doc.business || "无业务线")} / ${escapeHtml(doc.documentType)} · v${doc.revision ?? 1}</p>
      ${doc.currentRevision && doc.currentRevision !== doc.revision ? `<p class="system-notice">这是历史版本。<a href="${knowledgeLink(id)}">查看当前版本</a></p>` : ""}
      <p>责任团队 ${escapeHtml(metadata?.team ?? "待补齐")} · 下次复核 ${escapeHtml(metadata?.reviewBy ?? "历史待复核")}</p>
      ${metadata?.applicability ? `<section class="wiki-quick-check"><h3>适用条件</h3><p>${escapeHtml(metadata.applicability)}</p>
        ${metadata.sections?.checks ? `<h3>先查什么</h3><p class="wiki-document-body">${escapeHtml(metadata.sections.checks)}</p>` : ""}
        ${metadata.sections?.recovery ? `<h3>恢复判断</h3><p class="wiki-document-body">${escapeHtml(metadata.sections.recovery)}</p>` : ""}</section>` : ""}
      ${metadata?.targets?.length ? `<p>可见目标 ${metadata.targets.map((target) => escapeHtml(`${target.cluster}/${target.namespace}`)).join("、")}</p>` : ""}
      ${reminder ? `<p class="system-notice">${escapeHtml(reminder)}，使用前请核对适用范围。</p>` : ""}
      ${requested && !active ? '<p role="status">此版本没有所选章节，已显示全文。</p>' : ""}
      ${keys.length > 1 ? `<nav class="system-subtabs" aria-label="知识章节">${keys.map((key) =>
        `<a href="${knowledgeLink(id, doc.revision, key)}"${active === key ? ' aria-current="location"' : ""}>${escapeHtml(wikiSectionLabel(key, doc.documentType))}</a>`).join("")}</nav>` : ""}
      ${renderWikiSections(doc)}
      ${renderFeedback(id, doc.revision ?? 1, active)}
      ${sources.length ? `<section><h3>关联诊断</h3><ul>${sources.map((source) =>
        `<li><a href="#/findings/${encodeURIComponent(source.id)}?cluster=${encodeURIComponent(source.cluster)}">${escapeHtml(source.id)}</a> · 复核 v${source.revision}</li>`).join("")}</ul></section>` : ""}
    </section>`;
  }
  const data = await response.json() as {items: WikiDocument[]; filters: Record<string, string[]>; page: number; pageSize: number; total: number; nextCursor?: string};
  const pageLink = (page: number) => {
    const next = new URLSearchParams(query);
    next.delete("cursor");
    if (page === data.page + 1 && data.nextCursor) next.set("cursor", data.nextCursor);
    else next.set("page", String(page));
    return escapeHtml(next.toString());
  };
  return `<div class="wiki-workspace"><section class="panel wiki-card" aria-labelledby="wiki-list-title">
    <div class="wiki-toolbar"><h2 id="wiki-list-title">知识文档 <small>${data.total} 篇</small></h2>
      <div class="system-actions">${allowed?.ask !== false ? '<button type="button" class="button secondary" data-open-wiki-ask>AI 知识问答</button>' : ""}
      ${admin || access?.draft ? '<a class="button" href="#/wiki/draft/new">新建知识</a>' : ""}
      ${admin || access?.candidates || access?.governance ? '<a class="button secondary" href="#/wiki/governance">知识治理</a>' : ""}</div></div>
    <form data-wiki-filters class="wiki-filters"><label>搜索知识<input type="search" name="q" maxlength="512" value="${escapeHtml(query.get("q") ?? "")}" placeholder="报错、事件 Reason 或中文症状"></label>
      ${filters.map(([key, label]) => `<label>${label}<select name="${key}"><option value="">全部</option>${
        (data.filters[key] ?? []).map((value) => `<option value="${escapeHtml(value)}"${query.get(key) === value ? " selected" : ""}>${escapeHtml(value)}</option>`).join("")
      }</select></label>`).join("")}
      <button type="submit" class="button">搜索</button>
      <a class="button secondary" href="#/wiki">重置</a>
    </form>
    ${data.items.length ? `<div class="table-wrap" role="region" aria-labelledby="wiki-list-title" tabindex="0"><table><thead><tr><th scope="col">标题</th><th scope="col">环境</th><th scope="col">业务线</th><th scope="col">类型</th><th scope="col">更新时间</th></tr></thead><tbody>
    ${data.items.map((doc) => `<tr><td><a href="${knowledgeLink(doc.knowledgeId ?? "")}">${escapeHtml(doc.title)}</a><p>${escapeHtml(doc.metadata?.applicability ?? "历史条目")}</p>${doc.matchReason ? `<small>${escapeHtml(doc.matchReason)}</small>` : ""}</td>
      <td>${escapeHtml(doc.environment)}</td><td>${escapeHtml(doc.business)}</td><td>${escapeHtml(doc.documentType)}</td><td>${formatTimestamp(doc.createdAt)}</td></tr>`).join("")}
    </tbody></table></div>` : `<div class="wiki-empty"><strong>${query.size ? "暂无符合筛选条件的文档" : "知识库暂无文档"}</strong><p>${query.size ? "可重置筛选查看全部文档。" : "已发布的知识会显示在此处。"}</p></div>`}
    ${data.page > 1 || data.page * data.pageSize < data.total ? `<nav class="pagination" aria-label="文档分页">
      ${data.page > 1 ? `<a class="button secondary" href="#/wiki?${pageLink(data.page - 1)}">上一页</a>` : ""}
      ${data.page * data.pageSize < data.total ? `<a class="button secondary" href="#/wiki?${pageLink(data.page + 1)}">下一页</a>` : ""}
    </nav>` : ""}
  </section>
  <section class="wiki-management" aria-label="知识工具">${renderWikiTools(admin)}</section></div>`;
}

export function bindWiki(root: HTMLElement, reload: () => void): void {
  bindWikiTools(root);
  bindWikiFeedback(root);
  const section = new URLSearchParams(window.location.hash.split("?", 2)[1]).get("section");
  const target = section && /^[a-z]+$/.test(section) ? document.getElementById(`wiki-section-${section}`) : null;
  if (target && root.contains(target)) {
    target.focus({ preventScroll: true });
    target.scrollIntoView({ block: "start" });
  }
  bindWikiGovernance(root, reload);
  root.querySelector<HTMLFormElement>("[data-wiki-filters]")?.addEventListener("submit", (event) => {
    event.preventDefault();
    const form = event.currentTarget as HTMLFormElement;
    const query = new URLSearchParams();
    for (const [key, value] of new FormData(form)) if (value) query.set(key, String(value));
    window.location.hash = `#/wiki?${query}`;
  });

}
