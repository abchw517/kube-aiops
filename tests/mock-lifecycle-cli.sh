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
  *)
    echo "unexpected mock invocation: ${tool}:${MOCK_MODE:-unset} $*" >&2
    exit 99
    ;;
esac
