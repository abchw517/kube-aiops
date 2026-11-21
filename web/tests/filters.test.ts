import assert from "node:assert/strict";
import test from "node:test";
import {
  changeFindingScope,
  matchingFindingClusters,
  parseFindingFilters,
  serializeFindingFilters,
  toDetailCluster,
  toListParams,
  toSummaryParams,
} from "../src/domain/filters.ts";

test("parses supported Finding filters and rejects unknown severity", () => {
  assert.deepEqual(
    parseFindingFilters("#/findings?cluster=local&namespace=prod&kind=Pod&severity=critical"),
    { cluster: "local", environment: undefined, business: undefined, group: undefined, namespace: "prod", kind: "Pod", severity: "critical", problem: undefined },
  );
  assert.equal(parseFindingFilters("#/findings?severity=unknown").severity, undefined);
});

test("serializes filters without inventing API fields", () => {
  const query = serializeFindingFilters({
    cluster: "local",
    namespace: "prod",
    severity: "warning",
  });
  assert.equal(query, "cluster=local&namespace=prod&severity=warning");
});

test("list request uses generated contract parameters and bounded page size", () => {
  assert.deepEqual(toListParams({ cluster: "local", kind: "Deployment" }), {
    cluster: "local",
    kind: "Deployment",
    limit: 50,
  });
});

test("unscoped list and summary use ALL while legacy detail still uses local", () => {
  const filters = parseFindingFilters("#/findings");
  assert.equal(toListParams(filters).cluster, "*");
  assert.equal(toSummaryParams(filters).cluster, "*");
  assert.equal(toDetailCluster("#/findings/id"), "local");
  assert.equal(toDetailCluster("#/findings/id?cluster=test-south"), "test-south");
});

test("choosing an environment drops incompatible descendants and leaves all matching instances", () => {
  const clusters = [
    { id: "local", name: "local", status: "ready", environment: "生产", business: "商城", group: "生产组" },
    { id: "test-a", name: "测试 A", status: "ready", environment: "测试", business: "商城", group: "测试组" },
    { id: "test-b", name: "测试 B", status: "ready", environment: "测试", business: "商城", group: "测试组" },
  ];
  const current = parseFindingFilters("#/findings?cluster=local&environment=生产&business=商城&group=生产组&continue=previous");
  const next = changeFindingScope(clusters, current, "environment", "测试");
  assert.equal(serializeFindingFilters(next), "environment=%E6%B5%8B%E8%AF%95&business=%E5%95%86%E5%9F%8E");
  assert.deepEqual(matchingFindingClusters(clusters, next).map(({ id }) => id), ["test-a", "test-b"]);
  assert.equal(toSummaryParams(next).cluster, "*");
  assert.equal(serializeFindingFilters(changeFindingScope(clusters, current, "environment", undefined)), "");
  assert.equal(toListParams(parseFindingFilters("#/findings?cluster=local")).cluster, "local");
});
