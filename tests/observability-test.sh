#!/usr/bin/env bash
set -Eeuo pipefail

ROOT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
MOCK_BIN="$(mktemp -d -t kube-aiops-observability-test.XXXXXX)"
TEST_DIR="$(mktemp -d -t kube-aiops-observability-output.XXXXXX)"
cleanup() {
  [[ "$MOCK_BIN" != /tmp/kube-aiops-observability-test.* ]] || rm -rf -- "$MOCK_BIN"
  [[ "$TEST_DIR" != /tmp/kube-aiops-observability-output.* ]] || rm -rf -- "$TEST_DIR"
}
trap cleanup EXIT

ln -s "${ROOT_DIR}/tests/mock-observability-cli.sh" "${MOCK_BIN}/kubectl"
ln -s "${ROOT_DIR}/tests/mock-observability-cli.sh" "${MOCK_BIN}/helm"

run_expect() {
  local expected_rc="$1"
  local mode="$2"
  local script="$3"
  local log_file="${TEST_DIR}/${script}-${mode}.log"
  local count_file="${TEST_DIR}/${script}-${mode}.count"
  local rc=0

  MOCK_OBSERVABILITY_MODE="$mode" MOCK_K8SGPT_COUNT_FILE="$count_file" \
    PATH="${MOCK_BIN}:${PATH}" bash "${ROOT_DIR}/${script}" >"$log_file" 2>&1 || rc=$?
  [[ "$rc" == "$expected_rc" ]] || {
    cat "$log_file" >&2
    echo "ERROR: ${script} mode=${mode} expected rc=${expected_rc}, actual=${rc}" >&2
    exit 1
  }
  printf '%s' "$log_file"
}

log_file="$(run_expect 0 healthy verify.sh)"
[[ "$(<"${TEST_DIR}/verify.sh-healthy.count")" == "1" ]]
grep -q 'SUMMARY:.*fail=0' "$log_file"

log_file="$(run_expect 1 operator_partial verify.sh)"
grep -q 'Operator Deployment 未完全 Ready:.*ready=1/2' "$log_file"

log_file="$(run_expect 1 forbidden_namespace verify.sh)"
grep -q 'Namespace 查询失败:.*Forbidden' "$log_file"

log_file="$(run_expect 1 timeout_namespace verify.sh)"
grep -q 'Namespace 查询失败:.*deadline exceeded' "$log_file"

log_file="$(run_expect 1 namespace_absent verify.sh)"
grep -q 'Namespace 不存在' "$log_file"
if grep -q 'Namespace 查询失败' "$log_file"; then
  echo "ERROR: NotFound must not be reported as a query error" >&2
  exit 1
fi

log_file="$(run_expect 1 malformed_results verify.sh)"
grep -q 'Result API 返回结构不符合预期:.*schema error' "$log_file"

log_file="$(run_expect 0 healthy status.sh)"
grep -q 'SUMMARY:.*fail=0' "$log_file"

log_file="$(run_expect 1 operator_partial status.sh)"
grep -q 'Operator 未完全 Ready:.*1/2' "$log_file"

log_file="$(run_expect 1 api_timeout status.sh)"
grep -q 'Kubernetes API 不可访问:.*deadline exceeded' "$log_file"

log_file="$(run_expect 1 forbidden_results status.sh)"
grep -q 'Result CR 查询失败:.*Forbidden' "$log_file"

echo "observability and status tests passed"
