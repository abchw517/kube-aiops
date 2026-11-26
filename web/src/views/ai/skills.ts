import { escapeHtml as h } from "../../components/html.ts";
import { aiDialog, endpoint, requestAI, scopeOf, scopeText, showResult, skillsEndpoint, type Entry, type Scope } from "./services.ts";

type Skill = {
  scope: Scope; name: string; applies: string; keyword: string; body: string;
  manualId?: string; manualRevision?: number; author: string; reviewer?: string; status: "review" | "enabled" | "disabled";
};
type SkillData = { skills: Entry<Skill>[]; actor: string };
type Manual = { knowledgeId: string; revision?: number; title: string; metadata?: {sections?: {checks?: string}; applicability?: string} };

export async function loadAISkills() {
  const [data,bindings] = await Promise.all([requestAI<SkillData>(skillsEndpoint), requestAI<{models: Scope[]}>(endpoint)]);
  const scopes = bindings.models.filter(scope => scope.source !== "wiki");
  const rows = data.skills.map(({data: s, version}, index) => `<tr>
    <td><strong>${h(s.name)}</strong><small>${s.manualId ? `手册转化 · v${h(s.manualRevision)}` : "SKILL.md 上传"}</small></td>
    <td>${s.status === "review" ? "草稿" : `v${version}`}</td><td>${h(s.applies || scopeText(s.scope))}<small>${h(s.keyword ? `匹配：${s.keyword}` : scopeText(s.scope))}</small></td>
    <td><span class="ai-status" data-state="${s.status}">${{review:"审核中",enabled:"已启用",disabled:"已停用"}[s.status]}</span></td>
    <td><button class="button secondary" type="button" data-skill-view="${index}">${s.status === "review" ? "审核预览" : "查看技能"}</button></td></tr>`).join("");
  return { data, scopes, html: `<div class="portal-ai-page"><section class="panel ai-panel">
    <header class="ai-panel-heading"><h2>技能列表</h2><div class="system-actions">
      <button type="button" class="button secondary" data-skill-new="upload" ${scopes.length ? "" : "disabled"}>上传技能</button>
      <button type="button" class="button" data-skill-new="manual" ${scopes.length ? "" : "disabled"}>从知识库手册转化</button>
    </div></header>
    ${rows ? `<div class="table-wrap"><table><thead><tr><th>技能 / 来源</th><th>版本</th><th>适用</th><th>状态</th><th>说明</th></tr></thead><tbody>${rows}</tbody></table></div>` : "<p>暂无已提交技能。</p>"}
    ${scopes.length ? "" : '<p>添加非 Wiki 的 Holmes 范围绑定后可提交技能。</p>'}
    </section><section class="panel ai-panel"><h2>手册转化与审核</h2><p>选择已发布排障手册 → 编辑技能草稿 → 提交审核 → 另一位管理员审核并启用。</p></section>
    <section class="panel ai-panel"><h2>生效位置</h2><dl>
    <dt>深度调查</dt><dd>绑定和关键词匹配时自动加入技能参考，告警调查和 Finding 初步复核显示已应用技能。</dd>
    <dt>诊断助手</dt><dd>在 Finding 会话中可通过技能名称请求使用同一范围内已启用的技能。</dd>
    </dl></section>
    <dialog class="wiki-dialog ai-dialog" data-ai-dialog aria-labelledby="ai-dialog-title"></dialog></div>` };
}

function openSkillDraft(dialog: HTMLDialogElement, scopes: Scope[], mode: string, reload: () => void, initialManual?: string) {
  aiDialog(dialog, mode === "upload" ? "上传 AI 技能" : "从知识库手册转化", `<form class="ai-form">
    <label>适用范围<select name="scope">${scopes.map((scope, i) => `<option value="${i}">${h(scopeText(scope))}</option>`).join("")}</select></label>
    ${mode === "upload" ? '<label>选择 SKILL.md<input name="file" type="file" accept=".md,.markdown,text/markdown"></label>' :
      '<label>查找已发布手册<input name="search" type="search"></label><div class="system-actions"><button type="button" class="button secondary" data-find-manual>搜索手册</button></div><label>已发布手册<select name="manual" required></select></label>'}
    <label>技能名称<input name="name" required maxlength="60"></label>
    <label>适用说明<input name="applies" maxlength="100"></label>
    <label>匹配关键词<input name="keyword" maxlength="40" placeholder="留空时匹配该范围内全部调查"></label>
    <label>SKILL.md 正文<textarea name="body" rows="10" required maxlength="12000"></textarea></label>
    <p role="status"></p><div><button type="submit" class="button">提交审核</button></div></form>`);
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
      dialog.close();
      reload();
    });
  });
}

export function bindAISkills(root: HTMLElement, data: SkillData, scopes: Scope[], reload: () => void): void {
  const dialog = root.querySelector<HTMLDialogElement>("[data-ai-dialog]");
  if (!dialog) return;
  for (const button of root.querySelectorAll<HTMLButtonElement>("[data-skill-new], [data-skill-view]")) {
    button.addEventListener("click", () => {
      if (button.dataset.skillNew) { openSkillDraft(dialog, scopes, button.dataset.skillNew, reload); return; }
      const entry = data.skills[Number(button.dataset.skillView)];
      if (!entry) return;
      const skill = entry.data;
      const action = skill.status === "enabled" ? "disable" : skill.status === "review" && skill.author !== data.actor ? "approve" : undefined;
      aiDialog(dialog, skill.status === "review" ? "审核预览" : "查看技能", `<h3>${h(skill.name)}</h3><p>${h(scopeText(skill.scope))}</p>
        <p>提交人 ${h(skill.author)}${skill.reviewer ? ` · 审核人 ${h(skill.reviewer)}` : ""}</p>
        ${skill.manualId ? `<p>手册版本 v${h(skill.manualRevision)}</p>` : ""}
        <pre class="ai-skill-body">${h(skill.body)}</pre>
        <p role="status">${skill.status === "review" && !action ? "请由另一位管理员审核。" : ""}</p>
        ${action ? `<button class="button" type="button" data-skill-action>${action === "approve" ? "审核通过并启用" : "停用技能"}</button>` : ""}`);
      const change = dialog.querySelector<HTMLButtonElement>("[data-skill-action]");
      const feedback = dialog.querySelector("[role=status]");
      if (change && feedback && action) change.addEventListener("click", () => void showResult(change,feedback,async () => {
        await requestAI(`${skillsEndpoint}?action=${action}`,{id:entry.id,version:entry.version});
        dialog.close();
        reload();
      }));
    });
  }
  const query = new URLSearchParams(window.location.hash.split("?", 2)[1]);
  const manual = query.get("manual");
  if (manual && scopes.length) {
    history.replaceState(null,"","#/ai/skills");
    openSkillDraft(dialog,scopes,"manual",reload,manual);
  }
}
