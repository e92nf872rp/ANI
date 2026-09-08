#!/usr/bin/env python3
"""Validate the controlled seven-service image workflow contract."""

from __future__ import annotations

from pathlib import Path
from typing import Any

import yaml


WORKFLOW_PATH = Path(__file__).resolve().parents[1] / ".github/workflows/build-image.yml"
MAKEFILE_PATH = Path(__file__).resolve().parents[1] / "Makefile"
TRIVY_ACTION = "aquasecurity/trivy-action@ed142fd0673e97e23eac54620cfb913e5ce36c25"
EXPECTED_MATRIX = [
    {"service": "ani-gateway", "image": "ani-gateway", "dockerfile": "services/ani-gateway/Dockerfile"},
    {"service": "auth-service", "image": "ani-auth-service", "dockerfile": "services/auth-service/Dockerfile"},
    {"service": "model-service", "image": "model-service", "dockerfile": "services/model-service/Dockerfile"},
    {"service": "task-service", "image": "task-service", "dockerfile": "services/task-service/Dockerfile"},
    {"service": "inference-service", "image": "inference-service", "dockerfile": "services/inference-service/Dockerfile"},
    {"service": "tenant-service", "image": "tenant-service", "dockerfile": "services/tenant-service/Dockerfile"},
    {"service": "metering-service", "image": "metering-service", "dockerfile": "services/metering-service/Dockerfile"},
]
LOCAL_RUNTIME_TARGETS = [
    "image-gateway",
    "image-auth-service",
    "image-model-service",
    "image-task-service",
    "image-inference-service",
    "image-tenant-service",
    "image-metering-service",
]


def load_workflow(path: Path = WORKFLOW_PATH) -> dict[str, Any]:
    document = yaml.safe_load(path.read_text(encoding="utf-8"))
    if not isinstance(document, dict):
        raise ValueError("build image workflow must be a YAML object")
    return document


def load_makefile(path: Path = MAKEFILE_PATH) -> str:
    return path.read_text(encoding="utf-8")


def validate_local_makefile(makefile: str) -> list[str]:
    errors: list[str] = []
    if "RUNTIME_IMAGE_BUILD := docker buildx build" not in makefile:
        errors.append("local runtime image targets must use docker buildx build")
    expected_flags = "RUNTIME_IMAGE_BUILD_FLAGS := --load --sbom=true --provenance=mode=max"
    if expected_flags not in makefile:
        errors.append("local runtime image targets must load images with SBOM and max provenance")
    expected_command = "$(RUNTIME_IMAGE_BUILD) $(RUNTIME_IMAGE_BUILD_FLAGS) -t"
    for target in LOCAL_RUNTIME_TARGETS:
        marker = f"{target}:\n"
        if marker not in makefile:
            errors.append(f"local runtime image target is missing: {target}")
            continue
        body = makefile.split(marker, 1)[1].split("\n\n", 1)[0]
        if expected_command not in body:
            errors.append(f"{target} must use attested local runtime image build flags")
    return errors


def runtime_job(workflow: dict[str, Any]) -> dict[str, Any]:
    return workflow.get("jobs", {}).get("runtime-services", {})


def runtime_build_step(workflow: dict[str, Any]) -> dict[str, Any]:
    return next(
        step
        for step in runtime_job(workflow).get("steps", [])
        if step.get("name") == "Build runtime image"
    )


def validate(workflow: dict[str, Any]) -> list[str]:
    errors: list[str] = []
    environment = workflow.get("env", {})
    registry = str(environment.get("REGISTRY", ""))
    registry_host = str(environment.get("REGISTRY_HOST", ""))
    if not registry or registry_host != registry.split("/", 1)[0] or "/" in registry_host:
        errors.append("runtime workflow registry host must exclude the repository project path")
    job = runtime_job(workflow)
    if not job:
        return ["runtime-services job is missing"]
    matrix = job.get("strategy", {}).get("matrix", {}).get("include")
    if matrix != EXPECTED_MATRIX:
        errors.append("runtime-services matrix must contain exactly the seven controlled services")
    try:
        build = runtime_build_step(workflow)
    except StopIteration:
        return errors + ["Build runtime image step is missing"]
    build_with = build.get("with", {})
    if build_with.get("context") != "." or build_with.get("file") != "${{ matrix.dockerfile }}":
        errors.append("runtime images must use repository root context and their controlled Dockerfile")
    if build_with.get("push") is not True:
        errors.append("runtime image build must push in the publication workflow")
    expected_tag = "${{ env.REGISTRY }}/${{ matrix.image }}:${{ github.sha }}"
    if build_with.get("tags") != expected_tag or ":latest" in str(build_with.get("tags")):
        errors.append("runtime images must publish a single SHA tag and never latest")
    if build_with.get("platforms") != "linux/amd64,linux/arm64":
        errors.append("runtime images must build linux/amd64 and linux/arm64")
    if build_with.get("provenance") != "mode=max" or build_with.get("sbom") is not True:
        errors.append("runtime image builds must emit max provenance and SBOM attestations")
    steps = job.get("steps", [])
    login = next((step for step in steps if step.get("name") == "Login to Harbor"), None)
    if not login or login.get("with", {}).get("registry") != "${{ env.REGISTRY_HOST }}":
        errors.append("runtime workflow login must use the registry host")
    trivy = next((step for step in steps if step.get("name") == "Scan runtime image"), None)
    if not trivy or trivy.get("uses") != TRIVY_ACTION:
        errors.append("runtime image scan must use the fixed Trivy action commit")
    elif "steps.build.outputs.digest" not in str(trivy.get("with", {}).get("image-ref")):
        errors.append("Trivy must scan the pushed image digest")
    record = next((step for step in steps if step.get("name") == "Record digest evidence"), None)
    upload = next((step for step in steps if step.get("name") == "Upload digest evidence"), None)
    if not record or "steps.build.outputs.digest" not in str(record.get("run")):
        errors.append("runtime workflow must record the build digest")
    if not upload or upload.get("uses") != "actions/upload-artifact@v4" or upload.get("with", {}).get("if-no-files-found") != "error":
        errors.append("runtime workflow must upload a fail-closed digest artifact")
    return errors


def main() -> int:
    errors = validate(load_workflow()) + validate_local_makefile(load_makefile())
    for error in errors:
        print(f"ERROR: {error}")
    if errors:
        return 1
    print("runtime image workflow contract valid")
    return 0


if __name__ == "__main__":
    raise SystemExit(main())
