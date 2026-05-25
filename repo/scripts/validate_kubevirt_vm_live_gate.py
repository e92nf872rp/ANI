#!/usr/bin/env python3
"""Validate Sprint 5 M1-KUBEVIRT-LIVE-A KubeVirt VM live gate."""

from __future__ import annotations

import argparse
import json
import os
import re
import shutil
import subprocess
from dataclasses import dataclass
from pathlib import Path
from typing import Any

import yaml


ROOT = Path(__file__).resolve().parents[1]
DOC_ROOT = ROOT.parent
DEFAULT_GATE = ROOT / "deploy/real-k8s-lab/kubevirt-vm-live-gate.yaml"
REQUIRED_CHECKS = {
    "kubevirt-crds-ready",
    "kubevirt-control-plane-available",
    "kubevirt-vm-created",
    "kubevirt-vmi-ready",
    "kubevirt-vnc-subresource",
    "kubevirt-console-subresource",
    "kubevirt-vm-stopped",
    "kubevirt-vm-deleted",
}
REQUIRED_DOC_TOKENS = [
    "M1-KUBEVIRT-LIVE-A",
    "validate-kubevirt-vm-live-gate",
    "KubeVirt VM",
    "console/VNC",
]


def fail(message: str) -> None:
    raise SystemExit(f"KubeVirt VM live gate invalid: {message}")


def load_gate(path: Path) -> dict[str, Any]:
    if not path.exists():
        fail(f"missing {path.relative_to(ROOT)}")
    with path.open(encoding="utf-8") as handle:
        data = yaml.safe_load(handle)
    if not isinstance(data, dict):
        fail(f"{path.relative_to(ROOT)} must be a YAML object")
    return data


def validate_contract(document: dict[str, Any]) -> None:
    if document.get("profile") != "M1-KUBEVIRT-LIVE-A":
        fail("profile must be M1-KUBEVIRT-LIVE-A")
    if document.get("status") not in {"contract", "live", "production_like"}:
        fail("status must be contract, live or production_like")
    tools = document.get("required_tools")
    if not isinstance(tools, list) or "kubectl" not in tools:
        fail("required_tools must include kubectl")
    endpoints = document.get("required_endpoints")
    required_endpoints = {"kubernetes_api", "kubevirt_api", "kubevirt_subresources_api"}
    if not isinstance(endpoints, list) or required_endpoints - set(endpoints):
        fail("required_endpoints must include Kubernetes API, KubeVirt API and subresources API")
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
    namespace: str = ""
    vm_name: str = "ani-live-vm"
    container_disk_image: str = "quay.io/containerdisks/cirros:latest"
    memory: str = "256Mi"
    wait_timeout: str = "180s"
    kubeconfig: str = ""
    kubectl_binary: str = "kubectl"


class LiveRunner:
    def run(self, command: list[str], input_text: str | None = None) -> str:
        result = subprocess.run(command, input=input_text, text=True, capture_output=True, check=False)
        if result.returncode != 0:
            detail = result.stderr.strip() or result.stdout.strip()
            raise RuntimeError(f"{' '.join(command)} failed: {detail}")
        return result.stdout


def kubernetes_name(value: str) -> str:
    normalized = re.sub(r"[^a-z0-9-]+", "-", value.lower()).strip("-")
    if not normalized:
        normalized = "ani-live-vm"
    return normalized[:63].rstrip("-")


def tenant_namespace(config: LiveConfig) -> str:
    if config.namespace.strip():
        return config.namespace.strip()
    return "ani-tenant-" + config.tenant_id.replace("_", "-")


def kubectl(config: LiveConfig, args: list[str]) -> list[str]:
    command = [config.kubectl_binary]
    if config.kubeconfig.strip():
        command.extend(["--kubeconfig", config.kubeconfig.strip()])
    command.extend(args)
    return command


def validate_live_config(config: LiveConfig) -> None:
    required = {
        "tenant_id": config.tenant_id,
        "vm_name": config.vm_name,
        "container_disk_image": config.container_disk_image,
        "memory": config.memory,
        "wait_timeout": config.wait_timeout,
    }
    missing = [name for name, value in required.items() if not value.strip()]
    if missing:
        fail(f"live mode requires {', '.join(missing)}")
    if shutil.which(config.kubectl_binary) is None:
        fail(f"{config.kubectl_binary} is required for --live")


def vm_manifest(config: LiveConfig) -> str:
    namespace = tenant_namespace(config)
    vm_name = kubernetes_name(config.vm_name)
    return yaml.safe_dump(
        {
            "apiVersion": "kubevirt.io/v1",
            "kind": "VirtualMachine",
            "metadata": {
                "name": vm_name,
                "namespace": namespace,
                "labels": {
                    "app.kubernetes.io/part-of": "ani-platform",
                    "app.kubernetes.io/managed-by": "ani-core",
                    "ani.kubercloud.io/tenant-id": config.tenant_id,
                    "ani.kubercloud.io/live-gate": "m1-kubevirt-live-a",
                },
            },
            "spec": {
                "running": False,
                "template": {
                    "metadata": {
                        "labels": {
                            "app.kubernetes.io/part-of": "ani-platform",
                            "ani.kubercloud.io/tenant-id": config.tenant_id,
                        }
                    },
                    "spec": {
                        "domain": {
                            "devices": {"disks": [{"name": "containerdisk", "disk": {"bus": "virtio"}}]},
                            "resources": {"requests": {"memory": config.memory}},
                        },
                        "volumes": [
                            {"name": "containerdisk", "containerDisk": {"image": config.container_disk_image}}
                        ],
                    },
                },
            },
        },
        sort_keys=True,
    )


def load_json(raw: str, label: str) -> dict[str, Any]:
    try:
        document = json.loads(raw)
    except json.JSONDecodeError as err:
        fail(f"{label} did not return JSON: {err}")
    if not isinstance(document, dict):
        fail(f"{label} must return a JSON object")
    return document


def assert_kubevirt_available(document: dict[str, Any]) -> None:
    items = document.get("items")
    if not isinstance(items, list) or not items:
        fail("kubectl get kubevirt -A must return at least one item")
    for item in items:
        status = item.get("status", {}) if isinstance(item, dict) else {}
        conditions = status.get("conditions", []) if isinstance(status, dict) else []
        for condition in conditions:
            if (
                isinstance(condition, dict)
                and condition.get("type") in {"Available", "Ready"}
                and condition.get("status") == "True"
            ):
                return
    fail("KubeVirt control plane must report Available or Ready")


def assert_named_kind(document: dict[str, Any], expected_kind: str, expected_name: str) -> None:
    if document.get("kind") != expected_kind:
        fail(f"observed kind = {document.get('kind')!r}, want {expected_kind!r}")
    metadata = document.get("metadata", {})
    if not isinstance(metadata, dict) or metadata.get("name") != expected_name:
        fail(f"observed {expected_kind} metadata.name must be {expected_name}")


def assert_vmi_ready(document: dict[str, Any], expected_name: str) -> None:
    assert_named_kind(document, "VirtualMachineInstance", expected_name)
    status = document.get("status", {})
    if not isinstance(status, dict):
        fail("VirtualMachineInstance status must be an object")
    if status.get("phase") == "Running":
        return
    conditions = status.get("conditions", [])
    for condition in conditions:
        if isinstance(condition, dict) and condition.get("type") == "Ready" and condition.get("status") == "True":
            return
    fail("VirtualMachineInstance must be Running or Ready")


def subresource_path(namespace: str, vm_name: str, subresource: str) -> str:
    return f"/apis/subresources.kubevirt.io/v1/namespaces/{namespace}/virtualmachineinstances/{vm_name}/{subresource}"


def run_live(config: LiveConfig, runner: LiveRunner | None = None) -> dict[str, object]:
    runner = runner or LiveRunner()
    namespace = tenant_namespace(config)
    vm_name = kubernetes_name(config.vm_name)

    runner.run(kubectl(config, ["get", "crd", "virtualmachines.kubevirt.io", "-o", "json"]))
    runner.run(kubectl(config, ["get", "crd", "virtualmachineinstances.kubevirt.io", "-o", "json"]))
    assert_kubevirt_available(load_json(runner.run(kubectl(config, ["get", "kubevirt", "-A", "-o", "json"])), "kubectl get kubevirt"))

    runner.run(kubectl(config, ["apply", "-f", "-"]), input_text=vm_manifest(config))
    vm = load_json(
        runner.run(kubectl(config, ["get", "virtualmachine", vm_name, "-n", namespace, "-o", "json"])),
        "kubectl get virtualmachine",
    )
    assert_named_kind(vm, "VirtualMachine", vm_name)

    runner.run(
        kubectl(
            config,
            ["patch", "virtualmachine", vm_name, "-n", namespace, "--type=merge", "-p", '{"spec":{"running":true}}'],
        )
    )
    runner.run(
        kubectl(
            config,
            [
                "wait",
                "--for=condition=Ready",
                f"virtualmachineinstance/{vm_name}",
                "-n",
                namespace,
                f"--timeout={config.wait_timeout}",
            ],
        )
    )
    vmi = load_json(
        runner.run(kubectl(config, ["get", "virtualmachineinstance", vm_name, "-n", namespace, "-o", "json"])),
        "kubectl get virtualmachineinstance",
    )
    assert_vmi_ready(vmi, vm_name)

    for subresource in ("vnc", "console"):
        runner.run(kubectl(config, ["get", "--raw", subresource_path(namespace, vm_name, subresource)]))

    runner.run(
        kubectl(
            config,
            ["patch", "virtualmachine", vm_name, "-n", namespace, "--type=merge", "-p", '{"spec":{"running":false}}'],
        )
    )
    runner.run(
        kubectl(
            config,
            [
                "wait",
                "--for=delete",
                f"virtualmachineinstance/{vm_name}",
                "-n",
                namespace,
                f"--timeout={config.wait_timeout}",
            ],
        )
    )

    runner.run(kubectl(config, ["delete", "virtualmachine", vm_name, "-n", namespace, "--ignore-not-found=true"]))
    runner.run(
        kubectl(
            config,
            ["wait", "--for=delete", f"virtualmachine/{vm_name}", "-n", namespace, f"--timeout={config.wait_timeout}"],
        )
    )

    return {"status": "passed", "namespace": namespace, "vm": vm_name}


def write_live_evidence(path: Path, evidence: dict[str, object]) -> None:
    path.parent.mkdir(parents=True, exist_ok=True)
    path.write_text(json.dumps(evidence, ensure_ascii=False, indent=2, sort_keys=True) + "\n", encoding="utf-8")


def main() -> int:
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("--gate", default=str(DEFAULT_GATE), help="KubeVirt VM live gate YAML")
    parser.add_argument("--live", action="store_true", help="run live kubectl KubeVirt VM checks")
    parser.add_argument("--tenant-id", default=os.getenv("ANI_LIVE_TENANT_ID", "tenant-a"))
    parser.add_argument("--namespace", default=os.getenv("ANI_LIVE_NAMESPACE", ""))
    parser.add_argument("--vm-name", default=os.getenv("ANI_LIVE_KUBEVIRT_VM_NAME", "ani-live-vm"))
    parser.add_argument("--container-disk-image", default=os.getenv("ANI_LIVE_KUBEVIRT_IMAGE", "quay.io/containerdisks/cirros:latest"))
    parser.add_argument("--memory", default=os.getenv("ANI_LIVE_KUBEVIRT_MEMORY", "256Mi"))
    parser.add_argument("--wait-timeout", default=os.getenv("ANI_LIVE_KUBEVIRT_WAIT_TIMEOUT", "180s"))
    parser.add_argument("--kubeconfig", default=os.getenv("KUBECONFIG", ""))
    parser.add_argument(
        "--evidence-output",
        default=os.getenv("ANI_KUBEVIRT_VM_LIVE_EVIDENCE_OUTPUT", ""),
        help="write --live evidence JSON to this path",
    )
    args = parser.parse_args()

    document = load_gate(Path(args.gate))
    validate_contract(document)
    validate_docs()
    if args.live:
        config = LiveConfig(
            tenant_id=args.tenant_id,
            namespace=args.namespace,
            vm_name=args.vm_name,
            container_disk_image=args.container_disk_image,
            memory=args.memory,
            wait_timeout=args.wait_timeout,
            kubeconfig=args.kubeconfig,
        )
        validate_live_config(config)
        result = run_live(config)
        if args.evidence_output:
            write_live_evidence(Path(args.evidence_output), result)
            print(f"M1-KUBEVIRT-LIVE-A live checks valid; evidence written to {args.evidence_output}")
        else:
            print(f"M1-KUBEVIRT-LIVE-A live checks valid: {json.dumps(result, sort_keys=True)}")
    else:
        print("M1-KUBEVIRT-LIVE-A contract valid; use --live with KUBECONFIG against REAL-K8S-LAB-A")
    return 0


if __name__ == "__main__":
    raise SystemExit(main())
