#!/usr/bin/env bash
set -Eeuo pipefail

readonly NAMESPACE="k8sgpt-operator-system"
readonly SERVICE_ACCOUNT="k8sgpt"
readonly K8SGPT_NAME="k8sgpt-engine"
readonly RELEASE_NAME="k8sgpt-operator"
readonly SECRET_NAME="k8sgpt-openai-secret"
ROOT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
STRICT_RESULTS="${STRICT_RESULTS:-false}"
RESULT_SINCE="${RESULT_SINCE:-}"
EXPECTED_RESULT_KINDS="${EXPECTED_RESULT_KINDS:-}"
REQUIRE_ANALYSIS_HEALTH="${REQUIRE_ANALYSIS_HEALTH:-false}"
EXPECTED_ANALYSIS_INTERVAL="${EXPECTED_ANALYSIS_INTERVAL:-5m}"

PASS=0
FAIL=0
WARN=0

log() {
  printf '[%s] %s\n' "$(date '+%Y-%m-%d %H:%M:%S')" "$*"
}

pass() {
  PASS=$((PASS + 1))
  log "PASS: $*"
}

fail() {
  FAIL=$((FAIL + 1))
  log "FAIL: $*"
}

warn() {
  WARN=$((WARN + 1))
  log "WARN: $*"
}

CAPTURE_OUTPUT=""
CAPTURE_ERROR=""
CAPTURE_RC=0
QUERY_STATE=""
QUERY_JSON=""

capture() {
  local error_file
  error_file="$(mktemp -t kube-aiops-verify.XXXXXX)"
  CAPTURE_OUTPUT=""
  CAPTURE_ERROR=""
  CAPTURE_RC=0
  CAPTURE_OUTPUT="$("$@" 2>"$error_file")" || CAPTURE_RC=$?
  CAPTURE_ERROR="$(<"$error_file")"
  rm -f -- "$error_file"
}

query_object() {
  local resource="$1"
  local name="$2"
  local namespace="${3:-}"
  local -a args=(get "$resource" "$name" --ignore-not-found=true -o json)

  [[ -z "$namespace" ]] || args+=(-n "$namespace")
  capture kubectl "${args[@]}"
  QUERY_JSON="$CAPTURE_OUTPUT"
  if ((CAPTURE_RC != 0)); then
    QUERY_STATE="error"
  elif [[ -z "$QUERY_JSON" ]]; then
    QUERY_STATE="absent"
  else
    QUERY_STATE="present"
  fi
}

query_error() {
  printf '%s' "${CAPTURE_ERROR:-exit=${CAPTURE_RC}, no error detail}"
}

json_value() {
  local document="$1"
  local path="$2"
  python3 -c '
import json, sys
value = json.load(sys.stdin)
for key in sys.argv[1].split("."):
    value = value.get(key) if isinstance(value, dict) else None
    if value is None:
        break
if isinstance(value, bool):
    print(str(value).lower())
elif isinstance(value, list):
    print(" ".join(str(item) for item in value))
elif value is not None:
    print(value)
' "$path" <<<"$document"
}

check_cmd() {
  if command -v "$1" >/dev/null 2>&1; then
    pass "命令可用: $1"
  else
    fail "缺少命令: $1"
  fi
}

check_can_i_yes() {
  local verb="$1"
  local resource="$2"
  local result
  capture kubectl auth can-i "$verb" "$resource" --as="system:serviceaccount:${NAMESPACE}:${SERVICE_ACCOUNT}"
  result="$CAPTURE_OUTPUT"
  if [[ "$result" == "yes" ]]; then
    pass "RBAC allow: ${verb} ${resource}"
  elif [[ "$result" == "no" ]]; then
    fail "RBAC 应允许但实际拒绝: ${verb} ${resource}"
  else
    fail "RBAC 查询错误: ${verb} ${resource}: $(query_error)"
  fi
}

check_can_i_no() {
  local verb="$1"
  local resource="$2"
  local result
  if [[ "$resource" == "pods/log" ]]; then
    capture kubectl auth can-i "$verb" pods --subresource=log --as="system:serviceaccount:${NAMESPACE}:${SERVICE_ACCOUNT}"
  else
    capture kubectl auth can-i "$verb" "$resource" --as="system:serviceaccount:${NAMESPACE}:${SERVICE_ACCOUNT}"
  fi
  result="$CAPTURE_OUTPUT"
  if [[ "$result" == "no" ]]; then
    pass "RBAC deny: ${verb} ${resource}"
  elif [[ "$result" == "yes" ]]; then
    fail "RBAC 应拒绝但实际允许: ${verb} ${resource}"
  else
    fail "RBAC 查询错误: ${verb} ${resource}: $(query_error)"
  fi
}

log "Phase 1.1 验收开始"

for value in "$STRICT_RESULTS" "$REQUIRE_ANALYSIS_HEALTH"; do
  if [[ "$value" != "true" && "$value" != "false" ]]; then
    fail "布尔变量只能为 true 或 false，当前值=${value}"
  fi
done

check_cmd kubectl
check_cmd helm
check_cmd python3

if ! command -v kubectl >/dev/null 2>&1 || ! command -v helm >/dev/null 2>&1 || ! command -v python3 >/dev/null 2>&1; then
  log "SUMMARY: pass=${PASS} warn=${WARN} fail=${FAIL}"
  exit 1
fi

capture kubectl config current-context
CONTEXT="$CAPTURE_OUTPUT"
if ((CAPTURE_RC == 0)) && [[ -n "$CONTEXT" ]]; then
  pass "Current context: ${CONTEXT}"
else
  fail "无法获取 current-context: $(query_error)"
fi

capture kubectl version --request-timeout=10s
if ((CAPTURE_RC == 0)); then
  pass "Kubernetes API 可访问"
else
  fail "Kubernetes API 不可访问: $(query_error)"
  log "SUMMARY: pass=${PASS} warn=${WARN} fail=${FAIL}"
  exit 1
fi

capture helm status "$RELEASE_NAME" -n "$NAMESPACE" -o json
if ((CAPTURE_RC != 0)); then
  fail "Helm Release 查询失败: $(query_error)"
elif HELM_STATUS="$(json_value "$CAPTURE_OUTPUT" info.status)" && [[ "$HELM_STATUS" == "deployed" ]]; then
  pass "Helm Release 状态为 deployed"
else
  fail "Helm Release 状态不是 deployed: ${HELM_STATUS:-unknown}"
fi

query_object namespace "$NAMESPACE"
if [[ "$QUERY_STATE" == "present" ]]; then
  pass "Namespace 存在: ${NAMESPACE}"
elif [[ "$QUERY_STATE" == "absent" ]]; then
  fail "Namespace 不存在: ${NAMESPACE}"
else
  fail "Namespace 查询失败: ${NAMESPACE}: $(query_error)"
fi

query_object crd k8sgpts.core.k8sgpt.ai
if [[ "$QUERY_STATE" == "present" ]]; then
  pass "CRD 存在: k8sgpts.core.k8sgpt.ai"
elif [[ "$QUERY_STATE" == "absent" ]]; then
  fail "缺少 CRD: k8sgpts.core.k8sgpt.ai"
else
  fail "CRD 查询失败: k8sgpts.core.k8sgpt.ai: $(query_error)"
fi

query_object crd results.core.k8sgpt.ai
if [[ "$QUERY_STATE" == "present" ]]; then
  pass "CRD 存在: results.core.k8sgpt.ai"
elif [[ "$QUERY_STATE" == "absent" ]]; then
  fail "缺少 CRD: results.core.k8sgpt.ai"
else
  fail "CRD 查询失败: results.core.k8sgpt.ai: $(query_error)"
fi

query_object crd mutations.core.k8sgpt.ai
if [[ "$QUERY_STATE" == "present" ]]; then
  pass "CRD 存在且仅保留 API: mutations.core.k8sgpt.ai"
elif [[ "$QUERY_STATE" == "absent" ]]; then
  fail "缺少 CRD: mutations.core.k8sgpt.ai"
else
  fail "CRD 查询失败: mutations.core.k8sgpt.ai: $(query_error)"
fi

query_object serviceaccount "$SERVICE_ACCOUNT" "$NAMESPACE"
if [[ "$QUERY_STATE" == "present" ]]; then
  pass "ServiceAccount 存在: ${SERVICE_ACCOUNT}"
elif [[ "$QUERY_STATE" == "absent" ]]; then
  fail "ServiceAccount 不存在: ${SERVICE_ACCOUNT}"
else
  fail "ServiceAccount 查询失败: ${SERVICE_ACCOUNT}: $(query_error)"
fi

query_object networkpolicy kube-aiops-egress-baseline "$NAMESPACE"
if [[ "$QUERY_STATE" == "present" ]]; then
  pass "NetworkPolicy egress 基线存在"
elif [[ "$QUERY_STATE" == "absent" ]]; then
  fail "NetworkPolicy egress 基线不存在"
else
  fail "NetworkPolicy egress 基线查询失败: $(query_error)"
fi

check_can_i_yes get pods
check_can_i_yes list deployments.apps
check_can_i_yes list persistentvolumeclaims

check_can_i_no get secrets
check_can_i_no get pods/log
check_can_i_no create pods
check_can_i_no update deployments.apps
check_can_i_no patch deployments.apps
check_can_i_no delete pods
check_can_i_no list daemonsets.apps
check_can_i_no list networkpolicies.networking.k8s.io
check_can_i_no list validatingwebhookconfigurations.admissionregistration.k8s.io

capture kubectl get secret "$SECRET_NAME" -n "$NAMESPACE" --ignore-not-found=true \
  -o go-template='{{if .metadata.name}}{{if index .data "openai-api-key"}}present{{else}}missing{{end}}{{end}}'
SECRET_STATE="$CAPTURE_OUTPUT"
if ((CAPTURE_RC != 0)); then
  fail "AI Provider Secret 查询失败: $(query_error)"
elif [[ "$SECRET_STATE" == "present" ]]; then
  pass "AI Provider Secret 存在"
  pass "AI Provider Secret key 存在: openai-api-key"
elif [[ "$SECRET_STATE" == "missing" ]]; then
  fail "AI Provider Secret 缺少或包含空 key: openai-api-key"
elif [[ -z "$SECRET_STATE" ]]; then
  fail "AI Provider Secret 不存在"
else
  fail "AI Provider Secret 查询返回未知状态: ${SECRET_STATE}"
fi

query_object k8sgpt "$K8SGPT_NAME" "$NAMESPACE"
K8SGPT_JSON="$QUERY_JSON"
if [[ "$QUERY_STATE" == "present" ]]; then
  pass "K8sGPT CR 存在: ${K8SGPT_NAME}"
elif [[ "$QUERY_STATE" == "absent" ]]; then
  fail "K8sGPT CR 不存在: ${K8SGPT_NAME}"
else
  fail "K8sGPT CR 查询失败: ${K8SGPT_NAME}: $(query_error)"
fi

if [[ "$QUERY_STATE" == "present" ]]; then
  AI_ENABLED="$(json_value "$K8SGPT_JSON" spec.ai.enabled)"
  ANONYMIZED="$(json_value "$K8SGPT_JSON" spec.ai.anonymized)"
  BACKEND="$(json_value "$K8SGPT_JSON" spec.ai.backend)"
  INTERVAL="$(json_value "$K8SGPT_JSON" spec.analysis.interval)"
  SECRET_NAME_ACTUAL="$(json_value "$K8SGPT_JSON" spec.ai.secret.name)"
  SECRET_KEY_ACTUAL="$(json_value "$K8SGPT_JSON" spec.ai.secret.key)"
  FILTERS="$(json_value "$K8SGPT_JSON" spec.filters)"
  AUTO_REMEDIATION="$(json_value "$K8SGPT_JSON" spec.ai.autoRemediation.enabled)"

  if [[ "$AI_ENABLED" == "true" ]]; then pass "AI enabled=true"; else fail "AI enabled 非预期: ${AI_ENABLED:-unset}"; fi
  if [[ "$ANONYMIZED" == "true" ]]; then pass "AI anonymized=true"; else fail "AI anonymized 未开启，当前值: ${ANONYMIZED:-unset}"; fi
  if [[ "$BACKEND" == "openai" ]]; then pass "AI backend=openai"; else fail "AI backend 非预期: ${BACKEND:-unset}"; fi
  if [[ "$INTERVAL" == "$EXPECTED_ANALYSIS_INTERVAL" ]]; then pass "Analysis interval=${EXPECTED_ANALYSIS_INTERVAL}"; else fail "Analysis interval 非预期: ${INTERVAL:-unset}，期望 ${EXPECTED_ANALYSIS_INTERVAL}"; fi
  if [[ "${SECRET_NAME_ACTUAL}/${SECRET_KEY_ACTUAL}" == "${SECRET_NAME}/openai-api-key" ]]; then pass "AI Secret 引用正确"; else fail "AI Secret 引用非预期: ${SECRET_NAME_ACTUAL:-unset}/${SECRET_KEY_ACTUAL:-unset}"; fi
  if [[ " ${FILTERS} " == *" Log "* ]]; then fail "Log Analyzer 意外启用"; else pass "Log Analyzer 未启用"; fi
  if [[ -z "$AUTO_REMEDIATION" || "$AUTO_REMEDIATION" == "false" ]]; then pass "Auto Remediation 未启用"; else fail "Auto Remediation 意外启用: ${AUTO_REMEDIATION}"; fi
fi

capture kubectl get mutations -n "$NAMESPACE" -o json
MUTATION_JSON="$CAPTURE_OUTPUT"
if ((CAPTURE_RC == 0)); then
  MUTATION_COUNT="$(python3 -c 'import json,sys; print(len(json.load(sys.stdin).get("items", [])))' <<<"$MUTATION_JSON")"
  if [[ "$MUTATION_COUNT" == "0" ]]; then pass "Mutation 对象未启用"; else fail "发现 Mutation 对象，数量=${MUTATION_COUNT}"; fi
else
  fail "Mutation 对象查询失败: $(query_error)"
fi

RESULT_JSON=""
capture kubectl get results -n "$NAMESPACE" \
  -l "k8sgpts.k8sgpt.ai/name=${K8SGPT_NAME},k8sgpts.k8sgpt.ai/namespace=${NAMESPACE}" -o json
RESULT_JSON="$CAPTURE_OUTPUT"
if ((CAPTURE_RC == 0)); then
  RESULT_ERROR_FILE="$(mktemp -t kube-aiops-result-validation.XXXXXX)"
  RESULT_VALIDATION_RC=0
  RESULT_SUMMARY="$(python3 "${ROOT_DIR}/scripts/verify_results.py" \
    --since "$RESULT_SINCE" --expected-kinds "$EXPECTED_RESULT_KINDS" \
    <<<"$RESULT_JSON" 2>"$RESULT_ERROR_FILE")" || RESULT_VALIDATION_RC=$?
  RESULT_VALIDATION_ERROR="$(<"$RESULT_ERROR_FILE")"
  rm -f -- "$RESULT_ERROR_FILE"
  if ((RESULT_VALIDATION_RC == 0)); then
    RESULT_COUNT="$(python3 -c 'import json,sys; print(json.load(sys.stdin)["count"])' <<<"$RESULT_SUMMARY")"
    DETAIL_COUNT="$(python3 -c 'import json,sys; print(json.load(sys.stdin)["detail_count"])' <<<"$RESULT_SUMMARY")"
    MISSING_KINDS="$(python3 -c 'import json,sys; print(",".join(json.load(sys.stdin)["missing_kinds"]))' <<<"$RESULT_SUMMARY")"
  else
    if ((RESULT_VALIDATION_RC == 2)); then
      fail "Result 验证输入无效（含 RESULT_SINCE）: ${RESULT_VALIDATION_ERROR:-unknown}"
    elif ((RESULT_VALIDATION_RC == 3)); then
      fail "Result API 返回结构不符合预期: ${RESULT_VALIDATION_ERROR:-unknown}"
    else
      fail "Result 验证器异常 exit=${RESULT_VALIDATION_RC}: ${RESULT_VALIDATION_ERROR:-unknown}"
    fi
    RESULT_COUNT=0
    DETAIL_COUNT=0
    MISSING_KINDS="$EXPECTED_RESULT_KINDS"
  fi
  if ((RESULT_COUNT > 0)); then
    pass "Result CR 可读取且属于当前 K8sGPT 实例，新鲜结果数量=${RESULT_COUNT}"
  elif [[ "$STRICT_RESULTS" == "true" ]]; then
    fail "STRICT_RESULTS=true，但没有满足实例标签和 RESULT_SINCE 的 Result CR"
  else
    warn "Result CR API 可访问，但当前实例没有诊断结果；可执行 make demo 后等待分析周期再验证"
  fi
else
  RESULT_COUNT=0
  DETAIL_COUNT=0
  MISSING_KINDS="$EXPECTED_RESULT_KINDS"
  fail "Result CR 查询失败，不得按零结果降级: $(query_error)"
fi

capture kubectl get deployment -n "$NAMESPACE" -l app.kubernetes.io/name=k8sgpt-operator -o json
OPERATOR_JSON="$CAPTURE_OUTPUT"
if ((CAPTURE_RC != 0)); then
  fail "K8sGPT Operator Deployment 查询失败: $(query_error)"
elif OPERATOR_DETAIL="$(python3 -c '
import json, sys
items = json.load(sys.stdin).get("items", [])
if not items:
    print("no matching Deployment")
    raise SystemExit(1)
problems = []
for item in items:
    meta, spec, status = item.get("metadata", {}), item.get("spec", {}), item.get("status", {})
    name = meta.get("name", "<unnamed>")
    desired = int(spec.get("replicas", 1))
    if status.get("observedGeneration", 0) < meta.get("generation", 1):
        problems.append(f"{name}:observedGeneration stale")
    if status.get("readyReplicas", 0) != desired:
        problems.append(f"{name}:ready={status.get('"'"'readyReplicas'"'"', 0)}/{desired}")
    if status.get("availableReplicas", 0) != desired or status.get("unavailableReplicas", 0) != 0:
        problems.append(f"{name}:not fully available")
print("; ".join(problems) if problems else f"{len(items)} deployment(s) converged")
raise SystemExit(1 if problems else 0)
' <<<"$OPERATOR_JSON")"; then
  pass "K8sGPT Operator Deployment 完全 Ready: ${OPERATOR_DETAIL}"
else
  fail "K8sGPT Operator Deployment 未完全 Ready: ${OPERATOR_DETAIL:-invalid response}"
fi

query_object deployment "$K8SGPT_NAME" "$NAMESPACE"
ENGINE_JSON="$QUERY_JSON"
if [[ "$QUERY_STATE" == "present" ]]; then
  if python3 -c '
import json, sys
obj = json.load(sys.stdin)
meta, spec, status = obj.get("metadata", {}), obj.get("spec", {}), obj.get("status", {})
desired = int(spec.get("replicas", 1))
ok = (status.get("observedGeneration", 0) >= meta.get("generation", 1) and
      status.get("readyReplicas", 0) == desired and
      status.get("availableReplicas", 0) == desired and
      status.get("unavailableReplicas", 0) == 0)
raise SystemExit(0 if ok else 1)
' <<<"$ENGINE_JSON"; then
    pass "K8sGPT Engine Deployment Ready 且 observedGeneration 已收敛"
  else
    fail "K8sGPT Engine Deployment 未完全 Ready"
  fi
elif [[ "$QUERY_STATE" == "absent" ]]; then
  fail "K8sGPT Engine Deployment 不存在"
else
  fail "K8sGPT Engine Deployment 查询失败: $(query_error)"
fi

if [[ -n "$K8SGPT_JSON" ]]; then
  LAST_ANALYSIS_ERROR="$(json_value "$K8SGPT_JSON" status.lastAnalysisError)"
  if [[ -z "$LAST_ANALYSIS_ERROR" ]]; then
    pass "K8sGPT 最近一次分析无错误"
  elif [[ "$REQUIRE_ANALYSIS_HEALTH" == "true" ]]; then
    fail "K8sGPT 最近一次分析失败: ${LAST_ANALYSIS_ERROR}"
  else
    warn "K8sGPT 最近一次分析存在 Provider/Analyzer 错误；生命周期验收不阻断: ${LAST_ANALYSIS_ERROR}"
  fi
else
  fail "K8sGPT status 无法验证，因为 CR 不存在或查询失败"
fi

if [[ "$STRICT_RESULTS" == "true" && "$RESULT_COUNT" -gt 0 ]]; then
  if ((DETAIL_COUNT == RESULT_COUNT)); then pass "全部新鲜 Result 均包含 AI 分析详情"; else fail "部分新鲜 Result 缺少 AI 分析详情"; fi
  if [[ -z "$MISSING_KINDS" ]]; then
    pass "Result 覆盖预期 Analyzer kinds: ${EXPECTED_RESULT_KINDS:-未指定}"
  else
    fail "Result 未覆盖预期 Analyzer kinds: ${MISSING_KINDS}"
  fi
fi

printf '\n'
log "SUMMARY: pass=${PASS} warn=${WARN} fail=${FAIL}"

if (( FAIL > 0 )); then
  exit 1
fi

exit 0
