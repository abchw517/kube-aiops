import { diagnosticListPaths, diagnosticListView } from "../domain/diagnostic-routes.ts";

interface PortalShellState {
  hash: string;
  demoMode: boolean;
  identityAdmin: boolean;
  permissionCatalog?: boolean;
  auditRead?: boolean;
  canManageResources: boolean;
  mustChangePassword: boolean;
  canChangePassword: boolean;
  showLogout: boolean;
  signedOut: boolean;
  diagnosticDatasources?: boolean;
  diagnosticPolicies?: boolean;
}

/** 根据应用提供的路由和权限状态渲染控制台外框。 */
export function renderPortalShell(content: string, state: PortalShellState): string {
  const { hash, demoMode, identityAdmin, permissionCatalog, auditRead, canManageResources, mustChangePassword, canChangePassword, showLogout, signedOut } = state;
  const adminPage = mustChangePassword || ((identityAdmin || permissionCatalog || auditRead) && hash.startsWith("#/admin"));
  const resourcePage = !mustChangePassword && /^#\/resources(?:\/|$)/.test(hash);
  const wikiPage = /^#\/wiki(?:[/?]|$)/.test(hash);
  const detailPage = /^#\/findings\/[^?]+/.test(hash);
  const diagnosticView = diagnosticListView(hash);
  const eventDetail = /^#\/diagnostics\/(?:events|incidents)\/[^?]+/.test(hash);
  const section = hash.split("?", 1)[0];
  const currentDatasource = section === "#/diagnostics/datasources";
  const currentPolicy = section === "#/diagnostics/policies";
  const subpageTitle = currentDatasource ? "数据源管理" : currentPolicy ? "诊断策略" :
    section?.endsWith("/topology") && eventDetail ? "关联拓扑" : section?.endsWith("/rca") && eventDetail ? "AI 根因分析" : "";
  const diagnosticTitle = subpageTitle || (diagnosticView === "overview" ? "诊断总览" :
    diagnosticView === "events" ? (eventDetail ? "故障详情" : "故障事件") : detailPage ? "诊断详情" : "诊断控制台");
  const diagnosticPage = !adminPage && !resourcePage && !wikiPage;
  const title = diagnosticPage ? diagnosticTitle : adminPage ? "系统管理" : resourcePage ? "资源管理" : "运维知识库";
  const currentPage = ' aria-current="page"';
  return `
    <a class="skip-link" href="#portal-content" data-action="skip-content">跳到主要内容</a>
    <header class="topbar">
      <a class="brand" href="#/findings" aria-label="kube-aiops 诊断首页">
        <span class="brand-mark" aria-hidden="true">K</span>
        <span class="brand-name" translate="no">kube-aiops</span>
      </a>
      <div class="portal-actions">
        ${canChangePassword && !identityAdmin && !mustChangePassword && !signedOut ? '<a class="button secondary" href="#/admin/password">修改密码</a>' : ""}
        ${showLogout && !signedOut ? '<button class="button secondary" type="button" data-action="logout">退出登录</button>' : ""}
      </div>
    </header>
    <div class="portal-layout${signedOut ? " portal-signed-out" : ""}">
      ${signedOut || mustChangePassword ? "" : `<nav class="workspace-nav" aria-label="工作台导航">
        <a href="#/findings"${adminPage || wikiPage || resourcePage ? "" : currentPage}>诊断控制台</a>
        ${demoMode ? "" : `<a href="#/wiki"${wikiPage ? currentPage : ""}>运维知识库</a>`}
        ${canManageResources ? `<a href="#/resources"${resourcePage ? currentPage : ""}>资源管理</a>` : ""}
        ${identityAdmin || permissionCatalog || auditRead ? `<a href="${identityAdmin ? "#/admin/ldap" : permissionCatalog ? "#/admin/access/catalog" : "#/admin/operations"}"${adminPage ? currentPage : ""}>系统管理</a>` : ""}
      </nav>`}
      <main id="portal-content" class="page-shell" tabindex="-1">
        ${signedOut
          ? (demoMode ? '<p class="demo-notice">演示数据</p>' : "")
          : `<header class="page-intro"><h1>${title}</h1>${demoMode ? '<p class="demo-notice">演示数据</p>' : ""}</header>`}
        ${!signedOut && diagnosticPage ? `<nav class="diagnostic-nav" aria-label="诊断中心">
          <a href="${diagnosticListPaths.overview}"${diagnosticView === "overview" ? currentPage : ""}>诊断总览</a>
          <a href="${diagnosticListPaths.events}"${diagnosticView === "events" && !eventDetail ? currentPage : ""}>故障事件</a>
          ${state.diagnosticDatasources ? `<a href="#/diagnostics/datasources"${currentDatasource ? currentPage : ""}>数据源管理</a>` : ""}
          ${state.diagnosticPolicies ? `<a href="#/diagnostics/policies"${currentPolicy ? currentPage : ""}>诊断策略</a>` : ""}
          <a href="${diagnosticListPaths.findings}"${diagnosticView === "findings" && !detailPage && !subpageTitle ? currentPage : ""}>原有诊断结果</a>
        </nav>` : ""}
        ${content}
      </main>
    </div>`;
}
