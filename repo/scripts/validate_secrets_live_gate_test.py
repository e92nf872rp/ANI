#!/usr/bin/env python3
"""Tests for the Sprint 5 Secret live validation gate."""

from __future__ import annotations

import json
import tempfile
import unittest
from pathlib import Path
from unittest.mock import patch

import validate_secrets_live_gate as gate


class FakeLiveRunner:
    def __init__(self) -> None:
        self.json_calls: list[tuple[str, dict[str, object], str]] = []
        self.commands: list[tuple[list[str], str | None]] = []

    def post_json(self, url: str, payload: dict[str, object], bearer_token: str) -> dict[str, object]:
        self.json_calls.append((url, payload, bearer_token))
        if url.endswith("/secrets"):
            return {
                "id": "sec-live",
                "tenant_id": "tenant-a",
                "name": "live-secret",
                "keys": ["password", "token"],
                "state": "active",
                "dev_profile": {
                    "mode": "real",
                    "provider": "kubernetes-secret-provider",
                    "real_provider": True,
                },
            }
        if url.endswith("/secrets/sec-live/bindings"):
            return {
                "id": "sbind-live",
                "secret_id": "sec-live",
                "tenant_id": "tenant-a",
                "state": "bound",
                "dev_profile": {"mode": "local", "provider": "local-secret-service", "real_provider": False},
            }
        raise AssertionError(f"unexpected JSON URL: {url}")

    def run(self, command: list[str], input_text: str | None = None) -> str:
        self.commands.append((command, input_text))
        joined = " ".join(command)
        if joined.endswith("get secret sec-live -n ani-tenant-tenant-a -o json"):
            return json.dumps(
                {
                    "metadata": {"name": "sec-live", "namespace": "ani-tenant-tenant-a"},
                    "data": {"password": "cEBzc3cwcmQ=", "token": "dDBrZW4="},
                }
            )
        if "wait --for=condition=Ready pod/ani-secret-live-pod" in joined:
            return "pod/ani-secret-live-pod condition met\n"
        if joined.endswith("logs ani-secret-live-pod -n ani-tenant-tenant-a"):
            return "env:p@ssw0rd\nfile:t0ken\n"
        if command[:2] == ["kubectl", "apply"] and input_text:
            if "VirtualMachine" in input_text:
                return "virtualmachine.kubevirt.io/ani-secret-live-vm serverside-applied\n"
            return "pod/ani-secret-live-pod created\n"
        if joined.endswith("delete pod ani-secret-live-pod -n ani-tenant-tenant-a --ignore-not-found=true"):
            return "pod deleted\n"
        raise AssertionError(f"unexpected command: {joined}")


class SecretsLiveGateTest(unittest.TestCase):
    def test_contract_gate_defines_secret_write_pod_and_vm_checks(self) -> None:
        document = gate.load_gate(gate.DEFAULT_GATE)

        gate.validate_contract(document)

        check_ids = {check["id"] for check in document["live_checks"]}
        self.assertIn("core-create-kubernetes-secret", check_ids)
        self.assertIn("kubectl-read-secret", check_ids)
        self.assertIn("pod-secret-env-visible", check_ids)
        self.assertIn("pod-secret-file-visible", check_ids)
        self.assertIn("kubevirt-vm-secret-volume-accepted", check_ids)

    def test_live_gate_runs_core_secret_write_pod_env_file_and_vm_volume_checks(self) -> None:
        runner = FakeLiveRunner()
        result = gate.run_live(
            gate.LiveConfig(
                tenant_id="tenant-a",
                gateway_url="http://127.0.0.1:3000/api/v1",
                ani_bearer_token="ani-token",
                password_value="p@ssw0rd",
                token_value="t0ken",
            ),
            runner=runner,
        )

        self.assertEqual(result["status"], "passed")
        self.assertEqual(result["tenant_id"], "tenant-a")
        self.assertEqual(result["gateway_url"], "http://127.0.0.1:3000/api/v1")
        self.assertEqual(result["secret_id"], "sec-live")
        self.assertEqual(result["namespace"], "ani-tenant-tenant-a")
        self.assertEqual(result["pod"], "ani-secret-live-pod")
        self.assertEqual(result["vm"], "ani-secret-live-vm")
        self.assertEqual(runner.json_calls[0][0], "http://127.0.0.1:3000/api/v1/secrets")
        self.assertEqual(runner.json_calls[1][0], "http://127.0.0.1:3000/api/v1/secrets/sec-live/bindings")
        self.assertEqual(runner.commands[0][0], ["kubectl", "get", "secret", "sec-live", "-n", "ani-tenant-tenant-a", "-o", "json"])

    def test_cli_live_mode_rejects_missing_gateway_config(self) -> None:
        with patch.object(gate, "run_live") as run_live:
            with patch("sys.argv", ["validate_secrets_live_gate.py", "--live"]):
                with self.assertRaises(SystemExit):
                    gate.main()
        run_live.assert_not_called()

    def test_cli_live_mode_writes_evidence_json_when_requested(self) -> None:
        fake_evidence = {
            "status": "passed",
            "tenant_id": "tenant-a",
            "gateway_url": "http://127.0.0.1:3000/api/v1",
            "secret_id": "sec-live",
            "namespace": "ani-tenant-tenant-a",
            "pod": "ani-secret-live-pod",
            "vm": "ani-secret-live-vm",
        }
        with tempfile.TemporaryDirectory() as tmpdir:
            output = Path(tmpdir) / "secrets-live-evidence.json"
            with patch.object(gate, "validate_live_config"):
                with patch.object(gate, "run_live", return_value=fake_evidence):
                    with patch(
                        "sys.argv",
                        [
                            "validate_secrets_live_gate.py",
                            "--live",
                            "--tenant-id",
                            "tenant-a",
                            "--gateway-url",
                            "http://127.0.0.1:3000/api/v1",
                            "--ani-bearer-token",
                            "ani-token",
                            "--password-value",
                            "p@ssw0rd",
                            "--token-value",
                            "t0ken",
                            "--evidence-output",
                            str(output),
                        ],
                    ):
                        try:
                            gate.main()
                        except SystemExit:
                            pass

            self.assertTrue(output.exists())
            written = output.read_text(encoding="utf-8")
            self.assertEqual(json.loads(written), fake_evidence)
            self.assertNotIn("ani-token", written)
            self.assertNotIn("p@ssw0rd", written)
            self.assertNotIn("t0ken", written)


if __name__ == "__main__":
    unittest.main()
