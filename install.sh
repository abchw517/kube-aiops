#!/usr/bin/env bash
set -Eeuo pipefail

# Phase 1.1 使用固定资源身份。任意 Namespace 需要真正的模板层和唯一的集群级 RBAC 名称，
# 因此本阶段不暴露不完整的 Namespace/资源名覆盖参数。
readonly NAMESPACE="k8sgpt-operator-system"
readonly RELEASE_NAME="k8sgpt-operator"
readonly SECRET_NAME="k8sgpt-openai-secret"
readonly SERVICE_ACCOUNT="k8sgpt"
readonly K8SGPT_NAME="k8sgpt-engine"
OPERATOR_VERSION="${OPERATOR_VERSION:-0.2.27}"
TIMEOUT="${TIMEOUT:-5m}"

ROOT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
DEPLOY_DIR="${ROOT_DIR}/deploy/k8sgpt"
NEW_RELEASE=false
CR_CREATED=false

log() { printf '[%s] %s\n' "$(date '+%Y-%m-%d %H:%M:%S')" "$*"; }
fatal() { log "ERROR: $*"; exit 1; }
require_cmd() { command -v "$1" >/dev/null 2>&1 || fatal "缺少命令: $1"; }
require_file() { [[ -f "$1" ]] || fatal "文件不存在: $1"; }

rollback_new_install() {
  local rc="$1"
  ((rc == 0)) && return 0
  trap - ERR
  log "ERROR: 安装失败，开始清理本次新建资源"
  if [[ "$CR_CREATED" == "true" ]]; then
    kubectl delete k8sgpt "$K8SGPT_NAME" -n "$NAMESPACE" --ignore-not-found=true --wait=true --timeout="$TIMEOUT" || true
  fi
  if [[ "$NEW_RELEASE" == "true" ]]; then
    helm uninstall "$RELEASE_NAME" -n "$NAMESPACE" --wait --timeout "$TIMEOUT" || true
    kubectl delete clusterrolebinding k8sgpt-clusterrole-binding --ignore-not-found=true || true
    kubectl delete clusterrole k8sgpt-clusterrole --ignore-not-found=true || true
  fi
  log "ERROR: 安装未完成 exit=${rc}"
}
trap 'rc=$?; rollback_new_install "$rc"; exit "$rc"' EXIT

log "Phase 1.1 安装开始"
for cmd in kubectl helm python3; do require_cmd "$cmd"; done
python3 -c 'import yaml' >/dev/null 2>&1 || fatal "缺少 Python PyYAML；先执行 python3 -m pip install pyyaml"
for file in namespace.yaml serviceaccount.yaml clusterrole.yaml clusterrolebinding.yaml values-production.yaml k8sgpt.yaml; do
  require_file "${DEPLOY_DIR}/${file}"
done

CONTEXT="$(kubectl config current-context 2>/dev/null || true)"
[[ -n "$CONTEXT" ]] || fatal "未获取到 kubectl current-context"
log "Kubernetes Context: ${CONTEXT}"
kubectl version --request-timeout=10s >/dev/null

# 首次集群变更前完成全部业务前置条件检查。
kubectl get namespace "$NAMESPACE" >/dev/null 2>&1 || fatal "Namespace 不存在: ${NAMESPACE}；先执行 make bootstrap"
kubectl get secret "$SECRET_NAME" -n "$NAMESPACE" >/dev/null 2>&1 || fatal "Secret 不存在: ${NAMESPACE}/${SECRET_NAME}；先执行 make bootstrap-secret"
SECRET_KEY="$(kubectl get secret "$SECRET_NAME" -n "$NAMESPACE" -o jsonpath='{.data.openai-api-key}')"
[[ -n "$SECRET_KEY" ]] || fatal "Secret ${SECRET_NAME} 缺少或包含空 key: openai-api-key"

if ! helm status "$RELEASE_NAME" -n "$NAMESPACE" >/dev/null 2>&1; then NEW_RELEASE=true; fi

log "配置 K8sGPT Helm Repository"
helm repo add k8sgpt https://charts.k8sgpt.ai/ --force-update >/dev/null
helm repo update k8sgpt >/dev/null

log "安装/升级 K8sGPT Operator version=${OPERATOR_VERSION}"
helm upgrade --install "$RELEASE_NAME" k8sgpt/k8sgpt-operator \
  -n "$NAMESPACE" --version "$OPERATOR_VERSION" \
  -f "${DEPLOY_DIR}/values-production.yaml" \
  --post-renderer "${ROOT_DIR}/scripts/helm-post-renderer.py" \
  --atomic --cleanup-on-fail --wait --timeout "$TIMEOUT"

# Post-renderer 已阻止宽权限进入 API；这里再次 apply Git 基线以检测/修复漂移。
log "应用 Phase 1.1 RBAC Hardening"
kubectl apply -f "${DEPLOY_DIR}/serviceaccount.yaml"
kubectl apply -f "${DEPLOY_DIR}/clusterrole.yaml"
kubectl apply -f "${DEPLOY_DIR}/clusterrolebinding.yaml"

for check in "get secrets" "get pods/log" "patch deployments.apps" "delete pods"; do
  read -r verb resource <<<"$check"
  result="$(kubectl auth can-i "$verb" "$resource" --as="system:serviceaccount:${NAMESPACE}:${SERVICE_ACCOUNT}" 2>/dev/null || true)"
  [[ "$result" == "no" ]] || fatal "安全基线失败: ${verb} ${resource} result=${result}"
done

if ! kubectl get k8sgpt "$K8SGPT_NAME" -n "$NAMESPACE" >/dev/null 2>&1; then CR_CREATED=true; fi
kubectl apply -f "${DEPLOY_DIR}/k8sgpt.yaml"

OPERATOR_DEPLOY="$(kubectl get deploy -n "$NAMESPACE" -l app.kubernetes.io/name=k8sgpt-operator -o jsonpath='{.items[0].metadata.name}')"
[[ -n "$OPERATOR_DEPLOY" ]] || fatal "未发现 Operator Deployment"
kubectl rollout status deployment/"$OPERATOR_DEPLOY" -n "$NAMESPACE" --timeout="$TIMEOUT"

trap - EXIT
log "安装完成；下一步执行: make verify"
