#!/usr/bin/env python3
"""Validate GPU-SCHEDULING-SMOKE-AB live gate YAML structure and required fields."""

from __future__ import annotations

import sys
from pathlib import Path

import yaml

ROOT = Path(__file__).resolve().parents[1]
GATE_FILE = ROOT / "deploy/real-k8s-lab/gpu-scheduling-live-gate.yaml"
SMOKE_A_FILE = ROOT / "deploy/real-k8s-lab/gpu-scheduling-smoke-a-job.yaml"
SMOKE_B_FILE = ROOT / "deploy/real-k8s-lab/gpu-scheduling-smoke-b-job.yaml"

REQUIRED_GATE_FIELDS = {"profile", "status", "description", "required_tools", "live_checks", "readiness_levels"}
REQUIRED_LIVE_CHECK_IDS = {
    "volcano-scheduler-running",
    "hami-scheduler-running",
    "volcano-queues-exist",
    "smoke-a-whole-gpu-scheduling",
    "smoke-b-vgpu-scheduling",
}


def validate_gate_yaml() -> list[str]:
    errors: list[str] = []
    if not GATE_FILE.exists():
        errors.append(f"Gate file not found: {GATE_FILE}")
        return errors

    with GATE_FILE.open() as f:
        gate = yaml.safe_load(f)

    missing = REQUIRED_GATE_FIELDS - set(gate.keys())
    if missing:
        errors.append(f"Gate YAML missing required fields: {missing}")

    live_check_ids = {check.get("id") for check in gate.get("live_checks", [])}
    missing_checks = REQUIRED_LIVE_CHECK_IDS - live_check_ids
    if missing_checks:
        errors.append(f"Gate YAML missing required live_check IDs: {missing_checks}")

    if gate.get("status") != "live":
        errors.append(f"Gate status should be 'live', got '{gate.get('status')}'")

    if "kubectl" not in gate.get("required_tools", []):
        errors.append("Gate required_tools should contain 'kubectl'")

    return errors


def validate_smoke_jobs() -> list[str]:
    errors: list[str] = []

    for name, path, expect_scheduler in [
        ("Smoke A", SMOKE_A_FILE, True),
        ("Smoke B", SMOKE_B_FILE, False),
    ]:
        if not path.exists():
            errors.append(f"{name} Job file not found: {path}")
            continue
        with path.open() as f:
            job = yaml.safe_load(f)

        if job.get("kind") != "Job":
            errors.append(f"{name}: kind should be Job")
        if job.get("metadata", {}).get("namespace") != "ani-system":
            errors.append(f"{name}: namespace should be ani-system")

        template = job.get("spec", {}).get("template", {})
        spec = template.get("spec", {})

        ns = spec.get("nodeSelector", {})
        if ns.get("ani.kubercloud.io/gpu-node") != "true":
            errors.append(f"{name}: nodeSelector should have ani.kubercloud.io/gpu-node=true")

        if expect_scheduler and spec.get("schedulerName") != "volcano":
            errors.append(f"{name}: schedulerName should be volcano")

        container = spec.get("containers", [{}])[0]
        limits = container.get("resources", {}).get("limits", {})
        if limits.get("nvidia.com/gpu") != "1":
            errors.append(f"{name}: resources.limits.nvidia.com/gpu should be '1'")

    return errors


def main() -> int:
    errors = validate_gate_yaml() + validate_smoke_jobs()
    if errors:
        for e in errors:
            print(f"  [FAIL] {e}")
        return 1
    print("[PASS] GPU-SCHEDULING-SMOKE-AB live gate structure valid")
    return 0


if __name__ == "__main__":
    sys.exit(main())
