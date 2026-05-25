#!/usr/bin/env python3
"""Validate Sprint 5 M1-NETWORK-LIVE-A Kube-OVN network live gate."""

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
DEFAULT_GATE = ROOT / "deploy/real-k8s-lab/kubeovn-network-live-gate.yaml"
REQUIRED_CHECKS = {
    "kubeovn-crds-ready",
    "kubeovn-vpc-created",
    "kubeovn-subnet-created",
    "networkpolicy-created",
    "service-lb-created",
}
REQUIRED_DOC_TOKENS = [
    "M1-NETWORK-LIVE-A",
    "validate-kubeovn-network-live-gate",
    "Kube-OVN",
    "Vpc/Subnet",
]


def fail(message: str) -> None:
    raise SystemExit(f"Kube-OVN network live gate invalid: {message}")


def load_gate(path: Path) -> dict[str, Any]:
    if not path.exists():
        fail(f"missing {path.relative_to(ROOT)}")
    with path.open(encoding="utf-8") as handle:
        data = yaml.safe_load(handle)
    if not isinstance(data, dict):
        fail(f"{path.relative_to(ROOT)} must be a YAML object")
    return data


def validate_contract(document: dict[str, Any]) -> None:
    if document.get("profile") != "M1-NETWORK-LIVE-A":
        fail("profile must be M1-NETWORK-LIVE-A")
    if document.get("status") not in {"contract", "live", "production_like"}:
        fail("status must be contract, live or production_like")
    tools = document.get("required_tools")
    if not isinstance(tools, list) or "kubectl" not in tools:
        fail("required_tools must include kubectl")
    endpoints = document.get("required_endpoints")
    required_endpoints = {"kubernetes_api", "kube_ovn_crds", "networking_k8s_api"}
    if not isinstance(endpoints, list) or required_endpoints - set(endpoints):
        fail("required_endpoints must include Kubernetes API, Kube-OVN CRDs and NetworkPolicy API")
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
    vpc_name: str = "ani-live-net"
    subnet_name: str = "ani-live-subnet"
    security_group_name: str = "ani-live-sg"
    load_balancer_name: str = "ani-live-lb"
    namespace: str = ""
    cidr: str = "10.244.80.0/24"
    gateway: str = "10.244.80.1"
    kubeconfig: str = ""
    kubectl_binary: str = "kubectl"


class LiveRunner:
    def run(self, command: list[str], input_text: str | None = None) -> str:
        result = subprocess.run(command, input=input_text, text=True, capture_output=True, check=False)
        if result.returncode != 0:
            detail = result.stderr.strip() or result.stdout.strip()
            raise RuntimeError(f"{' '.join(command)} failed: {detail}")
        return result.stdout


def kubernetes_name(prefix: str, value: str) -> str:
    normalized = re.sub(r"[^a-z0-9-]+", "-", value.lower()).strip("-")
    if not normalized:
        normalized = "resource"
    name = f"{prefix}-{normalized}"
    return name[:63].rstrip("-")


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
        "vpc_name": config.vpc_name,
        "subnet_name": config.subnet_name,
        "security_group_name": config.security_group_name,
        "load_balancer_name": config.load_balancer_name,
        "cidr": config.cidr,
    }
    missing = [name for name, value in required.items() if not value.strip()]
    if missing:
        fail(f"live mode requires {', '.join(missing)}")
    if shutil.which(config.kubectl_binary) is None:
        fail(f"{config.kubectl_binary} is required for --live")


def vpc_manifest(config: LiveConfig) -> str:
    namespace = tenant_namespace(config)
    vpc_name = kubernetes_name("vpc", config.vpc_name)
    return yaml.safe_dump(
        {
            "apiVersion": "kubeovn.io/v1",
            "kind": "Vpc",
            "metadata": {"name": vpc_name, "labels": provider_labels(config.tenant_id, "vpc", config.vpc_name)},
            "spec": {"namespaces": [namespace]},
        },
        sort_keys=True,
    )


def subnet_manifest(config: LiveConfig) -> str:
    namespace = tenant_namespace(config)
    subnet_name = kubernetes_name("subnet", config.subnet_name)
    spec: dict[str, object] = {
        "protocol": "IPv4",
        "cidrBlock": config.cidr,
        "vpc": kubernetes_name("vpc", config.vpc_name),
        "namespaces": [namespace],
        "private": True,
        "natOutgoing": False,
    }
    if config.gateway.strip():
        spec["gateway"] = config.gateway.strip()
    return yaml.safe_dump(
        {
            "apiVersion": "kubeovn.io/v1",
            "kind": "Subnet",
            "metadata": {"name": subnet_name, "labels": provider_labels(config.tenant_id, "subnet", config.subnet_name)},
            "spec": spec,
        },
        sort_keys=True,
    )


def networkpolicy_manifest(config: LiveConfig) -> str:
    name = kubernetes_name("sg", config.security_group_name)
    return yaml.safe_dump(
        {
            "apiVersion": "networking.k8s.io/v1",
            "kind": "NetworkPolicy",
            "metadata": {
                "name": name,
                "namespace": tenant_namespace(config),
                "labels": provider_labels(config.tenant_id, "security-group", config.security_group_name),
            },
            "spec": {
                "podSelector": {},
                "policyTypes": ["Ingress", "Egress"],
                "ingress": [{"from": [{"ipBlock": {"cidr": "0.0.0.0/0"}}]}],
                "egress": [{"to": [{"ipBlock": {"cidr": "0.0.0.0/0"}}]}],
            },
        },
        sort_keys=True,
    )


def service_manifest(config: LiveConfig) -> str:
    name = kubernetes_name("lb", config.load_balancer_name)
    return yaml.safe_dump(
        {
            "apiVersion": "v1",
            "kind": "Service",
            "metadata": {
                "name": name,
                "namespace": tenant_namespace(config),
                "labels": provider_labels(config.tenant_id, "load-balancer", config.load_balancer_name),
                "annotations": {
                    "ani.kubercloud.io/load-balancer-scheme": "public",
                    "ani.kubercloud.io/vpc-id": config.vpc_name,
                    "ani.kubercloud.io/subnet-id": config.subnet_name,
                },
            },
            "spec": {
                "type": "LoadBalancer",
                "selector": {"ani.kubercloud.io/network-load-balancer": config.load_balancer_name},
                "ports": [{"name": "tcp-80", "protocol": "TCP", "port": 80, "targetPort": 8080}],
            },
        },
        sort_keys=True,
    )


def provider_labels(tenant_id: str, resource_kind: str, resource_id: str) -> dict[str, str]:
    return {
        "app.kubernetes.io/part-of": "ani-platform",
        "app.kubernetes.io/managed-by": "ani-core",
        "ani.kubercloud.io/tenant-id": tenant_id,
        "ani.kubercloud.io/network-kind": resource_kind,
        "ani.kubercloud.io/network-resource": resource_id,
    }


def load_json(raw: str, label: str) -> dict[str, Any]:
    try:
        document = json.loads(raw)
    except json.JSONDecodeError as err:
        fail(f"{label} did not return JSON: {err}")
    if not isinstance(document, dict):
        fail(f"{label} must return a JSON object")
    return document


def assert_observable_kubernetes_object(document: dict[str, Any], expected_kind: str, expected_name: str) -> None:
    if document.get("kind") != expected_kind:
        fail(f"observed kind = {document.get('kind')!r}, want {expected_kind!r}")
    metadata = document.get("metadata", {})
    if not isinstance(metadata, dict) or metadata.get("name") != expected_name:
        fail(f"observed {expected_kind} metadata.name must be {expected_name}")
    conditions = document.get("status", {}).get("conditions", []) if isinstance(document.get("status"), dict) else []
    for condition in conditions:
        if isinstance(condition, dict) and condition.get("type") == "Ready" and condition.get("status") == "False":
            fail(f"{expected_kind} {expected_name} is explicitly not Ready")


def assert_service(document: dict[str, Any], expected_name: str) -> None:
    assert_observable_kubernetes_object(document, "Service", expected_name)
    spec = document.get("spec", {})
    if isinstance(spec, dict) and spec.get("type") not in {None, "LoadBalancer"}:
        fail(f"Service {expected_name} type must be LoadBalancer")


def run_live(config: LiveConfig, runner: LiveRunner | None = None) -> dict[str, object]:
    runner = runner or LiveRunner()
    namespace = tenant_namespace(config)
    vpc_name = kubernetes_name("vpc", config.vpc_name)
    subnet_name = kubernetes_name("subnet", config.subnet_name)
    security_group_name = kubernetes_name("sg", config.security_group_name)
    load_balancer_name = kubernetes_name("lb", config.load_balancer_name)

    runner.run(kubectl(config, ["get", "crd", "vpcs.kubeovn.io", "-o", "json"]))
    runner.run(kubectl(config, ["get", "crd", "subnets.kubeovn.io", "-o", "json"]))
    auth = runner.run(kubectl(config, ["auth", "can-i", "create", "vpcs.kubeovn.io"]))
    if auth.strip() != "yes":
        fail("kubectl auth can-i create vpcs.kubeovn.io must return yes")

    runner.run(kubectl(config, ["apply", "-f", "-"]), input_text=vpc_manifest(config))
    runner.run(kubectl(config, ["apply", "-f", "-"]), input_text=subnet_manifest(config))
    runner.run(kubectl(config, ["apply", "-f", "-"]), input_text=networkpolicy_manifest(config))
    runner.run(kubectl(config, ["apply", "-f", "-"]), input_text=service_manifest(config))

    vpc = load_json(runner.run(kubectl(config, ["get", "vpc", vpc_name, "-o", "json"])), "kubectl get vpc")
    subnet = load_json(runner.run(kubectl(config, ["get", "subnet", subnet_name, "-o", "json"])), "kubectl get subnet")
    networkpolicy = load_json(
        runner.run(kubectl(config, ["get", "networkpolicy", security_group_name, "-n", namespace, "-o", "json"])),
        "kubectl get networkpolicy",
    )
    service = load_json(
        runner.run(kubectl(config, ["get", "service", load_balancer_name, "-n", namespace, "-o", "json"])),
        "kubectl get service",
    )

    assert_observable_kubernetes_object(vpc, "Vpc", vpc_name)
    assert_observable_kubernetes_object(subnet, "Subnet", subnet_name)
    assert_observable_kubernetes_object(networkpolicy, "NetworkPolicy", security_group_name)
    assert_service(service, load_balancer_name)

    return {
        "status": "passed",
        "namespace": namespace,
        "vpc": vpc_name,
        "subnet": subnet_name,
        "security_group": security_group_name,
        "load_balancer": load_balancer_name,
    }


def write_live_evidence(path: Path, evidence: dict[str, object]) -> None:
    path.parent.mkdir(parents=True, exist_ok=True)
    path.write_text(json.dumps(evidence, ensure_ascii=False, indent=2, sort_keys=True) + "\n", encoding="utf-8")


def main() -> int:
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("--gate", default=str(DEFAULT_GATE), help="Kube-OVN network live gate YAML")
    parser.add_argument("--live", action="store_true", help="run live kubectl Kube-OVN checks")
    parser.add_argument("--tenant-id", default=os.getenv("ANI_LIVE_TENANT_ID", "tenant-a"))
    parser.add_argument("--namespace", default=os.getenv("ANI_LIVE_NAMESPACE", ""))
    parser.add_argument("--vpc-name", default=os.getenv("ANI_LIVE_VPC_NAME", "ani-live-net"))
    parser.add_argument("--subnet-name", default=os.getenv("ANI_LIVE_SUBNET_NAME", "ani-live-subnet"))
    parser.add_argument("--security-group-name", default=os.getenv("ANI_LIVE_SECURITY_GROUP_NAME", "ani-live-sg"))
    parser.add_argument("--load-balancer-name", default=os.getenv("ANI_LIVE_LOAD_BALANCER_NAME", "ani-live-lb"))
    parser.add_argument("--cidr", default=os.getenv("ANI_LIVE_SUBNET_CIDR", "10.244.80.0/24"))
    parser.add_argument("--gateway", default=os.getenv("ANI_LIVE_SUBNET_GATEWAY", "10.244.80.1"))
    parser.add_argument("--kubeconfig", default=os.getenv("KUBECONFIG", ""))
    parser.add_argument(
        "--evidence-output",
        default=os.getenv("ANI_KUBEOVN_NETWORK_LIVE_EVIDENCE_OUTPUT", ""),
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
            vpc_name=args.vpc_name,
            subnet_name=args.subnet_name,
            security_group_name=args.security_group_name,
            load_balancer_name=args.load_balancer_name,
            cidr=args.cidr,
            gateway=args.gateway,
            kubeconfig=args.kubeconfig,
        )
        validate_live_config(config)
        result = run_live(config)
        if args.evidence_output:
            write_live_evidence(Path(args.evidence_output), result)
            print(f"M1-NETWORK-LIVE-A live checks valid; evidence written to {args.evidence_output}")
        else:
            print(f"M1-NETWORK-LIVE-A live checks valid: {json.dumps(result, sort_keys=True)}")
    else:
        print("M1-NETWORK-LIVE-A contract valid; use --live with KUBECONFIG against REAL-K8S-LAB-A")
    return 0


if __name__ == "__main__":
    raise SystemExit(main())
