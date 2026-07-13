#!/usr/bin/env python3
"""Tests for the repository binary-file guard."""

from __future__ import annotations

import subprocess
import tempfile
import unittest
from pathlib import Path

import validate_no_binary_files as binary_guard


class NoBinaryFilesValidationTest(unittest.TestCase):
    def test_scan_rejects_tracked_binary_files(self) -> None:
        with tempfile.TemporaryDirectory() as tmpdir:
            root = Path(tmpdir)
            self._git(root, "init")
            (root / "README.md").write_text("# ok\n", encoding="utf-8")
            (root / "artifact.bin").write_bytes(b"\x7fELF\x00\x01\x02\x03")
            self._git(root, "add", "README.md", "artifact.bin")

            violations = binary_guard.find_tracked_binary_files(root)

        self.assertEqual(["artifact.bin"], [item.path for item in violations])

    def test_scan_allows_tracked_text_files(self) -> None:
        with tempfile.TemporaryDirectory() as tmpdir:
            root = Path(tmpdir)
            self._git(root, "init")
            (root / "README.md").write_text("# ok\n", encoding="utf-8")
            self._git(root, "add", "README.md")

            violations = binary_guard.find_tracked_binary_files(root)

        self.assertEqual([], violations)

    def test_main_returns_failure_for_binary_files(self) -> None:
        with tempfile.TemporaryDirectory() as tmpdir:
            root = Path(tmpdir)
            self._git(root, "init")
            (root / "payload.png").write_bytes(b"\x89PNG\r\n\x1a\n\x00\x00")
            self._git(root, "add", "payload.png")

            exit_code = binary_guard.main(["--root", str(root)])

        self.assertEqual(1, exit_code)

    def test_main_allows_contract_registered_existing_binary_files(self) -> None:
        with tempfile.TemporaryDirectory() as tmpdir:
            root = Path(tmpdir)
            self._git(root, "init")
            (root / "payload.png").write_bytes(b"\x89PNG\r\n\x1a\n\x00\x00")
            self._git(root, "add", "payload.png")
            contract = root / "repository-file-contract.yaml"
            contract.write_text(
                """
binary_files:
  policy: no_new_binary_files
  enforcement: make validate-no-binary-files
  allowed_existing_paths:
    - payload.png
""".lstrip(),
                encoding="utf-8",
            )

            exit_code = binary_guard.main(["--root", str(root), "--contract", str(contract)])

        self.assertEqual(0, exit_code)

    def _git(self, root: Path, *args: str) -> None:
        subprocess.run(["git", *args], cwd=root, check=True, stdout=subprocess.PIPE, stderr=subprocess.PIPE)


if __name__ == "__main__":
    unittest.main()
