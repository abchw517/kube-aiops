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
LC_LOCK_DURATION_SECONDS="${LEASE_DURATION_SECONDS:-1800}"
LC_LOCK_RENEW_INTERVAL_SECONDS="${LEASE_RENEW_INTERVAL_SECONDS:-30}"
LC_LOCK_HEARTBEAT_PID=""
LC_LOCK_HEARTBEAT_FILE=""

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

# 同名 Helm Release 必须确实来自预期 Chart，避免升级或卸载其它系统的 Release。
lc_assert_helm_release_identity() {
  local release="$1"
  local namespace="$2"
  local expected_chart="$3"
  local expected_version="$4"
  local releases chart_name

  if ! releases="$(helm list -n "$namespace" --all --filter "^${release}$" -o json)"; then
    lc_log "ERROR: 无法读取 Helm Release 身份: ${namespace}/${release}"
    return 1
  fi
  if ! chart_name="$(python3 -c '
import json, sys
name = sys.argv[1]
items = [item for item in json.load(sys.stdin) if item.get("name") == name]
if len(items) != 1:
    raise SystemExit(1)
print(items[0].get("chart", ""))
' "$release" <<<"$releases")"; then
    lc_log "ERROR: 无法解析 Helm Release 身份: ${namespace}/${release}"
    return 1
  fi
  if [[ "$chart_name" != "${expected_chart}-${expected_version}" ]]; then
    lc_log "ERROR: 拒绝操作同名外部 Helm Release: expected=${expected_chart}-${expected_version} actual=${chart_name:-unknown}"
    return 1
  fi
}

# 仅认可完整且精确的三元身份。旧基线迁移只能由 install 显式开启，并且资源内容
# 必须与仓库基线语义一致；uninstall 永远不接受 legacy 身份。
lc_assert_cluster_resource_identity() {
  local mode="$1"
  local resource="$2"
  local name="$3"
  local baseline="$4"
  local allow_legacy="${5:-false}"
  local resource_json identity fingerprint_ok

  lc_kubectl_state "$resource" "$name"
  [[ "$LC_STATE" != "error" ]] || {
    lc_log "ERROR: 无法查询所有权: ${resource}/${name}"
    return 1
  }
  [[ "$LC_STATE" == "present" ]] || return 0

  resource_json="$(kubectl get "$resource" "$name" -o json)" || {
    lc_log "ERROR: 无法读取所有权: ${resource}/${name}"
    return 1
  }
  identity="$(python3 -c '
import json, sys
obj = json.load(sys.stdin)
meta = obj.get("metadata", {})
ann = meta.get("annotations", {})
labels = meta.get("labels", {})
print("|".join((
    ann.get("kube-aiops.io/owner", ""),
    labels.get("app.kubernetes.io/part-of", ""),
    labels.get("app.kubernetes.io/instance", ""),
)))
' <<<"$resource_json")" || return 1

  [[ "$identity" == "phase-1.1|kube-aiops|k8sgpt-operator" ]] && return 0
  if [[ "$mode" != "install" || "$allow_legacy" != "true" ||
    "$identity" != "phase-1.1|kube-aiops|" ]]; then
    lc_log "ERROR: 拒绝操作身份不匹配的资源: ${resource}/${name} identity=${identity:-unset}"
    return 1
  fi

  fingerprint_ok="$(python3 -c '
import json, sys, yaml
current = json.load(sys.stdin)
with open(sys.argv[1], encoding="utf-8") as stream:
    expected = yaml.safe_load(stream)
kind = current.get("kind")
def normalized(value):
    if isinstance(value, dict):
        return {key: normalized(value[key]) for key in sorted(value)}
    if isinstance(value, list):
        items = [normalized(item) for item in value]
        return sorted(items, key=lambda item: json.dumps(item, sort_keys=True))
    return value
if kind == "ClusterRole":
    ok = normalized(current.get("rules", [])) == normalized(expected.get("rules", []))
elif kind == "ClusterRoleBinding":
    ok = (normalized(current.get("roleRef", {})) == normalized(expected.get("roleRef", {})) and
          normalized(current.get("subjects", [])) == normalized(expected.get("subjects", [])))
else:
    ok = False
print("true" if ok else "false")
' "$baseline" <<<"$resource_json")" || return 1
  if [[ "$fingerprint_ok" != "true" ]]; then
    lc_log "ERROR: legacy 资源指纹不匹配，拒绝迁移: ${resource}/${name}"
    return 1
  fi
  lc_log "WARN: 显式迁移通过指纹校验的 legacy 资源: ${resource}/${name}"
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
    "  leaseDurationSeconds: ${LC_LOCK_DURATION_SECONDS}" \
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
  local now lease_json lease_state patch create_output

  LC_LOCK_NAMESPACE="$namespace"
  LC_LOCK_NAME="$name"
  LC_LOCK_HOLDER="$holder"

  [[ "$LC_LOCK_DURATION_SECONDS" =~ ^[1-9][0-9]*$ ]] || {
    lc_log "ERROR: LEASE_DURATION_SECONDS 必须是正整数"
    return 1
  }

  while ((SECONDS <= deadline)); do
    now="$(date -u '+%Y-%m-%dT%H:%M:%S.000000Z')"
    if create_output="$(lc_lease_manifest "$namespace" "$name" "$holder" "$now" |
      kubectl create -f - 2>&1)"; then
      LC_LOCK_HELD=true
      return 0
    fi

    if ! lease_json="$(kubectl get lease "$name" -n "$namespace" -o json 2>/dev/null)"; then
      [[ -z "$create_output" ]] || lc_log "ERROR: Lease 创建失败: ${create_output}"
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
  {"op":"add", "path":"/spec/leaseDurationSeconds", "value":int(sys.argv[3])},
  {"op":"add", "path":"/spec/renewTime", "value":now}
]))
' "$holder" "$now" "$LC_LOCK_DURATION_SECONDS" <<<"$lease_json")"
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
  local lease_json patch now

  [[ "$LC_LOCK_HELD" == "true" ]] || return 1
  lease_json="$(kubectl get lease "$LC_LOCK_NAME" -n "$LC_LOCK_NAMESPACE" -o json)" || return 1
  now="$(date -u '+%Y-%m-%dT%H:%M:%S.000000Z')"
  patch="$(python3 -c '
import json, sys
obj = json.load(sys.stdin)
spec = obj.get("spec", {})
if spec.get("holderIdentity", "") != sys.argv[1]:
    raise SystemExit(1)
print(json.dumps([
  {"op":"test", "path":"/metadata/resourceVersion", "value":obj["metadata"]["resourceVersion"]},
  {"op":"test", "path":"/spec/holderIdentity", "value":sys.argv[1]},
  {"op":"add", "path":"/spec/leaseDurationSeconds", "value":int(sys.argv[3])},
  {"op":"add", "path":"/spec/renewTime", "value":sys.argv[2]},
]))
' "$LC_LOCK_HOLDER" "$now" "$LC_LOCK_DURATION_SECONDS" <<<"$lease_json")" || return 1
  kubectl patch lease "$LC_LOCK_NAME" -n "$LC_LOCK_NAMESPACE" --type=json -p "$patch" >/dev/null
}

lc_assert_lock_held() {
  local lease_json state

  [[ "$LC_LOCK_HELD" == "true" ]] || return 1
  if [[ -n "$LC_LOCK_HEARTBEAT_FILE" && -s "$LC_LOCK_HEARTBEAT_FILE" ]]; then
    lc_log "ERROR: 生命周期 Lease heartbeat 已失败"
    return 1
  fi
  lease_json="$(kubectl get lease "$LC_LOCK_NAME" -n "$LC_LOCK_NAMESPACE" -o json)" || return 1
  state="$(python3 -c '
from datetime import datetime, timezone
import json, sys
obj = json.load(sys.stdin)
spec = obj.get("spec", {})
renew = spec.get("renewTime") or spec.get("acquireTime")
duration = int(spec.get("leaseDurationSeconds", 0))
valid = spec.get("holderIdentity", "") == sys.argv[1] and bool(renew) and duration > 0
if valid:
    renewed = datetime.fromisoformat(renew.replace("Z", "+00:00"))
    valid = (datetime.now(timezone.utc) - renewed).total_seconds() < duration
print("held" if valid else "lost")
' "$LC_LOCK_HOLDER" <<<"$lease_json")" || return 1
  [[ "$state" == "held" ]] || {
    lc_log "ERROR: 生命周期 Lease 已失去，拒绝继续修改集群"
    return 1
  }
}

lc_start_lock_heartbeat() {
  [[ "$LC_LOCK_HELD" == "true" ]] || return 1
  [[ "$LC_LOCK_RENEW_INTERVAL_SECONDS" =~ ^[1-9][0-9]*$ ]] || return 1
  LC_LOCK_HEARTBEAT_FILE="$(mktemp -t kube-aiops-lease-heartbeat.XXXXXX)"
  (
    while sleep "$LC_LOCK_RENEW_INTERVAL_SECONDS"; do
      if ! lc_renew_lock; then
        printf 'failed\n' >"$LC_LOCK_HEARTBEAT_FILE"
        exit 1
      fi
    done
  ) &
  LC_LOCK_HEARTBEAT_PID="$!"
}

lc_stop_lock_heartbeat() {
  if [[ -n "$LC_LOCK_HEARTBEAT_PID" ]]; then
    kill "$LC_LOCK_HEARTBEAT_PID" 2>/dev/null || true
    wait "$LC_LOCK_HEARTBEAT_PID" 2>/dev/null || true
    LC_LOCK_HEARTBEAT_PID=""
  fi
}

lc_release_lock() {
  local lease_json patch

  [[ "$LC_LOCK_HELD" == "true" ]] || return 0
  lc_stop_lock_heartbeat
  if ! lease_json="$(kubectl get lease "$LC_LOCK_NAME" -n "$LC_LOCK_NAMESPACE" -o json)"; then
    lc_log "ERROR: 无法读取待释放的生命周期 Lease"
    return 1
  fi
  if ! patch="$(python3 -c '
import json, sys
obj = json.load(sys.stdin)
holder = obj.get("spec", {}).get("holderIdentity", "")
if holder != sys.argv[1]:
    raise SystemExit(1)
print(json.dumps([
  {"op":"test", "path":"/metadata/resourceVersion", "value":obj["metadata"]["resourceVersion"]},
  {"op":"test", "path":"/spec/holderIdentity", "value":sys.argv[1]},
  {"op":"replace", "path":"/spec/holderIdentity", "value":""},
  {"op":"add", "path":"/spec/leaseDurationSeconds", "value":1},
  {"op":"add", "path":"/spec/renewTime", "value":"1970-01-01T00:00:00.000000Z"},
]))
' "$LC_LOCK_HOLDER" <<<"$lease_json")"; then
    lc_log "ERROR: Lease 持有者已变化，拒绝释放"
    return 1
  fi
  if ! kubectl patch lease "$LC_LOCK_NAME" -n "$LC_LOCK_NAMESPACE" --type=json -p "$patch" >/dev/null; then
    lc_log "ERROR: 生命周期 Lease CAS 释放失败"
    return 1
  fi
  LC_LOCK_HELD=false
  [[ -z "$LC_LOCK_HEARTBEAT_FILE" ]] || rm -f -- "$LC_LOCK_HEARTBEAT_FILE"
  LC_LOCK_HEARTBEAT_FILE=""
}
