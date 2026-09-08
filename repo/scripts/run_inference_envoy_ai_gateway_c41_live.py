#!/usr/bin/env python3
"""Run the explicitly approved, redacted C41 multi-tenant live gate.

Importing this module is inert.  ``main`` is the only entry point that can
call HTTP or kubectl, and it is intentionally not reached by local contract
validation.
"""

from __future__ import annotations

import base64
import copy
import ipaddress
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
from dataclasses import dataclass, field
from pathlib import Path
from typing import Any

import yaml

from validate_inference_envoy_ai_gateway_c41_live_gate import PROFILE, REQUIRED_CHECKS, REQUIRED_ENV

ROOT = Path(__file__).resolve().parents[1]
EVIDENCE = ROOT / "development-records/live-evidence/inference-envoy-ai-gateway-c41-live.json"
TASK8_MANIFEST = ROOT / "deploy/real-k8s-lab/inference-envoy-ai-gateway-c41.yaml"
PUBLISHER_NAMESPACE = "ani-system"
PUBLISHER_DEPLOYMENT = "inference-gateway-publisher"
GATEWAY_NAMESPACE = "ani-aigw"
SYSTEM_NAMESPACE = "ani-system"
MANAGED_BY_LABEL = "app.kubernetes.io/managed-by=ani-inference-gateway-publisher"
PUBLISHER_NAME = "ani-inference-gateway-publisher"
SERVICE_ID_LABEL = "services.ani.io/inference-service-id"
OWNER_REF_LABEL = "ani.kubercloud.io/owner-ref"
TENANT_ID_LABEL = "ani.kubercloud.io/tenant-id"
PUBLICATION_SERVICE_LABEL = "ani.kubercloud.io/inference-service-id"
PUBLICATION_GENERATION_LABEL = "ani.kubercloud.io/publication-generation"
PUBLICATION_KINDS = ("Backend", "AIServiceBackend", "AIGatewayRoute")
SENSITIVE_VALUE_RE = re.compile(r"(?:bearer\s+\S+|ani_[^\s\"']+)", re.IGNORECASE)
FORBIDDEN_EVIDENCE_TEXT_RE = re.compile(r"(?:authorization|bearer|ani_|prompt|completion|vector)", re.IGNORECASE)
FORBIDDEN_EVIDENCE_KEYS = {
    "authorization", "bearer", "api_key", "access_token", "token", "key_value",
    "prompt", "prompts", "completion", "completions", "vector", "vectors", "embedding",
    "input", "output", "logs", "secret", "data",
}
IMAGE_DIGEST_RE = re.compile(r"^.+@sha256:[0-9a-f]{64}$")
FAULT_DEPLOYMENTS = {
    "adapter": (GATEWAY_NAMESPACE, "envoy-authz-adapter", "envoy-authz-adapter"),
    "auth": (SYSTEM_NAMESPACE, "ani-auth-service", "ani-auth-service"),
    "inference": (SYSTEM_NAMESPACE, "inference-service", "inference-service"),
    "redis": (SYSTEM_NAMESPACE, "ani-reconcile-ha-redis", "ani-reconcile-ha-redis"),
}


class _NoRedirect(urllib.request.HTTPRedirectHandler):
    def redirect_request(self, req: Any, fp: Any, code: int, msg: str, headers: Any, newurl: str) -> None:
        return None


def _urlopen_no_redirect(request: urllib.request.Request, timeout: int):
    return urllib.request.build_opener(_NoRedirect()).open(request, timeout=timeout)


def fail(message: str) -> None:
    raise SystemExit(f"C41 AI Gateway live gate failed: {message}")


def aggregate_failures(primary: BaseException | None, cleanup: BaseException | None, publisher_restore: BaseException | None) -> SystemExit | None:
    categories = []
    if primary is not None:
        categories.append("primary")
    if cleanup is not None:
        categories.append("cleanup")
    if publisher_restore is not None:
        categories.append("publisher-restore")
    return SystemExit("live gate failed: " + ",".join(categories)) if categories else None


def aggregate_fault_failures(primary: BaseException | None, restore: BaseException | None) -> SystemExit | None:
    categories = []
    if primary is not None:
        categories.append("primary")
    if restore is not None:
        categories.append("restore")
    return SystemExit("fault injection failed: " + ",".join(categories)) if categories else None


def required_env(name: str) -> str:
    value = os.environ.get(name, "").strip()
    if not value:
        fail(f"{name} is required")
    return value


def _is_internal_host(host: str) -> bool:
    lowered = host.rstrip(".").lower()
    if lowered == "localhost" or lowered.endswith(".svc") or ".svc." in lowered:
        return lowered != "localhost"
    try:
        address = ipaddress.ip_address(lowered)
    except ValueError:
        return False
    return not address.is_loopback and (address.is_private or address.is_link_local or address.is_unspecified)


def validate_public_url(value: str, label: str) -> str:
    parsed = urllib.parse.urlsplit(value)
    if parsed.scheme not in {"http", "https"} or not parsed.hostname or parsed.query or parsed.fragment or parsed.username or parsed.password:
        fail(f"{label} must be an absolute HTTP(S) URL")
    if _is_internal_host(parsed.hostname):
        fail(f"{label} must not use a ClusterIP or .svc host")
    return urllib.parse.urlunsplit((parsed.scheme, parsed.netloc, parsed.path.rstrip("/"), "", ""))


def validate_control_plane_url(value: str) -> str:
    normalized = validate_public_url(value, "ANI_C41_CONTROL_PLANE_URL")
    parsed = urllib.parse.urlsplit(normalized)
    if parsed.path.rstrip("/") != "/api/v1":
        fail("ANI_C41_CONTROL_PLANE_URL must end in /api/v1")
    return normalized


def tenant_id_from_login_jwt(token: str) -> str:
    """Read only the non-secret tenant claim needed to bind runtime identity."""
    parts = token.split(".")
    if len(parts) != 3:
        fail("tenant access token must be a JWT")
    try:
        payload = parts[1] + "=" * (-len(parts[1]) % 4)
        claims = json.loads(base64.urlsafe_b64decode(payload.encode("ascii")).decode("utf-8"))
        tenant_id = str(uuid.UUID(str(claims.get("tid", ""))))
    except (ValueError, TypeError, UnicodeError, json.JSONDecodeError):
        fail("tenant access token has no valid tenant UUID claim")
    return tenant_id


def lifecycle_plan() -> list[str]:
    """Describe, but never execute, the ordered live workflow."""
    return [
        "authorize-live-run", "snapshot-publisher", "create-temporary-api-keys",
        "create-tenant-services", "wait-for-publication", "run-acceptance-matrix",
        "scan-memory-only-logs", "cleanup-and-restore", "write-redacted-evidence",
    ]


def control_request(method: str, path: str, access_token: str, body: dict[str, Any] | None = None, extra_headers: dict[str, str] | None = None) -> tuple[int, dict[str, Any]]:
    base = validate_control_plane_url(required_env("ANI_C41_CONTROL_PLANE_URL"))
    payload = json.dumps(body, separators=(",", ":")).encode("utf-8") if body is not None else None
    headers = {"Accept": "application/json", "Authorization": "Bearer " + access_token}
    headers.update(extra_headers or {})
    if payload is not None:
        headers["Content-Type"] = "application/json"
    request = urllib.request.Request(base + path, data=payload, headers=headers, method=method)
    try:
        with _urlopen_no_redirect(request, timeout=45) as response:
            raw = response.read().decode("utf-8")
            try:
                return response.status, json.loads(raw) if raw.strip() else {}
            except json.JSONDecodeError:
                fail("control-plane returned malformed JSON")
    except urllib.error.HTTPError as error:
        error.read()  # deliberately discard possibly credential-bearing response body
        return error.code, {}
    except urllib.error.URLError:
        fail("control-plane request could not be completed")
    return 0, {}


def gateway_response(path: str, *, method: str = "GET", body: dict[str, Any] | None = None, headers: dict[str, str] | None = None) -> tuple[int, str, dict[str, str]]:
    base = validate_public_url(required_env("ANI_C41_GATEWAY_URL"), "ANI_C41_GATEWAY_URL")
    payload = json.dumps(body, separators=(",", ":")).encode("utf-8") if body is not None else None
    request_headers = dict(headers or {})
    if payload is not None:
        request_headers.setdefault("Content-Type", "application/json")
    request = urllib.request.Request(base + path, data=payload, headers=request_headers, method=method)
    try:
        with _urlopen_no_redirect(request, timeout=120) as response:
            return response.status, response.read(1_000_000).decode("utf-8", errors="replace"), {str(k).lower(): str(v) for k, v in response.headers.items()}
    except urllib.error.HTTPError as error:
        error.read()  # no data-plane body is retained on failures
        return error.code, "", {str(k).lower(): str(v) for k, v in error.headers.items()}
    except urllib.error.URLError:
        fail("data-plane request could not be completed")
    return 0, "", {}


def gateway_request(path: str, **kwargs: Any) -> tuple[int, str]:
    status, response, _ = gateway_response(path, **kwargs)
    return status, response


def _forbid_secret_data_command(args: list[str]) -> None:
    if not any("secret" in part.lower() for part in args):
        return
    allowed = args == ["-n", GATEWAY_NAMESPACE, "get", "secrets", "-l", MANAGED_BY_LABEL, "-o", "name"]
    if not allowed:
        fail("runner permits only metadata-only kubectl get secret(s) ... -o name")


def kubectl(args: list[str], timeout: int = 120) -> str:
    _forbid_secret_data_command(args)
    completed = subprocess.run(["kubectl", *args], text=True, capture_output=True, check=False, timeout=timeout)
    if completed.returncode != 0:
        fail("kubectl command failed")
    return completed.stdout


def kubectl_json(args: list[str], timeout: int = 120) -> dict[str, Any]:
    _forbid_secret_data_command(args)
    output = kubectl([*args, "-o", "json"], timeout=timeout)
    try:
        document = json.loads(output)
    except json.JSONDecodeError:
        fail("kubectl returned malformed JSON")
    if not isinstance(document, dict):
        fail("kubectl JSON must be an object")
    return document


def create_registered_api_key(access_token: str, cleanup: list[tuple[str, str]], *, name: str, rpm: int) -> tuple[str, str]:
    status, result = control_request("POST", "/auth/api-keys", access_token, {"name": name, "scopes": ["scope:inference:invoke"], "rate_limit_rpm": rpm})
    if status != 201:
        fail(f"create temporary API key returned HTTP {status}")
    key_id = result.get("key_id")
    if not isinstance(key_id, str) or not key_id:
        fail("create temporary API key returned no key id")
    cleanup.append((access_token, key_id))  # registration precedes one-time plaintext validation
    key_value = result.get("key_value")
    if not isinstance(key_value, str) or not key_value:
        fail("create temporary API key returned no key value")
    return key_id, key_value


def revoke_api_key(access_token: str, key_id: str) -> None:
    status, _ = control_request("DELETE", f"/auth/api-keys/{key_id}", access_token)
    if status not in {200, 204, 404}:
        fail(f"temporary API key cleanup returned HTTP {status}")


def delete_inference_service(access_token: str, service_id: str) -> None:
    status, _ = control_request("DELETE", f"/svc/inference-services/{service_id}", access_token)
    if status not in {200, 202, 204, 404}:
        fail(f"temporary inference service cleanup returned HTTP {status}")
    if status != 404:
        wait_for_service_deleted(access_token, service_id)


def delete_inference_service_for_reuse(access_token: str, service_id: str) -> None:
    status, _ = control_request("DELETE", f"/svc/inference-services/{service_id}", access_token)
    if status not in {200, 202, 204}:
        fail(f"delete-before-name-reuse returned HTTP {status}")
    wait_for_service_deleted(access_token, service_id)
    wait_for_publications_absent(service_id)


def delete_policy(access_token: str, policy_id: str) -> None:
    status, _ = control_request("DELETE", f"/svc/inference-policies/{policy_id}", access_token, extra_headers={"Idempotency-Key": str(uuid.uuid4())})
    if status not in {200, 202, 204, 404}:
        fail(f"temporary policy cleanup returned HTTP {status}")


def create_policy(access_token: str, cleanup: "CleanupState", *, name: str, priority: int, scope: dict[str, Any], access: dict[str, Any]) -> str:
    body = {
        "idempotency_key": str(uuid.uuid4()), "name": name, "status": "enabled", "priority": priority,
        "scope": scope, "access": access, "rate_limits": {"qps": None, "rpm": None},
        "concurrency": {"max_in_flight": None, "lease_ttl_seconds": 60},
    }
    status, result = control_request("POST", "/svc/inference-policies", access_token, body)
    if status != 201:
        fail("temporary inference policy creation was not accepted")
    policy_id = result.get("id")
    if not isinstance(policy_id, str) or not policy_id:
        fail("temporary inference policy has no id")
    cleanup.register_policy(access_token, policy_id)
    return policy_id


@dataclass
class CleanupState:
    """Only IDs created by this runner are ever registered here."""

    services: list[tuple[str, str]] = field(default_factory=list)
    policies: list[tuple[str, str]] = field(default_factory=list)
    api_keys: list[tuple[str, str]] = field(default_factory=list)

    def register_service(self, token: str, resource_id: str) -> None:
        self.services.append((token, resource_id))

    def register_policy(self, token: str, resource_id: str) -> None:
        self.policies.append((token, resource_id))

    def register_api_key(self, token: str, resource_id: str) -> None:
        self.api_keys.append((token, resource_id))


def wait_for_service_deleted(access_token: str, service_id: str, timeout: int = 180) -> None:
    deadline = time.monotonic() + timeout
    while time.monotonic() < deadline:
        status, _ = control_request("GET", f"/svc/inference-services/{service_id}", access_token)
        if status == 404:
            return
        time.sleep(2)
    fail("deleted inference service remained visible")


def wait_for_publications_absent(service_id: str, timeout: int = 180) -> None:
    selector = MANAGED_BY_LABEL + ",ani.kubercloud.io/inference-service-id=" + service_id
    deadline = time.monotonic() + timeout
    while time.monotonic() < deadline:
        resources = kubectl_json(["-n", GATEWAY_NAMESPACE, "get", "backend,aiservicebackend,aigatewayroute", "-l", selector])
        if not resources.get("items"):
            return
        time.sleep(2)
    fail("runner-owned publisher resources did not disappear")


def snapshot_deployment(key: str) -> dict[str, Any]:
    namespace, name, _ = FAULT_DEPLOYMENTS[key]
    deployment = kubectl_json(["-n", namespace, "get", "deployment", name])
    replicas = (deployment.get("spec") or {}).get("replicas")
    generation = (deployment.get("metadata") or {}).get("generation")
    if not isinstance(replicas, int) or replicas <= 0 or not isinstance(generation, int):
        fail("fault target deployment must have positive replicas and a generation")
    return deployment


def scale_and_wait(key: str, replicas: int) -> None:
    namespace, name, _ = FAULT_DEPLOYMENTS[key]
    kubectl(["-n", namespace, "scale", "deployment/" + name, "--replicas=" + str(replicas)])
    kubectl(["-n", namespace, "rollout", "status", "deployment/" + name, "--timeout=120s"])


def _ready_endpoint_count(namespace: str, service_name: str) -> int:
    slices = kubectl_json(["-n", namespace, "get", "endpointslices", "-l", "kubernetes.io/service-name=" + service_name])
    return sum(
        1
        for item in slices.get("items") or []
        if isinstance(item, dict)
        for endpoint in item.get("endpoints") or []
        if isinstance(endpoint, dict) and endpoint.get("conditions", {}).get("ready") is True
    )


def scale_fault_to_zero(key: str, snapshot: dict[str, Any] | None = None) -> None:
    snapshot = snapshot or snapshot_deployment(key)
    if int((snapshot.get("spec") or {}).get("replicas") or 0) <= 0:
        fail("fault target must have positive original replicas")
    namespace, name, service_name = FAULT_DEPLOYMENTS[key]
    kubectl(["-n", namespace, "scale", "deployment/" + name, "--replicas=0"])
    deadline = time.monotonic() + 120
    while time.monotonic() < deadline:
        if _ready_endpoint_count(namespace, service_name) == 0:
            return
        time.sleep(1)
    fail("fault target still has ready endpoints")


def assert_gateway_counter_recovers(api_key: str, service_id: str, tenant_id: str) -> None:
    before = selected_vllm_counter(service_id, tenant_id)
    _require_status(_chat(api_key, "ani-c41-shared")[0], 200, "fault restoration")
    if selected_vllm_counter(service_id, tenant_id) <= before:
        fail("gateway did not recover to selected vLLM")


def restore_deployment(key: str, snapshot: dict[str, Any]) -> None:
    namespace, name, _ = FAULT_DEPLOYMENTS[key]
    replicas = (snapshot.get("spec") or {}).get("replicas")
    if not isinstance(replicas, int) or replicas <= 0:
        fail("fault target snapshot lacks positive replicas")
    current = kubectl_json(["-n", namespace, "get", "deployment", name])
    resource_version = ((current.get("metadata") or {}).get("resourceVersion"))
    if not isinstance(resource_version, str) or not resource_version:
        fail("fault target restore cannot establish current resourceVersion")
    restore = copy.deepcopy(snapshot)
    restore.pop("status", None)
    metadata = dict(restore.get("metadata") or {})
    metadata["resourceVersion"] = resource_version
    restore["metadata"] = metadata
    completed = subprocess.run(
        ["kubectl", "replace", "-f", "-"], text=True, input=json.dumps(restore),
        capture_output=True, check=False, timeout=120,
    )
    if completed.returncode != 0:
        fail("fault target exact restore failed")
    wait_fault_target_recovered(key, replicas)


def wait_fault_target_recovered(key: str, replicas: int, timeout: int = 180) -> None:
    namespace, name, service_name = FAULT_DEPLOYMENTS[key]
    deadline = time.monotonic() + timeout
    while time.monotonic() < deadline:
        deployment = kubectl_json(["-n", namespace, "get", "deployment", name])
        generation = (deployment.get("metadata") or {}).get("generation")
        status = deployment.get("status") or {}
        if (
            isinstance(generation, int)
            and int(status.get("observedGeneration") or 0) >= generation
            and int(status.get("readyReplicas") or status.get("availableReplicas") or 0) >= replicas
            and _ready_endpoint_count(namespace, service_name) >= replicas
        ):
            return
        time.sleep(1)
    fail("fault target did not recover generation, readiness, and endpoints")


def assert_fault_503_no_target_counter(api_key: str, service_id: str, tenant_id: str) -> None:
    before = selected_vllm_counter(service_id, tenant_id)
    _require_status(_chat(api_key, "ani-c41-shared")[0], 503, "dependency-faults-503-no-vllm")
    after = selected_vllm_counter(service_id, tenant_id)
    if after != before:
        fail("fault request reached the selected vLLM target")


def selected_vllm_counter(service_id: str, tenant_id: str) -> int:
    return sum(log.count("/v1/chat/completions") for log in selected_vllm_logs(service_id, tenant_id))


def assert_selected_route(api_key: str, model: str, expected_service_id: str, expected_tenant_id: str, other_service_id: str, other_tenant_id: str, check_id: str, headers: dict[str, str] | None = None) -> None:
    expected_before = selected_vllm_counter(expected_service_id, expected_tenant_id)
    other_before = selected_vllm_counter(other_service_id, other_tenant_id)
    _require_status(_chat(api_key, model, extra_headers=headers)[0], 200, check_id)
    if selected_vllm_counter(expected_service_id, expected_tenant_id) <= expected_before or selected_vllm_counter(other_service_id, other_tenant_id) != other_before:
        fail(check_id + " did not route exclusively to the selected vLLM target")


def run_fault_injections(api_key: str, service_id: str, tenant_id: str) -> None:
    for key in FAULT_DEPLOYMENTS:
        snapshot = snapshot_deployment(key)
        primary: BaseException | None = None
        restore_error: BaseException | None = None
        try:
            scale_fault_to_zero(key, snapshot)
            assert_fault_503_no_target_counter(api_key, service_id, tenant_id)
        except BaseException as error:
            primary = error
        finally:
            try:
                restore_deployment(key, snapshot)
            except BaseException as error:
                restore_error = error
        combined = aggregate_fault_failures(primary, restore_error)
        if combined is not None:
            raise combined
        assert_gateway_counter_recovers(api_key, service_id, tenant_id)


def assert_current_publication(access_token: str, service_id: str, tenant_id: str) -> None:
    status, service = control_request("GET", f"/svc/inference-services/{service_id}", access_token)
    generation = service.get("generation") if isinstance(service, dict) else None
    returned_id = service.get("id") or service.get("service_id") if isinstance(service, dict) else None
    if status != 200 or returned_id != service_id or not isinstance(generation, int) or generation <= 0:
        fail("publisher current-service probe returned invalid identity or generation")
    selector = MANAGED_BY_LABEL + "," + PUBLICATION_SERVICE_LABEL + "=" + service_id
    resources = kubectl_json(["-n", GATEWAY_NAMESPACE, "get", "backend,aiservicebackend,aigatewayroute", "-l", selector])
    items = resources.get("items") or []
    expected_labels = {
        "app.kubernetes.io/managed-by": PUBLISHER_NAME,
        TENANT_ID_LABEL: tenant_id,
        PUBLICATION_SERVICE_LABEL: service_id,
        PUBLICATION_GENERATION_LABEL: str(generation),
    }
    expected_name = "ani-inf-" + service_id
    for kind in PUBLICATION_KINDS:
        matches = [item for item in items if isinstance(item, dict) and item.get("kind") == kind]
        if len(matches) != 1:
            fail("publisher current generation must have exactly one resource per kind")
        metadata = matches[0].get("metadata") or {}
        labels = metadata.get("labels") or {}
        if metadata.get("name") != expected_name or metadata.get("namespace") != GATEWAY_NAMESPACE or labels != expected_labels:
            fail("publisher resource identity or publication generation is stale")
    if len(items) != len(PUBLICATION_KINDS):
        fail("publisher resource set contains unexpected duplicates")


def verify_publisher_reconcile(services: list[tuple[str, str, str]]) -> None:
    kubectl(["-n", PUBLISHER_NAMESPACE, "rollout", "restart", "deployment/" + PUBLISHER_DEPLOYMENT])
    kubectl(["-n", PUBLISHER_NAMESPACE, "rollout", "status", "deployment/" + PUBLISHER_DEPLOYMENT, "--timeout=180s"])
    for access_token, service_id, tenant_id in services:
        assert_current_publication(access_token, service_id, tenant_id)


def cleanup_or_fail(state: CleanupState) -> None:
    failures: list[str] = []
    for token, policy_id in reversed(state.policies):
        try:
            delete_policy(token, policy_id)
        except BaseException:
            failures.append("policy")
    for token, key_id in reversed(state.api_keys):
        try:
            revoke_api_key(token, key_id)
        except BaseException:
            failures.append("api-key")
    for token, service_id in reversed(state.services):
        try:
            delete_inference_service(token, service_id)
            wait_for_publications_absent(service_id)
        except BaseException:
            failures.append("service")
    if failures:
        fail("runner-owned cleanup failed: " + ",".join(sorted(set(failures))))


@dataclass
class PublisherResourceSnapshot:
    api_version: str
    kind: str
    namespace: str
    name: str
    resource: str
    existed: bool
    prior: dict[str, Any] | None
    prior_uid: str | None
    desired: dict[str, Any] | None = None
    created_uid: str | None = None
    mutation_attempted: bool = False


@dataclass
class PublisherSnapshot:
    resources: list[PublisherResourceSnapshot]
    mutation_started: bool = False


def _publisher_resource_from_document(document: dict[str, Any]) -> PublisherResourceSnapshot:
    api_version = document.get("apiVersion")
    kind = document.get("kind")
    metadata = document.get("metadata") if isinstance(document.get("metadata"), dict) else {}
    namespace, name = metadata.get("namespace"), metadata.get("name")
    if not all(isinstance(value, str) and value for value in (api_version, kind, namespace, name)):
        fail("Task8 manifest resource identity is incomplete")
    return PublisherResourceSnapshot(
        api_version=api_version, kind=kind, namespace=namespace, name=name,
        resource=kind.lower(), existed=False, prior=None, prior_uid=None,
        desired=copy.deepcopy(document),
    )


def _probe_publisher_resource(resource: PublisherResourceSnapshot) -> dict[str, Any] | None:
    probe = subprocess.run(
        ["kubectl", "-n", resource.namespace, "get", resource.resource, resource.name, "--ignore-not-found", "-o", "json"],
        text=True, capture_output=True, check=False, timeout=60,
    )
    if probe.returncode != 0 or not isinstance(probe.stdout, str):
        fail("publisher resource probe failed")
    if not probe.stdout.strip():
        return None
    try:
        current = json.loads(probe.stdout)
    except json.JSONDecodeError:
        fail("publisher resource probe returned malformed JSON")
    if not isinstance(current, dict):
        fail("publisher resource probe must return an object")
    metadata = current.get("metadata") if isinstance(current.get("metadata"), dict) else {}
    if (
        current.get("apiVersion") != resource.api_version
        or current.get("kind") != resource.kind
        or metadata.get("namespace") != resource.namespace
        or metadata.get("name") != resource.name
    ):
        fail("publisher resource probe returned wrong identity")
    if not isinstance(metadata.get("uid"), str) or not metadata.get("uid") or not isinstance(metadata.get("resourceVersion"), str) or not metadata.get("resourceVersion"):
        fail("publisher resource probe returned incomplete ownership metadata")
    return current


def snapshot_publisher() -> PublisherSnapshot:
    resources: list[PublisherResourceSnapshot] = []
    for document in _task8_manifest_documents():
        resource = _publisher_resource_from_document(document)
        prior = _probe_publisher_resource(resource)
        if prior is not None:
            metadata = prior.get("metadata") if isinstance(prior.get("metadata"), dict) else {}
            resource.existed = True
            resource.prior = copy.deepcopy(prior)
            resource.prior_uid = str(metadata["uid"])
        resources.append(resource)
    return PublisherSnapshot(resources)


def _task8_manifest_documents() -> list[dict[str, Any]]:
    try:
        documents = [item for item in yaml.safe_load_all(TASK8_MANIFEST.read_text(encoding="utf-8")) if item is not None]
    except (OSError, yaml.YAMLError):
        fail("Task8 manifest cannot be read for server dry-run")
    if len(documents) != 11 or not all(isinstance(item, dict) for item in documents):
        fail("Task8 manifest must contain exactly 11 Kubernetes resources")
    return documents


def validate_task8_server_dry_run() -> None:
    for document in _task8_manifest_documents():
        dry_run = subprocess.run(
            ["kubectl", "apply", "--server-side", "--dry-run=server", "--force-conflicts", "-f", "-"],
            text=True, input=json.dumps(document), capture_output=True, check=False, timeout=120,
        )
        if dry_run.returncode != 0:
            fail("Task8 manifest server dry-run failed")


def apply_publisher(url: str, snapshot: PublisherSnapshot) -> None:
    if len(snapshot.resources) != 11:
        fail("publisher apply requires snapshots for all 11 Task8 resources")
    for resource in snapshot.resources:
        if not isinstance(resource.desired, dict):
            fail("publisher apply resource has no desired manifest")
        if resource.existed:
            current = _probe_publisher_resource(resource)
            current_metadata = current.get("metadata") if isinstance(current, dict) and isinstance(current.get("metadata"), dict) else {}
            if current is None or current_metadata.get("uid") != resource.prior_uid:
                fail("publisher apply ownership changed after snapshot")
        snapshot.mutation_started = True
        resource.mutation_attempted = True
        applied = subprocess.run(
            ["kubectl", "apply", "--server-side", "--force-conflicts", "-f", "-"],
            text=True, input=json.dumps(resource.desired), capture_output=True, check=False, timeout=120,
        )
        ownership_error: BaseException | None = None
        if not resource.existed:
            try:
                current = _probe_publisher_resource(resource)
                metadata = current.get("metadata") if isinstance(current, dict) and isinstance(current.get("metadata"), dict) else {}
                created_uid = metadata.get("uid")
                if current is None or not isinstance(created_uid, str) or not created_uid:
                    fail("publisher-created resource ownership could not be established")
                resource.created_uid = created_uid
            except BaseException as error:
                ownership_error = error
        if applied.returncode != 0 or ownership_error is not None:
            categories = []
            if applied.returncode != 0:
                categories.append("apply")
            if ownership_error is not None:
                categories.append("ownership")
            fail("publisher manifest apply failed: " + ",".join(categories))
    set_publisher_public_base_url(url)


def prepare_publisher(url: str) -> PublisherSnapshot:
    validate_task8_server_dry_run()
    snapshot = snapshot_publisher()
    try:
        apply_publisher(url, snapshot)
    except BaseException as primary:
        restore_error: BaseException | None = None
        if snapshot.mutation_started:
            try:
                restore_publisher(snapshot)
            except BaseException as error:
                restore_error = error
        combined = aggregate_failures(primary, None, restore_error)
        if combined is not None:
            raise combined
        raise
    return snapshot


def set_publisher_public_base_url(url: str) -> None:
    validated = validate_public_url(url, "ANI_C41_GATEWAY_URL")
    kubectl(["-n", PUBLISHER_NAMESPACE, "set", "env", f"deployment/{PUBLISHER_DEPLOYMENT}", f"INFERENCE_AI_GATEWAY_PUBLIC_BASE_URL={validated}"])
    if urllib.parse.urlsplit(validated).scheme == "http":
        kubectl(["-n", PUBLISHER_NAMESPACE, "set", "env", f"deployment/{PUBLISHER_DEPLOYMENT}", "INFERENCE_AI_GATEWAY_ALLOW_HTTP=true"])
    else:
        kubectl(["-n", PUBLISHER_NAMESPACE, "set", "env", f"deployment/{PUBLISHER_DEPLOYMENT}", "INFERENCE_AI_GATEWAY_ALLOW_HTTP-"])
    kubectl(["-n", PUBLISHER_NAMESPACE, "rollout", "status", f"deployment/{PUBLISHER_DEPLOYMENT}", "--timeout=180s"])


def _wait_publisher_resource_absent(resource: PublisherResourceSnapshot, timeout: int = 120) -> None:
    deadline = time.monotonic() + timeout
    while time.monotonic() < deadline:
        if _probe_publisher_resource(resource) is None:
            return
        time.sleep(1)
    fail("publisher-created resource did not become absent")


def _restore_publisher_resource(resource: PublisherResourceSnapshot) -> None:
    current = _probe_publisher_resource(resource)
    if resource.existed:
        if not isinstance(resource.prior, dict) or not isinstance(resource.prior_uid, str):
            fail("publisher prior resource snapshot is incomplete")
        if current is None:
            fail("publisher prior resource disappeared before restore")
        current_metadata = current.get("metadata") if isinstance(current.get("metadata"), dict) else {}
        if current_metadata.get("uid") != resource.prior_uid:
            fail("publisher prior resource UID changed before restore")
        resource_version = current_metadata.get("resourceVersion")
        if not isinstance(resource_version, str) or not resource_version:
            fail("publisher restore cannot establish current resourceVersion")
        restore = copy.deepcopy(resource.prior)
        restore.pop("status", None)
        metadata = dict(restore.get("metadata") or {})
        metadata["resourceVersion"] = resource_version
        restore["metadata"] = metadata
        completed = subprocess.run(
            ["kubectl", "replace", "-f", "-"], text=True, input=json.dumps(restore),
            capture_output=True, check=False, timeout=120,
        )
        if completed.returncode != 0:
            fail("publisher existing resource CAS restore failed")
        return
    if current is None:
        return
    current_metadata = current.get("metadata") if isinstance(current.get("metadata"), dict) else {}
    if not isinstance(resource.created_uid, str) or current_metadata.get("uid") != resource.created_uid:
        fail("publisher-created resource UID no longer matches runner ownership")
    kubectl(["-n", resource.namespace, "delete", resource.resource, resource.name, "--ignore-not-found=false"])
    _wait_publisher_resource_absent(resource)


def restore_publisher(snapshot: PublisherSnapshot) -> None:
    failures: list[str] = []
    publisher_deployment_existed = False
    for resource in reversed(snapshot.resources):
        if not resource.mutation_attempted:
            continue
        if resource.kind == "Deployment" and resource.namespace == PUBLISHER_NAMESPACE and resource.name == PUBLISHER_DEPLOYMENT:
            publisher_deployment_existed = resource.existed
        try:
            _restore_publisher_resource(resource)
        except BaseException:
            failures.append("restore-existing" if resource.existed else "delete-created")
    if publisher_deployment_existed:
        try:
            kubectl(["-n", PUBLISHER_NAMESPACE, "rollout", "status", f"deployment/{PUBLISHER_DEPLOYMENT}", "--timeout=180s"])
        except BaseException:
            failures.append("publisher-readiness")
    if failures:
        fail("publisher manifest rollback failed: " + ",".join(failures))


def create_inference_service(token: str, cleanup: CleanupState, *, name: str, model_version_id: str, image_ref: str, mode: str) -> dict[str, Any]:
    if not IMAGE_DIGEST_RE.fullmatch(image_ref):
        fail("C41 image_ref must be digest-pinned with @sha256:<64hex>")
    body = {
        "idempotency_key": str(uuid.uuid4()), "name": name, "model": model_version_id,
        "model_version_id": model_version_id, "served_model_name": name, "replicas": 1,
        "image_ref": image_ref, "placement_mode": "auto",
    }
    status, result = control_request("POST", "/svc/inference-services", token, body)
    if status != 202:
        fail(f"create {mode} inference service returned HTTP {status}")
    service_id = result.get("id") or result.get("service_id")
    if not isinstance(service_id, str) or not service_id:
        fail("created inference service has no id")
    cleanup.register_service(token, service_id)
    return result


def wait_for_running(token: str, service_id: str, expected_url: str) -> dict[str, Any]:
    deadline = time.monotonic() + 300
    while time.monotonic() < deadline:
        status, service = control_request("GET", f"/svc/inference-services/{service_id}", token)
        if status == 200 and service.get("status") == "running" and service.get("invocation_url") == expected_url:
            return service
        time.sleep(2)
    fail("inference service did not become running at its expected public invocation URL")
    return {}


def wait_for_unpublished_then_stopped(token: str, service_id: str, api_key: str | None = None, tenant_id: str | None = None, timeout: int = 180) -> None:
    deadline = time.monotonic() + timeout
    unpublished = False
    while time.monotonic() < deadline:
        status, service = control_request("GET", f"/svc/inference-services/{service_id}", token)
        if status != 200:
            fail("stopped service disappeared before lifecycle probe completed")
        if not unpublished:
            if service.get("status") == "stopped":
                fail("stop reached stopped before observing unpublished intermediate state")
            gateway_status = _chat(api_key, "ani-c41-shared")[0]
            if gateway_status not in {200, 404}:
                fail("stop publication probe expected HTTP 200 or 404")
            unpublished = service.get("invocation_url") in {None, ""} and service.get("status") != "stopped"
            if unpublished:
                _require_status(gateway_status, 404, "stop-unpublishes-before-workload-stop")
                if not tenant_id:
                    fail("stop runtime-ready probe requires tenant identity")
                selected_vllm_identity(service_id, tenant_id)
        elif service.get("invocation_url") not in {None, ""}:
            fail("stopped service was republished during lifecycle probe")
        if unpublished and service.get("status") == "stopped":
            return
        time.sleep(2)
    fail("stop did not unpublish before reaching stopped")


def wait_for_started(token: str, service_id: str, expected_url: str, api_key: str, tenant_id: str) -> None:
    before = selected_vllm_counter(service_id, tenant_id)
    wait_for_running(token, service_id, expected_url)
    _require_status(_chat(api_key, "ani-c41-shared")[0], 200, "start-republishes-200")
    if selected_vllm_counter(service_id, tenant_id) <= before:
        fail("start did not reach the selected vLLM target")


def policy_precedence_evidence(specific_allow_200: bool, lower_tenant_deny_403: bool) -> dict[str, bool]:
    if not specific_allow_200 or not lower_tenant_deny_403:
        fail("policy precedence probes did not complete")
    return {"specific_allow_200": True, "lower_tenant_deny_403": True}


def probe_policy_precedence(
    access_token: str, cleanup: CleanupState, api_key: str, api_key_id: str,
    service_id: str, tenant_id: str, other_service_id: str, other_tenant_id: str,
) -> dict[str, bool]:
    specific_id = create_policy(
        access_token, cleanup, name="ani-c41-service-key-" + uuid.uuid4().hex[:8], priority=2000,
        scope={"type": "inference_service_api_key", "inference_service_ids": [service_id], "api_key_ids": [api_key_id]},
        access={"allow_all_tenant_keys": False, "allow_api_key_ids": [api_key_id], "deny_api_key_ids": []},
    )
    lower_id = create_policy(
        access_token, cleanup, name="ani-c41-tenant-deny-" + uuid.uuid4().hex[:8], priority=1000,
        scope={"type": "tenant_default"},
        access={"allow_all_tenant_keys": False, "allow_api_key_ids": [], "deny_api_key_ids": [api_key_id]},
    )
    specific_before = selected_vllm_counter(service_id, tenant_id)
    other_before = selected_vllm_counter(other_service_id, other_tenant_id)
    _require_status(_chat(api_key, "ani-c41-shared")[0], 200, "service-ak-policy-overrides-tenant")
    specific_allow = (
        selected_vllm_counter(service_id, tenant_id) > specific_before
        and selected_vllm_counter(other_service_id, other_tenant_id) == other_before
    )
    delete_policy(access_token, specific_id)
    cleanup.policies.remove((access_token, specific_id))
    denied_before = selected_vllm_counter(service_id, tenant_id)
    denied_other = selected_vllm_counter(other_service_id, other_tenant_id)
    _require_status(_chat(api_key, "ani-c41-shared")[0], 403, "service-ak-policy-overrides-tenant")
    lower_deny = (
        selected_vllm_counter(service_id, tenant_id) == denied_before
        and selected_vllm_counter(other_service_id, other_tenant_id) == denied_other
    )
    delete_policy(access_token, lower_id)
    cleanup.policies.remove((access_token, lower_id))
    return policy_precedence_evidence(specific_allow, lower_deny)


def _require_status(actual: int, expected: int, check_id: str) -> None:
    if actual != expected:
        fail(f"{check_id} expected HTTP {expected}, got {actual}")


def _chat(api_key: str | None, model: str, *, stream: bool = False, extra_headers: dict[str, str] | None = None) -> tuple[int, str]:
    headers = {"Accept": "text/event-stream" if stream else "application/json"}
    if api_key is not None:
        headers["Authorization"] = "Bearer " + api_key
    headers.update(extra_headers or {})
    return gateway_request("/v1/chat/completions", method="POST", headers=headers, body={"model": model, "messages": [{"role": "user", "content": "C41 in-memory probe"}], "stream": stream, "max_tokens": 8, "temperature": 0})


def _embeddings(api_key: str | None, model: str) -> tuple[int, str]:
    headers = {"Accept": "application/json"}
    if api_key is not None:
        headers["Authorization"] = "Bearer " + api_key
    return gateway_request("/v1/embeddings", method="POST", headers=headers, body={"model": model, "input": "C41 in-memory probe"})


def assert_invalid_credentials_401(login_jwt: str, revoked_key: str) -> None:
    for credential in (None, "random", login_jwt, revoked_key):
        _require_status(_chat(credential, "ani-c41-shared")[0], 401, "invalid-credentials-401")


def assert_spoof_ignored(api_key: str, model: str, expected_service_id: str, expected_tenant_id: str, other_service_id: str, other_tenant_id: str) -> None:
    headers = {
        "x-ani-inference-service-id": "spoof",
        "x-ani-tenant-id": "spoof",
        "x-ani-user-id": "spoof",
    }
    assert_selected_route(
        api_key, model, expected_service_id, expected_tenant_id,
        other_service_id, other_tenant_id, "tenant-service-spoof-ignored", headers,
    )


def _chat_json_evidence(raw: str) -> dict[str, Any]:
    try:
        value = json.loads(raw)
    except json.JSONDecodeError:
        fail("chat response was not JSON")
    if not isinstance(value, dict) or not value.get("choices"):
        fail("chat response has no choices")
    return {"chat_choices_nonempty": True}


def _embedding_evidence(raw: str) -> dict[str, Any]:
    try:
        value = json.loads(raw)
    except json.JSONDecodeError:
        fail("embedding response was not JSON")
    data = value.get("data") if isinstance(value, dict) else None
    vector = data[0].get("embedding") if isinstance(data, list) and data and isinstance(data[0], dict) else None
    if not isinstance(vector, list) or not vector or not all(isinstance(item, (int, float)) and not isinstance(item, bool) for item in vector):
        fail("embedding response has no numeric vector")
    return {"embedding_vector_length_gt_zero": True}


def assert_no_managed_ak_secret() -> None:
    # Metadata-only name output: intentionally no ``-o json`` and no Secret .data access.
    names = kubectl(["-n", GATEWAY_NAMESPACE, "get", "secrets", "-l", MANAGED_BY_LABEL, "-o", "name"])
    if names.strip():
        fail("C41 must not create a publisher-managed API key Secret")


def assert_logs_redacted(api_keys: list[str], services: list[tuple[str, str]] | None = None) -> None:
    material = {item for key in api_keys for item in (key, key[:20]) if item}
    commands = [
        ["-n", GATEWAY_NAMESPACE, "logs", "-l", "app.kubernetes.io/name=envoy-authz-adapter", "--all-containers=true", "--tail=500"],
        ["-n", "envoy-gateway-system", "logs", "-l", "gateway.envoyproxy.io/owning-gateway-name=ani-aigw", "--all-containers=true", "--tail=500"],
        ["-n", PUBLISHER_NAMESPACE, "logs", f"deployment/{PUBLISHER_DEPLOYMENT}", "--tail=500"],
        ["-n", SYSTEM_NAMESPACE, "logs", "deployment/inference-service", "--tail=500"],
    ]
    for output in (kubectl(command) for command in commands):
        if any(value in output for value in material):
            fail("temporary API key material appeared in a selected log")
    for service_id, tenant_id in services or []:
        if any(value in output for output in selected_vllm_logs(service_id, tenant_id) for value in material):
            fail("temporary API key material appeared in selected vLLM logs")


def resolve_vllm_targets(endpoint_slices: dict[str, Any], pods: dict[str, dict[str, Any]]) -> list[tuple[str, str]]:
    targets: list[tuple[str, str]] = []
    for item in endpoint_slices.get("items") or []:
        ports = {entry.get("port") for entry in item.get("ports") or [] if isinstance(entry, dict)} if isinstance(item, dict) else set()
        if 8000 not in ports:
            continue
        for endpoint in item.get("endpoints") or []:
            ref = endpoint.get("targetRef") or {} if isinstance(endpoint, dict) else {}
            name = ref.get("name") if endpoint.get("conditions", {}).get("ready") is True and ref.get("kind") == "Pod" else None
            pod = pods.get(name) if isinstance(name, str) else None
            containers = ((pod.get("spec") or {}).get("containers") or []) if isinstance(pod, dict) else []
            matches = [str(c.get("name")) for c in containers if isinstance(c, dict) and (c.get("name") == "vllm" or "vllm" in str(c.get("image") or "").lower())]
            if len(matches) != 1:
                fail("selected vLLM endpoint must resolve exactly one vLLM container")
            targets.append((name, matches[0]))
    unique_targets = set(targets)
    if len(unique_targets) != 1:
        fail("selected vLLM service must resolve exactly one ready target")
    return [next(iter(unique_targets))]


def selected_vllm_logs(service_id: str, tenant_id: str) -> list[str]:
    identity = selected_vllm_identity(service_id, tenant_id)
    return [kubectl(["-n", identity["namespace"], "logs", identity["pod"], "-c", identity["container"], "--tail=500"])]


def selected_vllm_identity(service_id: str, tenant_id: str) -> dict[str, str]:
    services = kubectl_json(["get", "services", "-A", "-l", SERVICE_ID_LABEL + "=" + service_id])
    items = services.get("items") or []
    if len(items) != 1 or not isinstance(items[0], dict): fail("selected vLLM service must be unique")
    metadata = items[0].get("metadata") or {}; labels = metadata.get("labels") or {}
    namespace = metadata.get("namespace")
    if (
        metadata.get("name") != "pw-" + service_id
        or namespace != "ani-tenant-" + tenant_id
        or labels.get(SERVICE_ID_LABEL) != service_id
        or labels.get(OWNER_REF_LABEL) != service_id
        or labels.get(TENANT_ID_LABEL) != tenant_id
    ):
        fail("selected vLLM service identity mismatch")
    slices = kubectl_json(["-n", namespace, "get", "endpointslices", "-l", "kubernetes.io/service-name=pw-" + service_id])
    pod_names = {ep.get("targetRef", {}).get("name") for item in slices.get("items") or [] if isinstance(item, dict) for ep in item.get("endpoints") or [] if isinstance(ep, dict) and ep.get("conditions", {}).get("ready") is True and ep.get("targetRef", {}).get("kind") == "Pod"}
    pods = {pod: kubectl_json(["-n", namespace, "get", "pod", pod]) for pod in pod_names if isinstance(pod, str)}
    pod, container = resolve_vllm_targets(slices, pods)[0]
    selected_pod = pods.get(pod) or {}
    if (selected_pod.get("metadata") or {}).get("namespace") not in {None, namespace}:
        fail("selected vLLM Pod namespace mismatch")
    return {"service_id": service_id, "tenant_id": tenant_id, "namespace": namespace, "pod": pod, "container": container}


def _record_http(checks: dict[str, dict[str, Any]], check_id: str, actual: int, expected: int) -> None:
    _require_status(actual, expected, check_id)
    checks[check_id] = {"status_code": expected}


def apply_lifecycle(token: str, service_id: str, action: str) -> None:
    status, _ = control_request("POST", f"/svc/inference-services/{service_id}/lifecycle", token, {"idempotency_key": str(uuid.uuid4()), "action": action})
    if status != 202:
        fail(f"{action} lifecycle request was not accepted")


def probe_remaining_matrix(
    checks: dict[str, dict[str, Any]], cleanup: CleanupState,
    tenant_a_login_jwt: str, tenant_a_key: str, tenant_a_key_id: str, tenant_b_key: str,
    rpm_key: str, revoked_key: str, shared_a: tuple[str, str, str],
    shared_b: tuple[str, str, str], a_only: tuple[str, str, str],
) -> None:
    """Perform every non-content C41 probe; evidence is written only after each success."""
    a_token, a_id, a_tenant_id = shared_a
    _, b_id, b_tenant_id = shared_b
    assert_selected_route(tenant_a_key, "ani-c41-shared", a_id, a_tenant_id, b_id, b_tenant_id, "tenant-isolation-same-model")
    assert_selected_route(tenant_b_key, "ani-c41-shared", b_id, b_tenant_id, a_id, a_tenant_id, "tenant-isolation-same-model")
    checks["tenant-isolation-same-model"] = {"tenant_a_and_b_selected_counters_isolated": True}
    _record_http(checks, "tenant-b-a-only-404", _chat(tenant_b_key, "ani-c41-a-only")[0], 404)
    _record_http(checks, "generate-embeddings-404", _embeddings(tenant_a_key, "ani-c41-shared")[0], 404)
    _record_http(checks, "embed-chat-404", _chat(tenant_a_key, "ani-c41-embed")[0], 404)
    assert_invalid_credentials_401(tenant_a_login_jwt, revoked_key)
    checks["invalid-credentials-401"] = {"missing_random_login_jwt_revoked": True}
    assert_selected_route(tenant_a_key, "ani-c41-shared", a_id, a_tenant_id, b_id, b_tenant_id, "body-model-wins-over-client-header", {"x-ai-eg-model": "ani-c41-a-only"})
    checks["body-model-wins-over-client-header"] = {"selected_counter_only": True}
    assert_spoof_ignored(tenant_a_key, "ani-c41-shared", a_id, a_tenant_id, b_id, b_tenant_id)
    checks["tenant-service-spoof-ignored"] = {"selected_counter_only": True}
    _record_http(checks, "models-404", gateway_request("/v1/models")[0], 404)
    first, _, = _chat(rpm_key, "ani-c41-shared")
    second, _, headers = gateway_response("/v1/chat/completions", method="POST", headers={"Authorization": "Bearer " + rpm_key}, body={"model": "ani-c41-shared", "messages": [{"role": "user", "content": "C41 in-memory rate probe"}]})
    _require_status(first, 200, "rpm-429-retry-after")
    _require_status(second, 429, "rpm-429-retry-after")
    retry_after = headers.get("retry-after", "")
    if not retry_after.isdigit() or int(retry_after) <= 0:
        fail("rpm-429-retry-after requires a positive Retry-After header")
    checks["rpm-429-retry-after"] = {"first_status_code": 200, "second_status_code": 429, "retry_after_positive": True}
    checks["service-ak-policy-overrides-tenant"] = probe_policy_precedence(
        a_token, cleanup, tenant_a_key, tenant_a_key_id,
        a_id, a_tenant_id, b_id, b_tenant_id,
    )
    apply_lifecycle(a_token, a_id, "stop")
    wait_for_unpublished_then_stopped(a_token, a_id, tenant_a_key, a_tenant_id)
    checks["stop-unpublishes-before-workload-stop"] = {"ak_status_code": 404, "runtime_target_ready_before_stopped": True}
    apply_lifecycle(a_token, a_id, "start")
    wait_for_started(
        a_token, a_id,
        validate_public_url(required_env("ANI_C41_GATEWAY_URL"), "ANI_C41_GATEWAY_URL") + "/v1/chat/completions",
        tenant_a_key, a_tenant_id,
    )
    checks["start-republishes-200"] = {"status_code": 200, "selected_counter_increased": True}


def assert_redacted_evidence(document: dict[str, Any]) -> None:
    def walk(value: Any) -> None:
        if isinstance(value, dict):
            for key, child in value.items():
                if str(key).lower() in FORBIDDEN_EVIDENCE_KEYS:
                    fail("evidence contains forbidden sensitive content")
                walk(child)
        elif isinstance(value, list):
            for child in value:
                walk(child)
        elif isinstance(value, str) and (SENSITIVE_VALUE_RE.search(value) or FORBIDDEN_EVIDENCE_TEXT_RE.search(value)):
            fail("evidence contains sensitive content")
    walk(document)


def write_evidence_atomically(target: Path, redacted_evidence: dict[str, Any]) -> None:
    assert_redacted_evidence(redacted_evidence)
    target.parent.mkdir(parents=True, exist_ok=True)
    fd, temp_name = tempfile.mkstemp(prefix=target.name + ".", dir=target.parent)
    try:
        os.fchmod(fd, 0o600)
        with os.fdopen(fd, "w", encoding="utf-8") as handle:
            json.dump(redacted_evidence, handle, ensure_ascii=False, indent=2, sort_keys=True)
            handle.write("\n")
        os.replace(temp_name, target)
        os.chmod(target, 0o600)
    finally:
        if os.path.exists(temp_name):
            os.unlink(temp_name)


def run_live() -> dict[str, Any]:
    """Execute only after the caller explicitly supplies all ephemeral inputs."""
    for name in REQUIRED_ENV:
        required_env(name)
    control_url = validate_control_plane_url(required_env("ANI_C41_CONTROL_PLANE_URL"))
    gateway_url = validate_public_url(required_env("ANI_C41_GATEWAY_URL"), "ANI_C41_GATEWAY_URL")
    del control_url  # validation-only; requests revalidate immediately before use.
    tenant_a, tenant_b = required_env("ANI_C41_TENANT_A_ACCESS_TOKEN"), required_env("ANI_C41_TENANT_B_ACCESS_TOKEN")
    tenant_a_id, tenant_b_id = tenant_id_from_login_jwt(tenant_a), tenant_id_from_login_jwt(tenant_b)
    if tenant_a_id == tenant_b_id:
        fail("C41 requires two different tenant UUID identities")
    cleanup, keys, checks = CleanupState(), [], {}
    snapshot: PublisherSnapshot | None = None
    primary_error: BaseException | None = None
    cleanup_error: BaseException | None = None
    publisher_restore_error: BaseException | None = None
    try:
        validate_task8_server_dry_run()
        snapshot = snapshot_publisher()
        apply_publisher(gateway_url, snapshot)
        for token, label, rpm in ((tenant_a, "tenant-a", 60), (tenant_b, "tenant-b", 60), (tenant_a, "rpm", 1), (tenant_a, "revoked", 60)):
            key_id, key = create_registered_api_key(token, cleanup.api_keys, name="ani-c41-" + label + "-" + uuid.uuid4().hex, rpm=rpm)
            del key_id
            keys.append(key)
        tenant_a_key, tenant_b_key, rpm_key, revoked_key = keys
        tenant_a_key_id = cleanup.api_keys[0][1]
        revoke_api_key(tenant_a, cleanup.api_keys[-1][1])
        cleanup.api_keys.pop()
        chat_version, embed_version = required_env("ANI_C41_CHAT_MODEL_VERSION_ID"), required_env("ANI_C41_EMBED_MODEL_VERSION_ID")
        chat_image, embed_image = required_env("ANI_C41_CHAT_IMAGE_REF"), required_env("ANI_C41_EMBED_IMAGE_REF")
        services = [
            (tenant_a, tenant_a_id, "ani-c41-shared", chat_version, chat_image, "generate"),
            (tenant_b, tenant_b_id, "ani-c41-shared", chat_version, chat_image, "generate"),
            (tenant_a, tenant_a_id, "ani-c41-embed", embed_version, embed_image, "embed"),
            (tenant_a, tenant_a_id, "ani-c41-a-only", chat_version, chat_image, "generate"),
        ]
        created = [create_inference_service(token, cleanup, name=name, model_version_id=model, image_ref=image, mode=mode) for token, _, name, model, image, mode in services]
        for (token, _, _, _, _, mode), service in zip(services, created):
            service_id = service.get("id") or service.get("service_id")
            if not isinstance(service_id, str):
                fail("created service has no id")
            wait_for_running(token, service_id, gateway_url + ("/v1/embeddings" if mode == "embed" else "/v1/chat/completions"))
        status, raw = _chat(tenant_a_key, "ani-c41-shared")
        _require_status(status, 200, "tenant-a-chat-json-200"); checks["tenant-a-chat-json-200"] = _chat_json_evidence(raw)
        status, raw = _chat(tenant_a_key, "ani-c41-shared", stream=True)
        _require_status(status, 200, "tenant-a-chat-sse-200")
        if "data:" not in raw or "[DONE]" not in raw: fail("tenant-a-chat-sse-200 requires SSE frames and DONE")
        checks["tenant-a-chat-sse-200"] = {"sse_data_frames": True, "sse_done": True}
        status, raw = _embeddings(tenant_a_key, "ani-c41-embed")
        _require_status(status, 200, "tenant-a-embeddings-200"); checks["tenant-a-embeddings-200"] = _embedding_evidence(raw)
        ids = [str(item.get("id") or item.get("service_id")) for item in created]
        probe_remaining_matrix(
            checks, cleanup, tenant_a, tenant_a_key, tenant_a_key_id, tenant_b_key, rpm_key, revoked_key,
            (tenant_a, ids[0], tenant_a_id), (tenant_b, ids[1], tenant_b_id), (tenant_a, ids[3], tenant_a_id),
        )
        run_fault_injections(tenant_a_key, ids[0], tenant_a_id)
        checks["dependency-faults-503-no-vllm"] = {"all_dependency_faults_503": True, "target_vllm_counter_unchanged": True, "restored_between_faults": True}
        old_a_only = (tenant_a, ids[3])
        delete_inference_service_for_reuse(*old_a_only)
        cleanup.services.remove(old_a_only)
        reused = create_inference_service(tenant_a, cleanup, name="ani-c41-a-only", model_version_id=chat_version, image_ref=chat_image, mode="generate")
        reused_id = str(reused.get("id") or reused.get("service_id"))
        wait_for_running(tenant_a, reused_id, gateway_url + "/v1/chat/completions")
        checks["delete-releases-same-tenant-name"] = {"owner_resources_removed": True, "same_tenant_name_reused": True}
        ids[3] = reused_id
        publication_targets = [
            (tenant_a, ids[0], tenant_a_id), (tenant_b, ids[1], tenant_b_id),
            (tenant_a, ids[2], tenant_a_id), (tenant_a, ids[3], tenant_a_id),
        ]
        verify_publisher_reconcile(publication_targets)
        checks["publisher-restart-idempotent"] = {"current_generation_owner_resources_exactly_once": True}
        assert_no_managed_ak_secret(); checks["no-managed-ak-secret"] = {"secret_metadata_clear": True}
        assert_logs_redacted(keys, [(service_id, tenant_id) for _, service_id, tenant_id in publication_targets])
        checks["temporary-key-log-redaction"] = {"temporary_key_log_scan_clear": True}
    except BaseException as error:
        primary_error = error
    finally:
        try:
            cleanup_or_fail(cleanup)
        except BaseException as error:
            cleanup_error = error
        try:
            if snapshot is not None and snapshot.mutation_started:
                restore_publisher(snapshot)
        except BaseException as error:
            publisher_restore_error = error
    if cleanup_error is None and publisher_restore_error is None:
        checks["cleanup-complete"] = {"runner_owned_resources_removed": True, "publisher_restored": True}
    combined = aggregate_failures(primary_error, cleanup_error, publisher_restore_error)
    if combined is not None:
        raise combined
    if set(checks) != REQUIRED_CHECKS:
        fail("runner did not execute every required C41 check")
    evidence = {"profile": PROFILE, "status": "passed", "checks": [{"id": key, **value} for key, value in sorted(checks.items())]}
    write_evidence_atomically(EVIDENCE, evidence)
    return evidence


def main() -> None:
    run_live()
    print("C41 inference Envoy AI Gateway live gate passed; redacted evidence written")


if __name__ == "__main__":
    main()
