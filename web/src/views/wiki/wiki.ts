import { escapeHtml, formatTimestamp } from "../../components/html.ts";

import { authenticationRequiredState, authorizationDeniedState } from "../../components/states.ts";

import { bindWikiTools, renderWikiTools } from "./wiki-tools.ts";

const endpoint = "/api/v1/wiki/knowledge";
const filters = [["environment", "环境"], ["business", "业务线"], ["documentType", "类型"]] as const;

interface WikiDocument {
  knowledgeId?: string;
  revision?: number;
  title: string;
  cluster: string;
  namespace: string;
  environment: string;
  business: string;
  documentType: string;
  body?: string;
  createdAt?: string;
}

export async function renderWiki(hash: string, admin: boolean): Promise<string> {
  const [path, search] = hash.split("?", 2);
  const id = path?.match(/^#\/wiki\/([a-f0-9]{64})$/)?.[1];
  const query = new URLSearchParams(search);
  const revision = Number(query.get("revision")) || 1;
  const response = await fetch(id ? `${endpoint}/${id}/revisions/${revision}` : `${endpoint}?${query}`);
  if (response.status === 401) return authenticationRequiredState();
  if (response.status === 403) return authorizationDeniedState();
  if (!response.ok) return '<section class="panel" role="alert">无法读取知识库内容。<a href="#/wiki">返回知识库</a></section>';
  if (id) {
    const doc = await response.json() as WikiDocument;
    return `<section class="panel"><a href="#/wiki">返回知识库</a>
      <h2>${escapeHtml(doc.title)}</h2><p>${escapeHtml(doc.environment)} / ${escapeHtml(doc.business)} / ${escapeHtml(doc.documentType)}</p>
      <div class="wiki-document-body">${escapeHtml(doc.body)}</div>
      <details><summary>订正文档</summary><form data-wiki-revision class="wiki-import-form">
      <input type="hidden" name="revision" value="${doc.revision ?? revision}">
      <label>标题<input name="title" value="${escapeHtml(doc.title)}" required></label>
      <label>正文<textarea name="body" required rows="8">${escapeHtml(doc.body)}</textarea></label>
      <p role="status"></p><button type="submit" class="button">确认发布修订</button>
      </form></details></section>`;
  }
  const data = await response.json() as {items: WikiDocument[]; filters: Record<string, string[]>; page: number; pageSize: number; total: number};
  const pageLink = (page: number) => {
    const next = new URLSearchParams(query);
    next.set("page", String(page));
    return escapeHtml(next.toString());
  };
  return `<section class="panel">
    <form data-wiki-filters class="filters">
      ${filters.map(([key, label]) => `<label>${label}<select name="${key}"><option value="">全部</option>${
        (data.filters[key] ?? []).map((value) => `<option value="${escapeHtml(value)}"${query.get(key) === value ? " selected" : ""}>${escapeHtml(value)}</option>`).join("")
      }</select></label>`).join("")}
      <button type="submit" class="button">筛选</button>
    </form>
    ${data.items.length ? `<div class="table-wrap"><table><thead><tr><th>标题</th><th>环境</th><th>业务线</th><th>类型</th><th>更新时间</th></tr></thead><tbody>
    ${data.items.map((doc) => `<tr><td><a href="#/wiki/${encodeURIComponent(doc.knowledgeId ?? "")}?revision=${doc.revision ?? 1}">${escapeHtml(doc.title)}</a></td>
      <td>${escapeHtml(doc.environment)}</td><td>${escapeHtml(doc.business)}</td><td>${escapeHtml(doc.documentType)}</td><td>${formatTimestamp(doc.createdAt)}</td></tr>`).join("")}
    </tbody></table></div>` : "<p>暂无符合条件的文档。</p>"}
    ${data.page > 1 ? `<a href="#/wiki?${pageLink(data.page - 1)}">上一页</a>` : ""}
    ${data.page * data.pageSize < data.total ? `<a href="#/wiki?${pageLink(data.page + 1)}">下一页</a>` : ""}
  </section>
  <section class="panel"><details><summary>新增知识</summary>
    <form data-wiki-import class="wiki-import-form">
      <label>集群实例<input name="cluster" required value="local"></label>
      <label>命名空间<input name="namespace" required></label>
      <label>环境<input name="environment" required></label>
      <label>业务线<input name="business" required></label>
      <label>类型<select name="documentType"><option>排障手册</option><option>故障复盘</option><option>操作规程</option><option>常见问题</option></select></label>
      <label>标题<input name="title" placeholder="留空时从正文提取" maxlength="256"></label>
      <label>导入 Markdown 或 TXT<input data-wiki-file type="file" accept=".md,.markdown,.txt"></label>
      <label>正文<textarea name="body" rows="8" required></textarea></label>
      <p data-wiki-status role="status"></p>
      <div><button class="button secondary" type="submit">预览并整理</button> <button class="button" type="submit" value="publish" disabled>确认发布</button></div>
    </form>
  </details></section>${renderWikiTools(admin)}`;
}

export function bindWiki(root: HTMLElement, reload: () => void): void {
  bindWikiTools(root);
  root.querySelector<HTMLFormElement>("[data-wiki-filters]")?.addEventListener("submit", (event) => {
    event.preventDefault();
    const form = event.currentTarget as HTMLFormElement;
    const query = new URLSearchParams();
    for (const [key, value] of new FormData(form)) if (value) query.set(key, String(value));
    window.location.hash = `#/wiki?${query}`;
  });

  const form = root.querySelector<HTMLFormElement>("[data-wiki-import]");
  const status = form?.querySelector<HTMLElement>("[data-wiki-status]");
  const publishButton = form?.querySelector<HTMLButtonElement>('button[value="publish"]');
  if (!form || !status || !publishButton) return;
  form.addEventListener("input", () => {
    publishButton.disabled = true;
    status.textContent = "内容已修改，请重新预览。";
  });
  form.querySelector<HTMLInputElement>("[data-wiki-file]")?.addEventListener("change", async (event) => {
    const input = event.currentTarget as HTMLInputElement;
    const file = input.files?.[0];
    const body = form.elements.namedItem("body");
    if (!file || !(body instanceof HTMLTextAreaElement)) return;
    if (!/\.(md|markdown|txt)$/i.test(file.name) || file.size > 48 * 1024) {
      status.textContent = "请选择不超过 48 KiB 的 Markdown 或 TXT 文件。";
      return;
    }
    try {
      const content = await file.text();
      if (input.files?.[0] === file) {
        body.value = content;
        publishButton.disabled = true;
        status.textContent = "文件已读入，请预览后发布。";
      }
    } catch {
      status.textContent = "无法读取文件。";
    }
  });
  form.addEventListener("submit", async (event) => {
    event.preventDefault();
    const publish = event.submitter === publishButton;
    const fields = Object.fromEntries(new FormData(form));
    const buttons = Array.from(form.querySelectorAll("button"));
    form.inert = true;
    for (const button of buttons) button.disabled = true;
    status.textContent = publish ? "正在保存…" : "正在整理…";
    try {
      const response = await fetch("/api/v1/wiki/imports", {
        method: "POST",
        headers: {"Content-Type": "application/json"},
        body: JSON.stringify({document: fields, publish}),
      });
      if (!response.ok) {
        status.textContent = await response.text();
        return;
      }
      const result = await response.json() as WikiDocument;
      if (publish) {
        reload();
        return;
      }
      for (const [name, value] of Object.entries(result)) {
        const field = form.elements.namedItem(name);
        if (field instanceof HTMLInputElement || field instanceof HTMLTextAreaElement || field instanceof HTMLSelectElement) field.value = String(value);
      }
      status.textContent = "已整理为 Wiki 文档，请核对后发布。";
      publishButton.disabled = false;
    } catch {
      status.textContent = "请求失败，请重试。";
    } finally {
      form.inert = false;
      for (const button of buttons) if (button !== publishButton) button.disabled = false;
    }
  });
}
