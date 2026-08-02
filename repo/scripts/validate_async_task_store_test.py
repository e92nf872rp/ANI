#!/usr/bin/env python3
import subprocess
from pathlib import Path


ROOT = Path(__file__).resolve().parents[1]


def test_validator_passes_repository_contract() -> None:
    result = subprocess.run(
        ["python3", str(ROOT / "scripts/validate_async_task_store.py")],
        cwd=ROOT,
        text=True,
        capture_output=True,
        check=False,
    )
    assert result.returncode == 0, result.stdout + result.stderr


if __name__ == "__main__":
    test_validator_passes_repository_contract()
    print("async task store validator tests passed")
