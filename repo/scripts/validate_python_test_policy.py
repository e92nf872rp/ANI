#!/usr/bin/env python3
"""Require tests when a PR changes Python AI source code."""

from __future__ import annotations

import argparse
import subprocess
from pathlib import Path


def is_python_test(path: str) -> bool:
    name = Path(path).name
    return name.startswith("test_") or name.endswith("_test.py")


def validate_changed_files(changed_files: list[str]) -> list[str]:
    ai_python = [
        path
        for path in changed_files
        if path.startswith("ai/") and path.endswith(".py") and not is_python_test(path)
    ]
    if not ai_python:
        return []
    ai_tests = [
        path
        for path in changed_files
        if path.startswith("ai/") and path.endswith(".py") and is_python_test(path)
    ]
    if ai_tests:
        return []
    return [
        "Python AI source changed without a matching test file in the same PR: "
        + ", ".join(sorted(ai_python))
    ]


def changed_files(base: str) -> list[str]:
    result = subprocess.run(
        ["git", "diff", "--name-only", f"{base}...HEAD"],
        check=True,
        capture_output=True,
        text=True,
    )
    return [line for line in result.stdout.splitlines() if line]


def main() -> int:
    parser = argparse.ArgumentParser()
    parser.add_argument("--base", default="origin/main")
    args = parser.parse_args()
    errors = validate_changed_files(changed_files(args.base))
    for error in errors:
        print(f"ERROR: {error}")
    if errors:
        return 1
    print("Python AI test policy valid")
    return 0


if __name__ == "__main__":
    raise SystemExit(main())
