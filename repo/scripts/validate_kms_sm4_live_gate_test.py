#!/usr/bin/env python3
"""Tests for the Sprint 5 KMS/SM4 live validation gate."""

from __future__ import annotations

import json
import tempfile
import unittest
from pathlib import Path
from unittest.mock import patch

import validate_kms_sm4_live_gate as gate


class FakeHTTPRunner:
    def __init__(self) -> None:
        self.json_calls: list[tuple[str, dict[str, object], str]] = []
        self.binary_calls: list[tuple[str, bytes, str, str]] = []

    def post_json(self, url: str, payload: dict[str, object], bearer_token: str) -> dict[str, object]:
        self.json_calls.append((url, payload, bearer_token))
        if url.endswith("/encryption/keys"):
            return {
                "id": "ekey-live",
                "tenant_id": "tenant-a",
                "algorithm": "SM4",
                "dev_profile": {"mode": "real", "real_provider": True, "provider": "kms-sm4-provider"},
            }
        if url.endswith("/encryption/seal"):
            return {
                "key_id": "ekey-live",
                "sealed_object_uri": "kms+sm4://tenant-a/ekey-live/model.bin",
                "unseal_token": "utok-live",
                "dev_profile": {"mode": "real", "real_provider": True, "provider": "kms-sm4-provider"},
            }
        if url.endswith("/encryption/unseal-token"):
            return {
                "key_id": "ekey-live",
                "sealed_object_uri": "kms+sm4://tenant-a/ekey-live/model.bin",
                "unseal_token": "utok-live-2",
                "dev_profile": {"mode": "real", "real_provider": True, "provider": "kms-sm4-provider"},
            }
        raise AssertionError(f"unexpected JSON URL: {url}")

    def post_bytes(self, url: str, content: bytes, bearer_token: str, content_type: str) -> bytes:
        self.binary_calls.append((url, content, bearer_token, content_type))
        if url.endswith("/v1/stream/seal"):
            return b"sealed:" + content[::-1]
        if url.endswith("/v1/stream/open"):
            self.assert_startswith(content, b"sealed:")
            return content.removeprefix(b"sealed:")[::-1]
        raise AssertionError(f"unexpected binary POST URL: {url}")

    def put_bytes(self, url: str, content: bytes, content_type: str) -> None:
        self.binary_calls.append((url, content, "", content_type))

    def get_bytes(self, url: str) -> bytes:
        self.binary_calls.append((url, b"", "", ""))
        return b"sealed:daolyap-ledom-evil"

    @staticmethod
    def assert_startswith(content: bytes, prefix: bytes) -> None:
        if not content.startswith(prefix):
            raise AssertionError(f"{content!r} does not start with {prefix!r}")


class KMSSM4LiveGateTest(unittest.TestCase):
    def test_contract_gate_defines_core_kms_streaming_and_objectstore_checks(self) -> None:
        document = gate.load_gate(gate.DEFAULT_GATE)

        gate.validate_contract(document)

        check_ids = {check["id"] for check in document["live_checks"]}
        self.assertIn("core-create-sm4-key", check_ids)
        self.assertIn("core-seal-object", check_ids)
        self.assertIn("kms-stream-seal", check_ids)
        self.assertIn("objectstore-write-sealed-content", check_ids)
        self.assertIn("kms-stream-open", check_ids)

    def test_live_gate_runs_core_provider_streaming_and_objectstore_round_trip(self) -> None:
        runner = FakeHTTPRunner()
        result = gate.run_live(
            gate.LiveConfig(
                tenant_id="tenant-a",
                gateway_url="http://127.0.0.1:3000/api/v1",
                ani_bearer_token="ani-token",
                kms_base_url="http://127.0.0.1:19090",
                kms_bearer_token="kms-token",
                object_put_url="http://127.0.0.1:9000/live/sealed.bin?put",
                object_get_url="http://127.0.0.1:9000/live/sealed.bin?get",
                plaintext=b"live-model-payload",
            ),
            runner=runner,
        )

        self.assertEqual(result["status"], "passed")
        self.assertEqual(result["tenant_id"], "tenant-a")
        self.assertEqual(result["gateway_url"], "http://127.0.0.1:3000/api/v1")
        self.assertEqual(result["kms_base_url"], "http://127.0.0.1:19090")
        self.assertEqual(result["object_uri"], "s3://ani-live-validation/model.bin")
        self.assertEqual(result["provider"], "kms-sm4-provider")
        self.assertEqual(result["object_round_trip_bytes"], len(b"live-model-payload"))
        self.assertEqual(runner.json_calls[0][0], "http://127.0.0.1:3000/api/v1/encryption/keys")
        self.assertEqual(runner.json_calls[1][0], "http://127.0.0.1:3000/api/v1/encryption/seal")
        self.assertEqual(runner.binary_calls[0][0], "http://127.0.0.1:19090/v1/stream/seal")
        self.assertEqual(runner.binary_calls[1][0], "http://127.0.0.1:9000/live/sealed.bin?put")
        self.assertEqual(runner.binary_calls[2][0], "http://127.0.0.1:9000/live/sealed.bin?get")
        self.assertEqual(runner.binary_calls[3][0], "http://127.0.0.1:19090/v1/stream/open")

    def test_cli_live_mode_rejects_missing_kms_and_objectstore_config(self) -> None:
        with patch.object(gate, "run_live") as run_live:
            with patch(
                "sys.argv",
                [
                    "validate_kms_sm4_live_gate.py",
                    "--live",
                    "--tenant-id",
                    "tenant-a",
                    "--gateway-url",
                    "http://127.0.0.1:3000/api/v1",
                    "--ani-bearer-token",
                    "ani-token",
                ],
            ):
                with self.assertRaises(SystemExit):
                    gate.main()
        run_live.assert_not_called()

    def test_cli_live_mode_writes_evidence_json_when_requested(self) -> None:
        fake_evidence = {
            "status": "passed",
            "tenant_id": "tenant-a",
            "gateway_url": "http://127.0.0.1:3000/api/v1",
            "kms_base_url": "http://127.0.0.1:19090",
            "object_uri": "s3://ani-live-validation/model.bin",
            "provider": "kms-sm4-provider",
            "key_id": "ekey-live",
            "sealed_object_uri": "kms+sm4://tenant-a/ekey-live/model.bin",
            "object_round_trip_bytes": 18,
        }
        with tempfile.TemporaryDirectory() as tmpdir:
            output = Path(tmpdir) / "kms-sm4-live-evidence.json"
            with patch.object(gate, "validate_live_config"):
                with patch.object(gate, "run_live", return_value=fake_evidence):
                    with patch(
                        "sys.argv",
                        [
                            "validate_kms_sm4_live_gate.py",
                            "--live",
                            "--tenant-id",
                            "tenant-a",
                            "--gateway-url",
                            "http://127.0.0.1:3000/api/v1",
                            "--ani-bearer-token",
                            "ani-token",
                            "--kms-base-url",
                            "http://127.0.0.1:19090",
                            "--kms-bearer-token",
                            "kms-token",
                            "--object-put-url",
                            "http://127.0.0.1:9000/live/sealed.bin?put",
                            "--object-get-url",
                            "http://127.0.0.1:9000/live/sealed.bin?get",
                            "--evidence-output",
                            str(output),
                        ],
                    ):
                        try:
                            gate.main()
                        except SystemExit:
                            pass

            written = json.loads(output.read_text(encoding="utf-8"))
            self.assertEqual(written, fake_evidence)
            self.assertNotIn("ani-token", output.read_text(encoding="utf-8"))
            self.assertNotIn("kms-token", output.read_text(encoding="utf-8"))
            self.assertNotIn("object-put-url", written)
            self.assertNotIn("object-get-url", written)


if __name__ == "__main__":
    unittest.main()
