export const clusterManagementTitle = "Kubernetes 集群管理";

export const resourcePages = [
  ["clusters", clusterManagementTitle],
  ["environments", "环境管理"],
  ["business-lines", "业务线管理"],
] as const;

type ResourceTab = typeof resourcePages[number][0];
export type ResourceAccess = Record<ResourceTab, boolean>;

/** 按访问能力解析资源页，根路由进入首个可用栏目。 */
export function resolveResourceTab(hash: string, access: ResourceAccess): ResourceTab | undefined {
  const requested = hash.split("/")[2];
  if (requested) return resourcePages.find(([key]) => key === requested && access[key])?.[0];
  return resourcePages.find(([key]) => access[key])?.[0];
}
