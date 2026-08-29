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
LEASE_DURATION_SECONDS="${LEASE_DURATION_SECONDS:-1800}"
LEASE_RENEW_INTERVAL_SECONDS="${LEASE_RENEW_INTERVAL_SECONDS:-30}"

ROOT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
DEPLOY_DIR="${ROOT_DIR}/deploy/k8sgpt"
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
[[ "$LEASE_DURATION_SECONDS" =~ ^[1-9][0-9]*$ ]] || fatal "LEASE_DURATION_SECONDS 必须是正整数"
[[ "$LEASE_RENEW_INTERVAL_SECONDS" =~ ^[1-9][0-9]*$ ]] || fatal "LEASE_RENEW_INTERVAL_SECONDS 必须是正整数"
((LEASE_RENEW_INTERVAL_SECONDS < LEASE_DURATION_SECONDS)) || fatal "Lease 续租间隔必须小于租期"

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
  lc_start_lock_heartbeat || fatal "无法启动生命周期 Lease heartbeat"
  log "已获取生命周期 Lease: ${NAMESPACE}/${LOCK_NAME}"
fi

# 在任何删除动作前验证集群级固定名称资源的所有权，避免半卸载。
lc_assert_cluster_resource_identity uninstall clusterrole k8sgpt-clusterrole \
  "${DEPLOY_DIR}/clusterrole.yaml" false || fatal "ClusterRole 所有权校验失败"
lc_assert_cluster_resource_identity uninstall clusterrolebinding k8sgpt-clusterrole-binding \
  "${DEPLOY_DIR}/clusterrolebinding.yaml" false || fatal "ClusterRoleBinding 所有权校验失败"

# 在任何删除动作前确认同名 Release 确实属于预期 Chart。
lc_helm_state "$RELEASE_NAME" "$NAMESPACE"
[[ "$LC_STATE" != "error" ]] || fatal "无法查询 Helm Release"
HELM_RELEASE_PRESENT="$LC_STATE"
if [[ "$HELM_RELEASE_PRESENT" == "present" ]]; then
  lc_assert_helm_release_identity "$RELEASE_NAME" "$NAMESPACE" k8sgpt-operator || fatal "Helm Release 身份校验失败"
fi

lc_kubectl_state crd k8sgpts.core.k8sgpt.ai
[[ "$LC_STATE" != "error" ]] || fatal "无法查询 K8sGPT CRD"
if [[ "$LC_STATE" == "present" && "$NAMESPACE_PRESENT" == "present" ]]; then
  log "删除并等待 K8sGPT CR"
  lc_assert_lock_held || fatal "删除 K8sGPT CR 前已失去生命周期 Lease"
  delete_resource_if_present k8sgpt "$K8SGPT_NAME" "$NAMESPACE"
fi

if [[ "$HELM_RELEASE_PRESENT" == "present" ]]; then
  log "卸载 Helm Release"
  lc_assert_lock_held || fatal "卸载 Helm Release 前已失去生命周期 Lease"
  helm uninstall "$RELEASE_NAME" -n "$NAMESPACE" --wait --timeout "$TIMEOUT"
fi

[[ "$NAMESPACE_PRESENT" != "present" ]] || lc_assert_lock_held || fatal "删除 RBAC 前已失去生命周期 Lease"
delete_resource_if_present clusterrolebinding k8sgpt-clusterrole-binding
delete_resource_if_present clusterrole k8sgpt-clusterrole
if [[ "$NAMESPACE_PRESENT" == "present" ]]; then
  delete_resource_if_present serviceaccount k8sgpt "$NAMESPACE"
  delete_resource_if_present networkpolicy kube-aiops-egress-baseline "$NAMESPACE"
fi

if [[ "$PURGE_DEMO" == "true" ]]; then
  [[ "$NAMESPACE_PRESENT" != "present" ]] || lc_assert_lock_held || fatal "删除 Demo 前已失去生命周期 Lease"
  delete_resource_if_present namespace k8sgpt-demo
fi
if [[ "$PURGE_SECRET" == "true" && "$NAMESPACE_PRESENT" == "present" ]]; then
  lc_assert_lock_held || fatal "删除 Secret 前已失去生命周期 Lease"
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
  assert_resource_absent networkpolicy kube-aiops-egress-baseline "$NAMESPACE"
  lc_kubectl_state crd k8sgpts.core.k8sgpt.ai
  [[ "$LC_STATE" != "error" ]] || fatal "K8sGPT CRD 残留检查失败"
  if [[ "$LC_STATE" == "present" ]]; then
    assert_resource_absent k8sgpt "$K8SGPT_NAME" "$NAMESPACE"
  fi
fi

log "卸载完成；CRD 默认保留"
