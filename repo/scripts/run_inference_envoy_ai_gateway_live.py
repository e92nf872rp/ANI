#!/usr/bin/env python3
"""Run the human-approved C40 Envoy AI Gateway live gate.

This file has no import-time side effects. Running it changes only the C40
adapter Deployment and creates/revokes short-lived ANI API keys; it never
changes auth-service, Kubernetes Secrets, or the retained vLLM workload.
"""

from __future__ import annotations

import base64
import datetime as dt
import json
import os
import re
import subprocess
import tempfile
import time
import urllib.error
import urllib.parse
import urllib.request
import uuid
from dataclasses import dataclass
from pathlib import Path
from typing import Any

from validate_inference_envoy_ai_gateway_live_gate import PROFILE, REQUIRED_CHECKS, validate_evidence

ROOT = Path(__file__).resolve().parents[1]
MANIFEST = ROOT / "deploy/real-k8s-lab/inference-envoy-ai-gateway-c40.yaml"
EVIDENCE = ROOT / "development-records/live-evidence/inference-envoy-ai-gateway-live-20260825.json"
ADAPTER_NAMESPACE = "ani-aigw"
ADAPTER_DEPLOYMENT = "envoy-authz-adapter"
AUTH_SERVICE_ADDR = "ani-auth-service.ani-system.svc.cluster.local:9101"
VLLM_NAMESPACE = "ani-tenant-00000000-0000-0000-0000-000000000001"
CHAT_VLLM_SERVICE = "pw-182df9a4-4a6a-4eed-9d50-51a458a15f6a"
EMBED_VLLM_SERVICE = "pw-6ae6f951-415d-454f-8459-cd38d32dc58f"
VLLM_SERVICES = {
    "chat": CHAT_VLLM_SERVICE,
    "embed": EMBED_VLLM_SERVICE,
}
CHAT_MODEL_ID = "ani-c40-chat"
EMBED_MODEL_ID = "ani-c40-embed"
PUBLIC_MODEL_ID = CHAT_MODEL_ID
MODEL_BACKENDS = {
    CHAT_MODEL_ID: "ani-c40-chat-vllm",
    EMBED_MODEL_ID: "ani-c40-embed-vllm",
}
ENVOY_GATEWAY_CONTROLLER = "gateway.envoyproxy.io/gatewayclass-controller"
SENSITIVE_RE = re.compile(r"(?:ani_(?:dev|prod)_[^\s\"']+|bearer\s+[^\s\"']+|password\s*=\s*[^\s\"']+)", re.IGNORECASE)
CONNECTION_STRING_RE = re.compile(r"\b(?:postgres(?:ql)?|redis|nats|mysql|mongodb)://\S+", re.IGNORECASE)


def fail(message: str) -> None:
    raise SystemExit(f"inference envoy ai gateway live gate failed: {message}")


def lifecycle_plan() -> list[str]:
    """Return the fixed live sequence without performing any side effects."""
    return [
        "apply-and-wait",
        "owner-nonstream",
        "owner-embedding",
        "owner-sse",
        "missing-and-malformed",
        "credential-location-boundary",
        "public-path-boundary",
        "workload-probes-ready",
        "expired-key",
        "revoked-key",
        "rpm-limit",
        "foreign-tenant",
        "adapter-fail-closed",
        "auth-service-unreachable",
        "credential-forwarding",
        "control-plane-regressions",
        "redaction-scan",
    ]


def redact_text(value: str) -> str:
    value = SENSITIVE_RE.sub("[redacted]", value)
    return CONNECTION_STRING_RE.sub("[redacted-connection-string]", value)


def assert_redacted_evidence(document: dict[str, Any]) -> None:
    raw = json.dumps(document, ensure_ascii=True, sort_keys=True)
    scrubbed = raw.replace('"authorization_not_forwarded"', "").replace(
        '"authorization-not-forwarded"', ""
    )
    lowered = scrubbed.lower()
    if "authorization" in lowered or "bearer " in lowered:
        fail("evidence would contain an Authorization header or Bearer value")
    if "ani_dev_" in lowered or "ani_prod_" in lowered:
        fail("evidence would contain API key material")
    if "password" in lowered or CONNECTION_STRING_RE.search(raw):
        fail("evidence would contain password or connection string material")
    if re.search(
        r'(?:\\?"kind\\?"\s*:\s*\\?"secret\\?"|\\?"data\\?"\s*:\s*\\?\{)', raw, re.IGNORECASE
    ):
        fail("evidence would contain Kubernetes Secret data")


def required_env(name: str) -> str:
    value = os.environ.get(name, "").strip()
    if not value:
        fail(f"{name} is required")
    return value


def validate_control_plane_url(value: str) -> str:
    parsed = urllib.parse.urlsplit(value)
    if parsed.scheme not in {"http", "https"} or not parsed.netloc or parsed.query or parsed.fragment:
        fail("ANI_C40_CONTROL_PLANE_URL must be an absolute API base URL")
    path = parsed.path.rstrip("/")
    if not path.endswith("/api/v1"):
        fail("ANI_C40_CONTROL_PLANE_URL must include /api/v1")
    return urllib.parse.urlunsplit((parsed.scheme, parsed.netloc, path, "", ""))


def control_request(method: str, path: str, access_token: str, body: dict | None = None) -> tuple[int, dict]:
    """Call the approved control-plane API without logging a bearer token."""
    base = validate_control_plane_url(required_env("ANI_C40_CONTROL_PLANE_URL"))
    payload = json.dumps(body).encode("utf-8") if body is not None else None
    headers = {"Accept": "application/json", "Authorization": "Bearer " + access_token}
    if payload is not None:
        headers["Content-Type"] = "application/json"
    request = urllib.request.Request(base + path, data=payload, headers=headers, method=method)
    try:
        with urllib.request.urlopen(request, timeout=30) as response:
            raw = response.read().decode("utf-8")
            try:
                return response.status, json.loads(raw) if raw.strip() else {}
            except json.JSONDecodeError:
                fail("control-plane response was not valid JSON")
                return 0, {}
    except urllib.error.HTTPError as err:
        return err.code, {}
    except urllib.error.URLError:
        fail("control-plane request could not be completed")
        return 0, {}


def create_api_key(
    access_token: str, *, name: str, scopes: list[str], rpm: int, expires_at: str | None = None
) -> tuple[str, str]:
    body: dict[str, Any] = {"name": name, "scopes": scopes, "rate_limit_rpm": rpm}
    if expires_at is not None:
        body["expires_at"] = expires_at
    status, result = control_request("POST", "/auth/api-keys", access_token, body)
    if status != 201:
        fail(f"create temporary API key returned HTTP {status}")
    key_id = result.get("key_id")
    key_value = result.get("key_value")
    if not isinstance(key_id, str) or not key_id or not isinstance(key_value, str) or not key_value:
        fail("create temporary API key returned no key id/value")
    return key_id, key_value


def create_registered_api_key(
    access_token: str,
    cleanup: list[tuple[str, str]],
    *,
    name: str,
    scopes: list[str],
    rpm: int,
    expires_at: str | None = None,
) -> tuple[str, str]:
    """Create a key and register its id before validating its secret value."""
    body: dict[str, Any] = {"name": name, "scopes": scopes, "rate_limit_rpm": rpm}
    if expires_at is not None:
        body["expires_at"] = expires_at
    status, result = control_request("POST", "/auth/api-keys", access_token, body)
    if status != 201:
        fail(f"create temporary API key returned HTTP {status}")
    key_id = result.get("key_id")
    if not isinstance(key_id, str) or not key_id:
        fail("create temporary API key returned no key id")
    cleanup.append((access_token, key_id))
    key_value = result.get("key_value")
    if isinstance(key_value, str) and key_value:
        return key_id, key_value
    try:
        revoke_api_key(access_token, key_id)
        cleanup.remove((access_token, key_id))
    except SystemExit:
        pass
    fail("create temporary API key returned no key value")
    return "", ""


def revoke_api_key(access_token: str, key_id: str) -> None:
    status, _ = control_request("DELETE", f"/auth/api-keys/{key_id}", access_token)
    if status not in {200, 204, 404}:
        fail(f"revoke temporary API key returned HTTP {status}")


def gateway_request(
    path: str,
    *,
    method: str = "GET",
    body: dict[str, Any] | None = None,
    headers: dict[str, str] | None = None,
    query: dict[str, str] | None = None,
    timeout: int = 90,
) -> tuple[int, object]:
    base = required_env("ANI_C40_GATEWAY_URL").rstrip("/")
    encoded_query = urllib.parse.urlencode(query or {})
    url = base + path + (("?" + encoded_query) if encoded_query else "")
    payload = json.dumps(body).encode("utf-8") if body is not None else None
    request_headers = dict(headers or {})
    if payload is not None:
        request_headers.setdefault("Content-Type", "application/json")
    request = urllib.request.Request(url, data=payload, headers=request_headers, method=method)
    try:
        with urllib.request.urlopen(request, timeout=timeout) as response:
            raw = response.read(1_000_000).decode("utf-8", errors="replace")
            return response.status, raw
    except urllib.error.HTTPError as err:
        err.read()
        return err.code, {}
    except urllib.error.URLError:
        fail("data-plane request could not be completed")
        return 0, {}


def chat(api_key: str | None, *, stream: bool, marker: str) -> tuple[int, object]:
    """Call the data plane; error bodies are deliberately discarded."""
    body = {
        "model": PUBLIC_MODEL_ID,
        "messages": [{"role": "user", "content": marker}],
        "stream": stream,
        "max_tokens": 8,
        "temperature": 0,
    }
    headers = {"Accept": "text/event-stream" if stream else "application/json"}
    if api_key is not None:
        headers["Authorization"] = "Bearer " + api_key
    status, raw = gateway_request(
        "/v1/chat/completions",
        method="POST",
        body=body,
        headers=headers,
    )
    if stream or not isinstance(raw, str):
        return status, raw
    try:
        return status, json.loads(raw)
    except json.JSONDecodeError:
        fail("data-plane response was not valid JSON")
        return 0, {}


def embeddings(api_key: str | None, *, marker: str) -> tuple[int, object]:
    """Call the embedding data plane; error bodies are deliberately discarded."""
    body = {"model": EMBED_MODEL_ID, "input": marker}
    headers = {"Accept": "application/json"}
    if api_key is not None:
        headers["Authorization"] = "Bearer " + api_key
    status, raw = gateway_request(
        "/v1/embeddings",
        method="POST",
        body=body,
        headers=headers,
    )
    if not isinstance(raw, str):
        return status, raw
    try:
        return status, json.loads(raw)
    except json.JSONDecodeError:
        fail("embedding data-plane response was not valid JSON")
        return 0, {}


def kubectl(args: list[str], timeout: int = 120) -> str:
    completed = subprocess.run(["kubectl", *args], text=True, capture_output=True, check=False, timeout=timeout)
    if completed.returncode != 0:
        fail("kubectl command failed")
    return completed.stdout


def kubectl_json(args: list[str], timeout: int = 120) -> dict[str, Any]:
    data = json.loads(kubectl([*args, "-o", "json"], timeout=timeout))
    if not isinstance(data, dict):
        fail("kubectl JSON response must be an object")
    return data


@dataclass(frozen=True)
class AdapterState:
    replicas: int
    auth_service_grpc_addr: str | None


def adapter_state_from_deployment(deployment: dict[str, Any]) -> AdapterState:
    spec = deployment.get("spec") or {}
    replicas = spec.get("replicas")
    if not isinstance(replicas, int) or replicas < 0:
        fail("adapter deployment has no valid replica count")
    containers = (((spec.get("template") or {}).get("spec") or {}).get("containers") or [])
    for container in containers:
        if not isinstance(container, dict) or container.get("name") != ADAPTER_DEPLOYMENT:
            continue
        for item in container.get("env") or []:
            if isinstance(item, dict) and item.get("name") == "AUTH_SERVICE_GRPC_ADDR":
                value = item.get("value")
                if not isinstance(value, str):
                    fail("adapter AUTH_SERVICE_GRPC_ADDR must be a literal value when present")
                return AdapterState(replicas, value)
        return AdapterState(replicas, None)
    fail("adapter deployment has no envoy-authz-adapter container")
    return AdapterState(0, None)


def snapshot_adapter_state() -> AdapterState:
    return adapter_state_from_deployment(
        kubectl_json(["-n", ADAPTER_NAMESPACE, "get", "deployment", ADAPTER_DEPLOYMENT])
    )


def apply_and_snapshot_adapter() -> AdapterState:
    """Establish and capture the manifest-owned adapter baseline for rollback."""
    kubectl(["apply", "-f", str(MANIFEST)])
    desired = snapshot_adapter_state()
    failures = wait_adapter_restored(desired)
    if failures:
        fail("manifest-owned adapter baseline did not reach the current generation")
    return snapshot_adapter_state()


def ready_endpoint_count(slices: dict[str, Any]) -> int:
    count = 0
    for item in slices.get("items", []):
        if not isinstance(item, dict):
            continue
        for endpoint in item.get("endpoints") or []:
            if isinstance(endpoint, dict) and ((endpoint.get("conditions") or {}).get("ready") is True):
                count += 1
    return count


def legacy_endpoint_count(endpoints: dict[str, Any]) -> int:
    return sum(
        len(subset.get("addresses") or [])
        for subset in endpoints.get("subsets") or []
        if isinstance(subset, dict)
    )


def wait_adapter_endpoints_zero(timeout: int = 120) -> None:
    deadline = time.monotonic() + timeout
    while time.monotonic() < deadline:
        slices = kubectl_json(
            [
                "-n", ADAPTER_NAMESPACE,
                "get", "endpointslices",
                "-l", f"kubernetes.io/service-name={ADAPTER_DEPLOYMENT}",
            ]
        )
        endpoints = kubectl_json(["-n", ADAPTER_NAMESPACE, "get", "endpoints", ADAPTER_DEPLOYMENT])
        if ready_endpoint_count(slices) == 0 and legacy_endpoint_count(endpoints) == 0:
            return
        time.sleep(1)
    fail("adapter Service still has ready EndpointSlice endpoints after bounded wait")


def wait_adapter_restored(state: AdapterState, timeout: int = 120) -> list[str]:
    deadline = time.monotonic() + timeout
    while time.monotonic() < deadline:
        deployment = kubectl_json(["-n", ADAPTER_NAMESPACE, "get", "deployment", ADAPTER_DEPLOYMENT])
        metadata = deployment.get("metadata") or {}
        status = deployment.get("status") or {}
        generation = metadata.get("generation")
        observed = status.get("observedGeneration")
        if isinstance(generation, int) and isinstance(observed, int) and observed >= generation:
            if state.replicas == 0:
                wait_adapter_endpoints_zero(timeout=max(1, int(deadline - time.monotonic())))
                return []
            if int(status.get("availableReplicas") or 0) >= state.replicas:
                return []
        time.sleep(1)
    return ["readiness"]


def restore_adapter_state(state: AdapterState) -> list[str]:
    """Attempt exact env and replica restoration independently; never leak errors."""
    failures: list[str] = []
    env_arg = (
        f"AUTH_SERVICE_GRPC_ADDR={state.auth_service_grpc_addr}"
        if state.auth_service_grpc_addr is not None
        else "AUTH_SERVICE_GRPC_ADDR-"
    )
    try:
        kubectl(["-n", ADAPTER_NAMESPACE, "set", "env", f"deployment/{ADAPTER_DEPLOYMENT}", env_arg])
    except SystemExit:
        failures.append("env")
    try:
        kubectl(["-n", ADAPTER_NAMESPACE, "scale", f"deployment/{ADAPTER_DEPLOYMENT}", f"--replicas={state.replicas}"])
    except SystemExit:
        failures.append("replicas")
    failures.extend(wait_adapter_restored(state))
    return failures


def wait_condition(kind: str, name: str, condition: str, namespace: str, timeout: int = 120) -> None:
    kubectl(
        ["-n", namespace, "wait", f"--for=condition={condition}", f"{kind}/{name}", f"--timeout={timeout}s"],
        timeout=timeout + 15,
    )


def require_status(status: int, wanted: int, check_id: str) -> None:
    if status != wanted:
        fail(f"{check_id} returned HTTP {status}, want {wanted}")


def temporary_name(label: str) -> str:
    return f"c40-{label}-{uuid.uuid4().hex[:10]}"


def verify_credential_location_boundary(owner_key: str) -> None:
    body = {
        "model": PUBLIC_MODEL_ID,
        "messages": [{"role": "user", "content": temporary_name("credential-boundary")}],
        "stream": False,
        "max_tokens": 8,
    }
    for headers in (
        {"Content-Type": "application/json", "x-api-key": owner_key},
        {"Content-Type": "application/json", "Cookie": "api_key=" + owner_key},
    ):
        status, _ = gateway_request(
            "/v1/chat/completions",
            method="POST",
            body=body,
            headers=headers,
        )
        require_status(status, 401, "alternate credential location")
    status, _ = gateway_request(
        "/v1/chat/completions",
        method="POST",
        body=body,
        headers={"Content-Type": "application/json"},
        query={"api_key": "not-a-credential"},
    )
    require_status(status, 401, "query credential location")


def verify_public_path_boundary() -> list[str]:
    markers: list[str] = []
    for path in ("/healthz", "/readyz", "/v1/not-registered"):
        marker = temporary_name("unregistered")
        markers.append(marker)
        status, _ = gateway_request(path, method="POST", body={"marker": marker})
        require_status(status, 404, "public unregistered path")
    return markers


def has_container_probes(deployment: dict[str, Any], required_names: set[str]) -> bool:
    containers = (((deployment.get("spec") or {}).get("template") or {}).get("spec") or {}).get("containers") or []
    found = {
        item.get("name")
        for item in containers
        if isinstance(item, dict) and item.get("readinessProbe") and item.get("livenessProbe")
    }
    return required_names <= found


def verify_workload_probes() -> None:
    adapter = kubectl_json(
        ["-n", ADAPTER_NAMESPACE, "get", "deployment", ADAPTER_DEPLOYMENT]
    )
    if not has_container_probes(adapter, {ADAPTER_DEPLOYMENT}):
        fail("adapter Deployment required container lacks readiness or liveness probe")
    deployments = kubectl_json(
        [
            "-n", "envoy-gateway-system", "get", "deployments",
            "-l", "gateway.envoyproxy.io/owning-gateway-name=ani-aigw",
        ]
    )
    items = deployments.get("items") or []
    if len(items) != 1 or not isinstance(items[0], dict):
        fail("owning Envoy Deployment selector must resolve exactly one Deployment")
    if not has_container_probes(items[0], {"envoy", "shutdown-manager"}):
        fail("owning Envoy Deployment required containers lack readiness or liveness probes")


def wait_for_expiry(api_key: str, marker: str, timeout: int = 45) -> None:
    deadline = time.monotonic() + timeout
    while time.monotonic() < deadline:
        status, _ = chat(api_key, stream=False, marker=marker)
        if status == 401:
            return
        time.sleep(1)
    fail("temporary expiring API key did not become unauthorized within the bounded wait")


def set_adapter_auth_service(address: str) -> None:
    kubectl(["-n", ADAPTER_NAMESPACE, "set", "env", f"deployment/{ADAPTER_DEPLOYMENT}", f"AUTH_SERVICE_GRPC_ADDR={address}"])
    kubectl(["-n", ADAPTER_NAMESPACE, "rollout", "status", f"deployment/{ADAPTER_DEPLOYMENT}", "--timeout=120s"])


def adapter_failure_probe(api_key: str, marker: str) -> None:
    status, _ = chat(api_key, stream=False, marker=marker)
    require_status(status, 503, "fail-closed probe")


def resolve_vllm_log_targets(endpoint_slices: dict[str, Any], pods: dict[str, dict[str, Any]]) -> list[tuple[str, str]]:
    """Resolve ready Service EndpointSlice targetRefs to their vLLM containers."""
    targets: list[tuple[str, str]] = []
    for item in endpoint_slices.get("items", []):
        if not isinstance(item, dict) or 8000 not in {port.get("port") for port in item.get("ports") or [] if isinstance(port, dict)}:
            continue
        for endpoint in item.get("endpoints") or []:
            if not isinstance(endpoint, dict) or (endpoint.get("conditions") or {}).get("ready") is not True:
                continue
            ref = endpoint.get("targetRef") or {}
            name = ref.get("name") if isinstance(ref, dict) and ref.get("kind") == "Pod" else None
            pod = pods.get(name) if isinstance(name, str) else None
            if not isinstance(pod, dict):
                continue
            containers = (pod.get("spec") or {}).get("containers") or []
            matches = [
                str(container.get("name"))
                for container in containers
                if isinstance(container, dict)
                and isinstance(container.get("name"), str)
                and (container.get("name") == "vllm" or "vllm" in str(container.get("image") or "").lower())
            ]
            if len(matches) != 1:
                fail("ready retained vLLM Pod must expose exactly one vLLM container")
            targets.append((name, matches[0]))
    if not targets:
        fail("retained vLLM Service has no ready EndpointSlice Pod target")
    return sorted(set(targets))


def selected_vllm_logs_for_service(service_name: str) -> list[str]:
    slices = kubectl_json(
        ["-n", VLLM_NAMESPACE, "get", "endpointslices", "-l", f"kubernetes.io/service-name={service_name}"]
    )
    pod_names = {
        endpoint.get("targetRef", {}).get("name")
        for item in slices.get("items", [])
        if isinstance(item, dict)
        for endpoint in item.get("endpoints") or []
        if isinstance(endpoint, dict)
        and (endpoint.get("conditions") or {}).get("ready") is True
        and isinstance(endpoint.get("targetRef"), dict)
        and endpoint["targetRef"].get("kind") == "Pod"
    }
    pods = {
        name: kubectl_json(["-n", VLLM_NAMESPACE, "get", "pod", name])
        for name in pod_names
        if isinstance(name, str) and name
    }
    return [
        kubectl(["-n", VLLM_NAMESPACE, "logs", pod_name, "-c", container, "--tail=500"])
        for pod_name, container in resolve_vllm_log_targets(slices, pods)
    ]


def selected_vllm_logs() -> list[str]:
    logs: list[str] = []
    for service_name in VLLM_SERVICES.values():
        logs.extend(selected_vllm_logs_for_service(service_name))
    return logs


def has_required_header_removals(route: dict[str, Any]) -> bool:
    required = {"authorization", "x-api-key", "x-ani-tenant-id", "x-ani-user-id"}
    for rule in ((route.get("spec") or {}).get("rules") or []):
        if not isinstance(rule, dict):
            continue
        for item in rule.get("filters") or []:
            if not isinstance(item, dict) or item.get("type") != "RequestHeaderModifier":
                continue
            modifier = item.get("requestHeaderModifier") or {}
            removed = modifier.get("remove") if isinstance(modifier, dict) else None
            if isinstance(removed, list) and required <= {str(header).lower() for header in removed}:
                return True
    return False


def is_current_c40_http_route(route: dict[str, Any]) -> bool:
    metadata = route.get("metadata") or {}
    status = route.get("status") or {}
    generation = metadata.get("generation")
    if metadata.get("name") != "ani-c40" or not isinstance(generation, int):
        return False
    parent_fresh = False
    for parent in status.get("parents") or []:
        if not isinstance(parent, dict) or parent.get("controllerName") != ENVOY_GATEWAY_CONTROLLER:
            continue
        parent_ref = parent.get("parentRef") or {}
        if not isinstance(parent_ref, dict):
            continue
        if (
            parent_ref.get("group", "gateway.networking.k8s.io") != "gateway.networking.k8s.io"
            or parent_ref.get("kind", "Gateway") != "Gateway"
            or parent_ref.get("name") != "ani-aigw"
            or (parent_ref.get("namespace") is not None and parent_ref.get("namespace") != ADAPTER_NAMESPACE)
        ):
            continue
        conditions = parent.get("conditions") or []
        if all(
            any(
                isinstance(item, dict)
                and item.get("type") == condition_type
                and item.get("status") == "True"
                and item.get("observedGeneration") == generation
                for item in conditions
            )
            for condition_type in ("Accepted", "ResolvedRefs")
        ):
            parent_fresh = True
            break
    if not parent_fresh:
        return False
    found_models: set[str] = set()
    for rule in ((route.get("spec") or {}).get("rules") or []):
        if not isinstance(rule, dict):
            continue
        matches = rule.get("matches") or []
        model = None
        for match in matches:
            if not isinstance(match, dict):
                continue
            for header in match.get("headers") or []:
                if (
                    isinstance(header, dict)
                    and str(header.get("name") or "").lower() == "x-ai-eg-model"
                    and header.get("type") == "Exact"
                    and header.get("value") in MODEL_BACKENDS
                ):
                    model = str(header["value"])
        if model is None:
            continue
        has_backend = any(
            isinstance(reference, dict) and reference.get("name") == MODEL_BACKENDS[model]
            for reference in rule.get("backendRefs") or []
        )
        if has_backend and has_required_header_removals({"spec": {"rules": [rule]}}):
            found_models.add(model)
    return set(MODEL_BACKENDS) <= found_models


def is_retained_clusterip_service(service: dict[str, Any]) -> bool:
    spec = service.get("spec") or {}
    cluster_ip = spec.get("clusterIP")
    if spec.get("type") != "ClusterIP" or not isinstance(cluster_ip, str) or not cluster_ip or cluster_ip == "None":
        return False
    if spec.get("externalIPs") or spec.get("loadBalancerIP"):
        return False
    return any(
        isinstance(port, dict)
        and port.get("protocol", "TCP") == "TCP"
        and port.get("port") == 8000
        and str(port.get("targetPort")) == "8000"
        for port in spec.get("ports") or []
    )


def is_exact_c40_backend(backend: dict[str, Any], service_name: str) -> bool:
    return (backend.get("spec") or {}) == {
        "type": "Endpoints",
        "endpoints": [
            {
                "fqdn": {
                    "hostname": f"{service_name}.{VLLM_NAMESPACE}.svc.cluster.local",
                    "port": 8000,
                }
            }
        ],
    }


def verify_header_and_backend(api_keys: list[str]) -> None:
    route = kubectl_json(["-n", ADAPTER_NAMESPACE, "get", "httproute", "ani-c40"])
    if not is_current_c40_http_route(route):
        fail("generated HTTPRoute is not current or lacks the exact C40 route binding")
    for model_id, backend_name in MODEL_BACKENDS.items():
        service_name = CHAT_VLLM_SERVICE if model_id == CHAT_MODEL_ID else EMBED_VLLM_SERVICE
        backend = kubectl_json(["-n", ADAPTER_NAMESPACE, "get", "backend", backend_name])
        if not is_exact_c40_backend(backend, service_name):
            fail("C40 Backend does not match the exact retained ClusterIP endpoint contract")
        service = kubectl_json(["-n", VLLM_NAMESPACE, "get", "service", service_name])
        if not is_retained_clusterip_service(service):
            fail("retained vLLM Service is not the required private TCP 8000 ClusterIP")
    logs = selected_vllm_logs()
    if any(api_key in log for log in logs for api_key in api_keys):
        fail("temporary API key material reached selected vLLM logs")


def scan_secret_data(api_keys: list[str]) -> int:
    matches = 0
    material = temporary_key_search_material(api_keys)
    for namespace in (ADAPTER_NAMESPACE, VLLM_NAMESPACE):
        secrets = kubectl_json(["-n", namespace, "get", "secrets"])
        for item in secrets.get("items", []):
            for encoded in ((item.get("data") or {}).values() if isinstance(item, dict) else []):
                try:
                    decoded = base64.b64decode(str(encoded)).decode("utf-8", errors="ignore")
                except ValueError:
                    continue
                matches += sum(value in decoded for value in material)
    return matches


def logs_for_selector(namespace: str, selector: str) -> list[str]:
    pods = kubectl_json(["-n", namespace, "get", "pods", "-l", selector])
    logs: list[str] = []
    for item in pods.get("items", []):
        if not isinstance(item, dict):
            continue
        pod_name = (item.get("metadata") or {}).get("name")
        if not isinstance(pod_name, str) or not pod_name:
            continue
        for container in ((item.get("spec") or {}).get("containers") or []):
            name = container.get("name") if isinstance(container, dict) else None
            if isinstance(name, str) and name:
                logs.append(kubectl(["-n", namespace, "logs", pod_name, "-c", name, "--tail=500"]))
    return logs


def scan_relevant_log_data(api_keys: list[str]) -> dict[str, bool]:
    material = temporary_key_search_material(api_keys)
    sources = {
        "envoy": logs_for_selector(
            "envoy-gateway-system",
            "app.kubernetes.io/name=envoy,gateway.envoyproxy.io/owning-gateway-name=ani-aigw",
        ),
        "adapter": logs_for_selector(ADAPTER_NAMESPACE, "app.kubernetes.io/name=envoy-authz-adapter"),
        "vllm": selected_vllm_logs(),
    }
    return {source: any(value in output for output in outputs for value in material) for source, outputs in sources.items()}


def temporary_key_search_material(api_keys: list[str]) -> set[str]:
    """Keep full temporary keys and a non-trivial prefix in memory-only scans."""
    return {material for key in api_keys for material in (key, key[:20]) if material}


def cleanup_api_keys(entries: list[tuple[str, str]]) -> list[str]:
    failed: list[str] = []
    for access_token, key_id in reversed(entries):
        try:
            revoke_api_key(access_token, key_id)
        except SystemExit:
            failed.append(key_id)
    return failed


def write_evidence_atomically(target: Path, evidence: dict[str, Any]) -> None:
    """Persist validated redacted evidence without ever exposing a partial target."""
    target.parent.mkdir(parents=True, exist_ok=True)
    fd, temporary_name = tempfile.mkstemp(prefix=".inference-envoy-ai-gateway-", suffix=".tmp", dir=target.parent)
    temporary = Path(temporary_name)
    try:
        os.fchmod(fd, 0o600)
        with os.fdopen(fd, "w", encoding="utf-8") as handle:
            handle.write(json.dumps(evidence, ensure_ascii=True, indent=2, sort_keys=True) + "\n")
            handle.flush()
            os.fsync(handle.fileno())
        validate_evidence(temporary)
        os.replace(temporary, target)
        directory_fd = os.open(target.parent, os.O_DIRECTORY)
        try:
            os.fsync(directory_fd)
        finally:
            os.close(directory_fd)
    finally:
        if temporary.exists():
            temporary.unlink()


def run_regressions() -> None:
    for command in (
        ["make", "validate-inference-control-plane"],
        ["make", "validate-inference-gpu-memory-live-gate"],
    ):
        completed = subprocess.run(command, cwd=ROOT, text=True, capture_output=True, check=False, timeout=300)
        if completed.returncode != 0:
            fail("required local regression command failed")


def run_live() -> dict[str, Any]:
    for name in (
        "KUBECONFIG",
        "ANI_C40_CONTROL_PLANE_URL",
        "ANI_C40_GATEWAY_URL",
        "ANI_C40_OWNER_ACCESS_TOKEN",
        "ANI_C40_FOREIGN_ACCESS_TOKEN",
    ):
        required_env(name)
    owner_access = required_env("ANI_C40_OWNER_ACCESS_TOKEN")
    foreign_access = required_env("ANI_C40_FOREIGN_ACCESS_TOKEN")
    validate_control_plane_url(required_env("ANI_C40_CONTROL_PLANE_URL"))
    key_cleanup: list[tuple[str, str]] = []
    all_key_values: list[str] = []
    checks: dict[str, dict[str, str]] = {}
    adapter_state: AdapterState | None = None
    try:
        adapter_state = apply_and_snapshot_adapter()
        for kind, name in (
            ("gateway", "ani-aigw"),
            ("httproute", "ani-c40"),
            ("backend", "ani-c40-chat-vllm"),
            ("backend", "ani-c40-embed-vllm"),
            ("aiservicebackend", "ani-c40-chat-vllm"),
            ("aiservicebackend", "ani-c40-embed-vllm"),
            ("aigatewayroute", "ani-c40"),
            ("securitypolicy", "ani-c40-ext-auth"),
        ):
            wait_condition(kind, name, "Accepted", ADAPTER_NAMESPACE)
        checks["envoy-resources-accepted"] = {"status": "passed"}
        checks["adapter-ready"] = {"status": "passed"}

        owner_id, owner_key = create_registered_api_key(owner_access, key_cleanup, name=temporary_name("owner"), scopes=["scope:models:read"], rpm=60)
        all_key_values.append(owner_key)
        status, response = chat(owner_key, stream=False, marker=temporary_name("nonstream"))
        require_status(status, 200, "valid owner non-stream chat")
        if not isinstance(response, dict) or not response.get("choices"):
            fail("valid owner non-stream chat returned no choices")
        checks["valid-ak-nonstream-chat"] = {"status": "passed"}
        status, response = embeddings(owner_key, marker=temporary_name("embedding"))
        require_status(status, 200, "valid owner embedding")
        data = response.get("data") if isinstance(response, dict) else None
        vector = data[0].get("embedding") if isinstance(data, list) and data and isinstance(data[0], dict) else None
        if not isinstance(vector, list) or not vector:
            fail("valid owner embedding returned no embedding vector")
        checks["valid-ak-embedding"] = {"status": "passed"}
        status, stream = chat(owner_key, stream=True, marker=temporary_name("sse"))
        require_status(status, 200, "valid owner SSE chat")
        if not isinstance(stream, str) or "data:" not in stream or "[DONE]" not in stream:
            fail("valid owner SSE chat did not include data and DONE")
        checks["valid-ak-sse-done"] = {"status": "passed"}

        require_status(chat(None, stream=False, marker=temporary_name("missing"))[0], 401, "missing AK")
        require_status(chat("malformed", stream=False, marker=temporary_name("malformed"))[0], 401, "malformed AK")
        checks["missing-ak-401"] = {"status": "passed"}
        checks["malformed-ak-401"] = {"status": "passed"}

        verify_credential_location_boundary(owner_key)
        checks["credential-location-boundary"] = {"status": "passed"}
        public_path_markers = verify_public_path_boundary()
        if any(
            marker in output
            for output in selected_vllm_logs()
            for marker in public_path_markers
        ):
            fail("public unregistered path request marker reached vLLM")
        checks["public-path-boundary"] = {"status": "passed"}
        verify_workload_probes()
        checks["workload-probes-ready"] = {"status": "passed"}

        expires_at = (dt.datetime.now(dt.UTC) + dt.timedelta(seconds=20)).isoformat().replace("+00:00", "Z")
        expired_id, expired_key = create_registered_api_key(owner_access, key_cleanup, name=temporary_name("expired"), scopes=["scope:models:read"], rpm=60, expires_at=expires_at)
        all_key_values.append(expired_key)
        wait_for_expiry(expired_key, temporary_name("expiry"))
        checks["expired-ak-401"] = {"status": "passed"}

        revoked_id, revoked_key = create_registered_api_key(owner_access, key_cleanup, name=temporary_name("revoked"), scopes=["scope:models:read"], rpm=60)
        all_key_values.append(revoked_key)
        require_status(chat(revoked_key, stream=False, marker=temporary_name("revoke-before"))[0], 200, "revocation precondition")
        revoke_api_key(owner_access, revoked_id)
        key_cleanup.remove((owner_access, revoked_id))
        require_status(chat(revoked_key, stream=False, marker=temporary_name("revoke-after"))[0], 401, "revoked AK")
        checks["revoked-ak-immediate-401"] = {"status": "passed"}

        rpm_id, rpm_key = create_registered_api_key(owner_access, key_cleanup, name=temporary_name("rpm"), scopes=["scope:models:read"], rpm=1)
        all_key_values.append(rpm_key)
        require_status(chat(rpm_key, stream=False, marker=temporary_name("rpm-one"))[0], 200, "RPM first request")
        require_status(chat(rpm_key, stream=False, marker=temporary_name("rpm-two"))[0], 429, "RPM second request")
        checks["rpm-limit-429"] = {"status": "passed"}

        foreign_id, foreign_key = create_registered_api_key(foreign_access, key_cleanup, name=temporary_name("foreign"), scopes=["scope:models:read"], rpm=60)
        all_key_values.append(foreign_key)
        require_status(chat(foreign_key, stream=False, marker=temporary_name("foreign"))[0], 404, "foreign tenant AK")
        checks["foreign-tenant-404"] = {"status": "passed"}

        marker = temporary_name("adapter-down")
        kubectl(["-n", ADAPTER_NAMESPACE, "scale", f"deployment/{ADAPTER_DEPLOYMENT}", "--replicas=0"])
        try:
            wait_adapter_endpoints_zero()
            adapter_failure_probe(owner_key, marker)
        finally:
            restore_failures = restore_adapter_state(adapter_state)
            if restore_failures:
                fail("adapter restoration failed: " + ",".join(restore_failures))
        if any(marker in output for output in selected_vllm_logs()):
            fail("adapter-down request marker reached vLLM")
        checks["authz-fail-closed-503"] = {"status": "passed"}

        marker = temporary_name("auth-unreachable")
        try:
            set_adapter_auth_service("127.0.0.1:1")
            adapter_failure_probe(owner_key, marker)
        finally:
            restore_failures = restore_adapter_state(adapter_state)
            if restore_failures:
                fail("adapter restoration failed: " + ",".join(restore_failures))
        if any(marker in output for output in selected_vllm_logs()):
            fail("auth-unreachable request marker reached vLLM")
        checks["auth-service-unreachable-503"] = {"status": "passed"}

        verify_header_and_backend(all_key_values)
        checks["authorization-not-forwarded"] = {"status": "passed"}
        checks["clusterip-only-backend"] = {"status": "passed"}
        run_regressions()
        checks["control-plane-regression-pass"] = {"status": "passed"}
        log_leaks = scan_relevant_log_data(all_key_values)
        secret_matches = scan_secret_data(all_key_values)
        if any(log_leaks.values()) or secret_matches != 0:
            fail("temporary API key material was found by an in-memory redaction scan")
        checks["secret-redaction-pass"] = {"status": "passed"}

        if set(checks) != REQUIRED_CHECKS:
            fail("runner did not produce every required C40 check")
        evidence = {
            "profile": PROFILE,
            "status": "passed",
            "envoy_invocation_ready": True,
            "dynamic_publication_ready": False,
            "runtime_ready": False,
            "authorization_not_forwarded": True,
            "clusterip_only_backend": True,
            "checks": [{"id": check_id, **checks[check_id]} for check_id in sorted(checks)],
        }
        assert_redacted_evidence(evidence)
        return evidence
    finally:
        cleanup_failed = cleanup_api_keys(key_cleanup)
        restore_failed = restore_adapter_state(adapter_state) if adapter_state is not None else []
        if cleanup_failed or restore_failed:
            failed = []
            if cleanup_failed:
                failed.append("api-keys")
            failed.extend("adapter-" + item for item in restore_failed)
            fail("cleanup/rollback incomplete: " + ",".join(failed))


def main() -> None:
    evidence = run_live()
    assert_redacted_evidence(evidence)
    write_evidence_atomically(EVIDENCE, evidence)
    print("inference envoy ai gateway live gate passed; redacted evidence written")


if __name__ == "__main__":
    main()
