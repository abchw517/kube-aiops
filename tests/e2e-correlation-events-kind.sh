#!/usr/bin/env bash
set -Eeuo pipefail

ROOT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
# shellcheck source=config/platform-versions.env
source "${ROOT_DIR}/config/platform-versions.env"

readonly CLUSTER_NAME="${EVENTS_KIND_CLUSTER_NAME:-kube-aiops-events-e2e}"
readonly NODE_IMAGE="${KIND_NODE_IMAGE}"
readonly API_IMAGE="kube-aiops-api:events-e2e"
readonly SA="system:serviceaccount:kube-aiops-system:kube-aiops-api"
PORT_FORWARD_PID=""

log() { printf '[events-e2e] %s\n' "$*"; }
fail() { printf '[events-e2e][ERROR] %s\n' "$*" >&2; exit 1; }

cleanup() {
  if [[ -n "$PORT_FORWARD_PID" ]]; then
    kill "$PORT_FORWARD_PID" >/dev/null 2>&1 || true
    wait "$PORT_FORWARD_PID" >/dev/null 2>&1 || true
  fi
  kind delete cluster --name "$CLUSTER_NAME" >/dev/null 2>&1 || true
}
trap cleanup EXIT

for cmd in kind kubectl docker curl python3; do
  command -v "$cmd" >/dev/null 2>&1 || fail "缺少命令: $cmd"
done

expect_can_i() {
  local expected="$1"
  shift
  local actual
  actual="$(kubectl auth can-i "$@" --as="$SA" 2>/dev/null || true)"
  [[ "$actual" == "$expected" ]] || fail "kubectl auth can-i $* 预期 ${expected}，实际 ${actual:-<empty>}"
}

cd "$ROOT_DIR"
kind create cluster --name "$CLUSTER_NAME" --image "$NODE_IMAGE" --wait 120s

log "安装最小 Result CRD 与 API RBAC"
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
kubectl apply -f deploy/api/namespace.yaml
kubectl apply -f deploy/api/serviceaccount.yaml
kubectl apply -f deploy/api/clusterrole.yaml
kubectl apply -f deploy/api/clusterrolebinding.yaml

log "验证 Phase 2.2 Events 最小权限与负向权限"
expect_can_i yes get events
expect_can_i yes list events
expect_can_i yes watch events
expect_can_i no get secrets
expect_can_i no get pods --subresource=log
expect_can_i no create events
expect_can_i no update events
expect_can_i no patch events
expect_can_i no delete events
expect_can_i no deletecollection events

log "创建 Finding 目标资源、真实 Event 与 Result"
kubectl create namespace events-e2e
kubectl run demo-pod -n events-e2e --image=registry.invalid.example/kube-aiops/demo:not-exist --restart=Never >/dev/null

event_time="$(date -u +'%Y-%m-%dT%H:%M:%SZ')"
kubectl apply -f - <<EOF >/dev/null
apiVersion: v1
kind: Event
metadata:
  name: phase22-backoff
  namespace: events-e2e
involvedObject:
  apiVersion: v1
  kind: Pod
  namespace: events-e2e
  name: demo-pod
reason: Phase22BackOff
message: 'Authorization: Bearer phase22-redaction-token container restart detected'
source:
  component: kube-aiops-e2e
type: Warning
firstTimestamp: ${event_time}
lastTimestamp: ${event_time}
count: 1
EOF

kubectl apply -f - <<'EOF'
apiVersion: core.k8sgpt.ai/v1alpha1
kind: Result
metadata:
  name: events-finding
  namespace: k8sgpt-operator-system
  labels:
    k8sgpts.k8sgpt.ai/name: k8sgpt-engine
    k8sgpts.k8sgpt.ai/namespace: k8sgpt-operator-system
spec:
  backend: openai
  kind: Pod
  name: events-e2e/demo-pod
  error:
    - text: CrashLoopBackOff
  details: Pod restart finding used by Phase 2.2 Events correlation E2E.
  targetRef:
    apiVersion: v1
    kind: Pod
    namespace: events-e2e
    name: demo-pod
EOF

log "构建并运行 API"
docker build --pull=false -t "$API_IMAGE" .
docker save "$API_IMAGE" | docker exec -i "${CLUSTER_NAME}-control-plane" \
  ctr --namespace=k8s.io images import - >/dev/null
kubectl apply -f deploy/api/deployment.yaml
kubectl set image deployment/kube-aiops-api -n kube-aiops-system api="$API_IMAGE" >/dev/null
kubectl set env deployment/kube-aiops-api -n kube-aiops-system SECURITY_MODE=development >/dev/null
kubectl apply -f deploy/api/service.yaml
kubectl rollout status deployment/kube-aiops-api -n kube-aiops-system --timeout=120s

kubectl port-forward -n kube-aiops-system service/kube-aiops-api 18082:8080 >/tmp/kube-aiops-events-port-forward.log 2>&1 &
PORT_FORWARD_PID=$!
for _ in $(seq 1 30); do
  if curl --fail --silent http://127.0.0.1:18082/healthz >/dev/null 2>&1; then
    break
  fi
  sleep 1
done
curl --fail --silent http://127.0.0.1:18082/readyz >/dev/null || fail "/readyz 未就绪"

log "验证真实 Event 经 Correlation API 规范化且敏感内容被裁剪"
correlation_json="$(curl --fail --silent http://127.0.0.1:18082/api/v1/findings/events-finding/correlation)"
CORRELATION_JSON="$correlation_json" python3 - <<'PY'
import json
import os

payload = json.loads(os.environ["CORRELATION_JSON"])
if payload.get("findingId") != "events-finding":
    raise SystemExit("findingId mismatch")

sources = {item.get("source"): item.get("state") for item in payload.get("sources", [])}
if sources.get("kubernetes-events") != "available":
    raise SystemExit(f"kubernetes-events source not available: {sources}")
for source in ("prometheus", "loki", "alertmanager"):
    if sources.get(source) != "disabled":
        raise SystemExit(f"unexpected {source} state: {sources.get(source)}")

matches = [signal for signal in payload.get("signals", []) if signal.get("name") == "Phase22BackOff"]
if len(matches) != 1:
    raise SystemExit(f"expected one Phase22BackOff signal, got {matches}")
signal = matches[0]
if signal.get("source") != "kubernetes-events" or signal.get("type") != "event":
    raise SystemExit(f"unexpected signal source/type: {signal}")
if signal.get("severity") != "warning" or signal.get("count", 0) < 1:
    raise SystemExit(f"unexpected signal severity/count: {signal}")
resource = signal.get("resource") or {}
if resource.get("kind") != "Pod" or resource.get("namespace") != "events-e2e" or resource.get("name") != "demo-pod":
    raise SystemExit(f"unexpected resource: {resource}")
summary = signal.get("summary", "")
if "phase22-redaction-token" in summary or "[REDACTED:credential]" not in summary:
    raise SystemExit(f"credential sanitizer boundary failed: {summary}")

raw = os.environ["CORRELATION_JSON"]
for forbidden in (
    '"metadata"', '"involvedObject"', '"source":{"component"', '"reportingComponent"',
    '"spec"', '"managedFields"', 'phase22-redaction-token',
):
    if forbidden in raw:
        raise SystemExit(f"raw Event or sensitive field leaked: {forbidden}")
PY

log "Phase 2.2 Kubernetes Events Correlation E2E 通过"
