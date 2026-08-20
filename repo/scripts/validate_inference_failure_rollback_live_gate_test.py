#!/usr/bin/env python3
from __future__ import annotations

import json
import unittest
from pathlib import Path

import validate_inference_failure_rollback_live_gate as gate


class InferenceFailureRollbackLiveGateTests(unittest.TestCase):
    def test_repo_contract_is_valid(self) -> None:
        document = gate.load_gate(gate.DEFAULT_GATE)
        if document.get("status") == "live":
            document = dict(document)
            document["status"] = "contract"
        gate.validate_contract(document)

    def test_rejects_live_status_without_evidence(self) -> None:
        document = gate.load_gate(gate.DEFAULT_GATE)
        document["status"] = "live"
        document["evidence"] = "development-records/live-evidence/missing-inference-failure-rollback.json"
        with self.assertRaises(SystemExit):
            gate.validate_contract(document)

    def test_accepts_live_status_with_redacted_evidence(self) -> None:
        document = gate.load_gate(gate.DEFAULT_GATE)
        document["status"] = "live"
        evidence = {
            "profile": gate.PROFILE,
            "status": "passed",
            "gateway": "lab-process-not-in-cluster-ani-gateway",
            "checks": [{"id": item, "status": "passed"} for item in sorted(gate.REQUIRED_CHECKS)],
        }
        target = gate.DEFAULT_EVIDENCE.with_name("inference-failure-rollback-live-test.json")
        target.parent.mkdir(parents=True, exist_ok=True)
        target.write_text(json.dumps(evidence), encoding="utf-8")
        self.addCleanup(target.unlink, missing_ok=True)
        document["evidence"] = str(target.relative_to(gate.ROOT))
        gate.validate_contract(document)

    def test_rejects_missing_rollback_check(self) -> None:
        document = gate.load_gate(gate.DEFAULT_GATE)
        document["live_checks"] = [
            item for item in document["live_checks"] if item.get("id") != "inference-service-scale-rolled-back"
        ]
        with self.assertRaises(SystemExit):
            gate.validate_contract(document)

    def test_gate_file_exists(self) -> None:
        self.assertTrue(Path(gate.DEFAULT_GATE).exists())


if __name__ == "__main__":
    unittest.main()
