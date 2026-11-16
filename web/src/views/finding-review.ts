export function findingReviewControls(): string {
  return `<button type="button" class="button secondary finding-review-trigger" data-open-review>AI 分析</button>
    <dialog class="finding-review-dialog" aria-labelledby="finding-review-title">
      <form class="finding-review-form">
        <h2 id="finding-review-title">AI 分析</h2>
        <p>补充证据，复核当前诊断。提交后，证据与历史结论会发送给配置的 AI 服务，请勿填写凭据或个人敏感信息。</p>
        <details><summary>原始结论与复核历史</summary><div data-review-history></div></details>
        <label>数据来源<textarea name="source" rows="2" required maxlength="1024" placeholder="例如 14:30 的 Pod 事件或监控查询"></textarea></label>
        <label>观察结论<textarea name="observation" rows="4" required maxlength="4000" placeholder="描述数据结果，以及它和现有结论的差异"></textarea></label>
        <p role="status"></p>
        <div class="finding-review-actions">
          <button type="button" class="button secondary" data-close-review>关闭</button>
          <button type="submit" class="button" disabled>分析并更新结论</button>
        </div>
      </form>
    </dialog>`;
}

export function bindFindingReview(root: HTMLElement, id: string, cluster = "local"): void {
  const trigger = root.querySelector<HTMLButtonElement>("[data-open-review]");
  const dialog = root.querySelector<HTMLDialogElement>(".finding-review-dialog");
  const form = dialog?.querySelector<HTMLFormElement>("form");
  const status = form?.querySelector<HTMLElement>('[role="status"]');
  const history = form?.querySelector<HTMLElement>("[data-review-history]");
  const submit = form?.querySelector<HTMLButtonElement>('[type="submit"]');
  const close = form?.querySelector<HTMLButtonElement>("[data-close-review]");
  const explanation = root.querySelector<HTMLElement>("[data-finding-explanation]");
  if (!trigger || !dialog || !form || !status || !history || !submit || !close || !explanation) return;
  const url = `/api/v1/admin/finding-analysis?${new URLSearchParams({ id, cluster })}`;
  let revision = 0;
  let busy = false;

  close.addEventListener("click", () => dialog.close());
  dialog.addEventListener("cancel", (event) => {
    if (busy) event.preventDefault();
  });
  trigger.addEventListener("click", async () => {
    submit.disabled = true;
    close.disabled = true;
    busy = true;
    history.replaceChildren();
    status.textContent = "正在加载复核记录…";
    dialog.showModal();
    try {
      const response = await fetch(url);
      if (!response.ok) throw new Error("review unavailable");
      const data = await response.json() as { original: string; history: { revision: number; conclusion: string }[] };
      revision = data.history[0]?.revision ?? 0;
      const conclusions: [string, string][] = [["原始结论", data.original],
        ...data.history.map((item): [string, string] => [`复核 #${item.revision}`, item.conclusion])];
      for (const [label, conclusion] of conclusions) {
        const title = document.createElement("strong");
        title.textContent = label;
        const text = document.createElement("p");
        text.className = "detail-text";
        text.textContent = conclusion || "暂无结论。";
        history.append(title, text);
      }
      status.textContent = "";
      submit.disabled = false;
    } catch {
      status.textContent = "无法加载复核记录，请关闭后重试。";
    } finally {
      busy = false;
      close.disabled = false;
    }
  });

  form.addEventListener("submit", async (event) => {
    event.preventDefault();
    if (submit.disabled) return;
    const fields = new FormData(form);
    const source = String(fields.get("source") ?? "").trim();
    const observation = String(fields.get("observation") ?? "").trim();
    const encoder = new TextEncoder();
    if (!source || !observation || encoder.encode(source).length > 1024 || encoder.encode(observation).length > 4000) {
      status.textContent = "请填写来源和结论，长度分别不能超过 1024 和 4000 字节。";
      return;
    }
    busy = true;
    submit.disabled = true;
    close.disabled = true;
    status.textContent = "AI 正在复核，请稍候…";
    let stale = false;
    try {
      const response = await fetch(url, {
        method: "POST",
        headers: { "Content-Type": "application/json" },
        body: JSON.stringify({ source, observation, revision }),
      });
      if (response.status === 409) {
        stale = true;
        status.textContent = "其他复核已更新诊断，请关闭后重新打开窗口。";
        return;
      }
      if (response.status === 502 && response.headers.get("Content-Type")?.includes("application/json")) {
        const { code } = await response.json() as { code?: string };
        const messages: Record<string, string> = {
          AI_ENGINE_PERMISSION_DENIED: "API 无权读取 Engine 或 Secret，请检查 RBAC。",
          AI_ENGINE_ENDPOINT_NOT_APPROVED: "Engine 模型地址未获批准，请检查 API 的允许地址。",
          AI_ENGINE_CONFIG_INVALID: "Engine 的 AI 配置无效，请检查模型、地址和 Secret 引用。",
          AI_ENGINE_UNAVAILABLE: "无法读取 Engine，请检查实例名称、命名空间和集群连接。",
          AI_ENGINE_SECRET_UNAVAILABLE: "无法读取 Secret 或指定密钥项，请检查 Engine 的 Secret 引用。",
          AI_PROVIDER_UNAVAILABLE: "模型调用失败，请检查服务连通性和模型凭据。",
        };
        status.textContent = code && Object.hasOwn(messages, code) ? messages[code] ?? "" : "AI 分析失败。";
        return;
      }
      if (!response.ok) throw new Error("analysis failed");
      const result = await response.json() as { conclusion: string };
      explanation.textContent = `AI 复核结论\n\n${result.conclusion}`;
      form.reset();
      dialog.close();
    } catch {
      status.textContent = "暂时无法确认复核结果。请重新打开窗口查看记录后再试。";
    } finally {
      busy = false;
      submit.disabled = stale;
      close.disabled = false;
    }
  });
}
