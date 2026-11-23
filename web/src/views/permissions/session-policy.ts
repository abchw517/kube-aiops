const endpoint = "/api/v1/admin/session-policy";
const minMinutes = 5;
const maxMinutes = 480;
const rangeMessage = "请输入 5 至 480 之间的整数。";

/** 在网络边界验证会话有效期。 */
function policyMinutes(data: unknown): number {
  const value = (data as { sessionLifetimeMinutes?: unknown } | null)?.sessionLifetimeMinutes;
  if (typeof value !== "number" || !Number.isInteger(value) || value < minMinutes || value > maxMinutes) {
    throw new Error("Invalid session policy");
  }
  return value;
}

/** 读取服务端已保存的会话有效期。 */
export async function loadSessionPolicy(): Promise<number> {
  const response = await fetch(endpoint);
  if (!response.ok) throw response;
  return policyMinutes(await response.json());
}

/** 默认收起会话设置，始终在标题行显示最近保存的有效期。 */
export function renderSessionPolicy(minutes: number | null, canEdit: boolean): string {
  const settings = minutes === null
    ? '<p class="auth-policy-error" role="alert">无法读取登录有效期，请刷新重试。</p>'
    : canEdit
      ? `<form data-session-policy data-saved-minutes="${minutes}" novalidate>
          <label for="session-minutes">登录有效期（分钟）</label>
          <div class="auth-policy-controls">
            <input id="session-minutes" name="sessionLifetimeMinutes" type="number" inputmode="numeric"
              min="${minMinutes}" max="${maxMinutes}" step="1" required value="${minutes}"
              aria-describedby="session-policy-hint session-policy-error">
            <button class="button" type="submit" disabled>保存设置</button>
          </div>
          <p class="auth-policy-hint" id="session-policy-hint">请输入 5 至 480 之间的整数，默认 30 分钟。</p>
          <p class="auth-policy-error" id="session-policy-error" role="alert"></p>
          <p class="auth-policy-status" role="status"></p>
        </form>`
      : '<p>仅本地超级管理员可修改登录有效期。</p>';
  return `<details class="auth-session-card"${minutes === null ? " open" : ""}>
    <summary class="auth-session-summary">登录会话<span class="auth-session-current">当前有效期
      <strong data-current-minutes>${minutes === null ? "暂不可用" : `${minutes} 分钟`}</strong></span></summary>
    <div class="auth-session-body">
      <div class="auth-session-settings">${settings}</div>
      <aside class="auth-session-scope"><h2>生效范围</h2>
        <p>本地账号与 LDAP 账号共用。新设置从下次登录起生效，已登录会话按原时间到期。</p>
      </aside>
    </div>
  </details>`;
}

/** 输入变化后清理反馈，仅在值变化时启用保存。 */
export function updateSessionPolicy(target: EventTarget | null): void {
  if (!(target instanceof HTMLInputElement) || !target.form?.matches("[data-session-policy]")) return;
  const form = target.form;
  target.removeAttribute("aria-invalid");
  form.querySelector('[role="alert"]')?.replaceChildren();
  form.querySelector('[role="status"]')?.replaceChildren();
  const button = form.querySelector<HTMLButtonElement>('button[type="submit"]');
  if (button) button.disabled = target.valueAsNumber === Number(form.dataset.savedMinutes);
}

/** 保存表单草稿，失败时保留输入和标题栏中的已保存值。 */
export async function submitSessionPolicy(form: HTMLFormElement, sessionExpired: () => void): Promise<void> {
  const input = form.querySelector<HTMLInputElement>('input[name="sessionLifetimeMinutes"]');
  const button = form.querySelector<HTMLButtonElement>('button[type="submit"]');
  const error = form.querySelector<HTMLElement>('[role="alert"]');
  const status = form.querySelector<HTMLElement>('[role="status"]');
  const current = form.closest("details")?.querySelector("[data-current-minutes]");
  if (!input || !button || button.disabled || !error || !status || !current) return;
  const minutes = input.valueAsNumber;
  error.textContent = "";
  if (!input.validity.valid || !Number.isInteger(minutes)) {
    input.setAttribute("aria-invalid", "true");
    error.textContent = rangeMessage;
    input.focus();
    return;
  }
  button.disabled = true;
  input.readOnly = true;
  status.textContent = "正在保存…";
  try {
    const response = await fetch(endpoint, {
      method: "PUT",
      headers: { "Content-Type": "application/json" },
      body: JSON.stringify({ sessionLifetimeMinutes: minutes }),
    });
    if (!form.isConnected) return;
    if (response.status === 401) {
      sessionExpired();
      return;
    }
    if (!response.ok) {
      status.textContent = "";
      error.textContent = response.status === 403 ? "仅本地超级管理员可修改登录有效期。"
        : response.status === 400 ? rangeMessage : "无法保存登录有效期，当前设置未更改。请重试。";
      return;
    }
    const saved = policyMinutes(await response.json());
    form.dataset.savedMinutes = String(saved);
    input.value = String(saved);
    current.textContent = `${saved} 分钟`;
    status.textContent = "已保存，新设置将在下次登录时生效。";
  } catch {
    status.textContent = "";
    error.textContent = "无法确认保存结果。请刷新查看当前有效期。";
  } finally {
    input.readOnly = false;
    button.disabled = input.valueAsNumber === Number(form.dataset.savedMinutes);
  }
}
