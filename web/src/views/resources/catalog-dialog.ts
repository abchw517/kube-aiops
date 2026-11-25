import { escapeHtml as h } from "../../components/html.ts";
import { catalogDefinition, type CatalogEntry, referenceLabel, validateCatalog } from "./catalog-model.ts";
import type { CatalogPage } from "./catalog-workspace.ts";
import { resourceDetail, restoreResourceFocus } from "./resource-workspace.ts";

type Action = "add" | "edit" | "delete";
type Draft = { action: Action; original?: CatalogEntry; unlocked: boolean };

function editorFields(draft: Draft, page: CatalogPage): string {
  const { noun, nameExample, idExample } = catalogDefinition(page.kind);
  const original = draft.original;
  return `<label for="catalog-entry-name">${noun}名称</label><input id="catalog-entry-name" name="name" value="${h(original?.name)}" placeholder="${h(nameExample)}" required aria-describedby="catalog-name-error">
    <p id="catalog-name-error" class="catalog-field-error" hidden></p>
    ${original ? `<div data-catalog-id-summary><span class="catalog-field-label">当前 ID</span><div class="catalog-id catalog-id-box">
      <code>${h(original.id)}</code><div class="catalog-actions">
      <button type="button" class="button secondary catalog-quiet" data-copy-current>复制 ID</button>
      <button type="button" class="button secondary catalog-quiet" data-unlock-id>修改 ID</button></div></div></div>` : ""}
    <div data-catalog-id-fields${original ? " hidden" : ""}>
      <label for="catalog-entry-id">${original ? "新 ID" : `${noun} ID`}</label>
      <input id="catalog-entry-id" name="id" value="${h(original?.id)}" placeholder="${h(idExample)}" autocomplete="off" spellcheck="false"
        aria-describedby="catalog-id-hint catalog-id-error" ${original ? "readonly" : 'required maxlength="128" pattern="[a-z][a-z0-9\\-]*"'}>
      <p class="muted" id="catalog-id-hint">以小写字母开头，可包含小写字母、数字和连字符，最多 128 个字符。</p>
      <p id="catalog-id-error" class="catalog-field-error" hidden></p>
      ${original ? `<p class="resource-notice catalog-id-impact">当前 ID <code>${h(original.id)}</code>。保存后，平台通过历史别名保留原引用。请核对使用该 ID 的外部脚本和配置。</p>
        <button type="button" class="button secondary catalog-quiet" data-lock-id>取消修改 ID</button>` : ""}
    </div>`;
}

function referencesMarkup(references: string[]): string {
  return `<p>该条目仍被引用，当前无法删除。</p><ul class="catalog-references" aria-label="引用清单">${references.map(ref =>
    `<li><span>${h(ref)}</span><small>${referenceLabel(ref)}</small></li>`).join("")}</ul>
    <p class="muted">请先在集群管理、命名空间归属或权限管理中处理相关引用，再重试删除。</p>
    <button type="button" class="button secondary" data-copy-references>复制引用清单</button>`;
}

export function bindCatalogDialog(region: HTMLElement, page: CatalogPage, search: HTMLInputElement, report: (message: string) => void,
  onSaved: (action: Action, original?: CatalogEntry, saved?: CatalogEntry) => void) {
  const detailNode = region.querySelector<HTMLDialogElement>("[data-catalog-detail]");
  const editorNode = region.querySelector<HTMLDialogElement>("[data-catalog-dialog]");
  const discardNode = region.querySelector<HTMLDialogElement>("[data-catalog-discard]");
  if (!detailNode || !editorNode || !discardNode) return;
  const drawer = detailNode;
  const dialog = editorNode;
  const discard = discardNode;
  const definition = catalogDefinition(page.kind);
  let draft: Draft | undefined;
  let detailOrigin: HTMLElement | null = null;
  let editOrigin: HTMLElement | null = null;
  let detailEntry: CatalogEntry | undefined;
  let restoreID: string | undefined;
  let busy = false;
  let references: string[] = [];

  const field = (key: "name" | "id") => dialog.querySelector<HTMLInputElement>(`[name="${key}"]`);
  const status = () => dialog.querySelector<HTMLElement>("[data-catalog-form-status]");
  const submit = () => dialog.querySelector<HTMLButtonElement>('button[type="submit"]');
  const fallback = (id?: string) => id ? region.querySelector<HTMLElement>(`tr[data-entry-id="${CSS.escape(id)}"] [data-catalog-action=detail]`) ?? search : search;

  function paintDetail(row: CatalogEntry): void {
    drawer.innerHTML = resourceDetail("catalog-detail-title", row.name, `${definition.noun}详情`,
      `<section class="resource-detail-card"><h3>基本信息</h3><dl><dt>名称</dt><dd>${h(row.name)}</dd>
        <dt>当前 ID</dt><dd class="catalog-id"><code>${h(row.id)}</code>
        <button type="button" class="button secondary catalog-quiet" data-detail-copy>复制 ID</button></dd></dl></section>
       <section class="resource-detail-card"><h3>关联用途</h3><p class="muted">${h(definition.usage)}</p></section>`,
      `<div class="catalog-actions"><button type="button" class="button secondary" data-resource-close>关闭</button>
       ${page.editable ? `<button type="button" class="button" data-detail-edit>编辑${definition.noun}</button>` : ""}</div>`);
  }

  function values(current: Draft): CatalogEntry {
    return {
      name: field("name")?.value.trim() ?? "",
      id: current.unlocked ? field("id")?.value.trim() ?? "" : current.original?.id ?? "",
    };
  }

  function changed(current: Draft): boolean {
    if (current.action === "delete") return false;
    const next = values(current);
    return next.name !== (current.original?.name ?? "") || next.id !== (current.original?.id ?? "");
  }

  function updateSave(): void {
    const button = submit();
    if (button && draft?.action !== "delete") button.disabled = busy || !draft || !changed(draft);
  }

  function setFieldError(key: "name" | "id", message = ""): void {
    const node = dialog.querySelector<HTMLElement>(`#catalog-${key}-error`);
    const input = field(key);
    if (node) { node.hidden = !message; node.textContent = message; }
    if (!input) return;
    if (message) {
      input.setAttribute("aria-invalid", "true");
      input.focus();
    } else input.removeAttribute("aria-invalid");
  }

  function toggleID(unlocked: boolean): void {
    if (!draft?.original || busy) return;
    draft.unlocked = unlocked;
    dialog.querySelector("[data-catalog-id-summary]")?.toggleAttribute("hidden", unlocked);
    dialog.querySelector("[data-catalog-id-fields]")?.toggleAttribute("hidden", !unlocked);
    const id = field("id");
    if (!id) return;
    id.readOnly = !unlocked;
    if (unlocked) id.focus();
    else {
      id.value = draft.original.id;
      setFieldError("id");
      dialog.querySelector<HTMLElement>("[data-unlock-id]")?.focus();
    }
    const feedback = status();
    if (feedback) feedback.textContent = changed(draft) ? "有未保存的修改。" : "";
    updateSave();
  }

  function open(action: Action, original: CatalogEntry | undefined, origin: HTMLElement): void {
    if (!page.editable || dialog.open || (action !== "add" && !original)) return;
    draft = { action, original, unlocked: action === "add" };
    editOrigin = origin;
    restoreID = original?.id;
    references = [];
    const removing = action === "delete";
    const noun = definition.noun;
    const title = `${removing ? "删除" : action === "add" ? "新增" : "编辑"}${noun}`;
    const body = removing ? `<p>确认删除 <strong>${h(original?.name)}</strong> <code>${h(original?.id)}</code>？</p>
      <p class="muted">提交后，平台会核对该${noun}是否仍被引用。</p><div data-catalog-conflict hidden></div>` : editorFields(draft, page);
    const footer = `<p role="status" data-catalog-form-status></p><div class="catalog-actions">
      <button type="button" class="button secondary" data-resource-close>取消</button>
      <button type="submit" class="button${removing ? " catalog-danger-fill" : ""}">${removing ? "确认删除" : "保存"}</button></div>`;
    dialog.innerHTML = `<form class="resource-overlay-form" data-resource-catalog="${page.kind}" data-operation="${action}" novalidate>
      ${resourceDetail("catalog-dialog-title", title, action === "add" ? "填写名称和可稳定引用的标识。" : "", body, footer)}</form>`;
    updateSave();
    dialog.showModal();
    (removing ? dialog.querySelector<HTMLElement>("footer [data-resource-close]") : field("name"))?.focus();
  }

  function requestClose(): void {
    if (busy) return;
    if (draft && changed(draft)) discard.showModal();
    else dialog.close();
  }

  async function copy(value: string, trigger: HTMLElement): Promise<void> {
    const owner = trigger.closest("dialog");
    try {
      await navigator.clipboard.writeText(value);
      if (!trigger.isConnected || (owner && !owner.open)) return;
      const message = owner?.querySelector<HTMLElement>("[data-catalog-form-status]");
      if (message) message.textContent = "已复制。";
      else if (!owner) report("已复制 ID。");
      else showCopyFallback(owner, "", "已复制 ID。");
    } catch {
      if (!trigger.isConnected || (owner && !owner.open)) return;
      showCopyFallback(owner ?? region, value, "请复制下面的内容。");
    }
  }

  function showCopyFallback(root: HTMLElement, value: string, message: string): void {
    const area = root.querySelector<HTMLElement>(".resource-overlay-body, .resource-list-body");
    if (!area) return;
    area.querySelector("[data-copy-result]")?.remove();
    const result = document.createElement("div");
    result.dataset.copyResult = "";
    result.className = "resource-notice catalog-copy-result";
    const label = document.createElement("p");
    label.setAttribute("role", "status");
    label.textContent = message;
    result.append(label);
    area.append(result);
    if (!value) return;
    const textarea = document.createElement("textarea");
    textarea.setAttribute("aria-label", "待复制内容");
    textarea.readOnly = true;
    textarea.value = value;
    result.append(textarea);
    textarea.focus();
    textarea.select();
  }

  drawer.addEventListener("click", event => {
    if (!(event.target instanceof Element)) return;
    if (event.target.closest("[data-resource-close]")) drawer.close();
    if (event.target instanceof HTMLButtonElement && detailEntry) {
      if (event.target.hasAttribute("data-detail-copy")) void copy(detailEntry.id, event.target);
      if (event.target.hasAttribute("data-detail-edit")) open("edit", detailEntry, event.target);
    }
  });
  drawer.addEventListener("close", () => {
    if (drawer.open) return;
    restoreResourceFocus(detailOrigin, fallback(detailEntry?.id));
    drawer.replaceChildren();
  });
  dialog.addEventListener("click", event => {
    if (!(event.target instanceof Element)) return;
    const button = event.target.closest<HTMLButtonElement>("button");
    if (!button) return;
    if (button.hasAttribute("data-resource-close")) requestClose();
    if (button.hasAttribute("data-unlock-id")) toggleID(true);
    if (button.hasAttribute("data-lock-id")) toggleID(false);
    if (button.hasAttribute("data-copy-current") && draft?.original) void copy(draft.original.id, button);
    if (button.hasAttribute("data-copy-references")) void copy(references.join("\n"), button);
  });
  dialog.addEventListener("input", event => {
    if (!(event.target instanceof HTMLInputElement) || !draft) return;
    if (event.target.name === "name" || event.target.name === "id") setFieldError(event.target.name);
    const feedback = status();
    if (feedback) feedback.textContent = changed(draft) ? "有未保存的修改。" : "";
    updateSave();
  });
  dialog.addEventListener("cancel", event => { event.preventDefault(); requestClose(); });
  dialog.addEventListener("close", () => {
    if (dialog.open) return;
    restoreResourceFocus(editOrigin, drawer.open ? drawer.querySelector("h2") : fallback(restoreID));
    dialog.replaceChildren();
    draft = undefined;
  });
  discard.querySelector("[data-keep-draft]")?.addEventListener("click", () => discard.close());
  discard.querySelector("[data-discard-draft]")?.addEventListener("click", () => { discard.close(); dialog.close(); });

  dialog.addEventListener("submit", async event => {
    event.preventDefault();
    event.stopPropagation();
    const current = draft;
    const feedback = status();
    const button = submit();
    const body = dialog.querySelector<HTMLElement>(".resource-overlay-body");
    if (!current || !feedback || !button || !body || busy || !page.editable) return;
    const { action, original } = current;
    const removing = action === "delete";
    const next = values(current);
    if (!removing) {
      if (!changed(current)) return;
      const error = validateCatalog(page.items, next, original);
      if (error) { setFieldError(error.field, error.message); return; }
    }
    const method = removing ? "DELETE" : action === "add" ? "POST" : "PUT";
    const payload = removing ? { id: original?.id } : { ...next, originalId: original?.id };
    busy = button.disabled = body.inert = true;
    dialog.setAttribute("aria-busy", "true");
    feedback.textContent = removing ? "正在核对引用…" : "正在保存…";
    try {
      const response = await fetch(`/api/v1/admin/${page.kind}`, { method,
        headers: { "Content-Type": "application/json" }, body: JSON.stringify(payload) });
      if (!region.isConnected) return;
      if (response.ok) {
        const saved = removing ? undefined : await response.json() as CatalogEntry;
        if (!region.isConnected) return;
        if (!removing && (!saved || typeof saved.id !== "string" || typeof saved.name !== "string")) throw new Error("invalid catalog response");
        restoreID = saved?.id;
        onSaved(action, original, saved);
        if (drawer.open && saved && detailEntry?.id === original?.id) {
          detailEntry = saved;
          paintDetail(saved);
        }
        dialog.close();
      } else {
        await handleFailure(response, current, feedback, button);
      }
    } catch {
      feedback.textContent = "操作失败，请重试。";
    } finally {
      busy = body.inert = false;
      dialog.removeAttribute("aria-busy");
      button.disabled = false;
      if (dialog.open) {
        updateSave();
        dialog.querySelector<HTMLElement>('[aria-invalid="true"]')?.focus();
      }
    }
  });

  async function handleFailure(response: Response, current: Draft, feedback: HTMLElement, button: HTMLButtonElement): Promise<void> {
    const contentType = response.headers.get("Content-Type") ?? "";
    if (response.status === 409 && current.action === "delete" && contentType.includes("application/json")) {
      const payload = await response.json() as { references?: unknown };
      references = Array.isArray(payload.references) ? payload.references.filter((item): item is string => typeof item === "string") : [];
      const conflict = dialog.querySelector<HTMLElement>("[data-catalog-conflict]");
      if (conflict && references.length) {
        conflict.hidden = false;
        conflict.innerHTML = referencesMarkup(references);
        const heading = dialog.querySelector<HTMLElement>("h2");
        if (heading) { heading.textContent = "删除受阻"; heading.focus(); }
        feedback.textContent = "已列出阻止删除的引用。";
        const cancel = dialog.querySelector("footer [data-resource-close]");
        if (cancel) cancel.textContent = "返回";
        button.textContent = "重试删除";
        return;
      }
    }
    const raw = contentType.includes("text/plain") ? (await response.text()).trim() : "";
    if (response.status === 401) { feedback.textContent = "会话已过期，请重新登录。"; return; }
    if (response.status === 403) { feedback.textContent = "当前账号无权执行此操作。"; return; }
    if (response.status === 409 && current.action !== "delete") {
      if (raw.startsWith("名称")) setFieldError("name", raw);
      else if (raw.startsWith("ID") && current.unlocked) setFieldError("id", raw);
      else feedback.textContent = raw || "名称或 ID 已被使用，请核对后重试。";
      return;
    }
    feedback.textContent = raw || "操作失败，请重试。";
  }

  return { open, copy, showDetail(row: CatalogEntry, trigger: HTMLElement) {
    detailOrigin = trigger;
    detailEntry = row;
    paintDetail(row);
    drawer.showModal();
    drawer.querySelector<HTMLElement>("h2")?.focus();
  } };
}
