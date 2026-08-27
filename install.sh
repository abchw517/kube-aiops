#!/usr/bin/env bash
set -Eeuo pipefail

NAMESPACE="${NAMESPACE:-k8sgpt-operator-system}"
OPERATOR_VERSION="${OPERATOR_VERSION:-0.2.27}"
RELEASE_NAME="${RELEASE_NAME:-k8sgpt-operator}"
SECRET_NAME="${SECRET_NAME:-k8sgpt-openai-secret}"
SERVICE_ACCOUNT="${SERVICE_ACCOUNT:-k8sgpt}"
TIMEOUT="${TIMEOUT:-5m}"

ROOT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
DEPLOY_DIR="${ROOT_DIR}/deploy/k8sgpt"

log() {
  printf '[%s] %s\n' "$(date '+%Y-%m-%d %H:%M:%S')" "$*"
}

fatal() {
  log "ERROR: $*"
  exit 1
}

on_error() {
  local line="$1"
  local cmd="$2"
  log "ERROR: 安装失败 line=${line}, command=${cmd}"
}
trap 'on_error "$LINENO" "$BASH_COMMAND"' ERR

require_cmd() {
  command -v "$1" >/dev/null 2>&1 || fatal "缺少命令: $1"
}

require_file() {
  [[ -f "$1" ]] || fatal "文件不存在: $1"
}

log "Phase 1.1 安装开始"

for cmd in kubectl helm; do
  require_cmd "$cmd"
done

for file in \
  "${DEPLOY_DIR}/namespace.yaml" \
  "${DEPLOY_DIR}/clusterrole.yaml" \
  "${DEPLOY_DIR}/clusterrolebinding.yaml" \
  "${DEPLOY_DIR}/values-production.yaml" \
  "${DEPLOY_DIR}/k8sgpt.yaml"; do
  require_file "$file"
done

CONTEXT="$(kubectl config current-context 2>/dev/null || true)"
[[ -n "$CONTEXT" ]] || fatal "未获取到 kubectl current-context"
log "Kubernetes Context: ${CONTEXT}"

kubectl version --request-timeout=10s >/dev/null

log "创建/确认 Namespace: ${NAMESPACE}"
kubectl apply -f "${DEPLOY_DIR}/namespace.yaml"

log "配置 K8sGPT Helm Repository"
helm repo add k8sgpt https://charts.k8sgpt.ai/ --force-update >/dev/null
helm repo update k8sgpt >/dev/null

log "安装/升级 K8sGPT Operator version=${OPERATOR_VERSION}"
helm upgrade --install "${RELEASE_NAME}" \
  k8sgpt/k8sgpt-operator \
  -n "${NAMESPACE}" \
  --version "${OPERATOR_VERSION}" \
  -f "${DEPLOY_DIR}/values-production.yaml" \
  --wait \
  --timeout "${TIMEOUT}"

log "应用 Phase 1.1 RBAC Hardening"
# dynamicRBAC=false 时 Helm Chart 会创建固定的 k8sgpt ServiceAccount / ClusterRole / ClusterRoleBinding。
# 这里使用 Git 管理的同名 ClusterRole 覆盖官方默认权限，移除 Secret 与 Pod Log 读取权限。
kubectl apply -f "${DEPLOY_DIR}/clusterrole.yaml"
kubectl apply -f "${DEPLOY_DIR}/clusterrolebinding.yaml"

log "验证 RBAC Hardening"
CAN_SECRET="$(kubectl auth can-i get secrets --as="system:serviceaccount:${NAMESPACE}:${SERVICE_ACCOUNT}" 2>/dev/null || true)"
[[ "$CAN_SECRET" == "no" ]] || fatal "安全基线失败: ${SERVICE_ACCOUNT} 仍可读取 secrets (result=${CAN_SECRET})"

CAN_PATCH="$(kubectl auth can-i patch deployments --as="system:serviceaccount:${NAMESPACE}:${SERVICE_ACCOUNT}" 2>/dev/null || true)"
[[ "$CAN_PATCH" == "no" ]] || fatal "安全基线失败: ${SERVICE_ACCOUNT} 仍可 patch deployments (result=${CAN_PATCH})"

CAN_DELETE="$(kubectl auth can-i delete pods --as="system:serviceaccount:${NAMESPACE}:${SERVICE_ACCOUNT}" 2>/dev/null || true)"
[[ "$CAN_DELETE" == "no" ]] || fatal "安全基线失败: ${SERVICE_ACCOUNT} 仍可 delete pods (result=${CAN_DELETE})"

log "检查 AI Provider Secret: ${SECRET_NAME}"
if ! kubectl get secret "${SECRET_NAME}" -n "${NAMESPACE}" >/dev/null 2>&1; then
  cat >&2 <<EOF
ERROR: 未发现 Secret ${NAMESPACE}/${SECRET_NAME}

请先创建：
  export OPENAI_TOKEN='replace-me'
  kubectl create secret generic ${SECRET_NAME} \\
    -n ${NAMESPACE} \\
    --from-literal=openai-api-key="\${OPENAI_TOKEN}"

Secret 不应提交到 Git 仓库。
EOF
  exit 1
fi

SECRET_KEY="$(kubectl get secret "${SECRET_NAME}" -n "${NAMESPACE}" -o jsonpath='{.data.openai-api-key}' 2>/dev/null || true)"
[[ -n "$SECRET_KEY" ]] || fatal "Secret ${SECRET_NAME} 缺少 key: openai-api-key"

log "部署 K8sGPT CR"
kubectl apply -f "${DEPLOY_DIR}/k8sgpt.yaml"

log "等待 K8sGPT Operator Deployment Ready"
OPERATOR_DEPLOY="$(kubectl get deploy -n "${NAMESPACE}" -l app.kubernetes.io/name=k8sgpt-operator -o jsonpath='{.items[0].metadata.name}' 2>/dev/null || true)"
if [[ -n "$OPERATOR_DEPLOY" ]]; then
  kubectl rollout status deployment/"${OPERATOR_DEPLOY}" -n "${NAMESPACE}" --timeout="${TIMEOUT}"
else
  log "WARN: 未通过标签发现 Operator Deployment，跳过 rollout status；请执行 make status 检查"
fi

log "安装完成"
log "下一步: make verify"
log "故障样例: make demo"
