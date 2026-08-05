#!/usr/bin/env python3
"""Regression tests for PR 83 Make and container build findings."""

from __future__ import annotations

import unittest
from pathlib import Path


ROOT = Path(__file__).resolve().parents[1]


class PR83BuildContractTest(unittest.TestCase):
    def test_make_help_and_success_receipts_cover_new_gates(self) -> None:
        makefile = (ROOT / "Makefile").read_text(encoding="utf-8")
        for target in (
            "validate-instance-management-live-gate",
            "validate-sandbox-live-gate",
            "validate-async-task-store",
            "validate-instance-sandbox-stateless-live-gate",
            "validate-instance-sandbox-checkpoint-live-gate",
            "validate-instance-reconcile-provider-loss-live-gate",
        ):
            self.assertIn(f'make {target} ', makefile)
        self.assertIn("ASYNC-TASK-STORE-A async task persistence contract valid", makefile)
        self.assertIn("INSTANCE-SANDBOX-STATELESS-LIVE-GATE-A sandbox runtime restart contract valid", makefile)

    def test_gateway_kubectl_is_current_and_checksum_verified(self) -> None:
        dockerfile = (ROOT / "services/ani-gateway/Dockerfile").read_text(encoding="utf-8")
        self.assertIn("ARG KUBECTL_VERSION=v1.36.1", dockerfile)
        self.assertIn("kubectl.sha256", dockerfile)
        self.assertIn("sha256sum -c", dockerfile)

    def test_reconcile_worker_uses_stdjson_build_tag(self) -> None:
        dockerfile = (ROOT / "services/reconcile-worker/Dockerfile").read_text(encoding="utf-8")
        self.assertIn("go build -tags stdjson", dockerfile)


if __name__ == "__main__":
    unittest.main()
