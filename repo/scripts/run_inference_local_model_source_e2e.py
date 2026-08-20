#!/usr/bin/env python3
"""Console-shaped live for tenant-local PVC model source.

Creates a Model + pvc:// version through ani-console nginx, then deploys an
InferenceService with that real model_version_id. Does not use lab catalog,
does not mint X-Dev-Tenant-ID, and does not delete the existing tenant
namespace or the tenant-local vllm-model PVC.
"""

from __future__ import annotations

import argparse
import datetime as dt
import json
import os
import signal
import subprocess
import sys
import tempfile
import time
import uuid
from pathlib import Path
from typing import Any

sys.path.insert(0, str(Path(__file__).resolve().parent))
import run_inference_console_shaped_e2e as c22
import run_inference_cpu_vllm_live_gate as live
import run_inference_incluster_e2e as c21

ROOT = live.ROOT
EVIDENCE = ROOT / "development-records/live-evidence/inference-local-model-source-live-20260817.json"
PROFILE = "INFERENCE-SERVICE-LOCAL-MODEL-SOURCE-LIVE-GATE-C23"
CONSOLE_DEPLOY = "ani-console"
SERVICE_NAME = "inf-c23-cpu-" + uuid.uuid4().hex[:8]
TENANT_ID = "11111111-1111-1111-1111-111111111111"
TENANT_NS = "ani-tenant-" + TENANT_ID
STORAGE_PATH = "pvc://vllm-model#/models/qwen"


def fail(message: str) -> None:
    raise SystemExit(f"inference local model source live gate failed: {message}")


def expect_http(status: int, wanted: int, label: str, body: dict[str, Any] | None = None) -> None:
    if status == wanted:
        return
    extra = ""
    if isinstance(body, dict):
        extra = live.redact_text(str(body.get("error_code") or body.get("code") or body.get("message") or ""))[:160]
    fail(f"{label}: status={status}, want={wanted} {extra}".strip())


def wait_seed(kubeconfig: str, timeout: int = 1200) -> None:
    pvc = live.run(["kubectl", "--kubeconfig", kubeconfig, "-n", TENANT_NS, "get", "pvc", "vllm-model"])
    if pvc.returncode != 0:
        fail("tenant namespace is missing vllm-model PVC")
    present = live.run(["kubectl", "--kubeconfig", kubeconfig, "-n", TENANT_NS, "get", "job", "seed-vllm-model"])
    if present.returncode != 0:
        return
    deadline = time.time() + timeout
    while time.time() < deadline:
        job = live.run(["kubectl", "--kubeconfig", kubeconfig, "-n", TENANT_NS, "get", "job", "seed-vllm-model", "-o", "jsonpath={.status.succeeded}"])
        if (job.stdout or "").strip() == "1":
            live.run(["kubectl", "--kubeconfig", kubeconfig, "-n", TENANT_NS, "delete", "job", "seed-vllm-model", "--wait=true", "--ignore-not-found"])
            return
        failed = live.run(["kubectl", "--kubeconfig", kubeconfig, "-n", TENANT_NS, "get", "job", "seed-vllm-model", "-o", "jsonpath={.status.failed}"])
        if (failed.stdout or "").strip() not in {"", "0"}:
            logs = live.run(["kubectl", "--kubeconfig", kubeconfig, "-n", TENANT_NS, "logs", "job/seed-vllm-model", "--tail=40"])
            fail("tenant model seed job failed: " + live.redact_text((logs.stdout or logs.stderr or "")[-400:]))
        time.sleep(5)
    fail("tenant model seed job did not finish")


def ensure_core_service_token(kubeconfig: str, tenant_id: str) -> None:
    issuer = c21.secret_bytes(kubeconfig, c21.AUTH_SECRET, "auth_jwt_issuer").decode("utf-8").strip()
    if not issuer:
        fail("auth-service issuer is not configured")
    private_key = c21.secret_bytes(kubeconfig, c21.AUTH_SECRET, "jwt_private_key_pem")
    service_token = c21.mint_jwt(private_key, c21.service_claims(issuer, tenant_id, int(time.time())))
    tmpdir = Path(tempfile.mkdtemp(prefix="ani-c23-core-"))
    try:
        token_file = tmpdir / "core.token"
        c21.write_secret_file(token_file, service_token)
        c21.apply_core_token_secret(kubeconfig, token_file)
    finally:
        service_token = ""
        private_key = b""
        live.run(["rm", "-rf", str(tmpdir)])
    ready = live.run(
        [
            "kubectl",
            "--kubeconfig",
            kubeconfig,
            "-n",
            c21.GATEWAY_NS,
            "get",
            "deploy",
            c21.INFERENCE_DEPLOY,
            "-o",
            "jsonpath={.status.readyReplicas}",
        ]
    )
    if (ready.stdout or "").strip() == "1":
        return
    live.kubectl(kubeconfig, ["-n", c21.GATEWAY_NS, "rollout", "restart", f"deploy/{c21.INFERENCE_DEPLOY}"])
    c21.wait_deploy(kubeconfig, c21.INFERENCE_DEPLOY)


def main() -> None:
    parser = argparse.ArgumentParser()
    parser.add_argument("--kubeconfig", default=os.environ.get("KUBECONFIG", str(Path.home() / ".kube/config")))
    parser.add_argument("--listen", default="127.0.0.1:18083")
    args = parser.parse_args()
    kubeconfig = args.kubeconfig
    if not Path(kubeconfig).exists():
        fail("kubeconfig is missing")

    checks: list[dict[str, Any]] = []
    forward: subprocess.Popen[str] | None = None
    tmpdir = Path(tempfile.mkdtemp(prefix="ani-inf-c23-"))
    service_id = ""
    token = ""
    origin = "http://" + args.listen
    try:
        if c21.gateway_env(kubeconfig, "ANI_AUTH_MODE") != "auth_service":
            fail("production ani-gateway ANI_AUTH_MODE is not auth_service")
        if c21.gateway_env(kubeconfig, "MODEL_SERVICE_GRPC_ADDR") != "model-service.ani-system.svc.cluster.local:9103":
            fail("production ani-gateway is missing model-service gRPC address")
        if c21.deploy_env(kubeconfig, "inference-service", "INFERENCE_LAB_CATALOG") == "1":
            fail("inference-service still has lab catalog enabled")
        if c21.deploy_env(kubeconfig, "inference-service", "MODEL_SERVICE_GRPC_ADDR") == "":
            fail("inference-service is missing model-service gRPC address")
        ns = live.run(["kubectl", "--kubeconfig", kubeconfig, "get", "ns", TENANT_NS])
        if ns.returncode != 0:
            fail("existing console tenant namespace is missing")

        print("c23: waiting for tenant-local model PVC seed", flush=True)
        wait_seed(kubeconfig)
        checks.append({"id": "tenant-local-pvc-ready", "status": "passed", "storage_path": STORAGE_PATH})

        print("c23: ensuring core service token", flush=True)
        ensure_core_service_token(kubeconfig, TENANT_ID)
        checks.append({"id": "in-cluster-inference-service-running", "status": "passed"})
        checks.append({"id": "production-gateway-auth-mode-preserved", "status": "passed", "auth_mode": "auth_service"})

        issuer = c21.secret_bytes(kubeconfig, c21.AUTH_SECRET, "auth_jwt_issuer").decode("utf-8").strip()
        private_key = c21.secret_bytes(kubeconfig, c21.AUTH_SECRET, "jwt_private_key_pem")
        token = c21.mint_jwt(private_key, c21.tenant_claims(issuer, TENANT_ID, str(uuid.uuid4()), int(time.time())))
        private_key = b""

        host, port = args.listen.split(":")
        forward = subprocess.Popen(
            ["kubectl", "--kubeconfig", kubeconfig, "-n", c21.GATEWAY_NS, "port-forward", f"svc/{CONSOLE_DEPLOY}", f"{port}:80"],
            stdout=subprocess.DEVNULL,
            stderr=subprocess.DEVNULL,
        )
        live.wait_tcp(host, int(port), timeout=30)
        base = f"http://{args.listen}"

        print("c23: listing through ani-console", flush=True)
        list_status, listed = c22.console_request("GET", f"{base}/api/v1/svc/inference-services", token, origin=origin)
        if list_status == 401:
            fail("console-shaped list rejected the tenant session token")
        if list_status != 200:
            fail(f"console-shaped list status={list_status}, want=200")
        if "X-Dev-Tenant-ID" in json.dumps(listed):
            fail("list response leaked a dev tenant header")
        checks.append({"id": "console-nginx-inference-list", "status": "passed", "entry": "ani-system/ani-console"})

        leftovers = [
            str(item.get("id") or "")
            for item in (listed.get("items") or [])
            if str(item.get("name") or "").startswith("inf-c23-cpu")
        ]
        for leftover_id in leftovers:
            print("c23: deleting leftover test InferenceService", flush=True)
            c22.console_request(
                "DELETE",
                f"{base}/api/v1/svc/inference-services/{leftover_id}",
                token,
                origin=origin,
            )
            deadline = time.time() + 180
            while time.time() < deadline:
                gone_status, _ = c22.console_request(
                    "GET",
                    f"{base}/api/v1/svc/inference-services/{leftover_id}",
                    token,
                    origin=origin,
                )
                leftover_deploy = live.run(
                    ["kubectl", "--kubeconfig", kubeconfig, "-n", TENANT_NS, "get", "deploy", live.runtime_resource_name(leftover_id)]
                )
                if gone_status == 404 and leftover_deploy.returncode != 0:
                    break
                time.sleep(3)
            else:
                fail("leftover test InferenceService was not removed")

        print("c23: creating model metadata", flush=True)
        model_status, model = c22.console_request(
            "POST",
            f"{base}/api/v1/svc/models",
            token,
            {
                "idempotency_key": str(uuid.uuid4()),
                "name": "qwen-local-" + uuid.uuid4().hex[:8],
                "display_name": "Qwen local PVC",
                "capabilities": ["text-generation"],
            },
            origin=origin,
        )
        expect_http(model_status, 201, "create-model", model)
        model_id = str(model.get("id") or "")
        if not model_id:
            fail("create model did not return an id")
        checks.append({"id": "create-model", "status": "passed", "http_status": 201})

        print("c23: registering tenant-local PVC version", flush=True)
        version_status, version = c22.console_request(
            "POST",
            f"{base}/api/v1/svc/models/{model_id}/versions",
            token,
            {
                "idempotency_key": str(uuid.uuid4()),
                "version": "v1",
                "format": "safetensors",
                "storage_path": STORAGE_PATH,
                "checksum_sha256": "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa",
                "size_bytes": 1,
            },
            origin=origin,
        )
        expect_http(version_status, 201, "create-model-version-pvc", version)
        version_id = str(version.get("id") or "")
        if not version_id or version.get("storage_path") != STORAGE_PATH:
            fail("create version did not freeze the tenant PVC path")
        checks.append({"id": "create-model-version-pvc", "status": "passed", "http_status": 201})

        print("c23: creating InferenceService with real model_version_id", flush=True)
        image = live.discover_image(kubeconfig)
        create_status, created = c22.console_request(
            "POST",
            f"{base}/api/v1/svc/inference-services",
            token,
            {
                "idempotency_key": str(uuid.uuid4()),
                "name": SERVICE_NAME,
                "model": version_id,
                "model_version_id": version_id,
                "served_model_name": "qwen2.5-0.5b",
                "image_ref": image,
                "replicas": 1,
                "resources": {"cpu": "4", "memory": "8Gi"},
            },
            origin=origin,
        )
        expect_http(create_status, 202, "product-inference-service-cpu-create", created)
        service_id = str(created.get("id") or "")
        if not service_id:
            fail("create did not return an inference service id")
        if created.get("image_ref") != image:
            fail("create did not freeze the request image_ref digest")
        checks.append({"id": "product-inference-service-cpu-create", "status": "passed", "http_status": 202})
        deploy_name = live.runtime_resource_name(service_id)
        deadline = time.time() + 180
        while time.time() < deadline:
            found = live.run(["kubectl", "--kubeconfig", kubeconfig, "-n", TENANT_NS, "get", "deploy", deploy_name])
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
            if code in {"UNAUTHORIZED", "FORBIDDEN", "UNAUTHENTICATED", "RUNTIME_MUTATION_FAILED", "MODEL_NOT_FOUND", "MODEL_INCOMPATIBLE"}:
                fail(f"platform-workload deployment was not created: {extra[:200]}")
            time.sleep(2)
        else:
            fail("platform-workload deployment was not created")
        live.assert_cpu_deployment(kubeconfig, TENANT_NS, deploy_name, image)
        checks.append({"id": "kubectl-vllm-cpu-deployment", "status": "passed", "image": "digest-pinned-vllm-cpu"})

        observed = c22.wait_console_service(base, token, service_id, "running", 900, kubeconfig, origin)
        if observed.get("invocation_url") is not None or observed.get("endpoint_url") is not None:
            fail("product response leaked an invocation URL")
        checks.append({"id": "inference-service-running-health-smoke", "status": "passed", "status_value": "running"})

        stop_status, _ = c22.console_request(
            "POST",
            f"{base}/api/v1/svc/inference-services/{service_id}/lifecycle",
            token,
            {"idempotency_key": str(uuid.uuid4()), "action": "stop"},
            origin=origin,
        )
        expect_http(stop_status, 202, "inference-service-stop")
        c22.wait_console_service(base, token, service_id, "stopped", 180, kubeconfig, origin)
        checks.append({"id": "inference-service-stop", "status": "passed", "status_value": "stopped"})

        start_status, _ = c22.console_request(
            "POST",
            f"{base}/api/v1/svc/inference-services/{service_id}/lifecycle",
            token,
            {"idempotency_key": str(uuid.uuid4()), "action": "start"},
            origin=origin,
        )
        expect_http(start_status, 202, "inference-service-start")
        started = c22.wait_console_service(base, token, service_id, "running", 900, kubeconfig, origin)
        if started.get("id") != service_id:
            fail("start did not reuse the same inference service id")
        checks.append({"id": "inference-service-start", "status": "passed", "same_service_id": True})

        delete_status, _ = c22.console_request(
            "DELETE",
            f"{base}/api/v1/svc/inference-services/{service_id}",
            token,
            origin=origin,
        )
        expect_http(delete_status, 202, "inference-service-delete")
        deadline = time.time() + 180
        while time.time() < deadline:
            gone_status, _ = c22.console_request("GET", f"{base}/api/v1/svc/inference-services/{service_id}", token, origin=origin)
            leftover = live.run(["kubectl", "--kubeconfig", kubeconfig, "-n", TENANT_NS, "get", "deploy", deploy_name])
            if gone_status == 404 and leftover.returncode != 0:
                break
            time.sleep(3)
        else:
            fail("delete did not remove the inference service and runtime")
        service_id = ""
        checks.append({"id": "inference-service-delete", "status": "passed", "get_status": 404})

        still = live.run(["kubectl", "--kubeconfig", kubeconfig, "get", "ns", TENANT_NS])
        if still.returncode != 0:
            fail("existing tenant namespace was deleted")
        pvc = live.run(["kubectl", "--kubeconfig", kubeconfig, "-n", TENANT_NS, "get", "pvc", "vllm-model"])
        if pvc.returncode != 0:
            fail("tenant-local model PVC was deleted")
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
            "model_source": "tenant_local_pvc",
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
        print("inference local model source live gate passed")
        print(f"evidence {EVIDENCE.relative_to(ROOT)}")
    finally:
        if service_id and token:
            try:
                c22.console_request(
                    "DELETE",
                    f"http://{args.listen}/api/v1/svc/inference-services/{service_id}",
                    token,
                    origin=origin,
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
        live.run(["rm", "-rf", str(tmpdir)])


if __name__ == "__main__":
    main()
