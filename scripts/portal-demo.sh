#!/usr/bin/env bash
set -Eeuo pipefail

ROOT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
readonly ROOT_DIR
readonly WEB_PORT="${PORTAL_DEMO_PORT:-4173}"
readonly API_PORT="${PORTAL_DEMO_API_PORT:-18082}"
readonly CHECK_MODE="${1:-}"

if [[ "$CHECK_MODE" != "" && "$CHECK_MODE" != "--check" ]] || (( $# > 1 )); then
  printf 'Usage: %s [--check]\n' "$0" >&2
  exit 2
fi
for port in "$WEB_PORT" "$API_PORT"; do
  if [[ ! "$port" =~ ^[1-9][0-9]{0,4}$ ]] || (( port > 65535 )); then
    printf 'Invalid demo port %s\n' "$port" >&2
    exit 2
  fi
done
if [[ "$WEB_PORT" == "$API_PORT" ]]; then
  printf 'Portal and API demo ports must differ.\n' >&2
  exit 2
fi
for dependency in go node npm openssl curl; do
  command -v "$dependency" >/dev/null || { printf 'Missing dependency %s\n' "$dependency" >&2; exit 1; }
done

work_dir="$(mktemp -d "${TMPDIR:-/tmp}/kube-aiops-portal-demo.XXXXXXXX")"
backend_pid=""
fixture_pid=""
# Called by the EXIT trap.
# shellcheck disable=SC2317,SC2329
cleanup() {
  if [[ -n "$backend_pid" ]]; then
    kill "$backend_pid" 2>/dev/null || true
    wait "$backend_pid" 2>/dev/null || true
  fi
  if [[ -n "$fixture_pid" ]]; then
    kill "$fixture_pid" 2>/dev/null || true
    wait "$fixture_pid" 2>/dev/null || true
  fi
  rm -rf -- "$work_dir"
}
trap 'cleanup' EXIT
trap 'exit 130' INT
trap 'exit 143' TERM

cd "$ROOT_DIR"
npm --prefix web run build
go build -o "$work_dir/backend" ./cmd/api
openssl req -x509 -newkey rsa:2048 -nodes -days 1 -subj /CN=127.0.0.1 \
  -addext subjectAltName=IP:127.0.0.1 -keyout "$work_dir/key.pem" -out "$work_dir/ca.pem" \
  >"$work_dir/tls.log" 2>&1 || { cat "$work_dir/tls.log" >&2; exit 1; }
openssl rand -hex 24 >"$work_dir/token"

export NO_PROXY=127.0.0.1,localhost no_proxy=127.0.0.1,localhost
node web/demo/server.mjs "$work_dir" "$WEB_PORT" "$API_PORT" >"$work_dir/fixture.log" 2>&1 &
fixture_pid=$!
for ((attempt=0; attempt<60; attempt++)); do
  if [[ -s "$work_dir/upstream-url" ]]; then break; fi
  if ! kill -0 "$fixture_pid" 2>/dev/null; then break; fi
  sleep 0.25
done
if [[ ! -s "$work_dir/upstream-url" ]]; then
  cat "$work_dir/fixture.log" >&2
  printf 'The demo fixture failed to start.\n' >&2
  exit 1
fi
upstream="$(<"$work_dir/upstream-url")"
SECURITY_MODE=development HTTP_ADDR="127.0.0.1:$API_PORT" READY_TIMEOUT=3s \
  K8SGPT_NAMESPACE=k8sgpt-operator-system K8SGPT_NAME=k8sgpt-engine \
  KUBERNETES_API_URL="$upstream" KUBERNETES_CA_FILE="$work_dir/ca.pem" \
  KUBERNETES_BEARER_TOKEN_FILE="$work_dir/token" \
  PROMETHEUS_URL="$upstream" PROMETHEUS_CA_FILE="$work_dir/ca.pem" \
  PROMETHEUS_CLUSTER_LABEL_VALUE='' PROMETHEUS_BEARER_TOKEN_FILE='' \
  VICTORIALOGS_URL="$upstream" VICTORIALOGS_CA_FILE="$work_dir/ca.pem" \
  VICTORIALOGS_CLUSTER_LABEL_VALUE='' VICTORIALOGS_BEARER_TOKEN_FILE='' \
  ALERTMANAGER_URL="$upstream" ALERTMANAGER_CA_FILE="$work_dir/ca.pem" \
  ALERTMANAGER_CLUSTER_LABEL_VALUE='' ALERTMANAGER_BEARER_TOKEN_FILE='' \
  "$work_dir/backend" >"$work_dir/backend.log" 2>&1 &
backend_pid=$!
base_url="http://127.0.0.1:$WEB_PORT"
for ((attempt=0; attempt<60; attempt++)); do
  if ! kill -0 "$backend_pid" "$fixture_pid" 2>/dev/null; then break; fi
  if curl --fail --silent --max-time 1 "$base_url/readyz" >/dev/null; then
    if [[ "$CHECK_MODE" == "--check" ]]; then
      node web/demo/check.mjs "$base_url"
      exit
    fi
    printf '\nPortal demo ready at %s/#/findings\nPress Ctrl+C to stop.\n' "$base_url"
    while kill -0 "$backend_pid" "$fixture_pid" 2>/dev/null; do sleep 1; done
    cat "$work_dir/fixture.log" "$work_dir/backend.log" >&2
    printf 'A demo process stopped.\n' >&2
    exit 1
  fi
  sleep 0.25
done
cat "$work_dir/fixture.log" "$work_dir/backend.log" >&2
printf 'The Portal demo did not become ready.\n' >&2
exit 1
