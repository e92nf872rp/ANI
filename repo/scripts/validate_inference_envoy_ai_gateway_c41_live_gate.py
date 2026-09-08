#!/usr/bin/env python3
"""Validate the local-only C41 multi-tenant live-gate contract."""

from __future__ import annotations

from pathlib import Path
from typing import Any

import yaml

ROOT = Path(__file__).resolve().parents[1]
DEFAULT_GATE = ROOT / "deploy/real-k8s-lab/inference-envoy-ai-gateway-c41-live-gate.yaml"
PROFILE = "INFERENCE-SERVICE-ENVOY-AI-GATEWAY-LIVE-C41"
REQUIRED_ENV = [
    "KUBECONFIG",
    "ANI_C41_CONTROL_PLANE_URL",
    "ANI_C41_GATEWAY_URL",
    "ANI_C41_TENANT_A_ACCESS_TOKEN",
    "ANI_C41_TENANT_B_ACCESS_TOKEN",
    "ANI_C41_CHAT_MODEL_VERSION_ID",
    "ANI_C41_EMBED_MODEL_VERSION_ID",
    "ANI_C41_CHAT_IMAGE_REF",
    "ANI_C41_EMBED_IMAGE_REF",
]
REQUIRED_CHECKS = {
    "tenant-a-chat-json-200", "tenant-a-chat-sse-200", "tenant-a-embeddings-200",
    "tenant-isolation-same-model", "tenant-b-a-only-404", "generate-embeddings-404",
    "embed-chat-404", "invalid-credentials-401", "body-model-wins-over-client-header",
    "tenant-service-spoof-ignored", "models-404", "rpm-429-retry-after",
    "service-ak-policy-overrides-tenant", "dependency-faults-503-no-vllm",
    "stop-unpublishes-before-workload-stop", "start-republishes-200",
    "delete-releases-same-tenant-name", "publisher-restart-idempotent",
    "no-managed-ak-secret", "temporary-key-log-redaction", "cleanup-complete",
}
FORBIDDEN_EVIDENCE_CONTENT = ["Authorization", "Bearer", "ani_", "prompts", "completions", "vectors", "Kubernetes Secret data"]


def fail(message: str) -> None:
    raise SystemExit(f"C41 AI Gateway live-gate contract invalid: {message}")


def load_gate(path: Path) -> dict[str, Any]:
    if not path.exists():
        fail(f"missing {path}")
    try:
        document = yaml.safe_load(path.read_text(encoding="utf-8"))
    except (OSError, yaml.YAMLError):
        fail("gate must be readable YAML")
    if not isinstance(document, dict):
        fail("gate must be a YAML object")
    return document


def validate_contract(document: dict[str, Any]) -> None:
    if document.get("profile") != PROFILE:
        fail(f"profile must be {PROFILE}")
    if document.get("status") != "contract":
        fail("local validator must not claim live status")
    if document.get("readiness_claims") != {"dynamic_publication_ready": False, "runtime_ready": False}:
        fail("readiness claims must keep the live boundary")
    if document.get("required_env") != REQUIRED_ENV:
        fail("required_env must be the exact C41 environment contract")
    if set(document.get("required_tools") or []) != {"kubectl"}:
        fail("required_tools must be exactly kubectl")
    endpoints = set(document.get("required_endpoints") or [])
    if endpoints != {"ani_control_plane_api", "envoy_ai_gateway_chat", "envoy_ai_gateway_embeddings", "kubernetes_api"}:
        fail("required_endpoints must be exact")
    checks = document.get("live_checks")
    check_ids = [entry.get("id") for entry in checks if isinstance(entry, dict)] if isinstance(checks, list) else []
    if len(check_ids) != len(checks or []) or len(set(check_ids)) != len(check_ids) or set(check_ids) != REQUIRED_CHECKS:
        fail("live_checks must cover the exact C41 acceptance matrix")
    policy = document.get("evidence_policy")
    if not isinstance(policy, dict) or policy.get("mode") != "atomic-0600" or policy.get("forbidden_content") != FORBIDDEN_EVIDENCE_CONTENT:
        fail("evidence policy must require atomic 0600 redaction")


def main() -> None:
    validate_contract(load_gate(DEFAULT_GATE))
    print("C41 inference Envoy AI Gateway live-gate contract valid (local only)")


if __name__ == "__main__":
    main()
