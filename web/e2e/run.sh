#!/usr/bin/env bash
set -Eeuo pipefail

WEB_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
CHROME_BIN="${CHROME_BIN:-}"
if [[ -z "${CHROME_BIN}" ]]; then
  CHROME_BIN="$(command -v google-chrome || command -v chromium || command -v chromium-browser || true)"
fi
[[ -n "${CHROME_BIN}" ]] || { echo "ERROR: Chrome/Chromium is required for web E2E" >&2; exit 1; }
export CHROME_BIN

cd "${WEB_DIR}"
npm run build
node e2e/mock-server.mjs >/tmp/kube-aiops-web-e2e.log 2>&1 &
SERVER_PID=$!

cleanup() {
  local exit_code=$?
  kill "${SERVER_PID}" >/dev/null 2>&1 || true
  if (( exit_code != 0 )); then
    echo "--- mock server log ---" >&2
    cat /tmp/kube-aiops-web-e2e.log >&2 || true
  fi
  exit "${exit_code}"
}
trap cleanup EXIT

for _ in $(seq 1 30); do
  if curl --fail --silent http://127.0.0.1:4173/ >/dev/null; then
    break
  fi
  sleep 1
done
curl --fail --silent http://127.0.0.1:4173/ >/dev/null

node e2e/browser-test.mjs
