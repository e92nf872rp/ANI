#!/usr/bin/env python3
from __future__ import annotations

import re
from pathlib import Path

ROOT = Path(__file__).resolve().parents[1]
MIGRATION_PATH = ROOT / "deploy/migrations/20260828_001_inference_access_policy.sql"
FORWARD_MIGRATION_PATH = ROOT / "deploy/migrations/20260831_002_inference_access_policy_key_effects.sql"
IDEMPOTENCY_MIGRATION_PATH = ROOT / "deploy/migrations/20260901_001_inference_access_policy_idempotency.sql"


def validate(sql: str, forward_sql: str, idempotency_sql: str) -> tuple[str, ...]:
    normalized = re.sub(r"\s+", " ", sql.lower()).strip()
    forward = re.sub(r"\s+", " ", forward_sql.lower()).strip()
    idempotency = re.sub(r"\s+", " ", idempotency_sql.lower()).strip()
    errors: list[str] = []
    for table in ("inference_access_policies", "inference_access_policy_services", "inference_access_policy_api_keys", "inference_access_policy_events"):
        if f"create table if not exists {table}" not in normalized:
            errors.append(f"missing {table}")
    for marker in ("tenant_id uuid not null", "enable row level security", "force row level security", "current_setting('app.current_tenant_id', true)", "key_prefix"):
        if marker not in normalized:
            errors.append(f"migration missing security marker: {marker}")
    if "api_key_value" in normalized or "key_value" in normalized:
        errors.append("migration must not persist API key values")
    if "create index if not exists idx_inference_access_policy_events" not in normalized:
        errors.append("events need a tenant/time index")
    for marker in (
        "effect text not null",
        "check (effect in ('scope','allow','deny'))",
        "primary key (policy_id, api_key_id, effect)",
        "constraint inference_access_policy_api_keys_effect_check",
        "constraint inference_access_policy_api_keys_pkey",
    ):
        if marker not in normalized:
            errors.append(f"fresh migration missing key effect schema: {marker}")
    for marker, error in (
        ("begin;", "forward migration must be transactional"),
        ("commit;", "forward migration must commit transaction"),
        ("drop constraint if exists inference_access_policy_api_keys_effect_check", "forward migration missing deterministic effect check drop"),
        ("add constraint inference_access_policy_api_keys_effect_check check (effect in ('scope', 'allow', 'deny'))", "forward migration missing scope/allow/deny effect check"),
        ("drop constraint if exists inference_access_policy_api_keys_pkey", "forward migration missing deterministic primary key drop"),
        ("add constraint inference_access_policy_api_keys_pkey primary key (policy_id, api_key_id, effect)", "forward migration missing effect primary key"),
        ("pg_get_constraintdef", "forward migration missing legacy primary key detection"),
        ("primary key (policy_id, api_key_id)", "forward migration missing legacy primary key detection"),
        ("status = 'enabled'", "forward migration missing enabled legacy predicate"),
        ("raise exception 'c41_access_policy_scope_reconciliation_required'", "forward migration missing stable reconciliation raise"),
        ("scope_type in ('api_key', 'inference_service_api_key')", "forward migration missing fail-closed scope filter"),
        ("alter table inference_access_policies disable row level security", "forward migration missing rls disable"),
        ("alter table inference_access_policy_api_keys disable row level security", "forward migration missing rls disable"),
        ("alter table inference_access_policies enable row level security", "forward migration missing rls restore"),
        ("alter table inference_access_policy_api_keys enable row level security", "forward migration missing rls restore"),
        ("alter table inference_access_policies force row level security", "forward migration missing rls restore"),
        ("alter table inference_access_policy_api_keys force row level security", "forward migration missing rls restore"),
    ):
        if marker not in forward:
            errors.append(error)
    if "insert into inference_access_policy_api_keys" in forward:
        errors.append("forward migration must reject automatic scope backfill")
    if "update inference_access_policies" in forward:
        errors.append("forward migration must reject automatic status mutation")
    if "delete from inference_access_policies" in forward or "delete from inference_access_policy_api_keys" in forward:
        errors.append("forward migration must not delete policy or key data")
    for marker, error in (
        ("create table if not exists inference_access_policy_mutations", "idempotency migration missing mutation table"),
        ("primary key (tenant_id, operation_scope, idempotency_key)", "idempotency migration missing tenant-scoped replay identity"),
        ("request_hash text not null", "idempotency migration missing request hash"),
        ("result_snapshot jsonb not null", "idempotency migration missing replay result"),
        ("check (lease_ttl_seconds between 1 and 3600)", "idempotency migration missing OpenAPI TTL bound"),
        ("enable row level security", "idempotency migration missing RLS enable"),
        ("force row level security", "idempotency migration missing RLS force"),
        ("current_setting('app.current_tenant_id', true)", "idempotency migration missing tenant isolation"),
    ):
        if marker not in idempotency:
            errors.append(error)
    return tuple(errors)


def main() -> int:
    errors = validate(
        MIGRATION_PATH.read_text(encoding="utf-8"),
        FORWARD_MIGRATION_PATH.read_text(encoding="utf-8"),
        IDEMPOTENCY_MIGRATION_PATH.read_text(encoding="utf-8"),
    )
    for error in errors:
        print(f"ERROR: {error}")
    if errors:
        print(f"inference access policy migration blocked: {len(errors)} error(s)")
        return 1
    print("inference access policy migration valid")
    return 0


if __name__ == "__main__":
    raise SystemExit(main())
