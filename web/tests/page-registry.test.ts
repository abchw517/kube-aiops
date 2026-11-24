import assert from "node:assert/strict";
import { readFileSync } from "node:fs";
import test from "node:test";
import { pageTrail, permissionTrail, portalPages } from "../src/domain/page-registry.ts";
import { resolvePortalNavigation, type PortalNavigationState } from "../src/domain/portal-navigation.ts";
import { projectPermissionCatalog, type PermissionCatalog } from "../src/views/permissions/permission-catalog.ts";

const catalog = JSON.parse(readFileSync(new URL("../../api/internal/permissions/catalog/catalog.json", import.meta.url), "utf8")) as PermissionCatalog;
const state: PortalNavigationState = {
  hash: "#/resources/datasources", demoMode: false, identityAdmin: false, permissionCatalog: false,
  auditRead: false, resourceAccess: { clusters: true, environments: false, "business-lines": false, datasources: true },
  wikiGovernance: false, mustChangePassword: false, canChangePassword: false, signedOut: false,
};

test("every current feature has a registered page and retains the original authorization operations", () => {
  const projected = projectPermissionCatalog(catalog);
  assert.equal(projected.rows.length, catalog.rows.length);
  for (const row of catalog.rows) {
    const mapped = projected.rows.find(({ key }) => key === row.key);
    assert.ok(mapped && permissionTrail(row.key).length > 1, row.name);
    assert.deepEqual(mapped.points, row.points);
    assert.equal(mapped.scope, row.scope);
  }
  assert.equal(projected.rows.find(({ key }) => key === "connector")?.module, "resources");
  assert.equal(projected.rows.find(({ key }) => key === "cluster")?.name, "Kubernetes 集群管理");
});

test("registry rename and move changes menu and permissions together without widening visibility", () => {
  const page = portalPages["resource.datasources"];
  assert.ok(page);
  const registry = { ...portalPages, [page.id]: { ...page, label: "采集数据源", parentId: "group.diagnosis.settings" } };
  const section = resolvePortalNavigation(state, registry);
  assert.equal(section.module, "diagnostics");
  assert.equal(section.title, "采集数据源");
  assert.ok(section.groups.some(({ label, links }) => label === "诊断配置" && links.some(link => link.label === section.title && link.active)));
  const hidden = resolvePortalNavigation({ ...state, resourceAccess: { ...state.resourceAccess, datasources: false } }, registry);
  assert.ok(!hidden.groups.flatMap(group => group.links).some(link => link.label === section.title));
  const projection = projectPermissionCatalog(catalog, registry);
  const connector = projection.rows.find(({ key }) => key === "connector");
  assert.equal(connector?.location, "诊断控制台 › 诊断配置 › 采集数据源");
  assert.equal(connector?.legacyName, "数据源管理");
  assert.equal(connector?.points[0]?.code, "diagnosis.connector.read");
});

test("an invalid or missing page path remains discoverable without changing the existing grant", () => {
  const page = portalPages["resource.datasources"];
  assert.ok(page);
  const registry = { ...portalPages, [page.id]: { ...page, parentId: page.id } };
  assert.deepEqual(pageTrail(page.id, registry), []);
  const row = projectPermissionCatalog(catalog, registry).rows.find(({ key }) => key === "connector");
  assert.equal(row?.location, "未关联页面");
  assert.equal(row?.points[0]?.code, "diagnosis.connector.read");
});
