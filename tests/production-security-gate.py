#!/usr/bin/env python3
"""Static Phase 1.4.5 production-composition guard."""

from __future__ import annotations

import pathlib
import sys

import yaml

ROOT = pathlib.Path(__file__).resolve().parent.parent


def fail(message: str) -> int:
    print(f"[production-security-gate][ERROR] {message}", file=sys.stderr)
    return 1


def main() -> int:
    deployment = yaml.safe_load((ROOT / "deploy/api/deployment.yaml").read_text(encoding="utf-8"))
    containers = deployment["spec"]["template"]["spec"].get("containers") or []
    api = next((item for item in containers if item.get("name") == "api"), None)
    if api is None:
        return fail("deploy/api/deployment.yaml is missing the api container")
    env = {item.get("name"): item.get("value") for item in api.get("env") or []}
    if env.get("SECURITY_MODE") != "production":
        return fail("the committed API Deployment must explicitly set SECURITY_MODE=production")

    main_source = (ROOT / "cmd/api/main.go").read_text(encoding="utf-8")
    if "httpapi.NewHandler(" in main_source:
        return fail("cmd/api/main.go must not call the compatibility NewHandler directly")
    if "buildHandler(" not in main_source or "defaultProductionBundle(" not in main_source:
        return fail("cmd/api/main.go must use the validated production composition path")

    composition_source = (ROOT / "cmd/api/security.go").read_text(encoding="utf-8")
    for required in ("ModeProduction", "ValidateForProduction", "NewHandlerWithOptions"):
        if required not in composition_source:
            return fail(f"cmd/api/security.go is missing production gate token: {required}")

    workflow_source = (ROOT / ".github/workflows/ci.yml").read_text(encoding="utf-8")
    for required in (
        '"reason":"security_bundle_invalid"',
        '"component":"authenticator"',
        "SECURITY_MODE=development",
    ):
        if required not in workflow_source:
            return fail(f"CI no longer proves production fail-closed behavior: missing {required}")

    print("[production-security-gate] production composition guard passed")
    return 0


if __name__ == "__main__":
    raise SystemExit(main())
