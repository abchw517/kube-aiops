import { escapeHtml as h } from "../../components/html.ts";

const endpoint = "/api/v1/admin/namespace-business";

type Assignment = {
  namespaces: { name: string }[];
  assigned: Record<string, string[]>;
  dedicatedBusinessId?: string;
};
type Business = { id: string; name: string };

type OwnershipDelta = { add: Set<string>; remove: Set<string> };
type OwnershipFormState = {
  businessId: string;
  choices: { box: HTMLInputElement; assigned: Set<string> }[];
  drafts: Map<string, OwnershipDelta>;
};
const ownershipForms = new WeakMap<HTMLFormElement, OwnershipFormState>();

/** 以业务线为入口，展示已采集的命名空间及已保存的归属。 */
export async function renderNamespaceOwnership(id: string, modal: boolean): Promise<string> {
  const [namespaceResponse, businessResponse] = await Promise.all([
    fetch(`${endpoint}?cluster=${encodeURIComponent(id)}`),
    fetch("/api/v1/admin/business-lines"),
  ]);
  if (!namespaceResponse.ok || !businessResponse.ok) throw new Error("namespace ownership unavailable");
  const { namespaces, assigned, dedicatedBusinessId } = await namespaceResponse.json() as Assignment;
  const businesses = await businessResponse.json() as Business[];
  const dedicated = businesses.find(({ id }) => id === dedicatedBusinessId);
  const initialBusinessId = dedicated?.id ?? "";
  const hasBusiness = dedicatedBusinessId === undefined ? businesses.length > 0 : Boolean(dedicated);
  const namespaceNames = (namespaces ?? []).map(({ name }) => name);
  const assignments = new Map(Object.entries(assigned));
  const choices = namespaceNames.map((name) => {
    const owners = assignments.get(name) ?? [];
    return `<label><input type="checkbox" name="namespaces" value="${h(name)}" data-assigned-businesses="${h(JSON.stringify(owners))}"${owners.includes(initialBusinessId) ? " checked" : ""}><span>${h(name)}</span></label>`;
  }).join("");
  return `<form class="namespace-ownership-form" data-namespace-ownership="${h(id)}" data-ownership-business-id="${h(initialBusinessId)}">
    <p class="namespace-ownership-cluster">集群 <strong>${h(id)}</strong></p>
    ${dedicated ? `<p class="namespace-ownership-fixed">业务线 <strong>${h(dedicated.name)}</strong></p>` :
      hasBusiness ? `<label class="namespace-ownership-business">业务线<select name="businessId" data-ownership-business required>
        <option value="" disabled selected>请选择业务线</option>
        ${businesses.map((business) => `<option value="${h(business.id)}">${h(business.name)}</option>`).join("")}
      </select></label>` : ""}
    ${!hasBusiness ? '<p class="namespace-ownership-hint">暂无可选业务线。</p>' :
      !namespaceNames.length ? '<p class="namespace-ownership-hint">尚未采集到命名空间。</p>' : `
      <p class="namespace-ownership-hint" data-ownership-prompt${dedicated ? " hidden" : ""}>请选择业务线后勾选命名空间。</p>
      <div data-ownership-choices${dedicated ? "" : " hidden"}>
        <p class="namespace-ownership-hint">勾选命名空间后保存当前业务线的归属。</p>
        <div class="namespace-ownership-list" role="region" aria-label="命名空间列表" tabindex="0">
          <div class="namespace-ownership-options">${choices}</div>
        </div>
      </div>`}
    <p role="status" aria-live="polite"></p>
    <div class="cluster-editor-actions">
      ${modal ? '<button type="button" class="button secondary" data-cluster-close>取消</button>' : ""}
      <button class="button" type="submit"${dedicated && namespaceNames.length ? "" : " disabled"}>保存归属</button>
    </div></form>`;
}

/** 读取当前表单的初始归属，并将草稿限制在该表单内。 */
function ownershipState(form: HTMLFormElement): OwnershipFormState {
  const current = ownershipForms.get(form);
  if (current) return current;
  const state: OwnershipFormState = {
    businessId: form.dataset.ownershipBusinessId ?? "",
    choices: [...form.querySelectorAll<HTMLInputElement>('input[name="namespaces"]')].map((box) => ({
      box,
      assigned: new Set<string>(JSON.parse(box.dataset.assignedBusinesses ?? "[]")),
    })),
    drafts: new Map(),
  };
  ownershipForms.set(form, state);
  return state;
}

/** 只记录当前业务线勾选状态和原始归属之间的差异。 */
function rememberDraft(state: OwnershipFormState): OwnershipDelta {
  const draft: OwnershipDelta = { add: new Set(), remove: new Set() };
  for (const { box, assigned } of state.choices) {
    if (box.checked === assigned.has(state.businessId)) continue;
    (box.checked ? draft.add : draft.remove).add(box.value);
  }
  if (draft.add.size || draft.remove.size) state.drafts.set(state.businessId, draft);
  else state.drafts.delete(state.businessId);
  return draft;
}

/** 切换业务线时保留各自的草稿并回填当前业务线。 */
export function changeNamespaceOwnership(event: Event): void {
  const select = event.target;
  if (!(select instanceof HTMLSelectElement) || !select.hasAttribute("data-ownership-business") || select.disabled || !select.form) return;
  const form = select.form;
  const panel = form.querySelector<HTMLElement>("[data-ownership-choices]");
  const prompt = form.querySelector<HTMLElement>("[data-ownership-prompt]");
  const submit = form.querySelector<HTMLButtonElement>('button[type="submit"]');
  const status = form.querySelector('[role="status"]');
  if (!panel || !prompt || !submit || !status) return;
  const state = ownershipState(form);
  if (state.businessId) rememberDraft(state);
  state.businessId = select.value;
  const draft = state.drafts.get(state.businessId);
  for (const { box, assigned } of state.choices) {
    box.checked = Boolean(draft?.add.has(box.value)) ||
      (assigned.has(state.businessId) && !draft?.remove.has(box.value));
  }
  const hasSelectedBusiness = Boolean(state.businessId);
  panel.hidden = !hasSelectedBusiness;
  prompt.hidden = hasSelectedBusiness;
  submit.disabled = !hasSelectedBusiness;
  status.textContent = state.drafts.size ? "已保留未保存的勾选。" : "";
}

/** 保存当前业务线的增删差异，保留其他业务线的关联。 */
export async function submitNamespaceOwnership(form: HTMLFormElement): Promise<void> {
  const status = form.querySelector('[role="status"]');
  const button = form.querySelector<HTMLButtonElement>('button[type="submit"]');
  const select = form.querySelector<HTMLSelectElement>("[data-ownership-business]");
  const dialog = form.closest("dialog");
  if (!status || !button || button.disabled) return;
  const state = ownershipState(form);
  const businessId = state.businessId;
  if (!businessId) return;
  const { add, remove } = rememberDraft(state);
  if (add.size === 0 && remove.size === 0) {
    status.textContent = "没有需要保存的变更。";
    return;
  }
  button.disabled = true;
  if (select) select.disabled = true;
  for (const { box } of state.choices) box.disabled = true;
  dialog?.setAttribute("aria-busy", "true");
  status.textContent = "正在保存…";
  try {
    const response = await fetch(`${endpoint}?cluster=${encodeURIComponent(form.dataset.namespaceOwnership ?? "")}`, {
      method: "PUT", headers: { "Content-Type": "application/json" },
      body: JSON.stringify({ businessId, add: [...add], remove: [...remove] }),
    });
    if (!response.ok) {
      status.textContent = (await response.text()).trim() || "保存失败。";
      return;
    }
    for (const { box, assigned } of state.choices) {
      if (add.has(box.value)) assigned.add(businessId);
      if (remove.has(box.value)) assigned.delete(businessId);
    }
    state.drafts.delete(businessId);
    status.textContent = state.drafts.size ? "已保存当前业务线。其他业务线仍有未保存的勾选。" : "已保存";
    if (!state.drafts.size) dialog?.close();
  } catch {
    status.textContent = "保存失败，请重试。";
  } finally {
    button.disabled = false;
    if (select) select.disabled = false;
    for (const { box } of state.choices) box.disabled = false;
    dialog?.removeAttribute("aria-busy");
  }
}
