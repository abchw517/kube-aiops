import assert from "node:assert/strict";
import test from "node:test";
import { incidentFilters, incidentMetricHref, renderIncidentScope } from "../src/views/diagnosis/incident-filters.ts";

test("a metric retains matching filters, starts on the first page and excludes temporary parameters", () => {
  const params = incidentFilters("#/diagnostics?clusterId=prod&namespace=pay&continue=opaque&create=1");
  assert.equal(incidentMetricHref(params, { key: "severity", value: "P1" }),
    "#/diagnostics/events?clusterId=prod&namespace=pay&severity=P1");
  assert.equal(incidentMetricHref({ ...params, status: "pending" }, { key: "status", value: "investigating" }), undefined);
});

test("applied scope and retained cluster identifiers are escaped in collapsed and expanded views", () => {
  const params = incidentFilters("#/diagnostics?clusterId=%3Cretired%3E&q=%22%3Cquery%3E");
  const html = renderIncidentScope([], params, "#/diagnostics");
  assert.match(html, /集群实例 &lt;retired&gt; · 关键词 &quot;&lt;query&gt;/);
  assert.match(html, /value="&lt;retired&gt;" selected/);
  assert.doesNotMatch(html, /<retired>|<query>/);
});
