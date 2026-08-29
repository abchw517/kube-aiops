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
readonly OPERATOR_CHART_SHA256="349d0f9d39fd0556c05c19fa0ddcc72cfde022b55a0b20877cae264f0737fe94"
OPERATOR_VERSION="${OPERATOR_VERSION:-0.2.29}"
TIMEOUT="${TIMEOUT:-5m}"
LOCK_WAIT_SECONDS="${LOCK_WAIT_SECONDS:-30}"
LEASE_DURATION_SECONDS="${LEASE_DURATION_SECONDS:-1800}"
LEASE_RENEW_INTERVAL_SECONDS="${LEASE_RENEW_INTERVAL_SECONDS:-30}"
MIGRATE_LEGACY_OWNERSHIP="${MIGRATE_LEGACY_OWNERSHIP:-false}"
ROLLBACK_STATE_DIR="${ROLLBACK_STATE_DIR:-${TMPDIR:-/tmp}}"

ROOT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
DEPLOY_DIR="${ROOT_DIR}/deploy/k8sgpt"
# shellcheck source=scripts/lifecycle-common.sh
source "${ROOT_DIR}/scripts/lifecycle-common.sh"

NEW_RELEASE=false
HELM_UPGRADE_SUCCEEDED=false
SNAPSHOTS_READY=false
PREVIOUS_REVISION=""
SNAPSHOT_DIR=""
SNAPSHOT_REPORT=""
PRESERVE_SNAPSHOT=false
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
  [[ -d "$ROLLBACK_STATE_DIR" ]] || fatal "回滚状态根目录不存在: ${ROLLBACK_STATE_DIR}"
  SNAPSHOT_DIR="$(mktemp -d "${ROLLBACK_STATE_DIR%/}/kube-aiops-install.XXXXXX")"
  chmod 0700 "$SNAPSHOT_DIR"
  SNAPSHOT_REPORT="${SNAPSHOT_DIR}/restore-report.tsv"
  printf 'operation\tresource\tresult\n' >"$SNAPSHOT_REPORT"
  python3 -c '
import json, os, sys
payload = {"operation_id": sys.argv[1], "release": sys.argv[2], "namespace": sys.argv[3], "previous_revision": sys.argv[4]}
fd = os.open(sys.argv[5], os.O_WRONLY | os.O_CREAT | os.O_EXCL, 0o600)
with os.fdopen(fd, "w", encoding="utf-8") as stream:
    json.dump(payload, stream, ensure_ascii=False, indent=2)
' "$OPERATION_ID" "$RELEASE_NAME" "$NAMESPACE" "$PREVIOUS_REVISION" "${SNAPSHOT_DIR}/operation.json"
  snapshot_resource serviceaccount "$SERVICE_ACCOUNT" "$NAMESPACE" "${SNAPSHOT_DIR}/serviceaccount.json"
  snapshot_resource clusterrole k8sgpt-clusterrole "" "${SNAPSHOT_DIR}/clusterrole.json"
  snapshot_resource clusterrolebinding k8sgpt-clusterrole-binding "" "${SNAPSHOT_DIR}/clusterrolebinding.json"
  snapshot_resource networkpolicy kube-aiops-egress-baseline "$NAMESPACE" "${SNAPSHOT_DIR}/networkpolicy.json"

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
    if ! kubectl apply --server-side --force-conflicts --field-manager=kube-aiops-rollback -f "$file"; then
      printf 'restore\t%s/%s\tfailed\n' "$resource" "$name" >>"$SNAPSHOT_REPORT"
      return 1
    fi
    printf 'restore\t%s/%s\tsucceeded\n' "$resource" "$name" >>"$SNAPSHOT_REPORT"
    return 0
  fi
  [[ -f "${file}.absent" ]] || return 0
  lc_kubectl_state "$resource" "$name" "$namespace"
  if [[ "$LC_STATE" == "error" ]]; then
    printf 'query\t%s/%s\tfailed\n' "$resource" "$name" >>"$SNAPSHOT_REPORT"
    return 1
  fi
  if [[ "$LC_STATE" == "present" ]]; then
    if [[ -n "$namespace" ]]; then
      kubectl delete "$resource" "$name" -n "$namespace" --wait=true --timeout="$TIMEOUT" || {
        printf 'delete\t%s/%s\tfailed\n' "$resource" "$name" >>"$SNAPSHOT_REPORT"
        return 1
      }
    else
      kubectl delete "$resource" "$name" --wait=true --timeout="$TIMEOUT" || {
        printf 'delete\t%s/%s\tfailed\n' "$resource" "$name" >>"$SNAPSHOT_REPORT"
        return 1
      }
    fi
  fi
  printf 'delete\t%s/%s\tsucceeded\n' "$resource" "$name" >>"$SNAPSHOT_REPORT"
  return 0
}

restore_pre_install_state() {
  local failures=0

  [[ "$SNAPSHOTS_READY" == "true" ]] || return 0
  restore_snapshot serviceaccount "$SERVICE_ACCOUNT" "$NAMESPACE" "${SNAPSHOT_DIR}/serviceaccount.json" || failures=$((failures + 1))
  restore_snapshot clusterrole k8sgpt-clusterrole "" "${SNAPSHOT_DIR}/clusterrole.json" || failures=$((failures + 1))
  restore_snapshot clusterrolebinding k8sgpt-clusterrole-binding "" "${SNAPSHOT_DIR}/clusterrolebinding.json" || failures=$((failures + 1))
  restore_snapshot networkpolicy kube-aiops-egress-baseline "$NAMESPACE" "${SNAPSHOT_DIR}/networkpolicy.json" || failures=$((failures + 1))
  restore_snapshot k8sgpt "$K8SGPT_NAME" "$NAMESPACE" "${SNAPSHOT_DIR}/k8sgpt.json" || failures=$((failures + 1))
  return "$failures"
}

rollback_failed_install() {
  local failures=0
  local restore_failures=0

  [[ "$HELM_UPGRADE_SUCCEEDED" == "true" ]] || return 0
  if ! lc_assert_lock_held; then
    printf 'rollback\tlease/%s\tfailed-lost-ownership\n' "$LOCK_NAME" >>"$SNAPSHOT_REPORT"
    log "ERROR: 已失去生命周期 Lease，拒绝与新持有者并发执行回滚"
    return 1
  fi
  if [[ "$NEW_RELEASE" == "true" ]]; then
    log "ERROR: 新装后置步骤失败，卸载本次 Helm Release"
    lc_helm_state "$RELEASE_NAME" "$NAMESPACE"
    if [[ "$LC_STATE" == "error" ]]; then
      failures=$((failures + 1))
      printf 'rollback\thelm/%s\tfailed-query\n' "$RELEASE_NAME" >>"$SNAPSHOT_REPORT"
    elif [[ "$LC_STATE" == "present" ]]; then
      if ! helm uninstall "$RELEASE_NAME" -n "$NAMESPACE" --wait --timeout "$TIMEOUT"; then
        failures=$((failures + 1))
        printf 'rollback\thelm/%s\tfailed-uninstall\n' "$RELEASE_NAME" >>"$SNAPSHOT_REPORT"
      fi
    fi
  else
    log "ERROR: 升级后置步骤失败，回滚至 Helm revision=${PREVIOUS_REVISION}"
    if ! helm rollback "$RELEASE_NAME" "$PREVIOUS_REVISION" -n "$NAMESPACE" \
      --wait --cleanup-on-fail --timeout "$TIMEOUT"; then
      failures=$((failures + 1))
      printf 'rollback\thelm/%s\tfailed-revision-%s\n' "$RELEASE_NAME" "$PREVIOUS_REVISION" >>"$SNAPSHOT_REPORT"
    fi
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
      PRESERVE_SNAPSHOT=true
      log "ERROR: 回滚存在 ${rollback_failures} 项失败，必须人工检查 Helm/RBAC/K8sGPT 残留"
      log "ERROR: 已保留 0700 回滚状态: ${SNAPSHOT_DIR}；对象报告: ${SNAPSHOT_REPORT}"
    fi
  fi

  if ! lc_release_lock; then
    release_failed=true
    log "ERROR: 生命周期 Lease 未能正常释放；它将在租期到期后可被接管"
  fi
  if [[ "$PRESERVE_SNAPSHOT" != "true" && -n "$SNAPSHOT_DIR" && -d "$SNAPSHOT_DIR" &&
    "$(basename "$SNAPSHOT_DIR")" == kube-aiops-install.* ]]; then
    rm -rf -- "$SNAPSHOT_DIR"
  fi

  if ((rc == 0)) && [[ "$release_failed" == "true" ]]; then rc=1; fi
  ((rc == 0)) || log "ERROR: 安装未完成 exit=${rc} rollback_failures=${rollback_failures}"
  exit "$rc"
}
trap 'on_exit $?' EXIT

log "Phase 1.1 安装开始 operation=${OPERATION_ID}"
for cmd in kubectl helm python3 curl sha256sum awk; do require_cmd "$cmd"; done
python3 -c 'import yaml' >/dev/null 2>&1 || fatal "缺少 Python PyYAML；先执行 python3 -m pip install pyyaml"
[[ "$LOCK_WAIT_SECONDS" =~ ^[0-9]+$ ]] || fatal "LOCK_WAIT_SECONDS 必须是非负整数"
[[ "$LEASE_DURATION_SECONDS" =~ ^[1-9][0-9]*$ ]] || fatal "LEASE_DURATION_SECONDS 必须是正整数"
[[ "$LEASE_RENEW_INTERVAL_SECONDS" =~ ^[1-9][0-9]*$ ]] || fatal "LEASE_RENEW_INTERVAL_SECONDS 必须是正整数"
((LEASE_RENEW_INTERVAL_SECONDS < LEASE_DURATION_SECONDS)) || fatal "Lease 续租间隔必须小于租期"
[[ "$MIGRATE_LEGACY_OWNERSHIP" == "true" || "$MIGRATE_LEGACY_OWNERSHIP" == "false" ]] ||
  fatal "MIGRATE_LEGACY_OWNERSHIP 只能为 true 或 false"
case "$OPERATOR_VERSION" in
  0.2.29) ;;
  *) fatal "不支持的 OPERATOR_VERSION=${OPERATOR_VERSION}；允许版本: 0.2.29" ;;
esac
for file in namespace.yaml serviceaccount.yaml clusterrole.yaml clusterrolebinding.yaml networkpolicy.yaml values-production.yaml k8sgpt.yaml; do
  require_file "${DEPLOY_DIR}/${file}"
done

CONTEXT="$(kubectl config current-context 2>/dev/null || true)"
[[ -n "$CONTEXT" ]] || fatal "未获取到 kubectl current-context"
log "Kubernetes Context: ${CONTEXT}"
kubectl version --request-timeout=10s >/dev/null

# 首次集群变更前完成全部业务前置条件检查。
require_resource_state present namespace "$NAMESPACE" || fatal "Namespace 不存在: ${NAMESPACE}；先执行 make bootstrap"
require_resource_state present secret "$SECRET_NAME" "$NAMESPACE" || fatal "Secret 不存在: ${NAMESPACE}/${SECRET_NAME}；先执行 make bootstrap-secret"
SECRET_KEY_STATE="$(kubectl get secret "$SECRET_NAME" -n "$NAMESPACE" \
  -o go-template='{{if index .data "openai-api-key"}}present{{else}}missing{{end}}')" ||
  fatal "无法检查 Secret key: ${NAMESPACE}/${SECRET_NAME}"
[[ "$SECRET_KEY_STATE" == "present" ]] || fatal "Secret ${SECRET_NAME} 缺少或包含空 key: openai-api-key"

lc_acquire_lock "$NAMESPACE" "$LOCK_NAME" "$OPERATION_ID" "$LOCK_WAIT_SECONDS" || fatal "无法获取生命周期 Lease"
lc_start_lock_heartbeat || fatal "无法启动生命周期 Lease heartbeat"
log "已获取生命周期 Lease: ${NAMESPACE}/${LOCK_NAME}"

# Helm 操作前确认固定名称的集群级资源未被其它安装占用。
lc_assert_cluster_resource_identity install clusterrole k8sgpt-clusterrole \
  "${DEPLOY_DIR}/clusterrole.yaml" "$MIGRATE_LEGACY_OWNERSHIP" || fatal "ClusterRole 所有权校验失败"
lc_assert_cluster_resource_identity install clusterrolebinding k8sgpt-clusterrole-binding \
  "${DEPLOY_DIR}/clusterrolebinding.yaml" "$MIGRATE_LEGACY_OWNERSHIP" || fatal "ClusterRoleBinding 所有权校验失败"

lc_helm_state "$RELEASE_NAME" "$NAMESPACE"
[[ "$LC_STATE" != "error" ]] || fatal "无法判断 Helm Release 是否存在"
if [[ "$LC_STATE" == "absent" ]]; then
  NEW_RELEASE=true
else
  [[ "$LC_OUTPUT" == "deployed" ]] || fatal "Helm Release 当前状态不是 deployed: ${LC_OUTPUT:-unknown}"
  lc_assert_helm_release_identity "$RELEASE_NAME" "$NAMESPACE" k8sgpt-operator 0.2.29 || fatal "Helm Release 身份校验失败"
  PREVIOUS_REVISION="$(helm history "$RELEASE_NAME" -n "$NAMESPACE" -o json |
    python3 -c 'import json,sys; items=json.load(sys.stdin); print(items[-1]["revision"] if items else "")')"
  [[ "$PREVIOUS_REVISION" =~ ^[0-9]+$ ]] || fatal "无法记录升级前 Helm revision"
fi
snapshot_pre_install_state

OPERATOR_CHART="${SNAPSHOT_DIR}/k8sgpt-operator-${OPERATOR_VERSION}.tgz"
OPERATOR_CHART_URL="https://charts.k8sgpt.ai/k8sgpt-operator-${OPERATOR_VERSION}.tgz"
log "下载并校验 K8sGPT Operator Chart sha256=${OPERATOR_CHART_SHA256}"
curl --proto '=https' --tlsv1.2 --location --fail --silent --show-error \
  --output "$OPERATOR_CHART" "$OPERATOR_CHART_URL"
printf '%s  %s\n' "$OPERATOR_CHART_SHA256" "$OPERATOR_CHART" | sha256sum --check --status ||
  fatal "Operator Chart SHA256 校验失败: ${OPERATOR_CHART_URL}"

log "安装/升级 K8sGPT Operator version=${OPERATOR_VERSION}"
lc_assert_lock_held || fatal "执行 Helm 前已失去生命周期 Lease"
helm upgrade --install "$RELEASE_NAME" "$OPERATOR_CHART" \
  -n "$NAMESPACE" \
  -f "${DEPLOY_DIR}/values-production.yaml" \
  --post-renderer "${ROOT_DIR}/scripts/helm-post-renderer.py" \
  --atomic --cleanup-on-fail --wait --timeout "$TIMEOUT"
HELM_UPGRADE_SUCCEEDED=true
lc_assert_lock_held || fatal "Helm 完成后生命周期 Lease 已失去"

# Post-renderer 已阻止宽权限进入 API；这里再次 apply Git 基线以检测/修复漂移。
log "应用 Phase 1.1 RBAC Hardening"
lc_assert_lock_held || fatal "应用 RBAC 前已失去生命周期 Lease"
for manifest in serviceaccount.yaml clusterrole.yaml clusterrolebinding.yaml; do
  kubectl apply --server-side --force-conflicts --field-manager=kube-aiops -f "${DEPLOY_DIR}/${manifest}"
done
kubectl apply --server-side --force-conflicts --field-manager=kube-aiops \
  -f "${DEPLOY_DIR}/networkpolicy.yaml"
for resource in clusterrole/k8sgpt-clusterrole clusterrolebinding/k8sgpt-clusterrole-binding; do
  kubectl label "$resource" app.kubernetes.io/instance="$RELEASE_NAME" --overwrite
done

check_denied() {
  local result decision rc=0
  result="$(kubectl auth can-i "$@" --as="system:serviceaccount:${NAMESPACE}:${SERVICE_ACCOUNT}" 2>&1)" || rc=$?
  decision="$(printf '%s\n' "$result" | awk 'NF {last=$0} END {print last}')"
  if [[ "$decision" == "no" ]]; then return 0; fi
  ((rc == 0)) || fatal "RBAC 查询失败: $* result=${result:-unknown}"
  fatal "安全基线失败: $* result=${decision:-unknown}"
}
check_denied get secrets
check_denied get pods --subresource=log
check_denied patch deployments.apps
check_denied delete pods
check_denied list daemonsets.apps
check_denied list networkpolicies.networking.k8s.io
check_denied list validatingwebhookconfigurations.admissionregistration.k8s.io --all-namespaces

lc_assert_lock_held || fatal "应用 K8sGPT CR 前已失去生命周期 Lease"
kubectl apply -f "${DEPLOY_DIR}/k8sgpt.yaml"

OPERATOR_DEPLOY="$(kubectl get deploy -n "$NAMESPACE" -l app.kubernetes.io/name=k8sgpt-operator \
  -o jsonpath='{.items[0].metadata.name}')"
[[ -n "$OPERATOR_DEPLOY" ]] || fatal "未发现 Operator Deployment"
kubectl rollout status deployment/"$OPERATOR_DEPLOY" -n "$NAMESPACE" --timeout="$TIMEOUT"

kubectl wait --for=create deployment/"$K8SGPT_NAME" -n "$NAMESPACE" --timeout="$TIMEOUT"
kubectl rollout status deployment/"$K8SGPT_NAME" -n "$NAMESPACE" --timeout="$TIMEOUT"

HELM_UPGRADE_SUCCEEDED=false
log "安装完成；下一步执行: make verify"
