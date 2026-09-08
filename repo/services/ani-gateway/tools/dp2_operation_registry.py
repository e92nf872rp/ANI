#!/usr/bin/env python3
"""Generate the Direct P2 public operation registry from frozen contracts.

The OpenAPI operation annotations are the single source for handler, owner,
exposure, authentication, permission and typed-obligation policy.  The sibling
policy manifest defines only shared error, trusted-context and obligation-type
contracts.  The generator validates and expands both inputs into deterministic
JSON and Go artifacts.
"""

from __future__ import annotations

import argparse
import hashlib
import json
import subprocess
import tempfile
from dataclasses import dataclass
from pathlib import Path
from typing import Any

import yaml

from dp2_git import read_git_object_text, verify_git_tree


ROOT = Path(__file__).resolve().parents[3]
DEFAULT_OPENAPI = ROOT / "api/openapi/v1.yaml"
DEFAULT_POLICY = ROOT / "api/openapi/operation-policy.v1.yaml"
DEFAULT_REPLACEMENTS = ROOT / "api/openapi/iam-replacement.v1.yaml"
DEFAULT_JSON = ROOT / "api/openapi/operation-registry.v1.json"
DEFAULT_GO = ROOT / "services/ani-gateway/internal/authz/zz_generated_target_operation_registry.go"

HTTP_METHODS = {"get", "post", "put", "patch", "delete", "head", "options"}
SCHEMA_VERSION = "ani.operation-policy/v1"
REPLACEMENT_SCHEMA_VERSION = "ani.iam-replacement/v1"
AUTH_CLASSIFICATIONS = {"public", "authenticated", "authorized"}
OWNERS = {"gateway", "core-control", "iam"}
SCOPES = {"own", "tenant", "platform"}
PRINCIPAL_KINDS = {"human", "service"}
CREDENTIAL_KINDS = {"access_token", "api_key", "service_token", "refresh_cookie", "oidc_state", "password"}
ERROR_STATUSES = ("401", "403", "409", "429", "503", "504")
MUTATION_METHODS = {"POST", "PUT", "PATCH", "DELETE"}
IAM_DECISIONS = {"public": "none", "authenticated": "validate_principal", "authorized": "check_permission"}
REPLACEMENT_DISPOSITIONS = {"preserve_semantics", "target_replacement", "breaking_drift", "final_delete"}
TRUSTED_CONTEXT_HEADERS = (
    "x-ani-authn-method",
    "x-ani-boundary",
    "x-ani-decision-id",
    "x-ani-grant-id",
    "x-ani-principal-id",
    "x-ani-principal-type",
    "x-ani-session-id",
    "x-ani-tenant-id",
)


class ContractError(ValueError):
    """A fail-closed contract validation error."""


@dataclass(frozen=True)
class Route:
    operation_id: str
    method: str
    path: str


def load_yaml(path: Path) -> dict[str, Any]:
    try:
        value = yaml.safe_load(path.read_text(encoding="utf-8"))
    except OSError as error:
        raise ContractError(f"cannot read {path}: {error}") from error
    if not isinstance(value, dict):
        raise ContractError(f"{path} must contain a YAML object")
    return value


def collect_routes(spec: dict[str, Any]) -> dict[str, Route]:
    paths = spec.get("paths")
    if not isinstance(paths, dict):
        raise ContractError("OpenAPI paths must be an object")
    routes: dict[str, Route] = {}
    route_keys: set[tuple[str, str]] = set()
    for path, path_item in sorted(paths.items()):
        if not isinstance(path_item, dict):
            continue
        for method, operation in sorted(path_item.items()):
            if method not in HTTP_METHODS:
                continue
            if not isinstance(operation, dict):
                raise ContractError(f"{method.upper()} {path} must be an object")
            operation_id = operation.get("operationId")
            if not isinstance(operation_id, str) or not operation_id:
                raise ContractError(f"{method.upper()} {path} missing explicit operationId")
            if operation_id in routes:
                raise ContractError(f"duplicate operationId {operation_id}")
            route_key = (method.upper(), path)
            if route_key in route_keys:
                raise ContractError(f"duplicate route {route_key[0]} {route_key[1]}")
            route_keys.add(route_key)
            routes[operation_id] = Route(operation_id, route_key[0], route_key[1])
    return routes


def related_fixed_routes(document: dict[str, Any], contract: str) -> dict[str, Route]:
    routes = collect_routes_with_derived_ids(document)
    selected: dict[str, Route] = {}
    for operation_id, route in routes.items():
        operation = document["paths"][route.path][route.method.lower()]
        tags = set(operation.get("tags", []))
        if contract == "core-v1":
            related = route.path.startswith("/auth/") or bool(tags & {"PlatformUsers", "TenantUsers"})
        else:
            related = (
                route.path.startswith("/tenant/members")
                or route.path.startswith("/tenant/roles")
                or route.path == "/tenant/sso"
                or route.path.startswith("/platform-admins")
                or route.path.startswith("/tenant-admins")
                or route.path.startswith("/tenants/{tenantId}/admins")
                or route.path == "/tenants/{tenantId}/roles"
            )
        if related:
            selected[operation_id] = route
    return selected


def collect_routes_with_derived_ids(spec: dict[str, Any]) -> dict[str, Route]:
    derived = {("POST", "/auth/refresh"): "refreshToken", ("GET", "/branding"): "getBranding"}
    paths = spec.get("paths")
    if not isinstance(paths, dict):
        raise ContractError("OpenAPI paths must be an object")
    routes: dict[str, Route] = {}
    for path, path_item in sorted(paths.items()):
        if not isinstance(path_item, dict):
            continue
        for method, operation in sorted(path_item.items()):
            if method not in HTTP_METHODS:
                continue
            if not isinstance(operation, dict):
                raise ContractError(f"{method.upper()} {path} must be an object")
            operation_id = operation.get("operationId") or derived.get((method.upper(), path))
            if not isinstance(operation_id, str) or not operation_id:
                raise ContractError(f"{method.upper()} {path} missing operationId")
            if operation_id in routes:
                raise ContractError(f"duplicate operationId {operation_id}")
            routes[operation_id] = Route(operation_id, method.upper(), path)
    return routes


def validate_replacement_manifest(
    manifest: dict[str, Any], current_routes: dict[str, Route]
) -> dict[str, Any]:
    if set(manifest) != {"schema_version", "source", "deletion_manifest", "groups"}:
        raise ContractError("replacement manifest must define exactly schema_version, source, deletion_manifest and groups")
    if manifest.get("schema_version") != REPLACEMENT_SCHEMA_VERSION:
        raise ContractError(f"replacement schema_version must be {REPLACEMENT_SCHEMA_VERSION}")
    source = manifest.get("source")
    required_source = {"commit", "tree", "core_openapi", "services_openapi"}
    if not isinstance(source, dict) or set(source) != required_source:
        raise ContractError(f"replacement source must define exactly {sorted(required_source)}")
    commit = require_string(source.get("commit"), "replacement source.commit")
    expected_tree = require_string(source.get("tree"), "replacement source.tree")
    verify_git_tree(ROOT.parent, commit, expected_tree, ContractError, "replacement source")

    deletion_manifest = manifest.get("deletion_manifest")
    deletion_fields = {"repository", "commit", "path", "sha256", "items"}
    if not isinstance(deletion_manifest, dict) or set(deletion_manifest) != deletion_fields:
        raise ContractError(f"deletion_manifest must define exactly {sorted(deletion_fields)}")
    if deletion_manifest.get("repository") != "ani-iam":
        raise ContractError("deletion_manifest.repository must be ani-iam")
    deletion_commit = require_string(deletion_manifest.get("commit"), "deletion_manifest.commit")
    deletion_path = require_string(deletion_manifest.get("path"), "deletion_manifest.path")
    deletion_sha256 = require_string(deletion_manifest.get("sha256"), "deletion_manifest.sha256")
    if len(deletion_commit) != 40 or any(character not in "0123456789abcdef" for character in deletion_commit):
        raise ContractError("deletion_manifest.commit must be a full lowercase Git SHA")
    if len(deletion_sha256) != 64 or any(character not in "0123456789abcdef" for character in deletion_sha256):
        raise ContractError("deletion_manifest.sha256 must be a lowercase SHA-256")
    deletion_items = deletion_manifest.get("items")
    expected_deletion_ids = [f"D{number:03d}" for number in range(1, 29)]
    if not isinstance(deletion_items, dict) or list(deletion_items) != expected_deletion_ids:
        raise ContractError("deletion_manifest.items must be the ordered D001-D028 set")
    deletion_group_refs: set[str] = set()
    for deletion_id, row in deletion_items.items():
        if not isinstance(row, dict) or set(row) != {"relation", "groups"}:
            raise ContractError(f"deletion_manifest item {deletion_id} must define relation and groups")
        require_string(row.get("relation"), f"deletion_manifest item {deletion_id}.relation")
        group_refs = row.get("groups")
        if not isinstance(group_refs, list) or any(not isinstance(item, str) or not item for item in group_refs):
            raise ContractError(f"deletion_manifest item {deletion_id}.groups must be a string array")
        if len(group_refs) != len(set(group_refs)):
            raise ContractError(f"deletion_manifest item {deletion_id}.groups contains duplicates")
        deletion_group_refs.update(group_refs)

    fixed_routes: dict[tuple[str, str], Route] = {}
    fixed_hashes: dict[str, str] = {}
    for contract, field in (("core-v1", "core_openapi"), ("services-v1", "services_openapi")):
        path = require_string(source.get(field), f"replacement source.{field}")
        text = read_git_object_text(ROOT.parent, commit, path, ContractError)
        document = yaml.safe_load(text)
        if not isinstance(document, dict):
            raise ContractError(f"fixed {contract} OpenAPI is not an object")
        fixed_hashes[contract] = hashlib.sha256(text.encode("utf-8")).hexdigest()
        for operation_id, route in related_fixed_routes(document, contract).items():
            fixed_routes[(contract, operation_id)] = route

    groups = manifest.get("groups")
    if not isinstance(groups, list) or not groups:
        raise ContractError("replacement groups must be a non-empty array")
    seen_groups: set[str] = set()
    entries: dict[tuple[str, str], dict[str, Any]] = {}
    for index, group in enumerate(groups):
        if not isinstance(group, dict):
            raise ContractError(f"replacement groups[{index}] must be an object")
        allowed_group = {
            "name", "contract", "owner", "disposition", "replacement_gate",
            "zero_reference", "affects", "operations",
        }
        if set(group) != allowed_group:
            raise ContractError(f"replacement group {index} has invalid fields")
        name = require_string(group.get("name"), f"replacement groups[{index}].name")
        if name in seen_groups:
            raise ContractError(f"duplicate replacement group {name}")
        seen_groups.add(name)
        contract = require_string(group.get("contract"), f"replacement group {name}.contract")
        if contract not in {"core-v1", "services-v1"}:
            raise ContractError(f"replacement group {name} has invalid contract {contract}")
        owner = require_string(group.get("owner"), f"replacement group {name}.owner")
        if owner not in OWNERS:
            raise ContractError(f"replacement group {name} has invalid owner {owner}")
        group_disposition = require_string(group.get("disposition"), f"replacement group {name}.disposition")
        if group_disposition not in REPLACEMENT_DISPOSITIONS:
            raise ContractError(f"replacement group {name} has invalid disposition {group_disposition}")
        gate = require_string(group.get("replacement_gate"), f"replacement group {name}.replacement_gate")
        zero_reference = require_string(group.get("zero_reference"), f"replacement group {name}.zero_reference")
        affects = require_string_list(group.get("affects"), f"replacement group {name}.affects")
        if "DP2-02" not in affects:
            raise ContractError(f"replacement group {name} must affect DP2-02")
        operations = group.get("operations")
        if not isinstance(operations, dict) or not operations:
            raise ContractError(f"replacement group {name}.operations must be a non-empty object")
        for operation_id, row in operations.items():
            if not isinstance(row, dict):
                raise ContractError(f"replacement operation {operation_id} must be an object")
            allowed_row = {"method", "path", "replacements", "disposition"}
            if set(row) - allowed_row or not {"method", "path", "replacements"}.issubset(row):
                raise ContractError(f"replacement operation {operation_id} has invalid fields")
            key = (contract, operation_id)
            if key in entries:
                raise ContractError(f"duplicate replacement operation {contract}:{operation_id}")
            method = require_string(row.get("method"), f"replacement {operation_id}.method")
            path = require_string(row.get("path"), f"replacement {operation_id}.path")
            replacements = row.get("replacements")
            if not isinstance(replacements, list) or any(not isinstance(item, str) or not item for item in replacements):
                raise ContractError(f"replacement {operation_id}.replacements must be a string array")
            disposition = row.get("disposition", group_disposition)
            if disposition not in REPLACEMENT_DISPOSITIONS:
                raise ContractError(f"replacement {operation_id} has invalid disposition {disposition}")
            if disposition == "final_delete" and replacements:
                raise ContractError(f"final-delete operation {operation_id} cannot name replacements")
            if disposition != "final_delete" and not replacements:
                raise ContractError(f"replacement operation {operation_id} must name a target operation")
            for replacement in replacements:
                if replacement not in current_routes:
                    raise ContractError(f"replacement operation {operation_id} names unknown target {replacement}")
            entries[key] = {
                "method": method,
                "path": path,
                "owner": owner,
                "disposition": disposition,
                "replacements": replacements,
                "replacement_gate": gate,
                "zero_reference": zero_reference,
                "affects": affects,
            }

    unknown_deletion_groups = sorted(deletion_group_refs - seen_groups)
    if unknown_deletion_groups:
        raise ContractError(f"deletion_manifest references unknown replacement groups {unknown_deletion_groups}")

    missing = sorted(set(fixed_routes) - set(entries))
    extra = sorted(set(entries) - set(fixed_routes))
    if missing or extra:
        raise ContractError(f"replacement completeness failed: missing={missing}, extra={extra}")
    for key, fixed in fixed_routes.items():
        row = entries[key]
        if (row["method"], row["path"]) != (fixed.method, fixed.path):
            raise ContractError(
                f"replacement source mismatch for {key}: {(row['method'], row['path'])} != {(fixed.method, fixed.path)}"
            )
    counts = {contract: sum(key[0] == contract for key in entries) for contract in ("core-v1", "services-v1")}
    if counts != {"core-v1": 28, "services-v1": 31}:
        raise ContractError(f"replacement counts are {counts}, want core-v1=28 services-v1=31")
    return {
        "schema_version": REPLACEMENT_SCHEMA_VERSION,
        "source_commit": commit,
        "source_tree": expected_tree,
        "source_openapi_sha256": fixed_hashes,
        "deletion_manifest": {
            "repository": "ani-iam",
            "commit": deletion_commit,
            "path": deletion_path,
            "sha256": deletion_sha256,
        },
        "deletion_ids": expected_deletion_ids,
        "manifest_sha256": hashlib.sha256(
            json.dumps(manifest, sort_keys=True, separators=(",", ":")).encode("utf-8")
        ).hexdigest(),
        "operation_counts": counts,
    }


def require_string(value: Any, field: str) -> str:
    if not isinstance(value, str) or not value.strip():
        raise ContractError(f"{field} must be a non-empty string")
    return value


def require_string_list(value: Any, field: str, allowed: set[str] | None = None) -> list[str]:
    if not isinstance(value, list) or not value or any(not isinstance(item, str) or not item for item in value):
        raise ContractError(f"{field} must be a non-empty string array")
    if len(value) != len(set(value)):
        raise ContractError(f"{field} contains duplicates")
    if allowed is not None and (unknown := set(value) - allowed):
        raise ContractError(f"{field} contains unsupported values: {sorted(unknown)}")
    return list(value)


def validate_error_contract(policy: dict[str, Any], spec: dict[str, Any]) -> dict[str, Any]:
    contract = policy.get("error_contract")
    if not isinstance(contract, dict) or set(contract) != set(ERROR_STATUSES):
        raise ContractError(f"error_contract must define exactly {list(ERROR_STATUSES)}")
    responses = spec.get("components", {}).get("responses", {})
    schemas = spec.get("components", {}).get("schemas", {})
    error_schema = schemas.get("ErrorResponse")
    if not isinstance(error_schema, dict) or set(error_schema.get("required", [])) != {"code", "message", "request_id"}:
        raise ContractError("ErrorResponse must require code, message and request_id")
    for status in ERROR_STATUSES:
        item = contract[status]
        if not isinstance(item, dict) or set(item) != {"response", "default_code", "reason_codes"}:
            raise ContractError(f"error_contract.{status} must be an object")
        response_name = require_string(item.get("response"), f"error_contract.{status}.response")
        default_code = require_string(item.get("default_code"), f"error_contract.{status}.default_code")
        reason_codes = require_string_list(item.get("reason_codes"), f"error_contract.{status}.reason_codes")
        if reason_codes != sorted(reason_codes):
            raise ContractError(f"error_contract.{status}.reason_codes must be sorted")
        if default_code not in reason_codes:
            raise ContractError(f"error_contract.{status}.default_code must be present in reason_codes")
        response = responses.get(response_name)
        ref = response.get("content", {}).get("application/json", {}).get("schema", {}).get("$ref") if isinstance(response, dict) else None
        if ref != "#/components/schemas/ErrorResponse":
            raise ContractError(f"error_contract.{status} response {response_name} must use ErrorResponse")
        openapi_reason_codes = response.get("x-ani-error-codes") if isinstance(response, dict) else None
        if not isinstance(openapi_reason_codes, list) or sorted(openapi_reason_codes) != reason_codes:
            raise ContractError(
                f"error_contract.{status}.reason_codes must match {response_name}.x-ani-error-codes"
            )
        for code in reason_codes:
            if not code.replace("_", "").isalnum() or code.upper() != code:
                raise ContractError(f"error_contract.{status} reason {code!r} must be an uppercase stable code")
    return contract


def validate_trusted_context(policy: dict[str, Any]) -> dict[str, Any]:
    contract = policy.get("trusted_context")
    if not isinstance(contract, dict) or set(contract) != {"strip_client_prefix", "inject_headers"}:
        raise ContractError("trusted_context must define exactly strip_client_prefix and inject_headers")
    if contract.get("strip_client_prefix") != "x-ani-":
        raise ContractError("trusted_context.strip_client_prefix must be x-ani-")
    headers = require_string_list(contract.get("inject_headers"), "trusted_context.inject_headers")
    if tuple(sorted(headers)) != TRUSTED_CONTEXT_HEADERS:
        raise ContractError(
            f"trusted_context.inject_headers must equal the fixed allowlist {list(TRUSTED_CONTEXT_HEADERS)}"
        )
    if any("role" in header or "permission" in header for header in headers):
        raise ContractError("trusted_context cannot inject role or permission headers")
    return {"strip_client_prefix": "x-ani-", "inject_headers": list(TRUSTED_CONTEXT_HEADERS)}


def resolve_parameter(spec: dict[str, Any], parameter: Any, operation_id: str) -> dict[str, Any]:
    if not isinstance(parameter, dict):
        raise ContractError(f"operation {operation_id} contains a non-object parameter")
    ref = parameter.get("$ref")
    if ref is None:
        return parameter
    prefix = "#/components/parameters/"
    if not isinstance(ref, str) or not ref.startswith(prefix):
        raise ContractError(f"operation {operation_id} has unsupported parameter reference {ref!r}")
    name = ref.removeprefix(prefix)
    resolved = spec.get("components", {}).get("parameters", {}).get(name)
    if not isinstance(resolved, dict):
        raise ContractError(f"operation {operation_id} references missing parameter {ref}")
    return resolved


def validate_operation_surface(
    spec: dict[str, Any],
    path_item: dict[str, Any],
    operation: dict[str, Any],
    route: Route,
    auth_classification: str,
    authn: dict[str, Any] | None,
    error_contract: dict[str, Any],
) -> None:
    security = operation.get("security", spec.get("security"))
    if auth_classification == "public":
        if operation.get("security") != []:
            raise ContractError(
                f"public operation {route.operation_id} must set security to an empty array"
            )
    else:
        if not isinstance(security, list) or not security:
            raise ContractError(f"protected operation {route.operation_id} must define authentication security")
        scheme_names: set[str] = set()
        for requirement in security:
            if not isinstance(requirement, dict) or not requirement:
                raise ContractError(f"operation {route.operation_id} has an invalid security requirement")
            if len(requirement) != 1:
                raise ContractError(
                    f"operation {route.operation_id} has unsupported AND security requirements"
                )
            scheme_names.update(requirement)
        defined_schemes = spec.get("components", {}).get("securitySchemes", {})
        unknown_schemes = sorted(scheme_names - set(defined_schemes))
        if unknown_schemes:
            raise ContractError(
                f"operation {route.operation_id} references unknown security schemes {unknown_schemes}"
            )
        if "ApiKeyAuth" in scheme_names:
            raise ContractError(f"protected operation {route.operation_id} cannot accept ApiKeyAuth")
        credential_kinds = set(authn.get("credential_kinds", [])) if isinstance(authn, dict) else set()
        allowed_schemes: set[str] = set()
        if credential_kinds & {"access_token", "api_key", "service_token"}:
            allowed_schemes.add("BearerAuth")
            if "BearerAuth" not in scheme_names:
                raise ContractError(f"operation {route.operation_id} is missing BearerAuth")
        if "refresh_cookie" in credential_kinds:
            cookie_schemes = {"ConsoleRefreshCookie", "BossRefreshCookie"}
            allowed_schemes.update(cookie_schemes)
            if not scheme_names & cookie_schemes:
                raise ContractError(f"operation {route.operation_id} is missing a registered Refresh Cookie scheme")
        unsupported_credentials = credential_kinds & {"oidc_state", "password"}
        if unsupported_credentials:
            raise ContractError(
                f"protected operation {route.operation_id} has non-authentication credential kinds {sorted(unsupported_credentials)}"
            )
        if extra_schemes := sorted(scheme_names - allowed_schemes):
            raise ContractError(
                f"operation {route.operation_id} accepts security schemes outside its authn contract: {extra_schemes}"
            )

    parameters = [*path_item.get("parameters", []), *operation.get("parameters", [])]
    resolved_parameters = [resolve_parameter(spec, item, route.operation_id) for item in parameters]
    for parameter in resolved_parameters:
        name = parameter.get("name")
        if parameter.get("in") == "header" and isinstance(name, str) and name.lower().startswith("x-ani-"):
            raise ContractError(
                f"operation {route.operation_id} exposes client-controlled x-ani header {name}"
            )

    # Direct P2-introduced operations have a stronger public error and
    # idempotency contract than retained pre-replacement operations.
    target_operation = operation.get("x-ani-contract") == "direct-p2"
    if not target_operation:
        return
    required_errors = {"503", "504"}
    if auth_classification in {"authenticated", "authorized"}:
        required_errors.add("401")
    if auth_classification == "authorized":
        required_errors.add("403")
    if route.method in MUTATION_METHODS:
        required_errors.update({"409", "429"})
        if not any(
            parameter.get("in") == "header"
            and parameter.get("name") == "Idempotency-Key"
            and parameter.get("required") is True
            for parameter in resolved_parameters
        ):
            raise ContractError(
                f"target mutation {route.operation_id} is missing required Idempotency-Key"
            )

    responses = operation.get("responses")
    if not isinstance(responses, dict):
        raise ContractError(f"target operation {route.operation_id} must define responses")
    missing_errors = sorted(required_errors - set(responses))
    if missing_errors:
        raise ContractError(
            f"target operation {route.operation_id} missing stable responses {missing_errors}"
        )
    for status in sorted(required_errors):
        expected_ref = f"#/components/responses/{error_contract[status]['response']}"
        response = responses.get(status)
        actual_ref = response.get("$ref") if isinstance(response, dict) else None
        if actual_ref != expected_ref:
            raise ContractError(
                f"target operation {route.operation_id} response {status} must reference {expected_ref}"
            )


def expand(spec: dict[str, Any], policy: dict[str, Any]) -> dict[str, Any]:
    expected_top_level = {
        "schema_version",
        "error_contract",
        "trusted_context",
        "obligation_handlers",
    }
    if set(policy) != expected_top_level:
        raise ContractError(
            f"policy top-level fields must equal {sorted(expected_top_level)}"
        )
    if policy.get("schema_version") != SCHEMA_VERSION:
        raise ContractError(f"schema_version must be {SCHEMA_VERSION}")
    routes = collect_routes(spec)
    error_contract = validate_error_contract(policy, spec)
    trusted_context = validate_trusted_context(policy)

    handler_rows = policy.get("obligation_handlers")
    if not isinstance(handler_rows, list):
        raise ContractError("obligation_handlers must be an array")
    handlers: dict[str, str] = {}
    for index, row in enumerate(handler_rows):
        if not isinstance(row, dict):
            raise ContractError(f"obligation_handlers[{index}] must be an object")
        name = require_string(row.get("name"), f"obligation_handlers[{index}].name")
        owner = require_string(row.get("owner"), f"obligation_handlers[{index}].owner")
        if owner not in OWNERS:
            raise ContractError(f"obligation handler {name} has unsupported owner {owner}")
        if name in handlers:
            raise ContractError(f"duplicate obligation handler {name}")
        handlers[name] = owner

    expanded: list[dict[str, Any]] = []
    gateway_handlers: set[str] = set()
    for operation_id, route in sorted(routes.items()):
        operation = spec["paths"][route.path][route.method.lower()]
        if "x-ani-handler" not in operation:
            raise ContractError(f"operation {operation_id} missing x-ani-handler")
        if "x-ani-owner" not in operation:
            raise ContractError(f"operation {operation_id} missing x-ani-owner")
        if "x-ani-auth-classification" not in operation:
            raise ContractError(f"operation {operation_id} missing x-ani-auth-classification")
        gateway_handler = require_string(operation.get("x-ani-handler"), f"operation {operation_id}.x-ani-handler")
        owner = require_string(operation.get("x-ani-owner"), f"operation {operation_id}.x-ani-owner")
        auth_classification = require_string(
            operation.get("x-ani-auth-classification"),
            f"operation {operation_id}.x-ani-auth-classification",
        )
        if not gateway_handler.startswith("gateway."):
            raise ContractError(f"operation {operation_id} has invalid Gateway handler {gateway_handler}")
        if gateway_handler in gateway_handlers:
            raise ContractError(f"duplicate Gateway handler {gateway_handler}")
        gateway_handlers.add(gateway_handler)
        if auth_classification not in AUTH_CLASSIFICATIONS:
            raise ContractError(
                f"operation {operation_id} has unsupported auth classification {auth_classification}"
            )
        if owner not in OWNERS:
            raise ContractError(f"operation {operation_id} has unsupported owner {owner}")
        contract_version = operation.get("x-ani-contract")
        if contract_version is not None and contract_version != "direct-p2":
            raise ContractError(f"operation {operation_id} has unsupported x-ani-contract {contract_version}")

        authn = operation.get("x-ani-authn")
        permission: dict[str, Any] | None = None
        obligations: list[dict[str, Any]] = []
        if auth_classification == "public":
            if authn is not None or "x-ani-authz" in operation:
                raise ContractError(f"public operation {operation_id} cannot define x-ani-authn or x-ani-authz")
            decision_calls = 0
        else:
            if not isinstance(authn, dict):
                raise ContractError(f"protected operation {operation_id} missing x-ani-authn")
            if set(authn) != {"principal_kinds", "credential_kinds"}:
                raise ContractError(f"operation {operation_id}.x-ani-authn has invalid fields")
            require_string_list(authn.get("principal_kinds"), f"operation {operation_id}.x-ani-authn.principal_kinds", PRINCIPAL_KINDS)
            require_string_list(authn.get("credential_kinds"), f"operation {operation_id}.x-ani-authn.credential_kinds", CREDENTIAL_KINDS)
            decision_calls = 1
            if auth_classification == "authenticated":
                if "x-ani-authz" in operation:
                    raise ContractError(f"authenticated operation {operation_id} cannot define x-ani-authz")
            else:
                annotation = operation.get("x-ani-authz")
                if not isinstance(annotation, dict):
                    raise ContractError(f"authorized operation {operation_id} missing x-ani-authz")
                expected_fields = {"version", "resource", "actions", "scope", "obligations"}
                if set(annotation) != expected_fields:
                    raise ContractError(f"operation {operation_id}.x-ani-authz has invalid fields")
                if annotation.get("version") != "v1":
                    raise ContractError(f"operation {operation_id}.x-ani-authz.version must be v1")
                resource = require_string(annotation.get("resource"), f"operation {operation_id}.x-ani-authz.resource")
                actions = require_string_list(annotation.get("actions"), f"operation {operation_id}.x-ani-authz.actions")
                scope = require_string(annotation.get("scope"), f"operation {operation_id}.x-ani-authz.scope")
                if scope not in SCOPES:
                    raise ContractError(f"operation {operation_id} has unsupported scope {scope}")
                obligations = annotation.get("obligations")
                if not isinstance(obligations, list):
                    raise ContractError(f"operation {operation_id}.x-ani-authz.obligations must be an array")
                normalized_obligations: list[dict[str, Any]] = []
                for obligation in obligations:
                    if not isinstance(obligation, dict):
                        raise ContractError(f"operation {operation_id} obligation must be an object")
                    if set(obligation) != {"type", "handler"}:
                        raise ContractError(f"operation {operation_id} obligation has invalid fields")
                    obligation_type = require_string(obligation.get("type"), f"operation {operation_id} obligation.type")
                    handler = require_string(obligation.get("handler"), f"operation {operation_id} obligation.handler")
                    if handler not in handlers:
                        raise ContractError(f"operation {operation_id} references unknown obligation handler {handler}")
                    if handlers[handler] != owner:
                        raise ContractError(
                            f"operation {operation_id} obligation handler {handler} is owned by {handlers[handler]}, want {owner}"
                        )
                    normalized_obligations.append({"type": obligation_type, "handler": handler})
                obligations = normalized_obligations
                permission = {"resource": resource, "actions": actions, "scope": scope}

        validate_operation_surface(
            spec,
            spec["paths"][route.path],
            operation,
            route,
            auth_classification,
            authn,
            error_contract,
        )
        item: dict[str, Any] = {
            "operation_id": operation_id,
            "method": route.method,
            "path": route.path,
            "gateway_handler": gateway_handler,
            "backend_owner": owner,
            "auth_classification": auth_classification,
            "iam_decision": IAM_DECISIONS[auth_classification],
            "iam_decision_calls": decision_calls,
        }
        if authn is not None:
            item["authn"] = authn
        if permission is not None:
            item["permission"] = permission
            item["obligations"] = obligations
        expanded.append(item)

    canonical = {
        "schema_version": SCHEMA_VERSION,
        "error_contract": error_contract,
        "trusted_context": trusted_context,
        "obligation_handlers": [{"name": name, "owner": handlers[name]} for name in sorted(handlers)],
        "operations": expanded,
    }
    revision = "sha256:" + hashlib.sha256(
        json.dumps(canonical, sort_keys=True, separators=(",", ":")).encode("utf-8")
    ).hexdigest()
    return {"policy_revision": revision, **canonical}


def render_json(registry: dict[str, Any]) -> str:
    return json.dumps(registry, indent=2, sort_keys=True, ensure_ascii=False) + "\n"


def go_quote(value: str) -> str:
    return json.dumps(value, ensure_ascii=False)


def render_go(registry: dict[str, Any]) -> str:
    lines = [
        "// Code generated by services/ani-gateway/tools/dp2_operation_registry.py; DO NOT EDIT.",
        "package authz",
        "",
        f"const TargetPolicyRevision = {go_quote(registry['policy_revision'])}",
        "",
        "var generatedTargetStableErrors = map[int]TargetStableError{",
    ]
    for status, item in sorted(registry["error_contract"].items()):
        lines.append(
            f"\t{status}: {{ResponseComponent: {go_quote(item['response'])}, Code: {go_quote(item['default_code'])}}},"
        )
    lines.extend([
        "}",
        "",
        "var generatedTargetStableErrorReasons = map[string]TargetStableErrorReason{",
    ])
    for status, item in sorted(registry["error_contract"].items()):
        for reason in item["reason_codes"]:
            lines.append(
                f"\t{go_quote(reason)}: {{HTTPStatus: {status}, ResponseComponent: {go_quote(item['response'])}, Code: {go_quote(reason)}}},"
            )
    lines.extend([
        "}",
        "",
        "var generatedTargetObligationHandlers = map[TargetObligationHandler]TargetBackendOwner{",
    ])
    for item in registry["obligation_handlers"]:
        lines.append(f"\tTargetObligationHandler({go_quote(item['name'])}): TargetBackendOwner({go_quote(item['owner'])}),")
    permission_resources = sorted({item["permission"]["resource"] for item in registry["operations"] if "permission" in item})
    permission_actions = sorted({action for item in registry["operations"] for action in item.get("permission", {}).get("actions", [])})
    obligation_types = sorted({obligation["type"] for item in registry["operations"] for obligation in item.get("obligations", [])})
    lines.extend([
        "}",
        "",
        "var generatedTargetPermissionResources = map[TargetPermissionResource]struct{}{",
    ])
    for resource in permission_resources:
        lines.append(f"\tTargetPermissionResource({go_quote(resource)}): {{}},")
    lines.extend([
        "}",
        "",
        "var generatedTargetPermissionActions = map[TargetPermissionAction]struct{}{",
    ])
    for action in permission_actions:
        lines.append(f"\tTargetPermissionAction({go_quote(action)}): {{}},")
    lines.extend([
        "}",
        "",
        "var generatedTargetObligationTypes = map[TargetObligationType]struct{}{",
    ])
    for obligation_type in obligation_types:
        lines.append(f"\tTargetObligationType({go_quote(obligation_type)}): {{}},")
    lines.extend([
        "}",
        "",
        "var generatedTargetTrustedContextHeaders = map[string]struct{}{",
    ])
    for header in registry["trusted_context"]["inject_headers"]:
        lines.append(f"\t{go_quote(header)}: {{}},")
    lines.extend([
        "}",
        "",
        "var generatedTargetOperationPolicies = map[string]TargetOperationPolicy{",
    ])
    for item in registry["operations"]:
        key = f"{item['method']} /api/v1{item['path']}" if item["path"] not in {"/healthz", "/readyz"} else f"{item['method']} {item['path']}"
        authn = item.get("authn", {})
        permission = item.get("permission", {})
        obligations = item.get("obligations", [])
        lines.extend([
            f"\t{go_quote(key)}: {{",
            f"\t\tOperationID: {go_quote(item['operation_id'])},",
            f"\t\tMethod: {go_quote(item['method'])},",
            f"\t\tPathTemplate: {go_quote(key.split(' ', 1)[1])},",
            f"\t\tGatewayHandler: {go_quote(item['gateway_handler'])},",
            f"\t\tBackendOwner: TargetBackendOwner({go_quote(item['backend_owner'])}),",
            f"\t\tAuthClassification: TargetAuthClassification({go_quote(item['auth_classification'])}),",
            f"\t\tIAMDecision: TargetIAMDecision({go_quote(item['iam_decision'])}),",
            f"\t\tIAMDecisionCalls: {item['iam_decision_calls']},",
        ])
        if authn:
            lines.append("\t\tPrincipalKinds: []TargetPrincipalKind{" + ", ".join(f"TargetPrincipalKind({go_quote(v)})" for v in authn["principal_kinds"]) + "},")
            lines.append("\t\tCredentialKinds: []TargetCredentialKind{" + ", ".join(f"TargetCredentialKind({go_quote(v)})" for v in authn["credential_kinds"]) + "},")
        if permission:
            lines.append(f"\t\tPermissionResource: TargetPermissionResource({go_quote(permission['resource'])}),")
            lines.append("\t\tPermissionActions: []TargetPermissionAction{" + ", ".join(f"TargetPermissionAction({go_quote(v)})" for v in permission["actions"]) + "},")
            lines.append(f"\t\tPermissionScope: TargetPermissionScope({go_quote(permission['scope'])}),")
            if obligations:
                rendered = ", ".join("{Type: TargetObligationType(" + go_quote(v["type"]) + "), Handler: TargetObligationHandler(" + go_quote(v["handler"]) + ")}" for v in obligations)
                lines.append(f"\t\tObligations: []TargetObligation{{{rendered}}},")
        lines.append("\t},")
    lines.extend(["}", ""])
    return "\n".join(lines)


def generated_outputs(openapi: Path, policy: Path, replacements: Path) -> tuple[str, str]:
    registry = expand(load_yaml(openapi), load_yaml(policy))
    registry["iam_replacement"] = validate_replacement_manifest(load_yaml(replacements), collect_routes(load_yaml(openapi)))
    return render_json(registry), render_go(registry)


def check_output(path: Path, expected: str) -> None:
    if not path.is_file() or path.read_text(encoding="utf-8") != expected:
        raise ContractError(f"generated artifact drift: {path}")


def main() -> int:
    parser = argparse.ArgumentParser()
    parser.add_argument("--openapi", type=Path, default=DEFAULT_OPENAPI)
    parser.add_argument("--policy", type=Path, default=DEFAULT_POLICY)
    parser.add_argument("--replacements", type=Path, default=DEFAULT_REPLACEMENTS)
    parser.add_argument("--json-output", type=Path, default=DEFAULT_JSON)
    parser.add_argument("--go-output", type=Path, default=DEFAULT_GO)
    parser.add_argument("--check", action="store_true")
    args = parser.parse_args()
    try:
        json_text, go_text = generated_outputs(args.openapi, args.policy, args.replacements)
        if args.check:
            check_output(args.json_output, json_text)
            with tempfile.TemporaryDirectory(prefix="ani-dp2-registry-") as directory:
                candidate = Path(directory) / "registry.go"
                candidate.write_text(go_text, encoding="utf-8")
                subprocess.run(["gofmt", "-w", str(candidate)], check=True)
                check_output(args.go_output, candidate.read_text(encoding="utf-8"))
        else:
            args.json_output.write_text(json_text, encoding="utf-8")
            args.go_output.write_text(go_text, encoding="utf-8")
            subprocess.run(["gofmt", "-w", str(args.go_output)], check=True)
    except (ContractError, OSError, subprocess.CalledProcessError) as error:
        print(f"FAIL: {error}")
        return 1
    print("pass")
    return 0


if __name__ == "__main__":
    raise SystemExit(main())
