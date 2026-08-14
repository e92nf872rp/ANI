#!/usr/bin/env python3
"""Validate Sprint 4 SPEC-CORE-BETA readiness guardrails."""

from __future__ import annotations

from pathlib import Path
from typing import Any
import json

import yaml


ROOT = Path(__file__).resolve().parents[1]
DOC_ROOT = ROOT.parent
SERVICE_PATH_PREFIXES = ("/models", "/inference-services", "/knowledge-bases")
CORE_P0_ROUTER_FILES = (
    "services/ani-gateway/internal/router/instances.go",
    "services/ani-gateway/internal/router/network_resources.go",
    "services/ani-gateway/internal/router/storage_resources.go",
    "services/ani-gateway/internal/router/vector_store_resources.go",
)


def fail(message: str) -> None:
    raise SystemExit(f"core beta contract invalid: {message}")


def read_repo(path: str) -> str:
    return (ROOT / path).read_text(encoding="utf-8")


def read_doc(path: str) -> str:
    return (DOC_ROOT / path).read_text(encoding="utf-8")


def load_yaml(path: str) -> dict[str, Any]:
    data = yaml.safe_load(read_repo(path))
    if not isinstance(data, dict):
        fail(f"{path} must parse to an object")
    return data


def schema_ref_name(operation: dict[str, Any]) -> str:
    body = operation.get("requestBody", {})
    content = body.get("content", {}).get("application/json", {})
    schema = content.get("schema", {})
    ref = schema.get("$ref", "")
    return ref.rsplit("/", 1)[-1] if ref else ""


def response_schema_name(operation: dict[str, Any], code: str = "200") -> str:
    content = operation.get("responses", {}).get(code, {}).get("content", {}).get("application/json", {})
    schema = content.get("schema", {})
    if "$ref" in schema:
        return schema["$ref"].rsplit("/", 1)[-1]
    for item in schema.get("allOf", []):
        ref = item.get("$ref", "")
        if ref:
            return ref.rsplit("/", 1)[-1]
    return ""


def validate_openapi(spec: dict[str, Any], matrix: dict[str, Any]) -> None:
    if spec.get("servers", [{}])[0].get("url") != "https://{host}/api/v1":
        fail("Core API server URL must be https://{host}/api/v1")

    paths = spec.get("paths", {})
    leaked = sorted(path for path in paths if path.startswith(SERVICE_PATH_PREFIXES))
    if leaked:
        fail(f"Core API contains Services business paths: {leaked}")

    schemas = spec.get("components", {}).get("schemas", {})
    responses = spec.get("components", {}).get("responses", {})
    required_responses = {"Unauthorized", "Forbidden", "NotFound", "BadRequest", "Conflict", "RateLimitExceeded"}
    missing_responses = sorted(required_responses - set(responses))
    if missing_responses:
        fail(f"Core API missing standard response components: {missing_responses}")

    for path in matrix.get("scope", {}).get("included_paths", []):
        if path not in paths:
            fail(f"Core API missing matrix path {path}")

    for resource in matrix.get("p0_resources", []):
        name = resource["name"]
        record_schema = schemas.get(resource["record_schema"], {})
        record_properties = record_schema.get("properties", {})
        missing_record_fields = sorted(set(resource["required_record_fields"]) - set(record_properties))
        if missing_record_fields:
            fail(f"{name} record schema missing fields: {missing_record_fields}")
        required_fields = set(record_schema.get("required", []))
        for field in ("id", "tenant_id", "state", "created_at", "updated_at"):
            if field in resource["required_record_fields"] and field not in required_fields:
                fail(f"{name} record schema must require {field}")

        create_path = resource["create_path"]
        post = paths.get(create_path, {}).get("post")
        if not isinstance(post, dict):
            fail(f"{name} missing POST {create_path}")
        if post.get("operationId") != resource["create_operation_id"]:
            fail(f"{name} POST operationId must be {resource['create_operation_id']}")
        if post.get("x-ani-rbac-scope") != resource["create_rbac_scope"]:
            fail(f"{name} POST rbac scope must be {resource['create_rbac_scope']}")
        if schema_ref_name(post) != resource["create_request_schema"]:
            fail(f"{name} POST request schema must be {resource['create_request_schema']}")
        for code in ("400", "401", "403"):
            if code not in post.get("responses", {}):
                fail(f"{name} POST {create_path} missing {code} response")

        request_schema = schemas.get(resource["create_request_schema"], {})
        request_required = set(request_schema.get("required", []))
        request_properties = request_schema.get("properties", {})
        if "idempotency_key" not in request_required or "idempotency_key" not in request_properties:
            fail(f"{name} create request must require idempotency_key")

        list_path = resource["list_path"]
        get = paths.get(list_path, {}).get("get")
        if not isinstance(get, dict):
            fail(f"{name} missing GET {list_path}")
        parameter_names = {param.get("name") for param in get.get("parameters", [])}
        if not {"limit", "cursor"}.issubset(parameter_names):
            fail(f"{name} list path must expose limit and cursor query parameters")
        if get.get("x-ani-rbac-scope") is None:
            fail(f"{name} list path missing x-ani-rbac-scope")
        if response_schema_name(get) != resource["list_response_schema"]:
            fail(f"{name} list response schema must be {resource['list_response_schema']}")
        list_schema = schemas.get(resource["list_response_schema"], {})
        list_properties = list_schema.get("properties", {})
        if not {"items", "total", "next_cursor"}.issubset(list_properties):
            fail(f"{name} list response must include items, total, and next_cursor")

    for item in matrix.get("operation_paths", []):
        operation = paths.get(item["path"], {}).get(item["method"])
        if not isinstance(operation, dict):
            fail(f"missing {item['method'].upper()} {item['path']}")
        if operation.get("operationId") != item["operation_id"]:
            fail(f"{item['method'].upper()} {item['path']} operationId must be {item['operation_id']}")
        if operation.get("x-ani-rbac-scope") != item["rbac_scope"]:
            fail(f"{item['method'].upper()} {item['path']} rbac scope must be {item['rbac_scope']}")
        if item.get("requires_idempotency_key"):
            request_schema = schemas.get(item["request_schema"], {})
            if "idempotency_key" not in set(request_schema.get("required", [])):
                fail(f"{item['request_schema']} must require idempotency_key")


def validate_sdk_metadata(matrix: dict[str, Any]) -> None:
    core_metadata = json.loads((ROOT / "sdks/core/sdk-metadata.json").read_text(encoding="utf-8"))
    services_metadata = json.loads((ROOT / "sdks/services/sdk-metadata.json").read_text(encoding="utf-8"))
    core_paths = {item["path"] for item in core_metadata.get("operations", [])}
    services_paths = {item["path"] for item in services_metadata.get("operations", [])}

    leaked = sorted(path for path in core_paths if path.startswith(SERVICE_PATH_PREFIXES))
    if leaked:
        fail(f"Core SDK metadata contains Services business paths: {leaked}")

    required_core_paths = set(matrix.get("scope", {}).get("included_paths", []))
    missing_core_paths = sorted(required_core_paths - core_paths)
    if missing_core_paths:
        fail(f"Core SDK metadata missing Core P0 paths: {missing_core_paths}")
    if any(path.startswith(("/instances", "/networks", "/volumes", "/filesystems", "/objects", "/vector-stores")) for path in services_paths):
        fail("Services SDK metadata contains Core infrastructure paths")


def validate_router_no_unowned_not_implemented() -> None:
    for path in CORE_P0_ROUTER_FILES:
        text = read_repo(path)
        if "NOT_IMPLEMENTED" in text or "not implemented" in text.lower():
            fail(f"{path} contains unowned NOT_IMPLEMENTED on Core P0 surface")


def validate_docs() -> None:
    required = {
        "ANI-06-开发计划.md": read_doc("ANI-06-开发计划.md"),
        "ANI-DOCS-INDEX.md": read_doc("ANI-DOCS-INDEX.md"),
        "CURRENT-SPRINT.md": read_repo("CURRENT-SPRINT.md"),
        "development-records/README.md": read_repo("development-records/README.md"),
    }
    for path, text in required.items():
        if "SPEC-CORE-BETA" not in text:
            fail(f"{path} must reference SPEC-CORE-BETA")
    if "core-beta-readiness.yaml" not in required["CURRENT-SPRINT.md"]:
        fail("CURRENT-SPRINT.md must reference api/core-beta-readiness.yaml")
    if "spec-core-beta-a-readiness-matrix.md" not in required["development-records/README.md"]:
        fail("development-records/README.md must link SPEC-CORE-BETA record")


def main() -> None:
    spec = load_yaml("api/openapi/v1.yaml")
    matrix = load_yaml("api/core-beta-readiness.yaml")
    validate_openapi(spec, matrix)
    validate_sdk_metadata(matrix)
    validate_router_no_unowned_not_implemented()
    validate_docs()
    print("core beta contract valid")


if __name__ == "__main__":
    main()
