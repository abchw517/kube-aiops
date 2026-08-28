#!/usr/bin/env bash
set -Eeuo pipefail

readonly CLUSTER_NAME="${KIND_CLUSTER_NAME:-kube-aiops-provider-e2e}"
readonly NODE_IMAGE="${KIND_NODE_IMAGE:-kindest/node:v1.34.8@sha256:02722c2dedddcfc00febf5d27fbeb9b7b2c14294c82109ff4a85d89ac9ba3256}"
ROOT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
FUNCTIONAL_TIMEOUT_SECONDS="${FUNCTIONAL_TIMEOUT_SECONDS:-900}"
VERIFY_LOG="$(mktemp -t kube-aiops-provider-verify.XXXXXX)"

log() { printf '[provider-e2e] %s\n' "$*"; }
cleanup() {
  kind delete cluster --name "$CLUSTER_NAME" >/dev/null 2>&1 || true
  rm -f -- "$VERIFY_LOG"
}
trap cleanup EXIT

: "${OPENAI_TOKEN:?OPENAI_TOKEN 未设置；Provider Functional E2E 必须使用受限测试凭据}"
[[ "$FUNCTIONAL_TIMEOUT_SECONDS" =~ ^[0-9]+$ ]] || { echo "ERROR: FUNCTIONAL_TIMEOUT_SECONDS 必须是非负整数" >&2; exit 1; }
for cmd in kind kubectl helm python3; do
  command -v "$cmd" >/dev/null 2>&1 || { echo "ERROR: 缺少命令: $cmd" >&2; exit 1; }
done

kind create cluster --name "$CLUSTER_NAME" --image "$NODE_IMAGE" --wait 120s
cd "$ROOT_DIR"

make bootstrap
make bootstrap-secret
make install

# 功能 E2E 使用 30s 分析周期降低 CI 等待时间；生产清单仍固定为 5m。
kubectl patch k8sgpt k8sgpt-engine -n k8sgpt-operator-system --type=merge \
  -p '{"spec":{"analysis":{"interval":"30s"}}}'
RESULT_SINCE="$(date -u '+%Y-%m-%dT%H:%M:%SZ')"
make demo

log "等待本次 Demo 生成 Pod、Deployment、PersistentVolumeClaim 三类新鲜 Result"
deadline=$((SECONDS + FUNCTIONAL_TIMEOUT_SECONDS))
while ((SECONDS <= deadline)); do
  if STRICT_RESULTS=true \
    RESULT_SINCE="$RESULT_SINCE" \
    EXPECTED_RESULT_KINDS="Pod,Deployment,PersistentVolumeClaim" \
    EXPECTED_ANALYSIS_INTERVAL="30s" \
    REQUIRE_ANALYSIS_HEALTH=true \
    bash ./verify.sh >"$VERIFY_LOG" 2>&1; then
    cat "$VERIFY_LOG"
    log "Provider Functional E2E 全部通过"
    PURGE_SECRET=true PURGE_NAMESPACE=true PURGE_DEMO=true make uninstall
    exit 0
  fi
  sleep 30
done

cat "$VERIFY_LOG" >&2
kubectl get k8sgpt k8sgpt-engine -n k8sgpt-operator-system -o yaml >&2 || true
kubectl get results -n k8sgpt-operator-system -o yaml >&2 || true
echo "ERROR: Provider Functional E2E 在 ${FUNCTIONAL_TIMEOUT_SECONDS}s 内未满足严格验收" >&2
exit 1
