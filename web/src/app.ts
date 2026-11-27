import { bindAISkills, loadAISkills } from "./views/ai/skills.ts";
import { bindAIServices, loadAIServices } from "./views/ai/services.ts";
import { bindKnowledgeAsk, renderKnowledgeAsk, type AssistantFinding } from "./views/wiki/wiki-tools.ts";
import { bindResourceCatalog } from "./views/resources/catalog-workspace.ts";
import type { Catalog, CatalogFilters } from "./views/resources/catalog-model.ts";
import { renderObservabilityDashboardLinks } from "./views/diagnosis/observability-sources.ts";
import { datasourcesPath, legacyDatasourcesPath, resolveResourceTab, type ResourceAccess } from "./domain/resource-routes.ts";
import { loadUnifiedOverview } from "./views/diagnosis/unified-overview.ts";
import { bindPermissionCatalog, loadPermissionCatalog, renderPermissionCatalog } from "./views/permissions/permission-catalog.ts";
import { bindPortalForms } from "./portal-forms.ts";
import { bindDefaultRoleWorkspace, filterAccounts, handlePlatformClick, updateScopeOptions } from "./views/permissions/platform-admin.ts";
import { bindClusterWorkspace } from "./views/clusters/cluster-workspace.ts";
import { renderResourceManagement } from "./views/resources/resource-management.ts";
import { bindWiki, renderWiki, type WikiAccess } from "./views/wiki/wiki.ts";
import { handleClusterMaintenanceClick, updateClusterCredential } from "./views/clusters/cluster-onboarding.ts";
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
import { diagnosticListPaths, diagnosticListView } from "./domain/diagnostic-routes.ts";
import { renderPortalShell } from "./components/portal-shell.ts";
import {
  authenticationConfigError,
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
import { bindFindingKnowledge, findingKnowledgePanel } from "./views/diagnosis/finding-knowledge.ts";
import { bindIncidentWorkspace, incidentError, loadIncidentWorkspace } from "./views/diagnosis/incident-list.ts";
import { incidentEvents, incidentPrefix } from "./views/diagnosis/incident-filters.ts";
import { bindRcaPage, loadRcaPage } from "./views/diagnosis/incident-rca.ts";
import { bindTopologyPage, loadTopologyPage } from "./views/diagnosis/incident-topology.ts";
import { bindPoliciesPage, loadPoliciesPage, policiesPath } from "./views/diagnosis/diagnostic-settings.ts";
import { bindDatasourcesWorkspace, loadDatasourcesPage } from "./views/resources/datasource-management.ts";
import { bindIncidentDetail, loadIncidentDetail } from "./views/diagnosis/incident-detail.ts";
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
  private permissionCatalog = false;
  private auditRead = false;
  private canUseAssistant = false;
  private canChatFinding = false;
  private wikiAccess: WikiAccess | undefined;
  private catalogFilters = new Map<Catalog, CatalogFilters>();
  private resourceAccess: ResourceAccess = { clusters: false, environments: false, "business-lines": false };
  private findingAI = false;
  private reviewAccess = false;
  private incidentsAvailable = false;
  private incidentCreate = false;
  private overviewRequest?: AbortController;
  private diagnosticSettings = { datasourceWrite: false, datasourceDelete: false, datasourceTest: false, policies: false, policyWrite: false };
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
      void handleClusterMaintenanceClick(event);
      handlePlatformClick(event);
      this.handleClick(event);
    });
    this.root.addEventListener("keydown", (event) => {
      const target = event.target;
      const button = this.root.querySelector<HTMLButtonElement>('.portal-menu-toggle[aria-expanded="true"]');
      if (event.key !== "Escape" || !button || !(target instanceof HTMLElement) ||
        !target.closest(".portal-sidebar, .portal-menu-toggle")) return;
      event.preventDefault();
      this.setPortalMenu(button, false);
    });
    bindPortalForms(this.root, {
      refresh: () => void this.route(),
      sessionExpired: () => this.renderAuthenticationExpired(),
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
    this.root.addEventListener("change", updateClusterCredential);
    this.root.addEventListener("input", (event) => {
      if (!(event.target instanceof HTMLElement)) return;
      const status = event.target.closest(".cluster-editor")?.querySelector('[role="status"]');
      if (status) status.textContent = "有未保存的修改。";
    });
    this.root.addEventListener("change", changeNamespaceOwnership);
    this.root.addEventListener("input", updateLDAP);
    this.root.addEventListener("input", filterAccounts);
    this.root.addEventListener("change", filterAccounts);
    this.root.addEventListener("change", (event) => void updateScopeOptions(event));
    this.root.addEventListener("change", updateLDAP);
    this.root.addEventListener("change", (event) => this.handleFilterChange(event));
    this.root.addEventListener("submit", (event) => {
      const form = event.target;
      if (!(form instanceof HTMLFormElement) || !form.hasAttribute("data-diagnostic-search")) return;
      event.preventDefault();
      const filters = parseFindingFilters(window.location.hash);
      filters.problem = String(new FormData(form).get("problem") ?? "");
      this.navigateFindingFilters(filters);
    });
    window.addEventListener("keydown", (event) => dismissSeverityHelp(event, this.root));
    if (!window.location.hash) window.location.hash = diagnosticListPaths.overview;
    void this.route();
  }

  private async route(): Promise<void> {
    const generation = ++this.renderGeneration;
    this.canUseAssistant = false;
    this.canChatFinding = false;
    this.findingAI = false;
    this.reviewAccess = false;
    this.overviewRequest?.abort();
    this.incidentsAvailable = false;
    this.incidentCreate = false;
    this.diagnosticSettings = { datasourceWrite: false, datasourceDelete: false, datasourceTest: false, policies: false, policyWrite: false };
    if (!demoMode) {
      const isAuthenticationRoute = window.location.hash === "#/admin/ldap";
      try {
        const response = await fetch("/api/v1/admin/status");
        if (generation !== this.renderGeneration) return;
        if (isAuthenticationRoute && response.status === 401) {
          this.renderAuthenticationExpired();
          return;
        }
        if (isAuthenticationRoute && !response.ok) {
          this.renderShell(response.status === 403 ? authorizationDeniedState() : authenticationConfigError());
          return;
        }
        const status = response.ok ? await response.json() : {};
        if (generation !== this.renderGeneration) return;
        this.identityAdmin = status.admin === true;
        this.canUseAssistant = status.wikiAsk === true;
        this.canChatFinding = status.findingChat === true;
        this.permissionCatalog = status.permissionCatalog === true || this.identityAdmin;
        this.auditRead = status.auditRead === true || this.identityAdmin;
        this.wikiAccess = status.interfaceRoleAccount === true ? {
          draft: status.wikiDraft === true, candidates: status.wikiCandidates === true,
          governance: status.wikiGovernance === true, ask: status.wikiAsk !== false,
        } : undefined;
        this.resourceAccess = {
          clusters: status.manageClusters === true || this.identityAdmin,
          environments: status.manageEnvironments === true,
          "business-lines": status.manageBusinessLines === true,
          datasources: status.diagnosticDatasources === true,
        };
        this.findingAI = status.findingAI === true;
        this.incidentsAvailable = status.incidents === true;
        this.incidentCreate = status.incidentCreate === true;
        this.diagnosticSettings = {
          datasourceWrite: status.diagnosticDatasourceWrite === true,
          datasourceDelete: status.diagnosticDatasourceDelete === true,
          datasourceTest: status.diagnosticDatasourceTest === true, policies: status.diagnosticPolicies === true,
          policyWrite: status.diagnosticPolicyWrite === true,
        };
        this.reviewAccess = status.reviewAccess === true || this.identityAdmin;
        this.mustChangePassword = status.mustChangePassword === true;
        this.canChangePassword = status.canChangePassword === true;
        this.superAdministrator = status.superAdministrator === true;
      } catch {
        if (generation !== this.renderGeneration) return;
        if (isAuthenticationRoute) {
          this.renderShell(authenticationConfigError());
          return;
        }
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
      const path = hash.split("?", 1)[0] ?? "";
      if (!this.mustChangePassword && (path === "#/ai" || path === "#/ai/skills")) {
        if (!this.identityAdmin) {
          this.renderShell(authorizationDeniedState());
          return;
        }
        this.renderShell(loadingState("正在读取 AI 服务配置。"));
        try {
          if (path === "#/ai/skills") {
            const page = await loadAISkills();
            if (generation !== this.renderGeneration) return;
            this.renderShell(page.html, false, true);
            bindAISkills(this.root, page.data, page.scopes, () => void this.route());
          } else {
            const page = await loadAIServices();
            if (generation !== this.renderGeneration) return;
            this.renderShell(page.html, false, true);
            bindAIServices(this.root, page.data, () => void this.route(), hash);
          }
        } catch {
          if (generation === this.renderGeneration) this.renderShell(errorState("无法加载 AI 服务配置。"));
        }
        return;
      }
      if (!this.mustChangePassword && path === legacyDatasourcesPath) {
        window.location.replace(datasourcesPath + hash.slice(legacyDatasourcesPath.length));
        return;
      }
      if (!this.mustChangePassword && /^#\/resources(?:[/?]|$)/.test(hash)) {
        if (path === datasourcesPath && !this.resourceAccess.datasources) {
          this.renderShell(authorizationDeniedState());
          return;
        }
        if (resolveResourceTab(hash, this.resourceAccess) === "datasources") {
          if (path !== datasourcesPath) {
            window.location.replace(datasourcesPath + hash.slice(path.length));
            return;
          }
          await this.renderDiagnosticSettings(hash, generation, true);
          return;
        }
        this.renderShell(loadingState("正在加载资源管理。"));
        try {
          const page = await renderResourceManagement(hash, this.resourceAccess, this.identityAdmin);
          if (generation === this.renderGeneration) {
            this.renderShell(page.html, false, page.ownsHeading);
            if (page.catalog) {
              const { kind } = page.catalog;
              const filters = this.catalogFilters.get(kind) ?? { query: "", sort: "sequence" };
              this.catalogFilters.set(kind, filters);
              bindResourceCatalog(this.root, page.catalog, filters);
            } else bindClusterWorkspace(this.root);
          }
        } catch (error) {
          if (generation === this.renderGeneration) {
            if (error instanceof Response && [401, 403].includes(error.status)) this.renderRequestError(new ApiError(error.status, undefined));
            else this.renderShell(errorState("无法加载资源管理。"));
          }
        }
        return;
      }
      if (!this.mustChangePassword && hash === "#/admin/access/catalog") {
        this.renderShell(loadingState("正在加载权限目录。"));
        try {
          const catalog = await loadPermissionCatalog();
          if (generation !== this.renderGeneration) return;
          const body = await renderIdentityAdmin("access", {
            initial: false, administrator: this.identityAdmin, superAdmin: this.superAdministrator,
            canChangePassword: this.canChangePassword, auditContent: `${this.identityAdmin ?
              '<nav class="identity-nav access-workspace-tabs" aria-label="权限管理"><a href="#/admin/access">角色</a><a href="#/admin/access/groups">用户组</a><a href="#/admin/access/catalog" aria-current="page">权限目录</a></nav>' : ""}${renderPermissionCatalog()}`,
          });
          if (generation !== this.renderGeneration) return;
          this.renderShell(body);
          bindPermissionCatalog(this.root, catalog, { roleLink: this.identityAdmin });
        } catch {
          if (generation === this.renderGeneration) this.renderShell(errorState("无法加载权限目录。请核对访问权限后重试。"));
        }
        return;
      }
      const adminRoute = hash.startsWith("#/admin");
      if (isAuthenticationRoute && !this.mustChangePassword && !this.identityAdmin) {
        this.renderShell(authorizationDeniedState());
        return;
      }
      if (this.mustChangePassword || (this.identityAdmin && adminRoute) || (this.canChangePassword && hash === "#/admin/password")) {
        const [, , resource = "ldap", roleID] = (hash.split("?", 1)[0] ?? "").split("/");
        try {
          const body = await renderIdentityAdmin(this.mustChangePassword ? "password" : resource, {
            initial: this.mustChangePassword,
            roleID,
            administrator: this.identityAdmin,
            superAdmin: this.superAdministrator,
            canChangePassword: this.canChangePassword,
          });
          if (generation === this.renderGeneration) {
            this.renderShell(body);
            bindDefaultRoleWorkspace(this.root);
          }
        } catch (error) {
          if (generation !== this.renderGeneration) return;
          if (error instanceof Response && error.status === 401) this.renderAuthenticationExpired();
          else this.renderShell(error instanceof Response && error.status === 403
            ? authorizationDeniedState() : authenticationConfigError());
        }
        return;
      }
    }
    if (!demoMode && /^#\/wiki(?:[/?]|$)/.test(window.location.hash)) {
      this.renderShell(loadingState("正在加载知识库。"));
      try {
        const body = await renderWiki(window.location.hash, this.identityAdmin, this.wikiAccess);
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
    const currentHash = window.location.hash;
    const diagnosticPath = currentHash.split("?", 1)[0] ?? "";
    if (diagnosticPath === policiesPath) {
      await this.renderDiagnosticSettings(currentHash, generation, false);
      return;
    }
    if (diagnosticPath.startsWith(incidentPrefix)) {
      await this.renderIncidentPage(currentHash, generation, diagnosticPath.slice(incidentPrefix.length));
      return;
    }
    if (diagnosticPath === diagnosticListPaths.overview) {
      this.renderShell(loadingState("正在加载诊断总览。"));
      const request = new AbortController();
      this.overviewRequest = request;
      try {
        const overview = await loadUnifiedOverview(currentHash, this.incidentsAvailable,
          AbortSignal.any([request.signal, AbortSignal.timeout(12_000)]), this.resourceAccess.datasources, this.findingAI, this.reviewAccess);
        if (generation === this.renderGeneration) {
          this.renderShell(overview.html);
          overview.bind(this.root, request.signal);
        }
      } catch (error) {
        if (generation === this.renderGeneration) this.renderRequestError(error);
      }
      return;
    }
    const createFromFinding = diagnosticPath === incidentEvents && this.incidentCreate &&
      new URLSearchParams(currentHash.split("?")[1]).get("create") === "1";
    if ((this.incidentsAvailable || createFromFinding) && diagnosticPath === incidentEvents) {
      await this.renderIncidentPage(currentHash, generation);
      return;
    }
    const detailMatch = window.location.hash.match(/^#\/(?:findings|diagnostics\/events)\/([^?]+)/);
    const detailId = detailMatch?.[1];
    if (detailId) {
      let id: string;
      try {
        id = decodeURIComponent(detailId);
      } catch {
        this.renderShell(errorState("诊断记录链接格式无效。"));
        return;
      }
      await this.renderDetail(id, generation);
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
    this.permissionCatalog = false;
    this.auditRead = false;
    this.superAdministrator = false;
    this.canChangePassword = false;
    this.renderShell(authenticationRequiredState("会话已过期。登录后可继续使用操作审计。"), true);
    this.root.querySelector<HTMLElement>("#login-heading")?.focus();
  }

  private async renderDiagnosticSettings(hash: string, generation: number, datasources: boolean): Promise<void> {
    this.renderShell(loadingState("正在读取诊断配置。"));
    try {
      const refresh = () => void this.route();
      if (datasources) {
        const page = await loadDatasourcesPage(hash, this.diagnosticSettings.datasourceWrite);
        if (generation !== this.renderGeneration) return;
        this.renderShell(page.html, false, true);
        bindDatasourcesWorkspace(this.root, page, {
          write: this.diagnosticSettings.datasourceWrite, remove: this.diagnosticSettings.datasourceDelete, test: this.diagnosticSettings.datasourceTest, createIncident: this.incidentCreate, aiAdmin: this.identityAdmin,
        });
      } else {
        const page = await loadPoliciesPage(hash, this.diagnosticSettings.policyWrite);
        if (generation !== this.renderGeneration) return;
        this.renderShell(page.html);
        bindPoliciesPage(this.root, page.items, refresh);
      }
    } catch (error) {
      if (generation === this.renderGeneration) this.renderIncidentError(error);
    }
  }

  private renderIncidentError(error: unknown): void {
    if (error instanceof ApiError && (error.status === 401 || error.status === 403)) this.renderRequestError(error);
    else this.renderShell(errorState(incidentError(error)));
  }

  private async renderIncidentPage(hash: string, generation: number, encodedID?: string): Promise<void> {
    this.renderShell(loadingState("正在加载故障数据。"));
    try {
      if (encodedID !== undefined) {
        const [segment, tab] = encodedID.split("/");
        if (!segment) throw new Error("故障 ID 无效。");
        const id = decodeURIComponent(segment);
        if (tab === "rca") {
          const page = await loadRcaPage(id, hash);
          if (generation !== this.renderGeneration) return;
          this.renderShell(page.html);
          bindRcaPage(this.root, id, hash, page);
          return;
        }
        if (tab === "topology") {
          const page = await loadTopologyPage(id, hash);
          if (generation !== this.renderGeneration) return;
          this.renderShell(page.html);
          bindTopologyPage(this.root, id, hash, page.graph, () => void this.route());
          return;
        }
        const { html, detail } = await loadIncidentDetail(id, hash);
        if (generation !== this.renderGeneration) return;
        this.renderShell(html);
        bindIncidentDetail(this.root, detail, hash, () => void this.route());
        const item = detail.incident;
        void this.loadObservabilityDashboards(generation, { clusterId: item.scope.clusterId, namespace: item.scope.namespace,
          businessId: item.scope.businessId, anchor: item.firstSeen });
      } else {
        const html = await loadIncidentWorkspace(hash, diagnosticListView(hash) === "overview", this.incidentCreate, this.incidentsAvailable);
        if (generation !== this.renderGeneration) return;
        this.renderShell(html);
        bindIncidentWorkspace(this.root, hash);
      }
    } catch (error) {
      if (generation === this.renderGeneration) this.renderIncidentError(error);
    }
  }

  private async renderList(generation: number): Promise<void> {
    const hash = window.location.hash;
    const filters = parseFindingFilters(hash);
    const view = diagnosticListView(hash);
    this.renderShell(loadingState("正在加载筛选选项和诊断结果。"));

    try {
      const clusters = await this.loadClusters();
      if (generation !== this.renderGeneration) return;
      this.clusters = clusters;
      if (clusters.length === 0) {
        this.renderShell(authorizationDeniedState());
        return;
      }
      const cursor = view === "overview" ? undefined : new URLSearchParams(hash.split("?")[1]).get("continue") ?? undefined;
      const [page, summary, namespaces] = await Promise.all([
        apiClient.listFindings(view === "overview" ? { ...toListParams(filters), limit: 5 } : { ...toListParams(filters), continue: cursor }),
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
        view,
      }));
      bindSeverityHelp(this.root);
    } catch (error) {
      if (generation !== this.renderGeneration) return;
      this.renderRequestError(error);
    }
  }

  private async renderDetail(id: string, generation: number): Promise<void> {
    this.renderShell(loadingState("正在加载诊断详情。"));
    try {
      const finding = await apiClient.getFinding({ id, cluster: toDetailCluster(window.location.hash) });
      if (generation !== this.renderGeneration) return;
      const reviewEnabled = finding.cluster === "local" ? this.findingAI :
        this.reviewAccess && (await this.loadClusters().catch(() => [])).some((instance) => instance.id === finding.cluster && instance.reviewEnabled);
      if (generation !== this.renderGeneration) return;
      const hash = window.location.hash;
      const params = new URLSearchParams(hash.split("?")[1]);
      const view = diagnosticListView(hash);
      const returnView = params.get("from") === "overview" ? "overview" : view;
      const assistantFinding = reviewEnabled && this.canChatFinding ? { id, cluster: finding.cluster, namespace: finding.namespace } : undefined;
      this.renderShell(renderFindingDetail(finding, reviewEnabled, params.get("return"), returnView, this.incidentCreate, Boolean(assistantFinding)) + renderCorrelationSection(id) + (demoMode ? "" : findingKnowledgePanel()), false, false, assistantFinding);
      if (!demoMode) bindFindingKnowledge(this.root, id, finding.cluster, finding.reviewRevision);
      if (reviewEnabled) bindFindingReview(this.root, id, finding.cluster);
      if (finding.namespace) void this.loadObservabilityDashboards(generation, { clusterId: finding.cluster,
        namespace: finding.namespace, anchor: finding.createdAt });
      const region = this.root.querySelector<HTMLElement>("[data-correlation-finding]");
      if (region) void this.loadCorrelation(id, generation, region);
    } catch (error) {
      if (generation !== this.renderGeneration) return;
      this.renderRequestError(error);
    }
  }

  private async loadObservabilityDashboards(generation: number, params: Parameters<typeof renderObservabilityDashboardLinks>[0]) {
    if (demoMode || !params.namespace) return;
    const html = await renderObservabilityDashboardLinks(params);
    if (html && generation === this.renderGeneration) this.root.querySelector("main")?.insertAdjacentHTML("beforeend", html);
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
  private renderShell(content: string, signedOut = false, contentOwnsHeading = false, assistantFinding?: AssistantFinding): void {
    const active = document.activeElement;
    const activeLink = active instanceof HTMLAnchorElement && this.root.contains(active) &&
      active.closest(".topbar, .portal-sidebar, .skip-link") ? active : null;
    const activeRegion = activeLink?.closest("nav")?.getAttribute("aria-label");
    const mainFocused = active instanceof HTMLElement && active.id === "portal-content" && this.root.contains(active);
    this.root.innerHTML = renderPortalShell(content, {
      hash: window.location.hash,
      demoMode,
      identityAdmin: this.identityAdmin, permissionCatalog: this.permissionCatalog, auditRead: this.auditRead,
      resourceAccess: this.resourceAccess,
      wikiGovernance: this.identityAdmin || this.wikiAccess?.candidates === true || this.wikiAccess?.governance === true,
      mustChangePassword: this.mustChangePassword,
      canChangePassword: this.canChangePassword,
      showLogout: this.showLogout,
      signedOut,
      contentOwnsHeading,
      diagnosticPolicies: this.diagnosticSettings.policies,
    });
    if ((this.canUseAssistant || assistantFinding) && !signedOut && !this.mustChangePassword) {
      const assistant = document.createElement("div");
      assistant.className = "portal-ai-assistant";
      assistant.innerHTML = `<button type="button" class="button ai-assistant-trigger" data-open-wiki-ask aria-haspopup="dialog"><span aria-hidden="true">✦</span>诊断助手</button>${renderKnowledgeAsk(true, assistantFinding)}`;
      this.root.append(assistant);
      bindKnowledgeAsk(assistant, assistantFinding, this.root.querySelector<HTMLElement>("[data-findings-assistant]"));
    }
    if (mainFocused) this.root.querySelector<HTMLElement>("#portal-content")?.focus({ preventScroll: true });
    if (activeLink) {
      const links = Array.from(this.root.querySelectorAll<HTMLAnchorElement>(".topbar a, .portal-sidebar a, .skip-link"));
      const replacement = links.find((link) => link.href === activeLink.href &&
        link.closest("nav")?.getAttribute("aria-label") === activeRegion && link.className === activeLink.className);
      if (replacement?.getClientRects().length) replacement.focus({ preventScroll: true });
      else this.root.querySelector<HTMLElement>("#portal-content")?.focus({ preventScroll: true });
    }
  }

  /** 会话超时后保留认证配置地址，重新登录即可回到当前页面。 */
  private renderAuthenticationExpired(): void {
    this.showLogout = false;
    this.identityAdmin = false;
    this.mustChangePassword = false;
    this.renderShell(authenticationRequiredState("会话已过期，请重新登录后查看认证配置。"), true);
    this.root.querySelector<HTMLElement>("#login-heading")?.focus();
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
    if (action === "toggle-portal-menu") {
      const button = target.closest<HTMLButtonElement>("button");
      if (button) this.setPortalMenu(button, button.getAttribute("aria-expanded") !== "true");
    }
    if (action === "retry") void this.route();
    if (action === "retry-correlation") {
      const region = target.closest<HTMLElement>("[data-correlation-finding]");
      const id = region?.dataset.correlationFinding;
      if (region && id) void this.loadCorrelation(id, this.renderGeneration, region);
    }
    if (action === "clear-filters") {
      const { cluster, environment, business, group } = parseFindingFilters(window.location.hash);
      this.navigateFindingFilters({ cluster, environment, business, group });
    }
  }

  /** 同步移动栏目状态，并在菜单和触发按钮之间转移键盘焦点。 */
  private setPortalMenu(button: HTMLButtonElement, isOpen: boolean): void {
    this.root.querySelector(".portal-layout")?.classList.toggle("menu-open", isOpen);
    button.setAttribute("aria-expanded", String(isOpen));
    if (isOpen) {
      const sidebar = this.root.querySelector(".portal-sidebar");
      (sidebar?.querySelector<HTMLAnchorElement>('a[aria-current="page"]') ??
        sidebar?.querySelector<HTMLAnchorElement>("a"))?.focus();
    } else {
      button.focus();
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
      this.navigateFindingFilters(next);
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

    this.navigateFindingFilters(filters);
  }

  private navigateFindingFilters(filters: FindingFilters): void {
    const view = diagnosticListView(window.location.hash);
    window.location.hash = `${diagnosticListPaths[view]}?${serializeFindingFilters(filters)}`;
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
      this.catalogFilters.clear();
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
