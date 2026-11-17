import { bindWiki, renderWiki } from "./views/wiki/wiki.ts";
import { submitOnboarding } from "./views/clusters/cluster-onboarding.ts";
import {
  ApiError,
  type Cluster,
  type FindingPage,
  type FindingSummary,
  type Namespace,
} from "./api/generated.ts";
import { renderIdentityAdmin, submitIdentityAdmin } from "./views/permissions/identity-admin.ts";
import { submitRole } from "./views/permissions/roles.ts";
import { apiClient } from "./api/client.ts";
import {
  authenticationRequiredState,
  authorizationDeniedState,
  emptyState,
  errorState,
  loadingState,
} from "./components/states.ts";
import {
  parseFindingFilters,
  serializeFindingFilters,
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
import {
  renderFilters,
  renderFindingTable,
  renderSummary,
} from "./views/diagnosis/finding-list.ts";

const demoMode = document.querySelector('meta[name="kube-aiops-demo"][content="true"]') !== null;

export class PortalApp {
  private readonly root: HTMLElement;
  private clusters: Cluster[] | undefined;
  private namespaces = new Map<string, Namespace[]>();
  private knownKinds = new Set<string>();
  private renderGeneration = 0;
  private identityAdmin = false;
  private findingAI = false;
  private mustChangePassword = false;
  private showLogout = !demoMode;

  constructor(root: HTMLElement) {
    this.root = root;
  }

  start(): void {
    window.addEventListener("hashchange", () => void this.route());
    this.root.addEventListener("click", (event) => this.handleClick(event));
    this.root.addEventListener("submit", (event) => {
      const form = event.target;
      if (form instanceof HTMLFormElement && form.hasAttribute("data-role-form")) {
        event.preventDefault();
        void submitRole(form, event instanceof SubmitEvent && event.submitter?.getAttribute("value") === "delete");
        return;
      }
      if (form instanceof HTMLFormElement && form.dataset.onboardingMode) {
        event.preventDefault();
        void submitOnboarding(form, event instanceof SubmitEvent && event.submitter?.getAttribute("value") === "test");
        return;
      }
      if (form instanceof HTMLFormElement && form.dataset.identityResource) {
        event.preventDefault();
        void submitIdentityAdmin(form, () => {
          this.identityAdmin = false;
          this.mustChangePassword = false;
          window.location.hash = "#/findings";
          void this.route();
        }, event instanceof SubmitEvent && event.submitter?.getAttribute("value") === "test");
        return;
      }
      void this.login(event);
    });
    this.root.addEventListener("change", (event) => this.handleFilterChange(event));
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
        this.findingAI = this.identityAdmin && status.findingAI === true;
        this.mustChangePassword = status.mustChangePassword === true;
      } catch {
        if (generation !== this.renderGeneration) return;
      }
      const adminRoute = window.location.hash.startsWith("#/admin");
      if (this.mustChangePassword || (this.identityAdmin && adminRoute)) {
        const [, , resource = "ldap", roleID] = window.location.hash.split("/");
        const body = await renderIdentityAdmin(this.mustChangePassword ? "password" : resource, this.mustChangePassword, roleID);
        if (generation === this.renderGeneration) this.renderShell(body);
        return;
      }
    }
    if (!demoMode && /^#\/wiki(?:[/?]|$)/.test(window.location.hash)) {
      this.renderShell(loadingState("正在加载知识库。"));
      try {
        const body = await renderWiki(window.location.hash, this.identityAdmin);
        if (generation === this.renderGeneration) {
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

  private async renderList(generation: number): Promise<void> {
    const filters = parseFindingFilters(window.location.hash);
    this.renderShell(loadingState("正在加载筛选选项和诊断结果。"));

    try {
      const clusters = await this.loadClusters();
      if (generation !== this.renderGeneration) return;
      if (clusters.length === 0) {
        this.renderShell(authorizationDeniedState());
        return;
      }
      const hash = window.location.hash;
      const explicitCluster = new URLSearchParams(hash.split("?")[1]).has("cluster");
      if (!explicitCluster && !clusters.some((cluster) => cluster.id === filters.cluster)) {
        window.location.replace(`#/findings?${serializeFindingFilters({ cluster: clusters[0]?.id })}`);
        return;
      }
      const namespaces = await this.loadNamespaces(filters.cluster ?? "local");
      if (generation !== this.renderGeneration) return;

      const [page, summary] = await Promise.all([
        apiClient.listFindings({ ...toListParams(filters), continue: this.pageCursor() }),
        apiClient.summarizeFindings(toSummaryParams(filters)),
      ]);
      if (generation !== this.renderGeneration) return;

      if (filters.kind) this.knownKinds.add(filters.kind);
      for (const kind of Object.keys(summary.byKind)) this.knownKinds.add(kind);
      this.renderFindingDomain(clusters, namespaces, filters, page, summary);
    } catch (error) {
      if (generation !== this.renderGeneration) return;
      this.renderShell(this.requestErrorState(error));
    }
  }

  private pageCursor(): string | undefined {
    return new URLSearchParams(window.location.hash.split("?")[1]).get("continue") ?? undefined;
  }

  private async renderDetail(id: string, generation: number): Promise<void> {
    this.renderShell(loadingState("正在加载诊断详情。"));
    try {
      const finding = await apiClient.getFinding({ id, cluster: parseFindingFilters(window.location.hash).cluster });
      if (generation !== this.renderGeneration) return;
      const reviewEnabled = finding.cluster === "local" ? this.findingAI :
        this.identityAdmin && (await this.loadClusters().catch(() => [])).some((instance) => instance.id === finding.cluster && instance.reviewEnabled);
      if (generation !== this.renderGeneration) return;
      this.renderShell(renderFindingDetail(finding, reviewEnabled) + renderCorrelationSection(id));
      if (this.identityAdmin) bindFindingReview(this.root, id, finding.cluster);
      const region = this.root.querySelector<HTMLElement>("[data-correlation-finding]");
      if (region) void this.loadCorrelation(id, generation, region);
    } catch (error) {
      if (generation !== this.renderGeneration) return;
      this.renderShell(this.requestErrorState(error));
    }
  }

  private async loadCorrelation(id: string, generation: number, region: HTMLElement): Promise<void> {
    region.setAttribute("aria-busy", "true");
    region.innerHTML = renderCorrelationLoading();
    let content: string;
    try {
      const bundle = await apiClient.getFindingCorrelation({ id, cluster: parseFindingFilters(window.location.hash).cluster });
      content = renderCorrelationEvidence(bundle);
    } catch (error) {
      content = renderCorrelationError(error);
    }
    if (generation !== this.renderGeneration || !region.isConnected) return;
    region.innerHTML = content;
    region.removeAttribute("aria-busy");
  }

  private async loadClusters(): Promise<Cluster[]> {
    const response = await apiClient.listClusters();
    this.clusters = response.items;
    return response.items;
  }

  private async loadNamespaces(cluster: string): Promise<Namespace[]> {
    const cached = this.namespaces.get(cluster);
    if (cached) return cached;
    const response = await apiClient.listNamespaces({ cluster });
    this.namespaces.set(cluster, response.items);
    return response.items;
  }

  private renderFindingDomain(
    clusters: Cluster[],
    namespaces: Namespace[],
    filters: FindingFilters,
    page: FindingPage,
    summary: FindingSummary,
  ): void {
    const body = `
      ${renderFilters(clusters, namespaces, [...this.knownKinds], filters)}
      ${renderSummary(summary)}
      ${page.items.length === 0 ? emptyState() : renderFindingTable(page.items, summary.total)}
    `;
    const params = new URLSearchParams(serializeFindingFilters(filters));
    if (page.pagination.continue) params.set("continue", page.pagination.continue);
    const next = page.pagination.continue
      ? `<a class="button" href="#/findings?${params.toString().replaceAll("&", "&amp;")}">下一页</a>`
      : "";
    const first = this.pageCursor()
      ? `<a class="button" href="#/findings?${serializeFindingFilters(filters).replaceAll("&", "&amp;")}">返回首页</a>`
      : "";
    this.renderShell(body + (first || next ? `<nav class="pagination" aria-label="诊断结果分页">${first}${next}</nav>` : ""));
  }

  private renderShell(content: string): void {
    const hash = window.location.hash;
    const adminPage = this.mustChangePassword || (this.identityAdmin && hash.startsWith("#/admin"));
    const detailPage = /^#\/findings\/[^?]+/.test(hash);
    const signedOut = content.includes("data-login");
    const wikiPage = /^#\/wiki(?:[/?]|$)/.test(hash);
    const title = adminPage ? "系统管理" : wikiPage ? "运维知识库" : detailPage ? "诊断详情" : "诊断控制台";
    this.root.innerHTML = `
      <a class="skip-link" href="#portal-content" data-action="skip-content">跳到主要内容</a>
      <header class="topbar">
        <a class="brand" href="#/findings" aria-label="kube-aiops 诊断首页">
          <span class="brand-mark" aria-hidden="true">K</span>
          <span><strong>kube-aiops</strong><small>Kubernetes 诊断</small></span>
        </a>
        <div class="portal-actions">
          ${this.showLogout && !signedOut ? '<button class="button secondary" type="button" data-action="logout">退出登录</button>' : ""}
        </div>
      </header>
      <div class="portal-layout${signedOut ? " portal-signed-out" : ""}">
        ${signedOut || this.mustChangePassword ? "" : `<nav class="workspace-nav" aria-label="工作台导航">
          <a href="#/findings"${adminPage || wikiPage ? "" : ' aria-current="page"'}>诊断控制台</a>
          ${this.identityAdmin ? `<a href="#/admin/ldap"${adminPage ? ' aria-current="page"' : ""}>系统管理</a>` : ""}
          ${demoMode ? "" : `<a href="#/wiki"${wikiPage ? ' aria-current="page"' : ""}>运维知识库</a>`}
        </nav>`}
        <main id="portal-content" class="page-shell" tabindex="-1">
          ${demoMode ? '<p class="demo-notice">演示数据</p>' : ""}
          ${signedOut ? "" : `<header class="page-intro"><h1>${title}</h1></header>`}
          ${content}
        </main>
      </div>`;
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
    if (action === "clear-filters") window.location.hash = `#/findings?${serializeFindingFilters({ cluster: parseFindingFilters(window.location.hash).cluster })}`;
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
    const dimensions = ["environment", "business", "group"] as const;
    for (const [index, dimension] of dimensions.entries()) {
      if (target.dataset.filter !== dimension) continue;
      const available = this.clusters ?? [];
      const current = available.find((entry) => entry.id === filters.cluster) ?? available[0];
      const matching = available.find((entry) => (entry[dimension] || "未分组") === value &&
        dimensions.slice(0, index).every((key) => (entry[key] || "未分组") === (current?.[key] || "未分组")));
      if (matching) {
        filters.cluster = matching.id;
        filters.namespace = undefined;
        window.location.hash = `#/findings?${serializeFindingFilters(filters)}`;
      }
      return;
    }
    switch (target.dataset.filter) {
      case "cluster":
        filters.cluster = value;
        filters.namespace = undefined;
        filters.kind = undefined;
        this.knownKinds.clear();
        break;
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
      this.namespaces.clear();
      this.knownKinds.clear();
      this.showLogout = false;
      this.identityAdmin = false;
      this.mustChangePassword = false;
      ++this.renderGeneration;
      this.renderShell(authenticationRequiredState());
    } catch {
      button.textContent = "退出失败，请重试";
      button.disabled = false;
    }
  }

  private requestErrorState(error: unknown): string {
    if (error instanceof ApiError) {
      if (error.status === 401) {
        this.showLogout = false;
        return authenticationRequiredState();
      }
      if (error.status === 403) return authorizationDeniedState();
    }
    return errorState("无法获取诊断数据，请稍后重试。");
  }
}
