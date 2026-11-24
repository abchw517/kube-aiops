import { apiClient } from "../../api/client.ts";
import type { IncidentEvidence, RCAConclusion, RCATask } from "../../api/generated.ts";
import { escapeHtml, formatTimestamp } from "../../components/html.ts";
import { incidentPrefix } from "./incident-filters.ts";
import { incidentError } from "./incident-list.ts";

const states: Record<RCATask["state"], string> = {
  queued: "排队中", running: "分析中", succeeded: "已完成", failed: "失败", timed_out: "超时", canceled: "已取消",
};
const failures: Record<string, string> = {
  model_timeout: "模型请求超时。可沿用原观察再次发起。",
  lease_expired: "任务执行超时。可沿用原观察再次发起。",
  evidence_changed: "证据归属已经变化，请重新选择证据。",
  invalid_model_response: "模型输出无法解析为分析结论。",
  access_changed: "任务权限或策略版本已经变化。",
  policy_changed: "策略已更新或停用。",
  incident_unavailable: "关联故障暂不可用。",
  input_limit: "请求超出模型输入限制。",
};

export function incidentSubview(id: string, tab: "" | "rca" | "topology", hash: string, additions?: Record<string, string>): string {
  const old = new URLSearchParams(hash.split("?")[1]);
  const params = new URLSearchParams();
  for (const key of ["from", "return"]) {
    const value = old.get(key);
    if (value) params.set(key, value);
  }
  for (const [key, value] of Object.entries(additions ?? {})) params.set(key, value);
  return `${incidentPrefix}${encodeURIComponent(id)}${tab ? `/${tab}` : ""}${params.size ? `?${params}` : ""}`;
}

export async function loadRcaPage(id: string, hash: string) {
  const detail = await apiClient.getIncident({ id });
  const caps = detail.actions.rca ?? {};
  const params = new URLSearchParams(hash.split("?")[1]);
  const after = params.get("runsFrom") || undefined;
  const history = caps.read ? await apiClient.listRcaRuns({ id, limit: 20, continue: after }) : { items: [], pagination: {} };
  const selected = params.get("run") || history.items[0]?.id;
  const job = selected && caps.read ? await apiClient.getRcaRun({ id, runId: selected }) : undefined;
  const available = caps.run ? (await apiClient.listIncidentEvidence({ id, limit: 25 })).items : [];
  const next = history.pagination.continue;
  const html = `<section class="panel incident-header">
    <a href="${escapeHtml(incidentSubview(id, "", hash))}">← 返回故障详情</a>
    <h2>${escapeHtml(detail.incident.title)}</h2>
    <p>${escapeHtml(detail.incident.displayKey)} · 根因分析</p>
  </section>
  ${caps.run ? analysisForm(available, job) : ""}
  <section class="panel incident-header" data-rca-task aria-live="polite">${taskMarkup(job, caps, hash)}</section>
  ${caps.read ? `<section class="panel incident-header"><h2>运行记录</h2>
    ${history.items.length ? `<ul class="incident-timeline-list">${history.items.map((item) => `<li>
      <a href="${escapeHtml(incidentSubview(id, "rca", hash, { run: item.id, ...(after ? { runsFrom: after } : {}) }))}">
        ${formatTimestamp(item.createdAt)} · ${states[item.state]}</a>
      <span> · ${escapeHtml(item.actor)}</span>
    </li>`).join("")}</ul>` : "<p>暂无分析任务。</p>"}
    ${after ? `<a class="button secondary" href="${escapeHtml(incidentSubview(id, "rca", hash))}">返回最新任务</a>` : ""}
    ${next ? `<a class="button secondary" href="${escapeHtml(incidentSubview(id, "rca", hash, { runsFrom: next }))}">更早任务</a>` : ""}
  </section>` : ""}`;
  return { html, caps, job };
}

function analysisForm(available: IncidentEvidence[], job?: RCATask): string {
  const selected = new Set(job?.evidence.map(({ id }) => id) ?? available.slice(0, 5).map(({ id }) => id));
  return `<section class="panel incident-header"><h2>发起分析</h2>
    <form data-rca-start>
      <label>人工观察<textarea name="observation" rows="4" maxlength="1300">${escapeHtml(job?.observation)}</textarea></label>
      <details class="incident-actions"><summary>选择证据（最多 12 条）</summary>
        ${available.length ? available.map((item) => `<label class="rca-evidence-option"><input type="checkbox" name="evidenceIds" value="${escapeHtml(item.id)}"${selected.has(item.id) ? " checked" : ""}>
        ${escapeHtml(item.source)} · ${formatTimestamp(item.observedAt)} · ${escapeHtml(item.summary || item.kind)}</label>`).join("") :
        "<p>暂无已采集证据。可在故障详情采集后再发起。</p>"}
      </details>
      <p data-form-status role="status"></p>
      <button class="button" type="submit">发起根因分析</button>
    </form>
  </section>`;
}

function taskMarkup(job: RCATask | undefined, caps: Record<string, boolean>, hash: string): string {
  if (!job) return "<p>选择一项运行记录查看详情。</p>";
  const conclusion = job.revisions[0]?.conclusion;
  const status = `<h2>任务状态 · ${states[job.state]}</h2>
    <p>${formatTimestamp(job.createdAt)} · ${escapeHtml(job.actor)}${job.policyVersion ? ` · 策略版本 ${job.policyVersion}` : ""}</p>
    ${job.errorCode ? `<p role="alert">${failures[job.errorCode] ?? "分析未完成，请核对模型和权限配置。"}</p>` : ""}
    ${job.observation ? `<p class="incident-description">人工观察 · ${escapeHtml(job.observation)}</p>` : ""}
    ${caps.cancel && active(job) ? '<button type="button" data-rca-cancel class="button secondary">取消任务</button><p role="status" data-rca-cancel-status></p>' : ""}`;
  if (!conclusion) return status;
  return `${status}<h3>当前结论</h3>${conclusionMarkup(conclusion, job, hash)}
    ${caps.save ? editConclusion(conclusion) : ""}
    ${job.revisions.length > 1 ? `<h3>历史版本</h3>${job.revisions.slice(1).map((item) =>
      `<details class="incident-actions"><summary>版本 ${item.version} · ${escapeHtml(item.actor)} · ${formatTimestamp(item.at)}</summary>
        ${conclusionMarkup(item.conclusion, job, hash)}</details>`).join("")}` : ""}`;
}

function conclusionMarkup(value: RCAConclusion, job: RCATask, hash: string): string {
  const status = { supported: "有证据支持", insufficient: "证据不足", conflicting: "证据冲突" }[value.state];
  return `
    <dl class="rca-conclusion"><dt>判断</dt><dd>${status ?? ""} · ${escapeHtml(value.judgment)}</dd>
      <dt>待验证项</dt><dd>${escapeHtml(value.gaps)}</dd>
      <dt>只读验证建议</dt><dd>${escapeHtml(value.next)}</dd></dl>
    ${value.references.length ? `<ul>${value.references.map((id) => {
      const ref = job.evidence.find((item) => item.id === id);
      if (!ref) return "";
      const href = incidentSubview(job.incidentId, "", hash, { evidence: id });
      return `<li><a href="${escapeHtml(href)}">${escapeHtml(ref.source)} · ${escapeHtml(ref.summary || ref.kind)}</a></li>`;
    }).join("")}</ul>` : ""}`;
}

function editConclusion(value: RCAConclusion): string {
  return `<details class="incident-actions"><summary>人工修订结论</summary>
    <form data-rca-save>
      <label>判断<textarea name="judgment" required maxlength="530">${escapeHtml(value.judgment)}</textarea></label>
      <label>待验证项<textarea name="gaps" required maxlength="530">${escapeHtml(value.gaps)}</textarea></label>
      <label>只读验证建议<textarea name="next" required maxlength="530">${escapeHtml(value.next)}</textarea></label>
      <p data-form-status role="status"></p><button type="submit" class="button">保存新版本</button>
    </form></details>`;
}

const active = (job: RCATask) => job.state === "queued" || job.state === "running";

export function bindRcaPage(root: HTMLElement, id: string, hash: string, page: Awaited<ReturnType<typeof loadRcaPage>>): void {
  const region = root.querySelector<HTMLElement>("[data-rca-task]");
  if (!region) return;
  let job = page.job;
  const render = (value: RCATask) => {
    if (!region.isConnected) return;
    job = value;
    region.innerHTML = taskMarkup(value, page.caps, hash);
    bindTaskActions(value);
  };
  const poll = (value: RCATask) => {
    if (!active(value) || !page.caps.read) return;
    window.setTimeout(async () => {
      if (!region.isConnected || value.id !== job?.id || value.version !== job.version) return;
      try {
        const next = await apiClient.getRcaRun({ id, runId: value.id });
        if (job?.id !== value.id || job.version !== value.version || !region.isConnected) return;
        if (next.version !== value.version) render(next);
        poll(next);
      } catch (error) {
        if (region.isConnected && job?.id === value.id && job.version === value.version) {
          region.textContent = incidentError(error);
        }
      }
    }, 1500);
  };
  function bindTaskActions(value: RCATask): void {
    const cancel = region?.querySelector<HTMLButtonElement>("[data-rca-cancel]");
    cancel?.addEventListener("click", async () => {
      if (cancel.disabled) return;
      cancel.disabled = true;
      try {
        await apiClient.cancelRcaRun({ id, runId: value.id });
        const next = page.caps.read ? await apiClient.getRcaRun({ id, runId: value.id }) : { ...value, state: "canceled" as const };
        if (region?.isConnected) render(next);
      } catch (error) {
        if (cancel.isConnected) {
          const status = region?.querySelector<HTMLElement>("[data-rca-cancel-status]");
          if (status) status.textContent = incidentError(error);
          cancel.disabled = false;
        }
      }
    });
    const edit = region?.querySelector<HTMLFormElement>("[data-rca-save]");
    edit?.addEventListener("submit", async (event) => {
      event.preventDefault();
      const conclusion = value.revisions[0]?.conclusion;
      if (!conclusion) return;
      await submit(edit, async (fields) => {
        await apiClient.saveRcaConclusion({ id, runId: value.id, "If-Match": `"${value.version}"`,
          body: { ...conclusion, judgment: String(fields.get("judgment")), gaps: String(fields.get("gaps")), next: String(fields.get("next")) } });
        if (edit.isConnected) render(await apiClient.getRcaRun({ id, runId: value.id }));
      });
    });
  }
  if (job) {
    bindTaskActions(job);
    poll(job);
  }
  const start = root.querySelector<HTMLFormElement>("[data-rca-start]");
  let lastInput = "";
  let key = "";
  start?.addEventListener("submit", async (event) => {
    event.preventDefault();
    await submit(start, async (fields) => {
      const body = { observation: String(fields.get("observation") ?? ""), evidenceIds: fields.getAll("evidenceIds").map(String) };
      if (body.evidenceIds.length > 12) throw new Error("最多可选择 12 条证据。");
      if (activeTask(job)) throw new Error("当前故障已有运行中的任务。");
      const input = JSON.stringify(body);
      if (input !== lastInput || (job && !active(job))) {
        lastInput = input;
        key = Array.from(crypto.getRandomValues(new Uint8Array(16)), (byte) => byte.toString(16).padStart(2, "0")).join("");
      }
      const result = await apiClient.startRcaRun({ id, body, "Idempotency-Key": key });
      if (!start.isConnected) return;
      if (page.caps.read) window.location.hash = incidentSubview(id, "rca", hash, { run: result.id });
      else render(result);
    });
  });
}

function activeTask(job: RCATask | undefined): boolean { return job !== undefined && active(job); }

export async function submit(form: HTMLFormElement, operation: (fields: FormData) => Promise<void>): Promise<void> {
  const status = form.querySelector<HTMLElement>("[data-form-status]");
  const button = form.querySelector<HTMLButtonElement>('button[type="submit"]');
  if (!status || !button || button.disabled) return;
  button.disabled = true;
  status.textContent = "正在提交…";
  try {
    await operation(new FormData(form));
    if (form.isConnected) status.textContent = "已提交。";
  } catch (error) {
    if (form.isConnected) status.textContent = error instanceof Error && error.constructor === Error ? error.message : incidentError(error);
  } finally {
    button.disabled = false;
  }
}
