#!/usr/bin/env python3
"""Validate the NVIDIA / Volcano GPU plugin node partition live gate."""

from __future__ import annotations

from pathlib import Path
from typing import Any

import yaml

ROOT = Path(__file__).resolve().parents[1]
DEFAULT_GATE = ROOT / "deploy/real-k8s-lab/gpu-plugin-node-partition-live-gate.yaml"
DEFAULT_EVIDENCE = ROOT / "development-records/live-evidence/gpu-plugin-node-partition-live-20260818.json"
PROFILE = "GPU-PLUGIN-NODE-PARTITION-LIVE-GATE-C33"
REQUIRED_CHECKS = {
    "wholecard-nodes-labeled",
    "vgpu-nodes-labeled",
    "nvidia-device-plugin-only-on-wholecard",
    "volcano-device-plugin-only-on-vgpu",
    "wholecard-nvidia-gpu-allocatable",
    "vgpu-nodes-without-nvidia-gpu",
    "vgpu-volcano-resources",
    "vgpu-nvidia-device-plugin-disabled",
}


def fail(message: str) -> None:
    raise SystemExit(f"gpu plugin node partition live gate invalid: {message}")


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
    if evidence.get("gpu_ready") is True or evidence.get("runtime_ready") is True:
        fail("evidence must not mark GPU or runtime ready")
    partition = evidence.get("partition")
    if not isinstance(partition, dict):
        fail("evidence partition must be an object")
    wholecard = partition.get("wholecard_nodes")
    vgpu = partition.get("vgpu_nodes")
    if not isinstance(wholecard, list) or not wholecard:
        fail("evidence must list wholecard nodes")
    if not isinstance(vgpu, list) or not vgpu:
        fail("evidence must list vgpu nodes")
    overlap = set(wholecard) & set(vgpu)
    if overlap:
        fail(f"nodes cannot be both wholecard and vgpu: {', '.join(sorted(overlap))}")
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
    print("gpu plugin node partition live gate valid")


if __name__ == "__main__":
    main()
