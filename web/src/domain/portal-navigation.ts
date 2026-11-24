import { diagnosticListView } from "./diagnostic-routes.ts";
import { comparePageTrails, pageTrail, portalPages, type PageRegistry } from "./page-registry.ts";
import { resourcePageIds, resolveResourceTab, type ResourceAccess } from "./resource-routes.ts";

export interface PortalNavigationState {
  hash: string;
  demoMode: boolean;
  identityAdmin: boolean;
  permissionCatalog: boolean;
  auditRead: boolean;
  resourceAccess: ResourceAccess;
  wikiGovernance: boolean;
  mustChangePassword: boolean;
  canChangePassword: boolean;
  signedOut: boolean;
  diagnosticPolicies?: boolean;
}

const modules = { diagnosis: "diagnostics", resources: "resources", knowledge: "wiki", system: "admin" } as const;
type PortalModule = typeof modules[keyof typeof modules];
type PageLink = { href: string; label: string; active: boolean };
type PageGroup = { label: string; links: PageLink[] };

function currentPage(path: string, state: PortalNavigationState): [string, string] {
  if (state.mustChangePassword) return ["system.password", "system.password"];
  if (/^#\/admin(?:\/|$)/.test(path)) {
    if (path === "#/admin/access/catalog") return ["system.catalog", state.identityAdmin ? "system.access" : "system.catalog"];
    if (/^#\/admin\/(?:access|policy)(?:\/|$)/.test(path)) return ["system.access", "system.access"];
    const page = path.startsWith("#/admin/operations") ? "system.audit" : path === "#/admin/password" ? "system.password" :
      path.startsWith("#/admin/users") ? "system.users" : path === "#/admin/ldap" || path === "#/admin" ? "system.ldap" : "system";
    return [page, page];
  }
  if (/^#\/resources(?:\/|$)/.test(path)) {
    const selected = resolveResourceTab(path, state.resourceAccess);
    if (path.startsWith("#/resources/clusters/")) return ["resource.ownership", selected ? resourcePageIds[selected] : ""];
    if (selected) return [resourcePageIds[selected], resourcePageIds[selected]];
    return [path === portalPages["resource.datasources"]?.route ? "resource.datasources" : "resources", ""];
  }
  if (/^#\/wiki(?:\/|$)/.test(path)) {
    if (/^#\/wiki\/(?:governance|draft|manage)(?:\/|$)/.test(path)) return ["knowledge.manage", "knowledge.manage"];
    return [path === "#/wiki" ? "knowledge.search" : "knowledge.detail", "knowledge.search"];
  }
  if (path === portalPages["diagnosis.policy"]?.route) return ["diagnosis.policy", "diagnosis.policy"];
  if (/^#\/diagnostics\/(?:events|incidents)\/[^/]+/.test(path)) {
    const page = path.endsWith("/topology") ? "diagnosis.incidents.topology" : path.endsWith("/rca") ?
      "diagnosis.incidents.rca" : "diagnosis.incident-detail";
    return [page, "diagnosis.incidents"];
  }
  if (path.startsWith("#/findings/")) return ["diagnosis.finding-detail", "diagnosis.findings"];
  const view = diagnosticListView(path);
  const page = view === "overview" ? "diagnosis.overview" : view === "events" ? "diagnosis.incidents" : "diagnosis.findings";
  return [page, page];
}

export function resolvePortalNavigation(state: PortalNavigationState, registry: PageRegistry = portalPages) {
  const path = state.hash.split("?", 1)[0] ?? "";
  const [pageId, activePage] = currentPage(path, state);
  const moduleId = pageTrail(pageId, registry)[0]?.id ?? "diagnosis";
  const module: PortalModule = modules[moduleId as keyof typeof modules] ?? "diagnostics";
  const moduleLabel = registry[moduleId]?.label ?? "";
  const title = registry[pageId]?.label ?? moduleLabel;
  if (state.signedOut || state.mustChangePassword) return { title, moduleLabel, module, groups: [], modules: [] };
  const allowed = [
    "diagnosis.overview", "diagnosis.incidents", "diagnosis.findings",
    ...(state.diagnosticPolicies ? ["diagnosis.policy"] : []),
    ...Object.entries(resourcePageIds).filter(([key]) => state.resourceAccess[key as keyof ResourceAccess]).map(([, id]) => id),
    ...(!state.demoMode ? ["knowledge.search"] : []),
    ...(state.wikiGovernance ? ["knowledge.manage"] : []),
    ...(state.identityAdmin ? ["system.ldap", "system.users", "system.access"] : state.permissionCatalog ? ["system.catalog"] : []),
    ...(state.canChangePassword ? ["system.password"] : []),
    ...(state.auditRead ? ["system.audit"] : []),
  ].map((id) => pageTrail(id, registry)).filter((trail) => trail.length > 0).sort(comparePageTrails);
  const groups = new Map<string, PageGroup>();
  for (const trail of allowed) {
    const owner = trail[0];
    const section = trail[1];
    const item = trail.at(-1);
    if (owner?.id !== moduleId || !section || !item?.route) continue;
    const group = groups.get(section.id) ?? { label: section.label, links: [] };
    group.links.push({ href: item.route, label: item.label, active: item.id === activePage });
    groups.set(section.id, group);
  }
  const nav = Object.keys(modules).sort((a, b) => comparePageTrails(pageTrail(a, registry), pageTrail(b, registry))).map((id) => {
    // Current module landing URLs are aliases. Moved pages can supply the first remaining link.
    const pending = allowed.filter((trail) => trail[0]?.id === id);
    if (!pending.length) return undefined;
    const landing = id === "resources" && pending.some((trail) => trail.at(-1)?.route?.startsWith("#/resources/")) ? "#/resources" :
      pending.find((trail) => trail.at(-1)?.id !== "system.password")?.at(-1)?.route;
    return landing ? { href: landing, label: registry[id]?.label ?? "", active: id === moduleId } : undefined;
  }).filter((item) => item !== undefined);
  return { title, moduleLabel, module, groups: [...groups.values()], modules: nav };
}
