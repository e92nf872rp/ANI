#!/usr/bin/env python3
"""Unit tests for sandbox live gate contract validation."""

from __future__ import annotations

import tempfile
import unittest
from copy import deepcopy
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

    def test_missing_file_safety_check_rejected(self) -> None:
        document = gate.load_gate(gate.DEFAULT_GATE)
        document["live_checks"] = [
            check for check in document["live_checks"]
            if check.get("id") != "core-instance-sandbox-file-safety"
        ]
        with self.assertRaises(SystemExit):
            gate.validate_contract(document)

    def test_workspace_mount_requires_isolated_emptydir(self) -> None:
        deployment = {
            "spec": {
                "template": {
                    "spec": {
                        "containers": [{
                            "volumeMounts": [{"name": "sandbox-workspace", "mountPath": "/workspace"}],
                        }],
                        "volumes": [{"name": "sandbox-workspace", "emptyDir": {}}],
                    },
                },
            },
        }
        gate.validate_workspace_mount(deployment)

        missing_mount = deepcopy(deployment)
        missing_mount["spec"]["template"]["spec"]["containers"][0]["volumeMounts"] = []
        with self.assertRaises(SystemExit):
            gate.validate_workspace_mount(missing_mount)

        host_path = deepcopy(deployment)
        host_path["spec"]["template"]["spec"]["volumes"][0] = {
            "name": "sandbox-workspace",
            "hostPath": {"path": "/tmp/workspace"},
        }
        with self.assertRaises(SystemExit):
            gate.validate_workspace_mount(host_path)

    def test_write_evidence_roundtrip(self) -> None:
        with tempfile.TemporaryDirectory() as tmp:
            path = Path(tmp) / "evidence.json"
            gate.write_evidence(
                path,
                {
                    "status": "passed",
                    "kind": "sandbox",
                    "ports_preview_url": "http://192.0.2.10:32000/preview",
                },
            )
            text = path.read_text(encoding="utf-8")
            self.assertIn('"id": "instance-sandbox-live-gate"', text)
            self.assertIn('"profile": "INSTANCE-SANDBOX-LIVE-GATE-A"', text)
            self.assertIn('http://<redacted-host>:32000/preview', text)
            self.assertNotIn("192.0.2.10", text)


if __name__ == "__main__":
    unittest.main()
