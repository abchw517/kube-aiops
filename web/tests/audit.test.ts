import assert from "node:assert/strict";
import { test } from "node:test";
import {
  auditLocation, auditPageCount, maxRetentionDays, newlyDisabledAuditLevels, parseAuditRoute, readAuditLimit, readAuditRetention,
  type AuditOperation,
} from "../src/views/permissions/audit-data.ts";
import { renderAuditBatch, renderAuditDetail, renderAuditWorkspace } from "../src/views/permissions/audit-view.ts";

test("audit return locations accept only the fixed route and supported filters", () => {
  const filters = { account: "研发?&", action: "授权", risk: "低", batch: "batch-fixture", page: 2, limit: 50 } as const;
  assert.deepEqual(parseAuditRoute(auditLocation(filters)), filters);
  assert.equal(parseAuditRoute("#/admin/operations/https://example.com"), undefined);
  assert.equal(parseAuditRoute("https://example.com/#/admin/operations"), undefined);
  assert.deepEqual(parseAuditRoute("#/admin/operations?page=-1&limit=101&before=42&risk=未知&redirect=https://example.com"), {});
  assert.equal(auditLocation({ ...filters, page: undefined, batch: undefined }).includes("undefined"), false);
  assert.equal(auditPageCount(76, 20), 4);
  assert.equal(auditPageCount(0, 100), 1);
  assert.equal(readAuditLimit("13"), 13);
  assert.deepEqual(parseAuditRoute("#/admin/operations?limit=13"), { limit: 13 });
  for (const limit of ["", "0", "-2", "1.5", "101"]) assert.equal(readAuditLimit(limit), undefined);
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
  const page = { items: [item], before: 0, total: 76, page: 4, limit: 20 as const, retention: { low: 0, medium: 5, high: 7 } };
  const view = renderAuditWorkspace(page, page, {}, false);
  assert.match(view, /data-audit-days="low">不保留/);
  assert.match(view, /role="region" aria-labelledby="audit-records-heading" tabindex="0"/);
  assert.match(view, /data-audit-count>共 76 条/);
  assert.match(view, /第 4 \/ 4 页/);
  assert.match(view, /data-audit-next disabled/);
  assert.match(view, /<option value="10">10 条\/页<\/option><option value="20" selected>20 条\/页<\/option><option value="50">50 条\/页<\/option><option value="custom">自定义…<\/option>/);
  assert.match(renderAuditWorkspace({ ...page, limit: 13 }, page, {}, false), /<option value="13" selected>13 条\/页<\/option>/);
  assert.doesNotMatch(view, /data-audit-edit|<admin>|<employee>/);
  assert.match(renderAuditWorkspace(page, page, {}, true), /data-audit-edit/);
  assert.match(renderAuditDetail(item), /2026年11月18日15:43:19/);
  assert.match(renderAuditDetail(item), /&lt;policy violation&gt;/);
  assert.match(renderAuditBatch({ items: [{ ...item, target: "批量操作" }, item], before: 0 }), /&lt;employee&gt;/);
  assert.doesNotMatch(renderAuditBatch({ items: [{ ...item, target: "批量操作" }, item], before: 0 }), /批量操作|<policy violation>/);
});
