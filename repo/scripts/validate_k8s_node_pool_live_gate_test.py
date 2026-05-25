#!/usr/bin/env python3
"""Tests for the Sprint 5 K8s node pool live validation gate."""

from __future__ import annotations

import json
import tempfile
import unittest
from pathlib import Path
from unittest.mock import patch

import validate_k8s_node_pool_live_gate as gate


class FakeLiveRunner:
    def __init__(self) -> None:
        self.json_calls: list[tuple[str, str, dict[str, object], str]] = []
        self.commands: list[tuple[list[str], str | None]] = []
        self.machine_deployment_replicas = 1

    def post_json(self, url: str, payload: dict[str, object], bearer_token: str) -> dict[str, object]:
        self.json_calls.append(("POST", url, payload, bearer_token))
        if url.endswith("/node-pools"):
            return {
                "id": "k8snp-live",
                "tenant_id": "tenant-a",
                "cluster_id": "k8sclu-live",
                "name": "gpu-pool",
                "node_count": 1,
                "instance_type": "gpu.l4.xlarge",
                "state": "running",
                "gpu": {"vendor": "nvidia", "model": "L4", "count": 1, "resource_name": "nvidia.com/gpu"},
                "dev_profile": {"mode": "real", "provider": "clusterapi-provider", "real_provider": True},
            }
        raise AssertionError(f"unexpected JSON URL: {url}")

    def patch_json(self, url: str, payload: dict[str, object], bearer_token: str) -> dict[str, object]:
        self.json_calls.append(("PATCH", url, payload, bearer_token))
        if url.endswith("/node-pools/k8snp-live"):
            self.machine_deployment_replicas = int(payload["node_count"])
            return {
                "id": "k8snp-live",
                "tenant_id": "tenant-a",
                "cluster_id": "k8sclu-live",
                "name": "gpu-pool",
                "node_count": payload["node_count"],
                "instance_type": payload["instance_type"],
                "state": "running",
                "gpu": payload["gpu"],
                "dev_profile": {"mode": "real", "provider": "clusterapi-provider", "real_provider": True},
            }
        raise AssertionError(f"unexpected JSON URL: {url}")

    def run(self, command: list[str], input_text: str | None = None) -> str:
        self.commands.append((command, input_text))
        joined = " ".join(command)
        if "get machinedeployment gpu-pool" in joined:
            return json.dumps(
                {
                    "metadata": {
                        "name": "gpu-pool",
                        "namespace": "ani-tenant-tenant-a",
                        "labels": {
                            "ani.kubercloud.io/node-pool-id": "k8snp-live",
                            "ani.kubercloud.io/gpu-vendor": "nvidia",
                        },
                    },
                    "spec": {
                        "replicas": self.machine_deployment_replicas,
                        "template": {"spec": {"gpu": {"count": 1, "resourceName": "nvidia.com/gpu"}}},
                    },
                }
            )
        if command[:2] == ["kubectl", "apply"] and input_text:
            return "pod/ani-node-pool-gpu-smoke created\n"
        if "wait --for=condition=PodScheduled pod/ani-node-pool-gpu-smoke" in joined:
            return "pod/ani-node-pool-gpu-smoke condition met\n"
        if joined.endswith("delete pod ani-node-pool-gpu-smoke -n ani-tenant-tenant-a --ignore-not-found=true"):
            return "pod deleted\n"
        raise AssertionError(f"unexpected command: {joined}")


class K8sNodePoolLiveGateTest(unittest.TestCase):
    def test_contract_gate_defines_core_clusterapi_scale_and_gpu_checks(self) -> None:
        document = gate.load_gate(gate.DEFAULT_GATE)

        gate.validate_contract(document)

        check_ids = {check["id"] for check in document["live_checks"]}
        self.assertIn("core-create-node-pool", check_ids)
        self.assertIn("clusterapi-machinedeployment-created", check_ids)
        self.assertIn("core-scale-node-pool", check_ids)
        self.assertIn("clusterapi-machinedeployment-scaled", check_ids)
        self.assertIn("gpu-workload-scheduled", check_ids)

    def test_live_gate_runs_core_node_pool_clusterapi_scale_and_gpu_scheduling_checks(self) -> None:
        runner = FakeLiveRunner()
        result = gate.run_live(
            gate.LiveConfig(
                tenant_id="tenant-a",
                cluster_id="k8sclu-live",
                gateway_url="http://127.0.0.1:3000/api/v1",
                ani_bearer_token="ani-token",
                node_pool_name="gpu-pool",
                instance_type="gpu.l4.xlarge",
                initial_node_count=1,
                scaled_node_count=3,
                gpu_vendor="nvidia",
                gpu_model="L4",
                gpu_count=1,
                gpu_resource_name="nvidia.com/gpu",
            ),
            runner=runner,
        )

        self.assertEqual(result["status"], "passed")
        self.assertEqual(result["node_pool_id"], "k8snp-live")
        self.assertEqual(result["scaled_replicas"], 3)
        self.assertEqual(runner.json_calls[0][0], "POST")
        self.assertEqual(runner.json_calls[0][1], "http://127.0.0.1:3000/api/v1/k8s-clusters/k8sclu-live/node-pools")
        self.assertEqual(runner.json_calls[1][0], "PATCH")
        self.assertEqual(runner.json_calls[1][1], "http://127.0.0.1:3000/api/v1/k8s-clusters/k8sclu-live/node-pools/k8snp-live")
        self.assertEqual(
            runner.commands[0][0],
            ["kubectl", "get", "machinedeployment", "gpu-pool", "-n", "ani-tenant-tenant-a", "-o", "json"],
        )
        self.assertIn("nvidia.com/gpu", runner.commands[-2][1] or "")

    def test_cli_live_mode_rejects_missing_gateway_config(self) -> None:
        with patch.object(gate, "run_live") as run_live:
            with patch("sys.argv", ["validate_k8s_node_pool_live_gate.py", "--live"]):
                with self.assertRaises(SystemExit):
                    gate.main()
        run_live.assert_not_called()

    def test_cli_live_mode_writes_evidence_json_when_requested(self) -> None:
        fake_evidence = {
            "status": "passed",
            "node_pool_id": "k8snp-live",
            "machine_deployment": "gpu-pool",
            "namespace": "ani-tenant-tenant-a",
            "scaled_replicas": 3,
            "gpu_workload": "ani-node-pool-gpu-smoke",
        }
        with tempfile.TemporaryDirectory() as tmpdir:
            output = Path(tmpdir) / "node-pool-live-evidence.json"
            with patch.object(gate, "validate_live_config"):
                with patch.object(gate, "run_live", return_value=fake_evidence):
                    with patch(
                        "sys.argv",
                        [
                            "validate_k8s_node_pool_live_gate.py",
                            "--live",
                            "--tenant-id",
                            "tenant-a",
                            "--cluster-id",
                            "k8sclu-live",
                            "--gateway-url",
                            "http://127.0.0.1:3000/api/v1",
                            "--ani-bearer-token",
                            "ani-token",
                            "--node-pool-name",
                            "gpu-pool",
                            "--scaled-node-count",
                            "3",
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
