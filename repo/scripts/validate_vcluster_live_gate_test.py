#!/usr/bin/env python3
"""Tests for the Sprint 5 vCluster live validation gate."""

from __future__ import annotations

import json
import tempfile
import unittest
from unittest.mock import patch
from pathlib import Path

import validate_vcluster_live_gate as gate


class FakeRunner:
    def __init__(self) -> None:
        self.commands: list[list[str]] = []
        self.posts: list[tuple[str, dict[str, object], str]] = []

    def run(self, command: list[str], env: dict[str, str] | None = None) -> str:
        self.commands.append(command)
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
            return '{"major":"1","minor":"30"}'
        return "ok"

    def post_json(self, url: str, payload: dict[str, object], bearer_token: str) -> dict[str, object]:
        self.posts.append((url, payload, bearer_token))
        return {"status_code": 200, "headers": {"x-upstream": "vcluster"}, "body": {"kind": "Status"}}


class VClusterLiveGateTest(unittest.TestCase):
    def test_contract_gate_defines_helm_kubeconfig_kubectl_and_proxy_checks(self) -> None:
        document = gate.load_gate(gate.DEFAULT_GATE)

        gate.validate_contract(document)

        check_ids = {check["id"] for check in document["live_checks"]}
        self.assertIn("helm-install", check_ids)
        self.assertIn("vcluster-kubeconfig", check_ids)
        self.assertIn("kubectl-version", check_ids)
        self.assertIn("core-proxy-version", check_ids)

    def test_live_gate_runs_helm_connect_kubectl_and_core_proxy(self) -> None:
        runner = FakeRunner()
        result = gate.run_live(
            gate.LiveConfig(
                tenant_id="tenant-a",
                cluster_id="k8sclu-live",
                gateway_url="http://127.0.0.1:3000/api/v1",
                ani_bearer_token="ani-token",
                vcluster_server="https://k8sclu-live.example",
                work_dir=Path("/tmp"),
            ),
            runner=runner,
        )

        self.assertEqual(result["status"], "passed")
        self.assertEqual(
            runner.commands[0],
            [
                "helm",
                "upgrade",
                "--install",
                "k8sclu-live",
                "vcluster",
                "--repo",
                "https://charts.loft.sh",
                "--namespace",
                "ani-tenant-tenant-a",
                "--create-namespace",
                "--repository-config=",
                "--set",
                "sync.toHost.service.enabled=true",
            ],
        )
        self.assertEqual(
            runner.commands[1],
            [
                "vcluster",
                "connect",
                "k8sclu-live",
                "--namespace",
                "ani-tenant-tenant-a",
                "--print",
                "--server",
                "https://k8sclu-live.example",
            ],
        )
        self.assertEqual(runner.commands[2][0:2], ["kubectl", "--kubeconfig"])
        self.assertEqual(runner.commands[2][3:], ["get", "--raw", "/version"])
        self.assertEqual(
            runner.posts[0],
            (
                "http://127.0.0.1:3000/api/v1/k8s-clusters/k8sclu-live/proxy",
                {
                    "idempotency_key": "live-proxy-k8sclu-live-version",
                    "method": "GET",
                    "path": "/version",
                    "query": {},
                    "body": {},
                },
                "ani-token",
            ),
        )

    def test_cli_live_mode_rejects_missing_gateway_before_running_commands(self) -> None:
        with patch.object(gate, "run_live") as run_live:
            with patch(
                "sys.argv",
                [
                    "validate_vcluster_live_gate.py",
                    "--live",
                    "--tenant-id",
                    "tenant-a",
                    "--cluster-id",
                    "k8sclu-live",
                ],
            ):
                with self.assertRaises(SystemExit):
                    gate.main()
        run_live.assert_not_called()

    def test_cli_live_mode_writes_evidence_json_when_requested(self) -> None:
        fake_evidence = {"status": "passed", "kubeconfig": "/tmp/k8sclu-live.kubeconfig", "proxy_status": 200}
        with tempfile.TemporaryDirectory() as tmpdir:
            output = Path(tmpdir) / "vcluster-live-evidence.json"

            with patch.object(gate, "validate_live_config"):
                with patch.object(gate, "run_live", return_value=fake_evidence):
                    with patch(
                        "sys.argv",
                        [
                            "validate_vcluster_live_gate.py",
                            "--live",
                            "--tenant-id",
                            "tenant-a",
                            "--cluster-id",
                            "k8sclu-live",
                            "--gateway-url",
                            "http://127.0.0.1:3000/api/v1",
                            "--ani-bearer-token",
                            "ani-token",
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
