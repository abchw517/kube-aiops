import { escapeHtml as h } from "../../components/html.ts";

export const endpoint = "/api/v1/admin/ai-services";
export const skillsEndpoint = "/api/v1/admin/ai-skills";
const sources: Record<string, string> = {
  zabbix: "Zabbix 告警", alertmanager: "Alertmanager 告警", finding: "诊断复核", incident: "故障根因分析", wiki: "知识问答",
};

export type Scope = { source: string; sourceId: string; environmentId: string; businessId: string; clusterId?: string; namespace?: string };
type Model = Scope & { model: string; service?: string; credential?: string };
type Settings = { name: string; provider: string; fast: string; accurate: string; disabled: boolean };
type Connection = { scope: Scope; desired: Settings; effective: Settings; effectiveVersion: number; publishedAt?: string };
export type Entry<T> = { id: string; version: number; data: T };
type ServiceStatus = { status: "configured" | "unconfigured" | "invalid"; models: Model[]; connections?: Entry<Connection>[] };
const providers = ["vLLM", "Ollama", "OpenAI", "DeepSeek", "智谱 AI", "OpenAI 兼容"];

export async function requestAI<T>(path: string, body?: unknown): Promise<T> {
  const response = await fetch(path, body === undefined ? undefined :
    { method: "POST", headers: {"Content-Type": "application/json"}, body: JSON.stringify(body) });
  if (!response.ok) {
    const message = (await response.text()).trim();
    throw new Error(response.headers.get("content-type")?.includes("text/plain") && message ? message : "请求失败，请重试。");
  }
  return response.json();
}

export function scopeOf({ source, sourceId, environmentId, businessId, clusterId, namespace }: Scope): Scope {
  return { source, sourceId, environmentId, businessId, clusterId, namespace };
}

export function scopeText(m: Scope): string {
  return [sources[m.source] ?? m.source, m.environmentId, m.businessId, m.clusterId, m.namespace, m.sourceId].filter(Boolean).join(" / ");
}

function connectionFor(data: ServiceStatus, model: Model) {
  return data.connections?.find(entry => JSON.stringify(scopeOf(entry.data.scope)) === JSON.stringify(scopeOf(model)));
}

export async function loadAIServices() {
  const data = await requestAI<ServiceStatus>(endpoint);
  const environments = [...new Set(data.models.map(m => m.environmentId).filter(Boolean))].sort();
  const label = { configured: "已接入部署绑定", unconfigured: "尚未绑定", invalid: "配置读取失败" }[data.status];
  const rows = data.models.map((m, index) => {
    const entry = connectionFor(data, m);
    const current = entry?.data.effective;
    const desired = entry?.data.desired;
    const pending = entry && (entry.data.effectiveVersion === 0 || JSON.stringify(current) !== JSON.stringify(desired));
    return `<tr data-ai-model-row="${h(m.environmentId)}"><td><strong>${h(desired?.name ?? sources[m.source] ?? m.source)}</strong><small>${h(desired?.provider ?? "Holmes 部署绑定")}</small></td>
      <td>${h(m.service ?? "由部署提供")}<small>${h(scopeText(m))}</small></td><td>${h(m.credential ?? "由部署提供")}</td>
      <td>${h(current?.fast || m.model || "Holmes 默认模型")}${current?.accurate && current.accurate !== current.fast ? ` · ${h(current.accurate)}` : ""}</td>
      <td><span role="status">${pending ? "待发布" : current?.disabled ? "已暂停" : entry ? "已发布" : "待检查"}</span></td>
      <td><div class="system-actions"><button class="button secondary" type="button" data-ai-probe="${index}">测试连接</button>
      <button class="button secondary" type="button" data-ai-edit="${index}">编辑</button>
      ${pending ? `<button class="button" type="button" data-ai-publish="${index}">发布配置</button>` : ""}</div></td></tr>`;
  }).join("");
  return { html: `<div class="portal-ai-page">
    <section class="panel ai-panel"><div class="system-actions">
      <label>环境 <select data-ai-environment><option value="">全部环境</option>${environments.map(name => `<option value="${h(name)}">${h(name)}</option>`).join("")}</select></label>
      <button class="button secondary" type="button" data-ai-architecture>查看架构</button>
      <button class="button secondary" type="button" data-ai-refresh>重新读取配置</button>
    </div></section>
    <section class="panel ai-panel"><header class="ai-panel-heading"><h2>HolmesGPT 服务</h2><span>${label}</span></header><dl>
      <dt>部署形态</dt><dd>独立内部服务，经 Portal API 鉴权</dd>
      <dt>接入端点</dt><dd>HolmesGPT /api/chat</dd>
      <dt>能力入口</dt><dd><a href="#/resources/datasources">数据源管理</a> · <a href="#/ai/skills">AI 技能</a></dd>
      <dt>模型回退</dt><dd>Finding 复核、事件 RCA 和知识问答沿用既有回退规则</dd>
    </dl></section>
    <section class="panel ai-panel"><header class="ai-panel-heading"><h2>模型连接</h2>
      <button class="button" type="button" data-ai-add ${data.models.length ? "" : "disabled"}>新增连接</button></header>
      <p>通过已部署的 Holmes 发现模型。服务地址和凭据由部署绑定维护。</p><p data-ai-action-status role="status"></p>
      ${rows ? `<div class="table-wrap"><table><thead><tr><th>提供商 / 类型</th><th>服务地址</th><th>凭据</th><th>生效模型</th><th>状态</th><th>操作</th></tr></thead><tbody>${rows}</tbody></table></div>` : "<p>请先部署 Holmes 并添加范围绑定。</p>"}
    </section>
    <section class="panel ai-panel"><h2>模型档位与发布版本</h2>
      <p>fast-model 用于知识问答和增强复核，accurate-model 用于告警调查、初步复核和 RCA。可按绑定暂停 Holmes 调用。</p>
      ${data.connections?.length ? `<ul>${data.connections.map(e => `<li data-ai-model-row="${h(e.data.scope.environmentId)}">${h(scopeText(e.data.scope))} · 期望 v${JSON.stringify(e.data.desired) === JSON.stringify(e.data.effective) ? e.data.effectiveVersion : e.version} · 生效 ${e.data.effectiveVersion ? `v${e.data.effectiveVersion}` : "部署默认"} · fast: ${h(e.data.desired.fast || "—")} · accurate: ${h(e.data.desired.accurate || "—")}${e.data.publishedAt ? ` · 最近发布 ${h(new Date(e.data.publishedAt).toLocaleString("zh-CN"))}` : ""}</li>`).join("")}</ul>` : "<p>尚无 Portal 保存的档位配置。</p>"}
    </section>
    <dialog class="wiki-dialog ai-dialog" data-ai-dialog aria-labelledby="ai-dialog-title"></dialog>
  </div>`, data };
}

export function aiDialog(dialog: HTMLDialogElement, title: string, content: string): void {
  dialog.innerHTML = `<header class="ai-panel-heading"><h2 id="ai-dialog-title">${h(title)}</h2>
    <button type="button" class="button secondary" data-ai-close>关闭</button></header>${content}`;
  dialog.querySelector("[data-ai-close]")?.addEventListener("click", () => dialog.close());
  if (!dialog.open) dialog.showModal();
}

export async function showResult(button: HTMLButtonElement, status: Element, run: () => Promise<void>): Promise<void> {
  if (button.disabled) return;
  button.disabled = true;
  status.textContent = "处理中…";
  try { await run(); }
  catch (error) { status.textContent = error instanceof Error ? error.message : "请求失败，请重试。"; }
  finally { button.disabled = false; }
}

function openEditor(dialog: HTMLDialogElement, data: ServiceStatus, reload: () => void, index?: number) {
  const selected = index === undefined ? undefined : data.models[index];
  const initial = selected && connectionFor(data, selected);
  const settings = initial?.data.desired;
  const choices = data.models.map((m, i) => index === undefined && connectionFor(data, m) ? "" :
    `<option value="${i}" ${i === index ? "selected" : ""}>${h(scopeText(m))}</option>`).join("");
  aiDialog(dialog, index === undefined ? "新增模型连接" : "编辑模型连接", `<form class="ai-form">
    <label>部署范围<select name="scope" ${index === undefined ? "" : "disabled"}>${choices}</select></label>
    <div data-ai-connection-source></div>
    <label>连接名称<input name="name" required maxlength="60" value="${h(settings?.name ?? "")}"></label>
    <label>提供商<select name="provider">${providers.map(name => `<option ${(settings?.provider ?? "OpenAI 兼容") === name ? "selected" : ""}>${h(name)}</option>`).join("")}</select></label>
    <div class="system-actions"><button class="button secondary" type="button" data-ai-discover>发现模型</button></div>
    <label>fast-model<select name="fast">${settings?.fast ? `<option>${h(settings.fast)}</option>` : ""}</select></label>
    <label>accurate-model<select name="accurate">${settings?.accurate ? `<option>${h(settings.accurate)}</option>` : ""}</select></label>
    <label><input type="checkbox" name="disabled" ${settings?.disabled ? "checked" : ""}> 暂停此范围的 Holmes 调用</label>
    <p role="status"></p><div class="system-actions"><button class="button" type="submit">保存草稿</button></div>
  </form>`);
  const form = dialog.querySelector<HTMLFormElement>("form");
  const scope = form?.querySelector<HTMLSelectElement>("[name=scope]");
  const discover = form?.querySelector<HTMLButtonElement>("[data-ai-discover]");
  const save = form?.querySelector<HTMLButtonElement>("[type=submit]");
  const status = form?.querySelector("[role=status]");
  if (!form || !scope || !discover || !save || !status) return;
  const selects = [...form.querySelectorAll<HTMLSelectElement>("[name=fast], [name=accurate]")];
  if (!scope.options.length) { save.disabled = true; discover.disabled = true; status.textContent = "现有范围已配置，可在列表编辑。"; }
  const source = form.querySelector("[data-ai-connection-source]");
  const showSource = () => {
    const selected = data.models[Number(scope.value)];
    if (source) source.textContent = selected ? `服务地址 ${selected.service ?? "由部署提供"} · 凭据 ${selected.credential ?? "由部署提供"}` : "";
  };
  showSource();
  scope.addEventListener("change", () => {
    for (const select of selects) select.replaceChildren();
    showSource();
    status.textContent = "";
  });
  discover.addEventListener("click", () => void showResult(discover, status, async () => {
    const model = data.models[Number(scope.value)];
    if (!model) return;
    const {models} = await requestAI<{models: string[]}>(`${endpoint}?action=discover`, scopeOf(model));
    if (!dialog.open || data.models[Number(scope.value)] !== model) return;
    for (const select of selects) {
      const previous = select.value;
      select.replaceChildren(...models.map(m => new Option(m, m)));
      if (models.includes(previous)) select.value = previous;
    }
    status.textContent = models.length ? `已发现 ${models.length} 个可用模型别名` : "Holmes 未返回模型别名";
  }));
  form.addEventListener("submit", event => {
    event.preventDefault();
    void showResult(save, status, async () => {
      const model = data.models[Number(scope.value)];
      if (!model) return;
      const fields = new FormData(form);
      const config = {name: fields.get("name"), provider: fields.get("provider"), fast: fields.get("fast") ?? "",
        accurate: fields.get("accurate") ?? "", disabled: fields.has("disabled")};
      await requestAI(`${endpoint}?action=save`, {...scopeOf(model),version: initial?.version ?? 0, config});
      dialog.close();
      reload();
    });
  });
}

export function bindAIServices(root: HTMLElement, data: ServiceStatus, reload: () => void): void {
  const dialog = root.querySelector<HTMLDialogElement>("[data-ai-dialog]");
  if (!dialog) return;
  root.querySelector("[data-ai-refresh]")?.addEventListener("click", reload);
  root.querySelector("[data-ai-architecture]")?.addEventListener("click", () => aiDialog(dialog,
    "HolmesGPT 部署与调用路径", "<p>Web Portal → Go API → 范围绑定的 HolmesGPT 服务 → 已部署的模型提供商。</p><p>发布后，新请求按绑定使用对应档位。模型服务地址和凭据在部署侧配置。</p>"));
  root.querySelector("[data-ai-add]")?.addEventListener("click", () => openEditor(dialog, data, reload));
  const filter = root.querySelector<HTMLSelectElement>("[data-ai-environment]");
  filter?.addEventListener("change", () => {
    for (const row of root.querySelectorAll<HTMLElement>("[data-ai-model-row]")) {
      row.hidden = Boolean(filter.value && row.dataset.aiModelRow !== filter.value);
    }
  });
  for (const button of root.querySelectorAll<HTMLButtonElement>("[data-ai-probe], [data-ai-edit], [data-ai-publish]")) {
    button.addEventListener("click", () => {
      if (button.dataset.aiEdit !== undefined) { openEditor(dialog, data, reload, Number(button.dataset.aiEdit)); return; }
      const model = data.models[Number(button.dataset.aiProbe ?? button.dataset.aiPublish)];
      const status = button.closest("tr")?.querySelector("[role=status]");
      if (!model || !status) return;
      if (button.dataset.aiProbe !== undefined) {
        void showResult(button, status, async () => {
          await requestAI(`${endpoint}?action=recheck`, scopeOf(model));
          const {models} = await requestAI<{models: string[]}>(`${endpoint}?action=discover`, scopeOf(model));
          status.textContent = `服务已就绪 · ${models.length} 个模型`;
        });
      } else {
        const entry = connectionFor(data, model);
        if (!entry) return;
        aiDialog(dialog, "发布模型配置", `<p>${h(scopeText(model))} · 草稿 v${entry.version}</p>
          <p role="status"></p><button class="button" type="button" data-ai-confirm>发布配置</button>`);
        const confirm = dialog.querySelector<HTMLButtonElement>("[data-ai-confirm]");
        const feedback = dialog.querySelector("[role=status]");
        if (confirm && feedback) confirm.addEventListener("click", () => void showResult(confirm, feedback, async () => {
          await requestAI(`${endpoint}?action=publish`, {...scopeOf(model),version: entry.version});
          dialog.close();
          reload();
        }));
      }
    });
  }
}
