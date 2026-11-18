import { escapeHtml as h } from "../../components/html.ts";

type Scope = { environment: string; cluster?: string; namespace?: string; business?: string };
type Role = { id: string; name: string; fixed?: boolean; scopes: Scope[] | null };
type Group = { id: string; name: string; description?: string; roles: string[] | null };
type Account = {
  id: string; username: string; kind: string; disabled: boolean; directoryDisabled: boolean;
  mustChangePassword: boolean; groups: string[] | null; roles: string[] | null;
  effective: (Scope & { sources: string[] })[] | null;
};
type Access = { groups: Group[]; roles: Role[] };
type Entry = { id: string; name: string; environment?: string };
type Operation = {
  id: number; at: number; actor: string; action: string; risk: string; target: string;
  result: string; reason: string; batch: string;
};
const environments: Record<string, string> = { development: "研发", testing: "测试", staging: "预发布", production: "生产" };
const kinds: Record<string, string> = { super: "超级管理员", administrator: "系统管理员", guest: "本地访客", ldap: "LDAP 员工" };

async function json<T>(path: string): Promise<T> {
  const response = await fetch(path);
  if (!response.ok) throw new Error("request failed");
  return await response.json() as T;
}
function admin<T>(name: string): Promise<T> {
  return json<T>(`/api/v1/admin/${name}`);
}

function options(entries: Entry[], selected = ""): string {
  return entries.map(({ id, name }) => `<option value="${h(id)}"${id === selected ? " selected" : ""}>${h(name)}</option>`).join("");
}

function submit(label: string, action = "", secondary = false): string {
  return `<button class="button${secondary ? " secondary" : ""}" type="submit" value="${action}">${label}</button>`;
}

function scopeText(scope: Scope): string {
  return [environments[scope.environment] ?? scope.environment, scope.cluster, scope.business, scope.namespace].filter(Boolean).map(h).join(" / ");
}

function authTable(user: Account): string {
  if (user.disabled || user.directoryDisabled || user.mustChangePassword) return "<p>账号当前无法访问诊断数据。</p>";
  if (user.kind === "super" || user.kind === "administrator") return "<p>全局 · 系统管理员</p>";
  if (!user.effective?.length) return "<p>暂无生效权限。</p>";
  return `<div class="catalog-table-wrap"><table class="catalog-table">
    <thead><tr><th>权限</th><th>资源范围</th><th>全部来源</th></tr></thead>
    <tbody>${user.effective.map((scope) => `<tr><td>历史诊断查看</td><td>${scopeText(scope)}</td>
      <td>${scope.sources.map(h).join("<br>")}</td></tr>`).join("")}</tbody></table></div>`;
}

function statusOf(user: Account): string {
  if (user.disabled) return "平台已停用";
  if (user.directoryDisabled) return "目录已停用";
  return user.mustChangePassword ? "待改密" : "正常";
}

function tempPassword(): string {
  return '<label>临时密码<input name="password" type="password" autocomplete="new-password" minlength="12" maxlength="72" required></label>';
}

function names(ids: string[] | null, items: Entry[]): string {
  return ids?.map((id) => h(items.find((item) => item.id === id)?.name ?? id)).join("、") || "—";
}

function editButton(label: string, target: string, secondary = false): string {
  return `<button type="button" class="button${secondary ? " secondary" : ""}" data-system-open="${h(target)}">${label}</button>`;
}

function editor(key: string, title: string, contents: string): string {
  return `<template data-system-editor="${h(key)}"><header class="system-dialog-header"><h2 id="system-dialog-title">${h(title)}</h2>
    <button type="button" class="button secondary" data-system-close>关闭</button></header>${contents}</template>`;
}

function operationForm(resource: string, id: string, fields: string, label: string, action: string): string {
  return `<form class="system-form" data-platform="${resource}" data-id="${h(id)}"><div class="system-editor-body">${fields}
    <p role="status" aria-live="polite"></p></div><footer class="system-dialog-footer">
    <button type="button" class="button secondary" data-system-close>取消</button>${submit(label, action)}</footer></form>`;
}

function choices(name: string, title: string, items: Entry[], selected: string[] | null = []): string {
  return `<fieldset class="system-choice-group"><legend>${title}</legend>
    ${items.length ? `<div class="system-choice-list">${items.map((item) => `<label><input type="checkbox"
      name="${name}" value="${h(item.id)}"${selected?.includes(item.id) ? " checked" : ""}><span>${h(item.name)}</span></label>`).join("")}</div>` :
      "<p>暂无可选项。</p>"}</fieldset>`;
}

function assignmentEditor(access: Access, account?: Account): string {
  const roles = access.roles.filter((role) => role.id !== "system-admin");
  return editor(account ? "assign" : "batch", account ? "分配用户组或角色" : "批量授权 / 撤销",
    operationForm("accounts", account?.id ?? "", `
      <p data-assignment-target>${account ? h(account.username) : ""}</p>
      <div class="system-field-grid">
        <label>授权对象<select data-assignment-kind><option value="groups">用户组</option><option value="roles">直接角色</option></select></label>
        <label>操作<select data-assignment-mode><option value="add">授予</option><option value="remove">撤销</option></select></label>
      </div>
      <div data-assignment-options="groups">${choices("groups", "选择用户组", access.groups)}</div>
      <div data-assignment-options="roles" hidden>${choices("roles", "选择直接角色", roles)}</div>
    `, "提交", "assignment"));
}

function accountDetail(user: Account, access: Access, superAdmin: boolean): string {
  const allowed = user.kind !== "super" && (user.kind !== "administrator" || superAdmin);
  const action = user.kind === "administrator" ? "delete" : user.disabled ? "enable" : "disable";
  const label = user.kind === "administrator" ? "删除系统管理员" : user.disabled ? "重新启用" : "停用账号";
  return `<section class="system-card system-detail">
    <header class="system-card-heading"><div><h3>${h(user.username)}</h3><code>${h(user.id)}</code></div>
      <span class="system-badge">${h(statusOf(user))}</span></header>
    ${allowed ? `<div class="system-actions">${editButton(label, "account-action", true)}
      ${user.kind !== "ldap" ? editButton("重置密码", "reset", true) : ""}</div>
      ${editor("account-action", label, operationForm("accounts", user.id,
        `<p>确认${label.replace("账号", "")}「${h(user.username)}」？</p>`, "确认", action))}
      ${user.kind !== "ldap" ? editor("reset", "重置密码", operationForm("accounts", user.id, tempPassword(), "重置为临时密码", "reset")) : ""}
    ` : ""}
    <nav class="identity-nav" aria-label="用户详情"><button type="button" data-system-tab="effective" aria-pressed="true">生效权限</button>
      <button type="button" data-system-tab="bindings" aria-pressed="false">关联关系</button></nav>
    <div data-system-pane="effective"><h4>生效权限及来源</h4>${authTable(user)}</div>
    <div data-system-pane="bindings" hidden><dl><dt>用户组</dt><dd>${names(user.groups, access.groups)}</dd>
      <dt>直接角色</dt><dd>${names(user.roles, access.roles)}</dd></dl>
      ${user.kind === "ldap" ? editButton("分配或撤销", "assign") : ""}</div>
    ${user.kind === "ldap" ? assignmentEditor(access, user) : ""}
  </section>`;
}

async function renderAccounts(id: string | undefined, superAdmin: boolean): Promise<string> {
  const [users, access] = await Promise.all([admin<Account[]>("accounts"), admin<Access>("access")]);
  const user = id ? users.find((item) => item.id === decodeURIComponent(id)) : users[0];
  const create = (kind: string, title: string) => editor(kind, title, operationForm("accounts", "",
    `<input type="hidden" name="kind" value="${kind}"><label>账号<input name="name" required maxlength="128" autocomplete="off"></label>
    ${tempPassword()}<p>账号使用临时密码登录后需修改密码。</p>`, title, "create"));
  const stats = [["平台账号", users.length], ["LDAP 员工", users.filter((item) => item.kind === "ldap").length],
    ["本地访客", users.filter((item) => item.kind === "guest").length]];
  return `<div class="platform-heading"><h2>用户管理</h2><div class="system-actions">
    ${editButton("＋ 创建访客", "guest")}${superAdmin ? editButton("创建系统管理员", "administrator", true) : ""}
    </div></div>
    <div class="system-stats">${stats.map(([label, count]) => `<div class="system-card"><span>${label}</span><strong>${count}</strong></div>`).join("")}</div>
    <div class="system-user-layout"><section class="system-card">
    <div class="platform-filters system-field-grid">
      <label>搜索账号<input type="search" data-platform-filter="query" placeholder="输入账号名或身份"></label>
      <label>账号类型<select data-platform-filter="kind"><option value="">全部类型</option>${options(Object.entries(kinds).map(([id,name]) => ({ id,name })))}</select></label>
      <label>账号状态<select data-platform-filter="status"><option value="">全部状态</option><option>正常</option><option>待改密</option><option>平台已停用</option><option>目录已停用</option></select></label>
    </div>
    <div class="system-actions system-selection"><span aria-live="polite" data-platform-selected>已选 0 项</span>
      <button type="button" class="button" data-system-open="batch" data-platform-batch disabled>批量授权 / 撤销</button>
      <button type="button" class="button secondary" data-platform-clear disabled>取消选择</button></div>
    <div class="catalog-table-wrap"><table class="catalog-table">
      <thead><tr><th><input type="checkbox" data-platform-select-all aria-label="选择当前结果"${users.some((item) => item.kind !== "super") ? "" : " disabled"}></th><th>账号</th><th>身份</th><th>状态</th><th>操作</th></tr></thead>
      <tbody>${users.map((item) => `<tr data-platform-row data-kind="${h(item.kind)}" data-status="${h(statusOf(item))}"${item.id === user?.id ? ' class="system-current-row"' : ""}><td><input type="checkbox" name="ids" value="${h(item.id)}" aria-label="选择 ${h(item.username)}"${item.kind === "super" ? " disabled" : ""}></td>
        <td><a href="#/admin/users/${encodeURIComponent(item.id)}">${h(item.username)}</a><br><small>${h(item.id)}</small></td>
        <td>${h(kinds[item.kind])}</td><td><span class="system-badge">${h(statusOf(item))}</span></td>
        <td><a href="#/admin/users/${encodeURIComponent(item.id)}">查看详情</a></td></tr>`).join("")}
      <tr data-platform-empty${users.length ? " hidden" : ""}><td colspan="5">暂无符合条件的账号。</td></tr></tbody></table></div></section>
      ${user ? accountDetail(user, access, superAdmin) : `<section class="system-card system-detail"><p>${id ? "账号不存在。" : "请选择一个账号。"}</p></section>`}
    </div>
    ${assignmentEditor(access)}
    ${create("guest", "创建访客")}${superAdmin ? create("administrator", "创建系统管理员") : ""}`;
}

function clusterScopeOptions(clusters: Entry[], current: string | undefined): string {
  const aliases: Record<string, string> = { ...Object.fromEntries(Object.keys(environments).map((id) => [id, id])),
    ...Object.fromEntries(Object.entries(environments).map(([id, label]) => [label, id])), dev: "development", test: "testing", prod: "production" };
  return clusters.map((cluster) => `<option value="${h(cluster.id)}" data-scope-env="${h(aliases[cluster.environment ?? ""] ?? "")}"
    ${cluster.id === current ? " selected" : ""}>${h(cluster.name)}</option>`).join("");
}

function scopeInputs(scope: Scope | undefined, clusters: Entry[], businesses: Entry[]): string {
  const env = scope?.environment ?? "development";
  const restrictBusiness = env === "production" || env === "staging";
  const selectedCluster = clusters.some((cluster) => cluster.id === scope?.cluster);
  const selectedBusiness = businesses.some((business) => business.id === scope?.business);
  return `<fieldset data-scope class="platform-scope">
    <label>环境<select name="environment">${options(Object.entries(environments).map(([id, name]) => ({ id, name })), env)}</select></label>
    <label>集群<select name="cluster"><option value="">${restrictBusiness ? "请选择" : "整个环境"}</option>${clusterScopeOptions(clusters, scope?.cluster)}
      ${!selectedCluster && scope?.cluster ? options([{ id: scope.cluster, name: scope.cluster }], scope.cluster) : ""}</select></label>
    <label data-scope-business${restrictBusiness ? "" : " hidden"}>业务线<select name="business"${restrictBusiness ? "" : " disabled"}><option value="">请选择</option>${options(businesses, scope?.business)}
      ${!selectedBusiness && scope?.business ? options([{ id: scope.business, name: scope.business }], scope.business) : ""}</select></label>
    <label>命名空间<select name="namespace" data-scope-namespace${scope?.cluster ? "" : " disabled"}><option value="">${restrictBusiness ? "请选择" : "全部"}</option>
      ${scope?.namespace ? options([{ id: scope.namespace, name: scope.namespace }], scope.namespace) : ""}</select></label>
    <button type="button" class="button secondary" data-scope-remove>移除此项</button>
  </fieldset>`;
}

async function renderAccess(tab?: string): Promise<string> {
  const [access, clusters, businesses, users] = await Promise.all([
    admin<Access>("access"), json<{ items: Entry[] }>("/api/v1/clusters"), admin<Entry[]>("business-lines"), admin<Account[]>("accounts"),
  ]);
  const groups = tab === "groups";
  const available = access.roles.filter((role) => role.id !== "system-admin");
  const groupEditor = (group?: Group) => editor(`group:${group?.id ?? ""}`, group ? "编辑用户组" : "新建用户组",
    operationForm("access", group?.id ?? "", `<label>用户组名称<input name="name" required maxlength="128" value="${h(group?.name)}"></label>
      <label>用途<input name="description" maxlength="256" value="${h(group?.description)}"></label>
      <div data-replace-group-roles>${choices("roles", "关联角色", available, group?.roles)}</div>`, "保存用户组", "group-save"));
  const roleEditor = (role?: Role) => editor(`role:${role?.id ?? ""}`, role ? "编辑角色" : "新建角色",
    operationForm("access", role?.id ?? "", `<label>角色名称<input name="name" required maxlength="128" value="${h(role?.name)}"></label>
      <h3>历史诊断查看</h3>
      <div data-scope-list>${(role?.scopes ?? []).map((scope) => scopeInputs(scope, clusters.items, businesses)).join("")}</div>
      <p class="system-scope-empty">请添加至少一项授权范围。</p>
      <template>${scopeInputs(undefined, clusters.items, businesses)}</template>
      <button type="button" class="button secondary" data-scope-add>＋ 添加授权范围</button>`, "保存角色", "role-save"));
  const remove = (item: Entry, kind: "group" | "role") => editor(`remove:${item.id}`, "确认删除",
    operationForm("access", item.id, `<p>确定删除「${h(item.name)}」？存在引用时会保留该条目。</p>`, "确认删除", `${kind}-delete`));
  const headers = groups ? ["用户组", "关联角色", "成员", "操作"] : ["角色", "范围", "引用", "操作"];
  const rows = groups ? access.groups.map((group) => `<tr><td><strong>${h(group.name)}</strong><br><small>${h(group.description)}</small></td>
    <td>${names(group.roles, access.roles)}</td><td>${users.filter((user) => user.groups?.includes(group.id)).length} 人</td>
    <td><div class="system-actions">${editButton("编辑", `group:${group.id}`, true)}${editButton("删除", `remove:${group.id}`, true)}</div></td></tr>`)
    : access.roles.map((role) => `<tr><td><strong>${h(role.name)}</strong>${role.fixed ? ' <span class="system-badge">预置</span>' : ""}</td>
    <td>${role.id === "system-admin" ? "全局管理" : `${role.scopes?.length ?? 0} 项历史诊断权限`}</td>
    <td>${users.filter((user) => user.roles?.includes(role.id)).length + access.groups.filter((group) => group.roles?.includes(role.id)).length} 处</td>
    <td><div class="system-actions">${editButton(role.fixed ? "查看" : "编辑", `role:${role.id}`, true)}
    ${role.fixed ? "" : editButton("删除", `remove:${role.id}`, true)}</div></td></tr>`);
  return `<div class="platform-heading"><h2>权限配置</h2>${groups ? editButton("＋ 新建用户组", "group:") : editButton("＋ 新建角色", "role:")}</div>
    <section class="system-card"><nav class="identity-nav" aria-label="权限配置">
      <a href="#/admin/access/roles"${groups ? "" : ' aria-current="page"'}>角色</a>
      <a href="#/admin/access/groups"${groups ? ' aria-current="page"' : ""}>用户组</a>
    </nav><div class="catalog-table-wrap"><table class="catalog-table">
      <thead><tr>${headers.map((title) => `<th>${title}</th>`).join("")}</tr></thead><tbody>
      ${rows.join("") || '<tr><td colspan="4">暂无用户组。</td></tr>'}</tbody></table></div></section>
    ${groups ? groupEditor() + access.groups.map((group) => groupEditor(group) + remove(group, "group")).join("") :
      roleEditor() + access.roles.map((role) => role.fixed ? editor(`role:${role.id}`, role.name,
        `<div class="system-editor-body">${role.scopes?.map((scope) => `<p>历史诊断查看 · ${scopeText(scope)}</p>`).join("") || "<p>管理系统与全局诊断</p>"}</div>`)
        : roleEditor(role) + remove(role, "role")).join("")}`;
}

function auditDetail(item: Operation): string {
  return editor(`audit:${item.id}`, "审计详情", `<div class="system-editor-body">
    <h3>${h(item.action)}</h3><p>${h(item.risk)}风险 · ${h(item.result)}</p>
    <dl><dt>操作者</dt><dd>${h(item.actor || "—")}</dd><dt>目标</dt><dd>${h(item.target)}</dd>
      <dt>时间</dt><dd>${h(new Date(item.at * 1000).toLocaleString("zh-CN"))}</dd>
      ${item.reason ? `<dt>失败原因</dt><dd>${h(item.reason)}</dd>` : ""}</dl></div>
    <footer class="system-dialog-footer">${item.batch ? `<button class="button secondary" type="button" data-audit-batch="${h(item.batch)}">查看同批次</button>` : ""}
      <button class="button" type="button" data-system-close>关闭</button></footer>`);
}

function operationRows(items: Operation[]): string {
  if (items.length === 0) return '<tr><td colspan="7">暂无匹配记录。</td></tr>';
  return items.map((item) => `<tr><td>${h(new Date(item.at * 1000).toLocaleString("zh-CN"))}</td><td>${h(item.actor || "—")}</td>
    <td>${h(item.action)}</td><td>${h(item.risk)}</td><td>${h(item.target)}</td><td>${h(item.result)}</td>
    <td>${editButton("详情", `audit:${item.id}`, true)}${auditDetail(item)}</td></tr>`).join("");
}

async function renderOperations(): Promise<string> {
  const { items, before } = await admin<{ items: Operation[]; before: number }>("operations");
  return `<h2>操作审计</h2><form class="platform-audit-filter" data-audit-filter>
    <label>账号或目标<input name="account" type="search"></label>
    <label>操作类型<input name="action" placeholder="例如：登录"></label>
    <label>风险等级<select name="risk"><option value="">全部</option><option>低</option><option>中</option><option>高</option></select></label>
    <input type="hidden" name="batch">
    ${submit("查询")}<button class="button secondary" type="submit" data-audit-next value="${before}"${before ? "" : " hidden"}>下一页</button>
    <p role="status"></p>
  </form><div class="catalog-table-wrap"><table class="catalog-table">
    <thead><tr><th>时间</th><th>操作者</th><th>操作类型</th><th>风险等级</th><th>目标</th><th>结果</th><th>操作</th></tr></thead>
    <tbody data-audit-results>${operationRows(items)}</tbody></table></div>`;
}

export function renderPlatform(resource: string, id: string | undefined, superAdmin: boolean): Promise<string> {
  if (resource === "users") return renderAccounts(id, superAdmin);
  if (resource === "access") return renderAccess(id);
  return renderOperations();
}

export async function submitPlatform(form: HTMLFormElement, action: string, reload: () => void): Promise<void> {
  const status = form.querySelector<HTMLElement>('[role="status"]');
  const buttons = [...form.querySelectorAll<HTMLButtonElement>('button[type="submit"]')];
  if (!status || buttons.some((button) => button.disabled)) return;
  if (form.querySelector("[data-scope][aria-busy]")) {
    status.textContent = "正在加载授权范围，请稍后提交。";
    return;
  }
  const data = new FormData(form);
  if (action === "assignment") {
    const group = form.querySelector<HTMLSelectElement>("[data-assignment-kind]")?.value === "groups";
    const remove = form.querySelector<HTMLSelectElement>("[data-assignment-mode]")?.value === "remove";
    action = group ? (remove ? "leave" : "join") : (remove ? "revoke" : "grant");
  }
  const values = data.getAll(action === "join" || action === "leave" ? "groups" : "roles");
  const ids = data.getAll("ids");
  if ((!form.dataset.id && ["join", "leave", "grant", "revoke"].includes(action) && !ids.length) ||
      (["join", "leave", "grant", "revoke", "group-roles-add", "group-roles-remove"].includes(action) && !values.length)) {
    status.textContent = "请选择目标及授权项。";
    return;
  }
  const scopes = [...form.querySelectorAll("[data-scope]")].map((row) => Object.fromEntries(
    [...row.querySelectorAll<HTMLSelectElement>("select")].map((select) => [select.name, select.value]),
  ));
  if (action === "role-save" && scopes.length === 0) {
    status.textContent = "请添加至少一项授权范围。";
    return;
  }
  for (const button of buttons) button.disabled = true;
  const dialog = form.closest<HTMLDialogElement>("dialog");
  dialog?.setAttribute("aria-busy", "true");
  status.textContent = "正在保存…";
  let completedBatch = false;
  try {
    const response = await fetch(`/api/v1/admin/${form.dataset.platform}`, {
      method: "POST", headers: { "Content-Type": "application/json" },
      body: JSON.stringify({ action, id: form.dataset.id, ids, values, scopes,
        roleIds: form.querySelector("[data-replace-group-roles]") ? data.getAll("roles") : undefined,
        name: data.get("name") ?? "", description: data.get("description") ?? "", kind: data.get("kind") ?? "", password: data.get("password") ?? "" }),
    });
    if (!response.ok) {
      status.textContent = (await response.text()).trim() || "操作失败。";
      return;
    }
    const results = await response.json() as { id: string; reason?: string }[];
    if (ids.length > 0 || action.startsWith("group-roles")) {
      status.textContent = results.map(({ id, reason }) => `${id}：${reason || "成功"}`).join("；");
      const refresh = document.createElement("button");
      refresh.className = "button secondary";
      refresh.type = "button";
      completedBatch = true;
      refresh.textContent = "完成并刷新";
      if (dialog) {
        dialog.addEventListener("close", reload, { once: true });
        refresh.addEventListener("click", () => dialog.close(), { once: true });
      } else refresh.addEventListener("click", reload, { once: true });
      status.append(" ", refresh);
    } else if (action === "delete") window.location.hash = "#/admin/users";
    else { dialog?.close(); reload(); }
  } catch {
    status.textContent = "请求失败，请重试。";
  } finally {
    const password = form.querySelector<HTMLInputElement>('[name="password"]');
    if (password) password.value = "";
    dialog?.removeAttribute("aria-busy");
    for (const button of buttons) button.disabled = completedBatch;
  }
}

export async function queryAudit(form: HTMLFormElement, before = "", batch = ""): Promise<void> {
  const panel = form.closest(".identity-panel");
  const body = panel?.querySelector("[data-audit-results]");
  const status = form.querySelector('[role="status"]');
  const next = form.querySelector<HTMLButtonElement>("[data-audit-next]");
  const batchInput = form.querySelector<HTMLInputElement>('[name="batch"]');
  if (!body || !status || !next || !batchInput || next.disabled) return;
  batchInput.value = batch || (before ? batchInput.value : "");
  const query = new URLSearchParams();
  for (const [name, value] of new FormData(form)) {
    if (value) query.set(name, String(value).trim());
  }
  if (batchInput.value) {
    query.delete("account");
    query.delete("action");
    query.delete("risk");
  }
  if (before) query.set("before", before);
  next.disabled = true;
  try {
    const page = await admin<{ items: Operation[]; before: number }>(`operations?${query}`);
    body.innerHTML = operationRows(page.items);
    next.value = String(page.before);
    next.hidden = !page.before;
    status.textContent = batchInput.value ? "正在查看同一批次的记录。" : "";
  } catch {
    status.textContent = "审计记录查询失败。";
  } finally { next.disabled = false; }
}

export function handlePlatformClick(event: MouseEvent): void {
  if (!(event.target instanceof HTMLElement)) return;
  const target = event.target;
  const panel = target.closest(".identity-panel");
  const dialog = panel?.querySelector<HTMLDialogElement>("[data-system-dialog]");
  const opener = target.closest<HTMLButtonElement>("[data-system-open]");
  if (opener && panel && dialog && !dialog.open) {
    const key = opener.dataset.systemOpen;
    const template = [...panel.querySelectorAll<HTMLTemplateElement>("[data-system-editor]")]
      .find((item) => item.dataset.systemEditor === key);
    if (template) {
      dialog.replaceChildren(template.content.cloneNode(true));
      if (key === "batch") {
        const form = dialog.querySelector("form");
        const selected = [...panel.querySelectorAll<HTMLInputElement>('[data-platform-row] input[name="ids"]:checked:not(:disabled)')];
        if (!form || !selected.length) return;
        for (const account of selected) {
          const hidden = document.createElement("input");
          hidden.type = "hidden";
          hidden.name = "ids";
          hidden.value = account.value;
          form.append(hidden);
        }
        const status = form.querySelector("[data-assignment-target]");
        if (status) status.textContent = `已选 ${selected.length} 个账号`;
      }
      dialog.oncancel = (event) => { if (dialog.hasAttribute("aria-busy")) event.preventDefault(); };
      dialog.showModal();
      for (const scope of dialog.querySelectorAll<HTMLElement>("[data-scope]")) void refreshScope(scope, true);
    }
    return;
  }
  if (target.closest("[data-system-close]") && dialog?.open && !dialog.hasAttribute("aria-busy")) dialog.close();
  const tab = target.closest<HTMLButtonElement>("[data-system-tab]");
  const detail = tab?.closest(".system-detail");
  if (tab && detail) {
    for (const item of detail.querySelectorAll("[data-system-tab]")) item.setAttribute("aria-pressed", String(item === tab));
    for (const pane of detail.querySelectorAll<HTMLElement>("[data-system-pane]")) pane.hidden = pane.dataset.systemPane !== tab.dataset.systemTab;
  }
  if (target.closest("[data-platform-clear]") && panel) {
    for (const box of panel.querySelectorAll<HTMLInputElement>('[data-platform-row] input[name="ids"]:checked')) box.checked = false;
    const master = panel.querySelector<HTMLInputElement>("[data-platform-select-all]");
    if (master) {
      master.checked = false;
      master.dispatchEvent(new Event("change", { bubbles: true }));
    }
  }
  if (target.closest("[data-scope-add]")) {
    const form = target.closest("form");
    const template = form?.querySelector("template");
    if (template) {
      const list = form?.querySelector("[data-scope-list]");
      list?.append(template.content.cloneNode(true));
      if (list?.lastElementChild instanceof HTMLElement) void refreshScope(list.lastElementChild);
    }
  }
  if (target.closest("[data-scope-remove]")) target.closest("[data-scope]")?.remove();
  const batch = target.closest<HTMLButtonElement>("[data-audit-batch]")?.dataset.auditBatch;
  const filter = target.closest(".identity-panel")?.querySelector<HTMLFormElement>("[data-audit-filter]");
  if (batch && filter) {
    dialog?.close();
    void queryAudit(filter, "", batch);
  }
}

export function filterAccounts(event: Event): void {
  const target = event.target;
  if (!(target instanceof HTMLInputElement || target instanceof HTMLSelectElement) ||
      !target.matches('[data-platform-filter], [data-platform-select-all], input[name="ids"]')) return;
  const panel = target.closest(".identity-panel");
  if (!panel) return;
  const field = (key: string) => panel.querySelector<HTMLInputElement | HTMLSelectElement>(`[data-platform-filter="${key}"]`)?.value ?? "";
  const search = field("query").trim().toLowerCase();
  const kind = field("kind");
  const status = field("status");
  const visible: HTMLInputElement[] = [];
  let matching = 0;
  for (const row of panel.querySelectorAll<HTMLElement>("[data-platform-row]")) {
    row.hidden = Boolean((kind && row.dataset.kind !== kind) || (status && row.dataset.status !== status) ||
      !row.textContent?.toLowerCase().includes(search));
    if (!row.hidden) matching++;
    const box = row.querySelector<HTMLInputElement>('input[name="ids"]');
    if (box?.checked && row.hidden) box.checked = false;
    if (box && !box.disabled && !row.hidden) visible.push(box);
  }
  const all = panel.querySelector<HTMLInputElement>("[data-platform-select-all]");
  if (all && target === all) for (const item of visible) item.checked = all.checked;
  const selected = visible.filter((item) => item.checked).length;
  if (all) {
    all.disabled = visible.length === 0;
    all.checked = visible.length > 0 && selected === visible.length;
    all.indeterminate = selected > 0 && selected < visible.length;
  }
  for (const button of panel.querySelectorAll<HTMLButtonElement>("[data-platform-batch], [data-platform-clear]")) button.disabled = selected === 0;
  const count = panel.querySelector("[data-platform-selected]");
  if (count) count.textContent = `已选 ${selected} 项`;
  const empty = panel.querySelector<HTMLElement>("[data-platform-empty]");
  if (empty) empty.hidden = matching > 0;
}

const scopeRequests = new WeakMap<HTMLElement, AbortController>();

export async function updateScopeOptions(event: Event): Promise<void> {
  const target = event.target;
  if (!(target instanceof HTMLSelectElement)) return;
  if (target.hasAttribute("data-assignment-kind")) {
    for (const pane of target.form?.querySelectorAll<HTMLElement>("[data-assignment-options]") ?? []) {
      pane.hidden = pane.dataset.assignmentOptions !== target.value;
    }
  }
  const field = target.closest<HTMLElement>("[data-scope]");
  if (field && ["cluster", "business", "environment"].includes(target.name)) {
    if (target.name === "environment") {
      const cluster = field.querySelector<HTMLSelectElement>('[name="cluster"]');
      if (cluster) cluster.value = "";
    }
    await refreshScope(field);
  }
}

async function refreshScope(field: HTMLElement, preserve = false): Promise<void> {
  const cluster = field.querySelector<HTMLSelectElement>('[name="cluster"]');
  const business = field.querySelector<HTMLSelectElement>('[name="business"]');
  const namespace = field.querySelector<HTMLSelectElement>("[data-scope-namespace]");
  const env = field.querySelector<HTMLSelectElement>('[name="environment"]');
  if (!cluster || !business || !namespace || !env) return;
  scopeRequests.get(field)?.abort();
  const controller = new AbortController();
  scopeRequests.set(field, controller);
  const selectedNamespace = preserve ? namespace.value : "";
  const restricted = env.value === "production" || env.value === "staging";
  if (cluster.options[0]) cluster.options[0].text = restricted ? "请选择" : "整个环境";
  for (const option of cluster.options) {
    option.hidden = Boolean(option.dataset.scopeEnv && option.dataset.scopeEnv !== env.value);
    option.disabled = option.hidden;
  }
  const businessField = business.closest<HTMLElement>("[data-scope-business]");
  if (businessField) businessField.hidden = !restricted;
  business.disabled = !restricted || !cluster.value;
  if (!restricted) business.value = "";
  const placeholder = `<option value="">${restricted ? "请选择" : "全部"}</option>`;
  namespace.disabled = true;
  namespace.innerHTML = placeholder;
  if (!cluster.value || (restricted && !business.value)) {
    field.removeAttribute("aria-busy");
    return;
  }
  field.setAttribute("aria-busy", "true");
  try {
    const response = await fetch(`/api/v1/admin/namespace-business?cluster=${encodeURIComponent(cluster.value)}`, { signal: controller.signal });
    if (!response.ok) throw new Error("namespaces unavailable");
    const data = await response.json() as { namespaces: { name: string }[]; assigned: Record<string, string[]> };
    if (scopeRequests.get(field) !== controller) return;
    const names = (data.namespaces ?? []).filter(({ name }) => !restricted || data.assigned[name]?.includes(business.value));
    const entries = names.map(({ name }) => ({ id: name, name }));
    if (selectedNamespace && !entries.some(({ id }) => id === selectedNamespace)) {
      entries.push({ id: selectedNamespace, name: `${selectedNamespace}（原配置）` });
    }
    namespace.innerHTML += options(entries, selectedNamespace);
    namespace.disabled = false;
  } catch {
    if (controller.signal.aborted) return;
    namespace.innerHTML = '<option value="">命名空间暂不可用</option>';
    if (selectedNamespace) namespace.innerHTML += options([{ id: selectedNamespace, name: selectedNamespace }], selectedNamespace);
  } finally {
    if (scopeRequests.get(field) === controller) field.removeAttribute("aria-busy");
  }
}
