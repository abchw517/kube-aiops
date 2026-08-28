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

echo "strict Result freshness tests passed"
