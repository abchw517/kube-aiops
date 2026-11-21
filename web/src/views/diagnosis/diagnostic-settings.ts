import { apiClient } from "../../api/client.ts";
import type { CreateIncidentScope, DatasourceConfig, DatasourceInput, DiagnosticDatasource, DiagnosticPolicy, PolicyInput } from "../../api/generated.ts";
import { escapeHtml, formatTimestamp } from "../../components/html.ts";
import { incidentError } from "./incident-list.ts";
import { incidentSubview, submit } from "./incident-rca.ts";

export const datasourcesPath = "#/diagnostics/datasources";
export const policiesPath = "#/diagnostics/policies";

const providers: Record<DatasourceConfig["kind"], string> = { zabbix: "Zabbix", "aliyun-ecs": "阿里云 ECS", "tencent-cvm": "腾讯云 CVM" };
const health: Record<string, string> = {
  untested: "待验证", available: "可用", partial: "部分可用", unavailable: "不可用",
  timeout: "超时", unauthorized: "上游拒绝授权", credentials_unavailable: "凭据不可用", configuration_error: "配置错误",
};
const sources = {
  any: "所有信号", "kubernetes-events": "Kubernetes 事件", prometheus: "Prometheus 指标", alertmanager: "Alertmanager 告警",
  victorialogs: "VictoriaLogs 日志", ...providers,
};

function field(form: FormData, name: string): string {
  return String(form.get(name) ?? "").trim();
}

function scopeFrom(form: FormData): CreateIncidentScope {
  return { clusterId: field(form, "clusterId"), namespace: field(form, "namespace"),
    environmentId: field(form, "environmentId") || undefined, businessId: field(form, "businessId") || undefined };
}

function scopeLabel(scope: CreateIncidentScope): string {
  return escapeHtml([scope.environmentId, scope.businessId, scope.clusterId, scope.namespace].filter(Boolean).join(" / "));
}

function scopeFields(scope?: CreateIncidentScope): string {
  return scope ? `<p>范围 · ${scopeLabel(scope)}</p>` : `<div class="incident-form-grid">
    <label>集群实例 ID<input name="clusterId" required maxlength="253"></label>
    <label>命名空间<input name="namespace" required maxlength="253"></label>
    <label>环境 ID<input name="environmentId" maxlength="253" placeholder="可随实例解析"></label>
    <label>业务 ID<input name="businessId" maxlength="253" placeholder="共享命名空间时填写"></label>
  </div>`;
}

function options(items: Record<string, string>, selected: string): string {
  return Object.entries(items).map(([value, label]) =>
    `<option value="${escapeHtml(value)}"${value === selected ? " selected" : ""}>${escapeHtml(label)}</option>`).join("");
}

function paging(path: string, after: string | undefined, next: string | undefined): string {
  return after || next ? `<nav class="pagination" aria-label="列表分页">
    ${after ? `<a class="button secondary" href="${path}">返回首页</a>` : ""}
    ${next ? `<a class="button secondary" href="${path}?continue=${encodeURIComponent(next)}">下一页</a>` : ""}
  </nav>` : "";
}

function datasourceForm(d?: DiagnosticDatasource): string {
  return `<form data-datasource-form="${escapeHtml(d?.id ?? "")}">
    <div class="incident-form-grid">
      <label>名称<input name="name" value="${escapeHtml(d?.name)}" required maxlength="60"></label>
      ${d ? `<p>类型 · ${providers[d.config.kind]}</p>` : `<label>类型<select name="kind">${options(providers, "zabbix")}</select></label>`}
      <label data-zabbix-option>Zabbix API 地址<input name="endpoint" type="url" value="${escapeHtml(d?.config.endpoint)}" placeholder="https://zabbix.example/api_jsonrpc.php"></label>
      <label data-cloud-option>云地域<input name="region" value="${escapeHtml(d?.config.region)}" placeholder="cn-hangzhou"></label>
      <label class="incident-form-wide">资产原生 ID（逗号分隔）<input name="assetIds" required value="${escapeHtml(d?.config.assetIds.join(","))}"></label>
      <label>凭据文件引用<input name="credentialRef" maxlength="128"${d ? ' placeholder="留空沿用原配置"' : " required"}></label>
      <label>推送凭据文件引用<input name="ingressRef" maxlength="128"${d ? ' placeholder="留空沿用原配置"' : ""}></label>
    </div>
    ${scopeFields(d?.scope)}
    <label><input type="checkbox" name="enabled"${!d || d.enabled ? " checked" : ""}> 启用数据源</label>
    ${d?.ingressConfigured ? '<label><input type="checkbox" name="clearIngress"> 停用信号推送</label>' : ""}
    <p data-form-status role="status"></p><button class="button" type="submit">保存数据源</button>
  </form>`;
}

export async function loadDatasourcesPage(hash: string, canWrite: boolean, canTest: boolean) {
  const after = new URLSearchParams(hash.split("?")[1]).get("continue") || undefined;
  const page = await apiClient.listDiagnosticDatasources({ continue: after, limit: 25 });
  return {
    items: page.items,
    html: `${canWrite ? `<details class="panel incident-header"><summary>新增数据源</summary>${datasourceForm()}</details>` : ""}
      ${page.items.map((d) => `<section class="panel incident-header">
        <div class="diagnostic-toolbar"><h2>${escapeHtml(d.name)}</h2><span>${providers[d.config.kind]} · ${d.enabled ? health[d.health] ?? "待验证" : "已停用"}</span></div>
        <p>${scopeLabel(d.scope)}</p>
        <p>上次检查 ${formatTimestamp(d.checkedAt)} · 上次同步 ${formatTimestamp(d.syncedAt)}</p>
        ${d.message ? `<p role="status">${escapeHtml(d.message)}</p>` : ""}
        <p>ID <code>${escapeHtml(d.id)}</code>${d.ingressConfigured ? " · 已配置推送" : ""}</p>
        ${canTest && d.enabled ? `<form data-datasource-test="${escapeHtml(d.id)}"><button class="button secondary" type="submit">测试并同步</button><p data-form-status role="status"></p></form>` : ""}
        <details class="incident-actions" data-inventory="${escapeHtml(d.id)}" data-scope="${escapeHtml(new URLSearchParams({
          clusterId: d.scope.clusterId, namespace: d.scope.namespace, businessId: d.scope.businessId ?? "", environmentId: d.scope.environmentId,
        }).toString())}"><summary>可见资产</summary><div role="status"></div></details>
        ${canWrite ? `<details class="incident-actions"><summary>编辑配置与启停</summary>${datasourceForm(d)}</details>` : ""}
      </section>`).join("")}
      ${page.items.length ? "" : '<section class="state-card"><h2>暂无数据源</h2><p>当前范围内没有已配置的诊断数据源。</p></section>'}
      ${paging(datasourcesPath, after, page.pagination.continue)}`,
  };
}

export function bindDatasourcesPage(root: HTMLElement, items: DiagnosticDatasource[], refresh: () => void, canCreateIncident: boolean): void {
  for (const form of root.querySelectorAll<HTMLFormElement>("[data-datasource-form]")) {
    const row = items.find(({ id }) => id === form.dataset.datasourceForm);
    const kindField = form.querySelector<HTMLSelectElement>('select[name="kind"]');
    const kind = () => row?.config.kind ?? ((kindField?.value ?? "zabbix") as DatasourceConfig["kind"]);
    const updateFields = () => {
      const zabbix = form.querySelector<HTMLElement>("[data-zabbix-option]");
      const cloud = form.querySelector<HTMLElement>("[data-cloud-option]");
      if (zabbix) zabbix.hidden = kind() !== "zabbix";
      if (cloud) cloud.hidden = kind() === "zabbix";
    };
    updateFields();
    kindField?.addEventListener("change", updateFields);
    form.addEventListener("submit", async (event) => {
      event.preventDefault();
      await submit(form, async (fields) => {
        const body: DatasourceInput = {
          name: field(fields, "name"), scope: row?.scope ?? scopeFrom(fields),
          config: { kind: kind(), endpoint: kind() === "zabbix" ? field(fields, "endpoint") : undefined,
            region: kind() === "zabbix" ? undefined : field(fields, "region"),
            assetIds: field(fields, "assetIds").split(",").map((id) => id.trim()).filter(Boolean) },
          enabled: fields.has("enabled"), credentialRef: field(fields, "credentialRef") || undefined,
          ingressRef: fields.has("clearIngress") ? "" : field(fields, "ingressRef") || undefined,
        };
        if (row) await apiClient.updateDiagnosticDatasource({ datasourceId: row.id, "If-Match": `"${row.version}"`, body });
        else await apiClient.createDiagnosticDatasource({ body });
        if (form.isConnected) refresh();
      });
    });
  }
  for (const form of root.querySelectorAll<HTMLFormElement>("[data-datasource-test]")) {
    const row = items.find(({ id }) => id === form.dataset.datasourceTest);
    form.addEventListener("submit", async (event) => {
      event.preventDefault();
      if (!row) return;
      await submit(form, async () => {
        await apiClient.testDiagnosticDatasource({ datasourceId: row.id, "If-Match": `"${row.version}"` });
        if (form.isConnected) refresh();
      });
    });
  }
  for (const details of root.querySelectorAll<HTMLDetailsElement>("[data-inventory]")) {
    details.addEventListener("toggle", async () => {
      const region = details.querySelector<HTMLElement>('[role="status"]');
      if (!details.open || !region || !details.dataset.inventory) return;
      region.textContent = "正在读取资产…";
      try {
        const { items } = await apiClient.listDiagnosticAssets({ datasourceId: details.dataset.inventory });
        if (!details.isConnected) return;
        const scope = details.dataset.scope;
        region.innerHTML = items.length ? `<ul>${items.map((a) => `<li>
          <strong>${escapeHtml(a.name)}</strong> · ${escapeHtml(a.state)} · ${formatTimestamp(a.observedAt)}
          <p>拓扑资产 ID <code>${escapeHtml(a.id)}</code> · 原生 ID ${escapeHtml(a.externalId)}</p>
          ${scope && canCreateIncident ? `<a href="#/diagnostics/events?create=1&amp;assetId=${encodeURIComponent(a.id)}&amp;${escapeHtml(scope)}">从资产创建故障</a>` : ""}
        </li>`).join("")}</ul>` : "<p>暂无同步资产。</p>";
      } catch (error) {
        if (details.isConnected) region.textContent = incidentError(error);
      }
    });
  }
}

function policyForm(p?: DiagnosticPolicy): string {
  return `<form data-policy-form="${escapeHtml(p?.id ?? "")}">
    <div class="incident-form-grid">
      <label>名称<input name="name" value="${escapeHtml(p?.name)}" required maxlength="60"></label>
      <label>触发来源<select name="source">${options(sources, p?.source ?? "any")}</select></label>
      <label>故障级别<select name="severity">${options({ "": "全部", P1: "P1", P2: "P2", P3: "P3" }, p?.severity ?? "")}</select></label>
      <label>冷却时间（秒）<input name="cooldownSeconds" type="number" min="60" max="86400" value="${p?.cooldownSeconds ?? 300}" required></label>
      <label>最大并发数<input name="maxConcurrent" type="number" min="1" max="5" value="${p?.maxConcurrent ?? 1}" required></label>
    </div>
    <p>模板 · 基于证据的只读根因分析</p>
    ${scopeFields(p?.scope)}
    <label><input type="checkbox" name="enabled"${p?.enabled ? " checked" : ""}> 启用自动触发</label>
    <p data-form-status role="status"></p><button class="button" type="submit">保存策略</button>
  </form>`;
}

export async function loadPoliciesPage(hash: string, canWrite: boolean) {
  const after = new URLSearchParams(hash.split("?")[1]).get("continue") || undefined;
  const page = await apiClient.listDiagnosticPolicies({ continue: after, limit: 25 });
  return {
    items: page.items,
    html: `${canWrite ? `<details class="panel incident-header"><summary>新增诊断策略</summary>${policyForm()}</details>` : ""}
      ${page.items.map((p) => `<section class="panel incident-header">
        <div class="diagnostic-toolbar"><h2>${escapeHtml(p.name)}</h2><span>版本 ${p.version} · ${p.enabled ? (p.automationAllowed ? "自动触发" : "等待环境启用") : "已停用"}</span></div>
        <p>${scopeLabel(p.scope)} · ${escapeHtml(sources[p.source as keyof typeof sources] ?? p.source)} · ${escapeHtml(p.severity || "全部级别")}</p>
        <p>冷却 ${p.cooldownSeconds} 秒 · 最大并发 ${p.maxConcurrent}</p>
        <details class="incident-actions" data-policy-runs="${escapeHtml(p.id)}"><summary>运行记录</summary><div role="status"></div></details>
        ${canWrite ? `<details class="incident-actions"><summary>编辑策略</summary>${policyForm(p)}</details>` : ""}
      </section>`).join("")}
      ${page.items.length ? "" : '<section class="state-card"><h2>暂无诊断策略</h2><p>当前范围内没有已配置的自动诊断策略。</p></section>'}
      ${paging(policiesPath, after, page.pagination.continue)}`,
  };
}

export function bindPoliciesPage(root: HTMLElement, items: DiagnosticPolicy[], refresh: () => void): void {
  for (const form of root.querySelectorAll<HTMLFormElement>("[data-policy-form]")) {
    const row = items.find(({ id }) => id === form.dataset.policyForm);
    form.addEventListener("submit", async (event) => {
      event.preventDefault();
      await submit(form, async (fields) => {
        const body: PolicyInput = {
          name: field(fields, "name"), scope: row?.scope ?? scopeFrom(fields), enabled: fields.has("enabled"),
          source: field(fields, "source"), severity: field(fields, "severity") || undefined, template: "evidence_rca",
          cooldownSeconds: Number(fields.get("cooldownSeconds")), maxConcurrent: Number(fields.get("maxConcurrent")),
        };
        if (row) await apiClient.updateDiagnosticPolicy({ policyId: row.id, "If-Match": `"${row.version}"`, body });
        else await apiClient.createDiagnosticPolicy({ body });
        if (form.isConnected) refresh();
      });
    });
  }
  for (const details of root.querySelectorAll<HTMLDetailsElement>("[data-policy-runs]")) {
    details.addEventListener("toggle", async () => {
      const region = details.querySelector<HTMLElement>('[role="status"]');
      if (!details.open || !region || !details.dataset.policyRuns) return;
      region.textContent = "正在读取运行记录…";
      try {
        const { items } = await apiClient.listDiagnosticPolicyRuns({ policyId: details.dataset.policyRuns });
        if (!details.isConnected) return;
        region.innerHTML = items.length ? `<ul>${items.map((entry) => `<li>
          <a href="${escapeHtml(incidentSubview(entry.incidentId, "rca", "", { run: entry.runId }))}">${formatTimestamp(entry.queuedAt)} · 版本 ${entry.version}</a>
          <span> · ${escapeHtml(entry.state)}</span></li>`).join("")}</ul>` : "<p>暂无策略运行。</p>";
      } catch (error) {
        if (details.isConnected) region.textContent = incidentError(error);
      }
    });
  }
}
