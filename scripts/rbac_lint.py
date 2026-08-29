#!/usr/bin/env python3
"""Scan repository RBAC roles and bindings against the Phase 1.1 baseline."""

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
BINDING_KINDS = {"RoleBinding", "ClusterRoleBinding"}
FORBIDDEN_SUBJECT_GROUPS = {
    "system:authenticated",
    "system:masters",
    "system:unauthenticated",
}


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


def resource_key(document: dict) -> tuple[str, str, str]:
    metadata = document.get("metadata") or {}
    namespace = metadata.get("namespace", "") if document.get("kind") == "Role" else ""
    return document.get("kind", ""), metadata.get("name", ""), namespace


def scan(root: pathlib.Path) -> tuple[list[str], int, int]:
    errors: list[str] = []
    roles = 0
    bindings = 0
    documents = list(iter_yaml_documents(root))
    defined_roles = {
        resource_key(document)
        for _, _, document in documents
        if document.get("kind") in ROLE_KINDS
    }

    for path, document_index, document in documents:
        kind = document.get("kind")
        if kind not in ROLE_KINDS | BINDING_KINDS:
            continue
        name = document.get("metadata", {}).get("name", "<unnamed>")
        prefix = f"{path}:doc[{document_index}] {kind}/{name}"

        if kind in ROLE_KINDS:
            roles += 1
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
            continue

        bindings += 1
        role_ref = document.get("roleRef") or {}
        ref_kind = role_ref.get("kind", "")
        ref_name = role_ref.get("name", "")
        binding_namespace = (document.get("metadata") or {}).get("namespace", "")
        if kind == "ClusterRoleBinding":
            expected_key = ("ClusterRole", ref_name, "")
            if ref_kind != "ClusterRole":
                errors.append(f"{prefix} roleRef.kind 必须是 ClusterRole")
            elif expected_key not in defined_roles:
                errors.append(f"{prefix} roleRef 未引用仓库内受检 ClusterRole: {ref_name}")
        elif ref_kind == "Role":
            if ("Role", ref_name, binding_namespace) not in defined_roles:
                errors.append(f"{prefix} roleRef 未引用同 Namespace 的受检 Role: {ref_name}")
        elif ref_kind == "ClusterRole":
            if ("ClusterRole", ref_name, "") not in defined_roles:
                errors.append(f"{prefix} roleRef 未引用仓库内受检 ClusterRole: {ref_name}")
        else:
            errors.append(f"{prefix} roleRef.kind 非法: {ref_kind or '<empty>'}")

        subjects = document.get("subjects") or []
        if not subjects:
            errors.append(f"{prefix} subjects 不能为空")
        for subject_index, subject in enumerate(subjects, start=1):
            subject_kind = subject.get("kind", "")
            subject_name = subject.get("name", "")
            if subject_kind != "ServiceAccount":
                errors.append(
                    f"{prefix} subject[{subject_index}] 只允许 ServiceAccount，"
                    f"实际为 {subject_kind or '<empty>'}/{subject_name or '<empty>'}"
                )
            if subject_kind == "Group" and subject_name in FORBIDDEN_SUBJECT_GROUPS:
                errors.append(f"{prefix} subject[{subject_index}] 禁止宽泛 Group: {subject_name}")
            if subject_kind == "ServiceAccount":
                namespace = subject.get("namespace") or binding_namespace
                if not subject_name or not namespace:
                    errors.append(
                        f"{prefix} subject[{subject_index}] ServiceAccount 必须包含 name/namespace"
                    )

    return errors, roles, bindings


def main() -> int:
    root = pathlib.Path(sys.argv[1]) if len(sys.argv) > 1 else pathlib.Path(".")
    errors, roles, bindings = scan(root)

    if roles == 0:
        errors.append("仓库中未发现任何 Role/ClusterRole")
    if errors:
        for error in errors:
            print(f"[preflight][ERROR] {error}", file=sys.stderr)
        return 1

    print(
        f"[preflight] RBAC 安全基线通过: 扫描 {roles} 个 Role/ClusterRole，"
        f"{bindings} 个 RoleBinding/ClusterRoleBinding"
    )
    return 0


if __name__ == "__main__":
    raise SystemExit(main())
