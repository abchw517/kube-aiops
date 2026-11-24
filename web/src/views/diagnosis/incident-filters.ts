import type { Cluster, IncidentStatus, ListIncidentsParams } from "../../api/generated.ts";
import { escapeHtml } from "../../components/html.ts";
import { diagnosticListPaths } from "../../domain/diagnostic-routes.ts";

export const incidentEvents = diagnosticListPaths.events;
export const incidentPrefix = "#/diagnostics/incidents/";
export const incidentStatusLabels: Record<IncidentStatus, string> = {
  pending: "待确认", investigating: "处理中", recovered: "已恢复", closed: "已关闭",
};
export const platformLabels: Record<string, string> = { kubernetes: "Kubernetes / ACK / TKE", zabbix: "Zabbix 主机", "aliyun-ecs": "阿里云 ECS", "tencent-cvm": "腾讯云 CVM" };
export const environmentLabels: Record<string, string> = { development: "研发", testing: "测试", staging: "预发", production: "生产" };

/** 读取故障 URL 中受支持的查询字段。 */
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

/** 序列化故障查询，用于分页和详情返回。 */
export function incidentQuery(p: ListIncidentsParams): string {
  const query = new URLSearchParams();
  for (const [name, value] of Object.entries(p)) {
    if (value !== undefined && value !== "") query.set(name, String(value));
  }
  return query.toString();
}

/** 编码故障标识和返回查询，确保详情能恢复原来范围。 */
export function incidentHref(id: string, query: string, overview = false): string {
  const params = new URLSearchParams({ return: query });
  if (overview) params.set("from", "overview");
  return `${incidentPrefix}${encodeURIComponent(id)}?${params}`;
}

/** 对所有选项文本和值统一转义，并保留当前选择。 */
export function options(items: Array<readonly [string, string]>, selected?: string): string {
  return items.map(([id, name]) =>
    `<option value="${escapeHtml(id)}"${id === selected ? " selected" : ""}>${escapeHtml(name)}</option>`,
  ).join("");
}

type IncidentListPath = typeof diagnosticListPaths.overview | typeof incidentEvents;

/** 生成从第一页开始的查询链接，排除分页参数和临时操作。 */
export function incidentFilterHref(path: IncidentListPath, params: ListIncidentsParams): string {
  const query = incidentQuery({ ...params, continue: undefined, limit: undefined });
  return query ? `${path}?${query}` : path;
}

/** 为当前筛选的子集生成快捷入口，遇到冲突时保留指标文字。 */
export function incidentMetricHref(
  params: ListIncidentsParams,
  filter: { key: "status"; value: IncidentStatus } | { key: "severity"; value: "P1" },
): string | undefined {
  if (params[filter.key] && params[filter.key] !== filter.value) return undefined;
  return incidentFilterHref(incidentEvents, { ...params, [filter.key]: filter.value });
}

/** 折叠表单时仍显示已提交的条件，集群名称来自可见列表。 */
export function renderIncidentScope(clusters: Cluster[], params: ListIncidentsParams, target: IncidentListPath): string {
  const clusterNames = clusters.map(({ id, name }) => [id, name] as const);
  const fields = [
    { key: "environmentId", label: "环境", choices: Object.entries(environmentLabels) },
    { key: "clusterId", label: "集群实例", choices: clusterNames },
    { key: "namespace", label: "命名空间" },
    { key: "businessId", label: "业务 ID" },
    { key: "platform", label: "平台", choices: Object.entries(platformLabels) },
    { key: "status", label: "状态", choices: Object.entries(incidentStatusLabels) },
    { key: "severity", label: "严重程度", choices: [["P1", "P1 高风险"], ["P2", "P2 中风险"], ["P3", "P3 低风险"]] },
    { key: "q", label: "关键词" },
  ] as const;
  const scope = fields.flatMap(({ key, label, ...field }) => {
    const value = params[key];
    if (!value) return [];
    const name = "choices" in field ? field.choices.find(([id]) => value === id)?.[1] ?? value : value;
    return [`${label} ${escapeHtml(name)}`];
  }).join(" · ");
  const controls = fields.map(({ key, label, ...field }) => {
    const value = params[key];
    if ("choices" in field) {
      const choices: Array<readonly [string, string]> = [...field.choices];
      if (value && !choices.some(([id]) => id === value)) choices.push([value, value]);
      return `<label>${label}<select name="${key}"><option value="">全部</option>${options(choices, value)}</select></label>`;
    }
    return `<label>${label}<input name="${key}" value="${escapeHtml(value)}"${key === "q" ? ' type="search" maxlength="80"' : ""}></label>`;
  }).join("");
  return `<details class="panel incident-scope">
    <summary><span><strong>查询范围</strong><span class="incident-scope-current">${scope || "全部可见范围"}</span></span>
      <span class="incident-scope-edit">故障筛选</span></summary>
    <form class="incident-filters" data-incident-filters aria-label="故障筛选">
      ${controls}
      <div class="incident-filter-actions">
        <button class="button" type="submit">应用筛选</button>
        <a href="${target}">清除筛选</a>
      </div>
    </form>
  </details>`;
}

/** 只提交既有筛选字段，重新查询时从第一页开始。 */
export function bindIncidentFilters(root: HTMLElement, path: IncidentListPath): void {
  const filters = root.querySelector<HTMLFormElement>("[data-incident-filters]");
  filters?.addEventListener("submit", (event) => {
    event.preventDefault();
    const query = new URLSearchParams();
    for (const [name, value] of new FormData(filters)) query.set(name, String(value));
    const href = incidentFilterHref(path, incidentFilters(`#?${query}`));
    if (window.location.hash === href) {
      const details = filters.closest("details");
      if (details) {
        details.open = false;
        details.querySelector("summary")?.focus();
      }
      return;
    }
    filters.closest("main")?.focus({ preventScroll: true });
    window.location.hash = href;
  });
}
