import assert from "node:assert/strict";
import test from "node:test";
import { invalidateLDAPDraft, submitIdentityAdmin } from "../src/views/permissions/identity-admin.ts";

test("LDAP activation enables save only for the tested draft and omits the employee password", async (t) => {
  class Input {
    name: string;
    value: string;
    placeholder = "";
    focus() {}

    constructor(name: string, value: string) {
      this.name = name;
      this.value = value;
    }
  }
  const data = {
    url: new Input("url", "ldaps://directory.example.com"),
    bindPassword: new Input("bindPassword", "fixture-bind"),
    testUsername: new Input("testUsername", "employee"),
    testPassword: new Input("testPassword", "fixture-employee"),
  };
  class FixtureFormData extends Map<string, string> {
    constructor() {
      super(Object.values(data).map(({ name, value }) => [name, value]));
    }
  }
  const originalFormData = Object.getOwnPropertyDescriptor(globalThis, "FormData");
  Object.defineProperty(globalThis, "FormData", { value: FixtureFormData, configurable: true });
  t.after(() => {
    if (originalFormData) Object.defineProperty(globalThis, "FormData", originalFormData);
  });
  Object.defineProperty(globalThis, "HTMLInputElement", { value: Input, configurable: true });
  t.after(() => Reflect.deleteProperty(globalThis, "HTMLInputElement"));
  const save = { disabled: true };
  const verify = { disabled: false };
  const status = { textContent: "" };
  const hint = { textContent: "" };
  const form = {
    dataset: { identityResource: "ldap" },
    querySelectorAll: () => [save, verify],
    querySelector: (selector: string) => {
      if (selector.includes("testUsername")) return data.testUsername;
      if (selector.includes("testPassword")) return data.testPassword;
      if (selector.includes("bindPassword")) return data.bindPassword;
      if (selector.includes("data-ldap-save-hint")) return hint;
      if (selector.includes("data-ldap-test-status")) return status;
      return save;
    },
    closest: () => null,
  } as unknown as HTMLFormElement;
  let testAttempt = 0;
  const requests: { path: string; body: Record<string, unknown> }[] = [];
  t.mock.method(globalThis, "fetch", async (path: string, init: RequestInit) => {
    requests.push({ path, body: JSON.parse(String(init.body)) });
    if (!path.endsWith("ldap-test")) return new Response(null, { status: 204 });
    testAttempt++;
    if (testAttempt === 1) return Response.json({ stage: "connection" });
    if (testAttempt === 2) throw new Error("fixture connection unavailable");
    return Response.json({ stage: "success", verification: "test-proof" });
  });

  await submitIdentityAdmin(form, () => {});
  assert.equal(requests.length, 0);
  assert.match(hint.textContent, /四项验证全部通过后可保存/);
  data.testPassword.value = "";
  await submitIdentityAdmin(form, () => {}, true);
  assert.equal(requests.length, 0);
  assert.match(status.textContent, /请输入 LDAP 测试员工密码/);
  data.testPassword.value = "fixture-employee";
  await submitIdentityAdmin(form, () => {}, true);
  assert.match(status.textContent, /连接失败/);
  assert.equal(save.disabled, true);
  assert.equal(data.testUsername.value, "employee");
  assert.equal(data.testPassword.value, "fixture-employee");

  await submitIdentityAdmin(form, () => {}, true);
  assert.match(status.textContent, /无法运行 LDAP 测试/);
  assert.equal(save.disabled, true);
  assert.equal(data.testUsername.value, "employee");
  assert.equal(data.testPassword.value, "fixture-employee");

  await submitIdentityAdmin(form, () => {}, true);
  assert.deepEqual(requests[2]?.body, requests[0]?.body);
  assert.equal(data.testUsername.value, "");
  assert.equal(data.testPassword.value, "");
  assert.equal(save.disabled, false);
  assert.match(hint.textContent, /四项验证已通过/);

  data.url.value = "ldaps://changed.example.com";
  invalidateLDAPDraft(form, data.url as unknown as EventTarget);
  assert.equal(save.disabled, true);
  await submitIdentityAdmin(form, () => {});
  assert.equal(requests.length, 3);

  data.testUsername.value = "employee";
  data.testPassword.value = "fixture-employee";
  await submitIdentityAdmin(form, () => {}, true);
  await submitIdentityAdmin(form, () => {});
  assert.equal(requests.at(-1)?.path, "/api/v1/admin/ldap");
  assert.deepEqual(requests.at(-1)?.body, {
    url: "ldaps://changed.example.com", bindPassword: "fixture-bind", verification: "test-proof",
  });
  assert.equal(data.bindPassword.value, "");
  assert.equal(hint.textContent, "已保存");
  assert.equal(save.disabled, true);
});
