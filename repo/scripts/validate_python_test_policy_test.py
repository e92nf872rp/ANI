#!/usr/bin/env python3
"""Tests for the Python AI test policy."""

from __future__ import annotations

import unittest

from validate_python_test_policy import validate_changed_files


class PythonTestPolicyTest(unittest.TestCase):
    def test_non_python_change_does_not_require_tests(self) -> None:
        self.assertEqual(validate_changed_files(["docs/guide.md"]), [])

    def test_python_ai_source_without_test_is_blocked(self) -> None:
        errors = validate_changed_files(["ai/rag-engine/app/service.py"])
        self.assertTrue(any("without a matching test" in error for error in errors))

    def test_python_ai_source_with_test_is_allowed(self) -> None:
        self.assertEqual(
            validate_changed_files(
                ["ai/rag-engine/app/service.py", "ai/rag-engine/tests/test_service.py"]
            ),
            [],
        )


if __name__ == "__main__":
    unittest.main()
