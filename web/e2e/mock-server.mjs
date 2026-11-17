import { createServer } from "node:http";
import { readFile } from "node:fs/promises";
import { extname, normalize } from "node:path";

const port = Number(process.env.PORT ?? "4173");
const root = new URL("../dist/", import.meta.url);

const longRegistryURL = `https://registry.example.com/v2/library/${"registry-path".repeat(28)}/manifests/1.36?ns=docker.io`;

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
  }
];

let warningEvidenceRequests = 0;
let authenticationRequired = false;
let authenticated = false;
let adminSession = false;
let role;

createServer(async (request, response) => {
  const url = new URL(request.url ?? "/", `http://127.0.0.1:${port}`);
  if (url.pathname === "/test/admin-session") {
    adminSession = true;
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
  if (url.pathname === "/api/v1/wiki/knowledge") {
    return json(response, 200, {items: [wikiDoc], filters: {environment: ["测试"], business: ["运维"], documentType: ["排障手册"]}, page: 1, total: 1, pageSize: 20});
  }
  if (url.pathname === `/api/v1/wiki/knowledge/${wikiID}/revisions/1`) {
    return json(response, 200, {...wikiDoc, body: "验证仓库权限"});
  }
  if (url.pathname === "/api/v1/admin/status" && adminSession) return json(response, 200, { admin: true, findingAI: true });
  if (url.pathname === "/api/v1/admin/finding-analysis" && adminSession && url.searchParams.get("id") === "finding-critical") {
    if (request.method === "GET") return json(response, 200, { original: "原始诊断结论", history: [] });
    const chunks = [];
    for await (const chunk of request) chunks.push(chunk);
    const input = JSON.parse(Buffer.concat(chunks).toString());
    const transcript = [
      { role: "user", text: "Pod 事件显示镜像仓库拒绝访问" },
      { role: "assistant", text: "请补充镜像仓库返回的状态码。" },
      { role: "user", text: "仓库返回 403" },
      { role: "assistant", text: "403 支持访问被拒绝，请核实拉取凭据。" },
    ];
    const matches = (length) => JSON.stringify(input.messages) === JSON.stringify(transcript.slice(0, length));
    if (input.revision === 0 && input.action === "chat") {
      if (matches(1)) return json(response, 200, { reply: transcript[1].text });
      if (matches(3)) return json(response, 200, { reply: transcript[3].text });
    }
    if (input.action === "enhance") return json(response, 200, {conclusion: "增强诊断案例", citations: []});
    if (input.revision === 0 && input.action === "summarize" && matches(4)) {
      return json(response, 200, {conclusion: "镜像仓库拒绝访问", draftId: "fixture-draft"});
    }
    if (input.revision === 0 && input.action === "confirm" && input.draftId === "fixture-draft") {
      return json(response, 200, { revision: 1, conclusion: "更新结论：镜像仓库拒绝访问" });
    }
    return json(response, 400, { error: "invalid fixture evidence" });
  }
  if (url.pathname === "/api/v1/admin/cluster-onboarding" && adminSession) {
    if (request.method === "GET") return json(response, 200, []);
    const chunks = [];
    for await (const chunk of request) chunks.push(chunk);
    const input = JSON.parse(Buffer.concat(chunks).toString());
    if (input.id === "dev-yunfan-01" && input.environment === "研发" && input.business === "云帆" &&
        input.group === "应用集群" && input.mode === "push" && input.enabled) {
      return json(response, 200, { token: "fixture-report-token" });
    }
    return json(response, 400, { error: "invalid onboarding fixture" });
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
  if (url.pathname === "/api/v1/clusters") return json(response, 200, { items: [{ id: "local", name: "local", status: "ready" }] });
  if (url.pathname === "/api/v1/clusters/local/namespaces") return json(response, 200, { items: [{ name: "default" }, { name: "payments" }] });
  if (url.pathname === "/api/v1/findings/summary") {
    const filtered = filterFindings(url);
    return json(response, 200, summarize(filtered));
  }
  if (url.pathname === "/api/v1/findings") {
    return json(response, 200, { items: filterFindings(url), pagination: { limit: 50 } });
  }
  const correlationPath = url.pathname.match(/^\/api\/v1\/findings\/([^/]+)\/correlation$/);
  if (correlationPath) return correlationResponse(response, decodeURIComponent(correlationPath[1]));
  if (url.pathname.startsWith("/api/v1/findings/")) {
    const id = decodeURIComponent(url.pathname.split("/").at(-1) ?? "");
    const finding = findings.find((item) => item.id === id) ??
      (["finding-auth", "finding-forbidden"].includes(id) ? { ...findings[0], id } : undefined);
    return finding ? json(response, 200, finding) : json(response, 404, { error: { code: "FINDING_NOT_FOUND", message: "finding not found" } });
  }

  try {
    const requested = url.pathname === "/" ? "index.html" : normalize(url.pathname).replace(/^\/+/, "");
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

function correlationResponse(response, id) {
  if (id === "finding-auth") return json(response, 401, { error: { code: "UNAUTHORIZED" } });
  if (id === "finding-forbidden") return json(response, 403, { error: { code: "FORBIDDEN" } });
  const finding = findings.find((item) => item.id === id);
  if (!finding) return json(response, 404, { error: { code: "FINDING_NOT_FOUND" } });
  if (id === "finding-warning" && ++warningEvidenceRequests === 1) {
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
  return findings.filter((item) => {
    const severity = url.searchParams.get("severity");
    const namespace = url.searchParams.get("namespace");
    const kind = url.searchParams.get("kind");
    return (!severity || item.severity === severity) && (!namespace || item.namespace === namespace) && (!kind || item.resource.kind === kind);
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
