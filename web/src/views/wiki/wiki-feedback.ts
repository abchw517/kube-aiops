import { escapeHtml as h } from "../../components/html.ts";

export type WikiFeedback = { revision: number; section?: string; kind: string; note?: string; at: number; resolved: boolean };

export function renderFeedback(id: string, revision: number, section: string): string {
  return `<form class="wiki-import-form" data-wiki-feedback data-id="${h(id)}" data-revision="${revision}" data-section="${h(section)}">
    <h3>知识反馈</h3><p>反馈将关联 v${revision}${section ? " 的当前章节" : " 版本"}。</p>
    <label>反馈类型<select name="kind"><option value="helpful">有帮助</option><option value="outdated">需复核</option></select></label>
    <label>说明（选填）<textarea name="note" rows="2" maxlength="300"></textarea></label>
    <button type="submit" class="button secondary">提交反馈</button><p role="status"></p></form>`;
}

export function bindWikiFeedback(root: HTMLElement): void {
  const form = root.querySelector<HTMLFormElement>("[data-wiki-feedback]");
  const status = form?.querySelector("[role=status]");
  if (!form || !status) return;
  form.addEventListener("submit", async (event) => {
    event.preventDefault();
    if (form.inert) return;
    form.inert = true;
    const fields = new FormData(form);
    try {
      const response = await fetch(`/api/v1/wiki/knowledge/${form.dataset.id}/feedback`, {
        method: "POST", headers: { "Content-Type": "application/json" },
        body: JSON.stringify({ revision: Number(form.dataset.revision), section: form.dataset.section, kind: fields.get("kind"), note: fields.get("note") }),
      });
      if (!response.ok) throw new Error(response.status === 404 ? "这个版本已不可用。请刷新知识详情。" : (await response.text()).trim());
      status.textContent = "反馈已记录。";
    } catch (error) { status.textContent = error instanceof Error && error.message || "反馈失败，请重试。"; }
    finally { form.inert = false; }
  });
}
