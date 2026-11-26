import { createOverviewClient } from "../../api/client.ts";
import { ApiError } from "../../api/generated.ts";
import { escapeHtml } from "../../components/html.ts";
import type { aggregateDiagnostics } from "../../domain/unified-diagnostics.ts";

type RecordRow = ReturnType<typeof aggregateDiagnostics>["records"][number];

export function bindHealthInvestigation(root: HTMLElement, rows: RecordRow[], lifetime: AbortSignal): void {
  root.addEventListener("click", event => {
    const opener = event.target instanceof Element && event.target.closest<HTMLButtonElement>("button[data-health-record]");
    if (!opener) return;
    const row = rows[Number(opener.dataset.healthRecord)];
    const sourceId = row?.problem?.sourceId ?? row?.alert?.sourceId;
    if (!row || !(row.problem || row.alert)) return;
    const dialog = document.createElement("dialog");
    dialog.className = "health-investigation";
    dialog.setAttribute("aria-labelledby", "health-investigation-title");
    const fields = [
      ["来源", row.origin], ["主机或实例", row.problem?.hosts.map(h => h.name).join("、") || row.cluster],
      ["首次发现", row.time === undefined ? "—" : new Date(row.time).toLocaleString("zh-CN", {timeZone: "Asia/Shanghai"}) + " · UTC+8"],
      ["记录编号", row.problem?.eventId ?? row.id],
      ["处置状态", row.problem ? (row.problem.recovered ? "已恢复" : "未恢复") :
        row.alert ? ({active: "活动", departed: "已离开当前列表", suppressed: "静默或抑制", unprocessed: "待处理"} as const)[row.alert.state] : "—"],
    ];
    dialog.innerHTML = `<header><h2 id="health-investigation-title">诊断记录详情</h2><button type="button" class="button secondary" autofocus data-close>关闭</button></header>
      <section><h3>${escapeHtml(row.title)}</h3><dl>${fields.map(([name, value]) => `<dt>${name}</dt><dd>${escapeHtml(value || "—")}</dd>`).join("")}</dl>
      <h3>AI 深度调查</h3><p>根据本条告警及其授权范围内的只读证据分析。</p>
      <div class="system-actions"><button type="button" class="button" data-run ${sourceId ? "" : "disabled"}>发起 AI 深度调查</button><button type="button" class="button secondary" data-cancel hidden>取消调查</button></div>
      <p data-result role="status">${sourceId ? "尚未发起" : "缺少来源实例，请刷新告警列表。"}</p></section>`;
    root.append(dialog);
    let investigation: AbortController | undefined;
    const close = () => dialog.close();
    dialog.querySelector("[data-close]")?.addEventListener("click", close);
    dialog.addEventListener("close", () => {
      investigation?.abort();
      dialog.remove();
      if (opener.isConnected) opener.focus({preventScroll: true});
    }, {once: true});
    const run = dialog.querySelector<HTMLButtonElement>("[data-run]");
    const result = dialog.querySelector<HTMLElement>("[data-result]");
    const cancel = dialog.querySelector<HTMLButtonElement>("[data-cancel]");
    if (!run || !result || !cancel) { dialog.remove(); return; }
    cancel.addEventListener("click", () => investigation?.abort());
    run.addEventListener("click", async () => {
      if (!sourceId || run.disabled) return;
      const controller = new AbortController();
      investigation = controller;
      run.disabled = true;
      cancel.hidden = false;
      result.textContent = "正在调查，关闭窗口可结束本次请求。";
      try {
        const response = await createOverviewClient(AbortSignal.any([controller.signal, lifetime])).investigateHealthAlert({source: row.source, sourceId, id: row.id});
        result.textContent = response.analysis;
      } catch (err) {
        if (lifetime.aborted) return;
        if (controller.signal.aborted) {
          result.textContent = "调查已取消，可以重新发起。";
          return;
        }
        const reason = err instanceof ApiError ? (err.body as {error?: {message?: string}} | null)?.error?.message : "";
        result.textContent = reason || "调查未完成，请检查连接后重试。";
      } finally {
        investigation = undefined;
        run.disabled = false;
        cancel.hidden = true;
      }
    });
    dialog.showModal();
  });
}
