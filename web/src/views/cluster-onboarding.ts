import { escapeHtml } from "../components/html.ts";

const endpoint = "/api/v1/admin/cluster-onboarding";
const route = "#/admin/clusters";
const fields = [
  ["id", "实例 ID", true], ["name", "实例名称", true],
  ["environment", "环境", true], ["business", "业务", true], ["group", "集群名称", true],
  ["apiURL", "Kubernetes API 地址", false], ["engineNamespace", "Engine 命名空间", false],
  ["engineName", "Engine 实例名", false], ["prometheusURL", "Prometheus 地址", false],
  ["victoriaLogsURL", "VictoriaLogs 地址", false], ["alertmanagerURL", "Alertmanager 地址", false],
] as const;

type Entry = Record<string, string | boolean | number | undefined> & {
  id: string;
  name: string;
  mode: string;
  enabled: boolean;
  reportedAt: number;
};

export async function renderOnboarding(id?: string): Promise<string> {
  const response = await fetch(endpoint);
  if (!response.ok || !response.headers.get("Content-Type")?.includes("application/json")) {
    throw new Error("directory unavailable");
  }
  const items = await response.json() as Entry[];
  if (!id) {
    return `<h2>集群接入</h2><p><a href="${route}/pull">手动配置</a> · <a href="${route}/push">采集上报</a></p>
      <ul class="role-list">${items.map((item) => {
        const status = !item.enabled ? "已停用" : item.mode === "pull" ? "已启用" :
          !item.reportedAt ? "等待首次上报" : Date.now() / 1000 - item.reportedAt > 180 ? "采集离线" : "采集在线";
        return `<li><div><strong>${escapeHtml([item.environment, item.business, item.group, item.name].join(" / "))}</strong>
          <p>${escapeHtml(item.id)} · ${item.mode === "pull" ? "手动配置" : "采集上报"} · ${status}
          ${item.reportedAt ? ` · 最近上报 ${escapeHtml(new Date(item.reportedAt * 1000).toLocaleString())}` : ""}</p></div>
          <a href="${route}/edit-${encodeURIComponent(item.id)}">编辑</a></li>`;
      }).join("")}</ul>`;
  }
  const editing = id.startsWith("edit-");
  const item = editing ? items.find((entry) => entry.id === id.slice(5)) : undefined;
  const mode = item?.mode ?? id;
  if ((editing && !item) || (mode !== "pull" && mode !== "push")) {
    return `<p>实例不存在。</p><a href="${route}">返回集群接入</a>`;
  }
  return `<h2>${mode === "pull" ? "手动配置" : "采集上报"}</h2><a href="${route}">返回集群接入</a>
    <form class="login-form identity-form" data-onboarding-mode="${mode}" data-editing="${editing}">
      ${fields.filter(([key, , base]) => base || mode === "pull" || !["apiURL", "engineNamespace", "engineName"].includes(key)).map(([name,label,base]) =>
        `<label>${label}<input name="${name}" value="${escapeHtml(String(item?.[name] ?? ""))}" ${base ? "required" : ""}
          ${editing && name === "id" ? "readonly" : ""}></label>`).join("")}
      ${mode === "pull" ? `<label><span><input type="checkbox" name="aiEnabled"${item?.aiEnabled === true ? " checked" : ""} style="width:auto"> 启用 AI 复核（读取目标 Engine Secret）</span></label>` : ""}
      <label><span><input type="checkbox" name="enabled"${item?.enabled !== false ? " checked" : ""} style="width:auto"> 启用实例</span></label>
      <p>${mode === "pull" ? "只读 token 和 CA 使用该实例 ID 对应的部署挂载。配置共享观测源时使用实例 ID 作为集群标签。" :
        "保存后会显示一次上报凭据。采集端首次提交快照后自动显示在线。"} </p>
      <p role="status"></p>
      <textarea rows="3" readonly hidden aria-label="上报凭据" data-report-token></textarea>
      <button type="submit" class="button">保存</button>
      ${mode === "pull" ? '<button type="submit" value="test" class="button secondary">测试连接</button>' : ""}
    </form>`;
}

export async function submitOnboarding(form: HTMLFormElement, testing: boolean): Promise<void> {
  const status = form.querySelector<HTMLElement>('[role="status"]');
  const token = form.querySelector<HTMLTextAreaElement>("[data-report-token]");
  const buttons = [...form.querySelectorAll<HTMLButtonElement>('button[type="submit"]')];
  if (!status || !token || buttons.some((button) => button.disabled)) return;
  const data = new FormData(form);
  const values = Object.fromEntries(fields.map(([key]) => [key, String(data.get(key) ?? "").trim()]));
  for (const button of buttons) button.disabled = true;
  status.textContent = testing ? "正在测试连接…" : "正在保存…";
  let keepDisabled = false;
  try {
    const response = await fetch(endpoint + (testing ? "?test=true" : ""), {
      method: form.dataset.editing === "true" ? "PUT" : "POST",
      headers: { "Content-Type": "application/json" },
      body: JSON.stringify({ ...values, mode: form.dataset.onboardingMode, enabled: data.has("enabled"), aiEnabled: data.has("aiEnabled") }),
    });
    if (!response.ok) {
      status.textContent = response.headers.get("Content-Type")?.includes("text/plain") ?
        (await response.text()).trim() : "操作失败，请检查配置。";
      return;
    }
    if (testing) {
      status.textContent = "已验证 Kubernetes API 连接和 Result 读取权限。";
      return;
    }
    const result = await response.json() as { token?: string };
    if (result.token) {
      token.value = result.token;
      token.hidden = false;
      keepDisabled = true;
      status.textContent = "保存成功。请将上报凭据保存到采集端 Secret。";
    } else {
      window.location.hash = route;
    }
  } catch {
    status.textContent = "集群接入请求失败。";
  } finally {
    for (const button of buttons) button.disabled = keepDisabled;
  }
}
