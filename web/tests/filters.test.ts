import assert from "node:assert/strict";
import test from "node:test";
import {
  parseFindingFilters,
  serializeFindingFilters,
  toListParams,
} from "../src/domain/filters.ts";

test("parses supported Finding filters and rejects unknown severity", () => {
  assert.deepEqual(
    parseFindingFilters("#/findings?cluster=local&namespace=prod&kind=Pod&severity=critical"),
    { cluster: "local", namespace: "prod", kind: "Pod", severity: "critical" },
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
    namespace: undefined,
    severity: undefined,
  });
});
