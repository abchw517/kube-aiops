#!/usr/bin/env python3
"""Render install inputs with optional image repository overrides."""

from __future__ import annotations

import os
import re
import sys
from pathlib import Path

import yaml


DEPLOY_DIR = Path(__file__).resolve().parent.parent / "deploy" / "k8sgpt"
HOST_LABEL = r"[a-z0-9](?:[a-z0-9-]*[a-z0-9])?"
REGISTRY = rf"{HOST_LABEL}(?:\.{HOST_LABEL})*(?::(?P<port>[0-9]{{1,5}}))?"
PATH_COMPONENT = r"[a-z0-9]+(?:(?:[._]|__|-+)[a-z0-9]+)*"
REGISTRY_PREFIX = re.compile(rf"{REGISTRY}(?:/{PATH_COMPONENT})*")
IMAGE_REPOSITORY = re.compile(rf"{REGISTRY}(?:/{PATH_COMPONENT})+")


def validate_repository(value: str, variable: str, pattern: re.Pattern[str]) -> str:
    match = pattern.fullmatch(value)
    if match is None or (match["port"] is not None and not 1 <= int(match["port"]) <= 65535):
        raise ValueError(f"{variable} 必须是镜像仓库路径，例如 harbor.example.com/project/image，不能含协议、标签或 digest")
    return value


def render(output_dir: Path) -> None:
    prefix = os.environ.get("K8SGPT_IMAGE_REGISTRY", "")
    if prefix:
        prefix = validate_repository(prefix.rstrip("/"), "K8SGPT_IMAGE_REGISTRY", REGISTRY_PREFIX)

    values_file = "values-production.yaml"
    engine_file = "k8sgpt.yaml"
    values = yaml.safe_load((DEPLOY_DIR / values_file).read_text(encoding="utf-8"))
    engine = yaml.safe_load((DEPLOY_DIR / engine_file).read_text(encoding="utf-8"))
    manager = values["controllerManager"]
    images = (
        ("Operator", "K8SGPT_OPERATOR_IMAGE_REPOSITORY", manager["manager"]["image"], "tag"),
        ("RBAC Proxy", "K8SGPT_RBAC_PROXY_IMAGE_REPOSITORY", manager["kubeRbacProxy"]["image"], "tag"),
        ("Engine", "K8SGPT_ENGINE_IMAGE_REPOSITORY", engine["spec"], "version"),
    )
    for name, variable, image, version_key in images:
        repository = os.environ.get(variable)
        if repository:
            repository = validate_repository(repository, variable, IMAGE_REPOSITORY)
        elif prefix:
            repository = f"{prefix}/{image['repository'].rsplit('/', 1)[-1]}"
        if repository:
            image["repository"] = repository
            image[version_key] = image[version_key].partition("@")[0]
        print(f"{name} 镜像 {image['repository']}:{image[version_key]}")

    for name, content in ((values_file, values), (engine_file, engine)):
        (output_dir / name).write_text(yaml.safe_dump(content, sort_keys=False), encoding="utf-8")


if __name__ == "__main__":
    try:
        render(Path(sys.argv[1]))
    except (OSError, ValueError) as error:
        print(f"镜像配置错误：{error}", file=sys.stderr)
        sys.exit(1)
