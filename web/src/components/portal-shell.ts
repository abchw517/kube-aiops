interface PortalShellState {
  hash: string;
  demoMode: boolean;
  identityAdmin: boolean;
  canManageResources: boolean;
  mustChangePassword: boolean;
  canChangePassword: boolean;
  showLogout: boolean;
  signedOut: boolean;
}

/** 根据应用提供的路由和权限状态渲染控制台外框。 */
export function renderPortalShell(content: string, state: PortalShellState): string {
  const { hash, demoMode, identityAdmin, canManageResources, mustChangePassword, canChangePassword, showLogout, signedOut } = state;
  const adminPage = mustChangePassword || (identityAdmin && hash.startsWith("#/admin"));
  const resourcePage = !mustChangePassword && /^#\/resources(?:\/|$)/.test(hash);
  const wikiPage = /^#\/wiki(?:[/?]|$)/.test(hash);
  const detailPage = /^#\/findings\/[^?]+/.test(hash);
  const title = adminPage ? "系统管理" : resourcePage ? "资源管理" : wikiPage ? "运维知识库" : detailPage ? "诊断详情" : "诊断控制台";
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
        ${identityAdmin ? `<a href="#/admin/ldap"${adminPage ? currentPage : ""}>系统管理</a>` : ""}
      </nav>`}
      <main id="portal-content" class="page-shell" tabindex="-1">
        ${signedOut
          ? (demoMode ? '<p class="demo-notice">演示数据</p>' : "")
          : `<header class="page-intro"><h1>${title}</h1>${demoMode ? '<p class="demo-notice">演示数据</p>' : ""}</header>`}
        ${content}
      </main>
    </div>`;
}
