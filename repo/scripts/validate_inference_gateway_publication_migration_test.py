#!/usr/bin/env python3
"""Mutation tests for the inference gateway publication migration contract."""

from __future__ import annotations

import unittest

import validate_inference_gateway_publication_migration as validator


class InferenceGatewayPublicationMigrationTests(unittest.TestCase):
    def test_current_migration_satisfies_publication_contract(self) -> None:
        self.assertEqual(validator.validate_text(validator.MIGRATION_PATH.read_text(encoding="utf-8")), ())

    def test_rejects_missing_generation_fence(self) -> None:
        sql = validator.MIGRATION_PATH.read_text(encoding="utf-8").replace(
            "publication_generation BIGINT NOT NULL DEFAULT 0", ""
        )
        self.assertTrue(any("publication_generation" in error for error in validator.validate_text(sql)))

    def test_rejects_non_null_initial_invocation_url(self) -> None:
        sql = validator.MIGRATION_PATH.read_text(encoding="utf-8").replace(
            "invocation_url = NULL", "invocation_url = invocation_url"
        )
        self.assertTrue(any("invocation_url" in error for error in validator.validate_text(sql)))

    def test_rejects_required_clauses_hidden_in_a_block_comment(self) -> None:
        sql = "/*\n" + validator.MIGRATION_PATH.read_text(encoding="utf-8") + "\n*/"
        self.assertTrue(validator.validate_text(sql))

    def test_rejects_required_clauses_hidden_in_nested_block_comments(self) -> None:
        sql = "/* outer /* nested */\n" + validator.MIGRATION_PATH.read_text(encoding="utf-8") + "\n*/"
        self.assertTrue(validator.validate_text(sql))

    def test_rejects_unclosed_block_comment(self) -> None:
        sql = validator.MIGRATION_PATH.read_text(encoding="utf-8") + "\n/* unclosed"
        self.assertTrue(validator.validate_text(sql))

    def test_comment_stripper_preserves_quoted_comment_markers(self) -> None:
        stripped, closed = validator.strip_sql_comments(
            "SELECT '-- not a comment /* either */', \"-- identifier /* name */\"; -- comment\n"
        )
        self.assertTrue(closed)
        self.assertIn("'-- not a comment /* either */'", stripped)
        self.assertIn('\"-- identifier /* name */\"', stripped)
        self.assertNotIn("-- comment", stripped)

    def test_rejects_every_required_clause_mutation(self) -> None:
        normalized = validator.normalize(validator.MIGRATION_PATH.read_text(encoding="utf-8"))
        for required in validator.REQUIRED_CLAUSES:
            with self.subTest(required=required):
                mutated = normalized.replace(required, "missing_required_clause", 1)
                self.assertTrue(validator.validate_text(mutated), required)

    def test_rejects_destructive_schema_changes_and_credentials(self) -> None:
        errors = validator.validate_text(validator.MIGRATION_PATH.read_text(encoding="utf-8") + "\nDROP COLUMN publication_phase;")
        self.assertTrue(any("drop column" in error for error in errors))
        errors = validator.validate_text(validator.MIGRATION_PATH.read_text(encoding="utf-8") + "\npassword=not-a-secret")
        self.assertTrue(any("credential" in error for error in errors))


if __name__ == "__main__":
    unittest.main()
