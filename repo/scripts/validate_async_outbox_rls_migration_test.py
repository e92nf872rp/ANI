#!/usr/bin/env python3
"""Contract tests for the async task/outbox RLS repair migration."""

import re
import unittest

import validate_async_outbox_rls_migration as validator


class AsyncOutboxRLSMigrationTests(unittest.TestCase):
    def test_migration_adds_permissive_platform_and_tenant_policies(self) -> None:
        errors = validator.validate(validator.MIGRATION_PATH.read_text(encoding="utf-8"))
        self.assertEqual(errors, ())

    def test_migration_must_keep_tenant_check(self) -> None:
        sql = validator.MIGRATION_PATH.read_text(encoding="utf-8")
        weakened = re.sub(
            r"WITH CHECK \(\s*tenant_id = .*?\);",
            "WITH CHECK (TRUE);",
            sql,
            count=1,
            flags=re.DOTALL,
        )
        errors = validator.validate(weakened)
        self.assertTrue(any("tenant self policy" in error for error in errors))


if __name__ == "__main__":
    unittest.main()
