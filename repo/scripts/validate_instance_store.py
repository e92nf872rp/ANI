#!/usr/bin/env python3
"""Offline checks for M1-INSTANCE-J instance persistence contracts."""

from __future__ import annotations

import pathlib
import sys

import yaml


MANIFEST_KEYWORDS = (
    "WorkloadInstanceStore",
    "MetadataInstanceStore",
    "workload_instances",
    "WorkloadInstanceOrchestrator",
    "WorkloadStatusReconciler",
    "tenant RLS",
    "WorkloadInstanceStore.Get",
    "WorkloadInstanceStore.List",
    "WorkloadInstanceRecord",
    "PlanningRuntime memory",
)

MIGRATION_KEYWORDS = (
    "CREATE TABLE workload_instances",
    "PRIMARY KEY (tenant_id, instance_id)",
    "resource_refs       JSONB",
    "state               TEXT NOT NULL",
    "CREATE INDEX idx_workload_instances_tenant",
    "ALTER TABLE workload_instances ENABLE ROW LEVEL SECURITY",
    "CREATE POLICY tenant_isolation ON workload_instances",
)


def load_manifests(root: pathlib.Path) -> str:
    chunks: list[str] = []
    for path in sorted(root.glob("*.yaml")):
        with path.open("r", encoding="utf-8") as handle:
            for doc in yaml.safe_load_all(handle):
                if isinstance(doc, dict):
                    chunks.append(str(doc))
    return "\n".join(chunks)


def main() -> int:
    root = pathlib.Path(sys.argv[1]) if len(sys.argv) > 1 else pathlib.Path("deploy/manifests/m1-instance-j")
    migration = pathlib.Path(sys.argv[2]) if len(sys.argv) > 2 else pathlib.Path("deploy/migrations/20260501000100_init_schema.sql")
    text = load_manifests(root)
    migration_text = migration.read_text(encoding="utf-8")

    errors = [f"{root}: missing {keyword}" for keyword in MANIFEST_KEYWORDS if keyword not in text]
    errors.extend(f"{migration}: missing {keyword}" for keyword in MIGRATION_KEYWORDS if keyword not in migration_text)
    if errors:
        for error in errors:
            print(error, file=sys.stderr)
        return 1

    print(f"validated instance store contracts under {root}")
    return 0


if __name__ == "__main__":
    raise SystemExit(main())
