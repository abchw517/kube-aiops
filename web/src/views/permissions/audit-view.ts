import { escapeHtml as h } from "../../components/html.ts";
import {
  auditLevels, auditPageCount, auditPageSizes, maxAuditLimit, maxRetentionDays, type AuditFilters, type AuditListPage,
  type AuditOperation, type AuditPage, type AuditRetention,
} from "./audit-data.ts";

const auditClock = new Intl.DateTimeFormat("zh-CN", {
  timeZone: "Asia/Shanghai", year: "numeric", month: "long", day: "numeric",
  hour: "2-digit", minute: "2-digit", second: "2-digit", hourCycle: "h23",
});

/** 列表和详情共用北京时间格式。 */
function auditTime(seconds: number): string {
  const date = new Date(seconds * 1000);
  return Number.isFinite(date.getTime()) ? auditClock.format(date).replace(/\s/g, "") : "—";
}

function badge(kind: "risk" | "result", value: string): string {
  return `<span class="system-badge" data-audit-${kind}="${h(value)}">${h(value)}${kind === "risk" ? "风险" : ""}</span>`;
}

function options(values: string[], selected?: string, suffix = ""): string {
  return values.map((value) => `<option value="${h(value)}"${value === selected ? " selected" : ""}>${h(value + suffix)}</option>`).join("");
}

/** 保留深链接里的已选操作，即使当前没有有效记录。 */
export function renderAuditActionOptions(metadata: AuditPage, selected?: string): string {
  const actions = new Set(metadata.actions ?? metadata.items.map(({ action }) => action));
  if (selected) actions.add(selected);
  return `<option value="">所有操作</option>${options([...actions], selected)}`;
}

/** 以固定等级顺序展示当前保留期限。 */
export function renderRetentionValues(retention: AuditRetention): string {
  return auditLevels.map(([key, label]) => `<div class="audit-retention-value">${badge("risk", label)}
    <strong data-audit-days="${key}">${retention[key] === 0 ? "不保留" : `${h(retention[key])} 天`}</strong></div>`).join("");
}

/** 以可访问的表头和按钮渲染审计结果。 */
export function renderAuditRows(items: AuditOperation[]): string {
  if (!items.length) return '<tr><td class="catalog-empty" colspan="7">暂无符合条件的审计记录。</td></tr>';
  return items.map((item) => `<tr><td>${auditTime(item.at)}</td><td>${h(item.actor || "—")}</td>
    <td>${h(item.action)}</td><td>${badge("risk", item.risk)}</td><td>${h(item.target || "—")}</td>
    <td>${badge("result", item.result)}</td><td><button class="button secondary" type="button" data-audit-detail="${h(item.id)}"
      aria-label="查看 ${h(item.action)} ${h(item.target || "")} 的详情">查看详情</button></td></tr>`).join("");
}

/** 自定义值显示为当前选项，并保留入口供再次修改。 */
export function renderAuditLimitOptions(limit: number): string {
  const sizes = auditPageSizes.includes(limit) ? auditPageSizes : [...auditPageSizes, limit];
  return `${options(sizes.map(String), String(limit), " 条/页")}<option value="custom">自定义…</option>`;
}

/** 分开呈现保留策略、检索工具和事件清单。 */
export function renderAuditWorkspace(page: AuditListPage, metadata: AuditPage, filters: AuditFilters, canEdit: boolean): string {
  const pages = auditPageCount(page.total, page.limit);
  return `<div class="audit-workspace" data-audit-workspace>
    <div class="platform-heading"><div><h2>操作审计</h2><p>查询账号变更、认证事件和逐项批量结果。</p></div></div>
    ${metadata.retention ? `<section class="system-card audit-retention" aria-labelledby="audit-policy-heading">
      <header class="audit-section-heading"><h3 id="audit-policy-heading" tabindex="-1">审计保留策略</h3>
        ${canEdit ? '<button class="button" type="button" data-audit-edit>编辑保留策略</button>' : ""}</header>
      <div class="audit-retention-grid" data-audit-retention-values>${renderRetentionValues(metadata.retention)}</div>
      <p class="audit-status" role="status" data-audit-retention-status></p>
    </section>` : ""}
    <section class="system-card audit-records" aria-labelledby="audit-records-heading">
      <header class="audit-section-heading"><h3 id="audit-records-heading" tabindex="-1">审计记录</h3></header>
      <form class="audit-filters" data-audit-filter autocomplete="off">
        <label class="audit-search">账号或目标<input name="account" type="search" spellcheck="false" value="${h(filters.account ?? "")}"></label>
        <label>操作类型<select name="action">${renderAuditActionOptions(metadata, filters.action)}</select></label>
        <label>风险等级<select name="risk"><option value="">所有等级</option>${options(auditLevels.map(([, label]) => label), filters.risk, "风险")}</select></label>
        <button class="button" type="submit">查询</button>
      </form>
      <div class="audit-batch-filter" data-audit-active-batch${filters.batch ? "" : " hidden"}>
        <p>同批次 <code data-audit-batch-id>${h(filters.batch ?? "")}</code></p>
        <button class="button secondary" type="button" data-audit-clear-batch>清除批次</button>
      </div>
      <p class="audit-status" role="status" data-audit-query-status></p>
      <div class="audit-table-wrap" role="region" aria-labelledby="audit-records-heading" tabindex="0">
        <table class="catalog-table audit-table">
          <thead><tr><th scope="col">时间（北京时间）</th><th scope="col">操作者</th><th scope="col">操作类型</th>
            <th scope="col">风险等级</th><th scope="col">目标</th><th scope="col">结果</th><th scope="col">操作</th></tr></thead>
          <tbody data-audit-results>${renderAuditRows(page.items)}</tbody>
        </table>
      </div>
      <footer class="audit-list-footer">
        <nav class="audit-pagination" aria-label="审计记录分页">
          <span data-audit-count>共 ${page.total} 条</span>
          <button class="button secondary" type="button" aria-label="上一页" data-audit-previous${page.page <= 1 ? " disabled" : ""}>‹</button>
          <span class="audit-page-number" data-audit-page aria-live="polite">第 ${page.page} / ${pages} 页</span>
          <button class="button secondary" type="button" aria-label="下一页" data-audit-next${page.page >= pages ? " disabled" : ""}>›</button>
          <select data-audit-limit aria-label="每页条数" aria-controls="audit-custom-page-size">${renderAuditLimitOptions(page.limit)}</select>
        </nav>
        <form class="audit-limit-editor" id="audit-custom-page-size" data-audit-limit-form hidden>
          <label>自定义每页条数
            <input type="number" name="customLimit" min="1" max="${maxAuditLimit}" step="1" required aria-describedby="audit-limit-range">
          </label>
          <small id="audit-limit-range">1–${maxAuditLimit} 条</small>
          <button class="button" type="submit">应用</button>
          <button class="button secondary" type="button" data-audit-limit-cancel>取消</button>
        </form>
      </footer>
    </section>
    <dialog class="audit-dialog" data-audit-dialog aria-labelledby="audit-dialog-heading"></dialog>
  </div>`;
}

/** 使审计各弹窗共用可滚动正文与可见操作区。 */
function dialogContent(title: string, body: string, buttons: string, header = ""): string {
  return `<header class="audit-dialog-header"><h2 id="audit-dialog-heading" tabindex="-1">${h(title)}</h2>${header}</header>
    <div class="audit-dialog-body">${body}</div><footer class="audit-dialog-footer">${buttons}</footer>`;
}

const closeButton = '<button class="button secondary" type="button" data-audit-close>关闭</button>';
const dialogStatus = '<p role="status" class="audit-status" data-audit-dialog-status></p>';

/** 显示已选事件以及异步加载的同批次逐项结果。 */
export function renderAuditDetail(item: AuditOperation): string {
  const metadata = [["操作者", item.actor], ["目标", item.target], ["时间（北京时间）", auditTime(item.at)]];
  if (item.batch) metadata.push(["批次", item.batch]);
  const body = `<dl class="audit-event-meta">${metadata.map(([label, value]) => `<div><dt>${label}</dt><dd>${h(value || "—")}</dd></div>`).join("")}</dl>
    ${item.reason ? `<div class="audit-reason"><h3>失败原因</h3><p>${h(item.reason)}</p></div>` : ""}
    ${item.batch ? `<section class="audit-batch-results" aria-labelledby="audit-batch-heading"><h3 id="audit-batch-heading">批次结果</h3>
      <div data-audit-batch-results aria-busy="true"><p role="status">正在加载批次结果…</p></div></section>` : ""}`;
  return dialogContent(item.action, body, `${item.batch ? '<button class="button secondary" type="button" data-audit-show-batch>查看同批次</button>' : ""}${closeButton}`,
    `<div class="audit-event-badges">${badge("risk", item.risk)}${badge("result", item.result)}</div>`);
}

/** 同批次列表以目标、结果和原因突出逐项差异。 */
export function renderAuditBatch(page: AuditPage): string {
  // 服务端同时保存批次汇总和逐项事件，详情仅列出逐项结果。
  const items = page.items.filter(({ target }) => target !== "批量操作");
  if (!items.length) return "<p>暂无可查看的逐项记录。</p>";
  return `<div class="audit-table-wrap" role="region" aria-labelledby="audit-batch-heading" tabindex="0">
    <table class="catalog-table audit-batch-table"><thead><tr><th scope="col">对象</th><th scope="col">结果</th><th scope="col">说明</th></tr></thead>
    <tbody>${items.map((item) => `<tr><td>${h(item.target)}</td><td>${badge("result", item.result)}</td><td>${h(item.reason || "—")}</td></tr>`).join("")}</tbody></table></div>
    ${page.before ? '<p class="audit-status">使用「查看同批次」翻阅其余结果。</p>' : ""}`;
}

/** 编辑窗口解释保留期计算方式和 0 天的效果。 */
export function renderRetentionEditor(retention: AuditRetention): string {
  const fields = `<p id="audit-retention-help">按记录发生时间计算。保存会清理超过新期限的记录。填 0 表示不保留。</p>
    <div class="audit-retention-grid">${auditLevels.map(([key, label]) => `<label>${label}风险（天）<input name="${key}" type="number"
      inputmode="numeric" min="0" max="${maxRetentionDays}" step="1" required value="${h(retention[key])}" aria-describedby="audit-retention-help"></label>`).join("")}</div>${dialogStatus}`;
  return `<form data-audit-retention autocomplete="off">${dialogContent("编辑保留策略", fields,
    '<button class="button secondary" type="button" data-audit-close>取消</button><button class="button" type="submit">保存保留策略</button>')}</form>`;
}

/** 清除整个等级前单独确认，并将取消放在首位。 */
export function renderRetentionConfirmation(levels: string[]): string {
  return `<form data-audit-confirm>${dialogContent("确认不保留审计记录",
    `<p class="audit-reason">保存后立即清除${h(levels.join("、"))}的所有现有记录，后续也不再保存这些记录。</p>${dialogStatus}`,
    '<button class="button secondary" type="button" data-audit-back>取消</button><button class="button catalog-danger-fill" type="submit">确认保存并清理</button>')}</form>`;
}
