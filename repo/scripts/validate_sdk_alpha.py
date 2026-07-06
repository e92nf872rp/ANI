#!/usr/bin/env python3
"""Validate generated SDK Alpha artifacts and run language smoke checks."""

from __future__ import annotations

from pathlib import Path
import json
import tempfile
import re
import subprocess
import sys
from typing import Any
import os

import yaml


LAYERS = {
    "core": "api/openapi/v1.yaml",
    "services": "api/openapi/services/v1.yaml",
}

HTTP_METHODS = {"get", "post", "put", "patch", "delete"}
KNOWN_ERROR_CODES = {
    "UNAUTHORIZED",
    "FORBIDDEN",
    "NOT_FOUND",
    "CONFLICT",
    "BAD_REQUEST",
    "RATE_LIMIT_EXCEEDED",
    "NOT_IMPLEMENTED",
    "INTERNAL_ERROR",
}
RESPONSE_ERROR_CODES = {
    "Unauthorized": "UNAUTHORIZED",
    "Forbidden": "FORBIDDEN",
    "NotFound": "NOT_FOUND",
    "BadRequest": "BAD_REQUEST",
    "Conflict": "CONFLICT",
    "RateLimitExceeded": "RATE_LIMIT_EXCEEDED",
}


def operation_id(method: str, path: str) -> str:
    tokens = re.sub(r"[^a-zA-Z0-9]+", " ", path).title().replace(" ", "")
    return method.lower() + tokens


def load_yaml(path: Path) -> dict[str, Any]:
    with path.open(encoding="utf-8") as handle:
        return yaml.safe_load(handle)


def expected_metadata(root: Path, layer: str, spec_path: str) -> dict[str, Any]:
    spec = load_yaml(root / spec_path)
    schemas = spec.get("components", {}).get("schemas", {})
    operations: list[dict[str, str]] = []
    idempotency_operations: list[str] = []
    cursor_pagination_operations: list[str] = []
    for path, path_item in sorted(spec.get("paths", {}).items()):
        if not isinstance(path_item, dict):
            continue
        for method, operation in sorted(path_item.items()):
            if method not in HTTP_METHODS or not isinstance(operation, dict):
                continue
            op_id = operation.get("operationId") or operation_id(method, path)
            operations.append(
                {
                    "operationId": op_id,
                    "method": method.upper(),
                    "path": path,
                    "scope": operation.get("x-ani-rbac-scope", ""),
                }
            )
            if method in {"post", "put", "patch"} and request_schema_requires_idempotency(operation, schemas):
                idempotency_operations.append(op_id)
            if method == "get" and operation_has_cursor_pagination(operation):
                cursor_pagination_operations.append(op_id)
    schema_names = sorted(schemas.keys())
    servers = spec.get("servers") or []
    server_url = normalize_sdk_server_url(servers[0].get("url", "") if servers else "")
    return {
        "layer": layer,
        "title": spec.get("info", {}).get("title", ""),
        "version": spec.get("info", {}).get("version", ""),
        "serverURL": server_url,
        "operations": operations,
        "schemas": schema_names,
        "idempotencyOperations": idempotency_operations,
        "cursorPaginationOperations": cursor_pagination_operations,
        "errorCodes": collect_error_codes(spec),
}


def normalize_sdk_server_url(server_url: str) -> str:
    if "{" in server_url or "}" in server_url:
        return "http://127.0.0.1:4010/api/v1"
    return server_url


def request_schema_requires_idempotency(operation: dict[str, Any], schemas: dict[str, Any]) -> bool:
    content = operation.get("requestBody", {}).get("content", {}).get("application/json", {})
    schema = content.get("schema", {})
    ref = schema.get("$ref", "")
    if not ref:
        return False
    schema_name = ref.rsplit("/", 1)[-1]
    return "idempotency_key" in set(schemas.get(schema_name, {}).get("required", []))


def operation_has_cursor_pagination(operation: dict[str, Any]) -> bool:
    parameter_names = {parameter.get("name") for parameter in operation.get("parameters", []) if isinstance(parameter, dict)}
    return {"limit", "cursor"}.issubset(parameter_names)


def collect_error_codes(spec: dict[str, Any]) -> list[str]:
    codes: set[str] = set()
    description = spec.get("info", {}).get("description", "")
    for code in KNOWN_ERROR_CODES:
        if code in description:
            codes.add(code)
    for name, response in spec.get("components", {}).get("responses", {}).items():
        mapped = RESPONSE_ERROR_CODES.get(name)
        if mapped:
            codes.add(mapped)
        if isinstance(response, dict):
            match = re.search(r"code=([A-Z0-9_]+)", response.get("description", ""))
            if match:
                codes.add(match.group(1))
    return sorted(codes)


def run(cmd: list[str], cwd: Path, env: dict[str, str] | None = None) -> None:
    merged_env = os.environ.copy()
    if env:
        merged_env.update(env)
    result = subprocess.run(cmd, cwd=cwd, env=merged_env, text=True, stdout=subprocess.PIPE, stderr=subprocess.STDOUT)
    if result.returncode != 0:
        sys.stdout.write(result.stdout)
        raise SystemExit(f"SDK alpha smoke failed: {' '.join(cmd)}")


def command_available(cmd: list[str], cwd: Path) -> bool:
    result = subprocess.run(cmd, cwd=cwd, text=True, stdout=subprocess.PIPE, stderr=subprocess.STDOUT)
    return result.returncode == 0


def validate_metadata(root: Path, layer: str, spec_path: str, errors: list[str]) -> None:
    metadata_path = root / "sdks" / layer / "sdk-metadata.json"
    if not metadata_path.exists():
        errors.append(f"missing {metadata_path.relative_to(root)}")
        return
    actual = json.loads(metadata_path.read_text(encoding="utf-8"))
    expected = expected_metadata(root, layer, spec_path)
    for key in ("layer", "title", "version", "serverURL", "schemas", "idempotencyOperations", "cursorPaginationOperations", "errorCodes"):
        if actual.get(key) != expected.get(key):
            errors.append(f"{layer} SDK metadata {key} is out of date")
    actual_ops = [item.get("operationId") for item in actual.get("operations", [])]
    expected_ops = [item.get("operationId") for item in expected.get("operations", [])]
    if actual_ops != expected_ops:
        errors.append(f"{layer} SDK operations are out of date")


def validate_separation(root: Path, errors: list[str]) -> None:
    core_metadata = json.loads((root / "sdks/core/sdk-metadata.json").read_text(encoding="utf-8"))
    services_metadata = json.loads((root / "sdks/services/sdk-metadata.json").read_text(encoding="utf-8"))
    core_paths = {item["path"] for item in core_metadata.get("operations", [])}
    services_paths = {item["path"] for item in services_metadata.get("operations", [])}
    if any(path.startswith(("/models", "/inference-services", "/knowledge-bases")) for path in core_paths):
        errors.append("core SDK contains Services business paths")
    if any(path.startswith(("/instances", "/networks", "/volumes", "/filesystems", "/objects", "/vector-stores")) for path in services_paths):
        errors.append("services SDK contains Core infrastructure paths")


def validate_files(root: Path, errors: list[str]) -> None:
    required = [
        "go/go.mod",
        "go/anisdk/client.go",
        "go/anisdk/client_test.go",
        "go/examples/basic/main.go",
        "python/smoke.py",
        "python/examples/basic.py",
        "typescript/src/index.ts",
        "typescript/src/index.mjs",
        "typescript/smoke.mjs",
        "typescript/examples/basic.mjs",
        "java/src/main/java",
        "java/src/test/java",
        "java/examples/Basic.java",
    ]
    for layer in LAYERS:
        for suffix in required:
            path = root / "sdks" / layer / suffix
            if not path.exists():
                errors.append(f"missing {path.relative_to(root)}")


def validate_idempotency_helpers(root: Path, errors: list[str]) -> None:
    required_tokens = {
        "go/anisdk/client.go": ["NewIdempotencyKey", "WithIdempotencyKey", "IdempotencyOperations", "CursorParams", "CursorPaginationOperations", "APIError", "ErrorCodes", "IsAPIErrorCode"],
        "python/kubercloud_ani_core/client.py": ["new_idempotency_key", "with_idempotency_key", "IDEMPOTENCY_OPERATIONS", "cursor_params", "CURSOR_PAGINATION_OPERATIONS", "APIError", "ERROR_CODES", "is_api_error_code"],
        "typescript/src/index.ts": ["newIdempotencyKey", "withIdempotencyKey", "idempotencyOperations", "cursorParams", "cursorPaginationOperations", "APIError", "errorCodes", "isAPIErrorCode"],
        "java/src/main/java/com/kubercloud/ani/core/ApiClient.java": ["newIdempotencyKey", "withIdempotencyKey", "IDEMPOTENCY_OPERATIONS", "cursorParams", "CURSOR_PAGINATION_OPERATIONS", "APIError", "ERROR_CODES", "isAPIErrorCode"],
    }
    services_required_tokens = {
        key.replace("kubercloud_ani_core", "kubercloud_ani_services").replace("com/kubercloud/ani/core", "com/kubercloud/ani/services"): value
        for key, value in required_tokens.items()
    }
    for layer, tokens_by_path in {"core": required_tokens, "services": services_required_tokens}.items():
        for suffix, tokens in tokens_by_path.items():
            path = root / "sdks" / layer / suffix
            if not path.exists():
                errors.append(f"missing {path.relative_to(root)}")
                continue
            content = path.read_text(encoding="utf-8")
            for token in tokens:
                if token not in content:
                    errors.append(f"{path.relative_to(root)} missing SDK helper token {token}")


def run_smoke(root: Path) -> None:
    go_cache = Path(tempfile.gettempdir()) / "ani-go-build"
    go_cache.mkdir(parents=True, exist_ok=True)
    go_env = {
        "GOWORK": "off",
        "GOCACHE": str(go_cache),
        "GOMODCACHE": str(root / ".cache/gomod"),
    }
    for layer in LAYERS:
        base = root / "sdks" / layer
        run(["go", "test", "./..."], base / "go", go_env)
        run([sys.executable, "smoke.py"], base / "python")
        run([sys.executable, "examples/basic.py"], base / "python", {"PYTHONPATH": str(base / "python")})
        run(["node", "--check", "src/index.mjs"], base / "typescript")
        run(["node", "smoke.mjs"], base / "typescript")
        run(["node", "examples/basic.mjs"], base / "typescript")
        java_files = sorted(str(path.relative_to(base / "java")) for path in (base / "java").glob("src/**/*.java"))
        java_example_files = sorted(str(path.relative_to(base / "java")) for path in (base / "java").glob("examples/**/*.java"))
        classes = base / "java" / "build" / "classes"
        classes.mkdir(parents=True, exist_ok=True)
        if command_available(["javac", "-version"], base / "java") and command_available(["java", "-version"], base / "java"):
            run(["javac", "-d", str(classes), *java_files], base / "java")
            package = "com.kubercloud.ani.core" if layer == "core" else "com.kubercloud.ani.services"
            run(["java", "-cp", str(classes), package + ".Smoke"], base / "java")
            run(["javac", "-cp", str(classes), "-d", str(classes), *java_example_files], base / "java")
            run(["java", "-cp", str(classes), "Basic"], base / "java")
        else:
            validate_java_sources(base / "java", layer)
            print(f"{layer} java SDK alpha source smoke ok (JDK unavailable for compile smoke)")


def validate_java_sources(java_root: Path, layer: str) -> None:
    package = "com.kubercloud.ani.core" if layer == "core" else "com.kubercloud.ani.services"
    package_path = Path(*package.split("."))
    client = java_root / "src/main/java" / package_path / "ApiClient.java"
    smoke = java_root / "src/test/java" / package_path / "Smoke.java"
    example = java_root / "examples/Basic.java"
    for path in (client, smoke):
        content = path.read_text(encoding="utf-8")
        if f"package {package};" not in content:
            raise SystemExit(f"SDK alpha smoke failed: {path} has wrong package")
        if "class " not in content or "{" not in content or "}" not in content:
            raise SystemExit(f"SDK alpha smoke failed: {path} does not look like Java source")
    example_content = example.read_text(encoding="utf-8")
    if f"import {package}.ApiClient;" not in example_content or "class Basic" not in example_content:
        raise SystemExit(f"SDK alpha smoke failed: {example} does not look like Java example source")


def main() -> None:
    root = Path(__file__).resolve().parents[1]
    errors: list[str] = []
    for layer, spec_path in LAYERS.items():
        validate_metadata(root, layer, spec_path, errors)
    if not errors:
        validate_separation(root, errors)
    validate_files(root, errors)
    validate_idempotency_helpers(root, errors)
    if errors:
        for error in errors:
            print(f"sdk alpha error: {error}", file=sys.stderr)
        raise SystemExit(1)
    run_smoke(root)
    print("SDK Alpha artifacts valid")


if __name__ == "__main__":
    main()
