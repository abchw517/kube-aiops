import assert from "node:assert/strict";
import test from "node:test";
import { renderPlatform } from "../src/views/permissions/platform-admin.ts";

test("super administrators can assign all direct roles while other administrators only read privileged LDAP accounts", async (t) => {
  t.mock.method(globalThis, "fetch", async (path: string) => Response.json(path.endsWith("/accounts") ? [{
    id: "ldap:reader", username: "reader", kind: "ldap", groups: [], roles: ["system-admin", "diagnosis-reader"],
    disabled: false, directoryDisabled: false, mustChangePassword: false, effective: null,
  }] : { groups: [], roles: [
    { id: "diagnosis-reader", name: "诊断观察员", scopes: [] },
    { id: "namespace-auditor", name: "命名空间审计员", scopes: [] },
    { id: "system-admin", name: "系统管理员", fixed: true, scopes: [] },
  ] }));

  const superView = await renderPlatform("users", undefined, true);
  assert.match(superView, /data-kind="ldap" data-account-name="reader" data-account-deletable="true"/);
  assert.match(superView, /全局管理与诊断查看/);
  assert.match(superView, /data-system-open="assign"/);
  assert.equal([...superView.matchAll(/name="roles" value="system-admin"/g)].length, 2);
  assert.match(superView, /管理员角色发生变更时，目标账号需重新登录/);
  assert.doesNotMatch(superView, /data-system-open="reset"/);

  const adminView = await renderPlatform("users", undefined, false);
  assert.match(adminView, /data-kind="ldap" data-account-name="reader" data-account-deletable="false"/);
  assert.match(adminView, /name="ids" value="ldap:reader" aria-label="选择 reader" disabled/);
  assert.match(adminView, /全局管理与诊断查看/);
  assert.match(adminView, /name="roles" value="diagnosis-reader"/);
  assert.match(adminView, /name="roles" value="namespace-auditor"/);
  assert.doesNotMatch(adminView, /name="roles" value="system-admin"|data-system-open="(?:assign|account-action|reset)"/);
});
