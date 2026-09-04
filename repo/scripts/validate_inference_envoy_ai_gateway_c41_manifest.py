#!/usr/bin/env python3
"""Validate the fail-closed shared C41 Envoy AI Gateway manifest."""

from __future__ import annotations

import re
from pathlib import Path
from typing import Any

import yaml

ROOT = Path(__file__).resolve().parents[1]
DEFAULT_MANIFEST = ROOT / "deploy/real-k8s-lab/inference-envoy-ai-gateway-c41.yaml"
GATEWAY_NAMESPACE = "ani-aigw"
SYSTEM_NAMESPACE = "ani-system"
ADAPTER_IMAGE = "docker.changqingyun.cn/ani/envoy-authz-adapter:c41-20260831"
PUBLISHER_IMAGE = "docker.changqingyun.cn/ani/inference-gateway-publisher:c41-20260831"

EXPECTED_RESOURCES = {
    ("ServiceAccount", "envoy-authz-adapter", GATEWAY_NAMESPACE),
    ("Deployment", "envoy-authz-adapter", GATEWAY_NAMESPACE),
    ("Service", "envoy-authz-adapter", GATEWAY_NAMESPACE),
    ("NetworkPolicy", "envoy-authz-adapter", GATEWAY_NAMESPACE),
    ("SecurityPolicy", "ani-inference-ext-auth", GATEWAY_NAMESPACE),
    ("ServiceAccount", "inference-gateway-publisher", SYSTEM_NAMESPACE),
    ("Deployment", "inference-gateway-publisher", SYSTEM_NAMESPACE),
    ("Role", "inference-gateway-publisher", GATEWAY_NAMESPACE),
    ("RoleBinding", "inference-gateway-publisher", GATEWAY_NAMESPACE),
    ("NetworkPolicy", "inference-gateway-publisher", SYSTEM_NAMESPACE),
    ("BackendTrafficPolicy", "ani-inference-ratelimit", GATEWAY_NAMESPACE),
}

PLAINTEXT_CREDENTIAL_PATTERNS = (
    re.compile(r"(?i)\bbearer\s+\S+"),
    re.compile(r"\bani_[A-Za-z0-9][A-Za-z0-9_-]*"),
    re.compile(r"(?i)\bak\s*[:=]\s*\S+"),
    re.compile(r"(?i)\b(?:password|passwd|api[_-]?key|access[_-]?token)\s*[:=]\s*\S+"),
    re.compile(r"\beyJ[A-Za-z0-9_-]+\.[A-Za-z0-9_-]+\.[A-Za-z0-9_-]+\b"),
    re.compile(r"(?i)(?:postgres(?:ql)?|redis|nats)://"),
    re.compile(r"(?i)[a-z][a-z0-9+.-]*://[^/\s:@]+:[^/\s@]+@"),
)


def fail(message: str) -> None:
    raise SystemExit(f"inference envoy ai gateway C41 manifest invalid: {message}")


def require(condition: bool, message: str) -> None:
    if not condition:
        fail(message)


def as_dict(value: Any, message: str) -> dict[str, Any]:
    require(isinstance(value, dict), message)
    return value


def as_list(value: Any, message: str) -> list[Any]:
    require(isinstance(value, list), message)
    return value


def require_keys(value: dict[str, Any], keys: set[str], message: str) -> None:
    require(set(value) == keys, message)


def load_documents(path: Path) -> list[dict[str, Any]]:
    require(path.exists(), "manifest file is missing")
    try:
        raw = path.read_text(encoding="utf-8")
    except (OSError, UnicodeError):
        fail("manifest is not readable YAML")
    validate_no_plaintext_manifest_text(raw)
    try:
        loaded = [item for item in yaml.safe_load_all(raw) if item is not None]
    except yaml.YAMLError:
        fail("manifest is not readable YAML")
    require(all(isinstance(item, dict) for item in loaded), "every YAML document must be an object")
    return loaded


def resource_identity(document: dict[str, Any]) -> tuple[Any, Any, Any]:
    metadata = as_dict(document.get("metadata"), "every resource metadata must be an object")
    return document.get("kind"), metadata.get("name"), metadata.get("namespace")


def by_kind_name(documents: list[dict[str, Any]], kind: str, name: str) -> dict[str, Any]:
    matches: list[dict[str, Any]] = []
    for document in documents:
        metadata = document.get("metadata")
        if document.get("kind") == kind and isinstance(metadata, dict) and metadata.get("name") == name:
            matches.append(document)
    require(len(matches) == 1, f"expected exactly one {kind}/{name}")
    return matches[0]


def walk_strings(value: Any):
    if isinstance(value, dict):
        for item in value.values():
            yield from walk_strings(item)
    elif isinstance(value, list):
        for item in value:
            yield from walk_strings(item)
    elif isinstance(value, str):
        yield value


def validate_no_plaintext_credentials(documents: list[dict[str, Any]]) -> None:
    for value in walk_strings(documents):
        if any(pattern.search(value) for pattern in PLAINTEXT_CREDENTIAL_PATTERNS):
            fail("plaintext credential material is forbidden")


def validate_no_plaintext_manifest_text(raw: str) -> None:
    require(isinstance(raw, str), "raw manifest must be text")
    if any(pattern.search(raw) for pattern in PLAINTEXT_CREDENTIAL_PATTERNS):
        fail("plaintext credential material is forbidden")


def require_resource_header(
    document: dict[str, Any], api_version: str, kind: str, name: str, namespace: str, body_key: str
) -> dict[str, Any]:
    require_keys(document, {"apiVersion", "kind", "metadata", body_key}, f"{kind}/{name} top-level shape must be exact")
    require(document.get("apiVersion") == api_version, f"{kind}/{name} apiVersion must be exact")
    require(document.get("kind") == kind, f"{kind}/{name} kind must be exact")
    metadata = as_dict(document.get("metadata"), f"{kind}/{name} metadata must be an object")
    require(metadata == {"name": name, "namespace": namespace}, f"{kind}/{name} metadata must be exact")
    return as_dict(document.get(body_key), f"{kind}/{name} {body_key} must be an object")


def validate_service_account(
    documents: list[dict[str, Any]], name: str, namespace: str, automount: bool
) -> None:
    account = by_kind_name(documents, "ServiceAccount", name)
    require_keys(
        account,
        {"apiVersion", "kind", "metadata", "automountServiceAccountToken"},
        f"ServiceAccount/{name} shape must be exact",
    )
    require(account.get("apiVersion") == "v1" and account.get("kind") == "ServiceAccount", f"ServiceAccount/{name} header must be exact")
    require(account.get("metadata") == {"name": name, "namespace": namespace}, f"ServiceAccount/{name} metadata must be exact")
    require(account.get("automountServiceAccountToken") is automount, f"ServiceAccount/{name} token automount must be exact")


def expected_pod_security() -> dict[str, Any]:
    return {
        "runAsNonRoot": True,
        "runAsUser": 65532,
        "runAsGroup": 65532,
        "seccompProfile": {"type": "RuntimeDefault"},
    }


def expected_container_security() -> dict[str, Any]:
    return {
        "allowPrivilegeEscalation": False,
        "capabilities": {"drop": ["ALL"]},
        "readOnlyRootFilesystem": True,
        "runAsNonRoot": True,
        "runAsUser": 65532,
        "runAsGroup": 65532,
    }


def deployment_container(documents: list[dict[str, Any]], name: str, namespace: str, automount: bool) -> dict[str, Any]:
    deployment = by_kind_name(documents, "Deployment", name)
    spec = require_resource_header(deployment, "apps/v1", "Deployment", name, namespace, "spec")
    require_keys(spec, {"replicas", "selector", "template"}, f"Deployment/{name} spec shape must be exact")
    labels = {"app.kubernetes.io/name": name}
    require(spec.get("replicas") == 1, f"Deployment/{name} must have one replica")
    require(spec.get("selector") == {"matchLabels": labels}, f"Deployment/{name} selector must be exact")
    template = as_dict(spec.get("template"), f"Deployment/{name} template must be an object")
    require_keys(template, {"metadata", "spec"}, f"Deployment/{name} template shape must be exact")
    require(template.get("metadata") == {"labels": labels}, f"Deployment/{name} Pod labels must be exact")
    pod_spec = as_dict(template.get("spec"), f"Deployment/{name} Pod spec must be an object")
    require_keys(
        pod_spec,
        {"serviceAccountName", "automountServiceAccountToken", "securityContext", "containers"},
        f"Deployment/{name} Pod spec shape must be exact",
    )
    require(pod_spec.get("serviceAccountName") == name, f"Deployment/{name} ServiceAccount must be exact")
    require(pod_spec.get("automountServiceAccountToken") is automount, f"Deployment/{name} Pod token automount must be exact")
    require(pod_spec.get("securityContext") == expected_pod_security(), f"Deployment/{name} Pod security must be exact")
    containers = as_list(pod_spec.get("containers"), f"Deployment/{name} containers must be a list")
    require(len(containers) == 1 and isinstance(containers[0], dict), f"Deployment/{name} must have exactly one container")
    return containers[0]


def validate_adapter(documents: list[dict[str, Any]]) -> None:
    validate_service_account(documents, "envoy-authz-adapter", GATEWAY_NAMESPACE, False)
    container = deployment_container(documents, "envoy-authz-adapter", GATEWAY_NAMESPACE, False)
    require_keys(
        container,
        {"name", "image", "imagePullPolicy", "ports", "env", "readinessProbe", "livenessProbe", "securityContext"},
        "adapter container shape must be exact",
    )
    require(container.get("name") == "envoy-authz-adapter", "adapter container name must be exact")
    require(container.get("image") == ADAPTER_IMAGE, "adapter image must be the C41 image")
    require(container.get("imagePullPolicy") == "Always", "adapter image pull policy must be Always")
    require(container.get("ports") == [{"name": "grpc", "containerPort": 9002, "protocol": "TCP"}], "adapter port must be exact")
    require(
        container.get("env")
        == [
            {"name": "AUTH_SERVICE_GRPC_ADDR", "value": "ani-auth-service.ani-system.svc.cluster.local:9101"},
            {"name": "INFERENCE_SERVICE_GRPC_ADDR", "value": "inference-service.ani-system.svc.cluster.local:9104"},
            {"name": "AUTH_TIMEOUT", "value": "2s"},
            {"name": "INFERENCE_TIMEOUT", "value": "2s"},
            {"name": "GRPC_PORT", "value": "9002"},
        ],
        "adapter environment must be the five exact non-secret settings",
    )
    require(container.get("readinessProbe") == {"grpc": {"port": 9002}, "initialDelaySeconds": 2}, "adapter readiness probe must be exact")
    require(container.get("livenessProbe") == {"grpc": {"port": 9002}, "initialDelaySeconds": 5}, "adapter liveness probe must be exact")
    require(container.get("securityContext") == expected_container_security(), "adapter container security must be exact")

    service = by_kind_name(documents, "Service", "envoy-authz-adapter")
    service_spec = require_resource_header(service, "v1", "Service", "envoy-authz-adapter", GATEWAY_NAMESPACE, "spec")
    require(
        service_spec
        == {
            "selector": {"app.kubernetes.io/name": "envoy-authz-adapter"},
            "ports": [{"name": "grpc", "port": 9002, "protocol": "TCP", "targetPort": "grpc"}],
        },
        "adapter Service must expose only gRPC 9002",
    )


def dns_peer() -> dict[str, Any]:
    return {
        "namespaceSelector": {"matchLabels": {"kubernetes.io/metadata.name": "kube-system"}},
        "podSelector": {"matchLabels": {"k8s-app": "kube-dns"}},
    }


def validate_adapter_network_policy(documents: list[dict[str, Any]]) -> None:
    policy = by_kind_name(documents, "NetworkPolicy", "envoy-authz-adapter")
    spec = require_resource_header(
        policy, "networking.k8s.io/v1", "NetworkPolicy", "envoy-authz-adapter", GATEWAY_NAMESPACE, "spec"
    )
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
    auth_peer = {
        "namespaceSelector": {"matchLabels": {"kubernetes.io/metadata.name": SYSTEM_NAMESPACE}},
        "podSelector": {"matchLabels": {"app.kubernetes.io/name": "ani-auth-service"}},
    }
    inference_peer = {
        "namespaceSelector": {"matchLabels": {"kubernetes.io/metadata.name": SYSTEM_NAMESPACE}},
        "podSelector": {"matchLabels": {"app.kubernetes.io/name": "inference-service"}},
    }
    expected = {
        "podSelector": {"matchLabels": {"app.kubernetes.io/name": "envoy-authz-adapter"}},
        "policyTypes": ["Ingress", "Egress"],
        "ingress": [{"from": [envoy_peer], "ports": [{"protocol": "TCP", "port": 9002}]}],
        "egress": [
            {"to": [dns_peer()], "ports": [{"protocol": "UDP", "port": 53}]},
            {"to": [dns_peer()], "ports": [{"protocol": "TCP", "port": 53}]},
            {"to": [auth_peer], "ports": [{"protocol": "TCP", "port": 9101}]},
            {"to": [inference_peer], "ports": [{"protocol": "TCP", "port": 9104}]},
        ],
    }
    require(spec == expected, "adapter NetworkPolicy must allow only owning Envoy, DNS, auth and inference gRPC")


def validate_security_policy(documents: list[dict[str, Any]]) -> None:
    policy = by_kind_name(documents, "SecurityPolicy", "ani-inference-ext-auth")
    spec = require_resource_header(
        policy, "gateway.envoyproxy.io/v1alpha1", "SecurityPolicy", "ani-inference-ext-auth", GATEWAY_NAMESPACE, "spec"
    )
    expected = {
        "targetRefs": [{"group": "gateway.networking.k8s.io", "kind": "Gateway", "name": "ani-aigw"}],
        "extAuth": {
            "failOpen": False,
            "statusOnError": 503,
            "recomputeRoute": True,
            "headersToExtAuth": ["authorization", "x-ai-eg-model", "accept"],
            "grpc": {"backendRefs": [{"name": "envoy-authz-adapter", "port": 9002}]},
        },
    }
    require(spec == expected, "SecurityPolicy must be the exact Gateway-level fail-closed ext_auth policy")


def validate_publisher(documents: list[dict[str, Any]]) -> None:
    validate_service_account(documents, "inference-gateway-publisher", SYSTEM_NAMESPACE, True)
    container = deployment_container(documents, "inference-gateway-publisher", SYSTEM_NAMESPACE, True)
    require_keys(
        container,
        {"name", "image", "imagePullPolicy", "ports", "env", "readinessProbe", "livenessProbe", "securityContext"},
        "publisher container shape must be exact",
    )
    require(container.get("name") == "inference-gateway-publisher", "publisher container name must be exact")
    require(container.get("image") == PUBLISHER_IMAGE, "publisher image must be the C41 image")
    require(container.get("imagePullPolicy") == "Always", "publisher image pull policy must be Always")
    require(container.get("ports") == [{"name": "health", "containerPort": 9206, "protocol": "TCP"}], "publisher health port must be exact")
    require(
        container.get("env")
        == [
            {
                "name": "DATABASE_URL",
                "valueFrom": {"secretKeyRef": {"name": "ani-services-runtime", "key": "database_url"}},
            },
            {"name": "INFERENCE_AI_GATEWAY_PUBLIC_BASE_URL", "value": "https://ai.example.com"},
            {"name": "INFERENCE_AI_GATEWAY_NAMESPACE", "value": "ani-aigw"},
            {"name": "INFERENCE_AI_GATEWAY_NAME", "value": "ani-aigw"},
        ],
        "publisher environment must use the exact database reference and direct public URL",
    )
    require(
        container.get("readinessProbe") == {"httpGet": {"path": "/readyz", "port": "health"}, "initialDelaySeconds": 2},
        "publisher readiness probe must use 9206 /readyz",
    )
    require(
        container.get("livenessProbe") == {"httpGet": {"path": "/healthz", "port": "health"}, "initialDelaySeconds": 5},
        "publisher liveness probe must use 9206 /healthz",
    )
    require(container.get("securityContext") == expected_container_security(), "publisher container security must be exact")


def validate_publisher_rbac(documents: list[dict[str, Any]]) -> None:
    role = by_kind_name(documents, "Role", "inference-gateway-publisher")
    require_keys(role, {"apiVersion", "kind", "metadata", "rules"}, "publisher Role shape must be exact")
    require(role.get("apiVersion") == "rbac.authorization.k8s.io/v1", "publisher Role apiVersion must be exact")
    require(role.get("kind") == "Role", "publisher RBAC must not be cluster-scoped")
    require(
        role.get("metadata") == {"name": "inference-gateway-publisher", "namespace": GATEWAY_NAMESPACE},
        "publisher Role metadata must be exact",
    )
    rules = as_list(role.get("rules"), "publisher Role rules must be a list")
    expected_verbs = ["get", "list", "watch", "create", "patch", "delete"]
    require(
        rules
        == [
            {"apiGroups": ["gateway.envoyproxy.io"], "resources": ["backends"], "verbs": expected_verbs},
            {
                "apiGroups": ["aigateway.envoyproxy.io"],
                "resources": ["aiservicebackends", "aigatewayroutes"],
                "verbs": expected_verbs,
            },
            {"apiGroups": ["gateway.networking.k8s.io"], "resources": ["gateways"], "verbs": ["get"]},
        ],
        "publisher Role permissions must be namespace-scoped and exact",
    )

    binding = by_kind_name(documents, "RoleBinding", "inference-gateway-publisher")
    require_keys(binding, {"apiVersion", "kind", "metadata", "roleRef", "subjects"}, "publisher RoleBinding shape must be exact")
    require(binding.get("apiVersion") == "rbac.authorization.k8s.io/v1", "publisher RoleBinding apiVersion must be exact")
    require(binding.get("kind") == "RoleBinding", "publisher binding must not be cluster-scoped")
    require(
        binding.get("metadata") == {"name": "inference-gateway-publisher", "namespace": GATEWAY_NAMESPACE},
        "publisher RoleBinding metadata must be exact",
    )
    require(
        binding.get("roleRef")
        == {"apiGroup": "rbac.authorization.k8s.io", "kind": "Role", "name": "inference-gateway-publisher"},
        "publisher RoleBinding roleRef must be exact",
    )
    require(
        binding.get("subjects")
        == [{"kind": "ServiceAccount", "name": "inference-gateway-publisher", "namespace": SYSTEM_NAMESPACE}],
        "publisher RoleBinding subject must be the ani-system ServiceAccount",
    )


def validate_publisher_network_policy(documents: list[dict[str, Any]]) -> None:
    policy = by_kind_name(documents, "NetworkPolicy", "inference-gateway-publisher")
    spec = require_resource_header(
        policy,
        "networking.k8s.io/v1",
        "NetworkPolicy",
        "inference-gateway-publisher",
        SYSTEM_NAMESPACE,
        "spec",
    )
    expected = {
        "podSelector": {"matchLabels": {"app.kubernetes.io/name": "inference-gateway-publisher"}},
        "policyTypes": ["Egress"],
        "egress": [
            {"to": [dns_peer()], "ports": [{"protocol": "UDP", "port": 53}]},
            {"to": [dns_peer()], "ports": [{"protocol": "TCP", "port": 53}]},
            {
                "to": [{"podSelector": {"matchLabels": {"app": "ani-reconcile-ha-postgres"}}}],
                "ports": [{"protocol": "TCP", "port": 5432}],
            },
            {"ports": [{"protocol": "TCP", "port": 443}]},
        ],
    }
    require(spec == expected, "publisher NetworkPolicy must allow only DNS, selected PostgreSQL and Kubernetes API 443")


def validate_rate_limit_policy(documents: list[dict[str, Any]]) -> None:
    policy = by_kind_name(documents, "BackendTrafficPolicy", "ani-inference-ratelimit")
    spec = require_resource_header(
        policy,
        "gateway.envoyproxy.io/v1alpha1",
        "BackendTrafficPolicy",
        "ani-inference-ratelimit",
        GATEWAY_NAMESPACE,
        "spec",
    )
    require(
        spec
        == {
            "targetRefs": [{"group": "gateway.networking.k8s.io", "kind": "Gateway", "name": "ani-aigw"}],
            "rateLimit": {
                "global": {"rules": [{"limit": {"requests": 600, "unit": "Minute"}, "shared": True}]}
            },
        },
        "BackendTrafficPolicy must be the shared Gateway-level 600 requests/minute limit",
    )


def validate(documents: list[dict[str, Any]]) -> None:
    require(isinstance(documents, list), "documents must be a list")
    require(all(isinstance(document, dict) for document in documents), "every YAML document must be an object")
    validate_no_plaintext_credentials(documents)
    identities = {resource_identity(document) for document in documents}
    require(
        len(documents) == len(EXPECTED_RESOURCES) and identities == EXPECTED_RESOURCES,
        "manifest must contain exactly the eleven shared C41 resources and no per-service resources",
    )
    validate_adapter(documents)
    validate_adapter_network_policy(documents)
    validate_security_policy(documents)
    validate_publisher(documents)
    validate_publisher_rbac(documents)
    validate_publisher_network_policy(documents)
    validate_rate_limit_policy(documents)


def main() -> None:
    validate(load_documents(DEFAULT_MANIFEST))
    print("inference envoy ai gateway C41 manifest valid")


if __name__ == "__main__":
    main()
