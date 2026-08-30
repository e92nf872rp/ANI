#!/usr/bin/env python3
"""C39 GPU memory live through production ani-gateway.

Cleans leftover inference test services, then creates both a whole-card
InferenceService (omit memory → nvidia.com/gpu) and a vGPU InferenceService
(memory in MiB → volcano.sh/vgpu-*). Temporarily scales the CPU service that
holds the RWO model PVC. Default run restores CPU/instance and deletes the
test services. --keep leaves both InferenceServices running (CPU and the
competing whole-card instance stay scaled to 0; vGPU uses a cloned model PVC
because vllm-model is RWO). --load-seconds runs a ClusterIP chat completion
soak from a same-namespace client pod. ANI_AUTH_MODE stays auth_service.
Cross-node LWS is out of scope.
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
import run_inference_cpu_vllm_live_gate as live
import run_inference_incluster_e2e as c21

ROOT = live.ROOT
EVIDENCE = ROOT / "development-records/live-evidence/inference-gpu-memory-live-20260821.json"
KEEP_EVIDENCE = ROOT / "development-records/live-evidence/inference-gpu-memory-keep-load-20260821.json"
PROFILE = "INFERENCE-SERVICE-GPU-MEMORY-LIVE-GATE-C39"
TENANT_ID = "11111111-1111-1111-1111-111111111111"
TENANT_NS = "ani-tenant-" + TENANT_ID
OLD_GPU_ID = "c7172479-6112-49d5-a5b2-e505aa75fdb4"
CPU_PVC_HOLDER_ID = "deb06740-9038-447e-8cda-32839cb3babe"
INSTANCE_NS = "ani-tenant-00000000-0000-0000-0000-000000000001"
WHOLE_CARD_INSTANCE = "whole-new3-2"
KEEP_SERVICE_IDS = {CPU_PVC_HOLDER_ID}
CLEAN_NAME_PREFIXES = ("inf-c36-", "inf-c37-", "inf-c39-", "inf-verify-", "inf-gpu-qwen")
SPEC_ID = "gpu-nvidia-geforce-rtx-4090"
MEMORY_MIB = 12280
CLONE_PVC = "vllm-model-c39-vgpu"
CLONE_SNAP = "vllm-model-c39"
LOAD_POD = "c39-load-client"
LOAD_CLIENT_IMAGE = (
    "docker.changqingyun.cn/mirror/vllm-openai-cpu@sha256:"
    "4c697ae650ebeb3a41f3c9c7020913d4c84d2729dc428ce39d60ca353975a4ce"
)
CUDA_IMAGE_REF = (
    "docker.changqingyun.cn/ani/vllm-openai@sha256:"
    "6cf9808ca8810fc6c3fd0451c2e7784fb224590d81f7db338e7eaf3c02a33d33"
)
LOAD_SCRIPT = """
import json
import sys
import time
import urllib.request
from concurrent.futures import ThreadPoolExecutor

target, model, seconds, concurrency = sys.argv[1], sys.argv[2], int(sys.argv[3]), int(sys.argv[4])
url = "http://%s:8000/v1/chat/completions" % target
payload = json.dumps(
    {
        "model": model,
        "messages": [{"role": "user", "content": "ping"}],
        "max_tokens": 16,
        "temperature": 0,
    }
).encode()


def one():
    started = time.time()
    req = urllib.request.Request(url, data=payload, headers={"Content-Type": "application/json"}, method="POST")
    try:
        with urllib.request.urlopen(req, timeout=60) as resp:
            resp.read(256)
            return resp.getcode() == 200, int((time.time() - started) * 1000)
    except Exception:
        return False, int((time.time() - started) * 1000)


deadline = time.time() + seconds
ok = err = 0
lat = []
one()
with ThreadPoolExecutor(max_workers=concurrency) as pool:
    futs = []
    while time.time() < deadline:
        while len(futs) < concurrency:
            futs.append(pool.submit(one))
        done = [item for item in futs if item.done()]
        if not done:
            time.sleep(0.01)
            continue
        for item in done:
            success, ms = item.result()
            ok += 1 if success else 0
            err += 0 if success else 1
            lat.append(ms)
            futs.remove(item)
    for item in futs:
        success, ms = item.result()
        ok += 1 if success else 0
        err += 0 if success else 1
        lat.append(ms)
lat.sort()


def pct(p):
    if not lat:
        return 0
    return lat[min(len(lat) - 1, int(round((p / 100) * (len(lat) - 1))))]


print(
    json.dumps(
        {
            "ok": ok,
            "err": err,
            "n": ok + err,
            "qps": round((ok + err) / seconds, 3) if seconds else 0,
            "p50_ms": pct(50),
            "p99_ms": pct(99),
        }
    )
)
"""


def fail(message: str) -> None:
    raise SystemExit(f"inference gpu memory live gate failed: {message}")


def expect_http(status: int, wanted: int, label: str, body: dict[str, Any] | None = None) -> None:
    if status == wanted:
        return
    extra = ""
    if isinstance(body, dict):
        extra = live.redact_text(str(body.get("error_code") or body.get("code") or body.get("message") or ""))[:160]
    fail(f"{label}: status={status}, want={wanted} {extra}".strip())


def refresh_core_service_token(kubeconfig: str) -> None:
    issuer = c21.secret_bytes(kubeconfig, c21.AUTH_SECRET, "auth_jwt_issuer").decode("utf-8").strip()
    if not issuer:
        fail("auth-service issuer is not configured")
    private_key = c21.secret_bytes(kubeconfig, c21.AUTH_SECRET, "jwt_private_key_pem")
    now = int(time.time())
    claims = c21.service_claims(issuer, TENANT_ID, now)
    claims["exp"] = now + 14400
    service_token = c21.mint_jwt(private_key, claims)
    tmpdir = Path(tempfile.mkdtemp(prefix="ani-c39-core-"))
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


def container_requests(item: dict[str, Any]) -> dict[str, str]:
    containers = ((item.get("spec") or {}).get("template") or {}).get("spec") or {}
    if item.get("kind") == "Pod" or "containers" in (item.get("spec") or {}):
        containers = item.get("spec") or {}
    for container in containers.get("containers") or []:
        requests = ((container.get("resources") or {}).get("requests") or {})
        return {str(k): str(v) for k, v in requests.items()}
    return {}


def inspect_runtime(kubeconfig: str, service_id: str, mode: str) -> dict[str, str]:
    deploy = find_by_owner(list_named(kubeconfig, "deploy"), service_id)
    if deploy is None:
        fail(f"{mode} Deployment was not created")
    lws = find_by_owner(list_named(kubeconfig, "leaderworkersets.leaderworkerset.x-k8s.io"), service_id)
    if lws is not None:
        fail(f"single-node {mode} create rendered LeaderWorkerSet")
    requests = container_requests(deploy)
    if mode == "whole-card":
        if requests.get("nvidia.com/gpu") != "1":
            fail(f"whole-card requests = {requests}")
        if "volcano.sh/vgpu-number" in requests or "volcano.sh/vgpu-memory" in requests:
            fail("whole-card runtime requested volcano vGPU")
        return {"nvidia_gpu": requests["nvidia.com/gpu"]}
    if requests.get("volcano.sh/vgpu-number") != "1" or requests.get("volcano.sh/vgpu-memory") != "1228":
        fail(f"vGPU requests = {requests}")
    if "nvidia.com/gpu" in requests:
        fail("vGPU runtime requested nvidia.com/gpu")
    return {
        "vgpu_number": requests["volcano.sh/vgpu-number"],
        "vgpu_memory": requests["volcano.sh/vgpu-memory"],
    }


def apply_manifest(kubeconfig: str, manifest: dict[str, Any]) -> None:
    handle = tempfile.NamedTemporaryFile("w", suffix=".json", delete=False, encoding="utf-8")
    try:
        handle.write(json.dumps(manifest))
        handle.close()
        live.kubectl(kubeconfig, ["apply", "-f", handle.name], timeout=60)
    finally:
        Path(handle.name).unlink(missing_ok=True)


def ensure_clone_pvc(kubeconfig: str) -> None:
    found = live.run(["kubectl", "--kubeconfig", kubeconfig, "-n", TENANT_NS, "get", "pvc", CLONE_PVC], timeout=30)
    if found.returncode == 0:
        return
    snap = live.run(
        ["kubectl", "--kubeconfig", kubeconfig, "-n", TENANT_NS, "get", "volumesnapshot", CLONE_SNAP, "-o", "json"],
        timeout=30,
    )
    ready = False
    if snap.returncode == 0:
        try:
            ready = bool(((json.loads(snap.stdout or "{}").get("status") or {}).get("readyToUse")))
        except json.JSONDecodeError:
            ready = False
    if not ready:
        apply_manifest(
            kubeconfig,
            {
                "apiVersion": "snapshot.storage.k8s.io/v1",
                "kind": "VolumeSnapshot",
                "metadata": {"name": CLONE_SNAP, "namespace": TENANT_NS},
                "spec": {
                    "volumeSnapshotClassName": "csi-rbdplugin-snapclass",
                    "source": {"persistentVolumeClaimName": "vllm-model"},
                },
            },
        )
        deadline = time.time() + 300
        while time.time() < deadline:
            current = live.run(
                ["kubectl", "--kubeconfig", kubeconfig, "-n", TENANT_NS, "get", "volumesnapshot", CLONE_SNAP, "-o", "json"],
                timeout=30,
            )
            if current.returncode == 0:
                try:
                    payload = json.loads(current.stdout or "{}")
                except json.JSONDecodeError:
                    payload = {}
                if bool(((payload.get("status") or {}).get("readyToUse"))):
                    break
            time.sleep(3)
        else:
            fail("model PVC snapshot did not become ready")
    apply_manifest(
        kubeconfig,
        {
            "apiVersion": "v1",
            "kind": "PersistentVolumeClaim",
            "metadata": {"name": CLONE_PVC, "namespace": TENANT_NS},
            "spec": {
                "accessModes": ["ReadWriteOnce"],
                "storageClassName": "ani-rbd-ssd",
                "resources": {"requests": {"storage": "20Gi"}},
                "dataSource": {
                    "apiGroup": "snapshot.storage.k8s.io",
                    "kind": "VolumeSnapshot",
                    "name": CLONE_SNAP,
                },
            },
        },
    )


def current_artifact_pvc(kubeconfig: str, service_id: str) -> tuple[str, int]:
    deploy = find_by_owner(list_named(kubeconfig, "deploy"), service_id)
    if deploy is None:
        return "", -1
    volumes = (((deploy.get("spec") or {}).get("template") or {}).get("spec") or {}).get("volumes") or []
    for index, volume in enumerate(volumes):
        claim = str(((volume.get("persistentVolumeClaim") or {}).get("claimName") or ""))
        if claim:
            return claim, index
    return "", -1


def patch_artifact_pvc(kubeconfig: str, service_id: str, claim_name: str) -> None:
    claim, index = current_artifact_pvc(kubeconfig, service_id)
    if index < 0:
        fail(f"{service_id} runtime has no model PVC volume")
    if claim == claim_name:
        return
    deploy = find_by_owner(list_named(kubeconfig, "deploy"), service_id)
    live.kubectl(
        kubeconfig,
        [
            "-n",
            TENANT_NS,
            "patch",
            f"deploy/{object_name(deploy)}",
            "--type=json",
            "-p",
            json.dumps(
                [
                    {
                        "op": "replace",
                        "path": f"/spec/template/spec/volumes/{index}/persistentVolumeClaim/claimName",
                        "value": claim_name,
                    }
                ]
            ),
        ],
        timeout=60,
    )


def service_cluster_ip(kubeconfig: str, service_id: str) -> str:
    svc = find_by_owner(list_named(kubeconfig, "svc"), service_id)
    if svc is None:
        fail(f"{service_id} ClusterIP Service was not created")
    ip = str((svc.get("spec") or {}).get("clusterIP") or "")
    if not ip or ip in {"None", "none"}:
        fail(f"{service_id} ClusterIP is empty")
    return ip


def ensure_load_pod(kubeconfig: str) -> None:
    live.run(
        ["kubectl", "--kubeconfig", kubeconfig, "-n", TENANT_NS, "delete", "pod", LOAD_POD, "--ignore-not-found", "--wait=false"],
        timeout=60,
    )
    apply_manifest(
        kubeconfig,
        {
            "apiVersion": "v1",
            "kind": "Pod",
            "metadata": {"name": LOAD_POD, "namespace": TENANT_NS, "labels": {"ani.c39/load-client": "true"}},
            "spec": {
                "restartPolicy": "Never",
                "containers": [
                    {
                        "name": "load",
                        "image": LOAD_CLIENT_IMAGE,
                        "command": ["sleep", "7200"],
                        "resources": {
                            "requests": {"cpu": "100m", "memory": "128Mi"},
                            "limits": {"memory": "256Mi"},
                        },
                    }
                ],
            },
        },
    )
    live.kubectl(kubeconfig, ["-n", TENANT_NS, "wait", f"pod/{LOAD_POD}", "--for=condition=Ready", "--timeout=180s"], timeout=210)
    script = Path(tempfile.mkdtemp(prefix="ani-c39-load-")) / "load.py"
    try:
        script.write_text(LOAD_SCRIPT.lstrip(), encoding="utf-8")
        live.kubectl(kubeconfig, ["-n", TENANT_NS, "cp", str(script), f"{LOAD_POD}:/tmp/c39-load.py"], timeout=60)
    finally:
        live.run(["rm", "-rf", str(script.parent)])


def delete_load_pod(kubeconfig: str) -> None:
    live.run(
        ["kubectl", "--kubeconfig", kubeconfig, "-n", TENANT_NS, "delete", "pod", LOAD_POD, "--ignore-not-found", "--wait=false"],
        timeout=60,
    )


def wait_vllm_http(kubeconfig: str, cluster_ip: str, timeout: int = 600) -> None:
    deadline = time.time() + timeout
    while time.time() < deadline:
        probe = live.run(
            [
                "kubectl",
                "--kubeconfig",
                kubeconfig,
                "-n",
                TENANT_NS,
                "exec",
                LOAD_POD,
                "--",
                "python3",
                "-c",
                "import sys,urllib.request\n"
                "try:\n"
                " urllib.request.urlopen(sys.argv[1], timeout=5)\n"
                " print('200')\n"
                "except Exception:\n"
                " print('err')\n",
                f"http://{cluster_ip}:8000/v1/models",
            ],
            timeout=30,
        )
        if "200" in (probe.stdout or ""):
            return
        time.sleep(5)
    fail("runtime HTTP did not become ready")


def run_load_test(kubeconfig: str, cluster_ip: str, served: str, seconds: int, concurrency: int = 4) -> dict[str, Any]:
    completed = live.run(
        [
            "kubectl",
            "--kubeconfig",
            kubeconfig,
            "-n",
            TENANT_NS,
            "exec",
            LOAD_POD,
            "--",
            "python3",
            "/tmp/c39-load.py",
            cluster_ip,
            served,
            str(seconds),
            str(concurrency),
        ],
        timeout=seconds + 90,
    )
    if completed.returncode != 0:
        fail("load client failed: " + live.redact_text((completed.stderr or completed.stdout or "")[:200]))
    try:
        summary = json.loads((completed.stdout or "").strip().splitlines()[-1])
    except (json.JSONDecodeError, IndexError):
        fail("load client did not print a JSON summary")
    if not isinstance(summary, dict):
        fail("load client summary is not an object")
    out = {
        "ok": int(summary.get("ok") or 0),
        "err": int(summary.get("err") or 0),
        "n": int(summary.get("n") or 0),
        "qps": float(summary.get("qps") or 0),
        "p50_ms": int(summary.get("p50_ms") or 0),
        "p99_ms": int(summary.get("p99_ms") or 0),
        "seconds": seconds,
        "concurrency": concurrency,
        "probe": "clusterip",
    }
    if out["n"] <= 0 or out["ok"] <= 0:
        fail("load test recorded no successful chat completions")
    return out


def wait_product(base: str, token: str, service_id: str, wanted: str, refresh) -> dict[str, Any]:
    deadline = time.time() + 1200
    last: dict[str, Any] = {}
    while time.time() < deadline:
        try:
            refresh()
            status, body = live.request(
                "GET",
                f"{base}/api/v1/svc/inference-services/{service_id}",
                TENANT_ID,
                token=token,
            )
            if status == 200:
                last = body
                if body.get("status") == wanted:
                    return body
        except Exception as err:
            last = {"error": live.redact_text(str(err))[:120]}
        time.sleep(5)
    fail(f"inference service {service_id} did not reach {wanted}: {last.get('status') or last.get('error') or 'unknown'}")
    return last


def scale_deploy(kubeconfig: str, namespace: str, name: str, replicas: int) -> None:
    live.run(
        ["kubectl", "--kubeconfig", kubeconfig, "-n", namespace, "scale", f"deploy/{name}", f"--replicas={replicas}"],
        timeout=60,
    )


def wait_named_pods_gone(kubeconfig: str, namespace: str, name: str, timeout: int = 120) -> None:
    deadline = time.time() + timeout
    while time.time() < deadline:
        pods = live.run(
            ["kubectl", "--kubeconfig", kubeconfig, "-n", namespace, "get", "pods", "-l", f"ani.kubercloud.io/instance={name}", "-o", "json"],
            timeout=60,
        )
        items = []
        if pods.returncode == 0:
            try:
                items = list((json.loads(pods.stdout or "{}").get("items") or []))
            except json.JSONDecodeError:
                items = []
        if not items:
            deploys = live.run(
                ["kubectl", "--kubeconfig", kubeconfig, "-n", namespace, "get", "pods", "-o", "json"],
                timeout=60,
            )
            if deploys.returncode == 0:
                try:
                    payload = json.loads(deploys.stdout or "{}")
                except json.JSONDecodeError:
                    payload = {}
                items = [
                    pod
                    for pod in payload.get("items") or []
                    if name in str((pod.get("metadata") or {}).get("name") or "")
                ]
        if not items:
            return
        time.sleep(3)
    fail(f"{namespace}/{name} pods did not release")


def wait_runtime_gone(kubeconfig: str, service_id: str, timeout: int = 180) -> None:
    deadline = time.time() + timeout
    while time.time() < deadline:
        leftover = False
        for kind in ("deploy", "pods", "svc"):
            if find_by_owner(list_named(kubeconfig, kind), service_id) is not None:
                leftover = True
                break
        if not leftover:
            return
        time.sleep(3)
    fail(f"test runtime {service_id} did not release")


def wait_deleted(base: str, token: str, service_id: str) -> None:
    deadline = time.time() + 180
    while time.time() < deadline:
        status, _ = live.request(
            "GET",
            f"{base}/api/v1/svc/inference-services/{service_id}",
            TENANT_ID,
            token=token,
        )
        if status == 404:
            return
        time.sleep(3)
    fail(f"service {service_id} was not deleted")


def delete_service(base: str, token: str, kubeconfig: str, service_id: str) -> None:
    if not service_id or service_id in KEEP_SERVICE_IDS:
        return
    live.request(
        "DELETE",
        f"{base}/api/v1/svc/inference-services/{service_id}",
        TENANT_ID,
        token=token,
    )
    wait_deleted(base, token, service_id)
    for kind in ("deploy", "svc", "netpol", "pod"):
        for item in list_named(kubeconfig, kind):
            if not owned_by_service(item, service_id):
                continue
            live.run(
                ["kubectl", "--kubeconfig", kubeconfig, "-n", TENANT_NS, "delete", kind, object_name(item), "--wait=false"],
                timeout=60,
            )
    wait_runtime_gone(kubeconfig, service_id)


def advertised_specs(payload: dict[str, Any]) -> list[str]:
    items = payload.get("accelerator_specs") or payload.get("acceleratorSpecs") or []
    out: list[str] = []
    for item in items:
        if isinstance(item, dict):
            spec_id = str(item.get("spec_id") or item.get("specId") or "")
            if spec_id:
                out.append(spec_id)
    return out


def listed_items(payload: dict[str, Any]) -> list[dict[str, Any]]:
    items = payload.get("items") or payload.get("services") or []
    return [item for item in items if isinstance(item, dict)]


def create_and_verify(
    base: str,
    token: str,
    kubeconfig: str,
    refresh,
    *,
    name: str,
    served: str,
    image_ref: str,
    model: str,
    memory: int | None,
    artifact_pvc: str = "",
) -> tuple[str, dict[str, str], dict[str, Any]]:
    resources: dict[str, Any] = {
        "cpu": "4",
        "memory": "16Gi",
        "accelerator": {"spec_id": SPEC_ID, "count_per_replica": 1},
    }
    mode = "vgpu"
    if memory is None:
        mode = "whole-card"
    else:
        resources["accelerator"]["memory"] = memory
    body = {
        "idempotency_key": str(uuid.uuid4()),
        "name": name,
        "model": model,
        "model_version_id": model,
        "served_model_name": served,
        "replicas": 1,
        "placement_mode": "single_node",
        "image_ref": image_ref,
        "resources": resources,
    }
    print(f"c39: creating {mode} InferenceService", flush=True)
    status, created = live.request(
        "POST",
        f"{base}/api/v1/svc/inference-services",
        TENANT_ID,
        body,
        token=token,
    )
    expect_http(status, 202, f"product-create-{mode}", created)
    service_id = str(created.get("id") or "")
    if not service_id:
        fail(f"{mode} create did not return a service id")
    deadline = time.time() + 180
    runtime: dict[str, str] = {}
    last_error = f"{mode} runtime was not observed"
    while time.time() < deadline:
        try:
            runtime = inspect_runtime(kubeconfig, service_id, mode)
            break
        except SystemExit as err:
            last_error = str(err)
            time.sleep(5)
    else:
        fail(last_error)
    if artifact_pvc:
        patch_artifact_pvc(kubeconfig, service_id, artifact_pvc)

        def refresh_and_pvc() -> None:
            refresh()
            patch_artifact_pvc(kubeconfig, service_id, artifact_pvc)

        observed = wait_product(base, token, service_id, "running", refresh_and_pvc)
    else:
        observed = wait_product(base, token, service_id, "running", refresh)
    if observed.get("invocation_url") is not None or observed.get("endpoint_url") is not None:
        fail("product response leaked an invocation URL")
    return service_id, runtime, observed


def main() -> None:
    parser = argparse.ArgumentParser()
    parser.add_argument("--kubeconfig", default=os.environ.get("KUBECONFIG", str(Path.home() / ".kube/config")))
    parser.add_argument("--listen", default="127.0.0.1:18089")
    parser.add_argument("--keep", action="store_true", help="leave whole-card and vGPU InferenceServices running")
    parser.add_argument("--load-seconds", type=int, default=0, help="ClusterIP chat-completion soak seconds per service")
    args = parser.parse_args()
    kubeconfig = args.kubeconfig
    if not Path(kubeconfig).exists():
        fail("kubeconfig is missing")
    if args.load_seconds < 0:
        fail("load-seconds must be >= 0")

    checks: list[dict[str, Any]] = []
    forward: subprocess.Popen[str] | None = None
    test_service_id = ""
    whole_service_id = ""
    vgpu_service_id = ""
    token = ""
    base = ""
    stopped_cpu = False
    stopped_instance = False
    load_pod_created = False
    image_ref = ""
    model = ""
    whole_runtime: dict[str, str] = {}
    vgpu_runtime: dict[str, str] = {}
    whole_load: dict[str, Any] = {}
    vgpu_load: dict[str, Any] = {}
    whole_served = ""
    vgpu_served = ""
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
        print("c39: refreshing core service token", flush=True)
        refresh_core_service_token(kubeconfig)
        checks.append({"id": "production-gateway-auth-mode-preserved", "status": "passed", "auth_mode": "auth_service"})

        issuer = c21.secret_bytes(kubeconfig, c21.AUTH_SECRET, "auth_jwt_issuer").decode("utf-8").strip()
        if not issuer:
            fail("auth-service issuer is not configured")
        private_key = c21.secret_bytes(kubeconfig, c21.AUTH_SECRET, "jwt_private_key_pem")
        now = int(time.time())
        token = c21.mint_jwt(private_key, c21.tenant_claims(issuer, TENANT_ID, str(uuid.uuid4()), now))
        service_token = c21.mint_jwt(private_key, c21.service_claims(issuer, TENANT_ID, now))
        private_key = b""

        host, port = args.listen.split(":")

        def ensure_forward() -> None:
            nonlocal forward
            if forward is not None and forward.poll() is None:
                try:
                    live.wait_tcp(host, int(port), timeout=2)
                    return
                except SystemExit:
                    forward.send_signal(signal.SIGTERM)
                    try:
                        forward.wait(timeout=5)
                    except subprocess.TimeoutExpired:
                        forward.kill()
            forward = subprocess.Popen(
                ["kubectl", "--kubeconfig", kubeconfig, "-n", c21.GATEWAY_NS, "port-forward", f"svc/{c21.PRODUCTION_GATEWAY}", f"{port}:8080"],
                stdout=subprocess.DEVNULL,
                stderr=subprocess.DEVNULL,
            )
            live.wait_tcp(host, int(port), timeout=30)

        ensure_forward()
        base = "http://" + args.listen
        live.wait_http(f"{base}/readyz", timeout=60)
        ready_deadline = time.time() + 60
        listed: dict[str, Any] = {}
        while time.time() < ready_deadline:
            list_status, listed = live.request(
                "GET",
                f"{base}/api/v1/svc/inference-services",
                TENANT_ID,
                token=token,
            )
            if list_status == 200:
                break
            time.sleep(3)
        else:
            expect_http(list_status, 200, "list-inference-services", listed)

        old_status, old = live.request(
            "GET",
            f"{base}/api/v1/svc/inference-services/{OLD_GPU_ID}",
            TENANT_ID,
            token=token,
        )
        if old_status == 200:
            image_ref = str(old.get("image_ref") or "")
            model = str(old.get("model_version_id") or old.get("model") or "")

        if not image_ref or not model:
            for item in listed_items(listed):
                ref = str(item.get("image_ref") or "")
                mid = str(item.get("model_version_id") or item.get("model") or "")
                if "@sha256:" in ref and "cpu" not in ref and mid:
                    image_ref, model = ref, mid
                    break
        if "@sha256:" not in image_ref:
            image_ref = CUDA_IMAGE_REF
        if not model:
            cpu_status, cpu = live.request(
                "GET",
                f"{base}/api/v1/svc/inference-services/{CPU_PVC_HOLDER_ID}",
                TENANT_ID,
                token=token,
            )
            if cpu_status == 200:
                model = str(cpu.get("model_version_id") or cpu.get("model") or "")
        if "@sha256:" not in image_ref or not model:
            fail("no digest-pinned CUDA image_ref available after cleanup inventory")

        print("c39: cleaning leftover inference test services", flush=True)
        to_delete: set[str] = set()
        if old_status == 200:
            to_delete.add(OLD_GPU_ID)
        for item in listed_items(listed):
            service_id = str(item.get("id") or "")
            name = str(item.get("name") or "")
            if service_id in KEEP_SERVICE_IDS:
                continue
            if any(name.startswith(prefix) for prefix in CLEAN_NAME_PREFIXES):
                to_delete.add(service_id)
        for item in list_named(kubeconfig, "deploy"):
            sid = str(object_labels(item).get("ani.kubercloud.io/platform-workload-name") or "")
            if sid and sid not in KEEP_SERVICE_IDS:
                to_delete.add(sid)
        for service_id in sorted(to_delete):
            print("c39: deleting leftover " + service_id, flush=True)
            delete_service(base, token, kubeconfig, service_id)
        live.run(["kubectl", "--kubeconfig", kubeconfig, "-n", TENANT_NS, "delete", "pod", "aigw-curl", "--ignore-not-found"], timeout=60)
        checks.append({"id": "leftover-test-services-cleaned", "status": "passed"})

        caps_status, caps = live.request(
            "GET",
            f"{base}/api/v1/platform-workload-capabilities",
            TENANT_ID,
            token=service_token,
        )
        expect_http(caps_status, 200, "platform-workload-capabilities", caps)
        specs = advertised_specs(caps)
        if SPEC_ID not in specs:
            fail(f"capabilities missing model spec {SPEC_ID}: {specs}")
        for spec in specs:
            if spec.endswith("-full") or spec.endswith("x"):
                fail(f"capabilities still advertise historical spec {spec}")
        checks.append({"id": "capabilities-advertise-model-spec", "status": "passed", "spec_id": SPEC_ID})

        zero_status, zero = live.request(
            "POST",
            f"{base}/api/v1/svc/inference-services",
            TENANT_ID,
            {
                "idempotency_key": str(uuid.uuid4()),
                "name": "inf-c39-zero-memory",
                "model": model,
                "model_version_id": model,
                "replicas": 1,
                "image_ref": image_ref,
                "resources": {
                    "cpu": "4",
                    "memory": "16Gi",
                    "accelerator": {"spec_id": SPEC_ID, "count_per_replica": 1, "memory": 0},
                },
            },
            token=token,
        )
        expect_http(zero_status, 400, "memory-zero", zero)
        code = str(zero.get("code") or zero.get("error_code") or "")
        if code and code not in {"INVALID_ARGUMENT", "BAD_REQUEST"}:
            fail(f"memory 0 returned unexpected code {code}")
        checks.append({"id": "memory-zero-400", "status": "passed", "http_status": 400, "code": code or "INVALID_ARGUMENT"})

        cpu_deploy = find_by_owner(list_named(kubeconfig, "deploy"), CPU_PVC_HOLDER_ID)
        if cpu_deploy is not None:
            print("c39: scaling CPU runtime to 0 to free the model PVC", flush=True)
            live.kubectl(kubeconfig, ["-n", TENANT_NS, "scale", f"deploy/{object_name(cpu_deploy)}", "--replicas=0"])
            scale_deadline = time.time() + 120
            while time.time() < scale_deadline:
                pods = live.kubectl_json(kubeconfig, ["-n", TENANT_NS, "get", "pods"])
                held = [pod for pod in pods.get("items") or [] if owned_by_service(pod, CPU_PVC_HOLDER_ID)]
                if not held:
                    break
                time.sleep(3)
            else:
                fail("CPU runtime pods did not release the model PVC")
            stopped_cpu = True

        print("c39: scaling instance whole-card blocker to 0", flush=True)
        scale_deploy(kubeconfig, INSTANCE_NS, WHOLE_CARD_INSTANCE, 0)
        wait_named_pods_gone(kubeconfig, INSTANCE_NS, WHOLE_CARD_INSTANCE)
        stopped_instance = True

        if args.keep:
            print("c39: cloning model PVC so whole-card and vGPU can stay running together", flush=True)
            ensure_clone_pvc(kubeconfig)

        suffix = uuid.uuid4().hex[:8]
        whole_served = "qwen2.5-0.5b-whole-c39"
        vgpu_served = "qwen2.5-0.5b-vgpu-c39"
        test_service_id, whole_runtime, _ = create_and_verify(
            base,
            token,
            kubeconfig,
            ensure_forward,
            name="inf-c39-whole-" + suffix,
            served=whole_served,
            image_ref=image_ref,
            model=model,
            memory=None,
        )
        whole_service_id = test_service_id
        checks.append({"id": "product-create-whole-card-202", "status": "passed", "http_status": 202})
        checks.append({"id": "runtime-nvidia-gpu", "status": "passed", "nvidia_gpu": whole_runtime.get("nvidia_gpu")})
        checks.append({"id": "runtime-no-vgpu-on-whole-card", "status": "passed"})
        checks.append({"id": "invocation-url-null", "status": "passed"})
        if args.keep:
            checks.append({"id": "services-kept-running", "status": "passed"})
        else:
            print("c39: deleting whole-card test service", flush=True)
            delete_service(base, token, kubeconfig, test_service_id)
            test_service_id = ""
            whole_service_id = ""
            checks.append({"id": "whole-card-deleted", "status": "passed"})

        test_service_id, vgpu_runtime, _ = create_and_verify(
            base,
            token,
            kubeconfig,
            ensure_forward,
            name="inf-c39-vgpu-" + suffix,
            served=vgpu_served,
            image_ref=image_ref,
            model=model,
            memory=MEMORY_MIB,
            artifact_pvc=CLONE_PVC if args.keep else "",
        )
        vgpu_service_id = test_service_id
        checks.append({"id": "product-create-vgpu-202", "status": "passed", "http_status": 202})
        checks.append({"id": "runtime-volcano-vgpu-resources", "status": "passed", **vgpu_runtime})
        checks.append({"id": "runtime-no-nvidia-gpu", "status": "passed"})
        if args.keep:
            checks.append({"id": "vgpu-kept-running", "status": "passed"})
        else:
            print("c39: deleting vGPU test service", flush=True)
            delete_service(base, token, kubeconfig, test_service_id)
            test_service_id = ""
            vgpu_service_id = ""
            checks.append({"id": "test-service-deleted", "status": "passed"})
        checks.append({"id": "lws-cross-node-skipped", "status": "passed", "reason": "skipped_cross_node_lws"})

        if args.load_seconds > 0:
            if not whole_service_id or not vgpu_service_id:
                fail("load test requires both whole-card and vGPU services")
            print("c39: starting same-namespace load client", flush=True)
            ensure_load_pod(kubeconfig)
            load_pod_created = True
            whole_ip = service_cluster_ip(kubeconfig, whole_service_id)
            vgpu_ip = service_cluster_ip(kubeconfig, vgpu_service_id)
            print("c39: waiting for whole-card runtime HTTP", flush=True)
            wait_vllm_http(kubeconfig, whole_ip)
            print(f"c39: load-testing whole-card for {args.load_seconds}s", flush=True)
            whole_load = run_load_test(kubeconfig, whole_ip, whole_served, args.load_seconds)
            print("c39: waiting for vGPU runtime HTTP", flush=True)
            wait_vllm_http(kubeconfig, vgpu_ip)
            print(f"c39: load-testing vGPU for {args.load_seconds}s", flush=True)
            vgpu_load = run_load_test(kubeconfig, vgpu_ip, vgpu_served, args.load_seconds)
            checks.append({"id": "clusterip-load-whole-card", "status": "passed", **whole_load})
            checks.append({"id": "clusterip-load-vgpu", "status": "passed", **vgpu_load})
            whole_ip = ""
            vgpu_ip = ""

        if stopped_cpu and not args.keep:
            print("c39: restoring CPU runtime replicas", flush=True)
            live.kubectl(kubeconfig, ["-n", TENANT_NS, "scale", f"deploy/{CPU_PVC_HOLDER_ID}", "--replicas=1"])
            live.kubectl(
                kubeconfig,
                ["-n", TENANT_NS, "rollout", "status", f"deploy/{CPU_PVC_HOLDER_ID}", "--timeout=180s"],
                timeout=210,
            )
            stopped_cpu = False
        if stopped_instance and not args.keep:
            print("c39: restoring instance whole-card blocker", flush=True)
            scale_deploy(kubeconfig, INSTANCE_NS, WHOLE_CARD_INSTANCE, 1)
            stopped_instance = False

        evidence = {
            "profile": PROFILE,
            "status": "passed",
            "generated_at": dt.datetime.now(dt.timezone.utc).strftime("%Y-%m-%dT%H:%M:%SZ"),
            "entry": "ani-system/ani-gateway",
            "gateway": "ani-system/ani-gateway",
            "auth_mode": "auth_service",
            "auth": "tenant_bearer",
            "gateway_image": "ani/ani-gateway:gpu-memory-c39-20260821",
            "inference_service_image": "ani/inference-service:gpu-memory-c39-20260821",
            "advertised_spec": SPEC_ID,
            "advertised_specs": specs,
            "memory_mib": MEMORY_MIB,
            "memory_zero_http_status": 400,
            "whole_card_accelerator": "nvidia.com/gpu=1",
            "whole_card_nvidia_gpu": whole_runtime.get("nvidia_gpu"),
            "vgpu_on_whole_card": False,
            "accelerator": "volcano.sh/vgpu-number=1,volcano.sh/vgpu-memory=1228",
            "vgpu_number": vgpu_runtime.get("vgpu_number"),
            "vgpu_memory": vgpu_runtime.get("vgpu_memory"),
            "nvidia_gpu_on_vgpu": False,
            "product_status": "running",
            "invocation_url": None,
            "endpoint_url": None,
            "leftover_cleaned": True,
            "test_service_deleted": not args.keep,
            "services_kept": args.keep,
            "cpu_service_restored": not args.keep,
            "instance_restored": not args.keep,
            "load_seconds": args.load_seconds,
            "load_whole_card": whole_load or None,
            "load_vgpu": vgpu_load or None,
            "lws_live": "skipped",
            "gpu_ready": False,
            "runtime_ready": False,
            "checks": checks,
        }
        live.assert_clean_evidence(evidence)
        target = KEEP_EVIDENCE if args.keep else EVIDENCE
        target.parent.mkdir(parents=True, exist_ok=True)
        target.write_text(json.dumps(evidence, indent=2, ensure_ascii=True) + "\n", encoding="utf-8")
        print("c39: evidence written", flush=True)
    finally:
        if load_pod_created:
            delete_load_pod(kubeconfig)
        if not args.keep:
            if test_service_id and token and base:
                delete_service(base, token, kubeconfig, test_service_id)
            if stopped_cpu:
                live.run(
                    [
                        "kubectl",
                        "--kubeconfig",
                        kubeconfig,
                        "-n",
                        TENANT_NS,
                        "scale",
                        f"deploy/{CPU_PVC_HOLDER_ID}",
                        "--replicas=1",
                    ],
                    timeout=60,
                )
            if stopped_instance:
                scale_deploy(kubeconfig, INSTANCE_NS, WHOLE_CARD_INSTANCE, 1)
        if forward is not None and forward.poll() is None:
            forward.send_signal(signal.SIGTERM)
            try:
                forward.wait(timeout=10)
            except subprocess.TimeoutExpired:
                forward.kill()


if __name__ == "__main__":
    main()
