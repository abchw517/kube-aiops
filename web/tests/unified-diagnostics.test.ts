import assert from "node:assert/strict";
import test from "node:test";
import type { Finding, Incident } from "../src/api/generated.ts";
import { aggregateDiagnostics, overviewDays, overviewLevel, overviewSearch } from "../src/domain/unified-diagnostics.ts";

const now = Date.parse("2026-11-24T13:00:00Z");
const scope = { clusterId: "test", environmentId: "testing", namespace: "default" };

function incident(id: string, patch: Partial<Incident> = {}): Incident {
  return { id, scope, title: "故障", displayKey: id, version: 1, resource: {}, resourceId: id,
    severity: "P1", status: "pending", source: "manual", firstSeen: "2026-11-24T10:00:00Z",
    lastSeen: "", createdAt: "", updatedAt: "", ...patch };
}

function finding(id: string, patch: Partial<Finding> = {}): Finding {
  return { id, cluster: "test", severity: "warning", source: "k8sgpt", resource: {}, problem: "警告",
    createdAt: "2026-11-23T23:59:00Z", ...patch };
}

test("unified diagnostics deduplicates an explicit reference only within its cluster before applying severity", () => {
  const events = [incident("one", { findingId: "shared" }), incident("two", { findingId: "shared" })];
  const findings = [finding("shared"), finding("shared", { cluster: "prod" }), finding("other")];
  const all = aggregateDiagnostics(events, findings, undefined, now);
  assert.deepEqual(all.counts, { critical: 2, warning: 2, info: 0 });
  assert.deepEqual(aggregateDiagnostics(events, findings, "warning", now).recent.map(({ cluster, id }) => [cluster, id]),
    [["prod", "shared"], ["test", "other"]]);
  assert.equal(overviewLevel("P1"), "critical");
});

test("visible merge targets keep the representative and UTC trend counts only dated records in the window", () => {
  const events = [incident("child", { findingId: "linked", mergedInto: "target" }), incident("target")];
  const result = aggregateDiagnostics(events, [finding("linked"), finding("old", { createdAt: "2026-11-17T23:59:59Z" }),
    finding("undated", { createdAt: "" }), finding("today"), finding("last", { createdAt: "2026-11-18T00:00:00Z" }),
    finding("outside")], undefined, now);
  assert.equal(result.total, 6);
  assert.equal(result.recent.length, 5);
  assert.equal(result.undated, 1);
  assert.deepEqual(result.daily[0], { date: "2026-11-18", incident: 0, finding: 1 });
  assert.deepEqual(result.daily.at(-1), { date: "2026-11-24", incident: 1, finding: 0 });
  assert.equal(result.recent.some(({ id }) => id === "child" || id === "linked"), false);
  assert.equal(aggregateDiagnostics([events[0]], [], undefined, now).total, 1);
});


test("widening the trend window does not alter diagnostic counts or the recent queue", () => {
  const rows = ["a", "b", "c"].map((id) => finding(id, { createdAt: "2026-11-16T10:01:35Z" }));
  const week = aggregateDiagnostics([], rows, undefined, now);
  const fortnight = aggregateDiagnostics([], rows, undefined, now, 14);
  const month = aggregateDiagnostics([], rows, undefined, now, 30);
  assert.deepEqual(week.counts, fortnight.counts);
  assert.deepEqual(week.recent, month.recent);
  assert.equal(week.daily.some((day) => day.finding > 0), false);
  assert.equal(fortnight.daily.find((day) => day.date === "2026-11-16")?.finding, 3);
  assert.equal(month.daily.length, 30);
  assert.equal(overviewDays("21"), 7);
  assert.equal(overviewSearch(new URLSearchParams("cluster=dev&severity=P2&trendDays=14&continue=ignored")).toString(),
    "clusterId=dev&severity=warning&trendDays=14");
});
