#!/usr/bin/env python3
"""Tests for the inference control-plane migration contract."""

from __future__ import annotations

import unittest

import validate_inference_control_plane_migration as validator


class InferenceControlPlaneMigrationTest(unittest.TestCase):
    def test_current_migration_satisfies_control_plane_contract(self) -> None:
        self.assertEqual(validator.validate(validator.MIGRATION_PATH.read_text(encoding="utf-8")), ())

    def test_missing_generation_cas_fields_is_rejected(self) -> None:
        sql = validator.MIGRATION_PATH.read_text(encoding="utf-8").replace(
            "target_generation BIGINT NOT NULL", "target_generation_missing BIGINT NOT NULL"
        )
        self.assertIn("inference_operations missing target_generation", validator.validate(sql))

    def test_missing_tenant_with_check_is_rejected(self) -> None:
        sql = validator.MIGRATION_PATH.read_text(encoding="utf-8").replace("WITH CHECK", "WITHOUT CHECK")
        self.assertIn("inference_operations RLS must define WITH CHECK", validator.validate(sql))

    def test_active_name_indexes_must_be_partial(self) -> None:
        sql = validator.MIGRATION_PATH.read_text(encoding="utf-8").replace(
            "WHERE deleted_at IS NULL", "WHERE TRUE"
        )
        self.assertIn("inference service active-name indexes must exclude tombstones", validator.validate(sql))

    def test_current_operation_foreign_key_must_be_deferred(self) -> None:
        sql = validator.MIGRATION_PATH.read_text(encoding="utf-8").replace(
            "DEFERRABLE INITIALLY DEFERRED", "NOT DEFERRABLE"
        )
        self.assertIn(
            "current operation foreign key must allow atomic service/operation creation",
            validator.validate(sql),
        )

    def test_legacy_rows_must_not_keep_empty_running_desired_spec(self) -> None:
        sql = validator.MIGRATION_PATH.read_text(encoding="utf-8").replace(
            "legacy_quarantined = TRUE", "legacy_quarantined = FALSE"
        )
        self.assertTrue(any("legacy inference rows" in error for error in validator.validate(sql)))

    def test_current_operation_constraint_lookup_is_schema_local(self) -> None:
        sql = validator.MIGRATION_PATH.read_text(encoding="utf-8").replace(
            "AND conrelid = 'inference_services'::regclass", "AND TRUE"
        )
        self.assertIn(
            "current operation foreign key lookup must be scoped to inference_services",
            validator.validate(sql),
        )


if __name__ == "__main__":
    unittest.main()
