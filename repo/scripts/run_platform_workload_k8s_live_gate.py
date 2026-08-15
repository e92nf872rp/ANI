#!/usr/bin/env python3
"""Run the CPU PlatformWorkload Kubernetes live gate against an approved lab cluster.

This starts a local lab Gateway harness with the current C8/C9 code. It does
not roll out the in-cluster ani-gateway Deployment.
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
GATE = ROOT / "deploy/real-k8s-lab/platform-workload-k8s-live-gate.yaml"
MIGRATION = ROOT / "deploy/migrations/20260815_001_platform_workloads.sql"
EVIDENCE = ROOT / "development-records/live-evidence/platform-workload-k8s-live-20260815.json"
PROFILE = "INFERENCE-PLATFORM-WORKLOAD-K8S-LIVE-GATE-C9"
BUSYBOX_FALLBACK = (
    "docker.changqingyun.cn/mirror/busybox@sha256:"
    "fd8d9aa63ba2f0982b5304e1ee8d3b90a210bc1ffb5314d980eb6962f1a9715d"
)
IPV4_RE = re.compile(r"\b\d{1,3}(?:\.\d{1,3}){3}\b")
TOKENISH_RE = re.compile(r"eyJ[A-Za-z0-9_-]{10,}\.[A-Za-z0-9_-]{10,}\.[A-Za-z0-9_-]{10,}")


def fail(message: str) -> None:
    raise SystemExit(f"platform workload k8s live gate failed: {message}")


def run(cmd: list[str], **kwargs: Any) -> subprocess.CompletedProcess[str]:
    return subprocess.run(cmd, text=True, capture_output=True, check=False, **kwargs)


def kubectl(kubeconfig: str, args: list[str], timeout: int = 60) -> str:
    completed = run(["kubectl", "--kubeconfig", kubeconfig, *args], timeout=timeout)
    if completed.returncode != 0:
        fail(f"kubectl {' '.join(args)} failed: {completed.stderr.strip() or completed.stdout.strip()}")
    return completed.stdout


def request(
    method: str,
    url: str,
    tenant_id: str,
    service: bool,
    body: dict[str, Any] | None = None,
    idempotency_key: str = "",
) -> tuple[int, dict[str, Any]]:
    data = None
    headers = {
        "Accept": "application/json",
        "X-Dev-Tenant-ID": tenant_id,
    }
    if service:
        headers["X-Dev-Principal-Kind"] = "service"
        headers["X-Dev-Service-Scope"] = "scope:platform-workloads:write"
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


def discover_busybox_image(kubeconfig: str) -> str:
    raw = kubectl(
        kubeconfig,
        ["get", "pods", "-A", "-o", "jsonpath={range .items[*]}{range .status.containerStatuses[*]}{.imageID}{\"\\n\"}{end}{end}"],
    )
    for line in raw.splitlines():
        line = line.strip()
        if "busybox@sha256:" in line:
            return line.split("://", 1)[-1]
    return BUSYBOX_FALLBACK


def rewrite_dsn(dsn: str, host: str, port: int) -> str:
    parsed = urllib.parse.urlparse(dsn)
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


def apply_migration(kubeconfig: str) -> None:
    sql = MIGRATION.read_text(encoding="utf-8").replace(
        "GRANT SELECT, INSERT, UPDATE, DELETE ON\n    platform_workloads,\n    platform_workload_intents\nTO ani_app;",
        """DO $$ BEGIN
  IF EXISTS (SELECT 1 FROM pg_roles WHERE rolname = 'ani_app') THEN
    GRANT SELECT, INSERT, UPDATE, DELETE ON platform_workloads, platform_workload_intents TO ani_app;
  END IF;
END $$;""",
    )
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
        fail(f"apply platform_workloads migration failed: {completed.stderr.strip() or completed.stdout.strip()}")


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
    return value


def harness_log(proc: subprocess.Popen[str] | None) -> str:
    path = getattr(proc, "log_path", None) if proc is not None else None
    if not path:
        return ""
    try:
        return Path(path).read_text(encoding="utf-8", errors="replace")
    except OSError:
        return ""


def wait_http(url: str, proc: subprocess.Popen[str] | None = None, timeout: int = 60) -> None:
    deadline = time.time() + timeout
    while time.time() < deadline:
        if proc is not None and proc.poll() is not None:
            fail(f"harness exited {proc.returncode}: {redact_text(harness_log(proc)[-2000:] or 'no output')}")
        try:
            with urllib.request.urlopen(url, timeout=2) as response:
                if response.status == 200:
                    return
        except (urllib.error.URLError, TimeoutError):
            time.sleep(0.5)
    if proc is not None and proc.poll() is None:
        proc.send_signal(signal.SIGTERM)
        try:
            proc.wait(timeout=5)
        except subprocess.TimeoutExpired:
            proc.kill()
    fail(f"harness did not become ready at {url}: {redact_text(harness_log(proc)[-2000:] or 'no output')}")


def wait_state(base: str, tenant: str, workload_id: str, wanted: str, timeout: int = 180) -> dict[str, Any]:
    deadline = time.time() + timeout
    last: dict[str, Any] = {}
    while time.time() < deadline:
        status, body = request("GET", f"{base}/api/v1/platform-workloads/{workload_id}", tenant, True)
        if status == 200:
            last = body
            if body.get("state") == wanted:
                return body
        time.sleep(2)
    fail(f"workload {workload_id} did not reach {wanted}: {last.get('state')}")
    return last




def assert_clean_evidence(document: dict[str, Any]) -> None:
    raw = json.dumps(document, ensure_ascii=True)
    if TOKENISH_RE.search(raw) or "Bearer " in raw or "password" in raw.lower():
        fail("evidence contains forbidden secret material")
    if IPV4_RE.search(raw):
        fail("evidence contains a raw IP")


def start_harness(env: dict[str, str], listen: str, log_path: Path) -> subprocess.Popen[str]:
    log_file = log_path.open("w", encoding="utf-8", buffering=1)
    proc = subprocess.Popen(
        ["go", "run", "./cmd/platform-workload-live"],
        cwd=str(ROOT / "services/ani-gateway"),
        env=env,
        stdout=log_file,
        stderr=subprocess.STDOUT,
        text=True,
    )
    proc.log_file = log_file  # type: ignore[attr-defined]
    proc.log_path = log_path  # type: ignore[attr-defined]
    try:
        wait_http(f"http://{listen}/readyz", proc, timeout=90)
    except SystemExit:
        log_file.close()
        raise
    return proc


def stop_harness(proc: subprocess.Popen[str] | None) -> None:
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


def main() -> None:
    parser = argparse.ArgumentParser()
    parser.add_argument("--kubeconfig", default=os.environ.get("KUBECONFIG", str(Path.home() / ".kube/config")))
    parser.add_argument("--listen", default="127.0.0.1:18080")
    args = parser.parse_args()
    kubeconfig = args.kubeconfig
    listen = args.listen
    if not Path(kubeconfig).exists():
        fail("kubeconfig is missing")

    tenant_id = str(uuid.uuid4())
    workload_name = "pw-c12-" + uuid.uuid4().hex[:8]
    namespace = f"ani-tenant-{tenant_id}"
    sa_name = "ani-pw-c12"
    checks: list[dict[str, Any]] = []
    harness: subprocess.Popen[str] | None = None
    forward: subprocess.Popen[str] | None = None
    tmpdir = Path(tempfile.mkdtemp(prefix="ani-pw-c12-"))
    created_ns = False
    try:
        server, ca_data = current_cluster(kubeconfig)
        (tmpdir / "ca.crt").write_bytes(base64.b64decode(ca_data))
        image = discover_busybox_image(kubeconfig)

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
        kubectl(kubeconfig, ["apply", "-f", str(tmpdir / "rbac.yaml")])
        token = kubectl(kubeconfig, ["-n", "ani-system", "create", "token", sa_name, "--duration=2h"]).strip()
        if not token:
            fail("failed to mint lab service account token")

        secret = kubectl(
            kubeconfig,
            ["-n", "ani-system", "get", "secret", "ani-services-runtime", "-o", "jsonpath={.data.database_url}"],
        ).strip()
        dsn = base64.b64decode(secret).decode("utf-8")
        forward = subprocess.Popen(
            ["kubectl", "--kubeconfig", kubeconfig, "-n", "ani-system", "port-forward", "pod/ani-postgres-0", "15432:5432"],
            stdout=subprocess.DEVNULL,
            stderr=subprocess.DEVNULL,
        )
        wait_tcp("127.0.0.1", 15432)
        database_url = rewrite_dsn(dsn, "127.0.0.1", 15432)
        apply_migration(kubeconfig)
        postgres_exec(
            kubeconfig,
            "INSERT INTO tenants (id, name, display_name, status, max_gpu_count, max_cpu_cores, max_memory_gb, settings) "
            f"VALUES ('{tenant_id}', 'pw-c12-lab', 'platform-workload-c12', 'active', 0, 1, 1, '{{}}') "
            "ON CONFLICT (id) DO NOTHING;",
        )

        env = os.environ.copy()
        env.update({
            "ANI_AUTH_MODE": "dev",
            "GATEWAY_LISTEN_ADDR": listen,
            "KUBERNETES_API_HOST": server,
            "KUBERNETES_BEARER_TOKEN": token,
            "KUBERNETES_SERVICE_ACCOUNT_CA_FILE": str(tmpdir / "ca.crt"),
            "DATABASE_URL": database_url,
            "PLATFORM_WORKLOAD_PROVIDER": "kubernetes_rest",
        })
        harness = start_harness(env, listen, tmpdir / "harness.log")
        base = f"http://{listen}"
        spec = {
            "idempotency_key": str(uuid.uuid4()),
            "name": workload_name,
            "workload_class": "inference",
            "runtime_kind": "container",
            "image_ref": image,
            "command": ["sh", "-c", "mkdir -p /www && echo ok >/www/index.html && httpd -f -p 8000 -h /www"],
            "replicas": 1,
            "resources": {"cpu": "100m", "memory": "128Mi"},
            "topology": {"mode": "single_node", "profile_id": "container-single-node", "profile_version": "v1"},
            "scheduling": {"queue_class": "inference", "gang": False},
            "network": {"exposure": "cluster_internal", "ports": [{"name": "http", "port": 8000}]},
            "health_check": {"protocol": "http", "path": "/", "port_name": "http"},
            "metadata": {"owner_ref": str(uuid.uuid4())},
        }

        denied_status, _ = request("POST", f"{base}/api/v1/platform-workloads", tenant_id, False, spec)
        expect(denied_status, 403, "tenant-jwt-forbidden")
        checks.append({"id": "tenant-jwt-forbidden", "status": "passed", "http_status": 403})

        create_status, created = request("POST", f"{base}/api/v1/platform-workloads", tenant_id, True, spec)
        expect(create_status, 202, "core-platform-workload-cpu-create")
        workload_id = str(created.get("resource_id") or "")
        if not workload_id:
            fail("create did not return resource_id")
        created_ns = True
        checks.append({"id": "core-platform-workload-cpu-create", "status": "passed", "http_status": 202})

        labels = kubectl(kubeconfig, ["-n", namespace, "get", "deploy,svc", workload_name, "--show-labels"])
        if "ani.platform_workload=inference" not in labels or "ani.kubercloud.io/instance" in labels:
            fail("kubectl labels did not match platform-workload isolation")
        checks.append({"id": "kubectl-deployment-service-labels", "status": "passed"})

        observed = wait_state(base, tenant_id, workload_id, "running")
        endpoint = str(observed.get("internal_endpoint") or "")
        if not endpoint.endswith(f"{workload_name}.{namespace}.svc:8000"):
            fail("internal endpoint was not a ClusterIP service DNS")
        checks.append({"id": "core-platform-workload-observe-running", "status": "passed", "state": "running"})

        scale_status, _ = request(
            "PATCH",
            f"{base}/api/v1/platform-workloads/{workload_id}",
            tenant_id,
            True,
            {"idempotency_key": str(uuid.uuid4()), "replicas": 2},
        )
        expect(scale_status, 202, "core-platform-workload-scale")
        scaled = wait_state(base, tenant_id, workload_id, "running")
        if int(scaled.get("desired_replicas") or 0) != 2:
            fail("desired_replicas was not 2 after scale")
        checks.append({"id": "core-platform-workload-scale", "status": "passed", "desired_replicas": 2})

        stop_status, _ = request(
            "POST",
            f"{base}/api/v1/platform-workloads/{workload_id}/lifecycle",
            tenant_id,
            True,
            {"idempotency_key": str(uuid.uuid4()), "action": "stop"},
        )
        expect(stop_status, 202, "core-platform-workload-stop")
        stopped = wait_state(base, tenant_id, workload_id, "stopped", timeout=120)
        if stopped.get("internal_endpoint"):
            fail("stop left an internal endpoint")
        deploy_after_stop = run(["kubectl", "--kubeconfig", kubeconfig, "-n", namespace, "get", "deploy", workload_name])
        if deploy_after_stop.returncode == 0:
            fail("stop left the Deployment")
        checks.append({"id": "core-platform-workload-stop", "status": "passed", "state": "stopped"})

        start_status, _ = request(
            "POST",
            f"{base}/api/v1/platform-workloads/{workload_id}/lifecycle",
            tenant_id,
            True,
            {"idempotency_key": str(uuid.uuid4()), "action": "start"},
        )
        expect(start_status, 202, "core-platform-workload-start")
        started = wait_state(base, tenant_id, workload_id, "running")
        if started.get("id") != workload_id:
            fail("start did not reuse the same workload id")
        checks.append({"id": "core-platform-workload-start", "status": "passed", "same_workload_id": True})

        stop_harness(harness)
        harness = start_harness(env, listen, tmpdir / "harness-restart.log")
        restored_status, restored = request("GET", f"{base}/api/v1/platform-workloads/{workload_id}", tenant_id, True)
        expect(restored_status, 200, "gateway-restart-get")
        if restored.get("id") != workload_id:
            fail("workload was not restored from postgres after harness restart")
        checks.append({"id": "gateway-restart-get", "status": "passed", "restart_mode": "lab_process"})

        logs_status, logs = request("GET", f"{base}/api/v1/platform-workloads/{workload_id}/logs", tenant_id, True)
        expect(logs_status, 200, "logs-empty-until-live-log-store")
        if logs.get("items") not in ([], None):
            fail("platform-workload logs were not empty")
        checks.append({"id": "logs-empty-until-live-log-store", "status": "passed", "http_status": 200})

        delete_status, _ = request(
            "DELETE",
            f"{base}/api/v1/platform-workloads/{workload_id}",
            tenant_id,
            True,
            idempotency_key=str(uuid.uuid4()),
        )
        expect(delete_status, 202, "core-platform-workload-delete")
        gone_status, _ = request("GET", f"{base}/api/v1/platform-workloads/{workload_id}", tenant_id, True)
        expect(gone_status, 404, "core-platform-workload-delete-get")
        leftover = run(["kubectl", "--kubeconfig", kubeconfig, "-n", namespace, "get", "deploy,svc", workload_name])
        if leftover.returncode == 0:
            fail("delete left Deployment or Service")
        checks.append({"id": "core-platform-workload-delete", "status": "passed", "get_status": 404})

        evidence = {
            "profile": PROFILE,
            "status": "passed",
            "generated_at": dt.datetime.now(dt.timezone.utc).strftime("%Y-%m-%dT%H:%M:%SZ"),
            "gateway": "lab-process-not-in-cluster-ani-gateway",
            "image": image,
            "namespace_kind": "ani-tenant-{uuid}",
            "restart_mode": "lab_process",
            "auth_mode": "dev",
            "checks": checks,
        }
        assert_clean_evidence(evidence)
        EVIDENCE.parent.mkdir(parents=True, exist_ok=True)
        EVIDENCE.write_text(json.dumps(evidence, indent=2) + "\n", encoding="utf-8")
        print("platform workload k8s live gate passed")
        print(f"evidence {EVIDENCE.relative_to(ROOT)}")
    finally:
        stop_harness(harness)
        if forward is not None and forward.poll() is None:
            forward.send_signal(signal.SIGTERM)
            try:
                forward.wait(timeout=5)
            except subprocess.TimeoutExpired:
                forward.kill()
        if created_ns:
            run(["kubectl", "--kubeconfig", kubeconfig, "delete", "ns", namespace, "--wait=false"])
        postgres_exec(kubeconfig, f"DELETE FROM tenants WHERE id='{tenant_id}' AND name='pw-c12-lab';")
        run(["kubectl", "--kubeconfig", kubeconfig, "delete", "clusterrolebinding", sa_name, "--ignore-not-found"])
        run(["kubectl", "--kubeconfig", kubeconfig, "delete", "clusterrole", sa_name, "--ignore-not-found"])
        run(["kubectl", "--kubeconfig", kubeconfig, "-n", "ani-system", "delete", "serviceaccount", sa_name, "--ignore-not-found"])


if __name__ == "__main__":
    try:
        main()
    except KeyboardInterrupt:
        sys.exit(130)
