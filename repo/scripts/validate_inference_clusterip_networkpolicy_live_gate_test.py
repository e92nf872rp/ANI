#!/usr/bin/env python3
from __future__ import annotations

import json
import unittest
from pathlib import Path

import validate_inference_clusterip_networkpolicy_live_gate as gate


class InferenceClusterIPNetworkPolicyLiveGateTests(unittest.TestCase):
    def test_repo_contract_is_valid(self) -> None:
        document = gate.load_gate(gate.DEFAULT_GATE)
        gate.validate_contract(document)

    def test_rejects_live_status_without_evidence(self) -> None:
        document = gate.load_gate(gate.DEFAULT_GATE)
        document["status"] = "live"
        document["evidence"] = "development-records/live-evidence/missing-inference-clusterip-np.json"
        with self.assertRaises(SystemExit):
            gate.validate_contract(document)

    def test_accepts_live_status_with_redacted_evidence(self) -> None:
        document = gate.load_gate(gate.DEFAULT_GATE)
        document["status"] = "live"
        evidence = {
            "profile": gate.PROFILE,
            "status": "passed",
            "gateway": "lab-process-not-in-cluster-ani-gateway",
            "exposure": "clusterip_networkpolicy",
            "product_test": "not_registered",
            "checks": [{"id": item, "status": "passed"} for item in sorted(gate.REQUIRED_CHECKS)],
        }
        target = gate.DEFAULT_EVIDENCE.with_name("inference-clusterip-networkpolicy-live-test.json")
        target.parent.mkdir(parents=True, exist_ok=True)
        target.write_text(json.dumps(evidence), encoding="utf-8")
        self.addCleanup(target.unlink, missing_ok=True)
        document["evidence"] = str(target.relative_to(gate.ROOT))
        gate.validate_contract(document)

    def test_rejects_missing_networkpolicy_check(self) -> None:
        document = gate.load_gate(gate.DEFAULT_GATE)
        document["live_checks"] = [
            item for item in document["live_checks"] if item.get("id") != "inference-service-networkpolicy-applied"
        ]
        with self.assertRaises(SystemExit):
            gate.validate_contract(document)

    def test_rejects_registered_product_test(self) -> None:
        document = gate.load_gate(gate.DEFAULT_GATE)
        document["status"] = "live"
        evidence = {
            "profile": gate.PROFILE,
            "status": "passed",
            "gateway": "lab-process-not-in-cluster-ani-gateway",
            "exposure": "clusterip_networkpolicy",
            "product_test": "registered",
            "checks": [{"id": item, "status": "passed"} for item in sorted(gate.REQUIRED_CHECKS)],
        }
        target = gate.DEFAULT_EVIDENCE.with_name("inference-clusterip-networkpolicy-live-bad-test.json")
        target.parent.mkdir(parents=True, exist_ok=True)
        target.write_text(json.dumps(evidence), encoding="utf-8")
        self.addCleanup(target.unlink, missing_ok=True)
        document["evidence"] = str(target.relative_to(gate.ROOT))
        with self.assertRaises(SystemExit):
            gate.validate_contract(document)

    def test_gate_file_exists(self) -> None:
        self.assertTrue(Path(gate.DEFAULT_GATE).exists())


if __name__ == "__main__":
    unittest.main()
