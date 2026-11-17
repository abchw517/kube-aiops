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
      status.textContent = error instanceof Error ? error.message : "请求失败";
    } finally {
      form.inert = false;
    }
  });
}

async function wikiRequest(url: string, method: string, body?: unknown): Promise<Response> {
  const response = await fetch(url, {
    method, body: body instanceof FormData ? body : body === undefined ? undefined : JSON.stringify(body),
    headers: body !== undefined && !(body instanceof FormData) ? {"Content-Type": "application/json"} : undefined,
  });
  if (!response.ok) throw new Error((await response.text()).trim() || "请求失败");
  return response;
}

export function renderWikiTools(admin: boolean): string {
  return `<button type="button" class="button" data-open-wiki-ask>AI 知识问答</button>
  <dialog data-wiki-dialog><section class="panel"><h2>AI 知识问答</h2>
    <div data-wiki-dialog-log role="log"></div>
    <form data-wiki-ask class="wiki-import-form">
      <label>问题<textarea name="question" required maxlength="2000"></textarea></label>
      <p role="status"></p><div><button type="submit" class="button">发送</button>
      <button type="button" data-close-wiki-ask class="button secondary">关闭</button></div>
    </form></section></dialog>
  <section class="panel"><details><summary>批量导入</summary><form data-wiki-batch class="wiki-import-form">
    <label>集群实例<input name="cluster" required value="local"></label><label>命名空间<input name="namespace" required></label>
    <label>环境<input name="environment" required></label><label>业务线<input name="business" required></label>
    <label>类型<select name="documentType"><option>排障手册</option><option>故障复盘</option><option>操作规程</option><option>常见问题</option></select></label>
    <label>文档<input name="files" type="file" accept=".md,.markdown,.txt,.docx" multiple required></label>
    <p role="status"></p><button class="button" type="submit">生成逐文件预览</button></form><div data-wiki-batch-list></div></details></section>
  <section class="panel"><details><summary>远端 Wiki 同步</summary><form data-wiki-sync class="wiki-import-form">
    <label>来源编号<input name="id" pattern="[a-z0-9][a-z0-9-]{0,62}" required></label>
    ${admin ? '<label>目录 URL<input type="url" name="url"></label><label>集群实例<input name="cluster" value="local"></label><label>命名空间<input name="namespace"></label><button type="submit" value="source" class="button secondary">注册来源</button>' : ""}
    <label>Wiki 账号<input name="username" autocomplete="off"></label>
    <label>Wiki 密码<input type="password" name="password" autocomplete="new-password"></label>
    <button type="submit" value="load" class="button secondary">读取本人绑定</button>
    <button type="submit" value="check" class="button secondary">测试连接</button>
    <button type="submit" value="credential" class="button secondary">保存绑定</button>
    <button type="submit" value="sync" class="button">立即同步</button>
    <label>定时间隔（分钟，0 为关闭）<input type="number" name="interval" min="0" max="10080" value="0"></label>
    <label>Cron（可选）<input name="cron" placeholder="0 2 * * *"></label>
    <label>时区<input name="zone" value="Asia/Shanghai"></label>
    <button type="submit" value="schedule" class="button secondary">保存定时计划</button><p role="status"></p>
  </form></details></section>`;
}

export function bindWikiTools(root: HTMLElement): void {
  const dialog = root.querySelector<HTMLDialogElement>("[data-wiki-dialog]");
  const log = root.querySelector<HTMLElement>("[data-wiki-dialog-log]");
  const prior: string[] = [];
  dialog?.showModal();
  root.querySelector("[data-open-wiki-ask]")?.addEventListener("click", () => dialog?.showModal());
  root.querySelector("[data-close-wiki-ask]")?.addEventListener("click", () => dialog?.close());
  bindWikiTool(root, "[data-wiki-ask]", async (fields, status) => {
    const query = new URLSearchParams(window.location.hash.split("?", 2)[1]);
    const question = String(fields.get("question"));
    const response = await wikiRequest("/api/v1/wiki/ask", "POST", {
      question, prior: prior.slice(-8), environment: query.get("environment") ?? "",
      business: query.get("business") ?? "", documentType: query.get("documentType") ?? "",
    });
    const data = await response.json() as {answer: string; citations: {knowledgeId: string; title: string; revision: number}[]};
    prior.push(question);
    const item = document.createElement("section");
    const prompt = document.createElement("p");
    prompt.textContent = question;
    const answer = document.createElement("p");
    answer.className = "wiki-document-body";
    answer.textContent = data.answer;
    item.append(prompt, answer);
    for (const citation of data.citations) {
      const link = document.createElement("a");
      link.href = `#/wiki/${encodeURIComponent(citation.knowledgeId)}?revision=${citation.revision}`;
      link.textContent = citation.title;
      item.append(link, document.createTextNode(" "));
    }
    log?.append(item);
    const field = root.querySelector<HTMLTextAreaElement>("[data-wiki-ask] textarea");
    if (field) field.value = "";
    status.textContent = "";
  });
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
        button.textContent = "确认发布此文档";
        button.addEventListener("click", async () => {
          button.disabled = true;
          try {
            await wikiRequest("/api/v1/wiki/imports", "POST", {document: row.document, publish: true});
            button.textContent = "已发布";
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
  const sourceInput = root.querySelector<HTMLInputElement>("[data-wiki-sync] [name=id]");
  sourceInput?.addEventListener("change", () => {
    const load = root.querySelector<HTMLButtonElement>('[data-wiki-sync] [value="load"]');
    if (sourceInput.checkValidity()) load?.click();
  });
  bindWikiTool(root, "[data-wiki-sync]", async (fields, status, event) => {
    const base = `/api/v1/wiki/sources/${encodeURIComponent(String(fields.get("id")))}`;
    const action = event.submitter?.getAttribute("value");
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
      const response = await wikiRequest(`${base}/sync`, "POST");
      const result = await response.json() as {saved: number; skipped: number; failed: number; missing: number};
      status.textContent = `已保存 ${result.saved}，已跳过 ${result.skipped}，失败 ${result.failed}，新增来源缺失 ${result.missing}。`;
    } else if (action === "schedule") {
      await wikiRequest(`${base}/schedule`, "PUT", {intervalMinutes: Number(fields.get("interval")), cron: fields.get("cron"), timeZone: fields.get("zone")});
      status.textContent = "定时计划已保存。";
    }
  });
  bindWikiTool(root, "[data-wiki-revision]", async (fields, status) => {
    const id = window.location.hash.split(/[/?]/)[2];
    if (!id) return;
    const response = await wikiRequest(`/api/v1/wiki/knowledge/${encodeURIComponent(id)}/revisions`, "POST", {
      expectedRevision: Number(fields.get("revision")), title: fields.get("title"), body: fields.get("body"),
    });
    const doc = await response.json() as {revision: number};
    window.location.hash = `#/wiki/${encodeURIComponent(id)}?revision=${doc.revision}`;
    status.textContent = "修订已保存。";
  });
}
