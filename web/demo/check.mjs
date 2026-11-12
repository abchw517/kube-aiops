import assert from "node:assert/strict";

const [origin] = process.argv.slice(2);
async function get(path) {
  const response = await fetch(`${origin}${path}`, { signal: AbortSignal.timeout(8_000) });
  assert.equal(response.status, 200, path);
  return response;
}

const html = await (await get("/")).text();
assert.match(html, /name="kube-aiops-demo" content="true"/);
await get("/assets/portal.js");
await get("/assets/portal.css");
await get("/readyz");

const findings = await (await get("/api/v1/findings")).json();
assert.equal(findings.items.length, 2);
const filtered = await (await get("/api/v1/findings?namespace=payments")).json();
assert.deepEqual(filtered.items.map(({ id }) => id), ["demo-checkout-crash"]);
const summary = await (await get("/api/v1/findings/summary")).json();
assert.equal(summary.total, 2);
const namespaces = await (await get("/api/v1/clusters/local/namespaces")).json();
assert.deepEqual(namespaces.items.map(({ name }) => name).sort(), ["payments", "platform"]);
const finding = await (await get("/api/v1/findings/demo-checkout-crash")).json();
assert.equal(finding.resource.name, "checkout-api");

const evidence = await (await get("/api/v1/findings/demo-checkout-crash/correlation")).json();
const expected = ["alertmanager", "kubernetes-events", "prometheus", "victorialogs"];
assert.deepEqual(evidence.sources.map(({ source }) => source).sort(), expected);
assert(evidence.sources.every(({ state }) => state === "available"));
assert.deepEqual([...new Set(evidence.signals.map(({ source }) => source))].sort(), expected);
const deployment = await (await get("/api/v1/findings/demo-worker-replicas/correlation")).json();
assert(deployment.signals.some(({ name, value }) => name === "workload_ready_replicas" && value === 1));
assert.equal(deployment.signals.some(({ source }) => source === "victorialogs"), false);
const worker = await (await get("/api/v1/clusters/local/resources/deployment/platform/queue-worker")).json();
assert.equal(worker.status.readyReplicas, 1);
const write = await fetch(`${origin}/api/v1/findings`, { method: "POST", signal: AbortSignal.timeout(8_000) });
assert.equal(write.status, 405);
console.log("Portal demo smoke: PASS (real API, two findings, four evidence sources)");
