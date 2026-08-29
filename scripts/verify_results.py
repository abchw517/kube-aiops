#!/usr/bin/env python3
"""Validate fresh K8sGPT Result objects supplied on stdin."""

from __future__ import annotations

import argparse
from datetime import datetime, timezone
import json
import sys


def parse_rfc3339(value: str) -> datetime:
    if not value:
        return datetime.min.replace(tzinfo=timezone.utc)
    parsed = datetime.fromisoformat(value.replace("Z", "+00:00"))
    if parsed.tzinfo is None:
        raise ValueError("timestamp must include timezone")
    return parsed.astimezone(timezone.utc)


def main() -> int:
    parser = argparse.ArgumentParser()
    parser.add_argument("--since", default="")
    parser.add_argument("--expected-kinds", default="")
    args = parser.parse_args()

    try:
        since = parse_rfc3339(args.since)
        document = json.load(sys.stdin)
    except (ValueError, json.JSONDecodeError) as exc:
        print(f"result validation input error: {exc}", file=sys.stderr)
        return 2

    if not isinstance(document, dict):
        print("result validation schema error: root must be an object", file=sys.stderr)
        return 3
    raw_items = document.get("items", [])
    if not isinstance(raw_items, list):
        print("result validation schema error: items must be an array", file=sys.stderr)
        return 3

    expected = {value.strip() for value in args.expected_kinds.split(",") if value.strip()}
    items = []
    for index, item in enumerate(raw_items):
        if not isinstance(item, dict):
            print(
                f"result validation schema error: items[{index}] must be an object",
                file=sys.stderr,
            )
            return 3
        metadata = item.get("metadata", {})
        spec = item.get("spec", {})
        if not isinstance(metadata, dict) or not isinstance(spec, dict):
            print(
                f"result validation schema error: items[{index}] metadata/spec must be objects",
                file=sys.stderr,
            )
            return 3
        created_value = metadata.get("creationTimestamp", "")
        if not isinstance(created_value, str) or not created_value:
            print(
                f"result validation schema error: items[{index}] has invalid creationTimestamp",
                file=sys.stderr,
            )
            return 3
        try:
            created = parse_rfc3339(created_value)
        except ValueError as exc:
            print(
                f"result validation schema error: items[{index}] creationTimestamp: {exc}",
                file=sys.stderr,
            )
            return 3
        kind = spec.get("kind", "")
        details = spec.get("details", "")
        if not isinstance(kind, str) or not isinstance(details, str):
            print(
                f"result validation schema error: items[{index}] kind/details must be strings",
                file=sys.stderr,
            )
            return 3
        if created >= since:
            items.append(item)

    kinds_with_details = {
        item.get("spec", {}).get("kind", "")
        for item in items
        if item.get("spec", {}).get("details", "").strip()
    }
    detail_count = sum(
        bool(item.get("spec", {}).get("details", "").strip()) for item in items
    )
    print(
        json.dumps(
            {
                "count": len(items),
                "detail_count": detail_count,
                "missing_kinds": sorted(expected - kinds_with_details),
            }
        )
    )
    return 0


if __name__ == "__main__":
    raise SystemExit(main())
