#!/usr/bin/env python3
from __future__ import annotations

import json
import unittest
from pathlib import Path

import validate_inference_gpu_lws_volcano_live_gate as gate


class InferenceGPULWSVolcanoLiveGateTests(unittest.TestCase):
    def test_repo_contract_is_valid(self) -> None:
        document = gate.load_gate(gate.DEFAULT_GATE)
        gate.validate_contract(document)
        self.assertEqual(document.get("status"), "contract")

    def test_rejects_live_status_without_evidence(self) -> None:
        document = gate.load_gate(gate.DEFAULT_GATE)
        document["status"] = "live"
        document["evidence"] = "development-records/live-evidence/missing-inference-gpu-lws.json"
        with self.assertRaises(SystemExit):
            gate.validate_contract(document)

    def test_accepts_live_status_with_redacted_evidence(self) -> None:
        document = gate.load_gate(gate.DEFAULT_GATE)
        document["status"] = "live"
        evidence = {
            "profile": gate.PROFILE,
            "status": "passed",
            "gateway": "lab-process-not-in-cluster-ani-gateway",
            "gpu_ready": False,
            "runtime_ready": False,
            "checks": [{"id": item, "status": "passed"} for item in sorted(gate.REQUIRED_CHECKS)],
        }
        target = gate.DEFAULT_EVIDENCE.with_name("inference-gpu-lws-volcano-live-test.json")
        target.parent.mkdir(parents=True, exist_ok=True)
        target.write_text(json.dumps(evidence), encoding="utf-8")
        self.addCleanup(target.unlink, missing_ok=True)
        document["evidence"] = str(target.relative_to(gate.ROOT))
        gate.validate_contract(document)

    def test_rejects_missing_lws_check(self) -> None:
        document = gate.load_gate(gate.DEFAULT_GATE)
        document["live_checks"] = [
            item for item in document["live_checks"] if item.get("id") != "multi-node-lws-fail-closed-without-crd"
        ]
        with self.assertRaises(SystemExit):
            gate.validate_contract(document)

    def test_gate_file_exists(self) -> None:
        self.assertTrue(Path(gate.DEFAULT_GATE).exists())


if __name__ == "__main__":
    unittest.main()
