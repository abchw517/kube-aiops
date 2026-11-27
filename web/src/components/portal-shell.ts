import { diagnosticListPaths } from "../domain/diagnostic-routes.ts";
import { resolvePortalNavigation, type PortalNavigationState } from "../domain/portal-navigation.ts";

interface PortalShellState extends PortalNavigationState {
  showLogout: boolean;
  contentOwnsHeading?: boolean;
}

/** 在共享外框中渲染可访问的模块、当前栏目与正文。 */
export function renderPortalShell(content: string, state: PortalShellState): string {
  const { module, title, moduleLabel, groups, modules } = resolvePortalNavigation(state);
  const isRestricted = state.signedOut || state.mustChangePassword;
  const showSidebar = groups.length > 0;
  return `<div class="portal-console">
    <a class="skip-link" href="#portal-content" data-action="skip-content">跳到主要内容</a>
    <header class="topbar">
      <div class="topbar-inner">
      <a class="brand" href="${diagnosticListPaths.overview}" aria-label="kube-aiops 诊断首页">
        <span class="brand-mark" aria-hidden="true">K</span><span class="brand-name" translate="no">kube-aiops</span>
      </a>
      ${isRestricted ? "" : `<nav class="workspace-nav" aria-label="模块导航">${modules.map(({ href, label, active }) =>
        `<a href="${href}"${active ? ' aria-current="location"' : ""}>${label}</a>`).join("")}</nav>`}
      <div class="portal-actions">
        ${state.canChangePassword && !state.identityAdmin && !isRestricted && module !== "admin" ?
          '<a class="button secondary" href="#/admin/password">修改密码</a>' : ""}
        ${state.showLogout && !state.signedOut ? '<button class="button secondary" type="button" data-action="logout">退出登录</button>' : ""}
      </div>
      ${showSidebar ? `<button class="portal-menu-toggle" type="button" data-action="toggle-portal-menu"
        aria-expanded="false" aria-controls="portal-sidebar">栏目</button>` : ""}
      </div>
    </header>
    <div class="portal-layout${showSidebar ? "" : " portal-layout-single"}${state.signedOut ? " portal-signed-out" : ""}">
      ${showSidebar ? `<nav class="portal-sidebar" id="portal-sidebar" aria-label="模块栏目">
        <h2>${moduleLabel}</h2>${groups.map(({ label, links }) => `<p>${label}</p>${links.map((link) =>
          `<a href="${link.href}"${link.active ? ' aria-current="page"' : ""}>${link.label}</a>`).join("")}`).join("")}
      </nav>` : ""}
      <main class="page-shell" id="portal-content" tabindex="-1">
        ${state.signedOut ? "" : `<header class="page-intro">
          ${!state.mustChangePassword ? `<nav aria-label="面包屑">${moduleLabel} <span aria-hidden="true">›</span> <span aria-current="page">${title}</span></nav>` : ""}
          ${state.contentOwnsHeading ? (state.demoMode ? '<p class="demo-notice">演示数据</p>' : "") : `<div class="page-title"><h1>${title}</h1>${state.demoMode ? '<p class="demo-notice">演示数据</p>' : ""}</div>`}
        </header>`}
        ${content}
      </main>
    </div>
  </div>`;
}
