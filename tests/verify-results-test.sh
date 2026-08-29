#!/usr/bin/env bash
set -Eeuo pipefail

ROOT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"

summary="$(python3 "${ROOT_DIR}/scripts/verify_results.py" \
  --since '2026-08-28T00:00:00Z' \
  --expected-kinds 'Pod,Deployment,PersistentVolumeClaim' <<'JSON'
{"items":[
  {"metadata":{"creationTimestamp":"2026-08-27T23:59:59Z"},"spec":{"kind":"Deployment","details":"old"}},
  {"metadata":{"creationTimestamp":"2026-08-28T00:00:01Z"},"spec":{"kind":"Pod","details":"AI pod detail"}},
  {"metadata":{"creationTimestamp":"2026-08-28T00:00:02Z"},"spec":{"kind":"PersistentVolumeClaim","details":""}}
]}
JSON
)"

python3 -c '
import json, sys
value = json.load(sys.stdin)
assert value == {
    "count": 2,
    "detail_count": 1,
    "missing_kinds": ["Deployment", "PersistentVolumeClaim"],
}, value
' <<<"$summary"

if printf '{"items":[]}' | python3 "${ROOT_DIR}/scripts/verify_results.py" --since invalid >/dev/null 2>&1; then
  echo "ERROR: invalid RESULT_SINCE should fail" >&2
  exit 1
fi

summary="$(python3 "${ROOT_DIR}/scripts/verify_results.py" \
  --since '2026-08-28T00:00:00Z' \
  --expected-kinds 'Pod, Deployment' <<'JSON'
{"items":[
  {"metadata":{"creationTimestamp":"2026-08-28T00:00:01Z"},"spec":{"kind":"Pod","details":"pod detail"}},
  {"metadata":{"creationTimestamp":"2026-08-28T00:00:02Z"},"spec":{"kind":"Deployment","details":"deployment detail"}}
]}
JSON
)"
python3 -c '
import json, sys
value = json.load(sys.stdin)
assert value["missing_kinds"] == [], value
' <<<"$summary"

assert_schema_error() {
  local document="$1"
  local error_file rc=0
  error_file="$(mktemp -t kube-aiops-result-error.XXXXXX)"
  printf '%s' "$document" | python3 "${ROOT_DIR}/scripts/verify_results.py" \
    >/dev/null 2>"$error_file" || rc=$?
  [[ "$rc" == "3" ]] || {
    echo "ERROR: malformed Result schema should return 3, got ${rc}" >&2
    rm -f -- "$error_file"
    exit 1
  }
  grep -q 'schema error' "$error_file"
  rm -f -- "$error_file"
}

assert_schema_error '[]'
assert_schema_error '{"items":{}}'
assert_schema_error '{"items":[{"metadata":{"creationTimestamp":"2026-08-28T00:00:01Z"},"spec":{"kind":"Pod","details":{}}}]}'
assert_schema_error '{"items":[{"metadata":{},"spec":{"kind":"Pod","details":"detail"}}]}'

echo "strict Result freshness tests passed"
