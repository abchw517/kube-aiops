import { ApiError } from "../../api/generated.ts";
import { fetchAuditPage, saveAuditRetention } from "./audit-api.ts";
import {
  auditLocation, newlyDisabledAuditLevels, readAuditFilters, readAuditRetention,
  type AuditFilters, type AuditOperation, type AuditPage, type AuditRetention,
} from "./audit-data.ts";
import {
  renderAuditActionOptions, renderAuditBatch, renderAuditDetail, renderAuditRows, renderAuditWorkspace, renderRetentionConfirmation,
  renderRetentionEditor, renderRetentionValues,
} from "./audit-view.ts";

/** 加载首屏及未筛选的策略和操作类型。 */
export async function loadAudit(filters: AuditFilters, canEdit: boolean) {
  const metadata = await fetchAuditPage({});
  const page = Object.keys(filters).length ? await fetchAuditPage(filters) : metadata;
  return { filters, page, retention: metadata.retention, canEdit, content: renderAuditWorkspace(page, metadata, filters, canEdit) };
}

/** 审计页拥有本页查询、游标、弹窗和草稿，路由负责权限失效后的去向。 */
export function bindAudit(root: HTMLElement, initial: Awaited<ReturnType<typeof loadAudit>>, onAccessError: (status: number) => void): void {
  const workspace = root.querySelector<HTMLElement>("[data-audit-workspace]");
  const form = workspace?.querySelector<HTMLFormElement>("[data-audit-filter]");
  const dialog = workspace?.querySelector<HTMLDialogElement>("[data-audit-dialog]");
  const results = workspace?.querySelector<HTMLElement>("[data-audit-results]");
  const count = workspace?.querySelector<HTMLElement>("[data-audit-count]");
  const previous = workspace?.querySelector<HTMLButtonElement>("[data-audit-previous]");
  const next = workspace?.querySelector<HTMLButtonElement>("[data-audit-next]");
  const activeBatch = workspace?.querySelector<HTMLElement>("[data-audit-active-batch]");
  const batchID = activeBatch?.querySelector("[data-audit-batch-id]");
  const queryStatus = workspace?.querySelector<HTMLElement>("[data-audit-query-status]");
  const actionField = form?.querySelector<HTMLSelectElement>('select[name="action"]');
  if (!workspace || !form || !dialog || !results || !count || !previous || !next || !activeBatch || !batchID || !queryStatus) return;

  const retentionValues = workspace.querySelector("[data-audit-retention-values]");
  const retentionStatus = workspace.querySelector("[data-audit-retention-status]");
  const recordsHeading = workspace.querySelector<HTMLElement>("#audit-records-heading");
  const listButtons = [...workspace.querySelectorAll<HTMLButtonElement>("[data-audit-filter] button, [data-audit-clear-batch], [data-audit-next], [data-audit-previous]")];
  let page = initial.page;
  let filters = initial.filters;
  let retention = initial.retention;
  let cursors: (string | undefined)[] = [];
  let busy = false;
  let saving = false;
  let draft: AuditRetention | undefined;
  let selected: AuditOperation | undefined;
  let trigger: HTMLButtonElement | null = null;
  let location = window.location.hash;
  const isCurrent = () => workspace.isConnected && window.location.hash === location;

  // 先确认结果仍属于本次渲染，过期请求无权改变另一个页面的会话界面。
  const handleAccessError = (error: unknown): boolean => {
    if (!(error instanceof ApiError) || (error.status !== 401 && error.status !== 403)) return false;
    if (isCurrent()) onAccessError(error.status);
    return true;
  };

  const setPage = (view: AuditPage, active: AuditFilters, trail: (string | undefined)[]): void => {
    page = view;
    filters = active;
    cursors = trail;
    results.innerHTML = renderAuditRows(page.items);
    count.textContent = `当前页 ${page.items.length} 条记录`;
    previous.hidden = !filters.before;
    next.hidden = !page.before;
    activeBatch.hidden = !filters.batch;
    batchID.textContent = filters.batch ?? "";
    const visibleFilters = ["account", "action", "risk"] as const;
    for (const name of visibleFilters) {
      const field = form.elements.namedItem(name);
      if (field instanceof HTMLInputElement || field instanceof HTMLSelectElement) field.value = filters[name] ?? "";
    }
    location = auditLocation(filters);
    window.history.replaceState(window.history.state, "", location);
  };

  const query = async (active: AuditFilters, trail: (string | undefined)[], afterSave = false): Promise<void> => {
    if (busy) return;
    busy = true;
    for (const button of listButtons) button.disabled = true;
    results.setAttribute("aria-busy", "true");
    queryStatus.textContent = "正在查询…";
    try {
      const viewRequest = fetchAuditPage(active);
      const [view, metadata] = afterSave && Object.values(active).some(Boolean)
        ? await Promise.all([viewRequest, fetchAuditPage({})]) : [await viewRequest, undefined];
      if (!isCurrent()) return;
      setPage(view, active, trail);
      const actionCatalog = metadata ?? (view.actions ? view : undefined);
      if (actionField && actionCatalog) actionField.innerHTML = renderAuditActionOptions(actionCatalog, actionField.value);
      queryStatus.textContent = "";
      if (!afterSave) recordsHeading?.focus();
    } catch (error) {
      if (!handleAccessError(error)) queryStatus.textContent = "审计记录查询失败，请重试。";
    } finally {
      results.removeAttribute("aria-busy");
      for (const button of listButtons) button.disabled = false;
      busy = false;
    }
  };

  const showDialog = (content: string, focus = "#audit-dialog-heading"): void => {
    dialog.innerHTML = content;
    if (!dialog.open) dialog.showModal();
    dialog.querySelector<HTMLElement>(focus)?.focus();
  };

  const loadBatch = async (batch: string): Promise<void> => {
    const region = dialog.querySelector<HTMLElement>("[data-audit-batch-results]");
    if (!region) return;
    try {
      const view = await fetchAuditPage({ batch });
      if (region.isConnected && dialog.open) region.innerHTML = renderAuditBatch(view);
    } catch (error) {
      if (region.isConnected && dialog.open && !handleAccessError(error)) {
        region.textContent = "批次结果加载失败。可关闭详情后重试，或查看同批次。";
      }
    } finally { region.removeAttribute("aria-busy"); }
  };

  const save = async (days: AuditRetention): Promise<void> => {
    const status = dialog.querySelector("[data-audit-dialog-status]");
    if (!status || saving || !initial.canEdit || !isCurrent()) return;
    saving = true;
    const controls = dialog.querySelectorAll<HTMLButtonElement | HTMLInputElement>("button, input");
    for (const control of controls) control.disabled = true;
    dialog.setAttribute("aria-busy", "true");
    status.textContent = "正在保存…";
    try {
      await saveAuditRetention(days);
      if (!isCurrent()) return;
      retention = days;
      if (retentionValues) retentionValues.innerHTML = renderRetentionValues(days);
      if (retentionStatus) retentionStatus.textContent = "已保存保留策略。";
      dialog.close();
      // 保存后旧行可能已被清理，刷新失败时也不继续显示旧结果。
      const active = { ...filters, before: undefined };
      setPage({ items: [], before: 0 }, active, []);
      await query(active, [], true);
    } catch (error) {
      if (!handleAccessError(error)) status.textContent = error instanceof ApiError && typeof error.body === "string" && error.body
        ? error.body : "保存失败，请重试。";
    } finally {
      dialog.removeAttribute("aria-busy");
      for (const control of controls) control.disabled = false;
      saving = false;
    }
  };

  workspace.addEventListener("submit", (event) => {
    const target = event.target;
    if (!(target instanceof HTMLFormElement)) return;
    event.preventDefault();
    if (!isCurrent()) return;
    if (target === form) {
      void query(readAuditFilters(new FormData(form)), []);
    } else if (target.hasAttribute("data-audit-retention") && initial.canEdit && retention) {
      const days = readAuditRetention(new FormData(target));
      if (!days) {
        const status = dialog.querySelector("[data-audit-dialog-status]");
        if (status) status.textContent = "请为每个风险等级填写有效的非负整数天数。";
        return;
      }
      const disabled = newlyDisabledAuditLevels(retention, days);
      if (!disabled.length) void save(days);
      else {
        draft = days;
        showDialog(renderRetentionConfirmation(disabled), "[data-audit-back]");
      }
    } else if (target.hasAttribute("data-audit-confirm") && draft) void save(draft);
  });

  workspace.addEventListener("click", (event) => {
    const target = event.target;
    if (!(target instanceof HTMLElement) || saving || !isCurrent()) return;
    const button = target.closest<HTMLButtonElement>("button");
    if (!button || busy) return;
    if (button.hasAttribute("data-audit-next")) void query({ ...filters, before: String(page.before) }, [...cursors, filters.before]);
    else if (button.hasAttribute("data-audit-previous")) void query({ ...filters, before: cursors.at(-1) }, cursors.slice(0, -1));
    else if (button.hasAttribute("data-audit-clear-batch")) void query({ ...filters, batch: undefined, before: undefined }, []);
    else if (button.hasAttribute("data-audit-show-batch") && selected?.batch) {
      const batch = selected.batch;
      dialog.close();
      void query({ batch }, []);
    } else if (button.hasAttribute("data-audit-close")) dialog.close();
    else if (button.hasAttribute("data-audit-back") && draft) showDialog(renderRetentionEditor(draft));
    else if (button.hasAttribute("data-audit-edit") && initial.canEdit && retention) {
      trigger = button;
      showDialog(renderRetentionEditor(retention));
    } else if (button.hasAttribute("data-audit-detail")) {
      selected = page.items.find((item) => String(item.id) === button.dataset.auditDetail);
      if (!selected) return;
      trigger = button;
      showDialog(renderAuditDetail(selected));
      if (selected.batch) void loadBatch(selected.batch);
    }
  });
  dialog.addEventListener("cancel", (event) => { if (saving) event.preventDefault(); });
  dialog.addEventListener("close", () => {
    if (dialog.open) return;
    selected = undefined;
    draft = undefined;
    if (trigger?.isConnected) trigger.focus();
    trigger = null;
  });
}
