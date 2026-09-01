#!/usr/bin/env python3
"""Validate M1-WKID-A Workload Identity contract guardrails."""

from __future__ import annotations

from pathlib import Path

import yaml


ROOT = Path(__file__).resolve().parents[1]


def fail(message: str) -> None:
    raise SystemExit(f"workload identity contract invalid: {message}")


def require_text(path: Path, *needles: str) -> str:
    text = path.read_text(encoding="utf-8")
    for needle in needles:
        if needle not in text:
            fail(f"{path.relative_to(ROOT)} missing {needle!r}")
    return text


def validate_openapi() -> None:
    spec = yaml.safe_load((ROOT / "api/openapi/v1.yaml").read_text(encoding="utf-8"))
    schemas = spec.get("components", {}).get("schemas", {})
    instance = schemas.get("InstanceRecord", {})
    props = instance.get("properties", {})
    if props.get("workload_identity", {}).get("$ref") != "#/components/schemas/WorkloadIdentityBinding":
        fail("InstanceRecord.workload_identity must reference WorkloadIdentityBinding")
    binding = schemas.get("WorkloadIdentityBinding")
    if not binding:
        fail("WorkloadIdentityBinding schema is required")
    binding_props = binding.get("properties", {})
    if "key_value" in binding_props:
        fail("WorkloadIdentityBinding must not expose API key plaintext")
    for required in ("key_id", "key_prefix", "scopes", "active", "created_at", "revoked_at"):
        if required not in binding_props:
            fail(f"WorkloadIdentityBinding.{required} is required")


def validate_code_contract() -> None:
    require_text(
        ROOT / "pkg/ports/workload_runtime.go",
        "type WorkloadIdentityService interface",
        "BindScopedKey",
        "RevokeForInstance",
        "GetForInstance",
        "Identity         *WorkloadIdentityBinding",
    )
    require_text(
        ROOT / "pkg/adapters/runtime/instance_service.go",
        "workload_identity_bind",
        "workload_identity_revoke",
        "WithWorkloadIdentityService",
    )
    require_text(
        ROOT / "pkg/adapters/runtime/dryrun_renderer.go",
        "ANI_WORKLOAD_TOKEN",
        "secretKeyRef",
        "workloadIdentityEnv",
    )
    require_text(
        ROOT / "pkg/adapters/runtime/instance_orchestrator.go",
        "WithInstanceOrchestratorWorkloadIdentityService",
        "request.Spec.Identity = identity",
    )
    require_text(
        ROOT / "pkg/bootstrap/deps.go",
        "WorkloadIdentity     ports.WorkloadIdentityService",
        "NewMetadataWorkloadIdentityService",
    )


def validate_migration() -> None:
    migration = require_text(
        ROOT / "deploy/migrations/20260520000700_workload_identity_api_keys.sql",
        "ALTER COLUMN instance_id TYPE TEXT",
        "idx_api_keys_instance",
    )
    if "UUID" in migration.split("COMMENT ON COLUMN", 1)[0]:
        fail("workload identity migration must not force instance_id back to UUID")


def main() -> None:
    validate_openapi()
    validate_code_contract()
    validate_migration()
    print("workload identity contract valid")


if __name__ == "__main__":
    main()
