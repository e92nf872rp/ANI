#!/usr/bin/env python3
"""Validate semantic guardrails for the ANI Services OpenAPI contract.

Existing contract violations are warning-only only when they are recorded as
exact operation-level baseline entries. Any new violation, or a stale baseline
entry, blocks the Services PR gate.
"""

from __future__ import annotations

import argparse
from dataclasses import dataclass
from pathlib import Path
from typing import Any, Iterable

import yaml


ROOT = Path(__file__).resolve().parents[1]
SPEC_PATH = ROOT / "api/openapi/services/v1.yaml"
BASELINE_PATH = ROOT / "architecture/services-contract-baseline.yaml"
BASELINE_STATUS = "accepted_baseline"
WRITE_METHODS = {"post", "put", "patch"}
HTTP_METHODS = {"get", "post", "put", "patch", "delete"}
RULES = {
    "write_requires_idempotency",
    "operation_security",
    "async_202_requires_async_task",
}
ASYNC_TASK_REF = "#/components/schemas/AsyncTask"


@dataclass(frozen=True)
class Finding:
    operation_id: str
    rule: str
    detail: str

    @property
    def key(self) -> tuple[str, str]:
        return self.operation_id, self.rule


@dataclass(frozen=True)
class BaselineEntry:
    operation_id: str
    rule: str
    status: str
    owner: str
    reason: str

    @property
    def key(self) -> tuple[str, str]:
        return self.operation_id, self.rule


@dataclass(frozen=True)
class Result:
    warnings: tuple[str, ...]
    errors: tuple[str, ...]


def load_yaml(path: Path) -> dict[str, Any]:
    with path.open(encoding="utf-8") as handle:
        value = yaml.safe_load(handle) or {}
    if not isinstance(value, dict):
        raise ValueError(f"{path} must contain a YAML object")
    return value


def operations(spec: dict[str, Any]) -> Iterable[tuple[str, str, dict[str, Any]]]:
    for path, path_item in sorted((spec.get("paths") or {}).items()):
        if not isinstance(path_item, dict):
            continue
        for method, operation in sorted(path_item.items()):
            if method in HTTP_METHODS and isinstance(operation, dict):
                yield path, method, operation


def operation_id(path: str, method: str, operation: dict[str, Any]) -> str:
    value = operation.get("operationId")
    if not isinstance(value, str) or not value.strip():
        raise ValueError(f"{method.upper()} {path} must declare operationId")
    return value.strip()


def resolve_schema(schema: Any, schemas: dict[str, Any]) -> dict[str, Any]:
    if not isinstance(schema, dict):
        return {}
    ref = schema.get("$ref")
    if isinstance(ref, str) and ref.startswith("#/components/schemas/"):
        target = schemas.get(ref.rsplit("/", 1)[-1])
        return target if isinstance(target, dict) else {}
    return schema


def required_fields(schema: Any, schemas: dict[str, Any]) -> set[str]:
    if not isinstance(schema, dict):
        return set()
    fields = set(schema.get("required", []))
    for branch in schema.get("allOf", []):
        fields.update(required_fields(branch, schemas))
    if "$ref" in schema:
        fields.update(required_fields(resolve_schema(schema, schemas), schemas))
    return fields


def request_has_idempotency(operation: dict[str, Any], schemas: dict[str, Any]) -> bool:
    request_body = operation.get("requestBody")
    if not isinstance(request_body, dict) or not request_body.get("required"):
        return False
    content = request_body.get("content")
    if not isinstance(content, dict) or not content:
        return False
    for media in content.values():
        if not isinstance(media, dict):
            return False
        schema = resolve_schema(media.get("schema"), schemas)
        if "idempotency_key" not in required_fields(schema, schemas):
            return False
    return True


def operation_has_security(spec: dict[str, Any], operation: dict[str, Any]) -> bool:
    security = operation.get("security", spec.get("security"))
    return isinstance(security, list) and bool(security)


def async_response_has_task(operation: dict[str, Any]) -> bool:
    response = operation.get("responses", {}).get("202")
    if not isinstance(response, dict):
        return True
    content = response.get("content")
    if not isinstance(content, dict) or not content:
        return False
    for media in content.values():
        schema = media.get("schema") if isinstance(media, dict) else None
        if not isinstance(schema, dict) or schema.get("$ref") != ASYNC_TASK_REF:
            return False
    return True


def collect_findings(spec: dict[str, Any]) -> list[Finding]:
    findings: list[Finding] = []
    schemas = spec.get("components", {}).get("schemas", {})
    if not isinstance(schemas, dict):
        schemas = {}
    for path, method, operation in operations(spec):
        op_id = operation_id(path, method, operation)
        if method in WRITE_METHODS and not request_has_idempotency(operation, schemas):
            findings.append(Finding(op_id, "write_requires_idempotency", f"{method.upper()} {path} request body must require idempotency_key"))
        if not operation_has_security(spec, operation):
            findings.append(Finding(op_id, "operation_security", f"{method.upper()} {path} must declare a non-empty security requirement"))
        if "202" in operation.get("responses", {}) and not async_response_has_task(operation):
            findings.append(Finding(op_id, "async_202_requires_async_task", f"{method.upper()} {path} 202 response must use {ASYNC_TASK_REF}"))
    return sorted(findings, key=lambda item: item.key)


def load_baseline(path: Path) -> dict[tuple[str, str], BaselineEntry]:
    document = load_yaml(path)
    if document.get("version") != 1:
        raise ValueError(f"{path}: version must be 1")
    raw_entries = document.get("exceptions")
    if not isinstance(raw_entries, list):
        raise ValueError(f"{path}: exceptions must be a list")
    entries: dict[tuple[str, str], BaselineEntry] = {}
    for raw in raw_entries:
        if not isinstance(raw, dict):
            raise ValueError(f"{path}: baseline entries must be mappings")
        op_id = str(raw.get("operation_id", "")).strip()
        rule = str(raw.get("rule", "")).strip()
        status = str(raw.get("status", document.get("status", ""))).strip()
        owner = str(raw.get("owner", document.get("owner", ""))).strip()
        reason = str(raw.get("reason", "")).strip()
        if not op_id or not rule or rule not in RULES or not status or not owner or not reason:
            raise ValueError(f"{path}: each baseline entry requires operation_id, supported rule, status, owner, reason")
        if status != BASELINE_STATUS:
            raise ValueError(f"{op_id}/{rule}: status must be {BASELINE_STATUS}")
        entry = BaselineEntry(op_id, rule, status, owner, reason)
        if entry.key in entries:
            raise ValueError(f"{op_id}/{rule}: duplicate baseline entry")
        entries[entry.key] = entry
    return entries


def validate(spec: dict[str, Any], baseline: dict[tuple[str, str], BaselineEntry]) -> Result:
    errors: list[str] = []
    warnings: list[str] = []
    server_url = ((spec.get("servers") or [{}])[0] or {}).get("url")
    if server_url != "https://{host}/api/v1/svc":
        errors.append("Services server URL must be https://{host}/api/v1/svc")
    schemes = spec.get("components", {}).get("securitySchemes", {})
    if not isinstance(schemes, dict) or not {"BearerAuth", "ApiKeyAuth"}.issubset(schemes):
        errors.append("Services contract must retain BearerAuth and ApiKeyAuth security schemes")

    findings = collect_findings(spec)
    finding_by_key = {finding.key: finding for finding in findings}
    for key, finding in finding_by_key.items():
        if key in baseline:
            warnings.append(f"accepted baseline warning: {finding.operation_id}/{finding.rule}: {baseline[key].reason}")
        else:
            errors.append(f"new Services contract violation: {finding.operation_id}/{finding.rule}: {finding.detail}")
    operation_ids = {operation_id(path, method, operation) for path, method, operation in operations(spec)}
    for key, entry in baseline.items():
        if key not in finding_by_key:
            if entry.operation_id not in operation_ids:
                errors.append(f"stale Services contract baseline: operation {entry.operation_id} no longer exists")
            else:
                errors.append(f"stale Services contract baseline: {entry.operation_id}/{entry.rule} no longer matches a violation")
    return Result(tuple(warnings), tuple(errors))


def validate_files(spec_path: Path = SPEC_PATH, baseline_path: Path = BASELINE_PATH) -> Result:
    return validate(load_yaml(spec_path), load_baseline(baseline_path))


def main() -> int:
    parser = argparse.ArgumentParser()
    parser.add_argument("--spec", type=Path, default=SPEC_PATH)
    parser.add_argument("--baseline", type=Path, default=BASELINE_PATH)
    args = parser.parse_args()
    try:
        result = validate_files(args.spec, args.baseline)
    except (OSError, ValueError, yaml.YAMLError) as exc:
        print(f"services contract invalid: {exc}")
        return 1
    for warning in result.warnings:
        print(f"⚠️  {warning}")
    for error in result.errors:
        print(f"❌ {error}")
    if result.errors:
        print(f"services contract blocked: {len(result.errors)} error(s), {len(result.warnings)} accepted baseline warning(s)")
        return 1
    print(f"services contract valid: {len(result.warnings)} accepted baseline warning(s)")
    return 0


if __name__ == "__main__":
    raise SystemExit(main())
