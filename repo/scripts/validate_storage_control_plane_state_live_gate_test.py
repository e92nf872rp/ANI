#!/usr/bin/env python3
"""Tests for STORAGE-CONTROL-PLANE-STATE-A live gate contract and runner."""

from __future__ import annotations

import json
import tempfile
import unittest
from copy import deepcopy
from pathlib import Path
from unittest.mock import patch

import validate_storage_control_plane_state_live_gate as gate


class FakeHTTPClient:
    def __init__(self) -> None:
        self.calls: list[tuple[str, str]] = []
        self.deleted: set[str] = set()

    def request(
        self,
        method: str,
        url: str,
        token: str,
        tenant_id: str,
        body: dict[str, object] | None = None,
    ) -> tuple[int, dict[str, object]]:
        self.calls.append((method, url))
        body = body or {}
        if method == "POST" and url.endswith("/volumes"):
            if body.get("name") == "storage-cp-live-vol-conflict":
                return 409, {"code": "CONFLICT"}
            return 201, {"id": "vol-1", "name": body.get("name")}
        if method == "POST" and url.endswith("/snapshots"):
            return 202, {"result": {"snapshot": {"id": "snap-1"}}}
        if method == "POST" and url.endswith("/filesystems"):
            return 201, {"id": "fs-1"}
        if method == "POST" and url.endswith("/mount-targets"):
            return 202, {"result": {"mount_target": {"id": "mt-1"}}}
        if method == "POST" and url.endswith("/buckets"):
            return 201, {"id": "bucket-1"}
        if method == "POST" and url.endswith("/objects"):
            return 201, {"id": "obj-1"}
        if method == "POST" and url.endswith("/vector-stores"):
            return 201, {"id": "vst-1"}
        if method == "PUT" and url.endswith("/knowledge-base-link"):
            return 200, {"id": "vst-1", "knowledge_base_ref": {"id": "kb-1"}}
        if method == "DELETE":
            resource_id = url.rsplit("/", 1)[-1]
            if resource_id == "bucket-1" or url.rstrip("/").endswith("/buckets"):
                raise AssertionError("OpenAPI has no DELETE /buckets/{bucket_id}")
            self.deleted.add(resource_id)
            return 200, {"id": resource_id}
        if method == "GET" and url.rstrip("/").endswith("/buckets"):
            items = []
            if "bucket-1" not in self.deleted:
                items.append({"id": "bucket-1"})
            return 200, {"items": items, "total": len(items)}
        if method == "GET" and "/volumes/vol-1/snapshots" in url:
            return 200, {"items": [{"id": "snap-1"}]}
        if method == "GET" and "/filesystems/fs-1/mount-targets" in url:
            return 200, {"items": [{"id": "mt-1"}]}
        for resource_id, suffix in (
            ("vol-1", "/volumes/vol-1"),
            ("fs-1", "/filesystems/fs-1"),
            ("vst-1", "/vector-stores/vst-1"),
            ("obj-1", "/objects/obj-1"),
        ):
            if url.endswith(suffix):
                if resource_id in self.deleted:
                    return 404, {"code": "NOT_FOUND"}
                return 200, {"id": resource_id}
        if method == "GET":
            return 404, {"code": "NOT_FOUND"}
        raise AssertionError(f"unexpected request {method} {url}")


class FakeRunner:
    def __init__(self) -> None:
        self.commands: list[list[str]] = []

    def run(self, command: list[str], input_text: str | None = None) -> str:
        self.commands.append(command)
        joined = " ".join(command)
        if "psql" in joined and "storage_volumes" in joined:
            return "1\n"
        if "psql" in joined and "vector_stores" in joined:
            return "1\n"
        if "rollout" in joined:
            return "deployment successfully rolled out\n"
        return ""


class StorageControlPlaneStateLiveGateTest(unittest.TestCase):
    def test_tombstone_count_passes_values_as_psql_variables(self) -> None:
        payload = "vol-1'; DROP TABLE storage_volumes; --"
        config = gate.LiveConfig(
            gateway_url="http://gateway.example/api/v1",
            ani_bearer_token="token",
            tenant_id="11111111-1111-1111-1111-111111111111'; SELECT 1; --",
            namespace="ani-tenant-a",
            subnet_id="subnet-a",
            vpc_id="vpc-a",
            storage_class="ani-rbd-ssd",
            gateway_deployment="ani-gateway",
            gateway_namespace="ani-system",
            postgres_namespace="ani-system",
            postgres_pod="ani-postgres-0",
            postgres_db="ani",
            postgres_user="ani",
            idempotency_prefix="storage-cp-live",
        )
        runner = FakeRunner()
        self.assertEqual("1", gate.pg_tombstone_count(config, runner, "storage_volumes", "volume_id", payload))
        command = runner.commands[-1]
        sql = command[-1]
        self.assertNotIn(payload, sql)
        self.assertIn(":'tenant_id'", sql)
        self.assertIn(":'resource_id'", sql)
        self.assertIn(f"resource_id={payload}", command)

    def test_contract_defines_restart_and_tombstone_checks(self) -> None:
        document = gate.load_gate(gate.DEFAULT_GATE)
        gate.validate_contract(document)
        check_ids = {check["id"] for check in document["live_checks"]}
        self.assertEqual(document["profile"], "STORAGE-CONTROL-PLANE-STATE-A")
        for check_id in (
            "gateway-fail-closed-database-url",
            "core-storage-graph-create",
            "gateway-rollout-restart",
            "core-storage-graph-reread-after-restart",
            "idempotency-replay-no-duplicate",
            "idempotency-conflict-different-intent",
            "soft-delete-api-hide-pg-tombstone",
            "provider-temp-cleanup",
        ):
            self.assertIn(check_id, check_ids)

    def test_contract_rejects_missing_check(self) -> None:
        document = deepcopy(gate.load_gate(gate.DEFAULT_GATE))
        document["live_checks"] = [
            check for check in document["live_checks"] if check["id"] != "gateway-rollout-restart"
        ]
        with self.assertRaises(SystemExit) as raised:
            gate.validate_contract(document)
        self.assertIn("missing live checks: gateway-rollout-restart", str(raised.exception))

    def test_production_shaped_rejects_local_gateway(self) -> None:
        config = gate.LiveConfig(
            gateway_url="http://127.0.0.1:8080/api/v1",
            ani_bearer_token="token",
            tenant_id="11111111-1111-1111-1111-111111111111",
            namespace="ani-tenant-a",
            subnet_id="subnet-a",
            vpc_id="vpc-a",
            storage_class="ani-rbd-ssd",
            gateway_deployment="ani-gateway",
            gateway_namespace="ani-system",
            postgres_namespace="ani-system",
            postgres_pod="ani-postgres-0",
            postgres_db="ani",
            postgres_user="ani",
            idempotency_prefix="storage-cp-live",
            production_shaped=True,
            evidence_output=Path("/tmp/evidence.json"),
        )
        with self.assertRaises(SystemExit) as raised:
            gate.validate_live_config(config)
        self.assertIn("non-local Gateway URL", str(raised.exception))

    def test_live_run_writes_redacted_evidence(self) -> None:
        with tempfile.TemporaryDirectory() as tmp:
            evidence_path = Path(tmp) / "storage-cp.json"
            with patch.object(gate.shutil, "which", return_value="/usr/bin/kubectl"):
                result = gate.run_live(
                    gate.LiveConfig(
                        gateway_url="http://gateway.example:30080/api/v1",
                        ani_bearer_token="secret-token",
                        tenant_id="11111111-1111-1111-1111-111111111111",
                        namespace="ani-tenant-a",
                        subnet_id="subnet-a",
                        vpc_id="vpc-a",
                        storage_class="ani-rbd-ssd",
                        gateway_deployment="ani-gateway",
                        gateway_namespace="ani-system",
                        postgres_namespace="ani-system",
                        postgres_pod="ani-postgres-0",
                        postgres_db="ani",
                        postgres_user="ani",
                        idempotency_prefix="storage-cp-live",
                        production_shaped=True,
                        cleanup=True,
                        evidence_output=evidence_path,
                    ),
                    http_client=FakeHTTPClient(),
                    runner=FakeRunner(),
                )
            gate.write_evidence(evidence_path, result)
            written = json.loads(evidence_path.read_text(encoding="utf-8"))

        self.assertEqual(result["status"], "passed")
        self.assertEqual(result["conflict_status"], 409)
        self.assertEqual(result["resource_ids"]["volume_id"], "vol-1")
        self.assertEqual(written["profile"], "STORAGE-CONTROL-PLANE-STATE-A")
        self.assertNotIn("secret-token", json.dumps(written))
        self.assertNotIn("password", json.dumps(written).lower())

    def test_cli_contract_mode_validates_docs(self) -> None:
        document = gate.load_gate(gate.DEFAULT_GATE)
        with (
            patch("sys.argv", ["validate_storage_control_plane_state_live_gate.py"]),
            patch.object(gate, "load_gate", return_value=document),
            patch.object(gate, "validate_docs") as validate_docs,
        ):
            self.assertEqual(0, gate.main())
        validate_docs.assert_called_once()


if __name__ == "__main__":
    unittest.main()
