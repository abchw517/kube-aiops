import assert from "node:assert/strict";
import test from "node:test";
import { renderOnboarding } from "../src/views/clusters/cluster-onboarding.ts";

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
      }, { id: "old", name: "旧挂载集群", mode: "pull", enabled: true }]);
    }
    return Response.json(path.endsWith("/environments") ? [{ id: "development", name: "研发" }] :
      [{ id: "yunfan", name: "云帆" }]);
  });
  const list = await renderOnboarding();
  assert.match(list, /已接入 · 集群异常/);
  assert.match(list, /worker-1 · <strong>NotReady/);
  assert.match(list, /dev-app/);
  assert.match(list, /待补录凭据/);
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
