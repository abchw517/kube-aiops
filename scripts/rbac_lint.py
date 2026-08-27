#!/usr/bin/env python3
"""Scan every repository YAML document containing a Role or ClusterRole."""

from __future__ import annotations

import pathlib
import sys

import yaml


FORBIDDEN_VERBS = {
    "bind",
    "create",
    "delete",
    "deletecollection",
    "escalate",
    "impersonate",
    "patch",
    "update",
}
FORBIDDEN_RESOURCES = {"pods/log", "secrets", "serviceaccounts/token"}
ROLE_KINDS = {"Role", "ClusterRole"}


def iter_yaml_documents(root: pathlib.Path):
    for path in sorted(root.rglob("*")):
        if not path.is_file() or path.suffix not in {".yaml", ".yml"}:
            continue
        if ".git" in path.parts:
            continue
        with path.open(encoding="utf-8") as stream:
            for index, document in enumerate(yaml.safe_load_all(stream), start=1):
                if isinstance(document, dict):
                    yield path, index, document


def main() -> int:
    errors: list[str] = []
    roles = 0

    for path, document_index, document in iter_yaml_documents(pathlib.Path(".")):
        if document.get("kind") not in ROLE_KINDS:
            continue
        roles += 1
        name = document.get("metadata", {}).get("name", "<unnamed>")
        prefix = f"{path}:doc[{document_index}] {document['kind']}/{name}"
        for rule_index, rule in enumerate(document.get("rules") or [], start=1):
            verbs = set(rule.get("verbs") or [])
            resources = set(rule.get("resources") or [])
            api_groups = set(rule.get("apiGroups") or [])
            if "*" in verbs:
                errors.append(f"{prefix} rule[{rule_index}] verbs 包含 *")
            if "*" in resources:
                errors.append(f"{prefix} rule[{rule_index}] resources 包含 *")
            if "*" in api_groups:
                errors.append(f"{prefix} rule[{rule_index}] apiGroups 包含 *")
            if rule.get("nonResourceURLs"):
                errors.append(f"{prefix} rule[{rule_index}] 不允许 nonResourceURLs")
            if bad_verbs := verbs & FORBIDDEN_VERBS:
                errors.append(
                    f"{prefix} rule[{rule_index}] 包含禁止 verbs: {sorted(bad_verbs)}"
                )
            if bad_resources := resources & FORBIDDEN_RESOURCES:
                errors.append(
                    f"{prefix} rule[{rule_index}] 包含禁止 resources: "
                    f"{sorted(bad_resources)}"
                )

    if roles == 0:
        errors.append("仓库中未发现任何 Role/ClusterRole")
    if errors:
        for error in errors:
            print(f"[preflight][ERROR] {error}", file=sys.stderr)
        return 1

    print(f"[preflight] RBAC 安全基线通过: 扫描 {roles} 个 Role/ClusterRole")
    return 0


if __name__ == "__main__":
    raise SystemExit(main())
