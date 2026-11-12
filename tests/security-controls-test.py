#!/usr/bin/env python3
"""Regression tests for Helm post-rendering and repository RBAC linting."""

from __future__ import annotations

import json
import os
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


def test_install_image_overrides_preserve_the_deployment_baseline() -> None:
    env = {name: value for name, value in os.environ.items() if not name.startswith("K8SGPT_")}
    renderer = ROOT / "scripts/render-k8sgpt-install.py"
    filenames = ("values-production.yaml", "k8sgpt.yaml")

    def read_inputs(directory: pathlib.Path) -> list[dict]:
        return [yaml.safe_load((directory / name).read_text()) for name in filenames]

    with tempfile.TemporaryDirectory() as temporary:
        directory = pathlib.Path(temporary)
        command = [sys.executable, str(renderer), str(directory)]
        expected = read_inputs(ROOT / "deploy/k8sgpt")
        subprocess.run(command, env=env, capture_output=True, check=True)
        assert read_inputs(directory) == expected

        subprocess.run(command, env={
            **env,
            "K8SGPT_ENGINE_IMAGE_REPOSITORY": "other.example.com/team/custom-engine",
        }, capture_output=True, check=True)
        expected[1]["spec"]["repository"] = "other.example.com/team/custom-engine"
        expected[1]["spec"]["version"] = "v0.4.37"
        assert read_inputs(directory) == expected

        subprocess.run(command, env={
            **env,
            "K8SGPT_IMAGE_REGISTRY": "harbor.example.com:8443/public/",
            "K8SGPT_ENGINE_IMAGE_REPOSITORY": "other.example.com/team/custom-engine",
        }, capture_output=True, check=True)
        manager = expected[0]["controllerManager"]
        manager["manager"]["image"] = {
            "repository": "harbor.example.com:8443/public/k8sgpt-operator", "tag": "v0.2.29",
        }
        manager["kubeRbacProxy"]["image"] = {
            "repository": "harbor.example.com:8443/public/kube-rbac-proxy", "tag": "v0.19.1",
        }
        assert read_inputs(directory) == expected

        result = subprocess.run(command, env={
            **env,
            "K8SGPT_OPERATOR_IMAGE_REPOSITORY": "harbor.example.com/public/k8sgpt-operator:latest",
        }, capture_output=True, text=True, check=False)
        assert result.returncode != 0
        assert "K8SGPT_OPERATOR_IMAGE_REPOSITORY" in result.stderr


def test_install_operator_resources_only_override_the_manager() -> None:
    env = {name: value for name, value in os.environ.items() if not name.startswith("K8SGPT_")}
    deploy_dir = ROOT / "deploy/k8sgpt"
    with tempfile.TemporaryDirectory() as temporary:
        directory = pathlib.Path(temporary)
        command = [sys.executable, str(ROOT / "scripts/render-k8sgpt-install.py"), temporary]
        expected = yaml.safe_load((deploy_dir / "values-production.yaml").read_text())
        resources = expected["controllerManager"]["manager"]["resources"]
        subprocess.run(command, env={**env, "K8SGPT_OPERATOR_MEMORY_LIMIT": "1Gi"}, check=True, capture_output=True)
        resources["limits"]["memory"] = "1Gi"
        assert yaml.safe_load((directory / "values-production.yaml").read_text()) == expected

        subprocess.run(command, env={
            **env,
            "K8SGPT_OPERATOR_CPU_REQUEST": "250m",
            "K8SGPT_OPERATOR_MEMORY_REQUEST": "512Mi",
            "K8SGPT_OPERATOR_CPU_LIMIT": "1",
            "K8SGPT_OPERATOR_MEMORY_LIMIT": "1Gi",
        }, check=True, capture_output=True)
        resources["requests"] = {"cpu": "250m", "memory": "512Mi"}
        resources["limits"]["cpu"] = "1"
        assert yaml.safe_load((directory / "values-production.yaml").read_text()) == expected
        assert yaml.safe_load((directory / "k8sgpt.yaml").read_text()) == yaml.safe_load(
            (deploy_dir / "k8sgpt.yaml").read_text()
        )

        for overrides, error in (
            ({"K8SGPT_OPERATOR_MEMORY_LIMIT": "1GB"}, "K8SGPT_OPERATOR_MEMORY_LIMIT"),
            ({"K8SGPT_OPERATOR_MEMORY_REQUEST": "1Gi", "K8SGPT_OPERATOR_MEMORY_LIMIT": "900Mi"}, "不能大于"),
        ):
            result = subprocess.run(command, env={**env, **overrides}, text=True, capture_output=True, check=False)
            assert result.returncode != 0
            assert error in result.stderr


def test_install_engine_ai_overrides() -> None:
    env = {name: value for name, value in os.environ.items() if not name.startswith("K8SGPT_")}
    source = ROOT / "deploy/k8sgpt/k8sgpt.yaml"
    with tempfile.TemporaryDirectory() as temporary:
        command = [sys.executable, str(ROOT / "scripts/render-k8sgpt-install.py"), temporary]
        output = pathlib.Path(temporary) / "k8sgpt.yaml"
        expected = yaml.safe_load(source.read_text())
        subprocess.run(command, env={**env, "K8SGPT_AI_LANGUAGE": "Chinese"}, capture_output=True, check=True)
        expected["spec"]["ai"]["language"] = "Chinese"
        assert yaml.safe_load(output.read_text()) == expected

        subprocess.run(command, env={
            **env,
            "K8SGPT_AI_MODEL": "glm-5.2",
            "K8SGPT_AI_BASE_URL": "https://open.bigmodel.cn/api/paas/v4",
            "K8SGPT_AI_LANGUAGE": "Chinese",
        }, capture_output=True, check=True)
        expected["spec"]["ai"].update(model="glm-5.2", baseUrl="https://open.bigmodel.cn/api/paas/v4")
        assert yaml.safe_load(output.read_text()) == expected

        for value in ("file:///tmp/provider", "https://user:private@example.com/v1", "https://example.com/v1?key=private"):
            result = subprocess.run(command, env={**env, "K8SGPT_AI_BASE_URL": value}, text=True, capture_output=True, check=False)
            assert result.returncode != 0
            assert "K8SGPT_AI_BASE_URL" in result.stderr and "private" not in result.stderr


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
