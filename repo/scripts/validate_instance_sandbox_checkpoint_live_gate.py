#!/usr/bin/env python3
from __future__ import annotations

import argparse
import json
import re
import sys
from pathlib import Path
from typing import Any

import yaml


ROOT = Path(__file__).resolve().parents[1]
DEFAULT_GATE = ROOT / "deploy/real-k8s-lab/instance-sandbox-checkpoint-live-gate.yaml"
PROFILE = "INSTANCE-SANDBOX-CHECKPOINT-LIVE-GATE-A"
REQUIRED_CHECKS = {
    "sandbox-workspace-pvc-bound",
    "checkpoint-create-ready",
    "checkpoint-list-after-gateway-restart",
    "workspace-restore-content",
    "checkpoint-clone-content",
    "keep-memory-capability-422",
    "legacy-emptydir-capability-422",
    "postgres-task-persistence",
    "sandbox-checkpoint-cleanup",
}
FORBIDDEN_PATTERNS = (
    re.compile(r"(?i)authorization[\"\s]*[:=][\"\s]*bearer"),
    re.compile(r"eyJ[A-Za-z0-9_-]{20,}\.[A-Za-z0-9_-]{20,}"),
    re.compile(r"(?i)(password|database_url|preview_url|internal_ip)[\"\s]*[:=]"),
    re.compile(r"(?i)(postgres|postgresql)://[^\s\"]+"),
    re.compile(r"(?<![0-9])(?:10\.|192\.168\.|172\.(?:1[6-9]|2[0-9]|3[01])\.)[0-9.]+"),
)


def fail(message: str) -> None:
    raise ValueError(message)


def load_yaml(path: Path) -> dict[str, Any]:
    document = yaml.safe_load(path.read_text(encoding="utf-8"))
    if not isinstance(document, dict):
        fail(f"{path} must contain an object")
    return document


def validate_gate(document: dict[str, Any]) -> None:
    if document.get("profile") != PROFILE:
        fail(f"profile must be {PROFILE}")
    status = document.get("status")
    if status not in {"contract", "live"}:
        fail("status must be contract or live")
    checks = document.get("live_checks")
    if not isinstance(checks, list):
        fail("live_checks must be a list")
    ids = {item.get("id") for item in checks if isinstance(item, dict)}
    missing = REQUIRED_CHECKS - ids
    if missing:
        fail(f"missing checks: {', '.join(sorted(missing))}")
    if status == "live" and not str(document.get("evidence", "")).strip():
        fail("live gate must reference evidence")
    policy = document.get("evidence_policy")
    if not isinstance(policy, dict) or "forbidden_content" not in policy:
        fail("evidence_policy.forbidden_content is required")


def validate_evidence(document: dict[str, Any], raw: str) -> None:
    if document.get("profile") != PROFILE or document.get("status") != "passed":
        fail("evidence must identify the profile with status=passed")
    if not isinstance(document.get("image"), str) or not document["image"].strip():
        fail("evidence image is required")
    digest = document.get("image_digest")
    if not isinstance(digest, str) or not re.fullmatch(r"sha256:[0-9a-f]{64}", digest):
        fail("evidence image_digest must be sha256")
    checks = document.get("checks")
    if not isinstance(checks, list):
        fail("evidence checks must be a list")
    states = {item.get("id"): item.get("status") for item in checks if isinstance(item, dict)}
    failed = sorted(check for check in REQUIRED_CHECKS if states.get(check) != "passed")
    if failed:
        fail(f"evidence checks not passed: {', '.join(failed)}")
    for pattern in FORBIDDEN_PATTERNS:
        if pattern.search(raw):
            fail(f"evidence contains forbidden sensitive pattern: {pattern.pattern}")


def main() -> int:
    parser = argparse.ArgumentParser()
    parser.add_argument("--gate", type=Path, default=DEFAULT_GATE)
    parser.add_argument("--evidence", type=Path)
    args = parser.parse_args()
    try:
        validate_gate(load_yaml(args.gate))
        if args.evidence:
            raw = args.evidence.read_text(encoding="utf-8")
            evidence = json.loads(raw)
            if not isinstance(evidence, dict):
                fail("evidence must contain an object")
            validate_evidence(evidence, raw)
    except (OSError, ValueError, json.JSONDecodeError, yaml.YAMLError) as err:
        print(f"sandbox checkpoint live gate invalid: {err}", file=sys.stderr)
        return 1
    print("sandbox checkpoint live gate validation passed")
    return 0


if __name__ == "__main__":
    raise SystemExit(main())
