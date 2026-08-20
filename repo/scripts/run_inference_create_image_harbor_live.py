#!/usr/bin/env python3
"""Live-gate Harbor image_id create for InferenceService.

Seeds the empty tenant Harbor project with a digest-pinned CPU vLLM image, then
proves unknown image_id is 422 and a real image_id creates a CPU runtime.
Product HTTP stays on ani-console nginx /api/. ANI_AUTH_MODE stays
auth_service. GPU/LWS remain skipped.
"""

from __future__ import annotations

import argparse
import base64
import datetime as dt
import json
import os
import shutil
import signal
import socket
import ssl
import subprocess
import sys
import tempfile
import time
import urllib.error
import urllib.parse
import urllib.request
import uuid
from pathlib import Path
from typing import Any

sys.path.insert(0, str(Path(__file__).resolve().parent))
import run_inference_console_shaped_e2e as c22
import run_inference_cpu_vllm_live_gate as live
import run_inference_incluster_e2e as c21
import run_inference_local_model_source_e2e as c23

ROOT = live.ROOT
EVIDENCE = ROOT / "development-records/live-evidence/inference-create-image-harbor-live-20260818.json"
PROFILE = "INFERENCE-SERVICE-CREATE-IMAGE-HARBOR-LIVE-GATE-C31"
CONSOLE_DEPLOY = "ani-console"
SERVICE_NAME = "inf-c31-cpu-" + uuid.uuid4().hex[:8]
TENANT_ID = "11111111-1111-1111-1111-111111111111"
TENANT_NS = "ani-tenant-" + TENANT_ID
STORAGE_PATH = "pvc://vllm-model#/models/qwen"
HARBOR_REPOSITORY = "vllm-openai-cpu"
HARBOR_TAG = "c31"
HARBOR_IMAGE_ID = f"{TENANT_ID}/{HARBOR_REPOSITORY}:{HARBOR_TAG}"
UNKNOWN_IMAGE_ID = f"{TENANT_ID}/missing-runtime:latest"
LEFTOVER_PREFIXES = ("inf-c31-cpu", "inf-c30-cpu", "inf-e2e-token", "inf-cluster-verify")
_ORIG_CONSOLE_REQUEST = c22.console_request
TUNNEL: ConsoleTunnel | None = None


class ConsoleTunnel:
    def __init__(self, kubeconfig: str, listen: str) -> None:
        self.kubeconfig = kubeconfig
        self.listen = listen
        self.proc: subprocess.Popen[str] | None = None

    def start(self) -> None:
        self.stop()
        host, port = self.listen.split(":")
        self.proc = subprocess.Popen(
            [
                "kubectl",
                "--kubeconfig",
                self.kubeconfig,
                "-n",
                c21.GATEWAY_NS,
                "port-forward",
                f"svc/{CONSOLE_DEPLOY}",
                f"{port}:80",
            ],
            stdout=subprocess.DEVNULL,
            stderr=subprocess.DEVNULL,
        )
        live.wait_tcp(host, int(port), timeout=30)

    def stop(self) -> None:
        if self.proc is None:
            return
        if self.proc.poll() is None:
            self.proc.send_signal(signal.SIGTERM)
            try:
                self.proc.wait(timeout=5)
            except subprocess.TimeoutExpired:
                self.proc.kill()
        self.proc = None

    def ensure(self) -> None:
        host, port = self.listen.split(":")
        try:
            with socket.create_connection((host, int(port)), timeout=1):
                if self.proc is not None and self.proc.poll() is None:
                    return
        except OSError:
            pass
        self.start()


def fail(message: str) -> None:
    raise SystemExit(f"inference create image harbor live gate failed: {message}")


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


def console_request(
    method: str,
    url: str,
    token: str,
    body: dict[str, Any] | None = None,
    origin: str = "",
) -> tuple[int, dict[str, Any]]:
    last: Exception | None = None
    for _ in range(20):
        try:
            if TUNNEL is not None:
                TUNNEL.ensure()
            return _ORIG_CONSOLE_REQUEST(method, url, token, body, origin=origin)
        except (urllib.error.URLError, TimeoutError, ConnectionError, OSError) as err:
            last = err
            time.sleep(2)
    fail(f"console request {method} failed: {type(last).__name__}")


c22.console_request = console_request


def create_body(name: str, model: str, image_id: str = "", image_ref: str = "") -> dict[str, Any]:
    body: dict[str, Any] = {
        "idempotency_key": str(uuid.uuid4()),
        "name": name,
        "model": model,
        "model_version_id": model,
        "served_model_name": "qwen-c31-" + uuid.uuid4().hex[:8],
        "replicas": 1,
        "resources": {"cpu": "4", "memory": "8Gi"},
    }
    if image_id:
        body["image_id"] = image_id
    if image_ref:
        body["image_ref"] = image_ref
    return body


def docker(*args: str, timeout: int = 180, input_text: str | None = None) -> None:
    completed = live.run(["docker", *args], timeout=timeout, input=input_text)
    if completed.returncode != 0:
        fail(f"docker {' '.join(args[:4])} failed: {live.redact_text(completed.stderr or completed.stdout)}")


def harbor_json(
    endpoint: str,
    username: str,
    password: str,
    method: str,
    path: str,
    body: dict[str, Any] | None = None,
) -> tuple[int, Any]:
    url = endpoint.rstrip("/") + path
    data = None
    headers = {"Accept": "application/json"}
    if body is not None:
        data = json.dumps(body).encode("utf-8")
        headers["Content-Type"] = "application/json"
    token = base64.b64encode(f"{username}:{password}".encode("utf-8")).decode("ascii")
    headers["Authorization"] = "Basic " + token
    request = urllib.request.Request(url, data=data, method=method, headers=headers)
    context = ssl._create_unverified_context()
    try:
        with urllib.request.urlopen(request, context=context, timeout=30) as response:
            raw = response.read()
            parsed: Any = {}
            if raw:
                parsed = json.loads(raw.decode("utf-8"))
            return int(response.status), parsed
    except urllib.error.HTTPError as err:
        raw = err.read() if err.fp is not None else b""
        parsed = {}
        if raw:
            try:
                parsed = json.loads(raw.decode("utf-8"))
            except Exception:
                parsed = {"message": live.redact_text(raw.decode("utf-8", errors="replace")[:160])}
        return int(err.code), parsed


def seed_harbor(kubeconfig: str) -> str:
    endpoint = c21.gateway_env(kubeconfig, "HARBOR_ENDPOINT").rstrip("/")
    username = c21.gateway_env(kubeconfig, "HARBOR_USERNAME")
    password = c21.gateway_env(kubeconfig, "HARBOR_PASSWORD")
    if not endpoint or not username or not password or password == "<from-ref>":
        fail("production ani-gateway is missing Harbor credentials")
    host = urllib.parse.urlparse(endpoint).hostname or ""
    if not host:
        fail("Harbor endpoint host is missing")
    status, _ = harbor_json(
        endpoint,
        username,
        password,
        "POST",
        "/api/v2.0/projects",
        {"project_name": TENANT_ID, "public": True},
    )
    if status not in {201, 409}:
        fail(f"Harbor tenant project create status={status}, want=201 or 409")
    source = live.IMAGE_FALLBACK
    inspected = live.run(["docker", "image", "inspect", source], timeout=30)
    if inspected.returncode != 0:
        print("c31: pulling CPU vLLM source image", flush=True)
        docker("pull", source, timeout=1800)
    target = f"{host}/{HARBOR_IMAGE_ID}"
    docker("tag", source, target, timeout=60)
    print("c31: pushing tenant Harbor image", flush=True)
    docker("login", host, "-u", username, "--password-stdin", timeout=60, input_text=password + "\n")
    try:
        docker("push", target, timeout=1800)
    finally:
        live.run(["docker", "logout", host], timeout=30)
    digest = ""
    deadline = time.time() + 120
    encoded_repo = urllib.parse.quote(HARBOR_REPOSITORY, safe="")
    while time.time() < deadline:
        status, artifacts = harbor_json(
            endpoint,
            username,
            password,
            "GET",
            f"/api/v2.0/projects/{urllib.parse.quote(TENANT_ID, safe='')}/repositories/{encoded_repo}/artifacts?with_tag=true",
        )
        if status == 200 and isinstance(artifacts, list):
            for item in artifacts:
                tags = [str(tag.get("name") or "") for tag in (item.get("tags") or []) if isinstance(tag, dict)]
                if HARBOR_TAG in tags:
                    digest = str(item.get("digest") or "").strip()
                    break
        if digest.startswith("sha256:") and len(digest) == 71:
            break
        time.sleep(3)
    username = ""
    password = ""
    if not digest.startswith("sha256:") or len(digest) != 71:
        fail("Harbor did not return a digest-pinned tenant image")
    name = f"{host}/{TENANT_ID}/{HARBOR_REPOSITORY}"
    return f"{name}@{digest}"


def refresh_core_service_token(kubeconfig: str) -> None:
    issuer = c21.secret_bytes(kubeconfig, c21.AUTH_SECRET, "auth_jwt_issuer").decode("utf-8").strip()
    if not issuer:
        fail("auth-service issuer is not configured")
    private_key = c21.secret_bytes(kubeconfig, c21.AUTH_SECRET, "jwt_private_key_pem")
    service_token = c21.mint_jwt(private_key, c21.service_claims(issuer, TENANT_ID, int(time.time())))
    token_file = Path(tempfile.mkdtemp(prefix="ani-inf-c31-token-")) / "core.token"
    c21.write_secret_file(token_file, service_token)
    c21.apply_core_token_secret(kubeconfig, token_file)
    token_file.unlink(missing_ok=True)
    live.kubectl(
        kubeconfig,
        ["-n", c21.GATEWAY_NS, "rollout", "restart", f"deploy/{c21.INFERENCE_DEPLOY}"],
    )
    c21.wait_deploy(kubeconfig, c21.INFERENCE_DEPLOY)
    service_token = ""
    private_key = b""


def rekey_cancelled_delete(kubeconfig: str, service_id: str) -> None:
    try:
        uuid.UUID(service_id)
    except ValueError:
        fail("leftover service id is not a uuid")
    live.postgres_exec(
        kubeconfig,
        "UPDATE inference_operations SET idempotency_key = gen_random_uuid(), updated_at = NOW() "
        f"WHERE service_id = '{service_id}'::uuid AND type = 'delete' AND state IN ('cancelled', 'failed');",
    )


def wait_gone(base: str, token: str, origin: str, kubeconfig: str, service_id: str, timeout: int = 360) -> None:
    deploy = live.runtime_resource_name(service_id)
    deadline = time.time() + timeout
    while time.time() < deadline:
        gone_status, _ = console_request("GET", f"{base}/api/v1/svc/inference-services/{service_id}", token, origin=origin)
        leftover = live.run(["kubectl", "--kubeconfig", kubeconfig, "-n", TENANT_NS, "get", "deploy", deploy])
        if gone_status == 404 and leftover.returncode != 0:
            return
        if gone_status != 404:
            console_request(
                "DELETE",
                f"{base}/api/v1/svc/inference-services/{service_id}",
                token,
                origin=origin,
            )
        time.sleep(3)
    fail(f"leftover InferenceService {service_id} was not removed")


def main() -> None:
    parser = argparse.ArgumentParser()
    parser.add_argument("--kubeconfig", default=os.environ.get("KUBECONFIG", str(Path.home() / ".kube/config")))
    parser.add_argument("--listen", default="127.0.0.1:18089")
    args = parser.parse_args()
    kubeconfig = args.kubeconfig
    if not Path(kubeconfig).exists():
        fail("kubeconfig is missing")

    global TUNNEL
    checks: list[dict[str, Any]] = []
    tmpdir = Path(tempfile.mkdtemp(prefix="ani-inf-c31-"))
    service_id = ""
    token = ""
    origin = "http://" + args.listen
    try:
        if c21.gateway_env(kubeconfig, "ANI_AUTH_MODE") != "auth_service":
            fail("production ani-gateway ANI_AUTH_MODE is not auth_service")
        if c21.gateway_env(kubeconfig, "INFERENCE_SERVICE_GRPC_ADDR") != c21.INFERENCE_GRPC_ADDR:
            fail("production ani-gateway is missing inference gRPC address")
        if c21.gateway_env(kubeconfig, "PLATFORM_WORKLOAD_PROVIDER") != "kubernetes_rest":
            fail("production ani-gateway is missing kubernetes_rest platform-workload provider")
        if c21.gateway_env(kubeconfig, "REGISTRY_PROVIDER_MODE") != "harbor":
            fail("production ani-gateway REGISTRY_PROVIDER_MODE is not harbor")
        ns = live.run(["kubectl", "--kubeconfig", kubeconfig, "get", "ns", TENANT_NS])
        if ns.returncode != 0:
            fail("existing console tenant namespace is missing")
        pvc = live.run(["kubectl", "--kubeconfig", kubeconfig, "-n", TENANT_NS, "get", "pvc", "vllm-model"])
        if pvc.returncode != 0:
            fail("tenant namespace is missing vllm-model PVC")
        c23.wait_seed(kubeconfig)
        checks.append({"id": "tenant-local-pvc-ready", "status": "passed", "storage_path": STORAGE_PATH})
        if "create-image-c30" not in c21.production_gateway_image(kubeconfig):
            fail("production ani-gateway is not the create-image C30 image")
        if "create-image-c30" not in c21.production_deploy_image(kubeconfig, c21.INFERENCE_DEPLOY):
            fail("inference-service is not the create-image C30 image")
        for name in c21.ENGINE_IMAGE_ENV:
            if c21.deploy_env(kubeconfig, c21.INFERENCE_DEPLOY, name):
                fail(f"inference-service still has {name}")
        print("c31: refreshing inference-service Core JWT", flush=True)
        refresh_core_service_token(kubeconfig)
        checks.append({"id": "in-cluster-inference-service-running", "status": "passed"})
        if c21.gateway_env(kubeconfig, "ANI_AUTH_MODE") != "auth_service":
            fail("production ani-gateway ANI_AUTH_MODE changed")
        checks.append({"id": "production-gateway-auth-mode-preserved", "status": "passed", "auth_mode": "auth_service"})

        pinned = seed_harbor(kubeconfig)
        checks.append({"id": "harbor-tenant-image-seeded", "status": "passed", "image_id": HARBOR_IMAGE_ID})

        issuer = c21.secret_bytes(kubeconfig, c21.AUTH_SECRET, "auth_jwt_issuer").decode("utf-8").strip()
        if not issuer:
            fail("auth-service issuer is not configured")
        private_key = c21.secret_bytes(kubeconfig, c21.AUTH_SECRET, "jwt_private_key_pem")
        token = c21.mint_jwt(private_key, c21.tenant_claims(issuer, TENANT_ID, str(uuid.uuid4()), int(time.time())))
        private_key = b""

        TUNNEL = ConsoleTunnel(kubeconfig, args.listen)
        TUNNEL.start()
        base = f"http://{args.listen}"

        print("c31: listing through ani-console", flush=True)
        list_status, listed = console_request("GET", f"{base}/api/v1/svc/inference-services", token, origin=origin)
        if list_status == 401:
            fail("console-shaped list rejected the tenant session token")
        if list_status != 200:
            fail(f"console-shaped list status={list_status}, want=200")
        checks.append({"id": "console-nginx-inference-list", "status": "passed", "entry": "ani-system/ani-console"})

        leftovers = [
            str(item.get("id") or "")
            for item in (listed.get("items") or [])
            if str(item.get("name") or "").startswith(LEFTOVER_PREFIXES)
        ]
        for leftover_id in leftovers:
            print("c31: deleting leftover InferenceService", flush=True)
            rekey_cancelled_delete(kubeconfig, leftover_id)
            console_request("DELETE", f"{base}/api/v1/svc/inference-services/{leftover_id}", token, origin=origin)
            wait_gone(base, token, origin, kubeconfig, leftover_id)

        dummy_model = str(uuid.uuid4())
        print("c31: probing missing image", flush=True)
        missing_status, missing_body = c22.console_request(
            "POST",
            f"{base}/api/v1/svc/inference-services",
            token,
            create_body("inf-c31-missing-image", dummy_model),
            origin=origin,
        )
        expect_error(missing_status, missing_body, 400, "INVALID_ARGUMENT", "create-missing-image")
        checks.append({"id": "create-missing-image-rejected", "status": "passed", "http_status": 400, "code": "INVALID_ARGUMENT"})

        print("c31: probing unknown image_id", flush=True)
        unknown_status, unknown_body = c22.console_request(
            "POST",
            f"{base}/api/v1/svc/inference-services",
            token,
            create_body("inf-c31-unknown-image", dummy_model, image_id=UNKNOWN_IMAGE_ID),
            origin=origin,
        )
        expect_error(unknown_status, unknown_body, 422, "IMAGE_UNAVAILABLE", "create-unknown-image-id")
        checks.append({"id": "create-unknown-image-id-rejected", "status": "passed", "http_status": 422, "code": "IMAGE_UNAVAILABLE"})

        print("c31: creating model metadata", flush=True)
        model_status, model = c22.console_request(
            "POST",
            f"{base}/api/v1/svc/models",
            token,
            {
                "idempotency_key": str(uuid.uuid4()),
                "name": "qwen-c31-" + uuid.uuid4().hex[:8],
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

        print("c31: registering tenant-local PVC version", flush=True)
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

        print("c31: creating InferenceService with Harbor image_id", flush=True)
        create_status, created = c22.console_request(
            "POST",
            f"{base}/api/v1/svc/inference-services",
            token,
            create_body(SERVICE_NAME, version_id, image_id=HARBOR_IMAGE_ID),
            origin=origin,
        )
        expect_http(create_status, 202, "product-inference-service-cpu-create", created)
        service_id = str(created.get("id") or "")
        if not service_id:
            fail("create did not return an inference service id")
        if created.get("image_id") != HARBOR_IMAGE_ID:
            fail("create did not project the request image_id")
        if created.get("image_ref") != pinned:
            fail("create did not freeze the Harbor digest image_ref")
        checks.append({"id": "product-inference-service-cpu-create", "status": "passed", "http_status": 202, "image_source": "request_image_id"})

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
        live.assert_cpu_deployment(kubeconfig, TENANT_NS, deploy_name, pinned)
        checks.append({"id": "kubectl-vllm-cpu-deployment", "status": "passed", "image": "harbor-digest-pinned-vllm-cpu"})

        observed = c22.wait_console_service(base, token, service_id, "running", 900, kubeconfig, origin)
        if observed.get("invocation_url") is not None or observed.get("endpoint_url") is not None:
            fail("product response leaked an invocation URL")
        if observed.get("image_id") != HARBOR_IMAGE_ID or observed.get("image_ref") != pinned:
            fail("running projection lost the frozen Harbor image")
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
        if started.get("image_id") != HARBOR_IMAGE_ID or started.get("image_ref") != pinned:
            fail("start did not reuse the frozen Harbor image")
        checks.append({"id": "inference-service-start", "status": "passed", "same_service_id": True})

        delete_status, _ = c22.console_request(
            "DELETE",
            f"{base}/api/v1/svc/inference-services/{service_id}",
            token,
            origin=origin,
        )
        expect_http(delete_status, 202, "inference-service-delete")
        wait_gone(base, token, origin, kubeconfig, service_id)
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
            "image_source": "request_image_id",
            "engine": "vllm-cpu",
            "image": "harbor-digest-pinned-vllm-cpu",
            "namespace_kind": "existing-ani-tenant-{uuid}",
            "probe": "clusterip",
            "gpu_live": "skipped_no_device_plugin",
            "checks": checks,
        }
        live.assert_clean_evidence(evidence)
        EVIDENCE.parent.mkdir(parents=True, exist_ok=True)
        EVIDENCE.write_text(json.dumps(evidence, indent=2) + "\n", encoding="utf-8")
        print("inference create image harbor live gate passed")
        print(f"evidence {EVIDENCE.relative_to(ROOT)}")
    finally:
        if service_id and token:
            try:
                rekey_cancelled_delete(kubeconfig, service_id)
                console_request(
                    "DELETE",
                    f"http://{args.listen}/api/v1/svc/inference-services/{service_id}",
                    token,
                    origin=origin,
                )
            except Exception:
                pass
        token = ""
        if TUNNEL is not None:
            TUNNEL.stop()
            TUNNEL = None
        shutil.rmtree(tmpdir, ignore_errors=True)


if __name__ == "__main__":
    main()
