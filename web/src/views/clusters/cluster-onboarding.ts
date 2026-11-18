import { renderNamespaceOwnership } from "./namespace-ownership.ts";
import { escapeHtml } from "../../components/html.ts";

const endpoint = "/api/v1/admin/cluster-onboarding";
const route = "#/resources/clusters";
const fields = [
  ["id", "实例 ID", true], ["name", "实例名称", true],
  ["environment", "环境", true], ["business", "业务线", true], ["group", "集群名称", true],
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
  firstCollectedAt: number;
  validatedAt: number;
  checkedAt: number;
  collection?: {
    nodes?: { name: string; ready: string }[];
    namespaces?: { name: string }[];
    nodeSampleAt?: number;
    nodeFailures?: number;
    nodeError?: string;
    namespaceError?: boolean;
    findingError?: boolean;
    findingCount?: number;
    workloadFinding?: boolean;
  };
};

function collectionStatus(item: Entry): string {
  if (!item.enabled) return "已停用";
  if (item.mode === "push") {
    if (!item.reportedAt) return "等待首次上报";
    if (Date.now() / 1000 - item.reportedAt > 180) return "采集离线";
    return item.firstCollectedAt ? "已接入" : "上报在线，等待首次诊断";
  }
  const timedOut = item.validatedAt > 0 && Date.now() / 1000 - item.validatedAt >= 300;
  if (!item.firstCollectedAt) {
    if (!item.validatedAt && item.checkedAt) return "待接入";
    return timedOut ? "首次采集超时" : "等待首次采集";
  }
  if ((item.collection?.nodeFailures ?? 0) >= 2 && item.collection?.nodeSampleAt) return "已接入 · 节点数据已过期";
  if (item.collection?.nodes?.some((node) => node.ready === "False")) return "已接入 · 集群异常";
  if (item.collection?.nodes?.some((node) => node.ready === "Unknown")) return "已接入 · 节点状态未知";
  if (item.collection?.nodes?.length) return "已接入 · 当前集群正常";
  return timedOut ? "已接入 · 集群状态信息采集异常" : "已接入 · 等待节点数据";
}

function displayTime(seconds: number): string {
  return escapeHtml(new Date(seconds * 1000).toLocaleString());
}

function renderCollection(item: Entry): string {
  const data = item.collection;
  return `<details class="collection-details"><summary>采集详情</summary>
    <p>${item.firstCollectedAt ? `首次采集成功 ${displayTime(item.firstCollectedAt)}` : "尚未收到有效采集数据"}</p>
    ${item.reportedAt ? `<p>最近上报 ${displayTime(item.reportedAt)}</p>` : ""}
    ${item.checkedAt ? `<p>最近检查 ${displayTime(item.checkedAt)}</p>` : ""}
    ${data?.nodeSampleAt ? `<p>节点更新时间 ${displayTime(data.nodeSampleAt)}</p>` : ""}
    ${(data?.nodeFailures ?? 0) >= 2 && data?.nodeSampleAt ? '<p role="status">节点数据已过期</p>' : ""}
    ${data?.nodeError ? `<p>${escapeHtml(data.nodeError)}</p>` : ""}
    ${data?.nodes?.length ? `<ul aria-label="节点状态">${data.nodes.map((node) =>
      `<li>${escapeHtml(node.name)} · <strong>${node.ready === "True" ? "Ready" : node.ready === "False" ? "NotReady" : "Unknown"}</strong></li>`).join("")}</ul>` : ""}
    ${data?.namespaces?.length ? `<p>命名空间 · ${data.namespaces.map(({ name }) => escapeHtml(name)).join("、")}</p>` : ""}
    ${data?.namespaceError ? "<p>命名空间采集异常</p>" : ""}
    ${data?.findingError ? "<p>诊断采集异常，请检查 K8sGPT 安装和 Result 读取权限</p>" :
      data?.findingCount ? "<p>已收到 K8sGPT 诊断</p>" : ""}
    ${!data?.findingError && data?.workloadFinding ? "<p>发现工作负载故障</p>" : ""}
  </details>`;
}

export async function renderOnboarding(id?: string): Promise<string> {
  if (id?.startsWith("ownership-")) return renderNamespaceOwnership(id.slice(10));
  const response = await fetch(endpoint);
  if (!response.ok || !response.headers.get("Content-Type")?.includes("application/json")) {
    throw new Error("directory unavailable");
  }
  const items = await response.json() as Entry[];
  if (!id) {
    return `<div class="catalog-section">
      <h2>集群接入</h2>
      <div class="catalog-table-wrap"><table class="catalog-table">
        <thead><tr><th scope="col">集群</th><th scope="col">归属</th><th scope="col">采集状态</th><th scope="col">操作</th></tr></thead>
        <tbody>${items.map((item) => `<tr>
          <td>${escapeHtml(item.group || item.name)} <code>${escapeHtml(item.id)}</code>
            <div>${item.mode === "local" ? "本地自动采集" : item.mode === "pull" ? "手动配置" : "采集上报"}</div></td>
          <td>${escapeHtml([item.environment, item.business].filter(Boolean).join(" / ") || "待补齐")}</td>
          <td>${collectionStatus(item)}${renderCollection(item)}</td>
          <td><div class="catalog-actions">${item.mode === "local" ? "" : `<a href="${route}/edit-${encodeURIComponent(item.id)}">编辑</a>`}
          ${item.enabled ? `<a href="${route}/ownership-${encodeURIComponent(item.id)}">命名空间归属</a>` : ""}</div></td>
        </tr>`).join("") || '<tr><td colspan="4" class="catalog-empty">暂无集群。</td></tr>'}</tbody>
      </table></div>
      <div class="catalog-bottom"><span>共 ${items.length} 条</span>
        <div class="catalog-actions"><a class="button secondary" href="${route}/push">采集上报</a>
          <a class="button" href="${route}/pull">＋ 新增集群</a></div>
      </div>
    </div>`;
  }
  const editing = id.startsWith("edit-");
  const item = editing ? items.find((entry) => entry.id === id.slice(5)) : undefined;
  const mode = item?.mode ?? id;
  if ((editing && !item) || (mode !== "pull" && mode !== "push")) {
    return `<p>实例不存在。</p><a href="${route}">返回集群接入</a>`;
  }
  const catalogs = await Promise.all(["environments", "business-lines"].map(async (kind) => {
    const response = await fetch(`/api/v1/admin/${kind}`);
    if (!response.ok) throw new Error("resource catalog unavailable");
    return await response.json() as { name: string; id: string }[];
  }));
  const locked = Boolean(item?.firstCollectedAt);
  return `<h2>${mode === "pull" ? "手动配置" : "采集上报"}</h2><a href="${route}">返回集群接入</a>
    <form class="login-form identity-form" data-onboarding-mode="${mode}" data-editing="${editing}">
      ${fields.filter(([key, , base]) => base || mode === "pull" || !["apiURL", "engineNamespace", "engineName"].includes(key)).map(([name,label,base]) => {
        const current = String(item?.[name] ?? "");
        const choices = name === "environment" ? catalogs[0] : name === "business" ? catalogs[1] : undefined;
        const fixed = editing && (name === "id" || (name === "environment" && locked));
        if (choices && !fixed) {
          const names = choices.map((entry) => entry.name);
          if (current && !names.includes(current)) names.unshift(current);
          return `<label>${label}<select name="${name}" required>
            <option value="">请选择${label}</option>${names.map((value) =>
              `<option value="${escapeHtml(value)}"${value === current ? " selected" : ""}>${escapeHtml(value)}</option>`).join("")}
          </select></label>`;
        }
        return `<label>${label}<input name="${name}" value="${escapeHtml(current)}" ${base ? "required" : ""}
          ${fixed ? "readonly" : ""}>${name === "environment" && locked ? "<small>首次采集已成功，环境已锁定。</small>" : ""}</label>`;
      }).join("")}
      ${mode === "pull" ? `<label><span><input type="checkbox" name="aiEnabled"${item?.aiEnabled === true ? " checked" : ""} style="width:auto"> 启用 AI 复核（读取目标 Engine Secret）</span></label>` : ""}
      <label><span><input type="checkbox" name="enabled"${item?.enabled !== false ? " checked" : ""} style="width:auto"> 启用实例</span></label>
      <p>${mode === "pull" ? "只读 token 和 CA 使用该实例 ID 对应的部署挂载。配置共享观测源时使用实例 ID 作为集群标签。" :
        "保存后会显示一次上报凭据。采集端提交快照后显示在线，收到真实诊断后锁定环境。"} </p>
      <p role="status"></p>
      <textarea rows="3" readonly hidden aria-label="上报凭据" data-report-token></textarea>
      <button type="submit" class="button">保存</button>
      ${mode === "pull" ? '<button type="submit" value="test" class="button secondary">测试连接</button>' : ""}
    </form>
    ${item ? `<section><h3>${collectionStatus(item)}</h3>${renderCollection(item)}</section>` : ""}
    ${item?.mode === "pull" && item.enabled ? `<form data-collection-check="${escapeHtml(item.id)}">
      <button type="submit" class="button secondary">重新校验</button><p role="status"></p></form>` : ""}`;
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
      status.textContent = "已验证 Kubernetes API 连接和节点读取权限。";
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

export async function recheckCollection(form: HTMLFormElement, reload: () => void): Promise<void> {
  const button = form.querySelector("button");
  const status = form.querySelector('[role="status"]');
  if (!button || !status || button.disabled) return;
  button.disabled = true;
  status.textContent = "正在重新校验…";
  try {
    const response = await fetch(`${endpoint}?action=recheck&id=${encodeURIComponent(form.dataset.collectionCheck ?? "")}`, { method: "POST" });
    if (!response.ok) throw new Error();
    reload();
  } catch {
    status.textContent = "重新校验失败，请稍后重试。";
    button.disabled = false;
  }
}
