#!/usr/bin/env bash
set -Eeuo pipefail

: "${REAL_KUBECTL:?REAL_KUBECTL 未设置}"
FAULT_MODE="${FAULT_MODE:-none}"

if [[ "$FAULT_MODE" == "fail_ssa_once" && "$*" == *"apply --server-side"* ]]; then
  : "${FAULT_MARKER:?FAULT_MARKER 未设置}"
  if [[ -f "$FAULT_MARKER" ]]; then
    rm -f -- "$FAULT_MARKER"
    echo 'injected server-side apply failure' >&2
    exit 42
  fi
fi

if [[ "$FAULT_MODE" == "fail_ssa_and_rollback" && "$*" == *"apply --server-side"* ]]; then
  : "${FAULT_MARKER:?FAULT_MARKER 未设置}"
  if [[ "$*" == *"--field-manager=kube-aiops-rollback"* ]]; then
    echo 'injected rollback restore failure' >&2
    exit 43
  fi
  if [[ -f "$FAULT_MARKER" ]]; then
    rm -f -- "$FAULT_MARKER"
    echo 'injected server-side apply failure' >&2
    exit 42
  fi
fi

if [[ "$FAULT_MODE" == "deny_clusterrole_get" && "$1" == "get" && "${2:-}" == "clusterrole" ]]; then
  echo 'Error from server (Forbidden): clusterroles.rbac.authorization.k8s.io is forbidden' >&2
  exit 1
fi

exec "$REAL_KUBECTL" "$@"
