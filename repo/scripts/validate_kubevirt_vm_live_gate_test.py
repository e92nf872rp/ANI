#!/usr/bin/env python3
"""Tests for the Sprint 5 KubeVirt VM live validation gate."""

from __future__ import annotations

import json
import tempfile
import unittest
from pathlib import Path
from unittest.mock import patch

import validate_kubevirt_vm_live_gate as gate


class FakeRunner:
    def __init__(self) -> None:
        self.commands: list[list[str]] = []

    def run(self, command: list[str], input_text: str | None = None) -> str:
        self.commands.append(command)
        joined = " ".join(command)
        if "get crd" in joined:
            return '{"metadata":{"name":"virtualmachines.kubevirt.io"}}'
        if "get kubevirt" in joined:
            return json.dumps(
                {
                    "items": [
                        {
                            "kind": "KubeVirt",
                            "metadata": {"name": "kubevirt", "namespace": "kubevirt"},
                            "status": {"conditions": [{"type": "Available", "status": "True"}]},
                        }
                    ]
                }
            )
        if "apply -f -" in joined:
            if not input_text or "VirtualMachine" not in input_text:
                raise AssertionError("apply command must receive a KubeVirt VM manifest")
            return "virtualmachine.kubevirt.io/ani-live-vm created\n"
        if "patch virtualmachine" in joined:
            return "virtualmachine.kubevirt.io/ani-live-vm patched\n"
        if "wait" in joined:
            return "condition met\n"
        if "get virtualmachine " in joined:
            return '{"kind":"VirtualMachine","metadata":{"name":"ani-live-vm"}}'
        if "get virtualmachineinstance" in joined:
            return json.dumps(
                {
                    "kind": "VirtualMachineInstance",
                    "metadata": {"name": "ani-live-vm"},
                    "status": {"phase": "Running", "conditions": [{"type": "Ready", "status": "True"}]},
                }
            )
        if "get --raw" in joined:
            return "subresource accepted\n"
        if "delete virtualmachine" in joined:
            return "virtualmachine.kubevirt.io/ani-live-vm deleted\n"
        raise AssertionError(f"unexpected command: {joined}")


class KubeVirtVMLiveGateTest(unittest.TestCase):
    def test_contract_gate_defines_vm_start_stop_console_and_delete_checks(self) -> None:
        document = gate.load_gate(gate.DEFAULT_GATE)

        gate.validate_contract(document)

        check_ids = {check["id"] for check in document["live_checks"]}
        self.assertIn("kubevirt-crds-ready", check_ids)
        self.assertIn("kubevirt-control-plane-available", check_ids)
        self.assertIn("kubevirt-vm-created", check_ids)
        self.assertIn("kubevirt-vmi-ready", check_ids)
        self.assertIn("kubevirt-vnc-subresource", check_ids)
        self.assertIn("kubevirt-console-subresource", check_ids)
        self.assertIn("kubevirt-vm-stopped", check_ids)
        self.assertIn("kubevirt-vm-deleted", check_ids)

    def test_live_gate_creates_starts_checks_console_stops_and_deletes_vm(self) -> None:
        runner = FakeRunner()
        result = gate.run_live(
            gate.LiveConfig(
                tenant_id="tenant-a",
                namespace="ani-tenant-tenant-a",
                vm_name="ani-live-vm",
            ),
            runner=runner,
        )

        self.assertEqual(result["status"], "passed")
        self.assertEqual(result["vm"], "ani-live-vm")
        self.assertIn(["kubectl", "get", "crd", "virtualmachines.kubevirt.io", "-o", "json"], runner.commands)
        self.assertTrue(any(command[-2:] == ["-f", "-"] for command in runner.commands))
        self.assertTrue(any("vnc" in " ".join(command) for command in runner.commands))
        self.assertTrue(any("console" in " ".join(command) for command in runner.commands))
        joined_commands = [" ".join(command) for command in runner.commands]
        stop_patch_indexes = [
            index
            for index, command in enumerate(joined_commands)
            if "patch virtualmachine" in command and '"running":false' in command
        ]
        self.assertTrue(stop_patch_indexes, "live gate must stop the KubeVirt VM before delete")
        stop_patch_index = stop_patch_indexes[0]
        delete_index = next(index for index, command in enumerate(joined_commands) if "delete virtualmachine" in command)
        self.assertLess(stop_patch_index, delete_index)
        self.assertTrue(any("wait --for=delete virtualmachineinstance/ani-live-vm" in command for command in joined_commands))

    def test_cli_live_mode_rejects_missing_tenant_config(self) -> None:
        with patch.object(gate, "run_live") as run_live:
            with patch("sys.argv", ["validate_kubevirt_vm_live_gate.py", "--live", "--tenant-id", ""]):
                with self.assertRaises(SystemExit):
                    gate.main()
        run_live.assert_not_called()

    def test_cli_live_mode_writes_evidence_json_when_requested(self) -> None:
        fake_evidence = {
            "status": "passed",
            "namespace": "ani-tenant-tenant-a",
            "vm": "ani-live-vm",
        }
        with tempfile.TemporaryDirectory() as tmpdir:
            output = Path(tmpdir) / "kubevirt-vm-live-evidence.json"
            with patch.object(gate, "validate_live_config"):
                with patch.object(gate, "run_live", return_value=fake_evidence):
                    with patch(
                        "sys.argv",
                        [
                            "validate_kubevirt_vm_live_gate.py",
                            "--live",
                            "--tenant-id",
                            "tenant-a",
                            "--namespace",
                            "ani-tenant-tenant-a",
                            "--vm-name",
                            "ani-live-vm",
                            "--evidence-output",
                            str(output),
                        ],
                    ):
                        try:
                            gate.main()
                        except SystemExit:
                            pass

            self.assertTrue(output.exists())
            self.assertEqual(json.loads(output.read_text(encoding="utf-8")), fake_evidence)


if __name__ == "__main__":
    unittest.main()
