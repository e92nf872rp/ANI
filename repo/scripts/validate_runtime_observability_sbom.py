#!/usr/bin/env python3
"""Generate and validate CycloneDX SBOMs for OBS-RUNTIME-P0 Go deliverables."""

from __future__ import annotations

import argparse
import hashlib
import json
import os
import subprocess
import sys
from pathlib import Path
from typing import Any


CYCLONEDX_GOMOD_VERSION = "v1.10.0"
CYCLONEDX_TOOL_MODULE = "github.com/CycloneDX/cyclonedx-gomod/cmd/cyclonedx-gomod"
RUNTIMEADMIN_MODULE = "github.com/kubercloud/ani/runtimeadmin"
LIBRARY_MODULES = {
    "runtimeadmin": RUNTIMEADMIN_MODULE,
    "pkg": "github.com/kubercloud/ani/pkg",
    "services/pkg": "github.com/kubercloud/ani/services/pkg",
}
APPLICATION_MODULES = {
    "services/ani-gateway": "github.com/kubercloud/ani/services/ani-gateway",
    "services/auth-service": "github.com/kubercloud/ani/services/auth-service",
    "services/model-service": "github.com/kubercloud/ani/services/model-service",
    "services/task-service": "github.com/kubercloud/ani/services/task-service",
    "services/inference-service": "github.com/kubercloud/ani/services/inference-service",
    "services/tenant-service": "github.com/kubercloud/ani/services/tenant-service",
    "services/metering-service": "github.com/kubercloud/ani/services/metering-service",
}
STDJSON_APPLICATIONS = {
    "services/ani-gateway",
    "services/auth-service",
}
PINNED_COMPONENT_VERSIONS = {
    "go.opentelemetry.io/otel": "v1.44.0",
    "go.opentelemetry.io/otel/exporters/prometheus": "v0.66.0",
    "github.com/prometheus/client_golang": "v1.24.1",
    "github.com/prometheus/otlptranslator": "v1.0.0",
    "google.golang.org/grpc": "v1.82.1",
    "github.com/zhangzhe-ctrl/ani-session-gateway/api": "v0.1.0",
}


def sbom_filename(module_path: str) -> str:
    return f"{module_path.replace('/', '--')}.cdx.json"


def _run(command: list[str], *, cwd: Path, env: dict[str, str], label: str) -> None:
    print(f"→ SBOM {label}")
    subprocess.run(command, cwd=cwd, env=env, check=True)


def generate_sboms(root: Path, output_dir: Path, tool_version: str) -> None:
    output_dir.mkdir(parents=True, exist_ok=True)
    base_env = os.environ.copy()
    tool = f"{CYCLONEDX_TOOL_MODULE}@{tool_version}"

    for module_path in LIBRARY_MODULES:
        output = output_dir / sbom_filename(module_path)
        env = base_env.copy()
        env["GOWORK"] = "off"
        _run(
            [
                "go",
                "run",
                tool,
                "mod",
                "-test",
                "-type",
                "library",
                "-json",
                "-output",
                str(output),
                ".",
            ],
            cwd=root / module_path,
            env=env,
            label=module_path,
        )

    for module_path in APPLICATION_MODULES:
        output = output_dir / sbom_filename(module_path)
        env = base_env.copy()
        env.update(
            {
                "CGO_ENABLED": "0",
                "GOOS": "linux",
                "GOARCH": "amd64",
                "GOFLAGS": "-tags=stdjson" if module_path in STDJSON_APPLICATIONS else "",
            }
        )
        _run(
            [
                "go",
                "run",
                tool,
                "app",
                "-packages",
                "-std",
                "-json",
                "-output",
                str(output),
                module_path,
            ],
            cwd=root,
            env=env,
            label=module_path,
        )


def _load_bom(path: Path) -> tuple[dict[str, Any] | None, list[str]]:
    try:
        document = json.loads(path.read_text(encoding="utf-8"))
    except FileNotFoundError:
        return None, [f"missing SBOM: {path}"]
    except (OSError, json.JSONDecodeError) as error:
        return None, [f"invalid SBOM {path}: {error}"]
    if not isinstance(document, dict):
        return None, [f"invalid SBOM {path}: root must be an object"]
    return document, []


def validate_output_dir(output_dir: Path) -> list[str]:
    errors: list[str] = []
    seen_versions: dict[str, set[str | None]] = {
        name: set() for name in PINNED_COMPONENT_VERSIONS
    }
    expected = {
        **{path: (name, "library") for path, name in LIBRARY_MODULES.items()},
        **{path: (name, "application") for path, name in APPLICATION_MODULES.items()},
    }

    for module_path, (module_name, component_type) in expected.items():
        path = output_dir / sbom_filename(module_path)
        bom, load_errors = _load_bom(path)
        errors.extend(load_errors)
        if bom is None:
            continue
        if bom.get("bomFormat") != "CycloneDX" or bom.get("specVersion") != "1.6":
            errors.append(f"{path}: must be CycloneDX 1.6")
        root_component = bom.get("metadata", {}).get("component", {})
        if root_component.get("name") != module_name:
            errors.append(
                f"{path}: root component must be {module_name}, got "
                f"{root_component.get('name')}"
            )
        if root_component.get("type") != component_type:
            errors.append(
                f"{path}: root component type must be {component_type}, got "
                f"{root_component.get('type')}"
            )
        components = bom.get("components")
        if not isinstance(components, list) or not components:
            errors.append(f"{path}: components must be non-empty")
            continue
        if not isinstance(bom.get("dependencies"), list) or not bom["dependencies"]:
            errors.append(f"{path}: dependencies must be non-empty")
        component_names = {
            component.get("name")
            for component in components
            if isinstance(component, dict)
        }
        if module_path in APPLICATION_MODULES and RUNTIMEADMIN_MODULE not in component_names:
            errors.append(f"{path}: application SBOM must include {RUNTIMEADMIN_MODULE}")
        for component in components:
            if not isinstance(component, dict):
                continue
            name = component.get("name")
            if name in seen_versions:
                seen_versions[name].add(component.get("version"))

    for name, expected_version in PINNED_COMPONENT_VERSIONS.items():
        versions = seen_versions[name]
        if not versions:
            errors.append(f"SBOM set missing pinned component {name}@{expected_version}")
        elif versions != {expected_version}:
            errors.append(
                f"SBOM set has version drift for {name}: expected {expected_version}, "
                f"got {sorted(str(version) for version in versions)}"
            )
    return errors


def main() -> int:
    parser = argparse.ArgumentParser()
    parser.add_argument("--root", type=Path, default=Path(__file__).resolve().parents[1])
    parser.add_argument(
        "--output-dir",
        type=Path,
        default=Path(".cache/sbom/obs-runtime-p0"),
    )
    parser.add_argument("--generate", action="store_true")
    parser.add_argument(
        "--tool-version",
        choices=[CYCLONEDX_GOMOD_VERSION],
        default=CYCLONEDX_GOMOD_VERSION,
    )
    args = parser.parse_args()
    root = args.root.resolve()
    output_dir = args.output_dir
    if not output_dir.is_absolute():
        output_dir = root / output_dir

    if args.generate:
        try:
            generate_sboms(root, output_dir, args.tool_version)
        except subprocess.CalledProcessError as error:
            print(f"SBOM generation failed with exit code {error.returncode}", file=sys.stderr)
            return 1

    errors = validate_output_dir(output_dir)
    if errors:
        for error in errors:
            print(f"ERROR: {error}", file=sys.stderr)
        return 1
    for module_path in [*LIBRARY_MODULES, *APPLICATION_MODULES]:
        path = output_dir / sbom_filename(module_path)
        digest = hashlib.sha256(path.read_bytes()).hexdigest()
        print(f"PASS SBOM {module_path}: sha256:{digest}")
    return 0


if __name__ == "__main__":
    raise SystemExit(main())
