#!/usr/bin/env python3
"""Render install inputs with optional image, Operator resource and Engine AI overrides."""

from __future__ import annotations

import os
import re
import sys
from decimal import Decimal, DecimalException
from pathlib import Path
from urllib.parse import urlsplit

import yaml


DEPLOY_DIR = Path(__file__).resolve().parent.parent / "deploy" / "k8sgpt"
HOST_LABEL = r"[a-z0-9](?:[a-z0-9-]*[a-z0-9])?"
REGISTRY = rf"{HOST_LABEL}(?:\.{HOST_LABEL})*(?::(?P<port>[0-9]{{1,5}}))?"
PATH_COMPONENT = r"[a-z0-9]+(?:(?:[._]|__|-+)[a-z0-9]+)*"
REGISTRY_PREFIX = re.compile(rf"{REGISTRY}(?:/{PATH_COMPONENT})*")
IMAGE_REPOSITORY = re.compile(rf"{REGISTRY}(?:/{PATH_COMPONENT})+")
QUANTITY = re.compile(r"(\+?(?:[0-9]+(?:\.[0-9]*)?|\.[0-9]+))([eE][+-]?[0-9]+|[KMGTPE]i|[numkMGTPE])?")
DECIMAL_EXPONENTS = {"": 0, "n": -9, "u": -6, "m": -3, "k": 3, "M": 6, "G": 9, "T": 12, "P": 15, "E": 18}
BINARY_FACTORS = {f"{unit}i": 1024**power for power, unit in enumerate("KMGTPE", start=1)}


def validate_repository(value: str, variable: str, pattern: re.Pattern[str]) -> str:
    match = pattern.fullmatch(value)
    if match is None or (match["port"] is not None and not 1 <= int(match["port"]) <= 65535):
        raise ValueError(f"{variable} 必须是镜像仓库路径，例如 harbor.example.com/project/image，不能含协议、标签或 digest")
    return value


def positive_quantity(value: str, variable: str) -> Decimal:
    match = QUANTITY.fullmatch(value)
    if match:
        amount = Decimal(match[1])
        suffix = match[2] or ""
        try:
            if suffix in BINARY_FACTORS:
                quantity = amount * BINARY_FACTORS[suffix]
            else:
                exponent = DECIMAL_EXPONENTS[suffix] if suffix in DECIMAL_EXPONENTS else int(suffix[1:])
                quantity = amount.scaleb(exponent)
            if quantity.is_finite() and quantity > 0:
                return quantity
        except (DecimalException, ValueError):
            pass
    raise ValueError(f"{variable} 必须是正数的 Kubernetes 资源量，例如 500m、1、512Mi 或 1Gi")


def configure_operator_resources(resources: dict) -> None:
    for resource in ("cpu", "memory"):
        request_key = f"K8SGPT_OPERATOR_{resource.upper()}_REQUEST"
        limit_key = f"K8SGPT_OPERATOR_{resource.upper()}_LIMIT"
        request = os.environ.get(request_key)
        limit = os.environ.get(limit_key)
        if not (request or limit):
            continue
        request = request or str(resources["requests"][resource])
        limit = limit or str(resources["limits"][resource])
        if positive_quantity(request, request_key) > positive_quantity(limit, limit_key):
            raise ValueError(f"{request_key} 不能大于 {limit_key}")
        resources["requests"][resource] = request
        resources["limits"][resource] = limit


def configure_engine_ai(ai: dict) -> None:
    for variable, field in (
        ("K8SGPT_AI_MODEL", "model"),
        ("K8SGPT_AI_BASE_URL", "baseUrl"),
        ("K8SGPT_AI_LANGUAGE", "language"),
    ):
        value = os.environ.get(variable)
        if not value:
            continue
        if value != value.strip() or not value.isprintable():
            raise ValueError(f"{variable} 必须是有效的单行字符串")
        if field == "baseUrl":
            try:
                url = urlsplit(value)
                valid = (url.scheme in {"http", "https"} and url.hostname
                         and url.username is None and url.port != 0
                         and not any(c.isspace() or c in "?#\\" for c in value))
            except ValueError:
                valid = False
            if not valid:
                raise ValueError(f"{variable} 必须是 HTTP(S) Base URL，凭据请通过 Secret 提供")
        ai[field] = value


def render(output_dir: Path) -> None:
    prefix = os.environ.get("K8SGPT_IMAGE_REGISTRY", "")
    if prefix:
        prefix = validate_repository(prefix.rstrip("/"), "K8SGPT_IMAGE_REGISTRY", REGISTRY_PREFIX)

    values_file = "values-production.yaml"
    engine_file = "k8sgpt.yaml"
    values = yaml.safe_load((DEPLOY_DIR / values_file).read_text(encoding="utf-8"))
    engine = yaml.safe_load((DEPLOY_DIR / engine_file).read_text(encoding="utf-8"))
    configure_engine_ai(engine["spec"]["ai"])
    manager = values["controllerManager"]
    resources = manager["manager"]["resources"]
    configure_operator_resources(resources)
    print(f"Operator 资源 requests cpu={resources['requests']['cpu']} memory={resources['requests']['memory']} "
          f"limits cpu={resources['limits']['cpu']} memory={resources['limits']['memory']}")
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
        print(f"安装配置错误：{error}", file=sys.stderr)
        sys.exit(1)
