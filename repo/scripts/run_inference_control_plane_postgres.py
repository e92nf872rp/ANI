#!/usr/bin/env python3
"""Run inference-service PostgreSQL integration against the lab database.

The DSN is read from the cluster secret and rewritten to a local port-forward.
Evidence must not contain the DSN, password, or server IP.
"""

from __future__ import annotations

import argparse
import datetime as dt
import json
import os
import signal
import subprocess
import sys
import time
from pathlib import Path
from typing import Any

sys.path.insert(0, str(Path(__file__).resolve().parent))
import run_inference_cpu_vllm_live_gate as live

ROOT = live.ROOT
EVIDENCE = ROOT / "development-records/live-evidence/inference-control-plane-postgres-20260817.json"
PROFILE = "INFERENCE-SERVICE-CONTROL-PLANE-POSTGRES-C20"
FORWARD_PORT = 15438


def fail(message: str) -> None:
    raise SystemExit(f"inference control-plane postgres failed: {message}")


def main() -> None:
    parser = argparse.ArgumentParser()
    parser.add_argument("--kubeconfig", default=os.environ.get("KUBECONFIG", str(Path.home() / ".kube/config")))
    args = parser.parse_args()
    kubeconfig = args.kubeconfig
    if not Path(kubeconfig).exists():
        fail("kubeconfig is missing")

    forward: subprocess.Popen[str] | None = None
    try:
        dsn = live.rewrite_url(live.secret_data(kubeconfig, "database_url"), "127.0.0.1", FORWARD_PORT)
        forward = subprocess.Popen(
            ["kubectl", "--kubeconfig", kubeconfig, "-n", "ani-system", "port-forward", "pod/ani-postgres-0", f"{FORWARD_PORT}:5432"],
            stdout=subprocess.DEVNULL,
            stderr=subprocess.DEVNULL,
        )
        live.wait_tcp("127.0.0.1", FORWARD_PORT, timeout=30)
        env = os.environ.copy()
        env["INFERENCE_TEST_DATABASE_URL"] = dsn
        env["GOWORK"] = "off"
        completed = live.run(
            ["go", "test", "-tags=integration", "./internal/repository", "-run", "TestPostgresControlPlaneIntegration", "-count=1", "-timeout", "180s"],
            cwd=str(ROOT / "services/inference-service"),
            env=env,
            timeout=180,
        )
        if completed.returncode != 0:
            fail(f"integration test failed: {live.redact_text(completed.stderr or completed.stdout)}")
        evidence = {
            "profile": PROFILE,
            "status": "passed",
            "generated_at": dt.datetime.now(dt.timezone.utc).strftime("%Y-%m-%dT%H:%M:%SZ"),
            "database": "lab-postgres-isolated-schema",
            "create_role": "passed",
            "rls": "passed",
            "lease": "passed",
            "checks": [
                {"id": "create-isolated-schema-and-roles", "status": "passed"},
                {"id": "apply-control-plane-migration", "status": "passed"},
                {"id": "tenant-rls-and-platform-bypass", "status": "passed"},
                {"id": "idempotent-create-and-lease", "status": "passed"},
            ],
        }
        live.assert_clean_evidence(evidence)
        EVIDENCE.parent.mkdir(parents=True, exist_ok=True)
        EVIDENCE.write_text(json.dumps(evidence, indent=2) + "\n", encoding="utf-8")
        print("inference control-plane postgres integration passed")
        print(f"evidence {EVIDENCE.relative_to(ROOT)}")
    finally:
        if forward is not None and forward.poll() is None:
            forward.send_signal(signal.SIGTERM)
            try:
                forward.wait(timeout=5)
            except subprocess.TimeoutExpired:
                forward.kill()


if __name__ == "__main__":
    try:
        main()
    except KeyboardInterrupt:
        sys.exit(130)
