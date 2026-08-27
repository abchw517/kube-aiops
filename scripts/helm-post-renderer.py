#!/usr/bin/env python3
"""Replace the upstream static analyzer RBAC before Helm sends it to the API."""

from __future__ import annotations

import pathlib
import sys

import yaml


ROOT = pathlib.Path(__file__).resolve().parent.parent
BASELINE = ROOT / "deploy" / "k8sgpt" / "clusterrole.yaml"


def main() -> int:
    rendered = [doc for doc in yaml.safe_load_all(sys.stdin) if doc is not None]
    with BASELINE.open(encoding="utf-8") as stream:
        restricted_role = yaml.safe_load(stream)

    found = False
    for index, document in enumerate(rendered):
        if (
            document.get("kind") == "ClusterRole"
            and document.get("metadata", {}).get("name") == "k8sgpt-clusterrole"
        ):
            # Preserve Helm ownership metadata while replacing only the rules.
            document["rules"] = restricted_role["rules"]
            rendered[index] = document
            found = True

    if not found:
        print("post-renderer: expected ClusterRole/k8sgpt-clusterrole not found", file=sys.stderr)
        return 1

    yaml.safe_dump_all(rendered, sys.stdout, sort_keys=False)
    return 0


if __name__ == "__main__":
    raise SystemExit(main())
