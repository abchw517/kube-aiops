#!/usr/bin/env python3
from __future__ import annotations

import re
import sys
from pathlib import Path

ROOT = Path(__file__).resolve().parents[1]

EXPECTED = {
    "KUBERNETES_VERSION": "v1.36.4",
    "KUBECONFORM_KUBERNETES_VERSION": "1.36.0",
    "GO_VERSION": "1.26.5",
    "K8SGPT_OPERATOR_VERSION": "0.2.29",
    "K8SGPT_ENGINE_VERSION": "v0.4.37",
    "K8SGPT_ENGINE_IMAGE": "ghcr.io/k8sgpt-ai/k8sgpt:v0.4.37@sha256:ebf0d1f5a8463190abdf1a9c84282cfbd9ce611e3dc1c490194b7aaf2676d088",
}

OPERATOR_IMAGE = (
    "ghcr.io/k8sgpt-ai/k8sgpt-operator:"
    "v0.2.29@sha256:82d0adcce816182bbfbef9f1d535db93ab0901f08d45a3a9e572c6e795c5bfa8"
)

# Kubernetes API versions removed long before/at the v1.36 support baseline.
FORBIDDEN_API_VERSIONS = {
    "extensions/v1beta1",
    "apps/v1beta1",
    "apps/v1beta2",
    "networking.k8s.io/v1beta1",
    "policy/v1beta1",
    "batch/v1beta1",
    "autoscaling/v2beta1",
    "autoscaling/v2beta2",
    "admissionregistration.k8s.io/v1beta1",
    "apiextensions.k8s.io/v1beta1",
}


def fail(message: str) -> None:
    print(f"[platform-baseline][ERROR] {message}", file=sys.stderr)
    raise SystemExit(1)


def read(path: str) -> str:
    return (ROOT / path).read_text(encoding="utf-8")


def parse_env(path: str) -> dict[str, str]:
    result: dict[str, str] = {}
    for raw in read(path).splitlines():
        line = raw.strip()
        if not line or line.startswith("#"):
            continue
        if "=" not in line:
            fail(f"invalid platform version entry: {line!r}")
        key, value = line.split("=", 1)
        result[key.strip()] = value.strip()
    return result


def require_contains(text: str, needle: str, where: str) -> None:
    if needle not in text:
        fail(f"{where} missing required baseline value: {needle}")


def validate_version_source() -> None:
    actual = parse_env("config/platform-versions.env")
    if actual != EXPECTED:
        fail(f"config/platform-versions.env drift: expected={EXPECTED!r}, actual={actual!r}")


def validate_workflow(path: str) -> None:
    text = read(path)

    for action, ref in re.findall(r"^\s*uses:\s*([^@\s]+)@([^\s#]+)", text, flags=re.MULTILINE):
        if action.startswith("./"):
            continue
        if not re.fullmatch(r"[0-9a-f]{40}", ref):
            fail(f"{path} action is not immutable-SHA pinned: {action}@{ref}")


def validate_go() -> None:
    go_mod = read("go.mod")
    require_contains(go_mod, "go 1.26.0", "go.mod")
    require_contains(go_mod, f"toolchain go{EXPECTED['GO_VERSION']}", "go.mod")

    dockerfile = read("Dockerfile")
    first = dockerfile.splitlines()[0].strip()
    if first != f"FROM golang:{EXPECTED['GO_VERSION']}-alpine3.23 AS builder":
        fail(f"Dockerfile Go builder drift: {first!r}")
    require_contains(dockerfile, "FROM scratch", "Dockerfile")
    require_contains(dockerfile, "USER 65532:65532", "Dockerfile")
    require_contains(dockerfile, "CGO_ENABLED=0", "Dockerfile")


def validate_k8sgpt() -> None:
    engine = read("deploy/k8sgpt/k8sgpt.yaml")
    expected_version = EXPECTED["K8SGPT_ENGINE_IMAGE"].split(":", 1)[1]
    require_contains(engine, f"version: {expected_version}", "deploy/k8sgpt/k8sgpt.yaml")
    require_contains(engine, "runAsNonRoot: true", "deploy/k8sgpt/k8sgpt.yaml")
    require_contains(engine, "allowPrivilegeEscalation: false", "deploy/k8sgpt/k8sgpt.yaml")
    require_contains(engine, "readOnlyRootFilesystem: true", "deploy/k8sgpt/k8sgpt.yaml")
    require_contains(engine, "anonymized: true", "deploy/k8sgpt/k8sgpt.yaml")

    values = read("deploy/k8sgpt/values-production.yaml")
    operator_tag = OPERATOR_IMAGE.split(":", 1)[1]
    require_contains(values, f"tag: {operator_tag}", "deploy/k8sgpt/values-production.yaml")

    for label, image in {
        "K8sGPT Engine": EXPECTED["K8SGPT_ENGINE_IMAGE"],
        "K8sGPT Operator": OPERATOR_IMAGE,
    }.items():
        if not re.search(r"@sha256:[0-9a-f]{64}$", image):
            fail(f"{label} image is not digest-pinned: {image}")


def validate_kubernetes_schema_baseline() -> None:
    preflight = read("preflight.sh")
    require_contains(
        preflight,
        f"-kubernetes-version {EXPECTED['KUBECONFORM_KUBERNETES_VERSION']}",
        "preflight.sh",
    )

    for path in sorted((ROOT / "deploy").rglob("*.y*ml")):
        if path.name == "values.yaml" or path.name.startswith("values-"):
            continue
        text = path.read_text(encoding="utf-8")
        for api_version in re.findall(r"^apiVersion:\s*([^\s#]+)", text, flags=re.MULTILINE):
            if api_version in FORBIDDEN_API_VERSIONS:
                fail(f"removed Kubernetes API {api_version} found in {path.relative_to(ROOT)}")


def validate_backend_api_surface() -> None:
    client = read("internal/kubernetes/client.go")
    for stable_path in ("/api/v1", "/apis/apps/v1"):
        require_contains(client, stable_path, "internal/kubernetes/client.go")

    forbidden_paths = (
        "/apis/extensions/v1beta1",
        "/apis/apps/v1beta1",
        "/apis/apps/v1beta2",
        "/apis/networking.k8s.io/v1beta1",
        "/apis/policy/v1beta1",
        "/apis/batch/v1beta1",
    )
    for path in forbidden_paths:
        if path in client:
            fail(f"backend references removed Kubernetes API path: {path}")

    # K8sGPT v1alpha1 is the upstream CRD API and must not be confused with a
    # deprecated Kubernetes built-in beta API.
    require_contains(client, "/apis/core.k8sgpt.ai/v1alpha1", "internal/kubernetes/client.go")


def main() -> None:
    validate_version_source()
    validate_workflow(".github/workflows/ci.yml")
    validate_go()
    validate_k8sgpt()
    validate_kubernetes_schema_baseline()
    validate_backend_api_surface()
    print("Platform baseline consistency: PASS (Kubernetes v1.36.4)")


if __name__ == "__main__":
    main()
