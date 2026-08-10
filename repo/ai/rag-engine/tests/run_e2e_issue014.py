"""Wrapper to run the E2E test and capture all output to a log file."""
import os
import sys
import subprocess
from pathlib import Path

repo_root = Path(__file__).resolve().parents[3]  # repo/
env = os.environ.copy()
env["PYTHONPATH"] = str(repo_root / "ai" / "rag-engine")
log_path = repo_root / "e2e_issue014_result.log"

test_script = str(repo_root / "ai" / "rag-engine" / "tests" / "demo_e2e_issue014_parse_grpc.py")

with open(log_path, "w", encoding="utf-8") as log:
    log.write("=" * 70 + "\n")
    log.write("E2E Test for Issue #14 — parse_worker NATS + gRPC Query RPC\n")
    log.write(f"Started at: {os.popen('date /t').read().strip()} {os.popen('time /t').read().strip()}\n")
    log.write("=" * 70 + "\n\n")
    log.flush()
    result = subprocess.run(
        [sys.executable, "-u", test_script],
        cwd=str(repo_root),
        env=env,
        stdout=log,
        stderr=subprocess.STDOUT,
        timeout=600,
    )
    log.write(f"\n\nEXIT CODE: {result.returncode}\n")

print(f"Log written to: {log_path}")
print(f"Exit code: {result.returncode}")
