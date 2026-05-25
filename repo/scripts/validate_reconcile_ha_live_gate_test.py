#!/usr/bin/env python3
"""Tests for the Sprint 5 reconcile controller HA live validation gate."""

from __future__ import annotations

import json
import tempfile
import unittest
from pathlib import Path
from unittest.mock import patch

import validate_reconcile_ha_live_gate as gate


class FakeLiveRunner:
    def __init__(self) -> None:
        self.commands: list[list[str]] = []
        self.metrics_urls: list[str] = []
        self.lease_holders = ["worker-a", "worker-b"]

    def run(self, command: list[str]) -> str:
        self.commands.append(command)
        joined = " ".join(command)
        if "get pods" in joined:
            return (
                "worker-a ani-reconcile-worker-a\n"
                "worker-b ani-reconcile-worker-b\n"
            )
        if "delete pod ani-reconcile-worker-a" in joined:
            return "pod \"ani-reconcile-worker-a\" deleted\n"
        raise AssertionError(f"unexpected command: {joined}")

    def query_lease_holder(self, config: gate.LiveConfig) -> str:
        return self.lease_holders.pop(0)

    def fetch_metrics(self, url: str) -> str:
        self.metrics_urls.append(url)
        return (
            "# HELP ani_workload_reconcile_leader_active active leader\n"
            "ani_workload_reconcile_leader_active{leader=\"true\"} 1\n"
            "ani_workload_reconcile_ticks_total 7\n"
            "ani_workload_reconcile_successes_total 7\n"
            "ani_workload_reconcile_failures_total 0\n"
        )


class ReconcileHALiveGateTest(unittest.TestCase):
    def test_contract_gate_defines_leader_failover_and_metrics_checks(self) -> None:
        document = gate.load_gate(gate.DEFAULT_GATE)

        gate.validate_contract(document)

        check_ids = {check["id"] for check in document["live_checks"]}
        self.assertIn("deploy-two-reconcile-workers", check_ids)
        self.assertIn("leader-lease-acquired", check_ids)
        self.assertIn("leader-metrics-active", check_ids)
        self.assertIn("kill-leader-pod", check_ids)
        self.assertIn("follower-acquires-lease", check_ids)
        self.assertIn("reconcile-continues-after-failover", check_ids)

    def test_live_gate_observes_lease_deletes_leader_and_confirms_failover(self) -> None:
        runner = FakeLiveRunner()
        result = gate.run_live(
            gate.LiveConfig(
                database_url="postgres://ani:ani@127.0.0.1:5432/ani",
                namespace="ani-system",
                worker_selector="app=ani-reconcile-worker",
                metrics_url="http://127.0.0.1:18080/metrics",
            ),
            runner=runner,
        )

        self.assertEqual(result["status"], "passed")
        self.assertEqual(result["namespace"], "ani-system")
        self.assertEqual(result["worker_selector"], "app=ani-reconcile-worker")
        self.assertEqual(result["lease_name"], "workload-reconcile-controller")
        self.assertEqual(result["metrics_url"], "http://127.0.0.1:18080/metrics")
        self.assertEqual(result["initial_leader"], "worker-a")
        self.assertEqual(result["new_leader"], "worker-b")
        self.assertEqual(runner.commands[0], ["kubectl", "get", "pods", "-n", "ani-system", "-l", "app=ani-reconcile-worker", "-o", "custom-columns=IDENTITY:.metadata.labels.ani\\.kubercloud\\.io/reconcile-identity,NAME:.metadata.name", "--no-headers"])
        self.assertEqual(runner.commands[1], ["kubectl", "delete", "pod", "ani-reconcile-worker-a", "-n", "ani-system"])
        self.assertEqual(runner.metrics_urls, ["http://127.0.0.1:18080/metrics", "http://127.0.0.1:18080/metrics"])

    def test_cli_live_mode_rejects_missing_database_config(self) -> None:
        with patch.object(gate, "run_live") as run_live:
            with patch("sys.argv", ["validate_reconcile_ha_live_gate.py", "--live"]):
                with self.assertRaises(SystemExit):
                    gate.main()
        run_live.assert_not_called()

    def test_cli_live_mode_writes_evidence_json_when_requested(self) -> None:
        fake_evidence = {
            "status": "passed",
            "namespace": "ani-system",
            "lease_name": "workload-reconcile-controller",
            "worker_selector": "app=ani-reconcile-worker",
            "metrics_url": "http://127.0.0.1:18080/metrics",
            "initial_leader": "worker-a",
            "new_leader": "worker-b",
            "deleted_pod": "ani-reconcile-worker-a",
        }
        with tempfile.TemporaryDirectory() as tmpdir:
            output = Path(tmpdir) / "reconcile-ha-live-evidence.json"
            with patch.object(gate, "validate_live_config"):
                with patch.object(gate, "run_live", return_value=fake_evidence):
                    with patch(
                        "sys.argv",
                        [
                            "validate_reconcile_ha_live_gate.py",
                            "--live",
                            "--database-url",
                            "postgres://ani:ani@127.0.0.1:5432/ani",
                            "--namespace",
                            "ani-system",
                            "--metrics-url",
                            "http://127.0.0.1:18080/metrics",
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
