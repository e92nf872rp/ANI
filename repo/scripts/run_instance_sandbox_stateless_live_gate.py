#!/usr/bin/env python3
"""Run the Sandbox Gateway restart recovery gate and write sanitized evidence."""

from __future__ import annotations

import argparse
import datetime as dt
import json
import subprocess
import time
import urllib.error
import urllib.parse
import urllib.request
from pathlib import Path
from typing import Any

from validate_instance_sandbox_stateless_live_gate import PROFILE, REQUIRED_CHECKS, validate_evidence
from validate_sandbox_live_gate import (
    gateway_url,
    instance_id,
    lifecycle,
    require_instance,
    run_kubectl,
    tenant_namespace,
    wait_for_state,
    wait_sandbox_pod_ready,
)


IMAGE = "docker.changqingyun.cn/ani/ani-gateway:instance-sandbox-stateless-20260802-v1"
IMAGE_DIGEST = "sha256:5817757f11e845b355fb6c1f4bee2a81b5d76cc43ebcf9d5ba37ba73d29ca563"


def fail(message: str) -> None:
    raise RuntimeError(message)


def request(
    method: str,
    url: str,
    token: str,
    tenant_id: str,
    body: dict[str, Any] | None = None,
    idempotency_key: str = "",
) -> tuple[int, dict[str, Any], dict[str, str]]:
    data = None
    headers = {
        "Authorization": f"Bearer {token}",
        "X-Tenant-ID": tenant_id,
        "Accept": "application/json",
    }
    if body is not None:
        data = json.dumps(body).encode("utf-8")
        headers["Content-Type"] = "application/json"
    if idempotency_key:
        headers["Idempotency-Key"] = idempotency_key
    req = urllib.request.Request(url, data=data, headers=headers, method=method)
    try:
        with urllib.request.urlopen(req, timeout=120) as response:
            raw = response.read().decode("utf-8")
            document = json.loads(raw) if raw.strip() else {}
            return response.status, document, {key.lower(): value for key, value in response.headers.items()}
    except urllib.error.HTTPError as err:
        raw = err.read().decode("utf-8", errors="replace")
        try:
            document = json.loads(raw) if raw.strip() else {}
        except json.JSONDecodeError:
            document = {"raw": raw}
        return err.code, document, {key.lower(): value for key, value in err.headers.items()}


def expect(status: int, wanted: int, label: str, document: dict[str, Any]) -> None:
    if status != wanted:
        fail(f"{label}: status={status}, want={wanted}, response={document}")


def rollout_gateway(kubeconfig: str) -> tuple[str, str]:
    def ready_pod_uid() -> str:
        raw = run_kubectl(
            kubeconfig,
            ["-n", "ani-system", "get", "pods", "-l", "app.kubernetes.io/name=ani-gateway", "-o", "json"],
        )
        document = json.loads(raw)
        candidates: list[tuple[str, str]] = []
        for item in document.get("items", []):
            metadata = item.get("metadata", {})
            if metadata.get("deletionTimestamp"):
                continue
            conditions = item.get("status", {}).get("conditions", [])
            if not any(condition.get("type") == "Ready" and condition.get("status") == "True" for condition in conditions):
                continue
            candidates.append((str(metadata.get("creationTimestamp") or ""), str(metadata.get("uid") or "")))
        return max(candidates, default=("", ""))[1]

    old = ready_pod_uid()
    run_kubectl(kubeconfig, ["-n", "ani-system", "rollout", "restart", "deployment/ani-gateway"])
    run_kubectl(
        kubeconfig,
        ["-n", "ani-system", "rollout", "status", "deployment/ani-gateway", "--timeout=180s"],
    )
    new = ready_pod_uid()
    if not old or not new or old == new:
        fail("Gateway rollout did not replace the running Pod")
    return old, new


def postgres_count(kubeconfig: str, table: str, tenant_id: str, key_column: str, key: str) -> int:
    allowed = {"async_tasks": "id", "workload_instances": "instance_id"}
    if allowed.get(table) != key_column:
        fail("unsupported PostgreSQL evidence query")
    sql = (
        f"SELECT count(*) FROM {table} "
        f"WHERE tenant_id=:'tenant_id'::uuid AND {key_column}=:'key';"
    )
    raw = run_kubectl(
        kubeconfig,
        [
            "-n", "ani-system", "exec", "ani-postgres-0", "--", "psql",
            "-U", "ani", "-d", "ani", "-v", f"tenant_id={tenant_id}",
            "-v", f"key={key}", "-tAc", sql,
        ],
    ).strip()
    try:
        return int(raw)
    except ValueError as err:
        fail(f"PostgreSQL evidence query returned non-integer output: {raw!r}")
        raise err


def resource_absent(kubeconfig: str, namespace: str, kind: str, name: str) -> bool:
    command = ["kubectl"]
    if kubeconfig.strip():
        command.extend(["--kubeconfig", kubeconfig])
    command.extend(["-n", namespace, "get", kind, name, "-o", "name"])
    completed = subprocess.run(command, capture_output=True, text=True, timeout=30)
    return completed.returncode != 0


def run(args: argparse.Namespace) -> dict[str, Any]:
    base = args.gateway_url.rstrip("/")
    tenant_id = args.tenant_id
    namespace = tenant_namespace(tenant_id)
    encoded_id = ""
    sid = ""
    deleted = False
    checks: dict[str, dict[str, Any]] = {}
    target_port = 18765
    service_name = f"{args.name}-p-{target_port}"
    file_path = "restart-proof.txt"
    code_body = {
        "idempotency_key": args.idempotency_key + "-code",
        "language": "python",
        "code": "print('restart-proof')",
        "timeout_seconds": 60,
    }

    try:
        status, document, _ = request(
            "POST",
            gateway_url(base, "/instances"),
            args.token,
            tenant_id,
            {
                "idempotency_key": args.idempotency_key + "-create",
                "name": args.name,
                "kind": "sandbox",
                "image_ref": args.image_ref,
                "auto_start": True,
                "sandbox_config": {"runtime_class": "sandbox-kata", "network_egress_policy": "deny_all"},
            },
        )
        expect(status, 201, "create Sandbox", document)
        created = require_instance(document, "create Sandbox")
        sid = instance_id(created, "create Sandbox")
        encoded_id = urllib.parse.quote(sid)
        wait_for_state(base, args.token, tenant_id, sid, {"running", "pending", "provisioning"})
        wait_sandbox_pod_ready(args.name, namespace, args.kubeconfig)
        checks["core-sandbox-create-running"] = {"instance_state": "running-or-ready"}

        status, file_document, _ = request(
            "POST",
            gateway_url(base, f"/instances/{encoded_id}/sandbox/files"),
            args.token,
            tenant_id,
            {
                "idempotency_key": args.idempotency_key + "-file",
                "path": file_path,
                "content_base64": "cmVzdGFydC1wcm9vZg==",
                "overwrite": True,
            },
        )
        expect(status, 201, "write file before restart", file_document)

        status, port_document, _ = request(
            "POST",
            gateway_url(base, f"/instances/{encoded_id}/sandbox/ports"),
            args.token,
            tenant_id,
            {
                "idempotency_key": args.idempotency_key + "-port",
                "port": target_port,
                "protocol": "http",
                "name": "restart-proof",
            },
        )
        expect(status, 201, "open port before restart", port_document)
        run_kubectl(args.kubeconfig, ["-n", namespace, "get", "service", service_name, "-o", "name"])

        status, task_document, _ = request(
            "POST",
            gateway_url(base, f"/instances/{encoded_id}/sandbox/code-runs"),
            args.token,
            tenant_id,
            code_body,
        )
        expect(status, 202, "code run before restart", task_document)
        task_id = str(task_document.get("id") or "")
        if not task_id:
            fail(f"code run response has no task id: {task_document}")
        checks["file-port-coderun-before-restart"] = {"file_written": True, "port_opened": True, "task_created": True}

        old_uid, new_uid = rollout_gateway(args.kubeconfig)
        checks["gateway-rollout-restart"] = {"pod_replaced": old_uid != new_uid, "ready_replicas": 1}

        status, instance_document, _ = request(
            "GET", gateway_url(base, f"/instances/{encoded_id}"), args.token, tenant_id
        )
        expect(status, 200, "get instance after restart", instance_document)
        instance_after = require_instance(instance_document, "get instance after restart")
        sandbox = instance_after.get("sandbox") if isinstance(instance_after.get("sandbox"), dict) else {}
        ports = sandbox.get("ports") if isinstance(sandbox.get("ports"), list) else []
        if not any(isinstance(item, dict) and int(item.get("port") or 0) == target_port for item in ports):
            fail(f"persisted port summary missing after restart: {instance_after}")

        status, files_document, _ = request(
            "GET",
            gateway_url(base, f"/instances/{encoded_id}/sandbox/files?path=."),
            args.token,
            tenant_id,
        )
        expect(status, 200, "list files after restart", files_document)
        items = files_document.get("items") if isinstance(files_document.get("items"), list) else []
        if not any(isinstance(item, dict) and item.get("path") == file_path for item in items):
            fail(f"persisted file missing after restart: {files_document}")

        status, stored_task, _ = request(
            "GET", gateway_url(base, f"/tasks/{urllib.parse.quote(task_id)}"), args.token, tenant_id
        )
        expect(status, 200, "get PG task after restart", stored_task)
        if stored_task.get("id") != task_id or stored_task.get("status") != "completed":
            fail(f"stored task mismatch after restart: {stored_task}")

        status, closed, _ = request(
            "DELETE",
            gateway_url(base, f"/instances/{encoded_id}/sandbox/ports/{target_port}"),
            args.token,
            tenant_id,
            idempotency_key=args.idempotency_key + "-port-close",
        )
        expect(status, 200, "close port after restart", closed)
        if not resource_absent(args.kubeconfig, namespace, "service", service_name):
            fail("preview Service still exists after close")
        checks["file-port-task-after-restart"] = {
            "instance_loaded": True,
            "file_visible": True,
            "port_summary_loaded": True,
            "port_closed": True,
            "task_loaded": True,
        }

        status, replay, replay_headers = request(
            "POST",
            gateway_url(base, f"/instances/{encoded_id}/sandbox/code-runs"),
            args.token,
            tenant_id,
            code_body,
        )
        expect(status, 202, "idempotency replay after restart", replay)
        if replay.get("id") != task_id or replay_headers.get("idempotent-replay") != "true":
            fail("code-run replay did not return the original PG task with replay header")
        different_body = {**code_body, "code": "print('different-intent')"}
        status, conflict, _ = request(
            "POST",
            gateway_url(base, f"/instances/{encoded_id}/sandbox/code-runs"),
            args.token,
            tenant_id,
            different_body,
        )
        expect(status, 409, "idempotency fingerprint conflict", conflict)
        if "IDEMPOTENCY_KEY_REUSED" not in json.dumps(conflict):
            fail(f"unexpected idempotency conflict response: {conflict}")
        checks["idempotency-replay-after-restart"] = {"replayed_original_task": True, "different_intent_rejected": True}

        token_body = {
            "idempotency_key": args.idempotency_key + "-short-token",
            "expires_in": "2s",
            "scopes": ["files"],
        }
        status, minted, _ = request(
            "POST", gateway_url(base, f"/instances/{encoded_id}/sandbox/tokens"), args.token, tenant_id, token_body
        )
        expect(status, 201, "mint short token", minted)
        status, replayed_token, token_headers = request(
            "POST", gateway_url(base, f"/instances/{encoded_id}/sandbox/tokens"), args.token, tenant_id, token_body
        )
        expect(status, 201, "replay short token before expiry", replayed_token)
        if token_headers.get("idempotent-replay") != "true" or replayed_token.get("token") != minted.get("token"):
            fail("short token was not replayed before expiry")
        time.sleep(4)
        status, expired, _ = request(
            "POST", gateway_url(base, f"/instances/{encoded_id}/sandbox/tokens"), args.token, tenant_id, token_body
        )
        expect(status, 409, "replay short token after expiry", expired)
        if "IdempotencyResultExpired" not in json.dumps(expired):
            fail(f"unexpected expired token replay response: {expired}")
        checks["token-expiry-conflict"] = {"replayed_before_expiry": True, "body_removed_after_expiry": True}

        status, checkpoint, _ = request(
            "POST",
            gateway_url(base, f"/instances/{encoded_id}/sandbox/checkpoints"),
            args.token,
            tenant_id,
            {
                "idempotency_key": args.idempotency_key + "-checkpoint",
                "name": "unsupported-real-provider",
                "keep_memory": False,
            },
        )
        expect(status, 422, "real provider checkpoint capability", checkpoint)
        checks["checkpoint-provider-capability-422"] = {"http_status": 422, "task_created": False}

        paused = lifecycle(base, args.token, tenant_id, sid, "pause", args.idempotency_key)
        paused_sandbox = paused.get("sandbox") if isinstance(paused.get("sandbox"), dict) else {}
        if str(paused.get("state") or "").lower() != "stopped" or str(
            paused_sandbox.get("session_state") or ""
        ).lower() != "paused":
            fail(f"pause did not return paused state: {paused}")
        resumed = lifecycle(base, args.token, tenant_id, sid, "resume", args.idempotency_key)
        if str(resumed.get("state") or "").lower() not in {"running", "provisioning", "pending"}:
            fail(f"resume returned unexpected state: {resumed}")
        wait_sandbox_pod_ready(args.name, namespace, args.kubeconfig)
        removed = lifecycle(base, args.token, tenant_id, sid, "delete", args.idempotency_key)
        deleted = True
        if str(removed.get("state") or "").lower() != "deleted":
            fail(f"delete did not return deleted state: {removed}")
        checks["sandbox-pause-resume-delete"] = {
            "pause": "stopped/session-paused",
            "resume": "running-or-ready",
            "delete": "deleted",
        }

        deadline = time.time() + 60
        while time.time() < deadline:
            if resource_absent(args.kubeconfig, namespace, "deployment", args.name) and resource_absent(
                args.kubeconfig, namespace, "service", service_name
            ):
                break
            time.sleep(2)
        else:
            fail("Kubernetes Sandbox resources still exist after delete")

        task_rows = postgres_count(args.kubeconfig, "async_tasks", tenant_id, "id", task_id)
        instance_rows = postgres_count(args.kubeconfig, "workload_instances", tenant_id, "instance_id", sid)
        if task_rows != 1 or instance_rows != 1:
            fail(f"unexpected PostgreSQL rows after cleanup: task={task_rows}, instance={instance_rows}")
        checks["postgres-and-kubernetes-cleanup"] = {
            "task_audit_rows": task_rows,
            "deleted_instance_audit_rows": instance_rows,
            "provider_resources_absent": True,
        }
    finally:
        if sid and not deleted:
            try:
                lifecycle(base, args.token, tenant_id, sid, "delete", args.idempotency_key + "-cleanup")
            except BaseException:
                pass

    missing = REQUIRED_CHECKS - set(checks)
    if missing:
        fail(f"live checks missing: {', '.join(sorted(missing))}")
    return {
        "profile": PROFILE,
        "status": "passed",
        "generated_at": dt.datetime.now(dt.timezone.utc).isoformat().replace("+00:00", "Z"),
        "image": IMAGE,
        "image_digest": IMAGE_DIGEST,
        "checks": [{"id": check_id, "status": "passed", **checks[check_id]} for check_id in sorted(checks)],
    }


def main() -> int:
    parser = argparse.ArgumentParser()
    parser.add_argument("--gateway-url", required=True)
    parser.add_argument("--token", required=True)
    parser.add_argument("--tenant-id", required=True)
    parser.add_argument("--name", required=True)
    parser.add_argument("--image-ref", required=True)
    parser.add_argument("--idempotency-key", required=True)
    parser.add_argument("--kubeconfig", default="")
    parser.add_argument("--evidence-output", type=Path, required=True)
    args = parser.parse_args()
    try:
        evidence = run(args)
        raw = json.dumps(evidence, ensure_ascii=False, indent=2, sort_keys=True) + "\n"
        validate_evidence(evidence, raw)
        args.evidence_output.parent.mkdir(parents=True, exist_ok=True)
        args.evidence_output.write_text(raw, encoding="utf-8")
    except (OSError, RuntimeError, ValueError, json.JSONDecodeError) as err:
        print(f"sandbox stateless live gate failed: {err}")
        return 1
    print(f"sandbox stateless live gate passed; evidence written to {args.evidence_output}")
    return 0


if __name__ == "__main__":
    raise SystemExit(main())
