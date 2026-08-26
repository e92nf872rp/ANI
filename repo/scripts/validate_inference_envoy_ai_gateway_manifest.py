#!/usr/bin/env python3
"""Validate the repository-managed static C40 Envoy AI Gateway manifest."""

from __future__ import annotations

from pathlib import Path
from typing import Any

import yaml

ROOT = Path(__file__).resolve().parents[1]
DEFAULT_MANIFEST = ROOT / "deploy/real-k8s-lab/inference-envoy-ai-gateway-c40.yaml"
NAMESPACE = "ani-aigw"
TENANT_ID = "00000000-0000-0000-0000-000000000001"
INFERENCE_SERVICE_ID = "182df9a4-4a6a-4eed-9d50-51a458a15f6a"
VLLM_NAMESPACE = "ani-tenant-00000000-0000-0000-0000-000000000001"
MODEL_BINDINGS = {
    "chat": {
        "model": "ani-c40-chat",
        "backend": "ani-c40-chat-vllm",
        "service": "pw-182df9a4-4a6a-4eed-9d50-51a458a15f6a",
    },
    "embed": {
        "model": "ani-c40-embed",
        "backend": "ani-c40-embed-vllm",
        "service": "pw-6ae6f951-415d-454f-8459-cd38d32dc58f",
    },
}
REMOVED_HEADERS = ["Authorization", "x-api-key", "x-ani-tenant-id", "x-ani-user-id"]


def fail(message: str) -> None:
    raise SystemExit(f"inference envoy ai gateway manifest invalid: {message}")


def require(condition: bool, message: str) -> None:
    if not condition:
        fail(message)


def load_documents(path: Path) -> list[dict[str, Any]]:
    if not path.exists():
        fail(f"missing {path}")
    try:
        documents = [item for item in yaml.safe_load_all(path.read_text(encoding="utf-8")) if item is not None]
    except yaml.YAMLError as err:
        fail(f"malformed YAML: {err}")
    require(all(isinstance(item, dict) for item in documents), "every YAML document must be an object")
    return documents


def by_kind_name(documents: list[dict[str, Any]], kind: str, name: str) -> dict[str, Any]:
    matches = [
        document
        for document in documents
        if document.get("kind") == kind and isinstance(document.get("metadata"), dict) and document["metadata"].get("name") == name
    ]
    require(len(matches) == 1, f"expected exactly one {kind}/{name}")
    return matches[0]


def has_secret_reference(value: Any) -> bool:
    if isinstance(value, dict):
        return any("secret" in str(key).lower() or has_secret_reference(item) for key, item in value.items())
    if isinstance(value, list):
        return any(has_secret_reference(item) for item in value)
    return isinstance(value, str) and value.lower() == "secret"


def vllm_dns(binding: dict[str, str]) -> str:
    return f"{binding['service']}.{VLLM_NAMESPACE}.svc.cluster.local"


def validate_adapter(documents: list[dict[str, Any]]) -> None:
    account = by_kind_name(documents, "ServiceAccount", "envoy-authz-adapter")
    require(account.get("apiVersion") == "v1", "ServiceAccount must use v1")
    require(account.get("metadata", {}).get("namespace") == NAMESPACE, "ServiceAccount namespace must be ani-aigw")
    require(account.get("automountServiceAccountToken") is False, "ServiceAccount token automount must be false")

    deployment = by_kind_name(documents, "Deployment", "envoy-authz-adapter")
    require(deployment.get("apiVersion") == "apps/v1", "Deployment must use apps/v1")
    spec = deployment.get("spec", {})
    require(spec.get("replicas") == 1, "adapter Deployment must have one replica")
    labels = {"app.kubernetes.io/name": "envoy-authz-adapter"}
    require(spec.get("selector", {}).get("matchLabels") == labels, "adapter Deployment selector must be exact")
    pod_spec = spec.get("template", {}).get("spec", {})
    require(pod_spec.get("serviceAccountName") == "envoy-authz-adapter", "adapter must use its ServiceAccount")
    require(pod_spec.get("automountServiceAccountToken") is False, "adapter Pod token automount must be false")
    pod_security = pod_spec.get("securityContext", {})
    require(pod_security.get("runAsNonRoot") is True, "adapter Pod must run as non-root")
    require(pod_security.get("seccompProfile", {}).get("type") == "RuntimeDefault", "adapter Pod must use RuntimeDefault seccomp")
    containers = pod_spec.get("containers")
    require(isinstance(containers, list) and len(containers) == 1, "adapter Deployment must have one container")
    container = containers[0]
    require(container.get("name") == "envoy-authz-adapter", "adapter container name must be exact")
    require(
        container.get("image") == "docker.changqingyun.cn/ani/envoy-authz-adapter:c40-20260824",
        "adapter image must be the C40 image",
    )
    require(container.get("imagePullPolicy") == "Always", "adapter imagePullPolicy must be Always")
    require(container.get("ports") == [{"name": "grpc", "containerPort": 9002, "protocol": "TCP"}], "adapter gRPC port must be exact")
    require(
        container.get("env")
        == [
            {"name": "AUTH_SERVICE_GRPC_ADDR", "value": "ani-auth-service.ani-system.svc.cluster.local:9101"},
            {"name": "AUTH_TIMEOUT", "value": "2s"},
            {"name": "GRPC_PORT", "value": "9002"},
        ],
        "adapter environment must contain only the three allowed non-secret variables",
    )
    for probe_name in ("readinessProbe", "livenessProbe"):
        require(container.get(probe_name, {}).get("grpc", {}).get("port") == 9002, f"adapter {probe_name} must use gRPC port 9002")
    security = container.get("securityContext", {})
    require(security.get("allowPrivilegeEscalation") is False, "adapter must not allow privilege escalation")
    require(security.get("readOnlyRootFilesystem") is True, "adapter root filesystem must be read-only")
    require(security.get("runAsNonRoot") is True and security.get("runAsUser") == 65532, "adapter must run as UID 65532")
    require(security.get("capabilities", {}).get("drop") == ["ALL"], "adapter must drop all Linux capabilities")

    service = by_kind_name(documents, "Service", "envoy-authz-adapter")
    require(service.get("apiVersion") == "v1", "adapter Service must use v1")
    require(service.get("spec", {}).get("selector") == labels, "adapter Service selector must be exact")
    require(
        service.get("spec", {}).get("ports") == [{"name": "grpc", "port": 9002, "protocol": "TCP", "targetPort": "grpc"}],
        "adapter Service must expose gRPC port 9002",
    )


def validate_network_policy(documents: list[dict[str, Any]]) -> None:
    policy = by_kind_name(documents, "NetworkPolicy", "envoy-authz-adapter")
    require(policy.get("apiVersion") == "networking.k8s.io/v1", "NetworkPolicy must use networking.k8s.io/v1")
    envoy_peer = {
        "namespaceSelector": {"matchLabels": {"kubernetes.io/metadata.name": "envoy-gateway-system"}},
        "podSelector": {
            "matchLabels": {
                "app.kubernetes.io/name": "envoy",
                "gateway.envoyproxy.io/owning-gateway-name": "ani-aigw",
                "gateway.envoyproxy.io/owning-gateway-namespace": "ani-aigw",
            }
        },
    }
    dns_peer = {
        "namespaceSelector": {"matchLabels": {"kubernetes.io/metadata.name": "kube-system"}},
        "podSelector": {"matchLabels": {"k8s-app": "kube-dns"}},
    }
    auth_peer = {
        "namespaceSelector": {"matchLabels": {"kubernetes.io/metadata.name": "ani-system"}},
        "podSelector": {"matchLabels": {"app.kubernetes.io/name": "ani-auth-service"}},
    }
    spec = policy.get("spec", {})
    require(spec.get("podSelector", {}).get("matchLabels") == {"app.kubernetes.io/name": "envoy-authz-adapter"}, "NetworkPolicy selector must be exact")
    require(spec.get("policyTypes") == ["Ingress", "Egress"], "NetworkPolicy must default deny ingress and egress")
    require(spec.get("ingress") == [{"from": [envoy_peer], "ports": [{"protocol": "TCP", "port": 9002}]}], "NetworkPolicy ingress must allow only the owning Envoy proxy")
    require(
        spec.get("egress")
        == [
            {"to": [dns_peer], "ports": [{"protocol": "UDP", "port": 53}]},
            {"to": [dns_peer], "ports": [{"protocol": "TCP", "port": 53}]},
            {"to": [auth_peer], "ports": [{"protocol": "TCP", "port": 9101}]},
        ],
        "NetworkPolicy egress must allow only kube-system DNS and ani-auth-service",
    )


def validate_data_plane(documents: list[dict[str, Any]]) -> None:
    for name, binding in MODEL_BINDINGS.items():
        backend = by_kind_name(documents, "Backend", binding["backend"])
        require(backend.get("apiVersion") == "gateway.envoyproxy.io/v1alpha1", f"{name} Backend must use installed v1alpha1")
        require(
            backend.get("spec")
            == {"type": "Endpoints", "endpoints": [{"fqdn": {"hostname": vllm_dns(binding), "port": 8000}}]},
            f"{name} Backend must use only the retained vLLM ClusterIP DNS and port",
        )
        ai_backend = by_kind_name(documents, "AIServiceBackend", binding["backend"])
        require(ai_backend.get("apiVersion") == "aigateway.envoyproxy.io/v1beta1", f"{name} AIServiceBackend must use installed v1beta1")
        require(
            ai_backend.get("spec")
            == {
                "backendRef": {"group": "gateway.envoyproxy.io", "kind": "Backend", "name": binding["backend"]},
                "schema": {"name": "OpenAI", "version": "v1"},
            },
            f"{name} AIServiceBackend must reference the C40 Backend with the OpenAI v1 schema",
        )
    route = by_kind_name(documents, "AIGatewayRoute", "ani-c40")
    require(route.get("apiVersion") == "aigateway.envoyproxy.io/v1beta1", "AIGatewayRoute must use installed v1beta1")
    require(
        route.get("spec", {}).get("parentRefs") == [{"group": "gateway.networking.k8s.io", "kind": "Gateway", "name": "ani-aigw"}],
        "AIGatewayRoute must target the owning Gateway",
    )
    require(
        route.get("spec", {}).get("rules")
        == [
            {
                "matches": [{"headers": [{"name": "x-ai-eg-model", "type": "Exact", "value": MODEL_BINDINGS["chat"]["model"]}]}],
                "backendRefs": [
                    {
                        "name": MODEL_BINDINGS["chat"]["backend"],
                        "priority": 0,
                        "weight": 1,
                        "headerMutation": {"remove": REMOVED_HEADERS},
                    }
                ],
            },
            {
                "matches": [{"headers": [{"name": "x-ai-eg-model", "type": "Exact", "value": MODEL_BINDINGS["embed"]["model"]}]}],
                "backendRefs": [
                    {
                        "name": MODEL_BINDINGS["embed"]["backend"],
                        "priority": 0,
                        "weight": 1,
                        "headerMutation": {"remove": REMOVED_HEADERS},
                    }
                ],
            }
        ],
        "AIGatewayRoute must bind chat and embedding models and remove all credentials and identity headers before vLLM",
    )


def validate_security_policy(documents: list[dict[str, Any]]) -> None:
    policy = by_kind_name(documents, "SecurityPolicy", "ani-c40-ext-auth")
    require(policy.get("apiVersion") == "gateway.envoyproxy.io/v1alpha1", "SecurityPolicy must use installed v1alpha1")
    spec = policy.get("spec", {})
    require("apiKeyAuth" not in spec, "SecurityPolicy must not use apiKeyAuth")
    require(not has_secret_reference(spec), "SecurityPolicy must not contain a Secret credential reference")
    require(spec.get("targetRefs") == [{"group": "gateway.networking.k8s.io", "kind": "HTTPRoute", "name": "ani-c40"}], "SecurityPolicy must target only generated HTTPRoute/ani-c40")
    ext_auth = spec.get("extAuth", {})
    require(ext_auth.get("failOpen") is False, "extAuth must fail closed")
    require(ext_auth.get("statusOnError") == 503, "extAuth must return 503 on dependency error")
    require(ext_auth.get("headersToExtAuth") == ["authorization"], "extAuth must receive only authorization")
    require(
        ext_auth.get("contextExtensions")
        == [
            {"name": "ani.target_tenant_id", "type": "Value", "value": TENANT_ID},
            {"name": "ani.inference_service_id", "type": "Value", "value": INFERENCE_SERVICE_ID},
        ],
        "extAuth must provide the exact trusted tenant and inference-service context",
    )
    require(ext_auth.get("grpc") == {"backendRefs": [{"name": "envoy-authz-adapter", "port": 9002}]}, "extAuth must use the adapter gRPC Service")


def validate(documents: list[dict[str, Any]]) -> None:
    required = {
        ("ServiceAccount", "envoy-authz-adapter"),
        ("Deployment", "envoy-authz-adapter"),
        ("Service", "envoy-authz-adapter"),
        ("NetworkPolicy", "envoy-authz-adapter"),
        ("Backend", "ani-c40-chat-vllm"),
        ("Backend", "ani-c40-embed-vllm"),
        ("AIServiceBackend", "ani-c40-chat-vllm"),
        ("AIServiceBackend", "ani-c40-embed-vllm"),
        ("AIGatewayRoute", "ani-c40"),
        ("SecurityPolicy", "ani-c40-ext-auth"),
    }
    actual = {(document.get("kind"), document.get("metadata", {}).get("name")) for document in documents}
    require(actual == required and len(documents) == len(required), "manifest must contain exactly the ten C40 resources")
    for document in documents:
        require(document.get("metadata", {}).get("namespace") == NAMESPACE, "every C40 resource must be in ani-aigw")
    validate_adapter(documents)
    validate_network_policy(documents)
    validate_data_plane(documents)
    validate_security_policy(documents)


def main() -> None:
    validate(load_documents(DEFAULT_MANIFEST))
    print("inference envoy ai gateway manifest valid")


if __name__ == "__main__":
    main()
