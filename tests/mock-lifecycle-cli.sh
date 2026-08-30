#!/usr/bin/env bash
set -Eeuo pipefail

tool="$(basename "$0")"
case "${tool}:${MOCK_MODE:-}" in
  kubectl:present)
    printf '%s/%s\n' "${2:-resource}" "${3:-name}"
    ;;
  kubectl:absent)
    ;;
  kubectl:error)
    echo 'Error from server (Forbidden): injected query failure' >&2
    exit 1
    ;;
  helm:present)
    printf '[{"name":"k8sgpt-operator","status":"deployed"}]\n'
    ;;
  helm:absent)
    printf '[]\n'
    ;;
  helm:error)
    echo 'Kubernetes cluster unreachable: injected failure' >&2
    exit 1
    ;;
  helm:identity_expected)
    printf '[{"name":"k8sgpt-operator","status":"deployed","chart":"k8sgpt-operator-0.2.29"}]\n'
    ;;
  helm:identity_foreign)
    printf '[{"name":"k8sgpt-operator","status":"deployed","chart":"foreign-chart-1.0.0"}]\n'
    ;;
  kubectl:identity_strict)
    if [[ "$*" == *"--ignore-not-found=true"* ]]; then
      printf '%s/%s\n' "${2:-resource}" "${3:-name}"
    else
      printf '{"kind":"ClusterRole","metadata":{"annotations":{"kube-aiops.io/owner":"phase-1.1"},"labels":{"app.kubernetes.io/part-of":"kube-aiops","app.kubernetes.io/instance":"k8sgpt-operator"}},"rules":[]}\n'
    fi
    ;;
  kubectl:identity_partial)
    if [[ "$*" == *"--ignore-not-found=true"* ]]; then
      printf '%s/%s\n' "${2:-resource}" "${3:-name}"
    else
      printf '{"kind":"ClusterRole","metadata":{"annotations":{"kube-aiops.io/owner":"phase-1.1"},"labels":{"app.kubernetes.io/part-of":"kube-aiops"}},"rules":[]}\n'
    fi
    ;;
  kubectl:identity_legacy_baseline)
    if [[ "$*" == *"--ignore-not-found=true"* ]]; then
      printf '%s/%s\n' "${2:-resource}" "${3:-name}"
    else
      : "${MOCK_BASELINE:?MOCK_BASELINE 未设置}"
      python3 -c '
import json, sys, yaml
with open(sys.argv[1], encoding="utf-8") as stream:
    obj = yaml.safe_load(stream)
obj.setdefault("metadata", {}).setdefault("labels", {}).pop("app.kubernetes.io/instance", None)
json.dump(obj, sys.stdout)
' "$MOCK_BASELINE"
    fi
    ;;
  kubectl:identity_legacy_drifted)
    if [[ "$*" == *"--ignore-not-found=true"* ]]; then
      printf '%s/%s\n' "${2:-resource}" "${3:-name}"
    else
      printf '{"kind":"ClusterRole","metadata":{"annotations":{"kube-aiops.io/owner":"phase-1.1"},"labels":{"app.kubernetes.io/part-of":"kube-aiops"}},"rules":[{"apiGroups":["*"],"resources":["*"],"verbs":["*"]}]}\n'
    fi
    ;;
  *)
    echo "unexpected mock invocation: ${tool}:${MOCK_MODE:-unset} $*" >&2
    exit 99
    ;;
esac
