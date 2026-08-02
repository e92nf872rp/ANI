#!/usr/bin/env python3
"""Validate Kubernetes provider-loss reconciliation through ANI Core."""

from __future__ import annotations

import argparse
import datetime as dt
import json
import os
import shutil
import subprocess
import time
import urllib.parse
from pathlib import Path
from typing import Any

import yaml

import validate_sandbox_live_gate as sandbox_gate


ROOT = Path(__file__).resolve().parents[1]
DEFAULT_GATE = ROOT / "deploy/real-k8s-lab/instance-reconcile-provider-loss-live-gate.yaml"
PROFILE = "INSTANCE-RECONCILE-PROVIDER-404-A"
GATE_ID = "instance-reconcile-provider-loss-live-gate"
REQUIRED_CHECKS = {
    "core-instance-create-running",
    "kubernetes-provider-resource-delete",
    "reconcile-provider-resource-lost",
    "reconcile-provider-resource-lost-idempotent",
    "core-instance-cleanup",
}


def fail(message: str) -> None:
    raise SystemExit(f"instance reconcile provider-loss live gate invalid: {message}")


def load_gate(path: Path) -> dict[str, Any]:
    if not path.exists():
        fail(f"missing {path}")
    try:
        document = yaml.safe_load(path.read_text(encoding="utf-8"))
    except yaml.YAMLError as err:
        fail(f"malformed {path}: {err}")
    if not isinstance(document, dict):
        fail(f"{path} must be a YAML object")
    return document


def validate_contract(document: dict[str, Any]) -> None:
    if document.get("profile") != PROFILE:
        fail(f"profile must be {PROFILE}")
    if document.get("status") not in {"contract", "live"}:
        fail("status must be contract or live")
    tools = document.get("required_tools")
    if not isinstance(tools, list) or "kubectl" not in tools:
        fail("required_tools must include kubectl")
    endpoints = document.get("required_endpoints")
    required_endpoints = {"ani_core_instances_api", "kubernetes_workload_api"}
    if not isinstance(endpoints, list) or required_endpoints - set(endpoints):
        fail("required_endpoints must include Core instances and Kubernetes workload APIs")
    checks = document.get("live_checks")
    if not isinstance(checks, list):
        fail("live_checks must be a list")
    check_ids = {check.get("id") for check in checks if isinstance(check, dict)}
    missing = REQUIRED_CHECKS - check_ids
    if missing:
        fail(f"missing live checks: {', '.join(sorted(missing))}")
    for check in checks:
        if not isinstance(check, dict):
            fail("live check must be an object")
        for field in ("id", "command", "pass_condition"):
            value = check.get(field)
            if not isinstance(value, str) or not value.strip():
                fail(f"live check {field} must be a non-empty string")


def is_provider_lost(instance: dict[str, Any]) -> bool:
    return (
        str(instance.get("state", "")).lower() == "failed"
        and instance.get("reason") == "ProviderResourceLost"
    )


def run_kubectl(kubeconfig: str, args: list[str], *, check: bool = True) -> str:
    command = ["kubectl"]
    if kubeconfig.strip():
        command.extend(["--kubeconfig", kubeconfig.strip()])
    command.extend(args)
    result = subprocess.run(command, text=True, capture_output=True, check=False, timeout=120)
    if check and result.returncode != 0:
        detail = result.stderr.strip() or result.stdout.strip()
        fail(f"{' '.join(command)} failed: {detail}")
    return result.stdout


def get_instance(base: str, token: str, tenant_id: str, instance_id: str) -> dict[str, Any]:
    status, document = sandbox_gate.HTTPClient().request(
        "GET",
        sandbox_gate.gateway_url(base, f"/instances/{urllib.parse.quote(instance_id)}"),
        token,
        tenant_id,
    )
    if status != 200:
        fail(f"instance get must return 200, got {status}: {document}")
    return sandbox_gate.require_instance(document, "instance get")


def wait_provider_loss(
    base: str,
    token: str,
    tenant_id: str,
    instance_id: str,
    attempts: int,
    interval: float,
) -> dict[str, Any]:
    last: dict[str, Any] = {}
    for _ in range(attempts):
        last = get_instance(base, token, tenant_id, instance_id)
        if is_provider_lost(last):
            return last
        time.sleep(interval)
    fail(f"instance {instance_id} did not reach failed/ProviderResourceLost; last={last}")


def wait_no_resources(namespace: str, name: str, kubeconfig: str, attempts: int, interval: float) -> None:
    selector = f"ani.kubercloud.io/instance={name}"
    for _ in range(attempts):
        output = run_kubectl(
            kubeconfig,
            ["get", "deployments,pods,services", "-n", namespace, "-l", selector, "-o", "name"],
        )
        if not output.strip():
            return
        time.sleep(interval)
    fail(f"provider resources remain for {namespace}/{name}")


def run_live(
    gateway: str,
    token: str,
    tenant_id: str,
    name: str,
    image_ref: str,
    idempotency_key: str,
    kubeconfig: str,
    poll_attempts: int,
    poll_interval: float,
    stability_wait: float,
) -> dict[str, object]:
    if shutil.which("kubectl") is None:
        fail("kubectl is required for --live")
    namespace = sandbox_gate.tenant_namespace(tenant_id)
    client = sandbox_gate.HTTPClient()
    instance_id = ""
    cleaned: dict[str, Any] = {}
    try:
        status, document = client.request(
            "POST",
            sandbox_gate.gateway_url(gateway, "/instances"),
            token,
            tenant_id,
            {
                "idempotency_key": idempotency_key + "-create",
                "name": name,
                "kind": "sandbox",
                "image_ref": image_ref,
                "auto_start": True,
                "sandbox_config": {
                    "runtime_class": "sandbox-kata",
                    "network_egress_policy": "deny_all",
                },
            },
        )
        if status != 201:
            fail(f"sandbox create must return 201, got {status}: {document}")
        created = sandbox_gate.require_instance(document, "sandbox create")
        instance_id = sandbox_gate.instance_id(created, "sandbox create")
        running = sandbox_gate.wait_for_state(
            gateway,
            token,
            tenant_id,
            instance_id,
            {"running"},
            attempts=poll_attempts,
            interval=poll_interval,
        )
        sandbox_gate.observe_deployment(name, namespace, kubeconfig)

        run_kubectl(kubeconfig, ["delete", "deployment", name, "-n", namespace, "--wait=true"])
        lost = wait_provider_loss(
            gateway,
            token,
            tenant_id,
            instance_id,
            poll_attempts,
            poll_interval,
        )
        if stability_wait > 0:
            time.sleep(stability_wait)
        stable = get_instance(gateway, token, tenant_id, instance_id)
        if not is_provider_lost(stable):
            fail(f"provider-loss state is not stable: {stable}")

        cleaned = sandbox_gate.lifecycle(gateway, token, tenant_id, instance_id, "delete", idempotency_key)
        if str(cleaned.get("state", "")).lower() != "deleted":
            fail(f"cleanup must return deleted instance, got {cleaned}")
        wait_no_resources(namespace, name, kubeconfig, poll_attempts, poll_interval)
        return {
            "status": "passed",
            "instance_id": instance_id,
            "kind": "sandbox",
            "state_before_provider_loss": running.get("state"),
            "state_after_provider_loss": lost.get("state"),
            "reason_after_provider_loss": lost.get("reason"),
            "state_after_repeat_reconcile": stable.get("state"),
            "reason_after_repeat_reconcile": stable.get("reason"),
            "state_after_cleanup": cleaned.get("state"),
            "provider_resources_after_cleanup": 0,
            "write_path": "Core /api/v1/instances",
        }
    finally:
        if instance_id and str(cleaned.get("state", "")).lower() != "deleted":
            try:
                sandbox_gate.lifecycle(gateway, token, tenant_id, instance_id, "delete", idempotency_key + "-fallback")
            except BaseException:
                pass
        if instance_id:
            run_kubectl(kubeconfig, ["delete", "deployment", name, "-n", namespace, "--ignore-not-found=true"], check=False)


def write_evidence(path: Path, evidence: dict[str, object]) -> None:
    document = {
        "id": GATE_ID,
        "profile": PROFILE,
        "generated_at": dt.datetime.now(dt.timezone.utc).isoformat().replace("+00:00", "Z"),
        **evidence,
    }
    path.parent.mkdir(parents=True, exist_ok=True)
    path.write_text(json.dumps(document, ensure_ascii=False, indent=2, sort_keys=True) + "\n", encoding="utf-8")


def main() -> int:
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("--gate", default=str(DEFAULT_GATE))
    parser.add_argument("--live", action="store_true")
    parser.add_argument("--gateway-url", default=os.getenv("ANI_GATEWAY_URL", ""))
    parser.add_argument("--ani-bearer-token", default=os.getenv("ANI_BEARER_TOKEN", ""))
    parser.add_argument("--tenant-id", default=os.getenv("ANI_LIVE_TENANT_ID", "11111111-1111-1111-1111-111111111111"))
    parser.add_argument("--name", default="ani-sandbox-provider-loss-live")
    parser.add_argument("--image-ref", default="")
    parser.add_argument("--idempotency-key", default="instance-reconcile-provider-loss-live")
    parser.add_argument("--kubeconfig", default=os.getenv("KUBECONFIG", ""))
    parser.add_argument("--poll-attempts", type=int, default=30)
    parser.add_argument("--poll-interval-seconds", type=float, default=5.0)
    parser.add_argument("--stability-wait-seconds", type=float, default=35.0)
    parser.add_argument("--evidence-output")
    args = parser.parse_args()

    validate_contract(load_gate(Path(args.gate)))
    if not args.live:
        print(f"{PROFILE} contract valid; use --live to validate provider-loss reconciliation")
        return 0
    for label, value in {
        "gateway-url": args.gateway_url,
        "ani-bearer-token": args.ani_bearer_token,
        "tenant-id": args.tenant_id,
        "image-ref": args.image_ref,
        "evidence-output": args.evidence_output,
    }.items():
        if not value or not str(value).strip():
            fail(f"--{label} is required for --live")
    evidence = run_live(
        args.gateway_url,
        args.ani_bearer_token,
        args.tenant_id,
        args.name,
        args.image_ref,
        args.idempotency_key,
        args.kubeconfig,
        args.poll_attempts,
        args.poll_interval_seconds,
        args.stability_wait_seconds,
    )
    write_evidence(Path(args.evidence_output), evidence)
    print(f"{PROFILE} live checks valid; evidence written to {args.evidence_output}")
    return 0


if __name__ == "__main__":
    raise SystemExit(main())
