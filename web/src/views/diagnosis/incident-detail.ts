import { overviewSearch } from "../../domain/unified-diagnostics.ts";
import { apiClient } from "../../api/client.ts";
import type { Incident, IncidentDetail, IncidentEvidence, IncidentSeverity, IncidentStatus, IncidentTimelineEntry } from "../../api/generated.ts";
import { escapeHtml, formatTimestamp } from "../../components/html.ts";
import { incidentEvents, incidentFilters, incidentHref, incidentQuery, incidentStatusLabels } from "./incident-filters.ts";
import { incidentError } from "./incident-list.ts";

import { incidentSubview } from "./incident-rca.ts";

const sourceLabels: Record<string, string> = {
  "kubernetes-events": "Kubernetes 事件", prometheus: "Prometheus 指标",
  alertmanager: "Alertmanager 告警", victorialogs: "VictoriaLogs 日志",
  zabbix: "Zabbix", "aliyun-ecs": "阿里云 ECS", "tencent-cvm": "腾讯云 CVM",
};
const sourceStates: Record<string, string> = { available: "可用", partial: "部分可用", unavailable: "不可用", disabled: "未启用" };
const timelineLabels: Record<string, string> = {
  created: "创建故障", updated: "更新故障", correlated: "采集证据", merged: "已归并",
  rca_queued: "启动根因分析", rca_completed: "分析任务结束", rca_canceled: "取消分析", rca_saved: "修订 RCA 结论",
  topology_changed: "更新资源关系", signal_received: "接收连接器信号",
  merged_into: "接收归并", split: "拆分证据", split_created: "从已有故障拆分",
};
const transition: Record<IncidentStatus, IncidentStatus[]> = {
  pending: ["investigating"], investigating: ["recovered"], recovered: ["investigating", "closed"], closed: [],
};

function detailQuery(hash: string): URLSearchParams {
  return new URLSearchParams(hash.split("?")[1]);
}

export async function loadIncidentDetail(id: string, hash: string): Promise<{ html: string; detail: IncidentDetail }> {
  const params = detailQuery(hash);
  const detail = await apiClient.getIncident({ id });
  const { incident, actions } = detail;
  const evidenceID = params.get("evidence");
  const [timeline, evidence, highlight] = await Promise.all([
    apiClient.listIncidentTimeline({ id, continue: params.get("timelineFrom") || undefined }).catch((error: unknown) => error),
    actions.evidence ? apiClient.listIncidentEvidence({ id, continue: params.get("evidenceFrom") || undefined }).catch((error: unknown) => error) : undefined,
    actions.evidence && evidenceID ? apiClient.getIncidentEvidence({ id, evidenceId: evidenceID }).catch(() => undefined) : undefined,
  ]);
  const fromOverview = params.get("from") === "overview";
  const returnParams = new URLSearchParams(params.get("return") ?? "");
  const returns = fromOverview
    ? overviewSearch(returnParams).toString()
    : incidentQuery(incidentFilters(`#?${returnParams}`));
  const back = `${fromOverview ? "#/diagnostics" : incidentEvents}?${returns}`;
  const finding = incident.findingId ?
    `<a href="#/findings/${encodeURIComponent(incident.findingId)}?cluster=${encodeURIComponent(incident.scope.clusterId)}">查看来源 Finding</a>` : "";
  const evidenceItems = evidence && typeof evidence === "object" && "items" in evidence ? evidence as Awaited<ReturnType<typeof apiClient.listIncidentEvidence>> : null;
  const timelineItems = timeline && typeof timeline === "object" && "items" in timeline ? timeline as Awaited<ReturnType<typeof apiClient.listIncidentTimeline>> : null;
  const evidenceList = evidenceItems?.items ?? [];
  const visibleEvidence = highlight && !evidenceList.some(({ id }) => id === highlight.id) ? [highlight, ...evidenceList] : evidenceList;
  const evidencePanel = evidenceItems ? evidenceGroups(visibleEvidence, incident, actions.split) : sectionError(evidence);
  const timelinePanel = timelineItems ? renderTimeline(timelineItems.items) : sectionError(timeline);
  return {
    detail,
    html: `
    <section class="panel incident-header" data-testid="incident-detail">
      <a href="${escapeHtml(back)}">← 返回${fromOverview ? "诊断总览" : "故障事件"}</a>
      <h2>${escapeHtml(incident.title)}</h2>
      <p>${escapeHtml(incident.displayKey)} · ${escapeHtml(incident.severity)} · ${escapeHtml(incidentStatusLabels[incident.status])} · 版本 ${incident.version}</p>
      <dl class="incident-info">
        <div><dt>资源范围</dt><dd>${escapeHtml([incident.scope.environmentId, incident.scope.businessId, incident.scope.clusterId, incident.scope.namespace].filter(Boolean).join(" / "))}</dd></div>
        <div><dt>关联资源</dt><dd>${escapeHtml([incident.resource.kind, incident.resource.name].filter(Boolean).join(" / "))}</dd></div>
        <div><dt>首次发生</dt><dd>${formatTimestamp(incident.firstSeen)}</dd></div>
        <div><dt>最近发生</dt><dd>${formatTimestamp(incident.lastSeen)}</dd></div>
      </dl>
      ${incident.description ? `<p class="incident-description">${escapeHtml(incident.description)}</p>` : ""}
      ${incident.ownerGroupId ? `<p>负责人组 ${escapeHtml(incident.ownerGroupId)}</p>` : ""}
      ${incident.mergedInto ? `<p><a href="${escapeHtml(incidentHref(incident.mergedInto, returns, fromOverview))}">查看归并后的故障</a></p>` : ""}
      ${finding}
      <div class="diagnostic-toolbar">
        ${actions.rca?.read || actions.rca?.run ? `<a class="button secondary" href="${escapeHtml(incidentSubview(id, "rca", hash))}">AI 深度调查</a>` : ""}
        ${actions.topology ? `<a class="button secondary" href="${escapeHtml(incidentSubview(id, "topology", hash))}">关联拓扑</a>` : ""}
      </div>
      ${actions.update ? updateForm(incident) : ""}
    </section>
    ${actions.evidence ? `<section class="panel incident-evidence">
      <div class="diagnostic-toolbar"><h2>关联证据</h2>
      ${actions.correlate && !["Host", "ECS", "CVM"].includes(incident.resource.kind ?? "") ?
        '<button class="button secondary" data-incident-collect>采集证据</button>' : ""}</div>
      <p role="status" data-incident-collect-status></p>
      ${detail.counts ? `<p>共 ${detail.counts.evidence ?? 0} 条证据快照 · ${detail.coverage === "partial" ? "来源部分可用" : detail.coverage === "complete" ? "已覆盖配置的数据源" : "暂无可用的采集结果"}</p>` : ""}
      ${evidencePanel}
      ${sectionPageLinks(hash, "evidenceFrom", evidenceItems?.pagination.continue)}
      ${actions.correlate ? ownershipForms(actions.split && Boolean(evidenceItems?.items.length), detail.candidates ?? []) : ""}
    </section>` : ""}
    <section class="panel incident-timeline"><h2>故障时间线</h2>
      ${timelinePanel}${sectionPageLinks(hash, "timelineFrom", timelineItems?.pagination.continue)}
    </section>`,
  };
}

function sectionError(error: unknown): string {
  return `<p role="alert">${escapeHtml(incidentError(error))} <button class="button secondary" data-action="retry">重新加载</button></p>`;
}

function sectionPageLinks(hash: string, key: string, next?: string): string {
  const params = detailQuery(hash);
  const path = hash.split("?")[0];
  const links: string[] = [];
  if (params.has(key)) {
    params.delete(key);
    links.push(`<a href="${escapeHtml(`${path}?${params}`)}">返回此区首页</a>`);
  }
  if (next) {
    params.set(key, next);
    links.push(`<a href="${escapeHtml(`${path}?${params}`)}">此区下一页</a>`);
  }
  return links.length ? `<nav class="pagination" aria-label="${key === "timelineFrom" ? "时间线分页" : "证据分页"}">${links.join("")}</nav>` : "";
}

function renderTimeline(items: IncidentTimelineEntry[]): string {
  return items.length ? `<ol class="incident-timeline-list">${items.map((entry) =>
    `<li><strong>${escapeHtml(timelineLabels[entry.kind] ?? entry.kind)}</strong> · ${formatTimestamp(entry.at)}
      <p>${escapeHtml([entry.actor, timelineValue(entry.before), timelineValue(entry.after), entry.reason].filter(Boolean).join(" · "))}</p></li>`).join("")}</ol>` : "<p>暂无时间线记录。</p>";
}

function timelineValue(value?: string): string {
  return value ? incidentStatusLabels[value as IncidentStatus] ?? value : "";
}

function evidenceGroups(items: IncidentEvidence[], incident: Incident, selectable: boolean): string {
  if (!items.length && !incident.sources?.length) return "<p>暂无已采集证据。</p>";
  return Object.entries(sourceLabels).map(([source, label]) => {
    const group = items.filter((item) => item.source === source);
    const status = incident.sources?.find((entry) => entry.source === source)?.state;
    if (!group.length && !status) return "";
    return `<details class="incident-source"><summary>
      <strong>${label}</strong><span>${status ? sourceStates[status] : "已有留存"} · ${group.length} 条</span>
      </summary>
      ${group.length ? group.map((item) => `<div class="incident-evidence-row">
        <p>${formatTimestamp(item.observedAt)} · ${escapeHtml(item.summary || item.kind)}</p>
        ${selectable ? `<label><input type="checkbox" name="evidenceIds" value="${escapeHtml(item.id)}" form="incident-split-form"> 选中以拆分</label>` : ""}
        <button class="button secondary" data-incident-evidence="${escapeHtml(item.id)}" aria-expanded="false">查看证据明细</button>
        <pre hidden></pre></div>`).join("") : "<p>当前页暂无此来源的留存证据。</p>"}
    </details>`;
  }).join("");
}

function updateForm(item: Incident): string {
  return `<details class="incident-actions"><summary>更新故障</summary>
    <form data-incident-operation="update">
      <div class="incident-form-grid">
        <label>状态<select name="status">${[item.status, ...transition[item.status]].map((status) => `<option value="${status}">${incidentStatusLabels[status]}</option>`).join("")}</select></label>
        <label>严重程度<select name="severity">${["P1", "P2", "P3"].map((value) => `<option${item.severity === value ? " selected" : ""}>${value}</option>`).join("")}</select></label>
        <label>负责人组 ID<input name="ownerGroupId" value="${escapeHtml(item.ownerGroupId)}"></label>
        <label>处理说明<input name="reason" required maxlength="320"></label>
      </div><p role="status"></p><button class="button" type="submit">保存变更</button>
    </form>
  </details>`;
}

function ownershipForms(canSplit: boolean, candidates: Incident[]): string {
  return `<details class="incident-actions"><summary>归并故障</summary><form data-incident-operation="merge">
    ${candidates.length ? `<p>可归并的同资源故障</p><ul>${candidates.map((item) =>
      `<li><button type="button" class="button secondary" data-merge-id="${escapeHtml(item.id)}" data-merge-version="${item.version}">${escapeHtml(item.displayKey)} · ${escapeHtml(item.title)}</button></li>`).join("")}</ul>` : ""}
    <div class="incident-form-grid">
      <label>源故障 ID<input name="sourceId" required maxlength="253"></label>
      <label>源故障版本<input name="sourceVersion" type="number" min="1" required></label>
      <label>归并原因<input name="reason" required maxlength="320"></label>
    </div><p role="status"></p><button class="button" type="submit">归并到当前故障</button>
  </form></details>
  ${canSplit ? `<details class="incident-actions"><summary>拆分故障</summary>
    <form data-incident-operation="split" id="incident-split-form">
      <p>先在关联证据中选中要移到新故障的证据。</p>
      <div class="incident-form-grid"><label>新故障标题<input name="title" required maxlength="80"></label>
      <label>拆分原因<input name="reason" required maxlength="320"></label></div>
      <p role="status"></p><button class="button" type="submit">创建拆分故障</button>
    </form></details>` : ""}`;
}

export function bindIncidentDetail(root: HTMLElement, detail: IncidentDetail, hash: string, refresh: () => void): void {
  const id = detail.incident.id;
  const version = { id, "If-Match": `"${detail.version}"` };
  const collect = root.querySelector<HTMLButtonElement>("[data-incident-collect]");
  const collectStatus = root.querySelector<HTMLElement>("[data-incident-collect-status]");
  collect?.addEventListener("click", async () => {
    if (!collectStatus || collect.disabled) return;
    collect.disabled = true;
    collectStatus.textContent = "正在采集证据…";
    try {
      await apiClient.collectIncidentEvidence(version);
      if (collect.isConnected) refresh();
    } catch (error) {
      if (collect.isConnected) collectStatus.textContent = incidentError(error);
    } finally {
      collect.disabled = false;
    }
  });

  const selectedEvidence = detailQuery(hash).get("evidence");
  for (const button of root.querySelectorAll<HTMLButtonElement>("[data-incident-evidence]")) {
    button.addEventListener("click", async () => {
      const content = button.nextElementSibling;
      const evidenceId = button.dataset.incidentEvidence;
      if (!(content instanceof HTMLElement) || !evidenceId || button.disabled) return;
      if (!content.hidden) {
        content.hidden = true;
        button.setAttribute("aria-expanded", "false");
        return;
      }
      content.hidden = false;
      button.setAttribute("aria-expanded", "true");
      if (content.dataset.loaded) return;
      button.disabled = true;
      content.textContent = "正在读取证据…";
      try {
        const result = await apiClient.getIncidentEvidence({ id, evidenceId });
        content.textContent = JSON.stringify(result.signal, null, 2);
        content.dataset.loaded = "1";
      } catch (error) {
        content.textContent = incidentError(error);
      } finally {
        button.disabled = false;
      }
    });
  }

  if (selectedEvidence) {
    const button = [...root.querySelectorAll<HTMLButtonElement>("[data-incident-evidence]")].find((item) => item.dataset.incidentEvidence === selectedEvidence);
    if (button) {
      const group = button.closest("details");
      if (group) group.open = true;
      button.click();
      button.focus();
    }
  }

  for (const button of root.querySelectorAll<HTMLButtonElement>("[data-merge-id]")) {
    button.addEventListener("click", () => {
      const form = button.form;
      const source = form?.elements.namedItem("sourceId");
      const expected = form?.elements.namedItem("sourceVersion");
      if (source instanceof HTMLInputElement && expected instanceof HTMLInputElement) {
        source.value = button.dataset.mergeId ?? "";
        expected.value = button.dataset.mergeVersion ?? "";
      }
    });
  }

  for (const form of root.querySelectorAll<HTMLFormElement>("[data-incident-operation]")) {
    form.addEventListener("submit", async (event) => {
      event.preventDefault();
      const button = form.querySelector<HTMLButtonElement>('button[type="submit"]');
      const status = form.querySelector<HTMLElement>('[role="status"]');
      if (!button || !status || button.disabled) return;
      const fields = new FormData(form);
      const text = (name: string) => String(fields.get(name) ?? "").trim();
      const reason = text("reason");
      button.disabled = true;
      status.textContent = "正在提交…";
      try {
        if (form.dataset.incidentOperation === "update") {
          await apiClient.updateIncident({ ...version, body: {
            status: text("status") as IncidentStatus, severity: text("severity") as IncidentSeverity, ownerGroupId: text("ownerGroupId"), reason,
          } });
        } else if (form.dataset.incidentOperation === "merge") {
          await apiClient.mergeIncidents({ ...version, body: { sourceId: text("sourceId"), sourceVersion: Number(fields.get("sourceVersion")), reason } });
        } else {
          const evidenceIds = fields.getAll("evidenceIds").map(String);
          if (!evidenceIds.length) {
            status.textContent = "请至少选中一条关联证据。";
            return;
          }
          const child = await apiClient.splitIncident({ ...version, body: { evidenceIds, title: text("title"), reason } });
          const params = detailQuery(hash);
          if (form.isConnected) window.location.hash = incidentHref(child.id, params.get("return") ?? "", params.get("from") === "overview");
          return;
        }
        if (form.isConnected) refresh();
      } catch (error) {
        if (form.isConnected) status.textContent = incidentError(error);
      } finally {
        button.disabled = false;
      }
    });
  }
}
