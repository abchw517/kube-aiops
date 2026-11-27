// Code generated from api/openapi.yaml by tools/openapi/contract.py. DO NOT EDIT.
// Contract source SHA-256 groups: 4ce29a03 a726fc19 116f53dd 1e644062 93ebb59c 56ef4d89 f35d4a25 8f86bfb8

export interface ObservabilityScope {
  "environmentId": string;
  "businessId": string;
  "clusterId": string;
}

export interface ObservabilityCredential {
  "method": "none" | "bearer" | "basic";
  "username"?: string;
  "secret"?: string;
}

export interface ObservabilityTenant {
  "account": number;
  "project": number;
}

export interface ObservabilityDashboard {
  "uid": string;
  "title": string;
  "url"?: string;
}

export interface HealthInvestigation {
  "analysis": string;
  "appliedSkills"?: Array<string>;
}

export interface ObservabilityAlert {
  "sourceId"?: string;
  "namespace"?: string;
  "id": string;
  "title": string;
  "state": "active" | "suppressed" | "unprocessed" | "departed";
  "severity": "critical" | "warning" | "info" | "";
  "rawSeverity"?: string;
  "startedAt": string;
  "scope": ObservabilityScope;
}

export interface ObservabilitySource {
  "coverageFrom"?: number;
  "id": string;
  "kind": "prometheus" | "victorialogs" | "alertmanager" | "grafana";
  "name": string;
  "endpoint": string;
  "scope": ObservabilityScope;
  "shared": boolean;
  "label"?: string;
  "value"?: string;
  "tenant"?: ObservabilityTenant;
  "includeSuppressed"?: boolean;
  "dashboards"?: Array<ObservabilityDashboard>;
  "auth": "none" | "bearer" | "basic";
  "enabled": boolean;
  "version": number;
  "checkedAt"?: number;
  "syncedAt"?: number;
  "failed"?: boolean;
}

export interface ObservabilityInput {
  "kind": "prometheus" | "victorialogs" | "alertmanager" | "grafana";
  "name": string;
  "endpoint": string;
  "scope": ObservabilityScope;
  "shared": boolean;
  "label"?: string;
  "value"?: string;
  "tenant"?: ObservabilityTenant;
  "includeSuppressed"?: boolean;
  "dashboardIds"?: Array<string>;
  "credential"?: ObservabilityCredential;
  "sourceId"?: string;
  "preview"?: boolean;
}

export interface ObservabilityPreview {
  "count": number;
  "suppressed"?: number;
  "dashboards"?: Array<ObservabilityDashboard>;
  "alerts"?: Array<ObservabilityAlert>;
}

export interface ObservabilitySources {
  "items": Array<ObservabilitySource>;
  "legacy": Array<"prometheus" | "victorialogs" | "alertmanager" | "grafana">;
}

export interface ObservabilityAlerts {
  "items": Array<ObservabilityAlert>;
  "sources": Array<ObservabilitySource>;
  "truncated": boolean;
}

export interface ObservabilityDashboards {
  "items": Array<ObservabilityDashboard>;
}

export interface ObservabilityToggle {
  "enabled": boolean;
}

export interface ObservabilityQueued {
  "queued": boolean;
}

export interface ZabbixScope {
  "environmentId": string;
  "businessId": string;
}

export interface ZabbixOption {
  "id": string;
  "name": string;
}

export interface ZabbixScopeChoice {
  "id": string;
  "name": string;
  "businesses": Array<ZabbixOption>;
}

export interface ZabbixScopes {
  "environments": Array<ZabbixScopeChoice>;
}

export interface ZabbixGroup {
  "groupid": string;
  "name": string;
}

export interface ZabbixHost {
  "clusterId"?: string;
  "hostid": string;
  "name": string;
}

export interface ZabbixTag {
  "tag": string;
  "value": string;
}

export interface ZabbixCredential {
  "method": "token" | "password";
  "username"?: string;
  "secret": string;
}

export interface ZabbixSource {
  "name"?: string;
  "environmentName"?: string;
  "businessName"?: string;
  "id": string;
  "endpoint": string;
  "scope": ZabbixScope;
  "groups": Array<ZabbixGroup>;
  "authMethod": string;
  "upstreamVersion": string;
  "enabled": boolean;
  "version": number;
  "health": string;
  "checkedAt"?: string;
  "syncedAt"?: string;
  "hostCount"?: number;
  "problemCount"?: number;
}

export interface ZabbixSources {
  "items": Array<ZabbixSource>;
}

export interface ZabbixTestInput {
  "endpoint"?: string;
  "credential"?: ZabbixCredential;
  "sourceId"?: string;
}

export interface ZabbixTestResult {
  "version": string;
  "endpoint": string;
  "groups": Array<ZabbixGroup>;
  "draft": string;
}

export interface ZabbixSelection {
  "name"?: string;
  "draft"?: string;
  "sourceId"?: string;
  "scope": ZabbixScope;
  "groupIds": Array<string>;
}

export interface ZabbixChange {
  "name"?: string;
  "draft"?: string;
  "groupIds"?: Array<string>;
  "enabled"?: boolean;
}

export interface ZabbixQueued {
  "queued": boolean;
}

export interface ZabbixProblem {
  "originalURL"?: string;
  "id": string;
  "eventId": string;
  "title": string;
  "severity": Severity;
  "rawSeverity": number;
  "scope": ZabbixScope;
  "hosts": Array<ZabbixHost>;
  "acknowledged": boolean;
  "recovered": boolean;
  "startedAt": string;
  "recoveredAt"?: string;
  "tags"?: Array<ZabbixTag>;
  "sourceId": string;
}

export interface ZabbixHealth {
  "environmentName"?: string;
  "businessName"?: string;
  "id": string;
  "scope": ZabbixScope;
  "health": string;
  "syncedAt"?: string;
  "hasSnapshot": boolean;
}

export interface ZabbixPreview {
  "hostCount": number;
  "problemCount": number;
  "problems": Array<ZabbixProblem>;
}

export interface ZabbixProblems {
  "items": Array<ZabbixProblem>;
  "sources": Array<ZabbixHealth>;
  "truncated": boolean;
}

export interface RCAInput {
  "observation"?: string;
  "evidenceIds"?: Array<string>;
}

export interface RCAConclusion {
  "state": string;
  "judgment": string;
  "references": Array<string>;
  "gaps": string;
  "next": string;
}

export interface RCARevision {
  "version": number;
  "actor": string;
  "at": string;
  "conclusion": RCAConclusion;
}

export interface RCATask {
  "id": string;
  "incidentId": string;
  "state": "queued" | "running" | "succeeded" | "failed" | "timed_out" | "canceled";
  "observation"?: string;
  "evidence": Array<IncidentEvidence>;
  "actor": string;
  "version": number;
  "createdAt": string;
  "startedAt"?: string;
  "finishedAt"?: string;
  "errorCode"?: string;
  "revisions": Array<RCARevision>;
  "policyId"?: string;
  "policyVersion"?: number;
}

export interface RCATaskPage {
  "items": Array<RCATask>;
  "pagination": IncidentPagination;
}

export interface TopologyNode {
  "id": string;
  "name": string;
  "kind": string;
  "scope": IncidentScope;
  "resourceId": string;
  "incidentId"?: string;
  "state"?: string;
  "platform": string;
}

export interface TopologyLink {
  "source": string;
  "target": string;
  "relation": string;
  "confidence": string;
  "reason": string;
  "actor": string;
  "recordedAt": string;
  "evidenceId"?: string;
}

export interface IncidentTopology {
  "root": string;
  "nodes": Array<TopologyNode>;
  "edges": Array<TopologyLink>;
  "coverage": string;
  "truncated": boolean;
}

export interface TopologyInput {
  "targetIncidentId"?: string;
  "targetAssetId"?: string;
  "relation": string;
  "reason": string;
  "confidence"?: string;
  "evidenceId"?: string;
  "remove"?: boolean;
}

export interface DatasourceConfig {
  "kind": "zabbix" | "aliyun-ecs" | "tencent-cvm";
  "endpoint"?: string;
  "region"?: string;
  "assetIds": Array<string>;
}

export interface DatasourceInput {
  "name": string;
  "scope": CreateIncidentScope;
  "config": DatasourceConfig;
  "enabled": boolean;
  "credentialRef"?: string;
  "ingressRef"?: string;
}

export interface DiagnosticDatasource {
  "id": string;
  "name": string;
  "scope": IncidentScope;
  "config": DatasourceConfig;
  "enabled": boolean;
  "version": number;
  "health": string;
  "message"?: string;
  "checkedAt"?: string;
  "syncedAt"?: string;
  "credentialConfigured": boolean;
  "ingressConfigured": boolean;
}

export interface DatasourcePage {
  "items": Array<DiagnosticDatasource>;
  "pagination": IncidentPagination;
}

export interface DiagnosticAsset {
  "id": string;
  "datasourceId": string;
  "externalId": string;
  "name": string;
  "kind": string;
  "state": string;
  "region"?: string;
  "observedAt": string;
}

export interface DiagnosticAssets {
  "items": Array<DiagnosticAsset>;
}

export interface IngressInput {
  "incidentId": string;
  "assetId": string;
  "externalId": string;
  "state": string;
  "observedAt": string;
  "summary": string;
}

export interface PolicyInput {
  "name": string;
  "scope": CreateIncidentScope;
  "enabled": boolean;
  "severity"?: string;
  "source": string;
  "template": string;
  "cooldownSeconds": number;
  "maxConcurrent": number;
}

export interface DiagnosticPolicy {
  "name": string;
  "scope": IncidentScope;
  "enabled": boolean;
  "severity"?: string;
  "source": string;
  "template": string;
  "cooldownSeconds": number;
  "maxConcurrent": number;
  "id": string;
  "version": number;
  "effectiveAt": string;
  "automationAllowed": boolean;
}

export interface PolicyPage {
  "items": Array<DiagnosticPolicy>;
  "pagination": IncidentPagination;
}

export interface PolicyExecution {
  "incidentId": string;
  "runId": string;
  "version": number;
  "state": string;
  "queuedAt": string;
}

export interface PolicyExecutions {
  "items": Array<PolicyExecution>;
}

export interface IncidentScope {
  "environmentId": string;
  "businessId"?: string;
  "clusterId": string;
  "namespace": string;
}

export interface CreateIncidentScope {
  "environmentId"?: string;
  "businessId"?: string;
  "clusterId": string;
  "namespace": string;
}

export type IncidentStatus = "pending" | "investigating" | "recovered" | "closed";

export type IncidentSeverity = "P1" | "P2" | "P3";

export interface Incident {
  "id": string;
  "displayKey": string;
  "scope": IncidentScope;
  "title": string;
  "description"?: string;
  "resource": ResourceRef;
  "resourceId": string;
  "findingId"?: string;
  "severity": IncidentSeverity;
  "status": IncidentStatus;
  "ownerGroupId"?: string;
  "source": string;
  "version": number;
  "firstSeen": string;
  "lastSeen": string;
  "createdAt": string;
  "updatedAt": string;
  "mergedInto"?: string;
  "sources"?: Array<SourceStatus>;
}

export interface CreateIncidentInput {
  "title": string;
  "description"?: string;
  "scope": CreateIncidentScope;
  "severity": IncidentSeverity;
  "findingId"?: string;
  "resource"?: ResourceRef;
  "resourceId"?: string;
  "assetId"?: string;
}

export interface UpdateIncidentInput {
  "status"?: IncidentStatus;
  "severity"?: IncidentSeverity;
  "ownerGroupId"?: string;
  "reason": string;
}

export interface IncidentActions {
  "update": boolean;
  "correlate": boolean;
  "split": boolean;
  "evidence": boolean;
  "rca"?: Record<string, boolean>;
  "topology"?: boolean;
  "topologyWrite"?: boolean;
}

export interface IncidentDetail {
  "incident": Incident;
  "actions": IncidentActions;
  "version": number;
  "counts"?: Record<string, number>;
  "coverage": string;
  "candidates"?: Array<Incident>;
}

export interface IncidentTimelineEntry {
  "id": number;
  "at": string;
  "actor": string;
  "kind": string;
  "reason"?: string;
  "before"?: string;
  "after"?: string;
}

export interface IncidentEvidence {
  "id": string;
  "source": string;
  "kind": string;
  "observedAt": string;
  "collectedAt": string;
  "state": string;
  "summary"?: string;
}

export interface IncidentEvidenceDetail {
  "id": string;
  "source": string;
  "kind": string;
  "observedAt": string;
  "collectedAt": string;
  "state": string;
  "summary"?: string;
  "signal": Record<string, unknown>;
}

export interface IncidentPagination {
  "continue"?: string;
}

export interface IncidentList {
  "items": Array<Incident>;
  "pagination": IncidentPagination;
  "coverage": string;
}

export interface IncidentTimelinePage {
  "items": Array<IncidentTimelineEntry>;
  "pagination": IncidentPagination;
}

export interface IncidentEvidencePage {
  "items": Array<IncidentEvidence>;
  "pagination": IncidentPagination;
}

export interface IncidentOverview {
  "counts": Record<string, number>;
  "trend": Record<string, number>;
  "byPlatform": Record<string, number>;
  "coverage": string;
}

export interface MergeIncidentInput {
  "sourceId": string;
  "sourceVersion": number;
  "reason": string;
}

export interface SplitIncidentInput {
  "evidenceIds": Array<string>;
  "title": string;
  "reason": string;
}

export interface StatusResponse {
  "status": string;
}

export interface Principal {
  "subject": string;
  "provider": string;
  "displayName"?: string;
  "groups"?: Array<string>;
}

export interface Cluster {
  "lastReportAt"?: number;
  "reviewEnabled"?: boolean;
  "business"?: string;
  "group"?: string;
  "environment"?: string;
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
  "resultUid"?: string;
  "id": string;
  "cluster": string;
  "namespace"?: string;
  "severity": Severity;
  "resource": ResourceRef;
  "problem": string;
  "reviewRevision"?: number;
  "originalDetails"?: string;
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

export type CorrelationSource = "kubernetes-events" | "prometheus" | "victorialogs" | "alertmanager";

export type SourceState = "available" | "unavailable" | "disabled" | "partial";

export type SignalType = "event" | "metric" | "log" | "alert";

export type SignalUnit = "count" | "bytes" | "seconds" | "milliseconds" | "percent" | "ratio" | "cores" | "bytes_per_second" | "requests_per_second";

export interface TimeWindow {
  "start": string;
  "end": string;
}

export interface CorrelationScope {
  "cluster": string;
  "namespace"?: string;
  "resource": ResourceRef;
}

export interface EvidenceRef {
  "source": CorrelationSource;
  "id": string;
}

export interface CorrelationBudget {
  "windowSeconds": number;
  "perSourceTimeoutMillis": number;
  "maxSignalsPerSource": number;
}

export interface SourceStatus {
  "source": CorrelationSource;
  "state": SourceState;
}

export interface CorrelationSignal {
  "id": string;
  "source": CorrelationSource;
  "type": SignalType;
  "severity"?: Severity;
  "resource"?: ResourceRef;
  "firstSeen"?: string;
  "lastSeen"?: string;
  "count"?: number;
  "name"?: string;
  "value"?: number;
  "unit"?: SignalUnit;
  "fingerprint"?: string;
  "summary"?: string;
  "evidence"?: Array<EvidenceRef>;
}

export interface CorrelationBundle {
  "findingId": string;
  "scope": CorrelationScope;
  "window": TimeWindow;
  "budget": CorrelationBudget;
  "sources": Array<SourceStatus>;
  "signals": Array<CorrelationSignal>;
}

export interface ErrorDetail {
  "code": string;
  "message": string;
}

export interface ErrorResponse {
  "error": ErrorDetail;
}

export interface ListNamespacesParams {
  "cluster": string;
}

export interface GetResourceParams {
  "cluster": string;
  "kind": "pod" | "pods" | "deployment" | "deployments";
  "namespace": string;
  "name": string;
}

export interface ListFindingsParams {
  "cluster"?: string;
  "environment"?: string;
  "business"?: string;
  "group"?: string;
  "namespace"?: string;
  "kind"?: string;
  "severity"?: Severity;
  "problem"?: string;
  "limit"?: number;
  "continue"?: string;
}

export interface SummarizeFindingsParams {
  "cluster"?: string;
  "environment"?: string;
  "business"?: string;
  "group"?: string;
  "namespace"?: string;
  "kind"?: string;
  "severity"?: Severity;
  "problem"?: string;
}

export interface GetFindingParams {
  "id": string;
  "cluster"?: string;
}

export interface GetFindingCorrelationParams {
  "id": string;
  "cluster"?: string;
}

export interface GetIncidentOverviewParams {
  "environmentId"?: string;
  "businessId"?: string;
  "clusterId"?: string;
  "platform"?: string;
  "namespace"?: string;
  "severity"?: IncidentSeverity;
  "status"?: IncidentStatus;
  "source"?: string;
  "q"?: string;
  "from"?: string;
  "to"?: string;
}

export interface ListIncidentsParams {
  "environmentId"?: string;
  "businessId"?: string;
  "clusterId"?: string;
  "platform"?: string;
  "namespace"?: string;
  "severity"?: IncidentSeverity;
  "status"?: IncidentStatus;
  "source"?: string;
  "q"?: string;
  "from"?: string;
  "to"?: string;
  "limit"?: number;
  "continue"?: string;
}

export interface CreateIncidentParams {
  "Idempotency-Key": string;
  "body": CreateIncidentInput;
}

export interface GetIncidentParams {
  "id": string;
}

export interface UpdateIncidentParams {
  "id": string;
  "If-Match": string;
  "body": UpdateIncidentInput;
}

export interface ListIncidentTimelineParams {
  "id": string;
  "limit"?: number;
  "continue"?: string;
}

export interface ListIncidentEvidenceParams {
  "id": string;
  "limit"?: number;
  "continue"?: string;
}

export interface GetIncidentEvidenceParams {
  "id": string;
  "evidenceId": string;
}

export interface CollectIncidentEvidenceParams {
  "id": string;
  "If-Match": string;
}

export interface MergeIncidentsParams {
  "id": string;
  "If-Match": string;
  "body": MergeIncidentInput;
}

export interface SplitIncidentParams {
  "id": string;
  "If-Match": string;
  "body": SplitIncidentInput;
}

export interface ListRcaRunsParams {
  "id": string;
  "limit"?: number;
  "continue"?: string;
}

export interface StartRcaRunParams {
  "id": string;
  "Idempotency-Key": string;
  "body": RCAInput;
}

export interface GetRcaRunParams {
  "id": string;
  "runId": string;
}

export interface CancelRcaRunParams {
  "id": string;
  "runId": string;
}

export interface SaveRcaConclusionParams {
  "id": string;
  "runId": string;
  "If-Match": string;
  "body": RCAConclusion;
}

export interface GetIncidentTopologyParams {
  "id": string;
  "depth"?: number;
}

export interface SaveTopologyLinkParams {
  "id": string;
  "body": TopologyInput;
}

export interface ListDiagnosticDatasourcesParams {
  "limit"?: number;
  "continue"?: string;
}

export interface CreateDiagnosticDatasourceParams {
  "body": DatasourceInput;
}

export interface DeleteDiagnosticDatasourceParams {
  "datasourceId": string;
  "If-Match": string;
}

export interface UpdateDiagnosticDatasourceParams {
  "datasourceId": string;
  "If-Match": string;
  "body": DatasourceInput;
}

export interface TestDiagnosticDatasourceParams {
  "datasourceId": string;
  "If-Match": string;
}

export interface ListDiagnosticAssetsParams {
  "datasourceId": string;
}

export interface ListDiagnosticPoliciesParams {
  "limit"?: number;
  "continue"?: string;
}

export interface CreateDiagnosticPolicyParams {
  "body": PolicyInput;
}

export interface UpdateDiagnosticPolicyParams {
  "policyId": string;
  "If-Match": string;
  "body": PolicyInput;
}

export interface ListDiagnosticPolicyRunsParams {
  "policyId": string;
}

export interface CreateZabbixSourceParams {
  "body": ZabbixSelection;
}

export interface DeleteZabbixSourceParams {
  "sourceId": string;
  "If-Match": string;
}

export interface UpdateZabbixSourceParams {
  "sourceId": string;
  "If-Match": string;
  "body": ZabbixChange;
}

export interface SyncZabbixSourceParams {
  "sourceId": string;
}

export interface TestZabbixConnectionParams {
  "body": ZabbixTestInput;
}

export interface PreviewZabbixProblemsParams {
  "body": ZabbixSelection;
}

export interface ListZabbixProblemsParams {
  "environmentId"?: string;
  "businessId"?: string;
  "recent"?: boolean;
  "metadataOnly"?: boolean;
}

export interface CreateObservabilitySourceParams {
  "body": ObservabilityInput;
}

export interface ProbeObservabilitySourceParams {
  "body": ObservabilityInput;
}

export interface DeleteObservabilitySourceParams {
  "sourceId": string;
  "If-Match": string;
}

export interface UpdateObservabilitySourceParams {
  "sourceId": string;
  "If-Match": string;
  "body": ObservabilityInput;
}

export interface ToggleObservabilitySourceParams {
  "sourceId": string;
  "If-Match": string;
  "body": ObservabilityToggle;
}

export interface SyncObservabilitySourceParams {
  "sourceId": string;
}

export interface ListObservabilityAlertsParams {
  "environmentId"?: string;
  "businessId"?: string;
  "clusterId"?: string;
  "recent"?: string;
}

export interface InvestigateHealthAlertParams {
  "source": string;
  "sourceId": string;
  "id": string;
}

export interface ListObservabilityDashboardsParams {
  "clusterId": string;
  "namespace": string;
  "businessId"?: string;
  "anchor"?: string;
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

  private async request<T>(path: string, init?: RequestInit): Promise<T> {
    const headers = new Headers(init?.headers);
    headers.set("Accept", "application/json");
    const response = await this.fetchImpl(`${this.baseUrl}${path}`, {
      ...init,
      headers,
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
    if (params["environment"] !== undefined) query.set("environment", String(params["environment"]));
    if (params["business"] !== undefined) query.set("business", String(params["business"]));
    if (params["group"] !== undefined) query.set("group", String(params["group"]));
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
    if (params["environment"] !== undefined) query.set("environment", String(params["environment"]));
    if (params["business"] !== undefined) query.set("business", String(params["business"]));
    if (params["group"] !== undefined) query.set("group", String(params["group"]));
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
    const query = new URLSearchParams();
    if (params["cluster"] !== undefined) query.set("cluster", String(params["cluster"]));
    const queryString = query.toString();
    if (queryString) path += `?${queryString}`;
    return this.request<Finding>(path);
  }

  async getFindingCorrelation(params: GetFindingCorrelationParams): Promise<CorrelationBundle> {
    let path = `/api/v1/findings/${encodeURIComponent(String(params["id"]))}/correlation`;
    const query = new URLSearchParams();
    if (params["cluster"] !== undefined) query.set("cluster", String(params["cluster"]));
    const queryString = query.toString();
    if (queryString) path += `?${queryString}`;
    return this.request<CorrelationBundle>(path);
  }

  async getIncidentOverview(params: GetIncidentOverviewParams = {}): Promise<IncidentOverview> {
    let path = `/api/v1/diagnostics/overview`;
    const query = new URLSearchParams();
    if (params["environmentId"] !== undefined) query.set("environmentId", String(params["environmentId"]));
    if (params["businessId"] !== undefined) query.set("businessId", String(params["businessId"]));
    if (params["clusterId"] !== undefined) query.set("clusterId", String(params["clusterId"]));
    if (params["platform"] !== undefined) query.set("platform", String(params["platform"]));
    if (params["namespace"] !== undefined) query.set("namespace", String(params["namespace"]));
    if (params["severity"] !== undefined) query.set("severity", String(params["severity"]));
    if (params["status"] !== undefined) query.set("status", String(params["status"]));
    if (params["source"] !== undefined) query.set("source", String(params["source"]));
    if (params["q"] !== undefined) query.set("q", String(params["q"]));
    if (params["from"] !== undefined) query.set("from", String(params["from"]));
    if (params["to"] !== undefined) query.set("to", String(params["to"]));
    const queryString = query.toString();
    if (queryString) path += `?${queryString}`;
    return this.request<IncidentOverview>(path);
  }

  async listIncidents(params: ListIncidentsParams = {}): Promise<IncidentList> {
    let path = `/api/v1/diagnostics/incidents`;
    const query = new URLSearchParams();
    if (params["environmentId"] !== undefined) query.set("environmentId", String(params["environmentId"]));
    if (params["businessId"] !== undefined) query.set("businessId", String(params["businessId"]));
    if (params["clusterId"] !== undefined) query.set("clusterId", String(params["clusterId"]));
    if (params["platform"] !== undefined) query.set("platform", String(params["platform"]));
    if (params["namespace"] !== undefined) query.set("namespace", String(params["namespace"]));
    if (params["severity"] !== undefined) query.set("severity", String(params["severity"]));
    if (params["status"] !== undefined) query.set("status", String(params["status"]));
    if (params["source"] !== undefined) query.set("source", String(params["source"]));
    if (params["q"] !== undefined) query.set("q", String(params["q"]));
    if (params["from"] !== undefined) query.set("from", String(params["from"]));
    if (params["to"] !== undefined) query.set("to", String(params["to"]));
    if (params["limit"] !== undefined) query.set("limit", String(params["limit"]));
    if (params["continue"] !== undefined) query.set("continue", String(params["continue"]));
    const queryString = query.toString();
    if (queryString) path += `?${queryString}`;
    return this.request<IncidentList>(path);
  }

  async createIncident(params: CreateIncidentParams): Promise<Incident> {
    let path = `/api/v1/diagnostics/incidents`;
    const headers: Record<string, string> = {};
    headers["Idempotency-Key"] = String(params["Idempotency-Key"]);
    headers["Content-Type"] = "application/json";
    return this.request<Incident>(path, { method: "POST", headers, body: JSON.stringify(params.body) });
  }

  async getIncident(params: GetIncidentParams): Promise<IncidentDetail> {
    let path = `/api/v1/diagnostics/incidents/${encodeURIComponent(String(params["id"]))}`;
    return this.request<IncidentDetail>(path);
  }

  async updateIncident(params: UpdateIncidentParams): Promise<Incident> {
    let path = `/api/v1/diagnostics/incidents/${encodeURIComponent(String(params["id"]))}`;
    const headers: Record<string, string> = {};
    headers["If-Match"] = String(params["If-Match"]);
    headers["Content-Type"] = "application/json";
    return this.request<Incident>(path, { method: "PATCH", headers, body: JSON.stringify(params.body) });
  }

  async listIncidentTimeline(params: ListIncidentTimelineParams): Promise<IncidentTimelinePage> {
    let path = `/api/v1/diagnostics/incidents/${encodeURIComponent(String(params["id"]))}/timeline`;
    const query = new URLSearchParams();
    if (params["limit"] !== undefined) query.set("limit", String(params["limit"]));
    if (params["continue"] !== undefined) query.set("continue", String(params["continue"]));
    const queryString = query.toString();
    if (queryString) path += `?${queryString}`;
    return this.request<IncidentTimelinePage>(path);
  }

  async listIncidentEvidence(params: ListIncidentEvidenceParams): Promise<IncidentEvidencePage> {
    let path = `/api/v1/diagnostics/incidents/${encodeURIComponent(String(params["id"]))}/evidence`;
    const query = new URLSearchParams();
    if (params["limit"] !== undefined) query.set("limit", String(params["limit"]));
    if (params["continue"] !== undefined) query.set("continue", String(params["continue"]));
    const queryString = query.toString();
    if (queryString) path += `?${queryString}`;
    return this.request<IncidentEvidencePage>(path);
  }

  async getIncidentEvidence(params: GetIncidentEvidenceParams): Promise<IncidentEvidenceDetail> {
    let path = `/api/v1/diagnostics/incidents/${encodeURIComponent(String(params["id"]))}/evidence/${encodeURIComponent(String(params["evidenceId"]))}`;
    return this.request<IncidentEvidenceDetail>(path);
  }

  async collectIncidentEvidence(params: CollectIncidentEvidenceParams): Promise<Incident> {
    let path = `/api/v1/diagnostics/incidents/${encodeURIComponent(String(params["id"]))}/collect`;
    const headers: Record<string, string> = {};
    headers["If-Match"] = String(params["If-Match"]);
    return this.request<Incident>(path, { method: "POST", headers });
  }

  async mergeIncidents(params: MergeIncidentsParams): Promise<Incident> {
    let path = `/api/v1/diagnostics/incidents/${encodeURIComponent(String(params["id"]))}/merge`;
    const headers: Record<string, string> = {};
    headers["If-Match"] = String(params["If-Match"]);
    headers["Content-Type"] = "application/json";
    return this.request<Incident>(path, { method: "POST", headers, body: JSON.stringify(params.body) });
  }

  async splitIncident(params: SplitIncidentParams): Promise<Incident> {
    let path = `/api/v1/diagnostics/incidents/${encodeURIComponent(String(params["id"]))}/split`;
    const headers: Record<string, string> = {};
    headers["If-Match"] = String(params["If-Match"]);
    headers["Content-Type"] = "application/json";
    return this.request<Incident>(path, { method: "POST", headers, body: JSON.stringify(params.body) });
  }

  async listRcaRuns(params: ListRcaRunsParams): Promise<RCATaskPage> {
    let path = `/api/v1/diagnostics/incidents/${encodeURIComponent(String(params["id"]))}/rca-runs`;
    const query = new URLSearchParams();
    if (params["limit"] !== undefined) query.set("limit", String(params["limit"]));
    if (params["continue"] !== undefined) query.set("continue", String(params["continue"]));
    const queryString = query.toString();
    if (queryString) path += `?${queryString}`;
    return this.request<RCATaskPage>(path);
  }

  async startRcaRun(params: StartRcaRunParams): Promise<RCATask> {
    let path = `/api/v1/diagnostics/incidents/${encodeURIComponent(String(params["id"]))}/rca-runs`;
    const headers: Record<string, string> = {};
    headers["Idempotency-Key"] = String(params["Idempotency-Key"]);
    headers["Content-Type"] = "application/json";
    return this.request<RCATask>(path, { method: "POST", headers, body: JSON.stringify(params.body) });
  }

  async getRcaRun(params: GetRcaRunParams): Promise<RCATask> {
    let path = `/api/v1/diagnostics/incidents/${encodeURIComponent(String(params["id"]))}/rca-runs/${encodeURIComponent(String(params["runId"]))}`;
    return this.request<RCATask>(path);
  }

  async cancelRcaRun(params: CancelRcaRunParams): Promise<void> {
    let path = `/api/v1/diagnostics/incidents/${encodeURIComponent(String(params["id"]))}/rca-runs/${encodeURIComponent(String(params["runId"]))}/cancel`;
    const headers: Record<string, string> = {};
    return this.request<void>(path, { method: "POST", headers });
  }

  async saveRcaConclusion(params: SaveRcaConclusionParams): Promise<RCARevision> {
    let path = `/api/v1/diagnostics/incidents/${encodeURIComponent(String(params["id"]))}/rca-runs/${encodeURIComponent(String(params["runId"]))}/conclusion`;
    const headers: Record<string, string> = {};
    headers["If-Match"] = String(params["If-Match"]);
    headers["Content-Type"] = "application/json";
    return this.request<RCARevision>(path, { method: "PATCH", headers, body: JSON.stringify(params.body) });
  }

  async getIncidentTopology(params: GetIncidentTopologyParams): Promise<IncidentTopology> {
    let path = `/api/v1/diagnostics/incidents/${encodeURIComponent(String(params["id"]))}/topology`;
    const query = new URLSearchParams();
    if (params["depth"] !== undefined) query.set("depth", String(params["depth"]));
    const queryString = query.toString();
    if (queryString) path += `?${queryString}`;
    return this.request<IncidentTopology>(path);
  }

  async saveTopologyLink(params: SaveTopologyLinkParams): Promise<void> {
    let path = `/api/v1/diagnostics/incidents/${encodeURIComponent(String(params["id"]))}/topology/links`;
    const headers: Record<string, string> = {};
    headers["Content-Type"] = "application/json";
    return this.request<void>(path, { method: "POST", headers, body: JSON.stringify(params.body) });
  }

  async listDiagnosticDatasources(params: ListDiagnosticDatasourcesParams = {}): Promise<DatasourcePage> {
    let path = `/api/v1/diagnostics/datasources`;
    const query = new URLSearchParams();
    if (params["limit"] !== undefined) query.set("limit", String(params["limit"]));
    if (params["continue"] !== undefined) query.set("continue", String(params["continue"]));
    const queryString = query.toString();
    if (queryString) path += `?${queryString}`;
    return this.request<DatasourcePage>(path);
  }

  async createDiagnosticDatasource(params: CreateDiagnosticDatasourceParams): Promise<DiagnosticDatasource> {
    let path = `/api/v1/diagnostics/datasources`;
    const headers: Record<string, string> = {};
    headers["Content-Type"] = "application/json";
    return this.request<DiagnosticDatasource>(path, { method: "POST", headers, body: JSON.stringify(params.body) });
  }

  async deleteDiagnosticDatasource(params: DeleteDiagnosticDatasourceParams): Promise<void> {
    let path = `/api/v1/diagnostics/datasources/${encodeURIComponent(String(params["datasourceId"]))}`;
    const headers: Record<string, string> = {};
    headers["If-Match"] = String(params["If-Match"]);
    return this.request<void>(path, { method: "DELETE", headers });
  }

  async updateDiagnosticDatasource(params: UpdateDiagnosticDatasourceParams): Promise<DiagnosticDatasource> {
    let path = `/api/v1/diagnostics/datasources/${encodeURIComponent(String(params["datasourceId"]))}`;
    const headers: Record<string, string> = {};
    headers["If-Match"] = String(params["If-Match"]);
    headers["Content-Type"] = "application/json";
    return this.request<DiagnosticDatasource>(path, { method: "PATCH", headers, body: JSON.stringify(params.body) });
  }

  async testDiagnosticDatasource(params: TestDiagnosticDatasourceParams): Promise<DiagnosticDatasource> {
    let path = `/api/v1/diagnostics/datasources/${encodeURIComponent(String(params["datasourceId"]))}/test`;
    const headers: Record<string, string> = {};
    headers["If-Match"] = String(params["If-Match"]);
    return this.request<DiagnosticDatasource>(path, { method: "POST", headers });
  }

  async listDiagnosticAssets(params: ListDiagnosticAssetsParams): Promise<DiagnosticAssets> {
    let path = `/api/v1/diagnostics/datasources/${encodeURIComponent(String(params["datasourceId"]))}/assets`;
    return this.request<DiagnosticAssets>(path);
  }

  async listDiagnosticPolicies(params: ListDiagnosticPoliciesParams = {}): Promise<PolicyPage> {
    let path = `/api/v1/diagnostics/policies`;
    const query = new URLSearchParams();
    if (params["limit"] !== undefined) query.set("limit", String(params["limit"]));
    if (params["continue"] !== undefined) query.set("continue", String(params["continue"]));
    const queryString = query.toString();
    if (queryString) path += `?${queryString}`;
    return this.request<PolicyPage>(path);
  }

  async createDiagnosticPolicy(params: CreateDiagnosticPolicyParams): Promise<DiagnosticPolicy> {
    let path = `/api/v1/diagnostics/policies`;
    const headers: Record<string, string> = {};
    headers["Content-Type"] = "application/json";
    return this.request<DiagnosticPolicy>(path, { method: "POST", headers, body: JSON.stringify(params.body) });
  }

  async updateDiagnosticPolicy(params: UpdateDiagnosticPolicyParams): Promise<DiagnosticPolicy> {
    let path = `/api/v1/diagnostics/policies/${encodeURIComponent(String(params["policyId"]))}`;
    const headers: Record<string, string> = {};
    headers["If-Match"] = String(params["If-Match"]);
    headers["Content-Type"] = "application/json";
    return this.request<DiagnosticPolicy>(path, { method: "PATCH", headers, body: JSON.stringify(params.body) });
  }

  async listDiagnosticPolicyRuns(params: ListDiagnosticPolicyRunsParams): Promise<PolicyExecutions> {
    let path = `/api/v1/diagnostics/policies/${encodeURIComponent(String(params["policyId"]))}/runs`;
    return this.request<PolicyExecutions>(path);
  }

  async listZabbixScopes(): Promise<ZabbixScopes> {
    let path = `/api/v1/diagnostics/zabbix/scopes`;
    return this.request<ZabbixScopes>(path);
  }

  async listZabbixSources(): Promise<ZabbixSources> {
    let path = `/api/v1/diagnostics/zabbix/sources`;
    return this.request<ZabbixSources>(path);
  }

  async createZabbixSource(params: CreateZabbixSourceParams): Promise<ZabbixSource> {
    let path = `/api/v1/diagnostics/zabbix/sources`;
    const headers: Record<string, string> = {};
    headers["Content-Type"] = "application/json";
    return this.request<ZabbixSource>(path, { method: "POST", headers, body: JSON.stringify(params.body) });
  }

  async deleteZabbixSource(params: DeleteZabbixSourceParams): Promise<void> {
    let path = `/api/v1/diagnostics/zabbix/sources/${encodeURIComponent(String(params["sourceId"]))}`;
    const headers: Record<string, string> = {};
    headers["If-Match"] = String(params["If-Match"]);
    return this.request<void>(path, { method: "DELETE", headers });
  }

  async updateZabbixSource(params: UpdateZabbixSourceParams): Promise<ZabbixSource> {
    let path = `/api/v1/diagnostics/zabbix/sources/${encodeURIComponent(String(params["sourceId"]))}`;
    const headers: Record<string, string> = {};
    headers["If-Match"] = String(params["If-Match"]);
    headers["Content-Type"] = "application/json";
    return this.request<ZabbixSource>(path, { method: "PATCH", headers, body: JSON.stringify(params.body) });
  }

  async syncZabbixSource(params: SyncZabbixSourceParams): Promise<ZabbixQueued> {
    let path = `/api/v1/diagnostics/zabbix/sources/${encodeURIComponent(String(params["sourceId"]))}/sync`;
    const headers: Record<string, string> = {};
    return this.request<ZabbixQueued>(path, { method: "POST", headers });
  }

  async testZabbixConnection(params: TestZabbixConnectionParams): Promise<ZabbixTestResult> {
    let path = `/api/v1/diagnostics/zabbix/test`;
    const headers: Record<string, string> = {};
    headers["Content-Type"] = "application/json";
    return this.request<ZabbixTestResult>(path, { method: "POST", headers, body: JSON.stringify(params.body) });
  }

  async previewZabbixProblems(params: PreviewZabbixProblemsParams): Promise<ZabbixPreview> {
    let path = `/api/v1/diagnostics/zabbix/preview`;
    const headers: Record<string, string> = {};
    headers["Content-Type"] = "application/json";
    return this.request<ZabbixPreview>(path, { method: "POST", headers, body: JSON.stringify(params.body) });
  }

  async listZabbixProblems(params: ListZabbixProblemsParams = {}): Promise<ZabbixProblems> {
    let path = `/api/v1/diagnostics/zabbix/problems`;
    const query = new URLSearchParams();
    if (params["environmentId"] !== undefined) query.set("environmentId", String(params["environmentId"]));
    if (params["businessId"] !== undefined) query.set("businessId", String(params["businessId"]));
    if (params["recent"] !== undefined) query.set("recent", String(params["recent"]));
    if (params["metadataOnly"] !== undefined) query.set("metadataOnly", String(params["metadataOnly"]));
    const queryString = query.toString();
    if (queryString) path += `?${queryString}`;
    return this.request<ZabbixProblems>(path);
  }

  async listObservabilitySources(): Promise<ObservabilitySources> {
    let path = `/api/v1/diagnostics/observability/sources`;
    return this.request<ObservabilitySources>(path);
  }

  async createObservabilitySource(params: CreateObservabilitySourceParams): Promise<ObservabilitySource> {
    let path = `/api/v1/diagnostics/observability/sources`;
    const headers: Record<string, string> = {};
    headers["Content-Type"] = "application/json";
    return this.request<ObservabilitySource>(path, { method: "POST", headers, body: JSON.stringify(params.body) });
  }

  async probeObservabilitySource(params: ProbeObservabilitySourceParams): Promise<ObservabilityPreview> {
    let path = `/api/v1/diagnostics/observability/probe`;
    const headers: Record<string, string> = {};
    headers["Content-Type"] = "application/json";
    return this.request<ObservabilityPreview>(path, { method: "POST", headers, body: JSON.stringify(params.body) });
  }

  async deleteObservabilitySource(params: DeleteObservabilitySourceParams): Promise<void> {
    let path = `/api/v1/diagnostics/observability/sources/${encodeURIComponent(String(params["sourceId"]))}`;
    const headers: Record<string, string> = {};
    headers["If-Match"] = String(params["If-Match"]);
    return this.request<void>(path, { method: "DELETE", headers });
  }

  async updateObservabilitySource(params: UpdateObservabilitySourceParams): Promise<ObservabilitySource> {
    let path = `/api/v1/diagnostics/observability/sources/${encodeURIComponent(String(params["sourceId"]))}`;
    const headers: Record<string, string> = {};
    headers["If-Match"] = String(params["If-Match"]);
    headers["Content-Type"] = "application/json";
    return this.request<ObservabilitySource>(path, { method: "PUT", headers, body: JSON.stringify(params.body) });
  }

  async toggleObservabilitySource(params: ToggleObservabilitySourceParams): Promise<ObservabilityToggle> {
    let path = `/api/v1/diagnostics/observability/sources/${encodeURIComponent(String(params["sourceId"]))}`;
    const headers: Record<string, string> = {};
    headers["If-Match"] = String(params["If-Match"]);
    headers["Content-Type"] = "application/json";
    return this.request<ObservabilityToggle>(path, { method: "PATCH", headers, body: JSON.stringify(params.body) });
  }

  async syncObservabilitySource(params: SyncObservabilitySourceParams): Promise<ObservabilityQueued> {
    let path = `/api/v1/diagnostics/observability/sources/${encodeURIComponent(String(params["sourceId"]))}/sync`;
    const headers: Record<string, string> = {};
    return this.request<ObservabilityQueued>(path, { method: "POST", headers });
  }

  async listObservabilityAlerts(params: ListObservabilityAlertsParams = {}): Promise<ObservabilityAlerts> {
    let path = `/api/v1/diagnostics/observability/alerts`;
    const query = new URLSearchParams();
    if (params["environmentId"] !== undefined) query.set("environmentId", String(params["environmentId"]));
    if (params["businessId"] !== undefined) query.set("businessId", String(params["businessId"]));
    if (params["clusterId"] !== undefined) query.set("clusterId", String(params["clusterId"]));
    if (params["recent"] !== undefined) query.set("recent", String(params["recent"]));
    const queryString = query.toString();
    if (queryString) path += `?${queryString}`;
    return this.request<ObservabilityAlerts>(path);
  }

  async investigateHealthAlert(params: InvestigateHealthAlertParams): Promise<HealthInvestigation> {
    let path = `/api/v1/diagnostics/health/${encodeURIComponent(String(params["source"]))}/${encodeURIComponent(String(params["sourceId"]))}/${encodeURIComponent(String(params["id"]))}/investigate`;
    const headers: Record<string, string> = {};
    return this.request<HealthInvestigation>(path, { method: "POST", headers });
  }

  async listObservabilityDashboards(params: ListObservabilityDashboardsParams): Promise<ObservabilityDashboards> {
    let path = `/api/v1/diagnostics/observability/dashboards`;
    const query = new URLSearchParams();
    query.set("clusterId", String(params["clusterId"]));
    query.set("namespace", String(params["namespace"]));
    if (params["businessId"] !== undefined) query.set("businessId", String(params["businessId"]));
    if (params["anchor"] !== undefined) query.set("anchor", String(params["anchor"]));
    const queryString = query.toString();
    if (queryString) path += `?${queryString}`;
    return this.request<ObservabilityDashboards>(path);
  }

}
