#!/usr/bin/env python3
"""Gateway authz policy drift 门禁（AUTHZ-POLICY-A）。

在临时目录重新生成 registry 并 gofmt，然后与提交的工作区文件字节比较。
不覆盖工作区；发现漂移立即失败，提示执行 make gen-gateway-authz。
"""

from __future__ import annotations

import argparse
import shutil
import subprocess
import tempfile
from pathlib import Path

import generate_gateway_authz as generator

ROOT = Path(__file__).resolve().parents[1]


def validate(input_path: Path, committed: Path) -> None:
    gofmt = shutil.which("gofmt")
    if gofmt is None:
        raise SystemExit("gofmt not found; install the Go toolchain to validate authz drift")
    if not committed.is_file():
        raise SystemExit(f"committed authz registry missing: {committed}")
    with tempfile.TemporaryDirectory() as directory:
        generated = Path(directory) / "zz_generated_core_policies.go"
        generator.generate(input_path, generated)
        subprocess.run([gofmt, "-w", str(generated)], check=True)
        if generated.read_bytes() != committed.read_bytes():
            raise SystemExit("generated gateway authz policy drift; run make gen-gateway-authz")


def main() -> int:
    parser = argparse.ArgumentParser(description="Validate Gateway authz registry drift.")
    parser.add_argument("--input", type=Path, default=generator.DEFAULT_INPUT)
    parser.add_argument("--committed", type=Path, default=generator.DEFAULT_OUTPUT)
    args = parser.parse_args()
    try:
        validate(args.input, args.committed)
    except ValueError as error:
        print(f"ERROR: {error}")
        return 1
    print("gateway authz registry: no drift")
    return 0


if __name__ == "__main__":
    raise SystemExit(main())
