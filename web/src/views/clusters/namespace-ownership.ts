import { escapeHtml as h } from "../../components/html.ts";

const endpoint = "/api/v1/admin/namespace-business";
const route = "#/resources/clusters";

type Assignment = { namespaces: { name: string }[]; assigned: Record<string, string[]> };
type Business = { id: string; name: string };

export async function renderNamespaceOwnership(id: string): Promise<string> {
  const [namespaceResponse, businessResponse] = await Promise.all([
    fetch(`${endpoint}?cluster=${encodeURIComponent(id)}`),
    fetch("/api/v1/admin/business-lines"),
  ]);
  if (!namespaceResponse.ok || !businessResponse.ok) throw new Error("namespace ownership unavailable");
  const { namespaces, assigned } = await namespaceResponse.json() as Assignment;
  const businesses = await businessResponse.json() as Business[];
  return `<a href="${route}">返回集群接入</a><h2>命名空间归属</h2><p>集群 ${h(id)}</p>
    <div class="catalog-table-wrap"><table class="catalog-table">
      <thead><tr><th>命名空间</th><th>业务线</th></tr></thead><tbody>
      ${namespaces.map(({ name }) => `<tr><td>${h(name)}</td><td>
        <form class="platform-assignment" data-namespace-ownership="${h(id)}" data-namespace="${h(name)}">
          <select name="businesses" multiple aria-label="${h(name)} 的业务线">
          ${businesses.map((business) => `<option value="${h(business.id)}"${assigned[name]?.includes(business.id) ? " selected" : ""}>
            ${h(business.name)}</option>`).join("")}</select>
          <button class="button secondary" type="submit">保存归属</button><span role="status"></span>
        </form>
      </td></tr>`).join("") || '<tr><td colspan="2">暂无命名空间。</td></tr>'}
      </tbody></table></div>`;
}

export async function submitNamespaceOwnership(form: HTMLFormElement): Promise<void> {
  const status = form.querySelector('[role="status"]');
  const button = form.querySelector("button");
  if (!status || !button || button.disabled) return;
  button.disabled = true;
  status.textContent = "正在保存…";
  try {
    const response = await fetch(`${endpoint}?cluster=${encodeURIComponent(form.dataset.namespaceOwnership ?? "")}`, {
      method: "PUT", headers: { "Content-Type": "application/json" },
      body: JSON.stringify({ namespace: form.dataset.namespace, businesses: new FormData(form).getAll("businesses") }),
    });
    status.textContent = response.ok ? "已保存" : (await response.text()).trim() || "保存失败。";
  } catch {
    status.textContent = "保存失败，请重试。";
  } finally { button.disabled = false; }
}
