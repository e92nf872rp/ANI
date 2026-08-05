#!/usr/bin/env python3
import json
import subprocess
import tempfile
from pathlib import Path


ROOT = Path(__file__).resolve().parents[1]
VALIDATOR = ROOT / "scripts/validate_instance_sandbox_stateless_live_gate.py"


def run(*args: str) -> subprocess.CompletedProcess[str]:
    return subprocess.run(["python3", str(VALIDATOR), *args], cwd=ROOT, text=True, capture_output=True, check=False)


def test_contract_passes() -> None:
    result = run()
    assert result.returncode == 0, result.stdout + result.stderr


def test_sensitive_evidence_is_rejected() -> None:
    checks = [
        "core-sandbox-create-running", "file-port-coderun-before-restart", "gateway-rollout-restart",
        "file-port-task-after-restart", "idempotency-replay-after-restart", "token-expiry-conflict",
        "checkpoint-provider-capability-422", "sandbox-pause-resume-delete", "postgres-and-kubernetes-cleanup",
    ]
    evidence = {
        "profile": "INSTANCE-SANDBOX-STATELESS-LIVE-GATE-A", "status": "passed",
        "image": "registry/ani-gateway:test", "image_digest": "sha256:" + "a" * 64,
        "checks": [{"id": item, "status": "passed"} for item in checks],
        "authorization": "".join(["Bear", "er sensitive"]),
    }
    with tempfile.TemporaryDirectory() as directory:
        path = Path(directory) / "evidence.json"
        path.write_text(json.dumps(evidence), encoding="utf-8")
        result = run("--evidence", str(path))
    assert result.returncode != 0


if __name__ == "__main__":
    test_contract_passes()
    test_sensitive_evidence_is_rejected()
    print("sandbox stateless live gate validator tests passed")
