#!/usr/bin/env python3
"""检查本地采集 API 的节点读取权限，按显式请求修复。"""

import argparse
import hashlib
import json
import subprocess
import sys


def check(context, namespace, deployment, repair):
    """核对实际 Deployment 的服务账号，修复时只授予 list nodes。"""
    kubectl = ["kubectl", "--context", context]
    account = subprocess.run(
        [*kubectl, "-n", namespace, "get", "deployment", deployment,
         "-o", "jsonpath={.spec.template.spec.serviceAccountName}"],
        check=True, capture_output=True, text=True, timeout=20,
    ).stdout.strip() or "default"
    subject = f"system:serviceaccount:{namespace}:{account}"
    review = [
        *kubectl, "auth", "can-i", "list", "nodes", f"--as={subject}",
        "--as-group=system:serviceaccounts",
        f"--as-group=system:serviceaccounts:{namespace}", "--as-group=system:authenticated",
    ]

    def allowed():
        result = subprocess.run(review, capture_output=True, text=True, timeout=20)
        if result.returncode == 0 and result.stdout.strip() == "yes":
            return True
        if result.returncode == 1 and result.stdout.strip() == "no":
            return False
        raise RuntimeError(result.stderr.strip() or "无法验证服务账号权限")

    if allowed():
        print(f"{subject} 已具备集群级 list nodes 权限")
        return 0
    if not repair:
        raise RuntimeError(f"{subject} 缺少集群级 list nodes 权限")
    if account == "default":
        raise RuntimeError("请先为 API Deployment 配置专用 ServiceAccount")

    group = "rbac.authorization.k8s.io"
    suffix = hashlib.sha256(f"{namespace}/{deployment}".encode()).hexdigest()[:16]
    name = f"kube-aiops-local-node-reader-{suffix}"
    meta = {"name": name, "labels": {"app.kubernetes.io/part-of": "kube-aiops"}}
    manifest = {
        "apiVersion": "v1",
        "kind": "List",
        "items": [
            {
                "apiVersion": f"{group}/v1", "kind": "ClusterRole", "metadata": meta,
                "rules": [{"apiGroups": [""], "resources": ["nodes"], "verbs": ["list"]}],
            },
            {
                "apiVersion": f"{group}/v1", "kind": "ClusterRoleBinding", "metadata": meta,
                "roleRef": {"apiGroup": group, "kind": "ClusterRole", "name": name},
                "subjects": [{"kind": "ServiceAccount", "name": account, "namespace": namespace}],
            },
        ],
    }
    subprocess.run(
        [*kubectl, "apply", "-f", "-"], input=json.dumps(manifest), text=True, check=True, timeout=30,
    )
    if not allowed():
        raise RuntimeError(f"已应用规则，但 {subject} 的节点读取仍未通过验证")
    print(f"{subject} 的集群级 list nodes 权限已验证")
    return 0


def main():
    """参数固定生效集群，避免误操作当前 kubectl 上下文。"""
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("--context", required=True)
    parser.add_argument("--namespace", default="kube-aiops-system")
    parser.add_argument("--deployment", default="kube-aiops-api")
    parser.add_argument("--repair", action="store_true")
    args = parser.parse_args()
    try:
        return check(args.context, args.namespace, args.deployment, args.repair)
    except (OSError, subprocess.SubprocessError, RuntimeError) as error:
        print(getattr(error, "stderr", None) or str(error), file=sys.stderr)
        return 1


if __name__ == "__main__":
    raise SystemExit(main())
