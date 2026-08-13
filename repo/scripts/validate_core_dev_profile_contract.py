#!/usr/bin/env python3
"""Validate Sprint 3 CORE-DEV-PROFILE-A boundary and contract guardrails."""

from __future__ import annotations

from pathlib import Path

import yaml


ROOT = Path(__file__).resolve().parents[1]

CORE_P0_PATH_PREFIXES = (
    "/instances",
    "/instance-operations",
    "/networks",
    "/volumes",
    "/filesystems",
    "/objects",
    "/vector-stores",
)

CORE_DEV_PROFILE_SCHEMAS = (
    "InstanceRecord",
    "NetworkVPC",
    "NetworkSubnet",
    "NetworkSecurityGroup",
    "NetworkLoadBalancer",
    "StorageVolume",
    "StorageFilesystem",
    "StorageObject",
    "VectorStore",
)

GATEWAY_RESPONSE_FILES = (
    "services/ani-gateway/internal/router/instances.go",
    "services/ani-gateway/internal/router/network_resources.go",
    "services/ani-gateway/internal/router/storage_resources.go",
    "services/ani-gateway/internal/router/vector_store_resources.go",
)


def fail(message: str) -> None:
    raise SystemExit(f"core dev profile contract invalid: {message}")


def read(path: str) -> str:
    return (ROOT / path).read_text(encoding="utf-8")


def load_yaml(path: str) -> dict:
    return yaml.safe_load(read(path))


def validate_docs() -> None:
    docs = {
        "ANI-DOCS-INDEX.md": read("../ANI-DOCS-INDEX.md"),
        "ANI-06-开发计划.md": read("../ANI-06-开发计划.md"),
        "development-records/README.md": read("development-records/README.md"),
        "development-records/core-dev-profile-a-boundary-contract.md": read("development-records/core-dev-profile-a-boundary-contract.md"),
    }
    for path, text in docs.items():
        if "CORE-DEV-PROFILE-A" not in text:
            fail(f"{path} must reference CORE-DEV-PROFILE-A")
    record = docs["development-records/core-dev-profile-a-boundary-contract.md"]
    for required in (
        "不做 ANI Services 业务 mock",
        "不能伪装成 real provider",
        "Services 业务",
    ):
        if required not in record:
            fail(f"core-dev-profile-a-boundary-contract.md missing boundary statement {required!r}")


def validate_openapi() -> None:
    core = load_yaml("api/openapi/v1.yaml")
    services = load_yaml("api/openapi/services/v1.yaml")
    schemas = core.get("components", {}).get("schemas", {})

    marker = schemas.get("CoreDevProfileInfo")
    if not marker:
        fail("Core API schema CoreDevProfileInfo is required")
    marker_props = marker.get("properties", {})
    for field in ("mode", "provider", "real_provider", "reason"):
        if field not in marker_props:
            fail(f"CoreDevProfileInfo.{field} is required")

    for schema_name in CORE_DEV_PROFILE_SCHEMAS:
        props = schemas.get(schema_name, {}).get("properties", {})
        dev_profile = props.get("dev_profile", {})
        if dev_profile.get("$ref") != "#/components/schemas/CoreDevProfileInfo":
            fail(f"{schema_name}.dev_profile must reference CoreDevProfileInfo")

    leaked = [
        path for path in services.get("paths", {})
        if path.startswith(CORE_P0_PATH_PREFIXES)
    ]
    if leaked:
        fail(f"Services API must not contain Core P0 paths: {leaked}")


def validate_gateway() -> None:
    helper = read("services/ani-gateway/internal/router/core_dev_profile.go")
    for token in ("coreDevProfileResponse", "localCoreDevProfile", "RealProvider: false"):
        if token not in helper:
            fail(f"core_dev_profile.go missing {token!r}")

    for file_path in GATEWAY_RESPONSE_FILES:
        text = read(file_path)
        if "DevProfile" not in text or "localCoreDevProfile(" not in text:
            fail(f"{file_path} must expose local dev_profile in Core P0 responses")

    stubs = read("services/ani-gateway/internal/router/stubs.go")
    forbidden_route_fragments = (
        '"/instances',
        '"/instance-operations',
        '"/networks',
        '"/volumes',
        '"/filesystems',
        '"/objects',
        '"/vector-stores',
    )
    for fragment in forbidden_route_fragments:
        if fragment in stubs:
            fail(f"stubs.go must not register Core P0 path {fragment}")

    for file_path in GATEWAY_RESPONSE_FILES:
        text = read(file_path)
        if "NOT_IMPLEMENTED" in text:
            fail(f"{file_path} must not return NOT_IMPLEMENTED for Core P0 paths")


def validate_tests() -> None:
    test_files = (
        "services/ani-gateway/internal/router/core_dev_profile_test.go",
        "services/ani-gateway/internal/router/instances_test.go",
        "services/ani-gateway/internal/router/network_resources_test.go",
        "services/ani-gateway/internal/router/storage_resources_test.go",
        "services/ani-gateway/internal/router/vector_store_resources_test.go",
    )
    for file_path in test_files:
        if "requireLocalCoreDevProfile" not in read(file_path):
            fail(f"{file_path} must assert local dev_profile")


def main() -> None:
    validate_docs()
    validate_openapi()
    validate_gateway()
    validate_tests()
    print("core dev profile contract valid")


if __name__ == "__main__":
    main()
