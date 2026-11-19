import assert from "node:assert/strict";
import { test } from "node:test";
import {
  auditLocation, maxRetentionDays, newlyDisabledAuditLevels, parseAuditRoute, readAuditRetention,
  type AuditOperation,
} from "../src/views/permissions/audit-data.ts";
import { renderAuditBatch, renderAuditDetail, renderAuditWorkspace } from "../src/views/permissions/audit-view.ts";

test("audit return locations accept only the fixed route and supported filters", () => {
  const filters = { account: "研发?&", action: "授权", risk: "低", batch: "batch-fixture", before: "42" };
  assert.deepEqual(parseAuditRoute(auditLocation(filters)), filters);
  assert.equal(parseAuditRoute("#/admin/operations/https://example.com"), undefined);
  assert.equal(parseAuditRoute("https://example.com/#/admin/operations"), undefined);
  assert.deepEqual(parseAuditRoute("#/admin/operations?before=-1&risk=未知&redirect=https://example.com"), {});
  assert.equal(auditLocation({ ...filters, before: undefined, batch: undefined }).includes("undefined"), false);
});

test("audit retention accepts zero and requires confirmation only for newly disabled tiers", () => {
  const fields = new URLSearchParams({ low: "0", medium: "5", high: String(maxRetentionDays) });
  const next = { low: 0, medium: 5, high: maxRetentionDays };
  assert.deepEqual(readAuditRetention(fields), next);
  assert.deepEqual(newlyDisabledAuditLevels({ low: 3, medium: 0, high: 7 }, next), ["低风险"]);
  assert.deepEqual(newlyDisabledAuditLevels({ low: 0, medium: 5, high: 7 }, next), []);
  for (const value of ["", "-1", "1.5", String(maxRetentionDays + 1)]) {
    fields.set("high", value);
    assert.equal(readAuditRetention(fields), undefined);
  }
});

test("audit views keep retention read-only for administrators and escape event details", () => {
  const item: AuditOperation = { id: 42, at: Date.parse("2026-11-18T07:43:19Z") / 1000, actor: "<admin>",
    action: "批量授予", risk: "高", target: "<employee>", result: "失败", reason: "<policy violation>", batch: "fixture-batch" };
  const page = { items: [item], before: 0, retention: { low: 0, medium: 5, high: 7 } };
  const view = renderAuditWorkspace(page, page, {}, false);
  assert.match(view, /data-audit-days="low">不保留/);
  assert.match(view, /role="region" aria-labelledby="audit-records-heading" tabindex="0"/);
  assert.doesNotMatch(view, /data-audit-edit|<admin>|<employee>/);
  assert.match(renderAuditWorkspace(page, page, {}, true), /data-audit-edit/);
  assert.match(renderAuditDetail(item), /2026年11月18日15:43:19/);
  assert.match(renderAuditDetail(item), /&lt;policy violation&gt;/);
  assert.match(renderAuditBatch({ items: [{ ...item, target: "批量操作" }, item], before: 0 }), /&lt;employee&gt;/);
  assert.doesNotMatch(renderAuditBatch({ items: [{ ...item, target: "批量操作" }, item], before: 0 }), /批量操作|<policy violation>/);
});
