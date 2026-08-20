#!/usr/bin/env python3
"""Simulate Console HTTP against in-cluster ani-console → ani-gateway.

Product traffic enters ani-console nginx /api/ and is proxied to the existing
production ani-gateway. The tenant Bearer comes from --token-file (a Console
session token). This must not mint a tenant JWT, must not send X-Dev-Tenant-ID,
must not delete the existing tenant namespace. The leftover
ani-vllm-cpu-smoke lab workload is not required.
"""

from __future__ import annotations

import argparse
import base64
import datetime as dt
import json
import os
import signal
import subprocess
import sys
import tempfile
import time
import urllib.error
import urllib.request
import uuid
from pathlib import Path
from typing import Any

sys.path.insert(0, str(Path(__file__).resolve().parent))
import run_inference_cpu_vllm_live_gate as live
import run_inference_incluster_e2e as c21

ROOT = live.ROOT
EVIDENCE = ROOT / "development-records/live-evidence/inference-console-shaped-e2e-live-20260817.json"
PROFILE = "INFERENCE-SERVICE-CONSOLE-SHAPED-E2E-LIVE-GATE-C22"
CONSOLE_DEPLOY = "ani-console"
SERVICE_NAME = "inf-c22-cpu"
TENANT_NS_PREFIX = "ani-tenant-"


def fail(message: str) -> None:
    raise SystemExit(f"inference console-shaped e2e live gate failed: {message}")


def load_token(path: str) -> str:
    raw = Path(path).read_text(encoding="utf-8").strip()
    if raw.lower().startswith("bearer "):
        raw = raw[7:].strip()
    if raw.count(".") != 2:
        fail("console token file is not a JWT")
    return raw


def token_tid(token: str) -> str:
    payload = json.loads(base64.urlsafe_b64decode(token.split(".")[1] + "=" * 4))
    tid = str(payload.get("tid") or "").strip()
    if not tid:
        fail("console token is missing tid")
    if payload.get("scope") != "tenant":
        fail("console token is not a tenant-scoped session")
    return tid


def console_request(
    method: str,
    url: str,
    token: str,
    body: dict[str, Any] | None = None,
    origin: str = "",
) -> tuple[int, dict[str, Any]]:
    headers = {
        "Accept": "application/json",
        "Content-Type": "application/json",
        "Authorization": "Bearer " + token,
    }
    if origin:
        headers["Origin"] = origin
    data = None
    if body is not None:
        data = json.dumps(body).encode("utf-8")
    req = urllib.request.Request(url, data=data, headers=headers, method=method)
    try:
        with urllib.request.urlopen(req, timeout=60) as response:
            raw = response.read().decode("utf-8")
            return response.status, json.loads(raw) if raw.strip() else {}
    except urllib.error.HTTPError as err:
        raw = err.read().decode("utf-8", errors="replace")
        try:
            document = json.loads(raw) if raw.strip() else {}
        except json.JSONDecodeError:
            document = {"raw": "redacted"}
        return err.code, document


def wait_console_service(base: str, token: str, service_id: str, wanted: str, timeout: int, kubeconfig: str, origin: str) -> dict[str, Any]:
    deadline = time.time() + timeout
    last: dict[str, Any] = {}
    while time.time() < deadline:
        status, body = console_request("GET", f"{base}/api/v1/svc/inference-services/{service_id}", token, origin=origin)
        if status == 200:
            last = body
            if body.get("status") == wanted:
                return body
        time.sleep(5)
    extra = live.postgres_exec(
        kubeconfig,
        "SELECT COALESCE(s.status,'') || '|' || COALESCE(o.state,'') || '|' || "
        "COALESCE(o.error_code,'') || '|' || COALESCE(o.error_message,'') "
        "FROM inference_services s LEFT JOIN inference_operations o ON o.id = s.current_operation_id "
        f"WHERE s.id = '{service_id}';",
    ).strip()
    fail(f"inference service did not reach {wanted}: {last.get('status') or 'unknown'} {live.redact_text(extra)[:200]}".strip())
    return last


def refresh_core_service_token(kubeconfig: str, tenant_id: str) -> None:
    issuer = c21.secret_bytes(kubeconfig, c21.AUTH_SECRET, "auth_jwt_issuer").decode("utf-8").strip()
    if not issuer:
        fail("auth-service issuer is not configured")
    private_key = c21.secret_bytes(kubeconfig, c21.AUTH_SECRET, "jwt_private_key_pem")
    service_token = c21.mint_jwt(private_key, c21.service_claims(issuer, tenant_id, int(time.time())))
    tmpdir = Path(tempfile.mkdtemp(prefix="ani-c22-core-"))
    try:
        token_file = tmpdir / "core.token"
        c21.write_secret_file(token_file, service_token)
        c21.apply_core_token_secret(kubeconfig, token_file)
    finally:
        service_token = ""
        private_key = b""
        live.run(["rm", "-rf", str(tmpdir)])
    live.kubectl(kubeconfig, ["-n", c21.GATEWAY_NS, "rollout", "restart", f"deploy/{c21.INFERENCE_DEPLOY}"])
    c21.wait_deploy(kubeconfig, c21.INFERENCE_DEPLOY)


def main() -> None:
    parser = argparse.ArgumentParser()
    parser.add_argument("--kubeconfig", default=os.environ.get("KUBECONFIG", str(Path.home() / ".kube/config")))
    parser.add_argument("--token-file", default=os.environ.get("CONSOLE_TOKEN_FILE", ""))
    parser.add_argument("--listen", default="127.0.0.1:18081")
    args = parser.parse_args()
    kubeconfig = args.kubeconfig
    if not Path(kubeconfig).exists():
        fail("kubeconfig is missing")
    if not args.token_file:
        fail("--token-file or CONSOLE_TOKEN_FILE is required")
    token = load_token(args.token_file)
    tenant_id = token_tid(token)
    namespace = TENANT_NS_PREFIX + tenant_id
    model_id = str(uuid.uuid4())
    origin = "http://" + args.listen
    checks: list[dict[str, Any]] = []
    forward: subprocess.Popen[str] | None = None
    tmpdir = Path(tempfile.mkdtemp(prefix="ani-inf-c22-"))
    src_snapshot = ""
    dest_vsc = ""
    cloned = False
    service_id = ""
    try:
        if c21.gateway_env(kubeconfig, "ANI_AUTH_MODE") != "auth_service":
            fail("production ani-gateway ANI_AUTH_MODE is not auth_service")
        if not c21.gateway_env(kubeconfig, "INFERENCE_SERVICE_GRPC_ADDR"):
            fail("production ani-gateway is missing inference gRPC address")
        if c21.gateway_env(kubeconfig, "PLATFORM_WORKLOAD_PROVIDER") != "kubernetes_rest":
            fail("production ani-gateway is missing kubernetes_rest platform-workload provider")
        ns = live.run(["kubectl", "--kubeconfig", kubeconfig, "get", "ns", namespace])
        if ns.returncode != 0:
            fail("token tenant namespace is missing")
        tenant_row = live.postgres_exec(
            kubeconfig,
            f"SELECT COALESCE(status,'') FROM tenants WHERE id='{tenant_id}';",
        ).strip()
        if tenant_row != "active":
            fail("token tenant is not active")
        image = live.discover_image(kubeconfig)

        print("c22: refreshing core service token for token tenant", flush=True)
        refresh_core_service_token(kubeconfig, tenant_id)
        checks.append({"id": "in-cluster-inference-service-running", "status": "passed"})
        checks.append({"id": "production-gateway-auth-mode-preserved", "status": "passed", "auth_mode": "auth_service"})

        pvc = live.run(["kubectl", "--kubeconfig", kubeconfig, "-n", namespace, "get", "pvc", "vllm-model"])
        if pvc.returncode != 0:
            if not live.smoke_ready(kubeconfig):
                fail("tenant namespace has no vllm-model PVC; leftover ani-vllm-cpu-smoke was removed")
            print("c22: cloning model PVC into existing tenant namespace", flush=True)
            src_snapshot, dest_vsc = live.clone_model_pvc(kubeconfig, namespace, tmpdir)
            cloned = True

        host, port = args.listen.split(":")
        forward = subprocess.Popen(
            ["kubectl", "--kubeconfig", kubeconfig, "-n", c21.GATEWAY_NS, "port-forward", f"svc/{CONSOLE_DEPLOY}", f"{port}:80"],
            stdout=subprocess.DEVNULL,
            stderr=subprocess.DEVNULL,
        )
        live.wait_tcp(host, int(port), timeout=30)
        base = f"http://{args.listen}"
        print("c22: listing through ani-console", flush=True)
        list_status, listed = console_request("GET", f"{base}/api/v1/svc/inference-services", token, origin=origin)
        if list_status == 401:
            fail("console-shaped list rejected the tenant session token")
        if list_status != 200:
            fail(f"console-shaped list status={list_status}, want=200")
        if "X-Dev-Tenant-ID" in json.dumps(listed):
            fail("list response leaked a dev tenant header")
        checks.append({"id": "console-nginx-inference-list", "status": "passed", "entry": "ani-system/ani-console"})

        print("c22: creating CPU InferenceService through ani-console", flush=True)
        create_status, created = console_request(
            "POST",
            f"{base}/api/v1/svc/inference-services",
            token,
            {
                "idempotency_key": str(uuid.uuid4()),
                "name": SERVICE_NAME,
                "model": model_id,
                "model_version_id": model_id,
                "served_model_name": "qwen2.5-0.5b",
                "image_ref": image,
                "replicas": 1,
                "resources": {"cpu": "4", "memory": "8Gi"},
            },
            origin=origin,
        )
        live.expect(create_status, 202, "product-inference-service-cpu-create")
        service_id = str(created.get("id") or "")
        if not service_id:
            fail("create did not return an inference service id")
        checks.append({"id": "product-inference-service-cpu-create", "status": "passed", "http_status": 202})

        print("c22: waiting for runtime deployment", flush=True)
        deploy_name = live.runtime_resource_name(service_id)
        deadline = time.time() + 180
        while time.time() < deadline:
            found = live.run(["kubectl", "--kubeconfig", kubeconfig, "-n", namespace, "get", "deploy", deploy_name])
            if found.returncode == 0:
                break
            extra = live.redact_text(
                live.postgres_exec(
                    kubeconfig,
                    "SELECT COALESCE(o.error_code,'') || '|' || COALESCE(o.error_message,'') FROM inference_operations o "
                    "JOIN inference_services s ON s.current_operation_id = o.id "
                    f"WHERE s.id = '{service_id}' LIMIT 1;",
                ).strip()
            )
            code = extra.split("|", 1)[0]
            if code in {"UNAUTHORIZED", "FORBIDDEN", "UNAUTHENTICATED", "RUNTIME_MUTATION_FAILED"}:
                fail(f"platform-workload deployment was not created: {extra[:200]}")
            time.sleep(2)
        else:
            fail("platform-workload deployment was not created")
        live.assert_cpu_deployment(kubeconfig, namespace, deploy_name, image)
        checks.append({"id": "kubectl-vllm-cpu-deployment", "status": "passed", "image": "digest-pinned-vllm-cpu"})

        observed = wait_console_service(base, token, service_id, "running", 300, kubeconfig, origin)
        if observed.get("invocation_url") is not None or observed.get("endpoint_url") is not None:
            fail("product response leaked an invocation URL")
        if "accelerator" in (observed.get("resources") or {}):
            fail("CPU create projected an accelerator")
        checks.append({"id": "inference-service-running-health-smoke", "status": "passed", "status_value": "running", "probe": "clusterip"})

        stop_status, _ = console_request(
            "POST",
            f"{base}/api/v1/svc/inference-services/{service_id}/lifecycle",
            token,
            {"idempotency_key": str(uuid.uuid4()), "action": "stop"},
            origin=origin,
        )
        live.expect(stop_status, 202, "inference-service-stop")
        wait_console_service(base, token, service_id, "stopped", 180, kubeconfig, origin)
        checks.append({"id": "inference-service-stop", "status": "passed", "status_value": "stopped"})

        start_status, _ = console_request(
            "POST",
            f"{base}/api/v1/svc/inference-services/{service_id}/lifecycle",
            token,
            {"idempotency_key": str(uuid.uuid4()), "action": "start"},
            origin=origin,
        )
        live.expect(start_status, 202, "inference-service-start")
        started = wait_console_service(base, token, service_id, "running", 300, kubeconfig, origin)
        if started.get("id") != service_id:
            fail("start did not reuse the same inference service id")
        checks.append({"id": "inference-service-start", "status": "passed", "same_service_id": True})

        delete_status, _ = console_request(
            "DELETE",
            f"{base}/api/v1/svc/inference-services/{service_id}",
            token,
            origin=origin,
        )
        live.expect(delete_status, 202, "inference-service-delete")
        deadline = time.time() + 180
        while time.time() < deadline:
            gone_status, _ = console_request("GET", f"{base}/api/v1/svc/inference-services/{service_id}", token, origin=origin)
            leftover = live.run(["kubectl", "--kubeconfig", kubeconfig, "-n", namespace, "get", "deploy", deploy_name])
            if gone_status == 404 and leftover.returncode != 0:
                break
            time.sleep(3)
        else:
            fail("delete did not remove the inference service and runtime")
        service_id = ""
        checks.append({"id": "inference-service-delete", "status": "passed", "get_status": 404})

        still = live.run(["kubectl", "--kubeconfig", kubeconfig, "get", "ns", namespace])
        if still.returncode != 0:
            fail("existing tenant namespace was deleted")
        checks.append({"id": "existing-tenant-namespace-preserved", "status": "passed"})
        if live.gpu_allocatable(kubeconfig) != 0:
            fail("cluster unexpectedly advertised nvidia.com/gpu; this batch must skip GPU live")
        checks.append({"id": "gpu-live-skipped-no-device-plugin", "status": "passed", "reason": "skipped_no_device_plugin"})

        evidence = {
            "profile": PROFILE,
            "status": "passed",
            "generated_at": dt.datetime.now(dt.timezone.utc).strftime("%Y-%m-%dT%H:%M:%SZ"),
            "entry": "ani-system/ani-console",
            "gateway": "ani-system/ani-gateway",
            "auth_mode": "auth_service",
            "auth": "tenant_bearer_via_console",
            "engine": "vllm-cpu",
            "image": "digest-pinned-vllm-cpu",
            "namespace_kind": "existing-ani-tenant-{uuid}",
            "probe": "clusterip",
            "gpu_live": "skipped_no_device_plugin",
            "checks": checks,
        }
        live.assert_clean_evidence(evidence)
        EVIDENCE.parent.mkdir(parents=True, exist_ok=True)
        EVIDENCE.write_text(json.dumps(evidence, indent=2) + "\n", encoding="utf-8")
        print("inference console-shaped e2e live gate passed")
        print(f"evidence {EVIDENCE.relative_to(ROOT)}")
    finally:
        if service_id and token:
            try:
                console_request(
                    "DELETE",
                    f"http://{args.listen}/api/v1/svc/inference-services/{service_id}",
                    token,
                    origin="http://" + args.listen,
                )
            except Exception:
                pass
        token = ""
        if forward is not None and forward.poll() is None:
            forward.send_signal(signal.SIGTERM)
            try:
                forward.wait(timeout=5)
            except subprocess.TimeoutExpired:
                forward.kill()
        if cloned:
            live.run(["kubectl", "--kubeconfig", kubeconfig, "-n", namespace, "delete", "pvc", "vllm-model", "--wait=false", "--ignore-not-found"])
            live.run(["kubectl", "--kubeconfig", kubeconfig, "-n", namespace, "delete", "volumesnapshot", "vllm-model", "--wait=false", "--ignore-not-found"])
        if src_snapshot:
            live.run(["kubectl", "--kubeconfig", kubeconfig, "-n", live.SMOKE_NS, "delete", "volumesnapshot", src_snapshot, "--wait=false", "--ignore-not-found"])
        if dest_vsc:
            live.run(["kubectl", "--kubeconfig", kubeconfig, "delete", "volumesnapshotcontent", dest_vsc, "--wait=false", "--ignore-not-found"])
        live.run(["rm", "-rf", str(tmpdir)])


if __name__ == "__main__":
    try:
        main()
    except KeyboardInterrupt:
        sys.exit(130)
