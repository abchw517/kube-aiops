#!/usr/bin/env bash
set -Eeuo pipefail

readonly CLUSTER_NAME="${API_KIND_CLUSTER_NAME:-kube-aiops-api-e2e}"
readonly NODE_IMAGE="${KIND_NODE_IMAGE:-kindest/node:v1.34.8@sha256:02722c2dedddcfc00febf5d27fbeb9b7b2c14294c82109ff4a85d89ac9ba3256}"
readonly API_IMAGE="kube-aiops-api:dev"
ROOT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
PORT_FORWARD_PID=""

log() { printf '[api-e2e] %s\n' "$*"; }
fail() { printf '[api-e2e][ERROR] %s\n' "$*" >&2; exit 1; }

cleanup() {
  if [[ -n "$PORT_FORWARD_PID" ]]; then
    kill "$PORT_FORWARD_PID" >/dev/null 2>&1 || true
    wait "$PORT_FORWARD_PID" >/dev/null 2>&1 || true
  fi
  kind delete cluster --name "$CLUSTER_NAME" >/dev/null 2>&1 || true
}
trap cleanup EXIT

for cmd in kind kubectl docker curl grep; do
  command -v "$cmd" >/dev/null 2>&1 || fail "缺少命令: $cmd"
done

cd "$ROOT_DIR"
kind create cluster --name "$CLUSTER_NAME" --image "$NODE_IMAGE" --wait 120s

log "安装最小 Result CRD"
kubectl apply -f - <<'EOF'
apiVersion: apiextensions.k8s.io/v1
kind: CustomResourceDefinition
metadata:
  name: results.core.k8sgpt.ai
spec:
  group: core.k8sgpt.ai
  scope: Namespaced
  names:
    plural: results
    singular: result
    kind: Result
  versions:
    - name: v1alpha1
      served: true
      storage: true
      schema:
        openAPIV3Schema:
          type: object
          x-kubernetes-preserve-unknown-fields: true
EOF

log "应用 Phase 1.2.2 API RBAC"
kubectl apply -f deploy/api/namespace.yaml
kubectl apply -f deploy/api/serviceaccount.yaml
kubectl apply -f deploy/api/clusterrole.yaml
kubectl apply -f deploy/api/clusterrolebinding.yaml

readonly SA="system:serviceaccount:kube-aiops-system:kube-aiops-api"
expect_can_i() {
  local expected="$1"
  shift
  local actual

  # `kubectl auth can-i` 在回答 `no` 时会返回非零退出码。这里的 DENY 是
  # 安全验收的预期结果，不能被全局 `set -e` 当成脚本异常提前终止。
  actual="$(kubectl auth can-i "$@" --as="$SA" 2>/dev/null || true)"
  [[ "$actual" == "$expected" ]] || fail "kubectl auth can-i $* 预期 ${expected}，实际 ${actual:-<empty>}"
}

log "验证精确 RBAC"
expect_can_i yes list results.core.k8sgpt.ai
expect_can_i yes list namespaces
expect_can_i yes get pods
expect_can_i yes get deployments.apps
expect_can_i no list pods
expect_can_i no get secrets
expect_can_i no get pods/log
expect_can_i no get statefulsets.apps
expect_can_i no patch deployments.apps
expect_can_i no delete pods

log "创建 Resource API 测试对象"
kubectl create namespace api-e2e
kubectl apply -f - <<'EOF'
apiVersion: v1
kind: Pod
metadata:
  name: demo-pod
  namespace: api-e2e
  annotations:
    should-not-leak: secret-looking-metadata
spec:
  containers:
    - name: demo
      image: registry.invalid.example/kube-aiops/demo:not-exist
      env:
        - name: SHOULD_NOT_LEAK
          value: do-not-return-me
EOF

log "构建并加载 API 镜像"
docker build --pull=false -t "$API_IMAGE" .
kind load docker-image "$API_IMAGE" --name "$CLUSTER_NAME"

kubectl apply -f deploy/api/deployment.yaml
kubectl apply -f deploy/api/service.yaml
kubectl rollout status deployment/kube-aiops-api -n kube-aiops-system --timeout=120s

log "启动本地端口转发"
kubectl port-forward -n kube-aiops-system service/kube-aiops-api 18080:8080 >/tmp/kube-aiops-api-port-forward.log 2>&1 &
PORT_FORWARD_PID=$!

for _ in $(seq 1 30); do
  if curl --fail --silent http://127.0.0.1:18080/healthz >/dev/null 2>&1; then
    break
  fi
  sleep 1
done
curl --fail --silent http://127.0.0.1:18080/healthz >/dev/null || fail "/healthz 未就绪"
curl --fail --silent http://127.0.0.1:18080/readyz >/dev/null || fail "/readyz 未就绪"

log "验证 Cluster / Namespace API"
clusters="$(curl --fail --silent http://127.0.0.1:18080/api/v1/clusters)"
grep -q '"id":"local"' <<<"$clusters" || fail "Cluster API 未返回 local"

namespaces="$(curl --fail --silent http://127.0.0.1:18080/api/v1/clusters/local/namespaces)"
grep -q '"name":"api-e2e"' <<<"$namespaces" || fail "Namespace API 未返回 api-e2e"

log "验证 Pod Resource Detail 与响应裁剪"
pod_json="$(curl --fail --silent http://127.0.0.1:18080/api/v1/clusters/local/resources/pods/api-e2e/demo-pod)"
grep -q '"kind":"Pod"' <<<"$pod_json" || fail "Pod API 未返回 Pod"
grep -q '"name":"demo-pod"' <<<"$pod_json" || fail "Pod API 名称错误"
if grep -Eq 'SHOULD_NOT_LEAK|do-not-return-me|should-not-leak|"spec"|"annotations"|"managedFields"' <<<"$pod_json"; then
  fail "Pod API 泄露了被禁止的原始对象字段"
fi

log "验证 Deployment Resource Detail"
deployment_json="$(curl --fail --silent http://127.0.0.1:18080/api/v1/clusters/local/resources/deployments/kube-aiops-system/kube-aiops-api)"
grep -q '"kind":"Deployment"' <<<"$deployment_json" || fail "Deployment API 未返回 Deployment"
grep -q '"name":"kube-aiops-api"' <<<"$deployment_json" || fail "Deployment API 名称错误"
if grep -Eq '"spec"|"annotations"|"managedFields"' <<<"$deployment_json"; then
  fail "Deployment API 泄露了被禁止的原始对象字段"
fi

log "验证 API 白名单与错误映射"
status="$(curl --silent --output /tmp/kube-aiops-unsupported.json --write-out '%{http_code}' \
  http://127.0.0.1:18080/api/v1/clusters/local/resources/secrets/api-e2e/demo)"
[[ "$status" == "400" ]] || fail "Secret kind 预期 HTTP 400，实际 ${status}"
grep -q 'UNSUPPORTED_RESOURCE_KIND' /tmp/kube-aiops-unsupported.json || fail "缺少 UNSUPPORTED_RESOURCE_KIND"

status="$(curl --silent --output /tmp/kube-aiops-missing.json --write-out '%{http_code}' \
  http://127.0.0.1:18080/api/v1/clusters/local/resources/pods/api-e2e/not-found)"
[[ "$status" == "404" ]] || fail "不存在 Pod 预期 HTTP 404，实际 ${status}"
grep -q 'RESOURCE_NOT_FOUND' /tmp/kube-aiops-missing.json || fail "缺少 RESOURCE_NOT_FOUND"

status="$(curl --silent --output /tmp/kube-aiops-cluster.json --write-out '%{http_code}' \
  http://127.0.0.1:18080/api/v1/clusters/other/namespaces)"
[[ "$status" == "404" ]] || fail "未知 Cluster 预期 HTTP 404，实际 ${status}"
grep -q 'CLUSTER_NOT_FOUND' /tmp/kube-aiops-cluster.json || fail "缺少 CLUSTER_NOT_FOUND"

log "Phase 1.2.2 Kubernetes API E2E 通过"
