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

    expected = {value for value in args.expected_kinds.split(",") if value}
    items = []
    for item in document.get("items", []):
        try:
            created = parse_rfc3339(item.get("metadata", {}).get("creationTimestamp", ""))
        except ValueError:
            continue
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
