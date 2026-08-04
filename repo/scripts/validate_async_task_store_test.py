#!/usr/bin/env python3
"""Tests for the async-task store source validator."""

from __future__ import annotations

import contextlib
import io
import tempfile
import unittest
from pathlib import Path
from unittest.mock import patch

import validate_async_task_store as validator


class AsyncTaskStoreValidatorTest(unittest.TestCase):
    def test_repository_contract_passes(self) -> None:
        self.assertEqual(0, validator.main())

    def test_missing_source_file_is_a_validation_failure_without_traceback(self) -> None:
        with tempfile.TemporaryDirectory() as directory:
            stderr = io.StringIO()
            with patch.object(validator, "ROOT", Path(directory)), contextlib.redirect_stderr(stderr):
                result = validator.main()
        output = stderr.getvalue()
        self.assertEqual(1, result)
        self.assertIn("missing file", output)
        self.assertNotIn("Traceback", output)


if __name__ == "__main__":
    unittest.main()
