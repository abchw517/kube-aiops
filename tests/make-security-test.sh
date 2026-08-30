#!/usr/bin/env bash
set -Eeuo pipefail

ROOT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
TEST_DIR="$(mktemp -d -t kube-aiops-make-security.XXXXXX)"
MOCK_BIN="${TEST_DIR}/bin"
ARGS_LOG="${TEST_DIR}/args.log"
STDIN_LOG="${TEST_DIR}/stdin.log"
readonly TEST_TOKEN='p1-test-token-without-shell-argv'

cleanup() {
  rm -rf -- "$TEST_DIR"
}
trap cleanup EXIT

mkdir -p "$MOCK_BIN"
cat >"${MOCK_BIN}/kubectl" <<'EOF'
#!/usr/bin/env bash
set -Eeuo pipefail
printf '%q ' "$@" >>"$ARGS_LOG"
printf '\n' >>"$ARGS_LOG"
if [[ " $* " == *" create secret generic "* ]]; then
  payload="$(cat)"
  printf '%s' "$payload" >"$STDIN_LOG"
  printf '%s\n' \
    'apiVersion: v1' \
    'kind: Secret' \
    'metadata:' \
    '  name: k8sgpt-openai-secret'
elif [[ " $* " == *" apply -f - "* ]]; then
  cat >/dev/null
fi
EOF
chmod 700 "${MOCK_BIN}/kubectl"

export ARGS_LOG STDIN_LOG
PATH="${MOCK_BIN}:${PATH}" OPENAI_TOKEN="$TEST_TOKEN" \
  make --no-print-directory -C "$ROOT_DIR" bootstrap-secret >/dev/null

if grep -Fq -- "$TEST_TOKEN" "$ARGS_LOG"; then
  echo "FAIL: OPENAI_TOKEN 泄露到 kubectl argv" >&2
  exit 1
fi
[[ "$(cat "$STDIN_LOG")" == "$TEST_TOKEN" ]] || {
  echo "FAIL: OPENAI_TOKEN 未通过 stdin 原样传递" >&2
  exit 1
}

install_plan="$(make --no-print-directory -n -C "$ROOT_DIR" install \
  OPERATOR_VERSION='0.2.29"; echo OPERATOR_INJECTED; #')"
[[ "$install_plan" != *"OPERATOR_INJECTED"* ]] || {
  echo "FAIL: OPERATOR_VERSION 仍可注入 recipe" >&2
  exit 1
}

clean_plan="$(make --no-print-directory -n -C "$ROOT_DIR" clean-demo \
  DEMO_NAMESPACE='"; echo NAMESPACE_INJECTED; #')"
[[ "$clean_plan" != *"NAMESPACE_INJECTED"* ]] || {
  echo "FAIL: DEMO_NAMESPACE 仍可注入 recipe" >&2
  exit 1
}

echo "PASS: Makefile 参数注入与 Secret argv 回归测试"
