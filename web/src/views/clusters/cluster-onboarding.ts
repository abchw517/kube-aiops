import { renderNamespaceOwnership } from "./namespace-ownership.ts";
import { escapeHtml } from "../../components/html.ts";

import { type ClusterEntry as Entry, clusterOnboardingEndpoint as endpoint, collectionStatus, renderCollection, renderCollectionRecheck } from "./cluster-records.ts";
import { renderClusterDetail, renderClusterWorkspace, refreshClusterWorkspace } from "./cluster-workspace.ts";
import { bindDirtyDialog, restoreResourceFocus } from "../resources/resource-workspace.ts";
const route = "#/resources/clusters";
import { clusterManagementTitle } from "../../domain/resource-routes.ts";
const fields = [
  ["id", "实例 ID", true], ["name", "实例名称", true],
  ["environment", "环境", true], ["business", "业务线", true], ["group", "集群名称", true],
  ["apiURL", "Kubernetes API 地址", false], ["engineNamespace", "Engine 命名空间", false],
  ["engineName", "Engine 实例名", false], ["prometheusURL", "Prometheus 地址", false],
  ["victoriaLogsURL", "VictoriaLogs 地址", false], ["alertmanagerURL", "Alertmanager 地址", false],
] as const;

export async function renderOnboarding(id?: string, modal = false, editable = true): Promise<string> {
  if (id && !editable) return '<p role="alert">当前账号只能查看集群接入状态。</p>';
  if (id?.startsWith("ownership-")) {
    const body = await renderNamespaceOwnership(decodeURIComponent(id.slice(10)), modal);
    return modal ? clusterDialog("命名空间归属", body, false) : `<h2>命名空间归属</h2><a href="${route}">返回${clusterManagementTitle}</a>${body}`;
  }
  const items = await readClusterDirectory();
  if (!id) return renderClusterWorkspace(items, editable);
  const rotating = id.startsWith("rotate-");
  const editing = rotating || id.startsWith("edit-");
  const item = editing ? items.find((entry) => entry.id === decodeURIComponent(id.slice(rotating ? 7 : 5))) : undefined;
  const mode = item?.mode ?? id;
  if ((editing && (!item || item.deleted)) || (rotating && mode !== "pull") || (mode === "local" && !editing) || (mode !== "pull" && mode !== "push" && mode !== "local")) {
    return modal ? clusterDialog(clusterManagementTitle, "<p>实例不存在。</p>") : `<p>实例不存在。</p><a href="${route}">返回${clusterManagementTitle}</a>`;
  }
  const catalogs = await Promise.all(["environments", "business-lines"].map(async (kind) => {
    const response = await fetch(`/api/v1/admin/${kind}`);
    if (!response.ok) throw new Error("resource catalog unavailable");
    return await response.json() as { name: string; id: string }[];
  }));
  const locked = Boolean(item?.firstCollectedAt && item.environment);
  const local = mode === "local";
  const startsWithKubeconfig = mode === "pull" && !item?.credentialKind && !rotating;
  const title = rotating ? "轮换直连凭据" : local ? "编辑归属" : editing && startsWithKubeconfig ? "补录直连凭据"
    : editing ? "编辑集群配置" : mode === "pull" ? "新增外部直连集群" : "采集上报";
  const start = rotating || (editing && startsWithKubeconfig) ? "connect" : "basic";
  const section = (step: string) => `class="cluster-editor-pane" data-editor-pane="${step}"${start === step ? "" : " hidden"}`;
  const configurationInput = (key: typeof fields[number][0]) => {
    const label = fields.find(([name]) => name === key)?.[1];
    return `<label>${label}<input name="${key}" value="${escapeHtml(item?.[key])}"
      ${key === "apiURL" && startsWithKubeconfig ? "disabled" : ["apiURL", "engineNamespace", "engineName"].includes(key) ? "required" : ""}></label>`;
  };
  const body = `<form novalidate class="login-form identity-form cluster-editor" data-onboarding-mode="${mode}" data-editing="${editing}" data-rotating="${rotating}">
      <div class="cluster-form-shell">
      ${local || rotating ? "" : `<nav class="cluster-editor-steps resource-view-tabs" aria-label="配置分区">
        <button type="button" data-editor-step="basic" aria-pressed="${start === "basic"}">基本信息</button>
        ${mode === "pull" ? `<button type="button" data-editor-step="connect" aria-pressed="${start === "connect"}">连接与凭据</button>` : ""}
        <button type="button" data-editor-step="observe" aria-pressed="false">观测与诊断</button>
      </nav>`}
      <div class="cluster-form-content">
      <section ${section("basic")}>
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
      ${local ? "" : `<label class="cluster-switch"><input type="checkbox" name="enabled"${item?.enabled !== false ? " checked" : ""}><span>启用实例</span></label>`}
      ${local ? "<p>本地采集使用部署内的服务账号。</p>" : mode === "push" ? "<p>保存后会显示一次上报凭据。采集端提交快照后显示在线，收到真实诊断后锁定环境。</p>" : ""}
      </section>
      ${mode === "pull" ? `<section ${section("connect")}>
        <fieldset><legend>Kubernetes API</legend>${configurationInput("apiURL")}
          <p data-api-url-hint>${startsWithKubeconfig ? "API 地址由 kubeconfig 提供。" : "测试连接验证 Kubernetes API 和节点读取权限。"}</p></fieldset>
        ${credentialFields(item?.credentialKind, rotating)}
        <p>直连凭据加密保存在 SQLite 中。轮换凭据通过节点读取校验后才会生效。</p>
      </section>` : ""}
      ${local ? "" : `<section ${section("observe")}>
        ${mode === "pull" ? `<fieldset><legend>诊断 Engine</legend><div class="cluster-form-grid">
          ${configurationInput("engineNamespace")}${configurationInput("engineName")}
        </div></fieldset>` : ""}
        <details class="cluster-observability"><summary>观测证据源</summary><div class="cluster-form-grid">
          ${configurationInput("prometheusURL")}${configurationInput("victoriaLogsURL")}${configurationInput("alertmanagerURL")}
        </div></details>
        ${mode === "pull" ? `<label class="cluster-switch"><input type="checkbox" name="aiEnabled"${item?.aiEnabled === true ? " checked" : ""}><span>启用 AI 复核（读取目标 Engine Secret）</span></label>` : ""}
      </section>`}
      <textarea rows="3" readonly hidden aria-label="上报凭据" data-report-token></textarea>
      </div></div>
      <footer class="cluster-form-footer"><p role="status"></p><div class="cluster-editor-actions">
        ${modal ? '<button type="button" class="button secondary" data-cluster-close>取消</button>' : ""}
        ${mode === "pull" && !rotating ? '<button type="submit" value="test" class="button secondary">测试连接</button>' : ""}
        <button type="submit" class="button">${rotating ? "校验并轮换" : local ? "保存归属" : "保存配置"}</button>
      </div></footer>
    </form>`;
  const summary = [item?.group || item?.name, item?.id, local ? "本地自动采集" : mode === "push" ? "采集上报" : "外部直连"].filter(Boolean).join(" · ");
  if (modal) return clusterDialog(title, body, false, summary, true);
  return `<h2>${title}</h2><a href="${route}">返回${clusterManagementTitle}</a>${body}

    ${item ? `<section><h3>${collectionStatus(item)}</h3>${renderCollection(item)}</section>` : ""}
    ${item ? renderCollectionRecheck(item) : ""}`;
}

function credentialFields(kind: string | undefined, rotating: boolean): string {
  const current = kind === "token" ? "ServiceAccount Token" : kind === "certificate" ? "客户端证书" : kind === "kubeconfig" ? "kubeconfig" : "";
  const defaultKubeconfig = !current && !rotating;
  const text = (label: string, name: string) => {
    const visible = defaultKubeconfig && name === "Kubeconfig";
    return `<label data-credential-piece="${name}"${visible ? "" : " hidden"}>${label}
      <textarea name="credential${name}" rows="4" autocomplete="off" ${visible ? "required" : "disabled"}></textarea></label>`;
  };
  return `<fieldset><legend>直连凭据</legend>
    <label>凭据方式<select name="credentialKind" data-cluster-credential${rotating ? " required" : ""}>
      ${rotating ? '<option value="" selected disabled>请选择新凭据方式</option>' : current ? `<option value="">沿用${current}</option>` : ""}
      <option value="kubeconfig"${defaultKubeconfig ? " selected" : ""}>kubeconfig</option><option value="token">ServiceAccount Token</option>
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
    field.value = "";
    piece.hidden = !visible.includes(piece.dataset.credentialPiece ?? "");
    field.disabled = piece.hidden;
    field.required = !piece.hidden;
  }
  const hint = select.form.querySelector("[data-api-url-hint]");
  if (hint) hint.textContent = select.value === "kubeconfig" ? "API 地址由 kubeconfig 提供。" : "测试连接验证 Kubernetes API 和节点读取权限。";
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
  const workspace = form.closest<HTMLElement>("[data-resource-workspace]");
  const invalid = form.querySelector<HTMLInputElement | HTMLSelectElement | HTMLTextAreaElement>("input:invalid, select:invalid, textarea:invalid");
  if (invalid) {
    const pane = invalid.closest<HTMLElement>("[data-editor-pane]")?.dataset.editorPane;
    if (pane) showEditorPane(form, pane);
    const details = invalid.closest("details");
    if (details) details.open = true;
    invalid.reportValidity();
    return;
  }
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
  const body = form.querySelector<HTMLElement>(".cluster-form-shell");
  if (body) body.inert = true;
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
      if (dialog) {
        dialogGuards.get(dialog)?.clean();
        dialog.addEventListener("close", () => void refreshAfterMutation(workspace, reload), { once: true });
      }
      for (const pane of form.querySelectorAll<HTMLElement>("[data-editor-pane], .cluster-editor-steps")) pane.hidden = true;
    } else if (dialog) {
      await refreshAfterMutation(workspace, reload);
      dialog.close();
    } else {
      window.location.hash = route;
    }
  } catch {
    status.textContent = "集群接入请求失败。";
  } finally {
    if (body) body.inert = false;
    if (keepDisabled) token.focus();
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
    const drawer = form.closest<HTMLDialogElement>("[data-cluster-drawer]");
    const workspace = drawer?.closest<HTMLElement>("[data-resource-workspace]");
    if (!drawer || !workspace) { reload(); return; }
    const items = await readClusterDirectory();
    if (!workspace.isConnected) return;
    refreshClusterWorkspace(workspace, items);
    if (!drawer.open || drawer.dataset.clusterId !== form.dataset.collectionCheck) return;
    const item = items.find(entry => entry.id === drawer.dataset.clusterId);
    if (!item) { drawer.close(); return; }
    drawer.innerHTML = renderClusterDetail(item, workspace.dataset.editable === "true");
    const next = drawer.querySelector<HTMLElement>("[data-collection-check] button, h2");
    next?.focus();
  } catch {
    status.textContent = "重新校验失败，请稍后重试。";
    button.disabled = false;
  }
}

const maintenance: Record<string, { title: string; description: string }> = {
  delete: { title: "删除集群连接", description: "停止采集并清理 kube-aiops 的采集快照和命名空间归属。此操作不会修改 Kubernetes 集群。已发布的知识库文档独立保留。" },
  reconnect: { title: "重新连接本地集群", description: "沿用删除前的环境、业务线和集群名称，开始新一轮采集。" },
  "rotate-token": { title: "轮换上报凭据", description: "生成新凭据并使原凭据立即失效。新凭据只显示一次，请同步更新采集端 Secret。" },
  recheck: { title: "重新校验", description: "使用当前连接配置检查节点读取权限，并更新最近校验结果。" },
};

async function readClusterDirectory(): Promise<Entry[]> {
  const response = await fetch(endpoint);
  if (!response.ok || !response.headers.get("Content-Type")?.includes("application/json")) throw new Error("directory unavailable");
  return await response.json() as Entry[];
}

function onClusterList(): boolean {
  return ["#/resources", route].includes(window.location.hash.split("?")[0] ?? "");
}

function showEditorPane(form: HTMLFormElement, pane: string): void {
  for (const node of form.querySelectorAll<HTMLElement>("[data-editor-pane]")) node.hidden = node.dataset.editorPane !== pane;
  for (const button of form.querySelectorAll("[data-editor-step]")) button.setAttribute("aria-pressed", String(button.getAttribute("data-editor-step") === pane));
}

function clusterDialog(title: string, body: string, showClose = true, summary = "", editor = false): string {
  return `<header class="resource-overlay-head"><div><h2 id="cluster-dialog-title" tabindex="-1">${title}</h2>${summary ? `<p>${escapeHtml(summary)}</p>` : ""}</div>
    <button type="button" class="zabbix-close" aria-label="关闭" data-cluster-close>×</button></header>
    <div class="catalog-dialog-body${editor ? " cluster-editor-container" : ""}">${body}</div>
    ${showClose ? '<footer><button type="button" class="button secondary" data-cluster-close>关闭</button></footer>' : ""}`;
}

const dialogGuards = new WeakMap<HTMLDialogElement, ReturnType<typeof bindDirtyDialog>>();
function guardDialog(dialog: HTMLDialogElement): void {
  if (!dialogGuards.has(dialog)) {
    dialogGuards.set(dialog, bindDirtyDialog(dialog, "[data-cluster-close]", () => dialog.hasAttribute("aria-busy")));
    dialog.addEventListener("close", () => {
      if (dialog.open) return;
      dialog.replaceChildren();
      dialogGuards.get(dialog)?.clean();
    });
  }
}

export async function handleClusterMaintenanceClick(event: MouseEvent): Promise<void> {
  const target = event.target;
  if (!(target instanceof HTMLElement)) return;
  const section = target.closest<HTMLElement>("[data-resource-workspace]");
  const dialog = section?.querySelector<HTMLDialogElement>("[data-cluster-dialog]");
  const editor = target.closest<HTMLButtonElement>("[data-cluster-editor]");
  const button = target.closest<HTMLButtonElement>("[data-cluster-action]");
  const step = target.closest<HTMLButtonElement>("[data-editor-step]");
  if (step?.form && step.dataset.editorStep) { showEditorPane(step.form, step.dataset.editorStep); return; }
  if (!dialog || !section || section.dataset.editable !== "true" || (!editor && !button)) return;
  const adding = editor?.dataset.clusterEditor === "choose";
  const fromPicker = dialog.open && editor?.hasAttribute("data-from-chooser");
  if (dialog.open && !fromPicker) return;
  guardDialog(dialog);
  const menu = target.closest<HTMLDetailsElement>(".resource-row-menu");
  const origin = menu?.querySelector<HTMLElement>("summary") ?? editor ?? button;
  if (menu) menu.open = false;
  const cluster = target.closest<HTMLElement>("[data-cluster-row], [data-cluster-drawer]");
  const clusterId = cluster?.dataset.cluster || cluster?.dataset.clusterId;
  if (!fromPicker) dialog.onclose = () => restoreResourceFocus(origin,
    section.querySelector(`[data-cluster-row]:not([hidden]) [data-cluster-details="${CSS.escape(clusterId ?? "")}"]`) ??
    section.querySelector("[data-cluster-filter=query]"));
  if (adding) {
    dialog.classList.remove("cluster-dialog-wide");
    dialog.innerHTML = clusterDialog("接入集群", `<div class="resource-picker">
      <button type="button" class="button secondary" data-cluster-editor="pull" data-from-chooser><strong>外部直连</strong><small>平台可直接访问目标集群的 Kubernetes API。</small></button>
      <button type="button" class="button secondary" data-cluster-editor="push" data-from-chooser><strong>采集上报</strong><small>采集端主动向平台上报诊断快照。</small></button></div>
      <p class="muted">本地自动采集实例由平台发现。</p>`);
    dialog.showModal();
    return;
  }
  if (editor) {
    dialog.classList.add("cluster-dialog-wide");
    dialog.innerHTML = clusterDialog(escapeHtml(editor.textContent ?? clusterManagementTitle), '<p role="status">正在加载…</p>');
    if (!dialog.open) dialog.showModal();
    const loading = dialog.firstElementChild;
    try {
      const body = await renderOnboarding(editor.dataset.clusterEditor, true);
      if (dialog.open && dialog.firstElementChild === loading) {
        dialog.innerHTML = body;
        dialog.querySelector<HTMLElement>("h2")?.focus();
      }
    } catch {
      if (dialog.open && dialog.firstElementChild === loading) dialog.innerHTML = clusterDialog(clusterManagementTitle, '<p role="alert">无法加载集群配置，请重试。</p>');
    }
    return;
  }
  dialog.classList.remove("cluster-dialog-wide");
  const action = button?.dataset.clusterAction ?? "";
  const content = maintenance[action];
  if (!button || !content) return;
  dialog.innerHTML = `<form data-cluster-maintenance="${action}" data-id="${escapeHtml(button.dataset.id)}">
    <header><h2 id="cluster-dialog-title">${content.title}</h2></header>
    <div class="catalog-dialog-body"><p>${escapeHtml(button.dataset.label)} · <code>${escapeHtml(button.dataset.id)}</code></p><p>${content.description}</p>
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
      await refreshAfterMutation(form.closest<HTMLElement>("[data-resource-workspace]"), reload);
      dialog.close();
    }
  } catch {
    status.textContent = "请求失败，请检查连接。";
  } finally {
    submit.disabled = submit.hidden;
    dialog.removeAttribute("aria-busy");
  }
}

async function refreshAfterMutation(workspace: HTMLElement | null, reload: () => void): Promise<void> {
  if (!workspace?.isConnected || !onClusterList()) return;
  try {
    const items = await readClusterDirectory();
    if (!workspace.isConnected) return;
    refreshClusterWorkspace(workspace, items);
    const drawer = workspace.querySelector<HTMLDialogElement>("[data-cluster-drawer][open]");
    if (!drawer) return;
    const item = items.find(entry => entry.id === drawer.dataset.clusterId);
    if (item) drawer.innerHTML = renderClusterDetail(item, workspace.dataset.editable === "true");
    else drawer.close();
  } catch {
    if (workspace.isConnected) reload();
  }
}
