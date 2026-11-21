import { apiClient } from "../../api/client.ts";
import type { IncidentTopology, TopologyNode } from "../../api/generated.ts";
import { escapeHtml, formatTimestamp } from "../../components/html.ts";
import { incidentSubview, submit } from "./incident-rca.ts";

const relationship: Record<string, string> = { calls: "调用", depends_on: "依赖", runs_on: "运行于", monitored_by: "监控来源" };

export async function loadTopologyPage(id: string, hash: string) {
  const params = new URLSearchParams(hash.split("?")[1]);
  const depth = Number(params.get("depth") ?? "2");
  const [detail, graph] = await Promise.all([apiClient.getIncident({ id }), apiClient.getIncidentTopology({ id, depth })]);
  return {
    graph,
    html: `<section class="panel incident-header">
      <a href="${escapeHtml(incidentSubview(id, "", hash))}">← 返回故障详情</a>
      <h2>${escapeHtml(detail.incident.title)}</h2>
      <nav class="pagination" aria-label="拓扑深度">${[1, 2, 3, 4].map((level) =>
        `<a class="button secondary" href="${escapeHtml(incidentSubview(id, "topology", hash, { depth: String(level) }))}"${depth === level ? ' aria-current="page"' : ""}>${level} 层</a>`).join("")}</nav>
      <p>${graph.coverage === "partial" ? "已显示有记录且可访问的关系。" : "当前范围内暂无可见关系。"}${graph.truncated ? " 结果达到显示上限。" : ""}</p>
    </section>
    <div class="topology-layout">
      <section class="panel incident-header"><h2>资源节点</h2>
        <div class="topology-nodes">${graph.nodes.map((node) => `<button class="button secondary topology-node" data-topology-node="${escapeHtml(node.id)}" aria-pressed="${String(node.id === graph.root)}">
          <strong>${escapeHtml(node.name)}</strong><span>${escapeHtml(node.kind)} · ${escapeHtml(node.platform)}</span></button>`).join("")}</div>
      </section>
      <section class="panel incident-header" data-topology-detail>${nodeDetail(graph.nodes.find((node) => node.id === graph.root), graph, hash)}</section>
    </div>
    ${detail.actions.topologyWrite ? topologyForm() : ""}`,
  };
}

function nodeDetail(node: TopologyNode | undefined, graph: IncidentTopology, hash: string): string {
  if (!node) return "<p>选择一个资源节点查看详情。</p>";
  const neighbors = graph.edges.filter((edge) => edge.source === node.id || edge.target === node.id);
  const label = (id: string) => graph.nodes.find((item) => item.id === id)?.name ?? "";
  return `<h2>${escapeHtml(node.name)}</h2>
    <p>${escapeHtml([node.scope.environmentId, node.scope.businessId, node.scope.clusterId, node.scope.namespace, node.state].filter(Boolean).join(" / "))}</p>
    ${node.incidentId ? `<a href="${escapeHtml(incidentSubview(node.incidentId, "", hash))}">查看关联故障</a>` : ""}
    ${neighbors.length ? `<ul class="incident-timeline-list">${neighbors.map((item) => `<li>
      <strong>${escapeHtml(label(item.source))} → ${relationship[item.relation] ?? ""} → ${escapeHtml(label(item.target))}</strong>
      <p>${item.confidence === "confirmed" ? "已确认" : "推断"} · ${escapeHtml(item.reason)} · ${formatTimestamp(item.recordedAt)}</p>
      ${item.evidenceId ? `<a href="${escapeHtml(incidentSubview(item.source.replace(/^incident:/, ""), "", hash, { evidence: item.evidenceId }))}">定位依据</a>` : ""}
    </li>`).join("")}</ul>` : "<p>无可见的相邻关系。</p>"}`;
}

function topologyForm(): string {
  return `<section class="panel incident-header"><h2>维护资源关系</h2>
    <form data-topology-form>
      <div class="incident-form-grid">
        <label>目标故障 ID<input name="targetIncidentId"></label>
        <label>目标资产 ID<input name="targetAssetId"></label>
        <label>关系<select name="relation">${Object.entries(relationship).map(([key, label]) => `<option value="${key}">${label}</option>`).join("")}</select></label>
        <label>可信度<select name="confidence"><option value="confirmed">已确认</option><option value="inferred">推断</option></select></label>
        <label>关联证据 ID<input name="evidenceId" placeholder="推断关系必填"></label>
        <label>理由<input name="reason" required maxlength="320"></label>
      </div>
      <label><input type="checkbox" name="remove"> 解除该关系</label>
      <p data-form-status role="status"></p><button class="button" type="submit">保存关系</button>
    </form>
  </section>`;
}

export function bindTopologyPage(root: HTMLElement, id: string, hash: string, graph: IncidentTopology, refresh: () => void): void {
  const panel = root.querySelector<HTMLElement>("[data-topology-detail]");
  const buttons = root.querySelectorAll<HTMLButtonElement>("[data-topology-node]");
  for (const button of buttons) {
    button.addEventListener("click", () => {
      if (!panel) return;
      for (const peer of buttons) peer.setAttribute("aria-pressed", String(peer === button));
      panel.innerHTML = nodeDetail(graph.nodes.find((node) => node.id === button.dataset.topologyNode), graph, hash);
    });
  }
  const form = root.querySelector<HTMLFormElement>("[data-topology-form]");
  form?.addEventListener("submit", async (event) => {
    event.preventDefault();
    await submit(form, async (fields) => {
      const field = (name: string) => String(fields.get(name) ?? "").trim();
      const targetIncidentId = field("targetIncidentId");
      const targetAssetId = field("targetAssetId");
      if (Boolean(targetIncidentId) === Boolean(targetAssetId)) throw new Error("请选择一种目标，填写故障 ID 或资产 ID。");
      await apiClient.saveTopologyLink({ id, body: {
        targetIncidentId: targetIncidentId || undefined, targetAssetId: targetAssetId || undefined,
        confidence: field("confidence"), evidenceId: field("evidenceId") || undefined,
        relation: field("relation"), reason: field("reason"), remove: fields.has("remove"),
      } });
      if (form.isConnected) refresh();
    });
  });
}
