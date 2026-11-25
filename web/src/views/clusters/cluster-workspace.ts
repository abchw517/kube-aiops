import { escapeHtml as h } from "../../components/html.ts";
import { clusterManagementTitle } from "../../domain/resource-routes.ts";
import { bindResourceMenus, resourceCell, resourceDetail, resourceMetric, resourceOptions, resourceState, restoreResourceFocus } from "../resources/resource-workspace.ts";
import { canRecheckCollection, type ClusterEntry, clusterCategory, collectionCaption, collectionStatus, displayTime, renderCollection, renderCollectionRecheck } from "./cluster-records.ts";

const modes: Record<string, string> = { local: "本地自动采集", pull: "外部直连", push: "采集上报" };
const states = [
  ["", "全部状态"], ["issue", "需处理"], ["waiting", "等待采集"], ["normal", "已接入"], ["paused", "已停用"], ["removed", "已解除连接"],
] as const;

const environment = (item: ClusterEntry) => String(item.environment || "未设置环境");
const title = (item: ClusterEntry) => String(item.group || item.name);

function editor(item: ClusterEntry, action: string, label: string): string {
  return `<button type="button" class="button secondary" data-cluster-editor="${action}-${h(encodeURIComponent(item.id))}">${label}</button>`;
}
function maintenance(item: ClusterEntry, action: string, label: string): string {
  return `<button type="button" class="button secondary${action === "delete" ? " catalog-danger" : ""}" data-cluster-action="${action}" data-id="${h(item.id)}" data-label="${h(title(item))}">${label}</button>`;
}
function canAssign(item: ClusterEntry): boolean {
  return !item.deleted && item.enabled && (item.mode !== "pull" || Boolean(item.credentialKind));
}
function actionMenu(item: ClusterEntry): string {
  const actions = item.deleted ? (item.mode === "local" ? maintenance(item, "reconnect", "重新连接") : "") :
    editor(item, "edit", item.mode === "local" ? "编辑归属" : item.mode === "pull" && !item.credentialKind ? "补录凭据" : "编辑配置") +
    (canAssign(item) ? editor(item, "ownership", "命名空间归属") : "") +
    (canRecheckCollection(item) ? maintenance(item, "recheck", "重新校验") : "") +
    (item.mode === "push" ? maintenance(item, "rotate-token", "轮换上报凭据") : "") +
    (item.mode === "pull" && item.credentialKind ? editor(item, "rotate", "轮换直连凭据") : "") +
    maintenance(item, "delete", "删除连接");
  return actions ? `<details class="resource-row-menu"><summary aria-label="${h(title(item))}的更多操作">⋯</summary><div>${actions}</div></details>` : "";
}

function nodes(item: ClusterEntry): string {
  if (item.deleted || !item.enabled) return "—";
  const data = item.collection;
  if (!data?.nodes?.length) return resourceState(data?.nodeError ? "未获取节点" : "未采集");
  if (data.nodeError || (data.nodeFailures ?? 0) >= 2) return resourceState("历史快照", "waiting");
  const ready = data.nodes.filter(node => node.ready === "True").length;
  if (data.nodes.some(node => node.ready === "False")) return `${resourceState(`${ready} / ${data.nodes.length} Ready`, "issue")}<small>有 NotReady 节点</small>`;
  if (ready !== data.nodes.length) return resourceState("含 Unknown 节点", "waiting");
  return resourceState(`${ready} / ${data.nodes.length} Ready`, "good");
}

export function renderClusterDetail(item: ClusterEntry, editable: boolean): string {
  const category = clusterCategory(item);
  return resourceDetail("cluster-detail-title", title(item), item.name === title(item) ? item.id : `${item.name} · ${item.id}`,
    `<div class="resource-detail-intro">${resourceState(collectionCaption(item), category === "issue" ? "issue" : category === "waiting" ? "waiting" : "neutral")}
    <p>${h(environment(item))} · ${h(item.business || "待补齐")} · ${h(modes[item.mode])}</p></div>${renderCollection(item)}`,
    `<span class="muted">时间 · UTC+8</span><div class="catalog-actions">
      ${!item.deleted && !item.collection?.namespaceError && item.collection?.namespaces?.length ?
        `<button type="button" class="button secondary" data-cluster-namespaces="${h(item.id)}" data-environment="${h(environment(item))}">查看命名空间</button>` : ""}
      ${editable && canAssign(item) ? editor(item, "ownership", "命名空间归属") : ""}
      ${editable ? renderCollectionRecheck(item) : ""}</div>`);
}

function clusterRows(items: ClusterEntry[], editable: boolean): string {
  return items.map(item => {
    const category = clusterCategory(item);
    const caption = collectionCaption(item);
    const time = item.mode === "push" ? item.reportedAt : item.checkedAt || item.collection?.nodeSampleAt;
    return `<tr data-cluster-row data-cluster="${h(item.id)}" data-environment="${h(environment(item))}" data-mode="${h(item.mode)}"
        data-category="${h(category)}" data-status="${h(collectionStatus(item))}" data-search="${h([item.group, item.name, item.id].filter(Boolean).join(" "))}"
        data-namespace-error="${item.collection?.namespaceError ? "true" : "false"}" data-namespaces="${item.collection?.namespaces?.length ?? 0}">
      <td><button type="button" class="resource-name" data-cluster-details="${h(item.id)}">${h(title(item))}</button>
        <small><code>${h(item.id)}</code> · ${item.clusterType === "dedicated" ? "独立业务集群" : "共用集群"}</small></td>
      ${resourceCell("环境 / 业务线", `${h(environment(item))}<small>${h(item.business || "待补齐")}</small>`)}
      ${resourceCell("接入方式", h(modes[item.mode]))}
      ${resourceCell("采集状态", resourceState(caption, caption === "已采集" || category === "normal" ? "good" : category === "issue" ? "issue" : category === "waiting" ? "waiting" : "neutral"))}
      ${resourceCell("节点状态", nodes(item))}
      ${resourceCell("最近活动", `${time ? displayTime(time) : "—"}<small>${item.mode === "push" ? "最近上报" : item.checkedAt ? "最近检查" : "节点采样"}</small>`)}
      <td><div class="catalog-actions"><button type="button" class="button secondary" data-cluster-details="${h(item.id)}">采集详情</button>${editable ? actionMenu(item) : ""}</div>
        <template data-collection-template>${renderClusterDetail(item, editable)}</template></td>
      </tr>`;
  }).join("");
}
function namespaceRows(items: ClusterEntry[]): string {
  return items.flatMap(item => (item.deleted || item.collection?.namespaceError ? [] : item.collection?.namespaces ?? []).map(({ name }) =>
    `<tr data-namespace-row data-cluster="${h(item.id)}" data-environment="${h(environment(item))}" data-search="${h(name)}">
      <td><strong>${h(name)}</strong></td>
      ${resourceCell("集群 / 实例 ID", `<button type="button" class="resource-name" data-cluster-details="${h(item.id)}">${h(title(item))}</button><small>${h(item.id)}</small>`)}
      ${resourceCell("环境", h(environment(item)))}</tr>`)).join("");
}

export function renderClusterWorkspace(items: ClusterEntry[], editable: boolean): string {
  return `<section class="resource-workspace catalog-section" data-resource-workspace data-editable="${editable}">
    <header class="resource-page-head"><div><h1 id="cluster-list-title">${clusterManagementTitle}</h1><p>查看集群采集状态，管理接入和命名空间归属。</p></div>
      ${editable ? '<button type="button" class="button" data-cluster-editor="choose">＋ 接入集群</button>' : ""}</header>
    <div class="panel resource-list-panel">
      <nav class="resource-view-tabs" aria-label="资源视图">
        <button type="button" data-resource-tab="clusters" aria-pressed="true">集群列表</button>
        <button type="button" data-resource-tab="namespaces" aria-pressed="false">命名空间</button>
      </nav>
      <div class="resource-list-body">
        <div class="resource-summary" data-cluster-summary>${resourceMetric("all", "全部集群")}${resourceMetric("issue", "需处理")}${resourceMetric("waiting", "等待采集")}</div>
        <div class="resource-filters">
          <label>搜索<input type="search" data-cluster-filter="query" placeholder="集群名称、实例名称或 ID"></label>
          <label>环境<select data-cluster-filter="environment"><option value="">全部环境</option>${resourceOptions([...new Set(items.map(environment))].map(name => [name, name]))}</select></label>
          <label data-cluster-only>接入方式<select data-cluster-filter="mode"><option value="">全部方式</option>${resourceOptions(Object.entries(modes))}</select></label>
          <label data-cluster-only>状态<select data-cluster-filter="status">${resourceOptions(states.map(([key,label]) => [key,label]))}</select></label>
          <label data-namespace-only hidden>集群实例<select data-cluster-filter="cluster"></select></label>
          <button type="button" class="button secondary" data-cluster-clear>清空筛选</button>
        </div>
        <p class="resource-result"><span data-cluster-count aria-live="polite">共 ${items.length} 个集群</span><span data-cluster-only>时间 · UTC+8</span></p>
        <p class="resource-notice" data-namespace-notice hidden role="status"></p>
        <div class="resource-table-wrap" data-resource-table="clusters"><table class="resource-table cluster-directory" aria-label="集群列表">
          <thead><tr><th scope="col">集群 / 实例 ID</th><th scope="col">环境 / 业务线</th><th scope="col">接入方式</th><th scope="col">采集状态</th><th scope="col">节点状态</th><th scope="col">最近活动</th><th scope="col">操作</th></tr></thead>
          <tbody data-cluster-rows>${clusterRows(items, editable)}</tbody>
        </table></div>
        <div class="resource-table-wrap" data-resource-table="namespaces" hidden><table class="resource-table" aria-label="命名空间列表">
          <thead><tr><th scope="col">命名空间</th><th scope="col">集群 / 实例 ID</th><th scope="col">环境</th></tr></thead>
          <tbody data-namespace-rows>${namespaceRows(items)}</tbody>
        </table></div>
        <div class="resource-empty" data-cluster-empty hidden><h3></h3><p></p>
          ${editable ? '<button type="button" class="button" data-cluster-editor="choose">接入集群</button>' : ""}
          <button type="button" class="button secondary" data-cluster-clear>清空筛选</button>
        </div>
      </div>
    </div>
    <dialog class="catalog-dialog" data-cluster-dialog aria-labelledby="cluster-dialog-title"></dialog>
    <dialog class="resource-drawer" data-cluster-drawer aria-labelledby="cluster-detail-title"></dialog>
  </section>`;
}

const workspace = (target: EventTarget | null) => target instanceof Element ? target.closest<HTMLElement>("[data-resource-workspace]") : null;
function selection(): URLSearchParams {
  return new URLSearchParams(location.hash.split("?")[1]);
}
function storeSelection(params: URLSearchParams): void {
  const path = location.hash.split("?")[0] || "#/resources/clusters";
  const query = params.toString();
  history.replaceState(null, "", path + (query ? `?${query}` : ""));
}

function applyFilters(region: HTMLElement, params = selection()): void {
  const namespaceView = params.get("view") === "namespaces";
  const env = params.get("environment") ?? "";
  const query = params.get(namespaceView ? "namespaceQuery" : "query") ?? "";
  const state = params.get("status") ?? "";
  const mode = params.get("mode") ?? "";
  const cluster = params.get("cluster") ?? "";
  for (const button of region.querySelectorAll<HTMLButtonElement>("[data-resource-tab]")) {
    button.setAttribute("aria-pressed", String(button.dataset.resourceTab === (namespaceView ? "namespaces" : "clusters")));
  }
  for (const node of region.querySelectorAll<HTMLElement>("[data-cluster-only], [data-cluster-summary], [data-namespace-only]")) {
    node.hidden = node.hasAttribute("data-namespace-only") ? !namespaceView : namespaceView;
  }
  const all = [...region.querySelectorAll<HTMLTableRowElement>("[data-cluster-row]")];
  const scoped = all.filter(row => !env || row.dataset.environment === env);
  const scopeOptions = region.querySelector<HTMLSelectElement>('[data-cluster-filter="cluster"]');
  if (scopeOptions) {
    scopeOptions.innerHTML = `<option value="">全部集群</option>${resourceOptions(scoped.filter(row => row.dataset.category !== "removed")
      .map(row => [row.dataset.cluster ?? "", `${row.querySelector(".resource-name")?.textContent} · ${row.dataset.cluster}`]))}`;
  }
  for (const [key, value] of Object.entries({ query, environment: env, mode, status: state, cluster })) {
    const field = region.querySelector<HTMLInputElement | HTMLSelectElement>(`[data-cluster-filter="${key}"]`);
    if (!field) continue;
    if (field instanceof HTMLSelectElement && value && !Array.from(field.options).some(option => option.value === value)) {
      if (key !== "cluster" && key !== "environment") continue;
      field.add(new Option(value, value));
    }
    if (field.value !== value) field.value = value;
    if (field instanceof HTMLInputElement) field.placeholder = namespaceView ? "搜索命名空间" : "集群名称、实例名称或 ID";
  }

  const needle = query.trim().toLocaleLowerCase();
  const matches = (row: HTMLElement) => (row.dataset.search ?? "").toLocaleLowerCase().includes(needle);
  const range = scoped.filter(row => (!mode || row.dataset.mode === mode) && matches(row));
  const visibleClusters = new Set(range.filter(row => !state || row.dataset.category === state));
  for (const row of all) row.hidden = !visibleClusters.has(row);
  const nsRange = scoped.filter(row => row.dataset.category !== "removed" && (!cluster || row.dataset.cluster === cluster));
  const nsRows = [...region.querySelectorAll<HTMLTableRowElement>("[data-namespace-row]")];
  const nsIds = new Set(nsRange.map(row => row.dataset.cluster));
  const visibleNamespaces = nsRows.filter(row => nsIds.has(row.dataset.cluster) && matches(row));
  const shownNamespaces = new Set(visibleNamespaces);
  for (const row of nsRows) row.hidden = !shownNamespaces.has(row);

  const count = namespaceView ? visibleNamespaces.length : visibleClusters.size;
  for (const table of region.querySelectorAll<HTMLElement>("[data-resource-table]")) {
    table.hidden = !count || table.dataset.resourceTable !== (namespaceView ? "namespaces" : "clusters");
  }
  for (const button of region.querySelectorAll<HTMLButtonElement>("[data-resource-metric]")) {
    const key = button.dataset.resourceMetric;
    button.setAttribute("aria-pressed", String((state || "all") === key));
    const number = button.querySelector("strong");
    if (number) number.textContent = String(key === "all" ? range.length : range.filter(row => row.dataset.category === key).length);
  }
  const results = region.querySelector("[data-cluster-count]");
  if (results) results.textContent = namespaceView ? `显示 ${count} 个命名空间` : `显示 ${count} / ${range.length} 个集群`;
  const uncollected = nsRange.filter(row => row.dataset.namespaceError === "true" || row.dataset.namespaces === "0");
  const note = region.querySelector<HTMLElement>("[data-namespace-notice]");
  if (note) {
    note.hidden = !namespaceView || !uncollected.length;
    note.textContent = `${uncollected.length} 个集群暂时没有可用命名空间数据，可查看采集详情。`;
  }
  const empty = region.querySelector<HTMLElement>("[data-cluster-empty]");
  if (!empty) return;
  empty.hidden = Boolean(count);
  const filtered = Boolean(env || query || (namespaceView ? cluster : mode || state));
  const heading = empty.querySelector("h3");
  const hint = empty.querySelector("p");
  if (heading) heading.textContent = count ? "" : !all.length ? "尚未接入集群" :
    namespaceView ? (query ? "没有匹配的命名空间" : "暂无命名空间数据") : "没有匹配的集群";
  if (hint) hint.textContent = !all.length ? "选择外部直连或采集上报开始接入。" :
    namespaceView && !query ? "集群采集到命名空间后，会在这里显示。" : "调整关键词或筛选条件后再试。";
  empty.querySelector("[data-cluster-clear]")?.toggleAttribute("hidden", !filtered);
  empty.querySelector("[data-cluster-editor]")?.toggleAttribute("hidden", Boolean(all.length));
}

export function refreshClusterWorkspace(region: HTMLElement, items: ClusterEntry[]): void {
  const rows = region.querySelector("[data-cluster-rows]");
  const namespaces = region.querySelector("[data-namespace-rows]");
  if (rows) rows.innerHTML = clusterRows(items, region.dataset.editable === "true");
  if (namespaces) namespaces.innerHTML = namespaceRows(items);
  const env = region.querySelector<HTMLSelectElement>('[data-cluster-filter="environment"]');
  if (env) env.innerHTML = `<option value="">全部环境</option>${resourceOptions([...new Set(items.map(environment))].map(name => [name,name]))}`;
  applyFilters(region);
}

export function bindClusterWorkspace(root: HTMLElement): void {
  const region = root.querySelector<HTMLElement>("[data-resource-workspace]");
  if (!region) return;
  applyFilters(region);
  bindResourceMenus(region);
  const drawer = region.querySelector<HTMLDialogElement>("[data-cluster-drawer]");
  let origin: HTMLElement | null = null;
  drawer?.addEventListener("close", () => {
    if (drawer.open) return;
    const id = drawer.dataset.clusterId;
    restoreResourceFocus(origin, region.querySelector(`[data-cluster-details="${CSS.escape(id ?? "")}"]`) ??
      region.querySelector('[data-cluster-filter="query"]'));
    drawer.replaceChildren();
  });
  region.addEventListener("input", changeFilters);
  region.addEventListener("change", changeFilters);
  region.addEventListener("click", event => {
    if (!(event.target instanceof HTMLElement)) return;
    const trigger = event.target.closest<HTMLButtonElement>("button");
    if (!trigger) return;
    if (trigger.hasAttribute("data-resource-close")) { drawer?.close(); return; }
    if (trigger.dataset.clusterDetails && drawer && !drawer.open) {
      const row = region.querySelector(`[data-cluster-row][data-cluster="${CSS.escape(trigger.dataset.clusterDetails)}"]`);
      const template = row?.querySelector<HTMLTemplateElement>("template");
      if (!template) return;
      origin = trigger;
      drawer.dataset.clusterId = trigger.dataset.clusterDetails;
      drawer.replaceChildren(template.content.cloneNode(true));
      drawer.showModal();
      drawer.querySelector<HTMLElement>("h2")?.focus();
      return;
    }
    const params = selection();
    if (trigger.dataset.resourceTab) {
      if (trigger.dataset.resourceTab === "namespaces") params.set("view", "namespaces");
      else params.delete("view");
    } else if (trigger.dataset.resourceMetric) {
      if (trigger.dataset.resourceMetric === "all") params.delete("status");
      else params.set("status", trigger.dataset.resourceMetric);
    } else if (trigger.hasAttribute("data-cluster-clear")) {
      for (const key of params.get("view") === "namespaces" ? ["environment", "cluster", "namespaceQuery"] : ["environment", "mode", "status", "query"]) params.delete(key);
    } else if (trigger.dataset.clusterNamespaces) {
      params.set("view", "namespaces");
      params.set("cluster", trigger.dataset.clusterNamespaces);
      params.set("environment", trigger.dataset.environment ?? "");
      params.delete("namespaceQuery");
      origin = region.querySelector('[data-resource-tab="namespaces"]');
      drawer?.close();
    } else return;
    storeSelection(params);
    applyFilters(region, params);
    if (trigger.hasAttribute("data-cluster-clear")) region.querySelector<HTMLInputElement>('[data-cluster-filter="query"]')?.focus();
  });
}

function changeFilters(event: Event): void {
  const target = event.target;
  const region = workspace(target);
  if (!region || !(target instanceof HTMLInputElement || target instanceof HTMLSelectElement) ||
    (target instanceof HTMLInputElement ? event.type !== "input" : event.type !== "change")) return;
  const key = target.dataset.clusterFilter;
  if (!key) return;
  const params = selection();
  const field = key === "query" && params.get("view") === "namespaces" ? "namespaceQuery" : key;
  if (target.value) params.set(field, target.value); else params.delete(field);
  if (key === "environment") params.delete("cluster");
  storeSelection(params);
  applyFilters(region, params);
}
