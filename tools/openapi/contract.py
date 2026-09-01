#!/usr/bin/env python3
from __future__ import annotations

import argparse
import difflib
import hashlib
import json
import re
import sys
from pathlib import Path
from typing import Any

import yaml

ROOT = Path(__file__).resolve().parents[2]
SPEC_PATH = ROOT / "api" / "openapi.yaml"
SERVER_PATH = ROOT / "internal" / "httpapi" / "server.go"
GENERATED_PATH = ROOT / "clients" / "typescript" / "generated.ts"
FINDING_MODEL_PATH = ROOT / "internal" / "finding" / "model.go"
KUBERNETES_MODEL_PATH = ROOT / "internal" / "kubernetes" / "client.go"

HTTP_METHODS = {"get", "put", "post", "delete", "patch", "head", "options", "trace"}
FORBIDDEN_PROPERTY_NAMES = {
    "sensitive",
    "unmasked",
    "masked",
    "rawresult",
    "rawobject",
    "serviceaccounttoken",
    "providertoken",
    "kubeconfig",
}


def fail(message: str) -> None:
    raise SystemExit(f"ERROR: {message}")


def load_spec() -> dict[str, Any]:
    try:
        spec = yaml.safe_load(SPEC_PATH.read_text(encoding="utf-8"))
    except FileNotFoundError:
        fail(f"OpenAPI spec not found: {SPEC_PATH.relative_to(ROOT)}")
    if not isinstance(spec, dict):
        fail("OpenAPI spec root must be a mapping")
    return spec


def resolve_ref(spec: dict[str, Any], node: Any) -> Any:
    while isinstance(node, dict) and "$ref" in node:
        ref = node["$ref"]
        if not isinstance(ref, str) or not ref.startswith("#/"):
            fail(f"only local OpenAPI refs are supported by the contract generator: {ref!r}")
        current: Any = spec
        for part in ref[2:].split("/"):
            current = current[part.replace("~1", "/").replace("~0", "~")]
        node = current
    return node


def validate_openapi(spec: dict[str, Any]) -> None:
    try:
        from openapi_spec_validator import validate_spec
    except ImportError:
        fail("openapi-spec-validator is required; install tools/openapi/requirements.txt")
    validate_spec(spec)


def extract_server_routes() -> set[tuple[str, str]]:
    text = SERVER_PATH.read_text(encoding="utf-8")
    return {
        (method, path)
        for method, path in re.findall(r'mux\.HandleFunc\("([A-Z]+) ([^"\\]+)"', text)
    }


def extract_spec_routes(spec: dict[str, Any]) -> set[tuple[str, str]]:
    routes: set[tuple[str, str]] = set()
    for path, path_item in spec.get("paths", {}).items():
        if not isinstance(path_item, dict):
            continue
        for method in path_item:
            if method.lower() in HTTP_METHODS:
                routes.add((method.upper(), path))
    return routes


def validate_route_coverage(spec: dict[str, Any]) -> None:
    server_routes = extract_server_routes()
    spec_routes = extract_spec_routes(spec)
    if server_routes != spec_routes:
        missing = sorted(server_routes - spec_routes)
        extra = sorted(spec_routes - server_routes)
        details = []
        if missing:
            details.append(f"missing from OpenAPI: {missing}")
        if extra:
            details.append(f"not implemented by server: {extra}")
        fail("route drift detected; " + "; ".join(details))


def validate_operation_ids(spec: dict[str, Any]) -> None:
    seen: set[str] = set()
    for path, path_item in spec.get("paths", {}).items():
        if not isinstance(path_item, dict):
            continue
        for method, operation in path_item.items():
            if method.lower() not in HTTP_METHODS or not isinstance(operation, dict):
                continue
            operation_id = operation.get("operationId")
            if not isinstance(operation_id, str) or not operation_id.strip():
                fail(f"{method.upper()} {path} must define operationId")
            if operation_id in seen:
                fail(f"duplicate operationId: {operation_id}")
            seen.add(operation_id)


def walk_schema_property_names(node: Any, path: str = "$") -> list[tuple[str, str]]:
    findings: list[tuple[str, str]] = []
    if isinstance(node, dict):
        properties = node.get("properties")
        if isinstance(properties, dict):
            for name, child in properties.items():
                findings.append((path, str(name)))
                findings.extend(walk_schema_property_names(child, f"{path}.properties.{name}"))
        for key, child in node.items():
            if key != "properties":
                findings.extend(walk_schema_property_names(child, f"{path}.{key}"))
    elif isinstance(node, list):
        for index, child in enumerate(node):
            findings.extend(walk_schema_property_names(child, f"{path}[{index}]"))
    return findings


def extract_go_json_fields(path: Path, struct_name: str) -> dict[str, bool]:
    text = path.read_text(encoding="utf-8")
    match = re.search(rf"type\s+{re.escape(struct_name)}\s+struct\s*\{{(.*?)\}}", text, re.S)
    if not match:
        fail(f"Go struct not found: {struct_name} in {path.relative_to(ROOT)}")
    fields: dict[str, bool] = {}
    for tag in re.findall(r'`json:"([^" ]+)"`', match.group(1)):
        parts = tag.split(",")
        name = parts[0]
        if name == "-":
            continue
        fields[name] = "omitempty" in parts[1:]
    return fields


def validate_go_schema_projection(spec: dict[str, Any]) -> None:
    mappings = [
        (KUBERNETES_MODEL_PATH, "Cluster", "Cluster"),
        (KUBERNETES_MODEL_PATH, "Namespace", "Namespace"),
        (KUBERNETES_MODEL_PATH, "ResourceStatus", "ResourceStatus"),
        (KUBERNETES_MODEL_PATH, "ResourceDetail", "ResourceDetail"),
        (FINDING_MODEL_PATH, "ResourceRef", "ResourceRef"),
        (FINDING_MODEL_PATH, "Finding", "Finding"),
        (FINDING_MODEL_PATH, "Pagination", "Pagination"),
        (FINDING_MODEL_PATH, "Page", "FindingPage"),
        (FINDING_MODEL_PATH, "Summary", "FindingSummary"),
    ]
    schemas = spec.get("components", {}).get("schemas", {})
    for path, struct_name, schema_name in mappings:
        fields = extract_go_json_fields(path, struct_name)
        schema = schemas.get(schema_name)
        if not isinstance(schema, dict):
            fail(f"OpenAPI schema missing for Go struct {struct_name}: {schema_name}")
        properties = schema.get("properties", {})
        if set(fields) != set(properties):
            fail(
                f"Go/OpenAPI field drift for {struct_name}/{schema_name}: "
                f"go={sorted(fields)}, openapi={sorted(properties)}"
            )
        expected_required = {name for name, optional in fields.items() if not optional}
        actual_required = set(schema.get("required", []))
        if expected_required != actual_required:
            fail(
                f"Go/OpenAPI required-field drift for {struct_name}/{schema_name}: "
                f"go={sorted(expected_required)}, openapi={sorted(actual_required)}"
            )

    model_text = FINDING_MODEL_PATH.read_text(encoding="utf-8")
    go_severities = set(re.findall(r'Severity[A-Za-z]+\s*=\s*"([^"]+)"', model_text))
    schema_severities = set(schemas.get("Severity", {}).get("enum", []))
    if go_severities != schema_severities:
        fail(
            f"severity enum drift: go={sorted(go_severities)}, "
            f"openapi={sorted(schema_severities)}"
        )


def validate_sensitive_guard(spec: dict[str, Any]) -> None:
    violations = []
    for path, name in walk_schema_property_names(spec.get("components", {}).get("schemas", {})):
        if name.lower() in FORBIDDEN_PROPERTY_NAMES:
            violations.append(f"{path}: {name}")
    if violations:
        fail("sensitive/raw property exposed by contract: " + ", ".join(violations))

    finding_page = spec["components"]["schemas"].get("FindingPage", {})
    required = set(finding_page.get("required", []))
    items = finding_page.get("properties", {}).get("items", {})
    if "items" not in required or items.get("type") != "array":
        fail("FindingPage.items must be a required array so empty results serialize as []")


def to_pascal(value: str) -> str:
    parts = re.split(r"[^A-Za-z0-9]+", value)
    if len(parts) == 1 and value:
        return value[:1].upper() + value[1:]
    return "".join(part[:1].upper() + part[1:] for part in parts if part)


def ts_literal(value: Any) -> str:
    if value is None:
        return "null"
    if isinstance(value, bool):
        return "true" if value else "false"
    if isinstance(value, (int, float)):
        return str(value)
    return json.dumps(str(value), ensure_ascii=False)


def ts_type(schema: Any) -> str:
    if not isinstance(schema, dict):
        return "unknown"
    if "$ref" in schema:
        return str(schema["$ref"]).split("/")[-1]
    enum = schema.get("enum")
    if isinstance(enum, list) and enum:
        return " | ".join(ts_literal(value) for value in enum)
    schema_type = schema.get("type")
    if schema_type == "string":
        return "string"
    if schema_type in {"integer", "number"}:
        return "number"
    if schema_type == "boolean":
        return "boolean"
    if schema_type == "array":
        return f"Array<{ts_type(schema.get('items', {}))}>"
    if schema_type == "object":
        additional = schema.get("additionalProperties")
        if isinstance(additional, dict):
            return f"Record<string, {ts_type(additional)}>"
        return "Record<string, unknown>"
    return "unknown"


def render_schemas(spec: dict[str, Any]) -> list[str]:
    lines: list[str] = []
    schemas = spec.get("components", {}).get("schemas", {})
    for name, schema in schemas.items():
        if not isinstance(schema, dict):
            continue
        if schema.get("type") == "object" and isinstance(schema.get("properties"), dict):
            required = set(schema.get("required", []))
            lines.append(f"export interface {name} {{")
            for prop, prop_schema in schema["properties"].items():
                optional = "" if prop in required else "?"
                lines.append(f"  {json.dumps(str(prop))}{optional}: {ts_type(prop_schema)};")
            if isinstance(schema.get("additionalProperties"), dict):
                lines.append(f"  [key: string]: {ts_type(schema['additionalProperties'])};")
            lines.append("}")
        else:
            lines.append(f"export type {name} = {ts_type(schema)};")
        lines.append("")
    return lines


def operation_parameters(spec: dict[str, Any], path_item: dict[str, Any], operation: dict[str, Any]) -> list[dict[str, Any]]:
    merged = list(path_item.get("parameters", [])) + list(operation.get("parameters", []))
    result = []
    for parameter in merged:
        resolved = resolve_ref(spec, parameter)
        if not isinstance(resolved, dict):
            fail("operation parameter must resolve to an object")
        result.append(resolved)
    return result


def success_schema(spec: dict[str, Any], operation: dict[str, Any]) -> Any:
    responses = operation.get("responses", {})
    for status in sorted(responses, key=str):
        if not str(status).startswith("2"):
            continue
        response = resolve_ref(spec, responses[status])
        content = response.get("content", {}) if isinstance(response, dict) else {}
        media = content.get("application/json", {}) if isinstance(content, dict) else {}
        if isinstance(media, dict) and "schema" in media:
            return media["schema"]
    return None


def render_operations(spec: dict[str, Any]) -> tuple[list[str], list[str]]:
    param_types: list[str] = []
    methods: list[str] = []
    for path, path_item in spec.get("paths", {}).items():
        if not isinstance(path_item, dict):
            continue
        for method, operation in path_item.items():
            if method.lower() not in HTTP_METHODS or not isinstance(operation, dict):
                continue
            operation_id = operation["operationId"]
            params = operation_parameters(spec, path_item, operation)
            params_name = f"{to_pascal(operation_id)}Params"
            if params:
                param_types.append(f"export interface {params_name} {{")
                for parameter in params:
                    name = str(parameter["name"])
                    required = bool(parameter.get("required"))
                    optional = "" if required else "?"
                    param_types.append(f"  {json.dumps(name)}{optional}: {ts_type(parameter.get('schema', {}))};")
                param_types.append("}")
                param_types.append("")

            response_schema = success_schema(spec, operation)
            return_type = ts_type(response_schema) if response_schema else "void"
            required_params = any(bool(p.get("required")) for p in params)
            if params:
                signature = f"params: {params_name}" if required_params else f"params: {params_name} = {{}}"
            else:
                signature = ""
            methods.append(f"  async {operation_id}({signature}): Promise<{return_type}> {{")
            rendered_path = path
            for parameter in params:
                if parameter.get("in") == "path":
                    name = str(parameter["name"])
                    rendered_path = rendered_path.replace(
                        "{" + name + "}", "${encodeURIComponent(String(params[" + json.dumps(name) + "]))}"
                    )
            methods.append(f"    let path = `{rendered_path}`;")
            query_params = [p for p in params if p.get("in") == "query"]
            if query_params:
                methods.append("    const query = new URLSearchParams();")
                for parameter in query_params:
                    name = str(parameter["name"])
                    access = f"params[{json.dumps(name)}]"
                    if parameter.get("required"):
                        methods.append(f"    query.set({json.dumps(name)}, String({access}));")
                    else:
                        methods.append(f"    if ({access} !== undefined) query.set({json.dumps(name)}, String({access}));")
                methods.append("    const queryString = query.toString();")
                methods.append("    if (queryString) path += `?${queryString}`;")
            methods.append(f"    return this.request<{return_type}>(path);")
            methods.append("  }")
            methods.append("")
    return param_types, methods


def generate_typescript(spec: dict[str, Any]) -> str:
    digest = hashlib.sha256(SPEC_PATH.read_bytes()).hexdigest()
    fingerprint = " ".join(digest[index:index + 8] for index in range(0, len(digest), 8))
    schema_lines = render_schemas(spec)
    param_lines, method_lines = render_operations(spec)
    lines = [
        "// Code generated from api/openapi.yaml by tools/openapi/contract.py. DO NOT EDIT.",
        f"// Contract source SHA-256 groups: {fingerprint}",
        "",
        *schema_lines,
        *param_lines,
        "export interface ApiClientOptions {",
        "  baseUrl?: string;",
        "  fetch?: typeof fetch;",
        "}",
        "",
        "export class ApiError extends Error {",
        "  constructor(public readonly status: number, public readonly body: unknown) {",
        "    super(`kube-aiops API request failed with HTTP ${status}`);",
        "    this.name = \"ApiError\";",
        "  }",
        "}",
        "",
        "export class KubeAIOpsApiClient {",
        "  private readonly baseUrl: string;",
        "  private readonly fetchImpl: typeof fetch;",
        "",
        "  constructor(options: ApiClientOptions = {}) {",
        "    this.baseUrl = (options.baseUrl ?? \"\").replace(/\\/$/, \"\");",
        "    this.fetchImpl = options.fetch ?? fetch;",
        "  }",
        "",
        "  private async request<T>(path: string): Promise<T> {",
        "    const response = await this.fetchImpl(`${this.baseUrl}${path}`, {",
        "      method: \"GET\",",
        "      headers: { Accept: \"application/json\" },",
        "    });",
        "    const body = await response.json().catch(() => undefined);",
        "    if (!response.ok) throw new ApiError(response.status, body);",
        "    return body as T;",
        "  }",
        "",
        *method_lines,
        "}",
        "",
    ]
    return "\n".join(lines)


def validate_generated_sensitive_guard(text: str) -> None:
    lowered = text.lower()
    leaked = sorted(name for name in FORBIDDEN_PROPERTY_NAMES if re.search(rf'\b{re.escape(name)}\b', lowered))
    if leaked:
        fail("generated TypeScript client contains forbidden sensitive/raw identifiers: " + ", ".join(leaked))


def drift_check(spec: dict[str, Any]) -> None:
    expected = generate_typescript(spec)
    try:
        actual = GENERATED_PATH.read_text(encoding="utf-8")
    except FileNotFoundError:
        fail(f"generated client not found: {GENERATED_PATH.relative_to(ROOT)}")
    validate_generated_sensitive_guard(actual)
    if actual != expected:
        diff = "".join(
            difflib.unified_diff(
                actual.splitlines(keepends=True),
                expected.splitlines(keepends=True),
                fromfile=str(GENERATED_PATH.relative_to(ROOT)),
                tofile="regenerated",
                n=3,
            )
        )
        sys.stderr.write(diff[:12000])
        fail("generated TypeScript client drift detected; run 'make api-client-generate'")


def run_checks(spec: dict[str, Any], include_drift: bool) -> None:
    validate_openapi(spec)
    validate_route_coverage(spec)
    validate_operation_ids(spec)
    validate_go_schema_projection(spec)
    validate_sensitive_guard(spec)
    if include_drift:
        drift_check(spec)


def main() -> None:
    parser = argparse.ArgumentParser(description="Validate kube-aiops OpenAPI and generate the TypeScript client")
    sub = parser.add_subparsers(dest="command", required=True)
    sub.add_parser("validate")
    generate = sub.add_parser("generate")
    generate.add_argument("--output", default=str(GENERATED_PATH))
    sub.add_parser("drift")
    sub.add_parser("check")
    args = parser.parse_args()

    spec = load_spec()
    if args.command == "validate":
        run_checks(spec, include_drift=False)
        print("OpenAPI contract validation: PASS")
    elif args.command == "generate":
        run_checks(spec, include_drift=False)
        output = Path(args.output)
        output.parent.mkdir(parents=True, exist_ok=True)
        generated = generate_typescript(spec)
        validate_generated_sensitive_guard(generated)
        output.write_text(generated, encoding="utf-8")
        print(f"Generated {output}")
    elif args.command == "drift":
        validate_route_coverage(spec)
        validate_operation_ids(spec)
        validate_go_schema_projection(spec)
        validate_sensitive_guard(spec)
        drift_check(spec)
        print("OpenAPI generated client drift check: PASS")
    elif args.command == "check":
        run_checks(spec, include_drift=True)
        print("OpenAPI contract gate: PASS")


if __name__ == "__main__":
    main()
