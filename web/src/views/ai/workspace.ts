import { escapeHtml as h } from "../../components/html.ts";

export const pill = (label: string, tone = "") => `<span class="pill ${tone}">${h(label)}</span>`;
export const icon = (mark: string, tone = "") => `<span class="icon ${tone}" aria-hidden="true">${h(mark)}</span>`;
export const stat = (count: number, label: string) => `<div class="stat"><strong>${count}</strong><small>${h(label)}</small></div>`;
export const options = (items: [string, string][]) => items.map(([value, label]) =>
  `<option value="${h(value)}">${h(label)}</option>`).join("");
export const searchMatches = (query: string, ...parts: (string | undefined)[]) =>
  parts.join(" ").toLowerCase().includes(query.trim().toLowerCase());
export const aiDialogMarkup = '<dialog class="wiki-dialog ai-dialog" data-ai-dialog aria-labelledby="ai-dialog-title"></dialog>';
export const dialogFooter = (button: string) => `<footer class="dialog-foot"><button class="btn" type="button" data-ai-dismiss>取消</button>${button}</footer>`;
export const scopeDetails = (pairs: [string, string | undefined][]) =>
  `<dl class="scope">${pairs.filter(([,value]) => value).map(([label,value]) => `<dt>${h(label)}</dt><dd>${h(value)}</dd>`).join("")}</dl>`;

export function updateWorkspaceQuery(key: string, value: string) {
  const [route, search] = window.location.hash.split("?", 2);
  const params = new URLSearchParams(search);
  if (value) params.set(key, value);
  else params.delete(key);
  history.replaceState(history.state, "", params.size ? `${route}?${params}` : route);
}
