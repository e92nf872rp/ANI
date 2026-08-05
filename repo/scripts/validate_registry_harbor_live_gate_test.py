#!/usr/bin/env python3
"""Tests for Harbor-backed Registry P0 closure live gate."""

from __future__ import annotations

import json
import tempfile
import unittest
from copy import deepcopy
from pathlib import Path
from unittest.mock import patch

import validate_registry_harbor_live_gate as gate


class RegistryHarborLiveGateTest(unittest.TestCase):
    def test_contract_defines_closure_checks(self) -> None:
        document = gate.load_gate(gate.DEFAULT_GATE)

        gate.validate_contract(document)

        check_ids = {check["id"] for check in document["live_checks"]}
        self.assertEqual(document["profile"], "REGISTRY-P0-CLOSURE-A")
        for check_id in (
            "gateway-registry-project-create",
            "gateway-registry-artifact-push",
            "gateway-registry-images-purpose",
            "gateway-registry-scan-status",
            "gateway-registry-instance-reference",
            "gateway-registry-delete-tag-blocked",
        ):
            self.assertIn(check_id, check_ids)

    def test_contract_rejects_missing_check(self) -> None:
        document = deepcopy(gate.load_gate(gate.DEFAULT_GATE))
        document["live_checks"] = [check for check in document["live_checks"] if check["id"] != "gateway-registry-delete-tag-blocked"]

        with self.assertRaises(SystemExit) as raised:
            gate.validate_contract(document)

        self.assertIn("missing live checks: gateway-registry-delete-tag-blocked", str(raised.exception))

    def test_live_requires_repository_and_tag(self) -> None:
        config = gate.LiveConfig(
            gateway_url="http://gateway.example:30080",
            ani_bearer_token="token",
            tenant_id="tenant-a",
            project="tenant-a",
            namespace="ani-registry-live",
            pull_secret_name="pull",
            idempotency_key="registry-live",
            repository="",
            tag="",
        )

        with self.assertRaises(SystemExit) as raised:
            gate.validate_live_config(config)

        self.assertIn("repository", str(raised.exception))

    def test_production_shaped_rejects_local_gateway_url(self) -> None:
        config = gate.LiveConfig(
            gateway_url="http://127.0.0.1:8080/api/v1",
            ani_bearer_token="token",
            tenant_id="tenant-a",
            project="tenant-a",
            namespace="ani-registry-live",
            pull_secret_name="pull",
            idempotency_key="registry-live",
            repository="runtime",
            tag="latest",
            production_shaped=True,
        )

        with self.assertRaises(SystemExit) as raised:
            gate.validate_live_config(config)

        self.assertIn("non-local Gateway URL", str(raised.exception))

    def test_live_writes_redacted_evidence(self) -> None:
        with tempfile.TemporaryDirectory() as tmp:
            output = Path(tmp) / "registry.json"
            evidence = {
                "status": "passed",
                "pull_secret_ref": "ani-registry-live/pull",
                "instance_id": "inst-1",
                "delete_tag_status": 409,
            }
            gate.write_evidence(output, evidence)

            data = json.loads(output.read_text(encoding="utf-8"))

        self.assertEqual(data["id"], "registry-p0-closure-live-gate")
        self.assertEqual(data["profile"], "REGISTRY-P0-CLOSURE-A")
        self.assertNotIn("ani_bearer_token", json.dumps(data))
        self.assertNotIn("password", json.dumps(data).lower())

    def test_cli_validates_docs_for_contract_mode(self) -> None:
        document = gate.load_gate(gate.DEFAULT_GATE)
        with (
            patch("sys.argv", ["validate_registry_harbor_live_gate.py"]),
            patch.object(gate, "load_gate", return_value=document),
            patch.object(gate, "validate_docs") as validate_docs,
        ):
            gate.main()

        validate_docs.assert_called_once()


if __name__ == "__main__":
    unittest.main()
