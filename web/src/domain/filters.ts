import type { Cluster, ListFindingsParams, Severity, SummarizeFindingsParams } from "../api/generated.ts";

export const allClusters = "*";
export const scopeDimensions = ["environment", "business", "group"] as const;
type ScopeDimension = (typeof scopeDimensions)[number];

export type FindingFilters = Pick<
  ListFindingsParams,
  "cluster" | "namespace" | "kind" | "severity"
> & Partial<Record<ScopeDimension, string>>;

const severityValues = new Set<Severity>(["critical", "warning", "info"]);

/** 解析列表筛选条件，未指定实例时由列表查询使用 ALL。 */
export function parseFindingFilters(hash: string): FindingFilters {
  const params = new URLSearchParams(hash.split("?")[1]);
  const severity = params.get("severity") as Severity | null;
  return {
    cluster: cleanValue(params.get("cluster")),
    environment: cleanValue(params.get("environment")),
    business: cleanValue(params.get("business")),
    group: cleanValue(params.get("group")),
    namespace: cleanValue(params.get("namespace")),
    kind: cleanValue(params.get("kind")),
    severity: severity && severityValues.has(severity) ? severity : undefined,
  };
}

/** 列表和详情分别使用聚合范围和既有 local 默认实例。 */
export function toDetailCluster(hash: string): string {
  return parseFindingFilters(hash).cluster ?? "local";
}

export function serializeFindingFilters(filters: FindingFilters): string {
  const params = new URLSearchParams();
  for (const key of ["cluster", ...scopeDimensions, "namespace", "kind", "severity"] as const) {
    const value = cleanValue(filters[key]);
    if (value) params.set(key, value);
  }
  return params.toString();
}

export function toListParams(filters: FindingFilters): ListFindingsParams {
  return { ...toSummaryParams(filters), limit: 50 };
}

export function toSummaryParams(filters: FindingFilters): SummarizeFindingsParams {
  return { ...filters, cluster: cleanValue(filters.cluster) ?? allClusters };
}

export function scopeValue(cluster: Cluster, key: ScopeDimension): string {
  return cluster[key]?.trim() || "未分组";
}

/** 旧实例链接仍能显示对应的上游筛选值。 */
export function displayFindingScope(clusters: Cluster[], filters: FindingFilters): FindingFilters {
  const selected = clusters.find((cluster) => cluster.id === filters.cluster);
  if (!selected) return filters;
  return {
    ...filters,
    environment: scopeValue(selected, "environment"),
    business: scopeValue(selected, "business"),
    group: scopeValue(selected, "group"),
  };
}

/** 使用当前生效的集群范围选择命名空间和实例候选。 */
export function matchingFindingClusters(clusters: Cluster[], filters: FindingFilters): Cluster[] {
  if (filters.cluster && filters.cluster !== allClusters) {
    return clusters.filter((cluster) => cluster.id === filters.cluster);
  }
  return clusters.filter((cluster) =>
    scopeDimensions.every((key) => !filters[key] || filters[key] === scopeValue(cluster, key)),
  );
}

/** 调整上游范围时仅保留仍匹配的下游选择，并重新选择结果子范围。 */
export function changeFindingScope(
  clusters: Cluster[],
  filters: FindingFilters,
  key: ScopeDimension | "cluster",
  value: string | undefined,
): FindingFilters {
  const current = displayFindingScope(clusters, filters);
  const next: FindingFilters = { ...filters, namespace: undefined, kind: undefined };
  if (key === "cluster") return { ...current, cluster: value, namespace: undefined, kind: undefined };
  const index = scopeDimensions.indexOf(key);
  next[key] = value;
  let candidates = clusters;
  for (const [position, dimension] of scopeDimensions.entries()) {
    if (position < index) next[dimension] = current[dimension];
    if (position > index && !value) next[dimension] = undefined;
    const choice = next[dimension];
    if (!choice) continue;
    const matching = candidates.filter((cluster) => scopeValue(cluster, dimension) === choice);
    if (position > index && !matching.length) next[dimension] = undefined;
    else candidates = matching;
  }
  if (!value || !candidates.some((cluster) => cluster.id === next.cluster)) next.cluster = undefined;
  return next;
}

function cleanValue(value: string | null | undefined): string | undefined {
  return value?.trim() || undefined;
}
