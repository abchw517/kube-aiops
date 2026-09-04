#!/usr/bin/env bash
set -Eeuo pipefail

ROOT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
# shellcheck source=config/platform-versions.env
source "${ROOT_DIR}/config/platform-versions.env"

readonly CLUSTER_NAME="${API_KIND_CLUSTER_NAME:-kube-aiops-api-e2e}"
readonly NODE_IMAGE="${KIND_NODE_IMAGE}"
readonly API_IMAGE="kube-aiops-api:dev"
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

for cmd in kind kubectl docker curl grep python3; do
  command -v "$cmd" >/dev/null 2>&1 || fail "缺少命令: $cmd"
done

cd "$ROOT_DIR"
kind create cluster --name "$CLUSTER_NAME" --image "$NODE_IMAGE" --wait 120s

log "安装最小 Result CRD 与 readiness 目标 Namespace"
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
kubectl create namespace k8sgpt-operator-system

log "应用 Portal Backend API RBAC"
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
expect_can_i yes get results.core.k8sgpt.ai
expect_can_i yes list namespaces
expect_can_i yes get pods
expect_can_i yes get deployments.apps
expect_can_i no list pods
expect_can_i no get secrets
# pods/log 必须用 --subresource=log；`pods/log` 位置参数会被 kubectl 当作 TYPE/NAME。
expect_can_i no get pods --subresource=log
expect_can_i no get statefulsets.apps
expect_can_i no create pods
expect_can_i no update deployments.apps
expect_can_i no patch deployments.apps
expect_can_i no delete pods
expect_can_i no deletecollection pods

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

log "构建并导入 API 镜像到 Kind containerd"
docker build --pull=false -t "$API_IMAGE" .
# 避免依赖 `kind load docker-image` 对 Runner/containerd snapshotter 的探测逻辑；
# 直接把 Docker image archive 导入当前单节点 Kind control-plane 的 k8s.io namespace。
docker save "$API_IMAGE" | docker exec -i "${CLUSTER_NAME}-control-plane" \
  ctr --namespace=k8s.io images import - >/dev/null

docker exec "${CLUSTER_NAME}-control-plane" \
  ctr --namespace=k8s.io images list name=="docker.io/library/${API_IMAGE}" >/dev/null

kubectl apply -f deploy/api/deployment.yaml
# Phase 1.2 readonly E2E intentionally exercises the compatibility surface. Production manifests
# remain fail-closed; only this test deployment opts into development mode.
kubectl set env deployment/kube-aiops-api -n kube-aiops-system SECURITY_MODE=development >/dev/null
kubectl apply -f deploy/api/service.yaml
kubectl rollout status deployment/kube-aiops-api -n kube-aiops-system --timeout=120s

log "创建 Phase 1.2.3 Finding 测试 Result"
kubectl apply -f - <<'EOF'
apiVersion: core.k8sgpt.ai/v1alpha1
kind: Result
metadata:
  name: pod-finding
  namespace: k8sgpt-operator-system
  labels:
    k8sgpts.k8sgpt.ai/name: k8sgpt-engine
    k8sgpts.k8sgpt.ai/namespace: k8sgpt-operator-system
spec:
  backend: openai
  kind: Pod
  name: api-e2e/demo-pod
  error:
    - text: CrashLoopBackOff
      sensitive:
        - unmasked: must-not-leak-value
          masked: redacted
  details: Pod repeatedly fails during startup.
  targetRef:
    apiVersion: v1
    kind: Pod
    namespace: api-e2e
    name: demo-pod
---
apiVersion: core.k8sgpt.ai/v1alpha1
kind: Result
metadata:
  name: deployment-finding
  namespace: k8sgpt-operator-system
  labels:
    k8sgpts.k8sgpt.ai/name: k8sgpt-engine
    k8sgpts.k8sgpt.ai/namespace: k8sgpt-operator-system
spec:
  backend: openai
  kind: Deployment
  name: kube-aiops-system/kube-aiops-api
  error:
    - text: AvailableReplicasMismatch
  details: Deployment available replicas do not match the expected state.
  targetRef:
    apiVersion: apps/v1
    kind: Deployment
    namespace: kube-aiops-system
    name: kube-aiops-api
---
apiVersion: core.k8sgpt.ai/v1alpha1
kind: Result
metadata:
  name: foreign-finding
  namespace: k8sgpt-operator-system
  labels:
    k8sgpts.k8sgpt.ai/name: foreign-engine
    k8sgpts.k8sgpt.ai/namespace: k8sgpt-operator-system
spec:
  backend: openai
  kind: Pod
  name: api-e2e/foreign-pod
  error:
    - text: MustNeverAppear
  details: Foreign K8sGPT instance result.
EOF

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

log "验证 Finding List、实例隔离和敏感字段裁剪"
findings="$(curl --fail --silent 'http://127.0.0.1:18080/api/v1/findings?cluster=local')"
FINDINGS_JSON="$findings" python3 - <<'PY'
import json, os, sys
payload = json.loads(os.environ['FINDINGS_JSON'])
items = payload.get('items', [])
ids = {item.get('id') for item in items}
if ids != {'pod-finding', 'deployment-finding'}:
    raise SystemExit(f'unexpected finding ids: {ids}')
raw = os.environ['FINDINGS_JSON']
for forbidden in ('foreign-finding', 'MustNeverAppear', 'must-not-leak-value', 'sensitive', 'unmasked', '"spec"', '"labels"', '"status"'):
    if forbidden in raw:
        raise SystemExit(f'forbidden content leaked: {forbidden}')
if any(item.get('severity') != 'warning' for item in items):
    raise SystemExit('Phase 1.2.3 severity baseline must be warning')
PY

log "验证 Finding Filter 一致性"
for query in \
  'namespace=api-e2e' \
  'kind=Deployment' \
  'problem=crashloop'; do
  filtered="$(curl --fail --silent "http://127.0.0.1:18080/api/v1/findings?${query}")"
  FILTERED_JSON="$filtered" python3 - <<'PY'
import json, os
items = json.loads(os.environ['FILTERED_JSON']).get('items', [])
if len(items) != 1:
    raise SystemExit(f'expected one filtered finding, got {len(items)}')
PY
done

warning_filtered="$(curl --fail --silent 'http://127.0.0.1:18080/api/v1/findings?severity=warning')"
WARNING_JSON="$warning_filtered" python3 - <<'PY'
import json, os
if len(json.loads(os.environ['WARNING_JSON']).get('items', [])) != 2:
    raise SystemExit('warning filter must return two findings')
PY

critical_filtered="$(curl --fail --silent 'http://127.0.0.1:18080/api/v1/findings?severity=critical')"
CRITICAL_JSON="$critical_filtered" python3 - <<'PY'
import json, os
if json.loads(os.environ['CRITICAL_JSON']).get('items', []) != []:
    raise SystemExit('critical filter must be empty in Phase 1.2.3 baseline')
PY

log "验证 Finding keyset continue 分页"
page1="$(curl --fail --silent 'http://127.0.0.1:18080/api/v1/findings?limit=1')"
read -r first_id continue_token < <(PAGE_JSON="$page1" python3 - <<'PY'
import json, os
p = json.loads(os.environ['PAGE_JSON'])
items = p.get('items', [])
if len(items) != 1:
    raise SystemExit('first page must contain one finding')
token = p.get('pagination', {}).get('continue', '')
if not token:
    raise SystemExit('first page must contain continue token')
print(items[0]['id'], token)
PY
)
page2="$(curl --fail --silent "http://127.0.0.1:18080/api/v1/findings?limit=1&continue=${continue_token}")"
SECOND_JSON="$page2" FIRST_ID="$first_id" python3 - <<'PY'
import json, os
items = json.loads(os.environ['SECOND_JSON']).get('items', [])
if len(items) != 1:
    raise SystemExit('second page must contain one finding')
if items[0].get('id') == os.environ['FIRST_ID']:
    raise SystemExit('pagination returned duplicate finding')
PY

log "验证 Finding Detail 与 foreign Result 隔离"
detail="$(curl --fail --silent http://127.0.0.1:18080/api/v1/findings/pod-finding)"
grep -q '"id":"pod-finding"' <<<"$detail" || fail "Finding Detail 未返回 pod-finding"
if grep -Eq 'must-not-leak-value|sensitive|unmasked|"spec"|"labels"' <<<"$detail"; then
  fail "Finding Detail 泄露了 Result 原始或敏感字段"
fi

status="$(curl --silent --output /tmp/kube-aiops-foreign-finding.json --write-out '%{http_code}' \
  http://127.0.0.1:18080/api/v1/findings/foreign-finding)"
[[ "$status" == "404" ]] || fail "foreign Finding Detail 预期 HTTP 404，实际 ${status}"
grep -q 'FINDING_NOT_FOUND' /tmp/kube-aiops-foreign-finding.json || fail "缺少 FINDING_NOT_FOUND"

log "验证 Finding Summary"
summary="$(curl --fail --silent http://127.0.0.1:18080/api/v1/findings/summary)"
SUMMARY_JSON="$summary" python3 - <<'PY'
import json, os
s = json.loads(os.environ['SUMMARY_JSON'])
if s.get('total') != 2:
    raise SystemExit(f"unexpected total: {s.get('total')}")
if s.get('bySeverity', {}).get('warning') != 2:
    raise SystemExit('warning summary must be 2')
if s.get('bySeverity', {}).get('critical') != 0 or s.get('bySeverity', {}).get('info') != 0:
    raise SystemExit('critical/info summary must be zero')
if s.get('byKind', {}).get('Pod') != 1 or s.get('byKind', {}).get('Deployment') != 1:
    raise SystemExit(f"unexpected byKind: {s.get('byKind')}")
if s.get('byNamespace', {}).get('api-e2e') != 1 or s.get('byNamespace', {}).get('kube-aiops-system') != 1:
    raise SystemExit(f"unexpected byNamespace: {s.get('byNamespace')}")
PY

filtered_summary="$(curl --fail --silent 'http://127.0.0.1:18080/api/v1/findings/summary?namespace=api-e2e')"
FILTERED_SUMMARY_JSON="$filtered_summary" python3 - <<'PY'
import json, os
s = json.loads(os.environ['FILTERED_SUMMARY_JSON'])
if s.get('total') != 1 or s.get('byKind', {}).get('Pod') != 1:
    raise SystemExit(f'filtered summary mismatch: {s}')
PY

log "验证 Finding 参数错误"
status="$(curl --silent --output /tmp/kube-aiops-finding-limit.json --write-out '%{http_code}' \
  'http://127.0.0.1:18080/api/v1/findings?limit=201')"
[[ "$status" == "400" ]] || fail "非法 limit 预期 HTTP 400，实际 ${status}"
grep -q 'INVALID_LIMIT' /tmp/kube-aiops-finding-limit.json || fail "缺少 INVALID_LIMIT"

status="$(curl --silent --output /tmp/kube-aiops-finding-cursor.json --write-out '%{http_code}' \
  'http://127.0.0.1:18080/api/v1/findings?continue=not-a-valid-cursor')"
[[ "$status" == "400" ]] || fail "非法 continue 预期 HTTP 400，实际 ${status}"
grep -q 'INVALID_CONTINUE_TOKEN' /tmp/kube-aiops-finding-cursor.json || fail "缺少 INVALID_CONTINUE_TOKEN"

status="$(curl --silent --output /tmp/kube-aiops-finding-cluster.json --write-out '%{http_code}' \
  'http://127.0.0.1:18080/api/v1/findings?cluster=other')"
[[ "$status" == "404" ]] || fail "未知 Finding cluster 预期 HTTP 404，实际 ${status}"
grep -q 'CLUSTER_NOT_FOUND' /tmp/kube-aiops-finding-cluster.json || fail "缺少 CLUSTER_NOT_FOUND"

log "验证 Resource API 白名单与错误映射"
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

log "Phase 1.2.3 Finding Domain Kubernetes E2E 通过"
