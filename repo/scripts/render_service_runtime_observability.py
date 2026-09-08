#!/usr/bin/env python3
"""Render the seven-service P0 Kubernetes bundle from immutable image inputs."""

from __future__ import annotations

import argparse
import json
import re
import sys
from pathlib import Path
from typing import Any

import yaml


NAMESPACE_PREFIX = "ani-service-observability-e2e-"
NAMESPACE_RE = re.compile(r"^ani-service-observability-e2e-[a-z0-9](?:[-a-z0-9]*[a-z0-9])?$")
DNS_LABEL_RE = re.compile(r"^[a-z0-9](?:[-a-z0-9]*[a-z0-9])?$")
VERSION_RE = re.compile(r"^[A-Za-z0-9](?:[-A-Za-z0-9_.]*[A-Za-z0-9])?$")
IMAGE_RE = re.compile(r"^[a-z0-9]+(?:[._-][a-z0-9]+)*(?::[0-9]+)?(?:/[a-z0-9]+(?:[._-][a-z0-9]+)*)+@sha256:[0-9a-f]{64}$")


def load_inventory(path: Path) -> dict[str, Any]:
    document = yaml.safe_load(path.read_text(encoding="utf-8"))
    if not isinstance(document, dict) or document.get("kind") != "ServiceRuntimeObservabilityBundle":
        raise ValueError("inventory must be a ServiceRuntimeObservabilityBundle")
    spec = document.get("spec")
    if not isinstance(spec, dict) or not isinstance(spec.get("services"), list):
        raise ValueError("inventory spec.services must be a list")
    return spec


def load_images(path: Path, expected_names: set[str]) -> dict[str, str]:
    raw = json.loads(path.read_text(encoding="utf-8"))
    if not isinstance(raw, dict) or set(raw) != expected_names:
        raise ValueError(f"image set must match canonical services: {sorted(expected_names)}")
    images = {str(name): str(reference) for name, reference in raw.items()}
    for name, reference in images.items():
        if not IMAGE_RE.fullmatch(reference):
            raise ValueError(f"{name} image must be an immutable digest reference")
    return images


def _field_env(name: str, field_path: str) -> dict[str, Any]:
    return {
        "name": name,
        "valueFrom": {"fieldRef": {"apiVersion": "v1", "fieldPath": field_path}},
    }


def _container_env(service: dict[str, Any], secret_name: str) -> list[dict[str, Any]]:
    env = [
        _field_env("ANI_SERVICE_NAME", "metadata.labels['ani.dev/service-name']"),
        _field_env("ANI_SERVICE_VERSION", "metadata.labels['app.kubernetes.io/version']"),
        _field_env("POD_UID", "metadata.uid"),
    ]
    health_port = next(port["port"] for port in service["ports"] if port["name"] == "health")
    env.append({"name": "HEALTH_PORT", "value": str(health_port)})
    for name, value in sorted((service.get("env") or {}).items()):
        env.append({"name": name, "value": str(value)})
    for name, key in sorted((service.get("secretEnv") or {}).items()):
        env.append(
            {
                "name": name,
                "valueFrom": {
                    "secretKeyRef": {"name": secret_name, "key": str(key), "optional": False}
                },
            }
        )
    return env


def _probes(probe: dict[str, Any]) -> tuple[dict[str, Any], dict[str, Any]]:
    transport = dict(probe)
    readiness = {
        **transport,
        "initialDelaySeconds": 3,
        "periodSeconds": 5,
        "timeoutSeconds": 2,
        "failureThreshold": 6,
    }
    liveness = {
        **transport,
        "initialDelaySeconds": 10,
        "periodSeconds": 10,
        "timeoutSeconds": 2,
        "failureThreshold": 3,
    }
    return readiness, liveness


def _resources(
    service: dict[str, Any],
    namespace: str,
    version: str,
    image: str,
    secret_name: str,
    run_id: str,
    image_pull_secret: str | None,
) -> list[dict[str, Any]]:
    name = service["name"]
    labels = {
        "app.kubernetes.io/name": name,
        "app.kubernetes.io/part-of": "ani-platform",
        "app.kubernetes.io/version": version,
        "ani.dev/service-name": name,
        "ani.dev/metrics-scrape": "true",
        "ani.dev/profile": "service-runtime-observability-p0",
        "ani.dev/run-id": run_id,
    }
    selector = {"app.kubernetes.io/name": name, "ani.dev/profile": "service-runtime-observability-p0"}
    readiness, liveness = _probes(service["probe"])
    container = {
        "name": name,
        "image": image,
        "imagePullPolicy": "IfNotPresent",
        "ports": [
            {"name": port["name"], "containerPort": port["port"], "protocol": "TCP"}
            for port in service["ports"]
        ],
        "env": _container_env(service, secret_name),
        "readinessProbe": readiness,
        "livenessProbe": liveness,
        "resources": {
            "requests": {"cpu": "50m", "memory": "64Mi"},
            "limits": {"cpu": "1", "memory": "512Mi"},
        },
        "securityContext": {
            "allowPrivilegeEscalation": False,
            "readOnlyRootFilesystem": True,
            "capabilities": {"drop": ["ALL"]},
        },
    }
    pod_spec: dict[str, Any] = {
        "automountServiceAccountToken": False,
        "securityContext": {
            "runAsNonRoot": True,
            "runAsUser": 65532,
            "runAsGroup": 65532,
            "seccompProfile": {"type": "RuntimeDefault"},
        },
        "containers": [container],
    }
    if image_pull_secret:
        pod_spec["imagePullSecrets"] = [{"name": image_pull_secret}]
    deployment = {
        "apiVersion": "apps/v1",
        "kind": "Deployment",
        "metadata": {"name": name, "namespace": namespace, "labels": labels},
        "spec": {
            "replicas": 1,
            "selector": {"matchLabels": selector},
            "template": {
                "metadata": {"labels": labels},
                "spec": pod_spec,
            },
        },
    }
    service_resource = {
        "apiVersion": "v1",
        "kind": "Service",
        "metadata": {"name": name, "namespace": namespace, "labels": labels},
        "spec": {
            "type": "ClusterIP",
            "selector": selector,
            "ports": [
                {
                    "name": port["name"],
                    "port": port["port"],
                    "targetPort": port["name"],
                    "protocol": "TCP",
                }
                for port in service["ports"]
            ],
        },
    }
    return [deployment, service_resource]


def render(
    inventory_path: Path,
    namespace: str,
    version: str,
    images: dict[str, str],
    image_pull_secret: str | None = None,
) -> str:
    if not NAMESPACE_RE.fullmatch(namespace):
        raise ValueError("namespace must be an isolated ani-service-observability-e2e-<run-id> namespace")
    if not VERSION_RE.fullmatch(version) or version in {"latest", "(devel)"}:
        raise ValueError("version must be a concrete Kubernetes label value")
    if image_pull_secret is not None and not DNS_LABEL_RE.fullmatch(image_pull_secret):
        raise ValueError("image pull secret must be a Kubernetes DNS label")
    run_id = namespace.removeprefix(NAMESPACE_PREFIX)

    spec = load_inventory(inventory_path)
    services = spec["services"]
    names = {service.get("name") for service in services}
    if None in names or len(names) != len(services):
        raise ValueError("inventory service names must be present and unique")
    if set(images) != names:
        raise ValueError(f"image set must match canonical services: {sorted(names)}")
    for name, reference in images.items():
        if not IMAGE_RE.fullmatch(reference):
            raise ValueError(f"{name} image must be an immutable digest reference")

    secret_name = str(spec.get("secretName", ""))
    if not secret_name:
        raise ValueError("inventory spec.secretName is required")
    documents: list[dict[str, Any]] = []
    for service in services:
        documents.extend(
            _resources(
                service,
                namespace,
                version,
                images[service["name"]],
                secret_name,
                run_id,
                image_pull_secret,
            )
        )
    return yaml.safe_dump_all(documents, sort_keys=False, explicit_start=True)


def parse_args() -> argparse.Namespace:
    root = Path(__file__).resolve().parents[1]
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument(
        "--inventory",
        type=Path,
        default=root / "deploy/real-k8s-lab/service-runtime-observability-p0.yaml",
    )
    parser.add_argument("--namespace", required=True)
    parser.add_argument("--version", required=True)
    parser.add_argument("--images-file", type=Path, required=True)
    parser.add_argument("--image-pull-secret")
    return parser.parse_args()


def main() -> int:
    args = parse_args()
    try:
        spec = load_inventory(args.inventory)
        expected = {service["name"] for service in spec["services"]}
        images = load_images(args.images_file, expected)
        sys.stdout.write(
            render(
                args.inventory,
                args.namespace,
                args.version,
                images,
                image_pull_secret=args.image_pull_secret,
            )
        )
    except (OSError, KeyError, TypeError, ValueError, json.JSONDecodeError, yaml.YAMLError) as exc:
        print(f"render failed: {exc}", file=sys.stderr)
        return 2
    return 0


if __name__ == "__main__":
    raise SystemExit(main())
