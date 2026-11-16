import type { Cluster } from "../../../clients/typescript/generated.ts";
import { escapeHtml } from "../components/html.ts";

const modules = [
  ["clusters:list", "集群列表", "查看平台已接入的集群"],
  ["namespaces:list", "命名空间列表", "查看所选集群的命名空间名称"],
  ["findings:list", "诊断列表", "打开诊断列表并查看授权范围内的结果"],
  ["findings:summary", "诊断汇总", "查看授权范围内的诊断统计"],
  ["findings:read", "诊断详情", "查看单条诊断及分析结论"],
  ["resources:read", "资源详情", "查看诊断关联的 Kubernetes 资源"],
  ["correlations:read", "关联证据", "查看诊断关联的事件、指标、日志和告警"],
  ["wiki:read", "知识库浏览", "查看授权范围内的文档"],
  ["wiki:publish", "知识库发布", "录入并发布授权范围内的文档"],
  ["wiki:sync", "知识库同步", "管理本人远端 Wiki 凭据"],
] as const;

interface Role {
  id: string;
  revision: number;
  name: string;
  subjects: string[];
  groups: string[];
  modules: string[];
  cluster: string;
  namespaces: string[];
}

interface LegacyRule {
  effect: string;
  subjects?: string[];
  groups?: string[];
  capabilities: string[];
  scopes: { cluster: string; namespace?: string }[];
}

function moduleName(id: string): string {
  return modules.find(([module]) => module === id)?.[1] ?? id;
}

function scopeName(cluster: string, namespace?: string): string {
  return `${cluster === "*" ? "全部集群" : cluster} / ${namespace === "*" ? "全部命名空间" : namespace || "集群级"}`;
}

export async function renderRoles(roleID?: string): Promise<string> {
  const response = await fetch("/api/v1/admin/roles");
  if (!response.ok || !response.headers.get("Content-Type")?.includes("application/json")) throw new Error("roles unavailable");
  const data = await response.json() as { roles: Role[] | null; legacyRules: LegacyRule[] | null };
  const roles = data.roles ?? [];
  const legacyRules = data.legacyRules ?? [];
  let clusters: Cluster[] = [];
  if (roleID) {
    try {
      const reply = await fetch("/api/v1/clusters");
      if (reply.ok) clusters = (await reply.json() as { items: Cluster[] }).items;
    } catch { /* The instance ID field remains available. */ }
  }
  if (roleID === "new") return roleForm(undefined, clusters);
  if (roleID) {
    const role = roles.find((entry) => entry.id === roleID);
    return role ? roleForm(role, clusters) : '<h2>角色不存在</h2><a href="#/admin/policy">返回角色列表</a>';
  }

  return `<div class="roles-heading"><h2>权限配置</h2><a class="button" href="#/admin/policy/new">添加角色</a></div>
    ${roles.length ? `<ul class="role-list">${roles.map((role) => `<li>
      <div><h3>${escapeHtml(role.name)}</h3>
        <p>${escapeHtml(role.modules.map(moduleName).join("、"))}</p>
        <p>范围：${escapeHtml((role.namespaces?.length ? role.namespaces : [""]).map((namespace) => scopeName(role.cluster, namespace)).join("、"))}</p>
        <p>成员：${escapeHtml([...(role.subjects ?? []), ...(role.groups ?? [])].join("、") || "未分配")}</p>
      </div>
      <a href="#/admin/policy/${encodeURIComponent(role.id)}" aria-label="编辑角色 ${escapeHtml(role.name)}">编辑</a>
    </li>`).join("")}</ul>` : "<p>还没有角色。添加角色后可为用户或组分配权限。</p>"}
    ${legacyRules.length ? `<details class="legacy-roles"><summary>已有策略规则（${legacyRules.length} 条）</summary>
      <p>已有策略继续生效。显式拒绝优先于角色授予的权限。</p>
      <ul>${legacyRules.map((rule) => `<li><strong>${rule.effect === "deny" ? "拒绝" : "允许"}</strong>
        ${escapeHtml(rule.capabilities.map(moduleName).join("、"))}<br>
        成员：${escapeHtml([...(rule.subjects ?? []), ...(rule.groups ?? [])].join("、"))}<br>
        范围：${escapeHtml(rule.scopes.map((scope) => scopeName(scope.cluster, scope.namespace)).join("、"))}
      </li>`).join("")}</ul>
    </details>` : ""}`;
}

function memberField(label: string, name: string, members: string[] | undefined, prefix: string, example: string): string {
  const values = (members ?? []).filter((member) => member.startsWith(prefix)).map((member) => member.slice(prefix.length));
  return `<label>${label}<textarea name="${name}" rows="2" placeholder="${example}">${escapeHtml(values.join("\n"))}</textarea></label>`;
}

function roleForm(role: Role | undefined, clusters: Cluster[]): string {
  const paths = new Map<string, { label: string; ids: string[] }>();
  for (const instance of clusters) {
    const parts = [instance.environment || "未分组", instance.business || "未分组", instance.group || "未分组"];
    for (let i = 1; i <= parts.length; i++) {
      const path = parts.slice(0, i);
      const key = JSON.stringify(path);
      const entry = paths.get(key) ?? { label: path.join(" / "), ids: [] };
      entry.ids.push(instance.id);
      paths.set(key, entry);
    }
    paths.set(JSON.stringify([instance.id, ...parts]), {
      label: [...parts, `${instance.name} (${instance.id})`].join(" / "), ids: [instance.id],
    });
  }
  const directory = paths.size ? `<label>从集群目录选择<select data-role-directory>
      <option value="">选择环境、业务、集群或实例</option>
      ${[...paths.values()].map(({ label, ids }) => `<option value="${escapeHtml(ids.join(","))}">${escapeHtml(label)}</option>`).join("")}
    </select></label>` : "";

  return `<div class="roles-heading"><h2>${role ? "编辑角色" : "添加角色"}</h2><a href="#/admin/policy">返回角色列表</a></div>
    <form class="login-form identity-form role-form" data-role-form data-role-id="${escapeHtml(role?.id ?? "")}" data-revision="${role?.revision ?? ""}">
      <label>角色名称<input name="name" value="${escapeHtml(role?.name ?? "")}" maxlength="128" required placeholder="例如 开发环境观察员"></label>
      <fieldset><legend>权限模块</legend><div class="role-modules">
        ${modules.map(([id, label, description]) => `<label class="role-module">
          <input type="checkbox" name="modules" value="${id}"${role?.modules.includes(id) ? " checked" : ""}>
          <span><strong>${label}</strong><small>${description}</small></span>
        </label>`).join("")}
      </div></fieldset>
      <fieldset><legend>授权范围</legend>
        <div class="role-fields">
          ${directory}
          <label>集群实例 ID（逗号分隔）<input name="cluster" value="${escapeHtml(role?.cluster ?? "local")}" maxlength="4096" required></label>
          <label>命名空间<input name="namespaces" value="${escapeHtml(role?.namespaces?.join(", ") ?? "")}" placeholder="例如 dev, staging"></label>
        </div>
        <small>目录选择授权给当前匹配的实例。新增实例需重新授权。按逗号分隔命名空间，填写 * 表示所选集群的全部命名空间。诊断、资源和证据权限按指定命名空间授权。</small>
      </fieldset>
      <fieldset><legend>角色成员</legend>
        <p>可稍后分配成员。每行填写一个用户或组，LDAP 请填写完整 DN。</p>
        <div class="role-fields">
          ${memberField("本地用户", "localUsers", role?.subjects, "local:", "例如 portal-operator")}
          ${memberField("本地组", "localGroups", role?.groups, "local:", "例如 operators")}
          ${memberField("LDAP 用户", "ldapUsers", role?.subjects, "ldap:", "例如 CN=Alice,OU=Users,DC=example,DC=com")}
          ${memberField("LDAP 组", "ldapGroups", role?.groups, "ldap:", "例如 CN=Readers,OU=Groups,DC=example,DC=com")}
        </div>
      </fieldset>
      <p role="status" aria-live="polite"></p>
      <div class="role-actions">
        <button type="submit" class="button">保存角色</button>
        ${role ? '<button type="submit" class="button secondary" value="delete" formnovalidate>删除角色</button>' : ""}
      </div>
    </form>`;
}

export async function submitRole(form: HTMLFormElement, deleting: boolean): Promise<void> {
  const status = form.querySelector<HTMLElement>('[role="status"]');
  const buttons = [...form.querySelectorAll<HTMLButtonElement>('button[type="submit"]')];
  if (!status || buttons.some((button) => button.disabled)) return;
  if (deleting && !window.confirm("确定删除此角色？通过该角色授予的权限将被撤销。")) return;
  const data = new FormData(form);
  const selected = data.getAll("modules");
  if (!deleting && selected.length === 0) {
    status.textContent = "请至少勾选一个权限模块。";
    form.querySelector<HTMLInputElement>('[name="modules"]')?.focus();
    return;
  }
  const members = (name: string, prefix: string) => String(data.get(name) ?? "").split(/\r?\n/)
    .map((entry) => entry.trim()).filter(Boolean).map((entry) => prefix + entry);
  const payload = {
    name: String(data.get("name") ?? "").trim(),
    subjects: [...members("localUsers", "local:"), ...members("ldapUsers", "ldap:")],
    groups: [...members("localGroups", "local:"), ...members("ldapGroups", "ldap:")],
    cluster: String(data.get("cluster") ?? "").trim(),
    // Preserve one explicit wildcard and remove duplicate namespace selections.
    namespaces: [...new Set(String(data.get("namespaces") ?? "").split(/[,，]/).map((entry) => entry.trim()).filter(Boolean))],
    modules: selected,
  };
  for (const button of buttons) button.disabled = true;
  status.textContent = deleting ? "正在删除角色…" : "正在保存角色…";
  const id = form.dataset.roleId;
  const query = id ? `?${new URLSearchParams({ id, revision: form.dataset.revision ?? "" })}` : "";
  try {
    const response = await fetch(`/api/v1/admin/roles${query}`, {
      method: deleting ? "DELETE" : id ? "PUT" : "POST",
      ...(deleting ? {} : { headers: { "Content-Type": "application/json" }, body: JSON.stringify(payload) }),
    });
    if (response.status !== 204) {
      status.textContent = [400, 404, 409, 503].includes(response.status) && response.headers.get("Content-Type")?.startsWith("text/plain")
        ? (await response.text()).trim() : "操作失败，请重新打开角色配置后重试。";
      return;
    }
    window.location.hash = "#/admin/policy";
  } catch {
    status.textContent = "无法保存角色，请检查连接后重试。";
  } finally {
    for (const button of buttons) button.disabled = false;
  }
}
