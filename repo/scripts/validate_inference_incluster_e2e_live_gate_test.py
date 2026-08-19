#!/usr/bin/env python3
from __future__ import annotations

import json
import unittest
from pathlib import Path

import validate_inference_incluster_e2e_live_gate as gate


class InferenceInclusterE2ELiveGateTests(unittest.TestCase):
    def test_repo_contract_is_valid(self) -> None:
        document = gate.load_gate(gate.DEFAULT_GATE)
        gate.validate_contract(document)
        self.assertEqual(document.get("status"), "live")

    def test_rejects_live_status_without_evidence(self) -> None:
        document = gate.load_gate(gate.DEFAULT_GATE)
        document["status"] = "live"
        document["evidence"] = "development-records/live-evidence/missing-inference-incluster-e2e.json"
        with self.assertRaises(SystemExit):
            gate.validate_contract(document)

    def test_accepts_live_status_with_redacted_evidence(self) -> None:
        document = gate.load_gate(gate.DEFAULT_GATE)
        document["status"] = "live"
        evidence = {
            "profile": gate.PROFILE,
            "status": "passed",
            "gateway": gate.GATEWAY_EVIDENCE,
            "auth_mode": "auth_service",
            "gpu_ready": False,
            "runtime_ready": False,
            "checks": [{"id": item, "status": "passed"} for item in sorted(gate.REQUIRED_CHECKS)],
        }
        target = gate.DEFAULT_EVIDENCE.with_name("inference-incluster-e2e-live-test.json")
        target.parent.mkdir(parents=True, exist_ok=True)
        target.write_text(json.dumps(evidence), encoding="utf-8")
        self.addCleanup(target.unlink, missing_ok=True)
        document["evidence"] = str(target.relative_to(gate.ROOT))
        gate.validate_contract(document)

    def test_rejects_second_gateway_evidence(self) -> None:
        evidence = {
            "profile": gate.PROFILE,
            "status": "passed",
            "gateway": "in-cluster-ani-inference-gateway-not-production-ani-gateway",
            "auth_mode": "auth_service",
            "checks": [{"id": item, "status": "passed"} for item in sorted(gate.REQUIRED_CHECKS)],
        }
        target = Path("/tmp/inference-incluster-e2e-bad-gateway.json")
        target.write_text(json.dumps(evidence), encoding="utf-8")
        self.addCleanup(target.unlink, missing_ok=True)
        with self.assertRaises(SystemExit):
            gate.validate_evidence(target)

    def test_rejects_dev_auth_mode_evidence(self) -> None:
        evidence = {
            "profile": gate.PROFILE,
            "status": "passed",
            "gateway": gate.GATEWAY_EVIDENCE,
            "auth_mode": "dev",
            "checks": [{"id": item, "status": "passed"} for item in sorted(gate.REQUIRED_CHECKS)],
        }
        target = Path("/tmp/inference-incluster-e2e-bad-auth.json")
        target.write_text(json.dumps(evidence), encoding="utf-8")
        self.addCleanup(target.unlink, missing_ok=True)
        with self.assertRaises(SystemExit):
            gate.validate_evidence(target)


if __name__ == "__main__":
    unittest.main()
