#!/usr/bin/env python3
"""Live-gate create InferenceService image_id/image_ref on production ani-gateway.

Rolls in-cluster ani-gateway and inference-service, strips leftover engine image
env, then proves missing image is 400, unpinned image is 422, and a digest-pinned
image_ref creates a CPU vLLM runtime. Product HTTP stays on ani-console nginx
/api/. ANI_AUTH_MODE stays auth_service. GPU/LWS remain skipped.
"""

from __future__ import annotations

import argparse
import datetime as dt
import json
import os
import shutil
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
import run_inference_local_model_source_e2e as c23

ROOT = live.ROOT
EVIDENCE = ROOT / "development-records/live-evidence/inference-create-image-live-20260818.json"
PROFILE = "INFERENCE-SERVICE-CREATE-IMAGE-LIVE-GATE-C30"
IMAGE_TAG = "create-image-c30-20260818"
STABLE_GATEWAY_IMAGE = f"{c21.REGISTRY}/ani-gateway:local-model-pvc-20260817"
STABLE_INFERENCE_IMAGE = f"{c21.REGISTRY}/inference-service:local-model-pvc-20260817"
CONSOLE_DEPLOY = "ani-console"
SERVICE_NAME = "inf-c30-cpu-" + uuid.uuid4().hex[:8]
TENANT_ID = "11111111-1111-1111-1111-111111111111"
TENANT_NS = "ani-tenant-" + TENANT_ID
STORAGE_PATH = "pvc://vllm-model#/models/qwen"
UNPINNED_IMAGE = "example.invalid/ani/not-a-runtime-image:latest"


def fail(message: str) -> None:
    raise SystemExit(f"inference create image live gate failed: {message}")


def expect_http(status: int, wanted: int, label: str, body: dict[str, Any] | None = None) -> None:
    if status == wanted:
        return
    extra = ""
    if isinstance(body, dict):
        extra = live.redact_text(str(body.get("error_code") or body.get("code") or body.get("message") or ""))[:160]
    fail(f"{label}: status={status}, want={wanted} {extra}".strip())


def expect_error(status: int, body: dict[str, Any], wanted_status: int, wanted_code: str, label: str) -> None:
    code = str(body.get("code") or "")
    if status == wanted_status and code == wanted_code:
        return
    extra = live.redact_text(str(body.get("message") or ""))[:160]
    fail(f"{label}: status={status} code={code}, want={wanted_status} {wanted_code} {extra}".strip())


def create_body(name: str, model: str, image_ref: str = "", image_id: str = "") -> dict[str, Any]:
    body: dict[str, Any] = {
        "idempotency_key": str(uuid.uuid4()),
        "name": name,
        "model": model,
        "model_version_id": model,
        "served_model_name": "qwen2.5-0.5b",
        "replicas": 1,
        "resources": {"cpu": "4", "memory": "8Gi"},
    }
    if image_ref:
        body["image_ref"] = image_ref
    if image_id:
        body["image_id"] = image_id
    return body


def restore_images(kubeconfig: str, gateway_image: str, inference_image: str) -> None:
    c21.patch_gateway(kubeconfig, image=gateway_image)
    c21.patch_deploy(kubeconfig, c21.INFERENCE_DEPLOY, image=inference_image)
    live.run(
        [
            "kubectl",
            "--kubeconfig",
            kubeconfig,
            "-n",
            c21.GATEWAY_NS,
            "rollout",
            "status",
            f"deploy/{c21.PRODUCTION_GATEWAY}",
            "--timeout=180s",
        ],
        timeout=210,
    )
    live.run(
        [
            "kubectl",
            "--kubeconfig",
            kubeconfig,
            "-n",
            c21.GATEWAY_NS,
            "rollout",
            "status",
            f"deploy/{c21.INFERENCE_DEPLOY}",
            "--timeout=180s",
        ],
        timeout=210,
    )


def main() -> None:
    parser = argparse.ArgumentParser()
    parser.add_argument("--kubeconfig", default=os.environ.get("KUBECONFIG", str(Path.home() / ".kube/config")))
    parser.add_argument("--listen", default="127.0.0.1:18084")
    parser.add_argument("--skip-build", action="store_true")
    args = parser.parse_args()
    kubeconfig = args.kubeconfig
    if not Path(kubeconfig).exists():
        fail("kubeconfig is missing")

    checks: list[dict[str, Any]] = []
    forward: subprocess.Popen[str] | None = None
    tmpdir = Path(tempfile.mkdtemp(prefix="ani-inf-c30-"))
    service_id = ""
    token = ""
    origin = "http://" + args.listen
    previous_gateway = c21.production_gateway_image(kubeconfig)
    previous_inference = c21.production_deploy_image(kubeconfig, c21.INFERENCE_DEPLOY)
    if IMAGE_TAG in previous_gateway:
        previous_gateway = STABLE_GATEWAY_IMAGE
    if IMAGE_TAG in previous_inference:
        previous_inference = STABLE_INFERENCE_IMAGE
    rolled = False
    try:
        if c21.gateway_env(kubeconfig, "ANI_AUTH_MODE") != "auth_service":
            fail("production ani-gateway ANI_AUTH_MODE is not auth_service")
        if c21.gateway_env(kubeconfig, "INFERENCE_SERVICE_GRPC_ADDR") != c21.INFERENCE_GRPC_ADDR:
            fail("production ani-gateway is missing inference gRPC address")
        if c21.gateway_env(kubeconfig, "PLATFORM_WORKLOAD_PROVIDER") != "kubernetes_rest":
            fail("production ani-gateway is missing kubernetes_rest platform-workload provider")
        if c21.gateway_env(kubeconfig, "MODEL_SERVICE_GRPC_ADDR") != "model-service.ani-system.svc.cluster.local:9103":
            fail("production ani-gateway is missing model-service gRPC address")
        ns = live.run(["kubectl", "--kubeconfig", kubeconfig, "get", "ns", TENANT_NS])
        if ns.returncode != 0:
            fail("existing console tenant namespace is missing")
        pvc = live.run(["kubectl", "--kubeconfig", kubeconfig, "-n", TENANT_NS, "get", "pvc", "vllm-model"])
        if pvc.returncode != 0:
            fail("tenant namespace is missing vllm-model PVC")
        c23.wait_seed(kubeconfig)
        checks.append({"id": "tenant-local-pvc-ready", "status": "passed", "storage_path": STORAGE_PATH})

        gateway_image = f"{c21.REGISTRY}/ani-gateway:{IMAGE_TAG}"
        inference_image = f"{c21.REGISTRY}/inference-service:{IMAGE_TAG}"
        if not args.skip_build:
            print("c30: building gateway and inference-service images", flush=True)
            c21.go_build(ROOT / "services/inference-service", tmpdir / "inference-service", ["."])
            c21.go_build(ROOT / "services/ani-gateway", tmpdir / "ani-gateway", ["-tags", "stdjson", "."])
            c21.build_inference_image(inference_image, tmpdir / "inference-service", tmpdir)
            c21.build_gateway_image(gateway_image, tmpdir / "ani-gateway", previous_gateway, tmpdir)
        elif not c21.docker_image_exists(gateway_image) or not c21.docker_image_exists(inference_image):
            fail("skip-build requires locally present C30 images")

        print("c30: minting core service token", flush=True)
        issuer = c21.secret_bytes(kubeconfig, c21.AUTH_SECRET, "auth_jwt_issuer").decode("utf-8").strip()
        if not issuer:
            fail("auth-service issuer is not configured")
        private_key = c21.secret_bytes(kubeconfig, c21.AUTH_SECRET, "jwt_private_key_pem")
        service_token = c21.mint_jwt(private_key, c21.service_claims(issuer, TENANT_ID, int(time.time())))
        token_file = tmpdir / "core.token"
        c21.write_secret_file(token_file, service_token)
        c21.apply_core_token_secret(kubeconfig, token_file)
        token_file.unlink(missing_ok=True)
        service_token = ""

        print("c30: rolling inference-service", flush=True)
        c21.patch_deploy(
            kubeconfig,
            c21.INFERENCE_DEPLOY,
            image=inference_image,
            drop_env=c21.ENGINE_IMAGE_ENV,
        )
        rolled = True
        c21.wait_deploy(kubeconfig, c21.INFERENCE_DEPLOY)
        if c21.production_deploy_image(kubeconfig, c21.INFERENCE_DEPLOY) != inference_image:
            fail("inference-service image was not rolled")
        for name in c21.ENGINE_IMAGE_ENV:
            if c21.deploy_env(kubeconfig, c21.INFERENCE_DEPLOY, name):
                fail(f"inference-service still has {name}")
        checks.append({"id": "in-cluster-inference-service-running", "status": "passed"})
        checks.append({"id": "default-engine-image-env-removed", "status": "passed"})

        print("c30: rolling production ani-gateway", flush=True)
        c21.patch_gateway(kubeconfig, image=gateway_image)
        c21.wait_deploy(kubeconfig, c21.PRODUCTION_GATEWAY)
        if c21.production_gateway_image(kubeconfig) != gateway_image:
            fail("production ani-gateway image was not rolled")
        if c21.gateway_env(kubeconfig, "ANI_AUTH_MODE") != "auth_service":
            fail("production ani-gateway ANI_AUTH_MODE changed")
        checks.append({"id": "production-ani-gateway-rolled", "status": "passed", "image": IMAGE_TAG})
        checks.append({"id": "production-gateway-auth-mode-preserved", "status": "passed", "auth_mode": "auth_service"})

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

        print("c30: listing through ani-console", flush=True)
        list_status, listed = c22.console_request("GET", f"{base}/api/v1/svc/inference-services", token, origin=origin)
        if list_status == 401:
            fail("console-shaped list rejected the tenant session token")
        if list_status != 200:
            fail(f"console-shaped list status={list_status}, want=200")
        checks.append({"id": "console-nginx-inference-list", "status": "passed", "entry": "ani-system/ani-console"})

        leftovers = [
            str(item.get("id") or "")
            for item in (listed.get("items") or [])
            if str(item.get("name") or "").startswith("inf-c30-cpu")
        ]
        for leftover_id in leftovers:
            print("c30: deleting leftover test InferenceService", flush=True)
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

        dummy_model = str(uuid.uuid4())
        print("c30: probing missing image", flush=True)
        missing_status, missing_body = c22.console_request(
            "POST",
            f"{base}/api/v1/svc/inference-services",
            token,
            create_body("inf-c30-missing-image", dummy_model),
            origin=origin,
        )
        expect_error(missing_status, missing_body, 400, "INVALID_ARGUMENT", "create-missing-image")
        checks.append({"id": "create-missing-image-rejected", "status": "passed", "http_status": 400, "code": "INVALID_ARGUMENT"})

        print("c30: probing unpinned image_ref", flush=True)
        unpinned_status, unpinned_body = c22.console_request(
            "POST",
            f"{base}/api/v1/svc/inference-services",
            token,
            create_body("inf-c30-unpinned-image", dummy_model, image_ref=UNPINNED_IMAGE),
            origin=origin,
        )
        expect_error(unpinned_status, unpinned_body, 422, "IMAGE_UNAVAILABLE", "create-unpinned-image")
        checks.append({"id": "create-unpinned-image-rejected", "status": "passed", "http_status": 422, "code": "IMAGE_UNAVAILABLE"})

        print("c30: creating model metadata", flush=True)
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

        print("c30: registering tenant-local PVC version", flush=True)
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

        image = live.discover_image(kubeconfig)
        if "@sha256:" not in image:
            fail("runtime image is not digest-pinned")
        print("c30: creating InferenceService with digest image_ref", flush=True)
        create_status, created = c22.console_request(
            "POST",
            f"{base}/api/v1/svc/inference-services",
            token,
            create_body(SERVICE_NAME, version_id, image_ref=image),
            origin=origin,
        )
        expect_http(create_status, 202, "product-inference-service-cpu-create", created)
        service_id = str(created.get("id") or "")
        if not service_id:
            fail("create did not return an inference service id")
        if created.get("image_ref") != image:
            fail("create did not freeze the request image_ref digest")
        if created.get("image_id") not in {None, ""}:
            fail("hand-filled image_ref projected a registry image_id")
        checks.append({"id": "product-inference-service-cpu-create", "status": "passed", "http_status": 202, "image_source": "request_image_ref"})

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
            if code in {
                "UNAUTHORIZED",
                "FORBIDDEN",
                "UNAUTHENTICATED",
                "RUNTIME_MUTATION_FAILED",
                "MODEL_NOT_FOUND",
                "MODEL_INCOMPATIBLE",
                "IMAGE_UNAVAILABLE",
            }:
                fail(f"platform-workload deployment was not created: {extra[:200]}")
            time.sleep(2)
        else:
            fail("platform-workload deployment was not created")
        live.assert_cpu_deployment(kubeconfig, TENANT_NS, deploy_name, image)
        checks.append({"id": "kubectl-vllm-cpu-deployment", "status": "passed", "image": "digest-pinned-vllm-cpu"})

        observed = c22.wait_console_service(base, token, service_id, "running", 900, kubeconfig, origin)
        if observed.get("invocation_url") is not None or observed.get("endpoint_url") is not None:
            fail("product response leaked an invocation URL")
        if observed.get("image_ref") != image:
            fail("running projection lost the frozen image_ref")
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
        if started.get("image_ref") != image:
            fail("start did not reuse the frozen image_ref")
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
            "image_source": "request_image_ref",
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
        print("inference create image live gate passed")
        print(f"evidence {EVIDENCE.relative_to(ROOT)}")
        rolled = False
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
        if rolled:
            restore_images(kubeconfig, previous_gateway, previous_inference)
        shutil.rmtree(tmpdir, ignore_errors=True)


if __name__ == "__main__":
    main()
