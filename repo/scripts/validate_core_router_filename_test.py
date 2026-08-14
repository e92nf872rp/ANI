#!/usr/bin/env python3
"""Prevent Core contract guards from regressing to the removed demo router name."""

from __future__ import annotations

import unittest
from pathlib import Path


ROOT = Path(__file__).resolve().parents[1]
GUARDS = (
    ROOT / "scripts/validate_core_alpha_contract.py",
    ROOT / "scripts/validate_core_beta_contract.py",
    ROOT / "scripts/validate_core_dev_profile_contract.py",
)


class CoreRouterFilenameTest(unittest.TestCase):
    def test_contract_guards_reference_current_instance_router(self) -> None:
        current_router = ROOT / "services/ani-gateway/internal/router/instances.go"
        self.assertTrue(current_router.is_file())

        for guard in GUARDS:
            with self.subTest(guard=guard.name):
                text = guard.read_text(encoding="utf-8")
                self.assertIn("services/ani-gateway/internal/router/instances.go", text)
                self.assertNotIn("demo_instances.go", text)


if __name__ == "__main__":
    unittest.main()
