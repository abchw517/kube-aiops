/** Portal presentation metadata. Capability and route enforcement do not consume this registry. */
export type PortalPage = {
  id: string;
  label: string;
  order: number;
  parentId: string | null;
  route: string | null;
};
export type PageRegistry = Readonly<Record<string, PortalPage>>;

export const portalPages: PageRegistry = {
  "diagnosis": {"id": "diagnosis", "label": "诊断控制台", "order": 1, "parentId": null, "route": null},
  "resources": {"id": "resources", "label": "资源管理", "order": 2, "parentId": null, "route": null},
  "knowledge": {"id": "knowledge", "label": "运维知识库", "order": 3, "parentId": null, "route": null},
  "system": {"id": "system", "label": "系统管理", "order": 4, "parentId": null, "route": null},
  "group.diagnosis.workspace": {"id": "group.diagnosis.workspace", "label": "诊断工作区", "order": 1, "parentId": "diagnosis", "route": null},
  "group.diagnosis.settings": {"id": "group.diagnosis.settings", "label": "诊断配置", "order": 2, "parentId": "diagnosis", "route": null},
  "group.resources.cluster": {"id": "group.resources.cluster", "label": "集群资源", "order": 1, "parentId": "resources", "route": null},
  "group.resources.catalog": {"id": "group.resources.catalog", "label": "平台目录", "order": 2, "parentId": "resources", "route": null},
  "group.knowledge.workspace": {"id": "group.knowledge.workspace", "label": "知识工作区", "order": 1, "parentId": "knowledge", "route": null},
  "group.system.identity": {"id": "group.system.identity", "label": "身份与访问", "order": 1, "parentId": "system", "route": null},
  "group.system.audit": {"id": "group.system.audit", "label": "审计", "order": 2, "parentId": "system", "route": null},
  "diagnosis.incidents": {"id": "diagnosis.incidents", "label": "故障事件", "order": 2, "parentId": "group.diagnosis.workspace", "route": "#/diagnostics/events"},
  "diagnosis.findings": {"id": "diagnosis.findings", "label": "原有诊断结果", "order": 3, "parentId": "group.diagnosis.workspace", "route": "#/findings"},
  "diagnosis.policy": {"id": "diagnosis.policy", "label": "诊断策略", "order": 1, "parentId": "group.diagnosis.settings", "route": "#/diagnostics/policies"},
  "resource.clusters": {"id": "resource.clusters", "label": "Kubernetes 集群管理", "order": 1, "parentId": "group.resources.cluster", "route": "#/resources/clusters"},
  "resource.environments": {"id": "resource.environments", "label": "环境管理", "order": 1, "parentId": "group.resources.catalog", "route": "#/resources/environments"},
  "resource.business": {"id": "resource.business", "label": "业务线管理", "order": 2, "parentId": "group.resources.catalog", "route": "#/resources/business-lines"},
  "resource.datasources": {"id": "resource.datasources", "label": "数据源管理", "order": 3, "parentId": "group.resources.catalog", "route": "#/resources/datasources"},
  "system.users": {"id": "system.users", "label": "用户管理", "order": 2, "parentId": "group.system.identity", "route": "#/admin/users"},
  "system.access": {"id": "system.access", "label": "权限管理", "order": 3, "parentId": "group.system.identity", "route": "#/admin/access"},
  "system.ldap": {"id": "system.ldap", "label": "认证配置", "order": 1, "parentId": "group.system.identity", "route": "#/admin/ldap"},
  "system.audit": {"id": "system.audit", "label": "操作审计", "order": 1, "parentId": "group.system.audit", "route": "#/admin/operations"},
  "knowledge.search": {"id": "knowledge.search", "label": "知识检索", "order": 1, "parentId": "group.knowledge.workspace", "route": "#/wiki"},
  "knowledge.detail": {"id":"knowledge.detail","label":"知识详情","order":1,"parentId":"knowledge.search","route":null},
  "knowledge.manage": {"id": "knowledge.manage", "label": "知识治理", "order": 2, "parentId": "group.knowledge.workspace", "route": "#/wiki/governance"},
  "diagnosis.incidents.rca": {"id": "diagnosis.incidents.rca", "label": "AI 根因分析", "order": 1, "parentId": "diagnosis.incidents", "route": null},
  "diagnosis.incidents.topology": {"id": "diagnosis.incidents.topology", "label": "关联拓扑", "order": 2, "parentId": "diagnosis.incidents", "route": null},
  "diagnosis.incidents.evidence": {"id": "diagnosis.incidents.evidence", "label": "关联证据", "order": 3, "parentId": "diagnosis.incidents", "route": null},
  "diagnosis.overview": {"id": "diagnosis.overview", "label": "诊断总览", "order": 1, "parentId": "group.diagnosis.workspace", "route": "#/diagnostics"},
  "diagnosis.incident-detail": {"id": "diagnosis.incident-detail", "label": "故障详情", "order": 4, "parentId": "diagnosis.incidents", "route": null},
  "diagnosis.finding-detail": {"id": "diagnosis.finding-detail", "label": "诊断详情", "order": 1, "parentId": "diagnosis.findings", "route": null},
  "resource.ownership": {"id": "resource.ownership", "label": "命名空间归属", "order": 1, "parentId": "resource.clusters", "route": null},
  "system.catalog": {"id": "system.catalog", "label": "权限目录", "order": 1, "parentId": "system.access", "route": "#/admin/access/catalog"},
  "system.password": {"id": "system.password", "label": "修改密码", "order": 4, "parentId": "group.system.identity", "route": "#/admin/password"}
};

/** Stable catalog feature keys point to stable page IDs, independently of navigation placement. */
export const permissionPages: Readonly<Record<string, string>> = {
  "rca": "diagnosis.incidents.rca",
  "topology": "diagnosis.incidents.topology",
  "connector": "resource.datasources",
  "policy": "diagnosis.policy",
  "incident": "diagnosis.incidents",
  "finding": "diagnosis.findings",
  "discovery": "diagnosis.findings",
  "evidence": "diagnosis.incidents.evidence",
  "review": "diagnosis.findings",
  "related": "diagnosis.findings",
  "document": "knowledge.search",
  "qa": "knowledge.search",
  "feedback": "knowledge.search",
  "candidate": "knowledge.manage",
  "workflow": "knowledge.manage",
  "governance": "knowledge.manage",
  "source": "knowledge.manage",
  "credential": "knowledge.manage",
  "sync": "knowledge.manage",
  "schedule": "knowledge.manage",
  "environment": "resource.environments",
  "business": "resource.business",
  "cluster": "resource.clusters",
  "ownership": "resource.clusters",
  "account": "system.users",
  "role": "system.access",
  "group": "system.access",
  "binding": "system.access",
  "ldap": "system.ldap",
  "catalog": "system.access",
  "audit": "system.audit",
  "retention": "system.audit"
};

export function pageTrail(id: string, registry: PageRegistry = portalPages): PortalPage[] {
  const trail: PortalPage[] = [];
  const seen = new Set<string>();
  let page: PortalPage | undefined = Object.hasOwn(registry, id) ? registry[id] : undefined;
  while (page) {
    if (seen.has(page.id)) return [];
    seen.add(page.id);
    trail.unshift(page);
    if (!page.parentId) return trail;
    page = Object.hasOwn(registry, page.parentId) ? registry[page.parentId] : undefined;
  }
  return [];
}

export function comparePageTrails(a: PortalPage[], b: PortalPage[]): number {
  for (const [index, left] of a.entries()) {
    const right = b[index];
    if (!right) break;
    const difference = left.order - right.order || left.id.localeCompare(right.id);
    if (difference) return difference;
  }
  return a.length - b.length;
}

export function permissionTrail(key: string | undefined, registry: PageRegistry = portalPages): PortalPage[] {
  const id = key && Object.hasOwn(permissionPages, key) ? permissionPages[key] : undefined;
  return id ? pageTrail(id, registry) : [];
}
