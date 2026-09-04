#!/usr/bin/env python3
"""Validate OBS-RUNTIME-P0 contracts and run promtool from the pinned image."""

from __future__ import annotations

import argparse
import hashlib
import os
import re
import subprocess
import sys
import tempfile
from pathlib import Path
from typing import Any

import yaml

sys.path.insert(0, str(Path(__file__).resolve().parent))
import render_service_runtime_observability as renderer


SERVICES = {
    "ani-gateway": {"management": 9200, "probe": ("httpGet", "http"), "probe_path": "/healthz"},
    "auth-service": {"management": 9201, "probe": ("tcpSocket", "grpc")},
    "model-service": {"management": 9203, "probe": ("tcpSocket", "grpc")},
    "task-service": {"management": 9204, "probe": ("tcpSocket", "grpc")},
    "inference-service": {"management": 9204, "probe": ("tcpSocket", "grpc")},
    "tenant-service": {"management": 9205, "probe": ("tcpSocket", "grpc")},
    "metering-service": {"management": 9210, "probe": ("tcpSocket", "health")},
}
SERVICE_ORDER = list(SERVICES)
SERVICE_REGEX = "|".join(SERVICE_ORDER)
PROMETHEUS_IMAGE = (
    "prom/prometheus:v2.55.1@sha256:"
    "2659f4c2ebb718e7695cb9b25ffa7d6be64db013daba13e05c875451cf51b0d3"
)
INVENTORY_PATH = Path("deploy/real-k8s-lab/service-runtime-observability-p0.yaml")
PROMETHEUS_PATH = Path("deploy/real-k8s-lab/sprint13-instance-observability-prometheus-live.yaml")
ALLOWED_RUNTIMEADMIN_IMPORTERS = {
    "pkg/bootstrap/runtimeadmin.go",
    "services/pkg/bootstrap/runtimeadmin.go",
    "services/ani-gateway/runtime_admin.go",
}
FORBIDDEN_PREFIXES = (
    "repo/services/reconcile-worker/",
    "repo/services/envoy-authz-adapter/",
    "repo/services/kb-service/",
)
FIXED_PLAN_REPO_PATH = Path(
    "services/tasks/modules/plan/plan-service-runtime-observability-p0-p1.md"
)
FIXED_PLAN_GIT_PATH = f"repo/{FIXED_PLAN_REPO_PATH.as_posix()}"
FIXED_PLAN_SHA256 = "c9c16e4e5df3a395b22fc8060a5811dab3a508d5d3baa3ba7fc5f9ed1b575034"
P0_EXACT_CHANGED_PATH_ALLOWLIST = frozenset(
    {
        ".github/workflows/ci.yml",
        "ANI-06-开发计划.md",
        "repo/.github/workflows/build-image.yml",
        "repo/CURRENT-SPRINT.md",
        "repo/Makefile",
        "repo/README.md",
        "repo/api/core-v1-compatibility-baseline.yaml",
        "repo/api/openapi/v1.yaml",
        "repo/deploy/docker/README.md",
        "repo/deploy/helm/ani-platform/README.md",
        "repo/deploy/real-k8s-lab/service-runtime-observability-p0.yaml",
        "repo/deploy/real-k8s-lab/sprint13-instance-observability-prometheus-live.yaml",
        "repo/development-records/README.md",
        "repo/development-records/live-evidence/service-runtime-observability-p0-live-20260904.json",
        "repo/development-records/service-runtime-observability-p0.md",
        "repo/docs/api/core.html",
        "repo/docs/api/index.html",
        "repo/docs/operations/service-runtime-observability.md",
        "repo/go.work",
        "repo/go.work.sum",
        "repo/pkg/adapters/runtime/prometheus_platform_service_health_reader.go",
        "repo/pkg/adapters/runtime/prometheus_platform_service_health_reader_test.go",
        "repo/pkg/bootstrap/probes.go",
        "repo/pkg/bootstrap/probes_test.go",
        "repo/pkg/bootstrap/runtimeadmin.go",
        "repo/pkg/bootstrap/server.go",
        "repo/pkg/go.mod",
        "repo/pkg/go.sum",
        "repo/pkg/ports/platform_service_health_reader.go",
        "repo/runtimeadmin/go.mod",
        "repo/runtimeadmin/go.sum",
        "repo/runtimeadmin/handler.go",
        "repo/runtimeadmin/metrics.go",
        "repo/runtimeadmin/readiness.go",
        "repo/runtimeadmin/runtime.go",
        "repo/runtimeadmin/runtime_test.go",
        "repo/runtimeadmin/sanitize.go",
        "repo/scripts/fixtures/service_runtime_observability_l2_prometheus.yml",
        "repo/scripts/render_service_runtime_observability.py",
        "repo/scripts/render_service_runtime_observability_l3.py",
        "repo/scripts/render_service_runtime_observability_l3_test.py",
        "repo/scripts/render_service_runtime_observability_test.py",
        "repo/scripts/run_service_runtime_observability_l3.py",
        "repo/scripts/run_service_runtime_observability_l3_test.py",
        "repo/scripts/validate_ci_workflow.py",
        "repo/scripts/validate_ci_workflow_test.py",
        "repo/scripts/validate_generated_idempotence.py",
        "repo/scripts/validate_generated_idempotence_test.py",
        "repo/scripts/validate_runtime_image_workflow.py",
        "repo/scripts/validate_runtime_image_workflow_test.py",
        "repo/scripts/validate_runtime_observability_sbom.py",
        "repo/scripts/validate_runtime_observability_sbom_test.py",
        "repo/scripts/validate_service_runtime_observability.py",
        "repo/scripts/validate_service_runtime_observability_test.py",
        "repo/sdks/core/go/anisdk/client.go",
        "repo/sdks/core/java/src/main/java/com/kubercloud/ani/core/ApiClient.java",
        "repo/sdks/core/python/kubercloud_ani_core/client.py",
        "repo/sdks/core/sdk-metadata.json",
        "repo/sdks/core/typescript/src/index.mjs",
        "repo/sdks/core/typescript/src/index.ts",
        "repo/services/ani-gateway/Dockerfile",
        "repo/services/ani-gateway/go.mod",
        "repo/services/ani-gateway/go.sum",
        "repo/services/ani-gateway/internal/authz/zz_generated_core_policies.go",
        "repo/services/ani-gateway/internal/router/health_test.go",
        "repo/services/ani-gateway/internal/router/platform_service_health.go",
        "repo/services/ani-gateway/internal/router/platform_service_health_test.go",
        "repo/services/ani-gateway/internal/router/router.go",
        "repo/services/ani-gateway/main.go",
        "repo/services/ani-gateway/main_test.go",
        "repo/services/ani-gateway/platform_service_health_runtime.go",
        "repo/services/ani-gateway/runtime_admin.go",
        "repo/services/auth-service/Dockerfile",
        "repo/services/inference-service/Dockerfile",
        "repo/services/metering-service/Dockerfile",
        "repo/services/model-service/Dockerfile",
        "repo/services/pkg/bootstrap/probes_test.go",
        "repo/services/pkg/bootstrap/runtimeadmin.go",
        "repo/services/pkg/bootstrap/server.go",
        "repo/services/pkg/go.mod",
        "repo/services/pkg/go.sum",
        "repo/services/task-service/Dockerfile",
        "repo/services/tenant-service/Dockerfile",
    }
)


def load_inventory(root: Path) -> dict[str, Any]:
    return renderer.load_inventory(root / INVENTORY_PATH)


def load_prometheus_manifest(root: Path) -> list[dict[str, Any]]:
    return [
        document
        for document in yaml.safe_load_all((root / PROMETHEUS_PATH).read_text(encoding="utf-8"))
        if document
    ]


def load_prometheus_config(root: Path) -> dict[str, Any]:
    for document in load_prometheus_manifest(root):
        if document.get("kind") == "ConfigMap" and document.get("metadata", {}).get("name") == "sprint13-prometheus-config":
            config = yaml.safe_load(document["data"]["prometheus.yml"])
            if isinstance(config, dict):
                return config
    raise ValueError("sprint13-prometheus-config prometheus.yml not found")


def validate_inventory(inventory: dict[str, Any]) -> list[str]:
    errors: list[str] = []
    services = inventory.get("services")
    if not isinstance(services, list):
        return ["inventory services must be a list"]
    names = [service.get("name") for service in services]
    if names != SERVICE_ORDER:
        errors.append(f"inventory service order/set must be {SERVICE_ORDER}, got {names}")
    if inventory.get("secretName") != "ani-service-runtime-observability-p0":
        errors.append("inventory must use the fixed runtime Secret name")
    for service in services:
        name = service.get("name")
        if name not in SERVICES:
            continue
        ports = {port.get("name"): port.get("port") for port in service.get("ports", [])}
        if ports.get("health") != SERVICES[name]["management"]:
            errors.append(f"{name} health port must be {SERVICES[name]['management']}")
        probe_kind, probe_port = SERVICES[name]["probe"]
        probe = service.get("probe") or {}
        if set(probe) != {probe_kind} or probe[probe_kind].get("port") != probe_port:
            errors.append(f"{name} probe must preserve {probe_kind} port {probe_port}")
        probe_path = SERVICES[name].get("probe_path")
        if probe_path is not None and probe.get(probe_kind, {}).get("path") != probe_path:
            errors.append(f"{name} probe path must be {probe_path}")
        secret_env = service.get("secretEnv") or {}
        if name == "tenant-service":
            required_secret_env = {"DATABASE_URL"}
        elif name == "ani-gateway":
            required_secret_env = {"DATABASE_URL", "GATEWAY_REDIS_URL"}
        else:
            required_secret_env = {"DATABASE_URL", "NATS_URL", "REDIS_URL"}
        missing = required_secret_env - set(secret_env)
        if missing:
            errors.append(f"{name} is missing required Secret env names {sorted(missing)}")
    return errors


def validate_prometheus_config(config: dict[str, Any]) -> list[str]:
    errors: list[str] = []
    jobs = [job for job in config.get("scrape_configs", []) if job.get("job_name") == "ani-components"]
    if len(jobs) != 1:
        return [f"Prometheus must contain exactly one ani-components job, got {len(jobs)}"]
    job = jobs[0]
    expected_scalars = {"scrape_interval": "15s", "scrape_timeout": "5s", "metrics_path": "/metrics"}
    for key, expected in expected_scalars.items():
        if job.get(key) != expected:
            errors.append(f"ani-components {key} must be {expected!r}")
    if job.get("honor_labels"):
        errors.append("ani-components must not enable honor_labels")
    discovery = job.get("kubernetes_sd_configs") or []
    if discovery != [{"role": "pod", "namespaces": {"names": ["ani-system"]}}]:
        errors.append("ani-components must use one pod discovery scoped to ani-system")
    relabel = job.get("relabel_configs") or []
    required = [
        (["__meta_kubernetes_pod_label_ani_dev_metrics_scrape"], "true", "keep", None),
        (["__meta_kubernetes_pod_container_port_name"], "health", "keep", None),
        (["__meta_kubernetes_pod_phase"], "Running", "keep", None),
        (["__meta_kubernetes_pod_label_ani_dev_service_name"], SERVICE_REGEX, "keep", None),
        (["__meta_kubernetes_pod_label_ani_dev_service_name"], None, None, "ani_service_name"),
        (["__meta_kubernetes_pod_label_app_kubernetes_io_version"], None, None, "k8s_service_version"),
        (["__meta_kubernetes_namespace"], None, None, "kubernetes_namespace"),
        (["__meta_kubernetes_pod_name"], None, None, "pod"),
    ]
    actual = [
        (item.get("source_labels"), item.get("regex"), item.get("action"), item.get("target_label"))
        for item in relabel
    ]
    if actual != required:
        errors.append("ani-components relabel contract does not match the fixed opt-in/whitelist/target labels")
    return errors


def validate_prometheus_manifest(root: Path) -> list[str]:
    errors = validate_prometheus_config(load_prometheus_config(root))
    images = [
        container.get("image")
        for document in load_prometheus_manifest(root)
        if document.get("kind") == "Deployment"
        for container in document.get("spec", {}).get("template", {}).get("spec", {}).get("containers", [])
        if container.get("name") == "prometheus"
    ]
    if images != [PROMETHEUS_IMAGE]:
        errors.append(f"Prometheus image must be pinned exactly to {PROMETHEUS_IMAGE}")
    return errors


def _dummy_images() -> dict[str, str]:
    return {
        name: f"registry.example.invalid/ani/{name}@sha256:{index:064x}"
        for index, name in enumerate(SERVICE_ORDER, start=1)
    }


def validate_rendered_bundle(root: Path) -> list[str]:
    errors: list[str] = []
    rendered = renderer.render(
        root / INVENTORY_PATH,
        "ani-service-observability-e2e-contract",
        "v0.0.0-contract",
        _dummy_images(),
    )
    documents = [document for document in yaml.safe_load_all(rendered) if document]
    deployments = {doc["metadata"]["name"]: doc for doc in documents if doc.get("kind") == "Deployment"}
    services = {doc["metadata"]["name"]: doc for doc in documents if doc.get("kind") == "Service"}
    if set(deployments) != set(SERVICES) or set(services) != set(SERVICES):
        return ["rendered bundle must contain exactly seven canonical Deployments and Services"]
    for name, contract in SERVICES.items():
        deployment = deployments[name]
        pod = deployment["spec"]["template"]
        container = pod["spec"]["containers"][0]
        labels = pod["metadata"]["labels"]
        env = {entry["name"]: entry for entry in container["env"]}
        ports = {entry["name"]: entry["containerPort"] for entry in container["ports"]}
        service = services[name]
        service_ports = {entry["name"]: entry for entry in service["spec"]["ports"]}
        if labels.get("ani.dev/service-name") != name or labels.get("ani.dev/metrics-scrape") != "true":
            errors.append(f"{name} must carry canonical service name and explicit scrape opt-in labels")
        if labels.get("app.kubernetes.io/part-of") != "ani-platform":
            errors.append(f"{name} must be part of ani-platform")
        if ports.get("health") != contract["management"]:
            errors.append(f"{name} rendered management port mismatch")
        if service_ports.get("health", {}).get("targetPort") != "health":
            errors.append(f"{name} Service health targetPort must be the named health port")
        if service["spec"].get("type") != "ClusterIP":
            errors.append(f"{name} observability Service must remain internal ClusterIP")
        for env_name, field_path in {
            "ANI_SERVICE_NAME": "metadata.labels['ani.dev/service-name']",
            "ANI_SERVICE_VERSION": "metadata.labels['app.kubernetes.io/version']",
            "POD_UID": "metadata.uid",
        }.items():
            actual = env.get(env_name, {}).get("valueFrom", {}).get("fieldRef", {}).get("fieldPath")
            if actual != field_path:
                errors.append(f"{name} {env_name} must use Downward API field {field_path}")
        for entry in env.values():
            secret_ref = entry.get("valueFrom", {}).get("secretKeyRef")
            if secret_ref and (secret_ref.get("name") != "ani-service-runtime-observability-p0" or secret_ref.get("optional") is not False):
                errors.append(f"{name} Secret references must use the fixed required Secret")
    return errors


def validate_sources(root: Path) -> list[str]:
    errors: list[str] = []
    importers: set[str] = set()
    for base in (root / "pkg", root / "services"):
        for path in base.rglob("*.go"):
            if "github.com/kubercloud/ani/runtimeadmin" in path.read_text(encoding="utf-8"):
                importers.add(path.relative_to(root).as_posix())
    if importers != ALLOWED_RUNTIMEADMIN_IMPORTERS:
        errors.append(
            f"runtimeadmin import deny-list violation: expected {sorted(ALLOWED_RUNTIMEADMIN_IMPORTERS)}, got {sorted(importers)}"
        )
    runtime_source = "\n".join(
        path.read_text(encoding="utf-8") for path in (root / "runtimeadmin").glob("*.go")
    )
    if re.search(r'github\.com/kubercloud/ani/(?:pkg|services)', runtime_source):
        errors.append("runtimeadmin must remain neutral and cannot import Core or Services modules")
    source_checks = {
        "ani-gateway": (root / "services/ani-gateway/runtime_admin.go", 'gatewayCanonicalServiceName = "ani-gateway"'),
        **{
            name: (root / f"services/{name}/internal/config/config.go", f'ServiceName: "{name}"')
            for name in SERVICE_ORDER
            if name != "ani-gateway"
        },
    }
    for name, (path, marker) in source_checks.items():
        if marker not in path.read_text(encoding="utf-8"):
            errors.append(f"{name} canonical runtime identity is missing from {path.relative_to(root)}")
    makefile = (root / "Makefile").read_text(encoding="utf-8")
    for name in SERVICE_ORDER:
        target = "gateway" if name == "ani-gateway" else name
        if f"build-{target}:" not in makefile or f"image-{target}:" not in makefile:
            errors.append(f"Makefile must provide controlled build/image targets for {name}")
        if not (root / f"services/{name}/Dockerfile").is_file():
            errors.append(f"{name} Dockerfile is missing")
    for metric in (
        "ani_workload_reconcile_ticks_total",
        "ani_workload_reconcile_successes_total",
        "ani_workload_reconcile_failures_total",
        "ani_workload_reconcile_backoff_skips_total",
    ):
        if metric not in (root / "pkg/bootstrap/runtimeadmin.go").read_text(encoding="utf-8"):
            errors.append(f"legacy metric {metric} must be preserved")
    return errors


def validate_forbidden_changes(changed: set[str]) -> list[str]:
    return [
        f"excluded service changed: {path}"
        for path in sorted(changed)
        if path.startswith(FORBIDDEN_PREFIXES)
    ]


def validate_changed_path_allowlist(changed: set[str]) -> list[str]:
    return [
        f"outside OBS-RUNTIME-P0 exact allowlist: {path}"
        for path in sorted(changed)
        if path not in P0_EXACT_CHANGED_PATH_ALLOWLIST and path != FIXED_PLAN_GIT_PATH
    ]


# OBS-RUNTIME-P0 批次锚点：批次计划与批次记录。
# 批次精确 allowlist 只约束"延续/修订本批次"的 PR（触碰锚点文件者），
# 避免批次门禁外溢成全仓库全局锁（ANI-15：开发并行，main 串行收口，
# 不存在全局锁）；FORBIDDEN_PREFIXES 全域禁令仍然无条件生效。
P0_BATCH_ANCHOR_GIT_PATHS = frozenset(
    {
        FIXED_PLAN_GIT_PATH,
        "repo/development-records/service-runtime-observability-p0.md",
    }
)


def should_enforce_changed_path_allowlist(changed: set[str]) -> bool:
    return bool(changed & P0_BATCH_ANCHOR_GIT_PATHS)


def validate_fixed_plan(root: Path) -> list[str]:
    plan_path = root / FIXED_PLAN_REPO_PATH
    # The Goal plan is a user-owned, intentionally untracked input. Validate it
    # when present locally, but do not require it in a clean CI checkout.
    if not plan_path.is_file():
        return []
    actual_hash = hashlib.sha256(plan_path.read_bytes()).hexdigest()
    if actual_hash == FIXED_PLAN_SHA256:
        return []
    return [
        f"fixed plan hash mismatch for {FIXED_PLAN_GIT_PATH}: "
        f"expected {FIXED_PLAN_SHA256}, got {actual_hash}"
    ]


def changed_paths(root: Path, base: str | None) -> set[str]:
    git_root_result = subprocess.run(
        ["git", "rev-parse", "--show-toplevel"],
        cwd=root,
        check=True,
        capture_output=True,
        text=True,
    )
    git_root = Path(git_root_result.stdout.strip())
    commands = []
    if base:
        commands.append(["git", "diff", "--name-only", "-z", f"{base}...HEAD", "--"])
    commands.extend(
        [
            ["git", "diff", "--name-only", "-z", "HEAD", "--"],
            ["git", "ls-files", "--others", "--exclude-standard", "-z"],
        ]
    )
    changed: set[str] = set()
    for command in commands:
        result = subprocess.run(
            command,
            cwd=git_root,
            check=True,
            capture_output=True,
            encoding="utf-8",
        )
        changed.update(path for path in result.stdout.split("\0") if path)
    return changed


def run_promtool(root: Path) -> list[str]:
    config = load_prometheus_config(root)
    with tempfile.TemporaryDirectory(prefix="ani-promtool-") as temp_dir:
        temp_path = Path(temp_dir)
        config_path = temp_path / "prometheus.yml"
        config_path.write_text(yaml.safe_dump(config, sort_keys=False), encoding="utf-8")
        service_account_path = temp_path / "serviceaccount"
        service_account_path.mkdir()
        (service_account_path / "token").write_text("promtool-static-validation", encoding="utf-8")
        result = subprocess.run(
            [
                "docker",
                "run",
                "--rm",
                "--entrypoint",
                "promtool",
                "-v",
                f"{config_path}:/tmp/prometheus.yml:ro",
                "-v",
                f"{service_account_path}:/var/run/secrets/kubernetes.io/serviceaccount:ro",
                PROMETHEUS_IMAGE,
                "check",
                "config",
                "/tmp/prometheus.yml",
            ],
            cwd=root,
            capture_output=True,
            text=True,
        )
    if result.returncode != 0:
        detail = (result.stdout + result.stderr).strip()
        return [f"promtool failed: {detail}"]
    print(result.stdout.strip())
    return []


def validate_repository(root: Path, run_promtool: bool, base: str | None) -> list[str]:
    errors: list[str] = []
    try:
        inventory = load_inventory(root)
        errors.extend(validate_inventory(inventory))
        errors.extend(validate_rendered_bundle(root))
        errors.extend(validate_prometheus_manifest(root))
        errors.extend(validate_sources(root))
        errors.extend(validate_fixed_plan(root))
        changed = changed_paths(root, base)
        errors.extend(validate_forbidden_changes(changed))
        if should_enforce_changed_path_allowlist(changed):
            errors.extend(validate_changed_path_allowlist(changed))
        if run_promtool:
            errors.extend(globals()["run_promtool"](root))
    except (OSError, KeyError, TypeError, ValueError, subprocess.CalledProcessError, yaml.YAMLError) as exc:
        errors.append(str(exc))
    return errors


def parse_args() -> argparse.Namespace:
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("--root", type=Path, default=Path(__file__).resolve().parents[1])
    parser.add_argument("--base", default=os.environ.get("BASE_SHA") or None)
    parser.add_argument("--skip-promtool", action="store_true", help="Unit-test helper; CI/local gate must not use this")
    return parser.parse_args()


def main() -> int:
    args = parse_args()
    errors = validate_repository(args.root.resolve(), not args.skip_promtool, args.base)
    if errors:
        for error in errors:
            print(f"FAIL: {error}", file=sys.stderr)
        return 1
    print("service runtime observability P0 contract valid")
    return 0


if __name__ == "__main__":
    raise SystemExit(main())
