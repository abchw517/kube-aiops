import { escapeHtml } from "../../components/html.ts";

export const clusterOnboardingEndpoint = "/api/v1/admin/cluster-onboarding";
const nodeValidationFailed = "已接入 · 节点校验失败";

export type ClusterEntry = {
  id: string;
  name: string;
  group?: string;
  environment?: string;
  business?: string;
  clusterType?: string;
  apiURL?: string;
  engineNamespace?: string;
  engineName?: string;
  prometheusURL?: string;
  victoriaLogsURL?: string;
  alertmanagerURL?: string;
  aiEnabled?: boolean;
  mode: string;
  enabled: boolean;
  deleted?: boolean;
  credentialKind?: string;
  reportedAt: number;
  firstCollectedAt: number;
  validatedAt: number;
  checkedAt: number;
  collection?: {
    nodes?: { name: string; ready: string }[];
    namespaces?: { name: string }[];
    nodeSampleAt?: number;
    nodeFailures?: number;
    nodeError?: string;
    namespaceError?: boolean;
    findingError?: boolean;
    findingCount?: number;
    workloadFinding?: boolean;
  };
};

export function collectionStatus(item: ClusterEntry): string {
  if (item.deleted) return "已删除";
  if (!item.enabled) return "已停用";
  if (item.mode === "pull" && !item.credentialKind) return "待补录凭据";
  if (item.mode === "push") {
    if (!item.reportedAt) return "等待首次上报";
    if (Date.now() / 1000 - item.reportedAt > 180) return "采集离线";
    return item.firstCollectedAt ? "已接入" : "上报在线，等待首次诊断";
  }
  const timedOut = item.validatedAt > 0 && Date.now() / 1000 - item.validatedAt >= 300;
  if (!item.firstCollectedAt) {
    if (!item.validatedAt && item.checkedAt) return "待接入";
    return timedOut ? "首次采集超时" : "等待首次采集";
  }
  if ((item.collection?.nodeFailures ?? 0) >= 2 && item.collection?.nodeSampleAt) return "已接入 · 节点数据已过期";
  // 历史节点快照仍可查阅，但最新校验失败时不能用它判断当前健康。
  if (item.collection?.nodeError) return nodeValidationFailed;
  if (item.collection?.nodes?.some((node) => node.ready === "False")) return "已接入 · 集群异常";
  if (item.collection?.nodes?.some((node) => node.ready === "Unknown")) return "已接入 · 节点状态未知";
  if (item.collection?.nodes?.length) return "已接入 · 当前集群正常";
  return timedOut ? "已接入 · 集群状态信息采集异常" : "已接入 · 等待节点数据";
}

export function displayTime(seconds: number): string {
  return escapeHtml(new Date(seconds * 1000).toLocaleString("zh-CN", { timeZone: "Asia/Shanghai" }));
}

/** 本地及已保存凭据的外部直连实例支持重新校验。 */
export function canRecheckCollection(item: ClusterEntry): boolean {
  return !item.deleted && item.enabled && (item.mode === "local" || (item.mode === "pull" && Boolean(item.credentialKind)));
}

/** 在采集详情和独立编辑页复用重新校验表单。 */
export function renderCollectionRecheck(item: ClusterEntry): string {
  return canRecheckCollection(item) ? `<form data-collection-check="${escapeHtml(item.id)}">
    <button type="submit" class="button secondary">重新校验</button><p role="status"></p></form>` : "";
}

export function renderCollection(item: ClusterEntry): string {
  const data = item.collection;
  const isHistorical = Boolean(data?.nodes?.length && (data.nodeError || (data.nodeFailures ?? 0) >= 2));
  const nodeHeading = isHistorical ? "历史节点快照" : "节点状态";
  return `<div class="collection-details">
    <section class="resource-detail-card"><h3>最近活动</h3>
    <p>${item.firstCollectedAt ? `首次采集成功 ${displayTime(item.firstCollectedAt)}` : "尚未收到有效采集数据"}</p>
    ${item.reportedAt ? `<p>最近上报 ${displayTime(item.reportedAt)}</p>` : ""}
    ${item.checkedAt ? `<p>最近检查 ${displayTime(item.checkedAt)}</p>` : ""}
    ${data?.nodeSampleAt ? `<p>${isHistorical ? "上次成功采样时间" : "节点更新时间"} ${displayTime(data.nodeSampleAt)}</p>` : ""}
    </section>
    ${data?.nodeError ? `<p class="resource-notice" role="status">${escapeHtml(data.nodeError)}</p>` : ""}
    <section class="resource-detail-card"><h3>${nodeHeading}</h3>
    ${(data?.nodeFailures ?? 0) >= 2 && data?.nodeSampleAt ? '<p role="status">节点数据已过期</p>' : ""}
    ${data?.nodes?.length ? `<ul aria-label="${nodeHeading}">${data.nodes.map((node) =>
      `<li>${escapeHtml(node.name)} · <strong>${node.ready === "True" ? "Ready" : node.ready === "False" ? "NotReady" : "Unknown"}</strong></li>`).join("")}</ul>` : "<p>尚未收到节点数据。</p>"}
    </section><section class="resource-detail-card"><h3>命名空间</h3>
    ${data?.namespaceError ? "<p>命名空间采集异常</p>" : data?.namespaces?.length ?
      `<p>${data.namespaces.map(({ name }) => escapeHtml(name)).join("、")}</p>` : "<p>暂无数据。</p>"}
    </section><section class="resource-detail-card"><h3>K8sGPT 诊断</h3>
    ${data?.findingError ? "<p>诊断采集异常，请检查 K8sGPT 安装和 Result 读取权限</p>" :
      data?.findingCount ? "<p>已收到 K8sGPT 诊断</p>" : "<p>尚未收到诊断。</p>"}
    ${!data?.findingError && data?.workloadFinding ? "<p>发现工作负载故障</p>" : ""}
    </section>
  </div>`;
}

export function clusterCategory(item: ClusterEntry): string {
  const label = collectionStatus(item);
  if (item.deleted) return "removed";
  if (!item.enabled) return "paused";
  if (["等待首次上报", "上报在线，等待首次诊断", "等待首次采集", "已接入 · 等待节点数据"].includes(label)) return "waiting";
  if (!["已接入", "已接入 · 当前集群正常"].includes(label) || item.collection?.namespaceError || item.collection?.findingError) return "issue";
  return "normal";
}

export function collectionCaption(item: ClusterEntry): string {
  const label = collectionStatus(item);
  if (["已接入 · 集群异常", "已接入 · 节点状态未知", "已接入 · 当前集群正常"].includes(label)) {
    return item.collection?.namespaceError || item.collection?.findingError ? "部分采集异常" : "已采集";
  }
  if (label === "已接入" && item.mode === "push") return "上报在线";
  if (item.deleted) return "已解除连接";
  return label.replace("已接入 · ", "");
}
