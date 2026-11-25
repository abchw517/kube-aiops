import { escapeHtml as h } from "../../components/html.ts";

export function resourceOptions(choices: Iterable<[string, string]>): string {
  return Array.from(choices, ([value, label]) => `<option value="${h(value)}">${h(label)}</option>`).join("");
}

export function resourceMetric(key: string, label: string): string {
  return `<button type="button" data-resource-metric="${key}" aria-pressed="${key === "all"}">${label}<strong>—</strong></button>`;
}

export function resourceCell(label: string, content: string): string {
  return `<td data-label="${h(label)}">${content}</td>`;
}

export function resourceState(label: string, tone: "issue" | "good" | "waiting" | "neutral" = "neutral"): string {
  return `<span class="resource-state" data-tone="${tone}">${h(label)}</span>`;
}

export function resourceDetail(titleId: string, title: string, subtitle: string, body: string, footer = ""): string {
  return `<header class="resource-overlay-head"><div><h2 id="${titleId}" tabindex="-1">${h(title)}</h2><p>${h(subtitle)}</p></div>
    <button type="button" class="zabbix-close" aria-label="关闭" data-resource-close>×</button></header>
    <div class="resource-overlay-body">${body}</div>
    ${footer ? `<footer class="resource-overlay-foot">${footer}</footer>` : ""}`;
}

export function restoreResourceFocus(origin: HTMLElement | null, fallback: HTMLElement | null): void {
  if (origin?.isConnected && !origin.closest("[hidden]") && !(origin instanceof HTMLButtonElement && origin.disabled)) origin.focus();
  else fallback?.focus();
}

export function bindDirtyDialog(dialog: HTMLDialogElement, closeSelector: string, busy: () => boolean): { clean: () => void } {
  let dirty = false;
  const canClose = () => !busy() && (!dirty || window.confirm("放弃未保存的修改？"));
  dialog.addEventListener("input", (event) => {
    if (event.target instanceof HTMLInputElement && event.target.type === "search") return;
    dirty = true;
  });
  dialog.addEventListener("click", (event) => {
    if (!(event.target instanceof Element) || !event.target.closest(closeSelector)) return;
    if (canClose()) dialog.close();
  });
  dialog.addEventListener("cancel", (event) => { if (!canClose()) event.preventDefault(); });
  return { clean: () => { dirty = false; } };
}

export function bindResourceMenus(region: HTMLElement): void {
  region.addEventListener("click", (event) => {
    if (!(event.target instanceof Element)) return;
    const current = event.target.closest(".resource-row-menu");
    for (const menu of region.querySelectorAll<HTMLDetailsElement>(".resource-row-menu[open]")) {
      if (menu !== current) menu.open = false;
    }
  });
  region.addEventListener("keydown", (event) => {
    if (event.key !== "Escape" || !(event.target instanceof Element)) return;
    const menu = event.target.closest<HTMLDetailsElement>(".resource-row-menu[open]");
    if (!menu) return;
    event.preventDefault();
    menu.open = false;
    menu.querySelector("summary")?.focus();
  });
}
