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

    await cdp.send("Emulation.setDeviceMetricsOverride", {
      width: 390, height: 844, deviceScaleFactor: 1, mobile: true,
    });
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
