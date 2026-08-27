#!/usr/bin/env bash
set -Eeuo pipefail

readonly NAMESPACE="k8sgpt-operator-system"
readonly RELEASE_NAME="k8sgpt-operator"
readonly SECRET_NAME="k8sgpt-openai-secret"
readonly K8SGPT_NAME="k8sgpt-engine"
PURGE_SECRET="${PURGE_SECRET:-false}"
PURGE_NAMESPACE="${PURGE_NAMESPACE:-false}"
PURGE_DEMO="${PURGE_DEMO:-false}"
TIMEOUT="${TIMEOUT:-5m}"

log() { printf '[%s] %s\n' "$(date '+%Y-%m-%d %H:%M:%S')" "$*"; }
fatal() { log "ERROR: $*"; exit 1; }
for cmd in kubectl helm; do command -v "$cmd" >/dev/null 2>&1 || fatal "缺少命令: $cmd"; done

CONTEXT="$(kubectl config current-context 2>/dev/null || true)"
[[ -n "$CONTEXT" ]] || fatal "未获取到 kubectl current-context"
kubectl version --request-timeout=10s >/dev/null
log "Kubernetes Context: ${CONTEXT}"

if kubectl get k8sgpt "$K8SGPT_NAME" -n "$NAMESPACE" >/dev/null 2>&1; then
  log "删除并等待 K8sGPT CR"
  kubectl delete k8sgpt "$K8SGPT_NAME" -n "$NAMESPACE" --wait=true --timeout="$TIMEOUT"
fi

if helm status "$RELEASE_NAME" -n "$NAMESPACE" >/dev/null 2>&1; then
  log "卸载 Helm Release"
  helm uninstall "$RELEASE_NAME" -n "$NAMESPACE" --wait --timeout "$TIMEOUT"
fi

# 固定名称仅由 Phase 1.1 单实例基线使用。
kubectl delete clusterrolebinding k8sgpt-clusterrole-binding --ignore-not-found=true
kubectl delete clusterrole k8sgpt-clusterrole --ignore-not-found=true
kubectl delete serviceaccount k8sgpt -n "$NAMESPACE" --ignore-not-found=true

if [[ "$PURGE_DEMO" == "true" ]]; then
  kubectl delete namespace k8sgpt-demo --ignore-not-found=true --wait=true --timeout="$TIMEOUT"
fi
if [[ "$PURGE_SECRET" == "true" ]]; then
  kubectl delete secret "$SECRET_NAME" -n "$NAMESPACE" --ignore-not-found=true
fi
if [[ "$PURGE_NAMESPACE" == "true" ]]; then
  kubectl delete namespace "$NAMESPACE" --ignore-not-found=true --wait=true --timeout="$TIMEOUT"
fi

helm status "$RELEASE_NAME" -n "$NAMESPACE" >/dev/null 2>&1 && fatal "Helm Release 仍存在"
kubectl get k8sgpt "$K8SGPT_NAME" -n "$NAMESPACE" >/dev/null 2>&1 && fatal "K8sGPT CR 仍存在"
kubectl get clusterrole k8sgpt-clusterrole >/dev/null 2>&1 && fatal "ClusterRole 仍存在"
kubectl get clusterrolebinding k8sgpt-clusterrole-binding >/dev/null 2>&1 && fatal "ClusterRoleBinding 仍存在"
log "卸载完成；CRD 默认保留"
