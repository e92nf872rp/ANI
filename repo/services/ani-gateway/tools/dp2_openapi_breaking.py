#!/usr/bin/env python3
"""Generate an exact Direct P2 OpenAPI compatibility and semantic diff."""

from __future__ import annotations

import argparse
import hashlib
import json
import subprocess
from pathlib import Path
from typing import Any

import yaml


ROOT = Path(__file__).resolve().parents[3]
SOURCE_COMMIT = "0cedae825a489d936cf41815dc27f278f6d3213c"
SOURCE_TREE = "552e50bd5bdd49b6abb168b1ef99bfb74dbd1df8"
SOURCE_PATH = "repo/api/openapi/v1.yaml"
TARGET_PATH = ROOT / "api/openapi/v1.yaml"
OUTPUT_PATH = ROOT / "api/openapi/openapi-breaking.v1.json"
SCHEMA_VERSION = "ani.openapi-breaking/v1"
HTTP_METHODS = {"get", "post", "put", "patch", "delete", "head", "options"}
DOCUMENTATION_KEYS = {"description", "summary", "title", "example", "examples", "externalDocs", "tags"}
SEMANTIC_OPERATION_KEYS = (
    "deprecated",
    "x-ani-owner",
    "x-ani-exposure",
    "x-ani-auth-classification",
    "x-ani-authn",
    "x-ani-authz",
    "x-ani-rbac-scope",
)


class DiffError(ValueError):
    """The fixed source or target document cannot be compared."""


def parse_yaml(text: str, label: str) -> dict[str, Any]:
    value = yaml.safe_load(text)
    if not isinstance(value, dict):
        raise DiffError(f"{label} must contain a YAML object")
    return value


def git_object_text(commit: str, path: str) -> str:
    try:
        return subprocess.run(
            ["git", "-C", str(ROOT.parent), "show", f"{commit}:{path}"],
            check=True,
            stdout=subprocess.PIPE,
            stderr=subprocess.PIPE,
            text=True,
        ).stdout
    except subprocess.CalledProcessError as error:
        detail = error.stderr.strip() or str(error)
        raise DiffError(f"cannot read fixed Git object {commit}:{path}: {detail}") from error


def verify_source_identity(commit: str, expected_tree: str) -> None:
    try:
        actual_tree = subprocess.run(
            ["git", "-C", str(ROOT.parent), "rev-parse", f"{commit}^{{tree}}"],
            check=True,
            stdout=subprocess.PIPE,
            stderr=subprocess.PIPE,
            text=True,
        ).stdout.strip()
    except subprocess.CalledProcessError as error:
        raise DiffError(f"cannot resolve fixed source commit {commit}") from error
    if actual_tree != expected_tree:
        raise DiffError(f"fixed source tree is {actual_tree}, want {expected_tree}")


def sha256(text: str) -> str:
    return hashlib.sha256(text.encode("utf-8")).hexdigest()


def structural(value: Any) -> Any:
    if isinstance(value, dict):
        return {
            key: structural(item)
            for key, item in sorted(value.items())
            if key not in DOCUMENTATION_KEYS
        }
    if isinstance(value, list):
        normalized = [structural(item) for item in value]
        if all(isinstance(item, (str, int, float, bool, type(None))) for item in normalized):
            return sorted(normalized, key=lambda item: json.dumps(item, sort_keys=True))
        return normalized
    return value


def collect_operations(spec: dict[str, Any]) -> dict[tuple[str, str], tuple[dict[str, Any], dict[str, Any]]]:
    paths = spec.get("paths")
    if not isinstance(paths, dict):
        raise DiffError("OpenAPI paths must be an object")
    result: dict[tuple[str, str], tuple[dict[str, Any], dict[str, Any]]] = {}
    for path, path_item in sorted(paths.items()):
        if not isinstance(path_item, dict):
            continue
        for method, operation in sorted(path_item.items()):
            if method not in HTTP_METHODS:
                continue
            if not isinstance(operation, dict):
                raise DiffError(f"{method.upper()} {path} must be an object")
            result[(method.upper(), path)] = (path_item, operation)
    return result


def resolve_parameter(spec: dict[str, Any], parameter: Any) -> dict[str, Any]:
    if not isinstance(parameter, dict):
        raise DiffError("operation contains a non-object parameter")
    ref = parameter.get("$ref")
    if ref is None:
        return parameter
    prefix = "#/components/parameters/"
    if not isinstance(ref, str) or not ref.startswith(prefix):
        return parameter
    resolved = spec.get("components", {}).get("parameters", {}).get(ref.removeprefix(prefix))
    if not isinstance(resolved, dict):
        raise DiffError(f"missing local parameter reference {ref}")
    return resolved


def parameter_contract(spec: dict[str, Any], path_item: dict[str, Any], operation: dict[str, Any]) -> list[dict[str, Any]]:
    raw = [*path_item.get("parameters", []), *operation.get("parameters", [])]
    rows = [structural(resolve_parameter(spec, item)) for item in raw]
    return sorted(rows, key=lambda row: (str(row.get("in")), str(row.get("name")), json.dumps(row, sort_keys=True)))


def operation_id(operation: dict[str, Any]) -> str | None:
    value = operation.get("operationId")
    return value if isinstance(value, str) and value else None


def operation_record(method: str, path: str, operation: dict[str, Any]) -> dict[str, Any]:
    return {"method": method, "path": path, "operation_id": operation_id(operation)}


def deep_diff(before: Any, after: Any, pointer: str = "") -> list[dict[str, Any]]:
    if before == after:
        return []
    if isinstance(before, dict) and isinstance(after, dict):
        changes: list[dict[str, Any]] = []
        for key in sorted(set(before) | set(after)):
            child = f"{pointer}/{key.replace('~', '~0').replace('/', '~1')}"
            if key not in before:
                changes.append({"pointer": child, "kind": "added", "after": after[key]})
            elif key not in after:
                changes.append({"pointer": child, "kind": "removed", "before": before[key]})
            else:
                changes.extend(deep_diff(before[key], after[key], child))
        return changes
    return [{"pointer": pointer or "/", "kind": "changed", "before": before, "after": after}]


def changed_operation_row(
    method: str,
    path: str,
    before_operation: dict[str, Any],
    after_operation: dict[str, Any],
    changes: list[dict[str, Any]],
) -> dict[str, Any]:
    return {
        "method": method,
        "path": path,
        "before_operation_id": operation_id(before_operation),
        "after_operation_id": operation_id(after_operation),
        "changes": changes,
    }


def compare_specs(source: dict[str, Any], target: dict[str, Any]) -> dict[str, Any]:
    source_operations = collect_operations(source)
    target_operations = collect_operations(target)
    source_keys = set(source_operations)
    target_keys = set(target_operations)

    removed_operations = [
        operation_record(method, path, source_operations[(method, path)][1])
        for method, path in sorted(source_keys - target_keys)
    ]
    added_operations = [
        operation_record(method, path, target_operations[(method, path)][1])
        for method, path in sorted(target_keys - source_keys)
    ]
    operation_id_changes: list[dict[str, Any]] = []
    security_changes: list[dict[str, Any]] = []
    parameter_changes: list[dict[str, Any]] = []
    request_contract_changes: list[dict[str, Any]] = []
    response_contract_changes: list[dict[str, Any]] = []
    semantic_operation_changes: list[dict[str, Any]] = []

    for method, path in sorted(source_keys & target_keys):
        source_path_item, before = source_operations[(method, path)]
        target_path_item, after = target_operations[(method, path)]
        before_id = operation_id(before)
        after_id = operation_id(after)
        if before_id != after_id:
            operation_id_changes.append(
                {"method": method, "path": path, "before": before_id, "after": after_id}
            )

        before_security = structural(before.get("security", source.get("security")))
        after_security = structural(after.get("security", target.get("security")))
        if before_security != after_security:
            security_changes.append(
                {
                    "method": method,
                    "path": path,
                    "operation_id": after_id or before_id,
                    "before": before_security,
                    "after": after_security,
                }
            )

        before_parameters = parameter_contract(source, source_path_item, before)
        after_parameters = parameter_contract(target, target_path_item, after)
        if changes := deep_diff(before_parameters, after_parameters):
            parameter_changes.append(changed_operation_row(method, path, before, after, changes))

        before_request = structural(before.get("requestBody"))
        after_request = structural(after.get("requestBody"))
        if changes := deep_diff(before_request, after_request):
            request_contract_changes.append(changed_operation_row(method, path, before, after, changes))

        before_responses = structural(before.get("responses"))
        after_responses = structural(after.get("responses"))
        if changes := deep_diff(before_responses, after_responses):
            response_contract_changes.append(changed_operation_row(method, path, before, after, changes))

        before_semantics = structural({key: before[key] for key in SEMANTIC_OPERATION_KEYS if key in before})
        after_semantics = structural({key: after[key] for key in SEMANTIC_OPERATION_KEYS if key in after})
        if changes := deep_diff(before_semantics, after_semantics):
            semantic_operation_changes.append(
                {
                    "method": method,
                    "path": path,
                    "operation_id": after_id or before_id,
                    "changes": changes,
                }
            )

    source_schemas = source.get("components", {}).get("schemas", {})
    target_schemas = target.get("components", {}).get("schemas", {})
    if not isinstance(source_schemas, dict) or not isinstance(target_schemas, dict):
        raise DiffError("components.schemas must be objects")
    removed_schemas = sorted(set(source_schemas) - set(target_schemas))
    added_schemas = sorted(set(target_schemas) - set(source_schemas))
    changed_schemas = []
    for name in sorted(set(source_schemas) & set(target_schemas)):
        changes = deep_diff(structural(source_schemas[name]), structural(target_schemas[name]))
        if changes:
            changed_schemas.append({"schema": name, "changes": changes})

    source_security_schemes = source.get("components", {}).get("securitySchemes", {})
    target_security_schemes = target.get("components", {}).get("securitySchemes", {})
    security_scheme_changes = deep_diff(
        structural(source_security_schemes), structural(target_security_schemes), "/components/securitySchemes"
    )
    source_component_contracts = {
        key: value
        for key, value in source.get("components", {}).items()
        if key not in {"schemas", "securitySchemes"}
    }
    target_component_contracts = {
        key: value
        for key, value in target.get("components", {}).items()
        if key not in {"schemas", "securitySchemes"}
    }
    component_contract_changes = deep_diff(
        source_component_contracts,
        target_component_contracts,
        "/components",
    )
    top_level_security_changes = deep_diff(
        structural(source.get("security")), structural(target.get("security")), "/security"
    )
    ignored_top_level = {"paths", "components", "security", *DOCUMENTATION_KEYS}
    source_document_contract = {
        key: value for key, value in source.items() if key not in ignored_top_level
    }
    target_document_contract = {
        key: value for key, value in target.items() if key not in ignored_top_level
    }
    document_contract_changes = deep_diff(
        structural(source_document_contract),
        structural(target_document_contract),
    )

    breaking_changes = sum(
        len(rows)
        for rows in (
            removed_operations,
            operation_id_changes,
            security_changes,
            parameter_changes,
            request_contract_changes,
            response_contract_changes,
            removed_schemas,
            changed_schemas,
            security_scheme_changes,
            component_contract_changes,
            top_level_security_changes,
            document_contract_changes,
        )
    )
    summary = {
        "source_operations": len(source_operations),
        "target_operations": len(target_operations),
        "removed_operations": len(removed_operations),
        "added_operations": len(added_operations),
        "operation_id_changes": len(operation_id_changes),
        "security_changes": len(security_changes),
        "parameter_changes": len(parameter_changes),
        "request_contract_changes": len(request_contract_changes),
        "response_contract_changes": len(response_contract_changes),
        "removed_schemas": len(removed_schemas),
        "added_schemas": len(added_schemas),
        "changed_schemas": len(changed_schemas),
        "semantic_operation_changes": len(semantic_operation_changes),
        "security_scheme_changes": len(security_scheme_changes),
        "component_contract_changes": len(component_contract_changes),
        "top_level_security_changes": len(top_level_security_changes),
        "document_contract_changes": len(document_contract_changes),
        "breaking_changes": breaking_changes,
    }
    return {
        "summary": summary,
        "removed_operations": removed_operations,
        "added_operations": added_operations,
        "operation_id_changes": operation_id_changes,
        "security_changes": security_changes,
        "parameter_changes": parameter_changes,
        "request_contract_changes": request_contract_changes,
        "response_contract_changes": response_contract_changes,
        "removed_schemas": removed_schemas,
        "added_schemas": added_schemas,
        "changed_schemas": changed_schemas,
        "semantic_operation_changes": semantic_operation_changes,
        "security_scheme_changes": security_scheme_changes,
        "component_contract_changes": component_contract_changes,
        "top_level_security_changes": top_level_security_changes,
        "document_contract_changes": document_contract_changes,
    }


def render_report(source_text: str, target_text: str) -> str:
    report = compare_specs(
        parse_yaml(source_text, "fixed source OpenAPI"),
        parse_yaml(target_text, "target OpenAPI"),
    )
    report = {
        "schema_version": SCHEMA_VERSION,
        "source": {
            "commit": SOURCE_COMMIT,
            "tree": SOURCE_TREE,
            "path": SOURCE_PATH,
            "sha256": sha256(source_text),
        },
        "target": {
            "path": str(TARGET_PATH.relative_to(ROOT)),
            "sha256": sha256(target_text),
        },
        **report,
    }
    return json.dumps(report, indent=2, sort_keys=True, ensure_ascii=False) + "\n"


def main() -> int:
    parser = argparse.ArgumentParser()
    parser.add_argument("--output", type=Path, default=OUTPUT_PATH)
    parser.add_argument("--check", action="store_true")
    args = parser.parse_args()
    try:
        verify_source_identity(SOURCE_COMMIT, SOURCE_TREE)
        source_text = git_object_text(SOURCE_COMMIT, SOURCE_PATH)
        target_text = TARGET_PATH.read_text(encoding="utf-8")
        rendered = render_report(source_text, target_text)
        if args.check:
            if not args.output.is_file() or args.output.read_text(encoding="utf-8") != rendered:
                raise DiffError(f"generated OpenAPI breaking report drift: {args.output}")
        else:
            args.output.write_text(rendered, encoding="utf-8")
    except (DiffError, OSError) as error:
        print(f"fail: {error}")
        return 1
    print("pass")
    return 0


if __name__ == "__main__":
    raise SystemExit(main())
