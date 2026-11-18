import { escapeHtml } from "../../components/html.ts";
import { renderPlatform } from "./platform-admin.ts";
import { renderRoles } from "./roles.ts";

const ldapFields = [
  ["url", "LDAP URL"],
  ["bindDN", "LDAP 搜索 DN"],
  ["baseDN", "LDAP 基础 DN"],
  ["userUID", "LDAP 用户 UID"],
  ["userFilter", "LDAP 附加用户过滤器"],
  ["groupAttribute", "LDAP 组成员属性"],
  ["groupBaseDN", "LDAP 组基础 DN"],
  ["groupFilter", "LDAP 组过滤器"],
] as const;

export async function renderIdentityAdmin(resource: string, initial: boolean, roleID?: string, administrator = true, superAdmin = false): Promise<string> {
  const navigation = initial || !administrator ? "" : `<nav class="identity-nav" aria-label="系统管理">
    <a href="#/admin/ldap"${resource === "ldap" ? ' aria-current="page"' : ""}>认证配置</a>
    <a href="#/admin/users"${resource === "users" ? ' aria-current="page"' : ""}>用户管理</a>
    <a href="#/admin/access"${resource === "access" || resource === "policy" ? ' aria-current="page"' : ""}>权限配置</a>
    <a href="#/admin/operations"${resource === "operations" ? ' aria-current="page"' : ""}>操作审计</a>
    <a href="#/admin/password"${resource === "password" ? ' aria-current="page"' : ""}>修改密码</a>
  </nav>`;
  let content: string;
  try {
    content = ["users", "access", "operations"].includes(resource) ? await renderPlatform(resource, roleID, superAdmin)
      : resource === "policy" ? await renderRoles(roleID) : await renderResource(resource, initial);
  } catch {
    content = "<h2>无法加载配置</h2><p>请刷新后重试。</p>";
  }
  return `<section class="panel identity-panel">${navigation}${content}</section>`;
}

async function renderResource(resource: string, initial: boolean): Promise<string> {
  if (resource === "password") {
    return `<h2>${initial ? "请先修改临时密码" : "修改密码"}</h2>
      ${form("password", `<label>当前密码<input name="current" type="password" autocomplete="current-password" required></label>
        ${passwordField()}`, "修改密码")}`;
  }
  const response = await fetch("/api/v1/admin/ldap");
  if (!response.ok || !response.headers.get("Content-Type")?.includes("application/json")) {
    throw new Error("identity configuration unavailable");
  }
  const data = await response.json();
  return `<h2>认证配置</h2><p>本地账号始终可用。支持 ldaps://（TLS）和 ldap://（StartTLS），均验证证书。AD 用户 UID 通常填写 sAMAccountName，附加过滤器可填 (objectClass=user)。已有 %s 模板时请留空 UID。搜索密码在此配置。私有 CA 和密码加密密钥通过部署 Secret 挂载。</p>
    ${data.issue ? `<p data-ldap-issue role="alert">LDAP 暂不可用：${escapeHtml(data.issue)}</p>` : ""}
    ${form("ldap", ldapFields.map(([key, label]) =>
      `<label>${label}<input name="${key}" value="${escapeHtml(data[key] ?? "")}"></label>`).join("") +
      `<label>LDAP 用户搜索范围<select name="searchScope">
        <option value="subtree">子树</option>
        <option value="onelevel"${data.searchScope === "onelevel" ? " selected" : ""}>单层</option>
        <option value="base"${data.searchScope === "base" ? " selected" : ""}>基础对象</option>
      </select></label>
      <label>LDAP 组搜索范围<select name="groupScope">
        <option value="">默认（子树）</option>
        <option value="subtree"${data.groupScope === "subtree" ? " selected" : ""}>子树</option>
        <option value="onelevel"${data.groupScope === "onelevel" ? " selected" : ""}>单层</option>
        <option value="base"${data.groupScope === "base" ? " selected" : ""}>基础对象</option>
      </select></label>
      <label>LDAP 搜索密码<input name="bindPassword" type="password" autocomplete="new-password" placeholder="${data.passwordConfigured === true ? "已配置，留空保留" : "输入搜索账号密码"}"></label>
      <label>LDAP 测试员工账号<input name="testUsername" maxlength="250" autocomplete="off" placeholder="例如 alice"></label>
      <label>LDAP 测试员工密码<input name="testPassword" type="password" maxlength="1024" autocomplete="off"></label>
      <p>先验证连接、搜索账号绑定、员工查询和员工密码，再保存并启用。</p>`, "保存配置")}`;
}

function form(resource: string, fields: string, label: string): string {
  return `<form class="login-form identity-form" data-identity-resource="${resource}">
    ${fields}${resource === "ldap" ? `<fieldset><legend>验证步骤</legend><ol>
      ${ldapSteps.map((step) => `<li>${step} · <span data-ldap-step>待验证</span></li>`).join("")}</ol></fieldset>` : ""}
    <p role="status"></p><button class="button" type="submit"${resource === "ldap" ? " disabled" : ""}>${label}</button>
    ${resource === "ldap" ? '<button class="button secondary" type="submit" value="test">测试 LDAP 服务器</button>' : ""}
  </form>`;
}

function passwordField(): string {
  return '<label>新密码<input name="password" type="password" autocomplete="new-password" required minlength="12" maxlength="72"></label>';
}

const ldapSteps = ["目录连接", "搜索账号绑定", "测试员工查询", "测试员工认证"] as const;

function showLDAPSteps(form: HTMLFormElement, stage = ""): void {
  const failureIndex: Record<string, number> = { connection: 0, bind: 1, search: 2, user_search: 2,
    user_match: 2, user_auth: 3, group_search: 3, group_query: 3, identity_mapping: 3 };
  const failed = failureIndex[stage];
  const items = form.querySelectorAll<HTMLElement>("[data-ldap-step]");
  items.forEach((item, index) => {
    item.textContent = stage === "success" || (failed !== undefined && index < failed) ? "通过" :
      index === failed ? "失败" : "待验证";
  });
}

const ldapVerification = new WeakMap<HTMLFormElement, string>();
const pendingLDAP = new WeakSet<HTMLFormElement>();

function ldapDraft(form: HTMLFormElement): FormData {
  const data = new FormData(form);
  data.delete("testUsername");
  data.delete("testPassword");
  return data;
}

export function invalidateLDAPDraft(form: HTMLFormElement, target: EventTarget | null): void {
  if (!(target instanceof HTMLInputElement || target instanceof HTMLSelectElement) ||
      target.name === "testUsername" || target.name === "testPassword") return;
  ldapVerification.delete(form);
  showLDAPSteps(form);
  const save = form.querySelector<HTMLButtonElement>('button[type="submit"]:not([value="test"])');
  if (save) save.disabled = true;
  const status = form.querySelector('[role="status"]');
  if (status) status.textContent = "配置已修改，请重新测试。";
}

export async function submitIdentityAdmin(form: HTMLFormElement, signedOut: () => void, testingLDAP = false): Promise<void> {
  const status = form.querySelector<HTMLElement>('[role="status"]');
  const button = form.querySelector<HTMLButtonElement>('button[type="submit"]');
  const ldap = form.dataset.identityResource === "ldap";
  if (!status || !button || (ldap ? pendingLDAP.has(form) || (!testingLDAP && !ldapVerification.has(form)) : button.disabled)) return;
  if (ldap) {
    pendingLDAP.add(form);
    if (testingLDAP) {
      ldapVerification.delete(form);
      showLDAPSteps(form);
    }
  }
  const buttons = form.querySelectorAll<HTMLButtonElement>('button[type="submit"]');
  for (const control of buttons) control.disabled = true;
  status.textContent = testingLDAP ? "正在测试 LDAP 服务器…" : "正在保存…";
  const resource = form.dataset.identityResource;
  const data = new FormData(form);
  const submittedDraft = ldap ? JSON.stringify(Object.fromEntries(ldapDraft(form))) : "";
  try {
    let payload: unknown = Object.fromEntries(data);
    if (ldap && !testingLDAP) {
      payload = { ...Object.fromEntries(ldapDraft(form)), verification: ldapVerification.get(form) };
    }
    const response = await fetch(`/api/v1/admin/${testingLDAP ? "ldap-test" : resource}`, {
      method: !testingLDAP && resource === "ldap" ? "PUT" : "POST",
      headers: { "Content-Type": "application/json" },
      body: JSON.stringify(payload),
    });
    if (resource === "ldap" && (response.status === 404 || response.status === 405 ||
        (response.ok && response.status !== 204 && !response.headers.get("Content-Type")?.includes("application/json")))) {
      status.textContent = "LDAP 管理接口不可用，请检查 API 镜像版本和 /api/v1 路由。";
      return;
    }
    if (!response.ok) {
      if (ldap && response.status === 409) ldapVerification.delete(form);
      if (ldap && [400, 409, 503].includes(response.status) && response.headers.get("Content-Type")?.startsWith("text/plain")) {
        const message = (await response.text()).trim();
        status.textContent = message === "password_required" ? "更换 LDAP 地址或搜索 DN 后，请重新填写搜索密码。" : message || "LDAP 配置无效。";
        return;
      }
      status.textContent = response.status === 409 ? "账号已存在或配置已更新。" :
        response.status === 400 ? "输入无效，请核对配置或密码。" : "操作失败，请重试。";
      return;
    }
    if (testingLDAP) {
      const { stage, verification } = await response.json();
      if (stage === "success" && typeof verification === "string" && verification.length > 0 &&
          submittedDraft === JSON.stringify(Object.fromEntries(ldapDraft(form)))) {
        ldapVerification.set(form, verification);
      }
      showLDAPSteps(form, submittedDraft === JSON.stringify(Object.fromEntries(ldapDraft(form))) ? stage : "");
      const messages: Record<string, string> = {
        success: ldapVerification.has(form)
          ? "连接、搜索账号绑定、员工查询和员工认证均成功，可以保存。"
          : "验证未能用于当前配置，请重新测试。",
        user_auth: "测试员工认证失败，请检查员工密码和目录账号状态。",
        connection: "连接失败，请检查域名、端口和 TLS 证书。",
        bind: "搜索账号绑定失败，请检查搜索 DN 和密码。",
        search: "搜索失败，请检查基础 DN 和账号读取权限。",
        group_search: "组基础 DN 搜索失败，请检查组基础 DN 和搜索账号的读取权限。",
        user_search: "用户查询失败，请检查 UID、用户过滤器、搜索范围和读取权限。",
        user_match: "未找到唯一匹配的用户，请检查用户名、UID、用户过滤器和搜索范围。",
        group_query: "组成员查询失败，请检查组成员属性、组过滤器和读取权限。",
        identity_mapping: "用户或组数据无法用于登录，请检查 DN 和组成员数量。",
      };
      status.textContent = messages[stage] ?? "测试失败，请检查 LDAP 配置。";
      return;
    }
    if (resource === "ldap" && response.status !== 204) {
      status.textContent = "API 未确认保存，请检查 API 镜像版本和 /api/v1 路由。";
      return;
    }
    status.textContent = "已保存";
    if (ldap) {
      ldapVerification.delete(form);
      form.closest("section")?.querySelector("[data-ldap-issue]")?.remove();
      const password = form.querySelector<HTMLInputElement>('[name="bindPassword"]');
      if (password) {
        password.value = "";
        password.placeholder = data.get("url") ? "已配置，留空保留" : "输入搜索账号密码";
      }
    }
    if (resource === "password") signedOut();
  } catch {
    status.textContent = "无法保存，请检查输入或连接。";
  } finally {
    if (resource !== "ldap") {
      for (const field of form.querySelectorAll<HTMLInputElement>('input[type="password"]')) field.value = "";
    }
    if (ldap) {
      pendingLDAP.delete(form);
      const testPassword = form.querySelector<HTMLInputElement>('[name="testPassword"]');
      if (testPassword) testPassword.value = "";
    }
    for (const control of buttons) control.disabled = ldap && control === button && !ldapVerification.has(form);
  }
}
