import { createServer } from "node:http";
import { readFile } from "node:fs/promises";
import { extname, posix } from "node:path";

const port = Number(process.env.PORT ?? "4173");
const root = new URL("../dist/", import.meta.url);

const longRegistryURL = `https://registry.example.com/v2/library/${"registry-path".repeat(28)}/manifests/1.36?ns=docker.io`;

const reviewHistory = [];
let observationRequests = 0;
let knowledgeCandidate;

const findings = [
  {
    id: "finding-critical",
    cluster: "local",
    namespace: "default",
    severity: "critical",
    resource: { apiVersion: "v1", kind: "Pod", namespace: "default", name: "api-0" },
    problem: `CrashLoopBackOff detected
Back-off pulling image "busybox:1.36": 403 Forbidden
镜像请求地址：${longRegistryURL}`,
    details: `Container restart backoff is increasing. <img id="sanitizer-xss-probe" src=x onerror="globalThis.__kubeAiopsXss=1"> javascript:alert(1)\n\n镜像地址：${longRegistryURL}`,
    source: "k8sgpt",
    createdAt: "2026-09-02T03:00:00Z"
  },
  {
    id: "finding-warning",
    cluster: "local",
    namespace: "payments",
    severity: "warning",
    resource: { apiVersion: "apps/v1", kind: "Deployment", namespace: "payments", name: "worker" },
    problem: "Replica availability degraded",
    details: "Available replicas are below desired replicas.",
    source: "k8sgpt",
    createdAt: "2026-09-02T03:05:00Z"
  },
  {
    id: "finding-warning",
    cluster: "test-k8s",
    namespace: "sandbox",
    severity: "info",
    resource: { apiVersion: "v1", kind: "Pod", namespace: "sandbox", name: "fixture" },
    problem: "测试集群容量建议",
    details: "测试环境容器资源建议",
    source: "k8sgpt",
    createdAt: "2026-09-02T03:05:00Z"
  }
];
const clusters = [
  { id: "local", name: "local", status: "ready", environment: "研发", business: "平台", group: "研发集群" },
  { id: "test-k8s", name: "测试实例", status: "ready", environment: "测试", business: "平台", group: "测试集群" },
];

let warningEvidenceRequests = 0;
let authenticationRequired = false;
let authenticated = false;
const ldapReaderId = "ldap:cn=reader,ou=安全组,ou=公共支撑部,ou=研发管理中心,ou=platform-engineering," + "ou=regional-infrastructure,dc=example,dc=internal";
let adminSession = false;
let fixtureSuperAdministrator = true;
let fixtureAccountDeleted = false;
const assignmentRoleIds = ["diagnosis-reader", "namespace-auditor", "system-admin"];
let fixtureReaderRoles = ["guest-readonly"];
let auditRetention = { low: 3, medium: 5, high: 7 };
let auditSaves = 0;
const auditAt = Date.parse("2026-11-18T07:43:19Z") / 1000;
const auditFixture = [
  { id: 76, at: auditAt, actor: "local:admin", action: "创建账号", risk: "高", target: "local:guest", result: "成功", reason: "", batch: "" },
  { id: 75, at: auditAt - 1, actor: "local:admin", action: "批量加入用户组", risk: "中", target: "批量操作", result: "部分成功", reason: "", batch: "batch-fixture" },
  { id: 74, at: auditAt - 2, actor: "local:admin", action: "批量加入用户组", risk: "中", target: "ldap:alice", result: "成功", reason: "", batch: "batch-fixture" },
  ...Array.from({ length: 19 }, (_, index) => ({ id: 73 - index, at: auditAt - 3 - index, actor: "local:admin",
    action: "批量加入用户组", risk: "中", target: `ldap:member-${index}`, result: "成功", reason: "", batch: "batch-fixture" })),
  { id: 54, at: auditAt - 22, actor: "local:admin", action: "批量加入用户组", risk: "中", target: "ldap:bob", result: "失败", reason: "账号已停用", batch: "batch-fixture" },
  ...Array.from({ length: 53 }, (_, index) => ({ id: 53 - index, at: auditAt - 23 - index, actor: "local:admin",
    action: "维护资源目录", risk: "低", target: `environment-${index}`, result: "成功", reason: "", batch: "" })),
];
let role;

let localConnectionDeleted = false;
let localCheckedAt = 0;
const catalogs = {
  environments: [{ id: "development", name: "研发" }],
  "business-lines": [{ id: "yunfan", name: "云帆" }, { id: "platform", name: "平台" }],
};

createServer(async (request, response) => {
  const url = new URL(request.url ?? "/", `http://127.0.0.1:${port}`);
  if (url.pathname === "/test/admin-session") {
    adminSession = true;
    fixtureSuperAdministrator = url.searchParams.get("super") !== "false";
    authenticated = true;
    response.writeHead(204).end();
    return;
  }
  if (url.pathname === "/test/require-auth") {
    authenticationRequired = true;
    authenticated = false;
    response.writeHead(204).end();
    return;
  }
  if (url.pathname === "/test/audit-saves") return json(response, 200, auditSaves);
  if (request.method === "POST" && url.pathname === "/api/v1/auth/login") {
    const chunks = [];
    for await (const chunk of request) chunks.push(chunk);
    const credentials = JSON.parse(Buffer.concat(chunks).toString());
    const expectedProvider = credentials.username === "ldap-test" ? "ldap" : "local";
    if (credentials.provider !== expectedProvider || !["ldap-test", "portal-test"].includes(credentials.username) ||
        credentials.password !== "fixture-password") {
      return json(response, 401, { error: { code: "UNAUTHORIZED" } });
    }
    authenticated = true;
    response.writeHead(204).end();
    return;
  }
  if (request.method === "POST" && url.pathname === "/api/v1/auth/logout") {
    authenticated = false;
    response.writeHead(204).end();
    return;
  }
  if (authenticationRequired && !authenticated && url.pathname.startsWith("/api/v1/")) {
    return json(response, 401, { error: { code: "UNAUTHORIZED" } });
  }
  const wikiID = "a".repeat(64);
  const wikiDoc = {knowledgeId: wikiID, title: "镜像拉取排障", environment: "测试", business: "运维", documentType: "排障手册"};
  const candidateID = "b".repeat(64);
  const syncRun = {id: "fixture-run", status: "partial", trigger: "manual", started: 1795150900,
    saved: 1, skipped: 0, failed: 1, missing: 0};
  if (url.pathname === "/api/v1/wiki/finding" && url.searchParams.get("id") === "finding-critical") {
    if (request.method === "GET") return json(response, 200, {items: [{...wikiDoc, revision: 1, matchReason: "命中 ContainerBackOff"}], links: [], status: "ready", canDraft: true});
    const chunks = [];
    for await (const chunk of request) chunks.push(chunk);
    const input = JSON.parse(Buffer.concat(chunks).toString());
    if (input.action !== "draft" || input.reviewRevision !== 1) return json(response, 409, {});
    knowledgeCandidate = {
      id: candidateID, knowledgeId: candidateID, version: 1, baseRevision: 0, status: "draft",
      document: {title: "镜像拉取案例草稿", cluster: "local", namespace: "payments", documentType: "故障复盘",
        metadata: {finding: {id: "finding-critical", revision: 1}, resourceType: "Pod",
          sections: {impact: "Pod 无法拉取镜像", conclusion: reviewHistory[0].conclusion}}},
    };
    return json(response, 200, knowledgeCandidate);
  }
  if (url.pathname === "/api/v1/wiki/candidates") return json(response, 200, knowledgeCandidate ? [knowledgeCandidate] : []);
  if (url.pathname === "/api/v1/wiki/governance") return json(response, 200, [{...wikiDoc, pendingFeedback: 1}]);
  if (url.pathname === `/api/v1/wiki/candidates/${candidateID}` && knowledgeCandidate) {
    if (request.method === "GET") return json(response, 200, knowledgeCandidate);
    const chunks = [];
    for await (const chunk of request) chunks.push(chunk);
    const input = JSON.parse(Buffer.concat(chunks).toString());
    if (input.action === "submit" && input.version === 1 && input.document.metadata.sections.recovery) {
      knowledgeCandidate = {...knowledgeCandidate, status: "review", version: 2, document: {...input.document, environment: "testing"}};
      return json(response, 200, knowledgeCandidate);
    }
    return json(response, 409, {});
  }
  if (url.pathname === "/api/v1/wiki/knowledge") {
    return json(response, 200, {items: [wikiDoc], filters: {environment: ["测试"], business: ["运维"], documentType: ["排障手册"]}, page: 1, total: 1, pageSize: 20});
  }
  if (url.pathname === "/api/v1/wiki/ask" && request.method === "POST") {
    const chunks = [];
    for await (const chunk of request) chunks.push(chunk);
    const input = JSON.parse(Buffer.concat(chunks).toString());
    if (input.question === "镜像拉取怎么排查？") {
      return json(response, 200, {answer: "请验证仓库权限。", citations: [{...wikiDoc, revision: 1, section: "checks"}]});
    }
    if (input.question === "旧范围提问") {
      await new Promise((resolve) => setTimeout(resolve, 350));
      return json(response, 200, {answer: "这个旧范围回答不应显示", citations: []});
    }
    return json(response, 400, {error: "invalid fixture question"});
  }
  if ([`/api/v1/wiki/knowledge/${wikiID}/revisions/1`, `/api/v1/wiki/knowledge/${wikiID}`].includes(url.pathname)) {
    return json(response, 200, {...wikiDoc, revision: 1, metadata: {sections: {checks: "验证仓库权限", recovery: "确认 Pod Ready"}}});
  }
  if (url.pathname === `/api/v1/wiki/knowledge/${wikiID}/feedback` && request.method === "POST") {
    const chunks = [];
    for await (const chunk of request) chunks.push(chunk);
    const input = JSON.parse(Buffer.concat(chunks).toString());
    if (input.revision !== 1 || input.section !== "checks" || input.kind !== "outdated") return json(response, 400, {});
    response.writeHead(204).end();
    return;
  }
  if (url.pathname === "/api/v1/wiki/sources/ops/runs") return json(response, 200, { items: [syncRun], lastSucceeded: syncRun.started - 3600 });
  if (url.pathname === "/api/v1/wiki/sources/ops/runs/fixture-run") {
    return json(response, 200, {run: syncRun, items: [{pageId: "page-2", status: "failed", reason: "文档格式无效"}], more: false});
  }
  if (url.pathname === "/api/v1/wiki/sources/ops/runs/fixture-run/retry" && request.method === "POST") {
    const chunks = [];
    for await (const chunk of request) chunks.push(chunk);
    const input = JSON.parse(Buffer.concat(chunks).toString());
    return json(response, input.pageId === "page-2" ? 200 : 400, {saved: 1, skipped: 0, failed: 0, missing: 0});
  }
  if (url.pathname === "/api/v1/admin/status" && adminSession) return json(response, 200, {
    admin: true, superAdministrator: fixtureSuperAdministrator, canChangePassword: fixtureSuperAdministrator,
    findingAI: true, manageEnvironments: true, manageBusinessLines: true,
  });
  if (url.pathname === "/api/v1/admin/finding-analysis" && adminSession && url.searchParams.get("id") === "finding-critical") {
    if (request.method === "GET") return json(response, 200, { original: "原始诊断结论", history: reviewHistory, canPublish: true });
    const chunks = [];
    for await (const chunk of request) chunks.push(chunk);
    const input = JSON.parse(Buffer.concat(chunks).toString());
    if (input.revision !== (reviewHistory[0]?.revision ?? 0)) return json(response, 409, {});
    if (input.action === "assess") {
      await new Promise((resolveDelay) => setTimeout(resolveDelay, 600));
      if (input.observation && ++observationRequests === 1) return json(response, 504, { code: "AI_PROVIDER_TIMEOUT" });
      return json(response, 200, {
        assessment: { state: "insufficient", judgment: input.observation ? "镜像仓库返回 403，仍需核对当前响应。" : "已有历史事件支持排查镜像仓库。", gaps: "当前镜像仓库响应", next: "核对最近一次拉取事件" },
        evidence: [{ source: "kubernetes-events", signalId: "event-critical", title: "ContainerBackOff", time: "2026-09-02T02:59:00Z", historical: true }],
        evidenceState: "available", sources: [{ source: "prometheus", state: "unavailable" }],
        citations: [{ knowledgeId: "fixture-knowledge", revision: 1, title: "镜像拉取案例" }],
      });
    }
    if (input.action === "save" && input.observation === "人工确认：镜像仓库拒绝访问") {
      reviewHistory.unshift({ revision: input.revision + 1, conclusion: input.observation });
      return json(response, 200, reviewHistory[0]);
    }
    return json(response, 400, { error: "invalid fixture evidence" });
  }
  const catalogKind = url.pathname.split("/").at(-1);
  if (catalogKind in catalogs && adminSession) {
    const items = catalogs[catalogKind];
    if (request.method === "GET") return json(response, 200, items);
    const chunks = [];
    for await (const chunk of request) chunks.push(chunk);
    const input = JSON.parse(Buffer.concat(chunks).toString());
    const entry = items.find((item) => item.id === (input.originalId ?? input.id));
    if (request.method === "DELETE") {
      if (input.id === "new-dev") return json(response, 409, { message: "仍有集群引用", references: ["dev-k8s"] });
      catalogs[catalogKind] = items.filter((item) => item.id !== input.id);
      response.writeHead(204).end();
      return;
    }
    if (request.method === "PUT" && entry && !items.some((item) => item !== entry && item.id === input.id)) Object.assign(entry, { id: input.id, name: input.name });
    else if (request.method === "POST" && !entry) items.push(input);
    else return json(response, 409, { message: "ID 已存在" });
    return json(response, 200, input);
  }
  if (url.pathname === "/api/v1/admin/cluster-onboarding" && adminSession) {
    if (!localConnectionDeleted && request.method === "POST" && url.searchParams.get("action") === "recheck" &&
      url.searchParams.get("id") === "local") {
      localCheckedAt = 1700000000;
      response.writeHead(204).end();
      return;
    }
    if (url.searchParams.get("id") === "local" &&
      (request.method === "DELETE" || url.searchParams.get("action") === "reconnect")) {
      localConnectionDeleted = request.method === "DELETE";
      response.writeHead(204).end();
      return;
    }
    if (request.method === "GET") {
      return json(response, 200, [{
        id: "dev-k8s", name: "研发集群", group: "应用集群", mode: "pull", enabled: true, credentialKind: "kubeconfig",
        environment: "研发", business: "云帆", firstCollectedAt: 1700000000,
        collection: { nodes: [{ name: "test-worker", ready: "False" }], namespaces: [{ name: "dev-app" }] },
      }, {
        id: "local", name: "本地可恢复集群", mode: "local", enabled: !localConnectionDeleted,
        deleted: localConnectionDeleted, checkedAt: localCheckedAt, environment: "研发", business: "云帆",
      }]);
    }
    const chunks = [];
    for await (const chunk of request) chunks.push(chunk);
    const input = JSON.parse(Buffer.concat(chunks).toString());
    if (input.id === "dev-yunfan-01" && input.environment === "研发环境" && input.business === "云帆" &&
        input.group === "应用集群" && input.mode === "push" && input.enabled) {
      return json(response, 200, { token: "fixture-report-token" });
    }
    return json(response, 400, { error: "invalid onboarding fixture" });
  }
  if (url.pathname === "/api/v1/admin/ldap" && adminSession && request.method === "GET") {
    return json(response, 200, { url: "ldaps://example.test", baseDN: "dc=example,dc=test" });
  }
  if (adminSession && (url.pathname === "/api/v1/admin/ldap-test" && request.method === "POST" ||
      url.pathname === "/api/v1/admin/ldap" && request.method === "PUT")) {
    const chunks = [];
    for await (const chunk of request) chunks.push(chunk);
    const input = JSON.parse(Buffer.concat(chunks).toString());
    if (request.method === "POST" && input.testUsername === "fixture-employee" && input.testPassword === "fixture-password") {
      if (input.baseDN === "dc=example,dc=test") return json(response, 200, { stage: "connection_tcp" });
      if (input.url !== "ldap://example.test" || input.allowPlainLDAP !== true) {
        return json(response, 400, { error: "missing plain LDAP opt-in" });
      }
      return json(response, 200, { stage: "success", verification: "fixture-verification" });
    }
    if (request.method === "PUT" && input.verification === "fixture-verification" && input.allowPlainLDAP === true &&
        !Object.hasOwn(input, "testPassword") && !Object.hasOwn(input, "testUsername")) {
      response.writeHead(204).end();
      return;
    }
    return json(response, 400, { error: "unexpected LDAP request" });
  }
  if (url.pathname === "/api/v1/admin/namespace-business" && adminSession) {
    if (request.method === "PUT") {
      const chunks = [];
      for await (const chunk of request) chunks.push(chunk);
      const delta = Buffer.concat(chunks).toString();
      if (url.searchParams.get("cluster") === "dev-k8s" &&
        (delta === '{"businessId":"yunfan","add":["payments"],"remove":["monitoring"]}' ||
          delta === '{"businessId":"platform","add":[],"remove":["payments"]}')) {
        response.writeHead(204).end();
        return;
      }
      return json(response, 400, { error: "unexpected namespace assignments" });
    }
    return json(response, 200, {
      namespaces: ["payments", "monitoring", ...Array.from({ length: 30 }, (_, index) => `team-${index}`)].map((name) => ({ name })),
      assigned: { payments: ["platform"], monitoring: ["yunfan"], "retired-ns": ["yunfan"] },
    });
  }
  if (url.pathname === "/api/v1/admin/accounts" && adminSession) {
    if (request.method === "POST") {
      const chunks = [];
      for await (const chunk of request) chunks.push(chunk);
      const change = JSON.parse(Buffer.concat(chunks).toString());
      if (change.action === "delete" && change.id === ldapReaderId && !fixtureAccountDeleted) {
        fixtureAccountDeleted = true;
        return json(response, 200, [{ id: ldapReaderId }]);
      }
      const targetsReader = change.id === ldapReaderId || (change.ids?.length === 1 && change.ids[0] === ldapReaderId);
      const hasExpectedRoles = change.values?.length === assignmentRoleIds.length &&
        assignmentRoleIds.every((id) => change.values.includes(id));
      if (fixtureSuperAdministrator && targetsReader && hasExpectedRoles && ["grant", "revoke"].includes(change.action)) {
        fixtureReaderRoles = change.action === "grant" ? [...new Set([...fixtureReaderRoles, ...change.values])] :
          fixtureReaderRoles.filter((roleId) => !change.values.includes(roleId));
        response.setHeader("X-Operation-Audit-Ref", "ABCDEFGHIJKLMNOPQRSTUVWXY2");
        return json(response, 200, [{ id: ldapReaderId }]);
      }
      return json(response, 400, { error: "unexpected account assignment" });
    }
    return json(response, 200, fixtureAccountDeleted ? [] : [{
      id: ldapReaderId, username: "reader", kind: "ldap", groups: ["ops"], roles: fixtureReaderRoles,
      effective: [{ environment: "development", sources: ["直接授予 → 访客只读", "用户组 运维 → 观察员"] }],
    }]);
  }
  if (url.pathname === "/api/v1/admin/access" && adminSession) {
    if (request.method === "POST") {
      const chunks = [];
      for await (const chunk of request) chunks.push(chunk);
      const change = JSON.parse(Buffer.concat(chunks).toString());
      if (change.action === "group-save" && change.id === "ops" && change.roleIds?.[0] === "guest-readonly") {
        return json(response, 200, [{ id: "ops" }]);
      }
      if (change.action === "role-save" && change.name === "浏览器检查" &&
          change.scopes?.length === 2 && change.scopes.every(({ environment, cluster }) => environment === "development" && cluster === "local") &&
          change.scopes.map(({ namespace }) => namespace).join(",") === "payments,monitoring") {
        response.writeHead(409, { "Content-Type": "text/plain" }).end("角色名称已使用");
        return;
      }
      return json(response, 400, { error: "unexpected system editor change" });
    }
    return json(response, 200, { groups: [{ id: "ops", name: "运维", roles: ["guest-readonly"] }], roles: [
      { id: "guest-readonly", name: "访客只读", fixed: true, scopes: [{ environment: "development" }] },
      { id: "diagnosis-reader", name: "诊断观察员", scopes: [{ environment: "development" }] },
      { id: "namespace-auditor", name: "命名空间审计员", scopes: [{ environment: "testing" }] },
      { id: "system-admin", name: "系统管理员", fixed: true, scopes: [] },
    ] });
  }
  if (url.pathname === "/api/v1/admin/operations" && adminSession) {
    if (request.method === "PUT") {
      if (!fixtureSuperAdministrator) return json(response, 403, { error: "audit retention is read-only" });
      const chunks = [];
      for await (const chunk of request) chunks.push(chunk);
      const { low, medium, high } = JSON.parse(Buffer.concat(chunks).toString());
      if ([low, medium, high].every((days) => Number.isInteger(days) && days >= 0)) {
        auditRetention = { low, medium, high };
        auditSaves++;
        response.writeHead(204).end();
        return;
      }
      return json(response, 400, { error: "unexpected audit retention configuration" });
    }
    const active = auditFixture.filter(({ risk }) => auditRetention[{ 低: "low", 中: "medium", 高: "high" }[risk]] !== 0);
    const { account, action, risk, batch, before, record } = Object.fromEntries(url.searchParams);
    if (record === "ABCDEFGHIJKLMNOPQRSTUVWXY2") return json(response, 200, { items: [{
      id: 9001, actor: "local:admin", at: Math.floor(Date.now() / 1000),
      action: "授予角色", risk: "高", target: ldapReaderId, result: "成功", reason: "", batch: "",
    }], total: 1, page: 1, limit: 10, before: 0 });
    const items = active.filter((item) => !record && (!account || `${item.actor}${item.target}`.includes(account)) &&
      (!action || item.action === action) && (!risk || item.risk === risk) && (!batch || item.batch === batch) && (!before || item.id < Number(before)));
    const metadata = { retention: auditRetention, actions: [...new Set(active.map((item) => item.action))] };
    if (url.searchParams.has("page")) {
      const limit = Number(url.searchParams.get("limit"));
      if (!Number.isInteger(limit) || limit < 1 || limit > 100) return json(response, 400, { error: "invalid audit page size" });
      const page = Math.min(Number(url.searchParams.get("page")), Math.max(1, Math.ceil(items.length / limit)));
      const start = (page - 1) * limit;
      return json(response, 200, { items: items.slice(start, start + limit), total: items.length, page, limit, before: 0,
        ...(!account && !action && !risk && !batch && page === 1 ? metadata : {}) });
    }
    return json(response, 200, { items: items.slice(0, 100), before: items.length > 100 ? items[99].id : 0,
      ...(!url.search ? metadata : {}) });
  }
  if (url.pathname === "/api/v1/admin/roles" && adminSession) {
    if (request.method === "GET") return json(response, 200, { roles: role ? [role] : [], legacyRules: [] });
    const chunks = [];
    for await (const chunk of request) chunks.push(chunk);
    const spec = JSON.parse(Buffer.concat(chunks).toString());
    if (request.method === "POST" && spec.name === "开发环境观察员" && spec.cluster === "local" &&
        JSON.stringify(spec.namespaces) === '["default","payments"]' &&
        JSON.stringify(spec.groups) === '["local:operators","ldap:CN=Readers,DC=example,DC=com"]' &&
        JSON.stringify(spec.modules) === '["findings:list","correlations:read"]') {
      role = { ...spec, id: "observer-role", revision: 1 };
      response.writeHead(204).end();
      return;
    }
    return json(response, 400, { error: "unexpected role configuration" });
  }
  if (url.pathname === "/api/v1/clusters") return json(response, 200, { items: clusters });
  if (url.pathname === "/api/v1/clusters/local/namespaces") return json(response, 200, { items: [{ name: "default" }, { name: "payments" }] });
  if (url.pathname === "/api/v1/clusters/test-k8s/namespaces") return json(response, 200, { items: [{ name: "sandbox" }] });
  if (url.pathname === "/api/v1/findings/summary") {
    const filtered = filterFindings(url);
    return json(response, 200, summarize(filtered));
  }
  if (url.pathname === "/api/v1/findings") {
    return json(response, 200, { items: filterFindings(url), pagination: { limit: 50 } });
  }
  const correlationPath = url.pathname.match(/^\/api\/v1\/findings\/([^/]+)\/correlation$/);
  if (correlationPath) return correlationResponse(response, decodeURIComponent(correlationPath[1]), url.searchParams.get("cluster") ?? "local");
  if (url.pathname.startsWith("/api/v1/findings/")) {
    const id = decodeURIComponent(url.pathname.split("/").at(-1) ?? "");
    const cluster = url.searchParams.get("cluster") ?? "local";
    const finding = findings.find((item) => item.id === id && item.cluster === cluster) ??
      (["finding-auth", "finding-forbidden"].includes(id) ? { ...findings[0], id } : undefined);
    return finding ? json(response, 200, finding) : json(response, 404, { error: { code: "FINDING_NOT_FOUND", message: "finding not found" } });
  }

  try {
    // HTTP 路径始终使用斜线，避免在 Windows 上把资源请求解析成文件系统根路径。
    const requested = url.pathname === "/" ? "index.html" : posix.normalize(url.pathname).replace(/^\/+/, "");
    const fileUrl = new URL(requested, root);
    const body = await readFile(fileUrl);
    response.writeHead(200, { "Content-Type": contentType(extname(requested)) });
    response.end(body);
  } catch {
    const body = await readFile(new URL("index.html", root));
    response.writeHead(200, { "Content-Type": "text/html; charset=utf-8" });
    response.end(body);
  }
}).listen(port, "127.0.0.1", () => console.log(`mock server listening on ${port}`));

function correlationResponse(response, id, cluster) {
  if (id === "finding-auth") return json(response, 401, { error: { code: "UNAUTHORIZED" } });
  if (id === "finding-forbidden") return json(response, 403, { error: { code: "FORBIDDEN" } });
  const finding = findings.find((item) => item.id === id && item.cluster === cluster);
  if (!finding) return json(response, 404, { error: { code: "FINDING_NOT_FOUND" } });
  if (id === "finding-warning" && cluster === "local" && ++warningEvidenceRequests === 1) {
    return json(response, 503, { error: { code: "CORRELATION_UNAVAILABLE", message: "private-provider-detail" } });
  }
  const signals = id === "finding-critical" ? [
    { id: "event-critical", source: "kubernetes-events", type: "event", severity: "critical",
      resource: finding.resource, name: "ContainerBackOff", count: 4, lastSeen: "2026-09-02T02:59:00Z",
      summary: '<em id="evidence-markup-probe">Rendered as text</em>' },
    { id: "event-info", source: "kubernetes-events", type: "event", severity: "info", name: "Started" },
    { id: "log-oom", source: "victorialogs", type: "log", name: "out_of_memory", count: 2 },
  ] : [];
  return json(response, 200, {
    findingId: id,
    scope: { cluster: finding.cluster, namespace: finding.namespace, resource: finding.resource },
    window: { start: new Date(Date.parse(finding.createdAt) - 1800_000).toISOString(), end: finding.createdAt },
    budget: { windowSeconds: 1800, perSourceTimeoutMillis: 4000, maxSignalsPerSource: 50 },
    sources: [
      { source: "kubernetes-events", state: id === "finding-critical" ? "partial" : "available" },
      { source: "prometheus", state: "unavailable" },
      { source: "victorialogs", state: "available" },
      { source: "alertmanager", state: "disabled" },
    ],
    signals,
  });
}

function filterFindings(url) {
  const selected = url.searchParams.get("cluster") ?? "local";
  const scope = clusters.filter((cluster) => selected === "*" ?
    ["environment", "business", "group"].every((key) => !url.searchParams.get(key) || cluster[key] === url.searchParams.get(key)) :
    selected === cluster.id);
  const instances = new Set(scope.map(({ id }) => id));
  return findings.filter((item) => {
    const severity = url.searchParams.get("severity");
    const namespace = url.searchParams.get("namespace");
    const kind = url.searchParams.get("kind");
    return instances.has(item.cluster) && (!severity || item.severity === severity) &&
      (!namespace || item.namespace === namespace) && (!kind || item.resource.kind === kind);
  });
}

function summarize(items) {
  const summary = { total: items.length, bySeverity: {}, byKind: {}, byNamespace: {} };
  for (const item of items) {
    summary.bySeverity[item.severity] = (summary.bySeverity[item.severity] ?? 0) + 1;
    summary.byKind[item.resource.kind] = (summary.byKind[item.resource.kind] ?? 0) + 1;
    summary.byNamespace[item.namespace] = (summary.byNamespace[item.namespace] ?? 0) + 1;
  }
  return summary;
}

function json(response, status, body) {
  response.writeHead(status, { "Content-Type": "application/json" });
  response.end(JSON.stringify(body));
}

function contentType(extension) {
  if (extension === ".html") return "text/html; charset=utf-8";
  if (extension === ".js") return "text/javascript; charset=utf-8";
  if (extension === ".css") return "text/css; charset=utf-8";
  return "application/octet-stream";
}
