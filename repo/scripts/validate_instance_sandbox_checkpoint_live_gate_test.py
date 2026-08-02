#!/usr/bin/env python3
import json
import re
import subprocess
import tempfile
from pathlib import Path


ROOT = Path(__file__).resolve().parents[1]
VALIDATOR = ROOT / "scripts/validate_instance_sandbox_checkpoint_live_gate.py"
CHECKS = [
    "sandbox-workspace-pvc-bound",
    "checkpoint-create-ready",
    "checkpoint-list-after-gateway-restart",
    "workspace-restore-content",
    "checkpoint-clone-content",
    "keep-memory-capability-422",
    "legacy-emptydir-capability-422",
    "postgres-task-persistence",
    "sandbox-checkpoint-cleanup",
]


def run(*args: str) -> subprocess.CompletedProcess[str]:
    return subprocess.run(
        ["python3", str(VALIDATOR), *args], cwd=ROOT, text=True, capture_output=True, check=False
    )


def evidence() -> dict[str, object]:
    return {
        "profile": "INSTANCE-SANDBOX-CHECKPOINT-LIVE-GATE-A",
        "status": "passed",
        "image": "registry/ani-gateway:checkpoint",
        "image_digest": "sha256:" + "a" * 64,
        "checks": [{"id": check, "status": "passed"} for check in CHECKS],
    }


def test_contract_passes() -> None:
    result = run()
    assert result.returncode == 0, result.stdout + result.stderr


def test_invalid_evidence_is_rejected() -> None:
    cases = []
    missing = evidence()
    missing["checks"] = missing["checks"][:-1]
    cases.append(missing)
    bad_digest = evidence()
    bad_digest["image_digest"] = "latest"
    cases.append(bad_digest)
    for key, value in {
        "authorization": "Bearer secret",
        "jwt": "eyJ" + "a" * 30 + "." + "b" * 30,
        "database_url": "postgresql://user:secret@db/core",
        "preview_url": "http://preview.internal/session",
        "internal_ip": "10.10.1.20",
    }.items():
        document = evidence()
        document[key] = value
        cases.append(document)
    with tempfile.TemporaryDirectory() as directory:
        for index, document in enumerate(cases):
            path = Path(directory) / f"evidence-{index}.json"
            path.write_text(json.dumps(document), encoding="utf-8")
            result = run("--evidence", str(path))
            assert result.returncode != 0, (index, result.stdout, result.stderr)


def test_live_gate_without_evidence_is_rejected() -> None:
    source = (ROOT / "deploy/real-k8s-lab/instance-sandbox-checkpoint-live-gate.yaml").read_text(encoding="utf-8")
    source = re.sub(r"^status: .*?$", "status: live", source, flags=re.MULTILINE)
    source = re.sub(r"^evidence: .*?\n", "", source, flags=re.MULTILINE)
    with tempfile.TemporaryDirectory() as directory:
        path = Path(directory) / "gate.yaml"
        path.write_text(source, encoding="utf-8")
        result = run("--gate", str(path))
    assert result.returncode != 0


if __name__ == "__main__":
    test_contract_passes()
    test_invalid_evidence_is_rejected()
    test_live_gate_without_evidence_is_rejected()
    print("sandbox checkpoint live gate validator tests passed")
