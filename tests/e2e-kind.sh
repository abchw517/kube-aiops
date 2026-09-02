#!/usr/bin/env bash
set -Eeuo pipefail

ROOT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
# shellcheck source=config/platform-versions.env
source "${ROOT_DIR}/config/platform-versions.env"

readonly CLUSTER_NAME="${KIND_CLUSTER_NAME:-kube-aiops-e2e}"
readonly NODE_IMAGE="${KIND_NODE_IMAGE}"
FAULT_BIN="$(mktemp -d -t kube-aiops-fault-bin.XXXXXX)"
REAL_KUBECTL="$(command -v kubectl || true)"

log() { printf '[e2e] %s\n' "$*"; }
cleanup() {
  kind delete cluster --name "$CLUSTER_NAME" >/dev/null 2>&1 || true
  if [[ -d "$FAULT_BIN" && "$FAULT_BIN" == /tmp/kube-aiops-fault-bin.* ]]; then
    rm -rf -- "$FAULT_BIN"
  fi
}
trap cleanup EXIT

for cmd in kind kubectl helm; do
  command -v "$cmd" >/dev/null 2>&1 || { echo "ERROR: 缺少命令: $cmd" >&2; exit 1; }
done
[[ -n "$REAL_KUBECTL" ]] || { echo "ERROR: 缺少真实 kubectl 路径" >&2; exit 1; }
ln -s "${ROOT_DIR}/tests/kubectl-fault-wrapper.sh" "${FAULT_BIN}/kubectl"

kind create cluster --name "$CLUSTER_NAME" --image "$NODE_IMAGE" --wait 120s
cd "$ROOT_DIR"

log "验证不支持的 Operator 版本在任何集群变更前失败"
if OPERATOR_VERSION=9.9.9 bash ./install.sh; then
  echo "ERROR: 不支持的 Operator 版本不应进入安装" >&2
  exit 1
fi
if kubectl get namespace k8sgpt-operator-system >/dev/null 2>&1; then
  echo "ERROR: Operator 版本门禁失败时不应创建 Namespace" >&2
  exit 1
fi

log "验证外部同名集群级 RBAC 不会被接管"
kubectl create clusterrole k8sgpt-clusterrole --verb=get --resource=pods
kubectl create clusterrolebinding k8sgpt-clusterrole-binding \
  --clusterrole=k8sgpt-clusterrole --serviceaccount=default:default
make bootstrap
OPENAI_TOKEN="e2e-placeholder-not-a-real-token" make bootstrap-secret
if bash ./install.sh; then
  echo "ERROR: 外部同名 RBAC 存在时安装不应成功" >&2
  exit 1
fi
if helm status k8sgpt-operator -n k8sgpt-operator-system >/dev/null 2>&1; then
  echo "ERROR: 所有权检查失败后不应存在 Helm Release" >&2
  exit 1
fi
kubectl delete clusterrolebinding k8sgpt-clusterrole-binding
kubectl delete clusterrole k8sgpt-clusterrole
PURGE_SECRET=true PURGE_NAMESPACE=true make uninstall

log "验证缺少 Namespace 时安装不会修改集群"
if bash ./install.sh; then
  echo "ERROR: 无前置条件时安装不应成功" >&2
  exit 1
fi
if helm status k8sgpt-operator -n k8sgpt-operator-system >/dev/null 2>&1; then
  echo "ERROR: 前置检查失败后不应存在 Helm Release" >&2
  exit 1
fi

make bootstrap
log "验证缺少 Secret 时安装不会创建 Helm Release"
if bash ./install.sh; then
  echo "ERROR: 缺少 Secret 时安装不应成功" >&2
  exit 1
fi
if helm status k8sgpt-operator -n k8sgpt-operator-system >/dev/null 2>&1; then
  echo "ERROR: 缺少 Secret 时不应存在 Helm Release" >&2
  exit 1
fi

OPENAI_TOKEN="e2e-placeholder-not-a-real-token" make bootstrap-secret

log "首次安装"
make install
log "重复安装（幂等）"
make install
make verify

log "验证 legacy 身份必须显式且通过指纹迁移"
kubectl label clusterrole k8sgpt-clusterrole app.kubernetes.io/instance-
kubectl label clusterrolebinding k8sgpt-clusterrole-binding app.kubernetes.io/instance-
if bash ./install.sh; then
  echo "ERROR: 未显式授权时不应迁移 legacy 身份" >&2
  exit 1
fi
MIGRATE_LEGACY_OWNERSHIP=true bash ./install.sh
[[ "$(kubectl get clusterrole k8sgpt-clusterrole -o jsonpath='{.metadata.labels.app\.kubernetes\.io/instance}')" == "k8sgpt-operator" ]]

log "验证已有 Release 的 Helm 后置失败会回滚并恢复健康状态"
FAULT_MARKER="$(mktemp -t kube-aiops-ssa-fault.XXXXXX)"
ROLLBACK_LOG="$(mktemp -t kube-aiops-rollback.XXXXXX)"
if REAL_KUBECTL="$REAL_KUBECTL" FAULT_MODE=fail_ssa_once FAULT_MARKER="$FAULT_MARKER" \
  PATH="${FAULT_BIN}:${PATH}" bash ./install.sh 2>&1 | tee "$ROLLBACK_LOG"; then
  echo "ERROR: 注入 SSA 故障后安装不应成功" >&2
  exit 1
fi
rm -f -- "$FAULT_MARKER"
grep -q '回滚至 Helm revision=' "$ROLLBACK_LOG" || {
  echo "ERROR: 未观察到已有 Release 的 Helm rollback" >&2
  exit 1
}
if grep -q '回滚存在 .* 项失败' "$ROLLBACK_LOG"; then
  echo "ERROR: 回滚出现二次失败" >&2
  exit 1
fi
rm -f -- "$ROLLBACK_LOG"
[[ "$(helm status k8sgpt-operator -n k8sgpt-operator-system -o json |
  python3 -c 'import json,sys; print(json.load(sys.stdin)["info"]["status"])')" == "deployed" ]]
make verify

log "验证回滚二次失败时保留 0700 状态与对象级报告"
FAULT_MARKER="$(mktemp -t kube-aiops-ssa-rollback-fault.XXXXXX)"
ROLLBACK_STATE_ROOT="$(mktemp -d -t kube-aiops-rollback-state.XXXXXX)"
touch "$FAULT_MARKER"
if REAL_KUBECTL="$REAL_KUBECTL" FAULT_MODE=fail_ssa_and_rollback FAULT_MARKER="$FAULT_MARKER" \
  ROLLBACK_STATE_DIR="$ROLLBACK_STATE_ROOT" PATH="${FAULT_BIN}:${PATH}" bash ./install.sh; then
  echo "ERROR: 回滚恢复故障注入后安装不应成功" >&2
  exit 1
fi
PRESERVED_STATE="$(find "$ROLLBACK_STATE_ROOT" -mindepth 1 -maxdepth 1 -type d -name 'kube-aiops-install.*' -print -quit)"
[[ -n "$PRESERVED_STATE" && "$(stat -c '%a' "$PRESERVED_STATE")" == "700" ]]
grep -q $'restore\t.*\tfailed' "$PRESERVED_STATE/restore-report.tsv"
[[ -f "$PRESERVED_STATE/operation.json" ]]
rm -rf -- "$ROLLBACK_STATE_ROOT"
rm -f -- "$FAULT_MARKER"
make verify

log "验证 Lease CAS 释放不会删除新持有者"
bash -Eeuo pipefail -c '
  source "$1/scripts/lifecycle-common.sh"
  lc_acquire_lock k8sgpt-operator-system kube-aiops-lifecycle cas-release-test 0
  kubectl patch lease kube-aiops-lifecycle -n k8sgpt-operator-system --type=merge \
    -p '\''{"spec":{"holderIdentity":"next-holder"}}'\'' >/dev/null
  if lc_release_lock; then
    echo "ERROR: 旧持有者不应释放新持有者 Lease" >&2
    exit 1
  fi
  [[ "$(kubectl get lease kube-aiops-lifecycle -n k8sgpt-operator-system -o jsonpath="{.spec.holderIdentity}")" == next-holder ]]
' _ "$ROOT_DIR"
kubectl delete lease kube-aiops-lifecycle -n k8sgpt-operator-system

log "验证短租期 heartbeat 会持续续租"
LEASE_DURATION_SECONDS=3 LEASE_RENEW_INTERVAL_SECONDS=1 bash -Eeuo pipefail -c '
  source "$1/scripts/lifecycle-common.sh"
  lc_acquire_lock k8sgpt-operator-system kube-aiops-lifecycle heartbeat-test 0
  lc_start_lock_heartbeat
  sleep 4
  lc_assert_lock_held
  lc_release_lock
' _ "$ROOT_DIR"

log "验证生命周期 Lease 阻止并发安装"
kubectl apply --server-side --force-conflicts -f - >/dev/null <<'EOF'
apiVersion: coordination.k8s.io/v1
kind: Lease
metadata:
  name: kube-aiops-lifecycle
  namespace: k8sgpt-operator-system
spec:
  holderIdentity: external-release-system
  leaseDurationSeconds: 1800
  acquireTime: "2099-01-01T00:00:00.000000Z"
  renewTime: "2099-01-01T00:00:00.000000Z"
EOF
if LOCK_WAIT_SECONDS=0 bash ./install.sh; then
  echo "ERROR: Lease 被外部操作持有时安装不应成功" >&2
  exit 1
fi
kubectl delete lease kube-aiops-lifecycle -n k8sgpt-operator-system
helm status k8sgpt-operator -n k8sgpt-operator-system >/dev/null

log "验证卸载不会把 Forbidden 查询误判为资源不存在"
if REAL_KUBECTL="$REAL_KUBECTL" FAULT_MODE=deny_clusterrole_get \
  PATH="${FAULT_BIN}:${PATH}" bash ./uninstall.sh; then
  echo "ERROR: 所有权查询 Forbidden 时卸载不应成功" >&2
  exit 1
fi
helm status k8sgpt-operator -n k8sgpt-operator-system >/dev/null

log "验证卸载拒绝删除失去所有权标识的集群级 RBAC"
kubectl annotate clusterrole k8sgpt-clusterrole kube-aiops.io/owner-
kubectl label clusterrole k8sgpt-clusterrole app.kubernetes.io/part-of-
if PURGE_SECRET=true PURGE_NAMESPACE=true bash ./uninstall.sh; then
  echo "ERROR: 所有权标识缺失时卸载不应成功" >&2
  exit 1
fi
helm status k8sgpt-operator -n k8sgpt-operator-system >/dev/null
kubectl annotate clusterrole k8sgpt-clusterrole kube-aiops.io/owner=phase-1.1
kubectl label clusterrole k8sgpt-clusterrole app.kubernetes.io/part-of=kube-aiops

log "首次完全卸载"
PURGE_SECRET=true PURGE_NAMESPACE=true PURGE_DEMO=true make uninstall
log "重复卸载（幂等）"
PURGE_SECRET=true PURGE_NAMESPACE=true PURGE_DEMO=true make uninstall

for resource in \
  "namespace/k8sgpt-operator-system" \
  "clusterrole/k8sgpt-clusterrole" \
  "clusterrolebinding/k8sgpt-clusterrole-binding"; do
  if kubectl get "$resource" >/dev/null 2>&1; then
    echo "ERROR: 卸载后资源残留: $resource" >&2
    exit 1
  fi
done
kubectl get crd k8sgpts.core.k8sgpt.ai >/dev/null
kubectl get crd results.core.k8sgpt.ai >/dev/null
log "Kind 生命周期 E2E 全部通过"
