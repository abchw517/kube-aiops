import { escapeHtml } from "../../components/html.ts";
import { renderOnboarding } from "../clusters/cluster-onboarding.ts";

import { resolveResourceTab, type ResourceAccess } from "../../domain/resource-routes.ts";

type Catalog = "environments" | "business-lines";
type Entry = { id: string; name: string };

export function renderCatalog(kind: Catalog, entries: Entry[], editable = true): string {
  const name = kind === "environments" ? "环境" : "业务线";
  return `<div class="catalog-section" data-catalog-kind="${kind}">
    <h2 id="catalog-title">${name}管理</h2>
    <div class="catalog-table-wrap" role="region" aria-labelledby="catalog-title" tabindex="0"><table class="catalog-table">
      <thead><tr><th scope="col">${name}名称</th><th scope="col">${name} ID</th><th scope="col">操作</th></tr></thead>
      <tbody>${entries.map((entry) => `<tr>
        <td>${escapeHtml(entry.name)}</td><td><code>${escapeHtml(entry.id)}</code></td>
        <td><div class="catalog-actions">${editable ? `
          <button class="button secondary" type="button" data-catalog-action="edit" data-entry-id="${escapeHtml(entry.id)}" data-entry-name="${escapeHtml(entry.name)}">编辑</button>
          <button class="button secondary catalog-danger" type="button" data-catalog-action="delete" data-entry-id="${escapeHtml(entry.id)}" data-entry-name="${escapeHtml(entry.name)}">删除</button>
        ` : "只读"}</div></td>
      </tr>`).join("") || `<tr><td colspan="3" class="catalog-empty">暂无${name}。</td></tr>`}</tbody>
    </table></div>
    <div class="catalog-bottom"><span>共 ${entries.length} 条</span>${editable ? `<button class="button" type="button" data-catalog-action="add">＋ 新增${name}</button>` : ""}</div>
    <dialog class="catalog-dialog" aria-label="${name}编辑与删除" data-catalog-dialog></dialog>
  </div>`;
}

export function handleResourceCatalogClick(event: MouseEvent): void {
  if (!(event.target instanceof Element)) return;
  const button = event.target.closest<HTMLButtonElement>("[data-catalog-action]");
  const section = button?.closest<HTMLElement>("[data-catalog-kind]");
  const dialog = section?.querySelector<HTMLDialogElement>("[data-catalog-dialog]");
  if (!button || !section || !dialog || dialog.querySelector("button:disabled")) return;
  const kind = section.dataset.catalogKind as Catalog;
  const action = button.dataset.catalogAction;
  if (action === "close") {
    dialog.close();
    return;
  }
  const id = button.dataset.entryId ?? "";
  const entryName = button.dataset.entryName ?? "";
  const name = kind === "environments" ? "环境" : "业务线";
  const deleting = action === "delete";
  const editing = action === "edit";
  dialog.innerHTML = `<form data-resource-catalog="${kind}" data-entry-id="${escapeHtml(id)}" data-operation="${deleting ? "delete" : editing ? "edit" : "add"}">
    <header><h2>${deleting ? "删除" : editing ? "编辑" : "新增"}${name}</h2></header>
    <div class="catalog-dialog-body">
      ${deleting ? `<p>确定删除「${escapeHtml(entryName)}」 <code>${escapeHtml(id)}</code>？</p>` : `
        <label>${name}名称<input name="name" value="${escapeHtml(entryName)}" required maxlength="128"></label>
        <label>${name} ID<input name="id" value="${escapeHtml(id)}" required maxlength="128" ${editing ? "" : 'pattern="[a-z][a-z0-9\\-]*"'}></label>
        <small>${editing ? "修改 ID 会保留原有资源引用。新 ID 使用小写字母、数字或连字符，并以字母开头。" : "使用小写字母开头，可包含小写字母、数字或连字符。"}</small>`}
      <p role="status" aria-live="polite"></p>
    </div>
    <footer>
      ${editing ? `<button type="button" class="button secondary catalog-danger catalog-delete" data-catalog-action="delete" data-entry-id="${escapeHtml(id)}" data-entry-name="${escapeHtml(entryName)}">删除</button>` : ""}
      <button type="button" class="button secondary" data-catalog-action="close">取消</button>
      <button type="submit" class="button ${deleting ? "catalog-danger-fill" : ""}"${deleting ? ' value="delete"' : ""}>${deleting ? "确认删除" : "保存"}</button>
    </footer>
  </form>`;
  if (!dialog.open) dialog.showModal();
}

export async function renderResourceManagement(hash: string, access: ResourceAccess, editable = true): Promise<string> {
  const path = hash.split("?", 1)[0] ?? "";
  const selected = resolveResourceTab(path, access);
  if (!selected) return '<p role="alert">无权访问此资源管理页面。</p>';
  const id = path.split("/")[3];
  let content: string;
  if (selected === "clusters") {
    content = await renderOnboarding(id, false, editable);
  } else {
    const response = await fetch(`/api/v1/admin/${selected}`);
    if (!response.ok || !response.headers.get("Content-Type")?.includes("application/json")) {
      throw new Error("resource catalog unavailable");
    }
    content = renderCatalog(selected, await response.json() as Entry[], editable);
  }
  return `<section class="resource-management${selected === "clusters" && !id ? "" : " panel identity-panel"}">
    ${content}
  </section>`;
}

export async function submitResourceCatalog(form: HTMLFormElement, remove: boolean, reload: () => void): Promise<void> {
  remove = remove || form.dataset.operation === "delete";
  const status = form.querySelector<HTMLElement>('[role="status"]');
  const buttons = [...form.querySelectorAll<HTMLButtonElement>("button")];
  const kind = form.dataset.resourceCatalog;
  if (!status || !kind || buttons.some((button) => button.disabled)) return;
  const editing = form.dataset.operation === "edit";
  const data = new FormData(form);
  const id = remove ? form.dataset.entryId : String(data.get("id") ?? "").trim();
  for (const button of buttons) button.disabled = true;
  status.textContent = remove ? "正在删除…" : "正在保存…";
  try {
    const response = await fetch(`/api/v1/admin/${kind}`, {
      method: remove ? "DELETE" : editing ? "PUT" : "POST",
      headers: { "Content-Type": "application/json" },
      body: JSON.stringify(remove ? { id } : { id, name: String(data.get("name") ?? "").trim(), originalId: editing ? form.dataset.entryId : undefined }),
    });
    if (response.ok) {
      form.closest<HTMLDialogElement>("dialog")?.close();
      reload();
      return;
    }
    if (response.status === 409 && response.headers.get("Content-Type")?.includes("application/json")) {
      const conflict = await response.json() as { message: string; references: string[] };
      status.textContent = `${conflict.message}：${conflict.references.join("、")}`;
    } else if (response.headers.get("Content-Type")?.includes("text/plain")) {
      status.textContent = (await response.text()).trim();
    } else {
      status.textContent = "操作失败，请重试。";
    }
  } catch {
    status.textContent = "资源目录请求失败。";
  } finally {
    for (const button of buttons) button.disabled = false;
  }
}
