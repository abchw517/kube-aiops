#!/usr/bin/env bash
set -Eeuo pipefail

readonly NAMESPACE="k8sgpt-operator-system"
readonly RELEASE_NAME="k8sgpt-operator"
readonly K8SGPT_NAME="k8sgpt-engine"
readonly LOCK_NAME="kube-aiops-lifecycle"

PASS=0
WARN=0
FAIL=0

log() { printf '[status] %s\n' "$*"; }
pass() { PASS=$((PASS + 1)); log "PASS: $*"; }
warn() { WARN=$((WARN + 1)); log "WARN: $*"; }
fail() { FAIL=$((FAIL + 1)); log "FAIL: $*" >&2; }

OUTPUT=""
ERROR=""
RC=0
capture() {
  local error_file
  error_file="$(mktemp -t kube-aiops-status.XXXXXX)"
  OUTPUT=""
  ERROR=""
  RC=0
  OUTPUT="$("$@" 2>"$error_file")" || RC=$?
  ERROR="$(<"$error_file")"
  rm -f -- "$error_file"
}

error_detail() { printf '%s' "${ERROR:-exit=${RC}, no error detail}"; }

for command_name in kubectl helm python3; do
  if command -v "$command_name" >/dev/null 2>&1; then
    pass "命令可用: ${command_name}"
  else
    fail "缺少命令: ${command_name}"
  fi
done

if ((FAIL > 0)); then
  log "SUMMARY: pass=${PASS} warn=${WARN} fail=${FAIL}"
  exit 1
fi

capture kubectl config current-context
if ((RC == 0)) && [[ -n "$OUTPUT" ]]; then
  pass "Kubernetes Context: ${OUTPUT}"
else
  fail "无法获取 current-context: $(error_detail)"
fi

capture kubectl version --request-timeout=10s
if ((RC == 0)); then
  pass "Kubernetes API 可访问"
else
  fail "Kubernetes API 不可访问: $(error_detail)"
  log "SUMMARY: pass=${PASS} warn=${WARN} fail=${FAIL}"
  exit 1
fi

capture helm status "$RELEASE_NAME" -n "$NAMESPACE" -o json
if ((RC == 0)); then
  HELM_SUMMARY="$(python3 -c '
import json, sys
obj = json.load(sys.stdin)
print(f"status={obj.get('"'"'info'"'"', {}).get('"'"'status'"'"', '"'"'unknown'"'"')} revision={obj.get('"'"'version'"'"', '"'"'unknown'"'"')}")
' <<<"$OUTPUT")"
  if [[ "$HELM_SUMMARY" == status=deployed* ]]; then
    pass "Helm Release ${HELM_SUMMARY}"
  else
    fail "Helm Release 未部署: ${HELM_SUMMARY}"
  fi
else
  fail "Helm Release 查询失败: $(error_detail)"
fi

capture helm list -n "$NAMESPACE" --all --filter "^${RELEASE_NAME}$" -o json
if ((RC != 0)); then
  fail "Helm Release 身份查询失败: $(error_detail)"
elif HELM_CHART="$(python3 -c '
import json, sys
items = [item for item in json.load(sys.stdin) if item.get("name") == sys.argv[1]]
if len(items) != 1:
    raise SystemExit(1)
print(items[0].get("chart", ""))
' "$RELEASE_NAME" <<<"$OUTPUT")" && [[ "$HELM_CHART" == "k8sgpt-operator-0.2.29" ]]; then
  pass "Helm Release Chart 身份正确: ${HELM_CHART}"
else
  fail "Helm Release Chart 身份非预期: ${HELM_CHART:-unknown}"
fi

capture kubectl get lease "$LOCK_NAME" -n "$NAMESPACE" --ignore-not-found=true -o json
if ((RC != 0)); then
  fail "生命周期 Lease 查询失败: $(error_detail)"
elif [[ -z "$OUTPUT" ]]; then
  pass "当前没有生命周期操作持锁"
else
  LEASE_SUMMARY="$(python3 -c '
from datetime import datetime, timezone
import json, sys
spec = json.load(sys.stdin).get("spec", {})
holder = spec.get("holderIdentity", "")
renew = spec.get("renewTime") or spec.get("acquireTime")
duration = int(spec.get("leaseDurationSeconds", 0) or 0)
active = bool(holder and renew and duration > 0)
if active:
    renewed = datetime.fromisoformat(renew.replace("Z", "+00:00"))
    active = (datetime.now(timezone.utc) - renewed).total_seconds() < duration
state = "active" if active else "inactive"
print(f"{state}|holder={holder or '"'"'none'"'"'} renewTime={renew or '"'"'unknown'"'"'} duration={duration}s")
' <<<"$OUTPUT")"
  if [[ "$LEASE_SUMMARY" == inactive\|* ]]; then
    pass "当前没有有效生命周期操作锁: ${LEASE_SUMMARY#inactive|}"
  else
    warn "生命周期操作持锁: ${LEASE_SUMMARY#active|}"
  fi
fi

capture kubectl get deployment -n "$NAMESPACE" -l app.kubernetes.io/name=k8sgpt-operator -o json
if ((RC != 0)); then
  fail "Operator Deployment 查询失败: $(error_detail)"
elif DEPLOYMENT_SUMMARY="$(python3 -c '
import json, sys
items = json.load(sys.stdin).get("items", [])
if not items:
    print("not found")
    raise SystemExit(1)
problems = []
summary = []
for item in items:
    meta, spec, status = item.get("metadata", {}), item.get("spec", {}), item.get("status", {})
    name = meta.get("name", "<unnamed>")
    desired = int(spec.get("replicas", 1))
    ready = status.get("readyReplicas", 0)
    summary.append(f"{name}={ready}/{desired}")
    if (desired < 1 or
            status.get("observedGeneration", 0) < meta.get("generation", 1) or
            ready != desired or status.get("availableReplicas", 0) != desired or
            status.get("unavailableReplicas", 0) != 0):
        problems.append(name)
print(" ".join(summary))
raise SystemExit(1 if problems else 0)
' <<<"$OUTPUT")"; then
  pass "Operator Ready: ${DEPLOYMENT_SUMMARY}"
else
  fail "Operator 未完全 Ready: ${DEPLOYMENT_SUMMARY:-invalid response}"
fi

capture kubectl get deployment "$K8SGPT_NAME" -n "$NAMESPACE" --ignore-not-found=true -o json
if ((RC != 0)); then
  fail "Engine Deployment 查询失败: $(error_detail)"
elif [[ -z "$OUTPUT" ]]; then
  fail "Engine Deployment 不存在"
elif ENGINE_SUMMARY="$(python3 -c '
import json, sys
item = json.load(sys.stdin)
meta, spec, status = item.get("metadata", {}), item.get("spec", {}), item.get("status", {})
desired = int(spec.get("replicas", 1))
ready = status.get("readyReplicas", 0)
print(f"ready={ready}/{desired} available={status.get('"'"'availableReplicas'"'"', 0)}")
ok = (desired >= 1 and
      status.get("observedGeneration", 0) >= meta.get("generation", 1) and
      ready == desired and status.get("availableReplicas", 0) == desired and
      status.get("unavailableReplicas", 0) == 0)
raise SystemExit(0 if ok else 1)
' <<<"$OUTPUT")"; then
  pass "Engine Ready: ${ENGINE_SUMMARY}"
else
  fail "Engine 未完全 Ready: ${ENGINE_SUMMARY:-invalid response}"
fi

capture kubectl get k8sgpt "$K8SGPT_NAME" -n "$NAMESPACE" --ignore-not-found=true -o json
if ((RC != 0)); then
  fail "K8sGPT CR 查询失败: $(error_detail)"
elif [[ -z "$OUTPUT" ]]; then
  fail "K8sGPT CR 不存在"
else
  LAST_ANALYSIS_ERROR="$(python3 -c 'import json,sys; print(json.load(sys.stdin).get("status",{}).get("lastAnalysisError",""))' <<<"$OUTPUT")"
  if [[ -z "$LAST_ANALYSIS_ERROR" ]]; then
    pass "K8sGPT 最近一次分析无错误"
  else
    warn "K8sGPT 最近一次分析错误: ${LAST_ANALYSIS_ERROR}"
  fi
fi

capture kubectl get results -n "$NAMESPACE" \
  -l "k8sgpts.k8sgpt.ai/name=${K8SGPT_NAME},k8sgpts.k8sgpt.ai/namespace=${NAMESPACE}" -o json
if ((RC == 0)); then
  RESULT_COUNT="$(python3 -c 'import json,sys; print(len(json.load(sys.stdin).get("items", [])))' <<<"$OUTPUT")"
  pass "Result CR 数量=${RESULT_COUNT}"
else
  fail "Result CR 查询失败: $(error_detail)"
fi

log "SUMMARY: pass=${PASS} warn=${WARN} fail=${FAIL}"
((FAIL == 0))
