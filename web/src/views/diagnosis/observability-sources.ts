import { apiClient } from "../../api/client.ts";
import { ApiError, type ObservabilityInput, type ObservabilitySource, type ObservabilityDashboard, type ListObservabilityDashboardsParams } from "../../api/generated.ts";
import { escapeHtml as h } from "../../components/html.ts";
import { sourceError } from "../resources/datasource-errors.ts";
import { bindDirtyDialog } from "../resources/resource-workspace.ts";

export const observabilityNames = { prometheus: "Prometheus", victorialogs: "VictoriaLogs", alertmanager: "Alertmanager", grafana: "Grafana" };
type Kind = keyof typeof observabilityNames;
export const observabilityDescriptions: Record<Kind, string> = {
  prometheus: "指标证据", victorialogs: "日志证据", alertmanager: "告警同步与诊断证据", grafana: "诊断仪表盘",
};
const options = (items: { id: string; name: string }[], placeholder: string) =>
  `<option value="">${placeholder}</option>${items.map(item => `<option value="${h(item.id)}">${h(item.name)}</option>`).join("")}`;
const value = (form: HTMLFormElement, key: string) => String(new FormData(form).get(key) ?? "").trim();

function renderForm(kind: Kind, row?: ObservabilitySource): string {
  return `<form novalidate>
    <header class="zabbix-dialog-head"><div><h2 id="obs-dialog-title">${row ? "编辑" : "接入"} ${observabilityNames[kind]}</h2><p>${observabilityDescriptions[kind]}</p></div>
      <button type="button" class="zabbix-close" aria-label="关闭" data-obs-close>×</button></header>
    <div class="zabbix-setup-grid zabbix-setup-columns">
      <section data-obs-connection><h3>连接</h3>
        <label>名称<input name="name" maxlength="120" value="${h(row?.name)}"></label>
        <label>服务地址<input type="url" name="endpoint" maxlength="512" placeholder="https://observability.example.com/${kind}" value="${h(row?.endpoint)}"></label>
        <p class="zabbix-hint">填写 HTTPS 基地址，保留反向代理使用的路径前缀。</p>
        <label>认证方式<select name="auth">
          <option value="bearer">${kind === "grafana" ? "Service Account Token" : "Bearer Token"}</option>
          ${kind === "grafana" ? "" : '<option value="basic">Basic 认证</option><option value="none">免认证（内网只读）</option>'}
        </select></label>
        <label data-obs-user hidden>用户名<input name="username" autocomplete="off"></label>
        <label data-obs-secret>Token 或密码<input name="secret" type="password" autocomplete="new-password" placeholder="${row ? "留空沿用原凭据" : "输入只读凭据"}"></label>
        <p role="status" data-obs-test-status>先选择授权的环境、业务和集群。</p>
      </section>
      <section data-obs-scope><h3>采集范围</h3>
        <label>环境<select name="environment"></select></label>
        <label>业务<select name="business"></select></label>
        <label>集群实例<select name="cluster"></select></label>
        ${kind === "grafana" ? '<div data-obs-dashboards>连接后发现可见仪表盘。</div>' : `<label>端点范围<select name="mapping"><option value="dedicated">此集群专用</option><option value="shared">多集群共享</option></select></label>
          <div class="incident-form-grid" data-obs-mapping hidden>
            <label>上游${kind === "victorialogs" ? "字段" : "标签"}<input name="label" placeholder="cluster" value="${h(row?.label)}"></label>
            <label>精确值<input name="value" placeholder="prod-a" value="${h(row?.value)}"></label>
          </div>`}
        ${kind === "victorialogs" ? `<div class="incident-form-grid">
          <label>AccountID<input type="number" name="account" min="0" max="4294967295" value="${h(row?.tenant?.account)}"></label>
          <label>ProjectID<input type="number" name="project" min="0" max="4294967295" value="${h(row?.tenant?.project)}"></label>
          </div>` : ""}
        ${kind === "alertmanager" ? `<p class="zabbix-hint">API v2 只读轮询。消失的告警标记为已离开列表。</p>
          <label><span><input type="checkbox" name="suppressed"${row?.includeSuppressed ? " checked" : ""}> 包含静默或抑制</span></label>` : ""}
        <div class="zabbix-preview" role="status" data-obs-preview-result></div>
      </section>
    </div>
    <footer class="zabbix-dialog-footer"><p role="status" data-obs-save-status></p>
      <div class="zabbix-dialog-actions"><button type="button" class="button secondary" data-obs-close>取消</button>
        <button type="button" class="button secondary" data-obs-test>测试连接</button>
        <button type="button" class="button secondary" data-obs-preview disabled>预览范围</button>
        <button type="submit" class="button" data-obs-save disabled>${row ? "保存" : "保存并启用"}</button>
      </div></footer>
  </form>`;
}

export function bindObservabilityEditor(dialog: HTMLDialogElement, onSaved: (source: ObservabilitySource) => void, onConflict: () => void) {
  let activeKind: Kind;
  let editing: ObservabilitySource | undefined;
  let renderVersion = 0;
  let tested = false;
  let ready = false;
  let busy = false;
  const guard = bindDirtyDialog(dialog, "[data-obs-close]", () => busy);

  function open(kind: Kind, row?: ObservabilitySource) {
    guard.clean();
    activeKind = kind;
    editing = row;
    tested = ready = busy = false;
    const version = ++renderVersion;
    dialog.innerHTML = renderForm(kind, row);
    dialog.showModal();
    const form = dialog.querySelector("form");
    if (!form) return;
    if (row) {
      (form.elements.namedItem("auth") as HTMLSelectElement).value = row.auth;
      const mapping = form.elements.namedItem("mapping") as HTMLSelectElement | null;
      if (mapping) mapping.value = row.shared ? "shared" : "dedicated";
    }
    updateUI(form);
    void Promise.all([apiClient.listZabbixScopes(), apiClient.listClusters()]).then(([scopes, clusters]) => {
      if (!dialog.open || renderVersion !== version) return;
      const env = form.elements.namedItem("environment") as HTMLSelectElement;
      const business = form.elements.namedItem("business") as HTMLSelectElement;
      const cluster = form.elements.namedItem("cluster") as HTMLSelectElement;
      env.innerHTML = options(scopes.environments, "选择环境");
      const updateScope = () => {
        const selected = scopes.environments.find(item => item.id === env.value);
        business.innerHTML = options(selected?.businesses ?? [], "选择业务");
        cluster.innerHTML = options(clusters.items.filter(item => item.environment === env.value || item.environment === selected?.name), "选择集群实例");
      };
      env.value = row?.scope.environmentId ?? "";
      updateScope();
      env.addEventListener("input", updateScope);
      if (row) {
        business.value = row.scope.businessId;
        cluster.value = row.scope.clusterId;
        // Keep the saved binding in FormData while scope changes are locked.
        for (const control of [env, business, cluster]) control.querySelectorAll("option").forEach(option => { option.disabled = !option.selected; });
      }
    }, error => {
      if (renderVersion === version && dialog.open) set(form, "[data-obs-test-status]", sourceError(error));
    });
  }

  function updateUI(form: HTMLFormElement) {
    form.querySelector("[data-obs-user]")?.toggleAttribute("hidden", value(form,"auth") !== "basic");
    form.querySelector("[data-obs-secret]")?.toggleAttribute("hidden", value(form,"auth") === "none");
    form.querySelector("[data-obs-mapping]")?.toggleAttribute("hidden", value(form,"mapping") !== "shared");
    form.querySelector("[data-obs-preview]")?.toggleAttribute("disabled", !tested || busy);
    form.querySelector("[data-obs-preview]")?.toggleAttribute("hidden", !tested);
    form.querySelector("[data-obs-save]")?.toggleAttribute("disabled", !ready || busy);
    form.querySelector("[data-obs-test]")?.toggleAttribute("disabled", busy);
  }

  function input(form: HTMLFormElement, preview: boolean): ObservabilityInput {
    const auth = value(form, "auth") as ObservabilitySource["auth"];
    const shared = value(form,"mapping") === "shared";
    const account = value(form,"account");
    const project = value(form,"project");
    const secret = String(new FormData(form).get("secret") ?? "");
    return {
      kind: activeKind, name: value(form,"name"), endpoint: value(form,"endpoint"),
      scope: { environmentId: value(form,"environment"), businessId: value(form,"business"), clusterId: value(form,"cluster") },
      shared, label: shared ? value(form,"label") : undefined, value: shared ? value(form,"value") : undefined,
      tenant: account || project ? { account: Number(account), project: Number(project) } : undefined,
      includeSuppressed: Boolean(new FormData(form).get("suppressed")),
      dashboardIds: preview && activeKind === "grafana" ? new FormData(form).getAll("dashboard").map(String) : undefined,
      sourceId: editing?.id,
      credential: auth === "none" || secret || !editing || editing.auth !== auth ? { method: auth,
        username: auth === "basic" ? value(form, "username") : undefined, secret: auth === "none" ? undefined : secret } : undefined,
      preview,
    };
  }

  function set(form: HTMLFormElement, selector: string, text: string) {
    const target = form.querySelector(selector);
    if (target) target.textContent = text;
  }

  async function probe(form: HTMLFormElement, preview: boolean) {
    const target = preview ? "[data-obs-preview-result]" : "[data-obs-test-status]";
    const version = renderVersion;
    ready = false;
    if (!preview) tested = false;
    if (!form.checkValidity()) { form.reportValidity(); return; }
    const request = input(form, preview);
    if (!request.name || !request.endpoint || Object.values(request.scope).some(item => !item)) {
      set(form, target, "请填写名称、地址和完整授权范围。");
      updateUI(form);
      return;
    }
    busy = true;
    const body = form.querySelector<HTMLElement>(".zabbix-setup-grid");
    if (body) body.inert = true;
    updateUI(form);
    set(form, target, preview ? "正在预览…" : "正在测试上游…");
    try {
      const result = await apiClient.probeObservabilitySource({ body: request });
      if (version !== renderVersion) return;
      if (!preview) {
        tested = true;
        set(form, target, "连接成功 · 可预览已选择范围。");
        if (activeKind === "grafana") paintDashboards(form, result.dashboards ?? [], editing?.dashboards ?? []);
      } else {
        ready = true;
        const metric = activeKind === "prometheus" ? "时序" : activeKind === "victorialogs" ? "日志" :
          activeKind === "grafana" ? "仪表盘" : "活动告警";
        const alerts = result.alerts?.map(item => item.title).join("、");
        set(form, target, `${result.count} 条${metric}${result.suppressed ? ` · ${result.suppressed} 条非活动告警` : ""}${alerts ? ` · ${alerts}` : ""}`);
      }
    } catch (error) {
      if (version === renderVersion) set(form, target, sourceError(error));
    } finally {
      if (body) body.inert = false;
      if (version === renderVersion) { busy = false; updateUI(form); }
    }
  }

  dialog.addEventListener("click", async event => {
    if (!(event.target instanceof Element)) return;
    const button = event.target.closest<HTMLButtonElement>("button");
    if (!button?.form) return;
    if (button.hasAttribute("data-obs-test")) await probe(button.form, false);
    if (button.hasAttribute("data-obs-preview")) await probe(button.form, true);
  });

  dialog.addEventListener("input", event => {
    const control = event.target;
    if (!(control instanceof HTMLInputElement || control instanceof HTMLSelectElement) || !control.form) return;
    if (busy) return;
    ready = false;
    if (control.name !== "dashboard") tested = false;
    set(control.form, "[data-obs-preview-result]", "");
    if (!tested) set(control.form, "[data-obs-test-status]", "配置已变更，请测试连接。");
    updateUI(control.form);
  });
  dialog.addEventListener("close", () => { ++renderVersion; dialog.replaceChildren(); });
  dialog.addEventListener("submit", async event => {
    event.preventDefault();
    if (!(event.target instanceof HTMLFormElement) || !ready || busy) return;
    const form = event.target;
    const request = input(form, true);
    busy = true;
    const body = form.querySelector(".zabbix-setup-grid");
    body?.setAttribute("inert", "");
    updateUI(form);
    try {
      const updated = editing ? await apiClient.updateObservabilitySource({ sourceId: editing.id, "If-Match": `"${editing.version}"`, body: request })
        : await apiClient.createObservabilitySource({ body: request });
      busy = false;
      guard.clean();
      dialog.close();
      onSaved(updated);
    } catch (error) {
      body?.removeAttribute("inert");
      const conflict = error instanceof ApiError && error.status === 412;
      set(form, "[data-obs-save-status]", conflict ? "配置已更新，请关闭窗口后核对最新配置。" : sourceError(error));
      if (conflict) onConflict();
      busy = false;
      updateUI(form);
    }
  });
  return open;
}

function paintDashboards(form: HTMLFormElement, dashboards: ObservabilityDashboard[], existing: ObservabilityDashboard[]) {
  const node = form.querySelector("[data-obs-dashboards]");
  if (node) node.innerHTML = dashboards.length ? dashboards.map(row => `<label><span><input type="checkbox" name="dashboard"
    value="${h(row.uid)}"${existing.some(item => item.uid === row.uid) ? " checked" : ""}> ${h(row.title)}</span></label>`).join("") : "暂无授权仪表盘。";
}

export async function renderObservabilityDashboardLinks(params: ListObservabilityDashboardsParams): Promise<string> {
  const result = await apiClient.listObservabilityDashboards(params).catch(() => undefined);
  const links = result?.items.filter(item => item.url?.startsWith("https://")) ?? [];
  return links.length ? `<section class="panel observability-dashboards"><h2>Grafana 仪表盘</h2>
    <ul>${links.map(item => `<li><a target="_blank" rel="noopener noreferrer" href="${h(item.url)}">${h(item.title)}</a></li>`).join("")}</ul></section>` : "";
}
