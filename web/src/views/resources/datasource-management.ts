import { apiClient } from "../../api/client.ts";
import { ApiError, type DiagnosticDatasource, type ObservabilitySource, type ZabbixScopeChoice, type ZabbixSource } from "../../api/generated.ts";
import { escapeHtml as h } from "../../components/html.ts";
import { diagnosticListPaths } from "../../domain/diagnostic-routes.ts";
import { datasourcesPath } from "../../domain/resource-routes.ts";
import { bindObservabilityEditor, observabilityDescriptions, observabilityNames } from "../diagnosis/observability-sources.ts";
import { bindZabbixEditor, zabbixError } from "../diagnosis/zabbix-sources.ts";
import { bindAssetSourceEditor, showInventory } from "./asset-source-editor.ts";
import { sourceError } from "./datasource-errors.ts";
import { cloudNames, isSync, sourceActivity, sourceKey, sourceKind, sourceName, type SourceRecord, sourceScope, sourceStatus, sourceTime } from "./datasource-rows.ts";
import { bindResourceMenus, resourceCell, resourceDetail, resourceMetric, resourceOptions, resourceState, restoreResourceFocus } from "./resource-workspace.ts";

type Family = SourceRecord["family"];
type Tab = "monitor" | "assets";
const monitors = ["zabbix", "observability"] as const;
const families = [...monitors, "assets"] as const;
const typeNames: Record<string, string> = { zabbix: "Zabbix", ...observabilityNames, ...cloudNames };
const pickerDescriptions: Record<string, string> = { zabbix: "同步主机问题", ...observabilityDescriptions,
  "aliyun-ecs": "同步阿里云实例资产", "tencent-cvm": "同步腾讯云实例资产" };
const familyNames: Record<Family, string> = { zabbix: "Zabbix", observability: "观测数据源", assets: "资产数据源" };
type Data = { records: SourceRecord[]; legacy?: string[]; next?: string };
type Filters = { query?: string; environment?: string; kind?: string; state?: string; grouped?: boolean };
type Permissions = { write: boolean; remove: boolean; test: boolean; createIncident: boolean; aiAdmin?: boolean };

async function readFamily(family: Family, cursor?: string): Promise<Data> {
  if (family === "zabbix") {
    const result = await apiClient.listZabbixSources();
    return { records: result.items.map(source => ({ family, source })) };
  }
  if (family === "observability") {
    const result = await apiClient.listObservabilitySources();
    return { records: result.items.map(source => ({ family, source })), legacy: result.legacy };
  }
  const result = await apiClient.listDiagnosticDatasources({ continue: cursor, limit: 25 });
  return { records: result.items.map(source => ({ family, source })), next: result.pagination.continue };
}

export async function loadDatasourcesPage(hash: string, canWrite: boolean) {
  const params = new URLSearchParams(hash.split("?")[1]);
  const cursor = params.get("continue") || undefined;
  const tab: Tab = (params.get("view") === "assets" || Boolean(cursor)) && params.get("view") !== "monitor" ? "assets" : "monitor";
  const results = await Promise.allSettled(families.map(family => readFamily(family, cursor)));
  const scopes = apiClient.listZabbixScopes().then(value => value.environments, () => []);
  const data = new Map<Family, Data>();
  const errors = new Map<Family, string>();
  for (const [index, result] of results.entries()) {
    const family = families[index];
    if (!family) continue;
    if (result.status === "fulfilled") data.set(family, result.value);
    else {
      if (result.reason instanceof ApiError && result.reason.status === 401) throw result.reason;
      errors.set(family, family === "zabbix" ? zabbixError(result.reason) : sourceError(result.reason));
    }
  }
  return { data, errors, scopes, cursor, tab, html: renderFrame(canWrite) };
}

function renderFrame(actions: boolean): string {
  return `<section data-datasource-workspace>
    <header class="resource-page-head"><div><h1>数据源管理</h1><p>接入诊断信号和云资产，查看验证与同步状态。</p></div>
      ${actions ? '<button type="button" class="button" data-source-add>＋ 新增数据源</button>' : ""}</header>
    <div class="panel resource-list-panel">
      <nav class="resource-view-tabs" aria-label="数据源分类">
        <button type="button" data-source-tab="monitor" aria-pressed="true">监控与诊断</button>
        <button type="button" data-source-tab="assets" aria-pressed="false">资产数据源</button>
      </nav>
      <div class="resource-list-body">
        <div class="resource-summary">${resourceMetric("all", "已接入")}${resourceMetric("issue", "需处理")}${resourceMetric("paused", "暂停或停用")}</div>
        <div class="resource-filters">
          <label>搜索<input type="search" data-source-filter="query" placeholder="搜索数据源名称"></label>
          <label>环境<select data-source-filter="environment"></select></label>
          <label>类型<select data-source-filter="kind"></select></label>
          <label>状态<select data-source-filter="state"></select></label>
          <button type="button" class="button secondary" data-source-clear>清空筛选</button>
        </div>
        <p class="resource-result"><span data-source-count aria-live="polite"></span><label><input type="checkbox" data-source-filter="grouped"> 按类型分组</label></p>
        <div class="resource-notice" data-source-errors hidden></div>
        <p class="resource-notice" data-source-notice role="status" hidden></p>
        <div class="resource-table-wrap" data-source-table><table class="resource-table datasource-directory" aria-label="数据源列表">
          <thead><tr><th scope="col">名称 / 类型</th><th scope="col">所属范围</th><th scope="col">状态</th><th scope="col">最近活动 · UTC+8</th><th scope="col">操作</th></tr></thead>
          <tbody data-source-rows></tbody>
        </table></div>
        <div class="resource-empty" data-source-empty hidden></div>
        <div data-source-paging></div>
        <details class="resource-deployment" data-source-deployment hidden><summary>部署配置</summary><ul></ul></details>
      </div>
    </div>
    <dialog class="resource-drawer" data-source-detail aria-labelledby="source-detail-title"></dialog>
    <dialog class="resource-modal" data-source-picker aria-labelledby="source-picker-title"></dialog>
    <dialog class="zabbix-dialog" data-zabbix-editor aria-labelledby="zabbix-dialog-title"></dialog>
    <dialog class="zabbix-dialog" data-observability-editor aria-labelledby="obs-dialog-title"></dialog>
    <dialog class="resource-modal" data-asset-editor aria-labelledby="asset-editor-title"></dialog>
  </section>`;
}

function action(row: SourceRecord, name: string, label: string, disabled = false): string {
  return `<button type="button" class="button secondary${name === "delete" ? " catalog-danger" : ""}" data-source-action="${name}" data-source-key="${h(sourceKey(row))}"${disabled ? " disabled" : ""}>${label}</button>`;
}

function mainAction(row: SourceRecord, permissions: Permissions, working: boolean, failure: boolean): string {
  if (!permissions.test || !row.source.enabled) return "";
  const syncing = row.family === "zabbix" && row.source.health === "syncing";
  const label = working || syncing ? "处理中…" : row.family === "assets" ? "测试并同步" :
    isSync(row) ? (sourceStatus(row, failure).state === "issue" ? "重试同步" : "立即同步") : "测试连接";
  return action(row, "run", label, working || syncing);
}

function lifecycle(row: SourceRecord, permissions: Permissions, working: boolean): string {
  const label = row.source.enabled ? row.family === "assets" ? "停用" : "暂停" : "恢复";
  return permissions.write ? action(row, "toggle", label, working) : "";
}

function editAction(row: SourceRecord, permissions: Permissions, working: boolean): string {
  if (!permissions.write || (row.family === "observability" && !permissions.test)) return "";
  return action(row, "edit", "编辑", working);
}

function zabbixActions(row: SourceRecord, permissions: Permissions, working: boolean): string {
  if (row.family !== "zabbix" || !permissions.write || !permissions.test) return "";
  return action(row, "scope", "调整主机组", working) + action(row, "rotate", "更换凭据", working);
}

function removeAction(row: SourceRecord, permissions: Permissions, working: boolean): string {
  return permissions.remove ? action(row, "delete", "删除", working) : "";
}

function stateBadge(row: SourceRecord, failure: boolean): string {
  const { label, state, snapshot } = sourceStatus(row, failure);
  return resourceState(label, state === "issue" ? "issue" : state === "waiting" || state === "untested" ? "waiting" : state === "good" ? "good" : "neutral") +
    (snapshot ? "<small>保留上次快照</small>" : "");
}

function listRow(row: SourceRecord, permissions: Permissions, scopes: ZabbixScopeChoice[], working: boolean, failure: boolean): string {
  const key = h(sourceKey(row));
  const name = h(sourceName(row));
  const recent = sourceActivity(row);
  const menu = zabbixActions(row, permissions, working) + lifecycle(row, permissions, working) + holmesBindingLink(row, permissions);
  return `<tr data-source-row="${key}"><td><button type="button" class="resource-name" data-source-action="detail" data-source-key="${key}">${name}</button>
      <small>${h(typeNames[sourceKind(row)])}${row.family === "assets" && row.source.config.kind === "zabbix" ? " · 存量资产" : ""}</small></td>
    ${resourceCell("所属范围", h(sourceScope(row, scopes)))}
    ${resourceCell("状态", stateBadge(row, failure))}
    ${resourceCell("最近活动", `${recent.time}<small>${recent.label}</small>`)}
    <td><div class="catalog-actions">${mainAction(row, permissions, working, failure)}${editAction(row, permissions, working)}${removeAction(row, permissions, working)}
      ${menu ? `<details class="resource-row-menu"><summary aria-label="${name}的更多操作">⋯</summary><div>${menu}</div></details>` : ""}</div></td></tr>`;
}

function holmesBindingLink(row: SourceRecord, permissions: Permissions): string {
  if (!permissions.aiAdmin || !row.source.enabled) return "";
  if (row.family !== "zabbix" && (row.family !== "observability" || row.source.kind !== "alertmanager")) return "";
  const params = new URLSearchParams({bindSource: row.family === "zabbix" ? "zabbix" : "alertmanager", sourceId: row.source.id});
  return `<a class="button secondary" href="#/ai?${h(params.toString())}">绑定 Holmes</a>`;
}

function detailBody(row: SourceRecord, failure: string | undefined): string {
  const recent = sourceActivity(row);
  let settings: string;
  let extra = "";
  const entry = (name: string, text: string) => `<dt>${name}</dt><dd>${text}</dd>`;
  if (row.family === "zabbix") {
    const { source } = row;
    const query = new URLSearchParams({ source: "zabbix", ...source.scope });
    settings = entry("服务地址", h(source.endpoint)) + entry("认证方式", source.authMethod === "password" ? "账号密码" : "API Token") +
      entry("上游版本", h(source.upstreamVersion)) + entry("主机组", h(source.groups.map(group => group.name).join("、"))) +
      entry("最近样本", source.hostCount === undefined ? "暂无可用数据" : `${source.hostCount} 台主机 · ${source.problemCount ?? 0} 条活跃问题`);
    extra = `<a class="button secondary" href="${diagnosticListPaths.overview}?${h(query.toString())}">查看诊断总览</a>`;
  } else if (row.family === "observability") {
    const { source } = row;
    settings = entry("服务地址", h(source.endpoint)) + entry("认证方式", source.auth === "basic" ? "Basic 认证" : source.auth === "none" ? "免认证" :
      source.kind === "grafana" ? "Service Account Token" : "Bearer Token");
    if (source.shared) settings += entry("共享范围", h(`${source.label ?? ""} = ${source.value ?? ""}`));
    if (source.tenant) settings += entry("日志租户", h(`${source.tenant.account} / ${source.tenant.project}`));
    if (source.dashboards?.length) settings += entry("仪表盘", h(source.dashboards.map(item => item.title).join("、")));
  } else {
    const { source } = row;
    settings = (source.config.kind === "zabbix" ? entry("服务地址", h(source.config.endpoint)) : entry("云地域", h(source.config.region))) +
      entry("凭据文件", source.credentialConfigured ? "已配置引用" : "未配置") +
      entry("信号推送", source.ingressConfigured ? "已配置" : "未配置");
    extra = '<details data-source-inventory><summary>可见资产</summary><div role="status"></div></details>';
  }
  const hint = failure || (row.family === "assets" ? row.source.message : "");
  return `${hint ? `<p class="resource-notice" role="status">${h(hint)}</p>` : ""}
    <section class="resource-detail-card"><h3>运行记录</h3>
      <p>${stateBadge(row, Boolean(failure))}</p><dl>${entry(recent.label, recent.time)}
        ${isSync(row) || row.family === "assets" ? entry("上次尝试", sourceTime(row.source.checkedAt)) : ""}</dl></section>
    <section class="resource-detail-card"><h3>连接与范围</h3><dl>${settings}</dl></section>${extra}`;
}

export function bindDatasourcesWorkspace(root: HTMLElement, page: Awaited<ReturnType<typeof loadDatasourcesPage>>, permissions: Permissions): void {
  const region = root.querySelector<HTMLElement>("[data-datasource-workspace]");
  const table = region?.querySelector<HTMLElement>("[data-source-table]");
  const tbody = region?.querySelector<HTMLElement>("[data-source-rows]");
  const drawer = region?.querySelector<HTMLDialogElement>("[data-source-detail]");
  const picker = region?.querySelector<HTMLDialogElement>("[data-source-picker]");
  const zabbixEditor = region?.querySelector<HTMLDialogElement>("[data-zabbix-editor]");
  const obsEditor = region?.querySelector<HTMLDialogElement>("[data-observability-editor]");
  const assetEditor = region?.querySelector<HTMLDialogElement>("[data-asset-editor]");
  if (!region || !table || !tbody || !drawer || !picker || !zabbixEditor || !obsEditor || !assetEditor) return;
  const rowsNode = tbody;
  const drawerNode = drawer;
  const frame = region;
  bindResourceMenus(frame);
  const { data, errors } = page;
  const filters: Record<Tab, Filters> = { monitor: {}, assets: {} };
  const collapsed = new Set<string>();
  const pending = new Set<string>();
  const failures = new Map<string, string>();
  const epoch: Record<Family, number> = { zabbix: 0, observability: 0, assets: 0 };
  const timers = new Map<Family, number>();
  const waiting = new Map<string, { checked?: string | number; deadline: number }>();
  let scopes: ZabbixScopeChoice[] = [];
  let cursor = page.cursor;
  let tab: Tab = page.tab;
  let detailsKey = "";
  let selectedType = "";
  let origin: HTMLElement | null = null;
  let originKey = "";
  let assetLoading = false;
  const records = (family: Family) => data.get(family)?.records ?? [];
  const lookup = (key: string) => families.flatMap(records).find(row => sourceKey(row) === key);
  const canAdd = () => permissions.write && (tab === "assets" || permissions.test);
  const notices = frame.querySelector<HTMLElement>("[data-source-notice]");
  const errorRegion = frame.querySelector<HTMLElement>("[data-source-errors]");
  const empty = frame.querySelector<HTMLElement>("[data-source-empty]");
  const count = frame.querySelector("[data-source-count]");
  const deployment = frame.querySelector<HTMLDetailsElement>("[data-source-deployment]");
  const pageNav = frame.querySelector("[data-source-paging]");
  const search = frame.querySelector<HTMLInputElement>('[data-source-filter="query"]');
  const add = frame.querySelector<HTMLButtonElement>(".resource-page-head [data-source-add]");

  function isWorking(row: SourceRecord): boolean {
    return pending.has(sourceKey(row)) || waiting.has(sourceKey(row));
  }

  function notice(message: string): void {
    if (!notices) return;
    notices.hidden = !message;
    notices.textContent = message;
  }

  function syncFilters(): void {
    const current = filters[tab];
    if (search && search.value !== (current.query ?? "")) search.value = current.query ?? "";
    const envs = new Map(scopes.map(scope => [scope.id, scope.name]));
    for (const row of tab === "assets" ? records("assets") : monitors.flatMap(records)) {
      const id = row.source.scope.environmentId || "";
      const name = row.family === "zabbix" ? row.source.environmentName : "";
      envs.set(id || "_unassigned", envs.get(id) || name || id || "未指定环境");
    }
    if (current.environment && !envs.has(current.environment)) envs.set(current.environment, current.environment);
    const environments: [string, string][] = [["", "全部环境"], ...envs];
    const types = tab === "assets" ? { ...cloudNames, ...(records("assets").some(row => sourceKind(row) === "zabbix") ? { zabbix: "Zabbix（存量资产）" } : {}) }
      : { zabbix: "Zabbix", ...observabilityNames };
    const options: Record<string, [string, string][]> = {
      environment: environments, kind: [["", "全部类型"], ...Object.entries(types)],
      state: [["", "全部状态"], ["enabled", "已启用"], ["issue", "需处理"], tab === "assets" ? ["untested", "待验证"] : ["waiting", "等待首次同步"], ["paused", "暂停或停用"]],
    };
    for (const [key, choices] of Object.entries(options)) {
      const node = frame.querySelector<HTMLSelectElement>(`[data-source-filter="${key}"]`);
      const currentValue = current[key as "environment" | "kind" | "state"];
      if (!node) continue;
      const markup = resourceOptions(choices);
      if (node.dataset.options !== markup) { node.innerHTML = markup; node.dataset.options = markup; }
      node.value = currentValue ?? "";
    }
    const group = frame.querySelector<HTMLInputElement>('[data-source-filter="grouped"]');
    if (group) group.checked = Boolean(current.grouped);
  }

  function paint(): void {
    if (!frame.isConnected) return;
    const active = document.activeElement as HTMLElement | null;
    const activeKey = active?.closest<HTMLElement>("[data-source-row]")?.dataset.sourceRow;
    const activeAction = active?.dataset.sourceAction;
    const groupKey = active?.dataset.sourceGroup;
    const current = filters[tab];
    const available = tab === "monitor" ? monitors : ["assets"] as const;
    const failed = available.filter(family => errors.has(family));
    const allRows = tab === "assets" && assetLoading ? [] : available.filter(family => !errors.has(family)).flatMap(records);
    const needle = (current.query ?? "").trim().toLocaleLowerCase();
    const visible = allRows.filter(row => {
      const status = sourceStatus(row, failures.has(sourceKey(row)));
      return (!needle || sourceName(row).toLocaleLowerCase().includes(needle)) &&
        (!current.environment || (row.source.scope.environmentId || "_unassigned") === current.environment) &&
        (!current.kind || sourceKind(row) === current.kind) &&
        (!current.state || (current.state === "enabled" ? row.source.enabled : status.state === current.state));
    });
    if (add) add.hidden = !canAdd();
    for (const button of frame.querySelectorAll<HTMLButtonElement>("[data-source-tab]")) button.setAttribute("aria-pressed", String(button.dataset.sourceTab === tab));
    for (const button of frame.querySelectorAll<HTMLButtonElement>("[data-resource-metric]")) {
      const key = button.dataset.resourceMetric;
      button.setAttribute("aria-pressed", String(key === (current.state || "all")));
      const number = button.querySelector("strong");
      if (number) number.textContent = failed.length === available.length ? "—" : String(key === "all" ? allRows.length :
        allRows.filter(row => sourceStatus(row, failures.has(sourceKey(row))).state === key).length);
    }
    if (count) count.textContent = assetLoading && tab === "assets" ? "正在读取资产数据源…" :
      failed.length === available.length ? "暂时无法统计" : `显示 ${visible.length} / ${allRows.length} 个数据源${failed.length ? " · 已加载来源" : tab === "assets" ? " · 当前资产页" : ""}`;
    if (errorRegion) {
      errorRegion.hidden = !failed.length;
      errorRegion.innerHTML = failed.map(family => `<div><span>${familyNames[family]} · ${h(errors.get(family))}</span>
        <button type="button" class="button secondary" data-source-retry="${family}">重试加载</button></div>`).join("");
    }
    tableCellVisibility(visible, current);
    if (empty) {
      empty.hidden = Boolean(visible.length) || failed.length > 0 || (tab === "assets" && assetLoading);
      const filtered = Boolean(current.query || current.environment || current.kind || current.state);
      empty.innerHTML = filtered ? '<h3>没有匹配的数据源</h3><p>调整搜索或筛选条件后再试。</p><button type="button" class="button secondary" data-source-clear>清空筛选</button>' :
        `<h3>${tab === "monitor" ? "尚未接入监控与诊断数据源" : "暂无资产数据源"}</h3>
        ${canAdd() ? '<button type="button" class="button" data-source-add>新增数据源</button>' : ""}`;
    }
    if (pageNav) {
      const next = data.get("assets")?.next;
      pageNav.innerHTML = tab === "assets" && !errors.has("assets") && (cursor || next) ? `<nav class="pagination" aria-label="资产数据源分页">
        ${cursor ? '<button type="button" class="button secondary" data-source-page="">返回首页</button>' : ""}
        ${next ? `<button type="button" class="button secondary" data-source-page="${h(next)}">下一页</button>` : ""}</nav>` : "";
    }
    if (deployment) {
      const legacy = data.get("observability")?.legacy ?? [];
      deployment.hidden = tab !== "monitor" || !legacy.length || errors.has("observability");
      const list = deployment.querySelector("ul");
      if (list) list.innerHTML = legacy.map(kind => `<li>${h(typeNames[kind])} · 由部署参数维护</li>`).join("");
    }
    syncFilters();
    if (activeKey) {
      const row = rowsNode.querySelector<HTMLElement>(`[data-source-row="${CSS.escape(activeKey)}"]`);
      restoreResourceFocus(row?.querySelector<HTMLElement>(activeAction ? `[data-source-action="${CSS.escape(activeAction)}"]:not(:disabled)` : "summary") ?? null,
        row?.querySelector(".resource-name") ?? search ?? null);
    } else if (groupKey) rowsNode.querySelector<HTMLElement>(`[data-source-group="${CSS.escape(groupKey)}"]`)?.focus();
  }

  function tableCellVisibility(visible: SourceRecord[], current: Filters): void {
    if (table) table.hidden = !visible.length;
    const html = (row: SourceRecord) => listRow(row, permissions, scopes, isWorking(row), failures.has(sourceKey(row)));
    if (!current.grouped) { rowsNode.innerHTML = visible.map(html).join(""); return; }
    const byType = new Map<string, SourceRecord[]>();
    for (const row of visible) {
      const kind = sourceKind(row);
      const group = byType.get(kind) ?? [];
      group.push(row);
      byType.set(kind, group);
    }
    rowsNode.innerHTML = [...byType].map(([kind, group]) => {
      const key = `${tab}:${kind}`;
      const open = !collapsed.has(key);
      return `<tr class="resource-group"><td colspan="5"><button type="button" data-source-group="${h(key)}" aria-expanded="${open}">
        ${h(typeNames[kind])} 数据源 <small>${group.length} 个</small></button></td></tr>${open ? group.map(html).join("") : ""}`;
    }).join("");
  }

  function replace(row: SourceRecord): void {
    ++epoch[row.family];
    const current = data.get(row.family);
    const key = sourceKey(row);
    const old = current?.records ?? [];
    const updated = old.some(item => sourceKey(item) === key)
      ? old.map(item => sourceKey(item) === key ? row : item) : [row, ...old];
    data.set(row.family, { ...current, records: updated });
    errors.delete(row.family);
    failures.delete(key);
    paint();
    if (detailsKey === key && drawerNode.open) paintDetail(row);
  }

  async function reload(family: Family): Promise<boolean> {
    const request = ++epoch[family];
    try {
      const result = await readFamily(family, cursor);
      if (!frame.isConnected || request !== epoch[family]) return false;
      const previous = detailsKey ? lookup(detailsKey)?.source : undefined;
      data.set(family, result);
      errors.delete(family);
      for (const row of result.records) {
        const key = sourceKey(row);
        const job = waiting.get(key);
        if (job && (job.checked !== row.source.checkedAt || !row.source.enabled)) waiting.delete(key);
      }
      for (const [key, job] of waiting) {
        if (job.deadline <= Date.now() || (key.startsWith(`${family}:`) && !lookup(key))) waiting.delete(key);
      }
      paint();
      const row = detailsKey ? lookup(detailsKey) : undefined;
      if (!row && drawerNode.open && detailsKey.startsWith(`${family}:`)) drawerNode.close();
      if (row && row.family === family && drawerNode.open && JSON.stringify(previous) !== JSON.stringify(row.source)) paintDetail(row);
      return true;
    } catch (error) {
      if (frame.isConnected && request === epoch[family]) {
        if (error instanceof ApiError && (error.status === 401 || error.status === 403)) {
          data.delete(family);
          if (drawerNode.open && detailsKey.startsWith(`${family}:`)) drawerNode.close();
        }
        errors.set(family, family === "zabbix" ? zabbixError(error) : sourceError(error));
        paint();
      }
      return false;
    }
  }

  function schedule(family: "zabbix" | "observability", remaining = 35): void {
    window.clearTimeout(timers.get(family));
    if (!frame.isConnected || remaining <= 0) return;
    timers.set(family, window.setTimeout(async () => {
      if (!frame.isConnected) return;
      const loaded = await reload(family);
      const outstanding = [...waiting.keys()].some(key => key.startsWith(`${family}:`));
      const starting = family === "zabbix" && records(family).some(row => row.family === "zabbix" && ["pending","syncing"].includes(row.source.health));
      if (loaded && (outstanding || starting)) schedule(family, remaining - 1);
      else if (!loaded) {
        for (const key of waiting.keys()) if (key.startsWith(`${family}:`)) waiting.delete(key);
        paint();
      }
    }, 2_000));
  }

  function paintDetail(row: SourceRecord): void {
    const key = sourceKey(row);
    const working = isWorking(row);
    const foot = `<div class="catalog-actions">${editAction(row, permissions, working)}${removeAction(row, permissions, working)}${zabbixActions(row, permissions, working)}${lifecycle(row, permissions, working)}
      ${mainAction(row, permissions, working, failures.has(key))}</div>`;
    drawerNode.innerHTML = resourceDetail("source-detail-title", sourceName(row),
      `${typeNames[sourceKind(row)]} · ${sourceScope(row, scopes)}`, detailBody(row, failures.get(key)), foot);
    if (row.family === "assets") {
      const inventory = drawerNode.querySelector<HTMLDetailsElement>("[data-source-inventory]");
      inventory?.addEventListener("toggle", () => {
        const status = inventory.querySelector<HTMLElement>("[role=status]");
        if (status && inventory.open) void showInventory(status, row.source, permissions.createIncident);
      });
    }
  }

  const openZabbix = bindZabbixEditor(zabbixEditor, (source: ZabbixSource, mode) => {
    replace({ family: "zabbix", source });
    if (mode === "edit") { notice(`${source.name} · 配置已保存。`); return; }
    notice(source.enabled ? "已保存配置，等待后台同步。" : "已保存配置。");
    schedule("zabbix");
  }, () => void reload("zabbix"));
  const openObservability = bindObservabilityEditor(obsEditor, (source: ObservabilitySource) => {
    replace({ family: "observability", source });
    notice(`${source.name} · 配置已保存。`);
    if (source.kind === "alertmanager") schedule("observability");
  }, () => void reload("observability"));
  const openAsset = bindAssetSourceEditor(assetEditor, (source: DiagnosticDatasource) => {
    if (!frame.isConnected || location.hash.split("?")[0] !== datasourcesPath) return;
    replace({ family: "assets", source });
    notice(`${source.name} · 配置已保存。`);
  }, () => void reload("assets"));

  function remember(trigger: HTMLElement, key = ""): void {
    origin = trigger.closest(".resource-row-menu")?.querySelector("summary") ?? trigger;
    originKey = key;
  }

  function restore(): void {
    if (drawerNode.open) { drawerNode.querySelector<HTMLElement>("h2")?.focus(); return; }
    const matching = rowsNode.querySelector<HTMLElement>(`[data-source-row="${CSS.escape(originKey)}"] .resource-name`);
    restoreResourceFocus(origin, matching ?? (canAdd() ? add ?? null : search ?? null));
  }

  for (const dialog of [zabbixEditor, obsEditor, assetEditor]) dialog.addEventListener("close", restore);
  drawerNode.addEventListener("close", () => {
    detailsKey = "";
    drawerNode.replaceChildren();
    restore();
  });
  picker.addEventListener("close", () => {
    if (!zabbixEditor.open && !obsEditor.open && !assetEditor.open) restore();
  });

  function openChooser(trigger: HTMLElement): void {
    if (!canAdd() || !picker) return;
    remember(trigger);
    selectedType = "";
    const types = tab === "assets" ? cloudNames : { zabbix: "Zabbix", ...observabilityNames };
    const choices = Object.entries(types).map(([kind, name]) => `<button type="button" class="button secondary" aria-pressed="false" data-source-kind="${kind}">
      <strong>${h(name)}</strong><small>${h(pickerDescriptions[kind])}</small></button>`).join("");
    picker.innerHTML = resourceDetail("source-picker-title", "新增数据源", tab === "assets" ? "资产数据源" : "监控与诊断",
      `<div class="resource-picker">${choices}</div>`, '<div class="catalog-actions"><button type="button" class="button secondary" data-resource-close>取消</button><button type="button" class="button" data-source-next disabled>继续配置</button></div>');
    picker.showModal();
  }

  function updateAddress(): void {
    const params = new URLSearchParams(location.hash.split("?")[1]);
    params.set("view", tab);
    if (cursor) params.set("continue", cursor); else params.delete("continue");
    history.replaceState(null, "", `${datasourcesPath}?${params.toString()}`);
  }

  async function operate(row: SourceRecord, command: string): Promise<void> {
    const key = sourceKey(row);
    if (isWorking(row) || (command === "run" && !permissions.test) || (command === "toggle" && !permissions.write) || (command === "delete" && !permissions.remove)) return;
    if (command === "toggle") {
      const verb = row.source.enabled ? row.family === "assets" ? "停用" : "暂停" : "恢复";
      const impact = row.source.enabled ? "对应的数据采集或查询将暂停。" : "数据源将恢复相应的采集或查询。";
      if (!window.confirm(`${verb}「${sourceName(row)}」？${impact}`)) return;
    }
    if (command === "delete" && !window.confirm(`删除「${sourceName(row)}」？删除后将停止同步，并移除其配置和采集快照。`)) return;
    const operatedCursor = cursor;
    pending.add(key);
    paint();
    if (detailsKey === key && drawerNode.open) paintDetail(row);
    const version = `"${row.source.version}"`;
    try {
      if (command === "delete") {
        if (row.family === "zabbix") await apiClient.deleteZabbixSource({ sourceId: row.source.id, "If-Match": version });
        else if (row.family === "observability") await apiClient.deleteObservabilitySource({ sourceId: row.source.id, "If-Match": version });
        else await apiClient.deleteDiagnosticDatasource({ datasourceId: row.source.id, "If-Match": version });
        if (!frame.isConnected) return;
        if (detailsKey === key && drawerNode.open) drawerNode.close();
        const current = data.get(row.family);
        if (current) data.set(row.family, { ...current, records: current.records.filter(item => sourceKey(item) !== key) });
        failures.delete(key);
        waiting.delete(key);
        await reload(row.family);
        notice(`已删除「${sourceName(row)}」。`);
        if (document.activeElement === document.body) search?.focus();
        return;
      }
      if (command === "run") {
        if (row.family === "assets") {
          const source = await apiClient.testDiagnosticDatasource({ datasourceId: row.source.id, "If-Match": version });
          if (!frame.isConnected) return;
          if (cursor === operatedCursor) replace({ family: "assets", source });
          notice(`${source.name} · 已返回测试与同步结果。`);
        } else if (row.family === "zabbix") {
          await apiClient.syncZabbixSource({ sourceId: row.source.id });
          waiting.set(key, { checked: row.source.checkedAt, deadline: Date.now() + 70_000 });
          schedule("zabbix");
          notice("已提交同步任务。");
        } else {
          const result = await apiClient.syncObservabilitySource({ sourceId: row.source.id });
          if (result.queued) {
            waiting.set(key, { checked: row.source.checkedAt, deadline: Date.now() + 70_000 });
            schedule("observability");
            notice("已提交同步任务。");
          } else {
            await reload("observability");
            notice("连接测试通过。");
          }
        }
        failures.delete(key);
      } else if (row.family === "assets") {
        const { id, config, name, scope, enabled } = row.source;
        const source = await apiClient.updateDiagnosticDatasource({ datasourceId: id, "If-Match": version,
          body: { config, name, scope, enabled: !enabled } });
        if (!frame.isConnected) return;
        if (cursor === operatedCursor) replace({ family: "assets", source });
      } else if (row.family === "zabbix") {
        const source = await apiClient.updateZabbixSource({ sourceId: row.source.id, "If-Match": version, body: { enabled: !row.source.enabled } });
        if (!frame.isConnected) return;
        replace({ family: "zabbix", source });
        if (source.enabled) schedule("zabbix");
      } else {
        await apiClient.toggleObservabilitySource({ sourceId: row.source.id, "If-Match": version, body: { enabled: !row.source.enabled } });
        await reload("observability");
        if (!row.source.enabled && row.source.kind === "alertmanager") schedule("observability");
      }
      if (command === "toggle") notice(row.source.enabled ? "数据源已暂停或停用。" : "数据源已恢复。");
    } catch (error) {
      const message = row.family === "zabbix" ? zabbixError(error) :
        error instanceof ApiError && error.status === 412 ? "配置已更新，请核对最新配置后重试。" : sourceError(error);
      if (command === "run" && (!(error instanceof ApiError) || ![401, 403, 409, 412].includes(error.status))) failures.set(key, message);
      if (command === "delete" && detailsKey === key && drawerNode.open) failures.set(key, message);
      notice(message);
      if (error instanceof ApiError && [401, 403, 412].includes(error.status)) await reload(row.family);
    } finally {
      pending.delete(key);
      if (frame.isConnected) {
        paint();
        if (detailsKey === key && drawerNode.open) {
          const next = lookup(key);
          if (next) {
            paintDetail(next);
            drawerNode.querySelector<HTMLElement>(`[data-source-action="${command}"]:not(:disabled)`)?.focus();
          }
        } else if (document.activeElement === document.body) {
          rowsNode.querySelector<HTMLElement>(`[data-source-row="${CSS.escape(key)}"] .resource-name`)?.focus();
        }
      }
    }
  }

  function updateFilter(event: Event): void {
    const target = event.target;
    if (!(target instanceof HTMLInputElement || target instanceof HTMLSelectElement)) return;
    if (target instanceof HTMLSelectElement ? event.type !== "change" : event.type !== "input") return;
    const key = target.dataset.sourceFilter;
    if (!key) return;
    if (key === "grouped" && target instanceof HTMLInputElement) filters[tab].grouped = target.checked;
    else filters[tab][key as "query" | "environment" | "kind" | "state"] = target.value;
    paint();
  }

  frame.addEventListener("input", updateFilter);
  frame.addEventListener("change", updateFilter);
  frame.addEventListener("click", event => {
    if (!(event.target instanceof Element)) return;
    const button = event.target.closest<HTMLButtonElement>("button");
    if (!button) return;
    if (button.hasAttribute("data-resource-close") && button.closest("dialog") !== assetEditor) {
      button.closest("dialog")?.close();
      return;
    }
    if (button.dataset.sourceTab === "assets" || button.dataset.sourceTab === "monitor") {
      tab = button.dataset.sourceTab;
      updateAddress();
      notice("");
    } else if (button.hasAttribute("data-source-add")) { openChooser(button); return; }
    else if (button.dataset.sourceKind && picker.open) {
      selectedType = button.dataset.sourceKind;
      for (const choice of picker.querySelectorAll("[data-source-kind]")) choice.setAttribute("aria-pressed", String(choice === button));
      const next = picker.querySelector<HTMLButtonElement>("[data-source-next]");
      if (next) next.disabled = false;
      return;
    } else if (button.hasAttribute("data-source-next") && picker.open) {
      if (!canAdd() || !Object.hasOwn(typeNames, selectedType)) return;
      picker.close();
      if (selectedType === "zabbix" && tab === "monitor") openZabbix("add");
      else if (tab === "monitor" && Object.hasOwn(observabilityNames, selectedType)) openObservability(selectedType as keyof typeof observabilityNames);
      else if (tab === "assets" && Object.hasOwn(cloudNames, selectedType)) openAsset(selectedType as keyof typeof cloudNames);
      return;
    } else if (button.hasAttribute("data-source-clear")) filters[tab] = { grouped: filters[tab].grouped };
    else if (button.dataset.resourceMetric) filters[tab].state = button.dataset.resourceMetric === "all" ? "" : button.dataset.resourceMetric;
    else if (button.dataset.sourceGroup) {
      const key = button.dataset.sourceGroup;
      if (collapsed.has(key)) collapsed.delete(key); else collapsed.add(key);
    } else if (button.dataset.sourceRetry) {
      void reload(button.dataset.sourceRetry as Family);
      return;
    } else if (button.hasAttribute("data-source-page")) {
      if (assetLoading) return;
      cursor = button.dataset.sourcePage || undefined;
      assetLoading = true;
      updateAddress();
      void reload("assets").finally(() => { assetLoading = false; paint(); search?.focus(); });
    } else if (button.dataset.sourceAction) {
      const row = lookup(button.dataset.sourceKey ?? "");
      if (!row) return;
      const command = button.dataset.sourceAction;
      const menu = button.closest<HTMLDetailsElement>(".resource-row-menu");
      if (menu) menu.open = false;
      if (command === "detail") {
        remember(button, sourceKey(row));
        detailsKey = originKey;
        paintDetail(row);
        drawerNode.showModal();
        drawerNode.querySelector<HTMLElement>("h2")?.focus();
      } else if (command === "run" || command === "toggle" || command === "delete") void operate(row, command);
      else if (permissions.write && (permissions.test || row.family !== "observability")) {
        if (!drawerNode.open) remember(button, sourceKey(row));
        if (row.family === "zabbix" && (command === "edit" || permissions.test && (command === "rotate" || command === "scope"))) openZabbix(command, row.source);
        else if (row.family === "observability" && command === "edit") openObservability(row.source.kind, row.source);
        else if (row.family === "assets" && command === "edit") openAsset(row.source.config.kind === "tencent-cvm" ? "tencent-cvm" : "aliyun-ecs", row.source);
      }
      return;
    } else return;
    paint();
    if (button.hasAttribute("data-source-clear")) search?.focus();
  });
  paint();
  void page.scopes.then(next => { scopes = next; paint(); });
  if (records("zabbix").some(row => row.family === "zabbix" && ["pending", "syncing"].includes(row.source.health))) schedule("zabbix");
}
