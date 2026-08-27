#!/usr/bin/env bash
set -Eeuo pipefail

NAMESPACE="${NAMESPACE:-k8sgpt-operator-system}"
RELEASE_NAME="${RELEASE_NAME:-k8sgpt-operator}"
SECRET_NAME="${SECRET_NAME:-k8sgpt-openai-secret}"
K8SGPT_NAME="${K8SGPT_NAME:-k8sgpt-engine}"
PURGE_SECRET="${PURGE_SECRET:-false}"
PURGE_NAMESPACE="${PURGE_NAMESPACE:-false}"

log() {
  printf '[%s] %s\n' "$(date '+%Y-%m-%d %H:%M:%S')" "$*"
}

fatal() {
  log "ERROR: $*"
  exit 1
}

command -v kubectl >/dev/null 2>&1 || fatal "缺少命令: kubectl"
command -v helm >/dev/null 2>&1 || fatal "缺少命令: helm"

CONTEXT="$(kubectl config current-context 2>/dev/null || true)"
[[ -n "$CONTEXT" ]] || fatal "未获取到 kubectl current-context"
log "Kubernetes Context: ${CONTEXT}"

log "删除 K8sGPT CR: ${NAMESPACE}/${K8SGPT_NAME}"
kubectl delete k8sgpt "${K8SGPT_NAME}" -n "${NAMESPACE}" --ignore-not-found=true || true

log "卸载 Helm Release: ${RELEASE_NAME}"
if helm status "${RELEASE_NAME}" -n "${NAMESPACE}" >/dev/null 2>&1; then
  helm uninstall "${RELEASE_NAME}" -n "${NAMESPACE}"
else
  log "Helm Release 不存在，跳过"
fi

# 清理可能因手工 hardening 留下的同名 RBAC 资源。
log "清理 Phase 1.1 RBAC 资源"
kubectl delete clusterrole k8sgpt-clusterrole --ignore-not-found=true || true
kubectl delete clusterrolebinding k8sgpt-clusterrole-binding --ignore-not-found=true || true
kubectl delete serviceaccount k8sgpt -n "${NAMESPACE}" --ignore-not-found=true || true

if [[ "$PURGE_SECRET" == "true" ]]; then
  log "PURGE_SECRET=true，删除 Secret: ${SECRET_NAME}"
  kubectl delete secret "${SECRET_NAME}" -n "${NAMESPACE}" --ignore-not-found=true || true
else
  log "默认保留 Secret: ${NAMESPACE}/${SECRET_NAME}"
fi

if [[ "$PURGE_NAMESPACE" == "true" ]]; then
  log "PURGE_NAMESPACE=true，删除 Namespace: ${NAMESPACE}"
  kubectl delete namespace "${NAMESPACE}" --ignore-not-found=true || true
else
  log "默认保留 Namespace: ${NAMESPACE}"
fi

log "CRD 默认保留，避免影响其它 K8sGPT 实例或后续重新安装"
log "卸载完成"
