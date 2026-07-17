#!/usr/bin/env python3
"""List the Go modules explicitly included by go.work.

The CI jobs use this command so new modules are not silently omitted from
linting or vulnerability scanning. The command intentionally validates that
each workspace entry still has a go.mod file.
"""

from __future__ import annotations

import argparse
from pathlib import Path


def parse_use_entries(work_file: Path) -> list[str]:
    entries: list[str] = []
    in_block = False
    for raw_line in work_file.read_text(encoding="utf-8").splitlines():
        line = raw_line.split("//", 1)[0].strip()
        if not line:
            continue
        if line == "use (":
            in_block = True
            continue
        if in_block and line == ")":
            in_block = False
            continue
        if in_block and line.startswith("./"):
            entries.append(line)
            continue
        if line.startswith("use ./"):
            entries.append(line.removeprefix("use "))
    if in_block:
        raise ValueError(f"{work_file}: unterminated use block")
    if not entries:
        raise ValueError(f"{work_file}: no Go modules found in use directives")
    return entries


def list_modules(work_file: Path) -> list[str]:
    modules: list[str] = []
    for entry in parse_use_entries(work_file):
        module_dir = (work_file.parent / entry).resolve()
        if not (module_dir / "go.mod").is_file():
            raise ValueError(f"{entry}: expected go.mod at {module_dir / 'go.mod'}")
        modules.append(entry)
    return modules


def main() -> int:
    parser = argparse.ArgumentParser()
    parser.add_argument("--work-file", type=Path, default=Path("go.work"))
    args = parser.parse_args()
    for module in list_modules(args.work_file):
        print(module)
    return 0


if __name__ == "__main__":
    raise SystemExit(main())
