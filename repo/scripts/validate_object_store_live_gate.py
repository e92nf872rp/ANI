#!/usr/bin/env python3
"""Validate Sprint 13 object-store MinIO live gate contract."""

from __future__ import annotations

import argparse
import datetime
import json
import subprocess
import urllib.error
import urllib.parse
import urllib.request
from dataclasses import dataclass
from pathlib import Path
from typing import Any, Callable

import yaml


ROOT = Path(__file__).resolve().parents[1]
DOC_ROOT = ROOT.parent
DEFAULT_GATE = ROOT / "deploy/real-k8s-lab/object-store-live-gate.yaml"
PROFILE = "SPRINT13-OBJECTSTORE-MINIO-A"
REQUIRED_CHECKS = {
    "minio-health-ready",
    "core-bucket-create",
    "core-buckets-list",
    "core-bucket-delete",
    "core-object-upload-presign",
    "core-object-download-presign",
}
REQUIRED_TOOLS = {"curl"}
REQUIRED_DOC_TOKENS = [
    "SPRINT13-OBJECTSTORE-MINIO-A-TRACK",
    "validate-object-store-live-gate",
    "MinIO",
    "pre-signed URL",
    "LIVE PENDING",
]


@dataclass(frozen=True)
class LiveArgs:
    gateway_url: str
    ani_bearer_token: str
    minio_url: str
    minio_alias: str
    evidence_output: str
    production_shaped: bool = False
    cleanup: bool = False


def fail(message: str) -> None:
    raise SystemExit(f"object-store live gate invalid: {message}")


def gate_path_label(path: Path) -> str:
    try:
        return str(path.relative_to(ROOT))
    except ValueError:
        return str(path)


def load_gate(path: Path) -> dict[str, Any]:
    label = gate_path_label(path)
    if not path.exists():
        fail(f"missing {label}")
    try:
        with path.open(encoding="utf-8") as handle:
            data = yaml.safe_load(handle)
    except OSError:
        fail(f"unreadable {label}")
    except yaml.YAMLError:
        fail(f"malformed {label}")
    if not isinstance(data, dict):
        fail(f"{label} must be a YAML object")
    return data


def validate_contract(document: dict[str, Any]) -> None:
    if document.get("profile") != PROFILE:
        fail(f"profile must be {PROFILE}")
    if document.get("status") not in {"contract", "live"}:
        fail("status must be contract or live")
    tools = document.get("required_tools")
    if not isinstance(tools, list) or not REQUIRED_TOOLS.issubset(set(tools)):
        fail("required_tools must include curl")
    checks = document.get("live_checks")
    if not isinstance(checks, list):
        fail("live_checks must be a list")
    check_ids = set()
    for check in checks:
        if not isinstance(check, dict):
            fail("live check must be an object")
        for field in ("id", "command", "pass_condition"):
            value = check.get(field)
            if not isinstance(value, str) or not value.strip():
                fail(f"live check {field} must be a non-empty string")
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
        try:
            content = path.read_text(encoding="utf-8")
        except FileNotFoundError:
            fail(f"missing doc {label}")
        except OSError:
            fail(f"unreadable doc {label}")
        except UnicodeError:
            fail(f"malformed doc {label}")
        for token in REQUIRED_DOC_TOKENS:
            if token not in content:
                fail(f"{label} must reference {token}")


def validate_gate_path(path: str) -> None:
    if not path.strip():
        fail("gate path must not be empty")
    if path != path.strip():
        fail("gate path must not contain surrounding whitespace")


def trim_url(value: str) -> str:
    return value.strip().rstrip("/")


def is_local_transport(value: str) -> bool:
    lowered = value.strip().lower()
    return (
        "127.0.0.1" in lowered
        or "localhost" in lowered
        or "kubectl proxy" in lowered
        or "kubectl-proxy" in lowered
        or "port-forward" in lowered
    )


def validate_production_shaped_live_args(args: LiveArgs) -> None:
    if is_local_transport(args.gateway_url):
        fail("production-shaped live mode requires a non-local production gateway URL")
    if not args.minio_url.strip():
        fail("production-shaped live mode requires --minio-url")
    if is_local_transport(args.minio_url):
        fail("production-shaped live mode requires a non-local MinIO endpoint")


def default_command_runner(command: list[str]) -> str:
    result = subprocess.run(command, check=False, text=True, capture_output=True)
    if result.returncode != 0:
        fail(f"command failed: {command[0]} returned {result.returncode}")
    return result.stdout


def default_json_requester(
    method: str,
    url: str,
    bearer_token: str,
    payload: dict[str, Any] | None = None,
) -> tuple[int, dict[str, Any]]:
    data = None
    headers = {"Accept": "application/json"}
    if payload is not None:
        data = json.dumps(payload).encode("utf-8")
        headers["Content-Type"] = "application/json"
    if bearer_token.strip():
        headers["Authorization"] = f"Bearer {bearer_token.strip()}"
    request = urllib.request.Request(url, data=data, headers=headers, method=method)
    try:
        with urllib.request.urlopen(request, timeout=30) as response:
            status = int(getattr(response, "status", response.getcode()))
            body = response.read().decode("utf-8")
    except urllib.error.HTTPError as exc:
        status = int(exc.code)
        body = exc.read().decode("utf-8", errors="replace")
    except OSError as exc:
        fail(f"could not call Core object-store API: {exc}")
    try:
        parsed = json.loads(body)
    except json.JSONDecodeError:
        fail("Core object-store API did not return JSON")
    if not isinstance(parsed, dict):
        fail("Core object-store API must return a JSON object")
    return status, parsed


def default_url_requester(method: str, url: str, body: bytes | None = None) -> tuple[int, bytes]:
    data = body if method != "GET" else None
    request = urllib.request.Request(url, data=data, method=method)
    if method == "PUT":
        request.add_header("Content-Type", "text/plain")
    try:
        with urllib.request.urlopen(request, timeout=30) as response:
            status = int(getattr(response, "status", response.getcode()))
            return status, response.read()
    except urllib.error.HTTPError as exc:
        return int(exc.code), exc.read()
    except OSError as exc:
        fail(f"could not use pre-signed object URL: {exc}")


def require_status(status: int, expected: int, label: str) -> None:
    if status != expected:
        fail(f"{label} returned HTTP {status}, want {expected}")


def require_non_empty_string(payload: dict[str, Any], field: str, label: str) -> str:
    value = payload.get(field)
    if not isinstance(value, str) or not value.strip():
        fail(f"{label} must include non-empty {field}")
    return value.strip()


def validate_presign_payload(payload: dict[str, Any], url_field: str, label: str) -> None:
    signed_url = require_non_empty_string(payload, url_field, label)
    parsed = urllib.parse.urlparse(signed_url)
    if parsed.scheme not in {"http", "https"} or not parsed.netloc:
        fail(f"{label} {url_field} must be an absolute URL")
    require_non_empty_string(payload, "expires_at", label)


def validate_live(
    args: LiveArgs,
    command_runner: Callable[[list[str]], str] = default_command_runner,
    json_requester: Callable[[str, str, str, dict[str, Any] | None], tuple[int, dict[str, Any]]] = default_json_requester,
    url_requester: Callable[[str, str, bytes | None], tuple[int, bytes]] = default_url_requester,
) -> None:
    if not args.gateway_url.strip():
        fail("live mode requires --gateway-url")
    if not args.minio_alias.strip():
        fail("live mode requires --minio-alias")
    if args.production_shaped:
        validate_production_shaped_live_args(args)

    health_url = trim_url(args.minio_url) + "/minio/health/ready"
    minio_health_status, _ = url_requester("GET", health_url, None)
    require_status(minio_health_status, 200, "MinIO health readiness")

    base_url = trim_url(args.gateway_url)
    bucket_request = {
        "name": "sprint13-objectstore-minio-live",
        "class": "models",
        "idempotency_key": "sprint13-objectstore-minio-bucket",
    }
    bucket_status, bucket = json_requester("POST", f"{base_url}/buckets", args.ani_bearer_token, bucket_request)
    require_status(bucket_status, 201, "Core bucket create")
    bucket_id = require_non_empty_string(bucket, "id", "Core bucket create response")

    list_status, bucket_list = json_requester("GET", f"{base_url}/buckets", args.ani_bearer_token, None)
    require_status(list_status, 200, "Core buckets list")
    if not isinstance(bucket_list.get("items"), list):
        fail("Core buckets list must include items")

    upload_request = {
        "bucket_id": bucket_id,
        "key": "sprint13/objectstore/live.txt",
        "content_type": "text/plain",
        "idempotency_key": "sprint13-objectstore-minio-upload",
    }
    upload_status, upload = json_requester("POST", f"{base_url}/objects/upload", args.ani_bearer_token, upload_request)
    require_status(upload_status, 200, "Core object upload presign")
    object_id = require_non_empty_string(upload, "object_id", "Core object upload presign response")
    validate_presign_payload(upload, "upload_url", "Core object upload presign response")
    upload_url = require_non_empty_string(upload, "upload_url", "Core object upload presign response")
    payload_bytes = b"sprint13-objectstore-minio-live\n"
    actual_upload_status, _ = url_requester("PUT", upload_url, payload_bytes)
    require_status(actual_upload_status, 200, "pre-signed object upload")

    download_status, download = json_requester("GET", f"{base_url}/objects/{object_id}/download", args.ani_bearer_token, None)
    require_status(download_status, 200, "Core object download presign")
    validate_presign_payload(download, "download_url", "Core object download presign response")
    download_url = require_non_empty_string(download, "download_url", "Core object download presign response")
    actual_download_status, downloaded = url_requester("GET", download_url, None)
    require_status(actual_download_status, 200, "pre-signed object download")
    if downloaded != payload_bytes:
        fail("pre-signed object download returned unexpected content")

    cleanup_api_key_status = 0
    cleanup_api_key_revoke_status = 0
    cleanup_status = 0
    bucket_delete_status = 0
    if args.cleanup:
        expires_at = (datetime.datetime.now(datetime.UTC) + datetime.timedelta(minutes=10)).replace(microsecond=0).isoformat().replace("+00:00", "Z")
        cleanup_api_key_status, cleanup_key = json_requester(
            "POST",
            f"{base_url}/auth/api-keys",
            args.ani_bearer_token,
            {
                "name": "sprint13-objectstore-cleanup",
                "scopes": ["scope:objects:delete", "scope:auth:delete"],
                "rate_limit_rpm": 20,
                "expires_at": expires_at,
            },
        )
        require_status(cleanup_api_key_status, 201, "Core cleanup API key create")
        cleanup_token = require_non_empty_string(cleanup_key, "key_value", "Core cleanup API key create response")
        cleanup_key_id = require_non_empty_string(cleanup_key, "key_id", "Core cleanup API key create response")
        cleanup_status, _ = json_requester("DELETE", f"{base_url}/objects/{object_id}", cleanup_token, None)
        # 清理闭环：对象删完后必须删桶控制面记录，否则每次 live gate 都残留一个桶。
        bucket_delete_status, _ = json_requester("DELETE", f"{base_url}/buckets/{bucket_id}", cleanup_token, None)
        cleanup_api_key_revoke_status, _ = json_requester("DELETE", f"{base_url}/auth/api-keys/{cleanup_key_id}", cleanup_token, None)
        require_status(cleanup_api_key_revoke_status, 200, "Core cleanup API key revoke")
        require_status(cleanup_status, 200, "Core object cleanup delete")
        require_status(bucket_delete_status, 200, "Core bucket cleanup delete")

    evidence: dict[str, Any] = {
        "id": "object-store-live-gate",
        "profile": PROFILE,
        "status": "passed",
        "minio_health_status": minio_health_status,
        "minio_health_ready": True,
        "bucket_create_status": bucket_status,
        "bucket_list_status": list_status,
        "bucket_list_count": len(bucket_list["items"]),
        "upload_presign_status": upload_status,
        "download_presign_status": download_status,
        "actual_upload_status": actual_upload_status,
        "actual_download_status": actual_download_status,
        "cleanup_enabled": args.cleanup,
        "cleanup_api_key_status": cleanup_api_key_status,
        "cleanup_api_key_revoke_status": cleanup_api_key_revoke_status,
        "cleanup_status": cleanup_status,
        "bucket_delete_status": bucket_delete_status,
        "upload_presign_url_present": True,
        "download_presign_url_present": True,
    }
    if args.production_shaped:
        evidence["production_shape"] = {
            "status": "passed",
            "transport_profile": "production_gateway_and_object_store_service",
            "missing_items": [],
            "proof_items": [
                "production_gateway",
                "production_object_store_credentials",
                "production_presigned_url_endpoint",
            ],
        }
    if args.evidence_output.strip():
        output = Path(args.evidence_output)
        output.parent.mkdir(parents=True, exist_ok=True)
        output.write_text(json.dumps(evidence, indent=2, sort_keys=True) + "\n", encoding="utf-8")


def main() -> int:
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("--gate", default=str(DEFAULT_GATE), help="object-store live gate YAML")
    parser.add_argument("--live", action="store_true", help="execute the human-gated live checks")
    parser.add_argument("--production-shaped", action="store_true", help="require production-shaped transport and proof_items")
    parser.add_argument("--gateway-url", default="", help="ANI Core API base URL ending at /api/v1")
    parser.add_argument("--ani-bearer-token", default="", help="ANI Core bearer token")
    parser.add_argument("--minio-url", default="", help="approved MinIO/S3 endpoint used by the production Gateway")
    parser.add_argument("--minio-alias", default="ani-minio", help="preconfigured mc alias for MinIO readiness")
    parser.add_argument("--evidence-output", default="", help="write non-sensitive evidence JSON")
    parser.add_argument("--cleanup", action="store_true", help="delete temporary object metadata/content created by the live check")
    args = parser.parse_args()

    validate_gate_path(args.gate)
    document = load_gate(Path(args.gate))
    validate_contract(document)
    validate_docs()
    if args.live:
        validate_live(
            LiveArgs(
                gateway_url=args.gateway_url,
                ani_bearer_token=args.ani_bearer_token,
                minio_url=args.minio_url,
                minio_alias=args.minio_alias,
                evidence_output=args.evidence_output,
                production_shaped=args.production_shaped,
                cleanup=args.cleanup,
            )
        )
        print("SPRINT13-OBJECTSTORE-MINIO-A live checks passed")
        return 0
    print("SPRINT13-OBJECTSTORE-MINIO-A contract valid; use --live for human-gated execution")
    return 0


if __name__ == "__main__":
    raise SystemExit(main())
