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
        text.includes("共 2 条诊断结果") &&
        text.includes("CrashLoopBackOff detected") &&
        text.includes("Replica availability degraded"),
      "Finding List did not render both normalized findings",
    );

    const localeEvaluation = await cdp.send("Runtime.evaluate", {
      expression: `({
        language: document.documentElement.lang,
        severity: document.querySelector('select[name="severity"] option[value="critical"]')?.textContent
      })`,
      returnByValue: true,
    });
    const locale = localeEvaluation.result?.value;
    if (locale?.language !== "zh-CN" || locale?.severity !== "严重") {
      throw new Error(`Chinese locale or severity label missing: ${JSON.stringify(locale)}`);
    }

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
      ["fixture-password", "共 2 条诊断结果"],
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
    await assertText(cdp, (text) => text.includes("共 2 条诊断结果"), "Checked LDAP option did not log in");
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
    await cdp.send("Runtime.evaluate", { expression: `document.querySelector('[data-cluster-editor="ownership-dev-k8s"]').click()` });
    await assertText(cdp, (text) => text.includes("payments") && text.includes("保存归属"), "Namespace ownership dialog did not render");
    await cdp.send("Runtime.evaluate", { expression: `(() => {
      const form = document.querySelector("dialog[open] [data-namespace-ownership]");
      form.querySelector('input[name="businesses"]').checked = true;
      form.requestSubmit();
    })()` });
    await assertText(cdp, (text) => !text.includes("保存归属") && text.includes("集群接入"), "Namespace ownership did not save and close");
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
      return {
        labels: links.map((link) => link.textContent).join("/"),
        indents: [...new Set(links.map((link) => getComputedStyle(link).paddingInlineStart))].length,
        sidebar: getComputedStyle(links[0].parentElement).backgroundColor,
        page: getComputedStyle(document.body).backgroundColor,
      };
    })()`, returnByValue: true });
    const navigation = nav.result?.value;
    if (navigation?.labels !== "诊断控制台/运维知识库/资源管理/系统管理" || navigation.indents !== 1) {
      throw new Error("The four modules must be peers in the workspace navigation");
    }
    if (navigation.sidebar !== "rgb(18, 38, 55)" || navigation.page !== "rgb(241, 245, 249)") {
      throw new Error("Workspace chrome must use the resource management prototype colors");
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
        !dialog.querySelector('[data-assignment-options="roles"]').hidden;
    })()` });
    if (!integrated.result?.value) throw new Error("Authorization target did not switch to role checkboxes");
    await cdp.send("Runtime.evaluate", { expression: `(() => {
      const form = document.querySelector("dialog[open] form");
      form.querySelector('input[name="roles"]').checked = true;
      form.requestSubmit(form.querySelector('button[value="assignment"]'));
    })()` });
    await assertText(cdp, (text) => text.includes("授权操作结果") && text.includes("已更新关联关系"), "Batch results did not display individual outcomes");
    await cdp.send("Runtime.evaluate", { expression: 'document.querySelector("dialog[open] footer [data-system-close]").click()' });
    await assertText(cdp, (text) => text.includes("已选 0 项") && !text.includes("授权操作结果"), "Batch assignment should refresh the accounts");
    await cdp.send("Runtime.evaluate", { expression: 'document.querySelector("[data-system-open=guest]").click()' });
    await assertText(cdp, (text) => text.includes("临时密码") && text.includes("取消"), "Create visitor dialog did not open");
    await cdp.send("Runtime.evaluate", { expression: 'document.querySelector("dialog[open] footer [data-system-close]").click()' });
    await cdp.send("Runtime.evaluate", { expression: `(() => {
      const kind = document.querySelector('[data-platform-filter="kind"]');
      kind.value = "guest";
      kind.dispatchEvent(new Event("change", { bubbles: true }));
    })()` });
    await assertText(cdp, (text) => text.includes("暂无符合条件的账号") && text.includes("已选 0 项"), "Filtering should clear hidden selections");
    await assertPage(cdp, "http://127.0.0.1:4173/#/admin/users/ldap%3Areader",
      (text) => text.includes("生效权限及来源") && text.includes("用户组 运维") && text.includes("直接授予"), "User detail omitted permission origins");
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
    await assertText(cdp, (text) => text.includes("均成功，可以保存"), "LDAP test did not update the verification panel");
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
    await assertPage(cdp, "http://127.0.0.1:4173/#/admin/operations",
      (text) => text.includes("操作审计") && text.includes("创建账号") && text.includes("local:guest"), "Operation audit did not render");
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
