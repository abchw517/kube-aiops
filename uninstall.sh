#!/usr/bin/env bash
set -Eeuo pipefail

readonly NAMESPACE="k8sgpt-operator-system"
readonly RELEASE_NAME="k8sgpt-operator"
readonly SECRET_NAME="k8sgpt-openai-secret"
readonly K8SGPT_NAME="k8sgpt-engine"
readonly LOCK_NAME="kube-aiops-lifecycle"
PURGE_SECRET="${PURGE_SECRET:-false}"
PURGE_NAMESPACE="${PURGE_NAMESPACE:-false}"
PURGE_DEMO="${PURGE_DEMO:-false}"
TIMEOUT="${TIMEOUT:-5m}"
LOCK_WAIT_SECONDS="${LOCK_WAIT_SECONDS:-30}"

ROOT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
# shellcheck source=scripts/lifecycle-common.sh
source "${ROOT_DIR}/scripts/lifecycle-common.sh"
OPERATION_ID="uninstall-$(hostname | tr -cd '[:alnum:].-' | cut -c1-32)-$$-$(date +%s)"

log() { lc_log "$@"; }
fatal() { log "ERROR: $*"; exit 1; }

on_exit() {
  local rc="$1"
  local release_failed=false

  trap - EXIT ERR
  if ! lc_release_lock; then
    release_failed=true
    log "ERROR: 生命周期 Lease 未能正常释放；它将在租期到期后可被接管"
  fi
  if ((rc == 0)) && [[ "$release_failed" == "true" ]]; then rc=1; fi
  ((rc == 0)) || log "ERROR: 卸载未完成 exit=${rc}"
  exit "$rc"
}
trap 'on_exit $?' EXIT

for cmd in kubectl helm python3; do command -v "$cmd" >/dev/null 2>&1 || fatal "缺少命令: $cmd"; done
for value in "$PURGE_SECRET" "$PURGE_NAMESPACE" "$PURGE_DEMO"; do
  [[ "$value" == "true" || "$value" == "false" ]] || fatal "PURGE_* 变量只能为 true 或 false"
done
[[ "$LOCK_WAIT_SECONDS" =~ ^[0-9]+$ ]] || fatal "LOCK_WAIT_SECONDS 必须是非负整数"

assert_cluster_resource_owned_or_absent() {
  local resource="$1"
  local name="$2"
  local resource_json ownership

  lc_kubectl_state "$resource" "$name"
  [[ "$LC_STATE" != "error" ]] || fatal "无法查询所有权: ${resource}/${name}"
  [[ "$LC_STATE" == "present" ]] || return 0

  resource_json="$(kubectl get "$resource" "$name" -o json)" || fatal "无法读取所有权: ${resource}/${name}"
  ownership="$(python3 -c '
import json, sys
obj = json.load(sys.stdin)
meta = obj.get("metadata", {})
print((meta.get("annotations", {}).get("kube-aiops.io/owner", "")) + "|" +
      (meta.get("labels", {}).get("app.kubernetes.io/part-of", "")))
' <<<"$resource_json")" || fatal "无法解析所有权: ${resource}/${name}"
  [[ "$ownership" == "phase-1.1|"* || "$ownership" == *"|kube-aiops" ]] || fatal \
    "拒绝删除外部资源: ${resource}/${name}；缺少 kube-aiops 所有权标识"
}

delete_resource_if_present() {
  local resource="$1"
  local name="$2"
  local namespace="${3:-}"
  local -a args=(delete "$resource" "$name" --wait=true --timeout="$TIMEOUT")

  lc_kubectl_state "$resource" "$name" "$namespace"
  [[ "$LC_STATE" != "error" ]] || fatal "删除前查询失败: ${resource}/${name}"
  [[ "$LC_STATE" == "present" ]] || return 0
  [[ -z "$namespace" ]] || args+=(-n "$namespace")
  kubectl "${args[@]}"

  lc_kubectl_state "$resource" "$name" "$namespace"
  [[ "$LC_STATE" != "error" ]] || fatal "删除后查询失败: ${resource}/${name}"
  [[ "$LC_STATE" == "absent" ]] || fatal "资源删除后仍存在: ${resource}/${name}"
}

assert_resource_absent() {
  local resource="$1"
  local name="$2"
  local namespace="${3:-}"

  lc_kubectl_state "$resource" "$name" "$namespace"
  [[ "$LC_STATE" != "error" ]] || fatal "残留检查查询失败: ${resource}/${name}"
  [[ "$LC_STATE" == "absent" ]] || fatal "卸载后资源仍存在: ${resource}/${name}"
}

CONTEXT="$(kubectl config current-context 2>/dev/null || true)"
[[ -n "$CONTEXT" ]] || fatal "未获取到 kubectl current-context"
kubectl version --request-timeout=10s >/dev/null
log "Kubernetes Context: ${CONTEXT} operation=${OPERATION_ID}"

lc_kubectl_state namespace "$NAMESPACE"
[[ "$LC_STATE" != "error" ]] || fatal "无法查询 Namespace: ${NAMESPACE}"
NAMESPACE_PRESENT="$LC_STATE"
if [[ "$NAMESPACE_PRESENT" == "present" ]]; then
  lc_acquire_lock "$NAMESPACE" "$LOCK_NAME" "$OPERATION_ID" "$LOCK_WAIT_SECONDS" || fatal "无法获取生命周期 Lease"
  log "已获取生命周期 Lease: ${NAMESPACE}/${LOCK_NAME}"
fi

# 在任何删除动作前验证集群级固定名称资源的所有权，避免半卸载。
assert_cluster_resource_owned_or_absent clusterrole k8sgpt-clusterrole
assert_cluster_resource_owned_or_absent clusterrolebinding k8sgpt-clusterrole-binding

lc_kubectl_state crd k8sgpts.core.k8sgpt.ai
[[ "$LC_STATE" != "error" ]] || fatal "无法查询 K8sGPT CRD"
if [[ "$LC_STATE" == "present" && "$NAMESPACE_PRESENT" == "present" ]]; then
  log "删除并等待 K8sGPT CR"
  delete_resource_if_present k8sgpt "$K8SGPT_NAME" "$NAMESPACE"
fi

lc_helm_state "$RELEASE_NAME" "$NAMESPACE"
[[ "$LC_STATE" != "error" ]] || fatal "无法查询 Helm Release"
if [[ "$LC_STATE" == "present" ]]; then
  log "卸载 Helm Release"
  helm uninstall "$RELEASE_NAME" -n "$NAMESPACE" --wait --timeout "$TIMEOUT"
fi

delete_resource_if_present clusterrolebinding k8sgpt-clusterrole-binding
delete_resource_if_present clusterrole k8sgpt-clusterrole
if [[ "$NAMESPACE_PRESENT" == "present" ]]; then
  delete_resource_if_present serviceaccount k8sgpt "$NAMESPACE"
fi

if [[ "$PURGE_DEMO" == "true" ]]; then
  delete_resource_if_present namespace k8sgpt-demo
fi
if [[ "$PURGE_SECRET" == "true" && "$NAMESPACE_PRESENT" == "present" ]]; then
  delete_resource_if_present secret "$SECRET_NAME" "$NAMESPACE"
fi
if [[ "$PURGE_NAMESPACE" == "true" && "$NAMESPACE_PRESENT" == "present" ]]; then
  # Lease 位于目标 Namespace；先安全释放，再删除 Namespace。
  lc_release_lock || fatal "删除 Namespace 前无法释放生命周期 Lease"
  delete_resource_if_present namespace "$NAMESPACE"
  NAMESPACE_PRESENT="absent"
fi

lc_helm_state "$RELEASE_NAME" "$NAMESPACE"
[[ "$LC_STATE" != "error" ]] || fatal "Helm 残留检查失败"
[[ "$LC_STATE" == "absent" ]] || fatal "Helm Release 仍存在"
assert_resource_absent clusterrole k8sgpt-clusterrole
assert_resource_absent clusterrolebinding k8sgpt-clusterrole-binding
if [[ "$NAMESPACE_PRESENT" == "present" ]]; then
  assert_resource_absent serviceaccount k8sgpt "$NAMESPACE"
  lc_kubectl_state crd k8sgpts.core.k8sgpt.ai
  [[ "$LC_STATE" != "error" ]] || fatal "K8sGPT CRD 残留检查失败"
  if [[ "$LC_STATE" == "present" ]]; then
    assert_resource_absent k8sgpt "$K8SGPT_NAME" "$NAMESPACE"
  fi
fi

log "卸载完成；CRD 默认保留"
