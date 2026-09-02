import type {
  Cluster,
  FindingPage,
  FindingSummary,
  Namespace,
} from "../../clients/typescript/generated.ts";
import { apiClient } from "./api/client.ts";
import { errorState, emptyState, loadingState } from "./components/states.ts";
import {
  parseFindingFilters,
  serializeFindingFilters,
  toListParams,
  toSummaryParams,
  type FindingFilters,
} from "./domain/filters.ts";
import { renderFindingDetail } from "./views/finding-detail.ts";
import {
  renderFilters,
  renderFindingTable,
  renderSummary,
} from "./views/finding-list.ts";

export class PortalApp {
  private readonly root: HTMLElement;
  private clusters: Cluster[] | undefined;
  private namespaces = new Map<string, Namespace[]>();
  private knownKinds = new Set<string>();
  private renderGeneration = 0;

  constructor(root: HTMLElement) {
    this.root = root;
  }

  start(): void {
    window.addEventListener("hashchange", () => void this.route());
    this.root.addEventListener("click", (event) => this.handleClick(event));
    this.root.addEventListener("change", (event) => this.handleFilterChange(event));
    if (!window.location.hash) window.location.hash = "#/findings";
    void this.route();
  }

  private async route(): Promise<void> {
    const generation = ++this.renderGeneration;
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
    this.renderShell(loadingState("Loading filter options and findings."));

    try {
      const [clusters, namespaces] = await Promise.all([
        this.loadClusters(),
        this.loadNamespaces(filters.cluster ?? "local"),
      ]);
      if (generation !== this.renderGeneration) return;

      const [page, summary] = await Promise.all([
        apiClient.listFindings(toListParams(filters)),
        apiClient.summarizeFindings(toSummaryParams(filters)),
      ]);
      if (generation !== this.renderGeneration) return;

      if (filters.kind) this.knownKinds.add(filters.kind);
      for (const kind of Object.keys(summary.byKind)) this.knownKinds.add(kind);
      this.renderFindingDomain(clusters, namespaces, filters, page, summary);
    } catch (error) {
      if (generation !== this.renderGeneration) return;
      this.renderShell(errorState(this.safeErrorMessage(error)));
    }
  }

  private async renderDetail(id: string, generation: number): Promise<void> {
    this.renderShell(loadingState("Loading finding detail."));
    try {
      const finding = await apiClient.getFinding({ id });
      if (generation !== this.renderGeneration) return;
      this.renderShell(renderFindingDetail(finding));
    } catch (error) {
      if (generation !== this.renderGeneration) return;
      this.renderShell(errorState(this.safeErrorMessage(error)));
    }
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
      ${page.items.length === 0 ? emptyState() : renderFindingTable(page.items)}
    `;
    this.renderShell(body);
  }

  private renderShell(content: string): void {
    this.root.innerHTML = `
      <header class="topbar">
        <a class="brand" href="#/findings" aria-label="kube-aiops findings home">
          <span class="brand-mark" aria-hidden="true">K</span>
          <span><strong>kube-aiops</strong><small>Read-only Finding Portal</small></span>
        </a>
        <span class="readonly-badge">READ ONLY</span>
      </header>
      <main class="page-shell">
        <section class="page-intro">
          <div>
            <p class="eyebrow">Phase 1.3 · Web Portal</p>
            <h1>Finding Operations Console</h1>
            <p>Explore normalized K8sGPT findings without exposing Pod logs, Secrets, raw Kubernetes objects, or mutation controls.</p>
          </div>
        </section>
        ${content}
      </main>`;
  }

  private handleClick(event: Event): void {
    const target = event.target;
    if (!(target instanceof HTMLElement)) return;
    const action = target.closest<HTMLElement>("[data-action]")?.dataset.action;
    if (action === "retry") void this.route();
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

  private safeErrorMessage(error: unknown): string {
    if (error instanceof Error) return error.message;
    return "The Portal Backend request failed.";
  }
}
