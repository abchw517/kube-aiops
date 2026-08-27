#!/usr/bin/env python3
"""Replace the upstream static analyzer RBAC before Helm sends it to the API."""

from __future__ import annotations

import pathlib
import sys

import yaml


ROOT = pathlib.Path(__file__).resolve().parent.parent
BASELINES = {
    ("ClusterRole", "k8sgpt-clusterrole"): ROOT
    / "deploy"
    / "k8sgpt"
    / "clusterrole.yaml",
    ("ClusterRoleBinding", "k8sgpt-clusterrole-binding"): ROOT
    / "deploy"
    / "k8sgpt"
    / "clusterrolebinding.yaml",
}


def main() -> int:
    rendered = [doc for doc in yaml.safe_load_all(sys.stdin) if doc is not None]
    baselines = {}
    for key, path in BASELINES.items():
        with path.open(encoding="utf-8") as stream:
            baselines[key] = yaml.safe_load(stream)

    found = set()
    for index, document in enumerate(rendered):
        key = (document.get("kind"), document.get("metadata", {}).get("name"))
        if key not in baselines:
            continue

        baseline = baselines[key]
        metadata = document.setdefault("metadata", {})
        for field in ("labels", "annotations"):
            metadata.setdefault(field, {}).update(
                baseline.get("metadata", {}).get(field, {})
            )
        if key[0] == "ClusterRole":
            # Preserve Helm ownership metadata while replacing the permission rules.
            document["rules"] = baseline["rules"]
        rendered[index] = document
        found.add(key)

    missing = set(baselines) - found
    if missing:
        expected = ", ".join(f"{kind}/{name}" for kind, name in sorted(missing))
        print(f"post-renderer: expected resources not found: {expected}", file=sys.stderr)
        return 1

    yaml.safe_dump_all(rendered, sys.stdout, sort_keys=False)
    return 0


if __name__ == "__main__":
    raise SystemExit(main())
