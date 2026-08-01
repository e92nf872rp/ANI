#!/usr/bin/env python3
"""Unit tests for sandbox live gate contract validation."""

from __future__ import annotations

import tempfile
import unittest
from pathlib import Path

import validate_sandbox_live_gate as gate


class SandboxLiveGateContractTest(unittest.TestCase):
    def test_default_gate_contract_valid(self) -> None:
        document = gate.load_gate(gate.DEFAULT_GATE)
        gate.validate_contract(document)

    def test_missing_runtimeclass_endpoint_rejected(self) -> None:
        document = gate.load_gate(gate.DEFAULT_GATE)
        document["required_endpoints"] = ["ani_core_instances_api", "kubernetes_read_api"]
        with self.assertRaises(SystemExit):
            gate.validate_contract(document)

    def test_write_evidence_roundtrip(self) -> None:
        with tempfile.TemporaryDirectory() as tmp:
            path = Path(tmp) / "evidence.json"
            gate.write_evidence(path, {"status": "passed", "kind": "sandbox"})
            text = path.read_text(encoding="utf-8")
            self.assertIn('"id": "instance-sandbox-live-gate"', text)
            self.assertIn('"profile": "INSTANCE-SANDBOX-LIVE-GATE-A"', text)


if __name__ == "__main__":
    unittest.main()
