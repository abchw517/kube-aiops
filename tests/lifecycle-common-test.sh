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
# shellcheck source=scripts/host-deps.sh
source "${ROOT_DIR}/scripts/host-deps.sh"

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

MOCK_MODE=identity_expected lc_assert_helm_release_identity \
  k8sgpt-operator k8sgpt-operator-system k8sgpt-operator 0.2.29
if MOCK_MODE=identity_foreign lc_assert_helm_release_identity \
  k8sgpt-operator k8sgpt-operator-system k8sgpt-operator 0.2.29; then
  echo "ERROR: foreign Helm release identity should be rejected" >&2
  exit 1
fi

# Helm 4 can be first on PATH while the lifecycle uses its own Helm 3.
(
  cache="${MOCK_BIN}/tools"
  test_path="${MOCK_BIN}/test-path"
  mkdir -p "$test_path"
  for cmd in bash basename cat chmod date ln mkdir mv python3; do
    ln -s "$(type -P "$cmd")" "$test_path/$cmd"
  done
  export PATH="${MOCK_BIN}:${test_path}"
  host_deps_select_helm3 "$cache"
  [[ "$HOST_DEPS_HELM" == "${MOCK_BIN}/helm" ]]
  cat >"${MOCK_BIN}/helm-v4" <<'SH'
#!/usr/bin/env bash
if [[ "$*" == 'version --short' ]]; then
  printf 'v4.3.0\n'
else
  printf 'Error: unknown flag: --all\n' >&2
  exit 1
fi
SH
  chmod +x "${MOCK_BIN}/helm-v4"
  mv -Tf -- "${MOCK_BIN}/helm-v4" "${MOCK_BIN}/helm"
  host_deps_install() {
    [[ "$1" == helm && "$3" == v3.21.3 ]] || return 1
    mkdir -p "${2%/*}"
    ln -s "${ROOT_DIR}/tests/mock-lifecycle-cli.sh" "$2"
  }
  host_deps_select_helm3 "$cache"
  [[ "$HOST_DEPS_HELM" == "$cache/helm-v3.21.3/bin/helm" ]]
  [[ "$(helm version --short)" == v4.3.0 ]]
  MOCK_MODE=present lc_helm_state k8sgpt-operator k8sgpt-operator-system
  assert_state present
  MOCK_MODE=identity_expected lc_assert_helm_release_identity \
    k8sgpt-operator k8sgpt-operator-system k8sgpt-operator 0.2.29
  host_deps_install() { return 1; }
  host_deps_select_helm3 "$cache"
)

BASELINE="${ROOT_DIR}/deploy/k8sgpt/clusterrole.yaml"
MOCK_MODE=identity_strict lc_assert_cluster_resource_identity \
  uninstall clusterrole k8sgpt-clusterrole "$BASELINE" false
if MOCK_MODE=identity_partial lc_assert_cluster_resource_identity \
  install clusterrole k8sgpt-clusterrole "$BASELINE" false; then
  echo "ERROR: partial ownership must require explicit migration" >&2
  exit 1
fi
if MOCK_MODE=identity_legacy_baseline MOCK_BASELINE="$BASELINE" \
  lc_assert_cluster_resource_identity uninstall clusterrole k8sgpt-clusterrole "$BASELINE" true; then
  echo "ERROR: uninstall must never migrate legacy ownership" >&2
  exit 1
fi
MOCK_MODE=identity_legacy_baseline MOCK_BASELINE="$BASELINE" \
  lc_assert_cluster_resource_identity install clusterrole k8sgpt-clusterrole "$BASELINE" true
if MOCK_MODE=identity_legacy_drifted lc_assert_cluster_resource_identity \
  install clusterrole k8sgpt-clusterrole "$BASELINE" true; then
  echo "ERROR: drifted legacy fingerprint must be rejected" >&2
  exit 1
fi

echo "lifecycle common tri-state tests passed"
