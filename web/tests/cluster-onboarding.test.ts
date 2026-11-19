import assert from "node:assert/strict";
import test from "node:test";
import { renderOnboarding } from "../src/views/clusters/cluster-onboarding.ts";
import { renderNamespaceOwnership } from "../src/views/clusters/namespace-ownership.ts";

test("shows the persisted first collection and protects an existing environment in the editor", async (t) => {
  t.mock.method(globalThis, "fetch", async (path: string) => {
    if (path.endsWith("/cluster-onboarding")) {
      return Response.json([{
        id: "dev-k8s", name: "开发集群", mode: "pull", enabled: true, environment: "旧研发",
        business: "云帆", credentialKind: "kubeconfig", firstCollectedAt: 1700000000,
        collection: {
          nodes: [{ name: "worker-1", ready: "False" }],
          namespaces: [{ name: "dev-app" }],
          nodeSampleAt: 1700000000,
        },
      }, { id: "old", name: "旧挂载集群", mode: "pull", enabled: true },
      { id: "local", name: "本地集群", mode: "local", enabled: true },
      { id: "push", name: "上报集群", mode: "push", enabled: true }]);
    }
    return Response.json(path.endsWith("/environments") ? [{ id: "development", name: "研发" }] :
      [{ id: "yunfan", name: "云帆" }]);
  });
  const list = await renderOnboarding();
  assert.match(list, /已接入 · 集群异常/);
  assert.match(list, /worker-1 · <strong>NotReady/);
  assert.match(list, /dev-app/);
  assert.match(list, /待补录凭据/);
  assert.match(list, /data-cluster-action="recheck" data-id="local"/);
  assert.doesNotMatch(list, /data-cluster-action="recheck" data-id="(?:old|push)"/);
  assert.match(await renderOnboarding("edit-local"), /data-collection-check="local"/);
  const edit = await renderOnboarding("edit-dev-k8s");
  assert.match(edit, /name="environment" value="旧研发"\s+required\s+readonly/);
  assert.match(edit, /重新校验/);
  assert.match(edit, /<option value="">沿用kubeconfig<\/option>/);
  const pending = await renderOnboarding("edit-old");
  assert.match(pending, /<option value="kubeconfig" selected>/);
  assert.doesNotMatch(pending, /重新校验|沿用/);
  const create = await renderOnboarding("pull");
  assert.match(create, /<select name="environment" required>/);
  assert.match(create, /<option value="研发">研发<\/option>/);
  assert.match(create, /直连凭据加密保存在 SQLite 中/);
  assert.match(create, /<option value="kubeconfig" selected>/);
  assert.match(create, /name="credentialKubeconfig" [^>]*required/);
  assert.match(create, /name="apiURL"[^>]*disabled/);
  assert.doesNotMatch(create, /部署挂载|服务端配置集群凭据加密密钥/);
});

test("dedicated ownership fixes the business line and only checks its collected namespaces", async (t) => {
  t.mock.method(globalThis, "fetch", async (path: string) => {
    if (path.endsWith("/business-lines")) {
      return Response.json([{ id: "team-a", name: "业务 A" }, { id: "team-b", name: "业务 B" }]);
    }
    return Response.json({
      namespaces: [{ name: "payments" }, { name: "jobs" }],
      assigned: { payments: ["team-a"], jobs: ["team-b"], retired: ["team-a"] },
      dedicatedBusinessId: "team-a",
    });
  });
  const editor = await renderNamespaceOwnership("dedicated", true);
  assert.match(editor, /业务线 <strong>业务 A<\/strong>/);
  assert.doesNotMatch(editor, /业务 B|<select|retired/);
  assert.match(editor, /value="payments"[^>]+ checked>/);
  assert.match(editor, /value="jobs"[^>]+>/);
  assert.doesNotMatch(editor, /value="jobs"[^>]+ checked>/);
  assert.match(editor, /role="region" aria-label="命名空间列表" tabindex="0"/);
  assert.match(editor, /type="submit">保存归属/);
});

test("shows failed rechecks before cached Ready nodes and restores health after a successful sample", async (t) => {
  const collection = {
    nodes: [{ name: "worker-1", ready: "True" }],
    nodeSampleAt: 1700000000,
    nodeFailures: 0,
    nodeError: "",
  };
  t.mock.method(globalThis, "fetch", async () => Response.json([{
    id: "dev-k8s", name: "开发集群", mode: "pull", enabled: true, credentialKind: "kubeconfig",
    firstCollectedAt: 1700000000, checkedAt: 1700000030, collection,
  }]));

  assert.match(await renderOnboarding(), /data-status="已接入 · 当前集群正常"/);
  collection.nodeError = "节点读取失败，请检查网络、TLS 和只读凭据";
  const failed = await renderOnboarding();
  assert.match(failed, /data-status="已接入 · 节点校验失败"/);
  assert.doesNotMatch(failed, /当前集群正常/);
  assert.match(failed, /节点读取失败，请检查网络、TLS 和只读凭据/);
  assert.match(failed, /上次成功采样时间/);
  assert.match(failed, /aria-label="历史节点快照"[^>]*><li>worker-1 · <strong>Ready<\/strong>/);

  collection.nodeFailures = 2;
  assert.match(await renderOnboarding(), /data-status="已接入 · 节点数据已过期"/);

  collection.nodeSampleAt = 1700000060;
  collection.nodeFailures = 0;
  collection.nodeError = "";
  const recovered = await renderOnboarding();
  assert.match(recovered, /data-status="已接入 · 当前集群正常"/);
  assert.doesNotMatch(recovered, /节点校验失败|节点数据已过期|历史节点快照/);
});
