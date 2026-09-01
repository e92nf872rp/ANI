#!/usr/bin/env python3
from __future__ import annotations

import sys
from pathlib import Path
from typing import Any

import yaml

REQUIRED_SCHEMAS = {
    "InferenceAccessPolicy",
    "InferenceAccessPolicyScope",
    "InferenceAccessPolicyAccess",
    "InferenceAccessPolicyRateLimits",
    "InferenceAccessPolicyConcurrency",
    "InferenceAccessPolicyBinding",
    "InferenceAccessPolicyEvent",
    "CreateInferenceAccessPolicyRequest",
    "PatchInferenceAccessPolicyRequest",
    "UpdateInferenceServicePoliciesRequest",
    "InferenceAccessPolicyListResponse",
    "InferenceAccessPolicyEventListResponse",
}

REQUIRED_OPERATIONS = {
    ("get", "/inference-policies"): "listInferenceAccessPolicies",
    ("post", "/inference-policies"): "createInferenceAccessPolicy",
    ("get", "/inference-policies/{policy_id}"): "getInferenceAccessPolicy",
    ("patch", "/inference-policies/{policy_id}"): "patchInferenceAccessPolicy",
    ("delete", "/inference-policies/{policy_id}"): "deleteInferenceAccessPolicy",
    ("get", "/inference-services/{service_id}/policies"): "listInferenceServicePolicies",
    ("put", "/inference-services/{service_id}/policies"): "updateInferenceServicePolicies",
    ("get", "/inference-policy-events"): "listInferencePolicyEvents",
}

REQUIRED_SECURITY = [{"BearerAuth": []}, {"ApiKeyAuth": []}]

REQUIRED_AUTHZ_ACTIONS = {
    ("get", "/inference-policies"): "read",
    ("post", "/inference-policies"): "create",
    ("get", "/inference-policies/{policy_id}"): "read",
    ("patch", "/inference-policies/{policy_id}"): "update",
    ("delete", "/inference-policies/{policy_id}"): "delete",
    ("get", "/inference-services/{service_id}/policies"): "read",
    ("put", "/inference-services/{service_id}/policies"): "update",
    ("get", "/inference-policy-events"): "read",
}

MUTATING_OPERATIONS = {
    ("post", "/inference-policies"),
    ("patch", "/inference-policies/{policy_id}"),
    ("delete", "/inference-policies/{policy_id}"),
    ("put", "/inference-services/{service_id}/policies"),
}


def load_spec(path: str) -> dict[str, Any]:
    with Path(path).open("r", encoding="utf-8") as handle:
        return yaml.safe_load(handle)


def _schema_has_idempotency(spec: dict[str, Any], schema_ref: str) -> bool:
    prefix = "#/components/schemas/"
    if not schema_ref.startswith(prefix):
        return False
    name = schema_ref[len(prefix) :]
    schema = spec["components"]["schemas"][name]
    return "idempotency_key" in schema.get("required", []) and "idempotency_key" in schema.get("properties", {})


def validate_mutating_idempotency(spec: dict[str, Any]) -> None:
    for method, path in MUTATING_OPERATIONS:
        operation = spec["paths"][path][method]
        if method == "delete":
            parameters = operation.get("parameters", [])
            if any(
                p.get("name") == "Idempotency-Key" and p.get("in") == "header" and p.get("required")
                for p in parameters
            ):
                continue
        body = operation.get("requestBody", {}).get("content", {}).get("application/json", {})
        schema_ref = body.get("schema", {}).get("$ref", "")
        if not _schema_has_idempotency(spec, schema_ref):
            raise AssertionError(f"{method.upper()} {path} must require idempotency_key")


def _validate_operation_ids(spec: dict[str, Any]) -> None:
    for (method, path), expected_operation_id in REQUIRED_OPERATIONS.items():
        operation = spec["paths"].get(path, {}).get(method)
        if operation is None:
            raise AssertionError(f"missing operation: {method.upper()} {path}")
        actual_operation_id = operation.get("operationId")
        if actual_operation_id != expected_operation_id:
            raise AssertionError(
                f"{method.upper()} {path} operationId = {actual_operation_id!r}, want {expected_operation_id!r}"
            )


def validate_authz_format(spec: dict[str, Any]) -> None:
    for method, path in REQUIRED_OPERATIONS:
        operation = spec["paths"][path][method]
        if operation.get("security") != REQUIRED_SECURITY:
            raise AssertionError(f"{method.upper()} {path} must allow BearerAuth or ApiKeyAuth")
        expected_authz = {
            "version": "v1",
            "resource": "inference_policy",
            "action": REQUIRED_AUTHZ_ACTIONS[(method, path)],
            "boundary": "tenant",
            "principal_kinds": ["user"],
        }
        if operation.get("x-ani-authz") != expected_authz:
            raise AssertionError(f"{method.upper()} {path} must define v1 x-ani-authz metadata")


def validate(spec: dict[str, Any]) -> None:
    schemas = spec["components"]["schemas"]
    missing = REQUIRED_SCHEMAS.difference(schemas)
    if missing:
        raise AssertionError(f"missing schemas: {sorted(missing)}")
    for path in ["/inference-policies", "/inference-policies/{policy_id}", "/inference-services/{service_id}/policies", "/inference-policy-events"]:
        if path not in spec["paths"]:
            raise AssertionError(f"missing path: {path}")
    service_put = spec["paths"]["/inference-services/{service_id}/policies"]["put"]
    if "501" in service_put.get("responses", {}):
        raise AssertionError("service policies must not return 501 after C42 contract")
    if "FEATURE_NOT_AVAILABLE" in service_put.get("description", ""):
        raise AssertionError("service policies description must not claim feature unavailable")
    _validate_operation_ids(spec)
    validate_authz_format(spec)
    validate_mutating_idempotency(spec)


def main() -> int:
    spec = load_spec("api/openapi/services/v1.yaml")
    validate(spec)
    print("inference access policy contract valid")
    return 0


if __name__ == "__main__":
    sys.exit(main())
