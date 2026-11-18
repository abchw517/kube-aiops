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
type Entry = { id: string; name: string };
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
  if (user.kind === "super" || user.kind === "administrator") return "<p>全局 · 系统管理员</p>";
  if (user.disabled || user.directoryDisabled || user.mustChangePassword) return "<p>账号当前无法访问诊断数据。</p>";
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

async function renderAccounts(id: string | undefined, superAdmin: boolean): Promise<string> {
  const [users, access] = await Promise.all([admin<Account[]>("accounts"), admin<Access>("access")]);
  const user = id ? users.find((item) => item.id === decodeURIComponent(id)) : undefined;
  const eligibleRoles = access.roles.filter((role) => role.id !== "system-admin");
  const selection = `<label>用户组<select name="groups" multiple>${options(access.groups)}</select></label>
    <label>直接角色<select name="roles" multiple>${options(eligibleRoles)}</select></label>
    <div class="catalog-actions">${submit("加入用户组", "join")}${submit("移出用户组", "leave", true)}
    ${submit("授予角色", "grant")}${submit("撤销角色", "revoke", true)}</div>`;
  if (id) {
    if (!user) return '<h2>用户管理</h2><p>账号不存在。</p><a href="#/admin/users">返回用户列表</a>';
    const allowed = user.kind !== "super" && (user.kind !== "administrator" || superAdmin);
    return `<a href="#/admin/users">返回用户列表</a><h2>${h(user.username)}</h2>
      <p>${h(kinds[user.kind])} · ${h(statusOf(user))}</p>
      <dl><dt>用户组</dt><dd>${names(user.groups, access.groups)}</dd>
      <dt>直接角色</dt><dd>${names(user.roles, access.roles)}</dd></dl>
      <h3>生效权限及来源</h3>${authTable(user)}
      ${user.kind === "ldap" ? `<form class="identity-form" data-platform="accounts" data-id="${h(user.id)}"
        data-stay="true"><h3>分配用户组和角色</h3>${selection}<p role="status"></p></form>` : ""}
      ${allowed ? `<div class="platform-operations">
        <form class="identity-form" data-platform="accounts" data-id="${h(user.id)}" data-stay="true">
          ${user.kind === "administrator" ? submit("删除系统管理员", "delete", true) :
            submit(user.disabled ? "重新启用" : "停用账号", user.disabled ? "enable" : "disable", true)}
          <p role="status"></p></form>
        ${user.kind !== "ldap" ? `<form class="identity-form" data-platform="accounts" data-id="${h(user.id)}"
          data-stay="true"><h3>重置密码</h3>${tempPassword()}${submit("重置为临时密码", "reset")}<p role="status"></p></form>` : ""}
      </div>` : ""}`;
  }
  return `<div class="platform-heading"><h2>用户管理</h2>
    <div class="platform-filters">
    <label>搜索账号<input type="search" data-platform-filter="query" placeholder="输入账号名或身份"></label>
    <label>账号类型<select data-platform-filter="kind"><option value="">全部类型</option>${options(Object.entries(kinds).map(([id,name]) => ({ id,name })))}</select></label>
    <label>账号状态<select data-platform-filter="status"><option value="">全部状态</option><option>正常</option><option>待改密</option><option>平台已停用</option><option>目录已停用</option></select></label>
    </div></div>
    <form class="platform-form" data-platform="accounts">
      <div class="catalog-table-wrap"><table class="catalog-table">
        <thead><tr><th><input type="checkbox" data-platform-select-all aria-label="选择当前结果"></th><th>账号</th><th>身份</th><th>状态</th><th>用户组</th><th>直接角色</th><th>操作</th></tr></thead><tbody>
        ${users.map((item) => `<tr data-platform-row data-kind="${h(item.kind)}" data-status="${h(statusOf(item))}"><td><input type="checkbox" name="ids" value="${h(item.id)}" aria-label="选择 ${h(item.username)}"${item.kind === "super" ? " disabled" : ""}></td>
          <td>${h(item.username)}</td><td>${h(kinds[item.kind])}</td><td>${h(statusOf(item))}</td>
          <td>${names(item.groups, access.groups)}</td><td>${names(item.roles, access.roles)}</td>
          <td><a href="#/admin/users/${encodeURIComponent(item.id)}">查看详情</a></td></tr>`).join("")}
        <tr data-platform-empty${users.length ? " hidden" : ""}><td colspan="7">暂无符合条件的账号。</td></tr>
        </tbody></table></div>
      <p aria-live="polite" data-platform-selected>已选 0 项</p>
      <details><summary>批量授权 / 撤销</summary><div class="identity-form">${selection}</div></details><p role="status"></p>
    </form>
    <details><summary>＋ 创建本地账号</summary>
      <form class="identity-form" data-platform="accounts">
        <label>账号<input name="name" required maxlength="128" autocomplete="off"></label>
        <label>账号类型<select name="kind"><option value="guest">本地访客</option>
        ${superAdmin ? '<option value="administrator">系统管理员</option>' : ""}</select></label>
        ${tempPassword()}${submit("创建账号", "create")}<p role="status"></p>
      </form>
    </details>`;
}

function scopeInputs(scope: Scope | undefined, clusters: Entry[], businesses: Entry[]): string {
  const env = scope?.environment ?? "development";
  const restrictBusiness = env === "production" || env === "staging";
  const selectedCluster = clusters.some((cluster) => cluster.id === scope?.cluster);
  const selectedBusiness = businesses.some((business) => business.id === scope?.business);
  return `<fieldset data-scope class="platform-scope">
    <label>环境<select name="environment">${options(Object.entries(environments).map(([id, name]) => ({ id, name })), env)}</select></label>
    <label>集群<select name="cluster"><option value="">整个环境</option>${options(clusters, scope?.cluster)}
      ${!selectedCluster && scope?.cluster ? options([{ id: scope.cluster, name: scope.cluster }], scope.cluster) : ""}</select></label>
    <label>业务线<select name="business"${restrictBusiness ? "" : " disabled"}><option value="">请选择</option>${options(businesses, scope?.business)}
      ${!selectedBusiness && scope?.business ? options([{ id: scope.business, name: scope.business }], scope.business) : ""}</select></label>
    <label>命名空间<select name="namespace" data-scope-namespace><option value="">全部</option>
      ${scope?.namespace ? options([{ id: scope.namespace, name: scope.namespace }], scope.namespace) : ""}</select></label>
    <button type="button" class="button secondary" data-scope-remove>移除此项</button>
  </fieldset>`;
}

async function renderAccess(): Promise<string> {
  const [access, clusters, businesses] = await Promise.all([
    admin<Access>("access"), json<{ items: Entry[] }>("/api/v1/clusters"), admin<Entry[]>("business-lines"),
  ]);
  const available = access.roles.filter((role) => role.id !== "system-admin");
  const groupList = access.groups.map((group) => `<details><summary>${h(group.name)} · ${names(group.roles, access.roles)}</summary>
    <form class="identity-form" data-platform="access" data-id="${h(group.id)}">
      <label>用户组名称<input name="name" required value="${h(group.name)}"></label>
      <label>用途<input name="description" maxlength="256" value="${h(group.description ?? "")}"></label>
      <label>角色<select name="roles" multiple>${options(available)}</select></label>
      <div class="catalog-actions">${submit("添加角色", "group-roles-add")}${submit("移除角色", "group-roles-remove", true)}
      ${submit("保存用户组", "group-save", true)}${submit("删除用户组", "group-delete", true)}</div><p role="status"></p>
    </form></details>`).join("");
  const roleForm = (role?: Role) => `<form class="identity-form platform-role" data-platform="access" data-id="${h(role?.id ?? "")}">
    <label>角色名称<input name="name" required maxlength="128" value="${h(role?.name ?? "")}"></label>
    <h3>历史诊断查看</h3>
    <div data-scope-list>${(role?.scopes ?? [undefined]).map((scope) => scopeInputs(scope, clusters.items, businesses)).join("")}</div>
    <template>${scopeInputs(undefined, clusters.items, businesses)}</template>
    <button type="button" class="button secondary" data-scope-add>＋ 添加授权范围</button>
    ${submit("保存角色", "role-save")}${role ? submit("删除角色", "role-delete", true) : ""}<p role="status"></p>
  </form>`;
  return `<h2>权限配置</h2><div class="platform-columns">
    <section><h3>用户组</h3>${groupList || "<p>暂无用户组。</p>"}
      <details><summary>＋ 新建用户组</summary>
        <form class="identity-form" data-platform="access">
          <label>用户组名称<input name="name" required maxlength="128"></label>
          <label>用途<input name="description" maxlength="256"></label>
          ${submit("创建用户组", "group-save")}<p role="status"></p>
        </form></details>
    </section>
    <section><h3>角色</h3>
      ${access.roles.map((role) => `<details><summary>${h(role.name)}${role.fixed ? " · 预置" : ""}</summary>
      ${role.fixed ? (role.scopes?.map((scope) => `<p>历史诊断查看 · ${scopeText(scope)}</p>`).join("") || "<p>管理系统与全局诊断</p>") : roleForm(role)}
      </details>`).join("")}
      <details><summary>＋ 新建角色</summary>${roleForm()}</details>
    </section></div>`;
}

function operationRows(items: Operation[]): string {
  if (items.length === 0) return '<tr><td colspan="8">暂无匹配记录。</td></tr>';
  return items.map((item) => `<tr><td>${h(new Date(item.at * 1000).toLocaleString("zh-CN"))}</td><td>${h(item.actor || "—")}</td>
    <td>${h(item.action)}</td><td>${h(item.risk)}</td><td>${h(item.target)}</td><td>${h(item.result)}</td>
    <td>${h(item.reason || "—")}</td><td>${item.batch ? `<button class="button secondary" type="button" data-audit-batch="${h(item.batch)}">查看同批次</button>` : "—"}</td></tr>`).join("");
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
    <thead><tr><th>时间</th><th>操作者</th><th>操作类型</th><th>风险等级</th><th>目标</th><th>结果</th><th>失败原因</th><th>批次</th></tr></thead>
    <tbody data-audit-results>${operationRows(items)}</tbody></table></div>`;
}

export function renderPlatform(resource: string, id: string | undefined, superAdmin: boolean): Promise<string> {
  if (resource === "users") return renderAccounts(id, superAdmin);
  if (resource === "access") return renderAccess();
  return renderOperations();
}

export async function submitPlatform(form: HTMLFormElement, action: string, reload: () => void): Promise<void> {
  const status = form.querySelector<HTMLElement>('[role="status"]');
  const buttons = [...form.querySelectorAll<HTMLButtonElement>('button[type="submit"]')];
  if (!status || buttons.some((button) => button.disabled)) return;
  if (["delete", "role-delete", "group-delete", "disable", "reset"].includes(action) && !window.confirm("确认执行该操作？")) return;
  const data = new FormData(form);
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
  for (const button of buttons) button.disabled = true;
  status.textContent = "正在保存…";
  try {
    const response = await fetch(`/api/v1/admin/${form.dataset.platform}`, {
      method: "POST", headers: { "Content-Type": "application/json" },
      body: JSON.stringify({ action, id: form.dataset.id, ids, values, scopes,
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
      refresh.textContent = "刷新结果";
      refresh.addEventListener("click", reload, { once: true });
      status.append(" ", refresh);
    } else if (action === "delete") window.location.hash = "#/admin/users";
    else reload();
  } catch {
    status.textContent = "请求失败，请重试。";
  } finally {
    const password = form.querySelector<HTMLInputElement>('[name="password"]');
    if (password) password.value = "";
    for (const button of buttons) button.disabled = false;
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
  if (target.closest("[data-scope-add]")) {
    const form = target.closest("form");
    const template = form?.querySelector("template");
    if (template) form?.querySelector("[data-scope-list]")?.append(template.content.cloneNode(true));
  }
  if (target.closest("[data-scope-remove]")) target.closest("[data-scope]")?.remove();
  const batch = target.closest<HTMLButtonElement>("[data-audit-batch]")?.dataset.auditBatch;
  const filter = target.closest(".identity-panel")?.querySelector<HTMLFormElement>("[data-audit-filter]");
  if (batch && filter) void queryAudit(filter, "", batch);
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
    all.checked = visible.length > 0 && selected === visible.length;
    all.indeterminate = selected > 0 && selected < visible.length;
  }
  const count = panel.querySelector("[data-platform-selected]");
  if (count) count.textContent = `已选 ${selected} 项`;
  const empty = panel.querySelector<HTMLElement>("[data-platform-empty]");
  if (empty) empty.hidden = matching > 0;
}

export async function updateScopeOptions(event: Event): Promise<void> {
  const target = event.target;
  if (!(target instanceof HTMLSelectElement) || !["cluster", "business", "environment"].includes(target.name)) return;
  const field = target.closest("[data-scope]");
  const cluster = field?.querySelector<HTMLSelectElement>('[name="cluster"]');
  const business = field?.querySelector<HTMLSelectElement>('[name="business"]');
  const namespace = field?.querySelector<HTMLSelectElement>("[data-scope-namespace]");
  const env = field?.querySelector<HTMLSelectElement>('[name="environment"]');
  if (!cluster || !business || !namespace || !env) return;
  const selectedCluster = cluster.value;
  const selectedBusiness = business.value;
  const selectedEnv = env.value;
  const restricted = selectedEnv === "production" || selectedEnv === "staging";
  namespace.disabled = true;
  namespace.innerHTML = '<option value="">全部</option>';
  business.disabled = !restricted;
  if (!restricted) business.value = "";
  if (!selectedCluster) return;
  try {
    const data = await admin<{ namespaces: { name: string }[]; assigned: Record<string, string[]> }>(`namespace-business?cluster=${encodeURIComponent(selectedCluster)}`);
    if (cluster.value !== selectedCluster || env.value !== selectedEnv || (restricted && business.value !== selectedBusiness)) return;
    const names = data.namespaces.filter(({ name }) => !restricted || data.assigned[name]?.includes(selectedBusiness));
    namespace.innerHTML += options(names.map(({ name }) => ({ id: name, name })));
    namespace.disabled = false;
  } catch {
    namespace.innerHTML = '<option value="">命名空间暂不可用</option>';
  }
}
