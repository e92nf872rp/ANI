#!/usr/bin/env python3
from __future__ import annotations

import json
import unittest
from pathlib import Path

import validate_inference_gpu_vllm_eager_live_gate as gate


def _evidence(**overrides: object) -> dict:
    body = {
        "profile": gate.PROFILE,
        "status": "passed",
        "entry": gate.GATEWAY_EVIDENCE,
        "gateway": gate.GATEWAY_EVIDENCE,
        "auth_mode": "auth_service",
        "launch": "enforce-eager",
        "pod_ready": True,
        "health": 200,
        "lws_live": "skipped",
        "gpu_ready": False,
        "runtime_ready": False,
        "checks": [{"id": item, "status": "passed"} for item in sorted(gate.REQUIRED_CHECKS)],
    }
    body.update(overrides)
    return body


class InferenceGPUVLLMEagerLiveGateTests(unittest.TestCase):
    def test_repo_contract_is_valid(self) -> None:
        document = gate.load_gate(gate.DEFAULT_GATE)
        gate.validate_contract(document)
        self.assertEqual(document.get("status"), "live")

    def test_rejects_live_status_without_evidence(self) -> None:
        document = gate.load_gate(gate.DEFAULT_GATE)
        document["status"] = "live"
        document["evidence"] = "development-records/live-evidence/missing-inference-gpu-vllm-eager.json"
        with self.assertRaises(SystemExit):
            gate.validate_contract(document)

    def test_accepts_live_status_with_redacted_evidence(self) -> None:
        document = gate.load_gate(gate.DEFAULT_GATE)
        document["status"] = "live"
        target = gate.DEFAULT_EVIDENCE.with_name("inference-gpu-vllm-eager-live-test.json")
        target.parent.mkdir(parents=True, exist_ok=True)
        target.write_text(json.dumps(_evidence()), encoding="utf-8")
        self.addCleanup(target.unlink, missing_ok=True)
        document["evidence"] = str(target.relative_to(gate.ROOT))
        gate.validate_contract(document)

    def test_rejects_missing_enforce_eager(self) -> None:
        target = Path("/tmp/inference-gpu-vllm-eager-no-flag.json")
        target.write_text(json.dumps(_evidence(launch="default")), encoding="utf-8")
        self.addCleanup(target.unlink, missing_ok=True)
        with self.assertRaises(SystemExit):
            gate.validate_evidence(target)

    def test_rejects_gpu_ready_evidence(self) -> None:
        target = Path("/tmp/inference-gpu-vllm-eager-ready.json")
        target.write_text(json.dumps(_evidence(gpu_ready=True)), encoding="utf-8")
        self.addCleanup(target.unlink, missing_ok=True)
        with self.assertRaises(SystemExit):
            gate.validate_evidence(target)


if __name__ == "__main__":
    unittest.main()
