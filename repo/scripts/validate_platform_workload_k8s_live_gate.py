#!/usr/bin/env python3
"""Validate the CPU PlatformWorkload Kubernetes live gate contract."""

from __future__ import annotations

from pathlib import Path
from typing import Any

import yaml

ROOT = Path(__file__).resolve().parents[1]
DEFAULT_GATE = ROOT / "deploy/real-k8s-lab/platform-workload-k8s-live-gate.yaml"
MIGRATION = ROOT / "deploy/migrations/20260815000100_platform_workloads.sql"
DEFAULT_EVIDENCE = ROOT / "development-records/live-evidence/platform-workload-k8s-live-20260815.json"
PROFILE = "INFERENCE-PLATFORM-WORKLOAD-K8S-LIVE-GATE-C9"
REQUIRED_CHECKS = {
    "core-platform-workload-cpu-create",
    "kubectl-deployment-service-labels",
    "core-platform-workload-observe-running",
    "core-platform-workload-scale",
    "core-platform-workload-stop",
    "core-platform-workload-start",
    "gateway-restart-get",
    "core-platform-workload-delete",
    "tenant-jwt-forbidden",
    "logs-empty-until-live-log-store",
}


def fail(message: str) -> None:
    raise SystemExit(f"platform workload k8s live gate invalid: {message}")


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
    required_endpoints = {"ani_gateway_platform_workloads_api", "kubernetes_api"}
    if not isinstance(endpoints, list) or required_endpoints - set(endpoints):
        fail("required_endpoints must include Gateway platform-workloads and Kubernetes API")
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
    if evidence.get("gateway") != "lab-process-not-in-cluster-ani-gateway":
        fail("evidence must record that the in-cluster Gateway was not rolled out")
    raw = path.read_text(encoding="utf-8")
    lowered = raw.lower()
    if "bearer " in lowered or "password" in lowered or "eyj" in raw:
        fail("evidence contains forbidden secret material")
    checks = evidence.get("checks")
    if not isinstance(checks, list):
        fail("evidence checks must be a list")
    check_ids = {item.get("id") for item in checks if isinstance(item, dict) and item.get("status") == "passed"}
    missing = REQUIRED_CHECKS - check_ids
    if missing:
        fail(f"evidence missing passed checks: {', '.join(sorted(missing))}")


def validate_migration(sql: str) -> None:
    normalized = " ".join(sql.lower().split())
    for marker in (
        "create table if not exists platform_workloads",
        "create table if not exists platform_workload_intents",
        "idx_platform_workloads_active_name",
        "where not deleted",
        "enable row level security",
        "force row level security",
        "app.current_tenant_id",
        "not tenant /instances",
    ):
        if marker not in normalized:
            fail(f"migration missing {marker}")


def main() -> None:
    validate_contract(load_gate(DEFAULT_GATE))
    if not MIGRATION.exists():
        fail(f"missing {MIGRATION}")
    validate_migration(MIGRATION.read_text(encoding="utf-8"))
    print("platform workload k8s live gate valid")


if __name__ == "__main__":
    main()
