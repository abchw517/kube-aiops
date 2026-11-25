import { apiClient } from "../../api/client.ts";
import { ApiError, type DatasourceInput, type DiagnosticDatasource } from "../../api/generated.ts";
import { escapeHtml as h, formatTimestamp } from "../../components/html.ts";
import { cloudNames } from "./datasource-rows.ts";
import { sourceError } from "./datasource-errors.ts";
import { bindDirtyDialog, resourceDetail } from "./resource-workspace.ts";

type Kind = keyof typeof cloudNames;
const value = (data: FormData, name: string) => String(data.get(name) ?? "").trim();

export function bindAssetSourceEditor(dialog: HTMLDialogElement, onSaved: (row: DiagnosticDatasource) => void, onConflict: () => void) {
  let current: DiagnosticDatasource | undefined;
  let provider: Kind;
  let busy = false;
  const guard = bindDirtyDialog(dialog, "[data-resource-close]", () => busy);
  dialog.addEventListener("close", () => { if (!dialog.open) dialog.replaceChildren(); });
  dialog.addEventListener("submit", async event => {
    event.preventDefault();
    if (busy || !(event.target instanceof HTMLFormElement)) return;
    const form = event.target;
    const status = form.querySelector<HTMLElement>("[role=status]");
    const submit = form.querySelector<HTMLButtonElement>('button[type="submit"]');
    const content = form.querySelector<HTMLElement>(".resource-overlay-body");
    if (!status || !submit || !content) return;
    const data = new FormData(form);
    const row = current;
    const kind = row?.config.kind ?? provider;
    const body: DatasourceInput = {
      name: value(data, "name"),
      scope: row?.scope ?? {
        clusterId: value(data, "clusterId"), namespace: value(data, "namespace"),
        environmentId: value(data, "environmentId") || undefined, businessId: value(data, "businessId") || undefined,
      },
      config: { kind, endpoint: kind === "zabbix" ? value(data, "endpoint") : undefined,
        region: kind === "zabbix" ? undefined : value(data, "region"),
        assetIds: value(data, "assetIds").split(",").map(id => id.trim()).filter(Boolean) },
      enabled: data.has("enabled"), credentialRef: value(data, "credentialRef") || undefined,
      ingressRef: data.has("clearIngress") ? "" : value(data, "ingressRef") || undefined,
    };
    if (!body.name || !body.config.assetIds.length) { status.textContent = "请填写名称和至少一个资产原生 ID。"; return; }
    busy = submit.disabled = content.inert = true;
    status.textContent = "正在保存配置…";
    try {
      const saved = row ? await apiClient.updateDiagnosticDatasource({ datasourceId: row.id, "If-Match": `"${row.version}"`, body })
        : await apiClient.createDiagnosticDatasource({ body });
      guard.clean();
      dialog.close();
      onSaved(saved);
    } catch (error) {
      const conflict = error instanceof ApiError && error.status === 412;
      status.textContent = conflict ? "配置已更新，请关闭窗口后核对最新配置。" : sourceError(error);
      if (conflict) onConflict();
    } finally {
      busy = submit.disabled = content.inert = false;
    }
  });
  return (kind: Kind, row?: DiagnosticDatasource) => {
    provider = kind;
    current = row;
    guard.clean();
    const type = row?.config.kind === "zabbix" ? "Zabbix（存量资产）" : cloudNames[row?.config.kind ?? kind];
    const config = `<section class="resource-detail-card"><h3>资产配置</h3><div class="incident-form-grid">
      <label>名称<input name="name" value="${h(row?.name)}" required maxlength="60"></label>
      ${row?.config.kind === "zabbix"
        ? `<label>Zabbix API 地址<input name="endpoint" type="url" value="${h(row.config.endpoint)}"></label>`
        : `<label>云地域<input name="region" value="${h(row?.config.region)}" placeholder="${kind === "aliyun-ecs" ? "cn-hangzhou" : "ap-guangzhou"}"></label>`}
      <label class="incident-form-wide">资产原生 ID（逗号分隔）<input name="assetIds" required value="${h(row?.config.assetIds.join(","))}"></label>
      <label>凭据文件引用<input name="credentialRef" maxlength="128" ${row ? 'placeholder="留空沿用原配置"' : "required"}></label>
      <label>推送凭据文件引用<input name="ingressRef" maxlength="128" ${row ? 'placeholder="留空沿用原配置"' : ""}></label>
      </div></section>`;
    const scope = row ? `<p>${h([row.scope.environmentId,row.scope.businessId,row.scope.clusterId,row.scope.namespace].filter(Boolean).join(" / "))}</p>` :
      `<div class="incident-form-grid"><label>集群实例 ID<input name="clusterId" required maxlength="253"></label>
        <label>命名空间<input name="namespace" required maxlength="253"></label>
        <label>环境 ID<input name="environmentId" maxlength="253" placeholder="可随实例解析"></label>
        <label>业务 ID<input name="businessId" maxlength="253" placeholder="共享命名空间时填写"></label></div>`;
    const body = `${config}<section class="resource-detail-card"><h3>授权范围</h3>${scope}</section>
      <label><input type="checkbox" name="enabled"${row?.enabled === false ? "" : " checked"}> 启用数据源</label>
      ${row?.ingressConfigured ? '<label><input type="checkbox" name="clearIngress"> 停用信号推送</label>' : ""}`;
    dialog.innerHTML = `<form class="resource-overlay-form" data-datasource-form="${h(row?.id)}">${resourceDetail("asset-editor-title", `${row ? "编辑" : "接入"} ${type}`, "保存后可从列表测试并同步。", body,
      '<p role="status"></p><div class="catalog-actions"><button type="button" class="button secondary" data-resource-close>取消</button><button type="submit" class="button">保存数据源</button></div>')}</form>`;
    dialog.showModal();
  };
}

export async function showInventory(node: HTMLElement, row: DiagnosticDatasource, canCreateIncident: boolean): Promise<void> {
  node.textContent = "正在读取资产…";
  try {
    const { items } = await apiClient.listDiagnosticAssets({ datasourceId: row.id });
    if (!node.isConnected) return;
    const { clusterId, namespace, businessId, environmentId } = row.scope;
    const scope = new URLSearchParams({ clusterId, namespace, businessId: businessId ?? "", environmentId });
    node.innerHTML = items.length ? `<ul>${items.map(a => `<li>
      <strong>${h(a.name)}</strong> · ${h(a.state)} · ${formatTimestamp(a.observedAt)}
      <p>拓扑资产 ID <code>${h(a.id)}</code> · 原生 ID ${h(a.externalId)}</p>
      ${canCreateIncident ? `<a href="#/diagnostics/events?create=1&amp;assetId=${encodeURIComponent(a.id)}&amp;${h(scope.toString())}">从资产创建故障</a>` : ""}</li>`).join("")}</ul>` :
      "<p>暂无同步资产。</p>";
  } catch (error) {
    if (node.isConnected) node.textContent = sourceError(error);
  }
}
