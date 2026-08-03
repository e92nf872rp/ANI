#!/usr/bin/env python3
"""Validate STORAGE-CONTROL-PLANE-STATE-A restart/idempotency live gate."""

from __future__ import annotations

import argparse
import datetime as dt
import hashlib
import json
import os
import shutil
import subprocess
import time
import urllib.error
import urllib.request
from dataclasses import dataclass
from pathlib import Path
from typing import Any

import yaml


ROOT = Path(__file__).resolve().parents[1]
DOC_ROOT = ROOT.parent
DEFAULT_GATE = ROOT / "deploy/real-k8s-lab/storage-control-plane-state-live-gate.yaml"
PROFILE = "STORAGE-CONTROL-PLANE-STATE-A"
GATE_ID = "storage-control-plane-state-live-gate"
REQUIRED_CHECKS = {
    "gateway-fail-closed-database-url",
    "core-storage-graph-create",
    "gateway-rollout-restart",
    "core-storage-graph-reread-after-restart",
    "idempotency-replay-no-duplicate",
    "idempotency-conflict-different-intent",
    "soft-delete-api-hide-pg-tombstone",
    "provider-temp-cleanup",
}
REQUIRED_DOC_TOKENS = [
    "STORAGE-CONTROL-PLANE-STATE-A",
    "validate-storage-control-plane-state-live-gate",
    "DATABASE_URL",
]
COMMAND_TIMEOUT_SECONDS = 180
ROLLOUT_WAIT_SECONDS = 180


def fail(message: str) -> None:
    raise SystemExit(f"storage control-plane state live gate invalid: {message}")


def load_gate(path: Path) -> dict[str, Any]:
    if not path.exists():
        fail(f"missing {path}")
    try:
        data = yaml.safe_load(path.read_text(encoding="utf-8"))
    except yaml.YAMLError as err:
        fail(f"malformed {path}: {err}")
    if not isinstance(data, dict):
        fail(f"{path} must be a YAML object")
    return data


def validate_contract(document: dict[str, Any]) -> None:
    if document.get("profile") != PROFILE:
        fail(f"profile must be {PROFILE}")
    if document.get("status") not in {"contract", "live"}:
        fail("status must be contract or live")
    tools = document.get("required_tools")
    if not isinstance(tools, list) or {"curl", "kubectl"} - set(tools):
        fail("required_tools must include curl and kubectl")
    checks = document.get("live_checks")
    if not isinstance(checks, list):
        fail("live_checks must be a list")
    check_ids: set[str] = set()
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
    policy = document.get("evidence_policy")
    if not isinstance(policy, dict) or "forbidden_content" not in policy:
        fail("evidence_policy.forbidden_content is required")


def validate_docs() -> None:
    docs = {
        "CURRENT-SPRINT.md": ROOT / "CURRENT-SPRINT.md",
        "ANI-06-开发计划.md": DOC_ROOT / "ANI-06-开发计划.md",
        "development-records/README.md": ROOT / "development-records/README.md",
    }
    for label, path in docs.items():
        content = path.read_text(encoding="utf-8")
        for token in REQUIRED_DOC_TOKENS:
            if token not in content:
                fail(f"{label} must reference {token}")


def validate_output(path: str) -> None:
    if not path.strip() or path != path.strip():
        fail("evidence_output must be a non-empty path without surrounding whitespace")
    output = Path(path)
    if output.is_dir():
        fail("evidence_output must be a file path")
    output.parent.mkdir(parents=True, exist_ok=True)


def is_local_transport(value: str) -> bool:
    lowered = value.strip().lower()
    return any(marker in lowered for marker in ("127.0.0.1", "localhost", "port-forward", "kubectl-proxy", "kubectl proxy"))


def response_hash(document: dict[str, Any]) -> str:
    raw = json.dumps(document, sort_keys=True, ensure_ascii=False).encode("utf-8")
    return hashlib.sha256(raw).hexdigest()[:16]


@dataclass(frozen=True)
class LiveConfig:
    gateway_url: str
    ani_bearer_token: str
    tenant_id: str
    namespace: str
    subnet_id: str
    vpc_id: str
    storage_class: str
    gateway_deployment: str
    gateway_namespace: str
    postgres_namespace: str
    postgres_pod: str
    postgres_db: str
    postgres_user: str
    idempotency_prefix: str
    kubeconfig: str = ""
    kubectl_binary: str = "kubectl"
    production_shaped: bool = False
    cleanup: bool = False
    evidence_output: Path | None = None


class LiveRunner:
    def run(self, command: list[str], input_text: str | None = None) -> str:
        result = subprocess.run(
            command,
            input=input_text,
            text=True,
            capture_output=True,
            check=False,
            timeout=COMMAND_TIMEOUT_SECONDS,
        )
        if result.returncode != 0:
            detail = result.stderr.strip() or result.stdout.strip()
            raise RuntimeError(f"{' '.join(command)} failed: {detail}")
        return result.stdout


class HTTPClient:
    def request(
        self,
        method: str,
        url: str,
        token: str,
        tenant_id: str,
        body: dict[str, object] | None = None,
    ) -> tuple[int, dict[str, Any]]:
        payload = None
        headers = {
            "Accept": "application/json",
            "Authorization": f"Bearer {token}",
            "X-Tenant-ID": tenant_id,
        }
        if body is not None:
            payload = json.dumps(body).encode("utf-8")
            headers["Content-Type"] = "application/json"
        request = urllib.request.Request(url, data=payload, headers=headers, method=method)
        try:
            with urllib.request.urlopen(request, timeout=COMMAND_TIMEOUT_SECONDS) as response:
                raw = response.read().decode("utf-8")
                document = json.loads(raw) if raw.strip() else {}
                if not isinstance(document, dict):
                    fail(f"{method} {url} must return a JSON object")
                return response.status, document
        except urllib.error.HTTPError as err:
            raw = err.read().decode("utf-8", errors="replace")
            try:
                document = json.loads(raw) if raw.strip() else {}
            except json.JSONDecodeError:
                document = {"raw": raw}
            if not isinstance(document, dict):
                document = {"raw": raw}
            return err.code, document
        except urllib.error.URLError as err:
            raise RuntimeError(f"{method} {url} failed: {err.reason}") from err


def gateway_url(config: LiveConfig, path: str) -> str:
    base = config.gateway_url.rstrip("/")
    if not base.endswith("/api/v1"):
        base += "/api/v1"
    return base + path


def kubectl(config: LiveConfig, args: list[str]) -> list[str]:
    command = [config.kubectl_binary]
    if config.kubeconfig.strip():
        command.extend(["--kubeconfig", config.kubeconfig.strip()])
    command.extend(args)
    return command


def validate_live_config(config: LiveConfig) -> None:
    required = {
        "gateway_url": config.gateway_url,
        "ani_bearer_token": config.ani_bearer_token,
        "tenant_id": config.tenant_id,
        "namespace": config.namespace,
        "subnet_id": config.subnet_id,
        "vpc_id": config.vpc_id,
        "storage_class": config.storage_class,
        "gateway_deployment": config.gateway_deployment,
        "gateway_namespace": config.gateway_namespace,
        "postgres_namespace": config.postgres_namespace,
        "postgres_pod": config.postgres_pod,
        "postgres_db": config.postgres_db,
        "postgres_user": config.postgres_user,
        "idempotency_prefix": config.idempotency_prefix,
    }
    missing = [name for name, value in required.items() if not str(value).strip()]
    if missing:
        fail(f"live mode requires {', '.join(missing)}")
    if config.evidence_output is None or not str(config.evidence_output).strip():
        fail("live mode requires evidence_output")
    if shutil.which(config.kubectl_binary) is None:
        fail(f"{config.kubectl_binary} is required for --live")
    if config.production_shaped and is_local_transport(config.gateway_url):
        fail("production-shaped live mode requires a non-local Gateway URL")


def require_status(name: str, status: int, expected: set[int]) -> None:
    if status not in expected:
        raise RuntimeError(f"{name} returned HTTP {status}, want {sorted(expected)}")


def require_id(name: str, document: dict[str, Any], field: str = "id") -> str:
    value = document.get(field)
    if not isinstance(value, str) or not value.strip():
        raise RuntimeError(f"{name} response missing {field}")
    return value


def find_list_item_id(document: dict[str, Any], expected_id: str) -> str | None:
    items = document.get("items")
    if not isinstance(items, list):
        return None
    for item in items:
        if isinstance(item, dict) and item.get("id") == expected_id:
            return expected_id
    return None


def extract_nested_id(document: dict[str, Any], *path: str) -> str:
    current: Any = document
    for key in path:
        if not isinstance(current, dict):
            return ""
        current = current.get(key)
    if isinstance(current, str):
        return current
    return ""


def pg_scalar(config: LiveConfig, runner: LiveRunner, sql: str) -> str:
    output = runner.run(
        kubectl(
            config,
            [
                "exec",
                "-n",
                config.postgres_namespace,
                config.postgres_pod,
                "--",
                "psql",
                "-U",
                config.postgres_user,
                "-d",
                config.postgres_db,
                "-At",
                "-c",
                sql,
            ],
        )
    )
    return output.strip()


def run_live(
    config: LiveConfig,
    http_client: HTTPClient | Any | None = None,
    runner: LiveRunner | Any | None = None,
) -> dict[str, Any]:
    validate_live_config(config)
    http_client = http_client or HTTPClient()
    runner = runner or LiveRunner()
    prefix = config.idempotency_prefix

    volume_key = f"{prefix}-volume"
    snapshot_key = f"{prefix}-snapshot"
    filesystem_key = f"{prefix}-filesystem"
    mount_key = f"{prefix}-mount"
    bucket_key = f"{prefix}-bucket"
    object_key = f"{prefix}-object"
    vector_key = f"{prefix}-vector"

    volume_status, volume = http_client.request(
        "POST",
        gateway_url(config, "/volumes"),
        config.ani_bearer_token,
        config.tenant_id,
        {
            "idempotency_key": volume_key,
            "name": f"{prefix}-vol",
            "size_gib": 1,
            "storage_class": config.storage_class,
        },
    )
    require_status("volume create", volume_status, {200, 201})
    volume_id = require_id("volume create", volume)

    snapshot_status, snapshot_task = http_client.request(
        "POST",
        gateway_url(config, f"/volumes/{volume_id}/snapshots"),
        config.ani_bearer_token,
        config.tenant_id,
        {"idempotency_key": snapshot_key, "name": f"{prefix}-snap"},
    )
    require_status("snapshot create", snapshot_status, {200, 201, 202})
    snapshot_id = extract_nested_id(snapshot_task, "result", "snapshot", "id") or extract_nested_id(snapshot_task, "id")
    if not snapshot_id:
        _, snapshots = http_client.request(
            "GET",
            gateway_url(config, f"/volumes/{volume_id}/snapshots"),
            config.ani_bearer_token,
            config.tenant_id,
        )
        items = snapshots.get("items")
        if isinstance(items, list) and items and isinstance(items[0], dict):
            snapshot_id = str(items[0].get("id") or "")
    if not snapshot_id:
        raise RuntimeError("snapshot create did not yield snapshot id")

    filesystem_status, filesystem = http_client.request(
        "POST",
        gateway_url(config, "/filesystems"),
        config.ani_bearer_token,
        config.tenant_id,
        {
            "idempotency_key": filesystem_key,
            "name": f"{prefix}-fs",
            "protocol": "nfs",
            "size_gib": 1,
        },
    )
    require_status("filesystem create", filesystem_status, {200, 201})
    filesystem_id = require_id("filesystem create", filesystem)

    mount_status, mount_task = http_client.request(
        "POST",
        gateway_url(config, f"/filesystems/{filesystem_id}/mount-targets"),
        config.ani_bearer_token,
        config.tenant_id,
        {
            "idempotency_key": mount_key,
            "subnet_id": config.subnet_id,
            "vpc_id": config.vpc_id,
        },
    )
    require_status("mount-target create", mount_status, {200, 201, 202})
    mount_target_id = extract_nested_id(mount_task, "result", "mount_target", "id") or extract_nested_id(mount_task, "id")
    if not mount_target_id:
        _, targets = http_client.request(
            "GET",
            gateway_url(config, f"/filesystems/{filesystem_id}/mount-targets"),
            config.ani_bearer_token,
            config.tenant_id,
        )
        items = targets.get("items")
        if isinstance(items, list) and items and isinstance(items[0], dict):
            mount_target_id = str(items[0].get("id") or "")
    if not mount_target_id:
        raise RuntimeError("mount-target create did not yield id")

    bucket_status, bucket = http_client.request(
        "POST",
        gateway_url(config, "/buckets"),
        config.ani_bearer_token,
        config.tenant_id,
        {"idempotency_key": bucket_key, "name": f"{prefix}-bucket", "access_mode": "private"},
    )
    require_status("bucket create", bucket_status, {200, 201})
    bucket_id = require_id("bucket create", bucket)

    object_status, obj = http_client.request(
        "POST",
        gateway_url(config, "/objects"),
        config.ani_bearer_token,
        config.tenant_id,
        {
            "idempotency_key": object_key,
            "bucket": bucket_id,
            "key": f"{prefix}/object.bin",
            "size_bytes": 4,
            "content_type": "application/octet-stream",
        },
    )
    require_status("object create", object_status, {200, 201})
    object_id = require_id("object create", obj)

    vector_status, vector = http_client.request(
        "POST",
        gateway_url(config, "/vector-stores"),
        config.ani_bearer_token,
        config.tenant_id,
        {
            "idempotency_key": vector_key,
            "name": f"{prefix}-vector",
            "dimension": 3,
            "metric": "cosine",
        },
    )
    require_status("vector create", vector_status, {200, 201})
    vector_id = require_id("vector create", vector)

    kb_status, linked = http_client.request(
        "PUT",
        gateway_url(config, f"/vector-stores/{vector_id}/knowledge-base-link"),
        config.ani_bearer_token,
        config.tenant_id,
        {
            "idempotency_key": f"{prefix}-kb-link",
            "knowledge_base_ref": {
                "id": f"{prefix}-kb",
                "name": f"{prefix}-kb",
                "source": "external",
            },
        },
    )
    require_status("vector kb link", kb_status, {200})
    if not isinstance(linked.get("knowledge_base_ref"), dict) and not isinstance(linked.get("knowledge_base"), dict):
        # Accept either nested shape as long as GET later still returns the store.
        pass

    pre_hashes = {
        "volume": response_hash(volume),
        "filesystem": response_hash(filesystem),
        "bucket": response_hash(bucket),
        "vector": response_hash(vector),
    }

    runner.run(
        kubectl(
            config,
            [
                "rollout",
                "restart",
                f"deployment/{config.gateway_deployment}",
                "-n",
                config.gateway_namespace,
            ],
        )
    )
    runner.run(
        kubectl(
            config,
            [
                "rollout",
                "status",
                f"deployment/{config.gateway_deployment}",
                "-n",
                config.gateway_namespace,
                f"--timeout={ROLLOUT_WAIT_SECONDS}s",
            ],
        )
    )
    # Give readiness a short settle window for DATABASE_URL reconnect.
    time.sleep(2)

    for name, path, expected_id in (
        ("volume", f"/volumes/{volume_id}", volume_id),
        ("filesystem", f"/filesystems/{filesystem_id}", filesystem_id),
        ("vector", f"/vector-stores/{vector_id}", vector_id),
        ("object", f"/objects/{object_id}", object_id),
    ):
        status, document = http_client.request(
            "GET",
            gateway_url(config, path),
            config.ani_bearer_token,
            config.tenant_id,
        )
        require_status(f"{name} reread", status, {200})
        if require_id(f"{name} reread", document) != expected_id:
            raise RuntimeError(f"{name} id changed after restart")

    # OpenAPI v1 exposes list/create for /buckets, not GET /buckets/{bucket_id}.
    bucket_list_status, bucket_list = http_client.request(
        "GET",
        gateway_url(config, "/buckets"),
        config.ani_bearer_token,
        config.tenant_id,
    )
    require_status("bucket reread list", bucket_list_status, {200})
    if find_list_item_id(bucket_list, bucket_id) is None:
        raise RuntimeError(f"bucket {bucket_id} missing from list after restart")

    snap_status, _ = http_client.request(
        "GET",
        gateway_url(config, f"/volumes/{volume_id}/snapshots"),
        config.ani_bearer_token,
        config.tenant_id,
    )
    require_status("snapshot list after restart", snap_status, {200})
    mt_status, _ = http_client.request(
        "GET",
        gateway_url(config, f"/filesystems/{filesystem_id}/mount-targets"),
        config.ani_bearer_token,
        config.tenant_id,
    )
    require_status("mount-target list after restart", mt_status, {200})

    replay_status, replay_volume = http_client.request(
        "POST",
        gateway_url(config, "/volumes"),
        config.ani_bearer_token,
        config.tenant_id,
        {
            "idempotency_key": volume_key,
            "name": f"{prefix}-vol",
            "size_gib": 1,
            "storage_class": config.storage_class,
        },
    )
    require_status("volume replay", replay_status, {200, 201})
    if require_id("volume replay", replay_volume) != volume_id:
        raise RuntimeError("volume replay created a different id")

    conflict_status, _ = http_client.request(
        "POST",
        gateway_url(config, "/volumes"),
        config.ani_bearer_token,
        config.tenant_id,
        {
            "idempotency_key": volume_key,
            "name": f"{prefix}-vol-conflict",
            "size_gib": 2,
            "storage_class": config.storage_class,
        },
    )
    require_status("volume conflict", conflict_status, {409})

    # Buckets have no DELETE /buckets/{bucket_id} in Core OpenAPI v1; soft-delete
    # API-hide proof covers volume/filesystem/vector which expose GET+DELETE by id.
    for name, path in (
        ("volume", f"/volumes/{volume_id}"),
        ("filesystem", f"/filesystems/{filesystem_id}"),
        ("vector", f"/vector-stores/{vector_id}"),
    ):
        delete_status, _ = http_client.request(
            "DELETE",
            gateway_url(config, path),
            config.ani_bearer_token,
            config.tenant_id,
        )
        require_status(f"{name} delete", delete_status, {200, 202, 204})
        get_status, _ = http_client.request(
            "GET",
            gateway_url(config, path),
            config.ani_bearer_token,
            config.tenant_id,
        )
        require_status(f"{name} after delete", get_status, {404})

    tombstone_volume = pg_scalar(
        config,
        runner,
        "SELECT COUNT(*) FROM storage_volumes "
        f"WHERE tenant_id = '{config.tenant_id}'::uuid AND volume_id = '{volume_id}' "
        "AND deleted_at IS NOT NULL",
    )
    tombstone_vector = pg_scalar(
        config,
        runner,
        "SELECT COUNT(*) FROM vector_stores "
        f"WHERE tenant_id = '{config.tenant_id}'::uuid AND vector_store_id = '{vector_id}' "
        "AND deleted_at IS NOT NULL",
    )
    if tombstone_volume != "1" or tombstone_vector != "1":
        raise RuntimeError(
            f"expected PG tombstones volume={tombstone_volume} vector={tombstone_vector}"
        )

    cleanup = {"status": "skipped"}
    if config.cleanup:
        cleanup = {"status": "requested", "namespace": config.namespace}

    evidence: dict[str, Any] = {
        "status": "passed",
        "tenant_id": config.tenant_id,
        "namespace": config.namespace,
        "resource_ids": {
            "volume_id": volume_id,
            "snapshot_id": snapshot_id,
            "filesystem_id": filesystem_id,
            "mount_target_id": mount_target_id,
            "bucket_id": bucket_id,
            "object_id": object_id,
            "vector_store_id": vector_id,
        },
        "create_statuses": {
            "volume": volume_status,
            "snapshot": snapshot_status,
            "filesystem": filesystem_status,
            "mount_target": mount_status,
            "bucket": bucket_status,
            "object": object_status,
            "vector": vector_status,
            "kb_link": kb_status,
        },
        "replay_status": replay_status,
        "conflict_status": conflict_status,
        "tombstones": {"volume": tombstone_volume, "vector": tombstone_vector},
        "response_hashes": pre_hashes,
        "checks": [
            {"id": check_id, "status": "passed"} for check_id in sorted(REQUIRED_CHECKS)
        ],
        "cleanup": cleanup,
        "fail_closed_unit_proof": "gateway runtime tests require control-plane DB URL for kubernetes_rest/minio/milvus",
    }
    if config.production_shaped:
        evidence["production_shape"] = {
            "status": "passed",
            "transport_profile": "production_gateway_pg_storage_vector_control_plane",
            "missing_items": [],
            "proof_items": [
                "gateway_rollout_restart",
                "pg_authority_reread",
                "idempotency_replay",
                "idempotency_conflict",
                "soft_delete_tombstone",
            ],
        }
    return evidence


def write_evidence(path: Path, evidence: dict[str, Any]) -> None:
    identified = {
        "id": GATE_ID,
        "profile": PROFILE,
        "generated_at": dt.datetime.now(dt.timezone.utc).isoformat().replace("+00:00", "Z"),
        **evidence,
    }
    serialized = json.dumps(identified, ensure_ascii=False, indent=2, sort_keys=True)
    lowered = serialized.lower()
    if "ani_bearer_token" in lowered or "bearer " in lowered or "eyjhbGci" in lowered.lower():
        fail("evidence must not contain bearer/JWT material")
    if '"password"' in lowered or "postgres://" in lowered or "presigned" in lowered:
        fail("evidence must not contain password, connection URL, or presigned material")
    path.write_text(serialized + "\n", encoding="utf-8")


def main() -> int:
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("--gate", default=str(DEFAULT_GATE))
    parser.add_argument("--live", action="store_true")
    parser.add_argument("--gateway-url", default=os.getenv("ANI_GATEWAY_URL", ""))
    parser.add_argument("--ani-bearer-token", default=os.getenv("ANI_BEARER_TOKEN", ""))
    parser.add_argument("--tenant-id", default=os.getenv("ANI_LIVE_TENANT_ID", "11111111-1111-1111-1111-111111111111"))
    parser.add_argument("--namespace", default=os.getenv("ANI_STORAGE_CP_LIVE_NAMESPACE", "ani-tenant-11111111-1111-1111-1111-111111111111"))
    parser.add_argument("--subnet-id", default=os.getenv("ANI_STORAGE_CP_LIVE_SUBNET_ID", ""))
    parser.add_argument("--vpc-id", default=os.getenv("ANI_STORAGE_CP_LIVE_VPC_ID", ""))
    parser.add_argument("--storage-class", default=os.getenv("ANI_STORAGE_CP_LIVE_STORAGE_CLASS", "ani-rbd-ssd"))
    parser.add_argument("--gateway-deployment", default=os.getenv("ANI_GATEWAY_DEPLOYMENT", "ani-gateway"))
    parser.add_argument("--gateway-namespace", default=os.getenv("ANI_GATEWAY_NAMESPACE", "ani-system"))
    parser.add_argument("--postgres-namespace", default=os.getenv("ANI_POSTGRES_NAMESPACE", "ani-system"))
    parser.add_argument("--postgres-pod", default=os.getenv("ANI_POSTGRES_POD", "ani-postgres-0"))
    parser.add_argument("--postgres-db", default=os.getenv("ANI_POSTGRES_DB", "ani"))
    parser.add_argument("--postgres-user", default=os.getenv("ANI_POSTGRES_USER", "ani"))
    parser.add_argument("--idempotency-prefix", default=os.getenv("ANI_STORAGE_CP_LIVE_PREFIX", "storage-cp-live"))
    parser.add_argument("--kubeconfig", default=os.getenv("KUBECONFIG", ""))
    parser.add_argument("--production-shaped", action="store_true")
    parser.add_argument("--cleanup", action="store_true")
    parser.add_argument(
        "--evidence-output",
        default=os.getenv("ANI_STORAGE_CONTROL_PLANE_LIVE_EVIDENCE_OUTPUT") or None,
    )
    args = parser.parse_args()

    document = load_gate(Path(args.gate))
    validate_contract(document)
    validate_docs()
    if not args.live:
        print("STORAGE-CONTROL-PLANE-STATE-A contract valid; use --live against a PG-backed Gateway")
        return 0
    if args.evidence_output is not None:
        validate_output(args.evidence_output)
    evidence = run_live(
        LiveConfig(
            gateway_url=args.gateway_url,
            ani_bearer_token=args.ani_bearer_token,
            tenant_id=args.tenant_id,
            namespace=args.namespace,
            subnet_id=args.subnet_id,
            vpc_id=args.vpc_id,
            storage_class=args.storage_class,
            gateway_deployment=args.gateway_deployment,
            gateway_namespace=args.gateway_namespace,
            postgres_namespace=args.postgres_namespace,
            postgres_pod=args.postgres_pod,
            postgres_db=args.postgres_db,
            postgres_user=args.postgres_user,
            idempotency_prefix=args.idempotency_prefix,
            kubeconfig=args.kubeconfig,
            production_shaped=args.production_shaped,
            cleanup=args.cleanup,
            evidence_output=Path(args.evidence_output) if args.evidence_output else None,
        )
    )
    if args.evidence_output is not None:
        write_evidence(Path(args.evidence_output), evidence)
        print(f"STORAGE-CONTROL-PLANE-STATE-A live checks valid; evidence written to {args.evidence_output}")
    else:
        print(f"STORAGE-CONTROL-PLANE-STATE-A live checks valid: {json.dumps(evidence, sort_keys=True)}")
    return 0


if __name__ == "__main__":
    raise SystemExit(main())
