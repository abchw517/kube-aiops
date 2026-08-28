#!/usr/bin/env bash
set -Eeuo pipefail

ROOT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
MOCK_BIN="$(mktemp -d -t kube-aiops-lifecycle-test.XXXXXX)"
cleanup() {
  if [[ -d "$MOCK_BIN" && "$MOCK_BIN" == /tmp/kube-aiops-lifecycle-test.* ]]; then
    rm -rf -- "$MOCK_BIN"
  fi
}
trap cleanup EXIT

ln -s "${ROOT_DIR}/tests/mock-lifecycle-cli.sh" "${MOCK_BIN}/kubectl"
ln -s "${ROOT_DIR}/tests/mock-lifecycle-cli.sh" "${MOCK_BIN}/helm"
PATH="${MOCK_BIN}:${PATH}"
# shellcheck source=scripts/lifecycle-common.sh
source "${ROOT_DIR}/scripts/lifecycle-common.sh"

assert_state() {
  local expected="$1"
  [[ "$LC_STATE" == "$expected" ]] || {
    echo "ERROR: expected state=${expected}, actual=${LC_STATE}" >&2
    exit 1
  }
}

MOCK_MODE=present lc_kubectl_state clusterrole k8sgpt-clusterrole
assert_state present
MOCK_MODE=absent lc_kubectl_state clusterrole k8sgpt-clusterrole
assert_state absent
MOCK_MODE=error lc_kubectl_state clusterrole k8sgpt-clusterrole
assert_state error

MOCK_MODE=present lc_helm_state k8sgpt-operator k8sgpt-operator-system
assert_state present
[[ "$LC_OUTPUT" == "deployed" ]]
MOCK_MODE=absent lc_helm_state k8sgpt-operator k8sgpt-operator-system
assert_state absent
MOCK_MODE=error lc_helm_state k8sgpt-operator k8sgpt-operator-system
assert_state error

echo "lifecycle common tri-state tests passed"
