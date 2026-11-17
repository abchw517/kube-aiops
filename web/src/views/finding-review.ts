export function findingReviewControls(): string {
  return `<button type="button" class="button secondary finding-review-trigger" data-open-review>AI 分析</button>
    <dialog class="finding-review-dialog" aria-labelledby="finding-review-title">
      <form class="finding-review-form">
        <h2 id="finding-review-title">AI 分析</h2>
        <p>补充已知信息，与 AI 核对原结论。AI 会参考当前诊断的关联证据。先生成总结草稿，确认后才会保存结论并发布知识。请勿填写凭据或个人敏感信息。</p>
        <details><summary>原始结论与复核历史</summary><div data-review-history></div></details>
        <div data-review-baseline></div>
        <div class="finding-review-conversation" data-review-conversation aria-label="复核对话" role="log"></div>
        <label>你的消息<textarea name="message" rows="3" required maxlength="4000" placeholder="输入已知信息、观察结果，或希望 AI 核实的问题"></textarea></label>
        <p role="status"></p>
        <div class="finding-review-actions">
          <button type="button" class="button secondary" data-close-review>关闭</button>
          <button type="submit" class="button secondary" disabled>发送</button>
          <button type="submit" class="button" data-finalize-review formnovalidate disabled>生成结论草稿</button>
          <button type="submit" class="button" data-confirm-review formnovalidate hidden>确认并发布知识</button>
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
  const finalize = form?.querySelector<HTMLButtonElement>("[data-finalize-review]");
  const confirm = form?.querySelector<HTMLButtonElement>("[data-confirm-review]");
  const baselineView = form?.querySelector<HTMLElement>("[data-review-baseline]");
  const log = form?.querySelector<HTMLElement>("[data-review-conversation]");
  const messageField = form?.querySelector<HTMLTextAreaElement>('[name="message"]');
  const close = form?.querySelector<HTMLButtonElement>("[data-close-review]");
  const explanation = root.querySelector<HTMLElement>("[data-finding-explanation]");
  if (!trigger || !dialog || !form || !status || !history || !submit || !finalize || !confirm || !baselineView || !log || !messageField || !close || !explanation) return;
  const url = `/api/v1/admin/finding-analysis?${new URLSearchParams({ id, cluster })}`;
  let revision = 0;
  let busy = false;
  let baseline = "";
  let draftId = "";
  const messages: {role: "user" | "assistant"; text: string}[] = [];
  const showMessage = (role: "user" | "assistant", text: string) => {
    const entry = document.createElement("div");
    entry.className = `finding-review-message finding-review-message-${role}`;
    const title = document.createElement("strong");
    title.textContent = role === "user" ? "你" : "AI";
    const paragraph = document.createElement("p");
    paragraph.className = "detail-text";
    paragraph.textContent = text;
    entry.append(title, paragraph);
    log.append(entry);
    log.scrollTop = log.scrollHeight;
  };

  close.addEventListener("click", () => dialog.close());
  dialog.addEventListener("cancel", (event) => {
    if (busy) event.preventDefault();
  });
  trigger.addEventListener("click", async () => {
    submit.disabled = true;
    finalize.disabled = true;
    close.disabled = true;
    busy = true;
    history.replaceChildren();
    baselineView.replaceChildren();
    draftId = "";
    confirm.hidden = true;
    status.textContent = "正在加载复核记录…";
    dialog.showModal();
    try {
      const response = await fetch(url);
      if (!response.ok) throw new Error("review unavailable");
      const data = await response.json() as { original: string; history: { revision: number; conclusion: string }[] };
      const latest = data.history[0]?.revision ?? 0;
      if (latest !== revision) {
        messages.length = 0;
        log.replaceChildren();
      }
      revision = latest;
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
      baseline = data.original;
      status.textContent = "正在结合 Wiki 生成增强说明…";
      try {
        const enhanced = await fetch(url, {method: "POST", headers: {"Content-Type": "application/json"}, body: JSON.stringify({action: "enhance", revision})});
        if (enhanced.ok) {
          const result = await enhanced.json() as {conclusion?: string; citations?: {knowledgeId: string; revision: number; title: string}[]};
          baseline = result.conclusion ?? baseline;
          const text = document.createElement("p");
          text.className = "detail-text";
          text.textContent = `增强诊断说明\n\n${baseline}`;
          baselineView.append(text);
          for (const citation of result.citations ?? []) {
            const link = document.createElement("a");
            link.href = `#/wiki/${encodeURIComponent(citation.knowledgeId)}?revision=${citation.revision}`;
            link.textContent = citation.title;
            baselineView.append(link, document.createTextNode(" "));
          }
        }
      } catch {
        // The original explanation remains available if enhancement fails.
      }
      status.textContent = "";
      submit.disabled = messages.length >= 20;
      finalize.disabled = messages.length === 0;
    } catch {
      status.textContent = "无法加载复核记录，请关闭后重试。";
    } finally {
      busy = false;
      close.disabled = false;
    }
  });

  form.addEventListener("submit", async (event) => {
    event.preventDefault();
    const finishing = event.submitter === finalize;
    const publishing = event.submitter === confirm;
    if (busy || (publishing ? !draftId : finishing ? finalize.disabled : submit.disabled)) return;
    const message = messageField.value.trim();
    if (!publishing && (finishing ? message !== "" : !message || new TextEncoder().encode(message).length > 4000)) {
      status.textContent = finishing ? "请先发送尚未提交的消息。" : "请填写消息，长度不能超过 4000 字节。";
      return;
    }
    busy = true;
    submit.disabled = true;
    finalize.disabled = true;
    close.disabled = true;
    messageField.readOnly = true;
    status.textContent = publishing ? "正在确认并发布…" : finishing ? "正在生成草稿…" : "AI 正在复核…";
    let stale = false;
    try {
      const response = await fetch(url, {
        method: "POST",
        headers: { "Content-Type": "application/json" },
        body: JSON.stringify(publishing ? {action: "confirm", draftId, revision} : {action: finishing ? "summarize" : "chat", messages: finishing ? messages : [...messages, { role: "user", text: message }], revision, baseline}),
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
      const result = await response.json() as { conclusion?: string; reply?: string; draftId?: string };
      if (!finishing && !publishing) {
        draftId = "";
        confirm.hidden = true;
        if (!result.reply) throw new Error("missing AI reply");
        messages.push({ role: "user", text: message }, { role: "assistant", text: result.reply });
        showMessage("user", message);
        showMessage("assistant", result.reply);
        form.reset();
        status.textContent = messages.length >= 20 ? "已达到 10 轮，可以生成结论草稿。" : "";
        messageField.focus();
        return;
      }
      if (!result.conclusion) throw new Error("missing conclusion");
      if (!publishing) {
        if (!result.draftId) throw new Error("missing draft");
        draftId = result.draftId;
        confirm.hidden = false;
        showMessage("assistant", `结论草稿\n\n${result.conclusion}`);
        status.textContent = "核对草稿后可确认并发布知识。";
        return;
      }
      explanation.textContent = `AI 复核结论\n\n${result.conclusion}`;
      form.reset();
      dialog.close();
    } catch {
      status.textContent = "暂时无法确认复核结果。请重新打开窗口查看记录后再试。";
    } finally {
      busy = false;
      submit.disabled = stale || messages.length >= 20;
      finalize.disabled = stale || messages.length === 0;
      messageField.readOnly = false;
      close.disabled = false;
    }
  });
}
