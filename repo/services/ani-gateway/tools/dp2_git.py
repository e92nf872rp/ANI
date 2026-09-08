#!/usr/bin/env python3
"""Shared fixed Git-object reader for Direct P2 contract tools."""

from __future__ import annotations

import subprocess
from pathlib import Path
from typing import TypeVar


ErrorT = TypeVar("ErrorT", bound=Exception)


def verify_git_tree(
    repository: Path,
    commit: str,
    expected_tree: str,
    error_type: type[ErrorT],
    label: str,
) -> None:
    """Verify a pinned commit tree while preserving the caller's terminology."""
    try:
        actual_tree = subprocess.run(
            ["git", "-C", str(repository), "rev-parse", f"{commit}^{{tree}}"],
            check=True,
            stdout=subprocess.PIPE,
            stderr=subprocess.PIPE,
            text=True,
        ).stdout.strip()
    except subprocess.CalledProcessError as error:
        raise error_type(f"cannot resolve {label} commit {commit}") from error
    if actual_tree != expected_tree:
        raise error_type(f"{label} tree is {actual_tree}, want {expected_tree}")


def read_git_object_text(
    repository: Path,
    commit: str,
    path: str,
    error_type: type[ErrorT],
) -> str:
    """Read one pinned Git object and preserve the caller's contract error type."""
    try:
        return subprocess.run(
            ["git", "-C", str(repository), "show", f"{commit}:{path}"],
            check=True,
            stdout=subprocess.PIPE,
            stderr=subprocess.PIPE,
            text=True,
        ).stdout
    except subprocess.CalledProcessError as error:
        detail = error.stderr.strip() or str(error)
        raise error_type(f"cannot read fixed Git object {commit}:{path}: {detail}") from error
