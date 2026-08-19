#!/usr/bin/env python3
"""Same-node 2-GPU LeaderWorkerSet live through production ani-gateway.

Stops the retained whole-card InferenceService (does not delete it) so both
GPUs on dev-phys-02 are free, then creates placement_mode=multi_node with
count_per_replica=2. Runtime must be LeaderWorkerSet + PodGroup + leader
ClusterIP on that one node. Cross-node LWS and vGPU are out of scope.
ANI_AUTH_MODE stays auth_service. After the test service is deleted, the
retained whole-card service is started again.
"""

from __future__ import annotations

import argparse
import datetime as dt
import json
import os
import re
import signal
import subprocess
import sys
import tempfile
import time
import uuid
from pathlib import Path
from typing import Any

sys.path.insert(0, str(Path(__file__).resolve().parent))
import run_inference_cpu_vllm_live_gate as live
import run_inference_incluster_e2e as c21
import run_inference_local_model_source_e2e as c23

ROOT = live.ROOT
EVIDENCE = ROOT / "development-records/live-evidence/inference-gpu-lws-same-node-live-20260819.json"
PROFILE = "INFERENCE-SERVICE-GPU-LWS-SAME-NODE-LIVE-GATE-C37"
SERVICE_NAME = "inf-c37-lws-" + uuid.uuid4().hex[:8]
TENANT_ID = "11111111-1111-1111-1111-111111111111"
TENANT_NS = "ani-tenant-" + TENANT_ID
STORAGE_PATH = "pvc://vllm-model#/models/qwen"
RETAINED_SERVICE_ID = "c7172479-6112-49d5-a5b2-e505aa75fdb4"
TARGET_NODE = "dev-phys-02"
SPEC_ID = "gpu-nvidia-geforce-rtx-4090-full"
SERVED_MODEL = "qwen2.5-0.5b-lws-c37"
UUID_RE = re.compile(r"[0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12}")


def refresh_core_service_token(kubeconfig: str) -> None:
    issuer = c21.secret_bytes(kubeconfig, c21.AUTH_SECRET, "auth_jwt_issuer").decode("utf-8").strip()
    if not issuer:
        fail("auth-service issuer is not configured")
    private_key = c21.secret_bytes(kubeconfig, c21.AUTH_SECRET, "jwt_private_key_pem")
    now = int(time.time())
    claims = c21.service_claims(issuer, TENANT_ID, now)
    claims["exp"] = now + 14400
    service_token = c21.mint_jwt(private_key, claims)
    tmpdir = Path(tempfile.mkdtemp(prefix="ani-c37-core-"))
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


def fail(message: str) -> None:
    raise SystemExit(f"inference gpu lws same-node live gate failed: {message}")


def expect_http(status: int, wanted: int, label: str, body: dict[str, Any] | None = None) -> None:
    if status == wanted:
        return
    extra = ""
    if isinstance(body, dict):
        extra = live.redact_text(str(body.get("error_code") or body.get("code") or body.get("message") or ""))[:160]
    fail(f"{label}: status={status}, want={wanted} {extra}".strip())


def gpu_request(pod: dict[str, Any]) -> dict[str, str]:
    out: dict[str, str] = {}
    for container in ((pod.get("spec") or {}).get("containers") or []):
        requests = ((container.get("resources") or {}).get("requests") or {})
        for key, value in requests.items():
            if "gpu" in key or "vgpu" in key:
                out[str(key)] = str(value)
    return out


def object_name(item: dict[str, Any]) -> str:
    return str((item.get("metadata") or {}).get("name") or "")


def object_labels(item: dict[str, Any]) -> dict[str, Any]:
    labels = (item.get("metadata") or {}).get("labels") or {}
    return labels if isinstance(labels, dict) else {}


def owned_by_service(item: dict[str, Any], service_id: str) -> bool:
    if not service_id:
        return False
    name = object_name(item)
    labels = object_labels(item)
    return service_id in name or service_id in str(labels.values())


def tenant_gpu_pods(kubeconfig: str) -> list[dict[str, Any]]:
    pods = live.kubectl_json(kubeconfig, ["-n", TENANT_NS, "get", "pods"])
    found: list[dict[str, Any]] = []
    for pod in pods.get("items") or []:
        if gpu_request(pod):
            found.append(pod)
    return found


def named_gpu_pods(kubeconfig: str, service_id: str) -> list[dict[str, Any]]:
    return [pod for pod in tenant_gpu_pods(kubeconfig) if owned_by_service(pod, service_id)]


def wait_named_gpu_pods_gone(kubeconfig: str, service_id: str, timeout: int, message: str) -> None:
    deadline = time.time() + timeout
    while time.time() < deadline:
        if not named_gpu_pods(kubeconfig, service_id):
            return
        time.sleep(3)
    fail(message)


def list_named(kubeconfig: str, kind: str) -> list[dict[str, Any]]:
    completed = live.run(
        ["kubectl", "--kubeconfig", kubeconfig, "-n", TENANT_NS, "get", kind, "-o", "json"],
        timeout=60,
    )
    if completed.returncode != 0:
        return []
    try:
        payload = json.loads(completed.stdout or "{}")
    except json.JSONDecodeError:
        return []
    return list(payload.get("items") or [])


def find_by_owner(items: list[dict[str, Any]], service_id: str) -> dict[str, Any] | None:
    for item in items:
        if owned_by_service(item, service_id):
            return item
    return None


def find_leader_http_service(items: list[dict[str, Any]], service_id: str) -> dict[str, Any] | None:
    matches: list[dict[str, Any]] = []
    for item in items:
        if not owned_by_service(item, service_id):
            continue
        spec = item.get("spec") or {}
        selector = spec.get("selector") or {}
        cluster_ip = str(spec.get("clusterIP") or "")
        if selector.get("ani.kubercloud.io/inference-role") != "leader":
            continue
        if cluster_ip in {"", "None"}:
            continue
        matches.append(item)
    for item in matches:
        if object_name(item).endswith("-http"):
            return item
    return matches[0] if matches else None


def service_id_from_item(item: dict[str, Any]) -> str:
    labels = object_labels(item)
    for key in ("ani.kubercloud.io/platform-workload-name", "ani.kubercloud.io/platform-workload-id"):
        value = str(labels.get(key) or "")
        if UUID_RE.fullmatch(value):
            return value
    match = UUID_RE.search(object_name(item))
    return match.group(0) if match else ""


def leftover_test_service_ids(kubeconfig: str) -> list[str]:
    found: set[str] = set()
    for kind in ("leaderworkersets.leaderworkerset.x-k8s.io", "sts", "deploy"):
        for item in list_named(kubeconfig, kind):
            service_id = service_id_from_item(item)
            if service_id and service_id != RETAINED_SERVICE_ID:
                found.add(service_id)
    for pod in tenant_gpu_pods(kubeconfig):
        service_id = service_id_from_item(pod)
        if service_id and service_id != RETAINED_SERVICE_ID:
            found.add(service_id)
    return sorted(found)


def delete_owned(kubeconfig: str, service_id: str, force_pods: bool = False) -> None:
    if not service_id or service_id == RETAINED_SERVICE_ID:
        return
    kinds = [
        "leaderworkersets.leaderworkerset.x-k8s.io",
        "sts",
        "deploy",
        "svc",
        "netpol",
        "podgroups.scheduling.volcano.sh",
        "pod",
    ]
    for kind in kinds:
        for item in list_named(kubeconfig, kind):
            if not owned_by_service(item, service_id):
                continue
            name = object_name(item)
            args = ["kubectl", "--kubeconfig", kubeconfig, "-n", TENANT_NS, "delete", kind, name, "--wait=false"]
            if force_pods and kind == "pod":
                args.extend(["--force", "--grace-period=0"])
            live.run(args, timeout=60)


def wait_test_runtime_gone(kubeconfig: str, service_id: str, timeout: int = 180) -> None:
    if not service_id:
        return
    deadline = time.time() + timeout
    forced = False
    while time.time() < deadline:
        leftover = False
        for kind in ("leaderworkersets.leaderworkerset.x-k8s.io", "sts", "pods"):
            if find_by_owner(list_named(kubeconfig, kind), service_id) is not None:
                leftover = True
                break
        if not leftover:
            return
        if not forced and time.time() > deadline - (timeout / 2):
            delete_owned(kubeconfig, service_id, force_pods=True)
            forced = True
        else:
            delete_owned(kubeconfig, service_id, force_pods=False)
        time.sleep(3)
    fail("test LWS runtime leftover pods or StatefulSets still hold GPUs")


def inspect_lws(kubeconfig: str, service_id: str) -> dict[str, Any]:
    lws = find_by_owner(list_named(kubeconfig, "leaderworkersets.leaderworkerset.x-k8s.io"), service_id)
    if lws is None:
        fail("LeaderWorkerSet was not created")
    deploy = find_by_owner(list_named(kubeconfig, "deploy"), service_id)
    if deploy is not None:
        fail("multi_node create rendered a Deployment")
    pods = [pod for pod in live.kubectl_json(kubeconfig, ["-n", TENANT_NS, "get", "pods"]).get("items") or [] if owned_by_service(pod, service_id)]
    if len(pods) < 2:
        fail(f"LWS did not create leader and worker pods: count={len(pods)}")
    nodes = sorted({str((pod.get("spec") or {}).get("nodeName") or "") for pod in pods})
    if nodes != [TARGET_NODE]:
        fail(f"LWS pods did not land on {TARGET_NODE}: nodes={nodes}")
    for pod in pods:
        req = gpu_request(pod)
        if req.get("nvidia.com/gpu") != "1":
            fail(f"LWS pod gpu request = {req}")
        if "volcano.sh/vgpu-number" in req:
            fail("LWS pod requested volcano vGPU")
        scheduler = str((pod.get("spec") or {}).get("schedulerName") or "")
        if scheduler != "volcano":
            fail(f"LWS pod schedulerName={scheduler}")
    pg = find_by_owner(list_named(kubeconfig, "podgroups.scheduling.volcano.sh"), service_id)
    if pg is None:
        fail("LWS PodGroup was not created")
    min_member = ((pg.get("spec") or {}).get("minMember"))
    if int(min_member or 0) != 2:
        fail(f"PodGroup minMember={min_member}")
    svc = find_leader_http_service(list_named(kubeconfig, "svc"), service_id)
    if svc is None:
        fail("leader ClusterIP was not created")
    if str((svc.get("spec") or {}).get("type") or "") != "ClusterIP":
        fail("LWS service type is not ClusterIP")
    selector = (svc.get("spec") or {}).get("selector") or {}
    if selector.get("ani.kubercloud.io/inference-role") != "leader":
        fail("LWS service does not select leader")
    return {
        "lws": True,
        "pod_count": len(pods),
        "nodes": nodes,
        "scheduler": "volcano",
        "gpu": "nvidia.com/gpu=1",
        "service_type": "ClusterIP",
        "ready": all(str((pod.get("status") or {}).get("phase") or "") == "Running" for pod in pods),
    }


def restore_retained(base: str, token: str, kubeconfig: str) -> dict[str, Any]:
    status, _ = live.request(
        "GET",
        f"{base}/api/v1/svc/inference-services/{RETAINED_SERVICE_ID}",
        TENANT_ID,
        token=token,
    )
    if status != 200:
        fail("retained whole-card service was lost")
    start_status, _ = live.request(
        "POST",
        f"{base}/api/v1/svc/inference-services/{RETAINED_SERVICE_ID}/lifecycle",
        TENANT_ID,
        {"idempotency_key": str(uuid.uuid4()), "action": "start"},
        token=token,
    )
    expect_http(start_status, 202, "retained-service-start")
    return live.wait_service(
        base, TENANT_ID, RETAINED_SERVICE_ID, "running", timeout=1800, kubeconfig="", token=token
    )


def main() -> None:
    parser = argparse.ArgumentParser()
    parser.add_argument("--kubeconfig", default=os.environ.get("KUBECONFIG", str(Path.home() / ".kube/config")))
    parser.add_argument("--listen", default="127.0.0.1:18088")
    args = parser.parse_args()
    kubeconfig = args.kubeconfig
    if not Path(kubeconfig).exists():
        fail("kubeconfig is missing")

    checks: list[dict[str, Any]] = []
    forward: subprocess.Popen[str] | None = None
    tmpdir = Path(tempfile.mkdtemp(prefix="ani-inf-c37-"))
    test_service_id = ""
    token = ""
    base = ""
    stopped_retained = False
    try:
        if c21.gateway_env(kubeconfig, "ANI_AUTH_MODE") != "auth_service":
            fail("production ani-gateway ANI_AUTH_MODE is not auth_service")
        if c21.gateway_env(kubeconfig, "INFERENCE_SERVICE_GRPC_ADDR") != c21.INFERENCE_GRPC_ADDR:
            fail("production ani-gateway is missing inference gRPC address")
        if c21.gateway_env(kubeconfig, "PLATFORM_WORKLOAD_PROVIDER") != "kubernetes_rest":
            fail("production ani-gateway is missing kubernetes_rest platform-workload provider")
        ns = live.run(["kubectl", "--kubeconfig", kubeconfig, "get", "ns", TENANT_NS])
        if ns.returncode != 0:
            fail("existing tenant namespace is missing")
        pvc = live.run(["kubectl", "--kubeconfig", kubeconfig, "-n", TENANT_NS, "get", "pvc", "vllm-model"])
        if pvc.returncode != 0:
            fail("tenant vllm-model PVC is missing")
        c21.wait_deploy(kubeconfig, c21.INFERENCE_DEPLOY)
        c21.wait_deploy(kubeconfig, c21.PRODUCTION_GATEWAY)
        print("c37: refreshing core service token", flush=True)
        refresh_core_service_token(kubeconfig)
        checks.append({"id": "tenant-local-pvc-ready", "status": "passed", "storage_path": STORAGE_PATH})
        checks.append({"id": "in-cluster-inference-service-running", "status": "passed"})
        checks.append({"id": "production-gateway-auth-mode-preserved", "status": "passed", "auth_mode": "auth_service"})

        lws_crd = live.run(["kubectl", "--kubeconfig", kubeconfig, "get", "crd", "leaderworkersets.leaderworkerset.x-k8s.io"])
        if lws_crd.returncode != 0:
            fail("LeaderWorkerSet CRD is missing")
        checks.append({"id": "lws-crd-ready", "status": "passed"})

        issuer = c21.secret_bytes(kubeconfig, c21.AUTH_SECRET, "auth_jwt_issuer").decode("utf-8").strip()
        if not issuer:
            fail("auth-service issuer is not configured")
        private_key = c21.secret_bytes(kubeconfig, c21.AUTH_SECRET, "jwt_private_key_pem")
        now = int(time.time())
        token = c21.mint_jwt(private_key, c21.tenant_claims(issuer, TENANT_ID, str(uuid.uuid4()), now))
        private_key = b""

        host, port = args.listen.split(":")
        forward = subprocess.Popen(
            ["kubectl", "--kubeconfig", kubeconfig, "-n", c21.GATEWAY_NS, "port-forward", f"svc/{c21.PRODUCTION_GATEWAY}", f"{port}:8080"],
            stdout=subprocess.DEVNULL,
            stderr=subprocess.DEVNULL,
        )
        live.wait_tcp(host, int(port), timeout=30)
        base = "http://" + args.listen
        live.wait_http(f"{base}/readyz", timeout=60)

        list_status, listed = live.request(
            "GET",
            f"{base}/api/v1/svc/inference-services",
            TENANT_ID,
            token=token,
        )
        expect_http(list_status, 200, "list-inference-services", listed)
        for item in listed.get("items") or listed.get("services") or []:
            name = str(item.get("name") or "")
            leftover_id = str(item.get("id") or "")
            if leftover_id and leftover_id != RETAINED_SERVICE_ID and name.startswith("inf-c37-lws-"):
                print("c37: deleting leftover product service " + leftover_id, flush=True)
                live.request(
                    "DELETE",
                    f"{base}/api/v1/svc/inference-services/{leftover_id}",
                    TENANT_ID,
                    token=token,
                )
                wait_test_runtime_gone(kubeconfig, leftover_id)
        for leftover_id in leftover_test_service_ids(kubeconfig):
            print("c37: cleaning leftover test runtime " + leftover_id, flush=True)
            wait_test_runtime_gone(kubeconfig, leftover_id)

        retained_status, retained = live.request(
            "GET",
            f"{base}/api/v1/svc/inference-services/{RETAINED_SERVICE_ID}",
            TENANT_ID,
            token=token,
        )
        expect_http(retained_status, 200, "get-retained-service", retained)
        image_ref = str(retained.get("image_ref") or "")
        model = str(retained.get("model_version_id") or retained.get("model") or "")
        if "@sha256:" not in image_ref:
            fail("retained service is missing a digest-pinned image_ref")
        if not model:
            fail("retained service is missing model_version_id")

        print("c37: stopping retained whole-card service", flush=True)
        stop_status, _ = live.request(
            "POST",
            f"{base}/api/v1/svc/inference-services/{RETAINED_SERVICE_ID}/lifecycle",
            TENANT_ID,
            {"idempotency_key": str(uuid.uuid4()), "action": "stop"},
            token=token,
        )
        expect_http(stop_status, 202, "retained-service-stop")
        live.wait_service(base, TENANT_ID, RETAINED_SERVICE_ID, "stopped", timeout=180, kubeconfig="", token=token)
        wait_named_gpu_pods_gone(
            kubeconfig,
            RETAINED_SERVICE_ID,
            180,
            "retained whole-card GPU pod did not release after stop",
        )
        for leftover_id in leftover_test_service_ids(kubeconfig):
            print("c37: cleaning leftover test runtime " + leftover_id, flush=True)
            live.request(
                "DELETE",
                f"{base}/api/v1/svc/inference-services/{leftover_id}",
                TENANT_ID,
                token=token,
            )
            wait_test_runtime_gone(kubeconfig, leftover_id)
        stopped_retained = True
        checks.append({"id": "retained-service-stopped", "status": "passed", "status_value": "stopped"})

        create_body = {
            "idempotency_key": str(uuid.uuid4()),
            "name": SERVICE_NAME,
            "model": model,
            "model_version_id": model,
            "served_model_name": SERVED_MODEL,
            "replicas": 1,
            "placement_mode": "multi_node",
            "image_ref": image_ref,
            "resources": {
                "cpu": "4",
                "memory": "16Gi",
                "accelerator": {"spec_id": SPEC_ID, "count_per_replica": 2},
            },
        }
        print("c37: creating same-node 2-GPU LWS service", flush=True)
        create_status, created = live.request(
            "POST",
            f"{base}/api/v1/svc/inference-services",
            TENANT_ID,
            create_body,
            token=token,
        )
        expect_http(create_status, 202, "product-inference-service-lws-create", created)
        test_service_id = str(created.get("id") or "")
        if not test_service_id:
            fail("create did not return a service id")
        checks.append({"id": "product-inference-service-lws-create", "status": "passed", "http_status": 202})

        deadline = time.time() + 300
        runtime: dict[str, Any] = {}
        last_error = "LWS runtime was not observed"
        while time.time() < deadline:
            try:
                runtime = inspect_lws(kubeconfig, test_service_id)
                break
            except SystemExit as err:
                last_error = str(err)
                time.sleep(5)
        else:
            fail(last_error)
        checks.append({"id": "kubectl-vllm-gpu-lws", "status": "passed", **runtime})
        if TARGET_NODE not in runtime.get("nodes", []):
            fail("same-node LWS did not use the whole-card worker node")
        checks.append({"id": "lws-same-node-dev-phys-02", "status": "passed", "node": TARGET_NODE})
        checks.append({"id": "lws-cross-node-skipped", "status": "passed", "reason": "same_node_two_gpu_only"})

        observed = live.wait_service(
            base, TENANT_ID, test_service_id, "running", timeout=1800, kubeconfig="", token=token
        )
        if observed.get("invocation_url") is not None or observed.get("endpoint_url") is not None:
            fail("product response leaked an invocation URL")
        checks.append({"id": "inference-service-running-health-smoke", "status": "passed", "status_value": "running"})
        checks.append({"id": "invocation-url-null", "status": "passed"})

        logs_status, logs_body = live.request(
            "GET",
            f"{base}/api/v1/svc/inference-services/{test_service_id}/logs",
            TENANT_ID,
            token=token,
        )
        expect_http(logs_status, 200, "inference-service-logs", logs_body)
        items = logs_body.get("items") or logs_body.get("logs") or []
        leaked = any(isinstance(item, dict) and "replica" in item for item in items)
        if leaked:
            fail("product logs leaked replica")
        checks.append({"id": "inference-service-logs", "status": "passed", "http_status": 200, "replica_leaked": False})

        print("c37: deleting test LWS service", flush=True)
        delete_status, _ = live.request(
            "DELETE",
            f"{base}/api/v1/svc/inference-services/{test_service_id}",
            TENANT_ID,
            token=token,
        )
        expect_http(delete_status, 202, "test-service-delete")
        gone_deadline = time.time() + 180
        while time.time() < gone_deadline:
            gone_status, _ = live.request(
                "GET",
                f"{base}/api/v1/svc/inference-services/{test_service_id}",
                TENANT_ID,
                token=token,
            )
            if gone_status == 404:
                break
            time.sleep(3)
        else:
            fail("test LWS service was not deleted")
        wait_test_runtime_gone(kubeconfig, test_service_id, timeout=180)
        wait_named_gpu_pods_gone(
            kubeconfig,
            test_service_id,
            120,
            "test LWS GPU pods did not release after delete",
        )
        test_service_id = ""
        checks.append({"id": "test-service-deleted", "status": "passed"})

        print("c37: starting retained whole-card service", flush=True)
        started = restore_retained(base, token, kubeconfig)
        if started.get("id") != RETAINED_SERVICE_ID:
            fail("start did not reuse the retained service id")
        stopped_retained = False
        checks.append({"id": "service-retained", "status": "passed"})
        checks.append({"id": "existing-tenant-namespace-preserved", "status": "passed"})

        evidence = {
            "profile": PROFILE,
            "status": "passed",
            "generated_at": dt.datetime.now(dt.timezone.utc).strftime("%Y-%m-%dT%H:%M:%SZ"),
            "entry": "ani-system/ani-gateway",
            "gateway": "ani-system/ani-gateway",
            "auth_mode": "auth_service",
            "auth": "tenant_bearer",
            "model_source": "tenant_local_pvc",
            "image_source": "request_image_ref",
            "engine": "vllm-gpu-lws",
            "image": "digest-pinned-vllm-cuda",
            "namespace_kind": "existing-ani-tenant-{uuid}",
            "probe": "clusterip",
            "scheduler": "volcano",
            "accelerator": "nvidia.com/gpu=1x2",
            "spec_id": SPEC_ID,
            "placement_mode": "multi_node",
            "gpu_live": "same_node_two_gpu_lws",
            "lws_live": "same_node",
            "cross_node_lws": "skipped",
            "vgpu": False,
            "test_service_deleted": True,
            "service_retained": True,
            "gpu_ready": False,
            "runtime_ready": False,
            "checks": checks,
        }
        live.assert_clean_evidence(evidence)
        EVIDENCE.parent.mkdir(parents=True, exist_ok=True)
        EVIDENCE.write_text(json.dumps(evidence, indent=2, ensure_ascii=True) + "\n", encoding="utf-8")
        print("c37: evidence written", flush=True)
    finally:
        if test_service_id:
            if token and base:
                live.request(
                    "DELETE",
                    f"{base}/api/v1/svc/inference-services/{test_service_id}",
                    TENANT_ID,
                    token=token,
                )
            try:
                wait_test_runtime_gone(kubeconfig, test_service_id, timeout=180)
            except SystemExit as err:
                print("c37: leftover test runtime: " + live.redact_text(str(err))[:160], flush=True)
        if stopped_retained and token and base:
            leftover = leftover_test_service_ids(kubeconfig)
            if leftover:
                print("c37: skip retained start while leftover test GPUs remain", flush=True)
            else:
                try:
                    restore_retained(base, token, kubeconfig)
                except Exception as err:
                    print("c37: failed to restore retained service: " + live.redact_text(str(err))[:160], flush=True)
        if forward is not None and forward.poll() is None:
            forward.send_signal(signal.SIGTERM)
            try:
                forward.wait(timeout=10)
            except subprocess.TimeoutExpired:
                forward.kill()
        live.run(["rm", "-rf", str(tmpdir)])
        token = ""


if __name__ == "__main__":
    main()
