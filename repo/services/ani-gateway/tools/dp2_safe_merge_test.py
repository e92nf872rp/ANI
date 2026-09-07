#!/usr/bin/env python3

from __future__ import annotations

import subprocess
import sys
import unittest
from pathlib import Path

import yaml


ROOT = Path(__file__).resolve().parents[3]


class SafeMergeContractTest(unittest.TestCase):
    def test_target_iam_capability_uses_ports_and_adapters(self) -> None:
        port = ROOT / "pkg/ports/target_iam.go"
        adapter = ROOT / "pkg/adapters/iam/client.go"
        generated = ROOT / "pkg/generated/pb/iam/v1"
        self.assertTrue(port.is_file())
        self.assertTrue(adapter.is_file())
        for name in (
            "authentication_service.pb.go",
            "authentication_service_grpc.pb.go",
            "authorization_service.pb.go",
            "authorization_service_grpc.pb.go",
            "contract.pb.go",
            "iam_admin_service.pb.go",
            "iam_admin_service_grpc.pb.go",
        ):
            self.assertTrue((generated / name).is_file(), name)

        old_package = ROOT / "services/ani-gateway/internal/targetiam"
        self.assertEqual(list(old_package.rglob("*.go")), [])
        forbidden_import = "github.com/kubercloud/ani/services/ani-gateway/internal/targetiam"
        offenders = []
        for path in (ROOT / "services/ani-gateway").rglob("*.go"):
            if forbidden_import in path.read_text(encoding="utf-8"):
                offenders.append(str(path.relative_to(ROOT)))
        self.assertEqual(offenders, [])

    def test_production_gateway_manifest_explicitly_disables_target_iam(self) -> None:
        manifest = ROOT / "deploy/real-k8s-lab/sprint13-production-shaped-gateway-deployment.yaml"
        documents = list(yaml.safe_load_all(manifest.read_text(encoding="utf-8")))
        deployments = [document for document in documents if document.get("kind") == "Deployment"]
        self.assertEqual(len(deployments), 1)
        containers = deployments[0]["spec"]["template"]["spec"]["containers"]
        gateway = next(container for container in containers if container["name"] == "ani-gateway")
        environment = {item["name"]: item for item in gateway["env"]}
        self.assertEqual(environment.get("IAM_TARGET_MODE"), {"name": "IAM_TARGET_MODE", "value": "disabled"})
        for name in (
            "IAM_TARGET_GRPC_ADDR",
            "IAM_TARGET_TLS_SERVER_NAME",
            "IAM_TARGET_TLS_CA_FILE",
            "IAM_TARGET_TLS_CERT_FILE",
            "IAM_TARGET_TLS_KEY_FILE",
        ):
            self.assertNotIn(name, environment)

    def test_aggregate_auth_validator_accepts_frozen_target_operation_ids(self) -> None:
        completed = subprocess.run(
            [sys.executable, str(ROOT / "scripts/validate_auth_gateway_contract.py")],
            cwd=ROOT,
            check=False,
            stdout=subprocess.PIPE,
            stderr=subprocess.PIPE,
            text=True,
        )
        self.assertEqual(completed.returncode, 0, completed.stderr or completed.stdout)


if __name__ == "__main__":
    unittest.main()
