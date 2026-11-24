import { diagnosticListPaths, diagnosticListView } from "./diagnostic-routes.ts";
import { datasourcesPath, resourcePages, resolveResourceTab, type ResourceAccess } from "./resource-routes.ts";

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

type PortalModule = "diagnostics" | "resources" | "wiki" | "admin";
type PageLink = { href: string; label: string; active: boolean };
type PageGroup = { label: string; links: PageLink[] };
type Workspace = { module: PortalModule; title: string; groups: PageGroup[] };

const moduleLabels: Record<PortalModule, string> = {
  diagnostics: "诊断控制台",
  resources: "资源管理",
  wiki: "运维知识库",
  admin: "系统管理",
};

/** 根据现有权限和路由计算诊断栏目及详情标题。 */
function diagnosticWorkspace(path: string, state: PortalNavigationState): Workspace {
  const view = diagnosticListView(path);
  const eventDetail = /^#\/diagnostics\/(?:events|incidents)\/[^/]+/.test(path);
  const findingDetail = path.startsWith("#/findings/");
  const policy = path === "#/diagnostics/policies";
  const title = policy ? "诊断策略" :
    eventDetail ? path.endsWith("/topology") ? "关联拓扑" : path.endsWith("/rca") ? "AI 根因分析" : "故障详情" :
    findingDetail ? "诊断详情" : view === "overview" ? "诊断总览" : view === "events" ? "故障事件" : "原有诊断结果";
  return { module: "diagnostics", title, groups: [
    { label: "诊断工作区", links: [
      { href: diagnosticListPaths.overview, label: "诊断总览", active: view === "overview" },
      { href: diagnosticListPaths.events, label: "故障事件", active: view === "events" },
      { href: diagnosticListPaths.findings, label: "原有诊断结果", active: view === "findings" && !policy },
    ] },
    { label: "诊断配置", links: [
      ...(state.diagnosticPolicies ? [{ href: "#/diagnostics/policies", label: "诊断策略", active: policy }] : []),
    ] },
  ] };
}

/** 使用资源页相同的权限顺序和默认路由。 */
function resourceWorkspace(path: string, access: ResourceAccess): Workspace {
  const selected = resolveResourceTab(path, access);
  const links = resourcePages.filter(([key]) => access[key]).map(([key, label]) => ({
    href: `#/resources/${key}`, label, active: key === selected,
  }));
  return { module: "resources", title: path.startsWith("#/resources/clusters/") ? "命名空间归属" :
    links.find((link) => link.active)?.label ?? (path === datasourcesPath ? "数据源管理" : "资源管理"), groups: [
    { label: "集群资源", links: links.filter((link) => link.href === "#/resources/clusters") },
    { label: "平台目录", links: links.filter((link) => link.href !== "#/resources/clusters") },
  ] };
}

/** 为知识详情和治理操作保留所属栏目。 */
function wikiWorkspace(path: string, governanceAccess: boolean): Workspace {
  const governance = /^#\/wiki\/(?:governance|draft|manage)(?:\/|$)/.test(path);
  return { module: "wiki", title: governance ? "知识治理" : path === "#/wiki" ? "知识检索" : "知识详情", groups: [
    { label: "知识工作区", links: [
      { href: "#/wiki", label: "知识检索", active: !governance },
      ...(governanceAccess ? [{ href: "#/wiki/governance", label: "知识治理", active: governance }] : []),
    ] },
  ] };
}

/** 目录和审计独立授权，首次改密前隐藏其他栏目。 */
function adminWorkspace(path: string, state: PortalNavigationState): Workspace {
  if (state.mustChangePassword) return { module: "admin", title: "修改密码", groups: [] };
  const catalog = path === "#/admin/access/catalog";
  const access = !catalog && /^#\/admin\/(?:access|policy)(?:\/|$)/.test(path);
  const audit = path.startsWith("#/admin/operations");
  const password = path === "#/admin/password";
  const accounts = path.startsWith("#/admin/users");
  const auth = path === "#/admin/ldap" || path === "#/admin";
  const title = catalog ? "权限目录" : access ? "权限配置" : audit ? "操作审计" : password ? "修改密码" :
    accounts ? "用户管理" : auth ? "认证配置" : "系统管理";
  return { module: "admin", title, groups: [
    { label: "身份与访问", links: [
      ...(state.identityAdmin ? [
        { href: "#/admin/ldap", label: "认证配置", active: auth },
        { href: "#/admin/users", label: "用户管理", active: accounts },
        { href: "#/admin/access", label: "权限管理", active: access || catalog },
      ] : state.permissionCatalog ? [{ href: "#/admin/access/catalog", label: "权限目录", active: catalog }] : []),
      ...(state.canChangePassword ? [{ href: "#/admin/password", label: "修改密码", active: password }] : []),
    ] },
    { label: "审计", links: state.auditRead ? [{ href: "#/admin/operations", label: "操作审计", active: audit }] : [] },
  ] };
}

type ModuleLink = { href: string; label: string; active: boolean };

/** 派生可用模块和当前栏目，供外框渲染使用。 */
export function resolvePortalNavigation(state: PortalNavigationState): {
  title: string;
  moduleLabel: string;
  module: PortalModule;
  groups: PageGroup[];
  modules: ModuleLink[];
} {
  const path = state.hash.split("?", 1)[0] ?? "";
  const workspace = state.mustChangePassword || /^#\/admin(?:\/|$)/.test(path) ? adminWorkspace(path, state) :
    /^#\/resources(?:\/|$)/.test(path) ? resourceWorkspace(path, state.resourceAccess) :
    /^#\/wiki(?:\/|$)/.test(path) ? wikiWorkspace(path, state.wikiGovernance) :
    diagnosticWorkspace(path, state);
  const { module, title } = workspace;
  const moduleLabel = moduleLabels[module];
  if (state.signedOut || state.mustChangePassword) {
    return { title, moduleLabel, module, groups: [], modules: [] };
  }
  const adminHref = state.identityAdmin ? "#/admin/ldap" : state.permissionCatalog ? "#/admin/access/catalog" :
    state.auditRead ? "#/admin/operations" : "";
  const entries: Array<[PortalModule, string]> = [
    ["diagnostics", diagnosticListPaths.overview],
    ["resources", Object.values(state.resourceAccess).some(Boolean) ? "#/resources" : ""],
    ["wiki", state.demoMode ? "" : "#/wiki"],
    ["admin", adminHref],
  ];
  const modules = entries.filter(([, href]) => href).map(([key, href]) =>
    ({ href, label: moduleLabels[key], active: key === module }));
  return { title, moduleLabel, module, groups: workspace.groups.filter(({ links }) => links.length), modules };
}
