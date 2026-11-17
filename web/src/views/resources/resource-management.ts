import { escapeHtml } from "../../components/html.ts";
import { renderOnboarding } from "../clusters/cluster-onboarding.ts";

const tabs = [
  ["clusters", "集群接入"],
  ["environments", "环境管理"],
  ["business-lines", "业务线管理"],
] as const;

type Tab = typeof tabs[number][0];
type Catalog = Exclude<Tab, "clusters">;
type Entry = { id: string; name: string };
export type ResourceAccess = Record<Tab, boolean>;

export function resolveResourceTab(hash: string, access: ResourceAccess): Tab | undefined {
  const requested = hash.split("/")[2];
  if (requested) return tabs.find(([key]) => key === requested && access[key])?.[0];
  return tabs.find(([key]) => access[key])?.[0];
}

export function renderCatalog(kind: Catalog, entries: Entry[]): string {
  const name = kind === "environments" ? "环境" : "业务线";
  const form = (entry?: Entry): string => `
    <form class="login-form identity-form" data-resource-catalog="${kind}" data-entry-id="${escapeHtml(entry?.id ?? "")}">
      <label>${name}名称<input name="name" value="${escapeHtml(entry?.name ?? "")}" maxlength="128" required></label>
      <button class="button" type="submit">${entry ? "保存名称" : `新增${name}`}</button>
      ${entry ? '<button class="button secondary" type="submit" value="delete" formnovalidate>删除</button>' : ""}
      <p role="status"></p>
    </form>`;
  return `<h2>${name}管理</h2>
    ${entries.length ? `<ul class="role-list">${entries.map((entry) => `<li>${form(entry)}</li>`).join("")}</ul>` : `<p>暂无${name}。</p>`}
    ${form()}`;
}

export async function renderResourceManagement(hash: string, access: ResourceAccess): Promise<string> {
  const selected = resolveResourceTab(hash, access);
  if (!selected) return '<p role="alert">无权访问此资源管理页面。</p>';
  const navigation = tabs.filter(([key]) => access[key]).map(([key, label]) =>
    `<a href="#/resources/${key}"${key === selected ? ' aria-current="page"' : ""}>${label}</a>`).join("");
  let content: string;
  if (selected === "clusters") {
    content = await renderOnboarding(hash.split("/")[3]);
  } else {
    const response = await fetch(`/api/v1/admin/${selected}`);
    if (!response.ok || !response.headers.get("Content-Type")?.includes("application/json")) {
      throw new Error("resource catalog unavailable");
    }
    content = renderCatalog(selected, await response.json() as Entry[]);
  }
  return `<section class="panel identity-panel">
    <nav class="identity-nav" aria-label="资源管理">${navigation}</nav>
    ${content}
  </section>`;
}

export async function submitResourceCatalog(form: HTMLFormElement, remove: boolean, reload: () => void): Promise<void> {
  const status = form.querySelector<HTMLElement>('[role="status"]');
  const buttons = [...form.querySelectorAll<HTMLButtonElement>("button")];
  const kind = form.dataset.resourceCatalog;
  if (!status || !kind || buttons.some((button) => button.disabled)) return;
  const id = form.dataset.entryId || undefined;
  const data = new FormData(form);
  for (const button of buttons) button.disabled = true;
  status.textContent = remove ? "正在删除…" : "正在保存…";
  try {
    const response = await fetch(`/api/v1/admin/${kind}`, {
      method: remove ? "DELETE" : id ? "PUT" : "POST",
      headers: { "Content-Type": "application/json" },
      body: JSON.stringify(remove ? { id } : { id, name: String(data.get("name") ?? "").trim() }),
    });
    if (response.ok) {
      reload();
      return;
    }
    if (response.status === 409 && response.headers.get("Content-Type")?.includes("application/json")) {
      const conflict = await response.json() as { message: string; references: string[] };
      status.textContent = `${conflict.message}：${conflict.references.join("、")}`;
    } else if (response.headers.get("Content-Type")?.includes("text/plain")) {
      status.textContent = (await response.text()).trim();
    } else {
      status.textContent = "操作失败，请重试。";
    }
  } catch {
    status.textContent = "资源目录请求失败。";
  } finally {
    for (const button of buttons) button.disabled = false;
  }
}
