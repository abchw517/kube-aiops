import { apiClient } from "../../api/client.ts";
import { ApiError, type Cluster, type CreateIncidentInput, type Incident, type IncidentSeverity, type ListIncidentsParams } from "../../api/generated.ts";
import { escapeHtml, formatTimestamp } from "../../components/html.ts";
import { diagnosticListPaths } from "../../domain/diagnostic-routes.ts";
import {
  bindIncidentFilters, environmentLabels, incidentEvents, incidentFilterHref, incidentFilters,
  incidentHref, incidentQuery, incidentStatusLabels, options, platformLabels, renderIncidentScope,
} from "./incident-filters.ts";
import { bindIncidentOverview, loadIncidentSeverityCounts, renderIncidentMetrics, renderIncidentOverviewCharts } from "./incident-overview.ts";

/** 将故障服务失败转换为可读的操作反馈。 */
export function incidentError(error: unknown): string {
  if (error instanceof ApiError) {
    if (error.status === 401) return "会话已过期，请重新登录。";
    if (error.status === 403) return "无权操作该故障或访问所选资源范围。";
    if (error.status === 404) return "故障或关联记录不存在。";
    if (error.status === 412) return "故障已被更新，请刷新后重试。";
    const message = (error.body as { error?: { message?: unknown } } | undefined)?.error?.message;
    if (typeof message === "string" && message.length < 300) return message;
  }
  return "操作未完成，请稍后重试。";
}

function createForm(clusters: Cluster[], draft: URLSearchParams): string {
  const cluster = draft.get("clusterId") || undefined;
  const candidates = clusters.map(({ id, name }) => [id, name] as const);
  if (cluster && !clusters.some(({ id }) => id === cluster)) candidates.push([cluster, cluster]);
  const names = options(candidates, cluster);
  const finding = draft.get("findingId");
  const asset = draft.get("assetId");
  return `
    <section class="panel incident-create">
      <h2>人工创建故障</h2>
      <form data-incident-create>
        <div class="incident-form-grid">
          <label>故障标题<input name="title" required maxlength="80"></label>
          <label>严重程度<select name="severity"><option>P1</option><option selected>P2</option><option>P3</option></select></label>
          <label>集群实例<select name="clusterId" required><option value="">选择实例</option>${names}</select></label>
          <label>命名空间<select name="namespace" required disabled><option value="">选择命名空间</option></select></label>
          <label>业务 ID<input name="businessId" maxlength="253" value="${escapeHtml(draft.get("businessId"))}" placeholder="共享命名空间时填写"></label>
          ${finding ? `<p>关联 Finding <code>${escapeHtml(finding)}</code></p>` : asset ?
          `<p>关联资产 <code>${escapeHtml(asset)}</code></p>` : '<label>资源类型<select name="kind"><option value="">命名空间</option><option value="Pod">Pod</option><option value="Deployment">Deployment</option></select></label><label>资源名称<input name="name" maxlength="253" placeholder="指定 Pod 或 Deployment 时填写"></label>'}
          <label class="incident-form-wide">故障描述<textarea name="description" maxlength="1300"></textarea></label>
        </div>
        <p role="status" data-incident-form-status></p>
        <button class="button" type="submit">创建故障</button>
      </form>
    </section>`;
}

/** 读取故障工作区数据，并以值班顺序组织总览和列表。 */
export async function loadIncidentWorkspace(hash: string, overview: boolean, canCreate: boolean, canList: boolean): Promise<string> {
  const filters = incidentFilters(hash);
  const params = { ...filters, continue: overview ? undefined : filters.continue };
  const firstPage = { ...params, continue: undefined };
  const draft = new URLSearchParams(hash.split("?")[1]);
  if (!canList && !canCreate) return "";
  const clusterRequest = apiClient.listClusters().then((result) => result.items).catch((error: unknown) => {
    if (error instanceof ApiError && error.status === 403) return [];
    throw error;
  });
  if (!canList) return createForm(await clusterRequest, draft);
  const summaryRequest = apiClient.getIncidentOverview(firstPage);
  const [clusters, page, summary, severityCounts] = await Promise.all([
    clusterRequest,
    apiClient.listIncidents({ ...params, limit: overview ? 5 : 50 }),
    summaryRequest,
    overview ? summaryRequest.then((summary) => loadIncidentSeverityCounts(firstPage, summary)) : undefined,
  ]);
  const query = incidentQuery(params);
  const firstQuery = incidentQuery(firstPage);
  const target = overview ? diagnosticListPaths.overview : incidentEvents;
  const table = renderIncidentTable(page.items, clusters, query, overview, firstPage);
  const scope = renderIncidentScope(clusters, params, target);
  const coverage = summary.coverage === "partial" || page.coverage === "partial"
    ? '<p class="system-notice">仅显示当前可获取的故障数据，统计或列表可能不完整。</p>' : "";
  const create = canCreate && draft.get("create") === "1" ? createForm(clusters, draft) : "";
  if (severityCounts) {
    const platforms = Object.entries(summary.byPlatform);
    return `<div class="incident-overview">
      ${create}
      ${scope}
      ${coverage}
      ${renderIncidentMetrics(summary, firstPage, severityCounts)}
      ${table}
      ${renderIncidentOverviewCharts(summary, severityCounts)}
      ${platforms.length ? `<details class="panel incident-overview-more"><summary>平台分布</summary><dl>${platforms.map(([platform, count]) =>
        `<div><dt>${escapeHtml(platformLabels[platform] ?? platform)}</dt><dd>${escapeHtml(count)}</dd></div>`).join("")}</dl></details>` : ""}
    </div>`;
  }

  const metrics = [
    ["活跃故障", summary.counts.active], ["待确认", summary.counts.pending],
    ["处理中", summary.counts.investigating], ["已恢复", summary.counts.recovered],
  ];
  return `<div class="incident-overview">
    ${scope}
    ${coverage}
    <div class="diagnostic-toolbar">
      <p>${summary.counts.total === undefined ? "暂不可用" : `共 ${summary.counts.total} 条故障`}</p>
      ${canCreate ? `<a class="button" href="${incidentEvents}?${escapeHtml(firstQuery)}${firstQuery ? "&amp;" : ""}create=1">人工创建故障</a>` : ""}
    </div>
    ${create}
    <section class="summary-grid" aria-label="故障汇总">
      ${metrics.map(([label, count], n) => `<div class="metric-card metric-${["total", "critical", "warning", "info"][n]}"><span>${label}</span><strong>${count ?? "暂不可用"}</strong></div>`).join("")}
    </section>
    ${table}
    ${params.continue || page.pagination.continue ? `<nav class="pagination" aria-label="故障分页">
      ${params.continue ? `<a class="button secondary" href="${incidentEvents}?${escapeHtml(firstQuery)}">返回首页</a>` : ""}
      ${page.pagination.continue ? `<a class="button secondary" href="${incidentEvents}?${escapeHtml(incidentQuery({ ...params, continue: page.pagination.continue }))}">下一页</a>` : ""}
    </nav>` : ""}
  </div>`;
}

/** 展示最近与完整故障列表，并保留原详情回跳参数。 */
function renderIncidentTable(items: Incident[], clusters: Cluster[], query: string, overview: boolean, firstPage: ListIncidentsParams): string {
  const title = overview ? "最近故障事件" : "故障列表";
  const link = escapeHtml(incidentFilterHref(incidentEvents, firstPage));
  return `<section class="panel table-panel incident-overview-panel">
    <header class="incident-overview-heading"><h2 id="incident-table-title">${title}</h2>
      ${overview ? `<a href="${link}" aria-label="查看全部故障事件">查看全部</a>` : ""}
    </header>
    ${items.length ? `<div class="table-wrap incident-table" role="region" aria-labelledby="incident-table-title" tabindex="0">
      <table><thead><tr><th scope="col">故障</th><th scope="col">环境</th><th scope="col">集群</th>
        <th scope="col">命名空间</th><th scope="col">级别</th><th scope="col">状态</th><th scope="col">最近发生</th></tr></thead>
        <tbody>${items.map((item) => renderIncidentRow(item, clusters, query, overview)).join("")}</tbody></table>
    </div>` : '<p class="incident-overview-empty" data-testid="incident-empty">当前范围内暂无故障。</p>'}
  </section>`;
}

function renderIncidentRow(item: Incident, clusters: Cluster[], query: string, overview: boolean): string {
  const cluster = clusters.find(({ id }) => id === item.scope.clusterId)?.name ?? item.scope.clusterId;
  const severityTone = { P1: "critical", P2: "warning", P3: "info" }[item.severity] ?? "info";
  const statusTone = { pending: "warning", investigating: "info", recovered: "recovered", closed: "closed" }[item.status] ?? "closed";
  return `<tr>
    <td><a href="${escapeHtml(incidentHref(item.id, query, overview))}">${escapeHtml(item.title)}</a><small>${escapeHtml(item.displayKey)}</small></td>
    <td>${escapeHtml(environmentLabels[item.scope.environmentId] ?? item.scope.environmentId) || "—"}</td>
    <td>${escapeHtml(cluster) || "—"}</td>
    <td>${escapeHtml(item.scope.namespace) || "—"}${item.scope.businessId ? `<small>${escapeHtml(item.scope.businessId)}</small>` : ""}</td>
    <td><span class="incident-status incident-status-${severityTone}">${escapeHtml(item.severity)}</span></td>
    <td><span class="incident-status incident-status-${statusTone}">${escapeHtml(incidentStatusLabels[item.status])}</span></td>
    <td>${formatTimestamp(item.lastSeen)}</td>
  </tr>`;
}

/** 将当前故障页面的表单和图表绑定到工作区。 */
export function bindIncidentWorkspace(root: HTMLElement, hash: string): void {
  bindIncidentOverview(root);
  const path = hash.split("?", 1)[0];
  bindIncidentFilters(root, path === diagnosticListPaths.overview ? path : incidentEvents);

  const form = root.querySelector<HTMLFormElement>("[data-incident-create]");
  if (!form) return;
  const draft = new URLSearchParams(hash.split("?")[1]);
  const cluster = form.querySelector<HTMLSelectElement>('select[name="clusterId"]');
  const namespace = form.querySelector<HTMLSelectElement>('select[name="namespace"]');
  const message = form.querySelector<HTMLElement>("[data-incident-form-status]");
  const button = form.querySelector<HTMLButtonElement>('button[type="submit"]');
  if (!cluster || !namespace || !message || !button) return;

  const anchored = draft.get("findingId") ?? undefined;
  const assetId = draft.get("assetId") ?? undefined;
  if (anchored || assetId) {
    cluster.disabled = true;
    if (draft.get("namespace")) namespace.innerHTML = options([[draft.get("namespace") ?? "", draft.get("namespace") ?? ""]]);
  }
  let namespaceRequest = 0;
  const updateNamespaces = async (selected?: string) => {
    const generation = ++namespaceRequest;
    namespace.disabled = true;
    namespace.innerHTML = '<option value="">选择命名空间</option>';
    if (!cluster.value) return;
    try {
      const { items } = await apiClient.listNamespaces({ cluster: cluster.value });
      if (!form.isConnected || generation !== namespaceRequest) return;
      namespace.innerHTML += options(items.map(({ name }) => [name, name]), selected);
      namespace.disabled = false;
      message.textContent = "";
    } catch (error) {
      if (form.isConnected && generation === namespaceRequest) message.textContent = incidentError(error);
    }
  };
  if (!anchored && !assetId) {
    cluster.addEventListener("change", () => void updateNamespaces());
    void updateNamespaces(draft.get("namespace") || undefined);
  }

  let previousPayload = "";
  let key = "";
  form.addEventListener("submit", async (event) => {
    event.preventDefault();
    if (button.disabled) return;
    const fields = new FormData(form);
    const kind = String(fields.get("kind") ?? "");
    const name = String(fields.get("name") ?? "").trim();
    if ((kind && !name) || (!kind && name)) {
      message.textContent = "请选择资源类型并填写资源名称。";
      return;
    }
    if (!cluster.value || !namespace.value) {
      message.textContent = "请选择集群实例和命名空间。";
      return;
    }
    const body: CreateIncidentInput = {
      title: String(fields.get("title") ?? "").trim(), description: String(fields.get("description") ?? "").trim(),
      severity: fields.get("severity") as IncidentSeverity, findingId: anchored, assetId,
      scope: { clusterId: cluster.value, namespace: namespace.value, businessId: String(fields.get("businessId") ?? "").trim() || undefined },
      resource: kind ? { kind, name } : undefined,
    };
    const payload = JSON.stringify(body);
    if (payload !== previousPayload) {
      previousPayload = payload;
      key = Array.from(crypto.getRandomValues(new Uint8Array(16)), (byte) => byte.toString(16).padStart(2, "0")).join("");
    }
    button.disabled = true;
    message.textContent = "正在创建故障…";
    try {
      const created = await apiClient.createIncident({ body, "Idempotency-Key": key });
      if (form.isConnected) window.location.hash = incidentHref(created.id, incidentQuery(incidentFilters(hash)));
    } catch (error) {
      if (form.isConnected) message.textContent = incidentError(error);
    } finally {
      button.disabled = false;
    }
  });
}
