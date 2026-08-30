#!/usr/bin/env python3
"""Replace the upstream static analyzer RBAC before Helm sends it to the API."""

from __future__ import annotations

import pathlib
import sys

import yaml


ROOT = pathlib.Path(__file__).resolve().parent.parent
BASELINES = {
    ("ServiceAccount", "k8sgpt"): ROOT
    / "deploy"
    / "k8sgpt"
    / "serviceaccount.yaml",
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
    malformed = [index for index, doc in enumerate(rendered, start=1) if not isinstance(doc, dict)]
    if malformed:
        indexes = ", ".join(str(index) for index in malformed)
        print(
            f"post-renderer: rendered YAML documents must be mappings: {indexes}",
            file=sys.stderr,
        )
        return 1
    baselines = {}
    for key, path in BASELINES.items():
        with path.open(encoding="utf-8") as stream:
            baselines[key] = yaml.safe_load(stream)

    found = {key: 0 for key in baselines}
    for index, document in enumerate(rendered):
        key = (document.get("kind"), document.get("metadata", {}).get("name"))
        if key not in baselines:
            continue

        found[key] += 1

        baseline = baselines[key]
        metadata = document.setdefault("metadata", {})
        for field in ("labels", "annotations"):
            metadata.setdefault(field, {}).update(
                baseline.get("metadata", {}).get(field, {})
            )
        if key[0] == "ClusterRole":
            # Preserve Helm ownership metadata while replacing the permission rules.
            document["rules"] = baseline["rules"]
        elif key[0] == "ClusterRoleBinding":
            # Binding identity is also a security boundary. Never trust subjects or
            # roleRef supplied by an upstream chart, even when its name is expected.
            document["roleRef"] = baseline["roleRef"]
            document["subjects"] = baseline["subjects"]
        elif key[0] == "ServiceAccount":
            document["automountServiceAccountToken"] = baseline[
                "automountServiceAccountToken"
            ]
            document.pop("imagePullSecrets", None)
            document.pop("secrets", None)
        rendered[index] = document

    missing = {key for key, count in found.items() if count == 0}
    if missing:
        expected = ", ".join(f"{kind}/{name}" for kind, name in sorted(missing))
        print(f"post-renderer: expected resources not found: {expected}", file=sys.stderr)
        return 1

    duplicates = {key: count for key, count in found.items() if count != 1}
    if duplicates:
        unexpected = ", ".join(
            f"{kind}/{name} count={count}"
            for (kind, name), count in sorted(duplicates.items())
        )
        print(
            f"post-renderer: expected exactly one critical resource: {unexpected}",
            file=sys.stderr,
        )
        return 1

    yaml.safe_dump_all(rendered, sys.stdout, sort_keys=False)
    return 0


if __name__ == "__main__":
    raise SystemExit(main())
