#!/usr/bin/env python3
from __future__ import annotations

import tempfile
import unittest
from copy import deepcopy
from pathlib import Path

import validate_instance_reconcile_provider_loss_live_gate as gate


class InstanceReconcileProviderLossLiveGateTest(unittest.TestCase):
    def test_default_contract_is_valid(self) -> None:
        gate.validate_contract(gate.load_gate(gate.DEFAULT_GATE))

    def test_missing_required_check_is_rejected(self) -> None:
        document = deepcopy(gate.load_gate(gate.DEFAULT_GATE))
        document["live_checks"] = [
            check
            for check in document["live_checks"]
            if check.get("id") != "reconcile-provider-resource-lost"
        ]
        with self.assertRaises(SystemExit):
            gate.validate_contract(document)

    def test_provider_loss_requires_failed_state_and_exact_reason(self) -> None:
        self.assertTrue(gate.is_provider_lost({"state": "failed", "reason": "ProviderResourceLost"}))
        self.assertFalse(gate.is_provider_lost({"state": "running", "reason": "ProviderResourceLost"}))
        self.assertFalse(gate.is_provider_lost({"state": "failed", "reason": "ProviderReadFailed"}))

    def test_evidence_does_not_persist_endpoints_or_token(self) -> None:
        with tempfile.TemporaryDirectory() as tmp:
            path = Path(tmp) / "evidence.json"
            gate.write_evidence(
                path,
                {
                    "status": "passed",
                    "instance_id": "sandbox_1",
                    "state_after_provider_loss": "failed",
                    "reason_after_provider_loss": "ProviderResourceLost",
                },
            )
            text = path.read_text(encoding="utf-8")
            self.assertIn('"profile": "INSTANCE-RECONCILE-PROVIDER-404-A"', text)
            self.assertNotIn("Bearer", text)
            self.assertNotIn("192.168.", text)


if __name__ == "__main__":
    unittest.main()
