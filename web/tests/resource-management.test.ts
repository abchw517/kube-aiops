import assert from "node:assert/strict";
import test from "node:test";
import { resolveResourceTab } from "../src/domain/resource-routes.ts";
import { filterCatalog, referenceLabel, validateCatalog } from "../src/views/resources/catalog-model.ts";
import { renderCatalog, type CatalogPage } from "../src/views/resources/catalog-workspace.ts";

const access = { clusters: true, environments: true, "business-lines": true };

test("resource navigation defaults to clusters and scopes explicit links", () => {
  const scoped = { ...access, "business-lines": false };
  assert.equal(resolveResourceTab("#/resources", scoped), "clusters");
  assert.equal(resolveResourceTab("#/resources/environments", scoped), "environments");
  assert.equal(resolveResourceTab("#/resources/business-lines", scoped), undefined);
  assert.equal(resolveResourceTab("#/resources", { ...scoped, clusters: false }), "environments");
});

const page: CatalogPage = { kind: "environments", items: [], failed: false, editable: true, access };

test("catalog separates empty, failed and read-only states and escapes stored names", () => {
  assert.match(renderCatalog({ ...page, kind: "business-lines" }), /暂无业务线/);
  assert.match(renderCatalog({ ...page, failed: true }), /目录加载失败.*重新加载/s);
  const content = renderCatalog({ ...page, editable: false, access: { ...access, "business-lines": false },
    items: [{ id: "development", name: '<研发>"' }] });
  assert.match(content, /&lt;研发&gt;&quot;/);
  assert.match(content, /data-entry-id="development"/);
  assert.match(content, /只读权限/);
  assert.match(content, /data-catalog-action="copy"/);
  assert.doesNotMatch(content, /data-catalog-action="(?:edit|delete|add)"|业务线管理/);
});

test("catalog shows compact rows with the add action ahead of the table", () => {
  const content = renderCatalog({ ...page, items: [{ id: "development", name: "研发" }, { id: "Legacy_ID-7", name: "旧环境" }] });
  assert.match(content, /环境 ID/);
  assert.match(content, /<code>Legacy_ID-7<\/code>/);
  assert.equal((content.match(/data-catalog-action="edit"/g) ?? []).length, 2);
  assert.equal((content.match(/data-catalog-action="add"/g) ?? []).length, 1);
  assert.ok(content.indexOf('data-catalog-action="add"') < content.indexOf("<table"));
});

test("catalog filters by name or ID and sorts a copy of the directory", () => {
  const items = [{ id: "p-10", name: "生产" }, { id: "p-2", name: "预发布" }, { id: "sandbox", name: "沙盒" }];
  assert.deepEqual(filterCatalog(items, { query: " P-", sort: "id" }).map(row => row.id), ["p-2", "p-10"]);
  assert.deepEqual(filterCatalog(items, { query: "沙盒", sort: "sequence" }).map(row => row.id), ["sandbox"]);
  assert.equal(items[0]?.id, "p-10");
});

test("catalog validation preserves an unchanged legacy ID and checks new identifiers", () => {
  const legacy = { id: "Legacy_ID", name: "旧环境" };
  const entries = [legacy, { id: "prod", name: "生产" }];
  assert.equal(validateCatalog(entries, { ...legacy, name: "改名" }, legacy), undefined);
  assert.equal(validateCatalog(entries, { id: "LEGACY", name: "新环境" })?.field, "id");
  assert.equal(validateCatalog(entries, { id: "a".repeat(129), name: "环境" })?.field, "id");
  assert.equal(validateCatalog(entries, { id: "prod", name: "环境" })?.field, "id");
  assert.equal(validateCatalog(entries, { id: "new", name: "生产" })?.field, "name");
  assert.equal(validateCatalog(entries, { id: "new", name: "😀".repeat(128) }), undefined);
  assert.equal(validateCatalog(entries, { id: "new", name: "新\u0000环境" })?.field, "name");
  assert.deepEqual(["角色 运维", "prod/ops", "prod", "legacy other:X"].map(referenceLabel),
    ["角色范围", "命名空间归属", "集群", "关联引用"]);
});
