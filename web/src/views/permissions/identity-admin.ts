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

type IdentityAdminView = {
  initial: boolean;
  roleID?: string;
  administrator: boolean;
  superAdmin: boolean;
  canChangePassword: boolean;
  auditContent?: string;
};

/** 根据当前会话能力展示认证和账号管理功能。 */
export async function renderIdentityAdmin(resource: string, view: IdentityAdminView): Promise<string> {
  const { initial, roleID, administrator, superAdmin, canChangePassword, auditContent } = view;
  const navigation = initial || !administrator ? "" : `<nav class="identity-nav" aria-label="系统管理">
    <a href="#/admin/ldap"${resource === "ldap" ? ' aria-current="page"' : ""}>认证配置</a>
    <a href="#/admin/users"${resource === "users" ? ' aria-current="page"' : ""}>用户管理</a>
    <a href="#/admin/access"${resource === "access" || resource === "policy" ? ' aria-current="page"' : ""}>权限配置</a>
    <a href="#/admin/operations"${resource === "operations" ? ' aria-current="page"' : ""}>操作审计</a>
    ${canChangePassword ? `<a href="#/admin/password"${resource === "password" ? ' aria-current="page"' : ""}>修改密码</a>` : ""}
  </nav>`;
  let content: string;
  try {
    content = auditContent ?? (["users", "access"].includes(resource) ? await renderPlatform(resource, roleID, superAdmin)
      : resource === "policy" ? await renderRoles(roleID) : await renderResource(resource, initial));
  } catch {
    content = "<h2>无法加载配置</h2><p>请刷新后重试。</p>";
  }
  return `<section class="panel identity-panel system-management">${navigation}${content}
    <dialog class="system-dialog" data-system-dialog aria-labelledby="system-dialog-title"></dialog></section>`;
}

async function renderResource(resource: string, initial: boolean): Promise<string> {
  if (resource === "password") {
    return `<div class="platform-heading"><div><h2>修改密码</h2>
      <p>${initial ? "请先设置新密码，再进入历史诊断。" : "为当前本地账号设置新密码。"}</p></div></div>
      ${form("password", `${initial ? '<p class="system-notice">当前会话仅可用于完成首次改密。</p>' : ""}<label>${initial ? "临时密码" : "当前密码"}<input name="current" type="password" autocomplete="current-password" required></label>
        ${passwordField()}<label>确认新密码<input name="confirmPassword" type="password" autocomplete="new-password" required></label>`, "保存新密码")}`;
  }
  const response = await fetch("/api/v1/admin/ldap");
  if (!response.ok || !response.headers.get("Content-Type")?.includes("application/json")) {
    throw new Error("identity configuration unavailable");
  }
  const data = await response.json();
  return `<div class="platform-heading"><div><h2>认证配置</h2><p>验证待启用的 LDAP 配置后保存。</p></div></div>
    ${data.issue ? `<p data-ldap-issue role="alert">LDAP 暂不可用：${escapeHtml(data.issue)}</p>` : ""}
    <form class="system-ldap" data-identity-resource="ldap">
    <section class="system-card system-form"><div class="system-card-heading"><h3>LDAP 连接配置</h3><span class="system-badge" data-ldap-config-state>${data.url ? "配置已启用" : "尚未配置"}</span></div><div class="system-field-grid">${ldapFields.map(([key, label]) =>
      `<label>${label}<input name="${key}" value="${escapeHtml(data[key] ?? "")}"></label>`).join("")}
      <label>LDAP 用户搜索范围<select name="searchScope">
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
      </div><label><input type="checkbox" name="allowPlainLDAP"${data.allowPlainLDAP === true ? " checked" : ""}> 允许明文 LDAP（仅用于 ldap://）</label>
      <button class="button" type="submit" aria-describedby="ldap-save-hint" disabled>保存并启用</button>
      <p id="ldap-save-hint" data-ldap-save-hint role="status">${ldapSaveNeedsTest}</p>
      <details><summary>LDAP 配置说明</summary><p>ldaps:// 使用 TLS 并校验证书。ldap:// 直接连接，搜索账号和员工凭据会以明文传输。AD 通常使用 sAMAccountName。已有 %s 模板时请留空 UID。LDAP 配置及搜索密码保存到 SQLite。使用私有 CA 时可通过部署 Secret 提供 CA 证书。</p></details></section>
    <section class="system-card system-form"><h3>测试待启用配置</h3>
      <label>LDAP 测试员工账号<input name="testUsername" maxlength="250" autocomplete="off" placeholder="例如 alice"></label>
      <label>LDAP 测试员工密码<input name="testPassword" type="password" maxlength="1024" autocomplete="off" aria-describedby="ldap-test-hint"></label>
      <small id="ldap-test-hint">请使用测试员工自己的 LDAP 密码。测试失败时，当前表单会保留账号和密码，方便重试。</small>
      <button class="button secondary" type="submit" value="test">运行测试</button>
      <fieldset><legend>验证步骤</legend><ol class="system-validation-steps">
      ${ldapSteps.map((step) => `<li><span>${step}</span><span class="system-badge" data-ldap-step>待验证</span></li>`).join("")}</ol></fieldset>
      <small>测试登录不会创建会话、用户记录或角色关联。</small><p data-ldap-test-status role="status" aria-live="polite"></p></section></form>`;
}

function form(resource: string, fields: string, label: string): string {
  return `<form class="system-card system-form system-password" data-identity-resource="${resource}">
    ${fields}<p role="status" aria-live="polite"></p><button class="button" type="submit">${label}</button></form>`;
}

function passwordField(): string {
  return '<label>新密码<input name="password" type="password" autocomplete="new-password" required minlength="12" maxlength="72"></label>';
}

const ldapSteps = ["目录连接", "搜索账号绑定", "测试员工查询", "测试员工认证"] as const;

function showLDAPSteps(form: HTMLFormElement, stage = ""): void {
  const failureIndex: Record<string, number> = { connection: 0, connection_dns: 0, connection_tcp: 0,
    connection_ldaps: 0, connection_certificate: 0, bind: 1, search: 2, user_search: 2,
    user_match: 2, user_auth: 3, group_search: 3, group_query: 3, identity_mapping: 3 };
  const failed = failureIndex[stage];
  const items = form.querySelectorAll<HTMLElement>("[data-ldap-step]");
  items.forEach((item, index) => {
    item.textContent = stage === "success" || (failed !== undefined && index < failed) ? "通过" :
      index === failed ? "失败" : "待验证";
  });
}

const ldapSaveNeedsTest = "请填写右侧的测试员工账号和密码，运行测试。四项验证全部通过后可保存。";
const ldapVerification = new WeakMap<HTMLFormElement, { token: string; draft: string }>();
const pendingLDAP = new WeakSet<HTMLFormElement>();

function ldapDraft(form: HTMLFormElement): Record<string, FormDataEntryValue | boolean> {
  const data = new FormData(form);
  data.delete("testUsername");
  data.delete("testPassword");
  const allowPlainLDAP = data.has("allowPlainLDAP");
  data.delete("allowPlainLDAP");
  return { ...Object.fromEntries(data), ...(allowPlainLDAP ? { allowPlainLDAP } : {}) };
}

export function invalidateLDAPDraft(form: HTMLFormElement, target: EventTarget | null): void {
  if (!(target instanceof HTMLInputElement || target instanceof HTMLSelectElement) ||
      target.name === "testUsername" || target.name === "testPassword") return;
  ldapVerification.delete(form);
  const state = form.querySelector("[data-ldap-config-state]");
  if (state) state.textContent = "有未保存变更";
  showLDAPSteps(form);
  const save = form.querySelector<HTMLButtonElement>('button[type="submit"]:not([value="test"])');
  if (save) save.disabled = true;
  const hint = form.querySelector("[data-ldap-save-hint]");
  if (hint) hint.textContent = "配置已修改。请在右侧重新运行测试后保存。";
  const status = form.querySelector("[data-ldap-test-status]");
  if (status) status.textContent = "";
}

export async function submitIdentityAdmin(form: HTMLFormElement, signedOut: () => void, testingLDAP = false): Promise<void> {
  const resource = form.dataset.identityResource;
  const ldap = resource === "ldap";
  const hint = ldap ? form.querySelector<HTMLElement>("[data-ldap-save-hint]") : null;
  const status = testingLDAP ? form.querySelector<HTMLElement>("[data-ldap-test-status]") :
    ldap ? hint : form.querySelector<HTMLElement>('[role="status"]');
  const button = form.querySelector<HTMLButtonElement>('button[type="submit"]');
  if (!status || !button || (ldap ? pendingLDAP.has(form) : button.disabled)) return;
  const data = new FormData(form);
  const draft = ldap ? ldapDraft(form) : {};
  const submittedDraft = JSON.stringify(draft);
  const proof = ldapVerification.get(form);
  if (ldap && !testingLDAP && proof?.draft !== submittedDraft) {
    ldapVerification.delete(form);
    button.disabled = true;
    showLDAPSteps(form);
    status.textContent = ldapSaveNeedsTest;
    return;
  }
  if (testingLDAP) {
    const missing = String(data.get("testUsername") ?? "").trim() === "" ? "testUsername" :
      data.get("testPassword") ? "" : "testPassword";
    if (missing) {
      status.textContent = missing === "testUsername" ? "请输入 LDAP 测试员工账号。" : "请输入 LDAP 测试员工密码。";
      form.querySelector<HTMLInputElement>(`[name="${missing}"]`)?.focus();
      return;
    }
    ldapVerification.delete(form);
    showLDAPSteps(form);
    if (hint) hint.textContent = "正在验证配置，通过后可保存。";
  }
  if (ldap) pendingLDAP.add(form);
  const buttons = form.querySelectorAll<HTMLButtonElement>('button[type="submit"]');
  for (const control of buttons) control.disabled = true;
  status.textContent = testingLDAP ? "正在测试 LDAP 服务器…" : "正在保存…";
  try {
    let payload: unknown = Object.fromEntries(data);
    if (resource === "password") {
      if (data.get("password") !== data.get("confirmPassword")) {
        status.textContent = "两次输入的新密码不一致。";
        return;
      }
      payload = { current: data.get("current"), password: data.get("password") };
    }
    if (ldap) {
      payload = testingLDAP ? { ...draft, testUsername: data.get("testUsername"), testPassword: data.get("testPassword") } :
        { ...draft, verification: proof?.token };
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
      if (ldap && response.status === 409) {
        ldapVerification.delete(form);
        showLDAPSteps(form);
      }
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
          submittedDraft === JSON.stringify(ldapDraft(form))) {
        ldapVerification.set(form, { token: verification, draft: submittedDraft });
        const username = form.querySelector<HTMLInputElement>('[name="testUsername"]');
        const password = form.querySelector<HTMLInputElement>('[name="testPassword"]');
        if (username && password && username.value === data.get("testUsername") && password.value === data.get("testPassword")) {
          username.value = "";
          password.value = "";
        }
      }
      showLDAPSteps(form, submittedDraft === JSON.stringify(ldapDraft(form)) ? stage : "");
      const messages: Record<string, string> = {
        success: ldapVerification.has(form)
          ? "连接、搜索账号绑定、员工查询和员工认证均成功，可以保存。"
          : "验证未能用于当前配置，请重新测试。",
        user_auth: "测试员工认证失败，请检查员工密码和目录账号状态。",
        connection: "连接失败，请检查域名、端口和 TLS 证书。",
        connection_dns: "API Pod 无法解析 LDAP 域名，请检查 Pod 的 DNS 配置和域名。",
        connection_tcp: "API Pod 无法建立到 LDAP 端口的 TCP 连接，请检查端口、网络策略和防火墙。",
        connection_ldaps: "TCP 已连通，但 LDAPS 握手失败。请确认端口提供 LDAPS，并检查 TLS 版本。",
        connection_certificate: "LDAPS 证书校验失败。请检查 URL 域名是否匹配证书、证书是否过期，以及 API Pod 是否信任签发 CA。",
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
      const state = form.querySelector("[data-ldap-config-state]");
      const sameDraft = submittedDraft === JSON.stringify(ldapDraft(form));
      if (state && sameDraft) state.textContent = "配置已启用";
      if (!sameDraft) {
        status.textContent = "已保存提交的配置。当前表单还有未保存变更，请重新测试。";
        return;
      }
      form.closest("section")?.querySelector("[data-ldap-issue]")?.remove();
      const password = form.querySelector<HTMLInputElement>('[name="bindPassword"]');
      if (password) {
        password.value = "";
        password.placeholder = data.get("url") ? "已配置，留空保留" : "输入搜索账号密码";
      }
    }
    if (resource === "password") signedOut();
  } catch {
    status.textContent = testingLDAP ? "无法运行 LDAP 测试，请检查接口连接后重试。" : "无法保存，请检查输入或连接。";
  } finally {
    if (resource !== "ldap") {
      for (const field of form.querySelectorAll<HTMLInputElement>('input[type="password"]')) field.value = "";
    }
    if (ldap) {
      pendingLDAP.delete(form);
      if (testingLDAP && hint) hint.textContent = ldapVerification.has(form) ?
        "四项验证已通过。请点击「保存并启用」。" : "验证未通过。请查看右侧测试结果，处理后重新运行测试。";
    }
    for (const control of buttons) control.disabled = ldap && control === button && !ldapVerification.has(form);
  }
}
