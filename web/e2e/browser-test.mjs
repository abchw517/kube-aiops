import { spawn } from "node:child_process";
import { once } from "node:events";
import { mkdtemp, rm } from "node:fs/promises";
import { tmpdir } from "node:os";
import { join } from "node:path";

const chromeBin = process.env.CHROME_BIN;
if (!chromeBin) throw new Error("CHROME_BIN is required");

const port = Number(process.env.CHROME_DEBUG_PORT ?? "9222");
const userDataDir = await mkdtemp(join(tmpdir(), "kube-aiops-chrome-"));
const chrome = spawn(
  chromeBin,
  [
    "--headless=new",
    "--no-sandbox",
    "--disable-gpu",
    "--disable-dev-shm-usage",
    "--no-first-run",
    "--no-default-browser-check",
    `--remote-debugging-port=${port}`,
    `--user-data-dir=${userDataDir}`,
    "about:blank",
  ],
  { stdio: ["ignore", "ignore", "inherit"] },
);

try {
  const page = await waitForPage(port);
  const cdp = await createCdpClient(page.webSocketDebuggerUrl);
  try {
    await cdp.send("Page.enable");
    await cdp.send("Runtime.enable");

    await assertPage(
      cdp,
      "http://127.0.0.1:4173/#/findings",
      (text) =>
        text.includes("诊断控制台") &&
        text.includes("清除筛选") &&
        text.includes("共 3 条诊断结果") &&
        text.includes("CrashLoopBackOff detected") &&
        text.includes("Replica availability degraded") &&
        text.includes("测试集群容量建议"),
      "Default ALL scope did not show findings from both environments",
    );

    const localeEvaluation = await cdp.send("Runtime.evaluate", {
      expression: `({
        language: document.documentElement.lang,
        severity: document.querySelector('select[name="severity"] option[value="critical"]')?.textContent,
        environment: document.querySelector('select[name="environment"]').selectedOptions[0].textContent,
        cluster: document.querySelector('select[name="cluster"]').value
      })`,
      returnByValue: true,
    });
    const locale = localeEvaluation.result?.value;
    if (locale?.language !== "zh-CN" || locale?.severity !== "严重" ||
        locale?.environment !== "全部环境（ALL）" || locale?.cluster !== "") {
      throw new Error(`Chinese locale or severity label missing: ${JSON.stringify(locale)}`);
    }
    await assertFindingWorkspaceLayout(cdp, 1440, 2);
    await assertFindingWorkspaceLayout(cdp, 390, 1);
    await assertSeverityDescriptions(cdp);
    await cdp.send("Emulation.clearDeviceMetricsOverride");
    await assertFindingEnvironment(cdp);
    await assertDiagnosticCenter(cdp);

    await assertPage(
      cdp,
      "http://127.0.0.1:4173/#/findings?cluster=local&severity=critical",
      (text) =>
        text.includes("共 1 条诊断结果") &&
        text.includes("CrashLoopBackOff detected") &&
        !text.includes("Replica availability degraded"),
      "Severity filter did not reduce the Finding set to critical only",
    );

    await assertPage(
      cdp,
      "http://127.0.0.1:4173/#/findings/finding-critical",
      (text) =>
        text.includes("诊断编号") &&
        text.includes("Container restart backoff is increasing.") &&
        text.includes("sanitizer-xss-probe"),
      "Finding Detail did not render the selected Finding as safe text",
    );

    const xssEvaluation = await cdp.send("Runtime.evaluate", {
      expression: `({
        text: document.body?.innerText ?? "",
        probeNode: Boolean(document.querySelector("#sanitizer-xss-probe")),
        executed: globalThis.__kubeAiopsXss === 1
      })`,
      returnByValue: true,
    });
    const xssState = xssEvaluation.result?.value ?? {};
    if (
      xssState.probeNode ||
      xssState.executed ||
      !String(xssState.text ?? "").includes("<img id=\"sanitizer-xss-probe\"")
    ) {
      throw new Error(
        `Finding diagnostic text crossed the browser safety boundary: ${JSON.stringify(xssState)}`,
      );
    }
    await assertText(cdp, (text) =>
      text.includes("关联证据") && text.includes("2 条证据") &&
      text.includes("部分可用") && text.includes("不可用") && text.includes("已禁用"),
    "Finding Detail did not show collapsed evidence sources and source states");

    const evidenceEvaluation = await cdp.send("Runtime.evaluate", {
      expression: `(() => {
        const panel = document.querySelector(".correlation-panel");
        const tools = [...panel.querySelectorAll(".correlation-grid > details.evidence-source")];
        const visibleOnOpen = [
          "ContainerBackOff",
          "此来源暂不可用",
          "out_of_memory",
          "此来源未启用",
        ];
        const startClosed = tools.length === 4 && tools.every(tool => !tool.open) &&
          visibleOnOpen.every(message => !panel.innerText.includes(message));
        const expandedIndependently = tools.map((tool, index) => {
          const summary = tool.querySelector("summary");
          const count = summary.querySelector(".evidence-source-count").textContent;
          const expectedCount = [2, 0, 1, 0][index] + " 条证据";
          summary.click();
          const expanded = tool.open && tools.every(other => other === tool || !other.open) &&
            tool.innerText.includes(visibleOnOpen[index]);
          summary.click();
          return count === expectedCount && expanded && !tool.open;
        });
        const events = panel.querySelector('details[aria-label="Kubernetes 事件"]');
        events.querySelector("summary").click();
        const text = panel.innerText;
        events.querySelector("summary").click();
        return {
          startClosed, expandedIndependently,
          probe: Boolean(document.querySelector("#evidence-markup-probe")),
          text,
          events: Array.from(events.querySelectorAll(".evidence-signal-heading strong"), node => node.textContent),
        };
      })()`,
      returnByValue: true,
    });
    const evidence = evidenceEvaluation.result?.value ?? {};
    if (!evidence.startClosed || evidence.expandedIndependently?.join(",") !== "true,true,true,true" ||
      evidence.probe || !evidence.text?.includes('<em id="evidence-markup-probe">') ||
      evidence.events?.join(",") !== "ContainerBackOff,Started") {
      throw new Error(`Unexpected evidence folding, markup or ranking: ${JSON.stringify(evidence)}`);
    }

    await assertDetailLayout(cdp, 1440, 2);
    await assertDetailLayout(cdp, 920, 1);
    await assertDetailLayout(cdp, 390, 1);
    const mobileLayout = await cdp.send("Runtime.evaluate", {
      expression: `({
        columns: getComputedStyle(document.querySelector(".correlation-grid")).gridTemplateColumns.trim().split(/\\s+/).length,
        overflow: document.documentElement.scrollWidth > innerWidth
      })`,
      returnByValue: true,
    });
    if (mobileLayout.result?.value?.columns !== 1 || mobileLayout.result?.value?.overflow) {
      throw new Error(`Evidence panel overflows on mobile: ${JSON.stringify(mobileLayout.result?.value)}`);
    }
    await cdp.send("Emulation.clearDeviceMetricsOverride");

    await assertPage(
      cdp, "http://127.0.0.1:4173/#/findings/finding-warning",
      (text) => text.includes("加载关联证据失败") && text.includes("Available replicas are below desired replicas.") &&
        text.includes("重新加载证据") && !text.includes("private-provider-detail"),
      "Failed evidence request did not preserve the finding or show a safe retry",
    );
    await cdp.send("Runtime.evaluate", {
      expression: "document.querySelector('[data-action=retry-correlation]').click()",
    });
    await assertText(cdp, (text) =>
      text.includes("0 条证据") && text.includes("Available replicas are below desired replicas.") &&
      !text.includes("此时间窗口内暂无证据。") && !text.includes("重新加载证据"),
    "Evidence retry did not render the collapsed empty result");
    await cdp.send("Runtime.evaluate", {
      expression: `document.querySelector('details[aria-label="Kubernetes 事件"] summary').click()`,
    });
    await assertText(cdp, (text) => text.includes("此时间窗口内暂无证据。"),
      "Opening the empty available evidence source did not show the empty message");

    for (const [id, message] of [
      ["finding-auth", "查看证据需要身份认证"],
      ["finding-forbidden", "无权查看关联证据"],
    ]) {
      await assertPage(
        cdp, `http://127.0.0.1:4173/#/findings/${id}`,
        (text) => text.includes(message) && text.includes("Container restart backoff is increasing."),
        `Evidence access state missing for ${id}`,
      );
    }
    await fetch("http://127.0.0.1:4173/test/require-auth");
    await assertPage(
      cdp, "http://127.0.0.1:4173/#/diagnostics",
      (text) => text.includes("需要身份认证") && !text.includes("最近诊断结果"),
      "Unauthenticated diagnostic overview exposed results",
    );
    await assertPage(cdp, "about:blank", (text) => text === "", "Anonymous overview document was not cleared");
    await assertPage(
      cdp, "http://127.0.0.1:4173/#/findings",
      (text) => text.includes("需要身份认证") && text.includes("LDAP / Active Directory"),
      "Anonymous session did not show the login form",
    );
    for (const width of [1280, 390]) {
      await cdp.send("Emulation.setDeviceMetricsOverride", { width, height: 760, deviceScaleFactor: 1, mobile: false });
      await assertDOM(cdp, `(() => {
        const scene = document.querySelector(".login-scene canvas");
        const form = document.querySelector("[data-login]");
        const checkbox = form.elements.provider.getBoundingClientRect();
        const label = form.elements.provider.labels[0].getBoundingClientRect();
        return scene.getBoundingClientRect().width > 0 && scene.hasAttribute("height") &&
          getComputedStyle(scene.parentElement).pointerEvents === "none" &&
          Math.abs(checkbox.top + checkbox.bottom - label.top - label.bottom) < 2 &&
          form.querySelector('[type="submit"]').getBoundingClientRect().bottom <= innerHeight &&
          document.documentElement.scrollWidth <= document.documentElement.clientWidth;
      })()`, `Login backdrop or form layout failed at ${width}px`);
    }
    await cdp.send("Emulation.clearDeviceMetricsOverride");
    for (const [password, expected] of [
      ["incorrect", "登录失败，请核对登录方式和账号密码。"],
      ["fixture-password", "共 3 条诊断结果"],
    ]) {
      await cdp.send("Runtime.evaluate", {
        expression: `(() => {
          const form = document.querySelector("[data-login]");
          form.elements.username.value = "portal-test";
          form.elements.password.value = ${JSON.stringify(password)};
          form.requestSubmit();
        })()`,
      });
      await assertText(cdp, (text) => text.includes(expected), `Login did not show ${expected}`);
    }
    await cdp.send("Runtime.evaluate", {
      expression: 'document.querySelector("[data-action=logout]").click()',
    });
    await assertText(cdp, (text) =>
      text.includes("需要身份认证") && !text.includes("CrashLoopBackOff detected"),
    "Logout did not clear the findings and restore the login form");
    await cdp.send("Runtime.evaluate", {
      expression: `(() => {
        const form = document.querySelector("[data-login]");
        form.elements.provider.click();
        form.elements.username.value = "ldap-test";
        form.elements.password.value = "fixture-password";
        form.requestSubmit();
      })()`,
    });
    await assertText(cdp, (text) => text.includes("共 3 条诊断结果"), "Checked LDAP option did not log in");
    await cdp.send("Runtime.evaluate", {
      expression: 'document.querySelector("[data-action=logout]").click()',
    });
    await assertText(cdp, (text) => text.includes("需要身份认证"), "LDAP logout did not restore login");
    await assertPage(cdp, "about:blank", (text) => text === "", "Previous Portal document was not cleared");
    await assertPage(cdp, "http://127.0.0.1:4173/#/findings/finding-critical",
      (text) => text.includes("需要身份认证"), "Logged out user still had access to a finding");
    await fetch("http://127.0.0.1:4173/test/admin-session");
    await assertPage(cdp, "http://127.0.0.1:4173/#/admin/policy/new",
      (text) => text.includes("权限模块") && text.includes("角色成员"), "Role form did not render");
    const controls = await cdp.send("Runtime.evaluate", {
      expression: `(() => {
        const form = document.querySelector("[data-role-form]");
        return [...form.querySelectorAll('input[name="modules"]')].map((input) => ({
          value: input.value,
          checked: input.checked,
        }));
      })()`, returnByValue: true,
    });
    const modules = controls.result?.value ?? [];
    if (modules.length !== 12 || modules.some(({ checked }) => checked) ||
        !["environments:manage", "business-lines:manage"].every((capability) =>
          modules.some(({ value }) => value === capability))) {
      throw new Error("New role did not offer all twelve permission modules unchecked");
    }
    await cdp.send("Runtime.evaluate", {
      expression: `(() => {
        const form = document.querySelector("[data-role-form]");
        form.elements.name.value = "开发环境观察员";
        form.elements.namespaces.value = "default， payments";
        form.elements.localGroups.value = "operators";
        form.elements.ldapGroups.value = "CN=Readers,DC=example,DC=com";
        form.querySelector('[value="findings:list"]').click();
        form.querySelector('[value="correlations:read"]').click();
        form.requestSubmit();
      })()`,
    });
    await assertText(cdp, (text) => text.includes("开发环境观察员") && text.includes("local:operators"),
      "Role was not listed after saving the selected modules");
    await cdp.send("Runtime.evaluate", {
      expression: `document.querySelector('a[href="#/admin/policy/observer-role"]').click()`,
    });
    await assertText(cdp, (text) => text.includes("编辑角色"), "Role editor did not open");
    const checked = await cdp.send("Runtime.evaluate", {
      expression: '[...document.querySelectorAll("[name=modules]:checked")].map((input) => input.value)',
      returnByValue: true,
    });
    if (JSON.stringify(checked.result?.value) !== '["findings:list","correlations:read"]') {
      throw new Error("Role editor did not restore selected checkboxes");
    }
    await assertPage(cdp, "http://127.0.0.1:4173/#/findings/finding-critical",
      (text) => text.includes("K8sGPT 原始说明") && text.includes("AI 复核") && !text.includes("正在复核关联证据"), "AI review was missing or started automatically");
    await cdp.send("Runtime.evaluate", {expression: `(() => {
      document.querySelector(".ai-assistant-trigger").click();
      const form = document.querySelector(".ai-assistant-drawer [data-wiki-ask]");
      form.elements.question.value = "当前诊断下一步是什么？";
      form.requestSubmit();
    })()`});
    await assertDOM(cdp, 'document.querySelector(".ai-assistant-drawer [role=log]")?.textContent.includes("最近一次镜像拉取事件")',
      "Diagnostic assistant did not use the current Finding");
    await cdp.send("Runtime.evaluate", {expression: 'document.querySelector("[data-assistant-clear]").click()'});
    await assertDOM(cdp, '!document.querySelector("[data-assistant-context]") && !document.querySelector(".ai-assistant-drawer [role=log]")?.textContent',
      "Diagnostic assistant retained data after clearing context");
    await cdp.send("Runtime.evaluate", {expression: 'document.querySelector(".ai-assistant-drawer [data-close-wiki-ask]").click()'});
    const observationReady = await cdp.send("Runtime.evaluate", { returnByValue: true, expression: `(() => {
      const originalFetch = window.fetch.bind(window);
      window.fetch = (input, init) => {
        if (String(input).startsWith("/api/v1/admin/finding-analysis?") && (init?.method ?? "GET") === "GET") {
          window.fetch = originalFetch;
          return new Promise((resolve, reject) => {
            window.releaseInitialReviewHistory = () => originalFetch(input, init).then(resolve, reject);
          });
        }
        return originalFetch(input, init);
      };
      document.querySelector('[data-review-action="toggle"]').click();
      const section = document.querySelector("[data-review-input]");
      section.open = true;
      const field = document.querySelector("[data-review-observation]");
      field.focus();
      return !section.hidden && !field.readOnly && document.activeElement === field;
    })()` });
    if (!observationReady.result?.value) throw new Error("Observation entry was locked while review history was pending");
    await cdp.send("Input.insertText", { text: "仓库返回 403" });
    const drafted = await cdp.send("Runtime.evaluate", { returnByValue: true, expression: `(() => {
      const ready = document.querySelector("[data-review-observation]").value === "仓库返回 403" &&
        !document.querySelector('[data-review-action="assess"]').disabled;
      window.releaseInitialReviewHistory();
      return ready;
    })()` });
    if (!drafted.result?.value) throw new Error("Could not enter an observation while the first review was pending");
    await assertText(cdp, (text) => text.includes("正在复核关联证据") && text.includes("K8sGPT 原始说明") && text.includes("关联证据"),
      "Inline AI review did not show its pending state alongside the diagnosis");
    await assertText(cdp, (text) => text.includes("证据不足") && text.includes("当前镜像仓库响应") && text.includes("核对最近一次拉取事件") &&
      text.includes("补充内容已保留，可点击「重新复核」提交。"),
      "Review did not display the structured judgment, gaps and next action");
    const clickedEvidence = await cdp.send("Runtime.evaluate", { returnByValue: true, expression: `(() => {
      document.querySelector("[data-review-jump]").click();
      const target = document.activeElement;
      return target.closest("details.evidence-source")?.open && target.dataset.evidenceSignal;
    })()` });
    if (clickedEvidence.result?.value !== "event-critical") throw new Error("Review citation did not focus the original event");
    const fallbackEvidence = await cdp.send("Runtime.evaluate", { returnByValue: true, expression: `(() => {
      [...document.querySelectorAll("[data-review-jump]")].find(button => button.textContent.includes("Prometheus")).click();
      const events = document.querySelector('details[aria-label="Kubernetes 事件"]');
      const metrics = document.querySelector('details[aria-label="Prometheus"]');
      return events.open && metrics.open && document.activeElement === metrics.querySelector("summary");
    })()` });
    if (!fallbackEvidence.result?.value) throw new Error("Stale citation did not open and focus its own source without closing existing evidence");
    const awaitingObservation = await cdp.send("Runtime.evaluate", { returnByValue: true, expression: `(() => {
      const button = document.querySelector('[data-review-action="assess"]');
      button.click();
      return button.disabled && button.textContent === "复核中…" &&
        !document.querySelector("[data-review-observation]").readOnly;
    })()` });
    if (!awaitingObservation.result?.value) throw new Error("Submitting an observation did not immediately show pending status");
    await assertText(cdp, (text) => text.includes("模型响应超时。观察内容已保留，可以重新复核。") && text.includes("K8sGPT 原始说明"), "Model timeout was not recoverable inline");
    const observation = await cdp.send("Runtime.evaluate", { returnByValue: true, expression: 'document.querySelector("[data-review-observation]").value' });
    if (observation.result?.value !== "仓库返回 403") throw new Error("Review discarded the observation after failure");
    await cdp.send("Runtime.evaluate", { expression: `document.querySelector('[data-review-action="assess"]').click()` });
    await assertText(cdp, (text) => text.includes("模型服务返回超时（HTTP 504）") && text.includes("观察内容已保留"),
      "Upstream timeout was not distinguished from the app deadline");
    await cdp.send("Runtime.evaluate", { expression: `document.querySelector('[data-review-action="assess"]').click()` });
    await assertText(cdp, (text) => text.includes("网关等待超时") && text.includes("观察内容已保留") &&
      text.includes("已有历史事件支持排查镜像仓库") && text.includes("K8sGPT 原始说明"),
      "Outer gateway timeout did not preserve the existing review or provide its own message");
    const retainedObservation = await cdp.send("Runtime.evaluate", { returnByValue: true,
      expression: `document.querySelector("[data-review-observation]").value` });
    if (retainedObservation.result?.value !== "仓库返回 403") throw new Error("A repeated timeout discarded the observation");
    await cdp.send("Runtime.evaluate", { expression: `document.querySelector('[data-review-action="assess"]').click()` });
    await assertText(cdp, (text) => text.includes("镜像仓库返回 403，仍需核对"), "Retry did not use the operator observation");
    await cdp.send("Runtime.evaluate", { expression: `(() => {
      document.querySelector("[data-review-conclusion]").value = "人工确认：镜像仓库拒绝访问";
      document.querySelector('[data-review-action="save"]').click();
    })()` });
    await assertText(cdp, (text) => text.includes("已保存复核结论") && text.includes("人工确认：镜像仓库拒绝访问") && !text.includes("已发布到知识库"),
      "Saving a review did not keep publication independent");
    await cdp.send("Runtime.evaluate", { expression: `document.querySelector('[data-knowledge-action="draft"]').click()` });
    await assertText(cdp, (text) => text.includes("编辑知识草稿") && text.includes("已保存") === false && text.includes("提交审核"),
      "Finding did not create a separate editable knowledge candidate");
    await cdp.send("Runtime.evaluate", { expression: `(() => {
      const form = document.querySelector("[data-wiki-candidate]");
      form.elements.namedItem("section-recovery").value = "已确认 Pod Ready";
      form.querySelector('button[value="submit"]').click();
    })()` });
    await assertText(cdp, (text) => text.includes("知识治理") && text.includes("待审核") && text.includes("镜像拉取案例草稿"),
      "Draft did not enter the review queue");
    await assertPage(cdp, "http://127.0.0.1:4173/#/ai",
      text => text.includes("AI 服务配置") && text.includes("accurate-model"), "AI service configuration did not show the configured mapping");
    await cdp.send("Runtime.evaluate", {expression: 'document.querySelector("[data-ai-probe]").click()'});
    await assertText(cdp, text => text.includes("服务已就绪"), "AI service probe did not update the row");
    await cdp.send("Runtime.evaluate", {expression: `(() => {
      document.querySelector(".ai-assistant-trigger").click();
      const form = document.querySelector(".ai-assistant-drawer [data-wiki-ask]");
      form.elements.question.value = "镜像拉取怎么排查？";
      form.requestSubmit();
    })()`});
    await assertDOM(cdp, 'document.querySelector(".ai-assistant-drawer [role=log] a")?.href.includes("section=checks")',
      "Diagnostic assistant did not render the cited chapter");
    await cdp.send("Runtime.evaluate", {expression: 'document.querySelector(".ai-assistant-drawer [data-close-wiki-ask]").click()'});
    await assertPage(cdp, "http://127.0.0.1:4173/#/wiki?q=ImagePullBackOff",
      (text) => text.includes("运维知识库") && text.includes("镜像拉取排障") && text.includes("新建知识"),
      "Wiki search did not load the knowledge list");
    await cdp.send("Runtime.evaluate", { expression: 'document.querySelector("[data-wiki-preview-id]").click()' });
    await assertText(cdp, text => text.includes("先查什么") && text.includes("验证仓库权限") && text.includes("恢复判断"),
      "Wiki quick preview did not load the visible version");
    await cdp.send("Runtime.evaluate", { expression: 'document.querySelector("[data-wiki-preview-read]").click()' });
    await assertText(cdp, text => text.includes("返回检索结果") && text.includes("验证仓库权限"), "Wiki full document did not load from preview");
    await cdp.send("Runtime.evaluate", { expression: `document.querySelector('a[href="#/wiki?q=ImagePullBackOff"]').click()` });
    await assertDOM(cdp, `document.querySelector("#wiki-search-term")?.value === "ImagePullBackOff"`, "Wiki return lost the search term");
    await cdp.send("Runtime.evaluate", { expression: `(() => {
      const sort = document.querySelector("[data-wiki-sort]");
      sort.value = "recent";
      sort.dispatchEvent(new Event("change", { bubbles: true }));
    })()` });
    await assertDOM(cdp, `document.querySelector('[data-wiki-sort] option[value="recent"]')?.defaultSelected && location.hash.includes("sort=recent")`,
      "Wiki recent order failed to persist");
    await cdp.send("Emulation.setDeviceMetricsOverride", { width: 390, height: 760, deviceScaleFactor: 1, mobile: false });
    await assertDOM(cdp, `document.documentElement.scrollWidth <= document.documentElement.clientWidth`, "Wiki results overflowed at 390 px");
    await cdp.send("Emulation.clearDeviceMetricsOverride");
    const wikiDialog = await cdp.send("Runtime.evaluate", {
      expression: 'document.querySelector("[data-wiki-dialog]").open',
      returnByValue: true,
    });
    if (wikiDialog.result?.value !== false) throw new Error("Wiki should open on the document list without a dialog");
    await cdp.send("Runtime.evaluate", {expression: 'document.querySelector("[data-open-wiki-ask]").click()'});
    await assertText(cdp, (text) => text.includes("问题") && text.includes("发送"), "Wiki question dialog did not open");
    await cdp.send("Runtime.evaluate", {
      expression: `(() => {
        const form = document.querySelector("[data-wiki-ask]");
        form.elements.question.value = "镜像拉取怎么排查？";
        form.requestSubmit();
      })()`,
    });
    await assertText(cdp, (text) => text.includes("请验证仓库权限。"), "Wiki question answer did not render");
    await cdp.send("Runtime.evaluate", {
      expression: `document.querySelector('[data-wiki-dialog-log] a[href*="section=checks"]').click()`,
    });
    await assertText(cdp, (text) => text.includes("验证仓库权限") && text.includes("反馈将关联 v1 的当前章节"), "Wiki citation did not open the exact version and chapter");
    const anchor = await cdp.send("Runtime.evaluate", {expression: 'document.activeElement.id === "wiki-section-checks"', returnByValue: true});
    if (!anchor.result?.value) throw new Error("Wiki chapter was not focused");
    await cdp.send("Runtime.evaluate", {expression: `(() => {
      const form = document.querySelector("[data-wiki-feedback]");
      form.elements.kind.value = "outdated";
      form.requestSubmit();
    })()`});
    await assertText(cdp, (text) => text.includes("反馈已记录"), "Feedback was not attached to the cited version and section");
    await assertPage(cdp, "http://127.0.0.1:4173/#/wiki?environment=测试",
      (text) => text.includes("AI 知识问答"), "Wiki scope did not load");
    await cdp.send("Runtime.evaluate", {expression: `(() => {
      document.querySelector("[data-open-wiki-ask]").click();
      const form = document.querySelector("[data-wiki-ask]");
      form.elements.question.value = "旧范围提问";
      form.requestSubmit();
      location.hash = "#/wiki?environment=生产";
    })()`});
    await assertText(cdp, (text) => text.includes("知识文档") && !text.includes("旧范围提问"), "Scope switch did not dismiss the old request");
    await sleep(400);
    const stale = await cdp.send("Runtime.evaluate", {expression: 'Boolean(document.querySelector("[data-wiki-dialog-log] section"))', returnByValue: true});
    if (stale.result?.value) throw new Error("An old answer reached the new scope");
    await cdp.send("Runtime.evaluate", {expression: `(() => {
      const source = document.querySelector("[data-wiki-sync]");
      source.closest("details").open = true;
      source.elements.id.value = "ops";
      document.querySelector('[data-wiki-run-action="reload"]').click();
    })()`});
    await assertText(cdp, (text) => text.includes("部分失败") && text.includes("查看明细"), "Remote source did not show persistent runs");
    await cdp.send("Runtime.evaluate", {expression: `document.querySelector('[data-wiki-run-action="detail"]').click()`});
    await assertText(cdp, (text) => text.includes("page-2") && text.includes("重试此页"), "Failed sync page could not be inspected");
    await cdp.send("Runtime.evaluate", {expression: `document.querySelector('[data-wiki-run-action="retry"][value="page-2"]').click()`});
    await assertText(cdp, (text) => text.includes("本次重试已记录"), "Failed page could not be retried");
    await assertPage(cdp, "http://127.0.0.1:4173/#/resources",
      (text) => text.includes("资源管理") && text.includes("Kubernetes 集群管理") && text.includes("环境管理"), "Resource navigation did not default to cluster onboarding");
    await assertDOM(cdp, `(() => {
      const shell = getComputedStyle(document.querySelector(".portal-console"));
      const nav = getComputedStyle(document.querySelector('.workspace-nav [aria-current]'));
      const card = getComputedStyle(document.querySelector(".resource-list-panel"));
      const input = getComputedStyle(document.querySelector('.resource-filters input[type="search"]'));
      return parseFloat(shell.fontSize) === 16 && parseFloat(nav.borderTopLeftRadius) >= 10 &&
        parseFloat(card.borderTopLeftRadius) === 20 && parseFloat(input.fontSize) === 15 &&
        parseFloat(input.borderTopLeftRadius) === 12;
    })()`, "Resource workspace does not use the shared type and corner scales");
    const clusterLabels = await cdp.send("Runtime.evaluate", { returnByValue: true, expression: `[
      document.querySelector('.portal-sidebar a[aria-current="page"]')?.textContent,
      document.querySelector("main h1")?.textContent,
      document.querySelector("#cluster-list-title")?.textContent,
    ]` });
    if (clusterLabels.result?.value?.length !== 3 ||
        !clusterLabels.result.value.every(label => label === "Kubernetes 集群管理")) {
      throw new Error("Cluster navigation, page title and list heading must all say Kubernetes 集群管理");
    }
    await assertText(cdp, (text) => text.includes("有 NotReady 节点") && text.includes("接入集群"), "Cluster table did not separate node status from collection");
    await cdp.send("Emulation.setDeviceMetricsOverride", { width: 390, height: 720, deviceScaleFactor: 1, mobile: true });
    await assertDOM(cdp, `document.documentElement.scrollWidth <= document.documentElement.clientWidth`, "Cluster rows overflowed at 390 px");
    await cdp.send("Emulation.clearDeviceMetricsOverride");
    await cdp.send("Runtime.evaluate", { expression: `(() => {
      document.querySelector('[data-cluster-editor="choose"]').click();
      document.querySelector('[data-cluster-editor="pull"]').click();
    })()` });
    await assertText(cdp, (text) => text.includes("新增外部直连集群") && text.includes("启用实例"), "Direct cluster setup did not open");
    for (const width of [1280, 390]) {
      await cdp.send("Emulation.setDeviceMetricsOverride", { width, height: 720, deviceScaleFactor: 1, mobile: width === 390 });
      for (const { step, name, checked } of [
        { step: "basic", name: "enabled", checked: true },
        { step: "observe", name: "aiEnabled", checked: false },
      ]) {
        await cdp.send("Runtime.evaluate", { expression: `document.querySelector('[data-editor-step="${step}"]').click()` });
        await assertDOM(cdp, `(() => {
          const input = document.querySelector('dialog[open] input[name="${name}"]');
          const labelText = input.labels[0].querySelector("span");
          const textRange = document.createRange();
          textRange.selectNodeContents(labelText);
          const lines = textRange.getClientRects();
          const checkbox = input.getBoundingClientRect();
          const text = lines[0];
          if (lines.length !== 1 || checkbox.width === 0 || checkbox.right >= text.left ||
              Math.abs((checkbox.top + checkbox.bottom - text.top - text.bottom) / 2) > 2 || input.checked !== ${checked}) return false;
          labelText.click();
          const toggled = input.checked !== ${checked};
          labelText.click();
          return toggled && input.checked === ${checked};
        })()`, `Cluster checkbox ${name} was misaligned or its label could not toggle it at ${width} px`);
      }
    }
    await cdp.send("Runtime.evaluate", { expression: `document.querySelector('dialog[open][data-cluster-dialog]').close()` });
    await cdp.send("Emulation.clearDeviceMetricsOverride");
    const environmentScopes = await cdp.send("Runtime.evaluate", { returnByValue: true, expression: `(() => {
      const field = document.querySelector('[data-cluster-filter="environment"]');
      const change = (value) => { field.value = value; field.dispatchEvent(new Event("change", { bubbles: true })); };
      const rows = [...document.querySelectorAll("[data-cluster-row]")];
      const environments = [...field.options].filter(item => item.value);
      const works = environments.length >= 2 && environments.every(option => {
        change(option.value);
        const matches = rows.filter(row => row.dataset.environment === option.value);
        return rows.every(row => row.hidden === !matches.includes(row)) &&
          document.querySelector("[data-cluster-count]").textContent === "显示 " + matches.length + " / " + matches.length + " 个集群";
      });
      change("研发");
      document.querySelector('[data-resource-metric="issue"]').click();
      const issueMatches = rows.filter(row => !row.hidden).every(row => row.dataset.category === "issue");
      return works && issueMatches;
    })()` });
    if (!environmentScopes.result?.value) throw new Error("Environment and issue filters did not scope cluster results");
    await cdp.send("Runtime.evaluate", { expression: `(() => {
      document.querySelector('[data-resource-tab="namespaces"]').click();
      const cluster = document.querySelector('[data-cluster-filter="cluster"]');
      cluster.value = "dev-k8s";
      cluster.dispatchEvent(new Event("change", { bubbles: true }));
    })()` });
    await assertText(cdp, (text) => text.includes("dev-app") && text.includes("显示 1 个命名空间") &&
      !text.includes("本地可恢复集群"), "Namespace view did not follow the selected cluster");
    await cdp.send("Runtime.evaluate", { expression: `(() => {
      document.querySelector('[data-resource-tab="clusters"]').click();
      document.querySelector('[data-cluster-clear]').click();
      document.querySelector('[data-cluster-details="dev-k8s"]').click();
    })()` });
    await assertText(cdp, (text) => text.includes("test-worker · NotReady") && text.includes("dev-app"), "Collection details omitted nodes or namespaces");
    await cdp.send("Runtime.evaluate", { expression: 'document.querySelector("dialog[open] [data-resource-close]").click()' });
    await cdp.send("Runtime.evaluate", { expression: `document.querySelector('[data-cluster-details="local"]').click()` });
    await cdp.send("Runtime.evaluate", { expression: `document.querySelector('dialog[open] [data-collection-check="local"]').requestSubmit()` });
    await assertText(cdp, (text) => text.includes("本地可恢复集群") && text.includes("待接入"), "Local cluster could not refresh collection without closing details");
    await cdp.send("Runtime.evaluate", { expression: 'document.querySelector("dialog[open] [data-resource-close]").click()' });
    await cdp.send("Runtime.evaluate", { expression: `document.querySelector('[data-cluster-editor="ownership-dev-k8s"]').click()` });
    await assertText(cdp, (text) => text.includes("请选择业务线后勾选命名空间。") && text.includes("保存归属"),
      "Namespace ownership did not start with business-line selection");
    await cdp.send("Emulation.setDeviceMetricsOverride", { width: 390, height: 720, deviceScaleFactor: 1, mobile: true });
    const ownershipSetup = await cdp.send("Runtime.evaluate", { returnByValue: true, expression: `(() => {
      const form = document.querySelector("dialog[open] [data-namespace-ownership]");
      const select = form.elements.businessId;
      const button = form.querySelector('button[type="submit"]');
      const choose = (id) => {
        select.value = id;
        select.dispatchEvent(new Event("change", { bubbles: true }));
      };
      const startsHidden = button.disabled && form.querySelector("[data-ownership-choices]").hidden;
      choose("yunfan");
      const list = form.querySelector('[aria-label="命名空间列表"]');
      const payments = form.querySelector('input[value="payments"]');
      const monitoring = form.querySelector('input[value="monitoring"]');
      const prefilled = monitoring.checked && !payments.checked;
      list.focus();
      list.scrollTop = list.scrollHeight;
      const scrollable = document.activeElement === list && list.scrollTop > 0 &&
        !list.contains(button) && button.getBoundingClientRect().bottom <= innerHeight;
      payments.checked = true;
      monitoring.checked = false;
      choose("platform");
      const otherLinePrefilled = payments.checked && !monitoring.checked;
      payments.checked = false;
      choose("yunfan");
      const draftPreserved = payments.checked && !monitoring.checked;
      form.requestSubmit();
      return startsHidden && prefilled && scrollable && otherLinePrefilled && draftPreserved &&
        !form.querySelector('input[value="retired-ns"]');
    })()` });
    if (!ownershipSetup.result?.value) throw new Error("Ownership choices, scroll region, or draft preservation failed");
    await assertText(cdp, (text) => text.includes("已保存当前业务线。其他业务线仍有未保存的勾选。"),
      "Business-scoped save should keep other drafts open");
    const actionsAreVisible = await cdp.send("Runtime.evaluate", {
      returnByValue: true,
      expression: `(() => {
        const dialog = document.querySelector("dialog[open]");
        const button = dialog.querySelector('button[type="submit"]');
        return button.getBoundingClientRect().bottom <= dialog.getBoundingClientRect().bottom;
      })()`,
    });
    if (!actionsAreVisible.result?.value) throw new Error("Ownership actions were clipped after the save status appeared");
    await cdp.send("Runtime.evaluate", { expression: `(() => {
      const form = document.querySelector("dialog[open] [data-namespace-ownership]");
      form.elements.businessId.value = "platform";
      form.elements.businessId.dispatchEvent(new Event("change", { bubbles: true }));
      form.requestSubmit();
    })()` });
    await assertText(cdp, (text) => !text.includes("保存归属") && text.includes("Kubernetes 集群管理"), "Namespace ownership did not save and close");
    await cdp.send("Emulation.clearDeviceMetricsOverride");
    await cdp.send("Runtime.evaluate", { expression: `(() => {
      const search = document.querySelector('[data-cluster-filter="query"]');
      search.value = "本地可恢复集群";
      search.dispatchEvent(new Event("input", { bubbles: true }));
    })()` });
    await assertText(cdp, (text) => text.includes("显示 1 / 1 个集群") && !text.includes("dev-k8s"), "Cluster search did not filter the directory");
    await cdp.send("Runtime.evaluate", { expression: `document.querySelector('[data-cluster-action="delete"][data-id="local"]').click()` });
    await assertText(cdp, (text) => text.includes("删除集群连接") && text.includes("清理 kube-aiops"), "Cluster deletion confirmation did not render");
    await cdp.send("Runtime.evaluate", { expression: `document.querySelector('dialog[open] [data-cluster-close]').click()` });
    await assertText(cdp, (text) => text.includes("本地可恢复集群") && text.includes("待接入"), "Canceling deletion changed the local connection");
    await cdp.send("Runtime.evaluate", { expression: `(() => {
      document.querySelector('[data-cluster-action="delete"][data-id="local"]').click();
      document.querySelector('dialog[open] button[type="submit"]').click();
    })()` });
    await assertDOM(cdp, `!!document.querySelector('[data-cluster-row][data-category="removed"] [data-cluster-action="reconnect"]')`, "Deleted local connection did not expose reconnect");
    await cdp.send("Runtime.evaluate", { expression: `(() => {
      document.querySelector('[data-cluster-action="reconnect"][data-id="local"]').click();
      document.querySelector('dialog[open] button[type="submit"]').click();
    })()` });
    await assertDOM(cdp, `!!document.querySelector('[data-cluster-row][data-cluster="local"]:not([data-category="removed"])')`, "Reconnect did not restore the local row");
    await cdp.send("Runtime.evaluate", { expression: `document.querySelector("[data-cluster-clear]").click(); document.querySelector('[data-cluster-editor="edit-dev-k8s"]').click();` });
    await assertText(cdp, (text) => text.includes("环境已锁定") && text.includes("取消"), "Cluster editor did not display the locked environment");
    const lockedEnvironment = await cdp.send("Runtime.evaluate", {
      expression: 'document.querySelector("dialog[open] [data-onboarding-mode] input[name=environment]").readOnly', returnByValue: true,
    });
    if (!lockedEnvironment.result?.value) throw new Error("A collected cluster must have a read-only environment");
    const catalogOrigin = "http://127.0.0.1:4173";
    await assertPage(cdp, `${catalogOrigin}/#/resources/environments`,
      (text) => text.includes("环境管理") && text.includes("新增环境"), "Environment catalog did not render");
    await cdp.send("Emulation.setDeviceMetricsOverride", { width: 390, height: 720, deviceScaleFactor: 1, mobile: true });
    await assertDOM(cdp, `document.documentElement.scrollWidth <= document.documentElement.clientWidth`, "Catalog overflowed at 390 px");
    await cdp.send("Runtime.evaluate", { expression: `document.querySelector('[data-catalog-action="add"]').click()` });
    const idPattern = await cdp.send("Runtime.evaluate", { returnByValue: true, expression: `(() => {
      const form = document.querySelector("[data-resource-catalog]");
      const id = form.elements.id;
      id.value = "new-dev";
      const valid = id.checkValidity();
      id.value = "INVALID";
      const invalid = !id.checkValidity();
      id.value = "";
      return valid && invalid && form.querySelector("footer").getBoundingClientRect().bottom <= innerHeight;
    })()` });
    if (!idPattern.result?.value) throw new Error("Catalog ID validation or mobile footer failed");
    await cdp.send("Runtime.evaluate", { expression: `document.querySelector('dialog[open] [data-resource-close]').click()` });
    await cdp.send("Emulation.clearDeviceMetricsOverride");
    await cdp.send("Runtime.evaluate", { expression: `
      document.querySelector('[data-catalog-action=detail]').click();
      document.querySelector('[data-detail-edit]').click();` });
    await assertDOM(cdp, `document.querySelector("[data-catalog-dialog] input[name=id]").readOnly &&
      document.querySelector("[data-catalog-id-fields]").hidden &&
      document.querySelector("[data-catalog-dialog] button[type=submit]").disabled`, "Existing ID must begin locked");
    await cdp.send("Runtime.evaluate", { expression: `(() => {
      const form = document.querySelector("[data-resource-catalog]");
      form.elements.name.value = "研发环境";
      form.elements.name.dispatchEvent(new Event("input", { bubbles: true }));
      form.querySelector("[data-resource-close]").click();
    })()` });
    await assertDOM(cdp, `document.querySelector("[data-catalog-discard]").open`, "Closing a modified draft did not ask for confirmation");
    await cdp.send("Runtime.evaluate", { expression: `
      document.querySelector("[data-keep-draft]").click();
      document.querySelector("[data-resource-catalog]").requestSubmit();` });
    await assertDOM(cdp, `!document.querySelector("[data-catalog-dialog]").open &&
      document.querySelector("[data-catalog-detail]").open &&
      document.querySelector('[data-entry-id=development] [data-catalog-action=detail]').textContent === "研发环境"`,
      "Name-only edit must preserve the current ID and update the open details");
    await cdp.send("Runtime.evaluate", { expression: `
      document.querySelector("[data-catalog-detail] [data-resource-close]").click();
      document.querySelector('[data-catalog-action=edit]').click();
      document.querySelector('[data-unlock-id]').click();
      document.querySelector("[data-resource-catalog]").elements.id.value = "retired-id";
      document.querySelector("[data-resource-catalog]").requestSubmit();` });
    await assertDOM(cdp, `document.querySelector("[name=id]").getAttribute("aria-invalid") === "true" &&
      document.querySelector('[id=catalog-id-error]').textContent.includes("历史 ID")`, "Server-side alias conflict did not identify the ID");
    await cdp.send("Runtime.evaluate", { expression: `
      document.querySelector("[name=id]").value = "new-dev";
      document.querySelector("[data-resource-catalog]").requestSubmit();` });
    await assertDOM(cdp, `!!document.querySelector('[data-entry-id=new-dev]') && !document.querySelector("[data-catalog-dialog]").open`, "Unlocked ID change was not saved");
    await cdp.send("Runtime.evaluate", { expression: `
      document.querySelector('[data-entry-id=new-dev] summary').click();
      document.querySelector('[data-catalog-action=delete]').click();
      document.querySelector("[data-resource-catalog]").requestSubmit();` });
    await assertDOM(cdp, `document.querySelector(".catalog-references li")?.textContent.includes("dev-k8s") &&
      !!document.querySelector("[data-copy-references]") && !!document.querySelector("[data-entry-id=new-dev]")`, "Deletion conflict must show references and keep the row");
    await cdp.send("Runtime.evaluate", { expression: `(() => {
      Object.defineProperty(navigator, "clipboard", { configurable: true, value: {
        writeText: async () => { throw new Error("fixture clipboard unavailable"); },
      } });
      document.querySelector("[data-copy-references]").click();
    })()` });
    await assertDOM(cdp, `document.querySelector("textarea[aria-label=待复制内容]")?.value === "dev-k8s"`,
      "Clipboard denial must offer the reference text");
    await cdp.send("Runtime.evaluate", { expression: `
      delete navigator.clipboard;
      document.querySelector('dialog[open] [data-resource-close]').click();` });
    await fetch(`${catalogOrigin}/test/catalog-mode?mode=save-error`);
    await cdp.send("Runtime.evaluate", { expression: `(() => {
      document.querySelector('[data-catalog-action="add"]').click();
      const form = document.querySelector("[data-resource-catalog]");
      form.elements.id.value = "sandbox";
      form.elements.name.value = "沙盒";
      form.requestSubmit();
    })()` });
    await assertDOM(cdp, `document.querySelector("[data-catalog-form-status]")?.textContent.includes("操作失败") &&
      document.querySelector("[data-resource-catalog]").elements.name.value === "沙盒"`, "Failed creation did not preserve the draft");
    await fetch(`${catalogOrigin}/test/catalog-mode?mode=normal`);
    await cdp.send("Runtime.evaluate", { expression: 'document.querySelector("[data-resource-catalog]").requestSubmit()' });
    await assertDOM(cdp, `!!document.querySelector("[data-entry-id=sandbox]")`, "Catalog creation did not preserve the entered ID");
    await cdp.send("Runtime.evaluate", { expression: `
      document.querySelector('[data-entry-id=sandbox] summary').click();
      document.querySelector('[data-entry-id=sandbox] [data-catalog-action=delete]').click();
      document.querySelector("[data-resource-catalog]").requestSubmit();` });
    await assertDOM(cdp, `!document.querySelector("[data-entry-id=sandbox]")`, "Unreferenced catalog deletion failed");
    await cdp.send("Runtime.evaluate", { expression: `
      const search = document.querySelector('[data-catalog-search]');
      search.value = "no-match";
      search.dispatchEvent(new Event("input", { bubbles: true }));` });
    await assertText(cdp, text => text.includes("没有匹配的条目"), "Catalog search did not show an empty filter result");
    await cdp.send("Runtime.evaluate", { expression: `document.querySelector('nav.catalog-tabs a[href="#/resources/business-lines"]').click()` });
    await assertText(cdp, (text) => text.includes("业务线 ID") && text.includes("云帆") && text.includes("新增业务线"), "Business catalog did not match the shared layout");
    await cdp.send("Runtime.evaluate", { expression: `(() => {
      document.querySelector('[data-entry-id=yunfan] [data-catalog-action=edit]').click();
      document.querySelector('[data-unlock-id]').click();
      const form = document.querySelector("[data-resource-catalog]");
      form.elements.id.value = "yunfan-app";
      form.requestSubmit();
    })()` });
    await assertDOM(cdp, `!!document.querySelector('[data-entry-id=yunfan-app]')`, "Business ID was not saved");
    await cdp.send("Runtime.evaluate", { expression: `
      document.querySelector('[data-entry-id=yunfan-app] summary').click();
      document.querySelector('[data-entry-id=yunfan-app] [data-catalog-action=delete]').click();
      document.querySelector("[data-resource-catalog]").requestSubmit();` });
    await assertDOM(cdp, `[...document.querySelectorAll(".catalog-references small")].map(el=>el.textContent).join("/") === "命名空间归属/角色范围"`,
      "Business deletion must distinguish namespace and role references");
    await cdp.send("Runtime.evaluate", { expression: `
      document.querySelector('dialog[open] [data-resource-close]').click();
      document.querySelector('nav.catalog-tabs a[href="#/resources/environments"]').click();` });
    await assertDOM(cdp, `document.querySelector('[data-catalog-kind=environments] [data-catalog-search]')?.value === "no-match"`,
      "Catalog tab switch lost environment filters");
    await cdp.send("Runtime.evaluate", { expression: `document.querySelector('.resource-filters [data-catalog-action=clear]').click()` });
    await fetch(`${catalogOrigin}/test/catalog-mode?mode=load-error`);
    await assertPage(cdp, `${catalogOrigin}/#/resources/business-lines?case=unavailable`,
      text => text.includes("目录加载失败") && text.includes("重新加载"), "Catalog list failed without a retry state");
    await fetch(`${catalogOrigin}/test/catalog-mode?mode=normal`);
    await cdp.send("Runtime.evaluate", { expression: `document.querySelector("[data-catalog-action=retry]").click()` });
    await assertDOM(cdp, `!!document.querySelector("[data-entry-id=yunfan-app]")`, "Catalog list did not recover in place");
    await fetch(`${catalogOrigin}/test/catalog-mode?mode=readonly`);
    await assertPage(cdp, `${catalogOrigin}/#/resources/business-lines?case=readonly`,
      text => text.includes("只读权限") && text.includes("云帆"), "Read-only catalog did not load");
    await assertDOM(cdp, `!document.querySelector("[data-catalog-action=add], [data-catalog-action=edit], [data-catalog-action=delete]") &&
      !!document.querySelector("[data-catalog-action=copy]")`, "Read-only catalog exposed a write action");
    await fetch(`${catalogOrigin}/test/catalog-mode?mode=normal`);
    await assertPage(cdp, `${catalogOrigin}/#/resources/business-lines`, text => text.includes("新增业务线"), "Catalog admin did not recover");
    const nav = await cdp.send("Runtime.evaluate", { expression: `(() => {
      const links = [...document.querySelectorAll(".workspace-nav > a")];
      const current = links.find((link) => link.getAttribute("aria-current") === "location");
      return {
        labels: links.map((link) => link.textContent).join("/"),
        indents: [...new Set(links.map((link) => getComputedStyle(link).paddingInlineStart))].length,
        current: current?.textContent,
        selectedBackground: current && getComputedStyle(current).backgroundColor,
        inactiveBackground: getComputedStyle(links[0]).backgroundColor,
      };
    })()`, returnByValue: true });
    const navigation = nav.result?.value;
    if (navigation?.labels !== "诊断控制台/资源管理/运维知识库/AI 服务/系统管理" || navigation.indents !== 1) {
      throw new Error("The five modules must be peers in the workspace navigation");
    }
    if (navigation.current !== "资源管理" || navigation.selectedBackground === navigation.inactiveBackground) {
      throw new Error("Workspace navigation must distinguish the active resource module");
    }
    await assertPage(cdp, "http://127.0.0.1:4173/#/admin/users",
      (text) => text.includes("账号类型") && text.includes("账号状态") && text.includes("最近登录") && text.includes("reader"), "User filters did not render");
    await cdp.send("Runtime.evaluate", { expression: 'document.querySelector("[data-platform-select-all]").click()' });
    await assertText(cdp, (text) => text.includes("已选 1 项"), "Select all did not update the selection count");
    await cdp.send("Runtime.evaluate", { expression: 'document.querySelector("[data-platform-batch]").click()' });
    await assertText(cdp, (text) => text.includes("已选 1 个账号") && text.includes("选择用户组"), "Batch assignment dialog did not open");
    const integrated = await cdp.send("Runtime.evaluate", { returnByValue: true, expression: `(() => {
      const dialog = document.querySelector("dialog[open]");
      const select = dialog.querySelector("[data-assignment-kind]");
      select.value = "roles";
      select.dispatchEvent(new Event("change", { bubbles: true }));
      return dialog.querySelector('[data-assignment-options="groups"]').hidden &&
        !dialog.querySelector('[data-assignment-options="roles"]').hidden &&
        dialog.innerText.includes("管理员角色发生变更时，目标账号需重新登录");
    })()` });
    if (!integrated.result?.value) throw new Error("Authorization target did not switch to role checkboxes");
    const assignmentRoleIds = ["diagnosis-reader", "namespace-auditor", "system-admin"];
    await cdp.send("Runtime.evaluate", { expression: `(() => {
      const form = document.querySelector("dialog[open] form");
      for (const id of ${JSON.stringify(assignmentRoleIds)}) form.querySelector('input[value="' + id + '"]').checked = true;
      form.requestSubmit(form.querySelector('button[value="assignment"]'));
    })()` });
    await assertText(cdp, (text) => text.includes("授权操作结果") && text.includes("已更新关联关系"), "Batch results did not display individual outcomes");
    await cdp.send("Runtime.evaluate", { expression: `document.querySelector('dialog[open] a[href*="?record="]').click()` });
    await assertText(cdp, (text) => text.includes("授予角色") && text.includes("操作者") && text.includes("local:admin"),
      "Authorization result did not open the exact retained audit record");
    await assertPage(cdp, "http://127.0.0.1:4173/#/admin/users",
      (text) => text.includes("reader") && text.includes("用户管理"), "The account list did not reload after viewing the audit");
    await assertText(cdp, (text) => text.includes("已选 0 项") && text.includes("LDAP 员工") && text.includes("全局管理与诊断查看"),
      "Batch grant did not show full administrator permissions while retaining the LDAP account type");
    await cdp.send("Runtime.evaluate", {
      awaitPromise: true,
      expression: `fetch("/test/admin-session?super=false").then(() => {
        location.hash = document.querySelector("[data-platform-row] a").hash;
      })`,
    });
    await assertText(cdp, (text) => text.includes("全局管理与诊断查看") && !text.includes("创建系统管理员"),
      "Ordinary administrator session did not load the account detail");
    const protectedLDAP = await cdp.send("Runtime.evaluate", { returnByValue: true, expression: `(() => {
      const roles = document.querySelector('template[data-system-editor="batch"]').content;
      return document.querySelector('[data-platform-row] input[name="ids"]').disabled &&
        document.querySelector("[data-platform-delete]").disabled &&
        !document.querySelector('[data-system-open="assign"], [data-system-open="account-action"], [data-system-open="reset"]') &&
        !document.querySelector('a[href="#/admin/password"]') &&
        !roles.querySelector('input[value="system-admin"]') &&
        roles.querySelector('input[value="diagnosis-reader"]') && roles.querySelector('input[value="namespace-auditor"]') !== null;
    })()` });
    if (!protectedLDAP.result?.value) throw new Error("Ordinary administrator saw restricted account or password controls");
    const guardedPassword = await cdp.send("Runtime.evaluate", { awaitPromise: true, returnByValue: true, expression: `new Promise((resolve) => {
      const timer = setTimeout(() => resolve(false), 2000);
      function onRedirect() {
        if (location.hash !== "#/admin/users") return;
        window.removeEventListener("hashchange", onRedirect);
        clearTimeout(timer);
        resolve(!document.querySelector('[data-identity-resource="password"]'));
      }
      window.addEventListener("hashchange", onRedirect);
      location.hash = "#/admin/password";
    })` });
    if (!guardedPassword.result?.value) throw new Error("LDAP administrator could open the local password route");
    await cdp.send("Runtime.evaluate", { awaitPromise: true, expression: 'fetch("/test/admin-session")' });
    await assertPage(cdp, "http://127.0.0.1:4173/?local-admin-session#/admin/password",
      (text) => text.includes("为当前本地账号设置新密码"), "Local administrator lost the password route");
    await assertPage(cdp, "http://127.0.0.1:4173/#/admin/users",
      (text) => text.includes("全局管理与诊断查看") && text.includes("创建系统管理员"),
      "Super administrator session did not restore account maintenance");
    await cdp.send("Runtime.evaluate", { expression: `(() => {
      document.querySelector('[data-system-open="assign"]').click();
      const form = document.querySelector("dialog[open] form");
      const kind = form.querySelector("[data-assignment-kind]");
      kind.value = "roles";
      kind.dispatchEvent(new Event("change", { bubbles: true }));
      const mode = form.querySelector("[data-assignment-mode]");
      mode.value = "remove";
      mode.dispatchEvent(new Event("change", { bubbles: true }));
      const search = form.querySelector("[data-assignment-search]");
      search.value = "命名空间";
      search.dispatchEvent(new Event("input", { bubbles: true }));
      if (form.querySelectorAll('[data-assignment-options="roles"] [data-assignment-choice]:not([hidden])').length !== 1) {
        throw new Error("Assignment search did not filter the current role options");
      }
      search.value = "";
      search.dispatchEvent(new Event("input", { bubbles: true }));
      for (const id of ${JSON.stringify(assignmentRoleIds)}) form.querySelector('input[value="' + id + '"]').checked = true;
      form.requestSubmit(form.querySelector('button[value="assignment"]'));
    })()` });
    await assertText(cdp, (text) => text.includes("授权操作结果") && text.includes("已更新关联关系"), "Single authorization did not show its outcome");
    await cdp.send("Runtime.evaluate", { expression: 'document.querySelector("dialog[open] footer [data-system-close]").click()' });
    await assertText(cdp, (text) => text.includes("用户组 运维") && !text.includes("全局管理与诊断查看"),
      "Revoking multiple roles did not restore scoped LDAP permissions");
    await cdp.send("Runtime.evaluate", { expression: 'document.querySelector("[data-system-open=guest]").click()' });
    await assertText(cdp, (text) => text.includes("临时密码") && text.includes("取消"), "Create visitor dialog did not open");
    await cdp.send("Runtime.evaluate", { expression: 'document.querySelector("dialog[open] footer [data-system-close]").click()' });
    await cdp.send("Runtime.evaluate", { expression: `(() => {
      const kind = document.querySelector('[data-platform-filter="kind"]');
      kind.value = "guest";
      kind.dispatchEvent(new Event("change", { bubbles: true }));
    })()` });
    await assertText(cdp, (text) => text.includes("暂无符合条件的账号") && text.includes("已选 0 项") && text.includes("请选择一个账号"),
      "Filtering should clear hidden selections and unrelated details");
    const inlineSearch = await cdp.send("Runtime.evaluate", { expression: `(() => {
      const kind = document.querySelector('[data-platform-filter="kind"]');
      kind.value = "";
      const input = document.querySelector('[data-platform-filter="query"]');
      input.focus();
      input.value = "reader";
      input.dispatchEvent(new Event("input", { bubbles: true }));
      const audit = document.querySelector('.account-detail-header a[href*="account="]');
      return input === document.activeElement && !!document.querySelector('.account-detail-header [data-system-open="assign"]') &&
        new URLSearchParams(audit.hash.split("?")[1]).get("account").startsWith("ldap:cn=reader");
    })()`, returnByValue: true });
    if (!inlineSearch.result?.value) throw new Error("Unique account search did not restore details, focus, or the account audit shortcut");
    await assertText(cdp, (text) => text.includes("生效权限及来源") && text.includes("用户组 运维") &&
      text.includes("直接授予"), "User detail omitted permission origins");
    await cdp.send("Runtime.evaluate", { expression: `document.querySelector('[data-system-open="account-action"]').click()` });
    await assertText(cdp, (text) => text.includes("确认停用") && text.includes("确认"), "Account disable did not open its confirmation");
    await cdp.send("Runtime.evaluate", { expression: 'document.querySelector("dialog[open] footer [data-system-close]").click()' });
    await cdp.send("Runtime.evaluate", { expression: `document.querySelector('[data-system-tab="bindings"]').click()` });
    await assertText(cdp, (text) => text.includes("直接角色") && text.includes("访客只读") && text.includes("分配或撤销"), "Account associations did not render");
    await assertAccountDetailLayout(cdp, 1440);
    await assertAccountDetailLayout(cdp, 390);
    await cdp.send("Emulation.clearDeviceMetricsOverride");
    await cdp.send("Runtime.evaluate", { expression: `document.querySelector('[data-system-open="assign"]').click()` });
    await assertText(cdp, (text) => text.includes("分配用户组或角色") && text.includes("授权对象"), "Account assignment did not open from associations");
    await assertPage(cdp, "http://127.0.0.1:4173/#/admin/ldap",
      (text) => text.includes("LDAP 连接配置") && text.includes("30 分钟"), "Authentication summaries did not render");
    await assertAuthenticationLayout(cdp, 1440, 232);
    await assertAuthenticationLayout(cdp, 390, 0);
    await cdp.send("Emulation.clearDeviceMetricsOverride");
    await cdp.send("Runtime.evaluate", { expression: `(() => {
      document.querySelector(".auth-session-summary").click();
      window.saveFixtureSessionMinutes = (value) => {
        const input = document.querySelector("#session-minutes");
        input.value = value;
        input.dispatchEvent(new Event("input", { bubbles: true }));
        input.form.querySelector("button").click();
      };
      window.saveFixtureSessionMinutes("4");
    })()` });
    await assertText(cdp, (text) => text.includes("请输入 5 至 480 之间的整数。"), "Invalid duration was not explained");
    await cdp.send("Runtime.evaluate", { expression: `window.saveFixtureSessionMinutes("90")` });
    await assertText(cdp, (text) => text.includes("当前设置未更改"), "Session policy save failure was not shown");
    await cdp.send("Runtime.evaluate", { expression: `window.saveFixtureSessionMinutes("60")` });
    await assertText(cdp, (text) => text.includes("60 分钟") && text.includes("下次登录时生效"), "Saved duration did not update the summary");
    await assertPage(cdp, "http://127.0.0.1:4173/#/admin/ldap",
      (text) => text.includes("60 分钟"), "Saved duration did not persist after reload");
    await cdp.send("Runtime.evaluate", { expression: `document.querySelector(".auth-ldap-card > summary").click()` });
    await assertText(cdp, (text) => text.includes("测试待启用配置"), "Expanded LDAP editor panels did not render");
    await assertLDAPEditorLayout(cdp, 1440, false);
    await assertLDAPEditorLayout(cdp, 390, true);
    await cdp.send("Emulation.clearDeviceMetricsOverride");
    await cdp.send("Runtime.evaluate", { expression: `document.querySelector('[data-identity-resource="ldap"] button[value="test"]').click()` });
    await assertText(cdp, (text) => text.includes("请输入 LDAP 测试员工账号"), "Missing LDAP employee credentials were not explained");
    await cdp.send("Runtime.evaluate", { expression: `(() => {
      const form = document.querySelector('[data-identity-resource="ldap"]');
      form.elements.testUsername.value = "fixture-employee";
      form.elements.testPassword.value = "fixture-password";
      form.querySelector('button[value="test"]').click();
    })()` });
    await assertText(cdp, (text) => text.includes("API Pod 无法建立到 LDAP 端口的 TCP 连接"), "LDAP TCP connection failure did not render");
    await cdp.send("Runtime.evaluate", { expression: `(() => {
      const form = document.querySelector('[data-identity-resource="ldap"]');
      form.elements.url.value = "ldap://example.test";
      form.elements.allowPlainLDAP.click();
      form.elements.baseDN.value = "dc=fixed,dc=test";
      form.elements.baseDN.dispatchEvent(new Event("input", { bubbles: true }));
      form.querySelector('button[value="test"]').click();
    })()` });
    await assertText(cdp, (text) => text.includes("均成功，可以保存"), "LDAP test did not update the verification panel");
    const ldapFieldsCleared = await cdp.send("Runtime.evaluate", { expression: `(() => {
      const form = document.querySelector('[data-identity-resource="ldap"]');
      return form.elements.testUsername.value === "" && form.elements.testPassword.value === "";
    })()`, returnByValue: true });
    if (!ldapFieldsCleared.result?.value) throw new Error("Successful LDAP test did not clear employee credentials");
    await cdp.send("Runtime.evaluate", { expression: `document.querySelector('[data-identity-resource="ldap"] button[type="submit"]:not([value="test"])').click()` });
    await assertText(cdp, (text) => text.includes("已保存") && text.includes("已启用"), "LDAP save did not show confirmation in its own panel");
    await assertPage(cdp, "http://127.0.0.1:4173/#/admin/access/groups",
      (text) => text.includes("新建用户组") && text.includes("运维"), "User group tab did not render");
    await cdp.send("Runtime.evaluate", { expression: `document.querySelector('[data-system-open="role:guest-readonly"]').click()` });
    await assertText(cdp, (text) => text.includes("预置角色的权限固定") && text.includes("历史诊断查看"), "Group link did not open the shared read-only role viewer");
    await cdp.send("Runtime.evaluate", { awaitPromise: true, expression: `new Promise(resolve => {
      const dialog = document.querySelector("dialog[open]");
      dialog.addEventListener("close", resolve, { once: true });
      dialog.querySelector("[data-system-close]").click();
    })` });
    await cdp.send("Runtime.evaluate", { expression: `document.querySelector('[data-system-open="group:ops"]').click()` });
    const rolePreselected = await cdp.send("Runtime.evaluate", { returnByValue: true,
      expression: `document.querySelector('dialog[open] input[name="roles"]').checked` });
    if (!rolePreselected.result?.value) throw new Error("Group editor did not preload existing role associations");
    await cdp.send("Runtime.evaluate", { expression: `document.querySelector('dialog[open] button[value="group-save"]').click()` });
    await assertText(cdp, (text) => text.includes("用户组保存结果"), "Group save did not show its result");
    await cdp.send("Runtime.evaluate", { expression: `document.querySelector('dialog[open] footer [data-system-close]').click()` });
    await assertText(cdp, (text) => !text.includes("用户组保存结果") && text.includes("新建用户组"), "Group save did not refresh the list");
    await assertPage(cdp, "http://127.0.0.1:4173/?group-editor#/admin/access/groups",
      text => text.includes("新建用户组") && text.includes("查看成员权限"), "Group editor did not reload");
    await cdp.send("Runtime.evaluate", { expression: `(() => {
      document.querySelector('[data-system-open="group:"]').click();
      const form = document.querySelector("dialog[open] form");
      form.elements.name.value = "管理员演练组";
      form.querySelector("[data-group-admin]").click();
      form.querySelector("[data-group-save]").click();
    })()` });
    await assertText(cdp, text => text.includes("确认管理员角色变更") && text.includes("当前成员 0 人") && text.includes("后续加入"),
      "New administrator group did not confirm empty membership and future inheritance");
    await cdp.send("Runtime.evaluate", { expression: `document.querySelector('dialog[open] [data-group-confirm]').click()` });
    await assertText(cdp, text => text.includes("用户组保存结果") && text.includes("查看本次审计"), "Administrator group save did not link to audit");
    await cdp.send("Runtime.evaluate", { expression: `document.querySelector('dialog[open] footer [data-system-close]').click()` });
    await assertText(cdp, text => text.includes("管理员演练组") && !text.includes("用户组保存结果"), "New administrator group was not listed");
    await cdp.send("Runtime.evaluate", { awaitPromise: true, expression: 'fetch("/test/admin-session?super=false")' });
    await assertPage(cdp, "http://127.0.0.1:4173/?ordinary-group-view#/admin/access/groups", text => text.includes("管理员演练组"), "Could not reload groups as ordinary administrator");
    const lockedGroup = await cdp.send("Runtime.evaluate", { returnByValue: true, expression: `(() => {
      const view = document.querySelector('[data-system-open="group:administrator-exercise"]');
      const create = document.querySelector('template[data-system-editor="group:"]').content;
      view.click();
      return view.textContent === "查看" && create.querySelector("[data-group-admin]").disabled &&
        !document.querySelector('[data-system-open="remove:administrator-exercise"]') &&
        !document.querySelector('dialog[open] button[value="group-save"]');
    })()` });
    if (!lockedGroup.result?.value) throw new Error("Ordinary administrator could edit or assign an administrator group");
    await cdp.send("Runtime.evaluate", { expression: 'document.querySelector("dialog[open] [data-system-close]").click()' });
    await cdp.send("Runtime.evaluate", { awaitPromise: true, expression: 'fetch("/test/admin-session")' });
    await cdp.send("Runtime.evaluate", { expression: 'document.querySelector(\'.portal-sidebar a[href="#/admin/access"]\').click()' });
    await assertText(cdp, text => ["角色", "用户组", "批量勾选当前结果", "授权清单", "预览并保存"].every(label => text.includes(label)),
      "Permission management entry did not display the role workspace");
    await cdp.send("Runtime.evaluate", { expression: `(() => {
      const form = document.querySelector("dialog[open] form");
      const field = form.querySelector("[data-pc-query]");
      field.value = "数据源管理";
      field.dispatchEvent(new Event("input", { bubbles: true }));
    })()` });
    const sourcePlacement = await cdp.send("Runtime.evaluate", { returnByValue: true, expression: `(() => {
      const point = document.querySelector('[data-pc-check="diagnosis.connector.read"]');
      if (!point?.closest(".permission-list-group").textContent.includes("资源管理 › 平台目录")) return false;
      document.querySelector("[data-pc-read]").click();
      return point.isConnected === false && document.querySelectorAll("[data-pc-check]:checked").length === 1;
    })()` });
    if (!sourcePlacement.result?.value) throw new Error("Datasource permissions were misplaced or read-only batch picked a write action");
    await cdp.send("Runtime.evaluate", { expression: `document.querySelector('[data-role-picker] [data-system-open="role:namespace-auditor"]').click()` });
    await assertText(cdp, text => text.includes("编辑角色 · 命名空间审计员"), "Role picker did not change the active editor");
    await cdp.send("Runtime.evaluate", { expression: `document.querySelector('[data-role-picker] [data-system-open="role:diagnosis-reader"]').click()` });
    await assertText(cdp, text => text.includes("编辑角色 · 诊断观察员") && text.includes("总待添加 1 项"), "Role picker lost the edited draft");
    await cdp.send("Runtime.evaluate", { expression: 'document.querySelector(\'main a[href="#/admin/access/catalog"]\').click()' });
    await assertText(cdp, text => ["按业务页面查看权限", "点击详情查看接口与授权条件", "权限目录"].every(label => text.includes(label)),
      "Interface permission catalog did not render independent modules");
    await cdp.send("Runtime.evaluate", { expression: `(() => {
      document.querySelector('[data-pc-module="knowledge"]').click();
      const query = document.querySelector("[data-pc-query]");
      query.value = "knowledge.workflow.publish";
      query.dispatchEvent(new Event("input", { bubbles: true }));
      document.querySelector('[data-pc-point="knowledge.workflow.publish"]').click();
    })()` });
    await assertText(cdp, text => text.includes("action=publish") && text.includes("/api/v1/wiki/candidates/{id}") && !text.includes("保存草稿"),
      "Catalog search did not isolate the publish action");
    await cdp.send("Runtime.evaluate", { expression: `document.querySelector('[data-pc-details] a[href*="permission="]').click()` });
    await assertText(cdp, text => text.includes("授权清单") && text.includes("1 个接口权限点"),
      "Catalog did not carry the permission search into the role workspace");
    await assertPage(cdp, "http://127.0.0.1:4173/#/admin/access/catalog",
      text => text.includes("按业务页面查看权限"), "Could not return to the read-only permission catalog");
    await assertRolePermissionSelection(cdp);
    await cdp.send("Runtime.evaluate", { expression: `document.querySelector('dialog[open] footer [data-system-close]').click()` });
    await assertText(cdp, text => text.includes("接口授权检查") && !text.includes("角色保存结果"), "Role list did not refresh after confirming the save");
    const configuredScope = await cdp.send("Runtime.evaluate", { returnByValue: true, expression: `(() => {
      document.querySelector('[data-system-open="role:interface-audit"]').click();
      const form = document.querySelector("dialog[open] form");
      form.querySelector('[data-pc-module="system"]').click();
      const filter = form.querySelector("[data-pc-filter]");
      filter.value = "configured";
      filter.dispatchEvent(new Event("change", { bubbles: true }));
      const point = form.querySelector('[data-pc-point="system.audit.list"]');
      point?.click();
      const visible = Boolean(point?.hasAttribute("data-pc-configured")) &&
        form.querySelector("[data-pc-details]").textContent.includes("已保存");
      const audit = [...form.querySelectorAll("[data-role-grant]")].find(row => JSON.parse(row.dataset.roleGrant).code === "system.audit.list");
      audit.querySelector("[data-role-grant-remove]").click();
      const removed = !form.querySelector('[data-pc-point="system.audit.list"]');
      document.querySelector("dialog[open] header [data-system-close]").click();
      return visible && removed;
    })()` });
    if (!configuredScope.result?.value) throw new Error("Role editor did not show or refresh configured interface scopes");
    await assertPage(cdp, "http://127.0.0.1:4173/#/admin/access/roles",
      (text) => text.includes("权限管理") && text.includes("权限目录") && text.includes("新建角色") && text.includes("访客只读"), "Renamed role workspace did not render");
    const permissionNames = await cdp.send("Runtime.evaluate", { returnByValue: true, expression: `({
      title: document.querySelector("main h1")?.textContent,
      tab: document.querySelector(".portal-sidebar [aria-current=page]")?.textContent,
      catalog: document.querySelector('main a[href="#/admin/access/catalog"]')?.textContent
    })` });
    if (permissionNames.result?.value?.title !== "权限管理" || permissionNames.result?.value?.tab !== "权限管理" ||
        permissionNames.result?.value?.catalog !== "权限目录") throw new Error("Permission workspace labels do not match their purpose");
    await cdp.send("Runtime.evaluate", { expression: `(() => {
      document.querySelector('[data-system-open="role:"]').click();
      const form = document.querySelector("dialog[open] form");
      form.elements.name.value = "浏览器检查";
      const realFetch = window.fetch;
      window.fetch = (input, options) => {
        if (!String(input).startsWith("/api/v1/admin/namespace-business?cluster=")) return realFetch(input, options);
        window.fetch = realFetch;
        return Promise.resolve(new Response("", { status: 503 }));
      };
      const cluster = form.querySelector('[name="cluster"]');
      cluster.value = "local";
      cluster.dispatchEvent(new Event("change", { bubbles: true }));
    })()` });
    await assertText(cdp, (text) => text.includes("命名空间暂不可用") && text.includes("重试加载"),
      "Namespace directory failure did not give a retry");
    await cdp.send("Runtime.evaluate", { expression: `(() => {
      const form = document.querySelector("dialog[open] form");
      form.querySelector("[data-scope-add]").click();
      if (form.querySelector("[data-saved-scope]")) throw new Error("Namespace failure added an overbroad grant");
      form.querySelector("[data-scope-retry]").click();
    })()` });
    for (let attempt = 0; attempt < 40; attempt += 1) {
      const loaded = await cdp.send("Runtime.evaluate", { returnByValue: true,
        expression: `Boolean(document.querySelector('dialog[open] input[name="namespace"][value="payments"]'))` });
      if (loaded.result?.value) break;
      if (attempt === 39) throw new Error("Role editor did not populate namespaces");
      await sleep(100);
    }
    await cdp.send("Runtime.evaluate", { expression: `document.querySelector('dialog[open] button[value="role-preview"]').click()` });
    await assertText(cdp, (text) => text.includes("请添加至少一项授权范围"), "Saving used an uncommitted scope");
    await cdp.send("Runtime.evaluate", { expression: `(() => {
      const form = document.querySelector("dialog[open] form");
      for (const name of ["payments", "monitoring"]) form.querySelector('input[name="namespace"][value="' + name + '"]').click();
      form.querySelector('[data-scope-add="preview"]').click();
    })()` });
    await assertText(cdp, (text) => text.includes("新增权限 · 2 项") && text.includes("payments") && text.includes("monitoring") && text.includes("确认保存"),
      "Role preview did not display both selected namespaces");
    await cdp.send("Runtime.evaluate", { expression: `(() => {
      const form = document.querySelector("dialog[open] form");
      form.querySelector("[data-role-back]").click();
      if (form.querySelectorAll("[data-saved-scope]").length !== 2) throw new Error("Preview lost the draft");
      form.querySelector('button[value="role-preview"]').click();
      form.querySelector('button[value="role-save"]').click();
    })()` });
    await assertText(cdp, (text) => text.includes("角色名称已使用") && text.includes("确认保存"), "Role editor did not keep the form open on conflict");
    await cdp.send("Runtime.evaluate", { expression: 'document.querySelector("dialog[open] header [data-system-close]").click()' });
    await assertPage(cdp, "http://127.0.0.1:4173/#/admin/users",
      (text) => text.includes("reader") && text.includes("删除用户"), "User deletion is missing from the selection bar");
    await cdp.send("Runtime.evaluate", { expression: `(() => {
      const button = document.querySelector("[data-platform-delete]");
      if (!button.disabled) throw new Error("Deletion was enabled without a selection");
      document.querySelector('[data-platform-row] input[name="ids"]').click();
      button.click();
    })()` });
    await assertText(cdp, (text) => text.includes("确定删除「reader」") && text.includes("无法通过原 LDAP 身份再次登录"), "Deletion confirmation did not show the selected account or impact");
    const cancelFocused = await cdp.send("Runtime.evaluate", { expression: 'document.activeElement.textContent === "取消"', returnByValue: true });
    if (!cancelFocused.result?.value) throw new Error("Deletion dialog must focus Cancel");
    await cdp.send("Runtime.evaluate", { expression: 'document.activeElement.click()' });
    await assertText(cdp, (text) => text.includes("reader") && !text.includes("确定删除"), "Cancelling deletion did not keep the account");
    await cdp.send("Runtime.evaluate", { expression: `(() => {
      document.querySelector("[data-platform-delete]").click();
      document.querySelector('dialog[open] button[value="delete"]').click();
    })()` });
    await assertText(cdp, (text) => text.includes("共 0 个账号") && !text.includes("reader"), "Deleted account still appeared in the list");
    await assertPage(cdp, "http://127.0.0.1:4173/#/admin/operations",
      (text) => text.includes("操作审计") && text.includes("创建账号") && text.includes("local:guest"), "Operation audit did not render");
    await assertText(cdp, (text) => text.includes("2026年11月18日15:43:19") && ["3 天", "5 天", "7 天"].every((label) => text.includes(label)), "Audit time or default retention did not render");
    await assertText(cdp, (text) => text.includes("共 76 条") && text.includes("第 1 / 8 页"), "Audit did not default to 10 rows per page");
    await cdp.send("Runtime.evaluate", { expression: 'document.querySelector("[data-audit-next]").click()' });
    await assertText(cdp, (text) => text.includes("第 2 / 8 页") && !text.includes("local:guest"), "Audit next page kept first-page results");
    await assertPage(cdp, "about:blank", (text) => text === "", "Audit document was not cleared");
    await assertPage(cdp, "http://127.0.0.1:4173/#/admin/operations?page=2",
      (text) => text.includes("第 2 / 8 页"), "Audit page number did not survive a fresh load");
    await cdp.send("Runtime.evaluate", { expression: 'document.querySelector("[data-audit-previous]").click()' });
    await assertText(cdp, (text) => text.includes("第 1 / 8 页") && text.includes("local:guest"), "Audit previous page failed after refresh");
    await cdp.send("Runtime.evaluate", { returnByValue: true, expression: `(() => {
      const select = document.querySelector("[data-audit-limit]");
      select.value = "custom";
      select.dispatchEvent(new Event("change", { bubbles: true }));
      const form = document.querySelector("[data-audit-limit-form]");
      form.elements.customLimit.value = "13";
      form.requestSubmit();
    })()` });
    await assertText(cdp, (text) => text.includes("第 1 / 6 页"), "Custom audit page size did not recalculate pages");
    const customPage = await cdp.send("Runtime.evaluate", { returnByValue: true, expression: `({
      size: document.querySelector("[data-audit-limit]").value,
      rows: document.querySelectorAll("[data-audit-detail]").length,
      saved: new URLSearchParams(location.hash.split("?")[1]).get("limit"),
    })` });
    if (customPage.result?.value?.size !== "13" || customPage.result?.value?.rows !== 13 || customPage.result?.value?.saved !== "13") {
      throw new Error("Custom audit page size was not applied or retained in the URL");
    }
    for (const [size, pages, rows] of [[50, 2, 50], [20, 4, 20]]) {
      await cdp.send("Runtime.evaluate", { expression: `(() => {
        const select = document.querySelector("[data-audit-limit]");
        select.value = "${size}";
        select.dispatchEvent(new Event("change", { bubbles: true }));
      })()` });
      await assertText(cdp, (text) => text.includes("共 76 条") && text.includes(`第 1 / ${pages} 页`), "Audit page size change did not recalculate pages");
      const count = await cdp.send("Runtime.evaluate", { returnByValue: true,
        expression: 'document.querySelectorAll("[data-audit-detail]").length' });
      if (count.result?.value !== rows) throw new Error(`Audit page size ${size} returned ${count.result?.value} rows`);
    }
    await cdp.send("Emulation.setDeviceMetricsOverride", { width: 390, height: 820, deviceScaleFactor: 1, mobile: false });
    const auditLayout = await cdp.send("Runtime.evaluate", { returnByValue: true, expression: `(() => {
      const table = document.querySelector('.audit-records [role="region"]');
      return table.tabIndex === 0 && table.scrollWidth > table.clientWidth && document.documentElement.scrollWidth <= innerWidth;
    })()` });
    if (!auditLayout.result?.value) throw new Error("Audit table must scroll within the mobile layout and support keyboard focus");
    await cdp.send("Emulation.clearDeviceMetricsOverride");
    await cdp.send("Runtime.evaluate", { expression: `(() => {
      const form = document.querySelector("[data-audit-filter]");
      form.elements.account.value = "批量操作";
      form.requestSubmit();
    })()` });
    await assertText(cdp, (text) => text.includes("共 1 条") && text.includes("批量加入用户组") && !text.includes("local:guest"),
      "Audit account search did not narrow the list before opening the batch");
    await cdp.send("Runtime.evaluate", { expression: 'document.querySelector(\'[data-audit-detail="75"]\').click()' });
    await assertText(cdp, (text) => text.includes("批次结果") && text.includes("ldap:alice") && text.includes("查看同批次"), "Audit batch detail did not render per-target outcomes");
    await cdp.send("Runtime.evaluate", { expression: 'document.querySelector("[data-audit-show-batch]").click()' });
    await assertText(cdp, (text) => text.includes("清除批次") && text.includes("ldap:alice") && !text.includes("local:guest"), "Batch filter did not exclude unrelated records");
    await cdp.send("Runtime.evaluate", { expression: 'document.querySelector("[data-audit-next]").click()' });
    await assertText(cdp, (text) => text.includes("ldap:bob") && text.includes("第 2 / 2 页") && !text.includes("ldap:alice"), "Audit paging did not advance within the batch");
    await cdp.send("Runtime.evaluate", { expression: 'document.querySelector(\'[data-audit-detail="54"]\').click()' });
    await assertText(cdp, (text) => text.includes("失败原因") && text.includes("账号已停用"), "Audit details omitted a per-target failure reason");
    await cdp.send("Runtime.evaluate", { expression: 'document.querySelector("[data-audit-close]").click(); document.querySelector("[data-audit-previous]").click();' });
    await assertText(cdp, (text) => text.includes("ldap:alice") && !text.includes("ldap:bob"), "Audit paging did not return to the first batch page");
    await cdp.send("Runtime.evaluate", { expression: 'document.querySelector("[data-audit-clear-batch]").click()' });
    await assertText(cdp, (text) => text.includes("local:guest") && !text.includes("清除批次"), "Clearing the audit batch failed");
    await cdp.send("Runtime.evaluate", { expression: `(() => {
      document.querySelector("[data-audit-edit]").click();
      const form = document.querySelector("[data-audit-retention]");
      form.elements.low.value = "2";
      form.elements.medium.value = "6";
      form.elements.high.value = "9";
      form.requestSubmit();
    })()` });
    await assertText(cdp, (text) => text.includes("已保存保留策略") && text.includes("local:guest") && ["2 天", "6 天", "9 天"].every((label) => text.includes(label)), "Audit retention did not save");
    const zeroMedium = `(() => {
      document.querySelector("[data-audit-edit]").click();
      const form = document.querySelector("[data-audit-retention]");
      form.elements.medium.value = "0";
      form.requestSubmit();
    })()`;
    await cdp.send("Runtime.evaluate", { expression: zeroMedium });
    await assertText(cdp, (text) => text.includes("确认不保留审计记录") && text.includes("清除中风险"), "Disabling a retention tier did not show an explicit confirmation");
    const auditCancel = await cdp.send("Runtime.evaluate", { returnByValue: true, expression: 'document.activeElement.matches("[data-audit-back]")' });
    if (!auditCancel.result?.value) throw new Error("Audit purge confirmation must focus Cancel");
    await cdp.send("Runtime.evaluate", { expression: 'document.querySelector("[data-audit-back]").click(); document.querySelector("[data-audit-close]").click()' });
    if (await fetch("http://127.0.0.1:4173/test/audit-saves").then((response) => response.json()) !== 1) throw new Error("Cancelling audit purge still saved the policy");
    await cdp.send("Runtime.evaluate", { expression: zeroMedium });
    await cdp.send("Runtime.evaluate", { expression: 'document.querySelector("[data-audit-confirm]").requestSubmit()' });
    await assertText(cdp, (text) => text.includes("不保留") && text.includes("local:guest") && !text.includes("批量加入用户组"), "Disabling audit retention did not remove affected records");

    await assertPage(cdp, "http://127.0.0.1:4173/#/admin/operations?risk=%E9%AB%98",
      (text) => text.includes("local:guest") && text.includes("编辑保留策略"), "Audit filter deep link did not load");
    const filterReady = await cdp.send("Runtime.evaluate", { returnByValue: true, awaitPromise: true, expression: `new Promise(resolve => {
      const ready = () => document.querySelector('[data-audit-filter] select[name=risk]')?.value === "高";
      if (ready()) { resolve(true); return; }
      const observer = new MutationObserver(() => { if (ready()) { observer.disconnect(); resolve(true); } });
      observer.observe(document.querySelector("#app"), { childList: true, subtree: true });
      setTimeout(() => { observer.disconnect(); resolve(false); }, 3000);
    })` });
    if (!filterReady.result?.value) throw new Error("Audit view was not initialized with the deep link filter");
    await cdp.send("Runtime.evaluate", { expression: 'document.querySelector("[data-audit-edit]").click()' });
    await fetch("http://127.0.0.1:4173/test/require-auth");
    await cdp.send("Runtime.evaluate", { expression: `(() => {
      const form = document.querySelector("[data-audit-retention]");
      form.elements.low.value = "4";
      form.requestSubmit();
    })()` });
    await assertText(cdp, (text) => text.includes("登录后可继续使用操作审计") && !text.includes("local:guest"), "Expired audit session did not clear the page and request sign-in");
    await cdp.send("Runtime.evaluate", { expression: `(() => {
      const login = document.querySelector("[data-login]");
      login.elements.username.value = "portal-test";
      login.elements.password.value = "fixture-password";
      login.requestSubmit();
    })()` });
    await assertText(cdp, (text) => text.includes("local:guest") && text.includes("2 天") && !text.includes("4 天"), "Audit login did not return to a read-only refresh of the current route");
    const auditFilterState = await cdp.send("Runtime.evaluate", { returnByValue: true, expression: 'document.querySelector("[data-audit-filter] select[name=risk]").value' });
    if (auditFilterState.result?.value !== "高" || await fetch("http://127.0.0.1:4173/test/audit-saves").then((response) => response.json()) !== 2) {
      throw new Error("Audit sign-in lost the filter or replayed the unsaved PUT");
    }
    await fetch("http://127.0.0.1:4173/test/admin-session?super=false");
    await cdp.send("Runtime.evaluate", { expression: `(() => {
      document.querySelector("[data-audit-edit]").click();
      const form = document.querySelector("[data-audit-retention]");
      form.elements.low.value = "4";
      form.requestSubmit();
    })()` });
    await assertText(cdp, (text) => text.includes("无访问权限") && !text.includes("需要身份认证"),
      "Audit retention permission denial incorrectly prompted sign-in");
    await assertPage(cdp, "http://127.0.0.1:4173/#/admin/operations",
      (text) => text.includes("审计保留策略") && text.includes("local:guest") && !text.includes("编辑保留策略"), "Ordinary administrators should only read audit retention");
    await fetch("http://127.0.0.1:4173/test/admin-session");
    await assertPage(cdp, "http://127.0.0.1:4173/#/admin/clusters",
      (text) => text.includes("Kubernetes 集群管理") && text.includes("接入集群"), "Legacy cluster route did not lead to resource management");
    await cdp.send("Runtime.evaluate", { expression: `document.querySelector('[data-cluster-editor="choose"]').click(); document.querySelector('[data-cluster-editor="push"]').click();` });
    await assertDOM(cdp, `!!document.querySelector('dialog[open] form[data-onboarding-mode="push"]')`, "Collector enrollment dialog did not render");
    await cdp.send("Runtime.evaluate", {
      expression: `(() => {
        const form = document.querySelector("[data-onboarding-mode]");
        form.elements.id.value = "dev-yunfan-01";
        form.elements.name.value = "研发实例";
        form.elements.environment.value = "研发环境";
        form.elements.business.value = "云帆";
        form.elements.group.value = "应用集群";
        form.requestSubmit();
      })()`,
    });
    await assertText(cdp, (text) => text.includes("请将上报凭据保存到采集端 Secret"), "Enrollment did not return credentials");
    await cdp.send("Runtime.evaluate", { expression: 'document.querySelector("dialog[open] [data-cluster-close]").click()' });
    await assertText(cdp, (text) => text.includes("Kubernetes 集群管理") && !text.includes("上报凭据保存"), "Enrollment did not return to the cluster list");

    await assertIncidentLifecycle(cdp);
    await assertRemainingDiagnosticPages(cdp);
    await assertPortalFollowupPrototypes(cdp);
    await assertPermissionPrototype(cdp);
  } finally {
    cdp.close();
  }

  console.log("Portal browser E2E: PASS");
} finally {
  chrome.kill("SIGTERM");
  if (chrome.exitCode === null) await once(chrome, "exit");
  await rm(userDataDir, {
    recursive: true,
    force: true,
    maxRetries: 5,
    retryDelay: 100,
  });
}

async function waitForPage(debugPort) {
  const url = `http://127.0.0.1:${debugPort}/json/list`;
  let lastError;
  // Chrome can take over 30 seconds to open DevTools on a cold CI runner.
  const deadline = Date.now() + 60_000;
  while (Date.now() < deadline) {
    try {
      const response = await fetch(url, { signal: AbortSignal.timeout(2_000) });
      if (response.ok) {
        const pages = await response.json();
        const page = pages.find(
          (item) => item.type === "page" && item.webSocketDebuggerUrl,
        );
        if (page) return page;
      }
    } catch (error) {
      lastError = error;
    }
    await sleep(100);
  }
  throw new Error(`Chrome DevTools endpoint did not become ready: ${lastError ?? "unknown error"}`);
}

// 按原型验证两块默认折叠、桌面侧栏和移动工作区。
async function assertAuthenticationLayout(cdp, width, sidebarWidth) {
  await cdp.send("Emulation.setDeviceMetricsOverride", { width, height: 900, deviceScaleFactor: 1, mobile: width < 680 });
  const result = await cdp.send("Runtime.evaluate", { returnByValue: true, expression: `({
    session: document.querySelector(".auth-session-card")?.open,
    ldap: document.querySelector(".auth-ldap-card")?.open,
    sidebar: document.querySelector(".portal-sidebar")?.getBoundingClientRect().width,
    overflows: document.documentElement.scrollWidth > innerWidth,
    active: document.querySelector(".portal-sidebar [aria-current=page]")?.textContent
  })` });
  const layout = result.result?.value;
  if (!layout || layout.session !== false || layout.ldap !== false || layout.sidebar !== sidebarWidth ||
      layout.overflows || layout.active !== "认证配置") {
    throw new Error(`Authentication layout at ${width}px: ${JSON.stringify(layout)}`);
  }
}

async function assertLDAPEditorLayout(cdp, width, stacked) {
  await cdp.send("Emulation.setDeviceMetricsOverride", { width, height: 900, deviceScaleFactor: 1, mobile: width < 680 });
  const result = await cdp.send("Runtime.evaluate", { returnByValue: true, expression: `(() => {
    const form = document.querySelector('[data-identity-resource="ldap"]');
    const [connection, validation] = form.querySelectorAll(".auth-ldap-section");
    const bounds = connection.getBoundingClientRect();
    const testBounds = validation.getBoundingClientRect();
    return {
      stacked: testBounds.top >= bounds.bottom,
      usable: ["url", "bindDN", "baseDN", "testUsername", "testPassword"].every(name => {
        const field = form.elements[name];
        const input = field.getBoundingClientRect();
        const label = field.closest("label").getBoundingClientRect();
        return input.top > label.top + 8 && input.width >= label.width - 1 && input.height >= 36;
      }),
      plainAligned: (() => {
        const field = form.elements.allowPlainLDAP;
        const checkbox = field.getBoundingClientRect();
        const text = field.nextElementSibling.getBoundingClientRect();
        return checkbox.right < text.left &&
          Math.abs(checkbox.top + checkbox.height / 2 - text.top - text.height / 2) <= 1;
      })(),
      clipped: document.documentElement.scrollWidth > innerWidth,
      advanced: form.querySelector(".auth-ldap-advanced")?.open,
      steps: validation.querySelectorAll("[data-ldap-step]").length,
      saveLocked: form.querySelector('button[type="submit"]:not([value="test"])').disabled,
    };
  })()` });
  const layout = result.result?.value;
  if (!layout || layout.stacked !== stacked || !layout.usable || !layout.plainAligned || layout.clipped || layout.advanced ||
      layout.steps !== 4 || !layout.saveLocked) {
    throw new Error(`LDAP editor layout at ${width}px: ${JSON.stringify(layout)}`);
  }
}

async function createCdpClient(webSocketUrl) {
  const socket = new WebSocket(webSocketUrl);
  const pending = new Map();
  let sequence = 0;

  await new Promise((resolve, reject) => {
    socket.addEventListener("open", resolve, { once: true });
    socket.addEventListener("error", reject, { once: true });
  });

  socket.addEventListener("message", (event) => {
    const message = JSON.parse(String(event.data));
    if (!message.id) return;
    const request = pending.get(message.id);
    if (!request) return;
    pending.delete(message.id);
    if (message.error) {
      request.reject(new Error(message.error.message ?? "Chrome DevTools error"));
      return;
    }
    request.resolve(message.result ?? {});
  });

  return {
    send(method, params = {}) {
      sequence += 1;
      const id = sequence;
      const promise = new Promise((resolve, reject) => {
        pending.set(id, { resolve, reject });
      });
      socket.send(JSON.stringify({ id, method, params }));
      return promise;
    },
    close() {
      socket.close();
    },
  };
}

// 验证说明可通过悬停、聚焦和点按阅读，关闭不改变筛选。
async function assertSeverityDescriptions(cdp) {
  const initial = await cdp.send("Runtime.evaluate", {
    expression: `(() => {
      const { x, y } = document.querySelector(".metric-critical button").getBoundingClientRect();
      return { x: x + 3, y: y + 3 };
    })()`,
    returnByValue: true,
  });
  await cdp.send("Input.dispatchMouseEvent", { type: "mouseMoved", ...initial.result.value });
  await assertText(cdp, (text) => text.includes("关键工作负载无可用副本"), "Critical definition did not open on hover");
  await cdp.send("Input.dispatchMouseEvent", { type: "mouseMoved", x: 1, y: 1 });
  const response = await cdp.send("Runtime.evaluate", {
    expression: `(() => {
      const hash = location.hash;
      const warning = document.querySelector(".metric-warning button");
      warning.focus();
      const warningHelp = document.getElementById(warning.getAttribute("aria-controls"));
      const focused = !warningHelp.hidden && warningHelp.textContent.includes("工作负载反复重启");
      warning.dispatchEvent(new KeyboardEvent("keydown", { key: "Escape", bubbles: true }));
      const dismissed = warningHelp.hidden && document.activeElement === warning;
      const info = document.querySelector(".metric-info button");
      info.click();
      const infoHelp = document.getElementById(info.getAttribute("aria-controls"));
      const bounds = infoHelp.getBoundingClientRect();
      const opened = !infoHelp.hidden && infoHelp.textContent.includes("暂未观察到直接服务影响") &&
        bounds.left >= 0 && bounds.right <= innerWidth;
      info.click();
      return focused && dismissed && opened && infoHelp.hidden && location.hash === hash;
    })()`,
    returnByValue: true,
  });
  if (response.result?.value !== true) throw new Error("Severity help focus, dismissal, or mobile positioning failed");
}

// 验证环境缩小查询范围，以及同名诊断的实例路由和返回链接。
async function assertFindingEnvironment(cdp) {
  await cdp.send("Runtime.evaluate", {
    expression: `(() => {
      const environment = document.querySelector('select[name="environment"]');
      environment.value = "测试";
      environment.dispatchEvent(new Event("change", { bubbles: true }));
    })()`,
  });
  await assertText(cdp, (text) => text.includes("共 1 条诊断结果") &&
    text.includes("测试集群容量建议") && !text.includes("Replica availability degraded"),
  "Environment selection did not limit the aggregate list and summary");
  await cdp.send("Runtime.evaluate", { expression: 'document.querySelector(".finding-problem a").click()' });
  await assertText(cdp, (text) => text.includes("测试环境容器资源建议"), "Duplicate finding ID opened the wrong cluster");
  await cdp.send("Runtime.evaluate", { expression: 'document.querySelector(".back-link").click()' });
  await assertText(cdp, (text) => text.includes("共 1 条诊断结果") &&
    text.includes("测试集群容量建议"), "Returning from finding detail lost the environment filter");
}

// 验证分组筛选、响应式统计栏和窄屏表格焦点。
async function assertFindingWorkspaceLayout(cdp, width, columns) {
  await cdp.send("Emulation.setDeviceMetricsOverride", {
    width, height: 900, deviceScaleFactor: 1, mobile: width < 560,
  });
  const response = await cdp.send("Runtime.evaluate", {
    expression: `(() => {
      const workspace = document.querySelector(".finding-workspace");
      const table = workspace.querySelector(".table-wrap");
      const heading = document.getElementById(table.getAttribute("aria-labelledby"));
      const primaryLink = table.querySelector(".finding-problem a");
      table.focus({ preventScroll: true });
      return {
        columns: getComputedStyle(workspace).gridTemplateColumns.split(" ").length,
        legends: [...document.querySelectorAll(".filters legend")].map((node) => node.textContent),
        tableOverflows: table.scrollWidth > table.clientWidth,
        pageOverflows: document.documentElement.scrollWidth > innerWidth,
        namedRegion: table.getAttribute("role") === "region" && heading?.textContent.includes("条诊断结果"),
        canFocusTable: table.tabIndex === 0 && document.activeElement === table,
        findingLinkVisible: primaryLink.getBoundingClientRect().right <= table.getBoundingClientRect().right,
      };
    })()`,
    returnByValue: true,
  });
  const layout = response.result?.value;
  if (!layout || layout.columns !== columns || layout.legends.join("/") !== "集群范围/结果筛选" ||
    layout.tableOverflows !== (width < 560) || layout.pageOverflows || !layout.namedRegion || !layout.canFocusTable ||
    !layout.findingLinkVisible) {
    throw new Error(`Finding workspace layout at ${width}px: ${JSON.stringify(response.exceptionDetails ?? layout)}`);
  }
}

async function assertDetailLayout(cdp, width, columns) {
  await cdp.send("Emulation.setDeviceMetricsOverride", {
    width, height: 900, deviceScaleFactor: 1, mobile: width < 560,
  });
  const response = await cdp.send("Runtime.evaluate", {
    expression: `(() => {
      const detail = document.querySelector(".detail-grid");
      const texts = [...detail.querySelectorAll(".detail-text")];
      return {
        columns: getComputedStyle(detail).gridTemplateColumns.split(" ").length,
        overflow: document.documentElement.scrollWidth > innerWidth,
        texts: texts.map((text) => {
          const style = getComputedStyle(text);
          const range = document.createRange();
          range.selectNodeContents(text);
          const bounds = text.getBoundingClientRect();
          const lines = [...range.getClientRects()];
          return {
            fontSize: Number.parseFloat(style.fontSize),
            lineHeight: Number.parseFloat(style.lineHeight),
            keepsNewline: text.innerText.includes("\\n"),
            safeWrap: lines.length > 3 && lines.every((line) =>
              line.left >= bounds.left - 1 && line.right <= bounds.right + 1),
          };
        }),
      };
    })()`,
    returnByValue: true,
  });
  const layout = response.result?.value;
  if (!layout || layout.columns !== columns || layout.overflow || layout.texts.length !== 2 ||
    layout.texts.some((text) => text.fontSize !== 16 || text.lineHeight < 24 || !text.keepsNewline || !text.safeWrap)) {
    throw new Error(`Finding text layout at ${width}px: ${JSON.stringify(response.exceptionDetails ?? layout)}`);
  }
}

async function assertAccountDetailLayout(cdp, width) {
  await cdp.send("Emulation.setDeviceMetricsOverride", { width, height: 900, deviceScaleFactor: 1, mobile: width < 560 });
  const response = await cdp.send("Runtime.evaluate", {
    expression: `(() => {
      const detail = document.querySelector(".system-detail");
      const rect = (selector) => detail.querySelector(selector).getBoundingClientRect();
      const status = rect(".account-status");
      const action = rect('[data-system-open="account-action"]');
      const bindings = rect(".account-bindings");
      const bindingAction = rect('[data-system-open="assign"]');
      const rows = [...detail.querySelectorAll(".account-binding")];
      const identity = detail.querySelector(".account-identity");
      const dn = document.createRange();
      dn.selectNodeContents(identity.querySelector("code"));
      const bounds = identity.getBoundingClientRect();
      return {
        overflow: document.documentElement.scrollWidth > innerWidth,
        aligned: Math.abs(status.top + status.height / 2 - action.top - action.height / 2) <= 1,
        actionSeparated: action.left - status.right >= 10,
        rowsFillPanel: rows.length === 2 && rows.every((row) => {
          const label = row.querySelector("dt").getBoundingClientRect();
          const value = row.querySelector("dd").getBoundingClientRect();
          return Math.abs(row.getBoundingClientRect().right - bindings.right) <= 1 && value.left > label.right;
        }),
        headerActions: bindingAction.bottom < bindings.top && bindingAction.width > 0 &&
          detail.querySelector(".account-detail-header a").textContent === "账号审计",
        wrapsDN: [...dn.getClientRects()].every((line) => line.right <= bounds.right + 1),
      };
    })()`,
    returnByValue: true,
  });
  const layout = response.result?.value;
  if (!layout || layout.overflow || (width >= 560 && (!layout.aligned || !layout.actionSeparated)) || !layout.rowsFillPanel || !layout.headerActions || !layout.wrapsDN) {
    throw new Error(`Account detail layout at ${width}px: ${JSON.stringify(response.exceptionDetails ?? layout)}`);
  }
}

async function assertIncidentLifecycle(cdp) {
  await fetch("http://127.0.0.1:4173/test/incident-mode");
  await assertPage(cdp, "http://127.0.0.1:4173/#/diagnostics/events?create=1", (text) =>
    text.includes("人工创建故障") && text.includes("暂无故障"), "Incident mode did not show the create form");
  await cdp.send("Runtime.evaluate", { expression: `(() => {
    const form = document.querySelector("[data-incident-create]");
    form.elements.title.value = "订单服务故障";
    form.elements.severity.value = "P1";
    form.elements.clusterId.value = "local";
    form.elements.clusterId.dispatchEvent(new Event("change"));
  })()` });
  await assertText(cdp, (text) => text.includes("default"), "Incident form did not load namespaces");
  await cdp.send("Runtime.evaluate", { expression: `(() => {
    const form = document.querySelector("[data-incident-create]");
    form.elements.namespace.value = "default";
    form.requestSubmit();
  })()` });
  await assertText(cdp, (text) => text.includes("订单服务故障") && text.includes("版本 1") &&
    text.includes("故障时间线"), "Persisted incident did not open after creation");
  await cdp.send("Runtime.evaluate", { expression: `(() => {
    const form = document.querySelector('[data-incident-operation="update"]');
    form.closest("details").open = true;
    form.elements.status.value = "investigating";
    form.elements.reason.value = "确认受影响";
    form.requestSubmit();
  })()` });
  await assertText(cdp, (text) => text.includes("处理中") && text.includes("版本 2") &&
    text.includes("确认受影响"), "Incident status and timeline did not refresh");
  await cdp.send("Runtime.evaluate", { expression: 'document.querySelector("[data-incident-collect]").click()' });
  await assertText(cdp, (text) => text.includes("版本 3") && text.includes("来源部分可用") &&
    text.includes("VictoriaLogs 日志") && text.includes("Prometheus 指标"), "Incident aggregation did not show source health");
  const state = await cdp.send("Runtime.evaluate", {
    expression: `Array.from(document.querySelectorAll(".incident-source"), item => item.open)`, returnByValue: true,
  });
  if (state.result?.value?.join(",") !== "false,false,false,false") throw new Error("Expected all incident evidence groups collapsed");
  await cdp.send("Runtime.evaluate", { expression: `(() => {
    const button = document.querySelector("[data-incident-evidence]");
    button.closest("details").open = true;
    button.click();
  })()` });
  await assertText(cdp, (text) => text.includes("Unhealthy"), "Evidence detail did not load on demand");
  await cdp.send("Runtime.evaluate", { expression: `
    window.originalOverviewClock = Date.now;
    Date.now = () => Date.parse("2026-11-28T12:00:00Z");
  ` });
  await assertPage(cdp, "http://127.0.0.1:4173/#/diagnostics", (text) =>
    ["严重度分布", "近 7 天无新增", "最近诊断", "订单服务故障", "测试集群容量建议"].every((label) => text.includes(label)),
  "Unified overview did not show historical records and a compact empty trend");
  const trendLayout = await cdp.send("Runtime.evaluate", { returnByValue: true, expression: `(() => {
    const analytics = document.querySelector(".overview-analytics");
    const records = document.querySelector(".overview-records");
    const ring = getComputedStyle(document.querySelector(".overview-ring"));
    const criticalShare = parseFloat(ring.getPropertyValue("--overview-ring-critical")) / 100;
    const warningShare = parseFloat(ring.getPropertyValue("--overview-ring-warning")) / 100 - criticalShare;
    window.overviewRecordsBeforeWindowChange = records;
    document.querySelector("[data-overview-jump]").click();
    return {
      emptyPlot: document.querySelector(".overview-plot") === null,
      focus: document.activeElement.id,
      beforeRecords: analytics.getBoundingClientRect().bottom <= records.getBoundingClientRect().top,
      ringRendered: ring.backgroundImage.startsWith("conic-gradient"),
      criticalShare,
      warningShare,
    };
  })()` });
  const layout = trendLayout.result?.value;
  if (!layout?.emptyPlot || !layout.ringRendered || layout.focus !== "overview-recent-title" || !layout.beforeRecords ||
      Math.abs(layout.criticalShare - 1 / 3) > .01 || Math.abs(layout.warningShare - 1 / 3) > .01) {
    throw new Error(`Overview layout or severity widths: ${JSON.stringify(layout ?? trendLayout.exceptionDetails)}`);
  }
  await cdp.send("Runtime.evaluate", { expression: 'document.querySelector(".overview-trend-empty a").click()' });
  await assertText(cdp, text => text.includes("窗口内 1 条") && text.includes("订单服务故障"), "Wider trend did not expose the historical record");
  const trendInteraction = await cdp.send("Runtime.evaluate", { returnByValue: true, expression: `(() => {
    const selected = document.querySelector('[data-overview-point][aria-pressed=true]');
    const bar = selected.querySelector(".overview-trend-bars span").getBoundingClientRect();
    const plot = selected.getBoundingClientRect();
    const note = document.querySelector("[data-overview-note]");
    const details = new URLSearchParams(document.querySelector(".unified-recent a").href.split("?")[1]).get("return");
    const correct = selected.getAttribute("aria-label").includes("2026-11-20（UTC+8）共 1 条，故障事件 1 条") &&
      note.textContent.includes("故障事件 1") && Math.abs(bar.bottom - plot.bottom) <= 1.5 &&
      Math.abs(bar.height - (plot.height - 1) / 4) <= 1 &&
      window.overviewRecordsBeforeWindowChange === document.querySelector(".overview-records") &&
      details.includes("trendDays=14") && location.hash.includes("trendDays=14") &&
      document.querySelector('[name="trendDays"]').value === "14";
    selected.dispatchEvent(new KeyboardEvent("keydown", { key: "Home", bubbles: true }));
    const first = document.querySelector('[data-overview-point="0"]');
    const keyboard = first === document.activeElement && first.tabIndex === 0 && selected.tabIndex === -1 &&
      note.textContent.includes("0 条");
    delete window.overviewRecordsBeforeWindowChange;
    return correct && keyboard;
  })()` });
  if (!trendInteraction.result?.value) throw new Error("Trend window changed records or single-day selection was incorrect");
  await cdp.send("Runtime.evaluate", { expression: 'document.querySelector(".overview-distribution .overview-critical a").click()' });
  await assertText(cdp, text => text.includes("查看全部级别") && !text.includes("测试集群容量建议"), "Severity distribution did not apply the page filter");
  const filteredDistribution = await cdp.send("Runtime.evaluate", { returnByValue: true, expression: `(() => {
    const ring = getComputedStyle(document.querySelector(".overview-ring"));
    const stops = ["critical", "warning", "mapped"].map(name => parseFloat(ring.getPropertyValue("--overview-ring-" + name)));
    const labels = [...document.querySelectorAll(".overview-distribution-row small")].map(label => parseFloat(label.textContent));
    return stops.every(stop => stop === 100) && labels.join(",") === "100,0,0";
  })()` });
  if (!filteredDistribution.result?.value) throw new Error("Filtered severity proportions are incorrect");
  await cdp.send("Runtime.evaluate", { expression: 'document.querySelector(".overview-distribution .overview-critical a").click()' });
  await assertText(cdp, text => text.includes("测试集群容量建议") && !text.includes("查看全部级别"), "Selected severity did not clear the filter");
  await assertIncidentDashboard(cdp);
  await cdp.send("Runtime.evaluate", { expression: "Date.now = window.originalOverviewClock; delete window.originalOverviewClock" });
}

// 验收统一筛选、跨来源下钻和窄屏键盘操作。
async function assertIncidentDashboard(cdp) {
  const overview = "http://127.0.0.1:4173/#/diagnostics";
  await assertPage(cdp, `${overview}?clusterId=local&severity=critical&trendDays=14`, text => text.includes("订单服务故障") &&
    !text.includes("测试集群容量建议"), "Unified instance and severity scope did not apply");
  const summary = await cdp.send("Runtime.evaluate", { returnByValue: true, expression: `(() => ({
    total: document.querySelector('[aria-label="诊断指标"] .metric-total strong').textContent,
    links: [...document.querySelectorAll('.unified-recent td a')].map(a => a.getAttribute("href")),
  }))()` });
  if (summary.result?.value?.total !== "1" || summary.result.value.links.length !== 1 ||
      !summary.result.value.links[0].includes("/incidents/")) throw new Error("Linked Finding was counted twice");
  await cdp.send("Runtime.evaluate", { expression: 'document.querySelector(".unified-recent a").click()' });
  await assertText(cdp, text => text.includes("返回诊断总览"), "Incident detail did not preserve its overview origin");
  await cdp.send("Runtime.evaluate", { expression: `document.querySelector('main a[href="#/diagnostics?clusterId=local&severity=critical&trendDays=14"]').click()` });
  await assertText(cdp, text => text.includes("最近诊断") && text.includes("订单服务故障"), "Overview did not restore scope");
  await assertPage(cdp, `${overview}?clusterId=test-k8s&severity=info&trendDays=30`, text => text.includes("测试集群容量建议"),
    "Unified overview did not display the remote Finding");
  await cdp.send("Runtime.evaluate", { expression: 'document.querySelector(".unified-recent a").click()' });
  await assertText(cdp, text => text.includes("返回诊断总览") && text.includes("测试环境容器资源建议"),
    "Finding detail did not preserve its cluster instance and overview origin");
  await cdp.send("Runtime.evaluate", { expression: 'document.querySelector(".back-link").click()' });
  await assertText(cdp, text => text.includes("测试集群容量建议") && text.includes("最近诊断"), "Finding back link lost the filters");
  const findingReturn = await cdp.send("Runtime.evaluate", { expression: "location.hash", returnByValue: true });
  if (!findingReturn.result?.value?.includes("trendDays=30")) throw new Error("Finding back link lost its trend window");
  await assertPage(cdp, `${overview}?clusterId=local&severity=info`,
    text => text.includes("当前范围暂无诊断记录"), "Empty unified overview did not report zero results");
  await cdp.send("Runtime.evaluate", { expression: `(() => {
    window.originalOverviewFetch = window.fetch;
    window.fetch = (input, options) => {
      const url = new URL(typeof input === "string" ? input : input.url, location.origin);
      if (url.pathname === "/api/v1/diagnostics/incidents" ||
        (window.failBothOverviewSources && url.pathname === "/api/v1/findings")) {
        return Promise.resolve(new Response("{}", { status: 503 }));
      }
      return window.originalOverviewFetch(input, options);
    };
  })()` });
  await assertPage(cdp, `${overview}?clusterId=test-k8s`, text => text.includes("仅统计已获取记录") &&
    text.includes("数据覆盖不完整") && text.includes("测试集群容量建议"),
  "Partial coverage was presented as an empty trend");
  await cdp.send("Runtime.evaluate", { expression: "window.failBothOverviewSources = true" });
  await assertPage(cdp, `${overview}?clusterId=test-k8s&trendDays=14`,
    text => text.includes("最近诊断暂不可用") && text.includes("诊断趋势暂不可用"), "Both unavailable sources were presented as zero");
  const unavailableTotal = await cdp.send("Runtime.evaluate", { expression: `
    document.querySelector(".metric-total strong").textContent`, returnByValue: true });
  if (unavailableTotal.result?.value !== "—") throw new Error("Failure had a definitive count");
  await cdp.send("Runtime.evaluate", { expression: "window.fetch = window.originalOverviewFetch; delete window.failBothOverviewSources; delete window.originalOverviewFetch" });
  await cdp.send("Runtime.evaluate", { expression: 'document.querySelector(".overview-notice button").click()' });
  await assertText(cdp, text => text.includes("测试集群容量建议") && !text.includes("诊断趋势暂不可用"), "Retry did not restore the requested range");
  await assertPage(cdp, `${overview}?trendDays=30`, text => text.includes("全部可见实例") && text.includes("订单服务故障") &&
    text.includes("窗口内 1 条"), "Unified overview failed to reload");
  for (const [width, height] of [[1920, 900], [1440, 900], [1280, 800], [390, 844], [320, 780]]) {
    await cdp.send("Emulation.setDeviceMetricsOverride", { width, height, deviceScaleFactor: 1, mobile: false });
    const state = await cdp.send("Runtime.evaluate", { returnByValue: true, expression: `(() => {
      const table = document.querySelector(".unified-recent");
      const cards = document.querySelector(".overview-record-cards");
      return {
        scopeVisible: document.querySelector("[data-unified-filters]").getBoundingClientRect().height > 0,
        pageOverflow: document.documentElement.scrollWidth > document.documentElement.clientWidth,
        tableVisible: getComputedStyle(table).display !== "none",
        cardsVisible: getComputedStyle(cards).display !== "none",
        recordsHeadingBottom: document.querySelector(".overview-record-heading").getBoundingClientRect().bottom,
        plotScrolls: document.querySelector(".overview-plot-scroll").scrollWidth >
          document.querySelector(".overview-plot-scroll").clientWidth,
        stacked: document.querySelector(".overview-distribution-panel").getBoundingClientRect().top >=
          document.querySelector(".overview-trend").getBoundingClientRect().bottom - 1,
      };
    })()` });
    const layout = state.result?.value;
    const desktop = width > 850;
    if (!layout?.scopeVisible || layout.pageOverflow || layout.tableVisible !== desktop ||
        layout.cardsVisible === desktop || layout.stacked === desktop ||
        layout.plotScrolls === desktop || (desktop && layout.recordsHeadingBottom >= height)) {
      throw new Error(`Incident overview does not fit at ${width}px: ${JSON.stringify(layout ?? state.exceptionDetails)}`);
    }
  }
  const mobile = await cdp.send("Runtime.evaluate", { returnByValue: true, expression: `(() => {
    const button = document.querySelector(".portal-menu-toggle");
    button.click();
    const opened = button.getAttribute("aria-expanded") === "true" &&
      document.activeElement.matches(".portal-sidebar a[aria-current=page]");
    document.activeElement.dispatchEvent(new KeyboardEvent("keydown", { key: "Escape", bubbles: true }));
    const closed = button.getAttribute("aria-expanded") === "false" && document.activeElement === button;
    button.click();
    const link = document.querySelector('.portal-sidebar a[href="#/diagnostics/events"]');
    link.focus();
    link.click();
    return opened && closed;
  })()` });
  if (!mobile.result?.value) throw new Error("Mobile sidebar did not move and restore keyboard focus");
  await assertText(cdp, text => text.includes("故障列表"), "Mobile sidebar navigation failed");
  const after = await cdp.send("Runtime.evaluate", { returnByValue: true, expression: `
    document.querySelector(".portal-menu-toggle").getAttribute("aria-expanded") === "false" &&
    document.activeElement.id === "portal-content"
  ` });
  if (!after.result?.value) throw new Error("Mobile navigation did not close the menu and focus the workspace");
  await cdp.send("Emulation.clearDeviceMetricsOverride");
  await cdp.send("Runtime.evaluate", { expression: `(() => {
    window.originalPagedFetch = window.fetch;
    window.fetch = (input, options) => {
      const url = new URL(typeof input === "string" ? input : input.url, location.origin);
      if (url.pathname !== "/api/v1/findings") return window.originalPagedFetch(input, options);
      const items = Array.from({length: 23}, (_, index) => ({
        id: "paged-" + index, cluster: "local", problem: "分页诊断 " + (index + 1),
        severity: "info", createdAt: new Date(Date.UTC(2026, 10, 25 - index)).toISOString()
      }));
      return Promise.resolve(Response.json({items, pagination: {}}));
    };
  })()` });
  await assertPage(cdp, overview + "?source=finding", text => text.includes("显示第 1–5 条") && text.includes("共 23 条"), "Recent diagnostics were not paginated");
  await cdp.send("Runtime.evaluate", { expression: `document.querySelector('[data-overview-page="2"]').click()` });
  await assertDOM(cdp, `document.querySelector(".unified-recent tbody tr")?.textContent.includes("分页诊断 6") &&
    location.hash.includes("recentPage=2") && document.querySelector(".unified-recent a")?.href.includes("recentPage%3D2")`, "Second diagnostics page or return link was incorrect");
  await cdp.send("Runtime.evaluate", { expression: `(() => {
    const select = document.querySelector("[data-overview-limit]");
    select.value = "custom";
    select.dispatchEvent(new Event("change", { bubbles: true }));
    const form = document.querySelector("#overview-custom-limit");
    form.elements.limit.value = "101";
    form.requestSubmit();
  })()` });
  await assertText(cdp, text => text.includes("请输入 1–100 之间的整数"), "Custom diagnostics limit accepted an invalid value");
  await cdp.send("Runtime.evaluate", { expression: `(() => {
    const form = document.querySelector("#overview-custom-limit");
    form.elements.limit.value = "7";
    form.requestSubmit();
  })()` });
  await assertDOM(cdp, `document.querySelectorAll(".unified-recent tbody tr").length === 7 &&
    location.hash.includes("recentPageSize=7") && !location.hash.includes("recentPage=2")`, "Custom limit did not return to the first page");
  await cdp.send("Runtime.evaluate", { expression: "window.fetch = window.originalPagedFetch; delete window.originalPagedFetch" });
}

async function assertRemainingDiagnosticPages(cdp) {
  const origin = "http://127.0.0.1:4173";
  await assertPage(cdp, `${origin}/#/diagnostics/incidents/fixture-p1/rca`, (text) => text.includes("发起分析") &&
    text.includes("运行记录"), "RCA page did not load");
  await cdp.send("Runtime.evaluate", { expression: `(() => {
    const form = document.querySelector("[data-rca-start]");
    form.elements.observation.value = "已核对发布记录";
    form.requestSubmit();
  })()` });
  await assertText(cdp, (text) => text.includes("探针失败需要核查容器状态") && text.includes("已核对发布记录"), "RCA result missing");
  await cdp.send("Runtime.evaluate", { expression: `document.querySelector('[data-rca-task] a[href*="evidence="]').click()` });
  await assertText(cdp, (text) => text.includes("故障时间线") && text.includes("Unhealthy"), "RCA reference did not open the matching evidence");

  await assertPage(cdp, `${origin}/#/diagnostics/incidents/fixture-p1/topology`, (text) => text.includes("资源节点") &&
    text.includes("业务主机 fixture"), "Topology page did not show affected resources");
  await cdp.send("Runtime.evaluate", { expression: `document.querySelector('[data-topology-node="asset:host-fixture"]').click()` });
  await assertText(cdp, (text) => text.includes("部署记录核对") && text.includes("已确认"), "Topology node details did not show link provenance");

  await assertPage(cdp, `${origin}/#/diagnostics/datasources?continue=fixture`, (text) => text.includes("监控与诊断") && text.includes("暂无资产数据源"), "Datasource page did not load");
  const sourceRoute = await cdp.send("Runtime.evaluate", { returnByValue: true, expression: `
    location.hash === "#/resources/datasources?continue=fixture" &&
    document.querySelector(".portal-sidebar [aria-current=page]").textContent === "数据源管理"
  ` });
  if (!sourceRoute.result?.value) throw new Error("Datasource alias lost its cursor or resource navigation");
  await cdp.send("Runtime.evaluate", { expression: `document.querySelector('[data-source-add]').click()` });
  const cloudOptions = await cdp.send("Runtime.evaluate", { returnByValue: true, expression: `
    [...document.querySelectorAll('[data-source-picker] [data-source-kind]')].map(option => option.dataset.sourceKind).join(",") === "aliyun-ecs,tencent-cvm"
  ` });
  if (!cloudOptions.result?.value) throw new Error("The asset creation picker must offer only cloud providers");
  await cdp.send("Runtime.evaluate", { expression: `(() => {
    document.querySelector('[data-source-kind="tencent-cvm"]').click();
    document.querySelector('[data-source-next]').click();
    const form = document.querySelector("[data-datasource-form]");
    form.elements.name.value = "云资产数据源 fixture";
    form.elements.region.value = "ap-guangzhou";
    form.elements.assetIds.value = "10010";
    form.elements.credentialRef.value = "fixture-inventory.json";
    form.elements.clusterId.value = "local";
    form.elements.namespace.value = "default";
    form.requestSubmit();
  })()` });
  await assertText(cdp, (text) => text.includes("云资产数据源 fixture") && text.includes("测试并同步"), "Datasource was not created");
  await cdp.send("Emulation.setDeviceMetricsOverride", { width: 390, height: 720, deviceScaleFactor: 1, mobile: true });
  await assertDOM(cdp, `document.documentElement.scrollWidth <= document.documentElement.clientWidth`, "Datasource rows overflowed at 390 px");
  await cdp.send("Emulation.clearDeviceMetricsOverride");
  await cdp.send("Runtime.evaluate", { expression: `document.querySelector('[data-source-action="run"]').click()` });
  await assertText(cdp, (text) => text.includes("已返回测试与同步结果"), "Datasource health did not update");
  await cdp.send("Runtime.evaluate", { expression: `
    document.querySelector('[data-source-action="detail"]').click();
    document.querySelector("[data-source-inventory]").open = true;` });
  await assertText(cdp, (text) => text.includes("已读取配置资产") && text.includes("已同步主机") && text.includes("fixture-asset"), "Datasource health and inventory were not shown");
  await cdp.send("Runtime.evaluate", { expression: `
    document.querySelector('[data-source-detail] [data-resource-close]').click();
    document.querySelector('[data-source-tab="monitor"]').click();
    document.querySelector('.resource-page-head [data-source-add]').click();
    document.querySelector('[data-source-kind="zabbix"]').click();
    document.querySelector('[data-source-next]').click();` });
  for (const width of [1100, 390]) {
    await cdp.send("Emulation.setDeviceMetricsOverride", { width, height: 800, deviceScaleFactor: 1, mobile: false });
    const layout = await cdp.send("Runtime.evaluate", { returnByValue: true, expression: `(() => {
      const dialog = document.querySelector(".zabbix-dialog");
      const grid = dialog.querySelector(".zabbix-setup-grid");
      const [left, right] = grid.children;
      const panel = dialog.getBoundingClientRect();
      const footer = dialog.querySelector("footer").getBoundingClientRect();
      return dialog.querySelector('[name="method"]:checked').value === "token" &&
        !!dialog.querySelector(".zabbix-scope-wait") && dialog.querySelector("[data-zabbix-save]").disabled &&
        left.contains(dialog.querySelector('[name="environmentId"]')) && left.contains(dialog.querySelector('[name="sourceName"]')) &&
        parseFloat(getComputedStyle(dialog).borderTopLeftRadius) === 24 &&
        panel.left >= 0 && panel.right <= innerWidth && footer.bottom <= innerHeight &&
        (innerWidth > 760 ? left.offsetTop === right.offsetTop : left.offsetTop < right.offsetTop);
    })()` });
    if (!layout.result?.value) throw new Error(`Zabbix onboarding dialog layout is incorrect at ${width} px`);
  }
  await cdp.send("Emulation.clearDeviceMetricsOverride");
  for (const position of ["footer", "header"]) {
    const closed = await cdp.send("Runtime.evaluate", { returnByValue: true, expression: `(() => {
      const dialog = document.querySelector(".zabbix-dialog[open]");
      const form = dialog.querySelector("[data-zabbix-form]");
      form.elements.sourceName.value = "未保存的 Zabbix";
      form.elements.sourceName.dispatchEvent(new Event("input", { bubbles: true }));
      form.elements.secret.value = "unsaved-fixture-token";
      form.elements.secret.dispatchEvent(new Event("input", { bubbles: true }));
      const confirm = window.confirm;
      let prompted = false;
      window.confirm = () => { prompted = true; return false; };
      try {
        dialog.querySelector('${position} [data-zabbix-action="close"]').click();
        return !prompted && !dialog.open;
      } finally {
        window.confirm = confirm;
      }
    })()` });
    if (!closed.result?.value) throw new Error(`Zabbix ${position} close must dismiss a changed form without asking for confirmation`);
    await assertDOM(cdp, `!document.querySelector("[data-zabbix-form]") && !document.body.innerText.includes("未保存的 Zabbix")`,
      "Closing Zabbix did not clear its unsaved draft");
    await cdp.send("Runtime.evaluate", { expression: `
      document.querySelector('.resource-page-head [data-source-add]').click();
      document.querySelector('[data-source-kind="zabbix"]').click();
      document.querySelector('[data-source-next]').click();` });
    await assertDOM(cdp, `(() => {
      const form = document.querySelector(".zabbix-dialog[open] [data-zabbix-form]");
      return !!form && form.elements.sourceName.value === "" && form.elements.secret.value === "";
    })()`, "Reopening Zabbix should start with a blank form");
  }
  const authCards = await cdp.send("Runtime.evaluate", { returnByValue: true, expression: `(() => {
    const form = document.querySelector("[data-zabbix-form]");
    const password = form.querySelector('[name="method"][value="password"]');
    const token = form.querySelector('[name="method"][value="token"]');
    const account = form.querySelector("[data-zabbix-account]");
    form.elements.secret.value = "previous-token";
    password.click();
    const passwordSelected = password.checked && !account.hidden && !form.elements.secret.value &&
      form.elements.secret.placeholder.includes("密码");
    token.click();
    return passwordSelected && token.checked && account.hidden && form.elements.secret.placeholder.includes("Token");
  })()` });
  if (!authCards.result?.value) throw new Error("Zabbix authentication cards do not switch credentials");
  await cdp.send("Runtime.evaluate", { expression: `(() => {
    const form = document.querySelector("[data-zabbix-form]");
    form.elements.endpoint.value = "https://blocked.example";
    form.elements.secret.value = "fixture-zabbix-token";
    form.querySelector('[data-zabbix-action="test"]').click();
  })()` });
  await assertText(cdp, text => text.includes("地址未在平台允许列表中") && text.includes("先测试连接"),
    "Zabbix policy rejection did not explain the issue");

  const environmentBeforeTest = await cdp.send("Runtime.evaluate", { returnByValue: true, awaitPromise: true, expression: `(async () => {
    const form = document.querySelector("[data-zabbix-form]");
    const environment = form.elements.environmentId;
    for (let times = 0; environment.disabled && times < 30; times++) {
      await new Promise(resolve => setTimeout(resolve, 100));
    }
    environment.value = "production";
    environment.dispatchEvent(new Event("change", { bubbles: true }));
    return environment.value === "production" && !form.elements.businessId && form.elements.sourceName.required;
  })()` });
  if (!environmentBeforeTest.result?.value) throw new Error("Zabbix environment must be selectable before the connection test");
  await cdp.send("Runtime.evaluate", { expression: `(() => {
    const form = document.querySelector("[data-zabbix-form]");
    form.elements.endpoint.value = "https://zabbix.example";
    form.elements.secret.value = "fixture-zabbix-token";
    form.querySelector('[data-zabbix-action="test"]').click();
  })()` });
  await assertText(cdp, text => text.includes("连接成功") && text.includes("Windows 应用主机"), "Zabbix connection test did not show groups");
  const scope = await cdp.send("Runtime.evaluate", { returnByValue: true, expression: `(() => {
    const form = document.querySelector("[data-zabbix-form]");
    const environment = form.elements.environmentId;
    const business = form.elements.businessId;
    const initialScope = environment.value === "production" && form.querySelectorAll('[name="environmentId"]').length === 1;
    business.value = "ops";
    environment.value = "testing";
    environment.dispatchEvent(new Event("change", { bubbles: true }));
    const businessReset = business.value === "" && [...business.options].some(option => option.value === "test") &&
      ![...business.options].some(option => option.value === "ops");
    environment.value = "production";
    environment.dispatchEvent(new Event("change", { bubbles: true }));
    business.value = "ops";
    business.dispatchEvent(new Event("change", { bubbles: true }));
    form.querySelector('[name="groupId"]').click();
    return initialScope && businessReset;
  })()` });
  if (!scope.result?.value) throw new Error("Zabbix business options did not follow the selected environment");
  await assertText(cdp, text => text.includes("1 台主机") && text.includes("Windows 内存使用率高"), "Zabbix problem preview was not shown");
  const nameRequired = await cdp.send("Runtime.evaluate", { returnByValue: true, expression: `(() => {
    const form = document.querySelector("[data-zabbix-form]");
    const save = form.querySelector("[data-zabbix-save]");
    const name = form.elements.sourceName;
    name.value = "  ";
    name.dispatchEvent(new Event("input", { bubbles: true }));
    const blocked = save.disabled;
    name.value = "  生产运维 Zabbix  ";
    name.dispatchEvent(new Event("input", { bubbles: true }));
    const enabled = !save.disabled;
    save.click();
    return blocked && enabled;
  })()` });
  if (!nameRequired.result?.value) throw new Error("Zabbix datasource name did not gate saving");
  await assertText(cdp, text => text.includes("生产运维 Zabbix") && text.includes("生产 / 运维") && text.includes("同步正常"), "Zabbix source could not be saved");
  await fetch(`${origin}/test/observability-unavailable?enabled=1`);
  await assertPage(cdp, `${origin}/#/resources/datasources?view=monitor&scenario=partial`,
    text => text.includes("已加载来源") && text.includes("生产运维 Zabbix") && text.includes("重试加载"), "One failed connector excluded healthy sources");
  await fetch(`${origin}/test/observability-unavailable?enabled=0`);
  await cdp.send("Runtime.evaluate", { expression: `document.querySelector('[data-source-retry="observability"]').click()` });
  await assertText(cdp, text => text.includes("生产运维 Zabbix") && !text.includes("已加载来源"), "Failed connector did not recover in place");
  await assertPage(cdp, "about:blank", text => text === "", "Could not unload the datasource page");
  await assertPage(cdp, `${origin}/#/resources/datasources`,
    text => text.includes("生产运维 Zabbix") && text.includes("同步正常"), "Zabbix source name did not survive reloading");
  const groupCollapse = await cdp.send("Runtime.evaluate", { returnByValue: true, expression: `(() => {
    document.querySelector('[data-source-filter="grouped"]').click();
    const toggle = () => document.querySelector('[data-source-group="monitor:zabbix"]');
    toggle().click();
    const closed = toggle().getAttribute("aria-expanded") === "false" && !document.querySelector('[data-source-row="zabbix:zabbix-fixture"]');
    toggle().click();
    document.querySelector('[data-source-action="detail"][data-source-key="zabbix:zabbix-fixture"]').click();
    document.querySelector('[data-source-detail] a').click();
    return closed;
  })()` });
  if (!groupCollapse.result?.value) throw new Error("Zabbix type group did not collapse or reopen");
  await assertText(cdp, text => text.includes("WIN-01") && text.includes("Windows 内存使用率高") && text.includes("最近诊断"),
    "Zabbix problems did not load into the unified overview");
  await cdp.send("Runtime.evaluate", {expression: `document.querySelector("[data-health-record]").click(); document.querySelector("dialog [data-run]").click()`});
  await assertText(cdp, text => text.includes("已核对测试证据"), "Zabbix investigation did not render the scoped response");
  const readable = await cdp.send("Runtime.evaluate", {returnByValue:true,expression:`(() => {
    const dialog=document.querySelector("dialog");
    dialog.querySelector("h3").textContent="kafka_consumer_group_".repeat(100);
    dialog.style.width="320px";
    const content=dialog.querySelector("section");
    content.scrollTop=content.scrollHeight;
    const close=dialog.querySelector("[data-close]");
    const visible=close.getBoundingClientRect().top >= dialog.getBoundingClientRect().top && content.scrollWidth<=content.clientWidth+1;
    close.click();
    return visible && !dialog.open;
  })()`});
  if (!readable.result?.value) throw new Error("Long alert overflowed or close control was inaccessible");
  await cdp.send("Runtime.evaluate", {expression:'document.querySelector("[data-health-record]").click()'});
  await cdp.send("Input.dispatchKeyEvent", {type:"keyDown",key:"Escape",code:"Escape",windowsVirtualKeyCode:27});
  await cdp.send("Input.dispatchKeyEvent", {type:"keyUp",key:"Escape",code:"Escape",windowsVirtualKeyCode:27});
  const closed=await cdp.send("Runtime.evaluate",{returnByValue:true,expression:'!document.querySelector("dialog[open]")'});
  if (!closed.result?.value) throw new Error("Escape did not dismiss alert details");
  for (const days of [7, 14, 30]) {
    const plot = await cdp.send("Runtime.evaluate", { returnByValue: true, expression: `(() => {
      document.querySelector('[data-overview-window="${days}"]').click();
      const grid = document.querySelector(".overview-plot-grid");
      const dates = document.querySelector(".overview-dates");
      const columnCount = node => getComputedStyle(node).gridTemplateColumns.split(" ").length;
      const first = grid.firstElementChild.getBoundingClientRect();
      const last = grid.lastElementChild.getBoundingClientRect();
      const bar = grid.querySelector(".overview-trend-bars .overview-series-zabbix").getBoundingClientRect();
      return {
        columns: columnCount(grid),
        dateColumns: columnCount(dates),
        rowAligned: first.top === last.top && first.left < last.left,
        barHeight: bar.height,
        zabbixCount: document.querySelector("[data-overview-note] .overview-series-zabbix").textContent,
      };
    })()` });
    const layout = plot.result?.value;
    if (layout?.columns !== days || layout.dateColumns !== days || !layout.rowAligned ||
        layout.barHeight <= 0 || layout.zabbixCount !== "Zabbix 1") {
      throw new Error(`Zabbix ${days}-day trend did not render under the Portal CSP: ${JSON.stringify(layout ?? plot.exceptionDetails)}`);
    }
  }

  await assertPage(cdp, `${origin}/#/resources/datasources`, text => text.includes("新增数据源"), "Datasource workspace did not load");
  await cdp.send("Runtime.evaluate", { expression: `
    document.querySelector('.resource-page-head [data-source-add]').click();
    document.querySelector('[data-source-kind="alertmanager"]').click();
    document.querySelector('[data-source-next]').click();` });
  await assertText(cdp, text => text.includes("选择环境") && text.includes("多集群共享"), "Observability scope options were not available");
  await cdp.send("Runtime.evaluate", { expression: `(() => {
    const f = document.querySelector("[data-observability-editor] form");
    const set = (name, value) => { const field = f.elements[name]; field.value = value; field.dispatchEvent(new Event("input",{bubbles:true})); };
    set("environment","testing");
    set("business","test");
    set("cluster","test-k8s");
    set("mapping","shared");
    set("label","cluster");
    set("value","test-a");
    set("name","测试告警中心");
    set("endpoint","https://alertmanager.example/alertmanager");
    set("secret","fixture-alertmanager-token");
    f.querySelector("[data-obs-test]").click();
  })()` });
  await assertText(cdp, text => text.includes("连接成功 · 可预览已选择范围"), "Alertmanager could not test the exact binding");
  await cdp.send("Runtime.evaluate", { expression: 'document.querySelector("[data-obs-preview]").click()' });
  await assertText(cdp, text => text.includes("1 条活动告警"), "Alertmanager preview did not return the activity count");
  await cdp.send("Runtime.evaluate", { expression: 'document.querySelector("[data-obs-save]").click()' });
  await assertText(cdp, text => text.includes("测试告警中心"), "Saved Alertmanager section was not expanded");
  await assertPage(cdp, `${origin}/#/diagnostics?source=alertmanager`, text => text.includes("GatewayErrors") && text.includes("窗口内 1 条"),
    "Alertmanager observations did not reach the unified overview");
  const alertBar = await cdp.send("Runtime.evaluate", { returnByValue: true, expression:
    'document.querySelector(".overview-trend-bars .overview-series-alertmanager").getBoundingClientRect().height' });
  if (!alertBar.result?.value) throw new Error("Alertmanager column did not render under CSP");

  await assertPage(cdp, `${origin}/#/diagnostics/policies`, (text) => text.includes("新增诊断策略") && text.includes("暂无诊断策略"), "Policy page did not load");
  await cdp.send("Runtime.evaluate", { expression: `(() => {
    const form = document.querySelector("[data-policy-form]");
    form.closest("details").open = true;
    form.elements.name.value = "订单服务自动复核";
    form.elements.clusterId.value = "local";
    form.elements.namespace.value = "default";
    form.elements.enabled.checked = true;
    form.requestSubmit();
  })()` });
  await assertText(cdp, (text) => text.includes("订单服务自动复核") && text.includes("自动触发"), "Policy was not created");
  await cdp.send("Runtime.evaluate", { expression: 'document.querySelector("[data-policy-runs]").open = true' });
  await assertText(cdp, (text) => text.includes("succeeded"), "Policy did not show run history");
}

async function assertDiagnosticCenter(cdp) {
  const origin = "http://127.0.0.1:4173";
  await assertPage(cdp, `${origin}/#/findings`, (text) => text.includes("共 3 条诊断结果"), "Legacy list is missing");
  await cdp.send("Runtime.evaluate", { expression: `document.querySelector('.portal-sidebar a[href="#/diagnostics"]').click()` });
  await assertText(cdp, (text) => text.includes("最近诊断") && text.includes("查看故障事件") &&
    text.includes("CrashLoopBackOff detected") && text.includes("测试集群容量建议"),
  "Diagnostic overview did not show the authorized recent Findings");
  await cdp.send("Runtime.evaluate", { expression: `(() => {
    const form = document.querySelector("[data-unified-filters]");
    form.elements.clusterId.value = "test-k8s";
    form.requestSubmit();
  })()` });
  await assertText(cdp, (text) => text.includes("查看故障事件") && text.includes("测试集群容量建议") &&
    !text.includes("CrashLoopBackOff detected"), "Overview did not preserve the selected scope");
  await cdp.send("Runtime.evaluate", { expression: 'document.querySelector(".overview-actions a").click()' });
  await assertText(cdp, (text) => text.includes("共 1 条故障信号") && text.includes("测试集群容量建议"),
    "Overview-to-events link lost the environment filter");

  await cdp.send("Runtime.evaluate", { expression: `
    (() => {
      const input = document.querySelector("#diagnostic-problem");
      input.value = "找不到的故障信号";
      input.form.requestSubmit();
    })()
  ` });
  await assertText(cdp, (text) => text.includes("暂无诊断结果"), "Unmatched search should show the empty state");
  await cdp.send("Runtime.evaluate", { expression: `
    (() => {
      const input = document.querySelector("#diagnostic-problem");
      input.value = "容量";
      input.form.requestSubmit();
    })()
  ` });
  await assertText(cdp, (text) => text.includes("共 1 条故障信号") && text.includes("测试集群容量建议"),
    "Searching the event list did not retain the matching Finding");
  const submitted = await cdp.send("Runtime.evaluate", { expression: "decodeURIComponent(location.hash)", returnByValue: true });
  if (!submitted.result?.value?.includes("problem=容量")) throw new Error("Problem filter is missing from the event route");

  await cdp.send("Runtime.evaluate", { expression: 'document.querySelector(".finding-problem a").click()' });
  await assertText(cdp, (text) => text.includes("故障详情") && text.includes("测试环境容器资源建议") &&
    text.includes("返回故障事件"), "Event details did not resolve the remote instance for the duplicated Finding ID");
  await cdp.send("Runtime.evaluate", { expression: 'document.querySelector(".back-link").click()' });
  await assertText(cdp, (text) => text.includes("共 1 条故障信号") && text.includes("测试集群容量建议"),
    "Event detail did not return to the filtered list");
  const returned = await cdp.send("Runtime.evaluate", {
    expression: `document.querySelector("#diagnostic-problem").value === "容量" && document.querySelector('select[name="environment"]').value === "测试"`,
    returnByValue: true,
  });
  if (!returned.result?.value) throw new Error("Returning from detail discarded the event filters");

  await assertPage(cdp, `${origin}/#/diagnostics/events?continue=2`, (text) => text.includes("共 3 条故障信号，本页 1 条") &&
    text.includes("测试集群容量建议"), "Event page did not use its cursor");
  await cdp.send("Runtime.evaluate", { expression: 'document.querySelector(".finding-problem a").click()' });
  await assertText(cdp, (text) => text.includes("测试环境容器资源建议"), "Paginated Finding opened the wrong detail");
  await cdp.send("Runtime.evaluate", { expression: 'document.querySelector(".back-link").click()' });
  await assertText(cdp, (text) => text.includes("本页 1 条") && text.includes("返回首页"), "Event detail discarded the cursor");

  await assertPage(cdp, `${origin}/#/diagnostics/events?cluster=restricted`, (text) => text.includes("无访问权限") &&
    !text.includes("CrashLoopBackOff detected"), "Forbidden event scope did not fail closed");
}

async function assertRolePermissionSelection(cdp) {
  const readOnly = await cdp.send("Runtime.evaluate", { returnByValue: true, expression: `(() => {
    const readOnly = !document.querySelector("[data-pc-check]");
    const fetchPlatform = window.fetch;
    let failCatalog = true;
    window.roleSaveRequests = 0;
    window.restorePlatformFetch = () => { window.fetch = fetchPlatform; };
    window.fetch = (input, options) => {
      if (input === "/api/v1/admin/permission-catalog" && failCatalog) {
        failCatalog = false;
        return Promise.resolve(new Response(null, { status: 503 }));
      }
      if (input === "/api/v1/admin/access" && options?.method === "POST") window.roleSaveRequests++;
      return fetchPlatform(input, options);
    };
    return readOnly;
  })()` });
  if (!readOnly.result?.value) throw new Error("Read-only catalog offers grant controls");
  await cdp.send("Runtime.evaluate", { expression: `[...document.querySelectorAll(".portal-sidebar a")]
    .find(link => link.textContent.trim() === "权限管理").click()` });
  await assertText(cdp, text => ["角色", "用户组", "授权清单", "重试加载接口权限"].every(label => text.includes(label)),
    "Permission management navigation did not directly open the editable role workspace");
  await cdp.send("Runtime.evaluate", { expression: `(() => {
    document.querySelector('[data-role-picker] [data-system-open="role:"]').click();
  })()` });
  await assertText(cdp, text => text.includes("新建角色") && text.includes("重试加载接口权限"),
    "New role did not open inside the workspace");
  await cdp.send("Runtime.evaluate", { expression: `document.querySelector("dialog[open] [data-role-edit]").elements.name.value = "接口授权检查"` });
  await assertText(cdp, text => text.includes("重试加载接口权限"), "Catalog failure removed the role editor recovery entry");
  await cdp.send("Runtime.evaluate", { expression: `document.querySelector("[data-role-catalog-retry]").click()` });
  await assertText(cdp, text => text.includes("批量勾选当前结果") && text.includes("选择接口权限"), "Retry did not restore permission controls");

  await cdp.send("Emulation.setDeviceMetricsOverride", { width: 390, height: 760, deviceScaleFactor: 1, mobile: false });
  const position = await cdp.send("Runtime.evaluate", { returnByValue: true, expression: `(() => {
    const form = document.querySelector("dialog[open] form");
    const check = form.querySelector('[data-pc-check="diagnosis.finding.list"]');
    const action = form.querySelector('[data-pc-point="diagnosis.finding.list"]');
    const matrix = check.closest(".permission-catalog-table");
    if (form.elements.name.value !== "接口授权检查" || matrix.scrollWidth > matrix.clientWidth ||
        !form.closest("[data-role-workspace]") || form.closest("dialog").matches(":modal") ||
        document.documentElement.scrollWidth > 390) return null;
    check.scrollIntoView({ block: "center", inline: "center" });
    const checkBounds = check.getBoundingClientRect();
    const actionBounds = action.getBoundingClientRect();
    const x = checkBounds.x + checkBounds.width / 2;
    const y = checkBounds.y + checkBounds.height / 2;
    return checkBounds.right <= actionBounds.right && document.elementFromPoint(x, y) === check ? { x, y } : null;
  })()` });
  if (!position.result?.value) throw new Error("Narrow-screen role checkbox was clipped, displaced or lost on catalog retry");
  for (const type of ["mousePressed", "mouseReleased"]) {
    await cdp.send("Input.dispatchMouseEvent", { type, ...position.result.value, button: "left", clickCount: 1 });
  }
  for (const expected of [false, true]) {
    await cdp.send("Input.dispatchKeyEvent", { type: "keyDown", key: " ", code: "Space", windowsVirtualKeyCode: 32 });
    await cdp.send("Input.dispatchKeyEvent", { type: "keyUp", key: " ", code: "Space", windowsVirtualKeyCode: 32 });
    const selection = await cdp.send("Runtime.evaluate", { returnByValue: true,
      expression: `document.querySelector('[data-pc-check="diagnosis.finding.list"]').checked` });
    if (selection.result?.value !== expected) throw new Error("Focused permission checkbox did not toggle with Space");
  }
  await cdp.send("Emulation.clearDeviceMetricsOverride");
  const operation = await cdp.send("Runtime.evaluate", { returnByValue: true, expression: `(() => {
    const form = document.querySelector("dialog[open] form");
    const find = '[data-pc-check="diagnosis.finding.list"]';
    const summary = '[data-pc-check="diagnosis.finding.summary"]';
    const toolkit = '[data-pc-toolkit-toggle="诊断控制台 › 诊断工作区"]';
    const filter = value => {
      const query = form.querySelector("[data-pc-query]");
      query.value = value;
      query.dispatchEvent(new Event("input", { bubbles: true }));
    };
    const module = id => form.querySelector('[data-pc-module="'+id+'"]').click();

    if (document.activeElement !== form.querySelector(find) || !form.querySelector(find).checked) return "native checkbox focus";
    form.querySelector('[data-pc-point="diagnosis.finding.list"]').click();
    if (!form.querySelector(find).checked || !form.querySelector("[data-pc-all]").indeterminate) return "independent detail and checkbox";
    filter("diagnosis.finding.summary");
    form.querySelector(toolkit).click();
    if (!form.querySelector(summary).checked || !form.querySelector("[data-pc-all]").checked || form.querySelector(find)) return "filtered toolkit selection";
    form.querySelector("[data-pc-all]").click();
    if (form.querySelector(summary).checked || !form.querySelector("[data-pc-selected]").textContent.includes("总待添加 1 项")) return "filtered bulk deselection";
    filter("no-such-permission");
    if (!form.querySelector("[data-pc-all]").disabled) return "empty bulk selection";
    filter("diagnosis.finding.summary");
    form.querySelector("[data-pc-all]").click();
    filter("");
    module("system");
    if (!form.querySelector('[data-pc-check="system.retention.update"]').disabled) return "restricted action";
    filter("system.audit.list");
    form.querySelector("[data-pc-all]").click();
    const kind = form.querySelector("[data-role-scope-kind] select");
    if (kind.disabled || kind.value !== "") return "mixed-scope confirmation";
    form.querySelector("[data-role-add-grants]").click();
    if (!form.querySelector("[data-role-grant-status]").textContent.includes("请选择本次添加的范围类型")) return "missing-scope validation";
    kind.value = "资源范围";
    form.elements.environment.value = "testing";
    form.elements.environment.dispatchEvent(new Event("change", { bubbles: true }));
    form.querySelector("[data-role-add-grants]").click();
    if (form.querySelectorAll("[data-role-grant]").length !== 2 ||
        !form.querySelector('[data-pc-check="system.audit.list"]').checked) return "selected-scope batch lost hidden or remaining permissions";
    form.querySelector('button[value="role-preview"]').click();
    if (form.querySelector("[data-role-preview]").hidden === false ||
        !form.querySelector(".system-editor-status").textContent.includes("加入授权清单")) return "pending permissions bypassed preview guard";
    form.querySelector("[data-role-add-grants]").click();
    if (form.querySelectorAll("[data-role-grant]").length !== 3 || window.roleSaveRequests !== 0) return "draft authorization was saved early";
    filter("");
    module("diagnosis");
    form.querySelector('button[value="role-preview"]').click();
    if (form.querySelector("[data-role-preview]").hidden || !form.closest("dialog").matches(":modal")) return "role preview";
    form.querySelector("[data-role-back]").click();
    if (window.roleSaveRequests !== 0 || form.closest("dialog").matches(":modal") ||
        form.querySelectorAll("[data-role-grant]").length !== 3) return "return to role draft";
    form.querySelector('button[value="role-preview"]').click();
    return "ok";
  })()` });
  if (operation.result?.value !== "ok") {
    throw new Error(`Role permission selection failed: ${operation.result?.value ?? operation.exceptionDetails?.exception?.description}`);
  }
  await cdp.send("Input.dispatchKeyEvent", { type: "keyDown", key: "Escape", code: "Escape", windowsVirtualKeyCode: 27 });
  await cdp.send("Input.dispatchKeyEvent", { type: "keyUp", key: "Escape", code: "Escape", windowsVirtualKeyCode: 27 });
  const canceled = await cdp.send("Runtime.evaluate", { returnByValue: true, expression: `(() => {
    const form = document.querySelector("[data-role-workspace] dialog[open]:not(:modal) [data-role-edit]");
    if (!form || window.roleSaveRequests !== 0 || form.querySelectorAll("[data-role-grant]").length !== 3) return false;
    form.querySelector('button[value="role-preview"]').click();
    return true;
  })()` });
  if (!canceled.result?.value) throw new Error("Escape did not close review and retain the role draft");
  await assertText(cdp, text => ["新增接口授权", "查询审计列表", "查询诊断列表", "读取诊断汇总"].every(label => text.includes(label)),
    "Scoped interface grants missing from role preview");
  await cdp.send("Runtime.evaluate", { expression: `document.querySelector('dialog[open] button[value="role-save"]').click()` });
  await assertText(cdp, text => text.includes("角色保存结果") && text.includes("查看本次审计") && text.includes("前往用户管理"),
    "Saving interface grants did not show the audit or role assignment shortcut");
  const writes = await cdp.send("Runtime.evaluate", { returnByValue: true, expression: `(() => {
    window.restorePlatformFetch();
    return window.roleSaveRequests;
  })()` });
  if (writes.result?.value !== 1) throw new Error("Role confirmation did not send exactly one save request");
}

async function assertPortalFollowupPrototypes(cdp) {
  const portalPrototype = new URL("../../docx/portal-ui/shell-regression.html", import.meta.url);
  await assertPage(cdp, `${portalPrototype.href}#/diagnostics`,
    text => text.includes("全部可见范围") && text.includes("最近故障事件"), "Prototype did not show the on-call dashboard");
  await cdp.send("Emulation.setDeviceMetricsOverride", { width: 390, height: 900, deviceScaleFactor: 1, mobile: false });
  const table = await cdp.send("Runtime.evaluate", { returnByValue: true, expression: `(() => {
    const region = document.querySelector(".incident-results .scroll");
    return region.scrollWidth > region.clientWidth && region.querySelector("td").getBoundingClientRect().width >= 160;
  })()` });
  if (!table.result?.value) throw new Error("Prototype fault titles were compressed on a narrow screen");
  await cdp.send("Emulation.clearDeviceMetricsOverride");
  await cdp.send("Runtime.evaluate", { expression: `(() => {
    document.querySelector(".scope-bar summary").click();
    const form = document.querySelector("#incident-filter");
    form.elements.environmentId.value = "production";
    form.requestSubmit();
  })()` });
  await assertText(cdp, text => text.includes("环境 生产") && text.includes("支付接口 5xx 错误率上升") &&
    !text.includes("日志采集延迟"), "Prototype incident filter did not apply consistently");
  await cdp.send("Runtime.evaluate", { expression: `document.querySelector('.metric a[href*="severity=P1"]').click()` });
  await assertText(cdp, text => text.includes("故障列表") && text.includes("严重程度 P1 高风险") &&
    text.includes("共 2 条故障"), "Prototype lost the current scope when opening an operating queue");
  await assertPage(cdp, `${portalPrototype.href}#/resources/clusters`,
    text => text.includes("Kubernetes 集群管理") && text.includes("集群列表"), "Global prototype did not show the renamed cluster page");
  const labels = await cdp.send("Runtime.evaluate", { returnByValue: true, expression: `[
    document.querySelector('#sidebar [aria-current="page"]')?.textContent,
    document.querySelector("main h1")?.textContent,
    document.querySelector("[data-filter-region] h2")?.textContent,
  ]` });
  if (!labels.result?.value?.every(label => label === "Kubernetes 集群管理")) throw new Error("Global cluster prototype labels are inconsistent");
  const scopes = await cdp.send("Runtime.evaluate", { returnByValue: true, expression: `(() => {
    const counts = {研发: 1, 测试: 1, 预发布: 1, 生产: 2};
    return Object.entries(counts).every(([name, count]) => {
      const scope = document.querySelector('summary[data-scope="' + name + '"]');
      if (!scope) return false;
      scope.click();
      const rows = [...document.querySelectorAll("#resource-table [data-row]:not([hidden])")];
      return rows.length === count && rows.every(row => row.cells[1].textContent === name) &&
        scope.parentElement.textContent.includes("全部" + name + "集群") === false;
    });
  })()` });
  if (!scopes.result?.value) throw new Error("Global prototype did not filter all environments from their headings");

  await assertPage(cdp, `${portalPrototype.href}#/admin/access`,
    text => text.includes("权限配置") && text.includes("角色列表"), "Global prototype lost the permission workspace");
  const catalog = await cdp.send("Runtime.evaluate", { returnByValue: true,
    expression: `document.querySelector('.permission-links a[href="../system-management/permission-catalog.html"]')?.textContent` });
  if (catalog.result?.value !== "权限配置") throw new Error("Global prototype did not rename the permission action");
  const frame = await cdp.send("Runtime.evaluate", { returnByValue: true,
    expression: `document.querySelector('iframe[title="角色配置与用户组"]')?.getAttribute("src")` });
  if (frame.result?.value !== "../system-management/group-admin-prototype.html?embed") {
    throw new Error("Global prototype is not using the PR #111 permission workflow");
  }

  await assertPage(cdp, `${portalPrototype.href}#/admin/ldap`,
    text => text.includes("LDAP 连接配置"), "Global LDAP prototype did not render");
  const ldap = await cdp.send("Runtime.evaluate", { returnByValue: true, expression: `(() => {
    const details = document.querySelector(".ldap");
    if (details.open) return false;
    details.querySelector("summary").click();
    const form = details.querySelector("form");
    const save = form.querySelector('[value="save"]');
    const test = form.querySelector('[value="test"]');
    if (!save.disabled || form.querySelectorAll("[data-ldap-step]").length !== 4) return false;
    form.elements.testUsername.value = "alice";
    form.elements.testPassword.value = "fixture-password";
    test.click();
    if (save.disabled || form.elements.testPassword.value !== "") return false;
    form.elements.url.value = "ldap://directory.example.test:389";
    form.elements.url.dispatchEvent(new Event("input", { bubbles: true }));
    if (!save.disabled) return false;
    const checkbox = form.elements.allowPlain;
    const copy = checkbox.nextElementSibling;
    const box = checkbox.getBoundingClientRect();
    const text = copy.getBoundingClientRect();
    if (box.height !== 14 || box.right >= text.left ||
      Math.abs(box.top + box.height / 2 - text.top - text.height / 2) > 1) return false;
    copy.click();
    if (!checkbox.checked) return false;
    form.elements.testUsername.value = "alice";
    form.elements.testPassword.value = "fixture-password";
    test.click();
    save.click();
    return save.disabled && form.querySelector("[data-ldap-save-message]").textContent.includes("已保存");
  })()` });
  if (!ldap.result?.value) throw new Error("Global LDAP prototype did not require verification before saving");

  const resource = new URL("../../docx/resource-management/cluster-onboarding.html", import.meta.url);
  await assertPage(cdp, resource.href, text => text.includes("Kubernetes 集群管理") && text.includes("采集状态"),
    "Resource-management prototype lost the renamed cluster page");
  const system = new URL("../../docx/system-management/admin-workspace.html#ldap", import.meta.url);
  await assertPage(cdp, system.href, text => text.includes("LDAP 连接配置"), "System-management LDAP prototype did not render");
  await cdp.send("Runtime.evaluate", { expression: `document.querySelector("[data-ldap-section] summary").click()` });
  await assertText(cdp, text => ["LDAP URL", "用户与组搜索设置", "测试员工认证", "保存并启用"].every(label => text.includes(label)),
    "System-management LDAP prototype did not expand to the redesigned form");
}

async function assertPermissionPrototype(cdp) {
  const directory = new URL("../../docx/system-management/permission-catalog.html", import.meta.url);
  await assertPage(cdp, directory.href, text => text.includes("编辑示例角色") && text.includes("权限编码"),
    "Permission catalog HTML prototype did not initialize");
  await cdp.send("Runtime.evaluate", { expression: `document.querySelector('a[href="group-admin-prototype.html"]').click()` });
  await assertText(cdp, text => text.includes("批量勾选当前结果") && text.includes("添加到授权清单"),
    "Catalog prototype link did not open the actionable role prototype");
  const result = await cdp.send("Runtime.evaluate", { returnByValue: true, expression: `(() => {
    document.querySelector('[data-point="diagnosis.finding.summary"]').click();
    document.querySelector('[data-module="system"]').click();
    document.querySelector('[data-point="system.audit.list"]').click();
    const kind = document.getElementById("grantKind");
    kind.value = "资源范围";
    kind.dispatchEvent(new Event("change", { bubbles: true }));
    document.getElementById("addGrants").click();
    if (!document.querySelector('[data-point="system.audit.list"]').checked ||
        !document.getElementById("grants").textContent.includes("读取诊断汇总")) return false;
    document.getElementById("addGrants").click();
    document.getElementById("saveRole").click();
    if (!document.getElementById("confirmDialog").open) return false;
    document.getElementById("confirmSave").click();
    return !document.getElementById("grants").textContent.includes("待保存");
  })()` });
  if (!result.result?.value) throw new Error("HTML role prototype did not preserve pending permissions and confirm both grants");
}

async function assertPage(cdp, url, predicate, failureMessage) {
  await cdp.send("Page.navigate", { url });
  await assertText(cdp, predicate, failureMessage);
}

async function assertText(cdp, predicate, failureMessage) {
  let latestText = "";
  for (let attempt = 0; attempt < 80; attempt += 1) {
    const response = await cdp.send("Runtime.evaluate", {
      expression: "document.body?.innerText ?? ''",
      returnByValue: true,
    });
    latestText = response.result?.value ?? "";
    if (predicate(latestText)) return;
    await sleep(100);
  }
  throw new Error(`${failureMessage}. Final page text:\n${latestText}`);
}

async function assertDOM(cdp, expression, failureMessage) {
  for (let attempt = 0; attempt < 80; attempt += 1) {
    const response = await cdp.send("Runtime.evaluate", { expression, returnByValue: true });
    if (response.result?.value === true) return;
    await sleep(100);
  }
  throw new Error(failureMessage);
}

function sleep(milliseconds) {
  return new Promise((resolve) => setTimeout(resolve, milliseconds));
}
