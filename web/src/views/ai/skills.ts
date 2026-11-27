import { escapeHtml as h } from "../../components/html.ts";
import { aiDialog, endpoint, requestAI, scopeOf, scopeSummary, scopeText, showResult, skillsEndpoint, type Entry, type Scope } from "./services.ts";
import { aiDialogMarkup, dialogFooter, icon, options, pill, scopeDetails, searchMatches, stat, updateWorkspaceQuery } from "./workspace.ts";

type Skill = {
  scope: Scope; name: string; applies: string; keyword: string; body: string;
  manualId?: string; manualRevision?: number; author: string; reviewer?: string; status: "review" | "enabled" | "disabled";
};
type SkillData = { skills: Entry<Skill>[]; actor: string };
type Manual = { knowledgeId: string; revision?: number; title: string; metadata?: {sections?: {checks?: string}; applicability?: string} };

const labels: Record<Skill["status"], string> = {review:"待审核",enabled:"已启用",disabled:"已停用"};

export async function loadAISkills() {
  const [data,bindings] = await Promise.all([requestAI<SkillData>(skillsEndpoint), requestAI<{models: Scope[]}>(endpoint)]);
  const scopes = bindings.models.filter(scope => scope.source !== "wiki");
  const review = data.skills.filter(entry => entry.data.status === "review");
  const mine = review.filter(entry => entry.data.author !== data.actor);
  const rows = data.skills.map(({data:s,version}, index) => {
    const canReview = s.status === "review" && s.author !== data.actor;
    return `<article class="item" data-skill-row="${index}">
      ${icon(s.manualId ? "≡" : "✧","lilac")}<div><h3>${h(s.name)}</h3>
      <small>${s.manualId ? "手册转化" : "Markdown 上传"} · ${h(s.author)}提交 · ${s.status === "review" ? "草稿" : `v${version}`}</small>
      <div class="chips">${pill(labels[s.status],s.status === "review" ? "warn" : s.status === "enabled" ? "good" : "")}
      <span class="scope-chip">${h(scopeSummary(s.scope))}</span></div>
      ${s.keyword ? `<div class="models">匹配关键词 <b>${h(s.keyword)}</b></div>` : ""}</div>
      <div class="actions"><button class="btn ${canReview ? "primary" : ""} small" type="button" data-skill-view="${index}">${canReview ? "审核技能" : "查看详情"}</button></div></article>`;
  }).join("");
  return { data, scopes, html: `<div class="portal-ai-page ai-workspace">
    <div class="page-head row between"><div><h1>AI 技能</h1><p class="subtitle">把可复用的排障步骤交给 Holmes，在匹配范围的诊断中引用。</p></div>
    <div class="actions"><button type="button" class="btn" data-skill-new="manual" ${scopes.length ? "" : "disabled"}>从手册转化</button>
    <button type="button" class="btn primary" data-skill-new="upload" ${scopes.length ? "" : "disabled"}>＋ 上传技能</button></div></div>
    <section class="hero" aria-label="技能审核状态"><div class="row hero-head">${icon("✧","lilac")}<div><h2>技能审核</h2>
    <p class="muted">另一位管理员审核通过后，技能才会在对应范围内生效。</p></div></div>
    <div class="end"><div class="inline-stats">${stat(data.skills.length,"全部技能")}${stat(review.length,"待审核")}${stat(data.skills.filter(entry => entry.data.status === "enabled").length,"已启用")}</div>
    <div class="actions"><button class="btn small" type="button" data-review-filter>查看待审核</button></div></div></section>
    <div class="workspace"><section class="panel" aria-label="技能列表">
      <div class="row between panel-head"><h2>技能列表</h2><small data-skill-count>${data.skills.length} 项</small></div>
      <div class="tabs" aria-label="技能状态">${[["","全部"],...Object.entries(labels)].map(([key,label]) =>
        `<button type="button" aria-pressed="${!key}" data-skill-filter="${key}">${label}</button>`).join("")}</div>
      <div class="filters"><input class="search" type="search" data-skill-search aria-label="搜索技能" placeholder="搜索名称、关键词或范围">
        <select data-skill-scope aria-label="技能范围"><option value="">全部范围</option>${options(scopes.map((scope,index) => [String(index),scopeText(scope)]))}</select></div>
      ${rows}<div class="empty" data-skill-empty ${rows ? "hidden" : ""}>没有符合条件的技能。<div class="actions"><button class="btn small" type="button" data-skill-clear>清除筛选</button></div></div>
      ${scopes.length ? "" : '<p>添加非 Wiki 的 Holmes 范围绑定后可提交技能。 <a href="#/ai">查看绑定范围</a></p>'}</section>
      <aside class="stack" aria-label="技能操作">
      <section class="panel"><div class="side-title">${icon("✓","sand")}<h2>待我审核</h2></div>
        ${mine.length ? mine.map(entry => `<button class="release" type="button" data-skill-view="${data.skills.indexOf(entry)}"><b>${h(entry.data.name)}</b><small>${h(entry.data.author)}提交 · ${h(scopeSummary(entry.data.scope))}</small></button>`).join("") : '<p class="side-note">当前身份没有待审核项。</p>'}</section>
      <section class="panel"><div class="side-title">${icon("⌁")}<h2>技能何时生效</h2></div>
        <p class="side-note">绑定范围与关键词匹配时，深度调查和 Finding 初步复核会引用已启用的技能。也可在 Finding 会话中按名称请求使用。</p>
        <a href="#/ai">查看绑定范围</a></section></aside>
    </div>${aiDialogMarkup}</div>` };
}

function openSkillDraft(dialog: HTMLDialogElement, scopes: Scope[], mode: string, reload: () => void, initialManual?: string) {
  aiDialog(dialog, mode === "upload" ? "上传 AI 技能" : "从已发布手册转化", `<form class="ai-form"><div class="dialog-body">
    <fieldset class="group"><legend>来源与使用范围</legend>
    ${mode === "upload" ? '<label>Markdown 文件<input name="file" type="file" accept=".md,.markdown,text/markdown"></label><small>支持 SKILL.md，最多 12 KiB。也可直接填写正文。</small>' :
      '<div class="grid-2 discovery"><label>搜索已发布手册<input name="search" type="search" placeholder="输入手册名称"></label><button type="button" class="btn" data-find-manual>搜索手册</button></div><label>已发布手册<select name="manual" required></select></label>'}
    <label>适用范围<select name="scope" required><option value="">选择已绑定范围</option>${options(scopes.map((scope,i) => [String(i),scopeText(scope)]))}</select></label></fieldset>
    <fieldset class="group"><legend>技能内容</legend>
      <div class="grid-2"><label>技能名称<input name="name" required maxlength="60"></label>
      <label>匹配关键词<input name="keyword" maxlength="40" placeholder="留空时匹配整个范围"></label></div>
      <label>适用说明<input name="applies" maxlength="100"></label>
      <label>排障步骤<textarea name="body" rows="6" required maxlength="12000"></textarea></label></fieldset>
    <p class="feedback" role="status"></p></div>${dialogFooter('<button type="submit" class="btn primary">提交审核</button>')}</form>`);
  const form = dialog.querySelector<HTMLFormElement>("form");
  const status = form?.querySelector("[role=status]");
  const save = form?.querySelector<HTMLButtonElement>("[type=submit]");
  const body = form?.querySelector<HTMLTextAreaElement>("[name=body]");
  const name = form?.querySelector<HTMLInputElement>("[name=name]");
  const applies = form?.querySelector<HTMLInputElement>("[name=applies]");
  const scopeField = form?.querySelector<HTMLSelectElement>("[name=scope]");
  if (!form || !status || !save || !body || !name || !applies || !scopeField) return;
  const upload = form.querySelector<HTMLInputElement>("[name=file]");
  upload?.addEventListener("change", async () => {
    const file = upload.files?.[0];
    if (!file) return;
    try {
      if (file.size > 12*1024 || !/\.m(?:d|arkdown)$/i.test(file.name)) throw new Error("请选择不超过 12 KiB 的 Markdown 文件。");
      const source = await file.text();
      if (upload.files?.[0] !== file) return;
      body.value = source;
      if (!name.value) name.value = source.match(/^#\s+(.+)$/m)?.[1]?.slice(0, 60) || file.name.replace(/\.[^.]+$/, "").slice(0, 60);
      status.textContent = "";
    } catch (error) { status.textContent = error instanceof Error ? error.message : "读取技能包失败。"; }
  });
  const manual = form.querySelector<HTMLSelectElement>("[name=manual]");
  const find = form.querySelector<HTMLButtonElement>("[data-find-manual]");
  let manualRevision = 0;
  const fillManual = async (id: string) => {
    manualRevision = 0;
    if (!id || !manual) return;
    const doc = await requestAI<Manual>(`/api/v1/wiki/knowledge/${encodeURIComponent(id)}`);
    if (manual.value !== id) return;
    manualRevision = doc.revision ?? 1;
    name.value = doc.title.slice(0,60);
    applies.value = doc.metadata?.applicability?.slice(0,100) ?? "";
    body.value = doc.metadata?.sections?.checks ?? "";
    if (status) status.textContent = body.value ? "检查步骤已载入，可编辑后送审。" : "请补充该手册对应的只读检查步骤。";
  };
  if (manual && find) {
    const findManuals = () => void showResult(find, status, async () => {
      const q = String(new FormData(form).get("search") ?? "");
      const query = new URLSearchParams({ documentType: "排障手册", q });
      const { items } = await requestAI<{items: Manual[]}>(`/api/v1/wiki/knowledge?${query}`);
      manualRevision = 0;
      manual.replaceChildren(new Option("请选择手册", ""), ...items.map(d => new Option(`${d.title} · v${d.revision ?? 1}`,d.knowledgeId)));
      status.textContent = items.length ? "请选择手册。" : "未找到手册，请修改关键词。";
    });
    find.addEventListener("click", findManuals);
    form.querySelector<HTMLInputElement>("[name=search]")?.addEventListener("keydown", event => {
      if (event.key === "Enter") { event.preventDefault(); findManuals(); }
    });
    manual.addEventListener("change", () => void showResult(find,status,() => fillManual(manual.value)));
    if (initialManual) {
      manual.replaceChildren(new Option("当前手册",initialManual));
      void showResult(find,status,() => fillManual(initialManual));
    } else findManuals();
  }
  form.addEventListener("submit", event => {
    event.preventDefault();
    void showResult(save, status, async () => {
      const scope = scopes[Number(scopeField.value)];
      if (!scope) return;
      if (manual && !manualRevision) throw new Error("请先选择并载入排障手册。");
      const fields = new FormData(form);
      await requestAI(`${skillsEndpoint}?action=${mode}`,{scope:scopeOf(scope),name:name.value,body:body.value,applies:applies.value,
        keyword:fields.get("keyword"),manualId:manual?.value ?? "",manualRevision});
      updateWorkspaceQuery("status","review");
      dialog.close();
      reload();
    });
  });
}

export function bindAISkills(root: HTMLElement, data: SkillData, scopes: Scope[], reload: () => void): void {
  const dialog = root.querySelector<HTMLDialogElement>("[data-ai-dialog]");
  if (!dialog) return;
  const search = root.querySelector<HTMLInputElement>("[data-skill-search]");
  const scopeSelect = root.querySelector<HTMLSelectElement>("[data-skill-scope]");
  const tabs = [...root.querySelectorAll<HTMLButtonElement>("[data-skill-filter]")];
  const rows = [...root.querySelectorAll<HTMLElement>("[data-skill-row]")];
  const query = new URLSearchParams(window.location.hash.split("?", 2)[1]);
  const requestedStatus = query.get("status") ?? "";
  let status = Object.hasOwn(labels,requestedStatus) ? requestedStatus : "";
  const update = () => {
    const chosen = scopeSelect?.value ? scopes[Number(scopeSelect.value)] : undefined;
    for (const row of rows) {
      const skill = data.skills[Number(row.dataset.skillRow)]?.data;
      if (!skill) continue;
      const scope = scopeOf(skill.scope);
      row.hidden = Boolean(status && status !== skill.status) ||
        Boolean(chosen && JSON.stringify(scopeOf(chosen)) !== JSON.stringify(scope)) ||
        !searchMatches(search?.value ?? "", skill.name, skill.keyword, scopeText(scope));
    }
    const count = rows.filter(row => !row.hidden).length;
    root.querySelector("[data-skill-empty]")?.toggleAttribute("hidden", count > 0);
    const label = root.querySelector("[data-skill-count]");
    if (label) label.textContent = `${count} 项`;
    for (const tab of tabs) tab.setAttribute("aria-pressed", String(tab.dataset.skillFilter === status));
    updateWorkspaceQuery("status", status);
  };
  search?.addEventListener("input", update);
  scopeSelect?.addEventListener("change", update);
  for (const tab of tabs) tab.addEventListener("click", () => { status = tab.dataset.skillFilter ?? ""; update(); });
  const reset = (next: string) => {
    status = next;
    if (search) search.value = "";
    if (scopeSelect) scopeSelect.value = "";
    update();
  };
  root.querySelector("[data-skill-clear]")?.addEventListener("click", () => { reset(""); search?.focus(); });
  root.querySelector("[data-review-filter]")?.addEventListener("click", () => {
    reset("review");
    tabs.find(tab => tab.dataset.skillFilter === "review")?.focus();
  });
  for (const button of root.querySelectorAll<HTMLButtonElement>("[data-skill-new], [data-skill-view]")) {
    button.addEventListener("click", () => {
      if (button.dataset.skillNew) { openSkillDraft(dialog, scopes, button.dataset.skillNew, reload); return; }
      const entry = data.skills[Number(button.dataset.skillView)];
      if (!entry) return;
      const skill = entry.data;
      const action = skill.status === "enabled" ? "disable" : skill.status === "review" && skill.author !== data.actor ? "approve" : undefined;
      aiDialog(dialog, action === "approve" ? "审核技能" : "技能详情", `<div class="dialog-body">
        <div class="row between"><h3>${h(skill.name)}</h3>${pill(labels[skill.status], skill.status === "review" ? "warn" : skill.status === "enabled" ? "good" : "")}</div>
        ${scopeDetails([["适用范围",scopeSummary(skill.scope)],["关键词",skill.keyword || "匹配整个范围"],
          ["来源", skill.manualId ? `手册转化 · v${skill.manualRevision}` : "Markdown 上传"],["提交人",skill.author],["审核人",skill.reviewer],["适用说明",skill.applies]])}
        <fieldset class="group"><legend>排障步骤</legend><pre class="preview">${h(skill.body)}</pre></fieldset>
        ${skill.status === "review" && !action ? "<p class='muted'>此技能由你提交，等待另一位管理员审核。</p>" : ""}
        ${action === "disable" ? "<p class='muted'>停用后，新调查将停止引用该技能。</p>" : ""}
        <p class="feedback" role="status"></p></div>${action ? dialogFooter(`<button class="btn primary" type="button" data-skill-action>${action === "approve" ? "审核通过并启用" : "停用技能"}</button>`) : ""}`);
      const change = dialog.querySelector<HTMLButtonElement>("[data-skill-action]");
      const feedback = dialog.querySelector("[role=status]");
      if (change && feedback && action) change.addEventListener("click", () => void showResult(change,feedback,async () => {
        await requestAI(`${skillsEndpoint}?action=${action}`,{id:entry.id,version:entry.version});
        updateWorkspaceQuery("status", action === "approve" ? "enabled" : "disabled");
        dialog.close();
        reload();
      }));
    });
  }
  update();
  const manual = query.get("manual");
  if (manual && scopes.length) {
    history.replaceState(null,"","#/ai/skills");
    openSkillDraft(dialog,scopes,"manual",reload,manual);
  }
}
