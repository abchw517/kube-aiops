import { escapeHtml as h } from "../../components/html.ts";

const endpoint = "/api/v1/admin/namespace-business";

type Assignment = {
  namespaces: { name: string }[];
  assigned: Record<string, string[]>;
  dedicatedBusinessId?: string;
};
type Business = { id: string; name: string };

export async function renderNamespaceOwnership(id: string, modal: boolean): Promise<string> {
  const [namespaceResponse, businessResponse] = await Promise.all([
    fetch(`${endpoint}?cluster=${encodeURIComponent(id)}`),
    fetch("/api/v1/admin/business-lines"),
  ]);
  if (!namespaceResponse.ok || !businessResponse.ok) throw new Error("namespace ownership unavailable");
  const { namespaces, assigned, dedicatedBusinessId } = await namespaceResponse.json() as Assignment;
  const businesses = (await businessResponse.json() as Business[]).filter((business) =>
    dedicatedBusinessId === undefined || business.id === dedicatedBusinessId);
  return `<form class="namespace-ownership-form" data-namespace-ownership="${h(id)}">
    <p>集群 ${h(id)}</p>
    <div class="namespace-ownership-list">${(namespaces ?? []).map(({ name }) =>
      `<fieldset class="system-choice-group" data-namespace="${h(name)}"><legend>${h(name)}</legend><div class="system-choice-list">
        ${businesses.map((business) => `<label><input type="checkbox" name="businesses" value="${h(business.id)}"
          ${assigned[name]?.includes(business.id) ? "checked" : ""}><span>${h(business.name)}</span></label>`).join("") ||
          "<p>暂无可选业务线。</p>"}</div></fieldset>`).join("") || "<p>尚未采集到命名空间。</p>"}</div>
    <p role="status" aria-live="polite"></p>
    <div class="cluster-editor-actions">
      ${modal ? '<button type="button" class="button secondary" data-cluster-close>取消</button>' : ""}
      <button class="button" type="submit"${namespaces?.length && businesses.length ? "" : " disabled"}>保存归属</button>
    </div></form>`;
}

export async function submitNamespaceOwnership(form: HTMLFormElement): Promise<void> {
  const status = form.querySelector('[role="status"]');
  const button = form.querySelector<HTMLButtonElement>('button[type="submit"]');
  const dialog = form.closest("dialog");
  if (!status || !button || button.disabled) return;
  const boxes = [...form.querySelectorAll<HTMLInputElement>('input[name="businesses"]')];
  const changes = [...form.querySelectorAll<HTMLElement>("[data-namespace]")].filter((row) =>
    [...row.querySelectorAll<HTMLInputElement>('input[name="businesses"]')].some((box) => box.checked !== box.defaultChecked),
  ).map((row) => ({
    namespace: row.dataset.namespace,
    businesses: [...row.querySelectorAll<HTMLInputElement>('input[name="businesses"]:checked')].map((box) => box.value),
  }));
  if (changes.length === 0) {
    status.textContent = "没有需要保存的变更。";
    return;
  }
  button.disabled = true;
  for (const box of boxes) box.disabled = true;
  dialog?.setAttribute("aria-busy", "true");
  status.textContent = "正在保存…";
  try {
    const response = await fetch(`${endpoint}?cluster=${encodeURIComponent(form.dataset.namespaceOwnership ?? "")}`, {
      method: "PUT", headers: { "Content-Type": "application/json" }, body: JSON.stringify({ assignments: changes }),
    });
    if (!response.ok) {
      status.textContent = (await response.text()).trim() || "保存失败。";
      return;
    }
    for (const box of boxes) box.defaultChecked = box.checked;
    status.textContent = "已保存";
    dialog?.close();
  } catch {
    status.textContent = "保存失败，请重试。";
  } finally {
    button.disabled = false;
    for (const box of boxes) box.disabled = false;
    dialog?.removeAttribute("aria-busy");
  }
}
