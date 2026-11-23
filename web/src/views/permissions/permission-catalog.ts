import { escapeHtml as h } from "../../components/html.ts";
import type { Scope } from "./platform-scopes.ts";

type Operation = {
  code: string;
  action: string;
  label: string;
  method: string;
  path: string;
  rule?: string;
  risk: string;
  status: string;
  restricted?: boolean;
};

type Feature = {
  module: string;
  toolkit: string;
  name: string;
  desc: string;
  scope: string;
  points: Operation[];
};

export type PermissionCatalog = {
  modules: Record<string, string>;
  rows: Feature[];
};

export type InterfaceGrant = { code: string; scope: Scope };
type Point = Operation & { feature: Feature };

const columns = [["C", "增 · 新增"], ["D", "删 · 撤销"], ["U", "改 · 变更"], ["R", "查 · 查询"]] as const;
const endpoint = "/api/v1/admin/permission-catalog";

export async function loadPermissionCatalog(): Promise<PermissionCatalog> {
  const res = await fetch(endpoint);
  if (!res.ok || !res.headers.get("Content-Type")?.includes("application/json")) throw new Error("permission catalog unavailable");
  return await res.json() as PermissionCatalog;
}

export function catalogSource(catalog: PermissionCatalog | null): string {
  return catalog ? `<template data-pc-source data-catalog="${h(JSON.stringify(catalog))}"></template>` : "";
}

export function renderPermissionCatalog(edit = false): string {
  return `<section class="permission-catalog" aria-label="接口权限目录" data-pc-root data-pc-edit="${edit}">
    ${edit ? "" : `<div class="permission-catalog-heading"><span class="permission-catalog-mark" aria-hidden="true">K</span>
      <div><h2>权限目录</h2><p>从工具集合查看功能动作和接口授权条件。</p></div></div>`}
    <div class="permission-catalog-workspace">
      <div class="permission-catalog-main">
        <nav class="permission-catalog-tabs" aria-label="权限模块"></nav>
        <div class="permission-catalog-filters">
          <input type="search" data-pc-query aria-label="搜索权限目录" placeholder="搜索功能、权限编码或接口">
          <select data-pc-toolkit aria-label="工具集合"></select>
          <select data-pc-filter aria-label="权限筛选"><option value="">全部权限</option><option value="risk">高风险</option>
          ${edit ? '<option value="selected">待加入清单</option><option value="configured">已配置权限</option>' : ""}</select>
        </div>
        ${edit ? `<div class="permission-selection-toolbar">
          <label class="permission-batch"><input type="checkbox" data-pc-all> 批量勾选当前结果</label>
          <span data-pc-selected role="status"></span>
          <button type="button" class="button secondary" data-pc-clear>清空待添加</button>
        </div>` : ""}
        <div class="catalog-table-wrap permission-catalog-table" role="region" aria-label="功能点增删改查矩阵" tabindex="0">
          <table class="catalog-table"><thead><tr><th scope="col">功能点</th>
            ${columns.map(([, label]) => `<th scope="col">${label}</th>`).join("")}
          </tr></thead><tbody data-pc-matrix></tbody></table>
        </div><p data-pc-count class="permission-catalog-count" role="status"></p>
      </div>
      <aside class="permission-catalog-detail" aria-label="接口权限详情" tabindex="-1"><h3>接口权限详情</h3><div data-pc-details></div></aside>
    </div>
  </section>`;
}

type State = {
  catalog: PermissionCatalog;
  module: string;
  points: Map<string, Point>;
  selected: Set<string>;
  configured: InterfaceGrant[];
  original: InterfaceGrant[];
  onSelectionChange?: () => void;
  active?: string;
};

const states = new WeakMap<HTMLElement, State>();

export function bindPermissionCatalog(container: HTMLElement, catalog: PermissionCatalog, options: {
  configured?: InterfaceGrant[];
  original?: InterfaceGrant[];
  onSelectionChange?: () => void;
} = {}): void {
  const root = container.querySelector<HTMLElement>("[data-pc-root]");
  if (!root) return;
  const { configured = [], original = configured, onSelectionChange } = options;
  const state: State = {
    catalog, module: Object.keys(catalog.modules)[0] ?? "", selected: new Set(), configured, original, onSelectionChange,
    points: new Map(catalog.rows.flatMap(feature => feature.points.map(point => [point.code, { ...point, feature }]))),
  };
  states.set(root, state);
  showModule(root, state);
  root.addEventListener("input", event => {
    if (event.target instanceof HTMLElement && event.target.matches("[data-pc-query]")) draw(root, state);
  });
  root.addEventListener("change", event => {
    if (event.target instanceof HTMLSelectElement && event.target.matches("[data-pc-toolkit],[data-pc-filter]")) draw(root, state);
    if (event.target instanceof HTMLInputElement) changeSelection(event.target, root, state);
  });
  root.addEventListener("click", event => click(event, root, state));
}

function showModule(root: HTMLElement, state: State): void {
  const { modules, rows } = state.catalog;
  const nav = root.querySelector(".permission-catalog-tabs");
  const toolkits = root.querySelector("[data-pc-toolkit]");
  if (!nav || !toolkits) return;
  nav.innerHTML = Object.entries(modules).map(([id, name]) =>
    `<button type="button" data-pc-module="${h(id)}" aria-pressed="${id === state.module}">${h(name)}</button>`).join("");
  const available = [...new Set(rows.filter(row => row.module === state.module).map(row => row.toolkit))];
  toolkits.innerHTML = `<option value="">全部工具集合</option>${available.map(label => `<option>${h(label)}</option>`).join("")}`;
  draw(root, state);
}

function click(event: MouseEvent, root: HTMLElement, state: State): void {
  if (!(event.target instanceof HTMLElement)) return;
  const target = event.target.closest<HTMLButtonElement>("button");
  if (!target) return;
  const nextModule = target.dataset.pcModule;
  if (nextModule && nextModule in state.catalog.modules && nextModule !== state.module) {
    state.module = nextModule;
    state.active = undefined;
    showModule(root, state);
    root.querySelector<HTMLElement>("[data-pc-module][aria-pressed=true]")?.focus();
  }
  if (target.dataset.pcPoint && state.points.has(target.dataset.pcPoint)) {
    state.active = target.dataset.pcPoint;
    draw(root, state);
    if (matchMedia("(max-width: 900px)").matches) root.querySelector<HTMLElement>(".permission-catalog-detail")?.focus();
    else root.querySelector<HTMLElement>("[data-pc-point][aria-pressed=true]")?.focus();
  }
  if (target.hasAttribute("data-pc-clear")) {
    state.selected.clear();
    draw(root, state);
    root.querySelector<HTMLElement>("[data-pc-query]")?.focus();
  }
}

function visibleChoices(root: HTMLElement, toolkit?: string): HTMLInputElement[] {
  return [...root.querySelectorAll<HTMLInputElement>("[data-pc-check]:not(:disabled)")]
    .filter(box => toolkit === undefined || box.dataset.pcSet === toolkit);
}

function changeSelection(box: HTMLInputElement, root: HTMLElement, state: State): void {
  if (box.disabled || !box.matches("[data-pc-check], [data-pc-toolkit-toggle], [data-pc-all]")) return;
  const targets = box.hasAttribute("data-pc-check") ? [box] : visibleChoices(root, box.dataset.pcToolkitToggle);
  for (const target of targets) {
    const code = target.dataset.pcCheck;
    if (!code) continue;
    if (box.checked) state.selected.add(code);
    else state.selected.delete(code);
  }
  const match = box.dataset.pcCheck === undefined
    ? `[data-pc-toolkit-toggle="${CSS.escape(box.dataset.pcToolkitToggle ?? "")}"]`
    : `[data-pc-check="${CSS.escape(box.dataset.pcCheck)}"]`;
  const restoreFocus = !box.hasAttribute("data-pc-all");
  draw(root, state);
  if (restoreFocus) (root.querySelector<HTMLElement>(match) ?? root.querySelector<HTMLElement>("[data-pc-query]"))?.focus();
}

function drawSelection(root: HTMLElement, state: State): void {
  for (const batch of root.querySelectorAll<HTMLInputElement>("[data-pc-all], [data-pc-toolkit-toggle]")) {
    const choices = visibleChoices(root, batch.dataset.pcToolkitToggle);
    const checked = choices.filter(choice => choice.checked).length;
    batch.disabled = choices.length === 0;
    batch.checked = checked > 0 && checked === choices.length;
    batch.indeterminate = checked > 0 && checked < choices.length;
    if (batch.hasAttribute("data-pc-all")) {
      const summary = root.querySelector("[data-pc-selected]");
      if (summary) summary.textContent = `当前结果 ${checked} / ${choices.length} 项 · 总待添加 ${state.selected.size} 项`;
    }
  }
  const clear = root.querySelector<HTMLButtonElement>("[data-pc-clear]");
  if (clear) clear.disabled = state.selected.size === 0;
  state.onSelectionChange?.();
}

function draw(root: HTMLElement, state: State): void {
  const matrix = root.querySelector("[data-pc-matrix]");
  const count = root.querySelector("[data-pc-count]");
  const details = root.querySelector("[data-pc-details]");
  if (!matrix || !details || !count) return;
  const edit = root.dataset.pcEdit === "true";
  const query = root.querySelector<HTMLInputElement>("[data-pc-query]")?.value.trim().toLowerCase() ?? "";
  const toolkit = root.querySelector<HTMLSelectElement>("[data-pc-toolkit]")?.value ?? "";
  const filter = root.querySelector<HTMLSelectElement>("[data-pc-filter]")?.value;
  const configuredCodes = new Set(state.configured.map(grant => grant.code));
  const features = state.catalog.rows.filter(row => row.module === state.module && (!toolkit || row.toolkit === toolkit))
    .map(row => {
      const match = `${row.name} ${row.desc} ${row.toolkit}`.toLowerCase().includes(query);
      return { ...row, points: row.points.filter(p =>
        (match || [p.code, p.label, p.method, p.path, p.rule].join(" ").toLowerCase().includes(query)) &&
        (filter !== "risk" || p.risk === "高风险") && (filter !== "selected" || state.selected.has(p.code)) &&
        (filter !== "configured" || configuredCodes.has(p.code))) };
    }).filter(row => row.points.length > 0);
  const visible = features.flatMap(row => row.points);
  if (!visible.some(p => p.code === state.active)) state.active = visible[0]?.code;
  const groups = new Map<string, typeof features>();
  for (const row of features) {
    const group = groups.get(row.toolkit);
    if (group) group.push(row);
    else groups.set(row.toolkit, [row]);
  }
  matrix.innerHTML = [...groups].map(([name, rows]) => {
    const head = `<tr class="permission-group"><th colspan="5"><div><span>${h(name)}</span>
       ${edit ? `<label class="permission-batch"><input type="checkbox" data-pc-toolkit-toggle="${h(name)}" aria-label="勾选本集合 ${h(name)}"> 勾选本集合</label>` : ""}</div></th></tr>`;
    return head + rows.map(row => `<tr><th scope="row">${h(row.name)}<small>${h(row.desc)}</small></th>${columns.map(([key]) => {
      const actions = row.points.filter(p => p.action === key);
      return `<td>${actions.map(p => `<div class="permission-action-row"${state.selected.has(p.code) ? ' data-pc-selected-point="true"' : ""}>
         <button type="button" class="permission-point" data-risk="${h(p.risk)}" data-pc-point="${h(p.code)}"
         aria-pressed="${p.code === state.active}"${configuredCodes.has(p.code) ? ' data-pc-configured="true"' : ""}>
         ${h(p.label)}${configuredCodes.has(p.code) ? " · 已配置" : ""}</button>
         ${edit ? `<label class="permission-action-check"><input type="checkbox" data-pc-check="${h(p.code)}" data-pc-set="${h(row.toolkit)}"
           aria-label="勾选 ${h(p.label)}${p.restricted ? "（仅现有管理身份）" : ""}"${state.selected.has(p.code) ? " checked" : ""}
           ${p.restricted ? ' disabled title="仅现有管理身份"' : ""}></label>` : ""}
         </div>`).join("") || '<span class="permission-empty" title="当前无此类动作">—</span>'}</td>`;
    }).join("")}</tr>`).join("");
  }).join("") || '<tr><td colspan="5">暂无匹配权限。</td></tr>';
  count.textContent = `${features.length} 项功能 · ${visible.length} 个接口权限点`;
  if (edit) drawSelection(root, state);
  const point = state.active ? state.points.get(state.active) : undefined;
  if (!point) {
    details.textContent = "请选择一个权限点查看详情。";
    return;
  }
  const scopeLabels: Record<string, string> = { development: "研发", testing: "测试", staging: "预发布", production: "生产" };
  const describeScope = ({ scope }: InterfaceGrant) =>
    [scopeLabels[scope.environment] ?? scope.environment, scope.cluster, scope.business, scope.namespace].filter(Boolean).join(" / ") || point.feature.scope;
  const original = new Map(state.original.filter(grant => grant.code === point.code).map(grant => [grantKey(grant), grant]));
  const draft = new Map(state.configured.filter(grant => grant.code === point.code).map(grant => [grantKey(grant), grant]));
  const configured = [...draft].map(([key, grant]) => `${describeScope(grant)} · ${original.has(key) ? "已保存" : "待保存"}`);
  configured.push(...[...original].filter(([key]) => !draft.has(key)).map(([, grant]) => `${describeScope(grant)} · 待移除`));
  details.innerHTML = `<p class="permission-ancestry">${h(point.feature.toolkit)} / ${h(point.feature.name)}</p>
    <h4>${h(point.label)}</h4><span class="system-badge" data-risk="${h(point.risk)}">${h(point.risk)}</span>
    ${point.restricted ? '<span class="system-badge">现有管理身份</span>' : ""}
    <dl><div><dt>权限编码</dt><dd><code>${h(point.code)}</code></dd></div>
    <div><dt>方法与路径</dt><dd><strong>${h(point.method)}</strong> <code>${h(point.path)}</code></dd></div>
    <div><dt>分流与约束</dt><dd>${h(point.rule || "按实际资源范围校验。")}</dd></div>
    <div><dt>适用范围</dt><dd>${h(point.feature.scope)}</dd></div>
    ${configured.length ? `<div><dt>角色授权范围</dt><dd><ul>${configured.map(scope => `<li>${h(scope)}</li>`).join("")}</ul></dd></div>` : ""}</dl>`;
}

export function selectedPermissions(form: HTMLFormElement): Point[] {
  const root = form.querySelector<HTMLElement>("[data-pc-root]");
  const state = root && states.get(root);
  if (!state) return [];
  return Array.from(state.selected).flatMap(code => {
    const point = state.points.get(code);
    return point ? [point] : [];
  });
}

export function updateCatalogGrants(form: HTMLFormElement, configured: InterfaceGrant[], addedCodes: string[] = []): void {
  const root = form.querySelector<HTMLElement>("[data-pc-root]");
  if (!root) return;
  const state = states.get(root);
  if (state) {
    state.configured = configured;
    for (const code of addedCodes) state.selected.delete(code);
    draw(root, state);
  }
}

export function describePermission(form: HTMLFormElement, code: string): Point | undefined {
  const root = form.querySelector<HTMLElement>("[data-pc-root]");
  return root ? states.get(root)?.points.get(code) : undefined;
}

export function grantKey(grant: InterfaceGrant): string {
  const { environment, cluster, business, namespace } = grant.scope;
  return JSON.stringify([grant.code, environment ?? "", cluster ?? "", business ?? "", namespace ?? ""]);
}
