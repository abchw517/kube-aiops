import { escapeHtml as h } from "../../components/html.ts";
import { knowledgeLink, wikiSectionLabel } from "./governance.ts";
import { bindWikiRuns, renderWikiRuns } from "./wiki-runs.ts";

type WikiFormAction = (fields: FormData, status: HTMLElement, event: SubmitEvent) => Promise<void>;

function bindWikiTool(root: HTMLElement, selector: string, action: WikiFormAction): void {
  const form = root.querySelector<HTMLFormElement>(selector);
  const status = form?.querySelector<HTMLElement>("[role=status]");
  if (!form || !status) return;
  form.addEventListener("submit", async (event) => {
    event.preventDefault();
    if (form.inert) return;
    form.inert = true;
    status.textContent = "正在处理…";
    try {
      await action(new FormData(form), status, event);
    } catch (error) {
      if (form.isConnected && !(error instanceof DOMException && error.name === "AbortError")) {
        status.textContent = error instanceof Error ? error.message : "请求失败";
      }
    } finally {
      form.inert = false;
    }
  });
}

async function wikiRequest(url: string, method: string, body?: unknown, signal?: AbortSignal): Promise<Response> {
  const response = await fetch(url, {
    signal, method, body: body instanceof FormData ? body : body === undefined ? undefined : JSON.stringify(body),
    headers: body !== undefined && !(body instanceof FormData) ? {"Content-Type": "application/json"} : undefined,
  });
  if (!response.ok) throw new Error((await response.text()).trim() || "请求失败");
  return response;
}

export type AssistantFinding = { id: string; cluster: string; namespace?: string };

export function renderKnowledgeAsk(assistant = false, context?: AssistantFinding): string {
  const titleId = assistant ? "assistant-title" : "wiki-ask-title";
  const header = `<header class="wiki-toolbar"><h2 id="${titleId}">${assistant ? "诊断助手" : "AI 知识问答"}</h2><button type="button" data-close-wiki-ask class="button secondary">关闭</button></header>`;
  const history = '<div data-wiki-dialog-log role="log" aria-label="对话记录"></div>';
  const form = `
    <form data-wiki-ask class="wiki-import-form">
      <label>问题<textarea name="question" rows="${assistant ? 2 : 4}" required maxlength="2000" placeholder="例如：如何排查镜像拉取失败？"></textarea></label>
      <p role="status"></p><div><button type="submit" class="button">发送</button></div>
    </form>`;
  if (!assistant) return `<dialog class="wiki-dialog" data-wiki-dialog aria-labelledby="${titleId}">${header}${history}${form}</dialog>`;

  return `<dialog class="wiki-dialog ai-assistant-drawer" data-wiki-dialog aria-labelledby="${titleId}">${header}
    <div class="assistant-scroll">
      <section class="assistant-scope">
        <div data-assistant-knowledge${context ? " hidden" : ""}>
          <h3>运维知识问答</h3><p>从授权的已发布运维知识查找排障建议。</p>
          <nav class="system-actions" aria-label="诊断快捷入口"><a class="button secondary" href="#/diagnostics">诊断总览</a><a class="button secondary" href="#/wiki">知识检索</a></nav>
        </div>
        ${context ? `<div data-assistant-context><h3>当前诊断</h3>
          <p>${h(context.cluster)}${context.namespace ? ` / ${h(context.namespace)}` : ""} · ${h(context.id)}</p>
          <button type="button" class="button secondary" data-assistant-clear>清除诊断上下文</button></div>` : ""}
      </section>
      <div class="assistant-prompts" aria-label="示例问题">
        <div data-assistant-general-prompts${context ? " hidden" : ""}>
          <button type="button" class="button secondary" data-assistant-prompt>网关 5xx 如何定位？</button>
          <button type="button" class="button secondary" data-assistant-prompt>主机内存压力怎么排查？</button>
          <button type="button" class="button secondary" data-assistant-prompt>服务副本不可用先看什么？</button>
        </div>
        ${context ? `<div data-assistant-finding-prompts>
          <button type="button" class="button secondary" data-assistant-prompt>当前证据说明了什么？</button>
          <button type="button" class="button secondary" data-assistant-prompt>接下来要核对哪些证据？</button>
        </div>` : ""}
      </div>
      ${history}
    </div>
    ${form}
  </dialog>`;
}

export function renderWikiTools(admin: boolean): string {
  return `${renderKnowledgeAsk()}
  <details class="panel wiki-tool"><summary>批量导入</summary><form data-wiki-batch class="wiki-import-form">
    <label>集群实例<input name="cluster" required value="local"></label><label>命名空间<input name="namespace" required></label>

    <label>类型<select name="documentType"><option>排障手册</option><option>故障复盘</option><option>操作规程</option><option>常见问题</option></select></label>
    <label>文档<input name="files" type="file" accept=".md,.markdown,.txt,.docx" multiple required></label>
    <p role="status"></p><button class="button" type="submit">生成逐文件预览</button></form><div data-wiki-batch-list></div></details>
  <details class="panel wiki-tool"><summary>远端 Wiki 同步</summary><form data-wiki-sync class="wiki-import-form">
    <label>来源编号<input name="id" pattern="[a-z0-9][a-z0-9\\-]{0,62}" required></label>
    ${admin ? '<label>目录 URL<input type="url" name="url"></label><label>集群实例<input name="cluster" value="local"></label><label>命名空间<input name="namespace"></label><button type="submit" value="source" class="button secondary">注册来源</button>' : ""}
    <label for="wiki-sync-username">Wiki 账号</label><input id="wiki-sync-username" name="username" autocomplete="off">
    <label>Wiki 密码<input type="password" name="password" autocomplete="new-password"></label>
    <button type="submit" value="load" class="button secondary">读取本人绑定</button>
    <button type="submit" value="check" class="button secondary">测试连接</button>
    <button type="submit" value="credential" class="button secondary">保存绑定</button>
    <button type="submit" value="sync" class="button">立即同步</button>
    ${admin ? '<button type="submit" value="disable" class="button secondary catalog-danger">停用来源</button>' : ""}
    <label>定时间隔（分钟，0 为关闭）<input type="number" name="interval" min="0" max="10080" value="0"></label>
    <label>Cron（可选）<input name="cron" placeholder="0 2 * * *"></label>
    <label>时区<input name="zone" value="Asia/Shanghai"></label>
    <button type="submit" value="schedule" class="button secondary">保存定时计划</button><p role="status"></p>
  </form>${renderWikiRuns()}</details>`;
}

export function bindKnowledgeAsk(root: HTMLElement, finding?: AssistantFinding, findingTrigger?: HTMLElement | null): void {
  const dialog = root.querySelector<HTMLDialogElement>("[data-wiki-dialog]");
  const log = root.querySelector<HTMLElement>("[data-wiki-dialog-log]");
  const field = dialog?.querySelector<HTMLTextAreaElement>("textarea");
  const prior: string[] = [];
  let pending: AbortController | undefined;
  let context = finding;
  const conversation: {role: "user" | "assistant"; text: string}[] = [];
  const clear = root.querySelector<HTMLButtonElement>("[data-assistant-clear]");
  clear?.addEventListener("click", () => {
    pending?.abort();
    context = undefined;
    conversation.length = 0;
    prior.length = 0;
    log?.replaceChildren();
    root.querySelector("[data-assistant-context]")?.remove();
    root.querySelector("[data-assistant-finding-prompts]")?.remove();
    for (const panel of root.querySelectorAll<HTMLElement>("[data-assistant-knowledge], [data-assistant-general-prompts]")) panel.hidden = false;
    if (field) { field.value = ""; field.focus(); }
  });
  dialog?.addEventListener("close", () => pending?.abort());
  const open = () => {
    if (!dialog || dialog.open) return;
    const search = root.querySelector<HTMLInputElement>("#wiki-search-term");
    if (field && !field.value && search?.value) field.value = search.value;
    dialog.showModal();
    field?.focus();
  };
  root.querySelector("[data-open-wiki-ask]")?.addEventListener("click", open);
  findingTrigger?.addEventListener("click", open);
  for (const prompt of root.querySelectorAll("[data-assistant-prompt]")) prompt.addEventListener("click", () => {
    if (!field) return;
    field.value = prompt.textContent?.trim() ?? "";
    field.focus();
  });
  root.querySelector("[data-close-wiki-ask]")?.addEventListener("click", () => dialog?.close());
  bindWikiTool(root, "[data-wiki-ask]", async (fields, status) => {
    const location = window.location.hash;
    const query = new URLSearchParams(location.startsWith("#/wiki") ? location.split("?", 2)[1] : "");
    const question = String(fields.get("question"));
    const controller = new AbortController();
    pending = controller;
    const cancel = () => controller.abort();
    window.addEventListener("hashchange", cancel, { once: true });
    let data: {answer: string; citations: {knowledgeId: string; title: string; revision: number; section?: string}[]};
    try {
      if (context) {
        const endpoint = `/api/v1/admin/finding-analysis?${new URLSearchParams({id: context.id, cluster: context.cluster})}`;
        const details = await wikiRequest(endpoint, "GET", undefined, controller.signal);
        const { history } = await details.json() as {history: {revision: number}[]};
        const messages = [...conversation.slice(-18), {role: "user", text: question}];
        const response = await wikiRequest(endpoint, "POST", {action: "chat", revision: history[0]?.revision ?? 0, messages}, controller.signal);
        const { reply } = await response.json() as {reply: string};
        data = {answer: reply, citations: []};
      } else {
        const response = await wikiRequest("/api/v1/wiki/ask", "POST", {
          question, prior: prior.slice(-8), environment: query.get("environment") ?? "",
          business: query.get("business") ?? "", documentType: query.get("documentType") ?? "",
          resourceType: query.get("resourceType") ?? "",
        }, controller.signal);
        data = await response.json() as typeof data;
      }
    } finally {
      window.removeEventListener("hashchange", cancel);
      pending = undefined;
      if (controller.signal.aborted) status.textContent = "";
    }
    if (controller.signal.aborted || !dialog?.open || !status.isConnected || window.location.hash !== location) return;
    if (context) conversation.push({role: "user", text: question}, {role: "assistant", text: data.answer.slice(0, 2000)});
    else prior.push(question);
    const item = document.createElement("section");
    const prompt = document.createElement("p");
    prompt.className = "assistant-question";
    prompt.textContent = question;
    const reply = document.createElement("div");
    reply.className = "assistant-reply";
    const answer = document.createElement("p");
    answer.className = "wiki-document-body";
    answer.textContent = data.answer;
    reply.append(answer);
    item.append(prompt, reply);
    for (const citation of data.citations) {
      const link = document.createElement("a");
      link.href = knowledgeLink(citation.knowledgeId, citation.revision, citation.section);
      link.textContent = `${citation.title} · v${citation.revision}${citation.section ? ` · ${wikiSectionLabel(citation.section)}` : ""}`;
      reply.append(link, document.createTextNode(" "));
    }
    log?.append(item);
    const scrollArea = log?.closest<HTMLElement>(".assistant-scroll") ?? log;
    if (scrollArea) scrollArea.scrollTop = scrollArea.scrollHeight;
    if (field) field.value = "";
    status.textContent = "";
  });
}

export function bindWikiTools(root: HTMLElement): void {
  bindKnowledgeAsk(root);
  bindWikiTool(root, "[data-wiki-batch]", async (fields, status) => {
    const response = await wikiRequest("/api/v1/wiki/imports/files", "POST", fields);
    const rows = await response.json() as {filename: string; error?: string; document?: {title: string; body: string}}[];
    const list = root.querySelector<HTMLElement>("[data-wiki-batch-list]");
    list?.replaceChildren();
    for (const row of rows) {
      const entry = document.createElement("details");
      const name = document.createElement("summary");
      name.textContent = row.filename;
      entry.append(name);
      if (row.document) {
        const preview = document.createElement("pre");
        preview.className = "wiki-document-body";
        preview.textContent = row.document.body;
        const button = document.createElement("button");
        button.type = "button";
        button.className = "button";
        button.textContent = "创建草稿";
        button.addEventListener("click", async () => {
          button.disabled = true;
          try {
            const imported = await wikiRequest("/api/v1/wiki/candidates", "POST", {document: row.document});
            const candidate = await imported.json() as {id: string};
            window.location.hash = `#/wiki/draft/${candidate.id}`;
          } catch (error) {
            status.textContent = error instanceof Error ? error.message : "发布失败";
            button.disabled = false;
          }
        });
        entry.append(preview, button);
      } else {
        const detail = document.createElement("p");
        detail.textContent = row.error ?? "转换失败";
        entry.append(detail);
      }
      list?.append(entry);
    }
    status.textContent = "转换完成，请逐项确认。";
  });
  const reloadRuns = bindWikiRuns(root, wikiRequest);
  const sourceInput = root.querySelector<HTMLInputElement>("[data-wiki-sync] [name=id]");
  sourceInput?.addEventListener("change", () => {
    const load = root.querySelector<HTMLButtonElement>('[data-wiki-sync] [value="load"]');
    for (const [name, value] of Object.entries({ username: "", password: "", interval: "0", cron: "", zone: "Asia/Shanghai" })) {
      const field = sourceInput.form?.elements.namedItem(name);
      if (field instanceof HTMLInputElement) field.value = value;
    }
    if (sourceInput.checkValidity()) load?.click();
  });
  bindWikiTool(root, "[data-wiki-sync]", async (fields, status, event) => {
    const base = `/api/v1/wiki/sources/${encodeURIComponent(String(fields.get("id")))}`;
    const action = event.submitter?.getAttribute("value") ?? "load";
    if (action === "source") {
      await wikiRequest(base, "PUT", {url: fields.get("url"), cluster: fields.get("cluster"), namespace: fields.get("namespace")});
      status.textContent = "来源已注册。";
    } else if (action === "check") {
      const response = await wikiRequest(`${base}/check`, "POST", {username: fields.get("password") ? fields.get("username") : "", password: fields.get("password")});
      const result = await response.json() as {firstPageCount: number};
      status.textContent = `连接成功，首页可读文档 ${result.firstPageCount} 条。`;
    } else if (action === "credential") {
      await wikiRequest(`${base}/credential`, "PUT", {username: fields.get("username"), password: fields.get("password")});
      const password = root.querySelector<HTMLInputElement>("[data-wiki-sync] [name=password]");
      if (password) password.value = "";
      status.textContent = "已保存本人绑定。";
    } else if (action === "load") {
      const response = await wikiRequest(`${base}/credential`, "GET");
      const data = await response.json() as {username?: string; configured: boolean};
      const username = root.querySelector<HTMLInputElement>("[data-wiki-sync] [name=username]");
      if (username) username.value = data.username ?? "";
      status.textContent = data.configured ? "密码已配置。" : "请绑定本人 Wiki 账号。";
      const plan = await (await wikiRequest(`${base}/schedule`, "GET")).json() as {intervalMinutes: number; cron?: string; timeZone?: string; nextRun?: number};
      const planFields = {interval: plan.intervalMinutes, cron: plan.cron ?? "", zone: plan.timeZone || "Asia/Shanghai"};
      for (const [name, value] of Object.entries(planFields)) {
        const field = sourceInput?.form?.elements.namedItem(name);
        if (field instanceof HTMLInputElement) field.value = String(value);
      }
      if (plan.nextRun) status.textContent += ` 下次运行：${new Date(plan.nextRun * 1000).toLocaleString("zh-CN")}`;
    } else if (action === "sync") {
      try {
        const response = await wikiRequest(`${base}/sync`, "POST");
        const result = await response.json() as {saved: number; skipped: number; failed: number; missing: number};
        status.textContent = `已生成候选 ${result.saved}，已跳过 ${result.skipped}，失败 ${result.failed}，新增来源缺失 ${result.missing}。`;
      } finally { await reloadRuns?.(); }
    } else if (action === "disable") {
      if (!window.confirm("停用后该来源的内容将退出普通阅读与推荐，确认继续？")) return;
      await wikiRequest(base, "DELETE");
      status.textContent = "来源已停用。";
      await reloadRuns?.();
    } else if (action === "schedule") {
      await wikiRequest(`${base}/schedule`, "PUT", {intervalMinutes: Number(fields.get("interval")), cron: fields.get("cron"), timeZone: fields.get("zone")});
      status.textContent = "定时计划已保存。";
    }
  });
}
