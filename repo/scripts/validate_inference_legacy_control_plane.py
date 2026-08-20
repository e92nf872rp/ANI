#!/usr/bin/env python3
"""Guard the retired inference.v1 / operator control plane from being rewired."""

from __future__ import annotations

import re
from pathlib import Path

import yaml


ROOT = Path(__file__).resolve().parents[1]
PROTO = ROOT / "api/proto/inference/v1/inference_service.proto"
CONTROL_PROTO = ROOT / "api/proto/inference/control/v1/inference_control.proto"
HELM_VALUES = ROOT / "deploy/helm/ani-platform/values.yaml"
GATEWAY_ROOT = ROOT / "services/ani-gateway"
OLD_IMPORT = "github.com/kubercloud/ani/pkg/generated/pb/inference/v1"
CONTROL_IMPORT = "github.com/kubercloud/ani/pkg/generated/pb/inference/control/v1"
SERVICE_IMPORT = "github.com/kubercloud/ani/services/inference-service/"
GENERATED_OLD = "pkg/generated/pb/inference/v1/"
GO_IMPORT_RE = re.compile(r'"([^"]+)"')
DEPRECATED_MARKERS = ("deprecated", "retired", "do not add callers")


def fail(message: str) -> None:
    raise SystemExit(f"inference legacy control plane invalid: {message}")


def parse_go_imports(path: Path) -> list[str]:
    imports: list[str] = []
    in_block = False
    for raw in path.read_text(encoding="utf-8").splitlines():
        line = raw.strip()
        if line.startswith("import ("):
            in_block = True
            continue
        if in_block and line.startswith(")"):
            in_block = False
            continue
        if in_block or line.startswith("import "):
            match = GO_IMPORT_RE.search(line)
            if match:
                imports.append(match.group(1))
    return imports


def iter_go_files(root: Path) -> list[Path]:
    return sorted(path for path in root.rglob("*.go") if path.is_file())


def relative(path: Path) -> str:
    return path.resolve().relative_to(ROOT.resolve()).as_posix()


def validate_proto_deprecated() -> None:
    if not PROTO.exists():
        fail(f"missing {PROTO.relative_to(ROOT)}")
    text = PROTO.read_text(encoding="utf-8")
    lowered = text.lower()
    if not any(marker in lowered for marker in DEPRECATED_MARKERS):
        fail("api/proto/inference/v1/inference_service.proto must mark the old control plane deprecated")
    if "InferenceServiceRPC" not in text or "GetEndpointURL" not in text:
        fail("legacy proto must remain a retired artifact, not be emptied")
    if not CONTROL_PROTO.exists():
        fail("missing inference.control.v1 proto")
    control = CONTROL_PROTO.read_text(encoding="utf-8")
    if "InferenceControl" not in control:
        fail("product control plane must stay on inference.control.v1.InferenceControl")
    if re.search(r"^\s*rpc\s+(GetEndpointURL|UpdateStatus)\b", control, re.MULTILINE):
        fail("inference.control.v1 must not revive GetEndpointURL or UpdateStatus")


def validate_no_new_callers() -> None:
    offenders: list[str] = []
    for path in iter_go_files(ROOT):
        rel = relative(path)
        if rel.startswith(GENERATED_OLD):
            continue
        for import_path in parse_go_imports(path):
            if import_path == OLD_IMPORT or import_path.startswith(OLD_IMPORT + "/"):
                offenders.append(rel)
                break
    if offenders:
        fail("new callers of retired inference.v1 proto: " + ", ".join(offenders))


def validate_gateway_wiring() -> None:
    control_seen = False
    for path in iter_go_files(GATEWAY_ROOT):
        rel = relative(path)
        imports = parse_go_imports(path)
        for import_path in imports:
            if import_path.startswith(SERVICE_IMPORT):
                fail(f"{rel} imports inference-service implementation")
            if import_path == OLD_IMPORT or import_path.startswith(OLD_IMPORT + "/"):
                fail(f"{rel} imports retired inference.v1 proto")
            if import_path == CONTROL_IMPORT:
                control_seen = True
        text = path.read_text(encoding="utf-8")
        if "InferenceServiceRPC" in text or "RegisterInferenceServiceRPCServer" in text:
            fail(f"{rel} must not wire retired InferenceServiceRPC")
    if not control_seen:
        fail("ani-gateway must keep using inference.control.v1")


def validate_helm_operator_disabled() -> None:
    data = yaml.safe_load(HELM_VALUES.read_text(encoding="utf-8"))
    inference = (
        data.get("infrastructure", {})
        .get("runtime", {})
        .get("providers", {})
        .get("inference", {})
    )
    if not isinstance(inference, dict):
        fail("helm values missing infrastructure.runtime.providers.inference")
    if inference.get("enabled") is not False:
        fail("P0 helm must keep infrastructure.runtime.providers.inference.enabled=false")


def main() -> None:
    validate_proto_deprecated()
    validate_no_new_callers()
    validate_gateway_wiring()
    validate_helm_operator_disabled()
    print("inference legacy control plane retired")


if __name__ == "__main__":
    main()
