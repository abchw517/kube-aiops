import { readFile, writeFile } from "node:fs/promises";
import { createServer as createHTTPServer } from "node:http";
import { createServer as createHTTPSServer } from "node:https";
import { join } from "node:path";
import { fixtureResponse } from "./fixtures.mjs";

const [workDir, webHost, webPort, apiPort] = process.argv.slice(2);
const loopbackHost = "127.0.0.1";
const apiOrigin = `http://${loopbackHost}:${apiPort}`;
const dist = new URL("../dist/", import.meta.url);
const [certificate, key, token, index, script, styles] = await Promise.all([
  readFile(join(workDir, "ca.pem")),
  readFile(join(workDir, "key.pem")),
  readFile(join(workDir, "token"), "utf8"),
  readFile(new URL("index.html", dist), "utf8"),
  readFile(new URL("assets/portal.js", dist)),
  readFile(new URL("assets/portal.css", dist)),
]);

const files = new Map([
  ["/", ["text/html; charset=utf-8", index.replace("</head>", '<meta name="kube-aiops-demo" content="true" /></head>')]],
  ["/assets/portal.js", ["text/javascript; charset=utf-8", script]],
  ["/assets/portal.css", ["text/css; charset=utf-8", styles]],
]);

function send(response, status, body, contentType = "application/json", extraHeaders = {}) {
  response.writeHead(status, {
    "Content-Type": contentType,
    "Cache-Control": "no-store",
    "X-Content-Type-Options": "nosniff",
    "Content-Security-Policy": "default-src 'self'; object-src 'none'; frame-ancestors 'none'; base-uri 'none'",
    ...extraHeaders,
  });
  response.end(body);
}

function rejectWrite(request, response) {
  if (request.method === "GET") return false;
  send(response, 405, '{"error":"method not allowed"}', "application/json", { Allow: "GET" });
  return true;
}

const upstream = createHTTPSServer({ cert: certificate, key }, (request, response) => {
  if (rejectWrite(request, response)) return;
  const url = new URL(request.url, `https://${loopbackHost}`);
  const kubernetesPath = url.pathname.startsWith("/apis/") || url.pathname.startsWith("/api/v1/namespaces");
  if (kubernetesPath && request.headers.authorization !== `Bearer ${token.trim()}`) {
    send(response, 401, '{"error":"unauthorized"}');
    return;
  }
  const fixture = fixtureResponse(url);
  if (fixture === undefined) {
    send(response, 404, '{"error":"not found"}');
    return;
  }
  send(response, 200, JSON.stringify(fixture));
});

const portal = createHTTPServer(async (request, response) => {
  if (rejectWrite(request, response)) return;
  const url = new URL(request.url, `http://${loopbackHost}`);
  const file = files.get(url.pathname);
  if (file) {
    send(response, 200, file[1], file[0]);
    return;
  }
  if (url.pathname !== "/readyz" && !url.pathname.startsWith("/api/v1/")) {
    send(response, 404, '{"error":"not found"}');
    return;
  }
  try {
    const backend = await fetch(`${apiOrigin}${url.pathname}${url.search}`, {
      headers: { Accept: "application/json" },
      redirect: "error",
      signal: AbortSignal.timeout(8_000),
    });
    send(response, backend.status, await backend.text());
  } catch {
    send(response, 502, '{"error":"demo backend unavailable"}');
  }
});

function listen(server, port, host) {
  return new Promise((resolve, reject) => {
    server.once("error", reject);
    server.listen(port, host, () => {
      server.removeListener("error", reject);
      resolve(server.address().port);
    });
  });
}

await listen(portal, Number(webPort), webHost);
const upstreamPort = await listen(upstream, 0, loopbackHost);
await writeFile(join(workDir, "upstream-url"), `https://${loopbackHost}:${upstreamPort}`, { mode: 0o600 });
