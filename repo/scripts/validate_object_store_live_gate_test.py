#!/usr/bin/env python3
"""Tests for Sprint 13 object-store MinIO live gate contract."""

from __future__ import annotations

import tempfile
import unittest
import json
from copy import deepcopy
from pathlib import Path
from unittest.mock import patch

import validate_object_store_live_gate as gate


class ObjectStoreLiveGateTest(unittest.TestCase):
    def test_contract_gate_defines_minio_bucket_and_presign_checks(self) -> None:
        document = gate.load_gate(gate.DEFAULT_GATE)

        gate.validate_contract(document)

        check_ids = {check["id"] for check in document["live_checks"]}
        self.assertIn("minio-health-ready", check_ids)
        self.assertIn("core-bucket-create", check_ids)
        self.assertIn("core-buckets-list", check_ids)
        self.assertIn("core-object-upload-presign", check_ids)
        self.assertIn("core-object-download-presign", check_ids)

    def test_contract_gate_rejects_missing_check(self) -> None:
        document = deepcopy(gate.load_gate(gate.DEFAULT_GATE))
        document["live_checks"] = [check for check in document["live_checks"] if check["id"] != "core-object-download-presign"]

        with self.assertRaises(SystemExit) as raised:
            gate.validate_contract(document)

        self.assertIn("missing live checks: core-object-download-presign", str(raised.exception))

    def test_contract_gate_requires_minio_tools(self) -> None:
        document = deepcopy(gate.load_gate(gate.DEFAULT_GATE))
        document["required_tools"] = []

        with self.assertRaises(SystemExit) as raised:
            gate.validate_contract(document)

        self.assertIn("required_tools must include curl", str(raised.exception))

    def test_contract_gate_rejects_production_like_status(self) -> None:
        document = deepcopy(gate.load_gate(gate.DEFAULT_GATE))
        document["status"] = "production_like"

        with self.assertRaises(SystemExit) as raised:
            gate.validate_contract(document)

        self.assertIn("status must be contract or live", str(raised.exception))

    def test_cli_reports_missing_gate_path_without_traceback(self) -> None:
        missing_gate = Path(tempfile.gettempdir()) / "ani-missing-object-store-live-gate.yaml"
        with (
            patch("sys.argv", ["validate_object_store_live_gate.py", "--gate", str(missing_gate)]),
            patch.object(gate, "validate_docs"),
        ):
            with self.assertRaises(SystemExit) as raised:
                gate.main()

        self.assertIn(f"missing {missing_gate}", str(raised.exception))

    def test_cli_validates_docs(self) -> None:
        document = gate.load_gate(gate.DEFAULT_GATE)
        with (
            patch("sys.argv", ["validate_object_store_live_gate.py"]),
            patch.object(gate, "load_gate", return_value=document),
            patch.object(gate, "validate_docs") as validate_docs,
        ):
            gate.main()

        validate_docs.assert_called_once()

    def test_live_production_shaped_writes_redacted_evidence(self) -> None:
        with tempfile.TemporaryDirectory() as tmp:
            evidence = Path(tmp) / "objectstore.json"
            args = gate.LiveArgs(
                gateway_url="https://gateway.example/api/v1",
                ani_bearer_token="token",
                minio_url="https://minio.example",
                minio_alias="ani-minio",
                evidence_output=str(evidence),
                production_shaped=True,
                cleanup=True,
            )

            gate.validate_live(
                args,
                command_runner=lambda command: "bucket-a\n",
                json_requester=object_store_json_requester,
                url_requester=object_store_url_requester,
            )

            payload = json.loads(evidence.read_text(encoding="utf-8"))

        self.assertEqual("passed", payload["status"])
        self.assertEqual(201, payload["bucket_create_status"])
        self.assertEqual(200, payload["bucket_list_status"])
        self.assertEqual(200, payload["upload_presign_status"])
        self.assertEqual(200, payload["download_presign_status"])
        self.assertEqual(200, payload["actual_upload_status"])
        self.assertEqual(200, payload["actual_download_status"])
        self.assertTrue(payload["cleanup_enabled"])
        self.assertEqual(201, payload["cleanup_api_key_status"])
        self.assertEqual(200, payload["cleanup_status"])
        self.assertEqual(200, payload["bucket_delete_status"])
        self.assertEqual(200, payload["cleanup_api_key_revoke_status"])
        self.assertEqual("passed", payload["production_shape"]["status"])
        self.assertIn("production_object_store_credentials", payload["production_shape"]["proof_items"])
        self.assertNotIn("https://upload.example", json.dumps(payload))
        self.assertNotIn("https://download.example", json.dumps(payload))

    def test_production_shaped_live_rejects_local_gateway(self) -> None:
        args = gate.LiveArgs(
            gateway_url="http://127.0.0.1:3000/api/v1",
            ani_bearer_token="token",
            minio_url="https://minio.example",
            minio_alias="ani-minio",
            evidence_output="",
            production_shaped=True,
        )

        with self.assertRaises(SystemExit) as raised:
            gate.validate_live(
                args,
                command_runner=lambda command: "",
                json_requester=object_store_json_requester,
                url_requester=object_store_url_requester,
            )

        self.assertIn("production-shaped live mode requires a non-local production gateway URL", str(raised.exception))


def object_store_json_requester(method: str, url: str, bearer_token: str, payload: dict | None = None) -> tuple[int, dict]:
    if method == "POST" and url.endswith("/buckets"):
        return 201, {"id": "bucket-a", "name": "models", "class": "models"}
    if method == "GET" and url.endswith("/buckets"):
        return 200, {"items": [{"id": "bucket-a"}], "total": 1}
    if method == "POST" and url.endswith("/objects/upload"):
        return 200, {"object_id": "obj-a", "upload_url": "https://upload.example/secret?X-Amz-Signature=sig", "expires_at": "2026-06-20T00:10:00Z"}
    if method == "GET" and url.endswith("/objects/obj-a/download"):
        return 200, {"download_url": "https://download.example/secret?X-Amz-Signature=sig", "expires_at": "2026-06-20T00:10:00Z"}
    if method == "POST" and url.endswith("/auth/api-keys"):
        return 201, {"key_id": "key-a", "key_value": "ani_cleanup_key"}
    if method == "DELETE" and url.endswith("/objects/obj-a"):
        return 200, {"id": "obj-a", "state": "deleted"}
    if method == "DELETE" and url.endswith("/buckets/bucket-a"):
        return 200, {"id": "bucket-a", "name": "models"}
    if method == "DELETE" and url.endswith("/auth/api-keys/key-a"):
        return 200, {}
    raise AssertionError(f"unexpected request {method} {url}")


def object_store_url_requester(method: str, url: str, body: bytes | None = None) -> tuple[int, bytes]:
    if method == "GET" and url == "https://minio.example/minio/health/ready":
        return 200, b""
    if method == "PUT" and url.startswith("https://upload.example/"):
        return 200, b""
    if method == "GET" and url.startswith("https://download.example/"):
        return 200, b"sprint13-objectstore-minio-live\n"
    raise AssertionError(f"unexpected URL request {method} {url}")


if __name__ == "__main__":
    unittest.main()
