import assert from "node:assert/strict";
import test from "node:test";
import { renderCatalog, resolveResourceTab } from "../src/views/resources/resource-management.ts";

test("resource navigation defaults to clusters and scopes explicit links", () => {
  const access = { clusters: true, environments: true, "business-lines": false };
  assert.equal(resolveResourceTab("#/resources", access), "clusters");
  assert.equal(resolveResourceTab("#/resources/environments", access), "environments");
  assert.equal(resolveResourceTab("#/resources/business-lines", access), undefined);
  assert.equal(resolveResourceTab("#/resources", { ...access, clusters: false }), "environments");
});

test("resource catalog renders an empty state and escapes stored names", () => {
  assert.match(renderCatalog("business-lines", []), /暂无业务线/);
  const content = renderCatalog("environments", [{ id: "development", name: '<研发>"' }]);
  assert.match(content, /&lt;研发&gt;&quot;/);
  assert.match(content, /data-entry-id="development"/);
  assert.match(content, /新增环境/);
});
