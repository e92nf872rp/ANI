#!/usr/bin/env python3
"""Tests for STORAGE-CONTROL-PLANE-STATE-A schema migration validator."""

from __future__ import annotations

import tempfile
import unittest
from pathlib import Path

import validate_storage_control_plane_state as validator


INCOMPLETE_SQL = """
BEGIN;
-- Intentionally missing required storage control-plane tables.
-- Keep session key so the validator reaches the table checklist.
SELECT current_setting('app.current_tenant_id', true);
COMMIT;
"""

FORBIDDEN_CONTENT_SQL = """
BEGIN;
CREATE TABLE IF NOT EXISTS storage_objects (
    tenant_id UUID NOT NULL,
    object_id TEXT NOT NULL,
    embedding BYTEA,
    presigned_url TEXT,
    PRIMARY KEY (tenant_id, object_id)
);
ALTER TABLE storage_objects ENABLE ROW LEVEL SECURITY;
ALTER TABLE storage_objects FORCE ROW LEVEL SECURITY;
CREATE POLICY tenant_isolation ON storage_objects
    USING (tenant_id = NULLIF(current_setting('ani.tenant_id', true), '')::uuid);
COMMIT;
"""


class StorageControlPlaneStateValidatorTest(unittest.TestCase):
    def test_repository_migration_passes(self) -> None:
        validator.validate_migration_file(validator.DEFAULT_MIGRATION)

    def test_incomplete_migration_fails_required_tables(self) -> None:
        with tempfile.TemporaryDirectory() as directory:
            path = Path(directory) / "bad.sql"
            path.write_text(INCOMPLETE_SQL, encoding="utf-8")
            with self.assertRaises(SystemExit) as ctx:
                validator.validate_migration_file(path)
            self.assertIn("missing table", str(ctx.exception))

    def test_forbidden_payload_and_legacy_session_key_fail(self) -> None:
        with tempfile.TemporaryDirectory() as directory:
            path = Path(directory) / "bad.sql"
            path.write_text(FORBIDDEN_CONTENT_SQL, encoding="utf-8")
            with self.assertRaises(SystemExit) as ctx:
                validator.validate_migration_file(path)
            message = str(ctx.exception)
            self.assertTrue(
                "embedding" in message
                or "presigned" in message
                or "ani.tenant_id" in message
            )

    def test_missing_policy_for_one_table_is_rejected(self) -> None:
        sql = validator.DEFAULT_MIGRATION.read_text(encoding="utf-8")
        sql = sql.replace(
            "CREATE POLICY tenant_isolation ON storage_volume_snapshots",
            "CREATE POLICY unrelated_policy ON storage_volume_snapshots",
            1,
        )
        with self.assertRaises(SystemExit) as ctx:
            validator.validate_migration_sql(sql)
        self.assertIn("tenant_isolation policy for storage_volume_snapshots", str(ctx.exception))

    def test_weakened_policy_for_one_table_is_rejected(self) -> None:
        sql = validator.DEFAULT_MIGRATION.read_text(encoding="utf-8")
        policy = """CREATE POLICY tenant_isolation ON storage_volume_snapshots
    AS RESTRICTIVE
    USING (tenant_id = NULLIF(current_setting('app.current_tenant_id', true), '')::uuid)
    WITH CHECK (tenant_id = NULLIF(current_setting('app.current_tenant_id', true), '')::uuid)"""
        self.assertIn(policy, sql)
        weakened = """CREATE POLICY tenant_isolation ON storage_volume_snapshots
    AS RESTRICTIVE
    USING (true)"""
        with self.assertRaises(SystemExit) as ctx:
            validator.validate_migration_sql(sql.replace(policy, weakened, 1))
        self.assertIn("tenant predicate for storage_volume_snapshots", str(ctx.exception))

    def test_missing_tenant_first_primary_key_for_one_table_is_rejected(self) -> None:
        sql = validator.DEFAULT_MIGRATION.read_text(encoding="utf-8")
        marker = "PRIMARY KEY (tenant_id, snapshot_id)"
        self.assertIn(marker, sql)
        with self.assertRaises(SystemExit) as ctx:
            validator.validate_migration_sql(sql.replace(marker, "PRIMARY KEY (snapshot_id)", 1))
        self.assertIn("tenant-first primary key for storage_volume_snapshots", str(ctx.exception))

    def test_missing_idempotency_columns_for_one_table_is_rejected(self) -> None:
        sql = validator.DEFAULT_MIGRATION.read_text(encoding="utf-8")
        start = sql.index("CREATE TABLE IF NOT EXISTS storage_volume_snapshots")
        end = sql.index(";", start) + 1
        table_sql = sql[start:end]
        table_sql = table_sql.replace("create_idempotency_key      TEXT,", "")
        table_sql = table_sql.replace("create_request_fingerprint  TEXT,", "")
        with self.assertRaises(SystemExit) as ctx:
            validator.validate_migration_sql(sql[:start] + table_sql + sql[end:])
        self.assertIn("create_idempotency_key for storage_volume_snapshots", str(ctx.exception))

    def test_comments_do_not_satisfy_or_reject_session_key_checks(self) -> None:
        sql = validator.DEFAULT_MIGRATION.read_text(encoding="utf-8")
        validator.validate_migration_sql("-- ani.tenant_id is legacy\n" + sql)
        without_required = sql.replace("app.current_tenant_id", "other.current_tenant_id")
        with self.assertRaises(SystemExit) as ctx:
            validator.validate_migration_sql("-- app.current_tenant_id\n" + without_required)
        self.assertIn("must use app.current_tenant_id", str(ctx.exception))

    def test_rls_accepts_only_keyword_and_flexible_whitespace(self) -> None:
        sql = validator.DEFAULT_MIGRATION.read_text(encoding="utf-8")
        sql = sql.replace(
            "ALTER TABLE storage_volume_snapshots ENABLE ROW LEVEL SECURITY;",
            "alter  table  only storage_volume_snapshots\n enable row level security;",
            1,
        ).replace(
            "ALTER TABLE storage_volume_snapshots FORCE ROW LEVEL SECURITY;",
            "alter table only storage_volume_snapshots force\nrow level security;",
            1,
        )
        validator.validate_migration_sql(sql)


if __name__ == "__main__":
    unittest.main()
