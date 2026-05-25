#!/usr/bin/env python3
"""Tests for the Sprint 5 vCluster upgrade live validation gate."""

from __future__ import annotations

import json
import tempfile
import unittest
from pathlib import Path
from unittest.mock import patch

import validate_vcluster_upgrade_live_gate as gate


class FakeRunner:
    def __init__(self) -> None:
        self.commands: list[list[str]] = []
        self.posts: list[tuple[str, dict[str, object], str]] = []

    def run(self, command: list[str]) -> str:
        self.commands.append(command)
        joined = " ".join(command)
        if joined.startswith("helm get values"):
            return json.dumps({"controlPlane": {"distro": {"k8s": {"version": "v1.31.0"}}}})
        if command[0] == "vcluster":
            return "\n".join(
                [
                    "apiVersion: v1",
                    "clusters:",
                    "- name: k8sclu-live",
                    "  cluster:",
                    "    server: https://k8sclu-live.example",
                    "users:",
                    "- name: k8sclu-live",
                    "  user:",
                    "    token: tenant-token",
                ]
            )
        if command[0] == "kubectl":
            return '{"major":"1","minor":"31","gitVersion":"v1.31.0"}'
        raise AssertionError(f"unexpected command: {joined}")

    def post_json(self, url: str, payload: dict[str, object], bearer_token: str) -> dict[str, object]:
        self.posts.append((url, payload, bearer_token))
        if url.endswith("/upgrade"):
            return {
                "id": "k8sclu-live",
                "version": "v1.31.0",
                "state": "running",
                "dev_profile": {"mode": "real", "provider": "vcluster", "real_provider": True},
            }
        if url.endswith("/proxy"):
            return {"status_code": 200, "headers": {"x-upstream": "vcluster"}, "body": {"gitVersion": "v1.31.0"}}
        raise AssertionError(f"unexpected JSON URL: {url}")


class VClusterUpgradeLiveGateTest(unittest.TestCase):
    def test_contract_gate_defines_core_helm_kubeconfig_kubectl_and_proxy_upgrade_checks(self) -> None:
        document = gate.load_gate(gate.DEFAULT_GATE)

        gate.validate_contract(document)

        check_ids = {check["id"] for check in document["live_checks"]}
        self.assertIn("core-upgrade-cluster", check_ids)
        self.assertIn("helm-values-target-version", check_ids)
        self.assertIn("vcluster-kubeconfig-after-upgrade", check_ids)
        self.assertIn("kubectl-version-after-upgrade", check_ids)
        self.assertIn("core-proxy-version-after-upgrade", check_ids)

    def test_live_gate_runs_core_upgrade_helm_values_kubectl_and_core_proxy(self) -> None:
        runner = FakeRunner()
        result = gate.run_live(
            gate.LiveConfig(
                tenant_id="tenant-a",
                cluster_id="k8sclu-live",
                gateway_url="http://127.0.0.1:3000/api/v1",
                ani_bearer_token="ani-token",
                target_version="v1.31.0",
                vcluster_server="https://k8sclu-live.example",
                work_dir=Path("/tmp"),
            ),
            runner=runner,
        )

        self.assertEqual(result["status"], "passed")
        self.assertEqual(result["target_version"], "v1.31.0")
        self.assertEqual(
            runner.posts[0],
            (
                "http://127.0.0.1:3000/api/v1/k8s-clusters/k8sclu-live/upgrade",
                {"idempotency_key": "live-upgrade-k8sclu-live-v1.31.0", "version": "v1.31.0"},
                "ani-token",
            ),
        )
        self.assertEqual(
            runner.commands[0],
            ["helm", "get", "values", "k8sclu-live", "--namespace", "ani-tenant-tenant-a", "-a", "-o", "json"],
        )
        self.assertEqual(runner.commands[1][0], "vcluster")
        self.assertEqual(runner.commands[2][0:2], ["kubectl", "--kubeconfig"])
        self.assertEqual(runner.posts[1][1]["path"], "/version")

    def test_cli_live_mode_rejects_missing_gateway_config(self) -> None:
        with patch.object(gate, "run_live") as run_live:
            with patch("sys.argv", ["validate_vcluster_upgrade_live_gate.py", "--live"]):
                with self.assertRaises(SystemExit):
                    gate.main()
        run_live.assert_not_called()

    def test_cli_live_mode_writes_evidence_json_when_requested(self) -> None:
        fake_evidence = {
            "status": "passed",
            "target_version": "v1.31.0",
            "kubeconfig": "/tmp/k8sclu-live-upgrade.kubeconfig",
            "proxy_status": 200,
        }
        with tempfile.TemporaryDirectory() as tmpdir:
            output = Path(tmpdir) / "vcluster-upgrade-evidence.json"

            with patch.object(gate, "validate_live_config"):
                with patch.object(gate, "run_live", return_value=fake_evidence):
                    with patch(
                        "sys.argv",
                        [
                            "validate_vcluster_upgrade_live_gate.py",
                            "--live",
                            "--tenant-id",
                            "tenant-a",
                            "--cluster-id",
                            "k8sclu-live",
                            "--gateway-url",
                            "http://127.0.0.1:3000/api/v1",
                            "--ani-bearer-token",
                            "ani-token",
                            "--target-version",
                            "v1.31.0",
                            "--evidence-output",
                            str(output),
                        ],
                    ):
                        try:
                            gate.main()
                        except SystemExit:
                            pass

            self.assertTrue(output.exists())
            self.assertEqual(fake_evidence, json.loads(output.read_text(encoding="utf-8")))


if __name__ == "__main__":
    unittest.main()
