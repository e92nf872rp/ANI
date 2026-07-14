#!/usr/bin/env python3
"""Validate the Sprint 2 Core API Alpha contract.

This guard keeps Services P0 Core dependencies aligned across the API contract,
Gateway route registration, and auth boundary. It intentionally focuses on the
Alpha freeze surface instead of every future Core resource.
"""

from __future__ import annotations

import sys
from pathlib import Path

import yaml


EXPECTED_PATHS = {
    "/instances": {
        "get": ("listInstances", "scope:instances:read"),
        "post": ("createInstance", "scope:instances:create"),
    },
    "/instances/{instance_id}": {
        "get": ("getInstance", "scope:instances:read"),
    },
    "/instances/{instance_id}/lifecycle": {
        "post": ("applyInstanceLifecycle", "scope:instances:update"),
    },
    "/instances/{instance_id}/console": {
        "post": ("createInstanceConsoleSession", "scope:instances:console"),
    },
    "/instances/{instance_id}/operations": {
        "get": ("listInstanceOperations", "scope:instances:read"),
    },
    "/instance-operations/{operation_id}": {
        "get": ("getInstanceOperation", "scope:instances:read"),
    },
}

EXPECTED_SCHEMAS = {
    "InstanceRecord",
    "InstanceOperation",
    "InstanceListResponse",
    "CreateInstanceRequest",
    "CreateInstanceResponse",
    "InstanceLifecycleRequest",
    "InstanceLifecycleResponse",
    "CreateInstanceConsoleSessionRequest",
    "InstanceConsoleSession",
}

EXPECTED_GATEWAY_ROUTES = {
    'v1.GET("/instances", api.list)',
    'v1.POST("/instances", api.create)',
    'v1.GET("/instances/:instance_id", api.get)',
    'v1.POST("/instances/:instance_id/lifecycle", api.lifecycle)',
    'v1.POST("/instances/:instance_id/console", api.console)',
    'v1.GET("/instances/:instance_id/operations", api.listOperations)',
    'v1.GET("/instance-operations/:operation_id", api.getOperation)',
}

PROTECTED_PATH_TOKENS = {
    '"/api/v1/instances"',
    '"/api/v1/instances/"',
}

ALPHA_FIELDS = {
    "InstanceRecord": {
        "termination_protection",
        "ssh",
        "snapshots",
        "volumes",
        "container",
        "gpu",
    },
    "InstanceOperation": {
        "precheck_result",
        "before_spec",
        "after_spec",
        "failure_reason",
        "retry_eligible",
        "steps",
    },
}

B1_SUPPORT_SCHEMA_FIELDS = {
    "InstanceLogListResponse": {"items", "total", "next_cursor", "dev_profile"},
    "InstanceEventListResponse": {"items", "total", "next_cursor", "dev_profile"},
    "InstanceMetrics": {"instance_id", "timestamp", "dev_profile"},
    "InstanceSecurityEventListResponse": {"items", "total", "next_cursor", "dev_profile"},
    "InstanceExecSession": {"id", "instance_id", "ws_url", "expires_at", "dev_profile"},
    "GPUInventoryRecord": {"id", "node_name", "gpu_type", "gpu_index", "status", "dev_profile"},
    "GPUInventoryListResponse": {"items", "total", "next_cursor", "dev_profile"},
    "GPUOccupancyStats": {"total", "in_use", "available", "fault", "dev_profile"},
    "SandboxTemplate": {"id", "name", "image", "created_at", "dev_profile"},
    "SandboxTemplateListResponse": {"items", "total", "next_cursor", "dev_profile"},
}

EXPECTED_LIFECYCLE_ACTIONS = {
    "start",
    "stop",
    "restart",
    "resize",
    "rebuild",
    "delete",
    "snapshot",
    "attach_volume",
    "detach_volume",
    "rollback",
}


def fail(errors: list[str]) -> None:
    if errors:
        for error in errors:
            print(f"ERROR: {error}", file=sys.stderr)
        raise SystemExit(1)


def load_yaml(path: Path) -> dict:
    with path.open("r", encoding="utf-8") as handle:
        data = yaml.safe_load(handle)
    if not isinstance(data, dict):
        raise SystemExit(f"ERROR: {path} did not parse to an object")
    return data


def validate_api_contract(root: Path, errors: list[str]) -> None:
    spec = load_yaml(root / "api/openapi/v1.yaml")
    freeze = load_yaml(root / "api/core-alpha-freeze.yaml")
    server_url = spec.get("servers", [{}])[0].get("url")
    if server_url != "https://{host}/api/v1":
        errors.append('api/openapi/v1.yaml servers[0].url must be "https://{host}/api/v1"')

    paths = spec.get("paths", {})
    components = spec.get("components", {})
    schemas = components.get("schemas", {})
    response_components = components.get("responses", {})
    freeze_paths = freeze.get("paths", {})
    freeze_schemas = freeze.get("schemas", {})

    for path, methods in EXPECTED_PATHS.items():
        if path not in paths:
            errors.append(f"api/openapi/v1.yaml missing {path}")
            continue
        if path not in freeze_paths:
            errors.append(f"api/core-alpha-freeze.yaml missing path {path}")
            continue
        for method, (operation_id, scope) in methods.items():
            operation = paths[path].get(method)
            if not isinstance(operation, dict):
                errors.append(f"{path} missing {method.upper()}")
                continue
            freeze_operation = freeze_paths[path].get(method)
            if not isinstance(freeze_operation, dict):
                errors.append(f"api/core-alpha-freeze.yaml missing {method.upper()} {path}")
                continue
            if operation.get("operationId") != operation_id:
                errors.append(f"{method.upper()} {path} missing operationId {operation_id}")
            if operation.get("x-ani-rbac-scope") != scope:
                errors.append(f"{method.upper()} {path} missing x-ani-rbac-scope {scope}")
            if freeze_operation.get("operation_id") != operation_id:
                errors.append(f"freeze {method.upper()} {path} operation_id must be {operation_id}")
            if freeze_operation.get("rbac_scope") != scope:
                errors.append(f"freeze {method.upper()} {path} rbac_scope must be {scope}")
            if freeze_operation.get("maturity") != "dev_profile":
                errors.append(f"freeze {method.upper()} {path} maturity must be dev_profile")
            operation_responses = operation.get("responses", {})
            frozen_responses = {str(code) for code in freeze_operation.get("responses", [])}
            missing_response_codes = frozen_responses - set(operation_responses.keys())
            if missing_response_codes:
                errors.append(f"{method.upper()} {path} missing frozen responses: {sorted(missing_response_codes)}")
            for code in ("401", "403"):
                if code not in operation_responses:
                    errors.append(f"{method.upper()} {path} missing {code} response")

    extra_freeze_paths = set(freeze_paths) - set(EXPECTED_PATHS)
    if extra_freeze_paths:
        errors.append(f"api/core-alpha-freeze.yaml has unexpected Core Alpha paths: {sorted(extra_freeze_paths)}")

    for schema in EXPECTED_SCHEMAS:
        if schema not in schemas:
            errors.append(f"api/openapi/v1.yaml missing schema {schema}")

    for schema, fields in B1_SUPPORT_SCHEMA_FIELDS.items():
        properties = schemas.get(schema, {}).get("properties", {})
        missing_fields = fields - set(properties)
        if missing_fields:
            errors.append(f"schema {schema} missing B1 support fields: {sorted(missing_fields)}")

    for schema, fields in ALPHA_FIELDS.items():
        properties = schemas.get(schema, {}).get("properties", {})
        for field in fields:
            if field not in properties:
                errors.append(f"schema {schema} missing Alpha field {field}")
    frozen_instance_fields = set(freeze_schemas.get("instance_record_fields", {}).get("alpha_fields", []))
    if not ALPHA_FIELDS["InstanceRecord"].issubset(frozen_instance_fields):
        missing = ALPHA_FIELDS["InstanceRecord"] - frozen_instance_fields
        errors.append(f"api/core-alpha-freeze.yaml instance alpha fields missing: {sorted(missing)}")

    create_required = set(schemas.get("CreateInstanceRequest", {}).get("required", []))
    if "idempotency_key" not in create_required:
        errors.append("CreateInstanceRequest must require idempotency_key")
    frozen_create_required = set(freeze_schemas.get("create_instance_request_fields", {}).get("required", []))
    expected_create_required = {"name", "kind", "idempotency_key"}
    if frozen_create_required != expected_create_required:
        errors.append(f"api/core-alpha-freeze.yaml create request required fields must be {sorted(expected_create_required)}")
    create_properties = schemas.get("CreateInstanceRequest", {}).get("properties", {})
    for field in ("ssh_username", "ssh_key_ref", "termination_protection"):
        if field not in create_properties:
            errors.append(f"CreateInstanceRequest missing Alpha field {field}")
    frozen_create_fields = set(freeze_schemas.get("create_instance_request_fields", {}).get("alpha_fields", []))
    expected_create_fields = {"ssh_username", "ssh_key_ref", "termination_protection", "replicas", "gpu.vendor", "gpu.model", "gpu.count"}
    missing_frozen_create_fields = expected_create_fields - frozen_create_fields
    if missing_frozen_create_fields:
        errors.append(f"api/core-alpha-freeze.yaml create request alpha fields missing: {sorted(missing_frozen_create_fields)}")

    lifecycle_required = set(schemas.get("InstanceLifecycleRequest", {}).get("required", []))
    if "idempotency_key" not in lifecycle_required:
        errors.append("InstanceLifecycleRequest must require idempotency_key")
    console_required = set(schemas.get("CreateInstanceConsoleSessionRequest", {}).get("required", []))
    if "idempotency_key" not in console_required:
        errors.append("CreateInstanceConsoleSessionRequest must require idempotency_key")
    frozen_lifecycle_required = set(freeze_schemas.get("lifecycle_request_fields", {}).get("required", []))
    expected_lifecycle_required = {"action", "idempotency_key"}
    if frozen_lifecycle_required != expected_lifecycle_required:
        errors.append(f"api/core-alpha-freeze.yaml lifecycle request required fields must be {sorted(expected_lifecycle_required)}")
    lifecycle_action = schemas.get("InstanceLifecycleRequest", {}).get("properties", {}).get("action", {})
    lifecycle_enum = set(lifecycle_action.get("enum", []))
    missing_actions = EXPECTED_LIFECYCLE_ACTIONS - lifecycle_enum
    if missing_actions:
        errors.append(f"InstanceLifecycleRequest.action missing Alpha actions: {sorted(missing_actions)}")
    frozen_lifecycle = set(freeze_schemas.get("lifecycle_actions", []))
    missing_frozen_actions = EXPECTED_LIFECYCLE_ACTIONS - frozen_lifecycle
    if missing_frozen_actions:
        errors.append(f"api/core-alpha-freeze.yaml lifecycle actions missing: {sorted(missing_frozen_actions)}")
    lifecycle_properties = schemas.get("InstanceLifecycleRequest", {}).get("properties", {})
    for field in ("snapshot_name", "volume_id", "revision"):
        if field not in lifecycle_properties:
            errors.append(f"InstanceLifecycleRequest missing Alpha field {field}")
    frozen_lifecycle_fields = set(freeze_schemas.get("lifecycle_request_fields", {}).get("alpha_fields", []))
    expected_lifecycle_fields = {"snapshot_name", "volume_id", "revision"}
    missing_frozen_lifecycle_fields = expected_lifecycle_fields - frozen_lifecycle_fields
    if missing_frozen_lifecycle_fields:
        errors.append(f"api/core-alpha-freeze.yaml lifecycle request alpha fields missing: {sorted(missing_frozen_lifecycle_fields)}")
    operation_enum = set(schemas.get("InstanceOperation", {}).get("properties", {}).get("operation", {}).get("enum", []))
    if "console_session" not in operation_enum:
        errors.append("InstanceOperation.operation missing console_session")
    ssh_properties = schemas.get("InstanceRecord", {}).get("properties", {}).get("ssh", {}).get("properties", {})
    for field in ("username", "host", "port", "key_ref", "ready", "reason"):
        if field not in ssh_properties:
            errors.append(f"InstanceRecord.ssh missing Alpha field {field}")
    console_properties = schemas.get("InstanceConsoleSession", {}).get("properties", {})
    for field in ("operation_id", "session_id", "connect_url", "url", "expires_at"):
        if field not in console_properties:
            errors.append(f"InstanceConsoleSession missing Alpha field {field}")
    container_properties = schemas.get("InstanceRecord", {}).get("properties", {}).get("container", {}).get("properties", {})
    for field in ("replicas", "ready_replicas", "revision", "rollout_status", "history"):
        if field not in container_properties:
            errors.append(f"InstanceRecord.container missing Alpha field {field}")
    gpu_properties = schemas.get("InstanceRecord", {}).get("properties", {}).get("gpu", {}).get("properties", {})
    for field in ("vendor", "model", "count", "scheduling_reason", "utilization_percent"):
        if field not in gpu_properties:
            errors.append(f"InstanceRecord.gpu missing Alpha field {field}")
    create_properties = schemas.get("CreateInstanceRequest", {}).get("properties", {})
    if "replicas" not in create_properties:
        errors.append("CreateInstanceRequest missing Alpha field replicas")
    create_gpu_properties = create_properties.get("gpu", {}).get("properties", {})
    for field in ("vendor", "model", "count"):
        if field not in create_gpu_properties:
            errors.append(f"CreateInstanceRequest.gpu missing Alpha field {field}")
    frozen_states = set(freeze.get("states", []))
    state_enum = set(schemas.get("InstanceRecord", {}).get("properties", {}).get("state", {}).get("enum", []))
    if frozen_states != state_enum:
        errors.append(f"api/core-alpha-freeze.yaml states must match InstanceRecord.state enum: {sorted(state_enum)}")
    frozen_error_components = set(freeze.get("error_responses", {}).get("components", []))
    required_error_components = {"Unauthorized", "Forbidden", "NotFound", "BadRequest", "Conflict", "RateLimitExceeded"}
    if not required_error_components.issubset(set(response_components.keys())):
        errors.append(f"api/openapi/v1.yaml missing response components: {sorted(required_error_components - set(response_components.keys()))}")
    if frozen_error_components != required_error_components:
        errors.append(f"api/core-alpha-freeze.yaml error response components must be {sorted(required_error_components)}")


def validate_runtime_contract(root: Path, errors: list[str]) -> None:
    workload_runtime_go = (root / "pkg/ports/workload_runtime.go").read_text(encoding="utf-8")
    required_tokens = {
        "WorkloadLifecycleRebuild",
        '"rebuild"',
        "WorkloadLifecycleSnapshot",
        '"snapshot"',
        "WorkloadLifecycleAttachVolume",
        '"attach_volume"',
        "WorkloadLifecycleDetachVolume",
        '"detach_volume"',
        "WorkloadLifecycleRollback",
        '"rollback"',
        "TerminationProtection bool",
        "AttachVolume(ctx context.Context",
        "DetachVolume(ctx context.Context",
        "Rollback(ctx context.Context",
        "ContainerInstanceStatus",
        "ContainerRevisionHistory",
        "GPUInstanceStatus",
    }
    for token in required_tokens:
        if token not in workload_runtime_go:
            errors.append(f"pkg/ports/workload_runtime.go missing Core Alpha token: {token}")

    migration_text = (root / "deploy/migrations/20260519_004_instance_u_vm_protection.sql").read_text(
        encoding="utf-8"
    )
    for token in ("lifecycle_policy", "ssh_connection", "snapshots", "container_status", "gpu_status", "termination_protection", "'rebuild'", "'snapshot'", "'rollback'", "'console_session'"):
        if token not in migration_text:
            errors.append(f"migration 004 missing Core Alpha persistence token: {token}")


def validate_gateway_routes(root: Path, errors: list[str]) -> None:
    routes_go = (root / "services/ani-gateway/internal/router/demo_instances.go").read_text(encoding="utf-8")
    for route in EXPECTED_GATEWAY_ROUTES:
        if route not in routes_go:
            errors.append(f"router/demo_instances.go missing route registration: {route}")
    for token in ('case "snapshot":', 'case "attach_volume":', 'case "detach_volume":', 'case "rollback":'):
        if token not in routes_go:
            errors.append(f"router/demo_instances.go missing lifecycle handler token: {token}")
    for token in ('json:"gpu"', 'req.GPU.Vendor', 'req.GPU.Model', 'req.GPU.Count'):
        if token not in routes_go:
            errors.append(f"router/demo_instances.go missing OpenAPI GPU create token: {token}")
    for token in ("errors.Is(err, ports.ErrConflict)", "http.StatusConflict"):
        if token not in routes_go:
            errors.append(f"router/demo_instances.go missing lifecycle conflict mapping token: {token}")
    for token in ("hasIdempotencyKey(req.IdempotencyKey)", '"idempotency_key is required"'):
        if token not in routes_go:
            errors.append(f"router/demo_instances.go missing idempotency requirement token: {token}")

    stubs_go = (root / "services/ani-gateway/internal/router/stubs.go").read_text(encoding="utf-8")
    for token in ('"/instances"', '"/instances/', '"/instance-operations/'):
        if token in stubs_go:
            errors.append(f"router/stubs.go must not contain Core Alpha instance stub {token}")


def validate_auth_boundary(root: Path, errors: list[str]) -> None:
    auth_go = (root / "services/ani-gateway/internal/middleware/auth.go").read_text(encoding="utf-8")
    for token in PROTECTED_PATH_TOKENS:
        if token in auth_go:
            errors.append(f"middleware/auth.go must keep Core instance path protected: {token}")


def main() -> None:
    root = Path(__file__).resolve().parents[1]
    errors: list[str] = []
    validate_api_contract(root, errors)
    validate_runtime_contract(root, errors)
    validate_gateway_routes(root, errors)
    validate_auth_boundary(root, errors)
    fail(errors)
    print("core alpha contract valid")


if __name__ == "__main__":
    main()
