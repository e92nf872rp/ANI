#!/usr/bin/env python3
"""Run the CPU vLLM InferenceService live gate against an approved lab cluster.

This starts a local lab Gateway harness and a local inference-service process.
It does not roll out the in-cluster ani-gateway Deployment and must not touch
ani-vllm-cpu-smoke.
"""

from __future__ import annotations

import argparse
import base64
import datetime as dt
import json
import os
import re
import signal
import socket
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

import yaml

ROOT = Path(__file__).resolve().parents[1]
GATE = ROOT / "deploy/real-k8s-lab/inference-cpu-vllm-live-gate.yaml"
PW_MIGRATION = ROOT / "deploy/migrations/20260815_001_platform_workloads.sql"
INF_MIGRATION = ROOT / "deploy/migrations/20260814_001_inference_control_plane.sql"
EVIDENCE = ROOT / "development-records/live-evidence/inference-cpu-vllm-live-20260815.json"
OPS_EVIDENCE = ROOT / "development-records/live-evidence/inference-cpu-vllm-ops-live-20260815.json"
PROFILE = "INFERENCE-SERVICE-CPU-VLLM-LIVE-GATE-C14"
OPS_PROFILE = "INFERENCE-SERVICE-CPU-VLLM-OPS-LIVE-GATE-C15"
SMOKE_NS = "ani-vllm-cpu-smoke"
SMOKE_DEPLOY = "vllm-cpu"
SMOKE_PVC = "vllm-model-cache"
SNAPCLASS = "csi-rbdplugin-snapclass"
IMAGE_FALLBACK = (
    "docker.changqingyun.cn/mirror/vllm-openai-cpu@sha256:"
    "4c697ae650ebeb3a41f3c9c7020913d4c84d2729dc428ce39d60ca353975a4ce"
)
IPV4_RE = re.compile(r"\b\d{1,3}(?:\.\d{1,3}){3}\b")
TOKENISH_RE = re.compile(r"eyJ[A-Za-z0-9_-]{10,}\.[A-Za-z0-9_-]{10,}\.[A-Za-z0-9_-]{10,}")


def fail(message: str) -> None:
    raise SystemExit(f"inference cpu vllm live gate failed: {message}")


def run(cmd: list[str], **kwargs: Any) -> subprocess.CompletedProcess[str]:
    return subprocess.run(cmd, text=True, capture_output=True, check=False, **kwargs)


def kubectl(kubeconfig: str, args: list[str], timeout: int = 60) -> str:
    if SMOKE_NS in args and any(item == "delete" for item in args):
        fail("refusing to delete the independent vLLM CPU smoke workload")
    completed = run(["kubectl", "--kubeconfig", kubeconfig, *args], timeout=timeout)
    if completed.returncode != 0:
        fail(f"kubectl {' '.join(args)} failed: {completed.stderr.strip() or completed.stdout.strip()}")
    return completed.stdout


def kubectl_json(kubeconfig: str, args: list[str], timeout: int = 60) -> Any:
    return json.loads(kubectl(kubeconfig, [*args, "-o", "json"], timeout=timeout))


def request(
    method: str,
    url: str,
    tenant_id: str,
    body: dict[str, Any] | None = None,
    idempotency_key: str = "",
) -> tuple[int, dict[str, Any]]:
    data = None
    headers = {
        "Accept": "application/json",
        "X-Dev-Tenant-ID": tenant_id,
    }
    if body is not None:
        data = json.dumps(body).encode("utf-8")
        headers["Content-Type"] = "application/json"
    if idempotency_key:
        headers["Idempotency-Key"] = idempotency_key
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


def expect(status: int, wanted: int, label: str) -> None:
    if status != wanted:
        fail(f"{label}: status={status}, want={wanted}")


def current_cluster(kubeconfig: str) -> tuple[str, str]:
    document = yaml.safe_load(Path(kubeconfig).read_text(encoding="utf-8"))
    context_name = document.get("current-context")
    cluster_name = ""
    for item in document.get("contexts", []):
        if item.get("name") == context_name:
            cluster_name = (item.get("context") or {}).get("cluster", "")
            break
    for item in document.get("clusters", []):
        if item.get("name") == cluster_name:
            cluster = item.get("cluster") or {}
            server = str(cluster.get("server") or "")
            ca_data = str(cluster.get("certificate-authority-data") or "")
            if not server or not ca_data:
                fail("kubeconfig cluster is missing server or certificate-authority-data")
            return server, ca_data
    fail("kubeconfig current context cluster not found")
    return "", ""


def rewrite_url(url: str, host: str, port: int) -> str:
    parsed = urllib.parse.urlparse(url)
    userinfo = parsed.netloc.rsplit("@", 1)
    auth = userinfo[0] if len(userinfo) == 2 else ""
    netloc = f"{auth}@{host}:{port}" if auth else f"{host}:{port}"
    return urllib.parse.urlunparse(parsed._replace(netloc=netloc))


def postgres_exec(kubeconfig: str, sql: str) -> str:
    return kubectl(
        kubeconfig,
        ["-n", "ani-system", "exec", "-i", "ani-postgres-0", "-c", "postgres", "--", "psql", "-U", "ani", "-d", "ani", "-v", "ON_ERROR_STOP=1", "-tAc", sql],
        timeout=120,
    )


def apply_sql(kubeconfig: str, sql: str, label: str) -> None:
    completed = run(
        [
            "kubectl", "--kubeconfig", kubeconfig, "-n", "ani-system", "exec", "-i",
            "ani-postgres-0", "-c", "postgres", "--",
            "psql", "-U", "ani", "-d", "ani", "-v", "ON_ERROR_STOP=1",
        ],
        input=sql,
        timeout=120,
    )
    if completed.returncode != 0:
        fail(f"{label} failed: {completed.stderr.strip() or completed.stdout.strip()}")


def apply_platform_workload_migration(kubeconfig: str) -> None:
    sql = PW_MIGRATION.read_text(encoding="utf-8").replace(
        "GRANT SELECT, INSERT, UPDATE, DELETE ON\n    platform_workloads,\n    platform_workload_intents\nTO ani_app;",
        """DO $$ BEGIN
  IF EXISTS (SELECT 1 FROM pg_roles WHERE rolname = 'ani_app') THEN
    GRANT SELECT, INSERT, UPDATE, DELETE ON platform_workloads, platform_workload_intents TO ani_app;
  END IF;
END $$;""",
    )
    apply_sql(kubeconfig, sql, "apply platform_workloads migration")


def wait_tcp(host: str, port: int, timeout: int = 20) -> None:
    deadline = time.time() + timeout
    while time.time() < deadline:
        try:
            with socket.create_connection((host, port), timeout=1):
                return
        except OSError:
            time.sleep(0.3)
    fail(f"port-forward {host}:{port} did not become ready")


def redact_text(value: str) -> str:
    value = TOKENISH_RE.sub("[redacted-token]", value)
    value = IPV4_RE.sub("[redacted-ip]", value)
    value = re.sub(r"postgres(?:ql)?://\S+", "postgres://[redacted]", value)
    value = re.sub(r"nats://\S+", "nats://[redacted]", value)
    value = re.sub(r"redis://\S+", "redis://[redacted]", value)
    return value


def proc_log(proc: subprocess.Popen[str] | None) -> str:
    path = getattr(proc, "log_path", None) if proc is not None else None
    if not path:
        return ""
    try:
        return Path(path).read_text(encoding="utf-8", errors="replace")
    except OSError:
        return ""


def start_proc(cmd: list[str], cwd: Path, env: dict[str, str], log_path: Path) -> subprocess.Popen[str]:
    log_file = log_path.open("w", encoding="utf-8", buffering=1)
    proc = subprocess.Popen(cmd, cwd=str(cwd), env=env, stdout=log_file, stderr=subprocess.STDOUT, text=True)
    proc.log_file = log_file  # type: ignore[attr-defined]
    proc.log_path = log_path  # type: ignore[attr-defined]
    return proc


def stop_proc(proc: subprocess.Popen[str] | None) -> None:
    if proc is None:
        return
    log_file = getattr(proc, "log_file", None)
    if proc.poll() is None:
        proc.send_signal(signal.SIGTERM)
        try:
            proc.wait(timeout=15)
        except subprocess.TimeoutExpired:
            proc.kill()
    if log_file is not None:
        log_file.close()


def wait_http(url: str, proc: subprocess.Popen[str] | None = None, timeout: int = 90) -> None:
    deadline = time.time() + timeout
    while time.time() < deadline:
        if proc is not None and proc.poll() is not None:
            fail(f"process exited {proc.returncode}: {redact_text(proc_log(proc)[-2000:] or 'no output')}")
        try:
            with urllib.request.urlopen(url, timeout=2) as response:
                if response.status == 200:
                    return
        except (urllib.error.URLError, TimeoutError):
            time.sleep(0.5)
    fail(f"endpoint did not become ready at {url}: {redact_text(proc_log(proc)[-2000:] or 'no output')}")


def wait_service(
    base: str,
    tenant: str,
    service_id: str,
    wanted: str,
    timeout: int = 900,
    kubeconfig: str = "",
) -> dict[str, Any]:
    deadline = time.time() + timeout
    last: dict[str, Any] = {}
    while time.time() < deadline:
        status, body = request("GET", f"{base}/api/v1/svc/inference-services/{service_id}", tenant)
        if status == 200:
            last = body
            if body.get("status") == wanted:
                return body
        time.sleep(5)
    detail = str(last.get("status") or "unknown")
    extra = ""
    if kubeconfig:
        extra = postgres_exec(
            kubeconfig,
            "SELECT COALESCE(s.status,'') || '|' || COALESCE(o.state,'') || '|' || "
            "COALESCE(o.error_code,'') || '|' || COALESCE(o.attempt::text,'') "
            "FROM inference_services s "
            "LEFT JOIN inference_operations o ON o.id = s.current_operation_id "
            f"WHERE s.id = '{service_id}';",
        ).strip()
    fail(f"inference service {service_id} did not reach {wanted}: {detail} {extra}".strip())
    return last


def assert_clean_evidence(document: dict[str, Any]) -> None:
    raw = json.dumps(document, ensure_ascii=True)
    if TOKENISH_RE.search(raw) or "Bearer " in raw or "password" in raw.lower():
        fail("evidence contains forbidden secret material")
    if IPV4_RE.search(raw):
        fail("evidence contains a raw IP")
    lowered = raw.lower()
    if "postgres://" in lowered or "nats://" in lowered or "redis://" in lowered:
        fail("evidence contains a connection string")


def secret_data(kubeconfig: str, key: str) -> str:
    raw = kubectl(
        kubeconfig,
        ["-n", "ani-system", "get", "secret", "ani-services-runtime", "-o", f"jsonpath={{.data.{key}}}"],
    ).strip()
    if not raw:
        fail(f"ani-services-runtime is missing {key}")
    return base64.b64decode(raw).decode("utf-8")


def discover_image(kubeconfig: str) -> str:
    deploy = kubectl_json(kubeconfig, ["-n", SMOKE_NS, "get", "deploy", SMOKE_DEPLOY])
    containers = (((deploy.get("spec") or {}).get("template") or {}).get("spec") or {}).get("containers") or []
    if not containers:
        return IMAGE_FALLBACK
    image = str(containers[0].get("image") or "")
    if "@sha256:" in image:
        return image
    return IMAGE_FALLBACK


def gpu_allocatable(kubeconfig: str) -> int:
    nodes = kubectl_json(kubeconfig, ["get", "nodes"])
    total = 0
    for node in nodes.get("items") or []:
        raw = str((((node.get("status") or {}).get("allocatable") or {}).get("nvidia.com/gpu") or "0"))
        if raw.isdigit():
            total += int(raw)
    return total


def smoke_ready(kubeconfig: str) -> bool:
    deploy = kubectl_json(kubeconfig, ["-n", SMOKE_NS, "get", "deploy", SMOKE_DEPLOY])
    status = deploy.get("status") or {}
    return int(status.get("readyReplicas") or 0) >= 1


def quarantine_leftover_c14(kubeconfig: str) -> None:
    leftover = postgres_exec(
        kubeconfig,
        "SELECT COALESCE(string_agg(id::text, ','), '') FROM tenants WHERE name LIKE 'inf-c14-lab%';",
    ).strip()
    apply_sql(
        kubeconfig,
        """
UPDATE inference_operations
SET state = 'failed',
    error_code = 'LAB_SUPERSEDED',
    error_message = 'superseded by a later C14 lab run',
    lease_owner = NULL,
    lease_until = NULL,
    lease_token = NULL,
    completed_at = NOW(),
    updated_at = NOW()
WHERE state IN ('pending', 'running')
  AND tenant_id IN (SELECT id FROM tenants WHERE name LIKE 'inf-c14-lab%');
""",
        "quarantine leftover C14 operations",
    )
    snaps = run(["kubectl", "--kubeconfig", kubeconfig, "-n", SMOKE_NS, "get", "volumesnapshot", "-o", "name"])
    for line in snaps.stdout.splitlines():
        name = line.rsplit("/", 1)[-1].strip()
        if name.startswith("inf-c14-src-"):
            run(["kubectl", "--kubeconfig", kubeconfig, "-n", SMOKE_NS, "delete", "volumesnapshot", name, "--wait=false", "--ignore-not-found"])
    contents = run(["kubectl", "--kubeconfig", kubeconfig, "get", "volumesnapshotcontent", "-o", "name"])
    for line in contents.stdout.splitlines():
        name = line.rsplit("/", 1)[-1].strip()
        if name.startswith("inf-c14-vsc-"):
            run(["kubectl", "--kubeconfig", kubeconfig, "delete", "volumesnapshotcontent", name, "--wait=false", "--ignore-not-found"])
    namespaces = run(["kubectl", "--kubeconfig", kubeconfig, "get", "ns", "-o", "name"])
    leftover_ids = {item.strip() for item in leftover.split(",") if item.strip()}
    for line in namespaces.stdout.splitlines():
        name = line.rsplit("/", 1)[-1].strip()
        if name.startswith("ani-tenant-") and name.removeprefix("ani-tenant-") in leftover_ids:
            run(["kubectl", "--kubeconfig", kubeconfig, "delete", "ns", name, "--wait=false"])
    postgres_exec(kubeconfig, "DELETE FROM tenants WHERE name LIKE 'inf-c14-lab%';")


def clone_model_pvc(kubeconfig: str, dest_ns: str, tmpdir: Path) -> tuple[str, str]:
    src_name = "inf-c14-src-" + uuid.uuid4().hex[:8]
    vsc_name = "inf-c14-vsc-" + uuid.uuid4().hex[:8]
    source = {
        "apiVersion": "snapshot.storage.k8s.io/v1",
        "kind": "VolumeSnapshot",
        "metadata": {"name": src_name, "namespace": SMOKE_NS},
        "spec": {
            "volumeSnapshotClassName": SNAPCLASS,
            "source": {"persistentVolumeClaimName": SMOKE_PVC},
        },
    }
    (tmpdir / "src-snapshot.yaml").write_text(yaml.safe_dump(source), encoding="utf-8")
    kubectl(kubeconfig, ["apply", "-f", str(tmpdir / "src-snapshot.yaml")])
    deadline = time.time() + 180
    handle = ""
    driver = "rook-ceph.rbd.csi.ceph.com"
    restore = "5Gi"
    while time.time() < deadline:
        snap = kubectl_json(kubeconfig, ["-n", SMOKE_NS, "get", "volumesnapshot", src_name])
        status = snap.get("status") or {}
        if status.get("readyToUse") and status.get("boundVolumeSnapshotContentName"):
            content = kubectl_json(kubeconfig, ["get", "volumesnapshotcontent", status["boundVolumeSnapshotContentName"]])
            handle = str((content.get("status") or {}).get("snapshotHandle") or "")
            driver = str((content.get("spec") or {}).get("driver") or driver)
            restore = str(status.get("restoreSize") or restore)
            if handle:
                break
        time.sleep(3)
    if not handle:
        fail("source model snapshot did not become ready")
    dest = [
        {
            "apiVersion": "snapshot.storage.k8s.io/v1",
            "kind": "VolumeSnapshotContent",
            "metadata": {"name": vsc_name},
            "spec": {
                "deletionPolicy": "Retain",
                "driver": driver,
                "volumeSnapshotClassName": SNAPCLASS,
                "source": {"snapshotHandle": handle},
                "volumeSnapshotRef": {"name": "vllm-model", "namespace": dest_ns},
            },
        },
        {
            "apiVersion": "snapshot.storage.k8s.io/v1",
            "kind": "VolumeSnapshot",
            "metadata": {"name": "vllm-model", "namespace": dest_ns},
            "spec": {"source": {"volumeSnapshotContentName": vsc_name}},
        },
        {
            "apiVersion": "v1",
            "kind": "PersistentVolumeClaim",
            "metadata": {"name": "vllm-model", "namespace": dest_ns},
            "spec": {
                "accessModes": ["ReadWriteOnce"],
                "storageClassName": "ani-rbd-ssd",
                "resources": {"requests": {"storage": restore}},
                "dataSource": {
                    "name": "vllm-model",
                    "kind": "VolumeSnapshot",
                    "apiGroup": "snapshot.storage.k8s.io",
                },
            },
        },
    ]
    (tmpdir / "dest-model.yaml").write_text(yaml.safe_dump_all(dest), encoding="utf-8")
    kubectl(kubeconfig, ["apply", "-f", str(tmpdir / "dest-model.yaml")])
    deadline = time.time() + 180
    while time.time() < deadline:
        snap = kubectl_json(kubeconfig, ["-n", dest_ns, "get", "volumesnapshot", "vllm-model"])
        pvc = kubectl_json(kubeconfig, ["-n", dest_ns, "get", "pvc", "vllm-model"])
        ready = bool((snap.get("status") or {}).get("readyToUse"))
        phase = str((pvc.get("status") or {}).get("phase") or "")
        if ready and phase in {"Pending", "Bound"}:
            return src_name, vsc_name
        time.sleep(3)
    fail("restored model snapshot/PVC did not become ready")
    return src_name, vsc_name


def runtime_resource_name(name: str) -> str:
    name = (name or "").strip().lower()
    if not name:
        return "pw"
    if name[0] < "a" or name[0] > "z":
        return "pw-" + name
    return name


def assert_cpu_deployment(kubeconfig: str, namespace: str, name: str, image: str) -> None:
    deploy = kubectl_json(kubeconfig, ["-n", namespace, "get", "deploy", name])
    labels = (deploy.get("metadata") or {}).get("labels") or {}
    if labels.get("ani.platform_workload") != "inference":
        fail("deployment missing ani.platform_workload=inference")
    if "ani.kubercloud.io/instance" in labels:
        fail("deployment carried an instance identity label")
    pod_spec = (((deploy.get("spec") or {}).get("template") or {}).get("spec") or {})
    containers = pod_spec.get("containers") or []
    if not containers:
        fail("deployment has no container")
    container = containers[0]
    if container.get("image") != image:
        fail("deployment image was not the digest-pinned vLLM CPU image")
    resources = ((container.get("resources") or {}).get("requests") or {})
    if "nvidia.com/gpu" in resources:
        fail("CPU deployment requested nvidia.com/gpu")
    mounts = {item.get("mountPath") for item in (container.get("volumeMounts") or [])}
    if "/models" not in mounts:
        fail("deployment did not mount the model PVC")
    if container.get("livenessProbe"):
        fail("deployment has a liveness probe that can kill model load")


def wait_deploy_replicas(kubeconfig: str, namespace: str, name: str, wanted: int, timeout: int = 120) -> None:
    deadline = time.time() + timeout
    last = 0
    while time.time() < deadline:
        deploy = kubectl_json(kubeconfig, ["-n", namespace, "get", "deploy", name])
        last = int(((deploy.get("spec") or {}).get("replicas") or 0))
        if last == wanted:
            return
        time.sleep(3)
    fail(f"deployment replicas stayed {last}, want {wanted}")


def main() -> None:
    parser = argparse.ArgumentParser()
    parser.add_argument("--kubeconfig", default=os.environ.get("KUBECONFIG", str(Path.home() / ".kube/config")))
    parser.add_argument("--listen", default="127.0.0.1:18081")
    args = parser.parse_args()
    kubeconfig = args.kubeconfig
    listen = args.listen
    if not Path(kubeconfig).exists():
        fail("kubeconfig is missing")
    if not GATE.exists() or not PW_MIGRATION.exists() or not INF_MIGRATION.exists():
        fail("gate or migration files are missing")

    tenant_id = str(uuid.uuid4())
    tenant_name = "inf-c14-lab-" + tenant_id[:8]
    model_id = str(uuid.uuid4())
    namespace = f"ani-tenant-{tenant_id}"
    sa_name = "ani-inf-c14"
    checks: list[dict[str, Any]] = []
    ops_checks: list[dict[str, Any]] = []
    harness: subprocess.Popen[str] | None = None
    inference: subprocess.Popen[str] | None = None
    forwards: list[subprocess.Popen[str]] = []
    tmpdir = Path(tempfile.mkdtemp(prefix="ani-inf-c14-"))
    created_ns = False
    src_snapshot = ""
    dest_vsc = ""
    try:
        if not smoke_ready(kubeconfig):
            fail("independent vLLM CPU smoke workload is not ready")
        image = discover_image(kubeconfig)
        gpu_count = gpu_allocatable(kubeconfig)
        server, ca_data = current_cluster(kubeconfig)
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
                    {"apiGroups": [""], "resources": ["pods/log"], "verbs": ["get"]},
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
        kubectl(kubeconfig, ["apply", "-f", str(tmpdir / "rbac.yaml")])
        token = kubectl(kubeconfig, ["-n", "ani-system", "create", "token", sa_name, "--duration=2h"]).strip()
        if not token:
            fail("failed to mint lab service account token")

        database_url = rewrite_url(secret_data(kubeconfig, "database_url"), "127.0.0.1", 15432)
        nats_url = rewrite_url(secret_data(kubeconfig, "nats_url"), "127.0.0.1", 14222)
        redis_url = rewrite_url(secret_data(kubeconfig, "redis_url"), "127.0.0.1", 16379)
        for spec in (
            (["-n", "ani-system", "port-forward", "pod/ani-postgres-0", "15432:5432"], 15432),
            (["-n", "ani-system", "port-forward", "svc/nats", "14222:4222"], 14222),
            (["-n", "ani-system", "port-forward", "svc/ani-redis", "16379:6379"], 16379),
        ):
            args_pf, port = spec
            proc = subprocess.Popen(
                ["kubectl", "--kubeconfig", kubeconfig, *args_pf],
                stdout=subprocess.DEVNULL,
                stderr=subprocess.DEVNULL,
            )
            forwards.append(proc)
            wait_tcp("127.0.0.1", port)

        apply_platform_workload_migration(kubeconfig)
        apply_sql(
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
        apply_sql(kubeconfig, INF_MIGRATION.read_text(encoding="utf-8"), "apply inference control-plane migration")
        quarantine_leftover_c14(kubeconfig)
        postgres_exec(
            kubeconfig,
            "INSERT INTO tenants (id, name, display_name, status, max_gpu_count, max_cpu_cores, max_memory_gb, settings) "
            f"VALUES ('{tenant_id}', '{tenant_name}', 'inference-cpu-vllm-c14', 'active', 0, 8, 16, '{{}}') "
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
        kubectl(kubeconfig, ["apply", "-f", str(tmpdir / "namespace.yaml")])
        created_ns = True
        src_snapshot, dest_vsc = clone_model_pvc(kubeconfig, namespace, tmpdir)

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
            "INFERENCE_SERVICE_GRPC_ADDR": "127.0.0.1:19104",
            "GRPC_PORT": "19104",
            "HEALTH_PORT": "19204",
            "INFERENCE_LAB_CATALOG": "1",
            "INFERENCE_LAB_IMAGE_REF": image,
            "INFERENCE_CPU_IMAGE_REF": image,
            "INFERENCE_LAB_ARTIFACT_REF": "pvc://vllm-model#/models/qwen",
            "INFERENCE_RUNTIME_PROBE_VIA": "kubernetes_proxy",
            "PLATFORM_WORKLOAD_PROVIDER": "kubernetes_rest",
        })

        harness_bin = tmpdir / "harness"
        inference_bin = tmpdir / "inference-service"
        built = run(["go", "build", "-o", str(harness_bin), "./cmd/platform-workload-live"], cwd=str(ROOT / "services/ani-gateway"))
        if built.returncode != 0:
            fail(f"build lab harness failed: {redact_text(built.stderr or built.stdout)}")
        built = run(["go", "build", "-o", str(inference_bin), "."], cwd=str(ROOT / "services/inference-service"), env={**env, "GOWORK": "off"})
        if built.returncode != 0:
            fail(f"build inference-service failed: {redact_text(built.stderr or built.stdout)}")

        inference = start_proc([str(inference_bin)], ROOT / "services/inference-service", env, tmpdir / "inference.log")
        wait_tcp("127.0.0.1", 19104, timeout=60)
        if inference.poll() is not None:
            fail(f"inference-service exited: {redact_text(proc_log(inference)[-2000:] or 'no output')}")
        harness = start_proc([str(harness_bin)], ROOT / "services/ani-gateway", env, tmpdir / "harness.log")
        wait_http(f"http://{listen}/readyz", harness, timeout=90)

        base = f"http://{listen}"
        create_body = {
            "idempotency_key": str(uuid.uuid4()),
            "name": "inf-c14-cpu",
            "model": model_id,
            "model_version_id": model_id,
            "served_model_name": "qwen2.5-0.5b",
            "replicas": 1,
            "resources": {"cpu": "4", "memory": "8Gi"},
        }
        create_status, created = request("POST", f"{base}/api/v1/svc/inference-services", tenant_id, create_body)
        expect(create_status, 202, "product-inference-service-cpu-create")
        service_id = str(created.get("id") or "")
        if not service_id:
            fail("create did not return an inference service id")
        checks.append({"id": "product-inference-service-cpu-create", "status": "passed", "http_status": 202})

        deploy_name = runtime_resource_name(service_id)
        deadline = time.time() + 180
        while time.time() < deadline:
            found = run(["kubectl", "--kubeconfig", kubeconfig, "-n", namespace, "get", "deploy", deploy_name])
            if found.returncode == 0:
                break
            time.sleep(3)
        else:
            fail("platform-workload deployment was not created")
        assert_cpu_deployment(kubeconfig, namespace, deploy_name, image)
        checks.append({"id": "kubectl-vllm-cpu-deployment", "status": "passed", "image": "digest-pinned-vllm-cpu"})

        observed = wait_service(base, tenant_id, service_id, "running", timeout=900, kubeconfig=kubeconfig)
        if observed.get("invocation_url") is not None or observed.get("endpoint_url") is not None:
            fail("product response leaked an invocation URL")
        if "accelerator" in (observed.get("resources") or {}):
            fail("CPU create projected an accelerator")
        checks.append({"id": "inference-service-running-health-smoke", "status": "passed", "status_value": "running"})

        logs_status, logs = request("GET", f"{base}/api/v1/svc/inference-services/{service_id}/logs?limit=20", tenant_id)
        expect(logs_status, 200, "inference-service-product-logs")
        items = logs.get("items") or []
        if not items:
            fail("product logs were empty after the runtime became running")
        for item in items:
            if not isinstance(item, dict):
                fail("product log item is not an object")
            if item.get("replica"):
                fail("product logs leaked a replica identity")
        ops_checks.append({"id": "inference-service-product-logs", "status": "passed", "item_count": len(items)})

        scale_status, _ = request(
            "PATCH",
            f"{base}/api/v1/svc/inference-services/{service_id}",
            tenant_id,
            {"idempotency_key": str(uuid.uuid4()), "replicas": 2},
        )
        expect(scale_status, 202, "inference-service-scale-desired-replicas")
        wait_deploy_replicas(kubeconfig, namespace, deploy_name, 2)
        ops_checks.append({"id": "inference-service-scale-desired-replicas", "status": "passed", "desired_replicas": 2})

        scale_back_status, _ = request(
            "PATCH",
            f"{base}/api/v1/svc/inference-services/{service_id}",
            tenant_id,
            {"idempotency_key": str(uuid.uuid4()), "replicas": 1},
        )
        expect(scale_back_status, 202, "inference-service-scale-back-running")
        wait_service(base, tenant_id, service_id, "running", timeout=900, kubeconfig=kubeconfig)
        wait_deploy_replicas(kubeconfig, namespace, deploy_name, 1)
        ops_checks.append({"id": "inference-service-scale-back-running", "status": "passed", "desired_replicas": 1})

        stop_proc(harness)
        stop_proc(inference)
        inference = start_proc([str(inference_bin)], ROOT / "services/inference-service", env, tmpdir / "inference-restart.log")
        wait_tcp("127.0.0.1", 19104, timeout=60)
        if inference.poll() is not None:
            fail(f"inference-service restart exited: {redact_text(proc_log(inference)[-2000:] or 'no output')}")
        harness = start_proc([str(harness_bin)], ROOT / "services/ani-gateway", env, tmpdir / "harness-restart.log")
        wait_http(f"http://{listen}/readyz", harness, timeout=90)
        restart_status, restarted = request("GET", f"{base}/api/v1/svc/inference-services/{service_id}", tenant_id)
        expect(restart_status, 200, "inference-service-lab-restart-get")
        if restarted.get("id") != service_id or restarted.get("status") != "running":
            fail("inference service was not restored after lab process restart")
        ops_checks.append({"id": "inference-service-lab-restart-get", "status": "passed", "restart_mode": "lab_process"})

        stop_status, _ = request(
            "POST",
            f"{base}/api/v1/svc/inference-services/{service_id}/lifecycle",
            tenant_id,
            {"idempotency_key": str(uuid.uuid4()), "action": "stop"},
        )
        expect(stop_status, 202, "inference-service-stop")
        wait_service(base, tenant_id, service_id, "stopped", timeout=180, kubeconfig=kubeconfig)
        checks.append({"id": "inference-service-stop", "status": "passed", "status_value": "stopped"})

        start_status, _ = request(
            "POST",
            f"{base}/api/v1/svc/inference-services/{service_id}/lifecycle",
            tenant_id,
            {"idempotency_key": str(uuid.uuid4()), "action": "start"},
        )
        expect(start_status, 202, "inference-service-start")
        started = wait_service(base, tenant_id, service_id, "running", timeout=900, kubeconfig=kubeconfig)
        if started.get("id") != service_id:
            fail("start did not reuse the same inference service id")
        checks.append({"id": "inference-service-start", "status": "passed", "same_service_id": True})

        delete_status, _ = request("DELETE", f"{base}/api/v1/svc/inference-services/{service_id}", tenant_id)
        expect(delete_status, 202, "inference-service-delete")
        deadline = time.time() + 180
        while time.time() < deadline:
            gone_status, _ = request("GET", f"{base}/api/v1/svc/inference-services/{service_id}", tenant_id)
            leftover = run(["kubectl", "--kubeconfig", kubeconfig, "-n", namespace, "get", "deploy,svc,netpol", deploy_name])
            if gone_status == 404 and leftover.returncode != 0:
                break
            time.sleep(3)
        else:
            fail("delete did not remove the inference service and runtime")
        checks.append({"id": "inference-service-delete", "status": "passed", "get_status": 404})

        if gpu_count != 0:
            fail("cluster unexpectedly advertised nvidia.com/gpu; this batch must skip GPU live")
        checks.append({"id": "gpu-live-skipped-no-device-plugin", "status": "passed", "reason": "skipped_no_device_plugin"})
        if not smoke_ready(kubeconfig):
            fail("independent vLLM CPU smoke workload was disturbed")
        checks.append({"id": "smoke-workload-untouched", "status": "passed", "namespace": SMOKE_NS})

        evidence = {
            "profile": PROFILE,
            "status": "passed",
            "generated_at": dt.datetime.now(dt.timezone.utc).strftime("%Y-%m-%dT%H:%M:%SZ"),
            "gateway": "lab-process-not-in-cluster-ani-gateway",
            "engine": "vllm-cpu",
            "image": "digest-pinned-vllm-cpu",
            "namespace_kind": "ani-tenant-{uuid}",
            "model_mount": "pvc-snapshot-restore",
            "probe": "kubernetes_service_proxy",
            "gpu_live": "skipped_no_device_plugin",
            "auth_mode": "dev",
            "checks": checks,
        }
        assert_clean_evidence(evidence)
        EVIDENCE.parent.mkdir(parents=True, exist_ok=True)
        EVIDENCE.write_text(json.dumps(evidence, indent=2) + "\n", encoding="utf-8")
        ops_checks.append({"id": "smoke-workload-untouched", "status": "passed", "namespace": SMOKE_NS})
        ops_evidence = {
            "profile": OPS_PROFILE,
            "status": "passed",
            "generated_at": dt.datetime.now(dt.timezone.utc).strftime("%Y-%m-%dT%H:%M:%SZ"),
            "gateway": "lab-process-not-in-cluster-ani-gateway",
            "engine": "vllm-cpu",
            "image": "digest-pinned-vllm-cpu",
            "namespace_kind": "ani-tenant-{uuid}",
            "restart_mode": "lab_process",
            "scale": "desired_replicas_only_rwo_preempt",
            "auth_mode": "dev",
            "checks": ops_checks,
        }
        assert_clean_evidence(ops_evidence)
        OPS_EVIDENCE.write_text(json.dumps(ops_evidence, indent=2) + "\n", encoding="utf-8")
        print("inference cpu vllm live gate passed")
        print(f"evidence {EVIDENCE.relative_to(ROOT)}")
        print(f"ops evidence {OPS_EVIDENCE.relative_to(ROOT)}")
    finally:
        stop_proc(harness)
        stop_proc(inference)
        for forward in forwards:
            if forward.poll() is None:
                forward.send_signal(signal.SIGTERM)
                try:
                    forward.wait(timeout=5)
                except subprocess.TimeoutExpired:
                    forward.kill()
        if src_snapshot:
            run(["kubectl", "--kubeconfig", kubeconfig, "-n", SMOKE_NS, "delete", "volumesnapshot", src_snapshot, "--wait=false", "--ignore-not-found"])
        if dest_vsc:
            run(["kubectl", "--kubeconfig", kubeconfig, "delete", "volumesnapshotcontent", dest_vsc, "--wait=false", "--ignore-not-found"])
        if created_ns:
            run(["kubectl", "--kubeconfig", kubeconfig, "delete", "ns", namespace, "--wait=false"])
        postgres_exec(kubeconfig, f"DELETE FROM tenants WHERE id='{tenant_id}';")
        run(["kubectl", "--kubeconfig", kubeconfig, "delete", "clusterrolebinding", sa_name, "--ignore-not-found"])
        run(["kubectl", "--kubeconfig", kubeconfig, "delete", "clusterrole", sa_name, "--ignore-not-found"])
        run(["kubectl", "--kubeconfig", kubeconfig, "-n", "ani-system", "delete", "serviceaccount", sa_name, "--ignore-not-found"])


if __name__ == "__main__":
    try:
        main()
    except KeyboardInterrupt:
        sys.exit(130)
