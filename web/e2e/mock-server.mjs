import { createServer } from "node:http";
import { readFile } from "node:fs/promises";
import { extname, normalize } from "node:path";

const port = Number(process.env.PORT ?? "4173");
const root = new URL("../dist/", import.meta.url);

const findings = [
  {
    id: "finding-critical",
    cluster: "local",
    namespace: "default",
    severity: "critical",
    resource: { apiVersion: "v1", kind: "Pod", namespace: "default", name: "api-0" },
    problem: "CrashLoopBackOff detected",
    details: "Container restart backoff is increasing. <img id=\"sanitizer-xss-probe\" src=x onerror=\"globalThis.__kubeAiopsXss=1\"> javascript:alert(1)",
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

createServer(async (request, response) => {
  const url = new URL(request.url ?? "/", `http://127.0.0.1:${port}`);
  if (url.pathname === "/api/v1/clusters") return json(response, 200, { items: [{ id: "local", name: "local", status: "ready" }] });
  if (url.pathname === "/api/v1/clusters/local/namespaces") return json(response, 200, { items: [{ name: "default" }, { name: "payments" }] });
  if (url.pathname === "/api/v1/findings/summary") {
    const filtered = filterFindings(url);
    return json(response, 200, summarize(filtered));
  }
  if (url.pathname === "/api/v1/findings") {
    return json(response, 200, { items: filterFindings(url), pagination: { limit: 50 } });
  }
  if (url.pathname.startsWith("/api/v1/findings/")) {
    const id = decodeURIComponent(url.pathname.split("/").at(-1) ?? "");
    const finding = findings.find((item) => item.id === id);
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
