import { escapeHtml as h } from "../../components/html.ts";
import type { ResourceAccess } from "../../domain/resource-routes.ts";
import { bindCatalogDialog } from "./catalog-dialog.ts";
import { type Catalog, catalogDefinition, type CatalogEntry, type CatalogFilters, catalogKinds, filterCatalog } from "./catalog-model.ts";
import { bindResourceMenus, resourceCell, restoreResourceFocus } from "./resource-workspace.ts";

export type CatalogPage = { kind: Catalog; items: CatalogEntry[]; failed: boolean; editable: boolean; access: ResourceAccess };

export async function loadCatalog(kind: Catalog): Promise<CatalogEntry[]> {
  const response = await fetch(`/api/v1/admin/${kind}`);
  if (!response.ok || !response.headers.get("Content-Type")?.includes("application/json")) throw response;
  const items: CatalogEntry[] = await response.json();
  if (!Array.isArray(items) || items.some(item => !item || typeof item.id !== "string" || typeof item.name !== "string")) {
    throw new Error("invalid catalog response");
  }
  return items;
}

function catalogRows(items: CatalogEntry[], page: CatalogPage): string {
  const { noun } = catalogDefinition(page.kind);
  return items.map(row => `<tr data-entry-id="${h(row.id)}"><td><button type="button" class="resource-name" data-catalog-action="detail">${h(row.name)}</button></td>
    ${resourceCell(`${noun} ID`, `<div class="catalog-id"><code>${h(row.id)}</code>
      <button type="button" class="button secondary catalog-quiet" data-catalog-action="copy" aria-label="复制 ${h(row.name)} 的 ID">复制</button></div>`)}
    <td><div class="catalog-actions">${page.editable ? `<button type="button" class="button secondary catalog-quiet" data-catalog-action="edit">编辑</button>
      <details class="resource-row-menu"><summary aria-label="${h(row.name)}的更多操作">⋯</summary><div>
        <button type="button" class="button secondary catalog-danger" data-catalog-action="delete">删除</button></div></details>` :
      '<button type="button" class="button secondary catalog-quiet" data-catalog-action="detail">查看详情</button>'}</div></td></tr>`).join("");
}

const addButton = (noun: string) => `<button type="button" class="button" data-catalog-action="add">＋ 新增${noun}</button>`;

function emptyMarkup(page: CatalogPage, filtered: boolean): string {
  const { noun } = catalogDefinition(page.kind);
  if (page.failed) return '<h3>目录加载失败</h3><p>重新加载后可继续查看该目录。</p><button type="button" class="button secondary" data-catalog-action="retry">重新加载</button>';
  if (filtered) return '<h3>没有匹配的条目</h3><p>换个关键词，或清空搜索查看完整目录。</p><button type="button" class="button secondary" data-catalog-action="clear">清空搜索</button>';
  return `<h3>暂无${noun}</h3><p>${page.editable ? "新增后可在对应资源模块使用。" : "目录中还没有已登记的条目。"}</p>`;
}

export function renderCatalog(page: CatalogPage): string {
  const { noun, title, purpose, group } = catalogDefinition(page.kind);
  return `<section class="resource-catalog" data-catalog-kind="${page.kind}">
    <header class="resource-page-head"><div><h1 id="catalog-title">${h(title)}</h1><p>${h(purpose)}</p></div>
      ${page.editable ? addButton(noun) : ""}</header>
    <div class="panel resource-list-panel">
      <nav class="resource-view-tabs catalog-tabs" aria-label="${h(group)}">
        ${catalogKinds.filter(kind => page.access[kind]).map(kind => {
          const target = catalogDefinition(kind);
          return `<a href="${h(target.route)}"${kind === page.kind ? ' aria-current="page"' : ""}>${h(target.title)}</a>`;
        }).join("")}
      </nav>
      <div class="resource-list-body">
        <div class="resource-filters">
          <label>搜索<input type="search" data-catalog-search placeholder="搜索名称或 ID"></label>
          <label>排序<select data-catalog-sort><option value="sequence">目录顺序</option><option value="name">按名称</option><option value="id">按 ID</option></select></label>
          <button type="button" class="button secondary catalog-quiet" data-catalog-action="clear">清空搜索</button>
        </div>
        <p class="resource-result"><span data-catalog-count role="status"></span>${page.editable ? "" : "<span>只读权限</span>"}</p>
        <div class="resource-table-wrap" data-catalog-table${page.items.length ? "" : " hidden"}>
          <table class="resource-table catalog-directory" aria-labelledby="catalog-title">
            <thead><tr><th scope="col">${noun}名称</th><th scope="col">${noun} ID</th><th scope="col">操作</th></tr></thead>
            <tbody>${catalogRows(page.items, page)}</tbody>
          </table>
        </div>
        <div class="resource-empty" data-catalog-empty${page.items.length ? " hidden" : ""}>${emptyMarkup(page, false)}</div>
        <p class="resource-notice" data-catalog-notice role="status" hidden></p>
      </div>
    </div>
    <dialog class="resource-drawer" data-catalog-detail aria-labelledby="catalog-detail-title"></dialog>
    <dialog class="resource-modal catalog-editor" data-catalog-dialog aria-labelledby="catalog-dialog-title"></dialog>
    <dialog class="resource-modal catalog-discard" data-catalog-discard aria-labelledby="catalog-discard-title">
      <h2 id="catalog-discard-title">放弃未保存的修改？</h2><p>关闭后，这次填写的内容会丢弃。</p>
      <div class="catalog-actions"><button type="button" class="button secondary" data-keep-draft autofocus>继续编辑</button>
        <button type="button" class="button" data-discard-draft>放弃修改</button></div>
    </dialog>
  </section>`;
}

export function bindResourceCatalog(root: HTMLElement, page: CatalogPage, filters: CatalogFilters): void {
  const region = root.querySelector<HTMLElement>("[data-catalog-kind]");
  const search = region?.querySelector<HTMLInputElement>("[data-catalog-search]");
  const sort = region?.querySelector<HTMLSelectElement>("[data-catalog-sort]");
  const table = region?.querySelector<HTMLElement>("[data-catalog-table]");
  const body = region?.querySelector<HTMLTableSectionElement>("tbody");
  const empty = region?.querySelector<HTMLElement>("[data-catalog-empty]");
  const count = region?.querySelector<HTMLElement>("[data-catalog-count]");
  const notice = region?.querySelector<HTMLElement>("[data-catalog-notice]");
  if (!region || !search || !sort || !table || !body || !empty || !count || !notice) return;
  const { noun } = catalogDefinition(page.kind);
  let loading = false;
  const report = (message: string) => { notice.hidden = !message; notice.textContent = message; };
  const paint = () => {
    const visible = filterCatalog(page.items, filters);
    const hasQuery = Boolean(filters.query.trim());
    table.hidden = loading || page.failed || !visible.length;
    body.innerHTML = catalogRows(visible, page);
    empty.hidden = loading || (!page.failed && Boolean(visible.length));
    empty.innerHTML = empty.hidden ? "" : emptyMarkup(page, Boolean(page.items.length && hasQuery));
    count.textContent = loading ? "正在加载目录…" : page.failed ? "暂时无法统计" : `显示 ${visible.length} / ${page.items.length} 个${noun}`;
    search.disabled = sort.disabled = loading || page.failed;
    const clear = region.querySelector<HTMLButtonElement>(".resource-filters [data-catalog-action=clear]");
    if (clear) clear.disabled = !hasQuery || search.disabled;
    const add = region.querySelector<HTMLButtonElement>("[data-catalog-action=add]");
    if (add) add.disabled = loading || page.failed;
  };
  const controller = bindCatalogDialog(region, page, search, report, (action, old, saved) => {
    if (action === "delete") page.items = page.items.filter(row => row.id !== old?.id);
    else if (saved) page.items = old ? page.items.map(row => row.id === old.id ? saved : row) : [...page.items, saved];
    paint();
    if (action === "delete") report(`已删除「${old?.name}」。`);
    else if (saved) {
      const verb = action === "add" ? "新增" : "保存";
      const matches = filterCatalog([saved], filters).length > 0;
      report(`已${verb}「${saved.name}」。${matches ? "" : "当前搜索未匹配此条目，可清空搜索查看。"}`);
    }
  });
  bindResourceMenus(region);
  search.value = filters.query;
  sort.value = filters.sort;
  paint();
  search.addEventListener("input", () => { filters.query = search.value; report(""); paint(); });
  sort.addEventListener("change", () => {
    if (sort.value === "sequence" || sort.value === "name" || sort.value === "id") filters.sort = sort.value;
    paint();
  });
  region.addEventListener("click", async (event) => {
    if (!(event.target instanceof Element)) return;
    const button = event.target.closest<HTMLButtonElement>("button[data-catalog-action]");
    if (!button) return;
    const action = button.dataset.catalogAction;
    if (action === "clear") {
      search.value = filters.query = "";
      report("");
      paint();
      search.focus();
      return;
    }
    if (action === "retry" && !loading) {
      loading = true;
      paint();
      try {
        page.items = await loadCatalog(page.kind);
        page.failed = false;
      } catch {
        page.failed = true;
      } finally {
        loading = false;
        if (region.isConnected) {
          paint();
          restoreResourceFocus(page.failed ? region.querySelector("[data-catalog-action=retry]") : search, null);
        }
      }
      return;
    }
    const row = page.items.find(item => item.id === button.closest<HTMLElement>("[data-entry-id]")?.dataset.entryId);
    if (action === "detail" && row) controller?.showDetail(row, button);
    if (action === "copy" && row) await controller?.copy(row.id, button);
    if (!page.editable || page.failed) return;
    if (action === "add" || ((action === "edit" || action === "delete") && row)) {
      const menu = button.closest<HTMLDetailsElement>(".resource-row-menu");
      const origin = menu?.querySelector("summary") ?? button;
      if (menu) menu.open = false;
      controller?.open(action, row, origin);
    }
  });
}
