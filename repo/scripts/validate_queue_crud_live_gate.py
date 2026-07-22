#!/usr/bin/env python3
"""Validate QUEUE-CRUD-LIVE-GATE YAML structure and required fields."""

from __future__ import annotations

import sys
from pathlib import Path

import yaml

ROOT = Path(__file__).resolve().parents[1]
GATE_FILE = ROOT / "deploy/real-k8s-lab/queue-crud-live-gate.yaml"

REQUIRED_GATE_FIELDS = {"profile", "status", "description", "required_tools", "live_checks", "readiness_levels"}
REQUIRED_LIVE_CHECK_IDS = {
    "volcano-queue-crd-registered",
    "gateway-queue-list",
    "gateway-queue-create",
    "gateway-queue-delete",
    "gateway-platform-default-delete-protected",
    "volcano-crd-persists-after-create",
}


def main() -> int:
    errors: list[str] = []

    if not GATE_FILE.exists():
        print(f"[FAIL] Gate file not found: {GATE_FILE}")
        return 1

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

    for tool in ("kubectl", "curl"):
        if tool not in gate.get("required_tools", []):
            errors.append(f"Gate required_tools should contain '{tool}'")

    rl = gate.get("readiness_levels", {})
    if "contract" not in rl or "live" not in rl:
        errors.append("Gate readiness_levels should contain 'contract' and 'live'")

    if errors:
        for e in errors:
            print(f"  [FAIL] {e}")
        return 1
    print("[PASS] QUEUE-CRUD-LIVE-GATE structure valid")
    return 0


if __name__ == "__main__":
    sys.exit(main())
