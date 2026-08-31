#!/usr/bin/env python3
"""Assert kube-aiops-api RBAC stays at the exact Phase 1.2.2 permission set."""

from __future__ import annotations

import pathlib
import sys

import yaml


ROOT = pathlib.Path(__file__).resolve().parent.parent
ROLE_PATH = ROOT / "deploy" / "api" / "clusterrole.yaml"

EXPECTED = {
    (("core.k8sgpt.ai",), ("results",), ("get", "list", "watch")),
    (("",), ("namespaces",), ("list",)),
    (("",), ("pods",), ("get",)),
    (("apps",), ("deployments",), ("get",)),
}


def normalized_rule(rule: dict) -> tuple[tuple[str, ...], tuple[str, ...], tuple[str, ...]]:
    return (
        tuple(sorted(rule.get("apiGroups") or [])),
        tuple(sorted(rule.get("resources") or [])),
        tuple(sorted(rule.get("verbs") or [])),
    )


def main() -> int:
    role = yaml.safe_load(ROLE_PATH.read_text(encoding="utf-8"))
    actual = {normalized_rule(rule) for rule in role.get("rules") or []}

    if actual != EXPECTED:
        print("[api-rbac][ERROR] kube-aiops-api RBAC 与 Phase 1.2.2 基线不一致", file=sys.stderr)
        print(f"expected={sorted(EXPECTED)}", file=sys.stderr)
        print(f"actual={sorted(actual)}", file=sys.stderr)
        return 1

    print("[api-rbac] Phase 1.2.2 精确权限基线通过")
    return 0


if __name__ == "__main__":
    raise SystemExit(main())
