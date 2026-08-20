#!/usr/bin/env python3
"""Validate the Harbor image_id InferenceService live gate."""

from __future__ import annotations

from pathlib import Path
from typing import Any

import yaml

ROOT = Path(__file__).resolve().parents[1]
DEFAULT_GATE = ROOT / "deploy/real-k8s-lab/inference-create-image-harbor-live-gate.yaml"
DEFAULT_EVIDENCE = ROOT / "development-records/live-evidence/inference-create-image-harbor-live-20260818.json"
PROFILE = "INFERENCE-SERVICE-CREATE-IMAGE-HARBOR-LIVE-GATE-C31"
ENTRY_EVIDENCE = "ani-system/ani-console"
GATEWAY_EVIDENCE = "ani-system/ani-gateway"
REQUIRED_CHECKS = {
    "tenant-local-pvc-ready",
    "in-cluster-inference-service-running",
    "production-gateway-auth-mode-preserved",
    "harbor-tenant-image-seeded",
    "console-nginx-inference-list",
    "create-missing-image-rejected",
    "create-unknown-image-id-rejected",
    "create-model",
    "create-model-version-pvc",
    "product-inference-service-cpu-create",
    "kubectl-vllm-cpu-deployment",
    "inference-service-running-health-smoke",
    "inference-service-stop",
    "inference-service-start",
    "inference-service-delete",
    "existing-tenant-namespace-preserved",
    "gpu-live-skipped-no-device-plugin",
}


def fail(message: str) -> None:
    raise SystemExit(f"inference create image harbor live gate invalid: {message}")


def load_gate(path: Path) -> dict[str, Any]:
    if not path.exists():
        fail(f"missing {path}")
    data = yaml.safe_load(path.read_text(encoding="utf-8"))
    if not isinstance(data, dict):
        fail(f"{path} must be a YAML object")
    return data


def validate_contract(document: dict[str, Any]) -> None:
    if document.get("profile") != PROFILE:
        fail(f"profile must be {PROFILE}")
    if document.get("status") not in {"contract", "live"}:
        fail("status must be contract or live")
    tools = document.get("required_tools")
    if not isinstance(tools, list) or "kubectl" not in tools or "docker" not in tools:
        fail("required_tools must include kubectl and docker")
    endpoints = document.get("required_endpoints")
    required_endpoints = {
        "ani_console_nginx_api_proxy",
        "ani_gateway_models_api",
        "ani_gateway_inference_services_api",
        "ani_gateway_registry_api",
        "inference_control_grpc",
        "model_service_grpc",
        "harbor_registry",
    }
    if not isinstance(endpoints, list) or required_endpoints - set(endpoints):
        fail("required_endpoints must include Console, Gateway, Harbor, and model/inference gRPC")
    checks = document.get("live_checks")
    if not isinstance(checks, list):
        fail("live_checks must be a list")
    check_ids = {item.get("id") for item in checks if isinstance(item, dict)}
    missing = REQUIRED_CHECKS - check_ids
    if missing:
        fail(f"missing live checks: {', '.join(sorted(missing))}")
    policy = document.get("evidence_policy")
    if not isinstance(policy, dict) or "forbidden_content" not in policy:
        fail("evidence_policy.forbidden_content is required")
    if document.get("status") == "live":
        evidence_path = document.get("evidence")
        path = ROOT / evidence_path if evidence_path else DEFAULT_EVIDENCE
        validate_evidence(path)


def validate_evidence(path: Path) -> None:
    if not path.exists():
        fail(f"status=live requires redacted evidence at {path}")
    try:
        evidence = yaml.safe_load(path.read_text(encoding="utf-8"))
    except yaml.YAMLError as err:
        fail(f"malformed evidence: {err}")
    if not isinstance(evidence, dict):
        fail("evidence must be a JSON object")
    if evidence.get("profile") != PROFILE:
        fail(f"evidence profile must be {PROFILE}")
    if evidence.get("status") != "passed":
        fail("evidence status must be passed")
    if evidence.get("entry") != ENTRY_EVIDENCE:
        fail("evidence must record ani-console as the HTTP entry")
    if evidence.get("gateway") != GATEWAY_EVIDENCE:
        fail("evidence must record production ani-gateway")
    if evidence.get("auth_mode") != "auth_service":
        fail("evidence must record that ANI_AUTH_MODE stayed auth_service")
    if evidence.get("auth") != "tenant_bearer_via_console":
        fail("evidence must record tenant Bearer via Console")
    if evidence.get("image_source") != "request_image_id":
        fail("evidence must record that the runtime image came from request image_id")
    if evidence.get("model_source") != "tenant_local_pvc":
        fail("evidence must record tenant-local PVC as the model source")
    if "ani-inference-gateway" in str(evidence.get("gateway") or ""):
        fail("evidence must not use a second Gateway entry")
    if evidence.get("gpu_ready") is True or evidence.get("runtime_ready") is True:
        fail("evidence must not mark GPU or runtime ready")
    raw = path.read_text(encoding="utf-8")
    lowered = raw.lower()
    if "bearer " in lowered or "password" in lowered or "eyj" in raw:
        fail("evidence contains forbidden secret material")
    if "postgres://" in lowered or "nats://" in lowered or "redis://" in lowered:
        fail("evidence contains a connection string")
    checks = evidence.get("checks")
    if not isinstance(checks, list):
        fail("evidence checks must be a list")
    check_ids = {item.get("id") for item in checks if isinstance(item, dict) and item.get("status") == "passed"}
    missing = REQUIRED_CHECKS - check_ids
    if missing:
        fail(f"evidence missing passed checks: {', '.join(sorted(missing))}")


def main() -> None:
    validate_contract(load_gate(DEFAULT_GATE))
    print("inference create image harbor live gate valid")


if __name__ == "__main__":
    main()
