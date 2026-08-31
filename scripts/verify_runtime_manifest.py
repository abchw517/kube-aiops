#!/usr/bin/env python3
"""Compare security-critical live resources with their repository baselines."""

from __future__ import annotations

import argparse
import json
import sys

import yaml


SECURITY_FIELDS = {
    "ServiceAccount": ("automountServiceAccountToken",),
    "ClusterRole": ("rules",),
    "ClusterRoleBinding": ("roleRef", "subjects"),
    "NetworkPolicy": ("spec",),
}


def normalized(value):
    if isinstance(value, dict):
        return {key: normalized(value[key]) for key in sorted(value)}
    if isinstance(value, list):
        items = [normalized(item) for item in value]
        return sorted(items, key=lambda item: json.dumps(item, sort_keys=True))
    return value


def main() -> int:
    parser = argparse.ArgumentParser()
    parser.add_argument("--baseline", required=True)
    args = parser.parse_args()

    try:
        live = json.load(sys.stdin)
        with open(args.baseline, encoding="utf-8") as stream:
            expected = yaml.safe_load(stream)
    except (OSError, json.JSONDecodeError, yaml.YAMLError) as exc:
        print(f"runtime manifest input error: {exc}", file=sys.stderr)
        return 2

    if not isinstance(live, dict) or not isinstance(expected, dict):
        print("runtime manifest input error: objects must be mappings", file=sys.stderr)
        return 2

    kind = expected.get("kind", "")
    if live.get("kind") != kind or kind not in SECURITY_FIELDS:
        print(
            f"runtime manifest kind mismatch: expected={kind or '<empty>'} "
            f"actual={live.get('kind', '<empty>')}",
            file=sys.stderr,
        )
        return 1

    mismatches = []
    live_meta = live.get("metadata") or {}
    expected_meta = expected.get("metadata") or {}
    for field in ("name", "namespace"):
        if expected_meta.get(field, "") != live_meta.get(field, ""):
            mismatches.append(f"metadata.{field}")
    for group in ("labels", "annotations"):
        live_values = live_meta.get(group) or {}
        for key, value in (expected_meta.get(group) or {}).items():
            if live_values.get(key) != value:
                mismatches.append(f"metadata.{group}.{key}")

    for field in SECURITY_FIELDS[kind]:
        if normalized(live.get(field)) != normalized(expected.get(field)):
            mismatches.append(field)

    if mismatches:
        print("runtime manifest drift: " + ", ".join(mismatches), file=sys.stderr)
        return 1
    return 0


if __name__ == "__main__":
    raise SystemExit(main())
