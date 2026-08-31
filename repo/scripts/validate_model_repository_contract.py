#!/usr/bin/env python3
from __future__ import annotations

import sys
from pathlib import Path
from typing import Any

import yaml

REQUIRED_SECURITY = [{"BearerAuth": []}, {"ApiKeyAuth": []}]

REQUIRED_OPERATIONS = {
    ("get", "/models"): ("listModels", "read"),
    ("post", "/models"): ("createModel", "create"),
    ("post", "/models/import"): ("importModel", "create"),
    ("get", "/models/{model_id}"): ("getModel", "read"),
    ("delete", "/models/{model_id}"): ("deleteModel", "delete"),
    ("get", "/models/{model_id}/versions"): ("listModelVersions", "read"),
    ("post", "/models/{model_id}/versions"): ("createModelVersion", "create"),
    ("post", "/models/{model_id}/upload-url"): ("getModelUploadURL", "create"),
    ("get", "/model-import-tasks/{task_id}"): ("getModelImportTask", "read"),
}

FORBIDDEN_SCHEMA_PREFIXES = (
    "ModelEvaluation",
    "ModelOptimization",
    "ModelGovernance",
    "ModelUsage",
)


def load_spec(path: str) -> dict[str, Any]:
    with Path(path).open("r", encoding="utf-8") as handle:
        return yaml.safe_load(handle)


def _schema_ref(spec: dict[str, Any], ref: str) -> dict[str, Any]:
    prefix = "#/components/schemas/"
    if not ref.startswith(prefix):
        raise AssertionError(f"unsupported schema reference: {ref!r}")
    name = ref[len(prefix) :]
    try:
        return spec["components"]["schemas"][name]
    except KeyError as exc:
        raise AssertionError(f"missing schema: {name}") from exc


def _json_schema(operation: dict[str, Any], container: str) -> dict[str, Any]:
    if container == "requestBody":
        return operation[container]["content"]["application/json"]["schema"]
    return operation["responses"][container]["content"]["application/json"]["schema"]


def validate_authz_format(spec: dict[str, Any]) -> None:
    for (method, path), (operation_id, action) in REQUIRED_OPERATIONS.items():
        operation = spec.get("paths", {}).get(path, {}).get(method)
        if operation is None:
            raise AssertionError(f"missing operation: {method.upper()} {path}")
        if operation.get("operationId") != operation_id:
            raise AssertionError(
                f"{method.upper()} {path} operationId = {operation.get('operationId')!r}, want {operation_id!r}"
            )
        if operation.get("security") != REQUIRED_SECURITY:
            raise AssertionError(f"{method.upper()} {path} must allow BearerAuth or ApiKeyAuth")
        expected = {
            "version": "v1",
            "resource": "model",
            "action": action,
            "boundary": "tenant",
            "principal_kinds": ["user"],
        }
        if operation.get("x-ani-authz") != expected:
            raise AssertionError(f"{method.upper()} {path} must define v1 model x-ani-authz metadata")


def validate_upload_contract(spec: dict[str, Any]) -> None:
    operation = spec.get("paths", {}).get("/models/{model_id}/upload-url", {}).get("post")
    if operation is None:
        raise AssertionError("missing operation: POST /models/{model_id}/upload-url")
    request_schema = _json_schema(operation, "requestBody")
    if "$ref" in request_schema:
        request_schema = _schema_ref(spec, request_schema["$ref"])
    required_request = {"idempotency_key", "version", "file_name", "size_bytes"}
    if set(request_schema.get("required", [])) != required_request:
        raise AssertionError("model upload URL request must require idempotency_key, version, file_name, size_bytes")
    if not required_request.issubset(request_schema.get("properties", {})):
        raise AssertionError("model upload URL request properties are incomplete")

    response_schema = _json_schema(operation, "201")
    if "$ref" in response_schema:
        response_schema = _schema_ref(spec, response_schema["$ref"])
    required_response = {"upload_url", "storage_path", "expires_at"}
    if set(response_schema.get("required", [])) != required_response:
        raise AssertionError("model upload URL response must require upload_url, storage_path, expires_at")
    if not required_response.issubset(response_schema.get("properties", {})):
        raise AssertionError("model upload URL response properties are incomplete")


def validate(spec: dict[str, Any]) -> None:
    schemas = spec["components"]["schemas"]
    model = schemas["Model"]["properties"]
    version = schemas["ModelVersion"]["properties"]
    required_model_fields = {"description", "updated_at", "versions"}
    required_version_fields = {"checksum_sha256", "storage_path"}
    if not required_model_fields.issubset(model):
        raise AssertionError("Model must expose description, updated_at, and versions")
    if model["versions"].get("items", {}).get("$ref") != "#/components/schemas/ModelVersion":
        raise AssertionError("Model.versions must contain ModelVersion items")
    if not required_version_fields.issubset(version):
        raise AssertionError("ModelVersion must expose checksum_sha256 and storage_path")

    list_parameters = spec["paths"]["/models"]["get"].get("parameters", [])
    parameter_names = {parameter.get("name") for parameter in list_parameters}
    if not {"keyword", "source", "capability"}.issubset(parameter_names):
        raise AssertionError("GET /models must expose keyword, source, and capability filters")

    validate_authz_format(spec)
    validate_upload_contract(spec)

    import_response = _json_schema(spec["paths"]["/model-import-tasks/{task_id}"]["get"], "200")
    if import_response.get("$ref") != "#/components/schemas/AsyncTask":
        raise AssertionError("model import task query must return AsyncTask")

    conflict = spec["paths"]["/models/{model_id}"]["delete"].get("responses", {}).get("409", {})
    if "MODEL_IN_USE" not in conflict.get("description", ""):
        raise AssertionError("DELETE /models/{model_id} must declare MODEL_IN_USE conflict")

    unexpected = sorted(
        name for name in schemas if any(name.startswith(prefix) for prefix in FORBIDDEN_SCHEMA_PREFIXES)
    )
    if unexpected:
        raise AssertionError(f"planned model schemas must stay out of P0: {unexpected}")


def main() -> int:
    spec = load_spec("api/openapi/services/v1.yaml")
    validate(spec)
    print("model repository contract valid")
    return 0


if __name__ == "__main__":
    sys.exit(main())
