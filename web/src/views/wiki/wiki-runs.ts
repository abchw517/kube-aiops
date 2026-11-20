import { escapeHtml as h } from "../../components/html.ts";

type Request = (path: string, method: string, body?: unknown) => Promise<Response>;
interface WikiRun {
  id: string; started: number; finished?: number; status: string; reason?: string; trigger: string; retryOf?: string; pageId?: string;
  saved: number; skipped: number; failed: number; missing: number;
}
interface RunDetail { run: WikiRun; items: { pageId?: string; status: string; reason?: string }[]; more: boolean }
const runStatus: Record<string, string> = {
  running: "运行中", succeeded: "完成", partial: "部分失败", failed: "失败", interrupted: "已中断",
  saved: "已生成候选", skipped: "已跳过", missing: "疑似缺失",
};
const runTriggers: Record<string, string> = { manual: "手动", scheduled: "定时", retry: "重试" };
const retryable = new Set(["failed", "interrupted", "partial"]);
const when = (time: number): string => new Date(time * 1000).toLocaleString("zh-CN", { timeZone: "Asia/Shanghai" });
const counts = (run: WikiRun): string => `候选 ${run.saved} · 跳过 ${run.skipped} · 失败 ${run.failed} · 疑似缺失 ${run.missing}`;
const button = (label: string, action: string, value = "") =>
  `<button type="button" class="button secondary" data-wiki-run-action="${action}" value="${h(value)}">${label}</button>`;

export function renderWikiRuns(): string {
  return `<section class="wiki-sync-runs"><div class="wiki-toolbar"><h3>同步执行记录</h3>${button("刷新记录", "reload")}</div>
    <p role="status" data-wiki-runs-status></p><div data-wiki-runs-list></div><div data-wiki-run-detail></div></section>`;
}

export function bindWikiRuns(root: HTMLElement, request: Request): (() => Promise<void>) | undefined {
  const form = root.querySelector<HTMLFormElement>("[data-wiki-sync]");
  const source = form?.querySelector<HTMLInputElement>('[name="id"]');
  const list = root.querySelector<HTMLElement>("[data-wiki-runs-list]");
  const detail = root.querySelector<HTMLElement>("[data-wiki-run-detail]");
  const status = root.querySelector<HTMLElement>("[data-wiki-runs-status]");
  const panel = status?.closest<HTMLElement>(".wiki-sync-runs");
  if (!source || !list || !detail || !status || !panel) return;
  let operation = 0;
  let active = "";
  let page = 1;
  let failures = false;
  let retrying = false;
  const endpoint = () => `/api/v1/wiki/sources/${encodeURIComponent(source.value.trim())}/runs`;
  const current = (id: number, path: string) => panel.isConnected && operation === id && path === endpoint();

  const reload = async () => {
    const id = ++operation;
    const path = endpoint();
    active = "";
    list.replaceChildren();
    detail.replaceChildren();
    if (!source.value.trim() || !source.checkValidity()) {
      status.textContent = "请先填写有效来源编号。";
      return;
    }
    status.textContent = "正在读取执行记录…";
    try {
      const response = await request(path, "GET");
      const { items: runs, lastSucceeded } = await response.json() as { items: WikiRun[]; lastSucceeded: number };
      if (!current(id, path)) return;
      status.textContent = "";
      list.innerHTML = runs.length ? `<p>${lastSucceeded ? `最近成功 · ${h(when(lastSucceeded))}` : "暂无成功记录"}</p><ul class="wiki-run-list">${runs.map((run) =>
        `<li><div><strong>${h(runStatus[run.status] ?? run.status)}</strong> · ${h(runTriggers[run.trigger] ?? run.trigger)}
          <p>${h(when(run.started))} · ${counts(run)}${run.reason ? ` · ${h(run.reason)}` : ""}</p></div>${button("查看明细", "detail", run.id)}</li>`).join("")}</ul>` :
        "<p>此来源暂无执行记录。</p>";
    } catch { if (current(id, path)) status.textContent = "无法读取此来源的执行记录。请核对来源或当前权限。"; }
  };

  const loadDetail = async () => {
    const id = ++operation;
    const path = endpoint();
    const runID = active;
    status.textContent = "正在读取执行明细…";
    try {
      const response = await request(`${path}/${encodeURIComponent(runID)}?page=${page}&failed=${failures}`, "GET");
      const data = await response.json() as RunDetail;
      if (!current(id, path)) return;
      const { run, items, more } = data;
      status.textContent = "";
      detail.innerHTML = `<h4>执行明细 · ${h(runStatus[run.status] ?? run.status)}</h4>
        <p>${h(when(run.started))}${run.finished ? ` 至 ${h(when(run.finished))}` : ""} · ${counts(run)}</p>
        ${run.retryOf ? `<p>重试自 ${h(run.retryOf)}${run.pageId ? ` · 页面 ${h(run.pageId)}` : ""}</p>` : ""}
        <div class="system-actions">${button(failures ? "显示全部" : "只看失败", "filter")}
        ${retryable.has(run.status) ? button("重试整个来源", "retry") : ""}</div>
        ${items.length ? `<div class="table-wrap"><table><thead><tr><th>远端页面 ID</th><th>状态</th><th>说明</th><th>操作</th></tr></thead>
        <tbody>${items.map((item) => `<tr><td>${h(item.pageId || "—")}</td><td>${h(runStatus[item.status] ?? item.status)}</td><td>${h(item.reason || "—")}</td>
        <td>${item.status === "failed" && item.pageId && retryable.has(run.status) ? button("重试此页", "retry", item.pageId) : ""}</td></tr>`).join("")}</tbody></table></div>` : "<p>暂无明细。</p>"}
        ${page > 1 || more ? `<nav class="system-actions" aria-label="同步明细分页">${page > 1 ? button("上一页", "previous") : ""}
        <span>第 ${page} 页</span>${more ? button("下一页", "next") : ""}</nav>` : ""}`;
    } catch {
      if (current(id, path)) {
        detail.replaceChildren();
        status.textContent = "无法读取同步明细。";
      }
    }
  };

  source.addEventListener("input", () => {
    operation++;
    active = "";
    list.replaceChildren();
    detail.replaceChildren();
    status.textContent = "";
  });
  panel.addEventListener("click", async (event) => {
    const item = event.target instanceof HTMLElement ? event.target.closest<HTMLButtonElement>("[data-wiki-run-action]") : null;
    if (!item || retrying) return;
    switch (item.dataset.wikiRunAction) {
      case "reload": await reload(); return;
      case "detail": active = item.value; page = 1; failures = false; break;
      case "next": page++; break;
      case "previous": page--; break;
      case "filter": failures = !failures; page = 1; break;
      case "retry": {
        const path = endpoint();
        retrying = true;
        item.disabled = true;
        status.textContent = "正在重试同步…";
        try {
          await request(`${path}/${encodeURIComponent(active)}/retry`, "POST", { pageId: item.value });
          if (panel.isConnected && endpoint() === path) {
            await reload();
            status.textContent = "本次重试已记录。";
          }
        } catch (error) {
          if (panel.isConnected && endpoint() === path) {
            await reload();
            status.textContent = error instanceof Error ? error.message : "同步重试失败。";
          }
        } finally { retrying = false; item.disabled = false; }
        return;
      }
    }
    if (active) await loadDetail();
  });
  return reload;
}
