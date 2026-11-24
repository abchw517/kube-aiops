import assert from "node:assert/strict";
import test from "node:test";
import { resolveResourceTab } from "../src/domain/resource-routes.ts";
import { renderCatalog } from "../src/views/resources/resource-management.ts";

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


test("catalog shows compact rows with IDs and one add action after the table", () => {
  const content = renderCatalog("environments", [
    { id: "development", name: "研发" }, { id: "Legacy_ID-7", name: "旧环境" },
  ]);
  assert.match(content, /环境 ID/);
  assert.match(content, /<code>Legacy_ID-7<\/code>/);
  assert.equal((content.match(/data-catalog-action="edit"/g) ?? []).length, 2);
  assert.equal((content.match(/data-catalog-action="add"/g) ?? []).length, 1);
  assert.ok(content.indexOf("</table>") < content.indexOf('data-catalog-action="add"'));
  assert.doesNotMatch(content, /<input/);
});
