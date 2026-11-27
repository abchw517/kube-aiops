import { createOverviewClient } from "../../api/client.ts";
import { ApiError, type HealthInvestigation, type IncidentEvidence } from "../../api/generated.ts";
import { escapeHtml as h, formatTimestamp } from "../../components/html.ts";
import { incidentHref } from "./incident-filters.ts";
import { bindRcaPage, loadRcaPage } from "./incident-rca.ts";
import { renderCorrelationError, renderCorrelationEvidence, renderCorrelationSection } from "./correlation.ts";
import { bindFindingReview, findingReviewPanel, findingReviewTrigger } from "./finding-review.ts";
import { recentSources, recordDate, recordStatus, recordTags, recordTarget, type RecentRecord } from "./recent-records.ts";

const button = (action: string, label: string, tone = "") => `<button type="button" class="btn ${tone}" data-workbench="${action}">${label}</button>`;
function required<T extends HTMLElement>(root: ParentNode, selector: string): T {
  const element = root.querySelector<T>(selector);
  if (!element) throw new Error(`Missing recent diagnosis element: ${selector}`);
  return element;
}
const aiTitle = (row: RecentRecord) => row.source === "finding" ? "Finding AI 复核" : row.source === "incident" ? "事件 RCA" : "AI 深度调查";
const card = (title: string, content: string) => `<section class="card"><h3>${title}</h3>${content}</section>`;
const fields = (items: [string, string | undefined][]) => `<dl class="meta">${items.map(([key, value]) => `<dt>${key}</dt><dd>${h(value || "—")}</dd>`).join("")}</dl>`;

function errorMessage(error: unknown, fallback: string) {
  if (error instanceof ApiError) {
    if (error.status === 403) return "当前账号缺少该范围的权限。";
    if (error.status === 401) return "会话已失效，请重新登录后重试。";
    const body = error.body as { error?: { message?: string } } | null;
    if (body?.error?.message) return body.error.message;
    if (error.status === 504) return "调查请求在网关等待超时。可重试本次调查，持续超时请检查入口代理时限。";
  }
  return fallback;
}

function snapshot(row: RecentRecord) {
  const problem = row.problem;
  const alert = row.alert;
  const start = problem ? Date.parse(problem.startedAt) : NaN;
  const end = problem?.recoveredAt ? Date.parse(problem.recoveredAt) : Date.now();
  const duration = Number.isFinite(start) && Number.isFinite(end) ? `${Math.max(0,Math.floor((end-start)/60_000))} 分钟` : undefined;
  const original = problem?.originalURL && /^https?:\/\//i.test(problem.originalURL) ? problem.originalURL : null;
  return fields([
    ["原始级别", problem ? (["未分类","信息","警告","一般","严重","灾难"][problem.rawSeverity] ?? String(problem.rawSeverity)) : alert?.rawSeverity],
    ["确认状态", problem ? (problem.acknowledged ? "已确认" : "未确认") : undefined],
    ["恢复时间", problem?.recoveredAt ? formatTimestamp(problem.recoveredAt) : undefined],
    ["持续时间", duration],
    ["告警标签", problem?.tags?.map(({tag,value}) => `${tag}＝${value}`).join("、")],
  ]) + (original ? `<a href="${h(original)}" target="_blank" rel="noopener noreferrer">查看 Zabbix 原始问题</a>` : "");
}

function scopeOf(row: RecentRecord) {
  const scope = row.problem?.scope ?? row.alert?.scope;
  return [scope?.environmentId, scope?.businessId, row.cluster, row.alert?.namespace].filter(Boolean).join(" / ");
}

function healthAnalysis(result: HealthInvestigation, row: RecentRecord) {
  // Holmes health investigations return text. Recognize clear headings and retain the original text otherwise.
  const sections = new Map<string, string>();
  const headings = /^(?:#{1,6}\s*|\d+[.、]\s*)?(?:\*\*)?(事实|当前判断|证据来源及时间|证据依据|尚缺证据|待验证项?|验证建议|下一步)(?:\*\*)?\s*(?:[：:]\s*|$)/gm;
  const anchors = [...result.analysis.matchAll(headings)];
  for (const [index, heading] of anchors.entries()) {
    const text = result.analysis.slice(heading.index + heading[0].length, anchors[index + 1]?.index).trim().replace(/^\*\*\s*/, "");
    if (text) sections.set(heading[1] ?? "", text);
  }
  const pick = (...keys: string[]) => keys.map(key => sections.get(key)).filter(Boolean).join("\n");
  const judgment = pick("事实", "当前判断");
  const evidence = pick("证据来源及时间", "证据依据");
  const block = (title: string, value: string) => value ? card(title, `<div class="analysis-text">${h(value)}</div>`) : "";
  return `<div class="result">
    ${block(judgment ? "当前判断" : "调查结果", judgment || result.analysis)}
    ${card("证据引用", `${evidence ? `<div class="analysis-text">${h(evidence)}</div>` : ""}
      <button class="ref" type="button" data-workbench="snapshot">${h(row.origin)} · ${h(recordDate(row))} · 查看原始告警快照</button>`)}
    ${block("待验证", pick("尚缺证据","待验证","待验证项"))}
    ${block("下一步", pick("验证建议","下一步"))}
    ${result.appliedSkills?.length ? `<p class="muted">已应用技能 ${h(result.appliedSkills.join("、"))}</p>` : ""}
  </div>`;
}

export function bindHealthInvestigation(root: HTMLElement, rows: RecentRecord[], lifetime: AbortSignal,
  query: URLSearchParams, canReview: (row: RecentRecord) => boolean): void {
  root.addEventListener("click", event => {
    const opener = event.target instanceof Element && event.target.closest<HTMLElement>("[data-recent-record]");
    if (opener instanceof HTMLAnchorElement && (event.button || event.metaKey || event.ctrlKey || event.shiftKey || event.altKey)) return;
    if (!opener) return;
    const row = rows[Number(opener.dataset.recentRecord)];
    if (!row) return;
    event.preventDefault();
    openRecord(root, opener, row, lifetime, query, canReview(row));
  }, {signal:lifetime});
}

function openRecord(root: HTMLElement, opener: HTMLElement, row: RecentRecord, lifetime: AbortSignal, query: URLSearchParams, review: boolean) {
  const controller = new AbortController();
  const signal = AbortSignal.any([controller.signal, lifetime]);
  const client = createOverviewClient(signal);
  const finding = row.source === "finding";
  const incident = row.source === "incident";
  const health = row.source === "zabbix" || row.source === "alertmanager";
  const showAI = health || incident || review;
  const incidentURL = incidentHref(row.id, query.toString(), true);
  const findingURL = `#/findings/${encodeURIComponent(row.id)}?${new URLSearchParams({cluster:row.cluster,from:"overview",return:query.toString()})}`;
  const host = document.createElement("div");
  host.className = "recent-workspace";
  host.innerHTML = `<dialog id="workbench" aria-labelledby="record-title">
    <header class="drawer-top"><div class="row between"><p class="crumb">${recentSources[row.source]} · ${h(row.problem?.eventId ?? row.id)}</p>
      ${button("close","关闭","quiet")}</div><h2 id="record-title">${h(row.title)}</h2><div class="chips">${recordTags(row)}<span class="pill">${h(recordTarget(row))}</span></div></header>
    ${showAI ? '<nav class="drawer-tabs" aria-label="记录工作台"><button type="button" aria-pressed="true" data-workbench="detail">详情与证据</button><button type="button" aria-pressed="false" data-tab="ai" data-workbench="ai">AI 调查</button></nav>' : '<div></div>'}
    <div class="drawer-body"><div data-record-detail>
      ${card("问题详情", fields([["记录编号",row.problem?.eventId ?? row.id],["来源",recentSources[row.source]],
        ["主机或实例",recordTarget(row)],["所属范围",scopeOf(row)],["首次发现",`${recordDate(row)} · UTC+8`],["处置状态",recordStatus(row)]]))}
      ${card(finding ? "K8sGPT 原始诊断" : incident ? "事件记录" : "原始告警", `<pre class="pre" tabindex="-1" data-finding-explanation>${h(row.title)}</pre>${health ? snapshot(row) : ""}`)}
      ${health ? card("关联证据", `<details class="evidence" data-health-snapshot><summary>${h(row.origin)} 告警快照 · 1 条</summary><div class="inside">${snapshot(row)}</div></details>`) :
        `<div data-record-enrichment><p role="status" class="notice">正在读取详细记录…</p></div>`}
      ${finding || incident ? `<a class="btn" href="${h(incident ? incidentURL : findingURL)}">打开完整${incident ? "故障事件" : "诊断结果"}</a>` : ""}
      </div><div data-record-ai hidden><section class="card"><div class="row between"><h3>${aiTitle(row)}</h3><span class="pill ai">只读证据</span></div>
        <p class="muted">${finding ? "基于当前 Finding 和关联证据复核，可补充现场观察。" : incident ? "按事件范围核对关联告警、变更和时间线。" : "使用当前告警及其授权范围内的证据。"}</p>
        <p class="cycle">${h(scopeOf(row) || recordTarget(row))}</p></section>
        <div data-investigation-content></div></div></div>
    <footer class="drawer-foot row between"><small>${h(recordDate(row))} · UTC+8</small><div class="actions" data-workbench-actions></div></footer></dialog>`;
  root.append(host);
  const dialog = required<HTMLDialogElement>(host,"dialog");
  const detail = required(dialog,"[data-record-detail]");
  const ai = required(dialog,"[data-record-ai]");
  const investigation = required(dialog,"[data-investigation-content]");
  const actions = required(dialog,"[data-workbench-actions]");
  const body = required(dialog,".drawer-body");
  const sourceId = row.problem?.sourceId ?? row.alert?.sourceId;
  let aiLoaded = false;
  let activeTab = "detail";
  let request: AbortController | undefined;
  let runLabel = "发起 AI 深度调查";
  if (health) investigation.innerHTML = `<p data-result class="notice" role="status"${sourceId ? " hidden" : ""}>${sourceId ? "" : "缺少来源实例，请刷新告警列表。"}</p><div data-health-results></div>`;

  function refreshActions() {
    let markup = button("close","返回列表","quiet");
    if (activeTab === "detail" && showAI) markup += button("ai",aiTitle(row),"ai");
    if (activeTab === "ai") {
      markup = button("detail","返回详情","quiet");
      if (health && sourceId) markup += request ? button("cancel","取消调查","danger") : button("run",runLabel,"primary");
      if (finding && review) markup += button("assess","发起 AI 复核","primary");
      if (incident && investigation.querySelector("[data-rca-start]")) markup += button("run-rca","运行事件 RCA","primary");
    }
    actions.innerHTML = markup;
    if (activeTab === "ai" && finding) syncFindingControls();
  }

  const syncFindingControls = () => {
    const trigger = actions.querySelector<HTMLButtonElement>("[data-workbench=assess]");
    const assess = investigation.querySelector<HTMLButtonElement>("[data-review-action=assess]");
    if (!trigger) return;
    trigger.disabled = !assess || assess.disabled;
    trigger.textContent = assess?.textContent?.trim() || "发起 AI 复核";
  };

  function select(tab: "ai" | "detail", focus = true) {
    activeTab = tab;
    detail.hidden = tab !== "detail";
    ai.hidden = tab !== "ai";
    body.scrollTop = 0;
    for (const item of dialog.querySelectorAll<HTMLButtonElement>(".drawer-tabs button")) item.setAttribute("aria-pressed",String(item.dataset.workbench === tab));
    if (tab === "ai" && !aiLoaded) {
      aiLoaded = true;
      if (finding && review) mountFindingReview();
      if (incident) void mountRca();
    }
    refreshActions();
    if (focus) dialog.querySelector<HTMLElement>(`.drawer-tabs [data-workbench=${tab}]`)?.focus();
  }

  function mountFindingReview() {
    investigation.innerHTML = `${findingReviewTrigger()}<div data-review-saved hidden></div>${findingReviewPanel(true)}`;
    const trigger = investigation.querySelector<HTMLElement>("[data-review-action=toggle]");
    if (trigger) trigger.hidden = true;
    investigation.querySelector("#finding-review")?.removeAttribute("hidden");
    bindFindingReview(dialog,row.id,row.cluster,{signal, onEvidence: () => select("detail",false)});
    const observer = new MutationObserver(syncFindingControls);
    observer.observe(investigation,{childList:true,subtree:true,attributes:true,attributeFilter:["disabled"]});
    signal.addEventListener("abort",()=>observer.disconnect(),{once:true});
  }

  async function mountRca(hash = incidentURL) {
    investigation.innerHTML = '<p class="notice" role="status">正在读取事件 RCA…</p>';
    if (activeTab === "ai") refreshActions();
    try {
      const rca = await loadRcaPage(row.id, hash, {client, embedded:true});
      if (signal.aborted) return;
      if (!rca.caps.read && !rca.caps.run) {
        investigation.innerHTML = '<p class="notice bad" role="status">当前账号缺少该范围的 AI 调查权限。</p>';
        return;
      }
      investigation.innerHTML = rca.html;
      bindRcaPage(investigation,row.id,hash,rca,{client,embedded:true});
      if (activeTab === "ai") refreshActions();
    } catch (error) {
      if (!signal.aborted) investigation.innerHTML = `<p class="notice bad" role="status">${h(errorMessage(error,"事件 RCA 暂不可用，请重试。"))}</p>${button("retry-rca","重新读取")}`;
    }
  }

  async function loadDetail() {
    const extra = detail.querySelector<HTMLElement>("[data-record-enrichment]");
    if (!extra) return;
    try {
      if (finding) {
        const item = await client.getFinding({id:row.id,cluster:row.cluster});
        if (signal.aborted) return;
        extra.innerHTML = card("资源范围",fields([["集群",item.cluster],["命名空间",item.namespace ?? item.resource.namespace],["资源",item.resource.name]])) +
          renderCorrelationSection(row.id);
        const explanation = detail.querySelector("[data-finding-explanation]");
        if (explanation) explanation.textContent = item.originalDetails || item.details || item.problem;
        await loadFindingEvidence();
      } else if (incident) {
        const {incident:item,actions: caps} = await client.getIncident({id:row.id});
        if (signal.aborted) return;
        const explanation = detail.querySelector("[data-finding-explanation]");
        if (explanation) explanation.textContent = item.description || item.title;
        extra.innerHTML = card("资源范围",fields([["环境",item.scope.environmentId],["业务",item.scope.businessId],["集群",item.scope.clusterId],["命名空间",item.scope.namespace]])) +
          '<div data-incident-refs></div>';
        const evidence = caps.evidence ? await client.listIncidentEvidence({id:row.id,limit:25}) : undefined;
        if (!signal.aborted) {
          const target = extra.querySelector<HTMLElement>("[data-incident-refs]");
          if (target) target.innerHTML = card("关联证据",evidence ? renderIncidentRefs(evidence.items) : "<p>当前账号无权查看事件证据。</p>");
        }
      }
    } catch (error) {
      if (!signal.aborted) extra.innerHTML = `<p class="notice bad" role="status">${h(errorMessage(error,"读取详情或证据失败。"))}</p>${button("retry-detail","重新读取")}`;
    }
  }

  async function loadFindingEvidence() {
    const target = detail.querySelector<HTMLElement>("[data-correlation-finding]");
    if (!target) return;
    target.setAttribute("aria-busy","true");
    try {
      const evidence = await client.getFindingCorrelation({id:row.id,cluster:row.cluster});
      if (!signal.aborted) target.innerHTML = renderCorrelationEvidence(evidence);
    } catch (error) {
      if (!signal.aborted) target.innerHTML = renderCorrelationError(error);
    } finally { target.removeAttribute("aria-busy"); }
  }

  async function runHealth() {
    if (!sourceId || request) return;
    const current = new AbortController();
    request = current;
    const feedback = required(investigation,"[data-result]");
    const results = required(investigation,"[data-health-results]");
    results.replaceChildren();
    feedback.hidden = false;
    feedback.className = "notice";
    feedback.innerHTML = '<span class="pulse" aria-hidden="true"></span>正在读取证据并生成分析…';
    refreshActions();
    actions.querySelector<HTMLElement>("[data-workbench=cancel]")?.focus();
    try {
      const response = await createOverviewClient(AbortSignal.any([signal,current.signal])).investigateHealthAlert({source:row.source,sourceId,id:row.id});
      if (signal.aborted || current.signal.aborted) return;
      results.innerHTML = healthAnalysis(response,row);
      feedback.hidden = true;
      runLabel = "重新调查";
    } catch (error) {
      if (signal.aborted) return;
      feedback.className = "notice warn";
      feedback.textContent = current.signal.aborted ? "调查已取消，可重新发起。" : errorMessage(error,"调查未完成，请检查连接后重试。");
      runLabel = current.signal.aborted ? "发起 AI 深度调查" : "重试调查";
    } finally {
      request = undefined;
      if (!signal.aborted) {
        refreshActions();
        if (activeTab === "ai") actions.querySelector<HTMLElement>("[data-workbench=run]")?.focus();
      }
    }
  }

  function locateEvidence(target?: HTMLElement | null) {
    select("detail",false);
    const source = target?.closest<HTMLDetailsElement>("details");
    if (source) source.open = true;
    const destination = target?.querySelector<HTMLElement>("summary") ?? target;
    destination?.focus({preventScroll:true});
    destination?.scrollIntoView({block:"nearest"});
  }

  dialog.addEventListener("click", event => {
    if (!(event.target instanceof Element)) return;
    const control = event.target.closest<HTMLButtonElement>("[data-workbench]");
    const action = control?.dataset.workbench;
    switch (action) {
      case "close": dialog.close(); break;
      case "detail": select("detail"); break;
      case "ai": select("ai"); break;
      case "snapshot": locateEvidence(detail.querySelector<HTMLElement>("[data-health-snapshot]")); break;
      case "run": void runHealth(); break;
      case "cancel": request?.abort(); break;
      case "assess": investigation.querySelector<HTMLButtonElement>("[data-review-action=assess]")?.click(); break;
      case "retry-detail": void loadDetail(); break;
      case "run-rca": investigation.querySelector<HTMLFormElement>("[data-rca-start]")?.requestSubmit(); break;
      case "retry-rca": void mountRca(); break;
    }
    if (event.target.closest("[data-action=retry-correlation]")) { event.stopPropagation(); void loadFindingEvidence(); }
    const link = event.target.closest<HTMLAnchorElement>("[data-embedded-rca-link]");
    if (link && event instanceof MouseEvent && !event.metaKey && !event.ctrlKey && !event.shiftKey && !event.altKey) {
      event.preventDefault();
      const params = new URLSearchParams(link.hash.split("?")[1]);
      const evidence = params.get("evidence");
      if (evidence) {
        const target = [...detail.querySelectorAll<HTMLElement>("[data-incident-ref]")].find(item => item.dataset.incidentRef === evidence);
        if (target) locateEvidence(target);
        else void client.getIncidentEvidence({id:row.id,evidenceId:evidence}).then(item => {
          if (signal.aborted) return;
          const region = detail.querySelector<HTMLElement>("[data-incident-refs]");
          if (!region) return;
          region.insertAdjacentHTML("afterbegin",renderIncidentRefs([item]));
          locateEvidence(region.querySelector<HTMLElement>("[data-incident-ref]"));
        }).catch(() => {
          if (!signal.aborted) { dialog.close(); window.location.hash = link.hash; }
        });
      } else void mountRca(link.hash);
    }
  },{signal});

  lifetime.addEventListener("abort",()=>{ controller.abort(); host.remove(); },{once:true,signal});
  dialog.addEventListener("close",()=>{
    controller.abort();
    host.remove();
    if (opener.isConnected) opener.focus({preventScroll:true});
  },{once:true});
  select(opener.hasAttribute("data-recent-ai") && showAI ? "ai" : "detail",false);
  dialog.showModal();
  dialog.querySelector<HTMLElement>("[data-workbench=close]")?.focus();
  if (!health) void loadDetail();
}

function renderIncidentRefs(items: IncidentEvidence[]) {
  if (!items.length) return "<p>暂无已采集证据。</p>";
  return [...new Set(items.map(item => item.source))].map(source => {
    const group = items.filter(item => item.source === source);
    return `<details class="evidence"><summary>${h(source)} · ${group.length} 条</summary><div class="inside">${group.map(item =>
      `<p tabindex="-1" data-incident-ref="${h(item.id)}">${formatTimestamp(item.observedAt)} · ${h(item.summary || item.kind)}</p>`).join("")}</div></details>`;
  }).join("");
}
