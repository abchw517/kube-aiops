#!/usr/bin/env bash

# install.sh / uninstall.sh 共用的生命周期原语。调用方必须启用 set -Eeuo pipefail。

# 由 source 本文件的调用方读取。
# shellcheck disable=SC2034
LC_STATE=""
# shellcheck disable=SC2034
LC_OUTPUT=""
LC_LOCK_HELD=false
LC_LOCK_NAMESPACE=""
LC_LOCK_NAME=""
LC_LOCK_HOLDER=""

lc_log() {
  printf '[%s] %s\n' "$(date '+%Y-%m-%d %H:%M:%S')" "$*"
}

# 查询 Kubernetes 对象并明确区分 present / absent / error。
lc_kubectl_state() {
  local resource="$1"
  local name="$2"
  local namespace="${3:-}"
  local -a args=(get "$resource" "$name" --ignore-not-found=true -o name)

  [[ -z "$namespace" ]] || args+=(-n "$namespace")
  LC_OUTPUT=""
  if ! LC_OUTPUT="$(kubectl "${args[@]}")"; then
    LC_STATE="error"
  elif [[ -n "$LC_OUTPUT" ]]; then
    LC_STATE="present"
  else
    LC_STATE="absent"
  fi
}

# Helm list 在 Release 不存在时返回空数组，在 API/RBAC 错误时返回非零。
lc_helm_state() {
  local release="$1"
  local namespace="$2"
  local releases

  LC_OUTPUT=""
  if ! releases="$(helm list -n "$namespace" --all --filter "^${release}$" -o json)"; then
    LC_STATE="error"
    return 0
  fi
  if ! LC_OUTPUT="$(python3 -c '
import json, sys
name = sys.argv[1]
items = json.load(sys.stdin)
matches = [item for item in items if item.get("name") == name]
if len(matches) > 1:
    raise SystemExit("duplicate Helm releases returned")
print(matches[0].get("status", "") if matches else "")
' "$release" <<<"$releases")"; then
    LC_STATE="error"
  elif [[ -n "$LC_OUTPUT" ]]; then
    LC_STATE="present"
  else
    LC_STATE="absent"
  fi
}

lc_lease_manifest() {
  local namespace="$1"
  local name="$2"
  local holder="$3"
  local renew_time="$4"

  printf '%s\n' \
    'apiVersion: coordination.k8s.io/v1' \
    'kind: Lease' \
    'metadata:' \
    "  name: ${name}" \
    "  namespace: ${namespace}" \
    '  labels:' \
    '    app.kubernetes.io/part-of: kube-aiops' \
    '    app.kubernetes.io/component: lifecycle-lock' \
    'spec:' \
    "  holderIdentity: ${holder}" \
    '  leaseDurationSeconds: 1800' \
    "  acquireTime: ${renew_time}" \
    "  renewTime: ${renew_time}"
}

# 使用 create + resourceVersion JSON Patch 实现跨 Jenkins/CI/人工操作的互斥。
lc_acquire_lock() {
  local namespace="$1"
  local name="$2"
  local holder="$3"
  local wait_seconds="${4:-30}"
  local deadline=$((SECONDS + wait_seconds))
  local now lease_json lease_state patch

  LC_LOCK_NAMESPACE="$namespace"
  LC_LOCK_NAME="$name"
  LC_LOCK_HOLDER="$holder"

  while ((SECONDS <= deadline)); do
    now="$(date -u '+%Y-%m-%dT%H:%M:%SZ')"
    if lc_lease_manifest "$namespace" "$name" "$holder" "$now" |
      kubectl create -f - >/dev/null 2>&1; then
      LC_LOCK_HELD=true
      return 0
    fi

    if ! lease_json="$(kubectl get lease "$name" -n "$namespace" -o json)"; then
      lc_log "ERROR: 无法读取生命周期 Lease ${namespace}/${name}"
      return 1
    fi

    if ! lease_state="$(python3 -c '
from datetime import datetime, timezone
import json, sys
obj = json.load(sys.stdin)
spec = obj.get("spec", {})
holder = spec.get("holderIdentity", "")
renew = spec.get("renewTime") or spec.get("acquireTime")
duration = int(spec.get("leaseDurationSeconds", 0))
expired = True
if renew and duration > 0:
    renewed = datetime.fromisoformat(renew.replace("Z", "+00:00"))
    expired = (datetime.now(timezone.utc) - renewed).total_seconds() >= duration
print("expired" if expired else holder)
' <<<"$lease_json")"; then
      lc_log "ERROR: 无法解析生命周期 Lease ${namespace}/${name}"
      return 1
    fi

    if [[ "$lease_state" == "$holder" || "$lease_state" == "expired" ]]; then
      patch="$(python3 -c '
import json, sys
obj = json.load(sys.stdin)
rv, holder, now = obj["metadata"]["resourceVersion"], sys.argv[1], sys.argv[2]
print(json.dumps([
  {"op":"test", "path":"/metadata/resourceVersion", "value":rv},
  {"op":"add", "path":"/spec/holderIdentity", "value":holder},
  {"op":"add", "path":"/spec/leaseDurationSeconds", "value":1800},
  {"op":"add", "path":"/spec/renewTime", "value":now}
]))
' "$holder" "$now" <<<"$lease_json")"
      if kubectl patch lease "$name" -n "$namespace" --type=json -p "$patch" >/dev/null 2>&1; then
        LC_LOCK_HELD=true
        return 0
      fi
      continue
    fi

    sleep 2
  done

  lc_log "ERROR: 生命周期操作被 Lease 持有者阻塞: ${lease_state:-unknown}"
  return 1
}

lc_renew_lock() {
  [[ "$LC_LOCK_HELD" == "true" ]] || return 0
  lc_acquire_lock "$LC_LOCK_NAMESPACE" "$LC_LOCK_NAME" "$LC_LOCK_HOLDER" 0
}

lc_release_lock() {
  local holder

  [[ "$LC_LOCK_HELD" == "true" ]] || return 0
  if ! holder="$(kubectl get lease "$LC_LOCK_NAME" -n "$LC_LOCK_NAMESPACE" \
    -o jsonpath='{.spec.holderIdentity}')"; then
    lc_log "ERROR: 无法读取待释放的生命周期 Lease"
    return 1
  fi
  if [[ "$holder" != "$LC_LOCK_HOLDER" ]]; then
    lc_log "ERROR: Lease 持有者已变化，拒绝删除: ${holder:-unknown}"
    return 1
  fi
  if ! kubectl delete lease "$LC_LOCK_NAME" -n "$LC_LOCK_NAMESPACE" --wait=false >/dev/null; then
    lc_log "ERROR: 生命周期 Lease 释放失败"
    return 1
  fi
  LC_LOCK_HELD=false
}
