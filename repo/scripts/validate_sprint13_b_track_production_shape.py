#!/usr/bin/env python3
"""Validate Sprint 13 S01-S07 B-track production-shaped evidence boundaries."""

from __future__ import annotations

import json
from pathlib import Path
from typing import Any

import yaml


ROOT = Path(__file__).resolve().parents[1]
RECORD_ROOT = ROOT / "development-records"
PRODUCTION_PROFILE = ROOT / "deploy/real-k8s-lab/sprint13-production-shaped-gateway-profile.yaml"
PRODUCTION_RBAC = ROOT / "deploy/real-k8s-lab/sprint13-production-shaped-gateway-rbac.yaml"
PRODUCTION_DEPLOYMENT = ROOT / "deploy/real-k8s-lab/sprint13-production-shaped-gateway-deployment.yaml"
GATEWAY_DOCKERFILE = ROOT / "services/ani-gateway/Dockerfile"
MAKEFILE = ROOT / "Makefile"
PROJECT_ROOT = ROOT.parent
DOCS_INDEX = PROJECT_ROOT / "ANI-DOCS-INDEX.md"
PLAN = PROJECT_ROOT / "ANI-06-开发计划.md"
CURRENT_SPRINT = ROOT / "CURRENT-SPRINT.md"
RECORDS_INDEX = RECORD_ROOT / "README.md"
PRODUCTION_READINESS_REVIEW = RECORD_ROOT / "sprint13-s01-s04-production-readiness-review.md"
AUTH_DEX_EVIDENCE = RECORD_ROOT / "live-evidence/sprint13-auth-dex-production-evidence.json"

SLICES = {
    "S01": {
        "evidence": RECORD_ROOT / "live-evidence/sprint13-netroute-kubeovn-live-evidence.json",
        "result": RECORD_ROOT / "sprint13-netroute-kubeovn-live-result.md",
        "required_missing": {
            "production_rbac_and_credential_management",
            "persistent_route_metadata_reconciliation",
        },
        "required_proof": {
            "production_gateway",
            "in_cluster_serviceaccount_rbac",
            "persistent_route_metadata_reconciliation",
        },
    },
    "S02": {
        "evidence": RECORD_ROOT / "live-evidence/sprint13-k8s-workloads-vcluster-live-evidence.json",
        "result": RECORD_ROOT / "sprint13-k8s-workloads-vcluster-live-result.md",
        "required_missing": {
            "production_per_cluster_metadata_target",
            "production_tls_and_token_management",
        },
        "required_proof": {
            "production_gateway",
            "production_per_cluster_metadata_target",
            "production_tls_and_token_management",
        },
    },
    "S03": {
        "evidence": RECORD_ROOT / "live-evidence/sprint13-storage-rook-ceph-live-evidence.json",
        "result": RECORD_ROOT / "sprint13-storage-rook-ceph-live-result.md",
        "required_missing": {
            "production_serviceaccount_rbac",
            "tenant_storage_lifecycle_and_backup_restore",
        },
        "required_proof": {
            "production_gateway",
            "in_cluster_serviceaccount_rbac",
            "tenant_storage_lifecycle_and_backup_restore",
        },
    },
    "S04": {
        "evidence": RECORD_ROOT / "live-evidence/sprint13-gpu-inventory-dcgm-live-evidence.json",
        "result": RECORD_ROOT / "sprint13-gpu-inventory-dcgm-live-result.md",
        "required_missing": {
            "production_in_cluster_kubernetes_api",
            "production_dcgm_service_or_prometheus_query",
        },
        "required_proof": {
            "production_gateway",
            "in_cluster_kubernetes_api",
            "production_dcgm_service_or_prometheus_query",
        },
    },
    "S05": {
        "evidence": RECORD_ROOT / "live-evidence/sprint13-objectstore-minio-live-evidence.json",
        "result": RECORD_ROOT / "sprint13-objectstore-minio-live-result.md",
        "required_missing": {
            "production_object_store_credentials",
            "production_presigned_url_endpoint",
        },
        "required_proof": {
            "production_gateway",
            "production_object_store_credentials",
            "production_presigned_url_endpoint",
        },
    },
    "S06": {
        "evidence": RECORD_ROOT / "live-evidence/sprint13-vector-milvus-live-evidence.json",
        "result": RECORD_ROOT / "sprint13-vector-milvus-live-result.md",
        "required_missing": {
            "production_vector_store_credentials",
            "production_vector_collection_lifecycle",
        },
        "required_proof": {
            "production_gateway",
            "production_vector_store_credentials",
            "production_vector_collection_lifecycle",
        },
    },
    "S07": {
        "evidence": RECORD_ROOT / "live-evidence/sprint13-instance-observability-prometheus-live-evidence.json",
        "result": RECORD_ROOT / "sprint13-instance-observability-prometheus-live-result.md",
        "required_missing": {
            "production_prometheus_service_or_query",
            "production_kubelet_or_kubernetes_api_access",
        },
        "required_proof": {
            "production_gateway",
            "production_prometheus_service_or_query",
            "production_kubelet_or_kubernetes_api_access",
        },
    },
}

ALLOWED_PRODUCTION_STATUSES = {"pending", "passed"}
PRODUCTION_FORBIDDEN_TRANSPORT_TOKENS = {"lab", "local", "port_forward", "port-forward", "dev_gateway", "dev-gateway", "kubectl_proxy", "kubectl-proxy"}
REQUIRED_RBAC_KINDS = {"ServiceAccount", "ClusterRole", "ClusterRoleBinding"}
REQUIRED_RBAC_RESOURCES = {
    "customresourcedefinitions",
    "configmaps",
    "clusterrolebindings",
    "clusterroles",
    "deployments",
    "endpoints",
    "endpointslices",
    "events",
    "namespaces",
    "nodes",
    "persistentvolumes",
    "pods",
    "pods/attach",
    "pods/ephemeralcontainers",
    "pods/exec",
    "pods/log",
    "pods/portforward",
    "pods/resize",
    "pods/status",
    "replicasets",
    "roles",
    "rolebindings",
    "secrets",
    "serviceaccounts",
    "services",
    "statefulsets",
    "persistentvolumeclaims",
    "networkpolicies",
    "storageclasses",
    "vpcs",
    "subnets",
    "volumesnapshotclasses",
    "volumesnapshotcontents",
    "volumesnapshots",
}
REQUIRED_STANDARD_SLICES = {"S01", "S02", "S03", "S04", "S05", "S06", "S07"}
REQUIRED_DEPLOYMENT_ENVS = {
    "ANI_AUTH_MODE",
    "AUTH_SERVICE_ADDR",
    "KUBERNETES_SERVICE_ACCOUNT_TOKEN_FILE",
    "KUBERNETES_SERVICE_ACCOUNT_CA_FILE",
    "KUBERNETES_PROVIDER_FIELD_MANAGER",
    "NETWORK_PROVIDER",
    "NETWORK_PROVIDER_APPLY_ENABLED",
    "STORAGE_PROVIDER",
    "STORAGE_PROVIDER_APPLY_ENABLED",
    "GPU_INVENTORY_PROVIDER",
    "K8S_CLUSTER_PROVIDER_MODE",
    "K8S_CLUSTER_PROXY_MODE",
    "DATABASE_URL",
    "VCLUSTER_HELM_BINARY",
    "VCLUSTER_BINARY",
    "VCLUSTER_HELM_SET_VALUES",
    "VCLUSTER_PROXY_SERVER_TEMPLATE",
    "VCLUSTER_KUBECONFIG_SERVER_TEMPLATE",
    "OBJECT_STORE_PROVIDER",
    "OBJECT_STORE_ENDPOINT",
    "OBJECT_STORE_PUBLIC_ENDPOINT",
    "OBJECT_STORE_ACCESS_KEY_ID",
    "OBJECT_STORE_SECRET_ACCESS_KEY",
    "OBJECT_STORE_REGION",
    "OBJECT_STORE_SECURE",
    "OBJECT_STORE_BUCKET_PREFIX",
    "VECTOR_STORE_PROVIDER",
    "VECTOR_STORE_ENDPOINT",
    "VECTOR_STORE_TOKEN",
    "VECTOR_STORE_DATABASE",
    "VECTOR_STORE_COLLECTION_PREFIX",
    "INSTANCE_OBSERVABILITY_PROVIDER",
    "INSTANCE_OBSERVABILITY_PROMETHEUS_URL",
    "INSTANCE_OBSERVABILITY_EXEC_BASE_URL",
    "REGISTRY_PROVIDER",
    "REGISTRY_ENDPOINT",
    "REGISTRY_USERNAME",
    "REGISTRY_PASSWORD",
    "REGISTRY_SECURE",
    "REGISTRY_TLS_INSECURE",
}
REQUIRED_PRODUCTION_READINESS_DOC_TOKENS = {
    "Auth/Dex production gate",
    "ANI_AUTH_MODE=auth_service",
    "SPRINT13-AUTH-DEX-PRODUCTION-GATE",
    "S05-S07 B 轨可以继续",
}
REQUIRED_AUTH_DEX_PROOF_ITEMS = {
    "gateway_non_dev_auth",
    "dex_discovery_and_jwks",
    "gateway_rejects_anonymous",
    "gateway_accepts_dex_oidc_token",
    "gateway_refresh_token",
    "auth_service_rbac_check",
}


def fail(message: str) -> None:
    raise SystemExit(f"sprint13 production-shape guard invalid: {message}")


def load_json(path: Path) -> dict[str, Any]:
    if not path.exists():
        fail(f"missing evidence {path.relative_to(ROOT)}")
    try:
        payload = json.loads(path.read_text(encoding="utf-8"))
    except json.JSONDecodeError as exc:
        fail(f"malformed evidence {path.relative_to(ROOT)}: {exc}")
    if not isinstance(payload, dict):
        fail(f"evidence {path.relative_to(ROOT)} must be a JSON object")
    return payload


def validate_evidence(slice_id: str, path: Path) -> None:
    payload = load_json(path)
    if payload.get("status") != "passed":
        fail(f"{slice_id} evidence status must remain passed for real-provider gate")

    shape = payload.get("production_shape")
    if not isinstance(shape, dict):
        fail(f"{slice_id} evidence must include production_shape")

    status = shape.get("status")
    if status not in ALLOWED_PRODUCTION_STATUSES:
        fail(f"{slice_id} production_shape.status must be pending or passed")

    transport = shape.get("transport_profile")
    if not isinstance(transport, str) or not transport.strip():
        fail(f"{slice_id} production_shape.transport_profile must be non-empty")

    missing_items = shape.get("missing_items")
    if not isinstance(missing_items, list):
        if status == "pending":
            fail(f"{slice_id} pending production_shape must list missing_items")
        fail(f"{slice_id} production_shape.missing_items must be a list")
    missing_set = {str(item).strip() for item in missing_items if str(item).strip()}

    if status == "pending":
        if not missing_set:
            fail(f"{slice_id} pending production_shape must list missing_items")
        required = SLICES.get(slice_id, {}).get("required_missing", set())
        absent = set(required) - missing_set
        if absent:
            fail(f"{slice_id} production_shape missing_items must include {', '.join(sorted(absent))}")
        return

    if any(token in transport for token in PRODUCTION_FORBIDDEN_TRANSPORT_TOKENS):
        fail(f"{slice_id} production_shape passed cannot use {transport}")
    if missing_set:
        fail(f"{slice_id} production_shape passed must not list missing_items")
    proof_items = shape.get("proof_items")
    if not isinstance(proof_items, list):
        fail(f"{slice_id} production_shape passed requires proof_items")
    proof_set = {str(item).strip() for item in proof_items if str(item).strip()}
    if not proof_set:
        fail(f"{slice_id} production_shape passed requires proof_items")
    required_proof = SLICES.get(slice_id, {}).get("required_proof", set())
    absent_proof = set(required_proof) - proof_set
    if absent_proof:
        fail(f"{slice_id} production_shape proof_items must include {', '.join(sorted(absent_proof))}")
    if slice_id == "S01":
        validate_s01_gateway_production_evidence(payload)
    if slice_id == "S02":
        validate_s02_gateway_workload_evidence(payload)
    if slice_id == "S03":
        validate_s03_storage_lifecycle_evidence(payload)
    if slice_id == "S04":
        validate_s04_gpu_dcgm_evidence(payload)
    if slice_id == "S05":
        validate_s05_object_store_evidence(payload)
    if slice_id == "S06":
        validate_s06_vector_store_evidence(payload)
    if slice_id == "S07":
        validate_s07_instance_observability_evidence(payload)


def validate_s01_gateway_production_evidence(payload: dict[str, Any]) -> None:
    expected_statuses = {
        "gateway_vpc_create_status": 201,
        "gateway_subnet_create_status": 201,
        "gateway_route_create_status": 201,
        "gateway_route_list_status": 200,
    }
    for field, expected in expected_statuses.items():
        if payload.get(field) != expected:
            fail(f"S01 production_shape passed requires {field}={expected}")
    for field in ("gateway_vpc_id", "gateway_subnet_id", "gateway_route_id"):
        value = payload.get(field)
        if not isinstance(value, str) or not value.strip():
            fail(f"S01 production_shape passed requires non-empty {field}")
    route_count = payload.get("gateway_route_count")
    if not isinstance(route_count, int) or route_count < 1:
        fail("S01 production_shape passed requires gateway_route_count >= 1")


def validate_s02_gateway_workload_evidence(payload: dict[str, Any]) -> None:
    expected_statuses = {
        "proxy_status": 200,
        "workloads_status": 200,
    }
    for field, expected in expected_statuses.items():
        if payload.get(field) != expected:
            fail(f"S02 production_shape passed requires {field}={expected}")
    core_cluster_id = payload.get("core_cluster_id")
    if not isinstance(core_cluster_id, str) or not core_cluster_id.strip():
        fail("S02 production_shape passed requires non-empty core_cluster_id")
    workload_name = payload.get("workload_name")
    if not isinstance(workload_name, str) or not workload_name.strip():
        fail("S02 production_shape passed requires non-empty workload_name")
    workload_count = payload.get("workload_count")
    if not isinstance(workload_count, int) or workload_count < 1:
        fail("S02 production_shape passed requires workload_count >= 1")
    if payload.get("cleanup") != "deleted":
        fail("S02 production_shape passed requires cleanup=deleted")


def validate_s03_storage_lifecycle_evidence(payload: dict[str, Any]) -> None:
    expected_statuses = {
        "volume_status": 201,
        "snapshot_status": 202,
        "filesystem_status": 201,
    }
    for field, expected in expected_statuses.items():
        if payload.get(field) != expected:
            fail(f"S03 production_shape passed requires {field}={expected}")
    mount_target_count = payload.get("mount_target_count")
    if not isinstance(mount_target_count, int) or mount_target_count < 1:
        fail("S03 production_shape passed requires mount_target_count >= 1")
    if payload.get("cleanup") != "deleted":
        fail("S03 production_shape passed requires cleanup=deleted")


def validate_s04_gpu_dcgm_evidence(payload: dict[str, Any]) -> None:
    expected_statuses = {
        "inventory_status": 200,
        "occupancy_status": 200,
    }
    for field, expected in expected_statuses.items():
        if payload.get(field) != expected:
            fail(f"S04 production_shape passed requires {field}={expected}")
    gpu_capacity_total = payload.get("gpu_capacity_total")
    if not isinstance(gpu_capacity_total, int) or gpu_capacity_total < 1:
        fail("S04 production_shape passed requires gpu_capacity_total >= 1")
    gpu_node_count = payload.get("gpu_node_count")
    if not isinstance(gpu_node_count, int) or gpu_node_count < 1:
        fail("S04 production_shape passed requires gpu_node_count >= 1")
    inventory_count = payload.get("inventory_count")
    if not isinstance(inventory_count, int) or inventory_count < 1:
        fail("S04 production_shape passed requires inventory_count >= 1")
    if payload.get("dcgm_metric_present") is not True:
        fail("S04 production_shape passed requires dcgm_metric_present=true")


def validate_s05_object_store_evidence(payload: dict[str, Any]) -> None:
    expected_statuses = {
        "bucket_create_status": 201,
        "bucket_list_status": 200,
        "upload_presign_status": 200,
        "download_presign_status": 200,
        "actual_upload_status": 200,
        "actual_download_status": 200,
        "cleanup_api_key_status": 201,
        "cleanup_status": 200,
        "cleanup_api_key_revoke_status": 200,
    }
    for field, expected in expected_statuses.items():
        if payload.get(field) != expected:
            fail(f"S05 production_shape passed requires {field}={expected}")
    bucket_list_count = payload.get("bucket_list_count")
    if not isinstance(bucket_list_count, int) or bucket_list_count < 1:
        fail("S05 production_shape passed requires bucket_list_count >= 1")
    if payload.get("minio_health_ready") is not True:
        fail("S05 production_shape passed requires minio_health_ready=true")
    if payload.get("upload_presign_url_present") is not True:
        fail("S05 production_shape passed requires upload_presign_url_present=true")
    if payload.get("download_presign_url_present") is not True:
        fail("S05 production_shape passed requires download_presign_url_present=true")
    if payload.get("cleanup_enabled") is not True:
        fail("S05 production_shape passed requires cleanup_enabled=true")


def validate_s06_vector_store_evidence(payload: dict[str, Any]) -> None:
    expected_statuses = {
        "milvus_health_status": 200,
        "vector_store_create_status": 201,
        "document_insert_status": 202,
        "search_status": 200,
        "cleanup_api_key_status": 201,
        "cleanup_status": 200,
        "cleanup_api_key_revoke_status": 200,
    }
    for field, expected in expected_statuses.items():
        if payload.get(field) != expected:
            fail(f"S06 production_shape passed requires {field}={expected}")
    inserted_count = payload.get("inserted_count")
    if not isinstance(inserted_count, int) or inserted_count < 1:
        fail("S06 production_shape passed requires inserted_count >= 1")
    search_hit_count = payload.get("search_hit_count")
    if not isinstance(search_hit_count, int) or search_hit_count < 1:
        fail("S06 production_shape passed requires search_hit_count >= 1")
    if payload.get("milvus_health_ready") is not True:
        fail("S06 production_shape passed requires milvus_health_ready=true")
    if payload.get("cleanup_enabled") is not True:
        fail("S06 production_shape passed requires cleanup_enabled=true")


def validate_s07_instance_observability_evidence(payload: dict[str, Any]) -> None:
    expected_statuses = {
        "prometheus_health_status": 200,
        "instance_create_status": 201,
        "logs_status": 200,
        "events_status": 200,
        "metrics_status": 200,
        "security_events_status": 200,
        "exec_status": 200,
        "cleanup_status": 200,
    }
    for field, expected in expected_statuses.items():
        if payload.get(field) != expected:
            fail(f"S07 production_shape passed requires {field}={expected}")
    for field in ("logs_count", "events_count", "security_events_count"):
        value = payload.get(field)
        if not isinstance(value, int) or value < 1:
            fail(f"S07 production_shape passed requires {field} >= 1")
    if payload.get("metrics_cpu_present") is not True:
        fail("S07 production_shape passed requires metrics_cpu_present=true")
    if payload.get("exec_ws_url_present") is not True:
        fail("S07 production_shape passed requires exec_ws_url_present=true")
    if payload.get("exec_token_exposed") is not False:
        fail("S07 production_shape passed requires exec_token_exposed=false")
    if payload.get("cleanup_enabled") is not True:
        fail("S07 production_shape passed requires cleanup_enabled=true")


def validate_result_doc(slice_id: str, path: Path) -> None:
    if not path.exists():
        fail(f"missing result doc {path.relative_to(ROOT)}")
    content = path.read_text(encoding="utf-8")
    required_tokens = ["Production-shaped gate", "production_shape"]
    for token in required_tokens:
        if token not in content:
            fail(f"{slice_id} result doc must reference {token}")
    if "not production ready" not in content and "不代表 production ready" not in content:
        fail(f"{slice_id} result doc must state not production ready")


def validate_production_profile() -> None:
    if not PRODUCTION_PROFILE.exists():
        fail(f"missing production profile {PRODUCTION_PROFILE.relative_to(ROOT)}")
    if not PRODUCTION_RBAC.exists():
        fail(f"missing production RBAC {PRODUCTION_RBAC.relative_to(ROOT)}")
    try:
        profile = yaml.safe_load(PRODUCTION_PROFILE.read_text(encoding="utf-8"))
    except yaml.YAMLError as exc:
        fail(f"malformed production profile {PRODUCTION_PROFILE.relative_to(ROOT)}: {exc}")
    if not isinstance(profile, dict):
        fail("production profile must be a YAML object")
    if profile.get("profile") != "SPRINT13-B-TRACK-PRODUCTION-SHAPED-GATEWAY":
        fail("production profile id must be SPRINT13-B-TRACK-PRODUCTION-SHAPED-GATEWAY")
    gateway = profile.get("gateway")
    if not isinstance(gateway, dict):
        fail("production profile must include gateway block")
    if gateway.get("deployment_mode") != "in_cluster":
        fail("production profile gateway deployment_mode must be in_cluster")
    if gateway.get("deployment_manifest") != "deploy/real-k8s-lab/sprint13-production-shaped-gateway-deployment.yaml":
        fail("production profile gateway deployment_manifest must reference sprint13 production-shaped deployment")
    kube_client = gateway.get("kubernetes_client")
    if not isinstance(kube_client, dict):
        fail("production profile must include gateway.kubernetes_client")
    expected_sources = {
        "host_source": "in_cluster_service",
        "token_source": "service_account_projected_token",
        "ca_source": "service_account_ca_bundle",
    }
    for field, expected in expected_sources.items():
        if kube_client.get(field) != expected:
            fail(f"production profile Kubernetes client {field} must be {expected}")
    proof_items = profile.get("slice_proof_items")
    if not isinstance(proof_items, dict):
        fail("production profile must include slice_proof_items")
    absent_slices = REQUIRED_STANDARD_SLICES - set(proof_items)
    if absent_slices:
        fail(f"production profile slice_proof_items missing {', '.join(sorted(absent_slices))}")
    for slice_id in sorted(REQUIRED_STANDARD_SLICES):
        items = proof_items.get(slice_id)
        if not isinstance(items, list) or "production_gateway" not in items:
            fail(f"production profile {slice_id} proof items must include production_gateway")

    try:
        docs = list(yaml.safe_load_all(PRODUCTION_RBAC.read_text(encoding="utf-8")))
    except yaml.YAMLError as exc:
        fail(f"malformed production RBAC {PRODUCTION_RBAC.relative_to(ROOT)}: {exc}")
    docs = [doc for doc in docs if isinstance(doc, dict)]
    kinds = {str(doc.get("kind", "")) for doc in docs}
    missing_kinds = REQUIRED_RBAC_KINDS - kinds
    if missing_kinds:
        fail(f"production RBAC missing {', '.join(sorted(missing_kinds))}")
    cluster_roles = [doc for doc in docs if doc.get("kind") == "ClusterRole"]
    if len(cluster_roles) != 1:
        fail("production RBAC must include exactly one ClusterRole")
    resources = set()
    for rule in cluster_roles[0].get("rules", []):
        if isinstance(rule, dict) and isinstance(rule.get("resources"), list):
            resources.update(str(resource) for resource in rule["resources"])
    missing_resources = REQUIRED_RBAC_RESOURCES - resources
    if missing_resources:
        fail(f"production RBAC missing resources {', '.join(sorted(missing_resources))}")
    if "*" in resources:
        fail("production RBAC must not grant wildcard resources")


def validate_production_deployment_contract() -> None:
    if not GATEWAY_DOCKERFILE.exists():
        fail(f"missing gateway Dockerfile {GATEWAY_DOCKERFILE.relative_to(ROOT)}")
    dockerfile = GATEWAY_DOCKERFILE.read_text(encoding="utf-8")
    for token in ("COPY pkg ./pkg", "COPY services/ani-gateway ./services/ani-gateway", "go build -tags stdjson", "vcluster-linux-amd64", "helm-", "USER 65532:65532"):
        if token not in dockerfile:
            fail(f"gateway Dockerfile must include {token}")

    makefile = MAKEFILE.read_text(encoding="utf-8")
    required_make_tokens = [
        "-f services/ani-gateway/Dockerfile",
        "\n\t\t.",
    ]
    for token in required_make_tokens:
        if token not in makefile:
            fail("image-gateway target must build from repo root with services/ani-gateway/Dockerfile")

    if not PRODUCTION_DEPLOYMENT.exists():
        fail(f"missing production deployment {PRODUCTION_DEPLOYMENT.relative_to(ROOT)}")
    try:
        docs = [doc for doc in yaml.safe_load_all(PRODUCTION_DEPLOYMENT.read_text(encoding="utf-8")) if isinstance(doc, dict)]
    except yaml.YAMLError as exc:
        fail(f"malformed production deployment {PRODUCTION_DEPLOYMENT.relative_to(ROOT)}: {exc}")
    deployments = [doc for doc in docs if doc.get("kind") == "Deployment"]
    services = [doc for doc in docs if doc.get("kind") == "Service"]
    if len(deployments) != 1:
        fail("production deployment manifest must include exactly one Deployment")
    if len(services) != 1:
        fail("production deployment manifest must include exactly one Service")

    deployment = deployments[0]
    spec = deployment.get("spec", {})
    template = spec.get("template", {}) if isinstance(spec, dict) else {}
    pod_spec = template.get("spec", {}) if isinstance(template, dict) else {}
    if pod_spec.get("serviceAccountName") != "ani-gateway":
        fail("production Deployment must use ani-gateway ServiceAccount")
    containers = pod_spec.get("containers")
    if not isinstance(containers, list) or len(containers) != 1 or not isinstance(containers[0], dict):
        fail("production Deployment must define exactly one Gateway container")
    container = containers[0]
    if container.get("imagePullPolicy") != "IfNotPresent":
        fail("production Deployment must use imagePullPolicy IfNotPresent for lab-imported images")
    env = container.get("env")
    if not isinstance(env, list):
        fail("production Deployment must define env")
    env_by_name = {item.get("name"): item for item in env if isinstance(item, dict)}
    absent_env = REQUIRED_DEPLOYMENT_ENVS - set(env_by_name)
    if absent_env:
        fail(f"production Deployment missing env {', '.join(sorted(absent_env))}")
    auth_mode = env_by_name.get("ANI_AUTH_MODE", {}).get("value")
    if auth_mode != "auth_service":
        fail("production Deployment ANI_AUTH_MODE must be auth_service after Auth/Dex production gate")
    auth_service_addr = env_by_name.get("AUTH_SERVICE_ADDR", {}).get("value")
    if auth_service_addr != "ani-auth-service.ani-system.svc.cluster.local:9101":
        fail("production Deployment AUTH_SERVICE_ADDR must point at in-cluster auth-service")
    database_env = env_by_name.get("DATABASE_URL", {})
    if "value" in database_env:
        fail("production Deployment must not commit DATABASE_URL literal")
    value_from = database_env.get("valueFrom")
    if not isinstance(value_from, dict) or "secretKeyRef" not in value_from:
        fail("production Deployment DATABASE_URL must come from secretKeyRef")
    if env_by_name.get("OBJECT_STORE_PROVIDER", {}).get("value") != "minio":
        fail("production Deployment OBJECT_STORE_PROVIDER must be minio")
    for name in ("OBJECT_STORE_ENDPOINT", "OBJECT_STORE_PUBLIC_ENDPOINT", "OBJECT_STORE_ACCESS_KEY_ID", "OBJECT_STORE_SECRET_ACCESS_KEY"):
        env_item = env_by_name.get(name, {})
        value_from = env_item.get("valueFrom")
        if not isinstance(value_from, dict) or "secretKeyRef" not in value_from:
            fail(f"production Deployment {name} must come from secretKeyRef")
        if "value" in env_item:
            fail(f"production Deployment must not commit {name} literal")
    if env_by_name.get("VECTOR_STORE_PROVIDER", {}).get("value") != "milvus":
        fail("production Deployment VECTOR_STORE_PROVIDER must be milvus")
    for name in ("VECTOR_STORE_ENDPOINT", "VECTOR_STORE_TOKEN", "VECTOR_STORE_DATABASE"):
        env_item = env_by_name.get(name, {})
        value_from = env_item.get("valueFrom")
        if not isinstance(value_from, dict) or "secretKeyRef" not in value_from:
            fail(f"production Deployment {name} must come from secretKeyRef")
        if "value" in env_item:
            fail(f"production Deployment must not commit {name} literal")
    if env_by_name.get("VECTOR_STORE_COLLECTION_PREFIX", {}).get("value") != "ani_s13_":
        fail("production Deployment VECTOR_STORE_COLLECTION_PREFIX must be ani_s13_")
    if env_by_name.get("INSTANCE_OBSERVABILITY_PROVIDER", {}).get("value") != "prometheus_kubernetes":
        fail("production Deployment INSTANCE_OBSERVABILITY_PROVIDER must be prometheus_kubernetes")
    if env_by_name.get("INSTANCE_OBSERVABILITY_PROMETHEUS_URL", {}).get("value") != "http://sprint13-prometheus.ani-s07-observability.svc.cluster.local:9090":
        fail("production Deployment INSTANCE_OBSERVABILITY_PROMETHEUS_URL must point at in-cluster Sprint 13 Prometheus")
    if env_by_name.get("INSTANCE_OBSERVABILITY_EXEC_BASE_URL", {}).get("value") != "wss://ani-gateway.ani-system.svc.cluster.local:8080/api/v1":
        fail("production Deployment INSTANCE_OBSERVABILITY_EXEC_BASE_URL must point at in-cluster Gateway API")
    if env_by_name.get("REGISTRY_PROVIDER", {}).get("value") != "harbor":
        fail("production Deployment REGISTRY_PROVIDER must be harbor")
    for name in ("REGISTRY_ENDPOINT", "REGISTRY_USERNAME", "REGISTRY_PASSWORD", "REGISTRY_SECURE", "REGISTRY_TLS_INSECURE"):
        env_item = env_by_name.get(name, {})
        value_from = env_item.get("valueFrom")
        if not isinstance(value_from, dict) or "secretKeyRef" not in value_from:
            fail(f"production Deployment {name} must come from secretKeyRef")
        if "value" in env_item:
            fail(f"production Deployment must not commit {name} literal")
        secret_ref = value_from.get("secretKeyRef", {})
        if secret_ref.get("name") != "ani-registry-production-shaped-runtime":
            fail(f"production Deployment {name} must use ani-registry-production-shaped-runtime secret")
    proxy_template = env_by_name.get("VCLUSTER_PROXY_SERVER_TEMPLATE", {}).get("value")
    kubeconfig_template = env_by_name.get("VCLUSTER_KUBECONFIG_SERVER_TEMPLATE", {}).get("value")
    if proxy_template != "https://{cluster_id}.{namespace}:443":
        fail("production Deployment VCLUSTER_PROXY_SERVER_TEMPLATE must match vCluster TLS SAN service namespace")
    if kubeconfig_template != proxy_template:
        fail("production Deployment VCLUSTER_KUBECONFIG_SERVER_TEMPLATE must match proxy server template")
    service = services[0]
    service_spec = service.get("spec", {})
    if not isinstance(service_spec, dict) or service_spec.get("type") != "NodePort":
        fail("production Gateway Service must be NodePort for non-local live-gate access")


def validate_production_readiness_boundary_docs() -> None:
    paths = [
        DOCS_INDEX,
        PLAN,
        CURRENT_SPRINT,
        RECORDS_INDEX,
        PRODUCTION_READINESS_REVIEW,
    ]
    for path in paths:
        if not path.exists():
            fail(f"missing production readiness boundary doc {path.relative_to(PROJECT_ROOT)}")
        content = path.read_text(encoding="utf-8")
        missing = [token for token in REQUIRED_PRODUCTION_READINESS_DOC_TOKENS if token not in content]
        if missing:
            fail(f"{path.relative_to(PROJECT_ROOT)} must document production readiness boundary tokens: {', '.join(missing)}")


def validate_auth_dex_evidence() -> None:
    payload = load_json(AUTH_DEX_EVIDENCE)
    if payload.get("status") != "passed":
        fail("Auth/Dex production evidence status must be passed")
    shape = payload.get("auth_dex_production_shape")
    if not isinstance(shape, dict):
        fail("Auth/Dex production evidence must include auth_dex_production_shape")
    if shape.get("status") != "passed":
        fail("Auth/Dex production shape status must be passed")
    if shape.get("gateway_auth_mode") != "auth_service":
        fail("Auth/Dex production evidence gateway_auth_mode must be auth_service")
    proof_items = shape.get("proof_items")
    if not isinstance(proof_items, list):
        fail("Auth/Dex production evidence proof_items must be a list")
    missing = REQUIRED_AUTH_DEX_PROOF_ITEMS - {str(item).strip() for item in proof_items}
    if missing:
        fail(f"Auth/Dex production evidence proof_items missing {', '.join(sorted(missing))}")
    expected_statuses = {
        "anonymous_status": 401,
        "oidc_begin_status": 200,
        "oidc_complete_status": 200,
        "authorized_status": 200,
        "refresh_status": 200,
    }
    for field, expected in expected_statuses.items():
        if shape.get(field) != expected:
            fail(f"Auth/Dex production evidence requires {field}={expected}")


def validate_all() -> None:
    validate_production_profile()
    validate_production_deployment_contract()
    validate_auth_dex_evidence()
    validate_production_readiness_boundary_docs()
    for slice_id, spec in SLICES.items():
        validate_evidence(slice_id, spec["evidence"])
        validate_result_doc(slice_id, spec["result"])


def main() -> int:
    validate_all()
    print("Sprint 13 S01-S07 production-shaped evidence boundaries valid")
    return 0


if __name__ == "__main__":
    raise SystemExit(main())
