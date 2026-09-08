#!/usr/bin/env python3
"""Validate the async_tasks/outbox_events tenant RLS repair migration."""

from __future__ import annotations

import re
from pathlib import Path

ROOT = Path(__file__).resolve().parents[1]
MIGRATION_PATH = ROOT / "deploy/migrations/20260829_002_async_outbox_rls_permissive.sql"


def _compact(sql: str) -> str:
    sql = re.sub(r"--[^\n]*", " ", sql)
    sql = re.sub(r"/\*.*?\*/", " ", sql, flags=re.DOTALL)
    compact = re.sub(r"\s+", " ", sql.lower()).strip()
    return re.sub(r"\(\s+", "(", re.sub(r"\s+\)", ")", compact))


def validate(sql: str) -> tuple[str, ...]:
    normalized = _compact(sql)
    errors: list[str] = []
    required_setting = "current_setting('app.current_tenant_id', true)"

    for table in ("async_tasks", "outbox_events"):
        if f"drop policy if exists tenant_isolation on {table}" not in normalized:
            errors.append(f"{table} must replace restrictive tenant_isolation")
        if f"create policy {table}_platform_bypass on {table}" not in normalized:
            errors.append(f"{table} missing platform bypass policy")
        if f"create policy {table}_self on {table}" not in normalized:
            errors.append(f"{table} missing tenant self policy")

    if "as permissive" not in normalized:
        errors.append("migration must create PERMISSIVE policies")
    if normalized.count(required_setting) < 8:
        errors.append("migration must bind both tables' policies to app.current_tenant_id")

    for table in ("async_tasks", "outbox_events"):
        self_start = normalized.find(f"create policy {table}_self")
        if self_start < 0:
            continue
        next_policy = normalized.find("create policy ", self_start + 1)
        self_sql = normalized[self_start : next_policy if next_policy >= 0 else len(normalized)]
        if "using (tenant_id = nullif(current_setting('app.current_tenant_id', true), '')::uuid)" not in self_sql:
            errors.append(f"{table} tenant self policy missing tenant predicate")
        if "with check (tenant_id = nullif(current_setting('app.current_tenant_id', true), '')::uuid)" not in self_sql:
            errors.append(f"{table} tenant self policy missing WITH CHECK tenant predicate")

    return tuple(errors)


def main() -> int:
    errors = validate(MIGRATION_PATH.read_text(encoding="utf-8"))
    for error in errors:
        print(f"ERROR: {error}")
    if errors:
        print(f"async/outbox RLS migration blocked: {len(errors)} error(s)")
        return 1
    print("async_tasks/outbox_events RLS migration valid")
    return 0


if __name__ == "__main__":
    raise SystemExit(main())
