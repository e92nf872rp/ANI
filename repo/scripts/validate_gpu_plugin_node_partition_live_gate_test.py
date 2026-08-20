#!/usr/bin/env python3
from __future__ import annotations

import json
import unittest
from pathlib import Path

import validate_gpu_plugin_node_partition_live_gate as gate


def _evidence(**overrides: object) -> dict:
    body = {
        "profile": gate.PROFILE,
        "status": "passed",
        "gpu_ready": False,
        "runtime_ready": False,
        "partition": {
            "wholecard_nodes": ["node-a", "node-b"],
            "vgpu_nodes": ["node-c"],
        },
        "checks": [{"id": item, "status": "passed"} for item in sorted(gate.REQUIRED_CHECKS)],
    }
    body.update(overrides)
    return body


class GPUPluginNodePartitionLiveGateTests(unittest.TestCase):
    def test_repo_contract_is_valid(self) -> None:
        document = gate.load_gate(gate.DEFAULT_GATE)
        gate.validate_contract(document)
        self.assertEqual(document.get("status"), "live")

    def test_rejects_live_status_without_evidence(self) -> None:
        document = gate.load_gate(gate.DEFAULT_GATE)
        document["status"] = "live"
        document["evidence"] = "development-records/live-evidence/missing-gpu-plugin-partition.json"
        with self.assertRaises(SystemExit):
            gate.validate_contract(document)

    def test_accepts_live_status_with_redacted_evidence(self) -> None:
        document = gate.load_gate(gate.DEFAULT_GATE)
        document["status"] = "live"
        target = gate.DEFAULT_EVIDENCE.with_name("gpu-plugin-node-partition-live-test.json")
        target.parent.mkdir(parents=True, exist_ok=True)
        target.write_text(json.dumps(_evidence()), encoding="utf-8")
        self.addCleanup(target.unlink, missing_ok=True)
        document["evidence"] = str(target.relative_to(gate.ROOT))
        gate.validate_contract(document)

    def test_rejects_overlapping_partition(self) -> None:
        target = Path("/tmp/gpu-plugin-partition-overlap.json")
        target.write_text(
            json.dumps(
                _evidence(
                    partition={
                        "wholecard_nodes": ["node-a"],
                        "vgpu_nodes": ["node-a"],
                    }
                )
            ),
            encoding="utf-8",
        )
        self.addCleanup(target.unlink, missing_ok=True)
        with self.assertRaises(SystemExit):
            gate.validate_evidence(target)

    def test_rejects_gpu_ready_evidence(self) -> None:
        target = Path("/tmp/gpu-plugin-partition-ready.json")
        target.write_text(json.dumps(_evidence(gpu_ready=True)), encoding="utf-8")
        self.addCleanup(target.unlink, missing_ok=True)
        with self.assertRaises(SystemExit):
            gate.validate_evidence(target)


if __name__ == "__main__":
    unittest.main()
