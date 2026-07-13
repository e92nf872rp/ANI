#!/usr/bin/env python3
"""Offline checks for M1-INSTANCE-E audit contracts and schema."""

from __future__ import annotations

import pathlib
import sys
from typing import Any

import yaml


REQUIRED_CONTRACT_KEYWORDS = (
    "WorkloadPlanAuditStore",
    "MetadataPlanAuditStore",
    "instance_plan_audits",
    "tenant RLS",
    "rendered_manifests",
    "admission_allowed",
)
REQUIRED_SCHEMA_KEYWORDS = (
    "CREATE TABLE instance_plan_audits",
    "'sandbox'",
    "rendered_manifests  JSONB",
    "admission_allowed   BOOLEAN",
    "ALTER TABLE instance_plan_audits ENABLE ROW LEVEL SECURITY",
    "CREATE POLICY tenant_isolation ON instance_plan_audits",
)


def load_contract_text(root: pathlib.Path) -> str:
    chunks: list[str] = []
    for path in sorted(root.glob("*.yaml")):
        with path.open("r", encoding="utf-8") as handle:
            for doc in yaml.safe_load_all(handle):
                if isinstance(doc, dict):
                    chunks.append(str(doc))
    return "\n".join(chunks)


def main() -> int:
    root = pathlib.Path(sys.argv[1]) if len(sys.argv) > 1 else pathlib.Path("deploy/manifests/m1-instance-e")
    schema = pathlib.Path(sys.argv[2]) if len(sys.argv) > 2 else pathlib.Path("deploy/migrations/20260501_001_init_schema.sql")
    contract_text = load_contract_text(root)
    schema_text = schema.read_text(encoding="utf-8")

    errors: list[str] = []
    errors.extend(f"{root}: missing {keyword}" for keyword in REQUIRED_CONTRACT_KEYWORDS if keyword not in contract_text)
    errors.extend(f"{schema}: missing {keyword}" for keyword in REQUIRED_SCHEMA_KEYWORDS if keyword not in schema_text)
    if errors:
        for error in errors:
            print(error, file=sys.stderr)
        return 1

    print(f"validated instance audit contracts under {root}")
    return 0


if __name__ == "__main__":
    raise SystemExit(main())
