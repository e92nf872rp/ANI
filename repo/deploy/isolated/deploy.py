#!/usr/bin/env python3
"""ANI isolated deployment — single entrypoint (co-located with deploy YAML).

Deploy order (dependencies):
  render → mirror → foundation (KubeVirt → CDI → Rook → Ceph SC → Harbor) → base-infra (PVC) → business → verify

Usage (from repo root):
  python3 deploy/isolated/deploy.py deploy [dev]
  python3 deploy/isolated/deploy.py deploy dev --build
  python3 deploy/isolated/deploy.py deploy dev --skip-mirror
  python3 deploy/isolated/deploy.py deploy --only foundation
  python3 deploy/isolated/deploy.py cleanup
  python3 deploy/isolated/deploy.py verify
  python3 deploy/isolated/deploy.py render
  python3 deploy/isolated/deploy.py mirror [--scope all]
"""

from __future__ import annotations

import argparse
import json
import os
import shutil
import subprocess
import sys
import tempfile
import time
import urllib.error
import urllib.parse
import urllib.request
from pathlib import Path

DEPLOY = Path(__file__).resolve().parent
ROOT = DEPLOY.parents[1]
CF = DEPLOY / "cluster-foundation"
YAML_DIR = CF / "yaml"
CHART = CF / "helm" / "ani-cluster-foundation"
REGISTRY_MIRROR = os.environ.get("REGISTRY", "docker.changqingyun.cn/mirror")
REGISTRY_ANI = os.environ.get("REGISTRY_ANI", "docker.changqingyun.cn/ani")
DOCKER_CONFIG = Path(os.environ.get("DOCKER_CONFIG", Path.home() / ".docker/config.json"))
STORAGE_CLASS = "ani-rbd-ssd"
DB_INIT_SQL = ROOT / "deploy" / "postgres" / "ani-dev-database-init.sql"
DB_ALIGNMENT_SQLS = [
    ROOT / "deploy" / "migrations" / "20260706_001_instance_network_selection.sql",
]

BASE_MIRRORS = [
    ("dockerproxy.net/library/busybox:latest", "busybox:latest"),
    ("ghcr.io/cloudnative-pg/postgresql:15.5-1", "cloudnative-pg/postgresql:15.5-1"),
    ("dockerproxy.net/library/redis:8.2.3-alpine", "redis:8.2.3-alpine"),
    ("dockerproxy.net/library/nats:2.10-alpine", "nats:2.10-alpine"),
    ("dockerproxy.net/minio/minio:RELEASE.2025-09-07T16-13-09Z", "minio/minio:RELEASE.2025-09-07T16-13-09Z"),
    ("quay.io/coreos/etcd:v3.5.5", "coreos/etcd:v3.5.5"),
    ("dockerproxy.net/milvusdb/milvus:v2.4.15", "milvusdb/milvus:v2.4.15"),
    ("dockerproxy.net/prom/prometheus:v2.39.1", "prom/prometheus:v2.39.1"),
    ("ghcr.io/dexidp/dex:v2.40.0", "dexidp/dex:v2.40.0"),
    ("dockerproxy.net/zilliz/attu:v2.4", "zilliz/attu:v2.4"),
]

FOUNDATION_MIRRORS = [
    ("quay.io/kubevirt/virt-operator:v1.8.2", "kubevirt/virt-operator:v1.8.2"),
    ("quay.io/kubevirt/virt-api:v1.8.2", "kubevirt/virt-api:v1.8.2"),
    ("quay.io/kubevirt/virt-controller:v1.8.2", "kubevirt/virt-controller:v1.8.2"),
    ("quay.io/kubevirt/virt-handler:v1.8.2", "kubevirt/virt-handler:v1.8.2"),
    ("quay.io/kubevirt/virt-launcher:v1.8.2", "kubevirt/virt-launcher:v1.8.2"),
    ("quay.io/kubevirt/cdi-operator:v1.65.0", "kubevirt/cdi-operator:v1.65.0"),
    ("quay.io/kubevirt/cdi-controller:v1.65.0", "kubevirt/cdi-controller:v1.65.0"),
    ("quay.io/kubevirt/cdi-apiserver:v1.65.0", "kubevirt/cdi-apiserver:v1.65.0"),
    ("quay.io/kubevirt/cdi-uploadproxy:v1.65.0", "kubevirt/cdi-uploadproxy:v1.65.0"),
    ("quay.io/kubevirt/cdi-importer:v1.65.0", "kubevirt/cdi-importer:v1.65.0"),
    ("quay.io/kubevirt/cdi-uploadserver:v1.65.0", "kubevirt/cdi-uploadserver:v1.65.0"),
    ("quay.io/kubevirt/cdi-cloner:v1.65.0", "kubevirt/cdi-cloner:v1.65.0"),
    ("dockerproxy.net/rook/ceph:v1.20.0", "rook/ceph:v1.20.0"),
    ("quay.io/ceph/ceph:v19.2.3", "ceph/ceph:v19.2.3"),
    ("quay.io/cephcsi/ceph-csi-operator:v1.0.1", "cephcsi/ceph-csi-operator:v1.0.1"),
    ("quay.io/cephcsi/cephcsi:v3.17.0", "cephcsi/cephcsi:v3.17.0"),
    ("registry.k8s.io/sig-storage/csi-node-driver-registrar:v2.17.0", "sig-storage/csi-node-driver-registrar:v2.17.0"),
    ("registry.k8s.io/sig-storage/csi-provisioner:v6.2.0", "sig-storage/csi-provisioner:v6.2.0"),
    ("registry.k8s.io/sig-storage/csi-snapshotter:v8.5.0", "sig-storage/csi-snapshotter:v8.5.0"),
    ("registry.k8s.io/sig-storage/csi-attacher:v4.12.0", "sig-storage/csi-attacher:v4.12.0"),
    ("registry.k8s.io/sig-storage/csi-resizer:v2.1.0", "sig-storage/csi-resizer:v2.1.0"),
    ("quay.io/csiaddons/k8s-sidecar:v0.14.0", "csiaddons/k8s-sidecar:v0.14.0"),
    ("dockerproxy.net/goharbor/harbor-core:v2.15.1", "goharbor/harbor-core:v2.15.1"),
    ("dockerproxy.net/goharbor/harbor-jobservice:v2.15.1", "goharbor/harbor-jobservice:v2.15.1"),
    ("dockerproxy.net/goharbor/nginx-photon:v2.15.1", "goharbor/nginx-photon:v2.15.1"),
    ("dockerproxy.net/goharbor/harbor-portal:v2.15.1", "goharbor/harbor-portal:v2.15.1"),
    ("dockerproxy.net/goharbor/registry-photon:v2.15.1", "goharbor/registry-photon:v2.15.1"),
    ("dockerproxy.net/goharbor/harbor-registryctl:v2.15.1", "goharbor/harbor-registryctl:v2.15.1"),
    ("dockerproxy.net/goharbor/harbor-db:v2.15.1", "goharbor/harbor-db:v2.15.1"),
    ("dockerproxy.net/goharbor/redis-photon:v2.15.1", "goharbor/redis-photon:v2.15.1"),
    ("dockerproxy.net/goharbor/trivy-adapter-photon:v2.15.1", "goharbor/trivy-adapter-photon:v2.15.1"),
]

ANI_IMAGES = (
    "ani-gateway",
    "ani-auth-service",
    "model-service",
    "task-service",
    "reconcile-worker",
)


class DeployError(Exception):
    pass


def log(msg: str) -> None:
    print(f"==> {msg}", flush=True)


def run(
    cmd: list[str],
    *,
    check: bool = True,
    cwd: Path | None = None,
    env: dict[str, str] | None = None,
    timeout: int | None = None,
    input: str | None = None,
    quiet: bool = False,
) -> subprocess.CompletedProcess[str]:
    merged = {**os.environ, **(env or {})}
    proc = subprocess.run(
        cmd,
        cwd=cwd or ROOT,
        env=merged,
        text=True,
        capture_output=True,
        timeout=timeout,
        input=input,
    )
    if proc.stdout and not quiet:
        print(proc.stdout, end="")
    if proc.stderr and not quiet:
        print(proc.stderr, end="", file=sys.stderr)
    if check and proc.returncode != 0:
        raise DeployError(f"command failed ({proc.returncode}): {' '.join(cmd)}")
    return proc


def kubectl(
    *args: str,
    check: bool = True,
    timeout: int | None = None,
    quiet: bool = False,
) -> subprocess.CompletedProcess[str]:
    return run(["kubectl", *args], check=check, timeout=timeout, quiet=quiet)


def helm(
    *args: str,
    cwd: Path | None = None,
    check: bool = True,
    quiet: bool = False,
) -> subprocess.CompletedProcess[str]:
    return run(["helm", *args], cwd=cwd, check=check, quiet=quiet)


def require_cmd(name: str) -> None:
    if shutil.which(name) is None:
        raise DeployError(f"missing required command: {name}")


def require_cluster() -> None:
    require_cmd("kubectl")
    try:
        kubectl("cluster-info", check=True, timeout=30)
    except (DeployError, subprocess.TimeoutExpired) as exc:
        raise DeployError("kubectl cannot reach cluster; check kubeconfig") from exc


def node_ip() -> str:
    proc = kubectl("get", "nodes", "-o", "json", check=True, quiet=True)
    data = json.loads(proc.stdout)
    if not data.get("items"):
        raise DeployError("no nodes found in cluster")
    for addr in data["items"][0]["status"]["addresses"]:
        if addr["type"] == "InternalIP":
            return addr["address"]
    raise DeployError("cannot determine node InternalIP")


def kubectl_apply(path: Path) -> None:
    kubectl("apply", "-f", str(path))


def kubectl_delete(path: Path, *, timeout: str = "60s") -> None:
    kubectl("delete", "-f", str(path), "--ignore-not-found", f"--timeout={timeout}", check=False)


def ensure_host_alias(namespace: str, deployment: str, hostname: str, ip: str) -> None:
    patch = {
        "spec": {
            "template": {
                "spec": {
                    "hostAliases": [
                        {
                            "ip": ip,
                            "hostnames": [hostname],
                        }
                    ]
                }
            }
        }
    }
    kubectl(
        "-n", namespace,
        "patch", "deployment", deployment,
        "--type=merge",
        "-p", json.dumps(patch),
    )


def rollout(namespace: str, kind: str, name: str, timeout: str = "300s") -> None:
    kubectl("-n", namespace, "rollout", "status", f"{kind}/{name}", f"--timeout={timeout}")


def wait_crd(name: str, timeout: str = "300s") -> None:
    kubectl("wait", "--for=condition=Established", f"crd/{name}", f"--timeout={timeout}")


def wait_kubevirt_crd() -> None:
    for _ in range(60):
        proc = kubectl("get", "crd", "kubevirts.kubevirt.io", check=False)
        if proc.returncode == 0:
            wait_crd("kubevirts.kubevirt.io")
            return
        time.sleep(5)
    raise DeployError("timeout waiting for kubevirts.kubevirt.io CRD")


def wait_cdi_crd(name: str) -> None:
    log(f"foundation: waiting for CDI CRD {name}")
    for _ in range(60):
        proc = kubectl("get", "crd", name, check=False)
        if proc.returncode == 0:
            wait_crd(name)
            return
        time.sleep(5)
    raise DeployError(f"timeout waiting for {name} CRD")


def get_k8s_nodes() -> list[str]:
    proc = kubectl("get", "nodes", "-o", "json", check=True, quiet=True)
    data = json.loads(proc.stdout)
    return [item["metadata"]["name"] for item in data.get("items", [])]


def wait_storage_class(name: str, timeout: int = 900) -> None:
    log(f"waiting for StorageClass {name}")
    for _ in range(timeout // 5):
        if kubectl("get", "storageclass", name, check=False).returncode == 0:
            print(f"✅ StorageClass {name} ready")
            return
        time.sleep(5)
    raise DeployError(f"timeout waiting for StorageClass {name}")


def set_default_storage_class(name: str = STORAGE_CLASS) -> None:
    log(f"setting default StorageClass {name}")
    kubectl(
        "annotate", "storageclass", name,
        "storageclass.kubernetes.io/is-default-class=true",
        "--overwrite",
    )
    if name != "local":
        kubectl(
            "annotate", "storageclass", "local",
            "storageclass.kubernetes.io/is-default-class=false",
            "--overwrite",
            check=False,
        )
    print(f"✅ StorageClass {name} is default")


def require_storage_class(name: str = STORAGE_CLASS) -> None:
    if kubectl("get", "storageclass", name, check=False).returncode != 0:
        raise DeployError(
            f"StorageClass {name} not found; run foundation with Ceph first "
            f"(deploy --only foundation)"
        )


def init_database(namespace: str) -> None:
    log("base-infra: initialize Postgres schema")
    if not DB_INIT_SQL.exists():
        raise DeployError(f"database init SQL not found: {DB_INIT_SQL}")
    probe = run(
        [
            "kubectl", "-n", namespace,
            "exec", "-i", "ani-postgres-0", "--",
            "psql", "-U", "ani", "-d", "ani", "-tAc",
            (
                "SELECT CASE WHEN "
                "to_regclass('public.tenants') IS NOT NULL "
                "AND to_regclass('public.roles') IS NOT NULL "
                "AND to_regclass('public.refresh_tokens') IS NOT NULL "
                "THEN 'initialized' ELSE 'missing' END"
            ),
        ],
        quiet=True,
    )
    if probe.stdout.strip() == "initialized":
        print("✅ database schema already initialized")
        align_database_schema(namespace)
        return
    sql = DB_INIT_SQL.read_text(encoding="utf-8")
    run(
        [
            "kubectl", "-n", namespace,
            "exec", "-i", "ani-postgres-0", "--",
            "psql", "-U", "ani", "-d", "ani", "-v", "ON_ERROR_STOP=1",
        ],
        input=sql,
        quiet=True,
    )
    align_database_schema(namespace)
    print("✅ database schema initialized")


def align_database_schema(namespace: str) -> None:
    for sql_path in DB_ALIGNMENT_SQLS:
        if not sql_path.exists():
            raise DeployError(f"database alignment SQL not found: {sql_path}")
        run(
            [
                "kubectl", "-n", namespace,
                "exec", "-i", "ani-postgres-0", "--",
                "psql", "-U", "ani", "-d", "ani", "-v", "ON_ERROR_STOP=1",
            ],
            input=sql_path.read_text(encoding="utf-8"),
            quiet=True,
        )


def prep_ceph_osd_devices() -> None:
    log("foundation: prepare OSD backing (50Gi block device per node)")
    kubectl_apply(CF / "ceph-osd-prep.yaml")
    rollout("rook-ceph", "ds", "ceph-osd-prep", timeout="300s")


def discover_osd_block_devices() -> dict[str, str]:
    devices: dict[str, str] = {}
    for node in get_k8s_nodes():
        proc = kubectl(
            "get", "pods", "-n", "rook-ceph",
            "-l", "app.kubernetes.io/name=ceph-osd-prep",
            "--field-selector", f"spec.nodeName={node}",
            "-o", "jsonpath={.items[0].metadata.name}",
            check=False,
        )
        pod = proc.stdout.strip()
        if not pod:
            raise DeployError(f"ceph-osd-prep pod missing on node {node}")
        proc2 = kubectl(
            "exec", "-n", "rook-ceph", pod, "--",
            "cat", "/host/var/lib/rook/osd-block-device",
        )
        dev = proc2.stdout.strip()
        if not dev:
            raise DeployError(f"OSD block device not ready on {node}")
        devices[node] = dev
        print(f"   {node}: {dev}")
    return devices


def wait_ceph_osds(min_count: int = 1, timeout: int = 900) -> None:
    log(f"waiting for >= {min_count} Ceph OSD pod(s)")
    for _ in range(timeout // 10):
        proc = kubectl(
            "-n", "rook-ceph", "get", "pods",
            "-l", "app=rook-ceph-osd",
            "--field-selector=status.phase=Running",
            "-o", "name",
            check=False,
        )
        count = len([ln for ln in proc.stdout.splitlines() if ln.strip()])
        if count >= min_count:
            print(f"✅ {count} OSD pod(s) running")
            return
        time.sleep(10)
    raise DeployError(f"timeout waiting for Ceph OSD pods (need>={min_count})")


def wait_ceph_blockpool(name: str = "ceph-rbd-ssd", timeout: int = 900) -> None:
    log(f"waiting for CephBlockPool {name} Ready")
    for _ in range(timeout // 10):
        proc = kubectl(
            "-n", "rook-ceph", "get", "cephblockpool", name,
            "-o", "jsonpath={.status.phase}",
            check=False,
        )
        if proc.stdout.strip() == "Ready":
            print(f"✅ CephBlockPool {name} ready")
            return
        time.sleep(10)
    raise DeployError(f"timeout waiting for CephBlockPool {name}")


def wait_k8s_resource(namespace: str, kind: str, name: str, timeout: int = 300) -> None:
    for _ in range(timeout // 5):
        if kubectl("-n", namespace, "get", kind, name, check=False, quiet=True).returncode == 0:
            return
        time.sleep(5)
    raise DeployError(f"timeout waiting for {namespace}/{kind}/{name}")


def wait_csi_rbd_driver(timeout: int = 600) -> None:
    log("waiting for RBD CSI driver")
    wait_k8s_resource("rook-ceph", "deploy", "rook-ceph.rbd.csi.ceph.com-ctrlplugin", timeout=timeout)
    wait_k8s_resource("rook-ceph", "ds", "rook-ceph.rbd.csi.ceph.com-nodeplugin", timeout=timeout)
    rollout("rook-ceph", "deploy", "rook-ceph.rbd.csi.ceph.com-ctrlplugin", timeout=f"{timeout}s")
    rollout("rook-ceph", "ds", "rook-ceph.rbd.csi.ceph.com-nodeplugin", timeout=f"{timeout}s")
    for _ in range(timeout // 5):
        proc = kubectl("get", "csidriver", "rook-ceph.rbd.csi.ceph.com", check=False, quiet=True)
        if proc.returncode == 0:
            print("✅ RBD CSI driver registered")
            return
        time.sleep(5)
    raise DeployError("timeout waiting for rook-ceph.rbd.csi.ceph.com CSIDriver")


def ensure_rbd_csi_driver() -> None:
    log("foundation: RBD CSI Driver + RBAC")
    manifests = [
        {
            "apiVersion": "rbac.authorization.k8s.io/v1",
            "kind": "ClusterRole",
            "metadata": {
                "name": "ani-rook-rbd-csi-plugin",
                "labels": {"ani.deploy/profile": "isolated"},
            },
            "rules": [
                {"apiGroups": [""], "resources": ["nodes", "pods"], "verbs": ["get", "list", "watch"]},
                {
                    "apiGroups": [""],
                    "resources": ["persistentvolumes"],
                    "verbs": ["get", "list", "watch", "create", "delete", "patch", "update"],
                },
                {
                    "apiGroups": [""],
                    "resources": ["persistentvolumeclaims"],
                    "verbs": ["get", "list", "watch", "update"],
                },
                {
                    "apiGroups": [""],
                    "resources": ["events"],
                    "verbs": ["get", "list", "watch", "create", "patch", "update"],
                },
                {"apiGroups": [""], "resources": ["secrets", "configmaps"], "verbs": ["get", "list", "watch"]},
                {
                    "apiGroups": ["storage.k8s.io"],
                    "resources": ["storageclasses", "csinodes", "volumeattachments"],
                    "verbs": ["get", "list", "watch", "create", "delete", "patch", "update"],
                },
                {
                    "apiGroups": ["storage.k8s.io"],
                    "resources": ["volumeattachments/status"],
                    "verbs": ["patch", "update"],
                },
                {
                    "apiGroups": ["snapshot.storage.k8s.io"],
                    "resources": ["volumesnapshots", "volumesnapshotcontents", "volumesnapshotclasses"],
                    "verbs": ["get", "list", "watch", "create", "delete", "patch", "update"],
                },
            ],
        },
        {
            "apiVersion": "rbac.authorization.k8s.io/v1",
            "kind": "ClusterRoleBinding",
            "metadata": {
                "name": "ani-rook-rbd-csi-plugin",
                "labels": {"ani.deploy/profile": "isolated"},
            },
            "roleRef": {
                "apiGroup": "rbac.authorization.k8s.io",
                "kind": "ClusterRole",
                "name": "ani-rook-rbd-csi-plugin",
            },
            "subjects": [{"kind": "ServiceAccount", "name": "ceph-csi", "namespace": "rook-ceph"}],
        },
        {
            "apiVersion": "csi.ceph.io/v1",
            "kind": "Driver",
            "metadata": {
                "name": "rook-ceph.rbd.csi.ceph.com",
                "namespace": "rook-ceph",
                "labels": {"ani.deploy/profile": "isolated"},
            },
            "spec": {
                "attachRequired": True,
                "controllerPlugin": {
                    "imagePullPolicy": "IfNotPresent",
                    "replicas": 1,
                    "serviceAccountName": "ceph-csi",
                },
                "fsGroupPolicy": "File",
                "imageSet": {"name": "rook-csi-operator-image-set-configmap"},
                "nodePlugin": {
                    "imagePullPolicy": "IfNotPresent",
                    "serviceAccountName": "ceph-csi",
                },
            },
        },
    ]
    for manifest in manifests:
        run(["kubectl", "apply", "-f", "-"], input=json.dumps(manifest))


def apply_ceph_cluster_stack(devices: dict[str, str]) -> None:
    nodes_spec = [
        {"name": node, "devices": [{"name": dev}]}
        for node, dev in sorted(devices.items())
    ]
    cluster = {
        "apiVersion": "ceph.rook.io/v1",
        "kind": "CephCluster",
        "metadata": {
            "name": "rook-ceph",
            "namespace": "rook-ceph",
            "labels": {
                "app.kubernetes.io/name": "rook-ceph",
                "ani.deploy/profile": "isolated",
            },
        },
        "spec": {
            "cephVersion": {
                "image": "docker.changqingyun.cn/mirror/ceph/ceph:v19.2.3",
                "allowUnsupported": False,
            },
            "dataDirHostPath": "/var/lib/rook",
            "skipUpgradeChecks": False,
            "continueUpgradeAfterChecksEvenIfNotHealthy": False,
            "waitTimeoutForHealthyOSDInMinutes": 15,
            "mon": {"count": 1, "allowMultiplePerNode": True},
            "mgr": {"count": 1},
            "dashboard": {"enabled": False},
            "crashCollector": {"disable": False},
            "storage": {
                "useAllNodes": False,
                "useAllDevices": False,
                "nodes": nodes_spec,
            },
        },
    }
    run(["kubectl", "apply", "-f", "-"], input=json.dumps(cluster))

    parts = (CF / "rook-ceph-cluster.yaml").read_text().split("\n---\n")
    tail = "\n---\n".join(parts[1:])
    with tempfile.NamedTemporaryFile(mode="w", suffix=".yaml", delete=False) as tmp:
        tmp.write(tail)
        tmp_path = tmp.name
    try:
        kubectl_apply(Path(tmp_path))
    finally:
        Path(tmp_path).unlink(missing_ok=True)


def deploy_ceph_cluster() -> None:
    prep_ceph_osd_devices()
    devices = discover_osd_block_devices()
    log("foundation: CephCluster + BlockPool + StorageClass")
    apply_ceph_cluster_stack(devices)
    kubectl(
        "-n", "rook-ceph", "wait", "cephcluster", "rook-ceph",
        "--for=condition=Ready", "--timeout=900s",
    )
    wait_ceph_osds(min_count=1)
    wait_ceph_blockpool()
    wait_storage_class(STORAGE_CLASS)
    set_default_storage_class(STORAGE_CLASS)
    apply_cdi_storage_profile()
    wait_crd("drivers.csi.ceph.io")
    ensure_rbd_csi_driver()
    wait_csi_rbd_driver()


def apply_cdi_storage_profile() -> None:
    """Prefer Filesystem+RWO on ani-rbd-ssd so CDI upload/importer can open volumes."""
    profile = YAML_DIR / "09-cdi-storageprofile-filesystem.yaml"
    if not profile.is_file():
        raise DeployError(f"missing CDI StorageProfile manifest: {profile}")
    log("foundation: CDI StorageProfile Filesystem+RWO for ani-rbd-ssd")
    kubectl_apply(profile)


def install_kubevirt() -> None:
    log("foundation: KubeVirt operator")
    kubectl_apply(YAML_DIR / "01-kubevirt-operator.yaml")
    rollout("kubevirt", "deploy", "virt-operator")
    log("foundation: waiting for KubeVirt CRD")
    wait_kubevirt_crd()
    log("foundation: KubeVirt CR")
    kubectl_apply(YAML_DIR / "02-kubevirt-cr.yaml")
    kubectl("-n", "kubevirt", "wait", "kubevirt", "kubevirt", "--for=condition=Available", "--timeout=600s")


def install_cdi() -> None:
    log("foundation: CDI operator")
    kubectl_apply(YAML_DIR / "06-cdi-operator.yaml")
    rollout("cdi", "deploy", "cdi-operator", timeout="300s")
    wait_cdi_crd("cdis.cdi.kubevirt.io")
    log("foundation: CDI CR")
    kubectl_apply(YAML_DIR / "07-cdi-cr.yaml")
    wait_cdi_crd("datavolumes.cdi.kubevirt.io")
    kubectl("wait", "cdi", "cdi", "--for=condition=Available", "--timeout=600s")
    log("foundation: CDI uploadproxy NodePort")
    kubectl_apply(YAML_DIR / "08-cdi-uploadproxy-nodeport.yaml")
    kubectl("-n", "cdi", "get", "svc", "cdi-uploadproxy-nodeport")


def helm_dependency_update() -> None:
    helm("dependency", "update", cwd=CHART)


def render_yaml() -> None:
    require_cmd("helm")
    YAML_DIR.mkdir(parents=True, exist_ok=True)
    helm_dependency_update()

    shutil.copy(
        CHART / "manifests/kubevirt-operator-v1.8.2.yaml",
        YAML_DIR / "01-kubevirt-operator.yaml",
    )

    proc = helm(
        "template", "ani-cluster-foundation", ".",
        "-n", "kubevirt",
        "--set", "harbor.enabled=false",
        "--set", "rook.enabled=false",
        "--set", "cephCluster.enabled=false",
        "--show-only", "templates/kubevirt-cr.yaml",
        cwd=CHART,
        quiet=True,
    )
    (YAML_DIR / "02-kubevirt-cr.yaml").write_text(proc.stdout)

    proc = helm(
        "template", "rook-ceph", str(CHART / "charts/rook-ceph-v1.20.0.tgz"),
        "-n", "rook-ceph",
        "-f", str(CF / "rook-ceph-install-values.yaml"),
        quiet=True,
    )
    (YAML_DIR / "03-rook-ceph-operator.yaml").write_text(proc.stdout)

    shutil.copy(CF / "rook-ceph-cluster.yaml", YAML_DIR / "04-rook-ceph-cluster.yaml")

    proc = helm(
        "template", "harbor", str(CHART / "charts/harbor-1.19.1.tgz"),
        "-n", "harbor",
        "-f", str(CF / "harbor-install-values.yaml"),
        quiet=True,
    )
    (YAML_DIR / "05-harbor.yaml").write_text(proc.stdout)

    parts = []
    for name in (
        "01-kubevirt-operator.yaml",
        "02-kubevirt-cr.yaml",
        "06-cdi-operator.yaml",
        "07-cdi-cr.yaml",
        "08-cdi-uploadproxy-nodeport.yaml",
        "09-cdi-storageprofile-filesystem.yaml",
        "03-rook-ceph-operator.yaml",
        "05-harbor.yaml",
    ):
        parts.append((YAML_DIR / name).read_text())
    (YAML_DIR / "cluster-foundation-all.yaml").write_text("\n---\n".join(parts))
    print(f"✅ rendered yaml under {YAML_DIR}")


def mirror_one(from_ref: str, target: str, *, dry_run: bool = False) -> None:
    to_ref = f"{REGISTRY_MIRROR}/{target}"
    if shutil.which("docker"):
        proc = run(["docker", "manifest", "inspect", to_ref], check=False)
        if proc.returncode == 0:
            print(f"↷ skip (exists) {to_ref}")
            return

    if dry_run:
        print(f"→ would mirror {from_ref} => {to_ref}")
        return

    if not shutil.which("oras"):
        raise DeployError("oras not found in PATH")
    if not DOCKER_CONFIG.is_file():
        raise DeployError(f"missing docker config at {DOCKER_CONFIG}; run: docker login docker.changqingyun.cn")

    retries = int(os.environ.get("MIRROR_RETRIES", "3"))
    for attempt in range(1, retries + 1):
        print(f"→ oras cp {from_ref} => {to_ref} (attempt {attempt}/{retries})")
        proc = run(
            ["oras", "cp", "--concurrency", "3", "--to-registry-config", str(DOCKER_CONFIG), from_ref, to_ref],
            check=False,
        )
        if proc.returncode == 0:
            print(f"✓ {to_ref}")
            return
        time.sleep(attempt * 5)

    if shutil.which("docker"):
        print(f"→ docker fallback {from_ref} => {to_ref}")
        run(["docker", "pull", from_ref])
        run(["docker", "tag", from_ref, to_ref])
        run(["docker", "push", to_ref])
        print(f"✓ {to_ref}")
        return

    raise DeployError(f"mirror failed: {from_ref} => {to_ref}")


def mirror_images(scope: str = "all", *, dry_run: bool = False) -> None:
    items: list[tuple[str, str]] = []
    if scope in ("base", "all"):
        items.extend(BASE_MIRRORS)
    if scope in ("foundation", "all"):
        items.extend(FOUNDATION_MIRRORS)
    if not items:
        raise DeployError(f"invalid mirror scope: {scope}")

    failed = 0
    for from_ref, target in items:
        try:
            mirror_one(from_ref, target, dry_run=dry_run)
        except DeployError as exc:
            print(f"✗ {exc}", file=sys.stderr)
            failed += 1

    if failed:
        raise DeployError(f"{failed} mirror(s) failed")
    print(f"✅ mirror complete (scope={scope}) under {REGISTRY_MIRROR}/")


def deploy_foundation(*, skip_harbor: bool = False, skip_ceph_cluster: bool = False) -> None:
    require_cmd("helm")
    require_cluster()

    if not (YAML_DIR / "01-kubevirt-operator.yaml").is_file():
        render_yaml()

    helm_dependency_update()
    install_kubevirt()
    install_cdi()

    log("foundation: Rook-Ceph operator")
    helm(
        "upgrade", "--install", "rook-ceph", str(CHART / "charts/rook-ceph-v1.20.0.tgz"),
        "-n", "rook-ceph", "--create-namespace",
        "-f", str(CF / "rook-ceph-install-values.yaml"),
    )
    rollout("rook-ceph", "deploy", "rook-ceph-operator")
    wait_crd("cephclusters.ceph.rook.io")

    if skip_ceph_cluster:
        raise DeployError(
            "CephCluster is required for isolated deploy (StorageClass + PVC). "
            "Remove --skip-ceph-cluster."
        )
    deploy_ceph_cluster()

    if not skip_harbor:
        log("foundation: Harbor (PVC via ani-rbd-ssd)")
        helm(
            "upgrade", "--install", "harbor", str(CHART / "charts/harbor-1.19.1.tgz"),
            "-n", "harbor", "--create-namespace",
            "-f", str(CF / "harbor-install-values.yaml"),
        )
        rollout("harbor", "deploy", "harbor-core", timeout="600s")
    else:
        log("foundation: skip Harbor")

    ip = node_ip()
    print("✅ cluster foundation installed")
    if not skip_harbor:
        print(f"   Harbor UI: http://{ip}:30002  (admin / ani-harbor-admin-dev)")
    print(f"   StorageClass: {STORAGE_CLASS}")


def deploy_base_infra(cfg: dict[str, str]) -> None:
    require_cluster()
    require_storage_class()
    ns = "ani-system"

    ns_yaml = run(["kubectl", "create", "namespace", ns, "--dry-run=client", "-o", "yaml"]).stdout
    run(["kubectl", "apply", "-f", "-"], input=ns_yaml)

    secrets = [
        ("ani-postgres-secret", [
            "POSTGRES_USER=ani",
            f"POSTGRES_PASSWORD={cfg['postgres_password']}",
            "POSTGRES_DB=ani",
        ]),
        ("ani-redis-secret", [f"password={cfg['redis_password']}"]),
        ("ani-s05-minio-root", [
            f"access_key_id={cfg['minio_access_key']}",
            f"secret_access_key={cfg['minio_secret_key']}",
        ]),
        ("milvus-minio", [
            f"access_key={cfg['milvus_minio_access']}",
            f"secret_key={cfg['milvus_minio_secret']}",
        ]),
    ]
    for name, literals in secrets:
        cmd = ["kubectl", "-n", ns, "create", "secret", "generic", name]
        for lit in literals:
            cmd.extend(["--from-literal", lit])
        cmd.extend(["--dry-run=client", "-o", "yaml"])
        run(["kubectl", "apply", "-f", "-"], input=run(cmd, quiet=True).stdout)

    kubectl_apply(DEPLOY / "base-infra.yaml")

    rollout(ns, "deploy", "ani-redis", timeout="240s")
    rollout(ns, "deploy", "nats", timeout="240s")
    rollout(ns, "sts", "ani-postgres", timeout="240s")
    init_database(ns)
    rollout(ns, "deploy", "ani-s05-minio", timeout="240s")
    rollout(ns, "deploy", "milvus", timeout="240s")
    rollout(ns, "deploy", "prometheus", timeout="240s")
    rollout(ns, "deploy", "ani-dex", timeout="180s")
    rollout(ns, "deploy", "attu", timeout="180s")
    print("✅ base-infra deployed")


def _gen_jwt_keys(workdir: Path) -> tuple[Path, Path]:
    priv = workdir / "jwt_private.pem"
    pub = workdir / "jwt_public.pem"
    run(["openssl", "genrsa", "-out", str(priv), "2048"])
    run(["openssl", "rsa", "-in", str(priv), "-pubout", "-out", str(pub)])
    return priv, pub


def deploy_business(cfg: dict[str, str], version: str) -> None:
    require_cluster()
    ns = cfg.get("namespace", "ani-system")

    with tempfile.TemporaryDirectory(prefix="ani-isolated-") as tmp:
        priv, pub = _gen_jwt_keys(Path(tmp))
        db_url = (
            f"postgres://ani:{cfg['postgres_password']}@"
            f"ani-postgres.ani-system.svc.cluster.local:5432/ani?sslmode=disable"
        )
        redis_url = f"redis://:{cfg['redis_password']}@ani-redis.ani-system.svc.cluster.local:6379/0"

        cmd = [
            "kubectl", "-n", ns, "create", "secret", "generic", "ani-services-runtime",
            "--from-literal", f"database_url={db_url}",
            "--from-literal", f"nats_url={cfg['nats_url']}",
            "--from-literal", f"oidc_issuer_url={cfg['oidc_issuer_url']}",
            "--from-literal", f"redis_url={redis_url}",
            "--from-literal", f"oidc_client_id={cfg['oidc_client_id']}",
            "--from-literal", f"oidc_client_secret={cfg['oidc_client_secret']}",
            "--from-literal", f"oidc_group_role_map_json={cfg['oidc_group_role_map']}",
            "--from-literal", f"auth_jwt_issuer={cfg['auth_jwt_issuer']}",
            "--from-file", f"jwt_private_key_pem={priv}",
            "--from-file", f"jwt_public_key_pem={pub}",
            "--dry-run=client", "-o", "yaml",
        ]
        run(["kubectl", "apply", "-f", "-"], input=run(cmd, quiet=True).stdout)

    kubectl_apply(DEPLOY / "business-stack.yaml")

    external_base = f"http://{node_ip()}"
    external_https = f"https://{node_ip()}"
    kubectl(
        "-n", ns, "set", "env", "deploy/ani-gateway",
        f"INSTANCE_OBSERVABILITY_EXEC_BASE_URL={external_base}:30080",
        f"INSTANCE_CONSOLE_BASE_URL={external_base}:30080",
        f"OBJECT_STORE_PUBLIC_ENDPOINT={external_base}:30900",
        f"CDI_UPLOADPROXY_URL={external_https}:31001",
        "CDI_UPLOADPROXY_INTERNAL_URL=https://cdi-uploadproxy.cdi.svc:443",
        "IMAGE_IMPORT_PROVIDER=cdi_rest",
    )

    issuer_host = urllib.parse.urlparse(cfg["oidc_issuer_url"]).hostname
    if issuer_host and "." in issuer_host:
        alias_ip = os.environ.get("OIDC_HOST_ALIAS_IP", node_ip())
        ensure_host_alias(ns, "ani-auth-service", issuer_host, alias_ip)

    for name in ANI_IMAGES:
        image = f"{REGISTRY_ANI}/{name}:{version}"
        kubectl("-n", ns, "set", "image", f"deploy/{name}", f"{name}={image}")

    for name in ANI_IMAGES:
        rollout(ns, "deploy", name, timeout="180s")
    print(f"✅ business stack deployed (version={version})")


def build_images(version: str) -> None:
    require_cmd("docker")
    require_cmd("make")
    env = {"VERSION": version, "REGISTRY": REGISTRY_ANI}
    run(
        ["make", "image-gateway", "image-auth-service", "image-model-service",
         "image-task-service", "image-reconcile-worker"],
        env=env,
    )
    for name in ANI_IMAGES:
        ref = f"{REGISTRY_ANI}/{name}:{version}"
        run(["docker", "push", ref])
    print(f"✅ ANI images published under {REGISTRY_ANI}/*:{version}")


def finalize_namespace(ns: str) -> None:
    proc = kubectl("get", "namespace", ns, "-o", "json", check=False, quiet=True)
    if proc.returncode != 0:
        return
    data = json.loads(proc.stdout)
    if data.get("status", {}).get("phase") != "Terminating":
        return
    body = {"apiVersion": "v1", "kind": "Namespace", "metadata": {"name": ns}, "spec": {"finalizers": []}}
    run(
        ["kubectl", "replace", "--raw", f"/api/v1/namespaces/{ns}/finalize", "-f", "-"],
        input=json.dumps(body),
        check=False,
    )


def cleanup() -> None:
    log("cleanup: ani-system")
    kubectl_delete(DEPLOY / "business-stack.yaml")
    kubectl_delete(DEPLOY / "base-infra.yaml")
    kubectl("delete", "namespace", "ani-system", "--ignore-not-found", "--timeout=180s", check=False)

    log("cleanup: Harbor / Rook helm")
    helm("uninstall", "harbor", "-n", "harbor", check=False)
    helm("uninstall", "rook-ceph", "-n", "rook-ceph", check=False)
    kubectl("delete", "namespace", "harbor", "--ignore-not-found", "--timeout=180s", check=False)

    log("cleanup: Rook-Ceph")
    kubectl("delete", "cephcluster", "rook-ceph", "-n", "rook-ceph", "--ignore-not-found", check=False)
    kubectl("delete", "-f", str(CF / "ceph-osd-prep.yaml"), "--ignore-not-found", "--timeout=60s", check=False)
    if (YAML_DIR / "04-rook-ceph-cluster.yaml").is_file():
        kubectl_delete(YAML_DIR / "04-rook-ceph-cluster.yaml")
    if (YAML_DIR / "03-rook-ceph-operator.yaml").is_file():
        kubectl_delete(YAML_DIR / "03-rook-ceph-operator.yaml", timeout="120s")
    kubectl("delete", "namespace", "rook-ceph", "--ignore-not-found", "--timeout=180s", check=False)

    log("cleanup: KubeVirt")
    if (YAML_DIR / "02-kubevirt-cr.yaml").is_file():
        kubectl_delete(YAML_DIR / "02-kubevirt-cr.yaml")
    for wh in ("virt-api-validator", "virt-operator-validator"):
        kubectl("delete", "validatingwebhookconfigurations", wh, "--ignore-not-found", check=False)
    kubectl("delete", "mutatingwebhookconfigurations", "virt-api-mutator", "--ignore-not-found", check=False)
    if (YAML_DIR / "01-kubevirt-operator.yaml").is_file():
        kubectl_delete(YAML_DIR / "01-kubevirt-operator.yaml", timeout="120s")
    kubectl("delete", "namespace", "kubevirt", "--ignore-not-found", "--timeout=180s", check=False)

    log("cleanup: leftover PV / StorageClass")
    kubectl(
        "patch", "storageclass", "local",
        "-p", '{"metadata":{"annotations":{"storageclass.kubernetes.io/is-default-class":"false"}}}',
        check=False,
    )
    proc = kubectl("get", "pv", "-o", "name", check=False)
    if proc.returncode == 0:
        for line in proc.stdout.splitlines():
            if any(x in line for x in ("ani-postgres", "harbor-", "pv-ani", "pv-harbor")):
                kubectl("patch", line, "-p", '{"spec":{"claimRef":null}}', check=False)
                kubectl("delete", line, "--ignore-not-found", "--timeout=30s", check=False)
    kubectl("delete", "storageclass", "local", "ani-rbd-ssd", "--ignore-not-found", check=False)

    for ns in ("ani-system", "harbor", "rook-ceph", "kubevirt"):
        kubectl("wait", "--for=delete", f"namespace/{ns}", "--timeout=180s", check=False)
        finalize_namespace(ns)

    print("✅ isolated cleanup done")


def probe(url: str) -> bool:
    try:
        with urllib.request.urlopen(url, timeout=10) as resp:
            return 200 <= resp.status < 400
    except (urllib.error.URLError, TimeoutError):
        return False


def verify() -> None:
    require_cluster()
    ip = node_ip()
    checks = [
        ("Gateway", f"http://{ip}:30080/readyz"),
        ("MinIO", f"http://{ip}:30900/minio/health/ready"),
        ("Prometheus", f"http://{ip}:31990/-/ready"),
        ("Harbor", f"http://{ip}:30002/"),
        ("Dex", f"http://{ip}:30556/dex/.well-known/openid-configuration"),
        ("Attu", f"http://{ip}:30300/"),
    ]

    print("==> Pods (ani-system)")
    kubectl("-n", "ani-system", "get", "pods", check=False)

    failed = 0
    print(f"\n==> HTTP probes ({ip})")
    for name, url in checks:
        ok = probe(url)
        mark = "OK  " if ok else "FAIL"
        print(f"  {mark} {name}  {url}")
        if not ok:
            failed += 1

    print("\n==> Endpoints")
    for label, port in [
        ("Gateway", 30080), ("MinIO", 30900), ("Prometheus", 31990),
        ("Dex", 30556), ("Attu", 30300), ("Harbor", 30002),
    ]:
        print(f"  {label:<12} http://{ip}:{port}")

    if failed:
        raise DeployError(f"{failed} probe(s) failed")
    print("\n✅ isolated deployment healthy")


def default_config() -> dict[str, str]:
    return {
        "postgres_password": os.environ.get("POSTGRES_PASSWORD", "ani_dev_password"),
        "redis_password": os.environ.get("REDIS_PASSWORD", "ani_dev_password"),
        "minio_access_key": os.environ.get("MINIO_ACCESS_KEY_ID", "ani-minio-access"),
        "minio_secret_key": os.environ.get("MINIO_SECRET_ACCESS_KEY", "ani-minio-secret"),
        "milvus_minio_access": os.environ.get("MILVUS_MINIO_ACCESS_KEY", "ani-milvus-access"),
        "milvus_minio_secret": os.environ.get("MILVUS_MINIO_SECRET_KEY", "ani-milvus-secret"),
        "nats_url": os.environ.get("NATS_URL", "nats://nats.ani-system.svc.cluster.local:4222"),
        "oidc_issuer_url": os.environ.get("OIDC_ISSUER_URL", "http://console.example.local:30556/dex"),
        "oidc_client_id": os.environ.get("OIDC_CLIENT_ID", "ani-console"),
        "oidc_client_secret": os.environ.get("OIDC_CLIENT_SECRET", "ani-dex-client-secret-dev"),
        "oidc_group_role_map": os.environ.get(
            "OIDC_GROUP_ROLE_MAP_JSON",
            '{"admins":["tenant-admin"],"platform-admins":["platform-admin"]}',
        ),
        "auth_jwt_issuer": os.environ.get("AUTH_JWT_ISSUER", "ani-auth-service"),
        "namespace": os.environ.get("NAMESPACE", "ani-system"),
    }


def cmd_deploy(args: argparse.Namespace) -> None:
    cfg = default_config()
    version = args.version
    only = args.only

    steps = ["render", "mirror", "foundation", "base-infra", "business", "verify"]
    if only:
        steps = [only]
    else:
        if args.build:
            steps.insert(steps.index("base-infra"), "build")
        if args.skip_render and "render" in steps:
            steps.remove("render")
        if args.skip_mirror and "mirror" in steps:
            steps.remove("mirror")
        if args.skip_foundation and "foundation" in steps:
            steps.remove("foundation")
        if args.skip_build and "build" in steps:
            steps.remove("build")
        if args.skip_verify and "verify" in steps:
            steps.remove("verify")

    for step in steps:
        if step == "render":
            if not (YAML_DIR / "01-kubevirt-operator.yaml").is_file() or args.force_render:
                render_yaml()
        elif step == "mirror":
            mirror_images(args.mirror_scope)
        elif step == "foundation":
            deploy_foundation(skip_harbor=args.skip_harbor, skip_ceph_cluster=args.skip_ceph_cluster)
        elif step == "build":
            build_images(version)
        elif step == "base-infra":
            deploy_base_infra(cfg)
        elif step == "business":
            deploy_business(cfg, version)
        elif step == "verify":
            verify()


def main() -> None:
    parser = argparse.ArgumentParser(description="ANI isolated deployment")
    sub = parser.add_subparsers(dest="command", required=True)

    p_deploy = sub.add_parser("deploy", help="deploy isolated stack (ordered steps)")
    p_deploy.add_argument("version", nargs="?", default="dev", help="ANI image tag (default: dev)")
    p_deploy.add_argument("--only", choices=["render", "mirror", "foundation", "build", "base-infra", "business", "verify"])
    p_deploy.add_argument("--skip-render", action="store_true")
    p_deploy.add_argument("--skip-mirror", action="store_true")
    p_deploy.add_argument("--skip-foundation", action="store_true")
    p_deploy.add_argument("--build", action="store_true", help="build and push ANI images before deploying business")
    p_deploy.add_argument("--skip-build", action="store_true", help="deprecated: build is skipped by default")
    p_deploy.add_argument("--skip-verify", action="store_true")
    p_deploy.add_argument("--force-render", action="store_true")
    p_deploy.add_argument("--skip-harbor", action="store_true")
    p_deploy.add_argument("--skip-ceph-cluster", action="store_true", help="not supported: Ceph is required for PVC")
    p_deploy.add_argument("--mirror-scope", default="all", choices=["base", "foundation", "all"])
    p_deploy.set_defaults(func=cmd_deploy)

    p_mirror = sub.add_parser("mirror", help="sync images to changqingyun mirror registry")
    p_mirror.add_argument("--scope", default="all", choices=["base", "foundation", "all"])
    p_mirror.add_argument("--dry-run", action="store_true")
    p_mirror.set_defaults(func=lambda a: mirror_images(a.scope, dry_run=a.dry_run))

    sub.add_parser("render", help="render cluster-foundation yaml").set_defaults(func=lambda a: render_yaml())
    sub.add_parser("cleanup", help="remove isolated deployment").set_defaults(func=lambda a: cleanup())
    sub.add_parser("verify", help="health check").set_defaults(func=lambda a: verify())

    args = parser.parse_args()
    try:
        args.func(args)
    except DeployError as exc:
        print(f"❌ {exc}", file=sys.stderr)
        sys.exit(1)


if __name__ == "__main__":
    main()
