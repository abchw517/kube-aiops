import assert from "node:assert/strict";
import test from "node:test";
import { resolvePortalNavigation, type PortalNavigationState } from "../src/domain/portal-navigation.ts";

const state: PortalNavigationState = {
  hash: "#/diagnostics",
  demoMode: false,
  identityAdmin: false,
  permissionCatalog: false,
  auditRead: false,
  resourceAccess: { clusters: false, environments: true, "business-lines": false },
  wikiGovernance: false,
  mustChangePassword: false,
  canChangePassword: false,
  signedOut: false,
};

test("the resource landing page uses the first allowed page and leaves denied deep links unselected", () => {
  const landing = resolvePortalNavigation({ ...state, hash: "#/resources?x=1" });
  assert.equal(landing.title, "环境管理");
  assert.equal(landing.modules.find(({ active }) => active)?.href, "#/resources");
  assert.deepEqual(landing.groups.flatMap(({ links }) => links), [
    { href: "#/resources/environments", label: "环境管理", active: true },
  ]);
  const direct = resolvePortalNavigation({ ...state, hash: "#/resources/clusters" });
  assert.ok(direct.groups.every(({ links }) => links.every(({ active }) => !active)));
});

test("audit-only accounts can open audit, while a required password change hides workspaces", () => {
  const audit = resolvePortalNavigation({ ...state, hash: "#/admin/operations", auditRead: true });
  assert.equal(audit.modules.find(({ active }) => active)?.href, "#/admin/operations");
  assert.equal(audit.groups[0]?.links[0]?.label, "操作审计");
  const password = resolvePortalNavigation({ ...state, auditRead: true, mustChangePassword: true });
  assert.equal(password.title, "修改密码");
  assert.deepEqual(password.modules, []);
  assert.deepEqual(password.groups, []);
});
