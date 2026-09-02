#!/usr/bin/env bash
set -Eeuo pipefail

ROOT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
# shellcheck source=config/platform-versions.env
source "${ROOT_DIR}/config/platform-versions.env"

readonly CLUSTER_NAME="${KIND_CLUSTER_NAME:-kube-aiops-v136-smoke}"
readonly EXPECTED_KUBERNETES_VERSION="${KUBERNETES_VERSION}"
readonly EXPECTED_KIND_VERSION="${KIND_VERSION}"
readonly NODE_IMAGE="${KIND_NODE_IMAGE}"

log() { printf '[k8s-v1.36-smoke] %s\n' "$*"; }
fail() { printf '[k8s-v1.36-smoke][ERROR] %s\n' "$*" >&2; exit 1; }

cleanup() {
  kind delete cluster --name "${CLUSTER_NAME}" >/dev/null 2>&1 || true
}
trap cleanup EXIT

for cmd in kind kubectl python3; do
  command -v "${cmd}" >/dev/null 2>&1 || fail "缺少依赖命令: ${cmd}"
done

actual_kind="$(kind version | awk '{print $2; exit}')"
[[ "${actual_kind}" == "${EXPECTED_KIND_VERSION}" ]] || \
  fail "Kind 版本不匹配: expected=${EXPECTED_KIND_VERSION}, actual=${actual_kind}"

client_version="$(kubectl version --client -o json | python3 -c 'import json,sys; print(json.load(sys.stdin)["clientVersion"]["gitVersion"])')"
[[ "${client_version}" == "${EXPECTED_KUBERNETES_VERSION}" ]] || \
  fail "kubectl 版本不匹配: expected=${EXPECTED_KUBERNETES_VERSION}, actual=${client_version}"

log "创建 ${EXPECTED_KUBERNETES_VERSION} Kind 集群"
kind create cluster \
  --name "${CLUSTER_NAME}" \
  --image "${NODE_IMAGE}" \
  --wait 120s

server_version="$(kubectl version -o json | python3 -c 'import json,sys; print(json.load(sys.stdin)["serverVersion"]["gitVersion"])')"
[[ "${server_version}" == "${EXPECTED_KUBERNETES_VERSION}" ]] || \
  fail "API Server 版本不匹配: expected=${EXPECTED_KUBERNETES_VERSION}, actual=${server_version}"

node_version="$(kubectl get nodes -o json | python3 -c 'import json,sys; versions={n["status"]["nodeInfo"]["kubeletVersion"] for n in json.load(sys.stdin)["items"]}; print(",".join(sorted(versions)))')"
[[ "${node_version}" == "${EXPECTED_KUBERNETES_VERSION}" ]] || \
  fail "kubelet 版本不匹配: expected=${EXPECTED_KUBERNETES_VERSION}, actual=${node_version}"

log "验证 kube-aiops 依赖的 Kubernetes v1.36 stable API groups"
for endpoint in \
  /api/v1 \
  /apis/apps/v1 \
  /apis/rbac.authorization.k8s.io/v1 \
  /apis/batch/v1 \
  /apis/networking.k8s.io/v1 \
  /apis/autoscaling/v2; do
  kubectl get --raw="${endpoint}" >/dev/null || fail "API discovery 失败: ${endpoint}"
done

kubectl api-resources >/dev/null
kubectl auth can-i get pods --all-namespaces >/dev/null

log "Kubernetes ${EXPECTED_KUBERNETES_VERSION} / Kind ${EXPECTED_KIND_VERSION} API smoke: PASS"
