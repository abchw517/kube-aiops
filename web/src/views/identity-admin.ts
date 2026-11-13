import { escapeHtml } from "../components/html.ts";

const ldapFields = [
  ["url", "LDAP URL"],
  ["bindDN", "LDAP 搜索 DN"],
  ["baseDN", "LDAP 基础 DN"],
  ["userUID", "LDAP 用户 UID"],
  ["userFilter", "LDAP 附加用户过滤器"],
  ["groupAttribute", "LDAP 组属性"],
] as const;

export async function renderIdentityAdmin(resource: string, initial: boolean): Promise<string> {
  const navigation = initial ? "" : `<nav class="identity-nav" aria-label="系统管理">
    <a href="#/admin/ldap"${resource === "ldap" ? ' aria-current="page"' : ""}>认证配置</a>
    <a href="#/admin/users"${resource === "users" ? ' aria-current="page"' : ""}>用户管理</a>
    <a href="#/admin/policy"${resource === "policy" ? ' aria-current="page"' : ""}>权限配置</a>
    <a href="#/admin/password"${resource === "password" ? ' aria-current="page"' : ""}>修改密码</a>
  </nav>`;
  let content: string;
  try {
    content = await renderResource(resource, initial);
  } catch {
    content = "<h2>无法加载配置</h2><p>请刷新后重试。</p>";
  }
  return `<section class="panel identity-panel">${navigation}${content}</section>`;
}

async function renderResource(resource: string, initial: boolean): Promise<string> {
  if (resource === "password") {
    return `<h2>${initial ? "首次登录，请修改管理员密码" : "修改管理员密码"}</h2>
      ${form("password", `<label>当前密码<input name="current" type="password" autocomplete="current-password" required></label>
        ${passwordField()}`, "修改密码")}`;
  }
  const selected = resource === "users" || resource === "policy" ? resource : "ldap";
  const response = await fetch(`/api/v1/admin/${selected}`);
  if (!response.ok || !response.headers.get("Content-Type")?.includes("application/json")) {
    throw new Error("identity configuration unavailable");
  }
  const data = await response.json();
  if (selected === "users") {
    const rows = (data as { username: string; groups: string[] | null }[]).map((user) =>
      `<li>${escapeHtml(user.username)} · ${escapeHtml(user.groups?.join("、") || "无组")}</li>`);
    return `<h2>本地用户</h2><ul>${rows.join("")}</ul>
      ${form("users", `<label>用户名<input name="username" required maxlength="250"></label>
        ${passwordField()}<label>所属组<input name="groups" placeholder="以逗号分隔"></label>`, "创建用户")}`;
  }
  if (selected === "policy") {
    return `<h2>权限配置</h2>
      <p>使用 local:用户名、local:组名或 ldap:完整组 DN 设置规则，并指定能力、集群和命名空间。</p>
      ${form("policy", `<label>授权策略 JSON<textarea name="policy" rows="16" required>${escapeHtml(JSON.stringify(data, null, 2))}</textarea></label>`, "保存策略")}`;
  }
  return `<h2>认证配置</h2><p>本地账号始终可用。支持 ldaps://（TLS）和 ldap://（StartTLS），均验证证书。AD 用户 UID 通常填写 sAMAccountName，附加过滤器可填 (objectClass=user)。已有 %s 模板时请留空 UID。搜索密码在此配置。私有 CA 通过部署 Secret 挂载。</p>
    ${form("ldap", ldapFields.map(([key, label]) =>
      `<label>${label}<input name="${key}" value="${escapeHtml(data[key] ?? "")}"></label>`).join("") +
      `<label>LDAP 用户搜索范围<select name="searchScope">
        <option value="subtree">子树</option>
        <option value="onelevel"${data.searchScope === "onelevel" ? " selected" : ""}>单层</option>
        <option value="base"${data.searchScope === "base" ? " selected" : ""}>基础对象</option>
      </select></label>
      <label>LDAP 搜索密码<input name="bindPassword" type="password" autocomplete="new-password" placeholder="${data.passwordConfigured === true ? "已配置，留空保留" : "输入搜索账号密码"}"></label>`, "保存配置")}`;
}

function form(resource: string, fields: string, label: string): string {
  return `<form class="login-form identity-form" data-identity-resource="${resource}">
    ${fields}<p role="status"></p><button class="button" type="submit">${label}</button>
    ${resource === "ldap" ? '<button class="button secondary" type="submit" value="test">测试 LDAP 服务器</button>' : ""}
  </form>`;
}

function passwordField(): string {
  return '<label>新密码<input name="password" type="password" autocomplete="new-password" required minlength="12" maxlength="72"></label>';
}

export async function submitIdentityAdmin(form: HTMLFormElement, signedOut: () => void, testingLDAP = false): Promise<void> {
  const status = form.querySelector<HTMLElement>('[role="status"]');
  const button = form.querySelector<HTMLButtonElement>('button[type="submit"]');
  if (!status || !button || button.disabled) return;
  const buttons = form.querySelectorAll<HTMLButtonElement>('button[type="submit"]');
  for (const control of buttons) control.disabled = true;
  status.textContent = testingLDAP ? "正在测试 LDAP 服务器…" : "正在保存…";
  const resource = form.dataset.identityResource;
  const data = new FormData(form);
  try {
    let payload: unknown = Object.fromEntries(data);
    if (resource === "policy") payload = JSON.parse(String(data.get("policy")));
    if (resource === "users") {
      payload = { username: data.get("username"), password: data.get("password"),
        groups: String(data.get("groups") ?? "").split(/[,，]/).map((group) => group.trim()).filter(Boolean) };
    }
    const response = await fetch(`/api/v1/admin/${testingLDAP ? "ldap-test" : resource}`, {
      method: !testingLDAP && (resource === "policy" || resource === "ldap") ? "PUT" : "POST",
      headers: { "Content-Type": "application/json" },
      body: JSON.stringify(payload),
    });
    if (resource === "ldap" && (response.status === 404 || response.status === 405 ||
        (response.ok && response.status !== 204 && !response.headers.get("Content-Type")?.includes("application/json")))) {
      status.textContent = "LDAP 管理接口不可用，请检查 API 镜像版本和 /api/v1 路由。";
      return;
    }
    if (!response.ok) {
      if (resource === "ldap" && response.status === 400 && response.headers.get("Content-Type")?.startsWith("text/plain")) {
        const message = (await response.text()).trim();
        status.textContent = message === "password_required" ? "更换 LDAP 地址或搜索 DN 后，请重新填写搜索密码。" : message || "LDAP 配置无效。";
        return;
      }
      status.textContent = response.status === 409 ? "账号已存在或配置已更新。" :
        response.status === 400 ? "输入无效，请核对配置或密码。" : "操作失败，请重试。";
      return;
    }
    if (testingLDAP) {
      const { stage } = await response.json();
      const messages: Record<string, string> = {
        success: "测试通过：TLS 连接、搜索账号绑定和基础 DN 搜索均成功。",
        connection: "连接失败，请检查域名、端口和 TLS 证书。",
        bind: "搜索账号绑定失败，请检查搜索 DN 和密码。",
        search: "搜索失败，请检查基础 DN 和账号读取权限。",
      };
      status.textContent = messages[stage] ?? "测试失败，请检查 LDAP 配置。";
      return;
    }
    if (resource === "ldap" && response.status !== 204) {
      status.textContent = "API 未确认保存，请检查 API 镜像版本和 /api/v1 路由。";
      return;
    }
    status.textContent = "已保存";
    if (resource === "ldap") {
      const password = form.querySelector<HTMLInputElement>('[name="bindPassword"]');
      if (password) password.placeholder = data.get("url") ? "已配置，留空保留" : "输入搜索账号密码";
    }
    if (resource === "password") signedOut();
    if (resource === "users") {
      const list = form.closest("section")?.querySelector("ul");
      const item = document.createElement("li");
      item.textContent = `${data.get("username")} · ${data.get("groups") || "无组"}`;
      list?.append(item);
      form.reset();
    }
  } catch {
    status.textContent = "无法保存，请检查输入或连接。";
  } finally {
    if (!testingLDAP) {
      for (const field of form.querySelectorAll<HTMLInputElement>('input[type="password"]')) field.value = "";
    }
    for (const control of buttons) control.disabled = false;
  }
}
