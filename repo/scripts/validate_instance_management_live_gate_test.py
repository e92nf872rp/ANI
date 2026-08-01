#!/usr/bin/env python3
"""Tests for the instance management VM live gate validator."""

from __future__ import annotations

import importlib
import tempfile
import unittest
from pathlib import Path
from unittest.mock import patch


class FakeHTTPClient:
    def __init__(self) -> None:
        self.requests: list[tuple[str, str, dict[str, object] | None]] = []

    def request(self, method: str, url: str, token: str, tenant_id: str, body: dict[str, object] | None = None) -> tuple[int, dict[str, object]]:
        self.requests.append((method, url, body))
        instance = {
            "id": "vm-live-1",
            "name": "vm-live",
            "kind": "vm",
            "state": "running",
            "provider": "kubernetes",
            "dev_profile": {"provider": "kubernetes", "real_provider": True},
            "access": {"console_available": True},
        }
        if method == "POST" and url.endswith("/instances"):
            return 201, {"instance": instance, "operation_id": "op-create"}
        if method == "GET" and url.endswith("/instances/vm-live-1"):
            return 200, instance
        if method == "POST" and url.endswith("/instances/vm-live-1/lifecycle"):
            action = str((body or {}).get("action", ""))
            state = "stopped" if action == "stop" else "running"
            changed = {**instance, "state": state}
            return 200, {"instance": changed, "operation_id": f"op-{action}"}
        if method == "POST" and url.endswith("/instances/vm-live-1/console"):
            protocol = str((body or {}).get("protocol", "vnc"))
            return 200, {
                "session_id": f"session-{protocol}",
                "protocol": protocol,
                "connect_url": f"wss://gateway.example/{protocol}",
                "url": f"wss://gateway.example/{protocol}",
                "expires_at": "2026-07-31T12:00:00Z",
            }
        raise AssertionError(f"unexpected request: {method} {url}")


class FakeRunner:
    def __init__(self) -> None:
        self.commands: list[list[str]] = []

    def run(self, command: list[str], input_text: str | None = None) -> str:
        self.commands.append(command)
        joined = " ".join(command)
        if "virtualmachineinstances" in joined:
            return '{"metadata":{"name":"vm-live"},"status":{"phase":"Running","nodeName":"ani-node-1"}}'
        if "virtualmachines" in joined:
            return '{"metadata":{"name":"vm-live"},"status":{"printableStatus":"Running"}}'
        return "{}"


class InstanceManagementLiveGateTests(unittest.TestCase):
    def test_contract_gate_validates_default_yaml(self) -> None:
        gate = importlib.import_module("validate_instance_management_live_gate")
        with patch("sys.argv", ["validate_instance_management_live_gate.py"]):
            self.assertEqual(0, gate.main())

    def test_live_vm_requires_core_api_not_direct_apply(self) -> None:
        gate = importlib.import_module("validate_instance_management_live_gate")
        with self.assertRaises(SystemExit) as raised:
            with patch(
                "sys.argv",
                [
                    "validate_instance_management_live_gate.py",
                    "--live",
                    "--kind",
                    "vm",
                    "--gateway-url",
                    "",
                ],
            ):
                gate.main()
        self.assertIn("gateway-url", str(raised.exception))

    def test_evidence_output_parent_must_be_directory(self) -> None:
        gate = importlib.import_module("validate_instance_management_live_gate")
        with tempfile.TemporaryDirectory() as tmpdir:
            parent_file = Path(tmpdir) / "not-dir"
            parent_file.write_text("x", encoding="utf-8")
            with self.assertRaises(SystemExit) as raised:
                gate.validate_evidence_output(str(parent_file / "evidence.json"))
        self.assertIn("parent must be a directory", str(raised.exception))

    def test_live_vm_uses_core_instance_api_and_only_read_observes_kubevirt(self) -> None:
        gate = importlib.import_module("validate_instance_management_live_gate")
        http = FakeHTTPClient()
        runner = FakeRunner()
        evidence = gate.run_live(
            gate.LiveConfig(
                gateway_url="https://gateway.example/api/v1",
                ani_bearer_token="token",
                tenant_id="tenant-a",
                kind="vm",
                name="vm-live",
                image_ref="registry.example/tenant/vm:latest",
                idempotency_key="vm-live-test",
                namespace="ani-tenant-a",
            ),
            http_client=http,
            runner=runner,
        )

        urls = [url for _, url, _ in http.requests]
        self.assertIn("https://gateway.example/api/v1/instances", urls)
        self.assertIn("https://gateway.example/api/v1/instances/vm-live-1/console", urls)
        self.assertTrue(any(url.endswith("/instances/vm-live-1/lifecycle") for url in urls))
        forbidden_kubectl_writes = {"apply", "patch", "delete", "create", "replace"}
        for command in runner.commands:
            self.assertTrue(forbidden_kubectl_writes.isdisjoint(command), command)
            joined = " ".join(command)
            if "virtualmachines" in joined or "virtualmachineinstances" in joined:
                self.assertIn("vm-live", command)
                self.assertNotIn("vm-live-1", command)
        self.assertEqual("passed", evidence["status"])
        self.assertEqual("vm-live-1", evidence["instance_id"])


if __name__ == "__main__":
    unittest.main()
