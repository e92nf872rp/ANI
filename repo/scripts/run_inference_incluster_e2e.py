#!/usr/bin/env python3
"""Run CPU InferenceService E2E against in-cluster inference-service.

This deploys inference-service in ani-system and rolls the existing production
ani-gateway to the current Gateway binary. Product traffic stays on
/api/v1/svc/inference-services through that Gateway. ANI_AUTH_MODE stays
auth_service. It must not deploy a second Gateway and must not touch
ani-vllm-cpu-smoke.
"""

from __future__ import annotations

import argparse
import base64
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

import yaml

sys.path.insert(0, str(Path(__file__).resolve().parent))
import run_inference_cpu_vllm_live_gate as live

ROOT = live.ROOT
MANIFEST = ROOT / "deploy/real-k8s-lab/inference-incluster-e2e.yaml"
EVIDENCE = ROOT / "development-records/live-evidence/inference-incluster-e2e-live-20260817.json"
PROFILE = "INFERENCE-SERVICE-INCLUSTER-E2E-LIVE-GATE-C21"
IMAGE_TAG = "incluster-e2e-20260817"
REGISTRY = "docker.changqingyun.cn/ani"
GATEWAY_NS = "ani-system"
INFERENCE_DEPLOY = "inference-service"
PRODUCTION_GATEWAY = "ani-gateway"
CORE_TOKEN_SECRET = "inference-c21-core-token"
AUTH_SECRET = "ani-services-runtime"
INFERENCE_GRPC_ADDR = "inference-service.ani-system.svc.cluster.local:9104"
SERVICE_ACTOR = "00000000-0000-0000-0000-0000000000aa"
STABLE_GATEWAY_IMAGE = f"{REGISTRY}/ani-gateway:storage-control-plane-state-20260803-v4"
STABLE_AUTH_IMAGE = f"{REGISTRY}/ani-auth-service:auth-admin-role-20260707-7cb8d5d-dirty"
AUTH_DEPLOY = "ani-auth-service"
GATEWAY_EXTRA_ENV = {
    "INFERENCE_SERVICE_GRPC_ADDR": INFERENCE_GRPC_ADDR,
    "PLATFORM_WORKLOAD_PROVIDER": "kubernetes_rest",
}


def fail(message: str) -> None:
    raise SystemExit(f"inference incluster e2e live gate failed: {message}")


def docker(*args: str) -> None:
    completed = live.run(["docker", *args], timeout=180)
    if completed.returncode != 0:
        fail(f"docker {' '.join(args[:6])} failed: {live.redact_text(completed.stderr or completed.stdout)}")


def wait_deploy(kubeconfig: str, name: str, timeout: int = 180) -> None:
    live.kubectl(
        kubeconfig,
        ["-n", GATEWAY_NS, "rollout", "status", f"deploy/{name}", f"--timeout={timeout}s"],
        timeout=timeout + 30,
    )


def docker_image_exists(tag: str) -> bool:
    return live.run(["docker", "image", "inspect", tag], timeout=30).returncode == 0


def production_auth_image(kubeconfig: str) -> str:
    deploy = live.kubectl_json(kubeconfig, ["-n", GATEWAY_NS, "get", "deploy", AUTH_DEPLOY])
    containers = (((deploy.get("spec") or {}).get("template") or {}).get("spec") or {}).get("containers") or []
    if not containers:
        fail("production ani-auth-service has no container")
    return str(containers[0].get("image") or "")


def patch_deploy_image(kubeconfig: str, deploy: str, image: str) -> None:
    completed = subprocess.run(
        [
            "kubectl",
            "--kubeconfig",
            kubeconfig,
            "-n",
            GATEWAY_NS,
            "patch",
            "deploy",
            deploy,
            "--type=json",
            f"--patch={json.dumps([{'op': 'replace', 'path': '/spec/template/spec/containers/0/image', 'value': image}], separators=(',', ':'))}",
        ],
        text=True,
        capture_output=True,
        check=False,
    )
    if completed.returncode != 0:
        fail(f"failed to patch {deploy} image")


def restore_auth(kubeconfig: str, image: str) -> None:
    patch_deploy_image(kubeconfig, AUTH_DEPLOY, image)
    live.run(
        [
            "kubectl",
            "--kubeconfig",
            kubeconfig,
            "-n",
            GATEWAY_NS,
            "rollout",
            "status",
            f"deploy/{AUTH_DEPLOY}",
            "--timeout=180s",
        ],
        timeout=210,
    )


def production_gateway_image(kubeconfig: str) -> str:
    return production_deploy_image(kubeconfig, PRODUCTION_GATEWAY)


def deploy_env(kubeconfig: str, deploy: str, name: str) -> str:
    document = live.kubectl_json(kubeconfig, ["-n", GATEWAY_NS, "get", "deploy", deploy])
    containers = (((document.get("spec") or {}).get("template") or {}).get("spec") or {}).get("containers") or []
    for item in (containers[0].get("env") or []) if containers else []:
        if item.get("name") == name:
            if item.get("valueFrom"):
                return "<from-ref>"
            return str(item.get("value") or "")
    return ""


def gateway_env(kubeconfig: str, name: str) -> str:
    return deploy_env(kubeconfig, PRODUCTION_GATEWAY, name)


def secret_bytes(kubeconfig: str, name: str, key: str) -> bytes:
    document = live.kubectl_json(kubeconfig, ["-n", GATEWAY_NS, "get", "secret", name])
    raw = ((document.get("data") or {}).get(key) or "")
    if not raw:
        fail("required runtime secret key is missing")
    try:
        return base64.b64decode(raw)
    except Exception:
        fail("required runtime secret key could not be decoded")


def b64url(data: bytes) -> str:
    return base64.urlsafe_b64encode(data).rstrip(b"=").decode("ascii")


def mint_jwt(private_key_pem: bytes, claims: dict[str, Any]) -> str:
    header = {"alg": "RS256", "typ": "JWT"}
    signing_input = (
        b64url(json.dumps(header, separators=(",", ":"), ensure_ascii=True).encode("ascii"))
        + "."
        + b64url(json.dumps(claims, separators=(",", ":"), ensure_ascii=True).encode("ascii"))
    )
    handle = tempfile.NamedTemporaryFile(prefix="ani-c21-jwt-", suffix=".pem", delete=False)
    key_path = Path(handle.name)
    try:
        handle.write(private_key_pem)
        handle.close()
        key_path.chmod(0o600)
        completed = subprocess.run(
            ["openssl", "dgst", "-sha256", "-sign", str(key_path)],
            input=signing_input.encode("ascii"),
            capture_output=True,
            check=False,
        )
        if completed.returncode != 0 or not completed.stdout:
            fail("failed to mint access token")
        return signing_input + "." + b64url(completed.stdout)
    finally:
        handle.close()
        key_path.unlink(missing_ok=True)


def tenant_claims(issuer: str, tenant_id: str, user_id: str, now: int) -> dict[str, Any]:
    return {
        "sub": user_id,
        "iss": issuer,
        "exp": now + 3600,
        "nbf": now - 5,
        "iat": now,
        "jti": str(uuid.uuid4()),
        "tid": tenant_id,
        "uid": user_id,
        "roles": ["tenant-admin"],
        "scope": "tenant",
    }


def service_claims(issuer: str, tenant_id: str, now: int) -> dict[str, Any]:
    return {
        "sub": SERVICE_ACTOR,
        "iss": issuer,
        "aud": "ani-core",
        "exp": now + 3600,
        "nbf": now - 5,
        "iat": now,
        "jti": str(uuid.uuid4()),
        "tid": tenant_id,
        "uid": SERVICE_ACTOR,
        "roles": ["service"],
        "scope": "scope:platform-workloads:write",
        "principal_kind": "service",
    }


def go_build(module: Path, output: Path, extra: list[str]) -> None:
    env = os.environ.copy()
    env["GOWORK"] = "off"
    env["CGO_ENABLED"] = "0"
    completed = subprocess.run(
        ["go", "build", "-ldflags", "-s -w", "-o", str(output), *extra],
        cwd=str(module),
        env=env,
        text=True,
        capture_output=True,
        timeout=180,
        check=False,
    )
    if completed.returncode != 0:
        fail(f"go build {module.name} failed: {live.redact_text((completed.stderr or completed.stdout)[-800:])}")


def write_secret_file(path: Path, value: str) -> None:
    path.write_text(value, encoding="utf-8")
    path.chmod(0o600)


def apply_core_token_secret(kubeconfig: str, token_file: Path) -> None:
    completed = live.run(
        [
            "kubectl",
            "--kubeconfig",
            kubeconfig,
            "-n",
            GATEWAY_NS,
            "create",
            "secret",
            "generic",
            CORE_TOKEN_SECRET,
            f"--from-file=token={token_file}",
            "--dry-run=client",
            "-o",
            "yaml",
        ]
    )
    if completed.returncode != 0:
        fail("failed to render core token secret")
    applied = subprocess.run(
        ["kubectl", "--kubeconfig", kubeconfig, "apply", "-f", "-"],
        input=completed.stdout,
        text=True,
        capture_output=True,
        check=False,
    )
    if applied.returncode != 0:
        fail("failed to apply core token secret")


ENGINE_IMAGE_ENV = (
    "INFERENCE_CPU_IMAGE_REF",
    "INFERENCE_GPU_IMAGE_REF",
    "INFERENCE_SGLANG_CPU_IMAGE_REF",
    "INFERENCE_SGLANG_GPU_IMAGE_REF",
)


def production_deploy_image(kubeconfig: str, deploy: str) -> str:
    document = live.kubectl_json(kubeconfig, ["-n", GATEWAY_NS, "get", "deploy", deploy])
    containers = (((document.get("spec") or {}).get("template") or {}).get("spec") or {}).get("containers") or []
    if not containers:
        fail(f"production {deploy} has no container")
    return str(containers[0].get("image") or "")


def deploy_env_names(kubeconfig: str, deploy: str) -> list[str]:
    document = live.kubectl_json(kubeconfig, ["-n", GATEWAY_NS, "get", "deploy", deploy])
    containers = (((document.get("spec") or {}).get("template") or {}).get("spec") or {}).get("containers") or []
    if not containers:
        return []
    return [str(item.get("name") or "") for item in (containers[0].get("env") or [])]


def gateway_env_names(kubeconfig: str) -> list[str]:
    return deploy_env_names(kubeconfig, PRODUCTION_GATEWAY)


def patch_deploy(
    kubeconfig: str,
    deploy: str,
    image: str | None = None,
    extra_env: dict[str, str] | None = None,
    drop_env: tuple[str, ...] = (),
) -> None:
    names = deploy_env_names(kubeconfig, deploy)
    ops: list[dict[str, Any]] = []
    extra_env = extra_env or {}
    if image:
        ops.append({"op": "replace", "path": "/spec/template/spec/containers/0/image", "value": image})
    for key, value in extra_env.items():
        item = {"name": key, "value": value}
        if key in names:
            ops.append(
                {
                    "op": "replace",
                    "path": f"/spec/template/spec/containers/0/env/{names.index(key)}",
                    "value": item,
                }
            )
        else:
            ops.append({"op": "add", "path": "/spec/template/spec/containers/0/env/-", "value": item})
    for index, name in reversed(list(enumerate(names))):
        if name in drop_env and name not in extra_env:
            ops.append({"op": "remove", "path": f"/spec/template/spec/containers/0/env/{index}"})
    if not ops:
        return
    completed = subprocess.run(
        [
            "kubectl",
            "--kubeconfig",
            kubeconfig,
            "-n",
            GATEWAY_NS,
            "patch",
            "deploy",
            deploy,
            "--type=json",
            f"--patch={json.dumps(ops, separators=(',', ':'))}",
        ],
        text=True,
        capture_output=True,
        check=False,
    )
    if completed.returncode != 0:
        fail(f"failed to patch {deploy}")


def patch_gateway(
    kubeconfig: str,
    image: str | None = None,
    extra_env: dict[str, str] | None = None,
    drop_env: tuple[str, ...] = (),
) -> None:
    patch_deploy(kubeconfig, PRODUCTION_GATEWAY, image=image, extra_env=extra_env, drop_env=drop_env)


def restore_gateway(kubeconfig: str, image: str) -> None:
    patch_gateway(kubeconfig, image=image, drop_env=tuple(GATEWAY_EXTRA_ENV))
    live.run(
        [
            "kubectl",
            "--kubeconfig",
            kubeconfig,
            "-n",
            GATEWAY_NS,
            "rollout",
            "status",
            f"deploy/{PRODUCTION_GATEWAY}",
            "--timeout=180s",
        ],
        timeout=210,
    )


def delete_second_gateway(kubeconfig: str) -> None:
    for args in (
        ["-n", GATEWAY_NS, "delete", "deploy", "ani-inference-gateway", "--ignore-not-found"],
        ["-n", GATEWAY_NS, "delete", "svc", "ani-inference-gateway", "--ignore-not-found"],
        ["-n", GATEWAY_NS, "delete", "sa", "ani-inference-gateway", "--ignore-not-found"],
        ["delete", "clusterrole", "ani-inference-gateway", "--ignore-not-found"],
        ["delete", "clusterrolebinding", "ani-inference-gateway", "--ignore-not-found"],
    ):
        live.run(["kubectl", "--kubeconfig", kubeconfig, *args])


def build_overlay_image(tag: str, binary_name: str, binary: Path, current_image: str, workdir: Path) -> None:
    context = workdir / f"{binary_name}-image"
    context.mkdir()
    shutil.copy2(binary, context / binary_name)
    (context / "Dockerfile").write_text(
        "\n".join(
            [
                f"FROM {current_image}",
                "USER root",
                f"COPY {binary_name} /usr/local/bin/{binary_name}",
                "USER 65532:65532",
                f'ENTRYPOINT ["/usr/local/bin/{binary_name}"]',
                "",
            ]
        ),
        encoding="utf-8",
    )
    docker("build", "-t", tag, str(context))
    docker("push", tag)


def build_gateway_image(tag: str, binary: Path, current_image: str, workdir: Path) -> None:
    build_overlay_image(tag, "ani-gateway", binary, current_image, workdir)


def build_inference_image(tag: str, binary: Path, workdir: Path) -> None:
    context = workdir / "inference-image"
    context.mkdir()
    shutil.copy2(binary, context / "inference-service")
    (context / "Dockerfile").write_text(
        "\n".join(
            [
                "FROM alpine:3.20",
                "RUN apk add --no-cache ca-certificates && adduser -D -H -u 65532 ani",
                "COPY inference-service /usr/local/bin/inference-service",
                "USER 65532:65532",
                'ENTRYPOINT ["/usr/local/bin/inference-service"]',
                "",
            ]
        ),
        encoding="utf-8",
    )
    docker("build", "-t", tag, str(context))
    docker("push", tag)


def main() -> None:
    parser = argparse.ArgumentParser()
    parser.add_argument("--kubeconfig", default=os.environ.get("KUBECONFIG", str(Path.home() / ".kube/config")))
    parser.add_argument("--listen", default="127.0.0.1:18086")
    parser.add_argument("--skip-build", action="store_true")
    args = parser.parse_args()
    kubeconfig = args.kubeconfig
    listen = args.listen
    if not Path(kubeconfig).exists():
        fail("kubeconfig is missing")
    if not MANIFEST.exists():
        fail("in-cluster e2e manifest is missing")

    tenant_id = str(uuid.uuid4())
    user_id = str(uuid.uuid4())
    tenant_name = "inf-c21-lab-" + tenant_id[:8]
    model_id = str(uuid.uuid4())
    namespace = f"ani-tenant-{tenant_id}"
    checks: list[dict[str, Any]] = []
    forward: subprocess.Popen[str] | None = None
    tmpdir = Path(tempfile.mkdtemp(prefix="ani-inf-c21-"))
    created_ns = False
    src_snapshot = ""
    dest_vsc = ""
    previous_image = production_gateway_image(kubeconfig)
    if IMAGE_TAG in previous_image:
        previous_image = STABLE_GATEWAY_IMAGE
    previous_auth_image = production_auth_image(kubeconfig)
    if IMAGE_TAG in previous_auth_image:
        previous_auth_image = STABLE_AUTH_IMAGE
    rolled_gateway = False
    rolled_auth = False
    tenant_token = ""
    try:
        delete_second_gateway(kubeconfig)
        if gateway_env(kubeconfig, "ANI_AUTH_MODE") != "auth_service":
            fail("production ani-gateway ANI_AUTH_MODE is not auth_service")
        if not live.smoke_ready(kubeconfig):
            fail("independent vLLM CPU smoke workload is not ready")
        image = live.discover_image(kubeconfig)
        inference_image = f"{REGISTRY}/inference-service:{IMAGE_TAG}"
        gateway_image = f"{REGISTRY}/ani-gateway:{IMAGE_TAG}"
        auth_image = f"{REGISTRY}/ani-auth-service:{IMAGE_TAG}"
        if not args.skip_build:
            go_build(ROOT / "services/inference-service", tmpdir / "inference-service", ["."])
            go_build(ROOT / "services/ani-gateway", tmpdir / "ani-gateway", ["-tags", "stdjson", "."])
            build_inference_image(inference_image, tmpdir / "inference-service", tmpdir)
            build_gateway_image(gateway_image, tmpdir / "ani-gateway", previous_image, tmpdir)
        if not docker_image_exists(auth_image):
            print("c21: building auth-service image", flush=True)
            go_build(ROOT / "services/auth-service", tmpdir / "auth-service", ["-tags", "stdjson", "."])
            build_overlay_image(auth_image, "auth-service", tmpdir / "auth-service", previous_auth_image, tmpdir)

        auth_issuer = secret_bytes(kubeconfig, AUTH_SECRET, "auth_jwt_issuer").decode("utf-8").strip()
        if not auth_issuer:
            fail("auth-service issuer is not configured")
        now = int(time.time())
        private_key = secret_bytes(kubeconfig, AUTH_SECRET, "jwt_private_key_pem")
        tenant_token = mint_jwt(private_key, tenant_claims(auth_issuer, tenant_id, user_id, now))
        service_token = mint_jwt(private_key, service_claims(auth_issuer, tenant_id, now))
        token_file = tmpdir / "core.token"
        write_secret_file(token_file, service_token)
        apply_core_token_secret(kubeconfig, token_file)
        token_file.unlink(missing_ok=True)
        private_key = b""

        print("c21: applying inference-service", flush=True)
        live.kubectl(kubeconfig, ["apply", "-f", str(MANIFEST)])
        patch_deploy(kubeconfig, INFERENCE_DEPLOY, image=inference_image, drop_env=ENGINE_IMAGE_ENV)
        wait_deploy(kubeconfig, INFERENCE_DEPLOY)
        checks.append({"id": "in-cluster-inference-service-running", "status": "passed"})
        print("c21: inference-service ready", flush=True)

        print("c21: rolling production ani-auth-service", flush=True)
        patch_deploy_image(kubeconfig, AUTH_DEPLOY, auth_image)
        rolled_auth = True
        wait_deploy(kubeconfig, AUTH_DEPLOY)
        print("c21: production ani-auth-service ready", flush=True)

        print("c21: rolling production ani-gateway", flush=True)
        patch_gateway(kubeconfig, image=gateway_image, extra_env=GATEWAY_EXTRA_ENV)
        rolled_gateway = True
        wait_deploy(kubeconfig, PRODUCTION_GATEWAY)
        if production_gateway_image(kubeconfig) != gateway_image:
            fail("production ani-gateway image was not rolled")
        if gateway_env(kubeconfig, "INFERENCE_SERVICE_GRPC_ADDR") != INFERENCE_GRPC_ADDR:
            fail("production ani-gateway is missing inference gRPC address")
        if gateway_env(kubeconfig, "PLATFORM_WORKLOAD_PROVIDER") != "kubernetes_rest":
            fail("production ani-gateway is missing kubernetes_rest platform-workload provider")
        if gateway_env(kubeconfig, "ANI_AUTH_MODE") != "auth_service":
            fail("production ani-gateway ANI_AUTH_MODE changed")
        checks.append({"id": "production-ani-gateway-rolled", "status": "passed", "image": IMAGE_TAG})
        checks.append({"id": "production-gateway-auth-mode-preserved", "status": "passed", "auth_mode": "auth_service"})
        print("c21: production ani-gateway ready", flush=True)

        host, port = listen.split(":")
        forward = subprocess.Popen(
            ["kubectl", "--kubeconfig", kubeconfig, "-n", GATEWAY_NS, "port-forward", f"svc/{PRODUCTION_GATEWAY}", f"{port}:8080"],
            stdout=subprocess.DEVNULL,
            stderr=subprocess.DEVNULL,
        )
        live.wait_tcp(host, int(port), timeout=30)
        base = f"http://{listen}"
        live.wait_http(f"{base}/readyz", timeout=60)
        print("c21: probing platform-workloads", flush=True)
        cap_status, cap_body = live.request(
            "GET",
            f"{base}/api/v1/platform-workload-capabilities",
            tenant_id,
            token=service_token,
        )
        cap_code = str(cap_body.get("code") or "")
        if cap_status != 200:
            fail(f"platform-workload capabilities status={cap_status} code={cap_code}")
        service_token = ""

        live.apply_platform_workload_migration(kubeconfig)
        live.apply_sql(kubeconfig, live.INF_MIGRATION.read_text(encoding="utf-8"), "apply inference control-plane migration")
        live.postgres_exec(kubeconfig, "DELETE FROM tenants WHERE name LIKE 'inf-c21-lab%';")
        live.postgres_exec(
            kubeconfig,
            "INSERT INTO tenants (id, name, display_name, status, max_gpu_count, max_cpu_cores, max_memory_gb, settings) "
            f"VALUES ('{tenant_id}', '{tenant_name}', 'inference-incluster-e2e-c21', 'active', 0, 8, 16, '{{}}') "
            "ON CONFLICT (id) DO NOTHING;",
        )
        ns_doc = {
            "apiVersion": "v1",
            "kind": "Namespace",
            "metadata": {
                "name": namespace,
                "labels": {
                    "app.kubernetes.io/part-of": "ani-platform",
                    "ani.kubercloud.io/tenant-id": tenant_id,
                },
            },
        }
        (tmpdir / "namespace.yaml").write_text(yaml.safe_dump(ns_doc), encoding="utf-8")
        live.kubectl(kubeconfig, ["apply", "-f", str(tmpdir / "namespace.yaml")])
        created_ns = True
        src_snapshot, dest_vsc = live.clone_model_pvc(kubeconfig, namespace, tmpdir)

        list_status, _ = live.request("GET", f"{base}/api/v1/svc/inference-services", tenant_id, token=tenant_token)
        if list_status == 401:
            fail("product inference API rejected tenant token")
        if list_status not in {200}:
            fail(f"product inference API list status={list_status}, want=200")

        print("c21: creating CPU InferenceService", flush=True)
        create_status, created = live.request(
            "POST",
            f"{base}/api/v1/svc/inference-services",
            tenant_id,
            {
                "idempotency_key": str(uuid.uuid4()),
                "name": "inf-c21-cpu",
                "model": model_id,
                "model_version_id": model_id,
                "served_model_name": "qwen2.5-0.5b",
                "image_ref": image,
                "replicas": 1,
                "resources": {"cpu": "4", "memory": "8Gi"},
            },
            token=tenant_token,
        )
        live.expect(create_status, 202, "product-inference-service-cpu-create")
        service_id = str(created.get("id") or "")
        if not service_id:
            fail("create did not return an inference service id")
        checks.append({"id": "product-inference-service-cpu-create", "status": "passed", "http_status": 202})

        print("c21: waiting for runtime deployment", flush=True)
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

        observed = live.wait_service(
            base, tenant_id, service_id, "running", timeout=300, kubeconfig=kubeconfig, token=tenant_token
        )
        if observed.get("invocation_url") is not None or observed.get("endpoint_url") is not None:
            fail("product response leaked an invocation URL")
        if "accelerator" in (observed.get("resources") or {}):
            fail("CPU create projected an accelerator")
        checks.append({"id": "inference-service-running-health-smoke", "status": "passed", "status_value": "running", "probe": "clusterip"})

        stop_status, _ = live.request(
            "POST",
            f"{base}/api/v1/svc/inference-services/{service_id}/lifecycle",
            tenant_id,
            {"idempotency_key": str(uuid.uuid4()), "action": "stop"},
            token=tenant_token,
        )
        live.expect(stop_status, 202, "inference-service-stop")
        live.wait_service(base, tenant_id, service_id, "stopped", timeout=180, kubeconfig=kubeconfig, token=tenant_token)
        checks.append({"id": "inference-service-stop", "status": "passed", "status_value": "stopped"})

        start_status, _ = live.request(
            "POST",
            f"{base}/api/v1/svc/inference-services/{service_id}/lifecycle",
            tenant_id,
            {"idempotency_key": str(uuid.uuid4()), "action": "start"},
            token=tenant_token,
        )
        live.expect(start_status, 202, "inference-service-start")
        started = live.wait_service(
            base, tenant_id, service_id, "running", timeout=300, kubeconfig=kubeconfig, token=tenant_token
        )
        if started.get("id") != service_id:
            fail("start did not reuse the same inference service id")
        checks.append({"id": "inference-service-start", "status": "passed", "same_service_id": True})

        delete_status, _ = live.request(
            "DELETE",
            f"{base}/api/v1/svc/inference-services/{service_id}",
            tenant_id,
            token=tenant_token,
        )
        live.expect(delete_status, 202, "inference-service-delete")
        deadline = time.time() + 180
        while time.time() < deadline:
            gone_status, _ = live.request(
                "GET",
                f"{base}/api/v1/svc/inference-services/{service_id}",
                tenant_id,
                token=tenant_token,
            )
            leftover = live.run(["kubectl", "--kubeconfig", kubeconfig, "-n", namespace, "get", "deploy,svc,netpol", deploy_name])
            if gone_status == 404 and leftover.returncode != 0:
                break
            time.sleep(3)
        else:
            fail("delete did not remove the inference service and runtime")
        checks.append({"id": "inference-service-delete", "status": "passed", "get_status": 404})

        if live.gpu_allocatable(kubeconfig) != 0:
            fail("cluster unexpectedly advertised nvidia.com/gpu; this batch must skip GPU live")
        checks.append({"id": "gpu-live-skipped-no-device-plugin", "status": "passed", "reason": "skipped_no_device_plugin"})
        if not live.smoke_ready(kubeconfig):
            fail("independent vLLM CPU smoke workload was disturbed")
        checks.append({"id": "smoke-workload-untouched", "status": "passed", "namespace": live.SMOKE_NS})

        evidence = {
            "profile": PROFILE,
            "status": "passed",
            "generated_at": dt.datetime.now(dt.timezone.utc).strftime("%Y-%m-%dT%H:%M:%SZ"),
            "gateway": "ani-system/ani-gateway",
            "engine": "vllm-cpu",
            "image": "digest-pinned-vllm-cpu",
            "namespace_kind": "ani-tenant-{uuid}",
            "model_mount": "pvc-snapshot-restore",
            "probe": "clusterip",
            "gpu_live": "skipped_no_device_plugin",
            "auth_mode": "auth_service",
            "checks": checks,
        }
        live.assert_clean_evidence(evidence)
        EVIDENCE.parent.mkdir(parents=True, exist_ok=True)
        EVIDENCE.write_text(json.dumps(evidence, indent=2) + "\n", encoding="utf-8")
        print("inference incluster e2e live gate passed")
        print(f"evidence {EVIDENCE.relative_to(ROOT)}")
        print("production ani-gateway was rolled; GPU/LWS remain skipped")
        rolled_gateway = False
        rolled_auth = False
    finally:
        tenant_token = ""
        if forward is not None and forward.poll() is None:
            forward.send_signal(signal.SIGTERM)
            try:
                forward.wait(timeout=5)
            except subprocess.TimeoutExpired:
                forward.kill()
        if rolled_gateway:
            restore_gateway(kubeconfig, previous_image)
        if rolled_auth:
            restore_auth(kubeconfig, previous_auth_image)
        if created_ns:
            live.run(["kubectl", "--kubeconfig", kubeconfig, "delete", "ns", namespace, "--wait=false"])
        live.postgres_exec(kubeconfig, f"DELETE FROM tenants WHERE id='{tenant_id}';")
        if src_snapshot:
            live.run(["kubectl", "--kubeconfig", kubeconfig, "-n", live.SMOKE_NS, "delete", "volumesnapshot", src_snapshot, "--wait=false", "--ignore-not-found"])
        if dest_vsc:
            live.run(["kubectl", "--kubeconfig", kubeconfig, "delete", "volumesnapshotcontent", dest_vsc, "--wait=false", "--ignore-not-found"])
        shutil.rmtree(tmpdir, ignore_errors=True)


if __name__ == "__main__":
    try:
        main()
    except KeyboardInterrupt:
        sys.exit(130)
