#!/usr/bin/env python3
from __future__ import annotations

import json
import unittest
from pathlib import Path

import validate_inference_gpu_memory_live_gate as gate


def _evidence(**overrides: object) -> dict:
    body = {
        "profile": gate.PROFILE,
        "status": "passed",
        "entry": gate.GATEWAY_EVIDENCE,
        "gateway": gate.GATEWAY_EVIDENCE,
        "auth_mode": "auth_service",
        "advertised_spec": gate.MODEL_SPEC,
        "advertised_specs": [gate.MODEL_SPEC],
        "memory_zero_http_status": 400,
        "whole_card_nvidia_gpu": "1",
        "vgpu_on_whole_card": False,
        "nvidia_gpu_on_vgpu": False,
        "vgpu_number": "1",
        "vgpu_memory": "1228",
        "lws_live": "skipped",
        "gpu_ready": False,
        "runtime_ready": False,
        "checks": [{"id": item, "status": "passed"} for item in sorted(gate.REQUIRED_CHECKS)],
    }
    body.update(overrides)
    return body


class InferenceGPUMemoryLiveGateTests(unittest.TestCase):
    def test_repo_contract_is_valid(self) -> None:
        document = gate.load_gate(gate.DEFAULT_GATE)
        gate.validate_contract(document)

    def test_rejects_live_status_without_evidence(self) -> None:
        document = gate.load_gate(gate.DEFAULT_GATE)
        document["status"] = "live"
        document["evidence"] = "development-records/live-evidence/missing-inference-gpu-memory.json"
        with self.assertRaises(SystemExit):
            gate.validate_contract(document)

    def test_accepts_live_status_with_redacted_evidence(self) -> None:
        document = gate.load_gate(gate.DEFAULT_GATE)
        document["status"] = "live"
        target = gate.DEFAULT_EVIDENCE.with_name("inference-gpu-memory-live-test.json")
        target.parent.mkdir(parents=True, exist_ok=True)
        target.write_text(json.dumps(_evidence()), encoding="utf-8")
        self.addCleanup(target.unlink, missing_ok=True)
        document["evidence"] = str(target.relative_to(gate.ROOT))
        gate.validate_contract(document)

    def test_rejects_ready_claims(self) -> None:
        target = Path("/tmp/inference-gpu-memory-ready.json")
        target.write_text(json.dumps(_evidence(gpu_ready=True)), encoding="utf-8")
        self.addCleanup(target.unlink, missing_ok=True)
        with self.assertRaises(SystemExit):
            gate.validate_evidence(target)

    def test_rejects_historical_spec_ids(self) -> None:
        target = Path("/tmp/inference-gpu-memory-legacy-spec.json")
        target.write_text(
            json.dumps(_evidence(advertised_specs=[gate.MODEL_SPEC, gate.MODEL_SPEC + "-8x"])),
            encoding="utf-8",
        )
        self.addCleanup(target.unlink, missing_ok=True)
        with self.assertRaises(SystemExit):
            gate.validate_evidence(target)


if __name__ == "__main__":
    unittest.main()
