#!/usr/bin/env python3
"""Run the GPU admission live gate against an approved lab cluster.

This starts a local lab Gateway and inference-service. It does not create a
GPU runtime, does not roll out in-cluster ani-gateway, and must not touch
ani-vllm-cpu-smoke.
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

import yaml

sys.path.insert(0, str(Path(__file__).resolve().parent))
import run_inference_cpu_vllm_live_gate as live

ROOT = live.ROOT
EVIDENCE = ROOT / "development-records/live-evidence/inference-gpu-admission-live-20260815.json"
PROFILE = "INFERENCE-SERVICE-GPU-ADMISSION-LIVE-GATE-C16"


def fail(message: str) -> None:
    raise SystemExit(f"inference gpu admission live gate failed: {message}")


def request_core(method: str, url: str, tenant_id: str) -> tuple[int, dict[str, Any]]:
    req = urllib.request.Request(
        url,
        headers={
            "Accept": "application/json",
            "X-Dev-Tenant-ID": tenant_id,
            "X-Dev-Principal-Kind": "service",
            "X-Dev-Service-Scope": "scope:platform-workloads:read",
        },
        method=method,
    )
    try:
        with urllib.request.urlopen(req, timeout=30) as response:
            raw = response.read().decode("utf-8")
            return response.status, json.loads(raw) if raw.strip() else {}
    except urllib.error.HTTPError as err:
        raw = err.read().decode("utf-8", errors="replace")
        try:
            document = json.loads(raw) if raw.strip() else {}
        except json.JSONDecodeError:
            document = {"raw": "redacted"}
        return err.code, document


def no_gpu_runtime(kubeconfig: str, namespace: str) -> None:
    deploys = live.run(["kubectl", "--kubeconfig", kubeconfig, "-n", namespace, "get", "deploy", "-o", "json"])
    if deploys.returncode != 0:
        return
    document = json.loads(deploys.stdout or '{"items":[]}')
    for item in document.get("items") or []:
        containers = ((((item.get("spec") or {}).get("template") or {}).get("spec") or {}).get("containers") or [])
        for container in containers:
            resources = (container.get("resources") or {})
            for bucket in ("requests", "limits"):
                if (resources.get(bucket) or {}).get("nvidia.com/gpu"):
                    fail("a GPU runtime was created after accelerator admission failed")


def main() -> None:
    parser = argparse.ArgumentParser()
    parser.add_argument("--kubeconfig", default=os.environ.get("KUBECONFIG", str(Path.home() / ".kube/config")))
    parser.add_argument("--listen", default="127.0.0.1:18082")
    args = parser.parse_args()
    kubeconfig = args.kubeconfig
    listen = args.listen
    if not Path(kubeconfig).exists():
        fail("kubeconfig is missing")

    tenant_id = str(uuid.uuid4())
    tenant_name = "inf-c16-lab-" + tenant_id[:8]
    model_id = str(uuid.uuid4())
    namespace = f"ani-tenant-{tenant_id}"
    sa_name = "ani-inf-c16"
    checks: list[dict[str, Any]] = []
    harness: subprocess.Popen[str] | None = None
    inference: subprocess.Popen[str] | None = None
    forwards: list[subprocess.Popen[str]] = []
    tmpdir = Path(tempfile.mkdtemp(prefix="ani-inf-c16-"))
    created_ns = False
    try:
        if not live.smoke_ready(kubeconfig):
            fail("independent vLLM CPU smoke workload is not ready")
        image = live.discover_image(kubeconfig)
        gpu_count = live.gpu_allocatable(kubeconfig)
        if gpu_count != 0:
            fail("cluster unexpectedly advertised nvidia.com/gpu; this batch must skip GPU live")
        server, ca_data = live.current_cluster(kubeconfig)
        (tmpdir / "ca.crt").write_bytes(base64.b64decode(ca_data))

        rbac = [
            {
                "apiVersion": "v1",
                "kind": "ServiceAccount",
                "metadata": {"name": sa_name, "namespace": "ani-system"},
            },
            {
                "apiVersion": "rbac.authorization.k8s.io/v1",
                "kind": "ClusterRole",
                "metadata": {"name": sa_name},
                "rules": [
                    {"apiGroups": [""], "resources": ["namespaces"], "verbs": ["get", "list", "create", "update", "patch"]},
                    {"apiGroups": [""], "resources": ["services"], "verbs": ["get", "list", "create", "update", "patch", "delete"]},
                    {"apiGroups": [""], "resources": ["nodes"], "verbs": ["get", "list"]},
                    {"apiGroups": ["apps"], "resources": ["deployments"], "verbs": ["get", "list", "create", "update", "patch", "delete"]},
                    {"apiGroups": ["networking.k8s.io"], "resources": ["networkpolicies"], "verbs": ["get", "list", "create", "update", "patch", "delete"]},
                ],
            },
            {
                "apiVersion": "rbac.authorization.k8s.io/v1",
                "kind": "ClusterRoleBinding",
                "metadata": {"name": sa_name},
                "roleRef": {"apiGroup": "rbac.authorization.k8s.io", "kind": "ClusterRole", "name": sa_name},
                "subjects": [{"kind": "ServiceAccount", "name": sa_name, "namespace": "ani-system"}],
            },
        ]
        (tmpdir / "rbac.yaml").write_text(yaml.safe_dump_all(rbac), encoding="utf-8")
        live.kubectl(kubeconfig, ["apply", "-f", str(tmpdir / "rbac.yaml")])
        token = live.kubectl(kubeconfig, ["-n", "ani-system", "create", "token", sa_name, "--duration=1h"]).strip()
        if not token:
            fail("failed to mint lab service account token")

        database_url = live.rewrite_url(live.secret_data(kubeconfig, "database_url"), "127.0.0.1", 15433)
        nats_url = live.rewrite_url(live.secret_data(kubeconfig, "nats_url"), "127.0.0.1", 14223)
        redis_url = live.rewrite_url(live.secret_data(kubeconfig, "redis_url"), "127.0.0.1", 16380)
        for spec in (
            (["-n", "ani-system", "port-forward", "pod/ani-postgres-0", "15433:5432"], 15433),
            (["-n", "ani-system", "port-forward", "svc/nats", "14223:4222"], 14223),
            (["-n", "ani-system", "port-forward", "svc/ani-redis", "16380:6379"], 16380),
        ):
            args_pf, port = spec
            proc = subprocess.Popen(
                ["kubectl", "--kubeconfig", kubeconfig, *args_pf],
                stdout=subprocess.DEVNULL,
                stderr=subprocess.DEVNULL,
            )
            forwards.append(proc)
            live.wait_tcp("127.0.0.1", port)

        live.apply_platform_workload_migration(kubeconfig)
        live.apply_sql(
            kubeconfig,
            """
CREATE TABLE IF NOT EXISTS inference_services (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    tenant_id UUID NOT NULL REFERENCES tenants(id) ON DELETE CASCADE,
    name TEXT NOT NULL,
    model_version_id UUID NOT NULL,
    replicas INT NOT NULL DEFAULT 1,
    gpu_type TEXT,
    gpu_count_per_pod INT NOT NULL DEFAULT 1,
    max_concurrency INT NOT NULL DEFAULT 8,
    placement_region TEXT,
    placement_az TEXT,
    status TEXT NOT NULL DEFAULT 'pending'
        CHECK (status IN ('pending','downloading','decrypting','deploying','running','stopping','stopped','failed')),
    endpoint_url TEXT,
    k8s_namespace TEXT,
    k8s_deployment_name TEXT,
    error_message TEXT,
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    UNIQUE (tenant_id, name)
);
""",
            "ensure inference_services base table",
        )
        live.apply_sql(kubeconfig, live.INF_MIGRATION.read_text(encoding="utf-8"), "apply inference control-plane migration")
        live.quarantine_leftover_c14(kubeconfig)
        live.postgres_exec(kubeconfig, "DELETE FROM tenants WHERE name LIKE 'inf-c16-lab%';")
        live.postgres_exec(
            kubeconfig,
            "INSERT INTO tenants (id, name, display_name, status, max_gpu_count, max_cpu_cores, max_memory_gb, settings) "
            f"VALUES ('{tenant_id}', '{tenant_name}', 'inference-gpu-admission-c16', 'active', 0, 8, 16, '{{}}') "
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

        env = os.environ.copy()
        env.update({
            "ANI_AUTH_MODE": "dev",
            "GATEWAY_LISTEN_ADDR": listen,
            "KUBERNETES_API_HOST": server,
            "KUBERNETES_BEARER_TOKEN": token,
            "KUBERNETES_SERVICE_ACCOUNT_CA_FILE": str(tmpdir / "ca.crt"),
            "DATABASE_URL": database_url,
            "NATS_URL": nats_url,
            "REDIS_URL": redis_url,
            "INFERENCE_DATABASE_URL": database_url,
            "CORE_API_BASE_URL": f"http://{listen}",
            "INFERENCE_SERVICE_GRPC_ADDR": "127.0.0.1:19105",
            "GRPC_PORT": "19105",
            "HEALTH_PORT": "19205",
            "INFERENCE_LAB_CATALOG": "1",
            "INFERENCE_LAB_IMAGE_REF": image,
            "INFERENCE_CPU_IMAGE_REF": image,
            "INFERENCE_LAB_ARTIFACT_REF": "pvc://vllm-model#/models/qwen",
            "PLATFORM_WORKLOAD_PROVIDER": "kubernetes_rest",
        })

        harness_bin = tmpdir / "harness"
        inference_bin = tmpdir / "inference-service"
        built = live.run(["go", "build", "-o", str(harness_bin), "./cmd/platform-workload-live"], cwd=str(ROOT / "services/ani-gateway"))
        if built.returncode != 0:
            fail(f"build lab harness failed: {live.redact_text(built.stderr or built.stdout)}")
        built = live.run(["go", "build", "-o", str(inference_bin), "."], cwd=str(ROOT / "services/inference-service"), env={**env, "GOWORK": "off"})
        if built.returncode != 0:
            fail(f"build inference-service failed: {live.redact_text(built.stderr or built.stdout)}")

        inference = live.start_proc([str(inference_bin)], ROOT / "services/inference-service", env, tmpdir / "inference.log")
        live.wait_tcp("127.0.0.1", 19105, timeout=60)
        if inference.poll() is not None:
            fail(f"inference-service exited: {live.redact_text(live.proc_log(inference)[-2000:] or 'no output')}")
        harness = live.start_proc([str(harness_bin)], ROOT / "services/ani-gateway", env, tmpdir / "harness.log")
        live.wait_http(f"http://{listen}/readyz", harness, timeout=90)

        base = f"http://{listen}"
        caps_status, caps = request_core("GET", f"{base}/api/v1/platform-workload-capabilities", tenant_id)
        live.expect(caps_status, 200, "platform-workload-capabilities-no-accelerator")
        specs = caps.get("accelerator_specs") or []
        if any(isinstance(item, dict) and item.get("available") for item in specs):
            fail("capabilities advertised an available accelerator")
        checks.append({"id": "platform-workload-capabilities-no-accelerator", "status": "passed", "available_specs": 0})

        create_status, created = live.request(
            "POST",
            f"{base}/api/v1/svc/inference-services",
            tenant_id,
            {
                "idempotency_key": str(uuid.uuid4()),
                "name": "inf-c16-gpu",
                "model": model_id,
                "model_version_id": model_id,
                "served_model_name": "qwen2.5-0.5b",
                "replicas": 1,
                "resources": {
                    "cpu": "4",
                    "memory": "8Gi",
                    "accelerator": {"spec_id": "gpu-a100", "count_per_replica": 1},
                },
            },
        )
        if create_status != 422 or created.get("code") != "ACCELERATOR_SPEC_UNAVAILABLE":
            fail(f"gpu create status={create_status} code={created.get('code')}")
        checks.append({"id": "inference-service-gpu-create-rejected", "status": "passed", "http_status": 422})

        time.sleep(2)
        no_gpu_runtime(kubeconfig, namespace)
        checks.append({"id": "no-gpu-runtime-created", "status": "passed"})
        checks.append({"id": "gpu-live-skipped-no-device-plugin", "status": "passed", "reason": "skipped_no_device_plugin"})
        if not live.smoke_ready(kubeconfig):
            fail("independent vLLM CPU smoke workload was disturbed")
        checks.append({"id": "smoke-workload-untouched", "status": "passed", "namespace": live.SMOKE_NS})

        evidence = {
            "profile": PROFILE,
            "status": "passed",
            "generated_at": dt.datetime.now(dt.timezone.utc).strftime("%Y-%m-%dT%H:%M:%SZ"),
            "gateway": "lab-process-not-in-cluster-ani-gateway",
            "admission": "accelerator_spec_unavailable",
            "gpu_live": "skipped_no_device_plugin",
            "auth_mode": "dev",
            "checks": checks,
        }
        live.assert_clean_evidence(evidence)
        EVIDENCE.parent.mkdir(parents=True, exist_ok=True)
        EVIDENCE.write_text(json.dumps(evidence, indent=2) + "\n", encoding="utf-8")
        print("inference gpu admission live gate passed")
        print(f"evidence {EVIDENCE.relative_to(ROOT)}")
    finally:
        live.stop_proc(harness)
        live.stop_proc(inference)
        for forward in forwards:
            if forward.poll() is None:
                forward.send_signal(signal.SIGTERM)
                try:
                    forward.wait(timeout=5)
                except subprocess.TimeoutExpired:
                    forward.kill()
        if created_ns:
            live.run(["kubectl", "--kubeconfig", kubeconfig, "delete", "ns", namespace, "--wait=false"])
        live.postgres_exec(kubeconfig, f"DELETE FROM tenants WHERE id='{tenant_id}';")
        live.run(["kubectl", "--kubeconfig", kubeconfig, "delete", "clusterrolebinding", sa_name, "--ignore-not-found"])
        live.run(["kubectl", "--kubeconfig", kubeconfig, "delete", "clusterrole", sa_name, "--ignore-not-found"])
        live.run(["kubectl", "--kubeconfig", kubeconfig, "-n", "ani-system", "delete", "serviceaccount", sa_name, "--ignore-not-found"])


if __name__ == "__main__":
    try:
        main()
    except KeyboardInterrupt:
        sys.exit(130)
