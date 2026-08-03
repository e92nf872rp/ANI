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


if __name__ == "__main__":
    unittest.main()
