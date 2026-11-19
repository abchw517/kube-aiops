import { bindPortalForms } from "./portal-forms.ts";
import { filterAccounts, handlePlatformClick, updateScopeOptions } from "./views/permissions/platform-admin.ts";
import { handleResourceCatalogClick, renderResourceManagement, type ResourceAccess } from "./views/resources/resource-management.ts";
import { bindWiki, renderWiki } from "./views/wiki/wiki.ts";
import { filterClusterRows, handleClusterMaintenanceClick, updateClusterCredential } from "./views/clusters/cluster-onboarding.ts";
import { changeNamespaceOwnership } from "./views/clusters/namespace-ownership.ts";
import {
  ApiError,
  type Cluster,
  type Namespace,
} from "./api/generated.ts";
import { invalidateLDAPDraft, renderIdentityAdmin } from "./views/permissions/identity-admin.ts";
import { parseAuditRoute, type AuditFilters } from "./views/permissions/audit-data.ts";
import { bindAudit, loadAudit } from "./views/permissions/audit.ts";
import { apiClient } from "./api/client.ts";
import { renderPortalShell } from "./components/portal-shell.ts";
import {
  authenticationRequiredState,
  authorizationDeniedState,
  errorState,
  loadingState,
} from "./components/states.ts";
import {
  allClusters,
  changeFindingScope,
  matchingFindingClusters,
  parseFindingFilters,
  scopeDimensions,
  serializeFindingFilters,
  toDetailCluster,
  toListParams,
  toSummaryParams,
  type FindingFilters,
} from "./domain/filters.ts";
import {
  renderCorrelationError,
  renderCorrelationEvidence,
  renderCorrelationLoading,
  renderCorrelationSection,
} from "./views/diagnosis/correlation.ts";
import { bindFindingReview } from "./views/diagnosis/finding-review.ts";
import { renderFindingDetail } from "./views/diagnosis/finding-detail.ts";
import { renderFindingWorkspace } from "./views/diagnosis/finding-list.ts";
import { bindSeverityHelp, dismissSeverityHelp } from "./views/diagnosis/severity-help.ts";

const demoMode = document.querySelector('meta[name="kube-aiops-demo"][content="true"]') !== null;

export class PortalApp {
  private readonly root: HTMLElement;
  private clusters: Cluster[] | undefined;
  private namespaces = new Map<string, Namespace[]>();
  private knownKinds = new Set<string>();
  private knownKindsScope = "";
  private renderGeneration = 0;
  private identityAdmin = false;
  private resourceAccess: ResourceAccess = { clusters: false, environments: false, "business-lines": false };
  private findingAI = false;
  private mustChangePassword = false;
  private canChangePassword = false;
  private superAdministrator = false;
  private showLogout = !demoMode;

  constructor(root: HTMLElement) {
    this.root = root;
  }

  start(): void {
    window.addEventListener("hashchange", () => void this.route());
    this.root.addEventListener("click", (event) => {
      handleResourceCatalogClick(event);
      void handleClusterMaintenanceClick(event);
      handlePlatformClick(event);
      this.handleClick(event);
    });
    bindPortalForms(this.root, {
      refresh: () => void this.route(),
      completePasswordChange: () => {
        this.identityAdmin = false;
        this.mustChangePassword = false;
        window.location.hash = "#/findings";
        void this.route();
      },
      login: (event) => void this.login(event),
    });
    const updateLDAP = (event: Event) => {
      const target = event.target;
      if (!(target instanceof HTMLElement)) return;
      const form = target.closest<HTMLFormElement>('form[data-identity-resource="ldap"]');
      if (form) invalidateLDAPDraft(form, target);
    };
    this.root.addEventListener("input", filterClusterRows);
    this.root.addEventListener("change", filterClusterRows);
    this.root.addEventListener("change", updateClusterCredential);
    this.root.addEventListener("change", changeNamespaceOwnership);
    this.root.addEventListener("input", updateLDAP);
    this.root.addEventListener("input", filterAccounts);
    this.root.addEventListener("change", filterAccounts);
    this.root.addEventListener("change", (event) => void updateScopeOptions(event));
    this.root.addEventListener("change", updateLDAP);
    this.root.addEventListener("change", (event) => this.handleFilterChange(event));
    window.addEventListener("keydown", (event) => dismissSeverityHelp(event, this.root));
    if (!window.location.hash) window.location.hash = "#/findings";
    void this.route();
  }

  private async route(): Promise<void> {
    const generation = ++this.renderGeneration;
    this.findingAI = false;
    if (!demoMode) {
      try {
        const response = await fetch("/api/v1/admin/status");
        const status = response.ok ? await response.json() : {};
        if (generation !== this.renderGeneration) return;
        this.identityAdmin = status.admin === true;
        this.resourceAccess = {
          clusters: this.identityAdmin,
          environments: status.manageEnvironments === true,
          "business-lines": status.manageBusinessLines === true,
        };
        this.findingAI = this.identityAdmin && status.findingAI === true;
        this.mustChangePassword = status.mustChangePassword === true;
        this.canChangePassword = status.canChangePassword === true;
        this.superAdministrator = status.superAdministrator === true;
      } catch {
        if (generation !== this.renderGeneration) return;
      }
      const hash = window.location.hash;
      if (!this.mustChangePassword && !this.canChangePassword && /^#\/admin\/password(?:[/?]|$)/.test(hash)) {
        window.location.replace(this.identityAdmin ? "#/admin/users" : "#/findings");
        return;
      }
      if (!this.mustChangePassword && this.identityAdmin && /^#\/admin\/clusters(?:\/|$)/.test(hash)) {
        window.location.replace(hash.replace("#/admin/clusters", "#/resources/clusters"));
        return;
      }
      const audit = parseAuditRoute(hash);
      if (audit && !this.mustChangePassword) {
        await this.renderOperationAudit(audit, generation);
        return;
      }
      if (!this.mustChangePassword && /^#\/resources(?:\/|$)/.test(hash)) {
        this.renderShell(loadingState("正在加载资源管理。"));
        try {
          const body = await renderResourceManagement(hash, this.resourceAccess);
          if (generation === this.renderGeneration) this.renderShell(body);
        } catch {
          if (generation === this.renderGeneration) this.renderShell(errorState("无法加载资源管理。"));
        }
        return;
      }
      const adminRoute = hash.startsWith("#/admin");
      if (this.mustChangePassword || (this.identityAdmin && adminRoute) || (this.canChangePassword && hash === "#/admin/password")) {
        const [, , resource = "ldap", roleID] = window.location.hash.split("/");
        const body = await renderIdentityAdmin(this.mustChangePassword ? "password" : resource, {
          initial: this.mustChangePassword,
          roleID,
          administrator: this.identityAdmin,
          superAdmin: this.superAdministrator,
          canChangePassword: this.canChangePassword,
        });
        if (generation === this.renderGeneration) this.renderShell(body);
        return;
      }
    }
    if (!demoMode && /^#\/wiki(?:[/?]|$)/.test(window.location.hash)) {
      this.renderShell(loadingState("正在加载知识库。"));
      try {
        const body = await renderWiki(window.location.hash, this.identityAdmin);
        if (generation === this.renderGeneration) {
          if (body === null) {
            this.renderShell(authenticationRequiredState(), true);
            return;
          }
          this.renderShell(body);
          bindWiki(this.root, () => void this.route());
        }
      } catch {
        if (generation === this.renderGeneration) this.renderShell(errorState("无法加载知识库。"));
      }
      return;
    }
    const detailMatch = window.location.hash.match(/^#\/findings\/([^?]+)/);
    const detailId = detailMatch?.[1];
    if (detailId) {
      await this.renderDetail(decodeURIComponent(detailId), generation);
      return;
    }
    await this.renderList(generation);
  }

  /** 审计会话恢复只重新读取当前固定路由，编辑草稿随旧会话销毁。 */
  private async renderOperationAudit(filters: AuditFilters, generation: number): Promise<void> {
    this.renderShell(loadingState("正在加载操作审计。"));
    const render = (auditContent: string) => renderIdentityAdmin("operations", {
      initial: false, administrator: this.identityAdmin, superAdmin: this.superAdministrator,
      canChangePassword: this.canChangePassword, auditContent,
    });
    try {
      const audit = await loadAudit(filters, this.superAdministrator);
      const body = await render(audit.content);
      if (generation !== this.renderGeneration) return;
      this.renderShell(body);
      bindAudit(this.root, audit, (status) => this.renderAuditAccessError(status));
    } catch (error) {
      if (generation !== this.renderGeneration) return;
      if (error instanceof ApiError && (error.status === 401 || error.status === 403)) {
        this.renderAuditAccessError(error.status);
        return;
      }
      const body = await render('<section class="system-card" role="alert"><h2>无法加载操作审计</h2><p>请重试查询。</p><button class="button" type="button" data-action="retry">重试</button></section>');
      if (generation === this.renderGeneration) this.renderShell(body);
    }
  }

  /** 权限消失时立即移除本页记录和保存草稿。 */
  private renderAuditAccessError(status: number): void {
    ++this.renderGeneration;
    if (status === 403) {
      this.renderShell(authorizationDeniedState());
      return;
    }
    this.clusters = undefined;
    this.namespaces = new Map();
    this.knownKinds.clear();
    this.showLogout = false;
    this.identityAdmin = false;
    this.superAdministrator = false;
    this.canChangePassword = false;
    this.renderShell(authenticationRequiredState("会话已过期。登录后可继续使用操作审计。"), true);
    this.root.querySelector<HTMLElement>("#login-heading")?.focus();
  }

  private async renderList(generation: number): Promise<void> {
    const filters = parseFindingFilters(window.location.hash);
    this.renderShell(loadingState("正在加载筛选选项和诊断结果。"));

    try {
      const clusters = await this.loadClusters();
      if (generation !== this.renderGeneration) return;
      this.clusters = clusters;
      if (clusters.length === 0) {
        this.renderShell(authorizationDeniedState());
        return;
      }
      const cursor = this.pageCursor();
      const [page, summary, namespaces] = await Promise.all([
        apiClient.listFindings({ ...toListParams(filters), continue: cursor }),
        apiClient.summarizeFindings(toSummaryParams(filters)),
        this.loadFindingNamespaces(clusters, filters),
      ]);
      if (generation !== this.renderGeneration) return;

      const scope = serializeFindingFilters({
        cluster: filters.cluster, environment: filters.environment, business: filters.business,
        group: filters.group,
      });
      if (this.knownKindsScope !== scope) this.knownKinds.clear();
      this.knownKindsScope = scope;
      if (filters.kind) this.knownKinds.add(filters.kind);
      for (const kind of Object.keys(summary.byKind)) this.knownKinds.add(kind);
      this.renderShell(renderFindingWorkspace({
        clusters,
        namespaces,
        filters,
        page,
        summary,
        knownKinds: [...this.knownKinds],
        cursor,
      }));
      bindSeverityHelp(this.root);
    } catch (error) {
      if (generation !== this.renderGeneration) return;
      this.renderRequestError(error);
    }
  }

  private pageCursor(): string | undefined {
    return new URLSearchParams(window.location.hash.split("?")[1]).get("continue") ?? undefined;
  }

  private async renderDetail(id: string, generation: number): Promise<void> {
    this.renderShell(loadingState("正在加载诊断详情。"));
    try {
      const finding = await apiClient.getFinding({ id, cluster: toDetailCluster(window.location.hash) });
      if (generation !== this.renderGeneration) return;
      const reviewEnabled = finding.cluster === "local" ? this.findingAI :
        this.identityAdmin && (await this.loadClusters().catch(() => [])).some((instance) => instance.id === finding.cluster && instance.reviewEnabled);
      if (generation !== this.renderGeneration) return;
      const returnQuery = new URLSearchParams(window.location.hash.split("?")[1]).get("return");
      this.renderShell(renderFindingDetail(finding, reviewEnabled, returnQuery) + renderCorrelationSection(id));
      if (this.identityAdmin) bindFindingReview(this.root, id, finding.cluster);
      const region = this.root.querySelector<HTMLElement>("[data-correlation-finding]");
      if (region) void this.loadCorrelation(id, generation, region);
    } catch (error) {
      if (generation !== this.renderGeneration) return;
      this.renderRequestError(error);
    }
  }

  private async loadCorrelation(id: string, generation: number, region: HTMLElement): Promise<void> {
    region.setAttribute("aria-busy", "true");
    region.innerHTML = renderCorrelationLoading();
    let content: string;
    try {
      const bundle = await apiClient.getFindingCorrelation({ id, cluster: toDetailCluster(window.location.hash) });
      content = renderCorrelationEvidence(bundle);
    } catch (error) {
      content = renderCorrelationError(error);
    }
    if (generation !== this.renderGeneration || !region.isConnected) return;
    region.innerHTML = content;
    region.removeAttribute("aria-busy");
  }

  private async loadClusters(): Promise<Cluster[]> {
    return (await apiClient.listClusters()).items;
  }

  /** 合并当前范围内可读的命名空间，按名称去重。 */
  private async loadFindingNamespaces(clusters: Cluster[], filters: FindingFilters): Promise<Namespace[]> {
    if (filters.cluster && filters.cluster !== allClusters) {
      return this.loadNamespaces(filters.cluster);
    }
    const batches = await Promise.all(matchingFindingClusters(clusters, filters).map(async ({ id }) => {
      try {
        return await this.loadNamespaces(id);
      } catch (error) {
        if (error instanceof ApiError && error.status === 403) return [];
        throw error;
      }
    }));
    return [...new Map(batches.flat().map((namespace) => [namespace.name, namespace])).values()];
  }

  private async loadNamespaces(cluster: string): Promise<Namespace[]> {
    const cache = this.namespaces;
    const cached = cache.get(cluster);
    if (cached) return cached;
    const response = await apiClient.listNamespaces({ cluster });
    cache.set(cluster, response.items);
    return response.items;
  }

  /** 更新统一外框，并恢复仍然存在的全局导航焦点。 */
  private renderShell(content: string, signedOut = false): void {
    const active = document.activeElement;
    const activeLink = active instanceof HTMLAnchorElement && this.root.contains(active) &&
      active.closest(".topbar, .workspace-nav, .skip-link") ? active : null;
    const mainFocused = active instanceof HTMLElement && active.id === "portal-content" && this.root.contains(active);
    this.root.innerHTML = renderPortalShell(content, {
      hash: window.location.hash,
      demoMode,
      identityAdmin: this.identityAdmin,
      canManageResources: Object.values(this.resourceAccess).some(Boolean),
      mustChangePassword: this.mustChangePassword,
      canChangePassword: this.canChangePassword,
      showLogout: this.showLogout,
      signedOut,
    });
    if (mainFocused) this.root.querySelector<HTMLElement>("#portal-content")?.focus({ preventScroll: true });
    if (activeLink) {
      const links = Array.from(this.root.querySelectorAll<HTMLAnchorElement>(".topbar a, .workspace-nav a, .skip-link"));
      links.find((link) => link.className === activeLink.className && link.href === activeLink.href)
        ?.focus({ preventScroll: true });
    }
  }

  private handleClick(event: Event): void {
    const target = event.target;
    if (!(target instanceof HTMLElement)) return;
    const action = target.closest<HTMLElement>("[data-action]")?.dataset.action;
    if (action === "skip-content") {
      event.preventDefault();
      this.root.querySelector<HTMLElement>("main")?.focus();
    }
    if (action === "logout") void this.logout(target.closest<HTMLButtonElement>("button"));
    if (action === "retry") void this.route();
    if (action === "retry-correlation") {
      const region = target.closest<HTMLElement>("[data-correlation-finding]");
      const id = region?.dataset.correlationFinding;
      if (region && id) void this.loadCorrelation(id, this.renderGeneration, region);
    }
    if (action === "clear-filters") {
      const { cluster, environment, business, group } = parseFindingFilters(window.location.hash);
      window.location.hash = `#/findings?${serializeFindingFilters({ cluster, environment, business, group })}`;
    }
  }

  private handleFilterChange(event: Event): void {
    const target = event.target;
    if (!(target instanceof HTMLSelectElement)) return;
    if (target.hasAttribute("data-role-directory")) {
      const field = target.form?.elements.namedItem("cluster");
      if (field instanceof HTMLInputElement && target.value) field.value = target.value;
      return;
    }
    if (!target.dataset.filter) return;

    const filters = parseFindingFilters(window.location.hash);
    const value = target.value || undefined;
    const scopeKey = [...scopeDimensions, "cluster" as const].find((key) => key === target.dataset.filter);
    if (scopeKey) {
      const next = changeFindingScope(this.clusters ?? [], filters, scopeKey, value);
      window.location.hash = `#/findings?${serializeFindingFilters(next)}`;
      return;
    }
    switch (target.dataset.filter) {
      case "namespace":
        filters.namespace = value;
        break;
      case "kind":
        filters.kind = value;
        break;
      case "severity":
        filters.severity = value as FindingFilters["severity"];
        break;
      default:
        return;
    }

    window.location.hash = `#/findings?${serializeFindingFilters(filters)}`;
  }

  private async login(event: Event): Promise<void> {
    const form = event.target;
    if (!(form instanceof HTMLFormElement) || !form.hasAttribute("data-login")) return;
    event.preventDefault();
    const button = form.querySelector<HTMLButtonElement>('button[type="submit"]');
    const status = form.querySelector<HTMLElement>("[data-login-status]");
    if (!button || !status || button.disabled) return;
    const fields = new FormData(form);
    button.disabled = true;
    status.textContent = "";
    try {
      const response = await fetch("/api/v1/auth/login", {
        method: "POST",
        headers: { "Content-Type": "application/json" },
        body: JSON.stringify({
          provider: fields.has("provider") ? "ldap" : "local",
          username: fields.get("username"),
          password: fields.get("password"),
        }),
        credentials: "same-origin",
      });
      if (response.status === 204) {
        form.reset();
        this.clusters = undefined;
        this.namespaces = new Map();
        this.knownKinds.clear();
        this.showLogout = true;
        await this.route();
      } else if (response.status === 401) {
        status.textContent = "登录失败，请核对登录方式和账号密码。";
      } else if (response.status === 429) {
        status.textContent = "登录请求过多，请稍后重试。";
      } else {
        status.textContent = "登录服务暂不可用，请稍后重试。";
      }
    } catch {
      status.textContent = "登录服务暂不可用，请稍后重试。";
    } finally {
      const password = form.elements.namedItem("password");
      if (password instanceof HTMLInputElement) password.value = "";
      button.disabled = false;
    }
  }

  private async logout(button: HTMLButtonElement | null): Promise<void> {
    if (!button || button.disabled) return;
    button.disabled = true;
    try {
      const response = await fetch("/api/v1/auth/logout", {
        method: "POST",
        credentials: "same-origin",
      });
      if (response.status !== 204) throw new Error("logout failed");
      this.clusters = undefined;
      this.namespaces = new Map();
      this.knownKinds.clear();
      this.showLogout = false;
      this.identityAdmin = false;
      this.mustChangePassword = false;
      ++this.renderGeneration;
      this.renderShell(authenticationRequiredState(), true);
    } catch {
      button.textContent = "退出失败，请重试";
      button.disabled = false;
    }
  }

  /** 根据诊断接口状态显示登录、无权限或重试页面。 */
  private renderRequestError(error: unknown): void {
    if (error instanceof ApiError && error.status === 401) {
      this.showLogout = false;
      this.renderShell(authenticationRequiredState(), true);
      return;
    }
    this.renderShell(error instanceof ApiError && error.status === 403
      ? authorizationDeniedState()
      : errorState("无法获取诊断数据，请稍后重试。"));
  }
}
