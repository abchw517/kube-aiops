import { escapeHtml as h } from "../../components/html.ts";

import { auditLocation, auditRecordReference } from "./audit-data.ts";
import { roleReferences, scopeDifference, type Scope } from "./platform-scopes.ts";
import { bindPermissionCatalog, catalogSource, describePermission, loadPermissionCatalog,
  renderPermissionCatalog, selectedPermissions, updateCatalogGrants, type InterfaceGrant, type PermissionCatalog } from "./permission-catalog.ts";
type Role = { id: string; name: string; fixed?: boolean; scopes: Scope[] | null; grants?: InterfaceGrant[] };
type Group = { id: string; name: string; description?: string; roles: string[] | null };
type Account = {
  id: string; username: string; kind: string; disabled: boolean; directoryDisabled: boolean;
  mustChangePassword: boolean; groups: string[] | null; roles: string[] | null;
  lastLoginAt?: number;
  effective: (Scope & { sources: string[] })[] | null;
  effectiveGrants?: (InterfaceGrant & { sources: string[] })[];
};
type Access = { groups: Group[]; roles: Role[] };
type Entry = { id: string; name: string; environment?: string };
const systemAdministratorRole = "system-admin";
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

/** LDAP 账号保留目录身份，其直接管理员角色仍赋予全局权限。 */
function hasAdministratorRole(user: Account): boolean {
  return user.kind === "administrator" || user.roles?.includes(systemAdministratorRole) === true;
}

/** 仅超级管理员可以维护其他拥有管理员角色的账号。 */
function canManageAccount(user: Account, superAdmin: boolean): boolean {
  return user.kind !== "super" && (superAdmin || !hasAdministratorRole(user));
}

function effectivePermissions(user: Account): string {
  if (user.disabled || user.directoryDisabled || user.mustChangePassword) {
    return '<p class="system-notice">账号当前无法访问诊断数据。启用并完成所需的改密后，按关联角色生效。</p>';
  }
  if (user.kind === "super" || hasAdministratorRole(user)) {
    return '<div class="system-permission"><strong>全局管理与诊断查看</strong><p>直接授予 → 系统管理员</p></div>';
  }
  if (!user.effective?.length && !user.effectiveGrants?.length) return "<p>暂无生效权限。</p>";
  const display = (label: string, scope: Scope, sources: string[]) => `<div class="system-permission">
    <div class="system-card-heading"><strong>${h(label)}</strong><span class="system-badge">${sources.length} 条来源</span></div>
    <p>${scopeText(scope) || "该动作适用范围"}</p><ul>${sources.map(source => `<li>${h(source)}</li>`).join("")}</ul></div>`;
  return `<div class="system-permissions">${(user.effective ?? []).map(scope => display("历史诊断查看", scope, scope.sources)).join("")}
    ${(user.effectiveGrants ?? []).map(item => display(item.code, item.scope, item.sources)).join("")}</div>`;
}

function accountAvatar(user: Account): string {
  return `<span class="system-avatar" aria-hidden="true">${h(Array.from(user.username)[0]?.toUpperCase())}</span>`;
}

function statusOf(user: Account): string {
  if (user.disabled) return "平台已停用";
  if (user.directoryDisabled) return "目录已停用";
  return user.mustChangePassword ? "待改密" : "正常";
}

function accountStatus(user: Account): string {
  const status = statusOf(user);
  return `<span class="system-badge account-status" data-account-status="${h(status)}">${h(status)}</span>`;
}

function tempPassword(): string {
  return '<label>临时密码<input name="password" type="password" autocomplete="new-password" minlength="12" maxlength="72" required></label>';
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

function choices(name: string, title: string, items: Entry[], selected: string[] | null = [], assignment = false): string {
  return `<fieldset class="system-choice-group"><legend>${title}</legend>
    ${items.length ? `<div class="system-choice-list">${items.map((item) => `<label${assignment ? ` data-assignment-choice data-associated="${selected?.includes(item.id) === true}"` : ""}><input type="checkbox"
      name="${name}" value="${h(item.id)}"${!assignment && selected?.includes(item.id) ? " checked" : ""}><span>${h(item.name)}</span>
      ${assignment && selected?.includes(item.id) ? '<small class="system-badge">已关联</small>' : ""}</label>`).join("")}</div>` :
      "<p>暂无可选项。</p>"}</fieldset>`;
}

/** 直接角色分配依据操作者权限开放管理员角色。 */
function assignmentEditor(access: Access, superAdmin: boolean, account?: Account): string {
  const roles = superAdmin ? access.roles : access.roles.filter((role) => role.id !== systemAdministratorRole);
  return editor(account ? "assign" : "batch", account ? "分配用户组或角色" : "批量授权 / 撤销",
    operationForm("accounts", account?.id ?? "", `
      <p data-assignment-target>${account ? h(account.username) : ""}</p>
      <div class="system-field-grid">
        <label>授权对象<select data-assignment-kind><option value="groups">用户组</option><option value="roles">直接角色</option></select></label>
        <label>操作<select data-assignment-mode><option value="add">授予</option><option value="remove">撤销</option></select></label>
      </div>
      <label>搜索可分配项<input type="search" data-assignment-search autocomplete="off" placeholder="搜索用户组或角色"></label>
      <div data-assignment-options="groups">${choices("groups", "选择用户组", access.groups, account?.groups, true)}</div>
      <div data-assignment-options="roles" hidden>${choices("roles", "选择直接角色", roles, account?.roles, true)}
        ${superAdmin ? "<p>管理员角色发生变更时，目标账号需重新登录。</p>" : ""}</div>
      <p data-assignment-empty hidden>没有可操作的关联。</p>
    `, "提交", "assignment"));
}

function canDeleteAccount(user: Account, superAdmin: boolean): boolean {
  return canManageAccount(user, superAdmin) && ["guest", "ldap", "administrator"].includes(user.kind);
}

function accountBinding(label: string, ids: string[] | null, items: Entry[], empty: string): string {
  return `<div class="account-binding"><dt>${label}</dt><dd>${ids?.length ?
    ids.map((id) => `<span class="system-badge">${h(items.find((item) => item.id === id)?.name ?? id)}</span>`).join("") :
    `<span class="account-binding-empty">${empty}</span>`}</dd></div>`;
}

function accountDetail(user: Account, access: Access, superAdmin: boolean): string {
  const allowed = canManageAccount(user, superAdmin);
  const canAssign = allowed && user.kind === "ldap";
  const bindingAction = user.kind !== "ldap" ? "<small>本地账号通过预置角色获得权限。</small>" :
    canAssign ? "" : "<small>仅超级管理员可维护此账号的关联关系。</small>";
  const action = user.kind === "administrator" ? "delete" : user.disabled ? "enable" : "disable";
  const label = user.kind === "administrator" ? "删除系统管理员" : user.disabled ? "重新启用" : "停用账号";
  return `<section class="system-card system-detail" data-user-detail="${h(user.id)}">
    <header class="account-detail-header">
      <div class="system-user-cell">${accountAvatar(user)}<div class="account-identity"><h3>${h(user.username)}</h3><code>${h(user.id)}</code></div></div>
      <div class="account-detail-actions">${accountStatus(user)}<div class="system-actions">
        ${canAssign ? editButton("分配或撤销", "assign") : ""}
        ${allowed ? `<span data-destructive="${action !== "enable"}">${editButton(label, "account-action", true)}</span>
          ${user.kind !== "ldap" ? editButton("重置密码", "reset", true) : ""}` : ""}
        <a class="button secondary" href="${h(auditLocation({ account: user.id }))}">账号审计</a></div>
      </div>
    </header>
    ${allowed ? `
      ${editor("account-action", label, operationForm("accounts", user.id,
        `<p>确认${label.replace("账号", "")}「${h(user.username)}」？</p>`, "确认", action))}
      ${user.kind !== "ldap" ? editor("reset", "重置密码", operationForm("accounts", user.id, tempPassword(), "重置为临时密码", "reset")) : ""}
    ` : ""}
    <nav class="identity-nav" aria-label="用户详情"><button type="button" data-system-tab="effective" aria-pressed="true">生效权限</button>
      <button type="button" data-system-tab="bindings" aria-pressed="false">关联关系</button></nav>
    <div data-system-pane="effective"><h4>生效权限及来源</h4>${effectivePermissions(user)}</div>
    <div data-system-pane="bindings" hidden>
      <dl class="account-bindings">
        ${accountBinding("用户组", user.groups, access.groups, "未加入用户组")}
        ${accountBinding("直接角色", user.roles, access.roles, "未分配直接角色")}
      </dl>
      ${bindingAction ? `<footer class="account-bindings-footer">${bindingAction}</footer>` : ""}
    </div>
    ${canAssign ? assignmentEditor(access, superAdmin, user) : ""}
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
  return `<div class="platform-heading"><div><h2>用户管理</h2><p>管理账号生命周期，并核对每项访问权限的来源。</p></div><div class="system-actions">
    ${editButton("＋ 创建访客", "guest")}${superAdmin ? editButton("创建系统管理员", "administrator", true) : ""}
    </div></div>
    <div class="system-stats">${stats.map(([label, count]) => `<div class="system-card"><span>${label}</span><strong>${count}</strong></div>`).join("")}</div>
    <div class="system-user-layout"><section class="system-card">
    <div class="platform-filters system-field-grid">
      <label>搜索账号<input type="search" name="query" autocomplete="off" data-platform-filter="query" placeholder="输入账号名或身份…"></label>
      <label>账号类型<select data-platform-filter="kind"><option value="">全部类型</option>${options(Object.entries(kinds).map(([id,name]) => ({ id,name })))}</select></label>
      <label>账号状态<select data-platform-filter="status"><option value="">全部状态</option><option>正常</option><option>待改密</option><option>平台已停用</option><option>目录已停用</option></select></label>
    </div>
    <div class="account-selection" role="group" aria-label="选中账号操作">
      <div class="system-actions"><span aria-live="polite" data-platform-selected>已选 0 项</span>
        <button type="button" class="button" data-system-open="batch" data-platform-batch disabled>批量授权 / 撤销</button>
        <button type="button" class="button secondary" data-platform-clear disabled>取消选择</button>
      </div>
      <button type="button" class="button secondary catalog-danger" data-system-open="delete-account" data-platform-delete
        title="选择一个可删除的账号" disabled>删除用户</button>
    </div>
    <div class="catalog-table-wrap" role="region" aria-label="账号列表" tabindex="0"><table class="catalog-table account-table">
      <thead><tr><th scope="col"><input type="checkbox" data-platform-select-all aria-label="选择当前结果"${users.some((item) => canManageAccount(item, superAdmin)) ? "" : " disabled"}></th><th scope="col">账号</th><th scope="col">类型</th><th scope="col">状态</th><th scope="col">最近登录</th></tr></thead>
      <tbody>${users.map((item) => `<tr data-platform-row data-id="${h(item.id)}" data-kind="${h(item.kind)}" data-account-name="${h(item.username)}" data-account-deletable="${canDeleteAccount(item, superAdmin)}" data-status="${h(statusOf(item))}"${item.id === user?.id ? ' class="system-current-row"' : ""}><td><input type="checkbox" name="ids" value="${h(item.id)}" aria-label="选择 ${h(item.username)}"${canManageAccount(item, superAdmin) ? "" : " disabled"}></td>
        <td><div class="system-user-cell">${accountAvatar(item)}<div class="account-identity"><a href="#/admin/users/${encodeURIComponent(item.id)}">${h(item.username)}</a><br><small>${h(item.id)}</small></div></div></td>
        <td>${h(kinds[item.kind])}</td><td>${accountStatus(item)}</td>
        <td>${item.lastLoginAt ? h(new Date(item.lastLoginAt * 1000).toLocaleString("zh-CN")) : "—"}</td></tr>`).join("")}
      <tr data-platform-empty${users.length ? " hidden" : ""}><td colspan="5">暂无符合条件的账号。</td></tr></tbody></table></div><div class="catalog-bottom" data-platform-count>共 ${users.length} 个账号</div></section>
      <div data-account-detail-slot>${user ? accountDetail(user, access, superAdmin) : `<section class="system-card system-detail"><p>${id ? "账号不存在。" : "请选择一个账号。"}</p></section>`}</div>
    </div>
    ${users.filter((item) => item !== user).map((item) => `<template data-account-detail="${h(item.id)}">${accountDetail(item, access, superAdmin)}</template>`).join("")}
    ${assignmentEditor(access, superAdmin)}
    ${editor("delete-account", "删除用户", operationForm("accounts", "",
      '<p>确定删除<strong data-delete-name></strong>？</p><p class="system-notice" data-delete-impact></p>', "确认删除", "delete"))}
    ${create("guest", "创建访客")}${superAdmin ? create("administrator", "创建系统管理员") : ""}`;
}

function clusterScopeOptions(clusters: Entry[], current: string | undefined): string {
  const aliases: Record<string, string> = { ...Object.fromEntries(Object.keys(environments).map((id) => [id, id])),
    ...Object.fromEntries(Object.entries(environments).map(([id, label]) => [label, id])), dev: "development", test: "testing", prod: "production" };
  return clusters.map((cluster) => `<option value="${h(cluster.id)}" data-scope-env="${h(aliases[cluster.environment ?? ""] ?? "")}"
    ${cluster.id === current ? " selected" : ""}>${h(cluster.name)}</option>`).join("");
}

function scopeInputs(clusters: Entry[], businesses: Entry[]): string {
  return `<fieldset data-scope class="platform-scope" aria-label="选择授权范围">
    <label>环境<select name="environment">${options(Object.entries(environments).map(([id, name]) => ({ id, name })), "development")}</select></label>
    <label>集群<select name="cluster"><option value="">整个环境</option>${clusterScopeOptions(clusters, undefined)}</select></label>
    <label data-scope-business hidden>业务线<select name="business" disabled><option value="">请选择</option>${options(businesses)}</select></label>
    <div class="system-scope-namespaces"><fieldset class="system-choice-group"><legend>命名空间</legend>
      <div class="system-choice-list" data-scope-namespace></div>
      <label data-entire-cluster hidden><input type="checkbox" name="entireCluster" disabled>整个集群</label>
      <button type="button" class="button secondary" data-scope-retry hidden>重试加载</button>
    </fieldset></div>
  </fieldset>`;
}

function scopeRow(scope: Scope): string {
  return `<div class="system-scope-row" data-saved-scope="${h(JSON.stringify(scope))}">
    <span><strong>历史诊断查看</strong><br>${scopeText(scope)}</span><button type="button" class="button secondary" data-scope-remove>移除</button></div>`;
}

async function renderAccess(tab?: string): Promise<string> {
  const [access, clusters, businesses, users, catalog] = await Promise.all([
    admin<Access>("access"), json<{ items: Entry[] }>("/api/v1/clusters"), admin<Entry[]>("business-lines"), admin<Account[]>("accounts"),
    loadPermissionCatalog().catch(() => null),
  ]);
  const groups = tab === "groups";
  const labels = new Map(catalog?.rows.flatMap(row => row.points.map(point => [point.code, point.label] as const)));
  const available = access.roles.filter((role) => role.id !== systemAdministratorRole);
  const groupEditor = (group?: Group) => editor(`group:${group?.id ?? ""}`, group ? "编辑用户组" : "新建用户组",
    operationForm("access", group?.id ?? "", `<label>用户组名称<input name="name" required maxlength="128" value="${h(group?.name)}"></label>
      <label>用途<input name="description" maxlength="256" value="${h(group?.description)}"></label>
      <div data-replace-group-roles>${choices("roles", "关联角色", available, group?.roles)}</div>`, "保存用户组", "group-save"));
  const roleEditor = (role?: Role) => {
    const refs = roleReferences(role?.id, users, access.groups);
    return editor(`role:${role?.id ?? ""}`, role ? "编辑角色" : "新建角色",
      `<form class="system-form" data-platform="access" data-id="${h(role?.id)}" data-role-edit
          data-role-original="${h(JSON.stringify(role ?? null))}">
        <div class="system-editor-body" data-role-draft>
          <label>角色名称<input name="name" required maxlength="128" value="${h(role?.name)}"></label>
          <h3>已配置权限</h3>
          <div data-scope-list>${(role?.scopes ?? []).map(scopeRow).join("")}</div>
          <div data-role-grants>${(role?.grants ?? []).map(grant => grantRow(grant, labels.get(grant.code))).join("")}</div>
          <p class="system-scope-empty">尚未添加权限范围。</p>
          ${catalog ? '<div data-role-catalog-host></div>' : '<p>接口权限目录暂不可用。</p>'}
          ${scopeInputs(clusters.items, businesses)}
          <div class="system-actions"><button type="button" class="button secondary" data-scope-add>添加到清单</button>
          <button type="button" class="button" data-scope-add="preview">添加并预览</button>
          ${catalog ? '<button type="button" class="button" data-role-add-grants>将所选动作加入授权</button>' : ""}</div>
        </div>
        <div class="system-editor-body" data-role-preview hidden>
          <h3 tabindex="-1">核对角色变更</h3>
          <p class="system-notice">直接关联 ${refs.direct} 人 · 关联用户组 ${refs.groups} 个 · 涉及账号 ${refs.affected} 人（去重）</p>
          <div data-role-diff></div>
        </div>
        <p class="system-editor-status" role="status" aria-live="polite"></p>
        <footer class="system-dialog-footer">
          <button type="button" class="button secondary" data-system-close>取消</button>
          <button type="button" class="button secondary" data-role-back hidden>返回编辑</button>
          ${submit("预览已配置权限", "role-preview")}
          <button class="button" type="submit" value="role-save" hidden disabled>确认保存</button>
        </footer>
      </form>`);
  };
  const remove = (item: Entry, kind: "group" | "role") => editor(`remove:${item.id}`, "确认删除",
    operationForm("access", item.id, `<p>确定删除「${h(item.name)}」？存在引用时会保留该条目。</p>`, "确认删除", `${kind}-delete`));
  const headers = groups ? ["用户组", "关联角色", "成员", "操作"] : ["角色", "范围", "引用", "操作"];
  const rows = groups ? access.groups.map((group) => `<tr><td><strong>${h(group.name)}</strong><br><small>${h(group.description)}</small></td>
    <td><div class="system-actions">${group.roles?.map((id) => {
      const role = access.roles.find((item) => item.id === id);
      return role ? `<button type="button" class="account-role-link" data-system-open="role:${h(role.id)}">${h(role.name)}</button>` : h(id);
    }).join("") || "—"}</div></td><td>${users.filter((user) => user.groups?.includes(group.id)).length} 人</td>
    <td><div class="system-actions">${editButton("编辑", `group:${group.id}`, true)}${editButton("删除", `remove:${group.id}`, true)}</div></td></tr>`)
    : access.roles.map((role) => `<tr><td><strong>${h(role.name)}</strong>${role.fixed ? ' <span class="system-badge">预置</span>' : ""}</td>
    <td>${role.id === systemAdministratorRole ? "全局管理" : `${role.scopes?.length ?? 0} 项历史诊断范围 · ${role.grants?.length ?? 0} 项接口授权`}</td>
    <td>${users.filter((user) => user.roles?.includes(role.id)).length + access.groups.filter((group) => group.roles?.includes(role.id)).length} 处</td>
    <td><div class="system-actions">${editButton(role.fixed ? "查看" : "编辑", `role:${role.id}`, true)}
    ${role.fixed ? '<span class="system-badge">禁止删除</span>' : editButton("删除", `remove:${role.id}`, true)}</div></td></tr>`);
  return `<div class="platform-heading"><div><h2>权限配置</h2><p>用角色定义数据范围，通过用户组批量授予 LDAP 员工。</p></div><div class="system-actions">${groups ? editButton("＋ 新建用户组", "group:") :
      `<a class="button secondary" href="#/admin/access/catalog">权限目录</a>${editButton("＋ 新建角色", "role:")}`}</div></div>
    <section class="system-card"><nav class="identity-nav" aria-label="权限配置">
      <a href="#/admin/access/roles"${groups ? "" : ' aria-current="page"'}>角色配置</a>
      <a href="#/admin/access/groups"${groups ? ' aria-current="page"' : ""}>用户组</a>
    </nav><div class="catalog-table-wrap" role="region" aria-label="${groups ? "用户组列表" : "角色列表"}" tabindex="0"><table class="catalog-table">
      <thead><tr>${headers.map((title) => `<th scope="col">${title}</th>`).join("")}</tr></thead><tbody>
      ${rows.join("") || '<tr><td colspan="4">暂无用户组。</td></tr>'}</tbody></table></div></section>
    ${groups ? groupEditor() + access.groups.map((group) => groupEditor(group) + remove(group, "group")).join("") : roleEditor()}
    ${catalogSource(catalog)}
    ${access.roles.map((role) => role.fixed ? editor(`role:${role.id}`, role.name,
        `<div class="system-editor-body"><p class="system-notice">预置角色的权限固定，始终禁止删除。</p>${role.scopes?.map((scope) => `<p>历史诊断查看 · ${scopeText(scope)}</p>`).join("") || "<p>管理系统与全局诊断</p>"}</div>`)
        : roleEditor(role) + (groups ? "" : remove(role, "role"))).join("")}`;
}

export function renderPlatform(resource: string, id: string | undefined, superAdmin: boolean): Promise<string> {
  if (resource === "users") return renderAccounts(id, superAdmin);
  return renderAccess(id);
}

function platformStatus(form: HTMLFormElement): HTMLElement | null {
  return form.querySelector<HTMLElement>(".system-editor-status") ?? form.querySelector<HTMLElement>('[role="status"]');
}

export async function submitPlatform(form: HTMLFormElement, action: string, reload: () => void): Promise<void> {
  const status = platformStatus(form);
  const dialog = form.closest<HTMLDialogElement>("dialog");
  if (!status || dialog?.hasAttribute("aria-busy")) return;
  const buttons = [...form.querySelectorAll<HTMLButtonElement>('button[type="submit"]:not(:disabled)')];
  if (action === "role-preview") {
    previewRole(form);
    return;
  }
  if (action === "role-save" && form.querySelector<HTMLElement>("[data-role-preview]")?.hidden !== false) return;
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
  const scopes = savedScopes(form);
  const grants = savedGrants(form);
  if (action === "role-save" && scopes.length === 0 && grants.length === 0) {
    status.textContent = "请添加至少一项授权范围。";
    return;
  }
  for (const button of buttons) button.disabled = true;
  dialog?.setAttribute("aria-busy", "true");
  status.textContent = "正在保存…";
  try {
    const response = await fetch(`/api/v1/admin/${form.dataset.platform}`, {
      method: "POST", headers: { "Content-Type": "application/json" },
      body: JSON.stringify({ action, id: form.dataset.id, ids, values, scopes,
        previousRole: action === "role-save" ? originalRole(form) : undefined,
        grants: action === "role-save" && form.querySelector("[data-role-catalog-host]") ? grants : undefined,
        roleIds: form.querySelector("[data-replace-group-roles]") ? data.getAll("roles") : undefined,
        name: data.get("name") ?? "", description: data.get("description") ?? "", kind: data.get("kind") ?? "", password: data.get("password") ?? "" }),
    });
    if (!response.ok) {
      status.textContent = (await response.text()).trim() || "操作失败。";
      return;
    }
    const results = await response.json() as { id: string; reason?: string }[];
    if (action === "role-save" && results.some(result => result.reason)) {
      status.textContent = results.map(result => result.reason).filter(Boolean).join("；");
      return;
    }
    if (dialog && (["join", "leave", "grant", "revoke"].includes(action) || action.startsWith("group-roles") || action === "role-save")) {
      const auditReference = auditRecordReference(response.headers.get("X-Operation-Audit-Ref"));
      const title = dialog.querySelector("#system-dialog-title");
      if (title) title.textContent = action === "role-save" ? "角色保存结果" : "授权操作结果";
      form.outerHTML = `<div class="system-editor-body"><div class="catalog-table-wrap"><table class="catalog-table">
        <thead><tr><th>账号或角色</th><th>结果</th><th>说明</th></tr></thead>
        <tbody>${results.map(({ id, reason }) => `<tr><td>${h(id)}</td><td><span class="system-badge" data-audit-result="${reason ? "失败" : "成功"}">
          ${reason ? "失败" : "成功"}</span></td><td>${h(reason || "已更新关联关系")}</td></tr>`).join("")}</tbody>
        </table></div></div><footer class="system-dialog-footer">
        <button class="button${auditReference ? " secondary" : ""}" type="button" data-system-close>完成并刷新</button>
        ${auditReference ? `<a class="button" href="${h(auditLocation({ record: auditReference }))}">查看本次审计</a>` : ""}</footer>`;
      const hash = window.location.hash;
      dialog.addEventListener("close", () => {
        if (!dialog.isConnected || window.location.hash !== hash) return;
        if (form.dataset.id && hash.startsWith("#/admin/users")) {
          const detail = `#/admin/users/${encodeURIComponent(form.dataset.id)}`;
          if (detail !== hash) {
            window.location.hash = detail;
            return;
          }
        }
        reload();
      }, { once: true });
      dialog.querySelector<HTMLButtonElement>("footer [data-system-close]")?.focus();
    } else if (action === "delete" && window.location.hash !== "#/admin/users") {
      dialog?.close();
      window.location.hash = "#/admin/users";
    } else { dialog?.close(); reload(); }
  } catch {
    status.textContent = "请求失败，请重试。";
  } finally {
    const password = form.querySelector<HTMLInputElement>('[name="password"]');
    if (password) password.value = "";
    dialog?.removeAttribute("aria-busy");
    for (const button of buttons) button.disabled = false;
  }
}

function openPlatformEditor(panel: HTMLElement, key: string): void {
  const dialog = panel.querySelector<HTMLDialogElement>("[data-system-dialog]");
  if (!dialog || dialog.open) return;
  const template = [...panel.querySelectorAll<HTMLTemplateElement>("[data-system-editor]")]
    .find((item) => item.dataset.systemEditor === key);
  if (template) {
    dialog.classList.toggle("system-role-dialog", key.startsWith("role:"));
    dialog.replaceChildren(template.content.cloneNode(true));
    if (key === "batch") {
      const form = dialog.querySelector("form");
      const selected = [...panel.querySelectorAll<HTMLInputElement>('[data-platform-row]:not([hidden]) input[name="ids"]:checked:not(:disabled)')];
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
    if (key === "delete-account") {
      const row = selectedAccountForDeletion(panel);
      const form = dialog.querySelector("form");
      const label = form?.querySelector("[data-delete-name]");
      const impact = form?.querySelector("[data-delete-impact]");
      const box = row?.querySelector<HTMLInputElement>('input[name="ids"]');
      if (!row || !form || !label || !impact || !box) return;
      form.dataset.id = box.value;
      label.textContent = `「${row.dataset.accountName}」`;
      impact.textContent = row.dataset.kind === "ldap" ?
        "删除后会立即结束已有会话并收回平台权限。该员工将无法通过原 LDAP 身份再次登录本系统，公司目录账号不受影响。" :
        "删除后会立即结束已有会话并移除该账号及其权限关联。此操作不可撤销。";
      form.querySelector('button[type="submit"]')?.classList.add("catalog-danger-fill");
    }
    const assignment = dialog.querySelector<HTMLFormElement>("form:has([data-assignment-kind])");
    if (assignment) updateAssignmentOptions(assignment);
    dialog.oncancel = (event) => { if (dialog.hasAttribute("aria-busy")) event.preventDefault(); };
    const host = dialog.querySelector<HTMLElement>("[data-role-catalog-host]");
    const source = panel.querySelector<HTMLElement>("[data-pc-source]")?.dataset.catalog;
    if (host && source) {
      const catalog = JSON.parse(source) as PermissionCatalog;
      host.innerHTML = renderPermissionCatalog(true);
      const roleForm = host.closest("form");
      bindPermissionCatalog(host, catalog, roleForm ? savedGrants(roleForm) : []);
    }
    dialog.showModal();
    if (key === "delete-account") dialog.querySelector<HTMLButtonElement>("footer [data-system-close]")?.focus();
    for (const scope of dialog.querySelectorAll<HTMLElement>("[data-scope]")) void refreshScope(scope);
  }
}

export function handlePlatformClick(event: MouseEvent): void {
  if (!(event.target instanceof HTMLElement)) return;
  const target = event.target;
  const panel = target.closest<HTMLElement>(".identity-panel");
  const dialog = panel?.querySelector<HTMLDialogElement>("[data-system-dialog]");
  const opener = target.closest<HTMLButtonElement>("[data-system-open]");
  if (opener && panel && dialog && !dialog.open) {
    openPlatformEditor(panel, opener.dataset.systemOpen ?? "");
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
  const scopeButton = target.closest<HTMLElement>("[data-scope-add]");
  const form = target.closest("form");
  if (form && !dialog?.hasAttribute("aria-busy")) {
    if (scopeButton && addScopes(form) && scopeButton.dataset.scopeAdd === "preview") previewRole(form);
    if (target.closest("[data-role-back]")) showRolePreview(form, false);
    if (target.closest("[data-role-add-grants]")) addInterfaceGrants(form);
    const retry = target.closest<HTMLElement>("[data-scope-retry]")?.closest<HTMLElement>("[data-scope]");
    if (retry) void refreshScope(retry);
  }
  if (form && target.closest("[data-role-grant-remove]") && !dialog?.hasAttribute("aria-busy")) {
    target.closest("[data-role-grant]")?.remove();
    updateCatalogGrants(form, savedGrants(form));
    form.querySelector<HTMLButtonElement>("[data-role-grant-remove], [data-pc-module]")?.focus();
  }
  if (target.closest("[data-scope-remove]") && !dialog?.hasAttribute("aria-busy")) {
    target.closest("[data-saved-scope]")?.remove();
  }
}

function selectedAccountForDeletion(panel: Element): HTMLElement | undefined {
  const selected = [...panel.querySelectorAll<HTMLInputElement>('[data-platform-row]:not([hidden]) input[name="ids"]:checked:not(:disabled)')];
  const row = selected[0]?.closest<HTMLElement>('[data-account-deletable="true"]');
  if (selected.length === 1 && row) return row;
}

const accountDetails = new WeakMap<Element, Map<string, HTMLElement>>();

function showAccountDetail(panel: Element, id: string): void {
  const slot = panel.querySelector("[data-account-detail-slot]");
  if (!slot) return;
  const active = slot.querySelector<HTMLElement>("[data-user-detail]");
  if (active?.dataset.userDetail === id) return;
  const saved = accountDetails.get(panel) ?? new Map<string, HTMLElement>();
  accountDetails.set(panel, saved);
  if (active?.dataset.userDetail) saved.set(active.dataset.userDetail, active);
  const template = [...panel.querySelectorAll<HTMLTemplateElement>("[data-account-detail]")]
    .find((candidate) => candidate.dataset.accountDetail === id);
  const node = saved.get(id) ?? template?.content.cloneNode(true);
  if (node) slot.replaceChildren(node);
  else slot.innerHTML = '<section class="system-card system-detail"><p>请选择一个账号。</p></section>';
  for (const row of panel.querySelectorAll<HTMLElement>("[data-platform-row]")) {
    row.classList.toggle("system-current-row", row.dataset.id === id);
  }
}

export function filterAccounts(event: Event): void {
  const target = event.target;
  if (target instanceof HTMLInputElement && target.hasAttribute("data-assignment-search")) {
    if (target.form) updateAssignmentOptions(target.form);
    return;
  }
  if (!(target instanceof HTMLInputElement || target instanceof HTMLSelectElement) ||
      !target.matches('[data-platform-filter], [data-platform-select-all], input[name="ids"]')) return;
  const panel = target.closest(".identity-panel");
  if (!panel) return;
  const field = (key: string) => panel.querySelector<HTMLInputElement | HTMLSelectElement>(`[data-platform-filter="${key}"]`)?.value ?? "";
  const search = field("query").trim().toLowerCase();
  const kind = field("kind");
  const status = field("status");
  const visible: HTMLInputElement[] = [];
  const matching: HTMLElement[] = [];
  for (const row of panel.querySelectorAll<HTMLElement>("[data-platform-row]")) {
    row.hidden = Boolean((kind && row.dataset.kind !== kind) || (status && row.dataset.status !== status) ||
      !row.textContent?.toLowerCase().includes(search));
    if (!row.hidden) matching.push(row);
    const box = row.querySelector<HTMLInputElement>('input[name="ids"]');
    if (box?.checked && row.hidden) box.checked = false;
    if (box && !box.disabled && !row.hidden) visible.push(box);
  }
  if (target.hasAttribute("data-platform-filter")) {
    if (matching.length === 1) showAccountDetail(panel, matching[0]?.dataset.id ?? "");
    else if (!matching.some((row) => row.classList.contains("system-current-row"))) showAccountDetail(panel, "");
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
  const remove = panel.querySelector<HTMLButtonElement>("[data-platform-delete]");
  if (remove) remove.disabled = !selectedAccountForDeletion(panel);
  const count = panel.querySelector("[data-platform-selected]");
  if (count) count.textContent = `已选 ${selected} 项`;
  const empty = panel.querySelector<HTMLElement>("[data-platform-empty]");
  if (empty) empty.hidden = matching.length > 0;
  const total = panel.querySelector("[data-platform-count]");
  if (total) total.textContent = `共 ${matching.length} 个账号`;
}

function updateAssignmentOptions(form: HTMLFormElement): void {
  const kind = form.querySelector<HTMLSelectElement>("[data-assignment-kind]")?.value;
  const remove = form.querySelector<HTMLSelectElement>("[data-assignment-mode]")?.value === "remove";
  const search = form.querySelector<HTMLInputElement>("[data-assignment-search]")?.value.trim().toLowerCase() ?? "";
  let matches = 0;
  for (const pane of form.querySelectorAll<HTMLElement>("[data-assignment-options]")) {
    pane.hidden = pane.dataset.assignmentOptions !== kind;
    for (const option of pane.querySelectorAll<HTMLElement>("[data-assignment-choice]")) {
      const box = option.querySelector("input");
      if (!box) continue;
      const associated = option.dataset.associated === "true";
      option.hidden = (remove && !!form.dataset.id && !associated) || !option.textContent?.toLowerCase().includes(search);
      box.disabled = pane.hidden || (remove ? !!form.dataset.id && !associated : associated);
      if (box.disabled) box.checked = false;
      if (!pane.hidden && !option.hidden) matches++;
    }
  }
  const empty = form.querySelector<HTMLElement>("[data-assignment-empty]");
  if (empty) empty.hidden = matches > 0;
}

function originalRole(form: HTMLFormElement): Role | null {
  return JSON.parse(form.dataset.roleOriginal ?? "null") as Role | null;
}

function savedScopes(form: HTMLFormElement): Scope[] {
  return [...form.querySelectorAll<HTMLElement>("[data-saved-scope]")]
    .map((row) => JSON.parse(row.dataset.savedScope ?? "{}") as Scope);
}

function grantKey(grant: InterfaceGrant): string {
  const { environment, cluster, business, namespace } = grant.scope;
  return JSON.stringify([grant.code, environment ?? "", cluster ?? "", business ?? "", namespace ?? ""]);
}

function grantRow(grant: InterfaceGrant, label = grant.code): string {
  return `<div class="system-scope-row" data-role-grant="${h(JSON.stringify(grant))}">
    <span><strong>${h(label)}</strong><br>${scopeText(grant.scope) || "平台全局或本人范围"}</span>
    <button type="button" class="button secondary" data-role-grant-remove>移除</button></div>`;
}

function savedGrants(form: HTMLFormElement): InterfaceGrant[] {
  return [...form.querySelectorAll<HTMLElement>("[data-role-grant]")].map(row => JSON.parse(row.dataset.roleGrant ?? "{}") as InterfaceGrant);
}

function addInterfaceGrants(form: HTMLFormElement): void {
  const status = platformStatus(form);
  const list = form.querySelector("[data-role-grants]");
  if (!status || !list) return;
  const points = selectedPermissions(form);
  if (!points.length) {
    status.textContent = "请先勾选要授予的接口动作。";
    return;
  }
  const scopeKinds = new Set(points.map(point => point.feature.scope));
  if (scopeKinds.size !== 1) {
    status.textContent = "请按适用范围分次添加所选动作。";
    return;
  }
  const kind = points[0]?.feature.scope;
  if (!kind) return;
  const data = new FormData(form);
  const environment = String(data.get("environment") ?? "");
  const cluster = String(data.get("cluster") ?? "");
  const business = String(data.get("business") ?? "");
  const namespaces = data.getAll("namespace").map(String);
  let scopes: Scope[];
  if (kind === "平台全局" || kind === "仅本人") {
    scopes = [{ environment: "" }];
  } else if (kind === "集群实例") {
    if (!cluster) {
      status.textContent = "请选择一个集群实例。";
      return;
    }
    scopes = [{ environment, cluster }];
  } else {
    const field = form.querySelector<HTMLElement>("[data-scope]");
    if (field?.hasAttribute("aria-busy") || field?.dataset.scopeUnavailable === "true") {
      status.textContent = "请先完成命名空间加载。";
      return;
    }
    if ((restrictedScope(environment) && (!cluster || !business || !namespaces.length)) ||
        (cluster && !namespaces.length && !data.has("entireCluster"))) {
      status.textContent = "请明确选择授权范围。预发布与生产需要集群、业务线和命名空间。";
      return;
    }
    scopes = (namespaces.length ? namespaces : [""]).map(namespace => ({ environment, cluster, business, namespace }));
  }
  const previous = new Set(savedGrants(form).map(grantKey));
  const additions = points.flatMap(point => scopes.flatMap(scope => {
    const grant = { code: point.code, scope };
    return previous.has(grantKey(grant)) ? [] : [grantRow(grant, point.label)];
  }));
  list.insertAdjacentHTML("beforeend", additions.join(""));
  updateCatalogGrants(form, savedGrants(form), true);
  for (const box of form.querySelectorAll<HTMLInputElement>('[data-scope] input[type="checkbox"]')) box.checked = false;
  status.textContent = additions.length ? `已添加 ${additions.length} 项接口授权。` : "所选接口授权已在清单中。";
}

function addScopes(form: HTMLFormElement): boolean {
  const list = form.querySelector("[data-scope-list]");
  const status = platformStatus(form);
  const field = form.querySelector<HTMLElement>("[data-scope]");
  if (!list || !status || !field) return false;
  if (field.hasAttribute("aria-busy") || field.dataset.scopeUnavailable === "true") {
    status.textContent = field.hasAttribute("aria-busy") ? "正在加载命名空间，请稍候。" : "请重试加载命名空间。";
    return false;
  }
  const data = new FormData(form);
  const environment = String(data.get("environment") ?? "");
  const cluster = String(data.get("cluster") ?? "");
  const business = String(data.get("business") ?? "");
  const namespaces = data.getAll("namespace").map(String);
  if (restrictedScope(environment) && (!cluster || !business || !namespaces.length)) {
    status.textContent = "请选择集群、业务线和至少一个命名空间。";
    return false;
  }
  if (cluster && !namespaces.length && !data.has("entireCluster")) {
    status.textContent = "请勾选命名空间或明确选择整个集群。";
    return false;
  }
  const candidates = (namespaces.length ? namespaces : [""]).map((namespace) => ({ environment, cluster, business, namespace }));
  const { added } = scopeDifference(savedScopes(form), candidates);
  list.insertAdjacentHTML("beforeend", added.map(scopeRow).join(""));
  for (const box of field.querySelectorAll<HTMLInputElement>('input[type="checkbox"]')) box.checked = false;
  status.textContent = added.length ? `已添加 ${added.length} 项权限。` : "所选范围已在清单中。";
  return true;
}

function showRolePreview(form: HTMLFormElement, showing: boolean): void {
  const draft = form.querySelector<HTMLElement>("[data-role-draft]");
  const preview = form.querySelector<HTMLElement>("[data-role-preview]");
  const previewButton = form.querySelector<HTMLButtonElement>('[value="role-preview"]');
  const confirm = form.querySelector<HTMLButtonElement>('[value="role-save"]');
  const back = form.querySelector<HTMLButtonElement>("[data-role-back]");
  if (!draft || !preview || !previewButton || !confirm || !back) return;
  draft.hidden = showing;
  preview.hidden = !showing;
  previewButton.hidden = previewButton.disabled = showing;
  confirm.hidden = confirm.disabled = !showing;
  back.hidden = !showing;
  platformStatus(form)?.replaceChildren();
  if (showing) preview.querySelector("h3")?.focus();
  else previewButton.focus();
}

function previewRole(form: HTMLFormElement): void {
  const diff = form.querySelector("[data-role-diff]");
  const status = platformStatus(form);
  if (!diff || !status || !form.reportValidity()) return;
  const scopes = savedScopes(form);
  const grants = savedGrants(form);
  if (selectedPermissions(form).length) {
    status.textContent = "请先将已勾选的接口动作加入授权清单。";
    return;
  }
  const name = String(new FormData(form).get("name") ?? "").trim();
  if (!name || !(scopes.length || grants.length) || form.querySelector('[name="namespace"]:checked, [name="entireCluster"]:checked')) {
    const message = !name ? "请输入角色名称。" : "请先把已选范围加入清单。";
    status.textContent = !scopes.length && !grants.length && name ? "请添加至少一项授权范围。" : message;
    return;
  }
  const original = originalRole(form);
  const { added, removed } = scopeDifference(original?.scopes ?? [], scopes);
  const section = (title: string, entries: Scope[]) => `<h4>${title} · ${entries.length} 项</h4>${
    entries.length ? `<ul>${entries.map((item) => `<li>历史诊断查看 · ${scopeText(item)}</li>`).join("")}</ul>` : "<p>无</p>"}`;
  const previous = new Set((original?.grants ?? []).map(grantKey));
  const next = new Set(grants.map(grantKey));
  const grantSection = (title: string, items: InterfaceGrant[]) => items.length ? `<h4>${title} · ${items.length} 项</h4><ul>${items.map(item => {
    const point = describePermission(form, item.code);
    return `<li>${h(point?.label ?? item.code)} · ${scopeText(item.scope) || h(point?.feature.scope ?? "该动作适用范围")}
      ${point?.risk === "高风险" ? ' <span class="system-badge">高风险</span>' : ""}</li>`;
  }).join("")}</ul>` : "";
  diff.innerHTML = `<p>角色名称 ${h(name)}${original && original.name !== name ? `（原名称 ${h(original.name)}）` : ""}</p>${
    section("新增权限", added)}${section("移除权限", removed)}
    ${grantSection("新增接口授权", grants.filter(grant => !previous.has(grantKey(grant))))}
    ${grantSection("移除接口授权", (original?.grants ?? []).filter(grant => !next.has(grantKey(grant))))}`;
  showRolePreview(form, true);
}

const scopeRequests = new WeakMap<HTMLElement, AbortController>();
const restrictedScope = (environment: string): boolean => environment === "production" || environment === "staging";

export async function updateScopeOptions(event: Event): Promise<void> {
  const target = event.target;
  if (target instanceof HTMLInputElement && target.type === "checkbox" &&
      (target.name === "namespace" || target.name === "entireCluster") && target.checked) {
    const alternative = target.name === "namespace" ? "entireCluster" : "namespace";
    for (const box of target.closest("[data-scope]")?.querySelectorAll<HTMLInputElement>(`input[name="${alternative}"]`) ?? []) box.checked = false;
  }
  if (!(target instanceof HTMLSelectElement)) return;
  if (target.matches("[data-assignment-kind], [data-assignment-mode]") && target.form) {
    updateAssignmentOptions(target.form);
  }
  const field = target.closest<HTMLElement>("[data-scope]");
  if (!field || !["cluster", "business", "environment"].includes(target.name)) return;
  if (target.name === "environment") {
    const cluster = field.querySelector<HTMLSelectElement>('[name="cluster"]');
    if (cluster) cluster.value = "";
  }
  if (target.name !== "business") {
    const business = field.querySelector<HTMLSelectElement>('[name="business"]');
    if (business) business.value = "";
  }
  await refreshScope(field);
}

async function refreshScope(field: HTMLElement): Promise<void> {
  const cluster = field.querySelector<HTMLSelectElement>('[name="cluster"]');
  const business = field.querySelector<HTMLSelectElement>('[name="business"]');
  const namespace = field.querySelector<HTMLElement>("[data-scope-namespace]");
  const env = field.querySelector<HTMLSelectElement>('[name="environment"]');
  const whole = field.querySelector<HTMLInputElement>('[name="entireCluster"]');
  const retry = field.querySelector<HTMLButtonElement>("[data-scope-retry]");
  if (!cluster || !business || !namespace || !env || !whole || !retry) return;
  scopeRequests.get(field)?.abort();
  const controller = new AbortController();
  scopeRequests.set(field, controller);
  const restricted = restrictedScope(env.value);
  if (cluster.options[0]) cluster.options[0].text = restricted ? "请选择集群" : "整个环境";
  for (const option of cluster.options) {
    option.hidden = Boolean(option.dataset.scopeEnv && option.dataset.scopeEnv !== env.value);
    option.disabled = option.hidden;
  }
  const businessField = business.closest<HTMLElement>("[data-scope-business]");
  if (businessField) businessField.hidden = !restricted;
  business.disabled = !restricted || !cluster.value;
  if (!restricted) business.value = "";
  field.dataset.scopeUnavailable = "false";
  retry.hidden = true;
  whole.disabled = true;
  whole.checked = false;
  whole.closest("label")?.setAttribute("hidden", "");
  if (!cluster.value || (restricted && !business.value)) {
    namespace.textContent = restricted ? "请先选择集群和业务线。" : "当前范围为整个环境。";
    field.removeAttribute("aria-busy");
    return;
  }
  namespace.textContent = "正在加载命名空间…";
  field.setAttribute("aria-busy", "true");
  try {
    const response = await fetch(`/api/v1/admin/namespace-business?cluster=${encodeURIComponent(cluster.value)}`, { signal: controller.signal });
    if (!response.ok) throw new Error("namespaces unavailable");
    const data = await response.json() as { namespaces: { name: string }[]; assigned: Record<string, string[]> };
    if (scopeRequests.get(field) !== controller) return;
    const names = [...new Set((data.namespaces ?? []).filter(({ name }) =>
      !restricted || data.assigned?.[name]?.includes(business.value)).map(({ name }) => name))];
    namespace.innerHTML = names.length ? names.map((name) =>
      `<label><input type="checkbox" name="namespace" value="${h(name)}"><span>${h(name)}</span></label>`).join("") :
      "<p>暂无可选命名空间。</p>";
    if (!restricted) {
      whole.disabled = false;
      whole.closest("label")?.removeAttribute("hidden");
    }
  } catch {
    if (controller.signal.aborted) return;
    namespace.textContent = "命名空间暂不可用。";
    field.dataset.scopeUnavailable = "true";
    retry.hidden = false;
  } finally {
    if (scopeRequests.get(field) === controller) field.removeAttribute("aria-busy");
  }
}
