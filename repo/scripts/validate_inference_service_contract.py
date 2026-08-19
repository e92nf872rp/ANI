#!/usr/bin/env python3
"""Validate the approved ANI Services InferenceService Stage B contract."""

from __future__ import annotations

from pathlib import Path
from typing import Any

import yaml


ROOT = Path(__file__).resolve().parents[1]
SPEC_PATH = ROOT / "api/openapi/services/v1.yaml"
HANDLER_PATH = ROOT / "services/ani-gateway/internal/router/inference_resources.go"
HANDLER_BASELINE_PATH = ROOT / "architecture/inference-service-handler-baseline.yaml"
ASYNC_TASK_REF = "#/components/schemas/AsyncTask"
INFERENCE_SERVICE_REF = "#/components/schemas/InferenceService"
STABLE_INFERENCE_ERROR_CODES = {
    "INVALID_ARGUMENT",
    "NAME_CONFLICT",
    "IDEMPOTENCY_CONFLICT",
    "OPERATION_IN_PROGRESS",
    "MODEL_NOT_READY",
    "MODEL_INCOMPATIBLE",
    "ACCELERATOR_SPEC_UNAVAILABLE",
    "INSUFFICIENT_CAPACITY",
    "UNSUPPORTED_TOPOLOGY",
    "INVALID_STATE_TRANSITION",
    "IMAGE_UNAVAILABLE",
}
REQUIRED_OPERATIONS = {
    ("/inference-services", "get"): "listInferenceServices",
    ("/inference-services", "post"): "createInferenceService",
    ("/inference-services/{service_id}", "get"): "getInferenceService",
    ("/inference-services/{service_id}", "patch"): "updateInferenceService",
    ("/inference-services/{service_id}", "delete"): "deleteInferenceService",
    ("/inference-services/{service_id}/lifecycle", "post"): "applyInferenceServiceLifecycle",
    ("/inference-operations/{operation_id}", "get"): "getInferenceOperation",
    ("/inference-services/{service_id}/logs", "get"): "getInferenceServiceLogs",
    ("/inference-services/{service_id}/test", "post"): "testInferenceService",
    ("/inference-services/{service_id}/policies", "put"): "updateInferenceServicePolicies",
}
EXPECTED_TASK_TYPES = {
    ("/inference-services", "post"): ["inference_service.create"],
    ("/inference-services/{service_id}", "patch"): ["inference_service.scale"],
    ("/inference-services/{service_id}", "delete"): ["inference_service.delete"],
    ("/inference-services/{service_id}/lifecycle", "post"): [
        "inference_service.start",
        "inference_service.stop",
        "inference_service.restart",
    ],
    ("/inference-operations/{operation_id}", "get"): [
        "inference_service.create",
        "inference_service.scale",
        "inference_service.start",
        "inference_service.stop",
        "inference_service.restart",
        "inference_service.delete",
    ],
}
HANDLER_OPERATIONS = (
    "createInferenceService",
    "updateInferenceService",
    "deleteInferenceService",
)
GO_STATUS_CODES = {
    "OK": 200,
    "Accepted": 202,
    "NoContent": 204,
}


def load_yaml(path: Path = SPEC_PATH) -> dict[str, Any]:
    with path.open(encoding="utf-8") as handle:
        value = yaml.safe_load(handle) or {}
    if not isinstance(value, dict):
        raise ValueError(f"{path} must contain a YAML object")
    return value


def response_ref(operation: dict[str, Any], status: str) -> str | None:
    response = (operation.get("responses") or {}).get(status) or {}
    schema = (((response.get("content") or {}).get("application/json") or {}).get("schema") or {})
    return schema.get("$ref") if isinstance(schema, dict) else None


def component_response_ref(operation: dict[str, Any], status: str) -> str | None:
    response = (operation.get("responses") or {}).get(status) or {}
    return response.get("$ref") if isinstance(response, dict) else None


def function_body(source: str, function_name: str) -> str:
    marker = f"func {function_name}("
    start = source.find(marker)
    if start < 0:
        raise ValueError(f"handler function not found: {function_name}")
    opening = source.find("{", start)
    if opening < 0:
        raise ValueError(f"handler function has no body: {function_name}")
    depth = 0
    for index in range(opening, len(source)):
        if source[index] == "{":
            depth += 1
        elif source[index] == "}":
            depth -= 1
            if depth == 0:
                return source[opening + 1 : index]
    raise ValueError(f"handler function has an unterminated body: {function_name}")


def handler_status(source: str, function_name: str) -> int:
    body = function_body(source, function_name)
    marker = "http.Status"
    start = body.find(marker)
    if start < 0:
        raise ValueError(f"handler function has no HTTP status: {function_name}")
    suffix = body[start + len(marker) :]
    status_name = ""
    for character in suffix:
        if not character.isalpha():
            break
        status_name += character
    if status_name not in GO_STATUS_CODES:
        raise ValueError(f"unsupported HTTP status in {function_name}: {status_name}")
    return GO_STATUS_CODES[status_name]


def expected_success_statuses(spec: dict[str, Any]) -> dict[str, int]:
    result: dict[str, int] = {}
    for path_item in (spec.get("paths") or {}).values():
        if not isinstance(path_item, dict):
            continue
        for operation in path_item.values():
            if not isinstance(operation, dict):
                continue
            operation_id = operation.get("operationId")
            if operation_id not in HANDLER_OPERATIONS:
                continue
            success = sorted(int(code) for code in (operation.get("responses") or {}) if str(code).startswith("2"))
            if len(success) != 1:
                raise ValueError(f"{operation_id} must declare exactly one success status")
            result[operation_id] = success[0]
    return result


def load_handler_baseline(path: Path = HANDLER_BASELINE_PATH) -> dict[str, tuple[int, int]]:
    document = load_yaml(path)
    if document.get("version") != 1:
        raise ValueError(f"{path}: version must be 1")
    result: dict[str, tuple[int, int]] = {}
    for entry in document.get("exceptions") or []:
        operation_id = str(entry.get("operation_id", ""))
        actual = entry.get("actual_status")
        expected = entry.get("expected_status")
        if operation_id not in HANDLER_OPERATIONS or not isinstance(actual, int) or not isinstance(expected, int):
            raise ValueError(f"{path}: invalid inference handler baseline entry")
        if operation_id in result:
            raise ValueError(f"{path}: duplicate baseline entry for {operation_id}")
        result[operation_id] = (actual, expected)
    return result


def validate_handler_statuses(
    spec: dict[str, Any],
    source: str,
    baseline: dict[str, tuple[int, int]],
) -> tuple[tuple[str, ...], tuple[str, ...]]:
    errors: list[str] = []
    warnings: list[str] = []
    expected = expected_success_statuses(spec)
    actual = {operation_id: handler_status(source, operation_id) for operation_id in HANDLER_OPERATIONS}
    mismatches = {
        operation_id: (actual[operation_id], expected[operation_id])
        for operation_id in HANDLER_OPERATIONS
        if actual[operation_id] != expected[operation_id]
    }
    for operation_id, statuses in mismatches.items():
        if baseline.get(operation_id) == statuses:
            warnings.append(
                f"accepted inference handler baseline: {operation_id} returns {statuses[0]}, contract requires {statuses[1]}"
            )
        else:
            errors.append(
                f"inference handler response mismatch: {operation_id} returns {statuses[0]}, contract requires {statuses[1]}"
            )
    for operation_id in baseline:
        if operation_id not in mismatches:
            errors.append(f"stale inference handler baseline: {operation_id}")
        elif baseline[operation_id] != mismatches[operation_id]:
            errors.append(f"stale inference handler baseline statuses: {operation_id}")
    return tuple(warnings), tuple(errors)


def validate(spec: dict[str, Any]) -> tuple[str, ...]:
    errors: list[str] = []
    paths = spec.get("paths") or {}
    components = spec.get("components") or {}
    schemas = components.get("schemas") or {}
    responses = components.get("responses") or {}

    for (path, method), expected_id in REQUIRED_OPERATIONS.items():
        operation = (paths.get(path) or {}).get(method)
        label = f"{method.upper()} {path}"
        if not isinstance(operation, dict):
            errors.append(f"missing operation: {label}")
            continue
        if operation.get("operationId") != expected_id:
            errors.append(f"{label} operationId must be {expected_id}")
        if not operation.get("security"):
            errors.append(f"{label} must require authentication")

    for (path, method), task_types in EXPECTED_TASK_TYPES.items():
        operation = (paths.get(path) or {}).get(method) or {}
        if operation.get("x-ani-async-task-types") != task_types:
            errors.append(f"{method.upper()} {path} has invalid inference task types")
        if operation.get("x-ani-async-resource-type") != "inference_service":
            errors.append(f"{method.upper()} {path} must use inference_service resource type")

    create = (paths.get("/inference-services") or {}).get("post") or {}
    create_schema = (((create.get("requestBody") or {}).get("content") or {}).get("application/json") or {}).get("schema") or {}
    if create_schema.get("$ref") != "#/components/schemas/CreateInferenceServiceRequest":
        errors.append("createInferenceService must use CreateInferenceServiceRequest")
    if response_ref(create, "202") != INFERENCE_SERVICE_REF:
        errors.append("createInferenceService 202 must preserve the InferenceService response")

    inference_mutations = (
        create,
        (paths.get("/inference-services/{service_id}") or {}).get("patch") or {},
        (paths.get("/inference-services/{service_id}/lifecycle") or {}).get("post") or {},
    )
    for operation in inference_mutations:
        op_id = operation.get("operationId", "unknown inference mutation")
        if component_response_ref(operation, "400") != "#/components/responses/InferenceBadRequest":
            errors.append(f"{op_id} 400 must use InferenceBadRequest")
        if component_response_ref(operation, "409") != "#/components/responses/InferenceConflict":
            errors.append(f"{op_id} 409 must use InferenceConflict")

    delete_operation = (paths.get("/inference-services/{service_id}") or {}).get("delete") or {}
    if component_response_ref(delete_operation, "409") != "#/components/responses/InferenceConflict":
        errors.append("deleteInferenceService 409 must use InferenceConflict")
    if component_response_ref(delete_operation, "503") != "#/components/responses/ServiceUnavailable":
        errors.append("deleteInferenceService 503 must use ServiceUnavailable")

    for path, method in (
        ("/inference-services/{service_id}", "patch"),
        ("/inference-services/{service_id}", "delete"),
        ("/inference-services/{service_id}/lifecycle", "post"),
    ):
        operation = (paths.get(path) or {}).get(method) or {}
        if response_ref(operation, "202") != ASYNC_TASK_REF:
            errors.append(f"{method.upper()} {path} 202 must use AsyncTask")

    operation_get = (paths.get("/inference-operations/{operation_id}") or {}).get("get") or {}
    if response_ref(operation_get, "200") != ASYNC_TASK_REF:
        errors.append("getInferenceOperation 200 must use AsyncTask")

    create_request = schemas.get("CreateInferenceServiceRequest") or {}
    required_create = set(create_request.get("required") or [])
    if required_create != {"idempotency_key", "name", "model"}:
        errors.append("CreateInferenceServiceRequest must preserve required idempotency_key, name, and model")
    create_properties = create_request.get("properties") or {}
    for field in ("image_id", "image_ref"):
        field_spec = create_properties.get(field) or {}
        if field_spec.get("type") != "string" or field_spec.get("minLength") != 1:
            errors.append(f"CreateInferenceServiceRequest.{field} must be a non-empty optional string")
        if field in required_create:
            errors.append(f"CreateInferenceServiceRequest.{field} must remain optional so registry or manual input both work")
    for field in ("model_version_id", "served_model_name", "resources", "placement_mode", "image_id", "image_ref", "engine"):
        if field not in create_properties:
            errors.append(f"CreateInferenceServiceRequest missing {field}")
    if (create_properties.get("engine") or {}).get("$ref") != "#/components/schemas/InferenceServiceEngine":
        errors.append("CreateInferenceServiceRequest.engine must reference InferenceServiceEngine")
    if "default" in (create_properties.get("engine") or {}):
        errors.append("CreateInferenceServiceRequest.engine must remain optional in generated clients")
    for field in ("gpu_type", "gpu_count_per_pod", "max_concurrency"):
        if not (create_properties.get(field) or {}).get("deprecated"):
            errors.append(f"CreateInferenceServiceRequest.{field} must be deprecated")
    for field in ("name", "model"):
        if "minLength" in (create_properties.get(field) or {}):
            errors.append(f"CreateInferenceServiceRequest.{field} must not tighten v1 minLength")
    for field in ("replicas", "gpu_count_per_pod", "max_concurrency"):
        if "minimum" in (create_properties.get(field) or {}):
            errors.append(f"CreateInferenceServiceRequest.{field} must not tighten v1 minimum")
    for field in ("replicas", "placement_mode", "gpu_count_per_pod", "max_concurrency", "image_id", "image_ref", "engine"):
        if "default" in (create_properties.get(field) or {}):
            errors.append(f"CreateInferenceServiceRequest.{field} must remain optional in generated clients")

    resources = schemas.get("InferenceServiceResources") or {}
    if set(resources.get("required") or []) != {"cpu", "memory"}:
        errors.append("InferenceServiceResources must require cpu and memory")
    accelerator = schemas.get("InferenceServiceAccelerator") or {}
    if set(accelerator.get("required") or []) != {"spec_id", "count_per_replica"}:
        errors.append("InferenceServiceAccelerator must require spec_id and count_per_replica")

    update_request = schemas.get("UpdateInferenceServiceRequest") or {}
    if set((update_request.get("properties") or {}).keys()) != {"idempotency_key", "replicas"}:
        errors.append("UpdateInferenceServiceRequest may only contain idempotency_key and replicas")
    lifecycle = schemas.get("InferenceServiceLifecycleRequest") or {}
    actions = (((lifecycle.get("properties") or {}).get("action") or {}).get("enum") or [])
    if actions != ["start", "stop", "restart"]:
        errors.append("InferenceServiceLifecycleRequest actions must be start, stop, restart")

    resource_properties = (schemas.get("InferenceService") or {}).get("properties") or {}
    required_response_fields = {
        "model_version_id",
        "image_id",
        "image_ref",
        "served_model_name",
        "ready_replicas",
        "resources",
        "placement_mode",
        "status_reason",
        "status_message",
        "generation",
        "observed_generation",
        "current_operation_id",
        "invocation_url",
        "endpoint_url",
        "updated_at",
        "engine",
    }
    for field in sorted(required_response_fields - set(resource_properties)):
        errors.append(f"InferenceService missing {field}")
    for forbidden in ("runtime_endpoint", "runtime_ref", "internal_endpoint"):
        if forbidden in resource_properties:
            errors.append(f"InferenceService must not expose {forbidden}")
    for endpoint in ("invocation_url", "endpoint_url"):
        if not (resource_properties.get(endpoint) or {}).get("nullable"):
            errors.append(f"InferenceService.{endpoint} must be nullable")
    if (resource_properties.get("engine") or {}).get("$ref") != "#/components/schemas/InferenceServiceEngine":
        errors.append("InferenceService.engine must reference InferenceServiceEngine")

    engine = schemas.get("InferenceServiceEngine") or {}
    if engine.get("additionalProperties") is not False:
        errors.append("InferenceServiceEngine must set additionalProperties false")
    if engine.get("x-ani-reserved-engine-arg-names"):
        errors.append("InferenceServiceEngine must not reserve CLI arg names; command is the complete argv")
    for leftover in ("extra_args", "args"):
        if leftover in (engine.get("properties") or {}):
            errors.append(f"InferenceServiceEngine must not keep {leftover}; command is the complete argv")
    if schemas.get("InferenceServiceEngineArg"):
        errors.append("InferenceServiceEngineArg must be removed; command is a string argv")
    engine_env = (engine.get("properties") or {}).get("env") or {}
    if engine_env.get("maxItems") != 32:
        errors.append("InferenceServiceEngine.env must cap at 32 items")
    if engine_env.get("items", {}).get("$ref") != "#/components/schemas/InferenceServiceEngineEnvVar":
        errors.append("InferenceServiceEngine.env items must be InferenceServiceEngineEnvVar")
    reserved_env = engine.get("x-ani-reserved-engine-env-names") or []
    expected_reserved_env = [
        "CUDA_VISIBLE_DEVICES",
        "NVIDIA_VISIBLE_DEVICES",
        "NVIDIA_DRIVER_CAPABILITIES",
        "PYTHONPATH",
        "PATH",
        "LD_PRELOAD",
        "LD_LIBRARY_PATH",
        "RAY_EXPERIMENTAL_NOSET_CUDA_VISIBLE_DEVICES",
    ]
    if reserved_env != expected_reserved_env:
        errors.append("InferenceServiceEngine must freeze the reserved engine env names")
    engine_env_var = schemas.get("InferenceServiceEngineEnvVar") or {}
    if set(engine_env_var.get("required") or []) != {"name", "value"}:
        errors.append("InferenceServiceEngineEnvVar must require name and value")
    if engine_env_var.get("additionalProperties") is not False:
        errors.append("InferenceServiceEngineEnvVar must set additionalProperties false")
    env_name = (engine_env_var.get("properties") or {}).get("name") or {}
    if env_name.get("pattern") != "^[A-Za-z_][A-Za-z0-9_]*$":
        errors.append("InferenceServiceEngineEnvVar.name must be a POSIX environment variable name")
    engine_command = (engine.get("properties") or {}).get("command") or {}
    if engine_command.get("minItems") != 1:
        errors.append("InferenceServiceEngine.command must require at least one argv item when present")
    if engine_command.get("maxItems") != 64:
        errors.append("InferenceServiceEngine.command must cap at 64 argv items")
    command_item = engine_command.get("items") or {}
    if command_item.get("type") != "string" or command_item.get("minLength") != 1:
        errors.append("InferenceServiceEngine.command items must be non-empty strings")
    if command_item.get("$ref"):
        errors.append("InferenceServiceEngine.command must be a string argv, not structured flags")

    policies = (paths.get("/inference-services/{service_id}/policies") or {}).get("put") or {}
    if "501" not in (policies.get("responses") or {}):
        errors.append("updateInferenceServicePolicies must declare 501 FEATURE_NOT_AVAILABLE")
    runtime_test = (paths.get("/inference-services/{service_id}/test") or {}).get("post") or {}
    missing_runtime_responses = {"422", "502", "503", "504"} - set(runtime_test.get("responses") or {})
    if missing_runtime_responses:
        errors.append(
            "testInferenceService missing stable failure responses: "
            + ", ".join(sorted(missing_runtime_responses))
        )
    declared_error_codes = set((responses.get("InferenceConflict") or {}).get("x-ani-error-codes") or [])
    declared_error_codes.update((responses.get("InferenceUnprocessableEntity") or {}).get("x-ani-error-codes") or [])
    if (responses.get("InferenceBadRequest") or {}).get("description", "").find("code=INVALID_ARGUMENT") >= 0:
        declared_error_codes.add("INVALID_ARGUMENT")
    missing_error_codes = STABLE_INFERENCE_ERROR_CODES - declared_error_codes
    if missing_error_codes:
        errors.append("missing stable inference error codes: " + ", ".join(sorted(missing_error_codes)))
    for legacy in ("InferenceEndpoint", "CreateInferenceEndpointRequest"):
        if not (schemas.get(legacy) or {}).get("deprecated"):
            errors.append(f"{legacy} must be marked deprecated")

    return tuple(errors)


def main() -> int:
    try:
        spec = load_yaml()
        errors = list(validate(spec))
        warnings, handler_errors = validate_handler_statuses(
            spec,
            HANDLER_PATH.read_text(encoding="utf-8"),
            load_handler_baseline(),
        )
        errors.extend(handler_errors)
    except (OSError, ValueError, yaml.YAMLError) as exc:
        print(f"inference service contract invalid: {exc}")
        return 1
    for warning in warnings:
        print(f"WARNING: {warning}")
    for error in errors:
        print(f"ERROR: {error}")
    if errors:
        print(f"inference service contract blocked: {len(errors)} error(s)")
        return 1
    print("inference service contract valid")
    return 0


if __name__ == "__main__":
    raise SystemExit(main())
