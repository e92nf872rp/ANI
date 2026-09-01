#!/usr/bin/env python3
"""Shared helpers for InferenceService product live runners.

C12-C20 used a local lab Gateway harness. That binary and those runners were
removed in C25. Product live uses in-cluster ani-gateway (C21+). Direct
execution of this file is rejected on purpose.
"""

from __future__ import annotations

import base64
import json
import os
import re
import signal
import socket
import subprocess
import sys
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
PW_MIGRATION = ROOT / "deploy/migrations/20260815000100_platform_workloads.sql"
INF_MIGRATION = ROOT / "deploy/migrations/20260814000100_inference_control_plane.sql"
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
    token: str = "",
) -> tuple[int, dict[str, Any]]:
    data = None
    headers = {
        "Accept": "application/json",
        "X-Dev-Tenant-ID": tenant_id,
    }
    if token:
        headers["Authorization"] = "Bearer " + token
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
    token: str = "",
) -> dict[str, Any]:
    deadline = time.time() + timeout
    last: dict[str, Any] = {}
    while time.time() < deadline:
        status, body = request(
            "GET",
            f"{base}/api/v1/svc/inference-services/{service_id}",
            tenant,
            token=token,
        )
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
    completed = run(
        ["kubectl", "--kubeconfig", kubeconfig, "-n", SMOKE_NS, "get", "deploy", SMOKE_DEPLOY, "-o", "json"],
        timeout=60,
    )
    if completed.returncode != 0:
        return IMAGE_FALLBACK
    deploy = json.loads(completed.stdout or "{}")
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
    completed = run(
        ["kubectl", "--kubeconfig", kubeconfig, "-n", SMOKE_NS, "get", "deploy", SMOKE_DEPLOY, "-o", "json"],
        timeout=60,
    )
    if completed.returncode != 0:
        return False
    deploy = json.loads(completed.stdout or "{}")
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
    annotations = (((deploy.get("spec") or {}).get("template") or {}).get("metadata") or {}).get("annotations") or {}
    if annotations.get("ovn.kubernetes.io/logical_switch") != "ovn-default":
        fail("deployment is not pinned to the cluster default overlay")
    if annotations.get("ovn.kubernetes.io/vpc") != "ovn-cluster":
        fail("deployment is not pinned to the cluster default VPC")
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
    raise SystemExit(
        "lab Gateway harness was removed in C25; product live uses in-cluster ani-gateway. "
        "Use scripts/run_inference_incluster_e2e.py, "
        "scripts/run_inference_console_shaped_e2e.py, or "
        "scripts/run_inference_local_model_source_e2e.py"
    )


if __name__ == "__main__":
    try:
        main()
    except KeyboardInterrupt:
        sys.exit(130)
