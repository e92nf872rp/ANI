#!/usr/bin/env python3
"""Run the ClusterIP/NetworkPolicy live gate against an approved lab cluster.

This starts a local lab Gateway and inference-service. It does not register
product /test, does not roll out in-cluster ani-gateway, and must not touch
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
import uuid
from pathlib import Path
from typing import Any

import yaml

sys.path.insert(0, str(Path(__file__).resolve().parent))
import run_inference_cpu_vllm_live_gate as live

ROOT = live.ROOT
EVIDENCE = ROOT / "development-records/live-evidence/inference-clusterip-networkpolicy-live-20260815.json"
PROFILE = "INFERENCE-SERVICE-CLUSTERIP-NP-LIVE-GATE-C17"
PROBE_NS = "ani-inf-c17-probe"
PROBE_POD = "ani-np-probe"


def fail(message: str) -> None:
    raise SystemExit(f"inference clusterip networkpolicy live gate failed: {message}")


def quarantine_leftover_c17(kubeconfig: str) -> None:
    leftover = live.postgres_exec(
        kubeconfig,
        "SELECT COALESCE(string_agg(id::text, ','), '') FROM tenants WHERE name LIKE 'inf-c17-lab%';",
    ).strip()
    live.apply_sql(
        kubeconfig,
        """
UPDATE inference_operations
SET state = 'failed',
    error_code = 'LAB_SUPERSEDED',
    error_message = 'superseded by a later C17 lab run',
    lease_owner = NULL,
    lease_until = NULL,
    lease_token = NULL,
    completed_at = NOW(),
    updated_at = NOW()
WHERE state IN ('pending', 'running')
  AND tenant_id IN (SELECT id FROM tenants WHERE name LIKE 'inf-c17-lab%');
""",
        "quarantine leftover C17 operations",
    )
    leftover_ids = {item.strip() for item in leftover.split(",") if item.strip()}
    namespaces = live.run(["kubectl", "--kubeconfig", kubeconfig, "get", "ns", "-o", "name"])
    for line in namespaces.stdout.splitlines():
        name = line.rsplit("/", 1)[-1].strip()
        if name == PROBE_NS or (name.startswith("ani-tenant-") and name.removeprefix("ani-tenant-") in leftover_ids):
            live.run(["kubectl", "--kubeconfig", kubeconfig, "delete", "ns", name, "--wait=false"])
    live.postgres_exec(kubeconfig, "DELETE FROM tenants WHERE name LIKE 'inf-c17-lab%';")


def assert_clusterip_only(kubeconfig: str, namespace: str, name: str) -> None:
    service = live.kubectl_json(kubeconfig, ["-n", namespace, "get", "svc", name])
    spec = service.get("spec") or {}
    if spec.get("type") != "ClusterIP":
        fail("runtime service is not ClusterIP")
    if spec.get("externalIPs") or spec.get("loadBalancerIP") or spec.get("externalName"):
        fail("runtime service exposed an external address")
    for port in spec.get("ports") or []:
        if not isinstance(port, dict):
            continue
        if port.get("nodePort"):
            fail("runtime service allocated a nodePort")
    ingress = live.run(["kubectl", "--kubeconfig", kubeconfig, "-n", namespace, "get", "ingress", "-o", "json"])
    if ingress.returncode == 0:
        document = json.loads(ingress.stdout or '{"items":[]}')
        if document.get("items"):
            fail("tenant namespace has an Ingress")


def assert_networkpolicy(kubeconfig: str, namespace: str, name: str) -> None:
    policy = live.kubectl_json(kubeconfig, ["-n", namespace, "get", "netpol", name])
    spec = policy.get("spec") or {}
    types = spec.get("policyTypes") or []
    if "Ingress" not in types:
        fail("network policy is missing Ingress")
    raw = json.dumps(spec)
    if "0.0.0.0/0" in raw:
        fail("network policy opened a public ingress")
    ingress = spec.get("ingress") or []
    if not ingress:
        fail("network policy has no ingress allow list")
    blob = json.dumps(ingress)
    if "kube-system" not in blob or "ani-system" not in blob:
        fail("network policy missing control-plane allow list")
    if '"podSelector": {}' not in blob and '"podSelector":{}' not in blob:
        fail("network policy missing same-namespace allow")
    for rule in ingress:
        if not isinstance(rule, dict):
            continue
        for peer in rule.get("from") or []:
            if not isinstance(peer, dict):
                continue
            block = peer.get("ipBlock") or {}
            cidr = str(block.get("cidr") or "")
            if cidr and not cidr.endswith("/32"):
                fail("network policy ipBlock is not a node /32")


def assert_foreign_namespace_denied(kubeconfig: str, image: str, namespace: str, name: str) -> None:
    probe_ns = {
        "apiVersion": "v1",
        "kind": "Namespace",
        "metadata": {"name": PROBE_NS, "labels": {"app.kubernetes.io/part-of": "ani-platform"}},
    }
    completed = live.run(
        ["kubectl", "--kubeconfig", kubeconfig, "apply", "-f", "-"],
        input=yaml.safe_dump(probe_ns),
        timeout=60,
    )
    if completed.returncode != 0:
        fail(f"failed to create probe namespace: {live.redact_text(completed.stderr or completed.stdout)}")
    live.run(["kubectl", "--kubeconfig", kubeconfig, "-n", PROBE_NS, "delete", "pod", PROBE_POD, "--ignore-not-found", "--wait=false"])
    probe_pod = {
        "apiVersion": "v1",
        "kind": "Pod",
        "metadata": {"name": PROBE_POD, "namespace": PROBE_NS, "labels": {"ani.probe": "c17"}},
        "spec": {
            "restartPolicy": "Never",
            "containers": [
                {
                    "name": "probe",
                    "image": image,
                    "imagePullPolicy": "IfNotPresent",
                    "command": ["wget"],
                    "args": [
                        "-q",
                        "--timeout=5",
                        "--tries=1",
                        "-O",
                        "/dev/null",
                        f"http://{name}.{namespace}.svc:8000/health",
                    ],
                }
            ],
        },
    }
    created = live.run(
        ["kubectl", "--kubeconfig", kubeconfig, "apply", "-f", "-"],
        input=yaml.safe_dump(probe_pod),
        timeout=60,
    )
    if created.returncode != 0:
        fail(f"failed to start foreign-namespace probe: {live.redact_text(created.stderr or created.stdout)}")
    deadline = time.time() + 90
    phase = ""
    exit_code: int | None = None
    while time.time() < deadline:
        pod = live.run(["kubectl", "--kubeconfig", kubeconfig, "-n", PROBE_NS, "get", "pod", PROBE_POD, "-o", "json"])
        if pod.returncode != 0:
            time.sleep(2)
            continue
        document = json.loads(pod.stdout or "{}")
        phase = str((document.get("status") or {}).get("phase") or "")
        statuses = (document.get("status") or {}).get("containerStatuses") or []
        if statuses and isinstance(statuses[0], dict):
            terminated = ((statuses[0].get("state") or {}).get("terminated") or {})
            if "exitCode" in terminated:
                exit_code = int(terminated["exitCode"])
        if phase in {"Succeeded", "Failed"} and exit_code is not None:
            break
        time.sleep(2)
    if phase == "Succeeded" or exit_code == 0:
        fail("foreign namespace reached the ClusterIP health endpoint")
    if phase != "Failed" and exit_code in {None, 0}:
        fail(f"foreign-namespace probe did not fail closed: phase={phase} exit={exit_code}")


def runtime_gone(kubeconfig: str, namespace: str, name: str) -> bool:
    leftover = live.run(["kubectl", "--kubeconfig", kubeconfig, "-n", namespace, "get", "deploy,svc,netpol", name])
    return leftover.returncode != 0


def main() -> None:
    parser = argparse.ArgumentParser()
    parser.add_argument("--kubeconfig", default=os.environ.get("KUBECONFIG", str(Path.home() / ".kube/config")))
    parser.add_argument("--listen", default="127.0.0.1:18083")
    args = parser.parse_args()
    kubeconfig = args.kubeconfig
    listen = args.listen
    if not Path(kubeconfig).exists():
        fail("kubeconfig is missing")

    tenant_id = str(uuid.uuid4())
    tenant_name = "inf-c17-lab-" + tenant_id[:8]
    model_id = str(uuid.uuid4())
    namespace = f"ani-tenant-{tenant_id}"
    sa_name = "ani-inf-c17"
    checks: list[dict[str, Any]] = []
    harness: subprocess.Popen[str] | None = None
    inference: subprocess.Popen[str] | None = None
    forwards: list[subprocess.Popen[str]] = []
    tmpdir = Path(tempfile.mkdtemp(prefix="ani-inf-c17-"))
    created_ns = False
    created_probe_ns = False
    src_snapshot = ""
    dest_vsc = ""
    try:
        if not live.smoke_ready(kubeconfig):
            fail("independent vLLM CPU smoke workload is not ready")
        image = live.discover_image(kubeconfig)
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
                    {"apiGroups": [""], "resources": ["services/proxy"], "verbs": ["get", "create"]},
                    {"apiGroups": [""], "resources": ["pods"], "verbs": ["get", "list"]},
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
        token = live.kubectl(kubeconfig, ["-n", "ani-system", "create", "token", sa_name, "--duration=2h"]).strip()
        if not token:
            fail("failed to mint lab service account token")

        database_url = live.rewrite_url(live.secret_data(kubeconfig, "database_url"), "127.0.0.1", 15434)
        nats_url = live.rewrite_url(live.secret_data(kubeconfig, "nats_url"), "127.0.0.1", 14224)
        redis_url = live.rewrite_url(live.secret_data(kubeconfig, "redis_url"), "127.0.0.1", 16381)
        for spec in (
            (["-n", "ani-system", "port-forward", "pod/ani-postgres-0", "15434:5432"], 15434),
            (["-n", "ani-system", "port-forward", "svc/nats", "14224:4222"], 14224),
            (["-n", "ani-system", "port-forward", "svc/ani-redis", "16381:6379"], 16381),
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
        quarantine_leftover_c17(kubeconfig)
        live.postgres_exec(
            kubeconfig,
            "INSERT INTO tenants (id, name, display_name, status, max_gpu_count, max_cpu_cores, max_memory_gb, settings) "
            f"VALUES ('{tenant_id}', '{tenant_name}', 'inference-clusterip-np-c17', 'active', 0, 8, 16, '{{}}') "
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
            "INFERENCE_SERVICE_GRPC_ADDR": "127.0.0.1:19106",
            "GRPC_PORT": "19106",
            "HEALTH_PORT": "19206",
            "INFERENCE_LAB_CATALOG": "1",
            "INFERENCE_LAB_IMAGE_REF": image,
            "INFERENCE_CPU_IMAGE_REF": image,
            "INFERENCE_LAB_ARTIFACT_REF": "pvc://vllm-model#/models/qwen",
            "INFERENCE_RUNTIME_PROBE_VIA": "kubernetes_proxy",
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
        live.wait_tcp("127.0.0.1", 19106, timeout=60)
        if inference.poll() is not None:
            fail(f"inference-service exited: {live.redact_text(live.proc_log(inference)[-2000:] or 'no output')}")
        harness = live.start_proc([str(harness_bin)], ROOT / "services/ani-gateway", env, tmpdir / "harness.log")
        live.wait_http(f"http://{listen}/readyz", harness, timeout=90)

        base = f"http://{listen}"
        create_status, created = live.request(
            "POST",
            f"{base}/api/v1/svc/inference-services",
            tenant_id,
            {
                "idempotency_key": str(uuid.uuid4()),
                "name": "inf-c17-cpu",
                "model": model_id,
                "model_version_id": model_id,
                "served_model_name": "qwen2.5-0.5b",
                "replicas": 1,
                "resources": {"cpu": "4", "memory": "8Gi"},
            },
        )
        live.expect(create_status, 202, "product-inference-service-cpu-create")
        service_id = str(created.get("id") or "")
        if not service_id:
            fail("create did not return an inference service id")
        checks.append({"id": "product-inference-service-cpu-create", "status": "passed", "http_status": 202})

        deploy_name = live.runtime_resource_name(service_id)
        deadline = time.time() + 180
        while time.time() < deadline:
            found = live.run(["kubectl", "--kubeconfig", kubeconfig, "-n", namespace, "get", "deploy,svc,netpol", deploy_name])
            if found.returncode == 0:
                break
            time.sleep(3)
        else:
            fail("platform-workload deployment, ClusterIP, or NetworkPolicy was not created")
        live.assert_cpu_deployment(kubeconfig, namespace, deploy_name, image)
        assert_clusterip_only(kubeconfig, namespace, deploy_name)
        checks.append({"id": "inference-service-clusterip-only", "status": "passed", "service_type": "ClusterIP"})
        assert_networkpolicy(kubeconfig, namespace, deploy_name)
        checks.append({"id": "inference-service-networkpolicy-applied", "status": "passed", "ingress": "same-ns-and-control-plane"})

        observed = live.wait_service(base, tenant_id, service_id, "running", timeout=900, kubeconfig=kubeconfig)
        if observed.get("invocation_url") is not None or observed.get("endpoint_url") is not None:
            fail("product response leaked an invocation URL")
        checks.append({"id": "inference-service-running-health-smoke", "status": "passed", "status_value": "running"})

        created_probe_ns = True
        assert_foreign_namespace_denied(kubeconfig, image, namespace, deploy_name)
        checks.append({"id": "inference-service-foreign-namespace-denied", "status": "passed", "peer": "foreign-namespace"})

        test_status, _ = live.request("POST", f"{base}/api/v1/svc/inference-services/{service_id}/test", tenant_id, {"prompt": "ping"})
        if test_status != 404:
            fail(f"product /test was reachable: status={test_status}")
        checks.append({"id": "inference-service-no-product-test", "status": "passed", "http_status": 404})

        stop_status, _ = live.request(
            "POST",
            f"{base}/api/v1/svc/inference-services/{service_id}/lifecycle",
            tenant_id,
            {"idempotency_key": str(uuid.uuid4()), "action": "stop"},
        )
        live.expect(stop_status, 202, "inference-service-stop-endpoint-gone")
        stopped = live.wait_service(base, tenant_id, service_id, "stopped", timeout=180, kubeconfig=kubeconfig)
        if stopped.get("invocation_url") is not None or stopped.get("endpoint_url") is not None:
            fail("stopped product response leaked an invocation URL")
        deadline = time.time() + 60
        while time.time() < deadline:
            if runtime_gone(kubeconfig, namespace, deploy_name):
                break
            time.sleep(3)
        else:
            fail("stop left ClusterIP, NetworkPolicy, or Deployment")
        checks.append({"id": "inference-service-stop-endpoint-gone", "status": "passed", "status_value": "stopped"})

        delete_status, _ = live.request("DELETE", f"{base}/api/v1/svc/inference-services/{service_id}", tenant_id)
        live.expect(delete_status, 202, "inference-service-delete")
        deadline = time.time() + 180
        while time.time() < deadline:
            gone_status, _ = live.request("GET", f"{base}/api/v1/svc/inference-services/{service_id}", tenant_id)
            if gone_status == 404 and runtime_gone(kubeconfig, namespace, deploy_name):
                break
            time.sleep(3)
        else:
            fail("delete did not remove the inference service and runtime")
        checks.append({"id": "inference-service-delete", "status": "passed", "get_status": 404})

        if not live.smoke_ready(kubeconfig):
            fail("independent vLLM CPU smoke workload was disturbed")
        checks.append({"id": "smoke-workload-untouched", "status": "passed", "namespace": live.SMOKE_NS})

        evidence = {
            "profile": PROFILE,
            "status": "passed",
            "generated_at": dt.datetime.now(dt.timezone.utc).strftime("%Y-%m-%dT%H:%M:%SZ"),
            "gateway": "lab-process-not-in-cluster-ani-gateway",
            "exposure": "clusterip_networkpolicy",
            "product_test": "not_registered",
            "probe": "kubernetes_service_proxy",
            "auth_mode": "dev",
            "checks": checks,
        }
        live.assert_clean_evidence(evidence)
        EVIDENCE.parent.mkdir(parents=True, exist_ok=True)
        EVIDENCE.write_text(json.dumps(evidence, indent=2) + "\n", encoding="utf-8")
        print("inference clusterip networkpolicy live gate passed")
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
        if src_snapshot:
            live.run(["kubectl", "--kubeconfig", kubeconfig, "-n", live.SMOKE_NS, "delete", "volumesnapshot", src_snapshot, "--wait=false", "--ignore-not-found"])
        if dest_vsc:
            live.run(["kubectl", "--kubeconfig", kubeconfig, "delete", "volumesnapshotcontent", dest_vsc, "--wait=false", "--ignore-not-found"])
        if created_probe_ns:
            live.run(["kubectl", "--kubeconfig", kubeconfig, "delete", "ns", PROBE_NS, "--wait=false"])
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
