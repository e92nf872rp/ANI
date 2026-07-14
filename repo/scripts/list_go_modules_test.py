#!/usr/bin/env python3
"""Tests for go.work module discovery."""

from __future__ import annotations

import tempfile
import unittest
from pathlib import Path

import list_go_modules


class GoModuleDiscoveryTest(unittest.TestCase):
    def test_lists_all_modules_from_block_and_ignores_comments(self) -> None:
        with tempfile.TemporaryDirectory() as directory:
            root = Path(directory)
            for module in ("pkg", "services/model-service"):
                module_dir = root / module
                module_dir.mkdir(parents=True)
                (module_dir / "go.mod").write_text("module example\n", encoding="utf-8")
            work_file = root / "go.work"
            work_file.write_text(
                "go 1.23\n\nuse (\n\t./pkg\n\t./services/model-service // current\n)\n",
                encoding="utf-8",
            )

            self.assertEqual(
                list_go_modules.list_modules(work_file),
                ["./pkg", "./services/model-service"],
            )

    def test_missing_go_mod_is_blocked(self) -> None:
        with tempfile.TemporaryDirectory() as directory:
            root = Path(directory)
            work_file = root / "go.work"
            work_file.write_text("use (\n\t./missing\n)\n", encoding="utf-8")

            with self.assertRaisesRegex(ValueError, "expected go.mod"):
                list_go_modules.list_modules(work_file)


if __name__ == "__main__":
    unittest.main()
