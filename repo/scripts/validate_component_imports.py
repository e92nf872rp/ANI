#!/usr/bin/env python3
"""Guardrail for ANI ports/adapters component boundaries."""

from __future__ import annotations

import argparse
import pathlib
import re
import sys
from dataclasses import dataclass

import yaml


FORBIDDEN_IMPORT_PREFIXES = (
    "github.com/jackc/pgx/",
    "github.com/nats-io/nats.go",
    "github.com/redis/go-redis/",
    "github.com/minio/",
    "github.com/milvus-io/",
    "github.com/goharbor/",
    "github.com/dexidp/",
    "github.com/coreos/go-oidc/",
)

COUPLING_LEVELS = {
    "port_required",
    "adapter_with_extensions",
    "bounded_direct",
    "temporary_exception",
}

ALLOWED_PATH_PARTS = (
    "/pkg/adapters/",
    "/pkg/bootstrap/",
    "/services/pkg/bootstrap/",
)

IMPORT_RE = re.compile(r'"([^"]+)"')


@dataclass(frozen=True)
class Finding:
    path: pathlib.Path
    import_path: str
    reason: str


def load_allowlist(path: pathlib.Path, repo_root: pathlib.Path) -> dict[tuple[str, str], str]:
    if not path.exists():
        return {}
    with path.open("r", encoding="utf-8") as handle:
        data = yaml.safe_load(handle) or {}
    allowed: dict[tuple[str, str], str] = {}
    for entry in data.get("allowed", []):
        rel_path = entry.get("path")
        coupling_level = entry.get("coupling_level")
        imports = entry.get("imports", [])
        reason = entry.get("reason", "")
        migrate_by = entry.get("migrate_by", "")
        if coupling_level not in COUPLING_LEVELS:
            raise ValueError(f"{path}: allowlist entry requires valid coupling_level for {rel_path}")
        if not rel_path or not imports or not reason:
            raise ValueError(f"{path}: allowlist entry requires path, imports, reason")
        if coupling_level in {"port_required", "temporary_exception"} and not migrate_by:
            raise ValueError(f"{path}: {coupling_level} entry requires migrate_by for {rel_path}")
        if coupling_level == "bounded_direct" and not str(reason).strip():
            raise ValueError(f"{path}: bounded_direct entry requires explicit reason for {rel_path}")
        normalized = normalize_path(repo_root / rel_path, repo_root)
        for import_path in imports:
            suffix = f"; migrate_by={migrate_by}" if migrate_by else ""
            allowed[(normalized, import_path)] = f"{coupling_level}: {reason}{suffix}"
    return allowed


def normalize_path(path: pathlib.Path, repo_root: pathlib.Path) -> str:
    return path.resolve().relative_to(repo_root.resolve()).as_posix()


def is_allowed_area(path: pathlib.Path, repo_root: pathlib.Path) -> bool:
    rel = "/" + normalize_path(path, repo_root)
    return any(part in rel for part in ALLOWED_PATH_PARTS)


def forbidden_import(import_path: str) -> bool:
    return any(import_path.startswith(prefix) for prefix in FORBIDDEN_IMPORT_PREFIXES)


def parse_go_imports(path: pathlib.Path) -> list[str]:
    imports: list[str] = []
    in_block = False
    with path.open("r", encoding="utf-8") as handle:
        for raw_line in handle:
            line = raw_line.strip()
            if line.startswith("import ("):
                in_block = True
                continue
            if in_block and line.startswith(")"):
                in_block = False
                continue
            if in_block or line.startswith("import "):
                match = IMPORT_RE.search(line)
                if match:
                    imports.append(match.group(1))
    return imports


def validate_allowlist_is_used(
    allowlist: dict[tuple[str, str], str],
    seen: set[tuple[str, str]],
) -> list[str]:
    # Only Go files are scanned, so allowlist entries for Python files
    # (.py) can never be "seen" by this validator.  Skip them to avoid
    # false "unused" errors while still keeping the allowlist as the
    # documented source of truth for Python direct-import coupling.
    unused = sorted(
        key for key in (set(allowlist) - seen)
        if not key[0].endswith(".py")
    )
    return [f"{path}: allowlist import is unused: {import_path}" for path, import_path in unused]


def main() -> int:
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("--root", default=".", help="repository root")
    parser.add_argument(
        "--allowlist",
        default="architecture/component-import-allowlist.yaml",
        help="YAML allowlist relative to root",
    )
    args = parser.parse_args()

    repo_root = pathlib.Path(args.root).resolve()
    allowlist_path = repo_root / args.allowlist
    try:
        allowlist = load_allowlist(allowlist_path, repo_root)
    except (OSError, ValueError, yaml.YAMLError) as err:
        print(err, file=sys.stderr)
        return 1

    findings: list[Finding] = []
    seen_allowed: set[tuple[str, str]] = set()
    for path in sorted(repo_root.rglob("*.go")):
        if not path.is_file():
            continue
        rel_path = normalize_path(path, repo_root)
        rel_with_slashes = f"/{rel_path}/"
        if (
            "/vendor/" in rel_with_slashes
            or "/.cache/" in rel_with_slashes
            or path.name.endswith("_test.go")
        ):
            continue
        for import_path in parse_go_imports(path):
            if not forbidden_import(import_path):
                continue
            key = (rel_path, import_path)
            if is_allowed_area(path, repo_root):
                continue
            if key in allowlist:
                seen_allowed.add(key)
                continue
            findings.append(
                Finding(
                    path=path,
                    import_path=import_path,
                    reason="business/shared code must depend on pkg/ports, not component SDKs",
                )
            )

    errors = [
        f"{normalize_path(f.path, repo_root)}: forbidden import {f.import_path}: {f.reason}"
        for f in findings
    ]
    errors.extend(validate_allowlist_is_used(allowlist, seen_allowed))

    if errors:
        for err in errors:
            print(err, file=sys.stderr)
        return 1

    print("component import guard passed")
    return 0


if __name__ == "__main__":
    raise SystemExit(main())
