#!/usr/bin/env python3
"""Assert kube-aiops-api RBAC stays at the exact readonly permission set."""

from __future__ import annotations

import contextlib
import io
import json
import pathlib
import runpy
import subprocess
import sys
from unittest import mock

import yaml


ROOT = pathlib.Path(__file__).resolve().parent.parent
ROLE_PATH = ROOT / "deploy" / "api" / "clusterrole.yaml"

EXPECTED = {
    (("core.k8sgpt.ai",), ("results",), ("get", "list", "watch")),
    (("",), ("namespaces",), ("list",)),
    (("",), ("nodes",), ("get", "list", "watch")),
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


def verify_node_repair() -> None:
    """验证修复只绑定指定 Deployment 的服务账号，保留只读范围。"""
    check = runpy.run_path(str(ROOT / "scripts" / "check-api-node-rbac.py"))["check"]

    def result(code: int, output: str, error: str = "") -> subprocess.CompletedProcess:
        return subprocess.CompletedProcess([], code, output, error)

    with mock.patch("subprocess.run", side_effect=[
        result(0, "api-runtime"), result(1, "no"), result(0, ""), result(0, "yes"),
    ]) as kubectl, contextlib.redirect_stdout(io.StringIO()):
        assert check("test-context", "portal", "api", True) == 0
        assert all(call.args[0][:3] == ["kubectl", "--context", "test-context"] for call in kubectl.call_args_list)
        assert "--as=system:serviceaccount:portal:api-runtime" in kubectl.call_args_list[1].args[0]
        assert kubectl.call_args_list[3].args == kubectl.call_args_list[1].args
        applied = kubectl.call_args_list[2]
        assert applied.args[0][-3:] == ["apply", "-f", "-"]
        role, binding = json.loads(applied.kwargs["input"])["items"]
        assert role["kind"] == "ClusterRole"
        assert role["rules"] == [{"apiGroups": [""], "resources": ["nodes"], "verbs": ["list"]}]
        assert binding["kind"] == "ClusterRoleBinding"
        assert binding["roleRef"]["name"] == role["metadata"]["name"]
        assert binding["subjects"] == [{"kind": "ServiceAccount", "name": "api-runtime", "namespace": "portal"}]

    for account, answer, repair, message in (
        ("api-runtime", result(1, "no"), False, "缺少"),
        ("", result(1, "no"), True, "专用 ServiceAccount"),
        ("api-runtime", result(1, "", "cannot impersonate"), True, "cannot impersonate"),
    ):
        with mock.patch("subprocess.run", side_effect=[result(0, account), answer]) as kubectl:
            try:
                check("test-context", "portal", "api", repair)
            except RuntimeError as error:
                assert message in str(error)
            else:
                raise AssertionError("Expected local node authorization check to fail")
            assert kubectl.call_count == 2


def main() -> int:
    role = yaml.safe_load(ROLE_PATH.read_text(encoding="utf-8"))
    rules = role.get("rules") or []
    actual = {normalized_rule(rule) for rule in rules}

    if actual != EXPECTED:
        print("[api-rbac][ERROR] kube-aiops-api RBAC 与精确只读基线不一致", file=sys.stderr)
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

    verify_node_repair()
    print("[api-rbac] 精确只读权限基线和节点权限修复验证通过（Nodes 支持 get、list、watch，Namespaces 仅 list）")
    return 0


if __name__ == "__main__":
    raise SystemExit(main())
