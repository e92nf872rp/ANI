#!/usr/bin/env python3
"""Tests for the Sprint 5 Kube-OVN network live validation gate."""

from __future__ import annotations

import json
import tempfile
import unittest
from pathlib import Path
from unittest.mock import patch

import validate_kubeovn_network_live_gate as gate


class FakeRunner:
    def __init__(self) -> None:
        self.commands: list[list[str]] = []

    def run(self, command: list[str], input_text: str | None = None) -> str:
        self.commands.append(command)
        joined = " ".join(command)
        if "get crd" in joined:
            return '{"metadata":{"name":"vpcs.kubeovn.io"}}'
        if "get vpc" in joined:
            return json.dumps(
                {
                    "kind": "Vpc",
                    "metadata": {"name": "vpc-ani-live-net"},
                    "status": {"conditions": [{"type": "Ready", "status": "True"}]},
                }
            )
        if "get subnet" in joined:
            return json.dumps(
                {
                    "kind": "Subnet",
                    "metadata": {"name": "subnet-ani-live-subnet"},
                    "status": {"conditions": [{"type": "Ready", "status": "True"}]},
                }
            )
        if "get networkpolicy" in joined:
            return '{"kind":"NetworkPolicy","metadata":{"name":"sg-ani-live-sg"}}'
        if "get service" in joined:
            return '{"kind":"Service","metadata":{"name":"lb-ani-live-lb"},"spec":{"type":"LoadBalancer"}}'
        if "auth can-i" in joined:
            return "yes\n"
        if "apply -f -" in joined:
            if not input_text or "apiVersion" not in input_text:
                raise AssertionError("apply command must receive a manifest")
            return "created\n"
        raise AssertionError(f"unexpected command: {joined}")


class KubeOVNNetworkLiveGateTest(unittest.TestCase):
    def test_contract_gate_defines_kubeovn_vpc_subnet_networkpolicy_and_lb_checks(self) -> None:
        document = gate.load_gate(gate.DEFAULT_GATE)

        gate.validate_contract(document)

        check_ids = {check["id"] for check in document["live_checks"]}
        self.assertIn("kubeovn-crds-ready", check_ids)
        self.assertIn("kubeovn-vpc-created", check_ids)
        self.assertIn("kubeovn-subnet-created", check_ids)
        self.assertIn("networkpolicy-created", check_ids)
        self.assertIn("service-lb-created", check_ids)

    def test_live_gate_applies_and_observes_kubeovn_network_resources(self) -> None:
        runner = FakeRunner()
        result = gate.run_live(
            gate.LiveConfig(
                tenant_id="tenant-a",
                vpc_name="ani-live-net",
                subnet_name="ani-live-subnet",
                security_group_name="ani-live-sg",
                load_balancer_name="ani-live-lb",
                namespace="ani-tenant-tenant-a",
            ),
            runner=runner,
        )

        self.assertEqual(result["status"], "passed")
        self.assertEqual(result["vpc"], "vpc-ani-live-net")
        self.assertEqual(result["subnet"], "subnet-ani-live-subnet")
        self.assertIn(["kubectl", "get", "crd", "vpcs.kubeovn.io", "-o", "json"], runner.commands)
        apply_commands = [command for command in runner.commands if command[-2:] == ["-f", "-"]]
        self.assertEqual(len(apply_commands), 4)

    def test_cli_live_mode_rejects_missing_tenant_config(self) -> None:
        with patch.object(gate, "run_live") as run_live:
            with patch("sys.argv", ["validate_kubeovn_network_live_gate.py", "--live", "--tenant-id", ""]):
                with self.assertRaises(SystemExit):
                    gate.main()
        run_live.assert_not_called()

    def test_cli_live_mode_writes_evidence_json_when_requested(self) -> None:
        fake_evidence = {
            "status": "passed",
            "namespace": "ani-tenant-tenant-a",
            "vpc": "vpc-ani-live-net",
            "subnet": "subnet-ani-live-subnet",
            "security_group": "sg-ani-live-sg",
            "load_balancer": "lb-ani-live-lb",
        }
        with tempfile.TemporaryDirectory() as tmpdir:
            output = Path(tmpdir) / "kubeovn-network-live-evidence.json"
            with patch.object(gate, "validate_live_config"):
                with patch.object(gate, "run_live", return_value=fake_evidence):
                    with patch(
                        "sys.argv",
                        [
                            "validate_kubeovn_network_live_gate.py",
                            "--live",
                            "--tenant-id",
                            "tenant-a",
                            "--namespace",
                            "ani-tenant-tenant-a",
                            "--vpc-name",
                            "ani-live-net",
                            "--subnet-name",
                            "ani-live-subnet",
                            "--security-group-name",
                            "ani-live-sg",
                            "--load-balancer-name",
                            "ani-live-lb",
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
