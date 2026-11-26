import { escapeHtml as h } from "../../components/html.ts";

const endpoint = "/api/v1/admin/ai-services";
const sources: Record<string, string> = {
  zabbix: "Zabbix 告警", alertmanager: "Alertmanager 告警", finding: "诊断复核", incident: "故障根因分析", wiki: "知识问答",
};

type Model = { source: string; sourceId: string; environmentId: string; businessId: string; clusterId?: string; namespace?: string; model: string };
type ServiceStatus = { status: "configured" | "unconfigured" | "invalid"; models: Model[] };

export async function loadAIServices() {
  const response = await fetch(endpoint);
  if (!response.ok) throw new Error("无法读取 AI 服务配置。");
  const {status, models}: ServiceStatus = await response.json();
  const environments = [...new Set(models.map(m => m.environmentId).filter(Boolean))].sort();
  const label = { configured: "已配置，等待验证", unconfigured: "尚未绑定", invalid: "配置读取失败" }[status];
  const rows = models.map((m, index) => `<tr data-ai-model-row="${h(m.environmentId)}"><td>${h(sources[m.source] ?? m.source)}</td>
    <td>${[m.environmentId, m.businessId, m.clusterId, m.namespace, m.sourceId].filter(Boolean).map(h).join(" / ")}</td>
    <td>${h(m.model || "Holmes 默认模型")}</td><td role="status">待检查</td>
    <td><button class="button secondary" type="button" data-ai-probe="${index}">测试连接</button></td></tr>`).join("");
  return { html: `<div class="portal-ai-page">
    <section class="panel ai-panel"><div class="system-actions">
      <label>环境 <select data-ai-environment><option value="">全部环境</option>${environments.map(name => `<option value="${h(name)}">${h(name)}</option>`).join("")}</select></label>
      <button class="button secondary" type="button" data-ai-refresh>重新读取配置</button>
    </div></section>
    <section class="panel ai-panel"><h2>HolmesGPT 服务</h2><dl>
      <dt>配置状态</dt><dd>${label}</dd><dt>部署形态</dt><dd>独立内部服务，经 Portal API 鉴权</dd>
      <dt>绑定方式</dt><dd>按来源、环境、业务、集群和命名空间匹配</dd>
      <dt>模型回退</dt><dd>Finding 复核、事件 RCA 和知识问答可沿用既有模型</dd>
    </dl></section>
    <section class="panel ai-panel"><h2>模型连接</h2><p>连接读取自部署绑定配置。服务探测检查 Holmes 就绪状态。</p>
      ${rows ? `<div class="table-wrap"><table><thead><tr><th>来源</th><th>绑定范围</th><th>模型别名</th><th>状态</th><th>操作</th></tr></thead><tbody>${rows}</tbody></table></div>` : "<p>当前没有模型绑定。</p>"}
    </section>
  </div>`, models };
}

export function bindAIServices(root: HTMLElement, models: Model[], reload: () => void): void {
  root.querySelector("[data-ai-refresh]")?.addEventListener("click", reload);
  const filter = root.querySelector<HTMLSelectElement>("[data-ai-environment]");
  filter?.addEventListener("change", () => {
    for (const row of root.querySelectorAll<HTMLElement>("[data-ai-model-row]")) {
      row.hidden = Boolean(filter.value && row.dataset.aiModelRow !== filter.value);
    }
  });
  for (const button of root.querySelectorAll<HTMLButtonElement>("[data-ai-probe]")) {
    button.addEventListener("click", async () => {
      const state = button.closest("tr")?.querySelector("[role=status]");
      const model = models[Number(button.dataset.aiProbe)];
      if (!state || !model || button.disabled) return;
      const { source, sourceId, environmentId, businessId, clusterId, namespace } = model;
      button.disabled = true;
      state.textContent = "检查中";
      try {
        const response = await fetch(endpoint, { method: "POST", headers: {"Content-Type": "application/json"},
          body: JSON.stringify({source, sourceId, environmentId, businessId, clusterId, namespace}) });
        state.textContent = response.ok ? "服务已就绪" : "连接检查失败";
      } catch {
        state.textContent = "连接检查失败";
      } finally {
        button.disabled = false;
      }
    });
  }
}

export const renderAISkills = () => `<section class="panel ai-panel"><h2>平台调查能力</h2>
  <div class="ai-skill-list"><article><h3>告警证据调查</h3><p>从 Zabbix 或 Alertmanager 告警详情分析现有证据。</p><a href="#/diagnostics">打开诊断总览</a></article>
  <article><h3>故障根因分析</h3><p>选择故障、关联证据并发起分析任务。</p><a href="#/diagnostics/events">打开故障事件</a></article>
  <article><h3>运维知识问答</h3><p>从有权访问的已发布章节查找排障建议。</p><a href="#/wiki">打开知识检索</a></article></div></section>`;
