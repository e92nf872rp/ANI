#!/usr/bin/env python3
"""Offline checks for Gateway instance workload provider runtime wiring."""

from __future__ import annotations

import pathlib
import sys


ROOT = pathlib.Path(__file__).resolve().parents[1]
GATEWAY = ROOT / "services" / "ani-gateway"
ROUTER = GATEWAY / "internal" / "router"

REQUIRED_SNIPPETS: tuple[tuple[pathlib.Path, str], ...] = (
    (GATEWAY / "workload_runtime.go", "WORKLOAD_PROVIDER"),
    (GATEWAY / "workload_runtime.go", "newGatewayInstanceWorkloadRuntime"),
    (GATEWAY / "main.go", "newGatewayInstanceWorkloadRuntime"),
    (GATEWAY / "main.go", "InstanceWorkloadRuntime"),
    (ROUTER / "instance_workload_runtime.go", "DefaultInstanceWorkloadRuntime"),
    (ROUTER / "router.go", "InstanceWorkloadRuntime"),
    (ROUTER / "demo_instances.go", "workload.DryRun"),
    (ROUTER / "demo_instances.go", "instanceDevProfile"),
)


def main() -> int:
    errors: list[str] = []
    for path, snippet in REQUIRED_SNIPPETS:
        text = path.read_text(encoding="utf-8")
        if snippet not in text:
            errors.append(f"{path.relative_to(ROOT)}: missing {snippet!r}")

    if errors:
        for error in errors:
            print(error, file=sys.stderr)
        return 1

    print("validated Gateway instance workload provider runtime wiring")
    return 0


if __name__ == "__main__":
    raise SystemExit(main())
