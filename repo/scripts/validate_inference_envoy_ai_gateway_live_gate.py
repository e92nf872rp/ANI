#!/usr/bin/env python3
"""Validate the C40 Envoy AI Gateway live-gate contract and evidence."""

from __future__ import annotations

import re
from pathlib import Path
from typing import Any

import yaml

ROOT = Path(__file__).resolve().parents[1]
DEFAULT_GATE = ROOT / "deploy/real-k8s-lab/inference-envoy-ai-gateway-live-gate.yaml"
DEFAULT_EVIDENCE = ROOT / "development-records/live-evidence/inference-envoy-ai-gateway-live-20260825.json"
PROFILE = "INFERENCE-SERVICE-ENVOY-AI-GATEWAY-LIVE-C40"
READINESS_CLAIMS = {
    "envoy_invocation_ready": True,
    "dynamic_publication_ready": False,
    "runtime_ready": False,
}
REQUIRED_CHECKS = {
    "envoy-resources-accepted",
    "adapter-ready",
    "valid-ak-nonstream-chat",
    "valid-ak-embedding",
    "valid-ak-sse-done",
    "missing-ak-401",
    "malformed-ak-401",
    "credential-location-boundary",
    "public-path-boundary",
    "workload-probes-ready",
    "expired-ak-401",
    "revoked-ak-immediate-401",
    "rpm-limit-429",
    "foreign-tenant-404",
    "authz-fail-closed-503",
    "auth-service-unreachable-503",
    "authorization-not-forwarded",
    "clusterip-only-backend",
    "control-plane-regression-pass",
    "secret-redaction-pass",
}
REQUIRED_ENV = {
    "KUBECONFIG",
    "ANI_C40_CONTROL_PLANE_URL",
    "ANI_C40_GATEWAY_URL",
    "ANI_C40_OWNER_ACCESS_TOKEN",
    "ANI_C40_FOREIGN_ACCESS_TOKEN",
}
REQUIRED_ENDPOINTS = {"ani_control_plane_api", "envoy_ai_gateway_chat", "envoy_ai_gateway_embeddings", "kubernetes_api"}
CONNECTION_STRING_RE = re.compile(r"\b(?:postgres(?:ql)?|redis|nats|mysql|mongodb)://", re.IGNORECASE)
SECRET_DATA_RE = re.compile(
    r'(?:\\?"kind\\?"\s*:\s*\\?"secret\\?"|\\?"data\\?"\s*:\s*\\?\{)', re.IGNORECASE
)


def fail(message: str) -> None:
    raise SystemExit(f"inference envoy ai gateway live gate invalid: {message}")


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
    if document.get("readiness_claims") != READINESS_CLAIMS:
        fail("readiness_claims must keep only static Envoy invocation ready")
    tools = document.get("required_tools")
    if not isinstance(tools, list) or "kubectl" not in tools:
        fail("required_tools must include kubectl")
    required_env = document.get("required_env")
    if not isinstance(required_env, list) or REQUIRED_ENV - set(required_env):
        fail("required_env must include every C40 runner input")
    endpoints = document.get("required_endpoints")
    if not isinstance(endpoints, list) or REQUIRED_ENDPOINTS - set(endpoints):
        fail("required_endpoints must include control plane, Envoy chat, Envoy embeddings, and Kubernetes")
    checks = document.get("live_checks")
    if not isinstance(checks, list):
        fail("live_checks must be a list")
    check_ids = {item.get("id") for item in checks if isinstance(item, dict)}
    missing = REQUIRED_CHECKS - check_ids
    if missing:
        fail(f"missing live checks: {', '.join(sorted(missing))}")
    policy = document.get("evidence_policy")
    if not isinstance(policy, dict) or not isinstance(policy.get("forbidden_content"), list):
        fail("evidence_policy.forbidden_content is required")
    if document.get("status") == "live":
        evidence_path = document.get("evidence")
        path = Path(evidence_path) if evidence_path else DEFAULT_EVIDENCE
        if not path.is_absolute():
            path = ROOT / path
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
    claims = {key: evidence.get(key) for key in READINESS_CLAIMS}
    if claims != READINESS_CLAIMS:
        fail("evidence must keep the C40 readiness boundary")
    if evidence.get("authorization_not_forwarded") is not True:
        fail("evidence must record authorization_not_forwarded=true")
    if evidence.get("clusterip_only_backend") is not True:
        fail("evidence must record clusterip_only_backend=true")
    raw = path.read_text(encoding="utf-8")
    scrubbed = raw.replace('"authorization_not_forwarded"', "").replace(
        '"authorization-not-forwarded"', ""
    )
    lowered = scrubbed.lower()
    if "authorization" in lowered or "bearer " in lowered:
        fail("evidence contains an Authorization header or Bearer value")
    if "ani_dev_" in lowered or "ani_prod_" in lowered:
        fail("evidence contains API key material")
    if "password" in lowered or CONNECTION_STRING_RE.search(raw):
        fail("evidence contains password or connection string material")
    if SECRET_DATA_RE.search(raw) or "secret data" in lowered:
        fail("evidence contains Kubernetes Secret data")
    checks = evidence.get("checks")
    if not isinstance(checks, list):
        fail("evidence checks must be a list")
    check_ids = {
        item.get("id") for item in checks if isinstance(item, dict) and item.get("status") == "passed"
    }
    missing = REQUIRED_CHECKS - check_ids
    if missing:
        fail(f"evidence missing passed checks: {', '.join(sorted(missing))}")


def main() -> None:
    validate_contract(load_gate(DEFAULT_GATE))
    print("inference envoy ai gateway live gate valid")


if __name__ == "__main__":
    main()
