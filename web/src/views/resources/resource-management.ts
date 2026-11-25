import { resolveResourceTab, type ResourceAccess } from "../../domain/resource-routes.ts";
import { renderOnboarding } from "../clusters/cluster-onboarding.ts";
import type { CatalogEntry } from "./catalog-model.ts";
import { loadCatalog, renderCatalog } from "./catalog-workspace.ts";

export async function renderResourceManagement(hash: string, access: ResourceAccess, editable = true) {
  const path = hash.split("?", 1)[0] ?? "";
  const selected = resolveResourceTab(path, access);
  if (!selected || selected === "datasources") return { html: '<p role="alert">无权访问此资源管理页面。</p>', ownsHeading: false };
  if (selected === "clusters") {
    const id = path.split("/")[3];
    const body = await renderOnboarding(id, false, editable);
    return { html: `<section class="resource-management${id ? " panel identity-panel" : ""}">${body}</section>`, ownsHeading: !id };
  }
  let items: CatalogEntry[] = [];
  let failed = false;
  try {
    items = await loadCatalog(selected);
  } catch (error) {
    if (error instanceof Response && [401, 403].includes(error.status)) throw error;
    failed = true;
  }
  const catalog = { kind: selected, items, failed, editable, access };
  return { catalog, html: renderCatalog(catalog), ownsHeading: true };
}
