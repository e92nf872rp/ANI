#!/usr/bin/env python3
"""Validate the local remote model-import deployment and source safety contract.

This is a static contract gate. It never contacts a cluster, reads Secret
objects, or treats a placeholder image as a runnable live deployment.
"""

from __future__ import annotations

import base64
import hashlib
import re
from pathlib import Path
from typing import Any, Iterable

import yaml

ROOT = Path(__file__).resolve().parents[1]
PROFILE = ROOT / "deploy/real-k8s-lab/inference-incluster-e2e.yaml"
MTLS_DEV = ROOT / "deploy/real-k8s-lab/model-repository-mtls-dev.yaml"
MAKEFILE = ROOT / "Makefile"
FETCHER_DOCKERFILE = ROOT / "services/model-fetcher/Dockerfile"
SOURCE_FILES = (
    ROOT / "services/model-service/internal/importer/source.go",
    ROOT / "services/model-service/internal/importer/worker.go",
    ROOT / "services/model-service/internal/importer/archive.go",
    ROOT / "services/model-fetcher/archive_extract.go",
)
IMAGE_DIGEST_RE = re.compile(r"^.+@sha256:[0-9a-f]{64}$")
DOCKER_FROM_DIGEST_RE = re.compile(
    r"^FROM(?:\s+--platform=\S+)?\s+\S+@sha256:[0-9a-f]{64}(?:\s+AS\s+\S+)?$",
    re.IGNORECASE,
)
PLAINTEXT_SECRET_KEY_RE = re.compile(r"(?:token|password|secret|access[_-]?key|credential)", re.I)
WORKER_MAX_FILES = 10000
WORKER_MAX_ARCHIVE_BYTES = 3 * 1024 * 1024 * 1024
ATLAS_SUM_LINE_RE = re.compile(r"^h1:(?P<sum>[A-Za-z0-9+/]+={0,2})$")
ATLAS_ENTRY_RE = re.compile(r"^(?P<name>[^\s]+)\s+h1:(?P<sum>[A-Za-z0-9+/]+={0,2})$")


def _documents(path: Path) -> list[dict[str, Any]]:
    try:
        values = list(yaml.safe_load_all(path.read_text(encoding="utf-8")))
    except OSError as exc:
        raise AssertionError(f"cannot read manifest: {path}") from exc
    return [value for value in values if isinstance(value, dict)]


def _find(documents: Iterable[dict[str, Any]], kind: str, name: str) -> dict[str, Any]:
    for document in documents:
        metadata = document.get("metadata") or {}
        if document.get("kind") == kind and metadata.get("name") == name:
            return document
    raise AssertionError(f"missing {kind}/{name}")


def validate_fetcher_build_contract(root: Path = ROOT) -> None:
    """Require a discoverable fetcher image target and immutable base images.

    The deployment profile intentionally carries an image digest supplied by
    the release process.  This static gate covers the other half of that
    contract: developers must have an explicit, reproducible Make target for
    building the fetcher, and every ``FROM`` image in its Dockerfile must be
    pinned by digest.  No Docker daemon or registry is contacted.
    """

    makefile_path = root / "Makefile"
    dockerfile_path = root / "services/model-fetcher/Dockerfile"
    try:
        makefile = makefile_path.read_text(encoding="utf-8")
    except OSError as exc:
        raise AssertionError(f"cannot read Makefile: {makefile_path}") from exc
    try:
        dockerfile = dockerfile_path.read_text(encoding="utf-8")
    except OSError as exc:
        raise AssertionError(f"cannot read model-fetcher Dockerfile: {dockerfile_path}") from exc

    if not re.search(r"(?m)^image-model-fetcher:\s*$", makefile):
        raise AssertionError("Makefile must define image-model-fetcher")
    target_match = re.search(
        r"(?ms)^image-model-fetcher:\s*\n(?P<body>(?:\t[^\n]*\n?)+)",
        makefile,
    )
    if not target_match:
        raise AssertionError("image-model-fetcher target must contain a recipe")
    # Make recipes readable in either one-line or conventional Make
    # backslash-continuation form before checking their semantic tokens.
    target_body = re.sub(r"\\\s*\n\s*", " ", target_match.group("body"))
    required_recipe_tokens = (
        "docker build",
        "-f services/model-fetcher/Dockerfile",
        "-t $(REGISTRY)/model-fetcher:$(VERSION)",
    )
    for token in required_recipe_tokens:
        if token not in target_body:
            raise AssertionError(f"image-model-fetcher recipe must include {token}")
    if not re.search(r"(?m)\bdocker\s+build\b[^\n]*\s\.\s*$", target_body):
        raise AssertionError("image-model-fetcher recipe must build from the repository root")
    if not re.search(r"(?m)^\.PHONY:.*\bimage-model-fetcher\b", makefile):
        raise AssertionError("image-model-fetcher must be declared phony")
    if "make image-model-fetcher" not in makefile:
        raise AssertionError("Makefile help must mention make image-model-fetcher")

    from_lines = [
        line.strip()
        for line in dockerfile.splitlines()
        if line.strip() and line.strip().upper().startswith("FROM ")
    ]
    if not from_lines:
        raise AssertionError("model-fetcher Dockerfile must declare at least one FROM image")
    for line in from_lines:
        if not DOCKER_FROM_DIGEST_RE.fullmatch(line):
            raise AssertionError("every model-fetcher Dockerfile FROM image must be digest pinned")


def _atlas_sum_ignored(content: bytes) -> bool:
    """Mirror Atlas' first-line ``atlas:sum ignore`` directive handling."""

    first_line = content.split(b"\n", 1)[0].decode("utf-8", errors="replace")
    return bool(re.match(r"^[ -~]*atlas:sum\s+ignore(?:\s*)$", first_line))


def validate_migration_checksum(root: Path = ROOT) -> None:
    """Validate ``deploy/migrations/atlas.sum`` using Atlas' HashFile algorithm.

    Atlas computes each file entry from one cumulative SHA-256 stream (the
    filename followed by file bytes), then computes the manifest root from the
    filename and cumulative base64 digest pairs. Reimplementing that small,
    deterministic algorithm here keeps the remote-import static gate useful in
    environments where the Atlas CLI is intentionally unavailable; it never
    contacts a database.
    """

    migration_dir = root / "deploy/migrations"
    sum_path = migration_dir / "atlas.sum"
    if not migration_dir.is_dir():
        raise AssertionError(f"missing migration directory: {migration_dir}")
    if not sum_path.is_file():
        raise AssertionError(f"missing Atlas checksum file: {sum_path}")

    files = sorted(migration_dir.glob("*.sql"), key=lambda path: path.name)
    if not files:
        raise AssertionError("migration directory contains no .sql files")

    cumulative = hashlib.sha256()
    expected: list[tuple[str, str]] = []
    for path in files:
        content = path.read_bytes()
        cumulative.update(path.name.encode("utf-8"))
        if _atlas_sum_ignored(content):
            continue
        cumulative.update(content)
        expected.append((path.name, base64.b64encode(cumulative.digest()).decode("ascii")))

    lines = sum_path.read_text(encoding="utf-8").splitlines()
    if not lines:
        raise AssertionError("Atlas checksum file is empty")
    root_match = ATLAS_SUM_LINE_RE.fullmatch(lines[0])
    if not root_match:
        raise AssertionError("Atlas checksum root line must be h1:<base64>")

    actual: list[tuple[str, str]] = []
    seen: set[str] = set()
    for line_number, line in enumerate(lines[1:], start=2):
        match = ATLAS_ENTRY_RE.fullmatch(line)
        if not match:
            raise AssertionError(f"invalid Atlas checksum entry at line {line_number}")
        name, digest = match.group("name"), match.group("sum")
        if name in seen:
            raise AssertionError(f"duplicate Atlas checksum entry: {name}")
        seen.add(name)
        actual.append((name, digest))

    if actual != expected:
        expected_names = [name for name, _ in expected]
        actual_names = [name for name, _ in actual]
        if actual_names != expected_names:
            raise AssertionError("Atlas checksum entries do not match the migration .sql files")
        for (name, expected_digest), (_, actual_digest) in zip(expected, actual):
            if expected_digest != actual_digest:
                raise AssertionError(f"Atlas checksum mismatch for migration: {name}")
        raise AssertionError("Atlas checksum entries are out of order")

    manifest_hash = hashlib.sha256()
    for name, digest in expected:
        manifest_hash.update(name.encode("utf-8"))
        manifest_hash.update(digest.encode("utf-8"))
    computed_root = base64.b64encode(manifest_hash.digest()).decode("ascii")
    if root_match.group("sum") != computed_root:
        raise AssertionError(
            f"Atlas checksum root mismatch: expected {computed_root}, found {root_match.group('sum')}"
        )


def validate_model_service_build_contract(root: Path = ROOT) -> None:
    """Require immutable base images for the model-service/worker build.

    The model-import-worker is a target of the model-service multi-stage
    Dockerfile, so both the worker and control-plane images inherit these
    stages.  A mutable tag here would make the release digest non-reproducible
    even when the resulting image reference is pinned in the deployment
    profile.
    """
    dockerfile_path = root / "services/model-service/Dockerfile"
    try:
        dockerfile = dockerfile_path.read_text(encoding="utf-8")
    except OSError as exc:
        raise AssertionError(f"cannot read model-service Dockerfile: {dockerfile_path}") from exc
    from_lines = [
        line.strip()
        for line in dockerfile.splitlines()
        if line.strip() and line.strip().upper().startswith("FROM ")
    ]
    if len(from_lines) < 3:
        raise AssertionError("model-service Dockerfile must declare build and runtime stages")
    for line in from_lines:
        if not DOCKER_FROM_DIGEST_RE.fullmatch(line):
            raise AssertionError("every model-service Dockerfile FROM image must be digest pinned")


def _walk_plaintext_secret_values(value: Any, location: str = "manifest") -> list[str]:
    findings: list[str] = []
    if isinstance(value, dict):
        # Kubernetes env entries encode their name and literal value as
        # siblings (``{"name": "HF_TOKEN", "value": "..."}``), so a
        # recursive check of the value key alone would miss this shape.
        env_name = value.get("name")
        literal_value = value.get("value")
        if (
            isinstance(env_name, str)
            and PLAINTEXT_SECRET_KEY_RE.search(env_name)
            and isinstance(literal_value, str)
            and literal_value.strip()
        ):
            findings.append(f"{location}.value")
        for key, child in value.items():
            key_text = str(key)
            if PLAINTEXT_SECRET_KEY_RE.search(key_text) and key_text not in {"secretKeyRef", "secretRef"}:
                if key_text in {"name", "key"}:
                    continue
                if key_text == "value" and isinstance(child, str) and child.strip():
                    findings.append(f"{location}.{key_text}")
            findings.extend(_walk_plaintext_secret_values(child, f"{location}.{key_text}"))
    elif isinstance(value, list):
        for index, child in enumerate(value):
            findings.extend(_walk_plaintext_secret_values(child, f"{location}[{index}]"))
    return findings


def validate_manifest(path: Path = PROFILE) -> None:
    documents = _documents(path)
    config = _find(documents, "ConfigMap", "ani-inference-materialization")
    data = config.get("data") or {}
    for key in ("model_service_grpc_addr", "model_fetcher_grpc_addr", "model_fetcher_image_ref", "model_fetcher_allow_insecure_http", "model_import_worker_image_ref"):
        if key not in data:
            raise AssertionError(f"ConfigMap missing {key}")
    if str(data.get("model_fetcher_allow_insecure_http", "")).strip().lower() not in {"true", "false"}:
        raise AssertionError("ConfigMap model_fetcher_allow_insecure_http must be a boolean")
    config_worker_image = str(data.get("model_import_worker_image_ref", "")).strip()
    for key in ("model_fetcher_image_ref", "model_import_worker_image_ref"):
        image_ref = str(data.get(key, "")).strip()
        if image_ref and not IMAGE_DIGEST_RE.fullmatch(image_ref):
            raise AssertionError(f"ConfigMap {key} must be digest pinned or explicitly left empty")
    if str(data.get("model_service_grpc_addr", "")).strip() != "model-service.ani-system.svc.cluster.local:9103":
        raise AssertionError("ConfigMap model_service_grpc_addr must target model-service 9103")
    if str(data.get("model_fetcher_grpc_addr", "")).strip() != "model-service.ani-system.svc.cluster.local:9105":
        raise AssertionError("ConfigMap model_fetcher_grpc_addr must target model-service mTLS 9105")

    worker = _find(documents, "Deployment", "model-import-worker")
    spec = worker.get("spec") or {}
    if spec.get("replicas") != 1:
        raise AssertionError("model-import-worker must start with one replica for the local profile")
    template = (spec.get("template") or {}).get("spec") or {}
    containers = template.get("containers") or []
    if len(containers) != 1:
        raise AssertionError("model-import-worker must have one container")
    container = containers[0]
    image = str(container.get("image", "")).strip()
    if image and not IMAGE_DIGEST_RE.fullmatch(image):
        raise AssertionError("model-import-worker image must be digest pinned or explicitly left empty")
    if image != config_worker_image:
        raise AssertionError("model-import-worker image must match ConfigMap model_import_worker_image_ref")
    env = {str(item.get("name")): item for item in container.get("env", []) if isinstance(item, dict)}
    for key in ("DATABASE_URL", "NATS_URL", "REDIS_URL", "OBJECT_STORE_ENDPOINT", "OBJECT_STORE_PUBLIC_ENDPOINT", "OBJECT_STORE_ACCESS_KEY_ID", "OBJECT_STORE_SECRET_ACCESS_KEY"):
        entry = env.get(key)
        if not entry or not isinstance(entry.get("valueFrom"), dict) or not isinstance(entry["valueFrom"].get("secretKeyRef"), dict):
            raise AssertionError(f"worker {key} must be wired through a SecretKeyRef")
    if _walk_plaintext_secret_values(worker):
        raise AssertionError("worker manifest contains a plaintext credential")
    for key in ("OBJECT_STORE_PROVIDER", "OBJECT_STORE_REGION", "OBJECT_STORE_SECURE", "OBJECT_STORE_BUCKET_PREFIX"):
        if key not in env:
            raise AssertionError(f"worker missing {key}")
    archive_limits = {}
    for key in (
        "MODEL_IMPORT_MAX_FILES",
        "MODEL_IMPORT_MAX_TOTAL_BYTES",
        "MODEL_IMPORT_MAX_FILE_BYTES",
        "MODEL_IMPORT_MAX_OUTPUT_BYTES",
    ):
        entry = env.get(key)
        if not entry or set(entry) != {"name", "value"}:
            raise AssertionError(f"worker {key} must be an explicit non-secret value")
        raw = str(entry.get("value", "")).strip()
        try:
            value = int(raw, 10)
        except (TypeError, ValueError) as exc:
            raise AssertionError(f"worker {key} must be a positive integer") from exc
        if value <= 0:
            raise AssertionError(f"worker {key} must be a positive integer")
        archive_limits[key] = value
    if archive_limits["MODEL_IMPORT_MAX_FILES"] > WORKER_MAX_FILES:
        raise AssertionError("worker MODEL_IMPORT_MAX_FILES exceeds archive policy")
    for key in ("MODEL_IMPORT_MAX_TOTAL_BYTES", "MODEL_IMPORT_MAX_FILE_BYTES", "MODEL_IMPORT_MAX_OUTPUT_BYTES"):
        if archive_limits[key] > WORKER_MAX_ARCHIVE_BYTES:
            raise AssertionError(f"worker {key} exceeds the 4Gi workspace headroom policy")
    if archive_limits["MODEL_IMPORT_MAX_FILE_BYTES"] > archive_limits["MODEL_IMPORT_MAX_TOTAL_BYTES"]:
        raise AssertionError("worker max file bytes cannot exceed max total bytes")
    mounts = container.get("volumeMounts") or []
    workspace_mount = next((item for item in mounts if item.get("name") == "archive-workspace"), None)
    if workspace_mount != {"name": "archive-workspace", "mountPath": "/tmp"}:
        raise AssertionError("worker must mount its writable archive workspace at /tmp")
    volumes = template.get("volumes") or []
    workspace = next((item for item in volumes if item.get("name") == "archive-workspace"), None)
    if workspace != {"name": "archive-workspace", "emptyDir": {"sizeLimit": "4Gi"}}:
        raise AssertionError("worker archive workspace must be a bounded 4Gi emptyDir")
    memory_limit = ((container.get("resources") or {}).get("limits") or {}).get("memory")
    if memory_limit != "2Gi":
        raise AssertionError("worker memory limit must cover streaming archive page cache")


def validate_sources(root: Path = ROOT) -> None:
    source_paths = (
        root / "services/model-service/internal/importer/source.go",
        root / "services/model-service/internal/importer/huggingface.go",
        root / "services/model-service/internal/importer/modelscope.go",
        root / "services/model-service/internal/importer/worker.go",
        root / "services/model-service/internal/importer/archive.go",
        root / "services/model-fetcher/archive_extract.go",
    )
    source = "\n".join(path.read_text(encoding="utf-8") for path in source_paths if path.exists())
    if not re.search(r"parsed\.Scheme\s*!=\s*[\"']https[\"']", source) or "huggingface.co" not in source or "modelscope.cn" not in source:
        raise AssertionError("remote sources must enforce HTTPS and the documented host allowlist")
    # Redirect handling may mention the Authorization header only to remove
    # it. Reject source credential inputs, not that defensive header scrub.
    if re.search(r"(?:HF_TOKEN|MODELSCOPE_TOKEN|BasicAuth)", source):
        raise AssertionError("remote source adapters must not accept repository credentials")
    if (
        "ObjectKey" not in source
        or "BucketClassModel" not in source
        or "model.tar.gz" not in source
        or "TenantID" not in source
        or 'parsed.Scheme != "object"' not in source
        or 'parsed.Host != "models"' not in source
        or "parts[0] != task.TenantID.String()" not in source
    ):
        raise AssertionError("worker must use tenant-scoped model archive identities")
    fetcher_main = (root / "services/model-fetcher/main.go").read_text(encoding="utf-8")
    if "credentials/insecure" in fetcher_main or "insecure.NewCredentials" in fetcher_main:
        raise AssertionError("model-fetcher must not have an insecure model-service gRPC fallback")
    for env_name in ("MODEL_SERVICE_TLS_CA_FILE", "MODEL_SERVICE_TLS_CERT_FILE", "MODEL_SERVICE_TLS_KEY_FILE"):
        if env_name not in fetcher_main:
            raise AssertionError(f"model-fetcher TLS client must require {env_name}")
    archive = (root / "services/model-fetcher/archive_extract.go").read_text(encoding="utf-8")
    for token in ("os.Rename", "extractionMarker", ".ani-model-extraction-complete", "duplicate archive entry", "non-regular entry", "MaxTotalBytes"):
        if token not in archive:
            raise AssertionError(f"archive extractor missing safety guard: {token}")
    revision_migration = root / "deploy/migrations/20260904000100_model_import_resolved_revision.sql"
    if not revision_migration.exists() or "ADD COLUMN IF NOT EXISTS resolved_revision" not in revision_migration.read_text(encoding="utf-8"):
        raise AssertionError("remote import must persist resolved source revisions")


def validate_workspace(root: Path = ROOT) -> None:
    """Validate all static remote-import contracts under a repository root."""
    validate_fetcher_build_contract(root)
    validate_model_service_build_contract(root)
    validate_migration_checksum(root)
    validate_manifest(root / "deploy/real-k8s-lab/inference-incluster-e2e.yaml")
    validate_fetcher_mtls_manifest(root / "deploy/real-k8s-lab/inference-incluster-e2e.yaml")
    validate_gateway_manifest(root / "deploy/real-k8s-lab/sprint13-production-shaped-gateway-deployment.yaml")
    validate_gateway_rbac_manifest(root / "deploy/real-k8s-lab/sprint13-production-shaped-gateway-rbac.yaml")
    validate_mtls_dev(root / "deploy/real-k8s-lab/model-repository-mtls-dev.yaml")
    validate_model_service_network_policy(root / "deploy/real-k8s-lab/model-repository-mtls-dev.yaml")
    validate_tenant_rbac_manifest(root / "deploy/real-k8s-lab/model-repository-mtls-dev.yaml")
    validate_sources(root)


def validate_fetcher_mtls_manifest(path: Path) -> None:
    """Require the model-service Deployment to expose only the mTLS fetcher port."""
    documents = _documents(path)
    model_service = _find(documents, "Deployment", "model-service")
    template = ((model_service.get("spec") or {}).get("template") or {}).get("spec") or {}
    containers = template.get("containers") or []
    if len(containers) != 1:
        raise AssertionError("model-service must have one container")
    container = containers[0]
    env = {str(item.get("name")): item for item in container.get("env", []) if isinstance(item, dict)}
    port = env.get("MODEL_FETCHER_GRPC_PORT")
    if not port or str(port.get("value", "")).strip() != "9105":
        raise AssertionError("model-service MODEL_FETCHER_GRPC_PORT must be 9105")
    expected_tls = {
        "MODEL_SERVICE_TLS_CERT_FILE": "/var/run/ani/model-service-tls/tls.crt",
        "MODEL_SERVICE_TLS_KEY_FILE": "/var/run/ani/model-service-tls/tls.key",
        "MODEL_SERVICE_TLS_CLIENT_CA_FILE": "/var/run/ani/model-service-tls/ca.crt",
    }
    for env_name, value in expected_tls.items():
        entry = env.get(env_name)
        if not entry or str(entry.get("value", "")).strip() != value:
            raise AssertionError(f"model-service {env_name} must reference the mTLS Secret mount")
    ports = container.get("ports") or []
    if not any(item.get("name") == "fetcher-grpc" and item.get("containerPort") == 9105 for item in ports if isinstance(item, dict)):
        raise AssertionError("model-service must expose named fetcher-grpc container port 9105")
    volumes = template.get("volumes") or []
    tls_volume = next((item for item in volumes if item.get("name") == "model-service-tls"), None)
    secret = (tls_volume or {}).get("secret") or {}
    if secret.get("secretName") != "model-service-grpc-tls" or secret.get("optional") is not False:
        raise AssertionError("model-service mTLS certificate Secret must be mandatory")


def validate_gateway_manifest(path: Path) -> None:
    """Require the gateway to pass the dedicated mTLS fetcher endpoint.

    The address/image/HTTP policy values live in the non-secret materialization
    ConfigMap. References are mandatory so a missing ConfigMap cannot silently
    make runtime fall back to the normal (non-fetcher) gRPC port.
    """
    documents = _documents(path)
    gateway = _find(documents, "Deployment", "ani-gateway")
    template = ((gateway.get("spec") or {}).get("template") or {}).get("spec") or {}
    containers = template.get("containers") or []
    if len(containers) != 1:
        raise AssertionError("ani-gateway must have one container")
    env = {str(item.get("name")): item for item in containers[0].get("env", []) if isinstance(item, dict)}
    expected = {
        "MODEL_SERVICE_GRPC_ADDR": "model_service_grpc_addr",
        "MODEL_FETCHER_GRPC_ADDR": "model_fetcher_grpc_addr",
        "MODEL_FETCHER_IMAGE_REF": "model_fetcher_image_ref",
        "MODEL_FETCHER_ALLOW_INSECURE_HTTP": "model_fetcher_allow_insecure_http",
    }
    for env_name, key in expected.items():
        entry = env.get(env_name)
        ref = (entry or {}).get("valueFrom") or {}
        config_ref = ref.get("configMapKeyRef") or {}
        if config_ref.get("name") != "ani-inference-materialization" or config_ref.get("key") != key:
            raise AssertionError(f"gateway {env_name} must reference ConfigMap key {key}")
        if config_ref.get("optional") is True:
            raise AssertionError(f"gateway {env_name} ConfigMap reference must be mandatory")


def validate_gateway_rbac_manifest(path: Path) -> None:
    """Ensure the gateway ClusterRole does not become a cert-manager bypass.

    Certificate issuance is deliberately delegated to a pre-provisioned
    tenant Role/RoleBinding. A ClusterRole rule for cert-manager certificates
    (including a wildcard rule that covers them) would silently turn that
    namespace boundary into a cluster-wide privilege.
    """
    documents = _documents(path)
    role = _find(documents, "ClusterRole", "ani-gateway-core-provider")
    for rule in role.get("rules") or []:
        if not isinstance(rule, dict):
            continue
        groups = {str(group) for group in (rule.get("apiGroups") or [])}
        resources = {str(resource) for resource in (rule.get("resources") or [])}
        if "cert-manager.io" in groups or ("*" in groups and ("certificates" in resources or "*" in resources)):
            raise AssertionError("gateway ClusterRole must not grant cert-manager Certificate access")


def validate_model_service_network_policy(path: Path = MTLS_DEV) -> None:
    """Keep the model-service control and fetcher ports separately fenced."""
    documents = _documents(path)
    policy = _find(documents, "NetworkPolicy", "model-service-fetcher-ingress")
    if (policy.get("metadata") or {}).get("namespace") != "ani-system":
        raise AssertionError("model-service ingress policy must stay in ani-system")
    spec = policy.get("spec") or {}
    if spec.get("podSelector") != {"matchLabels": {"app.kubernetes.io/name": "model-service"}}:
        raise AssertionError("model-service ingress policy must select only model-service pods")
    ingress = spec.get("ingress") or []
    control_plane_peers = [
        {
            "namespaceSelector": {"matchLabels": {"kubernetes.io/metadata.name": "ani-system"}},
            "podSelector": {"matchLabels": {"app.kubernetes.io/name": "ani-gateway"}},
        },
        {
            "namespaceSelector": {"matchLabels": {"kubernetes.io/metadata.name": "ani-system"}},
            "podSelector": {"matchLabels": {"app.kubernetes.io/name": "inference-service"}},
        },
    ]
    fetcher_peers = [
        {
            "namespaceSelector": {},
            "podSelector": {"matchLabels": {"ani.dev/model-fetcher-client": "true"}},
        }
    ]
    rules_by_port: dict[int, list[dict[str, Any]]] = {}
    for rule in ingress:
        if not isinstance(rule, dict):
            continue
        ports = rule.get("ports") or []
        for port in ports:
            if not isinstance(port, dict) or port.get("protocol") != "TCP":
                continue
            try:
                port_number = int(port.get("port"))
            except (TypeError, ValueError):
                continue
            rules_by_port[port_number] = rule.get("from") or []
    if rules_by_port.get(9103) != control_plane_peers:
        raise AssertionError("model-service 9103 must allow only ani-gateway and inference-service in ani-system")
    if rules_by_port.get(9105) != fetcher_peers:
        raise AssertionError("model-service 9105 must allow only labeled model-fetcher clients")


def validate_mtls_dev(path: Path = MTLS_DEV) -> None:
    """Validate the development-only cert-manager resource shape.

    The file contains references only; private keys are generated by
    cert-manager in-cluster and must never be committed.
    """
    documents = _documents(path)
    required = {
        ("Issuer", "ani-model-repository-selfsigned"),
        ("Certificate", "ani-model-repository-ca"),
        ("ClusterIssuer", "ani-model-repository-ca"),
        ("Certificate", "model-service-grpc"),
        ("Certificate", "model-fetcher-client"),
        ("NetworkPolicy", "model-service-fetcher-ingress"),
    }
    actual = {(str(d.get("kind")), str((d.get("metadata") or {}).get("name"))) for d in documents}
    if not required.issubset(actual):
        raise AssertionError(f"mTLS dev manifest missing resources: {sorted(required - actual)}")
    for document in documents:
        if document.get("kind") not in {"Issuer", "ClusterIssuer", "Certificate", "NetworkPolicy", "Role", "RoleBinding"}:
            raise AssertionError("mTLS dev manifest contains an unexpected resource")
        namespace = (document.get("metadata") or {}).get("namespace")
        if document.get("kind") == "ClusterIssuer":
            if namespace is not None:
                raise AssertionError("ClusterIssuer must not set a namespace")
        elif document.get("kind") in {"Role", "RoleBinding"}:
            if namespace != "ani-tenant-00000000-0000-0000-0000-000000000001":
                raise AssertionError("tenant fetcher RBAC resources must stay in the tenant namespace")
        elif document.get("kind") == "Issuer" and (document.get("metadata") or {}).get("name") == "ani-model-repository-selfsigned":
            if namespace != "cert-manager":
                raise AssertionError("self-signed bootstrap Issuer must stay in cert-manager namespace")
        elif document.get("kind") == "Certificate" and (document.get("metadata") or {}).get("name") == "ani-model-repository-ca":
            if namespace != "cert-manager":
                raise AssertionError("development CA must be stored in cert-manager namespace")
        elif namespace != "ani-system":
            raise AssertionError("mTLS service resources must stay in ani-system")
    ca = _find(documents, "Certificate", "ani-model-repository-ca")
    if (ca.get("spec") or {}).get("isCA") is not True:
        raise AssertionError("development CA certificate must set isCA=true")
    cluster_issuer = _find(documents, "ClusterIssuer", "ani-model-repository-ca")
    if ((cluster_issuer.get("spec") or {}).get("ca") or {}).get("secretName") != "ani-model-repository-ca":
        raise AssertionError("ClusterIssuer must reference the development CA Secret")
    service = _find(documents, "Certificate", "model-service-grpc")
    if "model-service.ani-system.svc.cluster.local" not in ((service.get("spec") or {}).get("dnsNames") or []):
        raise AssertionError("model-service certificate must cover the cluster DNS name")
    client = _find(documents, "Certificate", "model-fetcher-client")
    if not any(str(uri).startswith("spiffe://ani.dev/") for uri in ((client.get("spec") or {}).get("uris") or [])):
        raise AssertionError("fetcher certificate must carry a workload identity URI")
    if (client.get("spec") or {}).get("usages") != ["client auth"]:
        raise AssertionError("fetcher certificate must be constrained to client auth usage")


def validate_tenant_rbac_manifest(path: Path = MTLS_DEV) -> None:
    """Require pre-provisioned, namespace-scoped identity permissions.

    The Gateway service account must not gain a ClusterRole rule for
    cert-manager Certificates merely to create a tenant fetcher identity. A
    platform bootstrapper renders this Role/RoleBinding in each tenant
    namespace instead. This gate intentionally checks only the development
    tenant fixture; production provisioning must instantiate the same shape
    for every tenant before object-backed workloads are enabled.
    """
    documents = _documents(path)
    tenant_namespace = "ani-tenant-00000000-0000-0000-0000-000000000001"
    role = _find(documents, "Role", "ani-model-fetcher-provisioner")
    if (role.get("metadata") or {}).get("namespace") != tenant_namespace:
        raise AssertionError("tenant fetcher Role must be namespaced to the tenant")
    expected_rules = [
        {
            "apiGroups": ["cert-manager.io"],
            "resources": ["certificates"],
            "verbs": ["get", "list", "watch", "create", "update", "patch", "delete"],
        },
        {
            "apiGroups": [""],
            "resources": ["serviceaccounts"],
            "verbs": ["get", "list", "watch", "create", "update", "patch", "delete"],
        },
    ]
    if (role.get("rules") or []) != expected_rules:
        raise AssertionError("tenant fetcher Role must grant only Certificate and ServiceAccount namespaced verbs")
    binding = _find(documents, "RoleBinding", "ani-model-fetcher-provisioner")
    if (binding.get("metadata") or {}).get("namespace") != tenant_namespace:
        raise AssertionError("tenant fetcher RoleBinding must be namespaced to the tenant")
    if binding.get("roleRef") != {
        "apiGroup": "rbac.authorization.k8s.io",
        "kind": "Role",
        "name": "ani-model-fetcher-provisioner",
    }:
        raise AssertionError("tenant fetcher RoleBinding must reference the provisioner Role")
    if binding.get("subjects") != [{"kind": "ServiceAccount", "name": "ani-gateway", "namespace": "ani-system"}]:
        raise AssertionError("tenant fetcher RoleBinding must bind only the ani-gateway ServiceAccount")


def validate(root: Path = ROOT) -> None:
    # Keep the short entry point for existing make/CI callers while exposing a
    # descriptive API to focused tests and downstream validators.
    validate_workspace(root)


def main() -> int:
    validate()
    print("model repository remote import contract valid (live image/Secret values remain explicit prerequisites)")
    return 0


if __name__ == "__main__":
    raise SystemExit(main())
