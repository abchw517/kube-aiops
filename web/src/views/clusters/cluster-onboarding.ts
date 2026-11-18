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
  deleted?: boolean;
  credentialKind?: string;
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
  if (item.deleted) return "已删除";
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
  return `<div class="collection-details">
    <p>${item.firstCollectedAt ? `首次采集成功 ${displayTime(item.firstCollectedAt)}` : "尚未收到有效采集数据"}</p>
    ${item.reportedAt ? `<p>最近上报 ${displayTime(item.reportedAt)}</p>` : ""}
    ${item.checkedAt ? `<p>最近检查 ${displayTime(item.checkedAt)}</p>` : ""}
    ${data?.nodeSampleAt ? `<p>节点更新时间 ${displayTime(data.nodeSampleAt)}</p>` : ""}
    ${(data?.nodeFailures ?? 0) >= 2 && data?.nodeSampleAt ? '<p role="status">节点数据已过期</p>' : ""}
    ${data?.nodeError ? `<p>${escapeHtml(data.nodeError)}</p>` : ""}
    <h3>节点状态</h3>
    ${data?.nodes?.length ? `<ul aria-label="节点状态">${data.nodes.map((node) =>
      `<li>${escapeHtml(node.name)} · <strong>${node.ready === "True" ? "Ready" : node.ready === "False" ? "NotReady" : "Unknown"}</strong></li>`).join("")}</ul>` : "<p>尚未收到节点数据。</p>"}
    <h3>命名空间</h3>
    ${data?.namespaceError ? "<p>命名空间采集异常</p>" : data?.namespaces?.length ?
      `<p>${data.namespaces.map(({ name }) => escapeHtml(name)).join("、")}</p>` : "<p>暂无数据。</p>"}
    <h3>诊断采集</h3>
    ${data?.findingError ? "<p>诊断采集异常，请检查 K8sGPT 安装和 Result 读取权限</p>" :
      data?.findingCount ? "<p>已收到 K8sGPT 诊断</p>" : "<p>尚未收到诊断。</p>"}
    ${!data?.findingError && data?.workloadFinding ? "<p>发现工作负载故障</p>" : ""}
  </div>`;
}

export async function renderOnboarding(id?: string, modal = false): Promise<string> {
  if (id?.startsWith("ownership-")) {
    const body = await renderNamespaceOwnership(decodeURIComponent(id.slice(10)), modal);
    return modal ? clusterDialog("命名空间归属", body, false) : `<h2>命名空间归属</h2><a href="${route}">返回集群接入</a>${body}`;
  }
  const response = await fetch(endpoint);
  if (!response.ok || !response.headers.get("Content-Type")?.includes("application/json")) {
    throw new Error("directory unavailable");
  }
  const items = await response.json() as Entry[];
  if (!id) {
    const states = [...new Set(items.map(collectionStatus))];
    return `<div class="catalog-section">
      <div class="cluster-heading"><div><h2>集群接入</h2><p>查看接入状态、采集详情和命名空间归属。</p></div><div class="catalog-actions">
        <button type="button" class="button secondary" data-cluster-editor="push">采集上报</button><button type="button" class="button" data-cluster-editor="pull">＋ 新增集群</button>
      </div></div>
      <div class="cluster-filters">
        <label>搜索集群<input type="search" data-cluster-filter="query" placeholder="名称、ID、环境或业务线"></label>
        <label>采集状态<select data-cluster-filter="status"><option value="">全部状态</option>
          ${states.map((state) => `<option>${escapeHtml(state)}</option>`).join("")}</select></label>
      </div>
      <div class="catalog-table-wrap"><table class="catalog-table">
        <thead><tr><th scope="col">集群</th><th scope="col">环境 / 业务线</th><th scope="col">集群类型</th><th scope="col">采集状态</th><th scope="col">操作</th></tr></thead>
        <tbody>${items.map((item) => `<tr data-cluster-row data-status="${escapeHtml(collectionStatus(item))}">
          <td><strong>${escapeHtml(item.group || item.name)}</strong> <code>${escapeHtml(item.id)}</code>
            <div>${item.mode === "local" ? "本地自动采集" : item.mode === "pull" ? "外部直连" : "采集上报"}</div></td>
          <td>${escapeHtml([item.environment, item.business].filter(Boolean).join(" / ") || "待补齐")}</td>
          <td>${item.clusterType === "dedicated" ? "独立业务集群" : "共用集群"}</td>
          <td><span class="cluster-status">${collectionStatus(item)}</span></td>
          <td><div class="catalog-actions">
          ${item.deleted ? maintenanceButton(item, "reconnect", "重新连接") : `
            <button type="button" class="button secondary" data-cluster-details>采集详情</button>
            <template data-collection-template><p>${escapeHtml(item.group || item.name)} · ${collectionStatus(item)}</p>${renderCollection(item)}</template>
            ${editorButton(item, "edit", item.mode === "local" ? "编辑归属" : "编辑配置")}
            ${item.enabled ? editorButton(item, "ownership", "命名空间归属") : ""}
            ${item.enabled && item.mode === "pull" ? maintenanceButton(item, "recheck", "重新校验") : ""}
            ${item.mode === "push" ? maintenanceButton(item, "rotate-token", "轮换上报凭据") : ""}
            ${item.mode === "pull" ? editorButton(item, "rotate", "轮换直连凭据") : ""}
            ${maintenanceButton(item, "delete", "删除")}`}
          </div></td></tr>`).join("")}
          <tr data-cluster-empty${items.length ? " hidden" : ""}><td colspan="5" class="catalog-empty">暂无符合条件的集群。</td></tr></tbody>
      </table></div>
      <div class="catalog-bottom"><span data-cluster-count>共 ${items.length} 个集群</span></div>
      <dialog class="catalog-dialog" data-cluster-dialog aria-labelledby="cluster-dialog-title"></dialog>
    </div>`;
  }
  const rotating = id.startsWith("rotate-");
  const editing = rotating || id.startsWith("edit-");
  const item = editing ? items.find((entry) => entry.id === decodeURIComponent(id.slice(rotating ? 7 : 5))) : undefined;
  const mode = item?.mode ?? id;
  if ((editing && (!item || item.deleted)) || (rotating && mode !== "pull") || (mode === "local" && !editing) || (mode !== "pull" && mode !== "push" && mode !== "local")) {
    return modal ? clusterDialog("集群接入", "<p>实例不存在。</p>") : `<p>实例不存在。</p><a href="${route}">返回集群接入</a>`;
  }
  const catalogs = await Promise.all(["environments", "business-lines"].map(async (kind) => {
    const response = await fetch(`/api/v1/admin/${kind}`);
    if (!response.ok) throw new Error("resource catalog unavailable");
    return await response.json() as { name: string; id: string }[];
  }));
  const locked = Boolean(item?.firstCollectedAt && item.environment);
  const local = mode === "local";
  const title = rotating ? "轮换直连凭据" : local ? "编辑归属" : editing ? "编辑配置" : mode === "pull" ? "新增集群" : "采集上报";
  const body = `<form class="login-form identity-form cluster-editor" data-onboarding-mode="${mode}" data-editing="${editing}" data-rotating="${rotating}">
      <p>${local ? "本地自动采集" : mode === "pull" ? "外部直连" : "采集上报"}</p>
      <fieldset><legend>名称与归属</legend><div class="cluster-form-grid">
      ${fields.filter(([, , base]) => base).map(([name,label,base]) => {
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
      ${local ? "" : `<label>集群类型<select name="clusterType">
        <option value="shared">共用集群</option>
        <option value="dedicated"${item?.clusterType === "dedicated" ? " selected" : ""}>独立业务集群</option>
      </select></label>`}</div></fieldset>
      ${local ? "" : `<fieldset><legend>连接与观测配置</legend><div class="cluster-form-grid">
        ${fields.filter(([key, , base]) => !base && (mode === "pull" || !["apiURL", "engineNamespace", "engineName"].includes(key)))
          .map(([key, label]) => `<label>${label}<input name="${key}" value="${escapeHtml(item?.[key])}"
          ${["apiURL", "engineNamespace", "engineName"].includes(key) ? "required" : ""}></label>`).join("")}
      </div></fieldset>`}
      ${mode === "pull" ? credentialFields(item?.credentialKind, rotating) : ""}
      ${mode === "pull" ? `<label><span><input type="checkbox" name="aiEnabled"${item?.aiEnabled === true ? " checked" : ""} style="width:auto"> 启用 AI 复核（读取目标 Engine Secret）</span></label>` : ""}
      ${local ? "" : `<label><span><input type="checkbox" name="enabled"${item?.enabled !== false ? " checked" : ""} style="width:auto"> 启用实例</span></label>`}
      <p>${local ? "本地采集使用部署内的服务账号。" : mode === "pull" ? "部署挂载方式使用实例 ID 对应的 Token 和 CA。轮换直连凭据须校验节点读取通过后才会切换。" :
        "保存后会显示一次上报凭据。采集端提交快照后显示在线，收到真实诊断后锁定环境。"} </p>
      <p role="status"></p>
      <textarea rows="3" readonly hidden aria-label="上报凭据" data-report-token></textarea>
      <div class="cluster-editor-actions">
        ${modal ? '<button type="button" class="button secondary" data-cluster-close>取消</button>' : ""}
        ${mode === "pull" && !rotating ? '<button type="submit" value="test" class="button secondary">测试连接</button>' : ""}
        <button type="submit" class="button">${rotating ? "校验并轮换" : local ? "保存归属" : "保存"}</button>
      </div>
    </form>`;
  if (modal) return clusterDialog(title, body, false);
  return `<h2>${title}</h2><a href="${route}">返回集群接入</a>${body}

    ${item ? `<section><h3>${collectionStatus(item)}</h3>${renderCollection(item)}</section>` : ""}
    ${item?.mode === "pull" && item.enabled ? `<form data-collection-check="${escapeHtml(item.id)}">
      <button type="submit" class="button secondary">重新校验</button><p role="status"></p></form>` : ""}`;
}

function credentialFields(kind: string | undefined, rotating: boolean): string {
  const current = kind === "token" ? "ServiceAccount Token" : kind === "certificate" ? "客户端证书" : kind === "kubeconfig" ? "kubeconfig" : "部署挂载 Token 和 CA";
  const text = (label: string, name: string) => `<label data-credential-piece="${name}" hidden>${label}
    <textarea name="credential${name}" rows="4" autocomplete="off" disabled></textarea></label>`;
  return `<fieldset><legend>直连凭据</legend>
    <label>凭据方式<select name="credentialKind" data-cluster-credential>
      <option value="">${rotating ? "请选择新凭据方式" : `沿用${current}`}</option>
      <option value="kubeconfig">kubeconfig</option><option value="token">ServiceAccount Token</option>
      <option value="certificate">客户端证书与私钥</option></select></label>
    <div class="cluster-form-grid">${text("kubeconfig（内嵌 CA 与静态凭据）", "Kubeconfig")}
      <label data-credential-piece="Token" hidden>只读 Token<input type="password" name="credentialToken" autocomplete="off" disabled></label>
      ${text("Kubernetes CA（PEM）", "CA")}${text("客户端证书（PEM）", "Certificate")}${text("客户端私钥（PEM）", "PrivateKey")}</div>
  </fieldset>`;
}

export function updateClusterCredential(event: Event): void {
  const select = event.target;
  if (!(select instanceof HTMLSelectElement) || !select.hasAttribute("data-cluster-credential") || !select.form) return;
  const visible = select.value === "kubeconfig" ? ["Kubeconfig"] : select.value === "token" ? ["CA", "Token"] :
    select.value === "certificate" ? ["CA", "Certificate", "PrivateKey"] : [];
  for (const piece of select.form.querySelectorAll<HTMLElement>("[data-credential-piece]")) {
    const field = piece.querySelector<HTMLInputElement | HTMLTextAreaElement>("input, textarea");
    if (!field) continue;
    piece.hidden = !visible.includes(piece.dataset.credentialPiece ?? "");
    field.disabled = piece.hidden;
    field.required = !piece.hidden;
  }
  const url = select.form.querySelector<HTMLInputElement>('[name="apiURL"]');
  if (url) {
    url.disabled = select.value === "kubeconfig";
    url.required = !url.disabled;
  }
}

export async function submitOnboarding(form: HTMLFormElement, testing: boolean, reload: () => void): Promise<void> {
  const status = form.querySelector<HTMLElement>('[role="status"]');
  const token = form.querySelector<HTMLTextAreaElement>("[data-report-token]");
  const buttons = [...form.querySelectorAll<HTMLButtonElement>('button[type="submit"]')];
  if (!status || !token || buttons.some((button) => button.disabled)) return;
  const dialog = form.closest("dialog");
  const data = new FormData(form);
  const values = Object.fromEntries(fields.map(([key]) => [key, String(data.get(key) ?? "").trim()]));
  const credentialKind = data.get("credentialKind");
  if (form.dataset.rotating === "true" && !credentialKind) {
    status.textContent = "请选择要提交的新凭据方式。";
    return;
  }
  const credential = credentialKind ? { kind: credentialKind, token: data.get("credentialToken") ?? "",
    ca: data.get("credentialCA") ?? "", certificate: data.get("credentialCertificate") ?? "",
    privateKey: data.get("credentialPrivateKey") ?? "", kubeconfig: data.get("credentialKubeconfig") ?? "" } : undefined;
  for (const button of buttons) button.disabled = true;
  dialog?.setAttribute("aria-busy", "true");
  status.textContent = testing ? "正在测试连接…" : "正在保存…";
  let keepDisabled = false;
  try {
    const response = await fetch(endpoint + (testing ? "?test=true" : ""), {
      method: form.dataset.editing === "true" ? "PUT" : "POST",
      headers: { "Content-Type": "application/json" },
      body: JSON.stringify({ ...values, credential, clusterType: data.get("clusterType"), mode: form.dataset.onboardingMode, enabled: data.has("enabled"), aiEnabled: data.has("aiEnabled") }),
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
      const close = form.querySelector<HTMLButtonElement>("[data-cluster-close]");
      if (close) {
        close.textContent = "完成";
      }
      if (dialog) dialog.onclose = () => { if (form.isConnected && ["#/resources", route].includes(window.location.hash)) reload(); };
    } else if (dialog) {
      dialog.close();
      if (form.isConnected && ["#/resources", route].includes(window.location.hash)) reload();
    } else {
      window.location.hash = route;
    }
  } catch {
    status.textContent = "集群接入请求失败。";
  } finally {
    dialog?.removeAttribute("aria-busy");
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

function maintenanceButton(item: Entry, action: string, label: string): string {
  return `<button type="button" class="button secondary${action === "delete" ? " catalog-danger" : ""}"
    data-cluster-action="${action}" data-id="${escapeHtml(item.id)}" data-label="${escapeHtml(item.group || item.name)}">${label}</button>`;
}

export function filterClusterRows(event: Event): void {
  const target = event.target;
  if (!(target instanceof HTMLElement) || !target.matches("[data-cluster-filter]")) return;
  const section = target.closest(".catalog-section");
  if (!section) return;
  const search = section.querySelector<HTMLInputElement>('[data-cluster-filter="query"]')?.value.trim().toLowerCase() ?? "";
  const state = section.querySelector<HTMLSelectElement>('[data-cluster-filter="status"]')?.value ?? "";
  let visible = 0;
  for (const row of section.querySelectorAll<HTMLTableRowElement>("[data-cluster-row]")) {
    row.hidden = Boolean((state && state !== row.dataset.status) || !row.textContent?.toLowerCase().includes(search));
    if (!row.hidden) visible++;
  }
  const empty = section.querySelector<HTMLTableRowElement>("[data-cluster-empty]");
  if (empty) empty.hidden = visible > 0;
  const count = section.querySelector("[data-cluster-count]");
  if (count) count.textContent = `共 ${visible} 个集群`;
}

const maintenance: Record<string, { title: string; description: string }> = {
  delete: { title: "删除集群连接", description: "停止采集并清理 kube-aiops 的采集快照和命名空间归属。此操作不会修改 Kubernetes 集群。已发布的知识库文档独立保留。" },
  reconnect: { title: "重新连接本地集群", description: "沿用删除前的环境、业务线和集群名称，开始新一轮采集。" },
  "rotate-token": { title: "轮换上报凭据", description: "生成新凭据并使原凭据立即失效。新凭据只显示一次，请同步更新采集端 Secret。" },
  recheck: { title: "重新校验", description: "使用当前连接配置检查节点读取权限，并更新最近校验结果。" },
};

function editorButton(item: Entry, action: string, label: string): string {
  return `<button type="button" class="button secondary" data-cluster-editor="${action}-${encodeURIComponent(item.id)}">${label}</button>`;
}

function clusterDialog(title: string, body: string, showClose = true): string {
  return `<header><h2 id="cluster-dialog-title" tabindex="-1">${title}</h2></header>
    <div class="catalog-dialog-body">${body}</div>${showClose ? '<footer><button type="button" class="button secondary" data-cluster-close>关闭</button></footer>' : ""}`;
}

export async function handleClusterMaintenanceClick(event: MouseEvent): Promise<void> {
  const target = event.target;
  if (!(target instanceof HTMLElement)) return;
  const section = target.closest(".catalog-section");
  const dialog = section?.querySelector<HTMLDialogElement>("[data-cluster-dialog]");
  if (!dialog) return;
  if (target.closest("[data-cluster-close]")) {
    if (!dialog.hasAttribute("aria-busy")) dialog.close();
    return;
  }
  if (dialog.open) return;
  dialog.onclose = null;
  dialog.oncancel = (e) => { if (dialog.hasAttribute("aria-busy")) e.preventDefault(); };
  const details = target.closest("[data-cluster-details]")?.parentElement?.querySelector<HTMLTemplateElement>("[data-collection-template]");
  if (details) {
    dialog.classList.add("cluster-dialog-wide");
    dialog.innerHTML = clusterDialog("采集详情", details.innerHTML);
    dialog.showModal();
    return;
  }
  const editor = target.closest<HTMLButtonElement>("[data-cluster-editor]");
  if (editor) {
    dialog.classList.add("cluster-dialog-wide");
    dialog.innerHTML = clusterDialog(escapeHtml(editor.textContent ?? "集群接入"), '<p role="status">正在加载…</p>');
    dialog.showModal();
    const loading = dialog.firstElementChild;
    try {
      const body = await renderOnboarding(editor.dataset.clusterEditor, true);
      if (dialog.open && dialog.firstElementChild === loading) {
        dialog.innerHTML = body;
        dialog.querySelector<HTMLElement>("h2")?.focus();
      }
    } catch {
      if (dialog.open && dialog.firstElementChild === loading) {
        dialog.innerHTML = clusterDialog("集群接入", '<p role="alert">无法加载集群配置，请重试。</p>');
      }
    }
    return;
  }
  dialog.classList.remove("cluster-dialog-wide");
  const button = target.closest<HTMLButtonElement>("[data-cluster-action]");
  const action = button?.dataset.clusterAction ?? "";
  const content = maintenance[action];
  if (!button || !content) return;
  dialog.innerHTML = `<form data-cluster-maintenance="${action}" data-id="${escapeHtml(button.dataset.id)}">
    <header><h2 id="cluster-dialog-title">${content.title}</h2></header>
    <div class="catalog-dialog-body"><p>${escapeHtml(button.dataset.label)}</p><p>${content.description}</p>
      <p role="status" aria-live="polite"></p>
      <textarea aria-label="新上报凭据" readonly hidden data-cluster-token></textarea></div>
    <footer><button type="button" class="button secondary" data-cluster-close>取消</button>
      <button type="submit" class="button${action === "delete" ? " catalog-danger-fill" : ""}">确认</button></footer></form>`;
  dialog.showModal();
}

export async function submitClusterMaintenance(form: HTMLFormElement, reload: () => void): Promise<void> {
  const dialog = form.closest("dialog");
  const submit = form.querySelector<HTMLButtonElement>('button[type="submit"]');
  const status = form.querySelector('[role="status"]');
  if (!dialog || !status || !submit || submit.disabled) return;
  const action = form.dataset.clusterMaintenance ?? "";
  const search = new URLSearchParams({ id: form.dataset.id ?? "", action });
  submit.disabled = true;
  dialog.setAttribute("aria-busy", "true");
  status.textContent = "正在处理…";
  try {
    const response = await fetch(`${endpoint}?${search}`, { method: action === "delete" ? "DELETE" : "POST" });
    if (!response.ok) {
      status.textContent = response.headers.get("Content-Type")?.includes("text/plain") ? (await response.text()).trim() : "操作失败，请重试。";
      return;
    }
    if (action === "rotate-token") {
      const payload = await response.json() as { token?: string };
      const field = form.querySelector<HTMLTextAreaElement>("[data-cluster-token]");
      if (!field || !payload.token) throw new Error("missing replacement credential");
      field.value = payload.token;
      field.hidden = false;
      status.textContent = "轮换成功。新凭据只显示一次。";
      const close = form.querySelector("[data-cluster-close]");
      if (close) close.textContent = "关闭";
      submit.hidden = true;
    } else {
      dialog.close();
      reload();
    }
  } catch {
    status.textContent = "请求失败，请检查连接。";
  } finally {
    submit.disabled = submit.hidden;
    dialog.removeAttribute("aria-busy");
  }
}
