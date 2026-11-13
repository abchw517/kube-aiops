import {
  ApiError,
  type Cluster,
  type FindingPage,
  type FindingSummary,
  type Namespace,
} from "../../clients/typescript/generated.ts";
import { renderIdentityAdmin, submitIdentityAdmin } from "./views/identity-admin.ts";
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
} from "./views/correlation.ts";
import { renderFindingDetail } from "./views/finding-detail.ts";
import {
  renderFilters,
  renderFindingTable,
  renderSummary,
} from "./views/finding-list.ts";

const demoMode = document.querySelector('meta[name="kube-aiops-demo"][content="true"]') !== null;

export class PortalApp {
  private readonly root: HTMLElement;
  private clusters: Cluster[] | undefined;
  private namespaces = new Map<string, Namespace[]>();
  private knownKinds = new Set<string>();
  private renderGeneration = 0;
  private identityAdmin = false;
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
      if (form instanceof HTMLFormElement && form.dataset.identityResource) {
        event.preventDefault();
        void submitIdentityAdmin(form, () => {
          this.identityAdmin = false;
          this.mustChangePassword = false;
          window.location.hash = "#/findings";
          void this.route();
        });
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
    if (!demoMode) {
      try {
        const response = await fetch("/api/v1/admin/status");
        const status = response.ok ? await response.json() : {};
        if (generation !== this.renderGeneration) return;
        this.identityAdmin = status.admin === true;
        this.mustChangePassword = status.mustChangePassword === true;
      } catch {
        if (generation !== this.renderGeneration) return;
      }
      const adminRoute = window.location.hash.startsWith("#/admin");
      if (this.mustChangePassword || (this.identityAdmin && adminRoute)) {
        const resource = this.mustChangePassword ? "password" : window.location.hash.split("/")[2] ?? "ldap";
        const body = await renderIdentityAdmin(resource, this.mustChangePassword);
        if (generation === this.renderGeneration) this.renderShell(body);
        return;
      }
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
      const [clusters, namespaces] = await Promise.all([
        this.loadClusters(),
        this.loadNamespaces(filters.cluster ?? "local"),
      ]);
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
      const finding = await apiClient.getFinding({ id });
      if (generation !== this.renderGeneration) return;
      this.renderShell(renderFindingDetail(finding) + renderCorrelationSection(id));
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
      const bundle = await apiClient.getFindingCorrelation({ id });
      content = renderCorrelationEvidence(bundle);
    } catch (error) {
      content = renderCorrelationError(error);
    }
    if (generation !== this.renderGeneration || !region.isConnected) return;
    region.innerHTML = content;
    region.removeAttribute("aria-busy");
  }

  private async loadClusters(): Promise<Cluster[]> {
    if (this.clusters) return this.clusters;
    const response = await apiClient.listClusters();
    this.clusters = response.items;
    return response.items;
  }

  private async loadNamespaces(cluster: "local"): Promise<Namespace[]> {
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
    const title = adminPage ? "系统管理" : detailPage ? "诊断详情" : "诊断控制台";
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
          <a href="#/findings"${adminPage ? "" : ' aria-current="page"'}>诊断控制台</a>
          ${this.identityAdmin ? `<a href="#/admin/ldap"${adminPage ? ' aria-current="page"' : ""}>系统管理</a>` : ""}
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
    if (action === "login-provider") {
      const form = this.root.querySelector<HTMLFormElement>("[data-login]");
      const provider = form?.querySelector<HTMLInputElement>('[name="provider"]');
      const password = form?.querySelector<HTMLInputElement>('[name="password"]');
      const submit = form?.querySelector<HTMLButtonElement>('button[type="submit"]');
      if (provider && password && submit) {
        provider.value = provider.value === "local" ? "ldap" : "local";
        password.value = "";
        submit.textContent = provider.value === "ldap" ? "LDAP 登录" : "登录";
        target.textContent = provider.value === "ldap" ? "返回本地账号登录" : "LDAP / Active Directory 登录";
      }
    }
    if (action === "logout") void this.logout(target.closest<HTMLButtonElement>("button"));
    if (action === "retry") void this.route();
    if (action === "retry-correlation") {
      const region = target.closest<HTMLElement>("[data-correlation-finding]");
      const id = region?.dataset.correlationFinding;
      if (region && id) void this.loadCorrelation(id, this.renderGeneration, region);
    }
    if (action === "clear-filters") window.location.hash = "#/findings?cluster=local";
  }

  private handleFilterChange(event: Event): void {
    const target = event.target;
    if (!(target instanceof HTMLSelectElement) || !target.dataset.filter) return;

    const filters = parseFindingFilters(window.location.hash);
    const value = target.value || undefined;
    switch (target.dataset.filter) {
      case "cluster":
        filters.cluster = "local";
        filters.namespace = undefined;
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
          provider: fields.get("provider"),
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
