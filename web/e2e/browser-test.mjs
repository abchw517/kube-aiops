import { spawn } from "node:child_process";
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
        text.includes("2 findings") &&
        text.includes("CrashLoopBackOff detected") &&
        text.includes("Replica availability degraded"),
      "Finding List did not render both normalized findings",
    );

    await assertPage(
      cdp,
      "http://127.0.0.1:4173/#/findings?cluster=local&severity=critical",
      (text) =>
        text.includes("1 finding") &&
        text.includes("CrashLoopBackOff detected") &&
        !text.includes("Replica availability degraded"),
      "Severity filter did not reduce the Finding set to critical only",
    );

    await assertPage(
      cdp,
      "http://127.0.0.1:4173/#/findings/finding-critical",
      (text) =>
        text.includes("Finding ID") &&
        text.includes("Container restart backoff is increasing."),
      "Finding Detail did not render the selected Finding",
    );
  } finally {
    cdp.close();
  }

  console.log("Phase 1.3 web browser E2E: PASS");
} finally {
  chrome.kill("SIGTERM");
  await rm(userDataDir, { recursive: true, force: true });
}

async function waitForPage(debugPort) {
  const url = `http://127.0.0.1:${debugPort}/json/list`;
  let lastError;
  for (let attempt = 0; attempt < 60; attempt += 1) {
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
