import { comparePageTrails, pageTrail, portalPages } from "./page-registry.ts";

export const resourcePageIds = {
  clusters: "resource.clusters",
  environments: "resource.environments",
  "business-lines": "resource.business",
  datasources: "resource.datasources",
} as const;

type ResourceTab = keyof typeof resourcePageIds;
export const clusterManagementTitle = portalPages[resourcePageIds.clusters]?.label ?? "";
export const datasourcesPath = portalPages[resourcePageIds.datasources]?.route ?? "";
export const legacyDatasourcesPath = "#/diagnostics/datasources";

const resourceTabs = (Object.keys(resourcePageIds) as ResourceTab[]).sort((a, b) =>
  comparePageTrails(pageTrail(resourcePageIds[a]), pageTrail(resourcePageIds[b])));
export type ResourceAccess = Record<Exclude<ResourceTab, "datasources">, boolean> & { datasources?: boolean };

/** 按访问能力解析资源页，根路由进入首个可用栏目。 */
export function resolveResourceTab(hash: string, access: ResourceAccess): ResourceTab | undefined {
  const requested = hash.split("?", 1)[0]?.split("/")[2];
  if (requested) return resourceTabs.find((key) => key === requested && access[key]);
  return resourceTabs.find((key) => access[key]);
}
