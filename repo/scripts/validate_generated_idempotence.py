#!/usr/bin/env python3
"""Run one generator and prove that its declared outputs are idempotent."""

from __future__ import annotations

import argparse
import hashlib
import os
import subprocess
import sys
from dataclasses import dataclass
from pathlib import Path


Snapshot = dict[str, tuple[str, int]]
CACHE_DIRECTORIES = {"__pycache__", ".pytest_cache", ".mypy_cache", ".ruff_cache", "node_modules"}
CACHE_SUFFIXES = {".pyc", ".pyo"}
JAVA_CLASS_DIRECTORY = ("java", "build", "classes")


@dataclass(frozen=True)
class ValidationResult:
    command_returncode: int
    changed: list[str]


def _relative_label(root: Path, path: Path) -> str:
    return path.relative_to(root).as_posix()


def _entry(path: Path) -> tuple[str, int]:
    mode = path.lstat().st_mode & 0o111
    if path.is_symlink():
        return (f"symlink:{os.readlink(path)}", mode)
    return (hashlib.sha256(path.read_bytes()).hexdigest(), mode)


def _is_cache_artifact(path: Path, root: Path) -> bool:
    relative = path.relative_to(root)
    parts = relative.parts
    has_java_classes = any(
        parts[index : index + len(JAVA_CLASS_DIRECTORY)] == JAVA_CLASS_DIRECTORY
        for index in range(len(parts) - len(JAVA_CLASS_DIRECTORY) + 1)
    )
    return bool(CACHE_DIRECTORIES.intersection(parts)) or path.suffix in CACHE_SUFFIXES or has_java_classes


def _resolve_paths(root: Path, paths: list[Path]) -> list[Path]:
    resolved_root = root.resolve()
    resolved: list[Path] = []
    for path in paths:
        target = (resolved_root / path).resolve(strict=False) if not path.is_absolute() else path.resolve(strict=False)
        try:
            target.relative_to(resolved_root)
        except ValueError as exc:
            raise ValueError(f"generated path escapes root: {path}") from exc
        if not target.exists() and not target.is_symlink():
            raise ValueError(f"generated path does not exist: {path}")
        resolved.append(target)
    return resolved


def snapshot(root: Path, paths: list[Path]) -> Snapshot:
    resolved_root = root.resolve()
    result: Snapshot = {}
    for target in _resolve_paths(resolved_root, paths):
        if target.is_symlink() or target.is_file():
            result[_relative_label(resolved_root, target)] = _entry(target)
            continue
        for entry in sorted(target.rglob("*")):
            if not _is_cache_artifact(entry, resolved_root) and (entry.is_symlink() or entry.is_file()):
                result[_relative_label(resolved_root, entry)] = _entry(entry)
    return result


def _snapshot_after(root: Path, paths: list[Path]) -> Snapshot:
    result: Snapshot = {}
    resolved_root = root.resolve()
    for path in paths:
        target = (resolved_root / path).resolve(strict=False) if not path.is_absolute() else path.resolve(strict=False)
        try:
            target.relative_to(resolved_root)
        except ValueError as exc:
            raise ValueError(f"generated path escapes root: {path}") from exc
        if not target.exists() and not target.is_symlink():
            continue
        if target.is_symlink() or target.is_file():
            result[_relative_label(resolved_root, target)] = _entry(target)
            continue
        for entry in sorted(target.rglob("*")):
            if not _is_cache_artifact(entry, resolved_root) and (entry.is_symlink() or entry.is_file()):
                result[_relative_label(resolved_root, entry)] = _entry(entry)
    return result


def validate(root: Path, paths: list[Path], command: list[str]) -> ValidationResult:
    if not command:
        raise ValueError("generator command is required")
    before = snapshot(root, paths)
    completed = subprocess.run(command, cwd=root, check=False)
    after = _snapshot_after(root, paths)
    changed = sorted(key for key in before.keys() | after.keys() if before.get(key) != after.get(key))
    return ValidationResult(command_returncode=completed.returncode, changed=changed)


def parse_args() -> argparse.Namespace:
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("--root", type=Path, default=Path(__file__).resolve().parents[1])
    parser.add_argument("--path", type=Path, action="append", required=True, dest="paths")
    parser.add_argument("command", nargs=argparse.REMAINDER)
    args = parser.parse_args()
    if args.command and args.command[0] == "--":
        args.command = args.command[1:]
    return args


def main() -> int:
    args = parse_args()
    try:
        result = validate(args.root, args.paths, args.command)
    except (OSError, ValueError) as exc:
        print(f"generated idempotence check failed: {exc}", file=sys.stderr)
        return 2
    if result.command_returncode != 0:
        print(
            f"generated idempotence check failed: generator exited {result.command_returncode}",
            file=sys.stderr,
        )
        return result.command_returncode
    if result.changed:
        print("generated idempotence check failed: generator changed declared outputs", file=sys.stderr)
        for path in result.changed:
            print(f"  {path}", file=sys.stderr)
        return 1
    print("generated outputs are idempotent")
    return 0


if __name__ == "__main__":
    raise SystemExit(main())
