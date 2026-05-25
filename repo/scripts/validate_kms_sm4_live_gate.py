#!/usr/bin/env python3
"""Validate Sprint 5 M1-ENCRYPT-LIVE-A KMS/SM4 live gate."""

from __future__ import annotations

import argparse
import json
import os
import urllib.error
import urllib.request
from dataclasses import dataclass
from pathlib import Path
from typing import Any

import yaml


ROOT = Path(__file__).resolve().parents[1]
DOC_ROOT = ROOT.parent
DEFAULT_GATE = ROOT / "deploy/real-k8s-lab/kms-sm4-live-gate.yaml"
REQUIRED_CHECKS = {
    "core-create-sm4-key",
    "core-seal-object",
    "core-unseal-token",
    "kms-stream-seal",
    "objectstore-write-sealed-content",
    "objectstore-read-sealed-content",
    "kms-stream-open",
}
REQUIRED_DOC_TOKENS = [
    "M1-ENCRYPT-LIVE-A",
    "validate-kms-sm4-live-gate",
    "KMS/SM4",
    "provider streaming",
]


def fail(message: str) -> None:
    raise SystemExit(f"KMS/SM4 live gate invalid: {message}")


def load_gate(path: Path) -> dict[str, Any]:
    if not path.exists():
        fail(f"missing {path.relative_to(ROOT)}")
    with path.open(encoding="utf-8") as handle:
        data = yaml.safe_load(handle)
    if not isinstance(data, dict):
        fail(f"{path.relative_to(ROOT)} must be a YAML object")
    return data


def validate_contract(document: dict[str, Any]) -> None:
    if document.get("profile") != "M1-ENCRYPT-LIVE-A":
        fail("profile must be M1-ENCRYPT-LIVE-A")
    if document.get("status") not in {"contract", "live", "production_like"}:
        fail("status must be contract, live or production_like")
    endpoints = document.get("required_endpoints")
    required_endpoints = {
        "ani_gateway_api_v1",
        "kms_sm4_provider",
        "objectstore_presigned_put",
        "objectstore_presigned_get",
    }
    if not isinstance(endpoints, list) or required_endpoints - set(endpoints):
        fail("required_endpoints must include gateway, KMS provider and objectstore presigned URLs")
    checks = document.get("live_checks")
    if not isinstance(checks, list):
        fail("live_checks must be a list")
    check_ids = set()
    for check in checks:
        if not isinstance(check, dict):
            fail("live check must be an object")
        for field in ("id", "command", "pass_condition"):
            if not check.get(field):
                fail(f"live check missing {field}")
        check_ids.add(check["id"])
    missing = REQUIRED_CHECKS - check_ids
    if missing:
        fail(f"missing live checks: {', '.join(sorted(missing))}")


def validate_docs() -> None:
    docs = {
        "ANI-DOCS-INDEX.md": DOC_ROOT / "ANI-DOCS-INDEX.md",
        "ANI-06-开发计划.md": DOC_ROOT / "ANI-06-开发计划.md",
        "CURRENT-SPRINT.md": ROOT / "CURRENT-SPRINT.md",
        "development-records/README.md": ROOT / "development-records/README.md",
    }
    for label, path in docs.items():
        content = path.read_text(encoding="utf-8")
        for token in REQUIRED_DOC_TOKENS:
            if token not in content:
                fail(f"{label} must reference {token}")


@dataclass(frozen=True)
class LiveConfig:
    tenant_id: str
    gateway_url: str
    ani_bearer_token: str
    kms_base_url: str
    kms_bearer_token: str
    object_put_url: str
    object_get_url: str
    object_uri: str = "s3://ani-live-validation/model.bin"
    kms_stream_seal_path: str = "/v1/stream/seal"
    kms_stream_open_path: str = "/v1/stream/open"
    plaintext: bytes = b"ani-live-sm4-object-content"


class HTTPRunner:
    def post_json(self, url: str, payload: dict[str, object], bearer_token: str) -> dict[str, object]:
        body = json.dumps(payload).encode("utf-8")
        request = urllib.request.Request(
            url,
            data=body,
            method="POST",
            headers={
                "content-type": "application/json",
                "authorization": "Bearer " + bearer_token,
            },
        )
        with self._open(request) as response:
            response_body = response.read().decode("utf-8")
            return json.loads(response_body) if response_body else {}

    def post_bytes(self, url: str, content: bytes, bearer_token: str, content_type: str) -> bytes:
        request = urllib.request.Request(
            url,
            data=content,
            method="POST",
            headers={
                "content-type": content_type,
                "authorization": "Bearer " + bearer_token,
            },
        )
        with self._open(request) as response:
            return response.read()

    def put_bytes(self, url: str, content: bytes, content_type: str) -> None:
        request = urllib.request.Request(
            url,
            data=content,
            method="PUT",
            headers={"content-type": content_type},
        )
        with self._open(request) as response:
            response.read()

    def get_bytes(self, url: str) -> bytes:
        request = urllib.request.Request(url, method="GET")
        with self._open(request) as response:
            return response.read()

    @staticmethod
    def _open(request: urllib.request.Request):
        try:
            return urllib.request.urlopen(request, timeout=30)
        except urllib.error.HTTPError as err:
            detail = err.read().decode("utf-8", errors="replace")
            raise RuntimeError(f"{request.full_url} returned HTTP {err.code}: {detail}") from err


def validate_live_config(config: LiveConfig) -> None:
    required = {
        "tenant_id": config.tenant_id,
        "gateway_url": config.gateway_url,
        "ani_bearer_token": config.ani_bearer_token,
        "kms_base_url": config.kms_base_url,
        "kms_bearer_token": config.kms_bearer_token,
        "object_put_url": config.object_put_url,
        "object_get_url": config.object_get_url,
    }
    missing = [name for name, value in required.items() if not value.strip()]
    if missing:
        fail(f"live mode requires {', '.join(missing)}")


def run_live(config: LiveConfig, runner: HTTPRunner | None = None) -> dict[str, object]:
    validate_live_config(config)
    runner = runner or HTTPRunner()
    gateway = config.gateway_url.rstrip("/")
    kms = config.kms_base_url.rstrip("/")

    key = runner.post_json(
        gateway + "/encryption/keys",
        {
            "idempotency_key": "kms-sm4-live-key",
            "name": "kms-sm4-live-key",
            "algorithm": "SM4",
        },
        config.ani_bearer_token,
    )
    key_id = str(key.get("id") or key.get("key_id") or "")
    if not key_id:
        fail("Core create key response missing key id")
    profile = key.get("dev_profile", {})
    if not isinstance(profile, dict) or profile.get("mode") != "real" or not profile.get("real_provider"):
        fail("Core create key response must be provider-backed real dev profile")
    provider = str(profile.get("provider") or "kms-sm4-provider")

    sealed = runner.post_json(
        gateway + "/encryption/seal",
        {
            "idempotency_key": "kms-sm4-live-seal",
            "key_id": key_id,
            "object_uri": config.object_uri,
        },
        config.ani_bearer_token,
    )
    sealed_object_uri = str(sealed.get("sealed_object_uri") or "")
    if not sealed_object_uri.startswith("kms+sm4://") or not sealed.get("unseal_token"):
        fail("Core seal response must include kms+sm4 sealed_object_uri and unseal_token")

    token = runner.post_json(
        gateway + "/encryption/unseal-token",
        {
            "key_id": key_id,
            "sealed_object_uri": sealed_object_uri,
        },
        config.ani_bearer_token,
    )
    if not token.get("unseal_token"):
        fail("Core unseal-token response missing unseal_token")

    sealed_content = runner.post_bytes(
        kms + config.kms_stream_seal_path,
        config.plaintext,
        config.kms_bearer_token,
        "application/octet-stream",
    )
    if not sealed_content or sealed_content == config.plaintext:
        fail("KMS streaming seal must return non-empty ciphertext distinct from plaintext")
    runner.put_bytes(config.object_put_url, sealed_content, "application/octet-stream")
    stored_content = runner.get_bytes(config.object_get_url)
    if stored_content != sealed_content:
        fail("objectstore read content must match written sealed content")
    opened = runner.post_bytes(
        kms + config.kms_stream_open_path,
        stored_content,
        config.kms_bearer_token,
        "application/octet-stream",
    )
    if opened != config.plaintext:
        fail("KMS streaming open must recover original plaintext")
    return {
        "status": "passed",
        "tenant_id": config.tenant_id,
        "gateway_url": gateway,
        "kms_base_url": kms,
        "object_uri": config.object_uri,
        "provider": provider,
        "key_id": key_id,
        "sealed_object_uri": sealed_object_uri,
        "object_round_trip_bytes": len(opened),
    }


def write_live_evidence(path: Path, evidence: dict[str, object]) -> None:
    path.parent.mkdir(parents=True, exist_ok=True)
    path.write_text(json.dumps(evidence, ensure_ascii=False, indent=2, sort_keys=True) + "\n", encoding="utf-8")


def main() -> int:
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("--gate", default=str(DEFAULT_GATE), help="KMS/SM4 live gate YAML")
    parser.add_argument("--live", action="store_true", help="run live Core/KMS/objectstore checks")
    parser.add_argument("--tenant-id", default=os.getenv("ANI_LIVE_TENANT_ID", "tenant-a"))
    parser.add_argument("--gateway-url", default=os.getenv("ANI_GATEWAY_URL", ""))
    parser.add_argument("--ani-bearer-token", default=os.getenv("ANI_BEARER_TOKEN", ""))
    parser.add_argument("--kms-base-url", default=os.getenv("KMS_PROVIDER_BASE_URL", ""))
    parser.add_argument("--kms-bearer-token", default=os.getenv("KMS_PROVIDER_BEARER_TOKEN", ""))
    parser.add_argument("--object-put-url", default=os.getenv("OBJECTSTORE_LIVE_PUT_URL", ""))
    parser.add_argument("--object-get-url", default=os.getenv("OBJECTSTORE_LIVE_GET_URL", ""))
    parser.add_argument("--object-uri", default=os.getenv("OBJECTSTORE_LIVE_OBJECT_URI", "s3://ani-live-validation/model.bin"))
    parser.add_argument("--plaintext", default=os.getenv("KMS_SM4_LIVE_PLAINTEXT", "ani-live-sm4-object-content"))
    parser.add_argument(
        "--evidence-output",
        default=os.getenv("ANI_KMS_SM4_LIVE_EVIDENCE_OUTPUT", ""),
        help="write --live evidence JSON to this path",
    )
    args = parser.parse_args()

    document = load_gate(Path(args.gate))
    validate_contract(document)
    validate_docs()
    if args.live:
        config = LiveConfig(
            tenant_id=args.tenant_id,
            gateway_url=args.gateway_url,
            ani_bearer_token=args.ani_bearer_token,
            kms_base_url=args.kms_base_url,
            kms_bearer_token=args.kms_bearer_token,
            object_put_url=args.object_put_url,
            object_get_url=args.object_get_url,
            object_uri=args.object_uri,
            plaintext=args.plaintext.encode("utf-8"),
        )
        validate_live_config(config)
        result = run_live(config)
        if args.evidence_output:
            write_live_evidence(Path(args.evidence_output), result)
            print(f"M1-ENCRYPT-LIVE-A live checks valid; evidence written to {args.evidence_output}")
        else:
            print(f"M1-ENCRYPT-LIVE-A live checks valid: {json.dumps(result, sort_keys=True)}")
    else:
        print("M1-ENCRYPT-LIVE-A contract valid; use --live with ANI_GATEWAY_URL, KMS_PROVIDER_BASE_URL and objectstore presigned URLs")
    return 0


if __name__ == "__main__":
    raise SystemExit(main())
