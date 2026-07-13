#!/usr/bin/env python3
"""Reject unregistered binary files tracked by Git."""

from __future__ import annotations

import argparse
import subprocess
from dataclasses import dataclass
from pathlib import Path
from typing import Sequence

import yaml


PROJECT_ROOT = Path(__file__).resolve().parents[2]
REPO_ROOT = Path(__file__).resolve().parents[1]
DEFAULT_CONTRACT = REPO_ROOT / "architecture" / "repository-file-contract.yaml"


@dataclass(frozen=True)
class BinaryFileViolation:
    path: str


def load_contract(path: Path = DEFAULT_CONTRACT) -> dict[str, object]:
    if not path.exists():
        raise SystemExit(f"repository file contract does not exist: {path}")
    data = yaml.safe_load(path.read_text(encoding="utf-8"))
    if not isinstance(data, dict):
        raise SystemExit(f"{path}: repository file contract must be a mapping")
    binary_files = data.get("binary_files")
    if not isinstance(binary_files, dict):
        raise SystemExit(f"{path}: binary_files must be a mapping")
    if binary_files.get("policy") != "no_new_binary_files":
        raise SystemExit(f"{path}: binary_files.policy must be no_new_binary_files")
    if binary_files.get("enforcement") != "make validate-no-binary-files":
        raise SystemExit(f"{path}: binary_files.enforcement must be make validate-no-binary-files")
    allowed_existing_paths = binary_files.get("allowed_existing_paths")
    if not isinstance(allowed_existing_paths, list):
        raise SystemExit(f"{path}: binary_files.allowed_existing_paths must be a list")
    for allowed_path in allowed_existing_paths:
        if not isinstance(allowed_path, str) or not allowed_path:
            raise SystemExit(f"{path}: binary_files.allowed_existing_paths entries must be non-empty strings")
    return data


def allowed_existing_paths(contract: dict[str, object]) -> set[str]:
    binary_files = contract["binary_files"]
    assert isinstance(binary_files, dict)
    return set(binary_files["allowed_existing_paths"])


def find_tracked_binary_files(root: Path = PROJECT_ROOT, allowed_paths: set[str] | None = None) -> list[BinaryFileViolation]:
    allowed_paths = allowed_paths or set()
    paths = _git_ls_files(root)
    violations: list[BinaryFileViolation] = []
    for rel_path in paths:
        if rel_path in allowed_paths:
            continue
        full_path = root / rel_path
        if not full_path.is_file():
            continue
        if is_binary(full_path.read_bytes()):
            violations.append(BinaryFileViolation(rel_path))
    return violations


def is_binary(data: bytes) -> bool:
    if not data:
        return False
    if b"\0" in data:
        return True
    try:
        data.decode("utf-8")
    except UnicodeDecodeError:
        return True
    return False


def _git_ls_files(root: Path) -> list[str]:
    result = subprocess.run(
        ["git", "ls-files", "-z"],
        cwd=root,
        check=True,
        stdout=subprocess.PIPE,
        stderr=subprocess.PIPE,
    )
    if not result.stdout:
        return []
    return [path.decode("utf-8") for path in result.stdout.split(b"\0") if path]


def main(argv: Sequence[str] | None = None) -> int:
    parser = argparse.ArgumentParser(description="Validate that Git does not track unregistered binary files.")
    parser.add_argument("--root", type=Path, default=PROJECT_ROOT, help="Git working tree root to scan.")
    parser.add_argument("--contract", type=Path, default=DEFAULT_CONTRACT, help="Repository file contract to enforce.")
    args = parser.parse_args(argv)

    contract = load_contract(args.contract)
    violations = find_tracked_binary_files(args.root, allowed_existing_paths(contract))
    if violations:
        print("Tracked binary files are not allowed unless listed in repository-file-contract.yaml:")
        for violation in violations:
            print(f"  - {violation.path}")
        return 1
    print("No unregistered tracked binary files found.")
    return 0


if __name__ == "__main__":
    raise SystemExit(main())
