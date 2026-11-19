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
      text.includes("关联证据") && text.includes("ContainerBackOff") &&
      text.includes("out_of_memory") && text.includes("部分可用") &&
      text.includes("不可用") && text.includes("已禁用"),
    "Finding Detail did not render grouped correlation evidence and source states");

    const evidenceEvaluation = await cdp.send("Runtime.evaluate", {
      expression: `({
        probe: Boolean(document.querySelector("#evidence-markup-probe")),
        text: document.querySelector(".correlation-panel")?.innerText ?? "",
        events: Array.from(document.querySelectorAll('[aria-label="Kubernetes 事件"] .evidence-signal-heading strong'), node => node.textContent)
      })`,
      returnByValue: true,
    });
    const evidence = evidenceEvaluation.result?.value ?? {};
    if (evidence.probe || !evidence.text?.includes('<em id="evidence-markup-probe">') ||
      evidence.events?.join(",") !== "ContainerBackOff,Started") {
      throw new Error(`Unexpected evidence markup or ranking: ${JSON.stringify(evidence)}`);
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
      text.includes("此时间窗口内暂无证据。") && text.includes("Available replicas are below desired replicas.") &&
      !text.includes("重新加载证据"), "Evidence retry did not render the empty result");

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
      cdp, "http://127.0.0.1:4173/#/findings",
      (text) => text.includes("需要身份认证") && text.includes("LDAP / Active Directory"),
      "Anonymous session did not show the login form",
    );
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
      (text) => text.includes("诊断说明") && text.includes("AI 分析"), "AI review entry did not render");
    await cdp.send("Runtime.evaluate", {
      expression: 'document.querySelector("[data-open-review]").click()',
    });
    await assertText(cdp, (text) => text.includes("你的消息") && !text.includes("正在加载复核记录"),
      "AI review dialog did not load");
    await assertText(cdp, (text) => text.includes("正在结合 Wiki 生成增强说明"), "Enhancement did not enter its waiting state");
    await cdp.send("Runtime.evaluate", {expression: 'document.querySelector("[data-close-review]").click()'});
    await assertText(cdp, (text) => !text.includes("你的消息"), "AI review did not close while enhancement was pending");
    await cdp.send("Runtime.evaluate", {expression: 'document.querySelector("[data-open-review]").click()'});
    await assertText(cdp, (text) => text.includes("增强诊断案例") && !text.includes("正在结合 Wiki"),
      "AI review did not recover after reopening");
    for (const [message, reply] of [
      ["Pod 事件显示镜像仓库拒绝访问", "请补充镜像仓库返回的状态码。"],
      ["仓库返回 403", "403 支持访问被拒绝，请核实拉取凭据。"],
    ]) {
      await cdp.send("Runtime.evaluate", {
        expression: `(() => {
          const form = document.querySelector(".finding-review-form");
          form.elements.message.value = ${JSON.stringify(message)};
          form.requestSubmit();
        })()`,
      });
      await assertText(cdp, (text) => text.includes(message) && text.includes(reply) && !text.includes("更新结论：镜像仓库拒绝访问"),
        "AI review did not display the conversation without updating the diagnosis");
    }
    await cdp.send("Runtime.evaluate", {
      expression: 'document.querySelector("[data-finalize-review]").click()',
    });
    await assertText(cdp, (text) => text.includes("结论草稿") && text.includes("核对草稿后可确认"), "Draft was not shown");
    await cdp.send("Runtime.evaluate", {expression: `document.querySelector("[data-confirm-review]").click()`});
    await assertText(cdp, (text) => text.includes("AI 复核结论") && text.includes("更新结论：镜像仓库拒绝访问"),
      "AI review did not update the diagnostic explanation");
    await cdp.send("Runtime.evaluate", {
      expression: `document.querySelector('a[href="#/wiki"]').click()`,
    });
    await assertText(cdp, (text) => text.includes("运维知识库") && text.includes("镜像拉取排障") && text.includes("新增知识"),
      "Wiki navigation did not load the knowledge list");
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
    await cdp.send("Runtime.evaluate", {expression: 'document.querySelector("[data-close-wiki-ask]").click()'});
    await cdp.send("Runtime.evaluate", {
      expression: `document.querySelector('a[href^="#/wiki/"]').click()`,
    });
    await assertText(cdp, (text) => text.includes("验证仓库权限"), "Wiki detail did not render");
    await assertPage(cdp, "http://127.0.0.1:4173/#/resources",
      (text) => text.includes("资源管理") && text.includes("集群接入") && text.includes("环境管理"), "Resource navigation did not default to cluster onboarding");
    await assertText(cdp, (text) => text.includes("已接入 · 集群异常") && text.includes("新增集群"), "Cluster table did not render the persisted node status");
    await cdp.send("Runtime.evaluate", { expression: 'document.querySelector("[data-cluster-details]").click()' });
    await assertText(cdp, (text) => text.includes("test-worker · NotReady") && text.includes("dev-app"), "Collection details omitted nodes or namespaces");
    await cdp.send("Runtime.evaluate", { expression: 'document.querySelector("dialog[open] [data-cluster-close]").click()' });
    await cdp.send("Runtime.evaluate", { expression: `document.querySelector('[data-cluster-action="recheck"][data-id="local"]')
      .closest("tr").querySelector("[data-cluster-details]").click()` });
    await cdp.send("Runtime.evaluate", { expression: 'document.querySelector(\'dialog[open] [data-collection-check="local"]\').requestSubmit()' });
    await assertText(cdp, (text) => text.includes("本地可恢复集群") && text.includes("待接入") && !text.includes("尚未收到有效采集数据"),
      "Local cluster could not refresh collection from details");
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
    await assertText(cdp, (text) => !text.includes("保存归属") && text.includes("集群接入"), "Namespace ownership did not save and close");
    await cdp.send("Emulation.clearDeviceMetricsOverride");
    await cdp.send("Runtime.evaluate", { expression: `(() => {
      const search = document.querySelector('[data-cluster-filter="query"]');
      search.value = "本地可恢复集群";
      search.dispatchEvent(new Event("input", { bubbles: true }));
    })()` });
    await assertText(cdp, (text) => text.includes("共 1 个集群") && !text.includes("dev-k8s"), "Cluster search did not filter the directory");
    await cdp.send("Runtime.evaluate", { expression: `document.querySelector('[data-cluster-action="delete"][data-id="local"]').click()` });
    await assertText(cdp, (text) => text.includes("删除集群连接") && text.includes("清理 kube-aiops"), "Cluster deletion confirmation did not render");
    await cdp.send("Runtime.evaluate", { expression: `document.querySelector('dialog[open] [data-cluster-close]').click()` });
    await assertText(cdp, (text) => text.includes("本地可恢复集群") && !text.includes("已删除"), "Canceling deletion changed the local connection");
    await cdp.send("Runtime.evaluate", { expression: `(() => {
      document.querySelector('[data-cluster-action="delete"][data-id="local"]').click();
      document.querySelector('dialog[open] button[type="submit"]').click();
    })()` });
    await assertText(cdp, (text) => text.includes("已删除") && text.includes("重新连接"), "Deleted local connection did not expose reconnect");
    await cdp.send("Runtime.evaluate", { expression: `(() => {
      document.querySelector('[data-cluster-action="reconnect"][data-id="local"]').click();
      document.querySelector('dialog[open] button[type="submit"]').click();
    })()` });
    await assertText(cdp, (text) => text.includes("本地可恢复集群") && !text.includes("重新连接"), "Reconnect did not restore the local row");
    await cdp.send("Runtime.evaluate", { expression: `document.querySelector('[data-cluster-editor="edit-dev-k8s"]').click()` });
    await assertText(cdp, (text) => text.includes("环境已锁定") && text.includes("取消"), "Cluster editor did not display the locked environment");
    const lockedEnvironment = await cdp.send("Runtime.evaluate", {
      expression: 'document.querySelector("dialog[open] [data-onboarding-mode] input[name=environment]").readOnly', returnByValue: true,
    });
    if (!lockedEnvironment.result?.value) throw new Error("A collected cluster must have a read-only environment");
    await assertPage(cdp, "http://127.0.0.1:4173/#/resources/environments",
      (text) => text.includes("环境管理") && text.includes("新增环境"), "Environment catalog did not render");
    await cdp.send("Runtime.evaluate", { expression: `document.querySelector('[data-catalog-action="add"]').click()` });
    const idPattern = await cdp.send("Runtime.evaluate", { returnByValue: true, expression: `(() => {
      const id = document.querySelector('dialog[open] input[name="id"]');
      id.value = "new-dev";
      const valid = id.checkValidity();
      id.value = "INVALID";
      return valid && !id.checkValidity();
    })()` });
    if (!idPattern.result?.value) throw new Error("New catalog IDs must follow the browser ID pattern");
    await cdp.send("Runtime.evaluate", { expression: `document.querySelector('dialog[open] [data-catalog-action="close"]').click()` });
    await cdp.send("Runtime.evaluate", { expression: `document.querySelector('[data-catalog-action="edit"]').click()` });
    const writable = await cdp.send("Runtime.evaluate", {
      expression: 'document.querySelector("[data-catalog-dialog] input[name=id]").readOnly', returnByValue: true,
    });
    if (writable.result?.value !== false) throw new Error("Existing catalog ID must be editable");
    await cdp.send("Runtime.evaluate", { expression: `(() => {
      const form = document.querySelector("[data-resource-catalog]");
      form.elements.name.value = "研发环境";
      form.elements.id.value = "new-dev";
      form.requestSubmit();
    })()` });
    await assertText(cdp, (text) => text.includes("研发环境") && text.includes("new-dev") && !text.includes("保存"), "Catalog rename did not close the editor");
    await cdp.send("Runtime.evaluate", { expression: `document.querySelector('[data-catalog-action="delete"]').click()` });
    await cdp.send("Runtime.evaluate", { expression: 'document.querySelector("[data-resource-catalog]").requestSubmit()' });
    await assertText(cdp, (text) => text.includes("dev-k8s"), "Referenced catalog deletion did not explain the conflict");
    await cdp.send("Runtime.evaluate", { expression: `document.querySelector('[data-catalog-action="close"]').click()` });
    await cdp.send("Runtime.evaluate", { expression: `document.querySelector('[data-catalog-action="add"]').click()` });
    await cdp.send("Runtime.evaluate", { expression: `(() => {
      const form = document.querySelector("[data-resource-catalog]");
      form.elements.id.value = "sandbox";
      form.elements.name.value = "沙盒";
      form.requestSubmit();
    })()` });
    await assertText(cdp, (text) => text.includes("沙盒") && text.includes("sandbox"), "Catalog creation did not preserve the entered ID");
    await cdp.send("Runtime.evaluate", { expression: `document.querySelector('[data-catalog-action="delete"][data-entry-id="sandbox"]').click()` });
    await cdp.send("Runtime.evaluate", { expression: 'document.querySelector("[data-resource-catalog]").requestSubmit()' });
    await assertText(cdp, (text) => !text.includes("sandbox"), "Unreferenced catalog deletion failed");
    await assertPage(cdp, "http://127.0.0.1:4173/#/resources/business-lines",
      (text) => text.includes("业务线 ID") && text.includes("云帆") && text.includes("新增业务线"), "Business catalog did not match the shared layout");
    await cdp.send("Runtime.evaluate", { expression: `(() => {
      document.querySelector('[data-catalog-action="edit"][data-entry-id="yunfan"]').click();
      const form = document.querySelector("[data-resource-catalog]");
      form.elements.id.value = "yunfan-app";
      form.requestSubmit();
    })()` });
    await assertText(cdp, (text) => text.includes("yunfan-app"), "Business ID was not saved");
    const nav = await cdp.send("Runtime.evaluate", { expression: `(() => {
      const links = [...document.querySelectorAll(".workspace-nav > a")];
      const current = links.find((link) => link.getAttribute("aria-current") === "page");
      return {
        labels: links.map((link) => link.textContent).join("/"),
        indents: [...new Set(links.map((link) => getComputedStyle(link).paddingInlineStart))].length,
        current: current?.textContent,
        selectedBackground: current && getComputedStyle(current).backgroundColor,
        inactiveBackground: getComputedStyle(links[0]).backgroundColor,
      };
    })()`, returnByValue: true });
    const navigation = nav.result?.value;
    if (navigation?.labels !== "诊断控制台/运维知识库/资源管理/系统管理" || navigation.indents !== 1) {
      throw new Error("The four modules must be peers in the workspace navigation");
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
    await cdp.send("Runtime.evaluate", { expression: 'document.querySelector("dialog[open] footer [data-system-close]").click()' });
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
    await cdp.send("Runtime.evaluate", {
      awaitPromise: true, expression: `fetch("/test/admin-session").then(() => { location.hash = "#/admin/password"; })`,
    });
    await assertText(cdp, (text) => text.includes("为当前本地账号设置新密码"), "Local administrator lost the password route");
    await assertPage(cdp, "http://127.0.0.1:4173/#/admin/users",
      (text) => text.includes("全局管理与诊断查看") && text.includes("创建系统管理员"),
      "Super administrator session did not restore account maintenance");
    await cdp.send("Runtime.evaluate", { expression: `(() => {
      document.querySelector('[data-system-tab="bindings"]').click();
      document.querySelector('[data-system-open="assign"]').click();
      const form = document.querySelector("dialog[open] form");
      const kind = form.querySelector("[data-assignment-kind]");
      kind.value = "roles";
      kind.dispatchEvent(new Event("change", { bubbles: true }));
      form.querySelector("[data-assignment-mode]").value = "remove";
      for (const id of ${JSON.stringify(assignmentRoleIds)}) form.querySelector('input[value="' + id + '"]').checked = true;
      form.requestSubmit(form.querySelector('button[value="assignment"]'));
    })()` });
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
    await assertText(cdp, (text) => text.includes("暂无符合条件的账号") && text.includes("已选 0 项"), "Filtering should clear hidden selections");
    await assertPage(cdp, "http://127.0.0.1:4173/#/admin/users",
      (text) => text.includes("生效权限及来源") && text.includes("用户组 运维") && text.includes("直接授予"), "User detail omitted permission origins");
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
      (text) => text.includes("LDAP 连接配置") && text.includes("测试待启用配置"), "LDAP editor panels did not render");
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
    await assertText(cdp, (text) => text.includes("已保存") && text.includes("配置已启用"), "LDAP save did not show confirmation in its own panel");
    await assertPage(cdp, "http://127.0.0.1:4173/#/admin/access/groups",
      (text) => text.includes("新建用户组") && text.includes("运维"), "User group tab did not render");
    await cdp.send("Runtime.evaluate", { expression: `document.querySelector('[data-system-open="group:ops"]').click()` });
    const rolePreselected = await cdp.send("Runtime.evaluate", { returnByValue: true,
      expression: `document.querySelector('dialog[open] input[name="roles"]').checked` });
    if (!rolePreselected.result?.value) throw new Error("Group editor did not preload existing role associations");
    await cdp.send("Runtime.evaluate", { expression: `document.querySelector('dialog[open] button[value="group-save"]').click()` });
    await assertText(cdp, (text) => !text.includes("保存用户组") && text.includes("新建用户组"), "Group save did not close the editor");
    await assertPage(cdp, "http://127.0.0.1:4173/#/admin/access/roles",
      (text) => text.includes("新建角色") && text.includes("访客只读"), "Role tab did not render");
    await cdp.send("Runtime.evaluate", { expression: `(() => {
      document.querySelector('[data-system-open="role:"]').click();
      const form = document.querySelector("dialog[open] form");
      form.elements.name.value = "浏览器检查";
      const cluster = form.querySelector('[name="cluster"]');
      cluster.value = "local";
      cluster.dispatchEvent(new Event("change", { bubbles: true }));
    })()` });
    for (let attempt = 0; attempt < 40; attempt += 1) {
      const loaded = await cdp.send("Runtime.evaluate", { returnByValue: true,
        expression: `Boolean(document.querySelector('dialog[open] select[name="namespace"] option[value="payments"]'))` });
      if (loaded.result?.value) break;
      if (attempt === 39) throw new Error("Role editor did not populate namespaces");
      await sleep(100);
    }
    await cdp.send("Runtime.evaluate", { expression: `document.querySelector('dialog[open] button[value="role-save"]').click()` });
    await assertText(cdp, (text) => text.includes("请添加至少一项授权范围"), "Saving used an uncommitted scope");
    await cdp.send("Runtime.evaluate", { expression: `(() => {
      const form = document.querySelector("dialog[open] form");
      form.elements.namespace.value = "payments";
      form.querySelector("[data-scope-add]").click();
      form.requestSubmit(form.querySelector('button[value="role-save"]'));
    })()` });
    await assertText(cdp, (text) => text.includes("角色名称已使用") && text.includes("保存角色"), "Role editor did not keep the form open on conflict");
    await cdp.send("Runtime.evaluate", { expression: 'document.querySelector("dialog[open] footer [data-system-close]").click()' });
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
    await assertText(cdp, (text) => text.includes("当前页 1 条记录") && text.includes("批量加入用户组") && !text.includes("local:guest"),
      "Audit account search did not narrow the list before opening the batch");
    await cdp.send("Runtime.evaluate", { expression: 'document.querySelector(\'[data-audit-detail="3"]\').click()' });
    await assertText(cdp, (text) => text.includes("批次结果") && text.includes("ldap:alice") && text.includes("查看同批次"), "Audit batch detail did not render per-target outcomes");
    await cdp.send("Runtime.evaluate", { expression: 'document.querySelector("[data-audit-show-batch]").click()' });
    await assertText(cdp, (text) => text.includes("清除批次") && text.includes("ldap:alice") && !text.includes("local:guest"), "Batch filter did not exclude unrelated records");
    await cdp.send("Runtime.evaluate", { expression: 'document.querySelector("[data-audit-next]").click()' });
    await assertText(cdp, (text) => text.includes("ldap:bob") && text.includes("上一页") && !text.includes("ldap:alice"), "Audit cursor did not advance within the batch");
    await cdp.send("Runtime.evaluate", { expression: 'document.querySelector(\'[data-audit-detail="1"]\').click()' });
    await assertText(cdp, (text) => text.includes("失败原因") && text.includes("账号已停用"), "Audit details omitted a per-target failure reason");
    await cdp.send("Runtime.evaluate", { expression: 'document.querySelector("[data-audit-close]").click(); document.querySelector("[data-audit-previous]").click();' });
    await assertText(cdp, (text) => text.includes("ldap:alice") && !text.includes("ldap:bob"), "Audit cursor did not return to the first batch page");
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
      (text) => text.includes("集群接入") && text.includes("采集上报"), "Legacy cluster route did not lead to resource management");
    await cdp.send("Runtime.evaluate", { expression: `document.querySelector('[data-cluster-editor="push"]').click()` });
    await assertText(cdp, (text) => text.includes("采集上报") && text.includes("实例 ID"), "Collector enrollment dialog did not render");
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
    await assertText(cdp, (text) => text.includes("集群接入") && !text.includes("上报凭据保存"), "Enrollment did not return to the cluster list");


  } finally {
    cdp.close();
  }

  console.log("Finding Portal and correlation evidence browser E2E: PASS");
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
  for (let attempt = 0; attempt < 200; attempt += 1) {
    try {
      const response = await fetch(url);
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
        footerAligned: Math.abs(bindingAction.right - bindings.right) <= 1,
        wrapsDN: [...dn.getClientRects()].every((line) => line.right <= bounds.right + 1),
      };
    })()`,
    returnByValue: true,
  });
  const layout = response.result?.value;
  if (!layout || layout.overflow || !layout.aligned || !layout.actionSeparated || !layout.rowsFillPanel || !layout.footerAligned || !layout.wrapsDN) {
    throw new Error(`Account detail layout at ${width}px: ${JSON.stringify(response.exceptionDetails ?? layout)}`);
  }
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

function sleep(milliseconds) {
  return new Promise((resolve) => setTimeout(resolve, milliseconds));
}
