#!/usr/bin/env python3
"""Tests for Sprint 13 S01-S04 production-shaped evidence guard."""

from __future__ import annotations

import json
import tempfile
import unittest
from pathlib import Path
from unittest.mock import patch

import validate_sprint13_b_track_production_shape as guard


class Sprint13ProductionShapeGuardTest(unittest.TestCase):
    def test_repository_records_are_explicit_about_production_shape(self) -> None:
        guard.validate_all()

    def test_production_deployment_contract_is_present(self) -> None:
        guard.validate_production_deployment_contract()

    def test_production_deployment_rejects_vcluster_svc_suffix_not_in_tls_san(self) -> None:
        with tempfile.TemporaryDirectory() as tmp:
            deployment = Path(tmp) / "deployment.yaml"
            deployment.write_text(
                guard.PRODUCTION_DEPLOYMENT.read_text(encoding="utf-8").replace(
                    "https://{cluster_id}.{namespace}:443",
                    "https://{cluster_id}.{namespace}.svc:443",
                ),
                encoding="utf-8",
            )

            with patch.object(guard, "PRODUCTION_DEPLOYMENT", deployment):
                with self.assertRaises(SystemExit) as raised:
                    guard.validate_production_deployment_contract()

        self.assertIn("must match vCluster TLS SAN service namespace", str(raised.exception))

    def test_production_deployment_rejects_dev_auth_after_auth_dex_gate(self) -> None:
        with tempfile.TemporaryDirectory() as tmp:
            deployment = Path(tmp) / "deployment.yaml"
            deployment.write_text(
                guard.PRODUCTION_DEPLOYMENT.read_text(encoding="utf-8").replace(
                    "value: auth_service",
                    "value: dev",
                    1,
                ),
                encoding="utf-8",
            )

            with patch.object(guard, "PRODUCTION_DEPLOYMENT", deployment):
                with self.assertRaises(SystemExit) as raised:
                    guard.validate_production_deployment_contract()

        self.assertIn("ANI_AUTH_MODE must be auth_service after Auth/Dex production gate", str(raised.exception))

    def test_production_readiness_boundary_docs_are_explicit(self) -> None:
        guard.validate_production_readiness_boundary_docs()

    def test_evidence_requires_production_shape_block(self) -> None:
        with tempfile.TemporaryDirectory() as tmp:
            evidence = Path(tmp) / "evidence.json"
            evidence.write_text(json.dumps({"status": "passed"}) + "\n", encoding="utf-8")

            with self.assertRaises(SystemExit) as raised:
                guard.validate_evidence("S01", evidence)

        self.assertIn("must include production_shape", str(raised.exception))

    def test_pending_production_shape_requires_missing_items(self) -> None:
        with tempfile.TemporaryDirectory() as tmp:
            evidence = Path(tmp) / "evidence.json"
            evidence.write_text(json.dumps({
                "status": "passed",
                "production_shape": {"status": "pending", "transport_profile": "lab_proxy"},
            }) + "\n", encoding="utf-8")

            with self.assertRaises(SystemExit) as raised:
                guard.validate_evidence("S01", evidence)

        self.assertIn("pending production_shape must list missing_items", str(raised.exception))

    def test_production_passed_rejects_lab_transport(self) -> None:
        with tempfile.TemporaryDirectory() as tmp:
            evidence = Path(tmp) / "evidence.json"
            evidence.write_text(json.dumps({
                "status": "passed",
                "production_shape": {
                    "status": "passed",
                    "transport_profile": "lab_kubeconfig_and_dev_gateway",
                    "missing_items": [],
                },
            }) + "\n", encoding="utf-8")

            with self.assertRaises(SystemExit) as raised:
                guard.validate_evidence("S01", evidence)

        self.assertIn("production_shape passed cannot use lab_kubeconfig_and_dev_gateway", str(raised.exception))

    def test_production_passed_requires_proof_items(self) -> None:
        with tempfile.TemporaryDirectory() as tmp:
            evidence = Path(tmp) / "evidence.json"
            evidence.write_text(json.dumps({
                "status": "passed",
                "production_shape": {
                    "status": "passed",
                    "transport_profile": "in_cluster_serviceaccount",
                    "missing_items": [],
                },
            }) + "\n", encoding="utf-8")

            with self.assertRaises(SystemExit) as raised:
                guard.validate_evidence("S01", evidence)

        self.assertIn("production_shape passed requires proof_items", str(raised.exception))

    def test_s01_production_passed_requires_gateway_create_and_list_evidence(self) -> None:
        with tempfile.TemporaryDirectory() as tmp:
            evidence = Path(tmp) / "evidence.json"
            evidence.write_text(json.dumps({
                "status": "passed",
                "production_shape": {
                    "status": "passed",
                    "transport_profile": "production_gateway_in_cluster_serviceaccount",
                    "missing_items": [],
                    "proof_items": [
                        "production_gateway",
                        "in_cluster_serviceaccount_rbac",
                        "persistent_route_metadata_reconciliation",
                    ],
                },
            }) + "\n", encoding="utf-8")

            with self.assertRaises(SystemExit) as raised:
                guard.validate_evidence("S01", evidence)

        self.assertIn("S01 production_shape passed requires gateway_vpc_create_status=201", str(raised.exception))

    def test_s02_production_passed_requires_gateway_workload_evidence(self) -> None:
        with tempfile.TemporaryDirectory() as tmp:
            evidence = Path(tmp) / "evidence.json"
            evidence.write_text(json.dumps({
                "status": "passed",
                "production_shape": {
                    "status": "passed",
                    "transport_profile": "metadata_target_tls",
                    "missing_items": [],
                    "proof_items": [
                        "production_gateway",
                        "production_per_cluster_metadata_target",
                        "production_tls_and_token_management",
                    ],
                },
            }) + "\n", encoding="utf-8")

            with self.assertRaises(SystemExit) as raised:
                guard.validate_evidence("S02", evidence)

        self.assertIn("S02 production_shape passed requires proxy_status=200", str(raised.exception))

    def test_s03_production_passed_requires_storage_lifecycle_evidence(self) -> None:
        with tempfile.TemporaryDirectory() as tmp:
            evidence = Path(tmp) / "evidence.json"
            evidence.write_text(json.dumps({
                "status": "passed",
                "production_shape": {
                    "status": "passed",
                    "transport_profile": "in_cluster_serviceaccount",
                    "missing_items": [],
                    "proof_items": [
                        "production_gateway",
                        "in_cluster_serviceaccount_rbac",
                        "tenant_storage_lifecycle_and_backup_restore",
                    ],
                },
            }) + "\n", encoding="utf-8")

            with self.assertRaises(SystemExit) as raised:
                guard.validate_evidence("S03", evidence)

        self.assertIn("S03 production_shape passed requires volume_status=201", str(raised.exception))

    def test_s04_production_passed_requires_gpu_and_dcgm_evidence(self) -> None:
        with tempfile.TemporaryDirectory() as tmp:
            evidence = Path(tmp) / "evidence.json"
            evidence.write_text(json.dumps({
                "status": "passed",
                "production_shape": {
                    "status": "passed",
                    "transport_profile": "in_cluster_kubernetes_api_and_cluster_metrics_service",
                    "missing_items": [],
                    "proof_items": [
                        "production_gateway",
                        "in_cluster_kubernetes_api",
                        "production_dcgm_service_or_prometheus_query",
                    ],
                },
            }) + "\n", encoding="utf-8")

            with self.assertRaises(SystemExit) as raised:
                guard.validate_evidence("S04", evidence)

        self.assertIn("S04 production_shape passed requires inventory_status=200", str(raised.exception))


if __name__ == "__main__":
    unittest.main()
