#!/usr/bin/env python3
"""Assert kube-aiops-api RBAC stays at the exact Phase 2.2 readonly permission set."""

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
    (("",), ("events",), ("get", "list", "watch")),
    (("apps",), ("deployments",), ("get",)),
}

MUST_DENY = {
    ("", "secrets", "get"),
    ("", "pods/log", "get"),
    ("", "events", "create"),
    ("", "events", "update"),
    ("", "events", "patch"),
    ("", "events", "delete"),
    ("", "events", "deletecollection"),
}


def normalized_rule(rule: dict) -> tuple[tuple[str, ...], tuple[str, ...], tuple[str, ...]]:
    return (
        tuple(sorted(rule.get("apiGroups") or [])),
        tuple(sorted(rule.get("resources") or [])),
        tuple(sorted(rule.get("verbs") or [])),
    )


def allows(rules: list[dict], api_group: str, resource: str, verb: str) -> bool:
    return any(
        api_group in (rule.get("apiGroups") or [])
        and resource in (rule.get("resources") or [])
        and verb in (rule.get("verbs") or [])
        for rule in rules
    )


def main() -> int:
    role = yaml.safe_load(ROLE_PATH.read_text(encoding="utf-8"))
    rules = role.get("rules") or []
    actual = {normalized_rule(rule) for rule in rules}

    if actual != EXPECTED:
        print("[api-rbac][ERROR] kube-aiops-api RBAC 与 Phase 2.2 精确只读基线不一致", file=sys.stderr)
        print(f"expected={sorted(EXPECTED)}", file=sys.stderr)
        print(f"actual={sorted(actual)}", file=sys.stderr)
        return 1

    unexpected = sorted(
        (api_group, resource, verb)
        for api_group, resource, verb in MUST_DENY
        if allows(rules, api_group, resource, verb)
    )
    if unexpected:
        print(f"[api-rbac][ERROR] 禁止权限被意外放开: {unexpected}", file=sys.stderr)
        return 1

    print("[api-rbac] Phase 2.2 精确只读权限基线通过（Events 仅 get/list/watch）")
    return 0


if __name__ == "__main__":
    raise SystemExit(main())
