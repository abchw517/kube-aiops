import { pageTrail, portalPages } from "../../domain/page-registry.ts";
import { resourcePageIds } from "../../domain/resource-routes.ts";

export type Catalog = "environments" | "business-lines";
export type CatalogEntry = { id: string; name: string };
export type CatalogFilters = { query: string; sort: "sequence" | "name" | "id" };
export const catalogKinds: Catalog[] = ["environments", "business-lines"];

export function catalogDefinition(kind: Catalog) {
  const pageId = resourcePageIds[kind];
  const page = portalPages[pageId];
  const title = page?.label ?? "";
  const route = page?.route ?? "";
  const group = pageTrail(pageId).at(-2)?.label ?? "";
  return kind === "environments"
    ? { title, route, group, noun: "环境", nameExample: "例如 灾备演练", idExample: "例如 disaster-recovery",
      purpose: "维护集群、数据源和授权范围使用的环境名称与标识。",
      usage: "在 Kubernetes 集群管理、数据源管理和权限管理中使用环境目录。" }
    : { title, route, group, noun: "业务线", nameExample: "例如 数据平台", idExample: "例如 data-platform",
      purpose: "维护集群与命名空间归属使用的业务线目录。",
      usage: "在 Kubernetes 集群管理、命名空间归属和权限管理中使用业务线目录。" };
}

const collator = new Intl.Collator("zh-CN", { numeric: true });

export function filterCatalog(entries: CatalogEntry[], { query, sort }: CatalogFilters): CatalogEntry[] {
  const needle = query.trim().toLowerCase();
  const visible = entries.filter(row => row.name.toLowerCase().includes(needle) || row.id.toLowerCase().includes(needle));
  if (sort !== "sequence") visible.sort((a, b) => collator.compare(a[sort], b[sort]) || collator.compare(a.id, b.id));
  return visible;
}

export function validateCatalog(entries: CatalogEntry[], next: CatalogEntry, original?: CatalogEntry) {
  if (!next.name) return { field: "name", message: "请填写名称。" } as const;
  if ([...next.name].length > 128 || /\p{Cc}/u.test(next.name)) {
    return { field: "name", message: "名称最多 128 个字符，且不能包含控制字符。" } as const;
  }
  if ((!original || next.id !== original.id) && (!/^[a-z][a-z0-9-]*$/.test(next.id) || next.id.length > 128)) {
    return { field: "id", message: "ID 以小写字母开头，仅支持小写字母、数字和连字符，最多 128 个字符。" } as const;
  }
  if (entries.some(entry => entry.id !== original?.id && entry.name === next.name)) {
    return { field: "name", message: "名称已被使用。" } as const;
  }
  if (entries.some(entry => entry.id !== original?.id && entry.id === next.id)) {
    return { field: "id", message: "ID 已被使用。" } as const;
  }
  return undefined;
}

export function referenceLabel(reference: string): string {
  if (reference.startsWith("角色 ")) return "角色范围";
  const identifier = "[a-z0-9][a-z0-9.-]*";
  if (new RegExp(`^${identifier}/${identifier}$`, "i").test(reference)) return "命名空间归属";
  if (new RegExp(`^${identifier}$`, "i").test(reference)) return "集群";
  return "关联引用";
}
