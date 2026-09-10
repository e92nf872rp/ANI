#!/usr/bin/env python3
"""Run the approved, isolated OBS-RUNTIME-P0 L3 Kubernetes gate."""

from __future__ import annotations

import argparse
import base64
import hashlib
import json
import os
import secrets
import select
import shlex
import shutil
import socket
import subprocess
import sys
import tempfile
import time
import urllib.error
import urllib.parse
import urllib.request
import uuid
from contextlib import contextmanager
from dataclasses import dataclass
from pathlib import Path
from typing import Any, Iterator

import yaml

import render_service_runtime_observability as workload_renderer
import render_service_runtime_observability_l3 as fixture_renderer


ROOT = Path(__file__).resolve().parents[1]
SOURCE_COMMIT = "0cedae825a489d936cf41815dc27f278f6d3213c"
RUNTIME_SECRET = fixture_renderer.RUNTIME_SECRET
IMAGE_PULL_SECRET = "ani-runtime-observability-registry"
SCRAPE_FAULT_POLICY = "ani-service-observability-ingress"
FAULT_SERVICE = "auth-service"
MISSING_SERVICE = "model-service"
SERVICES = {
    "ani-gateway",
    "auth-service",
    "model-service",
    "task-service",
    "inference-service",
    "tenant-service",
    "metering-service",
}
EXCLUDED_SERVICES = {"reconcile-worker", "envoy-authz-adapter", "kb-service"}
SSH_HOST_RE = workload_renderer.DNS_LABEL_RE
REQUIRED_RUNTIME_SECRET_KEYS = {
    "database_url",
    "nats_url",
    "redis_url",
    "auth_jwt_public_key_pem",
    "auth_jwt_private_key_pem",
    "auth_oidc_client_secret",
    "auth_service_mint_credentials",
    "core_service_token",
    "auth_service_mint_secret",
    "core_api_token",
    "postgres_password",
    "redis_password",
}
_LOOPBACK_HTTP_OPENER = urllib.request.build_opener(urllib.request.ProxyHandler({}))


@dataclass(frozen=True)
class LiveConfig:
    kubectl_binary: str = "kubectl"
    kubeconfig: Path = Path("")
    ssh_host: str = ""
    ssh_config: Path | None = None
    context: str = ""
    expected_server: str = ""
    namespace: str = ""
    version: str = ""
    images_file: Path = Path("")
    confirmation: str = ""
    image_pull_secret_source: str = ""
    evidence_output: Path = Path("")
    source_commit: str = SOURCE_COMMIT
    wait_timeout: str = "300s"


def fixture_manifest_sha256(
    namespace: str,
    version: str,
    images: dict[str, str],
    image_pull_secret_source: str,
) -> str:
    manifest = fixture_renderer.render_l3_fixture(
        namespace=namespace,
        version=version,
        images=images,
        image_pull_secret=IMAGE_PULL_SECRET if image_pull_secret_source else None,
    )
    return "sha256:" + hashlib.sha256(manifest.encode("utf-8")).hexdigest()


def confirmation_value(
    context: str,
    server: str,
    namespace: str,
    version: str,
    images: dict[str, str],
    image_pull_secret_source: str,
) -> str:
    image_payload = json.dumps(images, separators=(",", ":"), sort_keys=True)
    fixture_image_payload = json.dumps(
        fixture_renderer.fixture_images(), separators=(",", ":"), sort_keys=True
    )
    object_plan_payload = json.dumps(
        object_plan(namespace, include_pull_secret=bool(image_pull_secret_source)),
        separators=(",", ":"),
        sort_keys=True,
    )
    manifest_digest = fixture_manifest_sha256(
        namespace,
        version,
        images,
        image_pull_secret_source,
    )
    payload = (
        f"{context}\n{server}\n{namespace}\n{version}\n{image_pull_secret_source}\n"
        f"{image_payload}\n{fixture_image_payload}\n{manifest_digest}\n{object_plan_payload}\n"
        f"H4+IMAGE_PUBLISH\n{SOURCE_COMMIT}"
    )
    return "h4-image-publish-" + hashlib.sha256(payload.encode("utf-8")).hexdigest()[:24]


def validate_config_values(
    config: LiveConfig,
    *,
    check_files: bool = True,
    require_confirmation: bool = True,
) -> list[str]:
    errors: list[str] = []
    if not config.context.strip():
        errors.append("context is required")
    parsed_server = urllib.parse.urlsplit(config.expected_server)
    if parsed_server.scheme not in {"https", "http"} or not parsed_server.netloc:
        errors.append("expected server must be an absolute HTTP(S) URL")
    if not workload_renderer.NAMESPACE_RE.fullmatch(config.namespace):
        errors.append("namespace must be an exact ani-service-observability-e2e-<run-id> name")
    if not workload_renderer.VERSION_RE.fullmatch(config.version) or config.version in {"latest", "(devel)"}:
        errors.append("version must be concrete")
    if config.source_commit != SOURCE_COMMIT:
        errors.append(f"source commit must remain {SOURCE_COMMIT}")
    if config.image_pull_secret_source:
        parts = config.image_pull_secret_source.split("/", 1)
        if len(parts) != 2 or not all(workload_renderer.DNS_LABEL_RE.fullmatch(part) for part in parts):
            errors.append("image pull secret source must be <namespace>/<name>")
    if config.ssh_host:
        if not SSH_HOST_RE.fullmatch(config.ssh_host):
            errors.append("SSH host must be a safe configured host alias")
        if not Path(config.kubectl_binary).is_absolute():
            errors.append("remote kubectl binary must be an absolute path")
        if not config.kubeconfig.is_absolute():
            errors.append("remote kubeconfig must be an absolute path")
    elif config.ssh_config is not None:
        errors.append("SSH config requires an SSH host")
    if check_files:
        if config.ssh_host:
            if shutil.which("ssh") is None:
                errors.append("ssh binary is unavailable")
            if config.ssh_config is None or not config.ssh_config.is_file():
                errors.append(f"SSH config does not exist: {config.ssh_config}")
        elif not config.kubeconfig.is_file():
            errors.append(f"kubeconfig does not exist: {config.kubeconfig}")
        if not config.images_file.is_file():
            errors.append(f"images file does not exist: {config.images_file}")
        if not config.evidence_output:
            errors.append("evidence output is required")
        elif config.evidence_output.exists() and config.evidence_output.is_dir():
            errors.append("evidence output must be a file")
        if config.ssh_host:
            pass
        elif os.path.sep in config.kubectl_binary:
            if not Path(config.kubectl_binary).is_file():
                errors.append(f"kubectl binary does not exist: {config.kubectl_binary}")
        elif shutil.which(config.kubectl_binary) is None:
            errors.append(f"kubectl binary is unavailable: {config.kubectl_binary}")
    images: dict[str, str] | None = None
    if config.images_file.is_file():
        try:
            images = workload_renderer.load_images(config.images_file, SERVICES)
        except (OSError, TypeError, ValueError, json.JSONDecodeError) as exc:
            errors.append(f"images file is invalid: {exc}")
    if images is not None:
        expected_confirmation = confirmation_value(
            config.context,
            config.expected_server,
            config.namespace,
            config.version,
            images,
            config.image_pull_secret_source,
        )
        if require_confirmation and config.confirmation != expected_confirmation:
            errors.append(f"confirmation must equal {expected_confirmation}")
    return errors


def _b64url(value: bytes) -> str:
    return base64.urlsafe_b64encode(value).rstrip(b"=").decode("ascii")


def _sign_jwt(private_key: Path, claims: dict[str, Any]) -> str:
    header = {"alg": "RS256", "typ": "JWT", "kid": ""}
    segments = [
        _b64url(json.dumps(header, separators=(",", ":"), sort_keys=True).encode("utf-8")),
        _b64url(json.dumps(claims, separators=(",", ":"), sort_keys=True).encode("utf-8")),
    ]
    signing_input = ".".join(segments).encode("ascii")
    completed = subprocess.run(
        ["openssl", "dgst", "-sha256", "-sign", str(private_key)],
        input=signing_input,
        capture_output=True,
        check=False,
    )
    if completed.returncode != 0:
        raise RuntimeError("failed to sign ephemeral L3 credential")
    return ".".join([*segments, _b64url(completed.stdout)])


def _service_token(
    private_key: Path,
    *,
    now: int,
    subject: str,
    permissions: list[str],
) -> str:
    claims = {
        "sub": subject,
        "iss": "auth-service",
        "aud": "ani-core",
        "exp": now + 3600,
        "nbf": now - 5,
        "iat": now,
        "jti": str(uuid.uuid4()),
        "principal_kind": "service",
        "credential_domain": "platform",
        "permissions": permissions,
        "tid": "",
        "uid": "00000000-0000-0000-0000-0000000000aa",
        "scope": permissions[0],
        "roles": ["service"],
    }
    return _sign_jwt(private_key, claims)


def _tenant_token(private_key: Path, *, now: int) -> str:
    tenant_id = str(uuid.uuid4())
    user_id = str(uuid.uuid4())
    return _sign_jwt(
        private_key,
        {
            "sub": user_id,
            "iss": "auth-service",
            "exp": now + 3600,
            "nbf": now - 5,
            "iat": now,
            "jti": str(uuid.uuid4()),
            "principal_kind": "user",
            "credential_domain": "tenant",
            "tid": tenant_id,
            "uid": user_id,
            "scope": "tenant",
            "roles": ["tenant-admin"],
        },
    )


def build_runtime_secret(
    namespace: str, *, now: int | None = None
) -> tuple[dict[str, Any], str, str]:
    if shutil.which("openssl") is None:
        raise RuntimeError("openssl is required to create ephemeral L3 credentials")
    now = int(time.time()) if now is None else now
    postgres_password = secrets.token_urlsafe(24)
    redis_password = secrets.token_urlsafe(24)
    mint_secret = secrets.token_urlsafe(24)
    with tempfile.TemporaryDirectory(prefix="ani-obs-runtime-l3-") as temp_dir:
        private_key = Path(temp_dir) / "jwt-private.pem"
        generated = subprocess.run(
            [
                "openssl",
                "genpkey",
                "-algorithm",
                "RSA",
                "-pkeyopt",
                "rsa_keygen_bits:2048",
                "-out",
                str(private_key),
            ],
            capture_output=True,
            check=False,
        )
        if generated.returncode != 0:
            raise RuntimeError("failed to generate ephemeral L3 RSA key")
        public = subprocess.run(
            ["openssl", "pkey", "-in", str(private_key), "-pubout"],
            capture_output=True,
            check=False,
        )
        if public.returncode != 0:
            raise RuntimeError("failed to derive ephemeral L3 RSA public key")
        private_pem = private_key.read_text(encoding="utf-8")
        public_pem = public.stdout.decode("utf-8")
        observability_token = _service_token(
            private_key,
            now=now,
            subject="obs-runtime-l3",
            permissions=["scope:observability:read"],
        )
        core_token = _service_token(
            private_key,
            now=now,
            subject="inference-service",
            permissions=["scope:platform-workloads:write"],
        )
        tenant_token = _tenant_token(private_key, now=now)

    quoted_postgres = urllib.parse.quote(postgres_password, safe="")
    quoted_redis = urllib.parse.quote(redis_password, safe="")
    string_data = {
        "database_url": (
            f"postgres://ani:{quoted_postgres}@ani-service-observability-postgres:5432/ani?sslmode=disable"
        ),
        "nats_url": "nats://ani-service-observability-nats:4222",
        "redis_url": f"redis://:{quoted_redis}@ani-service-observability-redis:6379/0",
        "auth_jwt_public_key_pem": public_pem,
        "auth_jwt_private_key_pem": private_pem,
        "auth_oidc_client_secret": secrets.token_urlsafe(24),
        "auth_service_mint_credentials": f"inference-service:{mint_secret}",
        "core_service_token": core_token,
        "auth_service_mint_secret": mint_secret,
        "core_api_token": core_token,
        "postgres_password": postgres_password,
        "redis_password": redis_password,
    }
    if not REQUIRED_RUNTIME_SECRET_KEYS <= set(string_data):
        raise RuntimeError("ephemeral runtime secret is incomplete")
    secret = {
        "apiVersion": "v1",
        "kind": "Secret",
        "metadata": {
            "name": RUNTIME_SECRET,
            "namespace": namespace,
            "labels": {"ani.dev/profile": fixture_renderer.PROFILE},
        },
        "type": "Opaque",
        "stringData": string_data,
    }
    return secret, observability_token, tenant_token


def secret_safe_summary(secret: dict[str, Any]) -> dict[str, Any]:
    return {
        "kind": secret.get("kind"),
        "namespace": (secret.get("metadata") or {}).get("namespace"),
        "name": (secret.get("metadata") or {}).get("name"),
        "keys": sorted((secret.get("stringData") or {}).keys()),
    }


def scrape_fault_network_policy(namespace: str) -> dict[str, Any]:
    run_id = namespace.removeprefix(workload_renderer.NAMESPACE_PREFIX)
    return {
        "apiVersion": "networking.k8s.io/v1",
        "kind": "NetworkPolicy",
        "metadata": {
            "name": SCRAPE_FAULT_POLICY,
            "namespace": namespace,
            "labels": {
                "ani.dev/profile": fixture_renderer.PROFILE,
                "ani.dev/run-id": run_id,
            },
        },
        "spec": {
            "podSelector": {
                "matchLabels": {
                    "ani.dev/profile": fixture_renderer.PROFILE,
                    "ani.dev/run-id": run_id,
                    "ani.dev/service-name": FAULT_SERVICE,
                }
            },
            "policyTypes": ["Ingress"],
            "ingress": [
                {
                    "from": [{"podSelector": {}}],
                    "ports": [{"protocol": "TCP", "port": 9101}],
                }
            ],
        },
    }


def parse_up_vector(payload: dict[str, Any]) -> dict[str, int]:
    if payload.get("status") != "success":
        raise ValueError("Prometheus query was not successful")
    data = payload.get("data")
    if not isinstance(data, dict) or data.get("resultType") != "vector":
        raise ValueError("Prometheus response must be a vector")
    result = data.get("result")
    if not isinstance(result, list):
        raise ValueError("Prometheus vector result must be a list")
    values: dict[str, int] = {}
    for sample in result:
        metric = sample.get("metric") if isinstance(sample, dict) else None
        value = sample.get("value") if isinstance(sample, dict) else None
        name = metric.get("ani_service_name") if isinstance(metric, dict) else None
        if name not in SERVICES:
            raise ValueError(f"unexpected ani_service_name: {name}")
        if name in values:
            raise ValueError(f"duplicate Prometheus sample for {name}")
        if not isinstance(value, list) or len(value) != 2 or str(value[1]) not in {"0", "1"}:
            raise ValueError(f"invalid up sample for {name}")
        values[name] = int(value[1])
    return values


def object_plan(namespace: str, *, include_pull_secret: bool) -> dict[str, Any]:
    namespaced = lambda kind, name: f"{kind}/{namespace}/{name}"
    create = [
        f"Namespace/{namespace}",
        namespaced("ConfigMap", "ani-service-observability-postgres-init"),
    ]
    for dependency in ("postgres", "nats", "redis"):
        name = f"ani-service-observability-{dependency}"
        create.extend([namespaced("Deployment", name), namespaced("Service", name)])
    create.extend(
        [
            namespaced("ServiceAccount", fixture_renderer.PROMETHEUS_NAME),
            namespaced("Role", fixture_renderer.PROMETHEUS_NAME),
            namespaced("RoleBinding", fixture_renderer.PROMETHEUS_NAME),
            namespaced("ConfigMap", fixture_renderer.PROMETHEUS_NAME),
            namespaced("Deployment", fixture_renderer.PROMETHEUS_NAME),
            namespaced("Service", fixture_renderer.PROMETHEUS_NAME),
            namespaced("NetworkPolicy", SCRAPE_FAULT_POLICY),
        ]
    )
    for name in sorted(SERVICES):
        create.extend([namespaced("Deployment", name), namespaced("Service", name)])
    create.append(namespaced("Secret", RUNTIME_SECRET))
    if include_pull_secret:
        create.append(namespaced("Secret", IMAGE_PULL_SECRET))
    selector = (
        f"ani.dev/run-id={namespace.removeprefix(workload_renderer.NAMESPACE_PREFIX)},"
        f"ani.dev/service-name={FAULT_SERVICE}"
    )
    return {
        "create": create,
        "modify_existing": [],
        "temporary": [
            namespaced("NetworkPolicy", SCRAPE_FAULT_POLICY),
            f"Pod/{namespace} selected by {selector}; delete one resolved name and UID",
            namespaced("Deployment", MISSING_SERVICE) + " replicas 1->0->1",
            namespaced("Deployment", fixture_renderer.PROMETHEUS_NAME) + " replicas 1->0->1",
        ],
        "cleanup": [f"Namespace/{namespace}"],
        "pod_delete_target": {
            "namespace": namespace,
            "label_selector": selector,
            "cardinality": "exactly_one",
        },
    }


def cleanup_command(config: LiveConfig) -> str:
    return shlex.join(
        kubectl_command(
            config,
            [
                "delete",
                "namespace",
                config.namespace,
                "--wait=true",
                f"--timeout={config.wait_timeout}",
            ],
        )
    )


def approval_plan(config: LiveConfig, images: dict[str, str]) -> dict[str, Any]:
    return {
        "context": config.context,
        "server": config.expected_server,
        "kubeconfig": str(config.kubeconfig),
        "execution": {
            "transport": "ssh" if config.ssh_host else "local",
            "ssh_host": config.ssh_host or None,
            "ssh_config": str(config.ssh_config) if config.ssh_config is not None else None,
            "kubectl_binary": config.kubectl_binary,
        },
        "namespace": config.namespace,
        "version": config.version,
        "source_commit": config.source_commit,
        "prometheus_image": fixture_renderer.PROMETHEUS_IMAGE,
        "fixture_images": dict(sorted(fixture_renderer.fixture_images().items())),
        "fixture_manifest_sha256": fixture_manifest_sha256(
            config.namespace,
            config.version,
            images,
            config.image_pull_secret_source,
        ),
        "images": dict(sorted(images.items())),
        "image_pull_secret_source": config.image_pull_secret_source or None,
        "configuration_loading": {
            "inventory": str(fixture_renderer.INVENTORY),
            "runtime_secret": "ephemeral values applied from stdin and never persisted",
            "image_pull_secret": (
                f"copy {config.image_pull_secret_source} data in memory to {IMAGE_PULL_SECRET}"
                if config.image_pull_secret_source
                else "none"
            ),
        },
        "baseline": "target namespace must not exist",
        "object_plan": object_plan(
            config.namespace,
            include_pull_secret=bool(config.image_pull_secret_source),
        ),
        "cleanup_command": cleanup_command(config),
        "required_confirmation": confirmation_value(
            config.context,
            config.expected_server,
            config.namespace,
            config.version,
            images,
            config.image_pull_secret_source,
        ),
    }


def kubectl_command(config: LiveConfig, args: list[str]) -> list[str]:
    remote_command = [
        config.kubectl_binary,
        "--kubeconfig",
        str(config.kubeconfig),
        "--context",
        config.context,
        *args,
    ]
    if not config.ssh_host:
        return remote_command
    if config.ssh_config is None:
        raise ValueError("SSH config is required for SSH transport")
    return [
        "ssh",
        "-F",
        str(config.ssh_config),
        "-o",
        "BatchMode=yes",
        "-o",
        "ConnectTimeout=10",
        "--",
        config.ssh_host,
        shlex.join(remote_command),
    ]


class Kubectl:
    def __init__(self, config: LiveConfig) -> None:
        self.config = config

    def command(self, args: list[str]) -> list[str]:
        return kubectl_command(self.config, args)

    def result(self, args: list[str], *, input_text: str | None = None) -> subprocess.CompletedProcess[str]:
        return subprocess.run(
            self.command(args),
            input=input_text,
            text=True,
            capture_output=True,
            check=False,
        )

    def run(
        self,
        args: list[str],
        *,
        input_text: str | None = None,
        sensitive: bool = False,
    ) -> str:
        completed = self.result(args, input_text=input_text)
        if completed.returncode != 0:
            if sensitive:
                raise RuntimeError("kubectl operation with sensitive input failed")
            detail = completed.stderr.strip() or completed.stdout.strip()
            raise RuntimeError(f"kubectl {' '.join(args)} failed: {detail}")
        return completed.stdout

    def json(self, args: list[str]) -> dict[str, Any]:
        output = self.run([*args, "-o", "json"])
        value = json.loads(output)
        if not isinstance(value, dict):
            raise RuntimeError("kubectl JSON output must be an object")
        return value

    def apply(self, document_or_yaml: dict[str, Any] | str, *, sensitive: bool = False) -> None:
        payload = (
            yaml.safe_dump(document_or_yaml, sort_keys=False)
            if isinstance(document_or_yaml, dict)
            else document_or_yaml
        )
        self.run(["apply", "-f", "-"], input_text=payload, sensitive=sensitive)


def _cluster_identity(kubectl: Kubectl) -> tuple[str, str]:
    config = kubectl.json(["config", "view", "--minify"])
    current = str(config.get("current-context") or "")
    clusters = config.get("clusters") or []
    if len(clusters) != 1:
        raise RuntimeError("minified kubeconfig must contain exactly one cluster")
    server = str(((clusters[0] or {}).get("cluster") or {}).get("server") or "")
    return current, server


def verify_cluster_permissions(
    kubectl: Kubectl,
    *,
    namespace: str,
    image_pull_secret_source: str,
) -> dict[str, Any]:
    """Prove every RBAC capability used by the live gate before mutation."""
    checks: list[tuple[str, str, str]] = [
        ("get", "namespaces", ""),
        ("create", "namespaces", ""),
        ("delete", "namespaces", ""),
    ]
    for resource in (
        "configmaps",
        "secrets",
        "serviceaccounts",
        "services",
        "deployments.apps",
        "roles.rbac.authorization.k8s.io",
        "rolebindings.rbac.authorization.k8s.io",
        "networkpolicies.networking.k8s.io",
    ):
        checks.append(("create", resource, namespace))
    checks.extend(
        [
            ("get", "deployments.apps", namespace),
            ("watch", "deployments.apps", namespace),
            ("patch", "deployments.apps", namespace),
            ("get", "pods", namespace),
            ("list", "pods", namespace),
            ("delete", "pods", namespace),
            ("create", "pods/portforward", namespace),
            ("get", "services", namespace),
            ("patch", "networkpolicies.networking.k8s.io", namespace),
        ]
    )
    if image_pull_secret_source:
        source_namespace, _ = image_pull_secret_source.split("/", 1)
        checks.append(("get", "secrets", source_namespace))

    denied: list[str] = []
    for verb, resource, check_namespace in checks:
        args = ["auth", "can-i", verb, resource]
        if check_namespace:
            args.extend(["--namespace", check_namespace])
        completed = kubectl.result(args)
        if completed.returncode != 0 or completed.stdout.strip().lower() != "yes":
            scope = f" in namespace {check_namespace}" if check_namespace else " cluster-wide"
            denied.append(f"{verb} {resource}{scope}")
    if denied:
        raise RuntimeError("L3 RBAC preflight denied: " + "; ".join(denied))
    return {
        "status": "passed",
        "checks": len(checks),
        "source_secret_read": bool(image_pull_secret_source),
    }


def _copy_pull_secret(kubectl: Kubectl, source: str, namespace: str) -> dict[str, Any]:
    source_namespace, source_name = source.split("/", 1)
    secret = kubectl.json(["-n", source_namespace, "get", "secret", source_name])
    if secret.get("type") != "kubernetes.io/dockerconfigjson":
        raise RuntimeError("source image pull secret must be kubernetes.io/dockerconfigjson")
    copied = {
        "apiVersion": "v1",
        "kind": "Secret",
        "metadata": {
            "name": IMAGE_PULL_SECRET,
            "namespace": namespace,
            "labels": {"ani.dev/profile": fixture_renderer.PROFILE},
        },
        "type": secret["type"],
        "data": secret.get("data") or {},
    }
    if ".dockerconfigjson" not in copied["data"]:
        raise RuntimeError("source image pull secret is missing .dockerconfigjson")
    return copied


def _http_get(base_url: str, path: str) -> str:
    request = urllib.request.Request(base_url + path)
    with _LOOPBACK_HTTP_OPENER.open(request, timeout=8) as response:
        return response.read().decode("utf-8")


def _prometheus_query(
    kubectl: Kubectl,
    namespace: str,
    query: str,
    *,
    evaluation_time: float | None = None,
) -> dict[str, Any]:
    parameters = {"query": query}
    if evaluation_time is not None:
        parameters["time"] = format(evaluation_time, ".9f")
    path = "/api/v1/query?" + urllib.parse.urlencode(parameters)
    with _service_port_forward(
        kubectl,
        namespace,
        fixture_renderer.PROMETHEUS_NAME,
        "http",
    ) as base_url:
        payload = json.loads(_http_get(base_url, path))
    if not isinstance(payload, dict):
        raise RuntimeError("Prometheus query response must be an object")
    return payload


def _prometheus_health_diagnostics(kubectl: Kubectl, namespace: str) -> dict[str, Any]:
    queries = (
        'up{job="ani-components"}',
        'timestamp(up{job="ani-components"})',
        'target_info{job="ani-components"}',
        'timestamp(target_info{job="ani-components"})',
    )
    evaluation_time = time.time()
    label_sets: dict[str, set[str]] = {}
    service_counts: dict[str, dict[str, int]] = {}
    for query in queries:
        payload = _prometheus_query(
            kubectl,
            namespace,
            query,
            evaluation_time=evaluation_time,
        )
        result = ((payload.get("data") or {}).get("result") or [])
        keys: set[str] = set()
        counts: dict[str, int] = {}
        for sample in result:
            labels = dict((sample or {}).get("metric") or {})
            labels.pop("__name__", None)
            keys.add(json.dumps(labels, separators=(",", ":"), sort_keys=True))
            service = str(labels.get("ani_service_name") or "<missing>")
            counts[service] = counts.get(service, 0) + 1
        label_sets[query] = keys
        service_counts[query] = dict(sorted(counts.items()))
    up = queries[0]
    up_timestamp = queries[1]
    target = queries[2]
    target_timestamp = queries[3]
    return {
        "query_service_counts": service_counts,
        "up_pair_symmetric_difference": len(label_sets[up] ^ label_sets[up_timestamp]),
        "target_info_pair_symmetric_difference": len(
            label_sets[target] ^ label_sets[target_timestamp]
        ),
    }


def _wait_up(
    kubectl: Kubectl,
    namespace: str,
    predicate: Any,
    *,
    attempts: int = 45,
) -> dict[str, int]:
    last: dict[str, int] = {}
    for _ in range(attempts):
        try:
            last = parse_up_vector(_prometheus_query(kubectl, namespace, 'up{job="ani-components"}'))
            if predicate(last):
                return last
        except (RuntimeError, ValueError, json.JSONDecodeError):
            pass
        time.sleep(3)
    raise RuntimeError(f"Prometheus up vector did not reach expected state; last={last}")


def _verify_target_info(kubectl: Kubectl, namespace: str, version: str) -> None:
    payload = _prometheus_query(kubectl, namespace, 'target_info{job="ani-components"}')
    result = ((payload.get("data") or {}).get("result") or []) if payload.get("status") == "success" else []
    seen: set[str] = set()
    for sample in result:
        metric = sample.get("metric") or {}
        name = metric.get("ani_service_name")
        if name not in SERVICES or name in seen:
            raise RuntimeError("target_info must contain exactly one sample per canonical service")
        if metric.get("service_namespace") != "ani" or metric.get("service_name") != name:
            raise RuntimeError(f"target_info identity mismatch for {name}")
        if metric.get("service_version") != version or not metric.get("service_instance_id"):
            raise RuntimeError(f"target_info version/instance mismatch for {name}")
        if metric.get("kubernetes_namespace") != namespace:
            raise RuntimeError(f"target_info Kubernetes namespace mismatch for {name}")
        seen.add(name)
    if seen != SERVICES:
        raise RuntimeError(f"target_info service set mismatch: {sorted(seen)}")


def _verify_management_endpoints(kubectl: Kubectl, namespace: str) -> None:
    for service in sorted(SERVICES):
        with _service_port_forward(kubectl, namespace, service, "health") as base_url:
            for path in ("/healthz", "/readyz"):
                payload = json.loads(_http_get(base_url, path))
                if payload.get("status") not in {"ok", "degraded"}:
                    raise RuntimeError(f"{service} {path} returned unexpected status")
            metrics = _http_get(base_url, "/metrics")
            if "target_info" not in metrics or f'service_name="{service}"' not in metrics:
                raise RuntimeError(f"{service} metrics is missing target_info identity")


def _free_port() -> int:
    with socket.socket(socket.AF_INET, socket.SOCK_STREAM) as listener:
        listener.bind(("127.0.0.1", 0))
        return int(listener.getsockname()[1])


def service_port_forward_command(
    kubectl: Kubectl,
    namespace: str,
    service: str,
    remote_port: int | str,
    local_port: int,
) -> list[str]:
    command = kubectl.command(
        [
            "-n",
            namespace,
            "port-forward",
            "--address=127.0.0.1",
            f"service/{service}",
            f"{local_port}:{remote_port}",
        ]
    )
    if kubectl.config.ssh_host:
        separator = command.index("--")
        command[separator:separator] = [
            "-o",
            "ExitOnForwardFailure=yes",
            "-L",
            f"127.0.0.1:{local_port}:127.0.0.1:{local_port}",
        ]
    return command


def gateway_port_forward_command(
    kubectl: Kubectl,
    namespace: str,
    port: int,
) -> list[str]:
    return service_port_forward_command(
        kubectl,
        namespace,
        "ani-gateway",
        8080,
        port,
    )


@contextmanager
def _service_port_forward(
    kubectl: Kubectl,
    namespace: str,
    service: str,
    remote_port: int | str,
) -> Iterator[str]:
    local_port = _free_port()
    process = subprocess.Popen(
        service_port_forward_command(
            kubectl,
            namespace,
            service,
            remote_port,
            local_port,
        ),
        stdout=subprocess.PIPE,
        stderr=subprocess.PIPE,
        text=True,
    )
    try:
        if process.stdout is None or process.stderr is None:
            raise RuntimeError(f"{service} port-forward output streams are unavailable")
        deadline = time.monotonic() + 10
        diagnostics: list[str] = []
        output_streams = [process.stdout, process.stderr]
        while time.monotonic() < deadline:
            if process.poll() is not None:
                detail = " ".join(line.strip() for line in diagnostics[-3:] if line.strip())
                suffix = f": {detail}" if detail else ""
                raise RuntimeError(
                    f"{service} port-forward exited before becoming ready{suffix}"
                )
            readable, _, _ = select.select(output_streams, [], [], 0.1)
            if not readable:
                continue
            ready = False
            for stream in readable:
                line = stream.readline()
                if not line:
                    continue
                diagnostics.append(line)
                if "Forwarding from " in line:
                    ready = True
            if ready:
                break
        else:
            raise RuntimeError(f"{service} port-forward did not become ready")
        yield f"http://127.0.0.1:{local_port}"
    finally:
        process.terminate()
        try:
            process.wait(timeout=5)
        except subprocess.TimeoutExpired:
            process.kill()
            process.wait(timeout=5)
        if process.stdout is not None:
            process.stdout.close()
        if process.stderr is not None:
            process.stderr.close()


@contextmanager
def _gateway_port_forward(kubectl: Kubectl, namespace: str) -> Iterator[str]:
    with _service_port_forward(kubectl, namespace, "ani-gateway", 8080) as base_url:
        yield base_url


def _platform_health(base_url: str, token: str | None) -> tuple[int, dict[str, Any]]:
    headers = {"Authorization": "Bearer " + token} if token else {}
    request = urllib.request.Request(
        base_url + "/api/v1/platform/services/health",
        headers=headers,
    )
    try:
        with _LOOPBACK_HTTP_OPENER.open(request, timeout=8) as response:
            status = response.status
            body = response.read()
    except urllib.error.HTTPError as exc:
        status = exc.code
        body = exc.read()
    try:
        payload = json.loads(body)
    except json.JSONDecodeError as exc:
        raise RuntimeError("Gateway platform health response is not JSON") from exc
    if not isinstance(payload, dict):
        raise RuntimeError("Gateway platform health response must be an object")
    return status, payload


def _component_status(payload: dict[str, Any], service: str) -> str:
    components = payload.get("components") or []
    matches = [item for item in components if item.get("service_name") == service]
    if len(matches) != 1:
        raise RuntimeError(f"Gateway response must include exactly one {service} component")
    return str(matches[0].get("scrape_status") or "")


def _wait_platform_status(
    base_url: str,
    token: str,
    service: str,
    expected: str,
    *,
    attempts: int = 40,
) -> dict[str, Any]:
    last: dict[str, Any] = {}
    status_counts: dict[int, int] = {}
    last_component = ""
    for _ in range(attempts):
        status, last = _platform_health(base_url, token)
        status_counts[status] = status_counts.get(status, 0) + 1
        if status == 200:
            last_component = _component_status(last, service)
            if last_component == expected:
                return last
        time.sleep(3)
    raise RuntimeError(
        f"Gateway {service} did not reach {expected}; last_status={status}; "
        f"last_code={last.get('code', '')}; last_component={last_component}; "
        f"status_counts={dict(sorted(status_counts.items()))}"
    )


def _wait_new_pod(kubectl: Kubectl, namespace: str, old_uid: str) -> tuple[str, str]:
    selector = (
        f"ani.dev/run-id={namespace.removeprefix(workload_renderer.NAMESPACE_PREFIX)},"
        f"ani.dev/service-name={FAULT_SERVICE}"
    )
    for _ in range(60):
        document = kubectl.json(["-n", namespace, "get", "pods", "-l", selector])
        items = document.get("items") or []
        ready = []
        for item in items:
            uid = str((item.get("metadata") or {}).get("uid") or "")
            conditions = (item.get("status") or {}).get("conditions") or []
            if uid and uid != old_uid and any(
                condition.get("type") == "Ready" and condition.get("status") == "True"
                for condition in conditions
            ):
                ready.append((str(item["metadata"]["name"]), uid))
        if len(ready) == 1:
            return ready[0]
        time.sleep(3)
    raise RuntimeError("replacement auth-service Pod did not become Ready")


def _initial_pod(kubectl: Kubectl, namespace: str) -> tuple[str, str, str]:
    run_id = namespace.removeprefix(workload_renderer.NAMESPACE_PREFIX)
    selector = f"ani.dev/run-id={run_id},ani.dev/service-name={FAULT_SERVICE}"
    document = kubectl.json(["-n", namespace, "get", "pods", "-l", selector])
    items = document.get("items") or []
    if len(items) != 1:
        raise RuntimeError("fault selector must resolve exactly one auth-service Pod")
    metadata = items[0].get("metadata") or {}
    return str(metadata.get("name") or ""), str(metadata.get("uid") or ""), selector


def _activate_scrape_fault(kubectl: Kubectl, namespace: str) -> dict[str, str]:
    old_name, old_uid, selector = _initial_pod(kubectl, namespace)
    kubectl.apply(scrape_fault_network_policy(namespace))
    # Existing scrape connections are not guaranteed to be terminated by a
    # NetworkPolicy update. Replace the one approved target Pod so the next
    # scrape must establish a new connection through the fault policy.
    kubectl.run(["-n", namespace, "delete", "pod", old_name, "--wait=true"])
    new_name, new_uid = _wait_new_pod(kubectl, namespace, old_uid)
    return {
        "selector": selector,
        "old_pod": old_name,
        "old_uid": old_uid,
        "new_pod": new_name,
        "new_uid": new_uid,
    }


def _baseline_network_policy(manifest: str) -> dict[str, Any]:
    matches = [
        document
        for document in yaml.safe_load_all(manifest)
        if document
        and document.get("kind") == "NetworkPolicy"
        and (document.get("metadata") or {}).get("name") == SCRAPE_FAULT_POLICY
    ]
    if len(matches) != 1:
        raise RuntimeError("rendered fixture must contain one baseline NetworkPolicy")
    return matches[0]


def _verify_exclusions(kubectl: Kubectl, namespace: str) -> None:
    regex = "|".join(sorted(EXCLUDED_SERVICES))
    payload = _prometheus_query(
        kubectl,
        namespace,
        f'up{{job="ani-components",ani_service_name=~"{regex}"}}',
    )
    result = ((payload.get("data") or {}).get("result") or []) if payload.get("status") == "success" else None
    if result != []:
        raise RuntimeError("excluded services must have zero ani-components targets")


def _write_evidence(path: Path, evidence: dict[str, Any]) -> None:
    path.parent.mkdir(parents=True, exist_ok=True)
    path.write_text(json.dumps(evidence, ensure_ascii=False, indent=2, sort_keys=True) + "\n", encoding="utf-8")


def run_live(config: LiveConfig) -> dict[str, Any]:
    errors = validate_config_values(config)
    if errors:
        raise RuntimeError("; ".join(errors))
    spec = workload_renderer.load_inventory(fixture_renderer.INVENTORY)
    images = workload_renderer.load_images(
        config.images_file,
        {service["name"] for service in spec["services"]},
    )
    kubectl = Kubectl(config)
    current_context, server = _cluster_identity(kubectl)
    if current_context != config.context or server != config.expected_server:
        raise RuntimeError(
            f"kube identity mismatch: context={current_context!r} server={server!r}"
        )
    permission_preflight = verify_cluster_permissions(
        kubectl,
        namespace=config.namespace,
        image_pull_secret_source=config.image_pull_secret_source,
    )
    namespace_probe = kubectl.result(["get", "namespace", config.namespace, "-o", "name"])
    if namespace_probe.returncode == 0:
        raise RuntimeError("target namespace already exists; refusing to reuse it")

    include_pull_secret = bool(config.image_pull_secret_source)
    manifest = fixture_renderer.render_l3_fixture(
        namespace=config.namespace,
        version=config.version,
        images=images,
        image_pull_secret=IMAGE_PULL_SECRET if include_pull_secret else None,
    )
    documents = [document for document in yaml.safe_load_all(manifest) if document]
    namespace_document = next(document for document in documents if document["kind"] == "Namespace")
    remaining_manifest = yaml.safe_dump_all(
        [document for document in documents if document["kind"] != "Namespace"],
        sort_keys=False,
        explicit_start=True,
    )
    runtime_secret, observability_token, tenant_token = build_runtime_secret(config.namespace)
    pull_secret = (
        _copy_pull_secret(kubectl, config.image_pull_secret_source, config.namespace)
        if include_pull_secret
        else None
    )
    evidence: dict[str, Any] = {
        "profile": "OBS-RUNTIME-P0-L3",
        "source_commit": config.source_commit,
        "context": config.context,
        "cluster_server": config.expected_server,
        "namespace": config.namespace,
        "version": config.version,
        "prometheus_image": fixture_renderer.PROMETHEUS_IMAGE,
        "fixture_images": dict(sorted(fixture_renderer.fixture_images().items())),
        "fixture_manifest_sha256": "sha256:"
        + hashlib.sha256(manifest.encode("utf-8")).hexdigest(),
        "images": images,
        "object_plan": object_plan(config.namespace, include_pull_secret=include_pull_secret),
        "permission_preflight": permission_preflight,
        "runtime_secret": secret_safe_summary(runtime_secret),
        "checks": [],
        "cleanup": {"status": "not_started"},
        "result": "running",
    }
    namespace_created = False
    try:
        kubectl.apply(namespace_document)
        namespace_created = True
        kubectl.apply(runtime_secret, sensitive=True)
        if pull_secret is not None:
            kubectl.apply(pull_secret, sensitive=True)
        kubectl.apply(remaining_manifest)
        evidence["checks"].append({"id": "isolated-object-apply", "status": "passed"})

        deployment_names = sorted(SERVICES | {fixture_renderer.PROMETHEUS_NAME})
        for dependency in ("postgres", "nats", "redis"):
            deployment_names.append(f"ani-service-observability-{dependency}")
        kubectl.run(
            [
                "-n",
                config.namespace,
                "wait",
                "--for=condition=Available",
                *[f"deployment/{name}" for name in deployment_names],
                f"--timeout={config.wait_timeout}",
            ]
        )
        evidence["checks"].append({"id": "deployments-available", "status": "passed"})

        _verify_management_endpoints(kubectl, config.namespace)
        evidence["checks"].append({"id": "seven-management-endpoints", "status": "passed"})
        _wait_up(kubectl, config.namespace, lambda values: set(values) == SERVICES and all(values.values()))
        _verify_target_info(kubectl, config.namespace, config.version)
        _verify_exclusions(kubectl, config.namespace)
        evidence["checks"].append(
            {"id": "kubernetes-discovery-target-info-exclusions", "status": "passed"}
        )

        with _gateway_port_forward(kubectl, config.namespace) as gateway_url:
            unauthenticated_status, unauthenticated = _platform_health(gateway_url, None)
            if (
                unauthenticated_status != 401
                or unauthenticated.get("code") != "UNAUTHORIZED"
            ):
                raise RuntimeError("Gateway platform health must reject missing credentials with 401")
            evidence["checks"].append(
                {"id": "gateway-missing-credential-unauthorized", "status": "passed"}
            )

            tenant_status, tenant_denied = _platform_health(gateway_url, tenant_token)
            if tenant_status != 403 or tenant_denied.get("code") != "FORBIDDEN":
                raise RuntimeError("Gateway platform health must reject tenant credentials with 403")
            evidence["checks"].append(
                {"id": "gateway-tenant-domain-forbidden", "status": "passed"}
            )

            baseline = _wait_platform_status(
                gateway_url,
                observability_token,
                FAULT_SERVICE,
                "reachable",
            )
            if (
                baseline.get("scope") != "ani_services"
                or baseline.get("coverage") != "partial"
                or baseline.get("signal") != "prometheus_scrape"
                or len(baseline.get("components") or []) != 7
            ):
                raise RuntimeError("Gateway platform health baseline contract mismatch")
            evidence["checks"].append({"id": "gateway-seven-reachable", "status": "passed"})

            baseline_policy = _baseline_network_policy(manifest)
            pod_replacement = _activate_scrape_fault(kubectl, config.namespace)
            _wait_up(
                kubectl,
                config.namespace,
                lambda values: values.get(FAULT_SERVICE) == 0,
            )
            _wait_platform_status(
                gateway_url,
                observability_token,
                FAULT_SERVICE,
                "unreachable",
            )
            evidence["checks"].append({"id": "real-up-zero-unreachable", "status": "passed"})
            kubectl.apply(baseline_policy)
            _wait_up(
                kubectl,
                config.namespace,
                lambda values: values.get(FAULT_SERVICE) == 1,
            )
            _wait_platform_status(
                gateway_url,
                observability_token,
                FAULT_SERVICE,
                "reachable",
            )
            evidence["checks"].append({"id": "scrape-recovery", "status": "passed"})
            evidence["checks"].append(
                {
                    "id": "pod-delete-automatic-recovery",
                    "status": "passed",
                    **pod_replacement,
                }
            )

            kubectl.run(
                ["-n", config.namespace, "scale", f"deployment/{MISSING_SERVICE}", "--replicas=0"]
            )
            _wait_up(kubectl, config.namespace, lambda values: MISSING_SERVICE not in values)
            try:
                _wait_platform_status(
                    gateway_url,
                    observability_token,
                    MISSING_SERVICE,
                    "unknown",
                )
            except RuntimeError as exc:
                try:
                    evidence["missing_transition_diagnostics"] = (
                        _prometheus_health_diagnostics(kubectl, config.namespace)
                    )
                except Exception as diagnostic_exc:
                    evidence["missing_transition_diagnostics"] = {
                        "error": type(diagnostic_exc).__name__ + ": " + str(diagnostic_exc)
                    }
                raise RuntimeError(
                    f"{exc}; direct_prometheus="
                    + json.dumps(
                        evidence["missing_transition_diagnostics"],
                        separators=(",", ":"),
                        sort_keys=True,
                    )
                ) from exc
            evidence["checks"].append(
                {"id": "target-missing-prometheus-staleness-unknown", "status": "passed"}
            )
            kubectl.run(
                ["-n", config.namespace, "scale", f"deployment/{MISSING_SERVICE}", "--replicas=1"]
            )
            kubectl.run(
                [
                    "-n",
                    config.namespace,
                    "wait",
                    "--for=condition=Available",
                    f"deployment/{MISSING_SERVICE}",
                    f"--timeout={config.wait_timeout}",
                ]
            )
            _wait_up(
                kubectl,
                config.namespace,
                lambda values: values.get(MISSING_SERVICE) == 1,
            )

            kubectl.run(
                [
                    "-n",
                    config.namespace,
                    "scale",
                    f"deployment/{fixture_renderer.PROMETHEUS_NAME}",
                    "--replicas=0",
                ]
            )
            for _ in range(20):
                status, unavailable = _platform_health(gateway_url, observability_token)
                if status == 503 and (unavailable.get("code") == "OBSERVABILITY_UNAVAILABLE"):
                    break
                time.sleep(2)
            else:
                raise RuntimeError("Gateway did not fail closed while Prometheus was unavailable")
            evidence["checks"].append(
                {"id": "prometheus-unavailable-gateway-fail-closed", "status": "passed"}
            )
            kubectl.run(
                [
                    "-n",
                    config.namespace,
                    "scale",
                    f"deployment/{fixture_renderer.PROMETHEUS_NAME}",
                    "--replicas=1",
                ]
            )
            kubectl.run(
                [
                    "-n",
                    config.namespace,
                    "wait",
                    "--for=condition=Available",
                    f"deployment/{fixture_renderer.PROMETHEUS_NAME}",
                    f"--timeout={config.wait_timeout}",
                ]
            )
            _wait_up(kubectl, config.namespace, lambda values: set(values) == SERVICES and all(values.values()))
            _wait_platform_status(
                gateway_url,
                observability_token,
                MISSING_SERVICE,
                "reachable",
            )
            evidence["checks"].append({"id": "final-full-recovery", "status": "passed"})

        evidence["result"] = "passed"
        return evidence
    except Exception as exc:
        evidence["result"] = "failed"
        evidence["failure"] = type(exc).__name__ + ": " + str(exc)
        raise
    finally:
        if namespace_created:
            cleanup = kubectl.result(
                ["delete", "namespace", config.namespace, "--wait=true", f"--timeout={config.wait_timeout}"]
            )
            if cleanup.returncode == 0:
                absent = kubectl.result(["get", "namespace", config.namespace, "-o", "name"])
                evidence["cleanup"] = {
                    "status": "passed" if absent.returncode != 0 else "failed",
                    "deleted": f"Namespace/{config.namespace}",
                }
            else:
                evidence["cleanup"] = {
                    "status": "failed",
                    "deleted": f"Namespace/{config.namespace}",
                    "error": cleanup.stderr.strip() or cleanup.stdout.strip(),
                }
        _write_evidence(config.evidence_output, evidence)


def parse_args() -> argparse.Namespace:
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("--live", action="store_true", help="execute the approved L3 mutations")
    parser.add_argument("--kubectl-binary", default="kubectl")
    parser.add_argument("--kubeconfig", type=Path, required=True)
    parser.add_argument("--ssh-host", default="", help="run kubectl through this SSH config alias")
    parser.add_argument("--ssh-config", type=Path)
    parser.add_argument("--context", required=True)
    parser.add_argument("--expected-server", required=True)
    parser.add_argument("--namespace", required=True)
    parser.add_argument("--version", required=True)
    parser.add_argument("--images-file", type=Path, required=True)
    parser.add_argument("--image-pull-secret-source", default="")
    parser.add_argument("--evidence-output", type=Path, required=True)
    parser.add_argument("--confirmation", default="")
    return parser.parse_args()


def main() -> int:
    args = parse_args()
    config = LiveConfig(
        kubectl_binary=args.kubectl_binary,
        kubeconfig=args.kubeconfig,
        ssh_host=args.ssh_host,
        ssh_config=args.ssh_config,
        context=args.context,
        expected_server=args.expected_server,
        namespace=args.namespace,
        version=args.version,
        images_file=args.images_file,
        confirmation=args.confirmation,
        image_pull_secret_source=args.image_pull_secret_source,
        evidence_output=args.evidence_output,
    )
    errors = validate_config_values(
        config,
        check_files=args.live,
        require_confirmation=args.live,
    )
    if errors:
        print("OBS-RUNTIME-P0 L3 configuration invalid:", file=sys.stderr)
        for error in errors:
            print(f"  - {error}", file=sys.stderr)
        return 2
    if not args.live:
        images = workload_renderer.load_images(config.images_file, SERVICES)
        print(json.dumps(approval_plan(config, images), ensure_ascii=False, indent=2, sort_keys=True))
        return 0
    try:
        evidence = run_live(config)
    except Exception as exc:
        print(f"OBS-RUNTIME-P0 L3 failed: {exc}", file=sys.stderr)
        return 1
    if evidence.get("result") != "passed" or (evidence.get("cleanup") or {}).get("status") != "passed":
        print("OBS-RUNTIME-P0 L3 did not close cleanly", file=sys.stderr)
        return 1
    print(f"OBS-RUNTIME-P0 L3 passed and cleaned; evidence={config.evidence_output}")
    return 0


if __name__ == "__main__":
    raise SystemExit(main())
