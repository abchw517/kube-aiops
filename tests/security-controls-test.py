#!/usr/bin/env python3
"""Regression tests for Helm post-rendering and repository RBAC linting."""

from __future__ import annotations

import json
import pathlib
import re
import subprocess
import sys
import tempfile

import yaml


ROOT = pathlib.Path(__file__).resolve().parent.parent


def run_post_renderer(source: str) -> subprocess.CompletedProcess[str]:
    return subprocess.run(
        [sys.executable, str(ROOT / "scripts" / "helm-post-renderer.py")],
        input=source,
        text=True,
        capture_output=True,
        check=False,
    )


def test_post_renderer_replaces_binding_identity() -> None:
    source = """
apiVersion: v1
kind: ServiceAccount
metadata:
  name: k8sgpt
  namespace: k8sgpt-operator-system
  labels:
    app.kubernetes.io/managed-by: Helm
imagePullSecrets:
  - name: unexpected-secret
---
apiVersion: rbac.authorization.k8s.io/v1
kind: ClusterRole
metadata:
  name: k8sgpt-clusterrole
  labels:
    app.kubernetes.io/managed-by: Helm
rules:
  - apiGroups: [""]
    resources: [secrets]
    verbs: [get]
---
apiVersion: rbac.authorization.k8s.io/v1
kind: ClusterRoleBinding
metadata:
  name: k8sgpt-clusterrole-binding
  labels:
    app.kubernetes.io/managed-by: Helm
roleRef:
  apiGroup: rbac.authorization.k8s.io
  kind: ClusterRole
  name: cluster-admin
subjects:
  - apiGroup: rbac.authorization.k8s.io
    kind: Group
    name: system:authenticated
"""
    completed = run_post_renderer(source)
    assert completed.returncode == 0, completed.stderr
    documents = list(yaml.safe_load_all(completed.stdout))
    role = next(item for item in documents if item["kind"] == "ClusterRole")
    service_account = next(
        item for item in documents if item["kind"] == "ServiceAccount"
    )
    binding = next(
        item for item in documents if item["kind"] == "ClusterRoleBinding"
    )
    assert "secrets" not in {
        resource for rule in role["rules"] for resource in rule.get("resources", [])
    }
    assert binding["roleRef"]["name"] == "k8sgpt-clusterrole"
    assert binding["subjects"] == [
        {
            "kind": "ServiceAccount",
            "name": "k8sgpt",
            "namespace": "k8sgpt-operator-system",
        }
    ]
    assert service_account["automountServiceAccountToken"] is True
    assert "imagePullSecrets" not in service_account
    expected_labels = {
        "app.kubernetes.io/managed-by": "Helm",
        "app.kubernetes.io/part-of": "kube-aiops",
        "app.kubernetes.io/instance": "k8sgpt-operator",
    }
    for key, value in expected_labels.items():
        assert binding["metadata"]["labels"][key] == value


def test_post_renderer_fails_closed_when_expected_resource_is_missing() -> None:
    completed = run_post_renderer(
        "apiVersion: v1\nkind: ConfigMap\nmetadata:\n  name: unrelated\n"
    )
    assert completed.returncode != 0
    assert "expected resources not found" in completed.stderr


def test_post_renderer_fails_closed_on_duplicate_critical_resource() -> None:
    baseline = """
apiVersion: v1
kind: ServiceAccount
metadata:
  name: k8sgpt
  namespace: k8sgpt-operator-system
---
apiVersion: rbac.authorization.k8s.io/v1
kind: ClusterRole
metadata:
  name: k8sgpt-clusterrole
rules: []
---
apiVersion: rbac.authorization.k8s.io/v1
kind: ClusterRoleBinding
metadata:
  name: k8sgpt-clusterrole-binding
roleRef:
  apiGroup: rbac.authorization.k8s.io
  kind: ClusterRole
  name: k8sgpt-clusterrole
subjects: []
"""
    duplicate_role = """
---
apiVersion: rbac.authorization.k8s.io/v1
kind: ClusterRole
metadata:
  name: k8sgpt-clusterrole
rules: []
"""
    completed = run_post_renderer(baseline + duplicate_role)
    assert completed.returncode != 0
    assert "expected exactly one critical resource" in completed.stderr


def run_rbac_lint(documents: list[dict]) -> subprocess.CompletedProcess[str]:
    with tempfile.TemporaryDirectory(prefix="kube-aiops-rbac-test.") as directory:
        fixture = pathlib.Path(directory) / "rbac.yaml"
        fixture.write_text(yaml.safe_dump_all(documents), encoding="utf-8")
        return subprocess.run(
            [sys.executable, str(ROOT / "scripts" / "rbac_lint.py"), directory],
            text=True,
            capture_output=True,
            check=False,
        )


def readonly_role() -> dict:
    return {
        "apiVersion": "rbac.authorization.k8s.io/v1",
        "kind": "ClusterRole",
        "metadata": {"name": "reader"},
        "rules": [
            {"apiGroups": [""], "resources": ["pods"], "verbs": ["get"]}
        ],
    }


def test_rbac_lint_accepts_scoped_service_account_binding() -> None:
    binding = {
        "apiVersion": "rbac.authorization.k8s.io/v1",
        "kind": "ClusterRoleBinding",
        "metadata": {"name": "reader"},
        "roleRef": {
            "apiGroup": "rbac.authorization.k8s.io",
            "kind": "ClusterRole",
            "name": "reader",
        },
        "subjects": [
            {"kind": "ServiceAccount", "name": "reader", "namespace": "safe"}
        ],
    }
    completed = run_rbac_lint([readonly_role(), binding])
    assert completed.returncode == 0, completed.stderr


def test_rbac_lint_rejects_external_admin_and_broad_group() -> None:
    binding = {
        "apiVersion": "rbac.authorization.k8s.io/v1",
        "kind": "ClusterRoleBinding",
        "metadata": {"name": "unsafe"},
        "roleRef": {
            "apiGroup": "rbac.authorization.k8s.io",
            "kind": "ClusterRole",
            "name": "cluster-admin",
        },
        "subjects": [
            {
                "apiGroup": "rbac.authorization.k8s.io",
                "kind": "Group",
                "name": "system:authenticated",
            }
        ],
    }
    completed = run_rbac_lint([readonly_role(), binding])
    assert completed.returncode != 0
    assert "未引用仓库内受检 ClusterRole" in completed.stderr
    assert "只允许 ServiceAccount" in completed.stderr
    assert "禁止宽泛 Group" in completed.stderr


def test_fixed_resource_identity_is_consistent() -> None:
    expected = {
        "NAMESPACE": "k8sgpt-operator-system",
        "RELEASE_NAME": "k8sgpt-operator",
        "SECRET_NAME": "k8sgpt-openai-secret",
        "K8SGPT_NAME": "k8sgpt-engine",
    }
    for script_name in ("install.sh", "uninstall.sh", "verify.sh", "status.sh"):
        source = (ROOT / script_name).read_text(encoding="utf-8")
        for variable, value in expected.items():
            if variable == "SECRET_NAME" and script_name == "status.sh":
                continue
            assert f'readonly {variable}="{value}"' in source, (
                f"{script_name} drifted from fixed {variable}={value}"
            )

    namespace = yaml.safe_load(
        (ROOT / "deploy/k8sgpt/namespace.yaml").read_text(encoding="utf-8")
    )
    service_account = yaml.safe_load(
        (ROOT / "deploy/k8sgpt/serviceaccount.yaml").read_text(encoding="utf-8")
    )
    binding = yaml.safe_load(
        (ROOT / "deploy/k8sgpt/clusterrolebinding.yaml").read_text(encoding="utf-8")
    )
    engine = yaml.safe_load(
        (ROOT / "deploy/k8sgpt/k8sgpt.yaml").read_text(encoding="utf-8")
    )
    assert namespace["metadata"]["name"] == expected["NAMESPACE"]
    assert service_account["metadata"]["namespace"] == expected["NAMESPACE"]
    assert binding["subjects"][0]["namespace"] == expected["NAMESPACE"]
    assert engine["metadata"] == {
        **engine["metadata"],
        "name": expected["K8SGPT_NAME"],
        "namespace": expected["NAMESPACE"],
    }
    network_policy = yaml.safe_load(
        (ROOT / "deploy/k8sgpt/networkpolicy.yaml").read_text(encoding="utf-8")
    )
    assert network_policy["metadata"]["namespace"] == expected["NAMESPACE"]


def test_runtime_artifacts_are_digest_pinned() -> None:
    values = yaml.safe_load(
        (ROOT / "deploy/k8sgpt/values-production.yaml").read_text(encoding="utf-8")
    )
    engine = yaml.safe_load(
        (ROOT / "deploy/k8sgpt/k8sgpt.yaml").read_text(encoding="utf-8")
    )
    image_versions = [
        values["controllerManager"]["manager"]["image"]["tag"],
        values["controllerManager"]["kubeRbacProxy"]["image"]["tag"],
        engine["spec"]["version"],
    ]
    digest_pattern = re.compile(r"^[^@]+@sha256:[0-9a-f]{64}$")
    assert all(digest_pattern.fullmatch(value) for value in image_versions)

    install_source = (ROOT / "install.sh").read_text(encoding="utf-8")
    assert re.search(
        r'OPERATOR_CHART_SHA256="[0-9a-f]{64}"', install_source
    )
    assert "sha256sum --check --status" in install_source


def main() -> int:
    tests = [value for name, value in globals().items() if name.startswith("test_")]
    failures = []
    for test in tests:
        try:
            test()
            print(f"PASS: {test.__name__}")
        except Exception as exc:  # noqa: BLE001 - standalone test runner
            failures.append((test.__name__, exc))
            print(f"FAIL: {test.__name__}: {exc}", file=sys.stderr)
    if failures:
        print(json.dumps([name for name, _ in failures]), file=sys.stderr)
        return 1
    return 0


if __name__ == "__main__":
    raise SystemExit(main())
