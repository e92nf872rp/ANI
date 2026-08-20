#!/usr/bin/env python3
"""Validate the engine.env/command and volcano vGPU live gate."""

from __future__ import annotations

from pathlib import Path
from typing import Any

import yaml

ROOT = Path(__file__).resolve().parents[1]
DEFAULT_GATE = ROOT / "deploy/real-k8s-lab/inference-engine-vgpu-live-gate.yaml"
DEFAULT_EVIDENCE = ROOT / "development-records/live-evidence/inference-engine-vgpu-live-20260819.json"
PROFILE = "INFERENCE-SERVICE-ENGINE-VGPU-LIVE-GATE-C36"
GATEWAY_EVIDENCE = "ani-system/ani-gateway"
REQUIRED_CHECKS = {
    "reserved-engine-env-400",
    "capabilities-advertise-vgpu-spec",
    "product-create-vgpu-202",
    "runtime-command-is-tenant-argv",
    "runtime-args-empty",
    "runtime-tenant-env-injected",
    "runtime-volcano-vgpu-resources",
    "runtime-no-nvidia-gpu",
    "invocation-url-null",
    "test-service-deleted",
    "service-retained",
    "production-gateway-auth-mode-preserved",
    "lws-cross-node-skipped",
}


def fail(message: str) -> None:
    raise SystemExit(f"inference engine vgpu live gate invalid: {message}")


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
    if not isinstance(tools, list) or "kubectl" not in tools:
        fail("required_tools must include kubectl")
    endpoints = document.get("required_endpoints")
    required_endpoints = {
        "ani_gateway_inference_services_api",
        "inference_control_grpc",
        "kubernetes_api",
    }
    if not isinstance(endpoints, list) or required_endpoints - set(endpoints):
        fail("required_endpoints must include inference HTTP, inference gRPC, and Kubernetes API")
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
    if evidence.get("entry") != GATEWAY_EVIDENCE or evidence.get("gateway") != GATEWAY_EVIDENCE:
        fail("evidence must record production ani-gateway as the HTTP entry")
    if evidence.get("auth_mode") != "auth_service":
        fail("evidence must record that ANI_AUTH_MODE stayed auth_service")
    if evidence.get("vgpu_spec") != "gpu-nvidia-geforce-rtx-4090-8x":
        fail("evidence must record the advertised volcano vGPU spec")
    if evidence.get("tenant_command") is not True or evidence.get("tenant_args_empty") is not True:
        fail("evidence must record tenant engine.command as the container command with empty args")
    if evidence.get("tenant_env") is not True:
        fail("evidence must record tenant engine.env injection")
    if evidence.get("reserved_env_http_status") != 400:
        fail("evidence must record reserved engine.env as HTTP 400")
    if evidence.get("nvidia_gpu_on_vgpu") is not False:
        fail("evidence must record that the vGPU runtime did not request nvidia.com/gpu")
    if evidence.get("pod_ready") is True:
        fail("evidence must not claim vGPU Pod Ready while the RWO PVC is held")
    if evidence.get("lws_live") != "skipped":
        fail("evidence must keep cross-node LWS skipped")
    if "ani-inference-gateway" in str(evidence.get("gateway") or "") or "ani-console" in str(evidence.get("entry") or ""):
        fail("evidence must not use a second Gateway or Console entry")
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
    print("inference engine vgpu live gate valid")


if __name__ == "__main__":
    main()
