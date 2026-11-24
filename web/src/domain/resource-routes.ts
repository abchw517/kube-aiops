export const clusterManagementTitle = "Kubernetes 集群管理";

export const datasourcesPath = "#/resources/datasources";
export const legacyDatasourcesPath = "#/diagnostics/datasources";

export const resourcePages = [
  ["clusters", clusterManagementTitle],
  ["environments", "环境管理"],
  ["business-lines", "业务线管理"],
  ["datasources", "数据源管理"],
] as const;

type ResourceTab = typeof resourcePages[number][0];
export type ResourceAccess = Record<Exclude<ResourceTab, "datasources">, boolean> & { datasources?: boolean };

/** 按访问能力解析资源页，根路由进入首个可用栏目。 */
export function resolveResourceTab(hash: string, access: ResourceAccess): ResourceTab | undefined {
  const requested = hash.split("?", 1)[0]?.split("/")[2];
  if (requested) return resourcePages.find(([key]) => key === requested && access[key])?.[0];
  return resourcePages.find(([key]) => access[key])?.[0];
}
