#!/usr/bin/env bash
set -Eeuo pipefail

readonly CLUSTER_NAME="${KIND_CLUSTER_NAME:-kube-aiops-e2e}"
readonly NODE_IMAGE="${KIND_NODE_IMAGE:-kindest/node:v1.34.0}"
ROOT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"

log() { printf '[e2e] %s\n' "$*"; }
cleanup() { kind delete cluster --name "$CLUSTER_NAME" >/dev/null 2>&1 || true; }
trap cleanup EXIT

for cmd in kind kubectl helm; do
  command -v "$cmd" >/dev/null 2>&1 || { echo "ERROR: 缺少命令: $cmd" >&2; exit 1; }
done

kind create cluster --name "$CLUSTER_NAME" --image "$NODE_IMAGE" --wait 120s
cd "$ROOT_DIR"

log "验证缺少 Namespace 时安装不会修改集群"
if bash ./install.sh; then
  echo "ERROR: 无前置条件时安装不应成功" >&2
  exit 1
fi
! helm status k8sgpt-operator -n k8sgpt-operator-system >/dev/null 2>&1

make bootstrap
log "验证缺少 Secret 时安装不会创建 Helm Release"
if bash ./install.sh; then
  echo "ERROR: 缺少 Secret 时安装不应成功" >&2
  exit 1
fi
! helm status k8sgpt-operator -n k8sgpt-operator-system >/dev/null 2>&1

OPENAI_TOKEN="e2e-placeholder-not-a-real-token" make bootstrap-secret

log "首次安装"
make install
log "重复安装（幂等）"
make install
make verify

log "首次完全卸载"
PURGE_SECRET=true PURGE_NAMESPACE=true PURGE_DEMO=true make uninstall
log "重复卸载（幂等）"
PURGE_SECRET=true PURGE_NAMESPACE=true PURGE_DEMO=true make uninstall

! kubectl get namespace k8sgpt-operator-system >/dev/null 2>&1
! kubectl get clusterrole k8sgpt-clusterrole >/dev/null 2>&1
! kubectl get clusterrolebinding k8sgpt-clusterrole-binding >/dev/null 2>&1
kubectl get crd k8sgpts.core.k8sgpt.ai >/dev/null
kubectl get crd results.core.k8sgpt.ai >/dev/null
log "Kind E2E 全部通过"
