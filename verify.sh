#!/usr/bin/env bash
set -Eeuo pipefail

readonly NAMESPACE="k8sgpt-operator-system"
readonly SERVICE_ACCOUNT="k8sgpt"
readonly K8SGPT_NAME="k8sgpt-engine"
readonly RELEASE_NAME="k8sgpt-operator"
readonly SECRET_NAME="k8sgpt-openai-secret"
STRICT_RESULTS="${STRICT_RESULTS:-false}"

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
  result="$(kubectl auth can-i "$verb" "$resource" --as="system:serviceaccount:${NAMESPACE}:${SERVICE_ACCOUNT}" 2>/dev/null || true)"
  if [[ "$result" == "yes" ]]; then
    pass "RBAC allow: ${verb} ${resource}"
  else
    fail "RBAC 应允许但实际为 ${result:-unknown}: ${verb} ${resource}"
  fi
}

check_can_i_no() {
  local verb="$1"
  local resource="$2"
  local result
  result="$(kubectl auth can-i "$verb" "$resource" --as="system:serviceaccount:${NAMESPACE}:${SERVICE_ACCOUNT}" 2>/dev/null || true)"
  if [[ "$result" == "no" ]]; then
    pass "RBAC deny: ${verb} ${resource}"
  else
    fail "RBAC 应拒绝但实际为 ${result:-unknown}: ${verb} ${resource}"
  fi
}

log "Phase 1.1 验收开始"

check_cmd kubectl
check_cmd helm
check_cmd python3

if ! command -v kubectl >/dev/null 2>&1 || ! command -v helm >/dev/null 2>&1 || ! command -v python3 >/dev/null 2>&1; then
  log "SUMMARY: pass=${PASS} warn=${WARN} fail=${FAIL}"
  exit 1
fi

CONTEXT="$(kubectl config current-context 2>/dev/null || true)"
if [[ -n "$CONTEXT" ]]; then
  pass "Current context: ${CONTEXT}"
else
  fail "无法获取 current-context"
fi

if kubectl version --request-timeout=10s >/dev/null 2>&1; then
  pass "Kubernetes API 可访问"
else
  fail "Kubernetes API 不可访问"
  log "SUMMARY: pass=${PASS} warn=${WARN} fail=${FAIL}"
  exit 1
fi

if command -v helm >/dev/null 2>&1 && helm status "$RELEASE_NAME" -n "$NAMESPACE" -o json 2>/dev/null |
  python3 -c 'import json,sys; sys.exit(0 if json.load(sys.stdin).get("info",{}).get("status") == "deployed" else 1)' 2>/dev/null; then
  pass "Helm Release 状态为 deployed"
else
  fail "Helm Release 不存在或状态不是 deployed"
fi

if kubectl get namespace "${NAMESPACE}" >/dev/null 2>&1; then
  pass "Namespace 存在: ${NAMESPACE}"
else
  fail "Namespace 不存在: ${NAMESPACE}"
fi

if kubectl get crd k8sgpts.core.k8sgpt.ai >/dev/null 2>&1; then
  pass "CRD 存在: k8sgpts.core.k8sgpt.ai"
else
  fail "缺少 CRD: k8sgpts.core.k8sgpt.ai"
fi

if kubectl get crd results.core.k8sgpt.ai >/dev/null 2>&1; then
  pass "CRD 存在: results.core.k8sgpt.ai"
else
  fail "缺少 CRD: results.core.k8sgpt.ai"
fi

if kubectl get crd mutations.core.k8sgpt.ai >/dev/null 2>&1; then
  pass "CRD 存在且仅保留 API: mutations.core.k8sgpt.ai"
else
  fail "缺少 CRD: mutations.core.k8sgpt.ai"
fi

if kubectl get serviceaccount "${SERVICE_ACCOUNT}" -n "${NAMESPACE}" >/dev/null 2>&1; then
  pass "ServiceAccount 存在: ${SERVICE_ACCOUNT}"
else
  fail "ServiceAccount 不存在: ${SERVICE_ACCOUNT}"
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

if kubectl get secret "$SECRET_NAME" -n "${NAMESPACE}" >/dev/null 2>&1; then
  pass "AI Provider Secret 存在"
  SECRET_KEY="$(kubectl get secret "$SECRET_NAME" -n "${NAMESPACE}" -o jsonpath='{.data.openai-api-key}' 2>/dev/null || true)"
  if [[ -n "$SECRET_KEY" ]]; then
    pass "AI Provider Secret key 存在: openai-api-key"
  else
    fail "AI Provider Secret 缺少 openai-api-key"
  fi
else
  fail "AI Provider Secret 不存在"
fi

if kubectl get k8sgpt "${K8SGPT_NAME}" -n "${NAMESPACE}" >/dev/null 2>&1; then
  pass "K8sGPT CR 存在: ${K8SGPT_NAME}"
else
  fail "K8sGPT CR 不存在: ${K8SGPT_NAME}"
fi

ANONYMIZED="$(kubectl get k8sgpt "${K8SGPT_NAME}" -n "${NAMESPACE}" -o jsonpath='{.spec.ai.anonymized}' 2>/dev/null || true)"
if [[ "$ANONYMIZED" == "true" ]]; then
  pass "AI anonymized=true"
else
  fail "AI anonymized 未开启，当前值: ${ANONYMIZED:-unset}"
fi

BACKEND="$(kubectl get k8sgpt "$K8SGPT_NAME" -n "$NAMESPACE" -o jsonpath='{.spec.ai.backend}' 2>/dev/null || true)"
if [[ "$BACKEND" == "openai" ]]; then pass "AI backend=openai"; else fail "AI backend 非预期: ${BACKEND:-unset}"; fi

INTERVAL="$(kubectl get k8sgpt "$K8SGPT_NAME" -n "$NAMESPACE" -o jsonpath='{.spec.analysis.interval}' 2>/dev/null || true)"
if [[ "$INTERVAL" == "5m" ]]; then pass "Analysis interval=5m"; else fail "Analysis interval 非预期: ${INTERVAL:-unset}"; fi

SECRET_REF="$(kubectl get k8sgpt "$K8SGPT_NAME" -n "$NAMESPACE" -o jsonpath='{.spec.ai.secret.name}/{.spec.ai.secret.key}' 2>/dev/null || true)"
if [[ "$SECRET_REF" == "${SECRET_NAME}/openai-api-key" ]]; then pass "AI Secret 引用正确"; else fail "AI Secret 引用非预期: ${SECRET_REF:-unset}"; fi

FILTERS="$(kubectl get k8sgpt "$K8SGPT_NAME" -n "$NAMESPACE" -o jsonpath='{.spec.filters[*]}' 2>/dev/null || true)"
if [[ " ${FILTERS} " == *" Log "* ]]; then fail "Log Analyzer 意外启用"; else pass "Log Analyzer 未启用"; fi

AUTO_REMEDIATION="$(kubectl get k8sgpt "${K8SGPT_NAME}" -n "${NAMESPACE}" -o jsonpath='{.spec.autoRemediation.enabled}' 2>/dev/null || true)"
if [[ -z "$AUTO_REMEDIATION" || "$AUTO_REMEDIATION" == "false" ]]; then
  pass "Auto Remediation 未启用"
else
  fail "Auto Remediation 意外启用: ${AUTO_REMEDIATION}"
fi

RESULT_COUNT="$(kubectl get results -n "${NAMESPACE}" --no-headers 2>/dev/null | wc -l | tr -d ' ' || true)"
if [[ "$RESULT_COUNT" =~ ^[0-9]+$ ]] && (( RESULT_COUNT > 0 )); then
  pass "Result CR 可读取，当前数量=${RESULT_COUNT}"
else
  if [[ "$STRICT_RESULTS" == "true" ]]; then
    fail "STRICT_RESULTS=true，但当前没有 Result CR"
  else
    warn "Result CR API 可访问，但当前没有诊断结果；可执行 make demo 后等待分析周期再验证"
  fi
fi

OPERATOR_READY="$(kubectl get deploy -n "${NAMESPACE}" -l app.kubernetes.io/name=k8sgpt-operator -o jsonpath='{range .items[*]}{.status.readyReplicas}{"\n"}{end}' 2>/dev/null | awk '$1 > 0 {count++} END {print count+0}')"
if [[ "$OPERATOR_READY" -gt 0 ]]; then
  pass "K8sGPT Operator Deployment Ready"
else
  fail "K8sGPT Operator Deployment 未 Ready"
fi

if [[ "$STRICT_RESULTS" == "true" && "$RESULT_COUNT" =~ ^[0-9]+$ && "$RESULT_COUNT" -gt 0 ]]; then
  DETAIL_COUNT="$(kubectl get results -n "$NAMESPACE" -o jsonpath='{range .items[*]}{.spec.details}{"\n"}{end}' 2>/dev/null | awk 'NF {n++} END {print n+0}' || true)"
  if [[ "$DETAIL_COUNT" -gt 0 ]]; then pass "Result 包含 AI 分析详情"; else fail "Result 不包含 AI 分析详情"; fi
fi

printf '\n'
log "SUMMARY: pass=${PASS} warn=${WARN} fail=${FAIL}"

if (( FAIL > 0 )); then
  exit 1
fi

exit 0
