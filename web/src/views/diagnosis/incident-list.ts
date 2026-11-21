import { apiClient } from "../../api/client.ts";
import { ApiError, type Cluster, type CreateIncidentInput, type Incident, type IncidentSeverity, type IncidentStatus, type ListIncidentsParams } from "../../api/generated.ts";
import { escapeHtml, formatTimestamp } from "../../components/html.ts";

export const incidentEvents = "#/diagnostics/events";
export const incidentPrefix = "#/diagnostics/incidents/";
export const incidentStatusLabels: Record<IncidentStatus, string> = {
  pending: "待确认", investigating: "处理中", recovered: "已恢复", closed: "已关闭",
};
const platformLabels: Record<string, string> = { kubernetes: "Kubernetes / ACK / TKE", zabbix: "Zabbix 主机", "aliyun-ecs": "阿里云 ECS", "tencent-cvm": "腾讯云 CVM" };
const environmentLabels: Record<string, string> = { development: "研发", testing: "测试", staging: "预发", production: "生产" };

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

export function incidentFilters(hash: string): ListIncidentsParams {
  const p = new URLSearchParams(hash.split("?")[1]);
  const value = (key: string) => p.get(key)?.trim() || undefined;
  const status = value("status");
  const severity = value("severity");
  return {
    clusterId: value("clusterId"), namespace: value("namespace"), environmentId: value("environmentId"), businessId: value("businessId"),
    status: status && Object.hasOwn(incidentStatusLabels, status) ? status as IncidentStatus : undefined,
    severity: severity === "P1" || severity === "P2" || severity === "P3" ? severity : undefined,
    q: value("q"), platform: value("platform"), continue: value("continue"),
  };
}

export function incidentQuery(p: ListIncidentsParams): string {
  const query = new URLSearchParams();
  for (const [name, value] of Object.entries(p)) {
    if (value !== undefined && value !== "") query.set(name, String(value));
  }
  return query.toString();
}

export function incidentHref(id: string, query: string, overview = false): string {
  const params = new URLSearchParams({ return: query });
  if (overview) params.set("from", "overview");
  return `${incidentPrefix}${encodeURIComponent(id)}?${params}`;
}

function options(items: Array<readonly [string, string]>, selected?: string): string {
  return items.map(([id, name]) =>
    `<option value="${escapeHtml(id)}"${id === selected ? " selected" : ""}>${escapeHtml(name)}</option>`,
  ).join("");
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

export async function loadIncidentWorkspace(hash: string, overview: boolean, canCreate: boolean, canList: boolean): Promise<string> {
  const params = incidentFilters(hash);
  if (overview) delete params.continue;
  const draft = new URLSearchParams(hash.split("?")[1]);
  const clusterRequest = apiClient.listClusters().then((result) => result.items).catch((error: unknown) => {
    if (error instanceof ApiError && error.status === 403) return [];
    throw error;
  });
  if (!canList) return canCreate ? createForm(await clusterRequest, draft) : "";
  const [clusters, page, summary] = await Promise.all([
    clusterRequest,
    apiClient.listIncidents({ ...params, limit: overview ? 5 : 50 }),
    apiClient.getIncidentOverview(params),
  ]);
  const query = incidentQuery(params);
  const firstQuery = incidentQuery({ ...params, continue: undefined });
  const target = overview ? "#/diagnostics" : incidentEvents;
  const pageRows = page.items.map((item) => renderIncidentRow(item, clusters, query, overview)).join("");
  const metrics = [
    ["活跃故障", summary.counts.active], ["待确认", summary.counts.pending],
    ["处理中", summary.counts.investigating], ["已恢复", summary.counts.recovered],
  ];
  return `
    <div class="diagnostic-toolbar">
      <p>共 ${summary.counts.total ?? 0} 条故障</p>
      ${overview ? `<a class="button" href="${incidentEvents}?${escapeHtml(firstQuery)}">查看故障事件</a>` :
        canCreate ? `<a class="button" href="${incidentEvents}?${escapeHtml(firstQuery)}${firstQuery ? "&amp;" : ""}create=1">人工创建故障</a>` : ""}
    </div>
    ${canCreate && draft.get("create") === "1" ? createForm(clusters, draft) : ""}
    <form class="panel incident-filters" data-incident-filters data-target="${target}" aria-label="故障筛选">
      <label>环境<select name="environmentId"><option value="">全部环境</option>${options(Object.entries(environmentLabels), params.environmentId)}</select></label>
      <label>集群实例<select name="clusterId"><option value="">全部实例</option>${options(clusters.map(({ id, name }) => [id, name]), params.clusterId)}</select></label>
      <label>命名空间<input name="namespace" value="${escapeHtml(params.namespace)}"></label>
      <label>业务 ID<input name="businessId" value="${escapeHtml(params.businessId)}"></label>
      <label>平台<select name="platform"><option value="">全部平台</option>${options(Object.entries(platformLabels), params.platform)}</select></label>
      <label>状态<select name="status"><option value="">全部状态</option>${options(Object.entries(incidentStatusLabels), params.status)}</select></label>
      <label>严重程度<select name="severity"><option value="">全部级别</option>${options([["P1", "P1"], ["P2", "P2"], ["P3", "P3"]], params.severity)}</select></label>
      <label>关键词<input name="q" type="search" maxlength="80" value="${escapeHtml(params.q)}"></label>
      <button class="button secondary" type="submit">筛选</button>
      <a href="${target}">清除筛选</a>
    </form>
    <section class="summary-grid" aria-label="故障汇总">
      ${metrics.map(([label, count], n) => `<div class="metric-card metric-${["total", "critical", "warning", "info"][n]}"><span>${label}</span><strong>${count ?? 0}</strong></div>`).join("")}
    </section>
    ${overview && Object.keys(summary.byPlatform).length ? `<section class="panel incident-trend"><h2>平台分布</h2><dl>${Object.entries(summary.byPlatform).map(([platform, count]) =>
      `<div><dt>${escapeHtml(platformLabels[platform] ?? platform)}</dt><dd>${count}</dd></div>`).join("")}</dl></section>` : ""}
    ${overview ? `<section class="panel incident-trend"><h2>近七日新发故障（UTC）</h2><dl>${Object.entries(summary.trend)
      .sort(([a], [b]) => a.localeCompare(b)).map(([day, count]) => `<div><dt>${escapeHtml(day.slice(5))}</dt><dd>${count}</dd></div>`).join("")}</dl></section>` : ""}
    ${pageRows ? `<section class="panel table-panel"><h2 class="section-heading">${overview ? "最近故障" : "故障列表"}</h2>
      <div class="table-wrap incident-table" role="region" aria-label="故障列表" tabindex="0">
        <table><thead><tr><th>故障</th><th>级别</th><th>状态</th><th>资源范围</th><th>最近发生</th></tr></thead>
        <tbody>${pageRows}</tbody></table></div></section>` :
      '<section class="state-card" data-testid="incident-empty"><h2>暂无故障</h2><p>当前范围内没有符合筛选条件的故障。</p></section>'}
    ${!overview && (params.continue || page.pagination.continue) ? `<nav class="pagination" aria-label="故障分页">
      ${params.continue ? `<a class="button secondary" href="${incidentEvents}?${escapeHtml(firstQuery)}">返回首页</a>` : ""}
      ${page.pagination.continue ? `<a class="button secondary" href="${incidentEvents}?${escapeHtml(incidentQuery({ ...params, continue: page.pagination.continue }))}">下一页</a>` : ""}
    </nav>` : ""}`;
}

function renderIncidentRow(item: Incident, clusters: Cluster[], query: string, overview: boolean): string {
  const cluster = clusters.find(({ id }) => id === item.scope.clusterId)?.name ?? item.scope.clusterId;
  return `<tr>
    <td><a href="${escapeHtml(incidentHref(item.id, query, overview))}">${escapeHtml(item.title)}</a><small>${escapeHtml(item.displayKey)}</small></td>
    <td>${escapeHtml(item.severity)}</td><td>${escapeHtml(incidentStatusLabels[item.status])}</td>
    <td>${escapeHtml([environmentLabels[item.scope.environmentId] || item.scope.environmentId, item.scope.businessId, cluster, item.scope.namespace].filter(Boolean).join(" / "))}</td>
    <td>${formatTimestamp(item.lastSeen)}</td>
  </tr>`;
}

export function bindIncidentWorkspace(root: HTMLElement, hash: string): void {
  const filters = root.querySelector<HTMLFormElement>("[data-incident-filters]");
  filters?.addEventListener("submit", (event) => {
    event.preventDefault();
    const query = new URLSearchParams();
    for (const [name, value] of new FormData(filters)) {
      if (String(value).trim()) query.set(name, String(value).trim());
    }
    window.location.hash = `${filters.dataset.target}?${query}`;
  });

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
