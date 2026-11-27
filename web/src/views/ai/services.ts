import { apiClient } from "../../api/client.ts";
import { escapeHtml as h } from "../../components/html.ts";
import { aiDialogMarkup, dialogFooter, icon, options, pill, scopeDetails, searchMatches, stat, updateWorkspaceQuery } from "./workspace.ts";

export const endpoint = "/api/v1/admin/ai-services";
export const skillsEndpoint = "/api/v1/admin/ai-skills";
const sources: Record<string, string> = {
  zabbix: "Zabbix 告警", alertmanager: "Alertmanager 告警", finding: "诊断复核", incident: "故障根因分析", wiki: "知识问答",
};

export type Scope = { source: string; sourceId: string; environmentId: string; businessId: string; clusterId?: string; namespace?: string };
type Model = Scope & { model: string; service?: string; credential?: string; managed?: boolean };
type Settings = { name: string; provider: string; fast: string; accurate: string; disabled: boolean };
type Connection = { scope: Scope; desired: Settings; effective: Settings; effectiveVersion: number; publishedAt?: string };
export type Entry<T> = { id: string; version: number; data: T };
type ServiceStatus = { status: "configured" | "unconfigured" | "invalid"; models: Model[]; connections?: Entry<Connection>[];
  pilot?: { address?: string; status: "ready" | "unreachable" | "invalid" } };
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
  return { source, sourceId, environmentId, businessId, clusterId: clusterId || undefined, namespace: namespace || undefined };
}

export function scopeText(m: Scope): string {
  return [sources[m.source] ?? m.source, m.environmentId, m.businessId, m.clusterId, m.namespace, m.sourceId].filter(Boolean).join(" / ");
}

function connectionFor(data: ServiceStatus, model: Model) {
  return data.connections?.find(entry => JSON.stringify(scopeOf(entry.data.scope)) === JSON.stringify(scopeOf(model)));
}

export function scopeSummary(m: Scope): string {
  return [m.environmentId, m.businessId, m.clusterId, m.namespace].filter(Boolean).join(" / ") || m.sourceId;
}

function pending(entry?: Entry<Connection>): boolean {
  return Boolean(entry && (entry.data.effectiveVersion === 0 || JSON.stringify(entry.data.effective) !== JSON.stringify(entry.data.desired)));
}

export async function loadAIServices() {
  const data = await requestAI<ServiceStatus>(endpoint);
  const [zabbix, observability] = await Promise.all([
    data.models.some(model => model.source === "zabbix") ? apiClient.listZabbixSources().catch(() => undefined) : undefined,
    data.models.some(model => model.source === "alertmanager") ? apiClient.listObservabilitySources().catch(() => undefined) : undefined,
  ]);
  const sourceNames = new Map<string, string | undefined>([
    ...(zabbix?.items ?? []).map(item => [`zabbix:${item.id}`,item.name] as const),
    ...(observability?.items ?? []).filter(item => item.kind === "alertmanager").map(item => [`alertmanager:${item.id}`,item.name] as const),
  ]);
  const environments = [...new Set(data.models.map(m => m.environmentId).filter(Boolean))].sort();
  const pilotStatus = data.pilot && { ready: "服务已就绪", unreachable: "服务不可达或未就绪", invalid: "服务地址无效" }[data.pilot.status];
  const rows = data.models.map((model, index) => {
    const entry = connectionFor(data, model);
    const live = entry?.data.effectiveVersion ? entry.data.effective : undefined;
    const name = live?.name || entry?.data.desired.name || sourceNames.get(`${model.source}:${model.sourceId}`) || sources[model.source] || model.source;
    return { model, entry, name, live, index, pending: pending(entry) };
  });
  const waiting = rows.filter(row => row.pending);
  const layout = `<div class="portal-ai-page ai-workspace">
    <div class="page-head row between"><div><h1>AI 服务配置</h1><p class="subtitle">查看 HolmesGPT 服务状态，管理数据范围和生效模型。</p></div>
      <button class="btn primary" type="button" data-ai-bind>＋ 新增范围绑定</button></div>
    <section class="hero" aria-label="HolmesGPT 服务状态">
      <div class="row hero-head">${icon("✦")}<div><div class="row"><h2>HolmesGPT 服务</h2>${pill(pilotStatus ?? "待检查", data.pilot?.status === "ready" ? "good" : "bad")}</div>
      <p class="muted"><code>${h(data.pilot?.address ?? "")}</code></p></div></div>
      <div class="end"><div class="inline-stats">${stat(rows.length, "已绑定范围")}${stat(rows.filter(row => row.live && !row.live.disabled).length, "已发布档位")}${stat(waiting.length, "待发布草稿")}</div>
        <div class="actions"><button class="btn small" type="button" data-ai-refresh>检查服务</button><button class="btn quiet small" type="button" data-ai-service-details>服务详情</button></div></div>
    </section>
    ${data.status === "invalid" ? '<p role="alert">部分绑定配置读取失败，请检查 API 日志或部署配置。</p>' : ""}
    <div class="workspace">
      <section class="panel" aria-label="范围与模型">
        <div class="row between panel-head"><h2>范围与模型</h2><small data-ai-count>${rows.length} 个范围</small></div>
        <div class="filters"><input class="search" type="search" data-ai-search aria-label="搜索绑定" placeholder="搜索数据源、集群或业务"><select data-ai-environment aria-label="环境"><option value="">全部环境</option>${options(environments.map(name => [name, name]))}</select></div>
        ${rows.map(({model, entry, name, live, index, pending: draft}) => `<article class="item" data-ai-model-row="${h(model.environmentId)}" data-ai-search-text="${h([name, scopeText(model), model.service].join(" "))}">
          ${icon(model.source === "zabbix" ? "Z" : model.source === "alertmanager" ? "A" : "H", model.source === "alertmanager" ? "sand" : "")}
          <div><h3>${h(name)}</h3><small>${h(sources[model.source] ?? model.source)} · ${model.managed ? "Portal 绑定" : "部署绑定"}</small>
          <div class="chips"><span class="scope-chip">${h(scopeSummary(model))}</span>${live?.disabled ? pill("已暂停","warn") : live ? pill("已发布","good") : pill("使用默认模型")}${draft ? pill("有待发布草稿","warn") : ""}</div>
          <div class="models">${live && !live.disabled ? `<span>快速 <b>${h(live.fast)}</b></span><span>深度 <b>${h(live.accurate)}</b></span>` : live?.disabled ? "" : `<span>绑定模型 <b>${h(model.model || "Holmes 默认模型")}</b></span>`}
          ${entry?.data.effectiveVersion ? `<span>生效 v${entry.data.effectiveVersion}</span>` : ""}</div></div>
          <div class="actions"><button class="btn small" type="button" data-ai-probe="${index}">测试</button>
          <button class="btn small" type="button" data-ai-edit="${index}">${entry ? "编辑档位" : "配置档位"}</button>
          ${draft ? `<button class="btn primary small" type="button" data-ai-publish="${index}">发布</button>` : ""}</div></article>`).join("")}
        <div class="empty" data-ai-empty ${rows.length ? "hidden" : ""}>${rows.length ? '当前筛选下没有范围绑定。<div class="actions"><button class="btn small" type="button" data-ai-clear>清除筛选</button></div>' : "从「新增范围绑定」选择已接入的数据源，或指定集群和命名空间。"}</div>
      </section>
      <aside class="stack" aria-label="配置操作">
        <section class="panel"><div class="side-title">${icon("↑","sand")}<h2>待发布配置</h2></div>
          ${waiting.length ? waiting.map(({index, entry, name}) => `<button class="release" type="button" data-ai-publish="${index}"><b>${h(entry?.data.desired.name || name)}</b><small>草稿 v${entry?.version} · 查看发布差异</small></button>`).join("") : '<p class="side-note">暂无待发布的草稿。</p>'}
        </section>
        <section class="panel"><div class="side-title">${icon("⌁","lilac")}<h2>模型档位</h2></div>
          <p class="side-note"><b>快速模型</b>用于知识问答和增强复核。<br><b>深度模型</b>用于告警调查、初步复核和事件 RCA。</p>
          <button class="btn small" type="button" data-ai-architecture>查看调用链路</button></section>
      </aside>
    </div>${aiDialogMarkup}
  </div>`;
  return { html: layout, data };
}

export function aiDialog(dialog: HTMLDialogElement, title: string, content: string): void {
  dialog.innerHTML = `<header class="dialog-head"><h2 id="ai-dialog-title">${h(title)}</h2>
    <button type="button" class="btn quiet" data-ai-close>关闭</button></header>${content}`;
  for (const button of dialog.querySelectorAll("[data-ai-close], [data-ai-dismiss]")) button.addEventListener("click", () => dialog.close());
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

function openEditor(dialog: HTMLDialogElement, data: ServiceStatus, reload: () => void, index: number) {
  const selected = data.models[index];
  if (!selected) return;
  const initial = connectionFor(data, selected);
  const settings = initial?.data.desired;
  const alias = selected.model;
  aiDialog(dialog, initial ? "编辑模型档位" : "配置模型档位", `<form class="ai-form">
    <div class="dialog-body">${scopeDetails([["绑定范围",scopeSummary(selected)],["生效版本",initial?.data.effectiveVersion ? `v${initial.data.effectiveVersion}` : "绑定默认模型"]])}
    <fieldset class="group"><legend>模型档位</legend>
    <div class="grid-2"><label>连接名称<input name="name" required maxlength="60" value="${h(settings?.name || sources[selected.source] || selected.sourceId)}"></label>
    <label>提供商<select name="provider">${providers.map(name => `<option ${(settings?.provider ?? "OpenAI 兼容") === name ? "selected" : ""}>${h(name)}</option>`).join("")}</select></label></div>
    <div class="grid-2"><label>快速模型<select name="fast">${settings?.fast || alias ? `<option>${h(settings?.fast || alias)}</option>` : ""}</select></label>
    <label>深度模型<select name="accurate">${settings?.accurate || alias ? `<option>${h(settings?.accurate || alias)}</option>` : ""}</select></label></div>
    <label class="check"><input type="checkbox" name="disabled" ${settings?.disabled ? "checked" : ""}>暂停此范围的 Holmes 调用</label>
    <div class="actions"><button class="btn small" type="button" data-ai-discover>发现模型</button></div>
    <small>保存草稿后，可在发布时确认影响范围和模型差异。</small>
    </fieldset><p class="feedback" role="status"></p></div>
    ${dialogFooter('<button class="btn primary" type="submit">保存草稿</button>')}</form>`);
  const form = dialog.querySelector<HTMLFormElement>("form");
  const discover = form?.querySelector<HTMLButtonElement>("[data-ai-discover]");
  const save = form?.querySelector<HTMLButtonElement>("[type=submit]");
  const status = form?.querySelector("[role=status]");
  if (!form || !discover || !save || !status) return;
  const selects = [...form.querySelectorAll<HTMLSelectElement>("[name=fast], [name=accurate]")];
  discover.addEventListener("click", () => void showResult(discover, status, async () => {
    const {models} = await requestAI<{models: string[]}>(`${endpoint}?action=discover`, scopeOf(selected));
    if (!dialog.open || !form.isConnected) return;
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
      const fields = new FormData(form);
      const config = {name: fields.get("name"), provider: fields.get("provider"), fast: fields.get("fast") ?? "",
        accurate: fields.get("accurate") ?? "", disabled: fields.has("disabled")};
      await requestAI(`${endpoint}?action=save`, {...scopeOf(selected),version: initial?.version ?? 0, config});
      dialog.close();
      reload();
    });
  });
}

export function bindAIServices(root: HTMLElement, data: ServiceStatus, reload: () => void, hash = ""): void {
  const dialog = root.querySelector<HTMLDialogElement>("[data-ai-dialog]");
  if (!dialog) return;
  root.querySelector("[data-ai-refresh]")?.addEventListener("click", reload);
  root.querySelector("[data-ai-architecture]")?.addEventListener("click", () => aiDialog(dialog,
    "HolmesGPT 部署与调用路径", "<p>Web Portal → Go API → 范围绑定的 HolmesGPT 服务 → 已部署的模型提供商。</p><p>发布后，新请求按绑定使用对应档位。模型服务地址和凭据在部署侧配置。</p>"));
  root.querySelector("[data-ai-bind]")?.addEventListener("click", () => void openBinding(dialog, reload));
  const params = new URLSearchParams(hash.split("?")[1]);
  const source = params.get("bindSource");
  const sourceId = params.get("sourceId");
  if ((source === "zabbix" || source === "alertmanager") && sourceId) {
    history.replaceState(null, "", "#/ai");
    void openBinding(dialog, reload, source, sourceId);
  }
  root.querySelector("[data-ai-service-details]")?.addEventListener("click", () => aiDialog(dialog, "HolmesGPT 服务详情",
    `<div class="dialog-body">${scopeDetails([["服务地址", data.pilot?.address],["状态",data.pilot ? {ready:"已就绪",unreachable:"不可达或未就绪",invalid:"服务地址无效"}[data.pilot.status] : "待检查"], ["就绪探测","/readyz"],["模型接口","/api/model"],["推理接口","/api/chat"],["凭据来源","部署配置"]])}<a href="#/resources/datasources" data-ai-dismiss>打开数据源管理</a></div>`));
  const filter = root.querySelector<HTMLSelectElement>("[data-ai-environment]");
  const search = root.querySelector<HTMLInputElement>("[data-ai-search]");
  const rows = [...root.querySelectorAll<HTMLElement>("[data-ai-model-row]")];
  const update = () => {
    for (const row of rows) row.hidden = Boolean(filter?.value && row.dataset.aiModelRow !== filter.value) ||
      !searchMatches(search?.value ?? "", row.dataset.aiSearchText);
    const count = rows.filter(row => !row.hidden).length;
    const label = root.querySelector("[data-ai-count]");
    const empty = root.querySelector<HTMLElement>("[data-ai-empty]");
    if (label) label.textContent = `${count} 个范围`;
    if (empty) empty.hidden = count > 0;
  };
  if (filter) {
    const initial = params.get("environment") ?? "";
    if (Array.from(filter.options).some(option => option.value === initial)) filter.value = initial;
    filter.addEventListener("change", () => {
      updateWorkspaceQuery("environment", filter.value);
      update();
    });
  }
  search?.addEventListener("input", update);
  root.querySelector("[data-ai-clear]")?.addEventListener("click", () => {
    if (filter) filter.value = "";
    updateWorkspaceQuery("environment", "");
    if (search) { search.value = ""; search.focus(); }
    update();
  });
  update();
  for (const trigger of root.querySelectorAll<HTMLButtonElement>("[data-ai-probe], [data-ai-edit], [data-ai-publish]")) {
    trigger.addEventListener("click", () => {
      if (trigger.dataset.aiEdit !== undefined) { openEditor(dialog, data, reload, Number(trigger.dataset.aiEdit)); return; }
      const model = data.models[Number(trigger.dataset.aiProbe ?? trigger.dataset.aiPublish)];
      if (!model) return;
      if (trigger.dataset.aiProbe !== undefined) {
        aiDialog(dialog, "测试范围连接", `<div class="dialog-body">${scopeDetails([["范围", scopeSummary(model)],["服务地址",model.service],["当前绑定模型",model.model]])}
          <p role="status" class="feedback"></p></div>`);
        const status = dialog.querySelector("[role=status]");
        if (status) void showResult(trigger, status, async () => {
          await requestAI(`${endpoint}?action=recheck`, scopeOf(model));
          const {models} = await requestAI<{models: string[]}>(`${endpoint}?action=discover`, scopeOf(model));
          status.textContent = `服务已就绪 · ${models.length} 个模型`;
        });
        return;
      }
      const entry = connectionFor(data, model);
      if (!entry) return;
      const { desired, effective, effectiveVersion } = entry.data;
      const before = effectiveVersion ? effective : { fast: model.model, accurate: model.model, disabled: false };
      aiDialog(dialog, "确认发布配置", `<div class="dialog-body">${scopeDetails([["影响范围",scopeSummary(model)],
        ["当前生效", effectiveVersion ? `v${effectiveVersion}` : "绑定默认模型"],["待发布",`草稿 v${entry.version}`]])}
        <fieldset class="group"><legend>生效值 → 草稿值</legend>${scopeDetails([
          ["快速模型", `${before.fast || "Holmes 默认模型"} → ${desired.fast || "—"}`],
          ["深度模型", `${before.accurate || "Holmes 默认模型"} → ${desired.accurate || "—"}`],
          ["调用状态", `${before.disabled ? "暂停" : "启用"} → ${desired.disabled ? "暂停" : "启用"}`],
        ])}</fieldset><p role="status" class="feedback"></p></div>
        ${dialogFooter('<button class="btn primary" type="button" data-ai-confirm>确认发布</button>')}`);
      const confirm = dialog.querySelector<HTMLButtonElement>("[data-ai-confirm]");
      const feedback = dialog.querySelector("[role=status]");
      if (confirm && feedback) confirm.addEventListener("click", () => void showResult(confirm, feedback, async () => {
        await requestAI(`${endpoint}?action=publish`, {...scopeOf(model),version: entry.version});
        dialog.close();
        reload();
      }));
    });
  }

}

type SourceChoice = { scope: Scope; name: string };

async function openBinding(dialog: HTMLDialogElement, reload: () => void, initialSource = "zabbix", initialID = "") {
  aiDialog(dialog, "新增范围绑定", `<form class="ai-form ai-binding-form">
    <div class="dialog-body"><fieldset class="group">
      <legend>数据范围</legend>
      <label>绑定类型<select name="source">${Object.entries(sources).map(([code, name]) =>
        `<option value="${h(code)}" ${code === initialSource ? "selected" : ""}>${h(name)}</option>`).join("")}</select></label>
      <div data-binding-fields></div>
    </fieldset>
    <fieldset class="group">
      <legend>Holmes 模型</legend>
      <div class="grid-2 discovery">
        <label>模型别名<select name="model" required disabled><option value="">请先发现模型</option></select></label>
        <button class="btn" type="button" data-binding-discover>发现模型</button>
      </div>
      <p class="ai-binding-feedback feedback" role="status"></p>
    </fieldset></div>
    <footer class="dialog-foot"><button class="btn" type="button" data-binding-cancel>取消</button>
      <button class="btn primary" type="submit" disabled>保存并启用绑定</button></footer>
  </form>`);
  const form = dialog.querySelector<HTMLFormElement>(".ai-binding-form");
  const fields = form?.querySelector<HTMLElement>("[data-binding-fields]");
  const source = form?.querySelector<HTMLSelectElement>("[name=source]");
  const model = form?.querySelector<HTMLSelectElement>("[name=model]");
  const discover = form?.querySelector<HTMLButtonElement>("[data-binding-discover]");
  const save = form?.querySelector<HTMLButtonElement>("[type=submit]");
  const status = form?.querySelector<HTMLElement>("[role=status]");
  if (!form || !fields || !source || !model || !discover || !save || !status) return;
  form.querySelector("[data-binding-cancel]")?.addEventListener("click", () => dialog.close());
  let choices: SourceChoice[] = [];
  let selectionVersion = 0;
  let saving = false;
  const feedback = (message: string, state = "") => {
    status.textContent = message;
    status.dataset.state = state;
    status.dataset.tone = state === "error" ? "bad" : "";
  };
  const updateSave = () => {
    const needsSource = source.value === "zabbix" || source.value === "alertmanager";
    const picker = fields.querySelector<HTMLSelectElement>("[name=sourceId]");
    save.disabled = saving || !model.value || (needsSource && !picker?.value) || !form.checkValidity();
  };
  const resetModel = (placeholder: string) => {
    model.replaceChildren(new Option(placeholder, ""));
    model.disabled = true;
    updateSave();
  };
  const changeSource = async (selectedID = "") => {
    const version = ++selectionVersion;
    const kind = source.value;
    choices = [];
    resetModel("请先发现模型");
    feedback("");
    if (kind === "zabbix" || kind === "alertmanager") {
      fields.textContent = "正在读取已接入的数据源…";
      try {
        const records = kind === "zabbix" ? (await apiClient.listZabbixSources()).items :
          (await apiClient.listObservabilitySources()).items.filter(item => item.kind === "alertmanager");
        if (version !== selectionVersion || !form.isConnected) return;
        choices = records.filter(item => item.enabled).map(item => ({
          scope: {source: kind, sourceId: item.id, ...item.scope}, name: item.name || "未命名数据源",
        }));
        if (!choices.length) {
          fields.innerHTML = '<p>当前没有已启用的数据源。<a href="#/resources/datasources">打开数据源管理</a></p>';
          return;
        }
        fields.innerHTML = `<label>数据源<select name="sourceId" required><option value="">请选择数据源</option>
          ${choices.map(({scope, name}) => `<option value="${h(scope.sourceId)}">${h(name)}${scope.environmentId ? ` · ${h(scope.environmentId)}` : ""}</option>`).join("")}</select></label>
          <dl class="scope" data-binding-scope hidden></dl>
          ${kind === "alertmanager" ? '<label>告警命名空间<input name="namespace" maxlength="128" placeholder="告警没有 namespace 时留空"></label>' : ""}`;
        const picker = fields.querySelector<HTMLSelectElement>("[name=sourceId]");
        const summary = fields.querySelector<HTMLElement>("[data-binding-scope]");
        if (picker && summary) {
          const renderScope = () => {
            const scope = choices.find(choice => choice.scope.sourceId === picker.value)?.scope;
            summary.hidden = !scope;
            summary.innerHTML = scope ? [
              ["环境", scope.environmentId], ["业务", scope.businessId],
              ["集群", scope.clusterId], ["数据源 ID", scope.sourceId],
            ].filter(([, value]) => value).map(([name, value]) => `<dt>${h(name)}</dt><dd>${h(value)}</dd>`).join("") : "";
            updateSave();
          };
          if (choices.some(choice => choice.scope.sourceId === selectedID)) picker.value = selectedID;
          picker.addEventListener("change", renderScope);
          renderScope();
        }
      } catch {
        if (version === selectionVersion) fields.textContent = "无法读取数据源。请检查数据源管理的权限与接入状态。";
      }
      return;
    }
    fields.innerHTML = kind === "wiki" ? "<p>知识问答使用部署侧配置的独立 Holmes 服务。</p>" :
      `<label>集群 ID<input name="clusterId" required maxlength="128"></label>
       <label>命名空间<input name="namespace" maxlength="128" placeholder="记录没有命名空间时留空"></label>
       ${kind === "incident" ? '<label>环境 ID<input name="environmentId" maxlength="128"></label><label>业务 ID<input name="businessId" maxlength="128"></label>' : ""}`;
  };
  form.addEventListener("input", updateSave);
  form.addEventListener("change", updateSave);
  source.addEventListener("change", () => void changeSource());
  discover.addEventListener("click", async () => {
    const version = selectionVersion;
    discover.disabled = true;
    resetModel("正在读取模型…");
    feedback("正在连接 Holmes…");
    try {
      const { models } = await requestAI<{models: string[]}>(`${endpoint}?action=discover-binding`, {source: source.value});
      if (version !== selectionVersion || !form.isConnected) return;
      if (!models.length) {
        resetModel("暂无模型别名");
        feedback("Holmes 返回的模型列表为空。请检查其 modelList 配置后重新发现。", "error");
        return;
      }
      model.replaceChildren(...models.map(alias => new Option(alias, alias)));
      model.value = models.includes("accurate-model") ? "accurate-model" : models[0] ?? "";
      model.disabled = false;
      feedback(`已发现 ${models.length} 个模型别名`, "success");
      updateSave();
    } catch (error) {
      if (version !== selectionVersion || !form.isConnected) return;
      resetModel("模型发现失败");
      feedback(error instanceof Error ? error.message : "无法读取模型，请重试。", "error");
    } finally {
      discover.disabled = false;
    }
  });
  form.addEventListener("submit", async event => {
    event.preventDefault();
    if (save.disabled) return;
    const kind = source.value;
    const values = new FormData(form);
    const field = (name: string) => String(values.get(name) ?? "").trim();
    const picked = choices.find(choice => choice.scope.sourceId === field("sourceId"));
    if ((kind === "zabbix" || kind === "alertmanager") && !picked) {
      feedback("请选择已接入并启用的数据源。", "error");
      return;
    }
    const scope: Scope = picked ? {...picked.scope} : {
      source: kind, sourceId: kind === "wiki" ? "published-excerpts" : field("clusterId"),
      environmentId: field("environmentId"), businessId: field("businessId"), clusterId: field("clusterId"),
    };
    scope.namespace = field("namespace");
    saving = true;
    updateSave();
    const groups = form.querySelectorAll("fieldset");
    for (const group of groups) group.disabled = true;
    feedback("正在保存绑定…");
    try {
      await requestAI(`${endpoint}?action=bind`, {...scope, model: model.value});
      if (!form.isConnected) return;
      updateWorkspaceQuery("environment", scope.environmentId);
      dialog.close();
      reload();
    } catch (error) {
      feedback(error instanceof Error ? error.message : "保存失败，请重试。", "error");
    } finally {
      saving = false;
      for (const group of groups) group.disabled = false;
      updateSave();
    }
  });
  await changeSource(initialID);
}
