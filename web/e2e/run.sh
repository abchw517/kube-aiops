#!/usr/bin/env bash
set -Eeuo pipefail

WEB_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
CHROME_BIN="${CHROME_BIN:-}"
if [[ -z "${CHROME_BIN}" ]]; then
  CHROME_BIN="$(command -v google-chrome || command -v chromium || command -v chromium-browser || true)"
fi
[[ -n "${CHROME_BIN}" ]] || { echo "ERROR: Chrome/Chromium is required for web E2E" >&2; exit 1; }

cd "${WEB_DIR}"
npm run build
node e2e/mock-server.mjs >/tmp/kube-aiops-web-e2e.log 2>&1 &
SERVER_PID=$!
trap 'kill "${SERVER_PID}" >/dev/null 2>&1 || true' EXIT

for _ in $(seq 1 30); do
  if curl --fail --silent http://127.0.0.1:4173/ >/dev/null; then
    break
  fi
  sleep 1
done
curl --fail --silent http://127.0.0.1:4173/ >/dev/null

chrome_dump() {
  "${CHROME_BIN}" --headless --no-sandbox --disable-gpu --virtual-time-budget=3000 --dump-dom "$1" 2>/dev/null
}

LIST_DOM="$(chrome_dump 'http://127.0.0.1:4173/#/findings')"
grep -q '2 findings' <<<"${LIST_DOM}"
grep -q 'CrashLoopBackOff detected' <<<"${LIST_DOM}"
grep -q 'Replica availability degraded' <<<"${LIST_DOM}"

FILTERED_DOM="$(chrome_dump 'http://127.0.0.1:4173/#/findings?cluster=local&severity=critical')"
grep -q '1 finding' <<<"${FILTERED_DOM}"
grep -q 'CrashLoopBackOff detected' <<<"${FILTERED_DOM}"
! grep -q 'Replica availability degraded' <<<"${FILTERED_DOM}"

DETAIL_DOM="$(chrome_dump 'http://127.0.0.1:4173/#/findings/finding-critical')"
grep -q 'Finding ID' <<<"${DETAIL_DOM}"
grep -q 'Container restart backoff is increasing.' <<<"${DETAIL_DOM}"

echo "Phase 1.3 web browser E2E: PASS"
