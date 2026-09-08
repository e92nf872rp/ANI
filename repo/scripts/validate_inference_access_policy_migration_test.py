#!/usr/bin/env python3
import unittest

import validate_inference_access_policy_migration as validator


class InferenceAccessPolicyMigrationTests(unittest.TestCase):
    def test_migration_contract(self):
        self.assertEqual(
            validator.validate(
                validator.MIGRATION_PATH.read_text(encoding="utf-8"),
                validator.FORWARD_MIGRATION_PATH.read_text(encoding="utf-8"),
                validator.IDEMPOTENCY_MIGRATION_PATH.read_text(encoding="utf-8"),
            ),
            (),
        )

    def test_rejects_missing_tenant_rls(self):
        sql = validator.MIGRATION_PATH.read_text(encoding="utf-8").replace("FORCE ROW LEVEL SECURITY", "ENABLE ROW LEVEL SECURITY")
        self.assertTrue(any("force row level security" in error for error in validator.validate(sql, validator.FORWARD_MIGRATION_PATH.read_text(encoding="utf-8"), validator.IDEMPOTENCY_MIGRATION_PATH.read_text(encoding="utf-8"))))

    def test_rejects_forward_migration_without_legacy_preflight_block(self):
        forward = validator.FORWARD_MIGRATION_PATH.read_text(encoding="utf-8")
        for removed, expected in (
            ("DROP CONSTRAINT IF EXISTS inference_access_policy_api_keys_effect_check", "effect check"),
            ("DROP CONSTRAINT IF EXISTS inference_access_policy_api_keys_pkey", "primary key"),
            ("pg_get_constraintdef", "legacy primary key detection"),
            ("status = 'enabled'", "enabled legacy predicate"),
            ("RAISE EXCEPTION 'C41_ACCESS_POLICY_SCOPE_RECONCILIATION_REQUIRED'", "stable reconciliation raise"),
            ("DISABLE ROW LEVEL SECURITY", "rls disable"),
            ("FORCE ROW LEVEL SECURITY", "rls restore"),
        ):
            with self.subTest(removed=removed):
                mutated = forward.replace(removed, "", 1)
                self.assertTrue(any(expected in error for error in validator.validate(validator.MIGRATION_PATH.read_text(encoding="utf-8"), mutated, validator.IDEMPOTENCY_MIGRATION_PATH.read_text(encoding="utf-8"))))

    def test_rejects_automatic_scope_or_status_mutation(self):
        forward = validator.FORWARD_MIGRATION_PATH.read_text(encoding="utf-8")
        for unsafe, expected in (
            (forward + "\nINSERT INTO inference_access_policy_api_keys(effect) VALUES ('scope');", "automatic scope"),
            (forward + "\nUPDATE inference_access_policies SET status='disabled';", "automatic status"),
        ):
            with self.subTest(expected=expected):
                self.assertTrue(any(expected in error for error in validator.validate(validator.MIGRATION_PATH.read_text(encoding="utf-8"), unsafe, validator.IDEMPOTENCY_MIGRATION_PATH.read_text(encoding="utf-8"))))

    def test_rejects_idempotency_migration_without_replay_or_rls_contract(self):
        migration = validator.IDEMPOTENCY_MIGRATION_PATH.read_text(encoding="utf-8")
        for removed, expected in (
            ("result_snapshot JSONB NOT NULL", "replay result"),
            ("PRIMARY KEY (tenant_id, operation_scope, idempotency_key)", "replay identity"),
            ("FORCE ROW LEVEL SECURITY", "RLS force"),
            ("BETWEEN 1 AND 3600", "TTL bound"),
        ):
            with self.subTest(removed=removed):
                mutated = migration.replace(removed, "", 1)
                errors = validator.validate(
                    validator.MIGRATION_PATH.read_text(encoding="utf-8"),
                    validator.FORWARD_MIGRATION_PATH.read_text(encoding="utf-8"),
                    mutated,
                )
                self.assertTrue(any(expected.lower() in error.lower() for error in errors), errors)


if __name__ == "__main__":
    unittest.main()
