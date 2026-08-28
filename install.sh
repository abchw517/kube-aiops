#!/usr/bin/env bash
set -Eeuo pipefail

# Phase 1.1 使用固定资源身份。任意 Namespace 需要真正的模板层和唯一的集群级 RBAC 名称，
# 因此本阶段不暴露不完整的 Namespace/资源名覆盖参数。
readonly NAMESPACE="k8sgpt-operator-system"
readonly RELEASE_NAME="k8sgpt-operator"
readonly SECRET_NAME="k8sgpt-openai-secret"
readonly SERVICE_ACCOUNT="k8sgpt"
readonly K8SGPT_NAME="k8sgpt-engine"
readonly LOCK_NAME="kube-aiops-lifecycle"
OPERATOR_VERSION="${OPERATOR_VERSION:-0.2.29}"
TIMEOUT="${TIMEOUT:-5m}"
LOCK_WAIT_SECONDS="${LOCK_WAIT_SECONDS:-30}"

ROOT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
DEPLOY_DIR="${ROOT_DIR}/deploy/k8sgpt"
# shellcheck source=scripts/lifecycle-common.sh
source "${ROOT_DIR}/scripts/lifecycle-common.sh"

NEW_RELEASE=false
HELM_UPGRADE_SUCCEEDED=false
SNAPSHOTS_READY=false
PREVIOUS_REVISION=""
SNAPSHOT_DIR=""
OPERATION_ID="install-$(hostname | tr -cd '[:alnum:].-' | cut -c1-32)-$$-$(date +%s)"

log() { lc_log "$@"; }
fatal() { log "ERROR: $*"; exit 1; }
require_cmd() { command -v "$1" >/dev/null 2>&1 || fatal "缺少命令: $1"; }
require_file() { [[ -f "$1" ]] || fatal "文件不存在: $1"; }

require_resource_state() {
  local expected="$1"
  local resource="$2"
  local name="$3"
  local namespace="${4:-}"

  lc_kubectl_state "$resource" "$name" "$namespace"
  [[ "$LC_STATE" != "error" ]] || fatal "查询失败: ${resource}/${name}"
  [[ "$LC_STATE" == "$expected" ]]
}

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
    "拒绝接管外部资源: ${resource}/${name}；缺少 kube-aiops 所有权标识"
}

snapshot_resource() {
  local resource="$1"
  local name="$2"
  local namespace="$3"
  local file="$4"
  local -a args=(get "$resource" "$name" -o json)

  lc_kubectl_state "$resource" "$name" "$namespace"
  [[ "$LC_STATE" != "error" ]] || fatal "快照查询失败: ${resource}/${name}"
  if [[ "$LC_STATE" == "absent" ]]; then
    : >"${file}.absent"
    return 0
  fi

  [[ -z "$namespace" ]] || args+=(-n "$namespace")
  kubectl "${args[@]}" | python3 -c '
import json, sys
obj = json.load(sys.stdin)
obj.pop("status", None)
meta = obj.get("metadata", {})
for key in ("creationTimestamp", "generation", "managedFields", "resourceVersion", "selfLink", "uid"):
    meta.pop(key, None)
annotations = meta.get("annotations", {})
annotations.pop("kubectl.kubernetes.io/last-applied-configuration", None)
json.dump(obj, sys.stdout)
' >"$file" || fatal "创建快照失败: ${resource}/${name}"
}

snapshot_pre_install_state() {
  SNAPSHOT_DIR="$(mktemp -d -t kube-aiops-install.XXXXXX)"
  snapshot_resource serviceaccount "$SERVICE_ACCOUNT" "$NAMESPACE" "${SNAPSHOT_DIR}/serviceaccount.json"
  snapshot_resource clusterrole k8sgpt-clusterrole "" "${SNAPSHOT_DIR}/clusterrole.json"
  snapshot_resource clusterrolebinding k8sgpt-clusterrole-binding "" "${SNAPSHOT_DIR}/clusterrolebinding.json"

  lc_kubectl_state crd k8sgpts.core.k8sgpt.ai
  [[ "$LC_STATE" != "error" ]] || fatal "无法查询 K8sGPT CRD"
  if [[ "$LC_STATE" == "present" ]]; then
    snapshot_resource k8sgpt "$K8SGPT_NAME" "$NAMESPACE" "${SNAPSHOT_DIR}/k8sgpt.json"
  else
    : >"${SNAPSHOT_DIR}/k8sgpt.json.absent"
  fi
  SNAPSHOTS_READY=true
}

restore_snapshot() {
  local resource="$1"
  local name="$2"
  local namespace="$3"
  local file="$4"

  if [[ -f "$file" ]]; then
    kubectl apply --server-side --force-conflicts --field-manager=kube-aiops-rollback -f "$file" || return 1
    return 0
  fi
  [[ -f "${file}.absent" ]] || return 0
  lc_kubectl_state "$resource" "$name" "$namespace"
  [[ "$LC_STATE" != "error" ]] || return 1
  if [[ "$LC_STATE" == "present" ]]; then
    if [[ -n "$namespace" ]]; then
      kubectl delete "$resource" "$name" -n "$namespace" --wait=true --timeout="$TIMEOUT"
    else
      kubectl delete "$resource" "$name" --wait=true --timeout="$TIMEOUT"
    fi
  fi
  return 0
}

restore_pre_install_state() {
  local failures=0

  [[ "$SNAPSHOTS_READY" == "true" ]] || return 0
  restore_snapshot serviceaccount "$SERVICE_ACCOUNT" "$NAMESPACE" "${SNAPSHOT_DIR}/serviceaccount.json" || failures=$((failures + 1))
  restore_snapshot clusterrole k8sgpt-clusterrole "" "${SNAPSHOT_DIR}/clusterrole.json" || failures=$((failures + 1))
  restore_snapshot clusterrolebinding k8sgpt-clusterrole-binding "" "${SNAPSHOT_DIR}/clusterrolebinding.json" || failures=$((failures + 1))
  restore_snapshot k8sgpt "$K8SGPT_NAME" "$NAMESPACE" "${SNAPSHOT_DIR}/k8sgpt.json" || failures=$((failures + 1))
  return "$failures"
}

rollback_failed_install() {
  local failures=0
  local restore_failures=0

  [[ "$HELM_UPGRADE_SUCCEEDED" == "true" ]] || return 0
  if [[ "$NEW_RELEASE" == "true" ]]; then
    log "ERROR: 新装后置步骤失败，卸载本次 Helm Release"
    lc_helm_state "$RELEASE_NAME" "$NAMESPACE"
    if [[ "$LC_STATE" == "error" ]]; then
      failures=$((failures + 1))
    elif [[ "$LC_STATE" == "present" ]]; then
      helm uninstall "$RELEASE_NAME" -n "$NAMESPACE" --wait --timeout "$TIMEOUT" || failures=$((failures + 1))
    fi
  else
    log "ERROR: 升级后置步骤失败，回滚至 Helm revision=${PREVIOUS_REVISION}"
    helm rollback "$RELEASE_NAME" "$PREVIOUS_REVISION" -n "$NAMESPACE" \
      --wait --cleanup-on-fail --timeout "$TIMEOUT" || failures=$((failures + 1))
  fi

  restore_pre_install_state || restore_failures=$?
  failures=$((failures + restore_failures))

  lc_helm_state "$RELEASE_NAME" "$NAMESPACE"
  if [[ "$LC_STATE" == "error" ]]; then
    failures=$((failures + 1))
  elif [[ "$NEW_RELEASE" == "true" && "$LC_STATE" != "absent" ]]; then
    failures=$((failures + 1))
  elif [[ "$NEW_RELEASE" == "false" && ("$LC_STATE" != "present" || "$LC_OUTPUT" != "deployed") ]]; then
    failures=$((failures + 1))
  fi
  return "$failures"
}

on_exit() {
  local rc="$1"
  local rollback_failures=0
  local release_failed=false

  trap - EXIT ERR
  if ((rc != 0)); then
    rollback_failed_install || rollback_failures=$?
    if ((rollback_failures > 0)); then
      log "ERROR: 回滚存在 ${rollback_failures} 项失败，必须人工检查 Helm/RBAC/K8sGPT 残留"
    fi
  fi

  if ! lc_release_lock; then
    release_failed=true
    log "ERROR: 生命周期 Lease 未能正常释放；它将在租期到期后可被接管"
  fi
  if [[ -n "$SNAPSHOT_DIR" && -d "$SNAPSHOT_DIR" && "$SNAPSHOT_DIR" == /tmp/kube-aiops-install.* ]]; then
    rm -rf -- "$SNAPSHOT_DIR"
  fi

  if ((rc == 0)) && [[ "$release_failed" == "true" ]]; then rc=1; fi
  ((rc == 0)) || log "ERROR: 安装未完成 exit=${rc} rollback_failures=${rollback_failures}"
  exit "$rc"
}
trap 'on_exit $?' EXIT

log "Phase 1.1 安装开始 operation=${OPERATION_ID}"
for cmd in kubectl helm python3; do require_cmd "$cmd"; done
python3 -c 'import yaml' >/dev/null 2>&1 || fatal "缺少 Python PyYAML；先执行 python3 -m pip install pyyaml"
[[ "$LOCK_WAIT_SECONDS" =~ ^[0-9]+$ ]] || fatal "LOCK_WAIT_SECONDS 必须是非负整数"
for file in namespace.yaml serviceaccount.yaml clusterrole.yaml clusterrolebinding.yaml values-production.yaml k8sgpt.yaml; do
  require_file "${DEPLOY_DIR}/${file}"
done

CONTEXT="$(kubectl config current-context 2>/dev/null || true)"
[[ -n "$CONTEXT" ]] || fatal "未获取到 kubectl current-context"
log "Kubernetes Context: ${CONTEXT}"
kubectl version --request-timeout=10s >/dev/null

# 首次集群变更前完成全部业务前置条件检查。
require_resource_state present namespace "$NAMESPACE" || fatal "Namespace 不存在: ${NAMESPACE}；先执行 make bootstrap"
require_resource_state present secret "$SECRET_NAME" "$NAMESPACE" || fatal "Secret 不存在: ${NAMESPACE}/${SECRET_NAME}；先执行 make bootstrap-secret"
SECRET_KEY="$(kubectl get secret "$SECRET_NAME" -n "$NAMESPACE" -o jsonpath='{.data.openai-api-key}')"
[[ -n "$SECRET_KEY" ]] || fatal "Secret ${SECRET_NAME} 缺少或包含空 key: openai-api-key"

lc_acquire_lock "$NAMESPACE" "$LOCK_NAME" "$OPERATION_ID" "$LOCK_WAIT_SECONDS" || fatal "无法获取生命周期 Lease"
log "已获取生命周期 Lease: ${NAMESPACE}/${LOCK_NAME}"

# Helm 操作前确认固定名称的集群级资源未被其它安装占用。
assert_cluster_resource_owned_or_absent clusterrole k8sgpt-clusterrole
assert_cluster_resource_owned_or_absent clusterrolebinding k8sgpt-clusterrole-binding
snapshot_pre_install_state

lc_helm_state "$RELEASE_NAME" "$NAMESPACE"
[[ "$LC_STATE" != "error" ]] || fatal "无法判断 Helm Release 是否存在"
if [[ "$LC_STATE" == "absent" ]]; then
  NEW_RELEASE=true
else
  [[ "$LC_OUTPUT" == "deployed" ]] || fatal "Helm Release 当前状态不是 deployed: ${LC_OUTPUT:-unknown}"
  PREVIOUS_REVISION="$(helm history "$RELEASE_NAME" -n "$NAMESPACE" -o json |
    python3 -c 'import json,sys; items=json.load(sys.stdin); print(items[-1]["revision"] if items else "")')"
  [[ "$PREVIOUS_REVISION" =~ ^[0-9]+$ ]] || fatal "无法记录升级前 Helm revision"
fi

log "配置 K8sGPT Helm Repository"
helm repo add k8sgpt https://charts.k8sgpt.ai/ --force-update >/dev/null
helm repo update k8sgpt >/dev/null

log "安装/升级 K8sGPT Operator version=${OPERATOR_VERSION}"
helm upgrade --install "$RELEASE_NAME" k8sgpt/k8sgpt-operator \
  -n "$NAMESPACE" --version "$OPERATOR_VERSION" \
  -f "${DEPLOY_DIR}/values-production.yaml" \
  --post-renderer "${ROOT_DIR}/scripts/helm-post-renderer.py" \
  --atomic --cleanup-on-fail --wait --timeout "$TIMEOUT"
HELM_UPGRADE_SUCCEEDED=true
lc_renew_lock || fatal "Helm 完成后无法续租生命周期 Lease"

# Post-renderer 已阻止宽权限进入 API；这里再次 apply Git 基线以检测/修复漂移。
log "应用 Phase 1.1 RBAC Hardening"
for manifest in serviceaccount.yaml clusterrole.yaml clusterrolebinding.yaml; do
  kubectl apply --server-side --force-conflicts --field-manager=kube-aiops -f "${DEPLOY_DIR}/${manifest}"
done

check_denied() {
  local result rc=0
  result="$(kubectl auth can-i "$@" --as="system:serviceaccount:${NAMESPACE}:${SERVICE_ACCOUNT}" 2>&1)" || rc=$?
  if [[ "$result" == "no" ]]; then return 0; fi
  ((rc == 0)) || fatal "RBAC 查询失败: $* result=${result:-unknown}"
  fatal "安全基线失败: $* result=${result:-unknown}"
}
check_denied get secrets
check_denied get pods --subresource=log
check_denied patch deployments.apps
check_denied delete pods

kubectl apply -f "${DEPLOY_DIR}/k8sgpt.yaml"

OPERATOR_DEPLOY="$(kubectl get deploy -n "$NAMESPACE" -l app.kubernetes.io/name=k8sgpt-operator \
  -o jsonpath='{.items[0].metadata.name}')"
[[ -n "$OPERATOR_DEPLOY" ]] || fatal "未发现 Operator Deployment"
kubectl rollout status deployment/"$OPERATOR_DEPLOY" -n "$NAMESPACE" --timeout="$TIMEOUT"

kubectl wait --for=create deployment/"$K8SGPT_NAME" -n "$NAMESPACE" --timeout="$TIMEOUT"
kubectl rollout status deployment/"$K8SGPT_NAME" -n "$NAMESPACE" --timeout="$TIMEOUT"

HELM_UPGRADE_SUCCEEDED=false
log "安装完成；下一步执行: make verify"
