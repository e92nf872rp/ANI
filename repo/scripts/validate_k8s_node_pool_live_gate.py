#!/usr/bin/env python3
"""Validate Sprint 5 M1-K8S-LIVE-B K8s node pool live gate."""

from __future__ import annotations

import argparse
import json
import os
import re
import shutil
import subprocess
import urllib.error
import urllib.request
from dataclasses import dataclass
from pathlib import Path
from typing import Any

import yaml


ROOT = Path(__file__).resolve().parents[1]
DOC_ROOT = ROOT.parent
DEFAULT_GATE = ROOT / "deploy/real-k8s-lab/k8s-node-pool-live-gate.yaml"
REQUIRED_CHECKS = {
    "core-create-node-pool",
    "clusterapi-machinedeployment-created",
    "core-scale-node-pool",
    "clusterapi-machinedeployment-scaled",
    "gpu-workload-scheduled",
}
REQUIRED_DOC_TOKENS = [
    "M1-K8S-LIVE-B",
    "validate-k8s-node-pool-live-gate",
    "Cluster API",
    "GPU",
]


def fail(message: str) -> None:
    raise SystemExit(f"K8s node pool live gate invalid: {message}")


def load_gate(path: Path) -> dict[str, Any]:
    if not path.exists():
        fail(f"missing {path.relative_to(ROOT)}")
    with path.open(encoding="utf-8") as handle:
        data = yaml.safe_load(handle)
    if not isinstance(data, dict):
        fail(f"{path.relative_to(ROOT)} must be a YAML object")
    return data


def validate_contract(document: dict[str, Any]) -> None:
    if document.get("profile") != "M1-K8S-LIVE-B":
        fail("profile must be M1-K8S-LIVE-B")
    if document.get("status") not in {"contract", "live", "production_like"}:
        fail("status must be contract, live or production_like")
    tools = document.get("required_tools")
    if not isinstance(tools, list) or "kubectl" not in tools:
        fail("required_tools must include kubectl")
    endpoints = document.get("required_endpoints")
    required_endpoints = {"ani_gateway_api_v1", "kubernetes_api", "cluster_api"}
    if not isinstance(endpoints, list) or required_endpoints - set(endpoints):
        fail("required_endpoints must include Gateway, Kubernetes API and Cluster API")
    checks = document.get("live_checks")
    if not isinstance(checks, list):
        fail("live_checks must be a list")
    check_ids = set()
    for check in checks:
        if not isinstance(check, dict):
            fail("live check must be an object")
        for field in ("id", "command", "pass_condition"):
            if not check.get(field):
                fail(f"live check missing {field}")
        check_ids.add(check["id"])
    missing = REQUIRED_CHECKS - check_ids
    if missing:
        fail(f"missing live checks: {', '.join(sorted(missing))}")


def validate_docs() -> None:
    docs = {
        "ANI-DOCS-INDEX.md": DOC_ROOT / "ANI-DOCS-INDEX.md",
        "ANI-06-开发计划.md": DOC_ROOT / "ANI-06-开发计划.md",
        "CURRENT-SPRINT.md": ROOT / "CURRENT-SPRINT.md",
        "development-records/README.md": ROOT / "development-records/README.md",
    }
    for label, path in docs.items():
        content = path.read_text(encoding="utf-8")
        for token in REQUIRED_DOC_TOKENS:
            if token not in content:
                fail(f"{label} must reference {token}")


@dataclass(frozen=True)
class LiveConfig:
    tenant_id: str
    cluster_id: str
    gateway_url: str
    ani_bearer_token: str
    node_pool_name: str = "gpu-pool"
    instance_type: str = "gpu.l4.xlarge"
    initial_node_count: int = 1
    scaled_node_count: int = 2
    gpu_vendor: str = "nvidia"
    gpu_model: str = "L4"
    gpu_count: int = 1
    gpu_resource_name: str = "nvidia.com/gpu"
    kubeconfig: str = ""
    workload_kubeconfig: str = ""
    kubectl_binary: str = "kubectl"
    gpu_smoke_pod_name: str = "ani-node-pool-gpu-smoke"


class LiveRunner:
    def request_json(self, method: str, url: str, payload: dict[str, object], bearer_token: str) -> dict[str, object]:
        body = json.dumps(payload).encode("utf-8")
        request = urllib.request.Request(
            url,
            data=body,
            method=method,
            headers={
                "content-type": "application/json",
                "authorization": "Bearer " + bearer_token,
            },
        )
        try:
            with urllib.request.urlopen(request, timeout=30) as response:
                response_body = response.read().decode("utf-8")
                return json.loads(response_body) if response_body else {}
        except urllib.error.HTTPError as err:
            detail = err.read().decode("utf-8", errors="replace")
            raise RuntimeError(f"{method} {url} returned HTTP {err.code}: {detail}") from err

    def post_json(self, url: str, payload: dict[str, object], bearer_token: str) -> dict[str, object]:
        return self.request_json("POST", url, payload, bearer_token)

    def patch_json(self, url: str, payload: dict[str, object], bearer_token: str) -> dict[str, object]:
        return self.request_json("PATCH", url, payload, bearer_token)

    def run(self, command: list[str], input_text: str | None = None) -> str:
        result = subprocess.run(
            command,
            input=input_text,
            text=True,
            capture_output=True,
            check=False,
        )
        if result.returncode != 0:
            detail = result.stderr.strip() or result.stdout.strip()
            raise RuntimeError(f"{' '.join(command)} failed: {detail}")
        return result.stdout


def tenant_namespace(tenant_id: str) -> str:
    return "ani-tenant-" + tenant_id.replace("_", "-")


def kubernetes_name(name: str) -> str:
    lowered = name.lower()
    normalized = re.sub(r"[^a-z0-9-]+", "-", lowered).strip("-")
    if not normalized:
        normalized = "node-pool"
    return normalized[:63].rstrip("-")


def validate_live_fields(config: LiveConfig) -> None:
    required = {
        "tenant_id": config.tenant_id,
        "cluster_id": config.cluster_id,
        "gateway_url": config.gateway_url,
        "ani_bearer_token": config.ani_bearer_token,
        "node_pool_name": config.node_pool_name,
        "instance_type": config.instance_type,
    }
    missing = [name for name, value in required.items() if not value.strip()]
    if missing:
        fail(f"live mode requires {', '.join(missing)}")
    if config.initial_node_count <= 0:
        fail("initial_node_count must be greater than zero")
    if config.scaled_node_count <= 0:
        fail("scaled_node_count must be greater than zero")
    if config.initial_node_count == config.scaled_node_count:
        fail("scaled_node_count must differ from initial_node_count")
    if config.gpu_count < 0:
        fail("gpu_count cannot be negative")
    if config.gpu_count > 0 and not config.gpu_resource_name.strip():
        fail("gpu_resource_name is required when gpu_count is greater than zero")


def validate_live_config(config: LiveConfig) -> None:
    validate_live_fields(config)
    if shutil.which(config.kubectl_binary) is None:
        fail(f"{config.kubectl_binary} is required for --live")


def kubectl(config: LiveConfig, args: list[str], *, workload: bool = False) -> list[str]:
    command = [config.kubectl_binary]
    kubeconfig = config.workload_kubeconfig if workload and config.workload_kubeconfig else config.kubeconfig
    if kubeconfig.strip():
        command.extend(["--kubeconfig", kubeconfig.strip()])
    command.extend(args)
    return command


def gpu_workload_manifest(namespace: str, pod_name: str, gpu_resource_name: str) -> str:
    return json.dumps(
        {
            "apiVersion": "v1",
            "kind": "Pod",
            "metadata": {"name": pod_name, "namespace": namespace},
            "spec": {
                "restartPolicy": "Never",
                "containers": [
                    {
                        "name": "gpu-smoke",
                        "image": "nvidia/cuda:12.4.1-base-ubuntu22.04",
                        "command": ["sh", "-c", "nvidia-smi || true; sleep 5"],
                        "resources": {"limits": {gpu_resource_name: 1}},
                    }
                ],
            },
        },
        indent=2,
        sort_keys=True,
    )


def assert_real_node_pool_response(response: dict[str, Any]) -> str:
    node_pool_id = str(response.get("id") or response.get("node_pool_id") or "")
    if not node_pool_id:
        fail("Core node pool response missing node pool id")
    profile = response.get("dev_profile", {})
    if not isinstance(profile, dict) or profile.get("mode") != "real" or not profile.get("real_provider"):
        fail("Core node pool response must be provider-backed real dev profile")
    return node_pool_id


def load_machine_deployment(config: LiveConfig, runner: LiveRunner, namespace: str, name: str) -> dict[str, Any]:
    raw = runner.run(kubectl(config, ["get", "machinedeployment", name, "-n", namespace, "-o", "json"]))
    try:
        document = json.loads(raw)
    except json.JSONDecodeError as err:
        fail(f"kubectl get machinedeployment did not return JSON: {err}")
    if not isinstance(document, dict):
        fail("kubectl get machinedeployment must return a JSON object")
    return document


def assert_machine_deployment(document: dict[str, Any], expected_replicas: int, node_pool_id: str, config: LiveConfig) -> None:
    metadata = document.get("metadata", {})
    if not isinstance(metadata, dict):
        fail("MachineDeployment metadata must be an object")
    labels = metadata.get("labels", {})
    if not isinstance(labels, dict) or labels.get("ani.kubercloud.io/node-pool-id") != node_pool_id:
        fail("MachineDeployment missing ANI node pool id label")
    spec = document.get("spec", {})
    if not isinstance(spec, dict):
        fail("MachineDeployment spec must be an object")
    if int(spec.get("replicas", -1)) != expected_replicas:
        fail(f"MachineDeployment replicas must be {expected_replicas}")
    template = spec.get("template", {})
    machine_spec = template.get("spec", {}) if isinstance(template, dict) else {}
    if config.gpu_count > 0:
        gpu = machine_spec.get("gpu") if isinstance(machine_spec, dict) else None
        if not isinstance(gpu, dict) or int(gpu.get("count", -1)) != config.gpu_count:
            fail("MachineDeployment template.spec.gpu must preserve GPU intent")


def run_gpu_workload_check(config: LiveConfig, runner: LiveRunner, namespace: str) -> None:
    if config.gpu_count <= 0:
        return
    runner.run(kubectl(config, ["delete", "pod", config.gpu_smoke_pod_name, "-n", namespace, "--ignore-not-found=true"], workload=True))
    runner.run(
        kubectl(config, ["apply", "-f", "-"], workload=True),
        input_text=gpu_workload_manifest(namespace, config.gpu_smoke_pod_name, config.gpu_resource_name),
    )
    runner.run(
        kubectl(
            config,
            ["wait", "--for=condition=PodScheduled", f"pod/{config.gpu_smoke_pod_name}", "-n", namespace, "--timeout=180s"],
            workload=True,
        )
    )


def run_live(config: LiveConfig, runner: LiveRunner | None = None) -> dict[str, object]:
    validate_live_fields(config)
    runner = runner or LiveRunner()
    gateway = config.gateway_url.rstrip("/")
    namespace = tenant_namespace(config.tenant_id)
    machine_deployment_name = kubernetes_name(config.node_pool_name)
    gpu = {
        "vendor": config.gpu_vendor,
        "model": config.gpu_model,
        "count": config.gpu_count,
        "resource_name": config.gpu_resource_name,
    }

    created = runner.post_json(
        gateway + f"/k8s-clusters/{config.cluster_id}/node-pools",
        {
            "idempotency_key": f"node-pool-live-{config.cluster_id}-{config.node_pool_name}",
            "name": config.node_pool_name,
            "node_count": config.initial_node_count,
            "instance_type": config.instance_type,
            "gpu": gpu,
        },
        config.ani_bearer_token,
    )
    node_pool_id = assert_real_node_pool_response(created)
    machine_deployment = load_machine_deployment(config, runner, namespace, machine_deployment_name)
    assert_machine_deployment(machine_deployment, config.initial_node_count, node_pool_id, config)

    updated = runner.patch_json(
        gateway + f"/k8s-clusters/{config.cluster_id}/node-pools/{node_pool_id}",
        {
            "idempotency_key": f"node-pool-live-scale-{config.cluster_id}-{node_pool_id}",
            "node_count": config.scaled_node_count,
            "instance_type": config.instance_type,
            "gpu": gpu,
        },
        config.ani_bearer_token,
    )
    assert_real_node_pool_response(updated)
    scaled = load_machine_deployment(config, runner, namespace, machine_deployment_name)
    assert_machine_deployment(scaled, config.scaled_node_count, node_pool_id, config)
    run_gpu_workload_check(config, runner, namespace)

    return {
        "status": "passed",
        "node_pool_id": node_pool_id,
        "machine_deployment": machine_deployment_name,
        "namespace": namespace,
        "scaled_replicas": config.scaled_node_count,
        "gpu_workload": config.gpu_smoke_pod_name if config.gpu_count > 0 else "",
    }


def write_live_evidence(path: Path, evidence: dict[str, object]) -> None:
    path.parent.mkdir(parents=True, exist_ok=True)
    path.write_text(json.dumps(evidence, ensure_ascii=False, indent=2, sort_keys=True) + "\n", encoding="utf-8")


def main() -> int:
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("--gate", default=str(DEFAULT_GATE), help="K8s node pool live gate YAML")
    parser.add_argument("--live", action="store_true", help="run live Core/Cluster API/GPU scheduling checks")
    parser.add_argument("--tenant-id", default=os.getenv("ANI_LIVE_TENANT_ID", "tenant-a"))
    parser.add_argument("--cluster-id", default=os.getenv("ANI_LIVE_K8S_CLUSTER_ID", ""))
    parser.add_argument("--gateway-url", default=os.getenv("ANI_GATEWAY_URL", ""))
    parser.add_argument("--ani-bearer-token", default=os.getenv("ANI_BEARER_TOKEN", ""))
    parser.add_argument("--node-pool-name", default=os.getenv("ANI_LIVE_NODE_POOL_NAME", "gpu-pool"))
    parser.add_argument("--instance-type", default=os.getenv("ANI_LIVE_NODE_POOL_INSTANCE_TYPE", "gpu.l4.xlarge"))
    parser.add_argument("--initial-node-count", type=int, default=int(os.getenv("ANI_LIVE_NODE_POOL_INITIAL_COUNT", "1")))
    parser.add_argument("--scaled-node-count", type=int, default=int(os.getenv("ANI_LIVE_NODE_POOL_SCALED_COUNT", "2")))
    parser.add_argument("--gpu-vendor", default=os.getenv("ANI_LIVE_NODE_POOL_GPU_VENDOR", "nvidia"))
    parser.add_argument("--gpu-model", default=os.getenv("ANI_LIVE_NODE_POOL_GPU_MODEL", "L4"))
    parser.add_argument("--gpu-count", type=int, default=int(os.getenv("ANI_LIVE_NODE_POOL_GPU_COUNT", "1")))
    parser.add_argument("--gpu-resource-name", default=os.getenv("ANI_LIVE_NODE_POOL_GPU_RESOURCE", "nvidia.com/gpu"))
    parser.add_argument("--kubeconfig", default=os.getenv("KUBECONFIG", ""))
    parser.add_argument("--workload-kubeconfig", default=os.getenv("ANI_WORKLOAD_KUBECONFIG", ""))
    parser.add_argument(
        "--evidence-output",
        default=os.getenv("ANI_K8S_NODE_POOL_LIVE_EVIDENCE_OUTPUT", ""),
        help="write --live evidence JSON to this path",
    )
    args = parser.parse_args()

    document = load_gate(Path(args.gate))
    validate_contract(document)
    validate_docs()
    if args.live:
        config = LiveConfig(
            tenant_id=args.tenant_id,
            cluster_id=args.cluster_id,
            gateway_url=args.gateway_url,
            ani_bearer_token=args.ani_bearer_token,
            node_pool_name=args.node_pool_name,
            instance_type=args.instance_type,
            initial_node_count=args.initial_node_count,
            scaled_node_count=args.scaled_node_count,
            gpu_vendor=args.gpu_vendor,
            gpu_model=args.gpu_model,
            gpu_count=args.gpu_count,
            gpu_resource_name=args.gpu_resource_name,
            kubeconfig=args.kubeconfig,
            workload_kubeconfig=args.workload_kubeconfig,
        )
        validate_live_config(config)
        result = run_live(config)
        if args.evidence_output:
            write_live_evidence(Path(args.evidence_output), result)
            print(f"M1-K8S-LIVE-B live checks valid; evidence written to {args.evidence_output}")
        else:
            print(f"M1-K8S-LIVE-B live checks valid: {json.dumps(result, sort_keys=True)}")
    else:
        print("M1-K8S-LIVE-B contract valid; use --live with ANI_GATEWAY_URL, ANI_BEARER_TOKEN and KUBECONFIG")
    return 0


if __name__ == "__main__":
    raise SystemExit(main())
