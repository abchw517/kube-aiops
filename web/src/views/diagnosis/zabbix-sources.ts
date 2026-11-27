import { apiClient } from "../../api/client.ts";
import { ApiError, type ZabbixCredential, type ZabbixGroup, type ZabbixScope, type ZabbixScopeChoice, type ZabbixSource } from "../../api/generated.ts";
import { escapeHtml as h } from "../../components/html.ts";
import { bindDirtyDialog } from "../resources/resource-workspace.ts";
import { diagnosticLevels } from "../../domain/unified-diagnostics.ts";

export function zabbixError(error: unknown): string {
  if (error instanceof ApiError) {
    if (error.status === 412) return "数据源配置已更新，请刷新后重试。";
    const body = error.body as { error?: { message?: string } } | null;
    if (body?.error?.message) return body.error.message;
  }
  return "暂时无法完成 Zabbix 操作，请重试。";
}

const scopeWaiting = '<p class="zabbix-scope-wait">测试连接成功后，可选择主机组并预览问题。</p>';

type Mode = "add" | "rotate" | "scope" | "edit";
type Wizard = {
  mode: Mode;
  source?: ZabbixSource;
  revision: number;
  previewRevision: number;
  draft?: string;
  groups: ZabbixGroup[];
  selected: Set<string>;
  scopes: ZabbixScopeChoice[];
  previewOK: boolean;
};
const value = (form: HTMLFormElement, name: string) => String(new FormData(form).get(name) ?? "");
const option = (id: string, name: string) => `<option value="${h(id)}">${h(name)}</option>`;

export function bindZabbixEditor(dialog: HTMLDialogElement, onSaved: (source: ZabbixSource, mode: Mode) => void, onConflict: () => void) {
  let current: Wizard | undefined;
  let saving = false;
  const guard = bindDirtyDialog(dialog, '[data-zabbix-action="close"]', () => saving, { confirmOnButtonClose: false });

  function open(mode: Mode, source?: ZabbixSource) {
    guard.clean();
    const wizard: Wizard = { mode, source, revision: 0, previewRevision: 0, groups: [],
      selected: new Set(source?.groups.map(group => group.groupid)), scopes: [], previewOK: false };
    current = wizard;
    dialog.dataset.mode = mode;
    if (mode === "edit" && source) {
      dialog.innerHTML = `<form data-zabbix-form>
        <header class="zabbix-dialog-head"><div><h2 id="zabbix-dialog-title">编辑 Zabbix 数据源</h2>
          <p>可修改显示名称。采集范围和认证信息在「调整主机组」与「更换凭据」中维护。</p></div>
          <button type="button" class="zabbix-close" aria-label="关闭" data-zabbix-action="close">×</button></header>
        <div class="zabbix-setup-grid"><section aria-label="基本信息">
          <label>名称<input name="sourceName" required maxlength="120" value="${h(source.name)}" autofocus></label>
          <p class="zabbix-hint">${h(source.endpoint)}</p>
        </section></div>
        <footer class="zabbix-dialog-footer"><p role="status" data-zabbix-save-result></p>
          <div class="zabbix-dialog-actions"><button type="button" class="button secondary" data-zabbix-action="close">取消</button>
          <button type="submit" class="button" data-zabbix-save>保存</button></div></footer></form>`;
      dialog.showModal();
      return;
    }
    const title = mode === "add" ? "接入 Zabbix" : mode === "scope" ? "调整采集主机组" : "更换 Zabbix 凭据";
    const password = source?.authMethod === "password";
    const description = mode === "add" ? "连接成功后在同一页选择采集范围和预览问题。" :
      mode === "scope" ? "选择主机组并预览这个数据源的采集范围。" : "验证新凭据后保存，平台将重新同步问题。";
    dialog.innerHTML = `<form data-zabbix-form novalidate>
      <header class="zabbix-dialog-head">
        <div><h2 id="zabbix-dialog-title">${title}</h2><p>${description}</p></div>
        <button type="button" class="zabbix-close" aria-label="关闭" data-zabbix-action="close">×</button>
      </header>
      <div class="zabbix-setup-grid ${mode === "add" ? "zabbix-setup-columns" : ""}">
      ${mode !== "scope" ? `<section aria-label="连接" data-zabbix-connection>
        <div class="zabbix-section-head"><h3>连接</h3><p>地址和凭据只用于连接 Zabbix。</p></div>
        ${mode === "add" ? `<div class="incident-form-grid">
          <label>环境<select name="environmentId" required disabled aria-describedby="zabbix-environment-status"><option value="">请选择环境</option></select></label>
          <label>名称<input name="sourceName" required maxlength="120" placeholder="例如 生产 Zabbix"></label>
        </div>
        <p class="zabbix-environment-status" id="zabbix-environment-status" role="status">正在加载环境…</p>
        <button type="button" class="button secondary zabbix-environment-retry" data-zabbix-action="retry-scopes" hidden>重新加载环境</button>` : ""}
        <label>Zabbix 访问地址<input name="endpoint" type="url" maxlength="512" placeholder="https://zabbix.example.com/zabbix"
          value="${h(source?.endpoint)}" ${source ? "readonly" : ""} aria-describedby="zabbix-endpoint-help"></label>
        <p class="zabbix-hint" id="zabbix-endpoint-help">填写 HTTPS 站点地址，平台自动补齐 API 路径。</p>
        <fieldset class="zabbix-auth-options">
          <legend class="zabbix-visually-hidden">认证方式</legend>
          <label><input name="method" type="radio" value="token" ${password ? "" : "checked"}>
            <span><strong>API Token</strong><small>推荐用于长期同步</small></span></label>
          <label><input name="method" type="radio" value="password" ${password ? "checked" : ""}>
            <span><strong>账号密码</strong><small>使用 Zabbix 只读账号</small></span></label>
        </fieldset>
        <div class="zabbix-credentials" data-zabbix-credentials>
          <label data-zabbix-account hidden>账号<input name="username" autocomplete="off" maxlength="255" placeholder="输入 Zabbix 账号"></label>
          <label><span data-zabbix-secret-label>API Token</span><input type="password" name="secret" autocomplete="new-password" maxlength="4096"></label>
        </div>
        <p class="zabbix-test-feedback" role="status" aria-live="polite" data-zabbix-test></p>
      </section>` : ""}
      ${mode !== "rotate" ? `<section aria-label="采集范围">
        <div class="zabbix-section-head"><h3>采集范围</h3><p>按主机组发现资源，主机可以独立归属业务。</p></div>
        <div data-zabbix-scope>${mode === "scope" ? '<p class="zabbix-scope-wait" role="status">正在读取主机组…</p>' : scopeWaiting}</div>
      </section>` : ""}
      </div>
      <footer class="zabbix-dialog-footer">
        <p role="status" data-zabbix-save-result>${mode === "scope" ? "请选择主机组" : "先测试连接"}</p>
        <div class="zabbix-dialog-actions">
          <button type="button" class="button secondary" data-zabbix-action="close">取消</button>
          ${mode !== "scope" ? '<button type="button" class="button secondary" data-zabbix-action="test">测试连接</button>' : ""}
          <button type="submit" class="button" disabled data-zabbix-save>${mode === "add" ? "保存并启用" : mode === "scope" ? "保存并重新同步" : "保存新凭据"}</button>
        </div>
      </footer>
      </form>`;
    const form = dialog.querySelector<HTMLFormElement>("form");
    if (!form) return;
    updateAuth(form);
    dialog.showModal();
    if (mode === "add") void loadScopeOptions(form, wizard);
    if (mode === "scope" && source) {
      void apiClient.testZabbixConnection({ body: { sourceId: source.id } }).then(result => {
        if (current !== wizard || !form.isConnected) return;
        wizard.groups = result.groups;
        renderScope(form, wizard);
        schedulePreview(form, wizard);
      }, error => { if (current === wizard) setText(form, "[data-zabbix-scope]", zabbixError(error)); });
    }
  }

  function setText(root: ParentNode, selector: string, message: string) {
    const node = root.querySelector<HTMLElement>(selector);
    if (node) node.textContent = message;
  }

  async function loadScopeOptions(form: HTMLFormElement, wizard: Wizard) {
    const environment = form.querySelector<HTMLSelectElement>('[name="environmentId"]');
    const retry = form.querySelector<HTMLButtonElement>('[data-zabbix-action="retry-scopes"]');
    if (!environment || !retry) return;
    retry.hidden = true;
    setText(form, "#zabbix-environment-status", "正在加载环境…");
    try {
      const result = await apiClient.listZabbixScopes();
      if (current !== wizard || !form.isConnected) return;
      wizard.scopes = result.environments;
      environment.innerHTML = `<option value="">请选择环境</option>${wizard.scopes.map(item => option(item.id, item.name)).join("")}`;
      environment.disabled = !wizard.scopes.length;
      setText(form, "#zabbix-environment-status", wizard.scopes.length ? "" : "当前没有可选环境，请检查资源目录与授权。");
    } catch (error) {
      if (current !== wizard || !form.isConnected) return;
      setText(form, "#zabbix-environment-status", zabbixError(error));
      retry.hidden = false;
    }
  }

  function updateAuth(form: HTMLFormElement) {
    const account = form.querySelector<HTMLElement>("[data-zabbix-account]");
    const secret = form.querySelector<HTMLInputElement>('[name="secret"]');
    const credentials = form.querySelector<HTMLElement>("[data-zabbix-credentials]");
    if (!account || !secret || !credentials) return;
    const password = value(form, "method") === "password";
    account.hidden = !password;
    credentials.classList.toggle("zabbix-credentials-password", password);
    secret.placeholder = password ? "输入 Zabbix 密码" : "粘贴 API Token";
    setText(form, "[data-zabbix-secret-label]", password ? "密码" : "API Token");
  }

  function setTestFeedback(form: HTMLFormElement, message: string, state: "error" | "success" | "busy" = "busy") {
    const node = form.querySelector<HTMLElement>("[data-zabbix-test]");
    if (!node) return;
    node.textContent = message;
    node.dataset.state = state;
  }

  function updateSaveHint(form: HTMLFormElement, wizard: Wizard) {
    if (wizard.mode === "edit") return;
    const hasConnection = wizard.mode === "scope" || Boolean(wizard.draft);
    const ready = wizard.mode === "rotate" ? hasConnection : wizard.previewOK;
    setText(form, "[data-zabbix-save-result]", !hasConnection ? "先测试连接" :
      wizard.mode === "add" && !value(form, "environmentId") ? "请选择环境" :
      wizard.mode === "add" && !value(form, "sourceName").trim() ? "请填写名称" :
      ready ? (wizard.mode === "rotate" ? "凭据已验证" : "采集范围已验证") : "选择采集范围并预览问题");
  }

  function renderScope(form: HTMLFormElement, wizard: Wizard) {
    const scope = form.querySelector<HTMLElement>("[data-zabbix-scope]");
    if (!scope) return;
    scope.innerHTML = `${wizard.source ? `<p>${h(wizard.source.environmentName ?? wizard.source.scope.environmentId)} / ${h(wizard.source.businessName ?? wizard.source.scope.businessId)}</p>` :
      '<p class="zabbix-scope-environment" data-zabbix-scope-environment></p><label>归属业务<select name="businessId"><option value="">请选择业务</option></select></label>'}
      <div class="zabbix-groups-head"><h4>采集主机组</h4><p data-zabbix-group-count role="status"></p></div>
      <label><span class="zabbix-visually-hidden">搜索主机组</span><input type="search" name="groupSearch" placeholder="按名称搜索"></label>
      <div class="zabbix-group-list" data-zabbix-groups></div><p class="zabbix-hint">组成员变化时自动同步。重叠主机只计一次。</p>
      <section aria-label="问题预览" class="zabbix-preview"><h3>问题预览</h3><div role="status" data-zabbix-preview>请选择归属和主机组。</div></section>`;
    updateBusinessOptions(form, wizard);
    showGroups(form, wizard);
  }

  function updateBusinessOptions(form: HTMLFormElement, wizard: Wizard) {
    const business = form.querySelector<HTMLSelectElement>('[name="businessId"]');
    if (!business) return;
    const environment = wizard.scopes.find(scope => scope.id === value(form, "environmentId"));
    const options = environment?.businesses ?? [];
    setText(form, "[data-zabbix-scope-environment]", environment ? `环境 · ${environment.name}` : "请先选择左侧环境");
    business.innerHTML = `<option value="">请选择业务</option>${options.map(item => option(item.id, item.name)).join("")}`;
    business.disabled = !options.length;
  }

  function showGroups(form: HTMLFormElement, wizard: Wizard) {
    const groupList = form.querySelector<HTMLElement>("[data-zabbix-groups]");
    const needle = value(form, "groupSearch").trim().toLocaleLowerCase();
    if (!groupList) return;
    groupList.innerHTML = wizard.groups.filter(group => group.name.toLocaleLowerCase().includes(needle)).map(group =>
      `<label><input type="checkbox" name="groupId" value="${h(group.groupid)}" ${wizard.selected.has(group.groupid) ? "checked" : ""}> ${h(group.name)}</label>`).join("") || "<p>暂无匹配主机组。</p>";
  }

  function selectedScope(form: HTMLFormElement, wizard: Wizard): ZabbixScope {
    return wizard.source?.scope ?? { environmentId: value(form, "environmentId"), businessId: value(form, "businessId") };
  }

  function schedulePreview(form: HTMLFormElement, wizard: Wizard) {
    const revision = ++wizard.previewRevision;
    const scope = selectedScope(form, wizard);
    setText(form, "[data-zabbix-group-count]", `已选 ${wizard.selected.size} 组`);
    wizard.previewOK = false;
    updateSaveHint(form, wizard);
    updateSave(form, wizard);
    if (!scope.environmentId || !scope.businessId || !wizard.selected.size || wizard.selected.size > 50) {
      setText(form, "[data-zabbix-preview]", "请选择环境、业务与 1–50 个主机组。");
      return;
    }
    setText(form, "[data-zabbix-preview]", "正在统计所选主机和活跃问题…");
    window.setTimeout(async () => {
      if (current !== wizard || revision !== wizard.previewRevision || !form.isConnected) return;
      try {
        const preview = await apiClient.previewZabbixProblems({ body: { scope, draft: wizard.draft,
          sourceId: wizard.source?.id, groupIds: [...wizard.selected] } });
        if (current !== wizard || revision !== wizard.previewRevision || !form.isConnected) return;
        const node = form.querySelector<HTMLElement>("[data-zabbix-preview]");
        if (node) {
          node.innerHTML = `<p>${preview.hostCount} 台主机 · ${preview.problemCount} 条活跃问题</p>
            ${preview.problems.length ? `<ul>${preview.problems.map(row => `<li>${h(diagnosticLevels[row.severity])} · ${h(row.title)}</li>`).join("")}</ul>` : "<p>当前范围暂无活跃问题。</p>"}`;
        }
        wizard.previewOK = true;
      } catch (error) {
        if (current !== wizard || revision !== wizard.previewRevision || !form.isConnected) return;
        setText(form, "[data-zabbix-preview]", zabbixError(error));
      }
      updateSave(form, wizard);
      updateSaveHint(form, wizard);
    }, 220);
  }

  function updateSave(form: HTMLFormElement, wizard: Wizard) {
    const button = form.querySelector<HTMLButtonElement>("[data-zabbix-save]");
    const missingName = (wizard.mode === "add" || wizard.mode === "edit") && !value(form, "sourceName").trim();
    if (button) button.disabled = saving || missingName || (wizard.mode === "rotate" ? !wizard.draft : wizard.mode !== "edit" && !wizard.previewOK);
  }

  async function test(form: HTMLFormElement, wizard: Wizard, button: HTMLButtonElement) {
    const method = value(form, "method") === "password" ? "password" : "token";
    const credential: ZabbixCredential = { method, secret: value(form, "secret"), username: method === "password" ? value(form, "username").trim() : undefined };
    const endpoint = value(form, "endpoint").trim();
    if (!endpoint || !credential.secret || (method === "password" && !credential.username)) {
      setTestFeedback(form, "请填写地址和认证凭据。", "error");
      return;
    }
    const revision = ++wizard.revision;
    button.disabled = true;
    setTestFeedback(form, "正在测试地址、凭据、主机组和问题读取权限…");
    try {
      const result = await apiClient.testZabbixConnection({ body: { endpoint, credential, sourceId: wizard.source?.id } });
      if (current !== wizard || revision !== wizard.revision) return;
      wizard.draft = result.draft;
      wizard.groups = result.groups;
      wizard.selected = new Set([...wizard.selected].filter(id => result.groups.some(group => group.groupid === id)));
      setTestFeedback(form, `连接成功 · Zabbix ${result.version} · ${result.groups.length} 个可读主机组`, "success");
      const secret = form.querySelector<HTMLInputElement>('[name="secret"]');
      if (secret) { secret.value = ""; secret.placeholder = "已验证 · 更改时重新填写"; }
      const address = form.querySelector<HTMLInputElement>('[name="endpoint"]');
      if (address) address.value = result.endpoint;
      renderScope(form, wizard);
      setText(form, "[data-zabbix-group-count]", `已选 ${wizard.selected.size} 组`);
      updateSave(form, wizard);
      updateSaveHint(form, wizard);
    } catch (error) {
      if (current === wizard && revision === wizard.revision) setTestFeedback(form, zabbixError(error), "error");
    } finally {
      button.disabled = false;
    }
  }

  async function save(form: HTMLFormElement, wizard: Wizard) {
    if (saving || form.querySelector<HTMLButtonElement>("[data-zabbix-save]")?.disabled) return;
    saving = true;
    const controls = form.querySelector<HTMLElement>(".zabbix-setup-grid");
    if (controls) controls.inert = true;
    const testAction = form.querySelector<HTMLButtonElement>('[data-zabbix-action="test"]');
    if (testAction) testAction.disabled = true;
    updateSave(form, wizard);
    setText(form, "[data-zabbix-save-result]", "正在验证采集范围并保存…");
    try {
      const groupIds = [...wizard.selected];
      const updated = wizard.source
        ? await apiClient.updateZabbixSource({ sourceId: wizard.source.id, "If-Match": `"${wizard.source.version}"`,
          body: wizard.mode === "edit" ? { name: value(form, "sourceName").trim() } :
            wizard.mode === "rotate" ? { draft: wizard.draft } : { groupIds } })
        : await apiClient.createZabbixSource({ body: { name: value(form, "sourceName").trim(), draft: wizard.draft,
          scope: selectedScope(form, wizard), groupIds } });
      if (current !== wizard) return;
      guard.clean();
      dialog.close();
      onSaved(updated, wizard.mode);
    } catch (error) {
      if (current === wizard) {
        setText(form, "[data-zabbix-save-result]", zabbixError(error));
        if (error instanceof ApiError && error.status === 412) onConflict();
      }
    } finally {
      saving = false;
      if (controls) controls.inert = false;
      if (testAction) testAction.disabled = false;
      if (current === wizard) updateSave(form, wizard);
    }
  }

  dialog.addEventListener("click", async event => {
    const button = event.target instanceof HTMLElement ? event.target.closest<HTMLButtonElement>("button[data-zabbix-action]") : null;
    if (!button?.form || !current) return;
    const action = button.dataset.zabbixAction;
    if (action === "retry-scopes" && current.mode === "add") await loadScopeOptions(button.form, current);
    if (action === "test") await test(button.form, current, button);
  });

  dialog.addEventListener("submit", event => {
    event.preventDefault();
    if (current && event.target instanceof HTMLFormElement) void save(event.target, current);
  });
  dialog.addEventListener("close", () => {
    if (dialog.open) return;
    current = undefined;
    dialog.replaceChildren();
  });
  dialog.addEventListener("input", event => {
    const field = event.target;
    const wizard = current;
    if (!(field instanceof HTMLInputElement || field instanceof HTMLSelectElement) || !field.form || !wizard) return;
    const form = field.form;
    if (field.name === "groupSearch") { showGroups(form, wizard); return; }
    if (field.name === "sourceName") { updateSave(form, wizard); updateSaveHint(form, wizard); return; }
    if (!["endpoint", "secret", "username", "method"].includes(field.name)) return;
    if (field.name === "method") {
      const secret = form.querySelector<HTMLInputElement>('[name="secret"]');
      if (secret) { secret.value = ""; secret.placeholder = ""; }
      updateAuth(form);
    }
    wizard.revision++;
    wizard.previewRevision++;
    wizard.draft = undefined;
    wizard.previewOK = false;
    if (field.name === "endpoint") wizard.selected.clear();
    setTestFeedback(form, "");
    const scope = form.querySelector<HTMLElement>("[data-zabbix-scope]");
    if (scope) scope.innerHTML = scopeWaiting;
    updateSave(form, wizard);
    updateSaveHint(form, wizard);
  });
  dialog.addEventListener("change", event => {
    const field = event.target;
    const wizard = current;
    if (!(field instanceof HTMLInputElement || field instanceof HTMLSelectElement) || !field.form || !wizard) return;
    const form = field.form;
    if (field.name === "environmentId") {
      updateBusinessOptions(form, wizard);
      if (wizard.mode === "add" && !wizard.draft) { updateSaveHint(form, wizard); return; }
    } else if (field.name === "groupId" && field instanceof HTMLInputElement) {
      if (field.checked) wizard.selected.add(field.value); else wizard.selected.delete(field.value);
    } else if (field.name !== "businessId") return;
    schedulePreview(form, wizard);
  });
  return open;
}
