#!/usr/bin/env python3
"""Validate ANI Core and Services OpenAPI documents with the repository-owned validator."""

from __future__ import annotations

import argparse
import subprocess
import sys
from pathlib import Path

DEFAULT_SPECS = (
    Path("api/openapi/v1.yaml"),
    Path("api/openapi/services/v1.yaml"),
)


def validate_spec(path: Path) -> None:
    if not path.is_file():
        raise FileNotFoundError(path)
    subprocess.run(
        [sys.executable, "-m", "openapi_spec_validator", str(path)],
        check=True,
    )


def main() -> int:
    parser = argparse.ArgumentParser()
    parser.add_argument("specs", nargs="*", type=Path)
    args = parser.parse_args()
    specs = tuple(args.specs) or DEFAULT_SPECS
    for spec in specs:
        print(f"→ validating {spec}")
        validate_spec(spec)
    print(f"OpenAPI specs valid: {len(specs)}")
    return 0


if __name__ == "__main__":
    raise SystemExit(main())
