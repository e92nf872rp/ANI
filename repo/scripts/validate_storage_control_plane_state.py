#!/usr/bin/env python3
"""Validate STORAGE-CONTROL-PLANE-STATE-A PostgreSQL schema migration."""

from __future__ import annotations

from pathlib import Path
import re
import sys


ROOT = Path(__file__).resolve().parents[1]
DEFAULT_MIGRATION = ROOT / "deploy/migrations/20260803_001_storage_control_plane_state.sql"

REQUIRED_TABLES = (
    "storage_volumes",
    "storage_volume_auto_snapshot_policies",
    "storage_volume_mount_events",
    "storage_volume_snapshots",
    "storage_filesystems",
    "storage_filesystem_mount_targets",
    "storage_filesystem_attachments",
    "storage_buckets",
    "storage_bucket_lifecycle_rules",
    "storage_objects",
    "vector_stores",
    "vector_store_knowledge_base_links",
)

CREATE_IDEMPOTENT_TABLES = (
    "storage_volumes",
    "storage_volume_snapshots",
    "storage_filesystems",
    "storage_filesystem_mount_targets",
    "storage_filesystem_attachments",
    "storage_buckets",
    "storage_bucket_lifecycle_rules",
    "storage_objects",
    "vector_stores",
)

# Word-boundary patterns so metadata names like embedding_model remain allowed.
FORBIDDEN_PATTERNS = (
    (r"\bpresigned_url\b", "presigned_url"),
    (r"\bpresigned\b", "presigned"),
    (r"\bembedding\b", "embedding"),
    (r"\bembeddings\b", "embedding"),
    (r"\bobject_body\b", "object_body"),
    (r"\bobject_content\b", "object_content"),
    (r"\bfile_content\b", "file_content"),
    (r"\bsearch_result\b", "search_result"),
)

LEGACY_SESSION_KEY = "ani.tenant_id"
REQUIRED_SESSION_KEY = "app.current_tenant_id"


def fail(message: str) -> None:
    raise SystemExit(f"storage control plane state invalid: {message}")


def require(condition: bool, message: str) -> None:
    if not condition:
        fail(message)


def strip_sql_comments(sql: str) -> str:
    without_block = re.sub(r"/\*.*?\*/", "", sql, flags=re.DOTALL)
    return re.sub(r"--[^\n]*", "", without_block)


def validate_migration_sql(sql: str) -> None:
    lowered = sql.lower()
    payload_surface = strip_sql_comments(sql).lower()

    for pattern, label in FORBIDDEN_PATTERNS:
        require(re.search(pattern, payload_surface) is None, f"migration must not persist {label}")

    require(LEGACY_SESSION_KEY not in sql, "migration must not use ani.tenant_id session key")
    require(REQUIRED_SESSION_KEY in sql, "migration must use app.current_tenant_id")

    for table in REQUIRED_TABLES:
        require(table in sql, f"migration missing table {table}")
        require(
            f"ALTER TABLE {table} ENABLE ROW LEVEL SECURITY" in sql
            or f"alter table {table} enable row level security" in lowered,
            f"migration missing ENABLE RLS for {table}",
        )
        require(
            f"ALTER TABLE {table} FORCE ROW LEVEL SECURITY" in sql
            or f"alter table {table} force row level security" in lowered,
            f"migration missing FORCE RLS for {table}",
        )
        require(
            f"ON {table}" in sql and "tenant_isolation" in sql,
            f"migration missing tenant_isolation policy for {table}",
        )
        require(
            f"PRIMARY KEY (tenant_id" in sql or f"primary key (tenant_id" in lowered,
            "migration must use tenant-first primary keys",
        )

    for table in CREATE_IDEMPOTENT_TABLES:
        require(
            f"create_idempotency_key" in sql and table in sql,
            f"migration must declare create_idempotency_key for {table}",
        )
        require(
            "create_request_fingerprint" in sql,
            "migration must declare create_request_fingerprint",
        )

    require("deleted_at" in sql, "migration must add soft-delete deleted_at")
    require("WITH CHECK" in sql, "migration RLS policies must include WITH CHECK")
    collapsed = re.sub(r"\s+", " ", lowered)
    require(
        "foreign key (tenant_id, volume_id) references storage_volumes(tenant_id, volume_id)"
        in collapsed,
        "snapshot/mount tables must use composite FK to storage_volumes",
    )
    require(
        "foreign key (tenant_id, filesystem_id) references storage_filesystems(tenant_id, filesystem_id)"
        in collapsed,
        "filesystem child tables must use composite FK to storage_filesystems",
    )
    require(
        "foreign key (tenant_id, subnet_id) references network_subnets(tenant_id, subnet_id)"
        in collapsed,
        "mount targets must use composite FK to network_subnets",
    )
    require(
        "UNIQUE INDEX" in sql or "unique index" in lowered or "UNIQUE (" in sql,
        "migration must declare create idempotency unique constraints",
    )
    require(
        "CHECK (state IN" in sql or "check (state in" in lowered,
        "migration must keep state check constraints",
    )
    require(
        "vector_store_knowledge_base_links" in sql
        and "knowledge_base_id" in sql
        and "WHERE deleted_at IS NULL" in sql,
        "KB link table must enforce one active link per vector store",
    )


def validate_migration_file(path: Path) -> None:
    require(path.is_file(), f"missing migration {path}")
    sql = path.read_text(encoding="utf-8")
    require(sql.strip() != "", f"migration {path} is empty")
    validate_migration_sql(sql)


def main(argv: list[str] | None = None) -> None:
    args = list(sys.argv[1:] if argv is None else argv)
    path = Path(args[0]) if args else DEFAULT_MIGRATION
    if not path.is_absolute():
        path = ROOT / path
    validate_migration_file(path)
    print("STORAGE-CONTROL-PLANE-STATE-A schema migration valid")


if __name__ == "__main__":
    main()
