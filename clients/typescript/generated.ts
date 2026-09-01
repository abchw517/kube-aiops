// Code generated from api/openapi.yaml by tools/openapi/contract.py. DO NOT EDIT.
// OpenAPI SHA256: 783df1f4c7c0d3fbbeff667af735c32296e640b017a903ba402d26ca35e68049

export interface StatusResponse {
  "status": string;
}

export interface Cluster {
  "id": string;
  "name": string;
  "status": string;
}

export interface ClusterList {
  "items": Array<Cluster>;
}

export interface Namespace {
  "name": string;
}

export interface NamespaceList {
  "items": Array<Namespace>;
}

export interface ResourceStatus {
  "phase"?: string;
  "replicas"?: number;
  "readyReplicas"?: number;
  "availableReplicas"?: number;
}

export interface ResourceDetail {
  "apiVersion": string;
  "kind": string;
  "namespace": string;
  "name": string;
  "createdAt"?: string;
  "status": ResourceStatus;
}

export type Severity = "critical" | "warning" | "info";

export interface ResourceRef {
  "apiVersion"?: string;
  "kind"?: string;
  "namespace"?: string;
  "name"?: string;
}

export interface Finding {
  "id": string;
  "cluster": "local";
  "namespace"?: string;
  "severity": Severity;
  "resource": ResourceRef;
  "problem": string;
  "details"?: string;
  "source": "k8sgpt";
  "createdAt"?: string;
}

export interface Pagination {
  "limit": number;
  "continue"?: string;
}

export interface FindingPage {
  "items": Array<Finding>;
  "pagination": Pagination;
}

export interface FindingSummary {
  "total": number;
  "bySeverity": Record<string, number>;
  "byKind": Record<string, number>;
  "byNamespace": Record<string, number>;
}

export interface ErrorDetail {
  "code": string;
  "message": string;
}

export interface ErrorResponse {
  "error": ErrorDetail;
}

export interface ListNamespacesParams {
  "cluster": "local";
}

export interface GetResourceParams {
  "cluster": "local";
  "kind": "pod" | "pods" | "deployment" | "deployments";
  "namespace": string;
  "name": string;
}

export interface ListFindingsParams {
  "cluster"?: "local";
  "namespace"?: string;
  "kind"?: string;
  "severity"?: Severity;
  "problem"?: string;
  "limit"?: number;
  "continue"?: string;
}

export interface SummarizeFindingsParams {
  "cluster"?: "local";
  "namespace"?: string;
  "kind"?: string;
  "severity"?: Severity;
  "problem"?: string;
}

export interface GetFindingParams {
  "id": string;
}

export interface ApiClientOptions {
  baseUrl?: string;
  fetch?: typeof fetch;
}

export class ApiError extends Error {
  constructor(public readonly status: number, public readonly body: unknown) {
    super(`kube-aiops API request failed with HTTP ${status}`);
    this.name = "ApiError";
  }
}

export class KubeAIOpsApiClient {
  private readonly baseUrl: string;
  private readonly fetchImpl: typeof fetch;

  constructor(options: ApiClientOptions = {}) {
    this.baseUrl = (options.baseUrl ?? "").replace(/\/$/, "");
    this.fetchImpl = options.fetch ?? fetch;
  }

  private async request<T>(path: string): Promise<T> {
    const response = await this.fetchImpl(`${this.baseUrl}${path}`, {
      method: "GET",
      headers: { Accept: "application/json" },
    });
    const body = await response.json().catch(() => undefined);
    if (!response.ok) throw new ApiError(response.status, body);
    return body as T;
  }

  async getHealth(): Promise<StatusResponse> {
    let path = `/healthz`;
    return this.request<StatusResponse>(path);
  }

  async getReadiness(): Promise<StatusResponse> {
    let path = `/readyz`;
    return this.request<StatusResponse>(path);
  }

  async listClusters(): Promise<ClusterList> {
    let path = `/api/v1/clusters`;
    return this.request<ClusterList>(path);
  }

  async listNamespaces(params: ListNamespacesParams): Promise<NamespaceList> {
    let path = `/api/v1/clusters/${encodeURIComponent(String(params["cluster"]))}/namespaces`;
    return this.request<NamespaceList>(path);
  }

  async getResource(params: GetResourceParams): Promise<ResourceDetail> {
    let path = `/api/v1/clusters/${encodeURIComponent(String(params["cluster"]))}/resources/${encodeURIComponent(String(params["kind"]))}/${encodeURIComponent(String(params["namespace"]))}/${encodeURIComponent(String(params["name"]))}`;
    return this.request<ResourceDetail>(path);
  }

  async listFindings(params: ListFindingsParams = {}): Promise<FindingPage> {
    let path = `/api/v1/findings`;
    const query = new URLSearchParams();
    if (params["cluster"] !== undefined) query.set("cluster", String(params["cluster"]));
    if (params["namespace"] !== undefined) query.set("namespace", String(params["namespace"]));
    if (params["kind"] !== undefined) query.set("kind", String(params["kind"]));
    if (params["severity"] !== undefined) query.set("severity", String(params["severity"]));
    if (params["problem"] !== undefined) query.set("problem", String(params["problem"]));
    if (params["limit"] !== undefined) query.set("limit", String(params["limit"]));
    if (params["continue"] !== undefined) query.set("continue", String(params["continue"]));
    const queryString = query.toString();
    if (queryString) path += `?${queryString}`;
    return this.request<FindingPage>(path);
  }

  async summarizeFindings(params: SummarizeFindingsParams = {}): Promise<FindingSummary> {
    let path = `/api/v1/findings/summary`;
    const query = new URLSearchParams();
    if (params["cluster"] !== undefined) query.set("cluster", String(params["cluster"]));
    if (params["namespace"] !== undefined) query.set("namespace", String(params["namespace"]));
    if (params["kind"] !== undefined) query.set("kind", String(params["kind"]));
    if (params["severity"] !== undefined) query.set("severity", String(params["severity"]));
    if (params["problem"] !== undefined) query.set("problem", String(params["problem"]));
    const queryString = query.toString();
    if (queryString) path += `?${queryString}`;
    return this.request<FindingSummary>(path);
  }

  async getFinding(params: GetFindingParams): Promise<Finding> {
    let path = `/api/v1/findings/${encodeURIComponent(String(params["id"]))}`;
    return this.request<Finding>(path);
  }

}
