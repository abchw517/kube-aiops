import assert from "node:assert/strict";
import test from "node:test";
import { renderOnboarding } from "../src/views/clusters/cluster-onboarding.ts";

test("shows the persisted first collection and protects an existing environment in the editor", async (t) => {
  t.mock.method(globalThis, "fetch", async (path: string) => {
    if (path.endsWith("/cluster-onboarding")) {
      return Response.json([{
        id: "dev-k8s", name: "开发集群", mode: "pull", enabled: true, environment: "旧研发",
        business: "云帆", firstCollectedAt: 1700000000,
        collection: {
          nodes: [{ name: "worker-1", ready: "False" }],
          namespaces: [{ name: "dev-app" }],
          nodeSampleAt: 1700000000,
        },
      }]);
    }
    return Response.json(path.endsWith("/environments") ? [{ id: "development", name: "研发" }] :
      [{ id: "yunfan", name: "云帆" }]);
  });
  const list = await renderOnboarding();
  assert.match(list, /已接入 · 集群异常/);
  assert.match(list, /worker-1 · <strong>NotReady/);
  assert.match(list, /dev-app/);
  const edit = await renderOnboarding("edit-dev-k8s");
  assert.match(edit, /name="environment" value="旧研发"\s+required\s+readonly/);
  assert.match(edit, /重新校验/);
  const create = await renderOnboarding("pull");
  assert.match(create, /<select name="environment" required>/);
  assert.match(create, /<option value="研发">研发<\/option>/);
  assert.match(create, /测试连接临时使用新填写的凭据。保存新凭据需要服务端配置集群凭据加密密钥。/);
});
