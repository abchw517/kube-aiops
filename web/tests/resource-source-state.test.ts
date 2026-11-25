import assert from "node:assert/strict";
import test from "node:test";
import { type ClusterEntry, clusterCategory, collectionCaption } from "../src/views/clusters/cluster-records.ts";
import { sourceActivity, sourceKey, sourceStatus } from "../src/views/resources/datasource-rows.ts";

test("uses distinct source IDs and keeps historical synchronization separate from a manual test", () => {
  const scope = { environmentId: "production", businessId: "ops", clusterId: "cluster" };
  const checkedAt = 1_700_000_200;
  const syncedAt = 1_700_000_100;
  const alertmanager = { family: "observability", source: {
    id: "same-id", kind: "alertmanager", name: "告警源", endpoint: "https://alerts.example",
    scope, enabled: true, auth: "none", shared: false, version: 1, checkedAt, syncedAt, failed: true,
  } } as const;
  assert.deepEqual(sourceStatus(alertmanager), { label: "同步失败", state: "issue", snapshot: true });
  assert.equal(sourceActivity(alertmanager).label, "上次成功同步");
  const prometheus = { ...alertmanager, source: { ...alertmanager.source, kind: "prometheus", failed: false } } as const;
  assert.deepEqual(sourceStatus(prometheus), { label: "已启用", state: "enabled" });
  assert.equal(sourceActivity(prometheus).label, "上次验证");
  assert.deepEqual(sourceStatus(prometheus, true), { label: "验证失败", state: "issue", snapshot: false });
  const assets = { family: "assets", source: {
    id: "same-id", name: "存量主机", config: { kind: "zabbix", assetIds: [] }, scope: { ...scope, namespace: "default" }, enabled: false,
    version: 1, health: "configuration_error", credentialConfigured: false, ingressConfigured: false,
  } } as const;
  assert.notEqual(sourceKey(alertmanager), sourceKey(assets));
  assert.deepEqual(sourceStatus(assets), { label: "已停用", state: "paused" });
});

test("separates a cluster's node health, diagnostic health and onboarding category", () => {
  const item: ClusterEntry = {
    id: "dev", name: "研发", environment: "研发", mode: "pull", credentialKind: "token", enabled: true,
    checkedAt: 1_700_000_010, firstCollectedAt: 1_700_000_000, validatedAt: 1_700_000_000, reportedAt: 0,
    collection: { nodes: [{ name: "worker", ready: "False" }] },
  };
  assert.equal(clusterCategory(item), "issue");
  assert.equal(collectionCaption(item), "已采集");
  item.collection = { nodes: [{ name: "worker", ready: "True" }], namespaceError: true };
  assert.equal(clusterCategory(item), "issue");
  assert.equal(collectionCaption(item), "部分采集异常");
  assert.equal(clusterCategory({ ...item, enabled: false, credentialKind: undefined }), "paused");
  assert.equal(clusterCategory({ ...item, mode: "push", firstCollectedAt: 0, reportedAt: 0 }), "waiting");
});
