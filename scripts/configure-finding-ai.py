#!/usr/bin/env python3
"""Configure Portal AI review from the deployed K8sGPT Engine."""
import argparse
import json
import os
from pathlib import Path
import subprocess
import tempfile
from urllib.parse import urlsplit, urlunsplit

import yaml


def kubectl(context, args, document=None):
    command = ["kubectl", "--context", context, *args]
    result = subprocess.run(command, input=document, text=True, capture_output=True, check=False)
    if result.returncode:
        # Kubernetes validation feedback can include submitted fields. Keep
        # Secret media and captured command output out of the deployment log.
        raise ValueError(f"kubectl {args[0]} 失败，请核对资源、命名空间和当前操作者权限")
    return result.stdout


def chat_endpoint(base):
    url = urlsplit(base or "https://api.openai.com/v1")
    if (url.scheme != "https" or not url.hostname or url.username is not None
            or url.query or url.fragment):
        raise ValueError("Engine Base URL 必须是无账号、查询参数和片段的 HTTPS 地址")
    path = url.path.rstrip("/")
    if not path.endswith("/chat/completions"):
        path += "/chat/completions"
    return urlunsplit(("https", url.netloc, path, "", ""))


def upsert(entries, value):
    entries[:] = [entry for entry in entries if entry.get("name") != value["name"]]
    entries.append(value)


def configure(path, context):
    manifest = yaml.safe_load(path.read_text(encoding="utf-8"))
    if (manifest.get("kind") != "Deployment" or
            manifest["metadata"].get("name") != "kube-aiops-api"):
        raise ValueError("请传入渲染后的 kube-aiops-api Deployment 清单")
    api_ns = manifest["metadata"]["namespace"]
    pod = manifest["spec"]["template"]["spec"]
    api = next(container for container in pod["containers"] if container["name"] == "api")
    env = {entry["name"]: entry.get("value") for entry in api.get("env", [])}
    engine_ns = env.get("K8SGPT_NAMESPACE", "k8sgpt-operator-system")
    engine_name = env.get("K8SGPT_NAME", "k8sgpt-engine")
    if not engine_ns or not engine_name:
        raise ValueError("API 的 K8SGPT_NAMESPACE 和 K8SGPT_NAME 需要使用直接值")
    engine = json.loads(kubectl(context, ["get", "k8sgpt.core.k8sgpt.ai", engine_name,
                                          "-n", engine_ns, "-o", "json"]))
    ai = engine["spec"]["ai"]
    if ai.get("enabled") is not True or ai.get("backend") != "openai":
        raise ValueError("仅支持已启用的 OpenAI 兼容 Engine")
    model = ai["model"].strip()
    if not model:
        raise ValueError("Engine 尚未指定模型")
    url = chat_endpoint(ai.get("baseUrl"))
    reference = ai["secret"]
    source_name, key = reference["name"], reference["key"]
    if not source_name or not key:
        raise ValueError("Engine Secret 引用缺少名称或键")
    local_name = source_name
    if api_ns != engine_ns:
        local_name = "kube-aiops-finding-ai-engine"
        source = json.loads(kubectl(context, ["get", "secret", source_name,
                                               "-n", engine_ns, "-o", "json"]))
        value = source.get("data", {}).get(key)
        if not value:
            raise ValueError("Engine Secret 缺少所引用的密钥")
        secret = {
            "apiVersion": "v1", "kind": "Secret", "type": "Opaque",
            "metadata": {"name": local_name, "namespace": api_ns},
            "data": {key: value},
        }
        kubectl(context, ["apply", "--server-side", "--field-manager=kube-aiops-finding-ai", "-f", "-"],
                json.dumps(secret))

    for name, value in (
        ("FINDING_AI_URL", url), ("FINDING_AI_MODEL", model),
        ("FINDING_AI_KEY_FILE", "/etc/kube-aiops/finding-ai/key"),
    ):
        upsert(api.setdefault("env", []), {"name": name, "value": value})
    upsert(api.setdefault("volumeMounts", []), {
        "name": "finding-ai", "mountPath": "/etc/kube-aiops/finding-ai", "readOnly": True,
    })
    upsert(pod.setdefault("volumes", []), {
        "name": "finding-ai", "secret": {"secretName": local_name, "items": [{"key": key, "path": "key"}]},
    })
    with tempfile.NamedTemporaryFile(mode="w", dir=path.parent, encoding="utf-8", delete=False) as output:
        temporary = Path(output.name)
        try:
            yaml.safe_dump(manifest, output, sort_keys=False, allow_unicode=True)
            output.close()
            os.chmod(temporary, path.stat().st_mode & 0o777)
            temporary.replace(path)
        finally:
            temporary.unlink(missing_ok=True)


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("manifest", type=Path)
    parser.add_argument("--context", required=True, help="Target Kubernetes context")
    args = parser.parse_args()
    if not args.context.strip():
        parser.error("缺少 Kubernetes context")
    try:
        configure(args.manifest, args.context)
    except (KeyError, StopIteration, AttributeError, TypeError, ValueError, OSError, yaml.YAMLError):
        # Do not interpolate values from Secret objects or YAML parser input.
        parser.exit(1, "无法复用 Engine 配置，请检查 API Deployment、Engine AI 配置、Secret 引用和操作者权限。\n")
    print("已在 API 清单中配置现有 Engine 模型与密钥引用")


if __name__ == "__main__":
    main()
