#!/usr/bin/env bash
set -Eeuo pipefail

readonly CLUSTER_NAME="${KIND_CLUSTER_NAME:-kube-aiops-e2e}"
readonly NODE_IMAGE="${KIND_NODE_IMAGE:-kindest/node:v1.34.8@sha256:02722c2dedddcfc00febf5d27fbeb9b7b2c14294c82109ff4a85d89ac9ba3256}"
ROOT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
FAULT_BIN="$(mktemp -d -t kube-aiops-fault-bin.XXXXXX)"
REAL_KUBECTL="$(command -v kubectl || true)"

log() { printf '[e2e] %s\n' "$*"; }
cleanup() {
  kind delete cluster --name "$CLUSTER_NAME" >/dev/null 2>&1 || true
  if [[ -d "$FAULT_BIN" && "$FAULT_BIN" == /tmp/kube-aiops-fault-bin.* ]]; then
    rm -rf -- "$FAULT_BIN"
  fi
}
trap cleanup EXIT

for cmd in kind kubectl helm; do
  command -v "$cmd" >/dev/null 2>&1 || { echo "ERROR: 缺少命令: $cmd" >&2; exit 1; }
done
[[ -n "$REAL_KUBECTL" ]] || { echo "ERROR: 缺少真实 kubectl 路径" >&2; exit 1; }
ln -s "${ROOT_DIR}/tests/kubectl-fault-wrapper.sh" "${FAULT_BIN}/kubectl"

kind create cluster --name "$CLUSTER_NAME" --image "$NODE_IMAGE" --wait 120s
cd "$ROOT_DIR"

log "验证外部同名集群级 RBAC 不会被接管"
kubectl create clusterrole k8sgpt-clusterrole --verb=get --resource=pods
kubectl create clusterrolebinding k8sgpt-clusterrole-binding \
  --clusterrole=k8sgpt-clusterrole --serviceaccount=default:default
make bootstrap
OPENAI_TOKEN="e2e-placeholder-not-a-real-token" make bootstrap-secret
if bash ./install.sh; then
  echo "ERROR: 外部同名 RBAC 存在时安装不应成功" >&2
  exit 1
fi
if helm status k8sgpt-operator -n k8sgpt-operator-system >/dev/null 2>&1; then
  echo "ERROR: 所有权检查失败后不应存在 Helm Release" >&2
  exit 1
fi
kubectl delete clusterrolebinding k8sgpt-clusterrole-binding
kubectl delete clusterrole k8sgpt-clusterrole
PURGE_SECRET=true PURGE_NAMESPACE=true make uninstall

log "验证缺少 Namespace 时安装不会修改集群"
if bash ./install.sh; then
  echo "ERROR: 无前置条件时安装不应成功" >&2
  exit 1
fi
if helm status k8sgpt-operator -n k8sgpt-operator-system >/dev/null 2>&1; then
  echo "ERROR: 前置检查失败后不应存在 Helm Release" >&2
  exit 1
fi

make bootstrap
log "验证缺少 Secret 时安装不会创建 Helm Release"
if bash ./install.sh; then
  echo "ERROR: 缺少 Secret 时安装不应成功" >&2
  exit 1
fi
if helm status k8sgpt-operator -n k8sgpt-operator-system >/dev/null 2>&1; then
  echo "ERROR: 缺少 Secret 时不应存在 Helm Release" >&2
  exit 1
fi

OPENAI_TOKEN="e2e-placeholder-not-a-real-token" make bootstrap-secret

log "首次安装"
make install
log "重复安装（幂等）"
make install
make verify

log "验证已有 Release 的 Helm 后置失败会回滚并恢复健康状态"
FAULT_MARKER="$(mktemp -t kube-aiops-ssa-fault.XXXXXX)"
if REAL_KUBECTL="$REAL_KUBECTL" FAULT_MODE=fail_ssa_once FAULT_MARKER="$FAULT_MARKER" \
  PATH="${FAULT_BIN}:${PATH}" bash ./install.sh; then
  echo "ERROR: 注入 SSA 故障后安装不应成功" >&2
  exit 1
fi
rm -f -- "$FAULT_MARKER"
[[ "$(helm status k8sgpt-operator -n k8sgpt-operator-system -o json |
  python3 -c 'import json,sys; print(json.load(sys.stdin)["info"]["status"])')" == "deployed" ]]
make verify

log "验证生命周期 Lease 阻止并发安装"
kubectl create -f - >/dev/null <<'EOF'
apiVersion: coordination.k8s.io/v1
kind: Lease
metadata:
  name: kube-aiops-lifecycle
  namespace: k8sgpt-operator-system
spec:
  holderIdentity: external-release-system
  leaseDurationSeconds: 1800
  acquireTime: "2099-01-01T00:00:00Z"
  renewTime: "2099-01-01T00:00:00Z"
EOF
if LOCK_WAIT_SECONDS=0 bash ./install.sh; then
  echo "ERROR: Lease 被外部操作持有时安装不应成功" >&2
  exit 1
fi
kubectl delete lease kube-aiops-lifecycle -n k8sgpt-operator-system
helm status k8sgpt-operator -n k8sgpt-operator-system >/dev/null

log "验证卸载不会把 Forbidden 查询误判为资源不存在"
if REAL_KUBECTL="$REAL_KUBECTL" FAULT_MODE=deny_clusterrole_get \
  PATH="${FAULT_BIN}:${PATH}" bash ./uninstall.sh; then
  echo "ERROR: 所有权查询 Forbidden 时卸载不应成功" >&2
  exit 1
fi
helm status k8sgpt-operator -n k8sgpt-operator-system >/dev/null

log "验证卸载拒绝删除失去所有权标识的集群级 RBAC"
kubectl annotate clusterrole k8sgpt-clusterrole kube-aiops.io/owner-
kubectl label clusterrole k8sgpt-clusterrole app.kubernetes.io/part-of-
if PURGE_SECRET=true PURGE_NAMESPACE=true bash ./uninstall.sh; then
  echo "ERROR: 所有权标识缺失时卸载不应成功" >&2
  exit 1
fi
helm status k8sgpt-operator -n k8sgpt-operator-system >/dev/null
kubectl annotate clusterrole k8sgpt-clusterrole kube-aiops.io/owner=phase-1.1
kubectl label clusterrole k8sgpt-clusterrole app.kubernetes.io/part-of=kube-aiops

log "首次完全卸载"
PURGE_SECRET=true PURGE_NAMESPACE=true PURGE_DEMO=true make uninstall
log "重复卸载（幂等）"
PURGE_SECRET=true PURGE_NAMESPACE=true PURGE_DEMO=true make uninstall

for resource in \
  "namespace/k8sgpt-operator-system" \
  "clusterrole/k8sgpt-clusterrole" \
  "clusterrolebinding/k8sgpt-clusterrole-binding"; do
  if kubectl get "$resource" >/dev/null 2>&1; then
    echo "ERROR: 卸载后资源残留: $resource" >&2
    exit 1
  fi
done
kubectl get crd k8sgpts.core.k8sgpt.ai >/dev/null
kubectl get crd results.core.k8sgpt.ai >/dev/null
log "Kind 生命周期 E2E 全部通过"
