#!/usr/bin/env python3
"""Validate Sprint 4 SDK Beta helper readiness."""

from __future__ import annotations

from pathlib import Path
from typing import Any
import json
import re

import yaml


ROOT = Path(__file__).resolve().parents[1]
DOC_ROOT = ROOT.parent


def fail(message: str) -> None:
    raise SystemExit(f"sdk beta invalid: {message}")


def load_yaml(path: Path) -> dict[str, Any]:
    data = yaml.safe_load(path.read_text(encoding="utf-8"))
    if not isinstance(data, dict):
        fail(f"{path.relative_to(ROOT)} must parse to an object")
    return data


def read(path: Path) -> str:
    return path.read_text(encoding="utf-8")


def expected_core_idempotency_operations(matrix: dict[str, Any]) -> list[str]:
    operations: list[str] = []
    for resource in matrix.get("p0_resources", []):
        operation_id = resource.get("create_operation_id")
        if operation_id:
            operations.append(operation_id)
    for operation in matrix.get("operation_paths", []):
        if operation.get("requires_idempotency_key"):
            operations.append(operation["operation_id"])
    return sorted(set(operations))


def expected_idempotency_operations_from_spec(spec: dict[str, Any]) -> list[str]:
    schemas = spec.get("components", {}).get("schemas", {})
    operations: list[str] = []
    for path, path_item in spec.get("paths", {}).items():
        if not isinstance(path_item, dict):
            continue
        for method, operation in path_item.items():
            if method not in {"post", "put", "patch"} or not isinstance(operation, dict):
                continue
            schema = operation.get("requestBody", {}).get("content", {}).get("application/json", {}).get("schema", {})
            ref = schema.get("$ref", "")
            if not ref:
                continue
            schema_name = ref.rsplit("/", 1)[-1]
            required = set(schemas.get(schema_name, {}).get("required", []))
            if "idempotency_key" in required:
                operations.append(operation.get("operationId") or fallback_operation_id(method, path))
    return sorted(set(operations))


def expected_cursor_pagination_operations(spec: dict[str, Any]) -> list[str]:
    operations: list[str] = []
    for path_item in spec.get("paths", {}).values():
        if not isinstance(path_item, dict):
            continue
        get = path_item.get("get")
        if not isinstance(get, dict):
            continue
        parameter_names = {parameter.get("name") for parameter in get.get("parameters", []) if isinstance(parameter, dict)}
        if {"limit", "cursor"}.issubset(parameter_names):
            operations.append(get.get("operationId", ""))
    return sorted(operation for operation in operations if operation)


def fallback_operation_id(method: str, path: str) -> str:
    tokens = re.sub(r"[^a-zA-Z0-9]+", " ", path).title().replace(" ", "")
    return method.lower() + tokens


def expected_error_codes(spec: dict[str, Any]) -> list[str]:
    known = {
        "UNAUTHORIZED",
        "FORBIDDEN",
        "NOT_FOUND",
        "CONFLICT",
        "BAD_REQUEST",
        "RATE_LIMIT_EXCEEDED",
        "NOT_IMPLEMENTED",
        "INTERNAL_ERROR",
    }
    response_map = {
        "Unauthorized": "UNAUTHORIZED",
        "Forbidden": "FORBIDDEN",
        "NotFound": "NOT_FOUND",
        "BadRequest": "BAD_REQUEST",
        "Conflict": "CONFLICT",
        "RateLimitExceeded": "RATE_LIMIT_EXCEEDED",
    }
    codes: set[str] = set()
    description = spec.get("info", {}).get("description", "")
    for code in known:
        if code in description:
            codes.add(code)
    for name, response in spec.get("components", {}).get("responses", {}).items():
        if name in response_map:
            codes.add(response_map[name])
        if isinstance(response, dict):
            description = response.get("description", "")
            if "code=" in description:
                codes.add(description.split("code=", 1)[1].split("）", 1)[0].split(")", 1)[0].strip())
    return sorted(code for code in codes if code)


def validate_metadata(matrix: dict[str, Any], spec: dict[str, Any]) -> None:
    core_metadata = json.loads((ROOT / "sdks/core/sdk-metadata.json").read_text(encoding="utf-8"))
    services_metadata = json.loads((ROOT / "sdks/services/sdk-metadata.json").read_text(encoding="utf-8"))
    expected_core = expected_idempotency_operations_from_spec(spec)
    actual_core = sorted(core_metadata.get("idempotencyOperations", []))
    if actual_core != expected_core:
        fail(f"Core SDK idempotencyOperations mismatch: got {actual_core}, want {expected_core}")
    expected_cursor = expected_cursor_pagination_operations(spec)
    actual_cursor = sorted(core_metadata.get("cursorPaginationOperations", []))
    if actual_cursor != expected_cursor:
        fail(f"Core SDK cursorPaginationOperations mismatch: got {actual_cursor}, want {expected_cursor}")
    expected_errors = expected_error_codes(spec)
    actual_errors = sorted(core_metadata.get("errorCodes", []))
    if actual_errors != expected_errors:
        fail(f"Core SDK errorCodes mismatch: got {actual_errors}, want {expected_errors}")
    validate_services_idempotency_separation(core_metadata, services_metadata)


def validate_services_idempotency_separation(core_metadata: dict[str, Any], services_metadata: dict[str, Any]) -> None:
    core_operations = set(core_metadata.get("idempotencyOperations", []))
    services_operations = set(services_metadata.get("idempotencyOperations", []))
    leaked_operations = sorted(core_operations & services_operations)
    if leaked_operations:
        fail(f"Services SDK must not declare Core idempotency operations: {leaked_operations}")


def validate_language_helpers() -> None:
    helper_tokens = {
        "sdks/core/go/anisdk/client.go": ["NewIdempotencyKey", "WithIdempotencyKey", "IdempotencyOperations", "CursorParams", "CursorPaginationOperations", "APIError", "ErrorCodes", "IsAPIErrorCode"],
        "sdks/core/python/kubercloud_ani_core/client.py": ["new_idempotency_key", "with_idempotency_key", "IDEMPOTENCY_OPERATIONS", "cursor_params", "CURSOR_PAGINATION_OPERATIONS", "APIError", "ERROR_CODES", "is_api_error_code"],
        "sdks/core/typescript/src/index.ts": ["newIdempotencyKey", "withIdempotencyKey", "idempotencyOperations", "cursorParams", "cursorPaginationOperations", "APIError", "errorCodes", "isAPIErrorCode"],
        "sdks/core/java/src/main/java/com/kubercloud/ani/core/ApiClient.java": ["newIdempotencyKey", "withIdempotencyKey", "IDEMPOTENCY_OPERATIONS", "cursorParams", "CURSOR_PAGINATION_OPERATIONS", "APIError", "ERROR_CODES", "isAPIErrorCode"],
        "sdks/services/go/anisdk/client.go": ["NewIdempotencyKey", "WithIdempotencyKey", "IdempotencyOperations", "CursorParams", "CursorPaginationOperations", "APIError", "ErrorCodes", "IsAPIErrorCode"],
        "sdks/services/python/kubercloud_ani_services/client.py": ["new_idempotency_key", "with_idempotency_key", "IDEMPOTENCY_OPERATIONS", "cursor_params", "CURSOR_PAGINATION_OPERATIONS", "APIError", "ERROR_CODES", "is_api_error_code"],
        "sdks/services/typescript/src/index.ts": ["newIdempotencyKey", "withIdempotencyKey", "idempotencyOperations", "cursorParams", "cursorPaginationOperations", "APIError", "errorCodes", "isAPIErrorCode"],
        "sdks/services/java/src/main/java/com/kubercloud/ani/services/ApiClient.java": ["newIdempotencyKey", "withIdempotencyKey", "IDEMPOTENCY_OPERATIONS", "cursorParams", "CURSOR_PAGINATION_OPERATIONS", "APIError", "ERROR_CODES", "isAPIErrorCode"],
    }
    for relative, tokens in helper_tokens.items():
        content = read(ROOT / relative)
        for token in tokens:
            if token not in content:
                fail(f"{relative} missing {token}")


def validate_examples() -> None:
    example_tokens = {
        "sdks/core/go/examples/basic/main.go": ["WithIdempotencyKey", "CursorParams", "NewAPIError", "IsAPIErrorCode"],
        "sdks/core/python/examples/basic.py": ["with_idempotency_key", "cursor_params", "APIError", "is_api_error_code"],
        "sdks/core/typescript/examples/basic.mjs": ["withIdempotencyKey", "cursorParams", "apiError", "isAPIErrorCode"],
        "sdks/core/java/examples/Basic.java": ["withIdempotencyKey", "cursorParams", "apiError", "isAPIErrorCode"],
        "sdks/services/go/examples/basic/main.go": ["WithIdempotencyKey", "CursorParams", "NewAPIError", "IsAPIErrorCode"],
        "sdks/services/python/examples/basic.py": ["with_idempotency_key", "cursor_params", "APIError", "is_api_error_code"],
        "sdks/services/typescript/examples/basic.mjs": ["withIdempotencyKey", "cursorParams", "apiError", "isAPIErrorCode"],
        "sdks/services/java/examples/Basic.java": ["withIdempotencyKey", "cursorParams", "apiError", "isAPIErrorCode"],
    }
    for relative, tokens in example_tokens.items():
        content = read(ROOT / relative)
        for token in tokens:
            if token not in content:
                fail(f"{relative} missing example token {token}")


def validate_docs() -> None:
    required = {
        "ANI-DOCS-INDEX.md": read(DOC_ROOT / "ANI-DOCS-INDEX.md"),
        "ANI-06-开发计划.md": read(DOC_ROOT / "ANI-06-开发计划.md"),
        "CURRENT-SPRINT.md": read(ROOT / "CURRENT-SPRINT.md"),
        "development-records/README.md": read(ROOT / "development-records/README.md"),
    }
    for path, content in required.items():
        if "SDK-BETA-A" not in content:
            fail(f"{path} must reference SDK-BETA-A")
        if "SDK-BETA-B" not in content:
            fail(f"{path} must reference SDK-BETA-B")
        if "SDK-BETA-C" not in content:
            fail(f"{path} must reference SDK-BETA-C")
        if "SDK-BETA-D" not in content:
            fail(f"{path} must reference SDK-BETA-D")
    if "validate-sdk-beta" not in required["CURRENT-SPRINT.md"]:
        fail("CURRENT-SPRINT.md must list validate-sdk-beta")


def main() -> None:
    matrix = load_yaml(ROOT / "api/core-beta-readiness.yaml")
    spec = load_yaml(ROOT / "api/openapi/v1.yaml")
    validate_metadata(matrix, spec)
    validate_language_helpers()
    validate_examples()
    validate_docs()
    print("SDK Beta helpers valid")


if __name__ == "__main__":
    main()
