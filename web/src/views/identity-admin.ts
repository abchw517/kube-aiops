import { escapeHtml } from "../components/html.ts";

const ldapFields = [
  ["url", "LDAP URL"],
  ["bindDN", "LDAP 搜索 DN"],
  ["baseDN", "LDAP 基础 DN"],
  ["userFilter", "LDAP 用户过滤器"],
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
  if (!response.ok) throw new Error("identity configuration unavailable");
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
  return `<h2>认证配置</h2><p>本地账号始终可用。填写 LDAPS URL 启用目录登录。搜索密码和 CA 通过部署 Secret 配置。</p>
    ${form("ldap", ldapFields.map(([key, label]) =>
      `<label>${label}<input name="${key}" value="${escapeHtml(data[key] ?? "")}"></label>`).join(""), "保存配置")}`;
}

function form(resource: string, fields: string, label: string): string {
  return `<form class="login-form identity-form" data-identity-resource="${resource}">
    ${fields}<p role="status"></p><button class="button" type="submit">${label}</button>
  </form>`;
}

function passwordField(): string {
  return '<label>新密码<input name="password" type="password" autocomplete="new-password" required minlength="12" maxlength="72"></label>';
}

export async function submitIdentityAdmin(form: HTMLFormElement, signedOut: () => void): Promise<void> {
  const status = form.querySelector<HTMLElement>('[role="status"]');
  const button = form.querySelector<HTMLButtonElement>('button[type="submit"]');
  if (!status || !button || button.disabled) return;
  button.disabled = true;
  const resource = form.dataset.identityResource;
  const data = new FormData(form);
  try {
    let payload: unknown = Object.fromEntries(data);
    if (resource === "policy") payload = JSON.parse(String(data.get("policy")));
    if (resource === "users") {
      payload = { username: data.get("username"), password: data.get("password"),
        groups: String(data.get("groups") ?? "").split(/[,，]/).map((group) => group.trim()).filter(Boolean) };
    }
    const response = await fetch(`/api/v1/admin/${resource}`, {
      method: resource === "policy" || resource === "ldap" ? "PUT" : "POST",
      headers: { "Content-Type": "application/json" },
      body: JSON.stringify(payload),
    });
    if (!response.ok) {
      status.textContent = response.status === 409 ? "账号已存在或配置已更新。" :
        response.status === 400 ? "输入无效，请核对配置或密码。" : "操作失败，请重试。";
      return;
    }
    status.textContent = "已保存";
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
    for (const field of form.querySelectorAll<HTMLInputElement>('input[type="password"]')) field.value = "";
    button.disabled = false;
  }
}
