#!/usr/bin/env python3
"""Render the isolated OBS-RUNTIME-P0 L3 Kubernetes fixture.

The runtime Secret is deliberately not rendered: the live runner creates that
single object from ephemeral values without writing or printing secret data.
"""

from __future__ import annotations

import argparse
import json
import sys
from pathlib import Path
from typing import Any

import yaml

import render_service_runtime_observability as workloads


ROOT = Path(__file__).resolve().parents[1]
INVENTORY = ROOT / "deploy/real-k8s-lab/service-runtime-observability-p0.yaml"
MIGRATION_DIR = ROOT / "deploy/migrations"
RUNTIME_SECRET = "ani-service-runtime-observability-p0"
PROFILE = "service-runtime-observability-p0"
PROMETHEUS_NAME = "ani-service-observability-prometheus"
PROMETHEUS_IMAGE = (
    "docker.changqingyun.cn/ani/prometheus@sha256:"
    "2659f4c2ebb718e7695cb9b25ffa7d6be64db013daba13e05c875451cf51b0d3"
)
POSTGRES_IMAGE = (
    "docker.changqingyun.cn/ani/postgres@sha256:"
    "5660c2cbfea50c7a9127d17dc4e48543eedd3d7a41a595a2dfa572471e37e64c"
)
NATS_IMAGE = (
    "docker.changqingyun.cn/ani/nats@sha256:"
    "b83efabe3e7def1e0a4a31ec6e078999bb17c80363f881df35edc70fcb6bb927"
)
REDIS_IMAGE = (
    "docker.changqingyun.cn/ani/redis@sha256:"
    "ff02b58f971e7d7d156a1267e283fcbbeee91773b6aa36c49dac28ecfe28eadf"
)
SERVICE_PATTERN = (
    "ani-gateway|auth-service|model-service|task-service|"
    "inference-service|tenant-service|metering-service"
)


def fixture_images() -> dict[str, str]:
    """Return every pinned third-party image used by the isolated L3 fixture."""
    return {
        "nats": NATS_IMAGE,
        "postgres": POSTGRES_IMAGE,
        "prometheus": PROMETHEUS_IMAGE,
        "redis": REDIS_IMAGE,
    }


def _labels(run_id: str, name: str) -> dict[str, str]:
    return {
        "app.kubernetes.io/name": name,
        "app.kubernetes.io/part-of": "ani-platform",
        "ani.dev/profile": PROFILE,
        "ani.dev/run-id": run_id,
    }


def _secret_env(name: str, key: str) -> dict[str, Any]:
    return {
        "name": name,
        "valueFrom": {
            "secretKeyRef": {"name": RUNTIME_SECRET, "key": key, "optional": False}
        },
    }


def _service(namespace: str, run_id: str, name: str, port_name: str, port: int) -> dict[str, Any]:
    labels = _labels(run_id, name)
    return {
        "apiVersion": "v1",
        "kind": "Service",
        "metadata": {"name": name, "namespace": namespace, "labels": labels},
        "spec": {
            "type": "ClusterIP",
            "selector": {
                "app.kubernetes.io/name": name,
                "ani.dev/run-id": run_id,
            },
            "ports": [
                {
                    "name": port_name,
                    "port": port,
                    "targetPort": port_name,
                    "protocol": "TCP",
                }
            ],
        },
    }


def _dependency_resources(namespace: str, run_id: str) -> list[dict[str, Any]]:
    postgres_name = "ani-service-observability-postgres"
    nats_name = "ani-service-observability-nats"
    redis_name = "ani-service-observability-redis"

    postgres_labels = _labels(run_id, postgres_name)
    postgres = {
        "apiVersion": "apps/v1",
        "kind": "Deployment",
        "metadata": {"name": postgres_name, "namespace": namespace, "labels": postgres_labels},
        "spec": {
            "replicas": 1,
            "selector": {
                "matchLabels": {
                    "app.kubernetes.io/name": postgres_name,
                    "ani.dev/run-id": run_id,
                }
            },
            "template": {
                "metadata": {"labels": postgres_labels},
                "spec": {
                    "automountServiceAccountToken": False,
                    "containers": [
                        {
                            "name": "postgres",
                            "image": POSTGRES_IMAGE,
                            "imagePullPolicy": "IfNotPresent",
                            "ports": [{"name": "postgres", "containerPort": 5432}],
                            "env": [
                                {"name": "POSTGRES_DB", "value": "ani"},
                                {"name": "POSTGRES_USER", "value": "ani"},
                                _secret_env("POSTGRES_PASSWORD", "postgres_password"),
                                {"name": "PGDATA", "value": "/var/lib/postgresql/data/pgdata"},
                            ],
                            "readinessProbe": {
                                "exec": {"command": ["pg_isready", "-U", "ani", "-d", "ani"]},
                                "initialDelaySeconds": 3,
                                "periodSeconds": 3,
                                "timeoutSeconds": 2,
                                "failureThreshold": 20,
                            },
                            "securityContext": {
                                "allowPrivilegeEscalation": False,
                                "capabilities": {
                                    "drop": ["ALL"],
                                    "add": [
                                        "CHOWN",
                                        "DAC_OVERRIDE",
                                        "FOWNER",
                                        "SETGID",
                                        "SETUID",
                                    ],
                                },
                            },
                            "volumeMounts": [
                                {
                                    "name": "migrations",
                                    "mountPath": "/docker-entrypoint-initdb.d",
                                    "readOnly": True,
                                },
                                {"name": "data", "mountPath": "/var/lib/postgresql/data"},
                            ],
                        }
                    ],
                    "volumes": [
                        {
                            "name": "migrations",
                            "configMap": {"name": "ani-service-observability-postgres-init"},
                        },
                        {"name": "data", "emptyDir": {}},
                    ],
                },
            },
        },
    }

    nats_labels = _labels(run_id, nats_name)
    nats = {
        "apiVersion": "apps/v1",
        "kind": "Deployment",
        "metadata": {"name": nats_name, "namespace": namespace, "labels": nats_labels},
        "spec": {
            "replicas": 1,
            "selector": {
                "matchLabels": {
                    "app.kubernetes.io/name": nats_name,
                    "ani.dev/run-id": run_id,
                }
            },
            "template": {
                "metadata": {"labels": nats_labels},
                "spec": {
                    "automountServiceAccountToken": False,
                    "containers": [
                        {
                            "name": "nats",
                            "image": NATS_IMAGE,
                            "imagePullPolicy": "IfNotPresent",
                            "args": ["-js", "-sd", "/data"],
                            "ports": [{"name": "nats", "containerPort": 4222}],
                            "readinessProbe": {
                                "tcpSocket": {"port": "nats"},
                                "initialDelaySeconds": 2,
                                "periodSeconds": 3,
                            },
                            "securityContext": {
                                "allowPrivilegeEscalation": False,
                                "capabilities": {"drop": ["ALL"]},
                            },
                            "volumeMounts": [{"name": "data", "mountPath": "/data"}],
                        }
                    ],
                    "volumes": [{"name": "data", "emptyDir": {}}],
                },
            },
        },
    }

    redis_labels = _labels(run_id, redis_name)
    redis = {
        "apiVersion": "apps/v1",
        "kind": "Deployment",
        "metadata": {"name": redis_name, "namespace": namespace, "labels": redis_labels},
        "spec": {
            "replicas": 1,
            "selector": {
                "matchLabels": {
                    "app.kubernetes.io/name": redis_name,
                    "ani.dev/run-id": run_id,
                }
            },
            "template": {
                "metadata": {"labels": redis_labels},
                "spec": {
                    "automountServiceAccountToken": False,
                    "containers": [
                        {
                            "name": "redis",
                            "image": REDIS_IMAGE,
                            "imagePullPolicy": "IfNotPresent",
                            "args": ["--requirepass", "$(REDIS_PASSWORD)"],
                            "ports": [{"name": "redis", "containerPort": 6379}],
                            "env": [_secret_env("REDIS_PASSWORD", "redis_password")],
                            "readinessProbe": {
                                "tcpSocket": {"port": "redis"},
                                "initialDelaySeconds": 2,
                                "periodSeconds": 3,
                            },
                            "securityContext": {
                                "allowPrivilegeEscalation": False,
                                "capabilities": {
                                    "drop": ["ALL"],
                                    "add": ["CHOWN", "SETGID", "SETUID"],
                                },
                            },
                        }
                    ],
                },
            },
        },
    }
    return [
        postgres,
        _service(namespace, run_id, postgres_name, "postgres", 5432),
        nats,
        _service(namespace, run_id, nats_name, "nats", 4222),
        redis,
        _service(namespace, run_id, redis_name, "redis", 6379),
    ]


def _prometheus_config(namespace: str) -> str:
    config = {
        "global": {"scrape_interval": "15s", "evaluation_interval": "15s"},
        "scrape_configs": [
            {
                "job_name": "ani-components",
                "scrape_interval": "15s",
                "scrape_timeout": "5s",
                "metrics_path": "/metrics",
                "kubernetes_sd_configs": [
                    {"role": "pod", "namespaces": {"names": [namespace]}}
                ],
                "relabel_configs": [
                    {
                        "source_labels": ["__meta_kubernetes_pod_label_ani_dev_metrics_scrape"],
                        "regex": "true",
                        "action": "keep",
                    },
                    {
                        "source_labels": ["__meta_kubernetes_pod_container_port_name"],
                        "regex": "health",
                        "action": "keep",
                    },
                    {
                        "source_labels": ["__meta_kubernetes_pod_phase"],
                        "regex": "Running",
                        "action": "keep",
                    },
                    {
                        "source_labels": ["__meta_kubernetes_pod_label_ani_dev_service_name"],
                        "regex": SERVICE_PATTERN,
                        "action": "keep",
                    },
                    {
                        "source_labels": ["__meta_kubernetes_pod_label_ani_dev_service_name"],
                        "target_label": "ani_service_name",
                    },
                    {
                        "source_labels": ["__meta_kubernetes_pod_label_app_kubernetes_io_version"],
                        "target_label": "k8s_service_version",
                    },
                    {
                        "source_labels": ["__meta_kubernetes_namespace"],
                        "target_label": "kubernetes_namespace",
                    },
                    {
                        "source_labels": ["__meta_kubernetes_pod_name"],
                        "target_label": "pod",
                    },
                ],
            }
        ],
    }
    return yaml.safe_dump(config, sort_keys=False)


def _prometheus_resources(namespace: str, run_id: str) -> list[dict[str, Any]]:
    labels = _labels(run_id, PROMETHEUS_NAME)
    service_account = {
        "apiVersion": "v1",
        "kind": "ServiceAccount",
        "metadata": {"name": PROMETHEUS_NAME, "namespace": namespace, "labels": labels},
        "automountServiceAccountToken": True,
    }
    role = {
        "apiVersion": "rbac.authorization.k8s.io/v1",
        "kind": "Role",
        "metadata": {"name": PROMETHEUS_NAME, "namespace": namespace, "labels": labels},
        "rules": [
            {
                "apiGroups": [""],
                "resources": ["pods"],
                "verbs": ["get", "list", "watch"],
            }
        ],
    }
    role_binding = {
        "apiVersion": "rbac.authorization.k8s.io/v1",
        "kind": "RoleBinding",
        "metadata": {"name": PROMETHEUS_NAME, "namespace": namespace, "labels": labels},
        "roleRef": {
            "apiGroup": "rbac.authorization.k8s.io",
            "kind": "Role",
            "name": PROMETHEUS_NAME,
        },
        "subjects": [
            {"kind": "ServiceAccount", "name": PROMETHEUS_NAME, "namespace": namespace}
        ],
    }
    config = {
        "apiVersion": "v1",
        "kind": "ConfigMap",
        "metadata": {"name": PROMETHEUS_NAME, "namespace": namespace, "labels": labels},
        "data": {"prometheus.yml": _prometheus_config(namespace)},
    }
    deployment = {
        "apiVersion": "apps/v1",
        "kind": "Deployment",
        "metadata": {"name": PROMETHEUS_NAME, "namespace": namespace, "labels": labels},
        "spec": {
            "replicas": 1,
            "selector": {
                "matchLabels": {
                    "app.kubernetes.io/name": PROMETHEUS_NAME,
                    "ani.dev/run-id": run_id,
                }
            },
            "template": {
                "metadata": {"labels": labels},
                "spec": {
                    "serviceAccountName": PROMETHEUS_NAME,
                    "automountServiceAccountToken": True,
                    "securityContext": {
                        "runAsNonRoot": True,
                        "runAsUser": 65534,
                        "runAsGroup": 65534,
                        "fsGroup": 65534,
                        "seccompProfile": {"type": "RuntimeDefault"},
                    },
                    "containers": [
                        {
                            "name": "prometheus",
                            "image": PROMETHEUS_IMAGE,
                            "imagePullPolicy": "IfNotPresent",
                            "args": [
                                "--config.file=/etc/prometheus/prometheus.yml",
                                "--storage.tsdb.path=/prometheus",
                                "--storage.tsdb.retention.time=2h",
                            ],
                            "ports": [{"name": "http", "containerPort": 9090}],
                            "readinessProbe": {
                                "httpGet": {"path": "/-/ready", "port": "http"},
                                "initialDelaySeconds": 3,
                                "periodSeconds": 3,
                            },
                            "livenessProbe": {
                                "httpGet": {"path": "/-/healthy", "port": "http"},
                                "initialDelaySeconds": 10,
                                "periodSeconds": 10,
                            },
                            "securityContext": {
                                "allowPrivilegeEscalation": False,
                                "readOnlyRootFilesystem": True,
                                "capabilities": {"drop": ["ALL"]},
                            },
                            "volumeMounts": [
                                {
                                    "name": "config",
                                    "mountPath": "/etc/prometheus",
                                    "readOnly": True,
                                },
                                {"name": "data", "mountPath": "/prometheus"},
                            ],
                        }
                    ],
                    "volumes": [
                        {"name": "config", "configMap": {"name": PROMETHEUS_NAME}},
                        {"name": "data", "emptyDir": {}},
                    ],
                },
            },
        },
    }
    service = _service(namespace, run_id, PROMETHEUS_NAME, "http", 9090)
    return [service_account, role, role_binding, config, deployment, service]


def _network_policy(namespace: str, run_id: str) -> dict[str, Any]:
    return {
        "apiVersion": "networking.k8s.io/v1",
        "kind": "NetworkPolicy",
        "metadata": {
            "name": "ani-service-observability-ingress",
            "namespace": namespace,
            "labels": _labels(run_id, "ani-service-observability-ingress"),
        },
        "spec": {
            "podSelector": {"matchLabels": {"ani.dev/profile": PROFILE}},
            "policyTypes": ["Ingress"],
            "ingress": [{"from": [{"podSelector": {}}]}],
        },
    }


def _migration_config(namespace: str, run_id: str, migration_dir: Path) -> dict[str, Any]:
    files = sorted(migration_dir.glob("*.sql"))
    if not files:
        raise ValueError("migration directory contains no SQL files")
    return {
        "apiVersion": "v1",
        "kind": "ConfigMap",
        "metadata": {
            "name": "ani-service-observability-postgres-init",
            "namespace": namespace,
            "labels": _labels(run_id, "ani-service-observability-postgres-init"),
        },
        "data": {path.name: path.read_text(encoding="utf-8") for path in files},
    }


def render_l3_fixture(
    namespace: str,
    version: str,
    images: dict[str, str],
    image_pull_secret: str | None = None,
    migration_dir: Path = MIGRATION_DIR,
) -> str:
    if not workloads.NAMESPACE_RE.fullmatch(namespace):
        raise ValueError("namespace must be an isolated ani-service-observability-e2e-<run-id> namespace")
    run_id = namespace.removeprefix(workloads.NAMESPACE_PREFIX)
    namespace_document = {
        "apiVersion": "v1",
        "kind": "Namespace",
        "metadata": {"name": namespace, "labels": _labels(run_id, namespace)},
    }
    workload_documents = [
        document
        for document in yaml.safe_load_all(
            workloads.render(
                INVENTORY,
                namespace,
                version,
                images,
                image_pull_secret=image_pull_secret,
            )
        )
        if document
    ]
    documents = [
        namespace_document,
        _migration_config(namespace, run_id, migration_dir),
        *_dependency_resources(namespace, run_id),
        *_prometheus_resources(namespace, run_id),
        _network_policy(namespace, run_id),
        *workload_documents,
    ]
    return yaml.safe_dump_all(documents, sort_keys=False, explicit_start=True)


def parse_args() -> argparse.Namespace:
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("--namespace", required=True)
    parser.add_argument("--version", required=True)
    parser.add_argument("--images-file", type=Path, required=True)
    parser.add_argument("--image-pull-secret")
    return parser.parse_args()


def main() -> int:
    args = parse_args()
    try:
        spec = workloads.load_inventory(INVENTORY)
        expected = {service["name"] for service in spec["services"]}
        images = workloads.load_images(args.images_file, expected)
        sys.stdout.write(
            render_l3_fixture(
                namespace=args.namespace,
                version=args.version,
                images=images,
                image_pull_secret=args.image_pull_secret,
            )
        )
    except (OSError, KeyError, TypeError, ValueError, json.JSONDecodeError, yaml.YAMLError) as exc:
        print(f"L3 render failed: {exc}", file=sys.stderr)
        return 2
    return 0


if __name__ == "__main__":
    raise SystemExit(main())
