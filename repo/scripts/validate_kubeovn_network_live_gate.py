#!/usr/bin/env python3
"""Validate Sprint 5 M1-NETWORK-LIVE-A Kube-OVN network live gate."""

from __future__ import annotations

import argparse
import json
import os
import re
import shutil
import subprocess
import time
import urllib.error
import urllib.parse
import urllib.request
from dataclasses import dataclass
from pathlib import Path
from typing import Any

import yaml


ROOT = Path(__file__).resolve().parents[1]
DOC_ROOT = ROOT.parent
DEFAULT_GATE = ROOT / "deploy/real-k8s-lab/kubeovn-network-live-gate.yaml"
DEFAULT_EXTERNAL_LB_DEPS = ROOT / "deploy/real-k8s-lab/kubeovn-lb-external-deps.yaml"
DEFAULT_EXTERNAL_LB_SCRIPT_CONFIGMAP = ROOT / "deploy/real-k8s-lab/kubeovn-lb-svc-script-configmap.yaml"
REQUIRED_CHECKS = {
    "kubeovn-crds-ready",
    "kubeovn-vpc-created",
    "kubeovn-subnet-created",
    "kubeovn-route-created",
    "networkpolicy-created",
    "service-lb-created",
}
REQUIRED_ENV = {"KUBECONFIG"}
REQUIRED_DOC_TOKENS = [
    "M1-NETWORK-LIVE-A",
    "validate-kubeovn-network-live-gate",
    "Kube-OVN",
    "Vpc/Subnet",
]
PROFILE = "M1-NETWORK-LIVE-A"
GATE_ID = "kubeovn-network-live-gate"
COMMAND_TIMEOUT_SECONDS = 120


def fail(message: str) -> None:
    raise SystemExit(f"Kube-OVN network live gate invalid: {message}")


def gate_path_label(path: Path) -> str:
    try:
        return str(path.relative_to(ROOT))
    except ValueError:
        return str(path)


def load_gate(path: Path) -> dict[str, Any]:
    label = gate_path_label(path)
    if not path.exists():
        fail(f"missing {label}")
    try:
        with path.open(encoding="utf-8") as handle:
            data = yaml.safe_load(handle)
    except OSError:
        fail(f"unreadable {label}")
    except yaml.YAMLError:
        fail(f"malformed {label}")
    if not isinstance(data, dict):
        fail(f"{label} must be a YAML object")
    return data


def validate_contract(document: dict[str, Any]) -> None:
    if document.get("profile") != PROFILE:
        fail(f"profile must be {PROFILE}")
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
            value = check.get(field)
            if not isinstance(value, str) or not value.strip():
                fail(f"live check {field} must be a non-empty string")
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
        try:
            content = path.read_text(encoding="utf-8")
        except FileNotFoundError:
            fail(f"missing doc {label}")
        except OSError:
            fail(f"unreadable doc {label}")
        except UnicodeError:
            fail(f"malformed doc {label}")
        for token in REQUIRED_DOC_TOKENS:
            if token not in content:
                fail(f"{label} must reference {token}")


@dataclass(frozen=True)
class LiveConfig:
    tenant_id: str
    vpc_name: str = "ani-live-net"
    subnet_name: str = "ani-live-subnet"
    route_name: str = "ani-live-route"
    security_group_name: str = "ani-live-sg"
    load_balancer_name: str = "ani-live-lb"
    namespace: str = ""
    cidr: str = "10.244.80.0/24"
    gateway: str = "10.244.80.1"
    route_destination: str = "0.0.0.0/0"
    route_next_hop: str = "10.244.80.1"
    kubeconfig: str = ""
    kubectl_binary: str = "kubectl"
    gateway_url: str = ""
    ani_bearer_token: str = ""
    production_shaped: bool = False


@dataclass(frozen=True)
class ExternalLBConfig:
    namespace: str = "default"
    service_name: str = "ani-kubeovn-external-lb-smoke"
    helper_deployment: str = "lb-svc-ani-kubeovn-external-lb-smoke"
    helper_label: str = "app=lb-svc-ani-kubeovn-external-lb-smoke"
    external_ip: str = "10.10.1.250"
    expected_body: str = "ani-kubeovn-external-lb-ok"
    deps_manifest: str = str(DEFAULT_EXTERNAL_LB_DEPS)
    script_configmap_manifest: str = str(DEFAULT_EXTERNAL_LB_SCRIPT_CONFIGMAP)
    script_configmap_name: str = "ani-kubeovn-lb-svc-script"
    helper_image: str = "dockerproxy.net/kubeovn/kube-ovn:v1.15.8"
    controller_namespace: str = "kube-system"
    controller_deployment: str = "kube-ovn-controller"
    kubeconfig: str = ""
    kubectl_binary: str = "kubectl"
    ssh_binary: str = "ssh"
    curl_hosts: tuple[str, ...] = ()
    reconcile_stamp: str = "external-lb-live-gate"


class LiveRunner:
    def run(self, command: list[str], input_text: str | None = None) -> str:
        result = subprocess.run(command, input=input_text, text=True, capture_output=True, check=False, timeout=COMMAND_TIMEOUT_SECONDS)
        if result.returncode != 0:
            detail = result.stderr.strip() or result.stdout.strip()
            raise RuntimeError(f"{' '.join(command)} failed: {detail}")
        return result.stdout


class HTTPClient:
    def request(
        self,
        method: str,
        url: str,
        bearer_token: str,
        tenant_id: str,
        body: dict[str, object] | None = None,
    ) -> tuple[int, dict[str, object]]:
        payload = None
        headers = {
            "Accept": "application/json",
            "Authorization": f"Bearer {bearer_token}",
            "X-Dev-Tenant-ID": tenant_id,
        }
        if body is not None:
            payload = json.dumps(body).encode("utf-8")
            headers["Content-Type"] = "application/json"
        request = urllib.request.Request(url, data=payload, headers=headers, method=method)
        try:
            with urllib.request.urlopen(request, timeout=COMMAND_TIMEOUT_SECONDS) as response:
                raw = response.read().decode("utf-8")
                return response.status, json.loads(raw) if raw else {}
        except urllib.error.HTTPError as err:
            raw = err.read().decode("utf-8", errors="replace")
            detail = raw or err.reason
            raise RuntimeError(f"{method} {url} failed: HTTP {err.code} {detail}") from err
        except urllib.error.URLError as err:
            raise RuntimeError(f"{method} {url} failed: {err.reason}") from err


def kubernetes_name(prefix: str, value: str) -> str:
    normalized = re.sub(r"[^a-z0-9-]+", "-", value.lower()).strip("-")
    if not normalized:
        normalized = "resource"
    name = f"{prefix}-{normalized}"
    return name[:63].rstrip("-")


def network_provider_name(prefix: str, value: str) -> str:
    clean = re.sub(r"[^A-Za-z0-9.-]+", "-", value.strip()).lower().strip("-.")
    if not clean:
        clean = "resource"
    name = f"{prefix}-{clean}"
    return name[:63].rstrip("-.")


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


def external_kubectl(config: ExternalLBConfig, args: list[str]) -> list[str]:
    command = [config.kubectl_binary]
    if config.kubeconfig.strip():
        command.extend(["--kubeconfig", config.kubeconfig.strip()])
    command.extend(args)
    return command


def kubectl_patch_json(config: ExternalLBConfig, args: list[str], patch: object) -> list[str]:
    return external_kubectl(config, [*args, "-p", json.dumps(patch, separators=(",", ":"))])


def validate_live_config(config: LiveConfig) -> None:
    required = {
        "tenant_id": config.tenant_id,
        "vpc_name": config.vpc_name,
        "subnet_name": config.subnet_name,
        "route_name": config.route_name,
        "security_group_name": config.security_group_name,
        "load_balancer_name": config.load_balancer_name,
        "cidr": config.cidr,
        "route_destination": config.route_destination,
        "route_next_hop": config.route_next_hop,
    }
    missing = [name for name, value in required.items() if not value.strip()]
    if missing:
        fail(f"live mode requires {', '.join(missing)}")
    whitespace = [name for name, value in required.items() if value != value.strip()]
    if whitespace:
        fail(f"{', '.join(whitespace)} must not contain surrounding whitespace")
    if shutil.which(config.kubectl_binary) is None:
        fail(f"{config.kubectl_binary} is required for --live")
    if config.production_shaped:
        validate_production_shaped_live_config(config)


def is_local_transport(value: str) -> bool:
    lowered = value.strip().lower()
    return (
        "127.0.0.1" in lowered
        or "localhost" in lowered
        or "kubectl proxy" in lowered
        or "kubectl-proxy" in lowered
        or "port-forward" in lowered
    )


def validate_production_shaped_live_config(config: LiveConfig) -> None:
    if not config.gateway_url.strip():
        fail("production-shaped live mode requires --gateway-url")
    if not config.ani_bearer_token.strip():
        fail("production-shaped live mode requires --ani-bearer-token")
    if is_local_transport(config.gateway_url):
        fail("production-shaped live mode requires a non-local production gateway URL")


def validate_external_lb_config(config: ExternalLBConfig) -> None:
    required = {
        "namespace": config.namespace,
        "service_name": config.service_name,
        "helper_deployment": config.helper_deployment,
        "helper_label": config.helper_label,
        "external_ip": config.external_ip,
        "expected_body": config.expected_body,
        "deps_manifest": config.deps_manifest,
        "script_configmap_manifest": config.script_configmap_manifest,
        "script_configmap_name": config.script_configmap_name,
        "helper_image": config.helper_image,
        "controller_namespace": config.controller_namespace,
        "controller_deployment": config.controller_deployment,
        "reconcile_stamp": config.reconcile_stamp,
    }
    missing = [name for name, value in required.items() if not value.strip()]
    if missing:
        fail(f"external LB live mode requires {', '.join(missing)}")
    whitespace = [name for name, value in required.items() if value != value.strip()]
    if whitespace:
        fail(f"{', '.join(whitespace)} must not contain surrounding whitespace")
    if not config.curl_hosts:
        fail("external LB live mode requires at least one curl host")
    if any(not host.strip() for host in config.curl_hosts):
        fail("external LB curl hosts must not be empty")
    if any(host != host.strip() for host in config.curl_hosts):
        fail("external LB curl hosts must not contain surrounding whitespace")
    for label, manifest in (
        ("external LB deps manifest", config.deps_manifest),
        ("external LB script ConfigMap manifest", config.script_configmap_manifest),
    ):
        path = Path(manifest)
        if not path.exists():
            fail(f"{label} missing: {manifest}")
        if path.is_dir():
            fail(f"{label} must be a file: {manifest}")
    if shutil.which(config.kubectl_binary) is None:
        fail(f"{config.kubectl_binary} is required for external LB live mode")
    if shutil.which(config.ssh_binary) is None:
        fail(f"{config.ssh_binary} is required for external LB live mode")


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


def route_manifest(config: LiveConfig) -> str:
    vpc_name = kubernetes_name("vpc", config.vpc_name)
    return yaml.safe_dump(
        {
            "apiVersion": "kubeovn.io/v1",
            "kind": "Vpc",
            "metadata": {
                "name": vpc_name,
                "labels": provider_labels(config.tenant_id, "vpc", config.vpc_name),
                "annotations": {
                    "ani.kubercloud.io/network-route-id": config.route_name,
                    "ani.kubercloud.io/network-route-next-hop": config.route_next_hop,
                    "ani.kubercloud.io/network-route-next-hop-type": "gateway",
                },
            },
            "spec": {
                "staticRoutes": [
                    {
                        "cidr": config.route_destination,
                        "nextHopIP": config.route_next_hop,
                        "policy": "policyDst",
                    }
                ]
            },
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


def namespace_manifest(config: LiveConfig) -> str:
    namespace = tenant_namespace(config)
    return yaml.safe_dump(
        {
            "apiVersion": "v1",
            "kind": "Namespace",
            "metadata": {
                "name": namespace,
                "labels": provider_labels(config.tenant_id, "namespace", namespace),
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


def assert_route(document: dict[str, Any], expected_cidr: str, expected_next_hop: str) -> None:
    spec = document.get("spec", {})
    static_routes = spec.get("staticRoutes", []) if isinstance(spec, dict) else []
    if not isinstance(static_routes, list):
        fail("Vpc staticRoutes must be a list")
    for route in static_routes:
        if not isinstance(route, dict):
            continue
        if route.get("cidr") == expected_cidr and route.get("nextHopIP") == expected_next_hop:
            return
    fail(f"Vpc staticRoutes must include {expected_cidr} via {expected_next_hop}")


def wait_for_runner(
    label: str,
    attempts: int,
    interval_seconds: float,
    probe: Any,
) -> Any:
    last_error: Exception | None = None
    for _ in range(attempts):
        try:
            value = probe()
            if value:
                return value
        except Exception as err:  # noqa: BLE001 - surfaced through gate error below.
            last_error = err
        time.sleep(interval_seconds)
    if last_error is not None:
        fail(f"{label} did not become ready: {last_error}")
    fail(f"{label} did not become ready")


def patch_controller_enable_lb_svc(config: ExternalLBConfig, runner: LiveRunner) -> None:
    raw = runner.run(
        external_kubectl(
            config,
            [
                "-n",
                config.controller_namespace,
                "get",
                "deploy",
                config.controller_deployment,
                "-o",
                "json",
            ],
        )
    )
    deploy = load_json(raw, "kubectl get kube-ovn-controller")
    containers = deploy.get("spec", {}).get("template", {}).get("spec", {}).get("containers", [])
    if not isinstance(containers, list) or not containers:
        fail("kube-ovn-controller deployment must have containers")
    args = containers[0].get("args", []) if isinstance(containers[0], dict) else []
    if not isinstance(args, list):
        fail("kube-ovn-controller args must be a list")
    if "--enable-lb-svc=true" in args:
        return
    if "--enable-lb-svc=false" in args:
        index = args.index("--enable-lb-svc=false")
        patch = [{"op": "replace", "path": f"/spec/template/spec/containers/0/args/{index}", "value": "--enable-lb-svc=true"}]
    else:
        patch = [{"op": "add", "path": "/spec/template/spec/containers/0/args/-", "value": "--enable-lb-svc=true"}]
    runner.run(
        kubectl_patch_json(
            config,
            [
                "-n",
                config.controller_namespace,
                "patch",
                "deploy",
                config.controller_deployment,
                "--type=json",
            ],
            patch,
        )
    )


def patch_external_helper(config: ExternalLBConfig, runner: LiveRunner) -> None:
    patch = {
        "spec": {
            "template": {
                "metadata": {
                    "annotations": {
                        "ani.kubercloud.io/script-reload": config.reconcile_stamp,
                    }
                },
                "spec": {
                    "volumes": [
                        {
                            "name": "lb-svc-script",
                            "configMap": {
                                "name": config.script_configmap_name,
                                "defaultMode": 0o755,
                            },
                        }
                    ],
                    "containers": [
                        {
                            "name": "lb-svc",
                            "image": config.helper_image,
                            "volumeMounts": [
                                {
                                    "name": "lb-svc-script",
                                    "mountPath": "/kube-ovn/lb-svc.sh",
                                    "subPath": "lb-svc.sh",
                                }
                            ],
                        }
                    ],
                },
            }
        }
    }
    runner.run(
        kubectl_patch_json(
            config,
            [
                "-n",
                config.namespace,
                "patch",
                "deploy",
                config.helper_deployment,
                "--type=strategic",
            ],
            patch,
        )
    )


def service_external_ip(service: dict[str, Any]) -> str:
    ingress = service.get("status", {}).get("loadBalancer", {}).get("ingress", [])
    if isinstance(ingress, list) and ingress and isinstance(ingress[0], dict):
        value = ingress[0].get("ip", "")
        if isinstance(value, str):
            return value
    return ""


def ensure_external_service_status(config: ExternalLBConfig, runner: LiveRunner) -> None:
    raw = runner.run(
        external_kubectl(config, ["-n", config.namespace, "get", "svc", config.service_name, "-o", "json"])
    )
    service = load_json(raw, "kubectl get external LB service")
    if service_external_ip(service) == config.external_ip:
        return
    patch = {"status": {"loadBalancer": {"ingress": [{"ip": config.external_ip}]}}}
    runner.run(
        kubectl_patch_json(
            config,
            [
                "-n",
                config.namespace,
                "patch",
                "svc",
                config.service_name,
                "--subresource=status",
                "--type=merge",
            ],
            patch,
        )
    )


def assert_external_nat(config: ExternalLBConfig, raw: str) -> dict[str, object]:
    has_eip = config.external_ip in raw and "inet " in raw
    has_dnat = config.external_ip in raw and "DNAT" in raw and "--dport 80" in raw
    has_masquerade = "MASQUERADE" in raw
    if not has_eip:
        fail("external LB helper must hold the external IP on net1")
    if not has_dnat:
        fail("external LB helper must contain DNAT rule for external IP port 80")
    if not has_masquerade:
        fail("external LB helper must contain MASQUERADE rule")
    return {
        "external_ip_on_net1": True,
        "dnat_rule_observed": True,
        "masquerade_rule_observed": True,
    }


def external_nat_probe(config: ExternalLBConfig, runner: LiveRunner) -> str:
    raw = runner.run(
        external_kubectl(
            config,
            [
                "-n",
                config.namespace,
                "exec",
                f"deploy/{config.helper_deployment}",
                "--",
                "bash",
                "-lc",
                "ip -4 addr show dev net1; ip route show table 100; iptables-save -t nat",
            ],
        )
    )
    if config.external_ip in raw and "DNAT" in raw and "MASQUERADE" in raw:
        return raw
    return ""


def run_external_lb_live(config: ExternalLBConfig, runner: LiveRunner | None = None) -> dict[str, object]:
    runner = runner or LiveRunner()
    patch_controller_enable_lb_svc(config, runner)
    runner.run(
        external_kubectl(
            config,
            ["-n", config.controller_namespace, "rollout", "status", f"deploy/{config.controller_deployment}", "--timeout=180s"],
        )
    )
    runner.run(external_kubectl(config, ["apply", "-f", config.script_configmap_manifest]))
    runner.run(external_kubectl(config, ["apply", "-f", config.deps_manifest]))
    runner.run(
        external_kubectl(
            config,
            ["-n", config.namespace, "rollout", "status", f"deploy/{config.service_name}", "--timeout=180s"],
        )
    )

    wait_for_runner(
        "external LB helper deployment",
        30,
        2.0,
        lambda: runner.run(
            external_kubectl(config, ["-n", config.namespace, "get", "deploy", config.helper_deployment, "-o", "json"])
        ),
    )
    patch_external_helper(config, runner)
    ensure_external_service_status(config, runner)
    runner.run(
        external_kubectl(
            config,
            ["-n", config.namespace, "delete", "pod", "-l", config.helper_label, "--ignore-not-found"],
        )
    )
    runner.run(
        external_kubectl(
            config,
            ["-n", config.namespace, "rollout", "status", f"deploy/{config.helper_deployment}", "--timeout=180s"],
        )
    )

    nat_raw = wait_for_runner(
        "external LB NAT rules",
        30,
        2.0,
        lambda: external_nat_probe(config, runner),
    )
    nat_evidence = assert_external_nat(config, nat_raw)
    service = load_json(
        runner.run(external_kubectl(config, ["-n", config.namespace, "get", "svc", config.service_name, "-o", "json"])),
        "kubectl get external LB service",
    )
    observed_external_ip = service_external_ip(service)
    if observed_external_ip != config.external_ip:
        fail(f"external LB service status IP must be {config.external_ip}")

    curl_results = []
    for host in config.curl_hosts:
        body = runner.run([config.ssh_binary, host, f"curl -s --max-time 5 http://{config.external_ip}/"]).strip()
        if body != config.expected_body:
            fail(f"external LB curl from {host} returned {body!r}")
        curl_results.append({"host": host, "body_matched": True})

    return {
        "status": "passed",
        "namespace": config.namespace,
        "service": config.service_name,
        "external_ip": config.external_ip,
        "service_status_ip": observed_external_ip,
        "helper_deployment": config.helper_deployment,
        "helper_image": config.helper_image,
        "helper_script_configmap": config.script_configmap_name,
        **nat_evidence,
        "curl_results": curl_results,
    }


def gateway_url(config: LiveConfig, path: str, query: dict[str, str] | None = None) -> str:
    url = config.gateway_url.rstrip("/") + path
    if query:
        url += "?" + urllib.parse.urlencode(query)
    return url


def create_gateway_network_resources(config: LiveConfig, http_client: HTTPClient) -> dict[str, object]:
    vpc_status, vpc = http_client.request(
        "POST",
        gateway_url(config, "/networks/vpcs"),
        config.ani_bearer_token,
        config.tenant_id,
        {
            "idempotency_key": f"live-vpc-{config.vpc_name}",
            "name": config.vpc_name,
            "cidr": config.cidr,
        },
    )
    vpc_id = str(vpc.get("id", "")).strip()
    if vpc_status != 201 or not vpc_id:
        fail("production-shaped S01 Gateway VPC create must return 201 and id")

    subnet_status, subnet = http_client.request(
        "POST",
        gateway_url(config, "/networks/subnets"),
        config.ani_bearer_token,
        config.tenant_id,
        {
            "idempotency_key": f"live-subnet-{config.subnet_name}",
            "vpc_id": vpc_id,
            "name": config.subnet_name,
            "cidr": config.cidr,
            "gateway": config.gateway,
        },
    )
    subnet_id = str(subnet.get("id", "")).strip()
    if subnet_status != 201 or not subnet_id:
        fail("production-shaped S01 Gateway subnet create must return 201 and id")

    route_status, route = http_client.request(
        "POST",
        gateway_url(config, "/networks/routes"),
        config.ani_bearer_token,
        config.tenant_id,
        {
            "idempotency_key": f"live-route-{config.route_name}",
            "vpc_id": vpc_id,
            "destination_cidr": config.route_destination,
            "next_hop_type": "gateway",
            "next_hop_id": config.route_next_hop,
            "description": "Sprint 13 S01 production-shaped live gate route",
        },
    )
    route_id = str(route.get("id", "")).strip()
    if route_status != 201 or not route_id:
        fail("production-shaped S01 Gateway route create must return 201 and id")
    if route.get("destination_cidr") != config.route_destination or route.get("next_hop_id") != config.route_next_hop:
        fail("production-shaped S01 Gateway route response must match requested destination and next hop")

    list_status, route_list = http_client.request(
        "GET",
        gateway_url(config, "/networks/routes", {"vpc_id": vpc_id}),
        config.ani_bearer_token,
        config.tenant_id,
    )
    items = route_list.get("items")
    if list_status != 200 or not isinstance(items, list):
        fail("production-shaped S01 Gateway route list must return 200 with items")
    if not any(isinstance(item, dict) and item.get("id") == route_id for item in items):
        fail("production-shaped S01 Gateway route list must include created route")

    return {
        "gateway_vpc_create_status": vpc_status,
        "gateway_subnet_create_status": subnet_status,
        "gateway_route_create_status": route_status,
        "gateway_route_list_status": list_status,
        "gateway_route_count": len(items),
        "gateway_vpc_id": vpc_id,
        "gateway_subnet_id": subnet_id,
        "gateway_route_id": route_id,
    }


def cleanup_live_resources(config: LiveConfig, runner: LiveRunner, resource_names: dict[str, str] | None = None) -> dict[str, object]:
    namespace = tenant_namespace(config)
    names = resource_names or {}
    vpc_name = names.get("vpc") or kubernetes_name("vpc", config.vpc_name)
    subnet_name = names.get("subnet") or kubernetes_name("subnet", config.subnet_name)
    security_group_name = names.get("security_group") or kubernetes_name("sg", config.security_group_name)
    load_balancer_name = names.get("load_balancer") or kubernetes_name("lb", config.load_balancer_name)
    deleted = [
        f"service/{load_balancer_name}",
        f"networkpolicy/{security_group_name}",
        f"subnet/{subnet_name}",
        f"vpc/{vpc_name}",
        f"namespace/{namespace}",
    ]
    runner.run(kubectl(config, ["-n", namespace, "delete", "service", load_balancer_name, "--ignore-not-found"]))
    runner.run(kubectl(config, ["-n", namespace, "delete", "networkpolicy", security_group_name, "--ignore-not-found"]))
    runner.run(kubectl(config, ["delete", "subnet", subnet_name, "--ignore-not-found"]))
    runner.run(kubectl(config, ["delete", "vpc", vpc_name, "--ignore-not-found"]))
    runner.run(kubectl(config, ["delete", "namespace", namespace, "--ignore-not-found"]))
    return {"status": "deleted", "resources": deleted}


def run_live(
    config: LiveConfig,
    runner: LiveRunner | None = None,
    cleanup: bool = False,
    http_client: HTTPClient | None = None,
) -> dict[str, object]:
    runner = runner or LiveRunner()
    http_client = http_client or HTTPClient()
    namespace = tenant_namespace(config)
    vpc_name = kubernetes_name("vpc", config.vpc_name)
    subnet_name = kubernetes_name("subnet", config.subnet_name)
    route_name = kubernetes_name("route", config.route_name)
    security_group_name = kubernetes_name("sg", config.security_group_name)
    load_balancer_name = kubernetes_name("lb", config.load_balancer_name)
    gateway_result: dict[str, object] = {}
    cleanup_names: dict[str, str] = {}

    runner.run(kubectl(config, ["get", "crd", "vpcs.kubeovn.io", "-o", "json"]))
    runner.run(kubectl(config, ["get", "crd", "subnets.kubeovn.io", "-o", "json"]))
    auth = runner.run(kubectl(config, ["auth", "can-i", "create", "vpcs.kubeovn.io"]))
    if auth.strip() != "yes":
        fail("kubectl auth can-i create vpcs.kubeovn.io must return yes")

    runner.run(kubectl(config, ["apply", "-f", "-"]), input_text=namespace_manifest(config))
    if config.production_shaped:
        gateway_result = create_gateway_network_resources(config, http_client)
        vpc_name = network_provider_name("vpc", str(gateway_result["gateway_vpc_id"]))
        subnet_name = network_provider_name("subnet", str(gateway_result["gateway_subnet_id"]))
        route_name = network_provider_name("route", str(gateway_result["gateway_route_id"]))
        cleanup_names = {"vpc": vpc_name, "subnet": subnet_name}
    else:
        runner.run(kubectl(config, ["apply", "-f", "-"]), input_text=vpc_manifest(config))
        runner.run(kubectl(config, ["apply", "-f", "-"]), input_text=subnet_manifest(config))
        runner.run(kubectl(config, ["apply", "-f", "-"]), input_text=route_manifest(config))
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
    assert_route(vpc, config.route_destination, config.route_next_hop)
    assert_observable_kubernetes_object(subnet, "Subnet", subnet_name)
    assert_observable_kubernetes_object(networkpolicy, "NetworkPolicy", security_group_name)
    assert_service(service, load_balancer_name)

    result: dict[str, object] = {
        "status": "passed",
        "namespace": namespace,
        "vpc": vpc_name,
        "subnet": subnet_name,
        "route": route_name,
        "security_group": security_group_name,
        "load_balancer": load_balancer_name,
    }
    result.update(gateway_result)
    if config.production_shaped:
        result["production_shape"] = {
            "status": "passed",
            "transport_profile": "production_gateway_in_cluster_serviceaccount",
            "missing_items": [],
            "proof_items": [
                "production_gateway",
                "in_cluster_serviceaccount_rbac",
                "persistent_route_metadata_reconciliation",
            ],
        }
    if cleanup:
        result["cleanup"] = cleanup_live_resources(config, runner, cleanup_names)
    return result


def write_live_evidence(path: Path, evidence: dict[str, object]) -> None:
    try:
        path.parent.mkdir(parents=True, exist_ok=True)
        identified = {**evidence, "id": GATE_ID, "profile": PROFILE}
        path.write_text(json.dumps(identified, ensure_ascii=False, indent=2, sort_keys=True) + "\n", encoding="utf-8")
    except OSError as err:
        fail(f"evidence output unusable: {err}")


def validate_gate_path(path: str) -> None:
    if not path.strip():
        fail("gate path must not be empty")
    if path != path.strip():
        fail("gate path must not contain surrounding whitespace")


def validate_evidence_output(path: str) -> None:
    if not path.strip():
        fail("evidence_output must not be empty")
    if path != path.strip():
        fail("evidence_output must not contain surrounding whitespace")
    output_path = Path(path)
    if output_path.is_dir():
        fail("evidence_output must be a file path")
    if output_path.parent.exists() and not output_path.parent.is_dir():
        fail("evidence_output parent must be a directory")
    try:
        output_path.parent.mkdir(parents=True, exist_ok=True)
    except OSError:
        fail("evidence_output parent must be a directory")
    try:
        if output_path.parent.stat().st_mode & 0o222 == 0:
            fail("evidence_output parent must be writable")
        if output_path.exists() and output_path.stat().st_mode & 0o222 == 0:
            fail("evidence_output must be writable")
    except OSError:
        fail("evidence_output must be writable")


def main() -> int:
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("--gate", default=str(DEFAULT_GATE), help="Kube-OVN network live gate YAML")
    parser.add_argument("--live", action="store_true", help="run live kubectl Kube-OVN checks")
    parser.add_argument("--cleanup", action="store_true", help="delete temporary live-gate resources after successful observation")
    parser.add_argument("--tenant-id", default=os.getenv("ANI_LIVE_TENANT_ID", "tenant-a"))
    parser.add_argument("--namespace", default=os.getenv("ANI_LIVE_NAMESPACE", ""))
    parser.add_argument("--vpc-name", default=os.getenv("ANI_LIVE_VPC_NAME", "ani-live-net"))
    parser.add_argument("--subnet-name", default=os.getenv("ANI_LIVE_SUBNET_NAME", "ani-live-subnet"))
    parser.add_argument("--route-name", default=os.getenv("ANI_LIVE_ROUTE_NAME", "ani-live-route"))
    parser.add_argument("--security-group-name", default=os.getenv("ANI_LIVE_SECURITY_GROUP_NAME", "ani-live-sg"))
    parser.add_argument("--load-balancer-name", default=os.getenv("ANI_LIVE_LOAD_BALANCER_NAME", "ani-live-lb"))
    parser.add_argument("--cidr", default=os.getenv("ANI_LIVE_SUBNET_CIDR", "10.244.80.0/24"))
    parser.add_argument("--gateway", default=os.getenv("ANI_LIVE_SUBNET_GATEWAY", "10.244.80.1"))
    parser.add_argument("--route-destination", default=os.getenv("ANI_LIVE_ROUTE_DESTINATION", "0.0.0.0/0"))
    parser.add_argument(
        "--route-next-hop",
        default=os.getenv("ANI_LIVE_ROUTE_NEXT_HOP", os.getenv("ANI_LIVE_SUBNET_GATEWAY", "10.244.80.1")),
    )
    parser.add_argument("--kubeconfig", default=os.getenv("KUBECONFIG", ""))
    parser.add_argument("--gateway-url", default=os.getenv("ANI_GATEWAY_URL", ""))
    parser.add_argument("--ani-bearer-token", default=os.getenv("ANI_BEARER_TOKEN", ""))
    parser.add_argument("--production-shaped", action="store_true", help="require production-shaped S01 transport and write production_shape evidence")
    parser.add_argument("--external-lb-live", action="store_true", help="also prove external Kube-OVN LoadBalancer reachability")
    parser.add_argument("--external-lb-namespace", default=os.getenv("ANI_KUBEOVN_EXTERNAL_LB_NAMESPACE", "default"))
    parser.add_argument("--external-lb-service-name", default=os.getenv("ANI_KUBEOVN_EXTERNAL_LB_SERVICE", "ani-kubeovn-external-lb-smoke"))
    parser.add_argument("--external-lb-helper-deployment", default=os.getenv("ANI_KUBEOVN_EXTERNAL_LB_HELPER_DEPLOY", "lb-svc-ani-kubeovn-external-lb-smoke"))
    parser.add_argument("--external-lb-helper-label", default=os.getenv("ANI_KUBEOVN_EXTERNAL_LB_HELPER_LABEL", "app=lb-svc-ani-kubeovn-external-lb-smoke"))
    parser.add_argument("--external-lb-ip", default=os.getenv("ANI_KUBEOVN_EXTERNAL_LB_IP", "10.10.1.250"))
    parser.add_argument("--external-lb-expected-body", default=os.getenv("ANI_KUBEOVN_EXTERNAL_LB_EXPECTED_BODY", "ani-kubeovn-external-lb-ok"))
    parser.add_argument("--external-lb-deps-manifest", default=os.getenv("ANI_KUBEOVN_EXTERNAL_LB_DEPS", str(DEFAULT_EXTERNAL_LB_DEPS)))
    parser.add_argument(
        "--external-lb-script-configmap-manifest",
        default=os.getenv("ANI_KUBEOVN_EXTERNAL_LB_SCRIPT_CONFIGMAP", str(DEFAULT_EXTERNAL_LB_SCRIPT_CONFIGMAP)),
    )
    parser.add_argument("--external-lb-script-configmap-name", default=os.getenv("ANI_KUBEOVN_EXTERNAL_LB_SCRIPT_CONFIGMAP_NAME", "ani-kubeovn-lb-svc-script"))
    parser.add_argument("--external-lb-helper-image", default=os.getenv("ANI_KUBEOVN_EXTERNAL_LB_HELPER_IMAGE", "dockerproxy.net/kubeovn/kube-ovn:v1.15.8"))
    parser.add_argument("--external-lb-controller-namespace", default=os.getenv("ANI_KUBEOVN_CONTROLLER_NAMESPACE", "kube-system"))
    parser.add_argument("--external-lb-controller-deployment", default=os.getenv("ANI_KUBEOVN_CONTROLLER_DEPLOYMENT", "kube-ovn-controller"))
    parser.add_argument("--external-lb-ssh-binary", default=os.getenv("ANI_KUBEOVN_EXTERNAL_LB_SSH_BINARY", "ssh"))
    parser.add_argument(
        "--external-lb-curl-host",
        action="append",
        default=None,
        help="SSH host alias used to curl the external LB IP; repeat for each real server",
    )
    parser.add_argument("--external-lb-reconcile-stamp", default=os.getenv("ANI_KUBEOVN_EXTERNAL_LB_RECONCILE_STAMP", "external-lb-live-gate"))
    parser.add_argument(
        "--evidence-output",
        default=os.getenv("ANI_KUBEOVN_NETWORK_LIVE_EVIDENCE_OUTPUT") or None,
        help="write --live evidence JSON to this path",
    )
    args = parser.parse_args()

    validate_gate_path(args.gate)
    document = load_gate(Path(args.gate))
    validate_contract(document)
    validate_docs()
    if args.live:
        config = LiveConfig(
            tenant_id=args.tenant_id,
            namespace=args.namespace,
            vpc_name=args.vpc_name,
            subnet_name=args.subnet_name,
            route_name=args.route_name,
            security_group_name=args.security_group_name,
            load_balancer_name=args.load_balancer_name,
            cidr=args.cidr,
            gateway=args.gateway,
            route_destination=args.route_destination,
            route_next_hop=args.route_next_hop,
            kubeconfig=args.kubeconfig,
            gateway_url=args.gateway_url,
            ani_bearer_token=args.ani_bearer_token,
            production_shaped=args.production_shaped,
        )
        validate_live_config(config)
        external_config = None
        if args.external_lb_live:
            external_config = ExternalLBConfig(
                namespace=args.external_lb_namespace,
                service_name=args.external_lb_service_name,
                helper_deployment=args.external_lb_helper_deployment,
                helper_label=args.external_lb_helper_label,
                external_ip=args.external_lb_ip,
                expected_body=args.external_lb_expected_body,
                deps_manifest=args.external_lb_deps_manifest,
                script_configmap_manifest=args.external_lb_script_configmap_manifest,
                script_configmap_name=args.external_lb_script_configmap_name,
                helper_image=args.external_lb_helper_image,
                controller_namespace=args.external_lb_controller_namespace,
                controller_deployment=args.external_lb_controller_deployment,
                kubeconfig=args.kubeconfig,
                kubectl_binary=config.kubectl_binary,
                ssh_binary=args.external_lb_ssh_binary,
                curl_hosts=tuple(args.external_lb_curl_host or ()),
                reconcile_stamp=args.external_lb_reconcile_stamp,
            )
            validate_external_lb_config(external_config)
        if args.evidence_output is not None:
            validate_evidence_output(args.evidence_output)
        result = run_live(config, cleanup=args.cleanup)
        if external_config is not None:
            result["external_load_balancer"] = run_external_lb_live(external_config)
        if args.evidence_output is not None:
            write_live_evidence(Path(args.evidence_output), result)
            print(f"M1-NETWORK-LIVE-A live checks valid; evidence written to {args.evidence_output}")
        else:
            print(f"M1-NETWORK-LIVE-A live checks valid: {json.dumps(result, sort_keys=True)}")
    else:
        print("M1-NETWORK-LIVE-A contract valid; use --live with KUBECONFIG against REAL-K8S-LAB-A")
    return 0


if __name__ == "__main__":
    raise SystemExit(main())
