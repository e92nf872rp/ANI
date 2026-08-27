#!/usr/bin/env python3
"""Validate the additive inference-service control-plane migration."""

from __future__ import annotations

import re
from pathlib import Path


ROOT = Path(__file__).resolve().parents[1]
MIGRATION_PATH = ROOT / "deploy/migrations/20260814000100_inference_control_plane.sql"


def normalize(sql: str) -> str:
    return re.sub(r"\s+", " ", sql.lower()).strip()


def validate(sql: str) -> tuple[str, ...]:
    normalized = normalize(sql)
    errors: list[str] = []

    service_columns = (
        "served_model_name",
        "model_display_snapshot",
        "desired_spec",
        "applied_spec",
        "status_reason",
        "status_message",
        "generation",
        "observed_generation",
        "desired_state",
        "runtime_ref",
        "runtime_endpoint",
        "ready_replicas",
        "current_operation_id",
        "legacy_quarantined",
        "deleted_at",
    )
    if "alter table inference_services" not in normalized:
        errors.append("migration must alter inference_services additively")
    for column in service_columns:
        if f"add column if not exists {column}" not in normalized:
            errors.append(f"inference_services missing {column}")

    if "create table if not exists inference_operations" not in normalized:
        errors.append("migration must create inference_operations")
    operation_columns = (
        "operation_scope",
        "idempotency_key",
        "request_hash",
        "target_generation",
        "rollback_generation",
        "before_spec",
        "target_spec",
        "next_attempt_at",
        "lease_owner",
        "lease_until",
        "lease_token",
        "runtime_task_id",
        "result_snapshot",
        "error_code",
        "error_message",
    )
    for column in operation_columns:
        if not re.search(rf"\b{re.escape(column)}\s+", normalized):
            errors.append(f"inference_operations missing {column}")

    if "unique (tenant_id, operation_scope, idempotency_key)" not in normalized:
        errors.append("inference_operations must freeze tenant/scope/idempotency uniqueness")
    if normalized.count("where deleted_at is null") < 2:
        errors.append("inference service active-name indexes must exclude tombstones")
    if "where state in ('pending', 'running')" not in normalized:
        errors.append("inference_operations must enforce one active target generation")
    for marker in (
        "desired_spec = jsonb_build_object",
        "legacy_control_plane_quarantined",
        "legacy_quarantined = true",
        "status in ('downloading', 'decrypting')",
    ):
        if marker not in normalized:
            errors.append(f"legacy inference rows require safe quarantine/backfill marker: {marker}")
    if "foreign key (current_operation_id) references inference_operations(id) deferrable initially deferred" not in normalized:
        errors.append("current operation foreign key must allow atomic service/operation creation")
    if "and conrelid = 'inference_services'::regclass" not in normalized:
        errors.append("current operation foreign key lookup must be scoped to inference_services")

    if "alter table inference_operations force row level security" not in normalized:
        errors.append("inference_operations must force RLS")
    if "create policy inference_operations_tenant_isolation" not in normalized:
        errors.append("inference_operations tenant RLS policy missing")
    for policy in ("inference_services_tenant_access", "inference_operations_tenant_access"):
        if f"create policy {policy}" not in normalized or "as permissive" not in normalized:
            errors.append(f"{policy} must pair permissive access with restrictive tenant isolation")
    if "with check" not in normalized:
        errors.append("inference_operations RLS must define WITH CHECK")
    tenant_setting = "current_setting('app.current_tenant_id', true)"
    if normalized.count(tenant_setting) < 2:
        errors.append("inference_operations RLS must scope USING and WITH CHECK by tenant")

    return tuple(errors)


def main() -> int:
    try:
        sql = MIGRATION_PATH.read_text(encoding="utf-8")
    except OSError as exc:
        print(f"inference control-plane migration invalid: {exc}")
        return 1
    errors = validate(sql)
    for error in errors:
        print(f"ERROR: {error}")
    if errors:
        print(f"inference control-plane migration blocked: {len(errors)} error(s)")
        return 1
    print("inference control-plane migration valid")
    return 0


if __name__ == "__main__":
    raise SystemExit(main())
