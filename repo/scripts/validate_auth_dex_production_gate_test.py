#!/usr/bin/env python3
"""Tests for Sprint 13 Auth/Dex production-shaped gate."""

from __future__ import annotations

import json
import tempfile
import unittest
from copy import deepcopy
from pathlib import Path
from unittest.mock import patch

import validate_auth_dex_production_gate as gate
import validate_auth_dex_production_live as live


class AuthDexProductionGateTest(unittest.TestCase):
    def test_contract_defines_auth_dex_and_gateway_checks(self) -> None:
        document = gate.load_gate(gate.DEFAULT_GATE)

        gate.validate_contract(document)

        check_ids = {check["id"] for check in document["live_checks"]}
        self.assertIn("dex-discovery-ready", check_ids)
        self.assertIn("dex-jwks-ready", check_ids)
        self.assertIn("gateway-protected-route-rejects-anonymous", check_ids)
        self.assertIn("gateway-oidc-auth-code-flow", check_ids)
        self.assertIn("gateway-protected-route-accepts-dex-token", check_ids)
        self.assertIn("gateway-refresh-token-flow", check_ids)

    def test_contract_rejects_missing_auth_check(self) -> None:
        document = deepcopy(gate.load_gate(gate.DEFAULT_GATE))
        document["live_checks"] = [
            check for check in document["live_checks"]
            if check["id"] != "gateway-protected-route-accepts-dex-token"
        ]

        with self.assertRaises(SystemExit) as raised:
            gate.validate_contract(document)

        self.assertIn("missing live checks: gateway-protected-route-accepts-dex-token", str(raised.exception))

    def test_production_manifest_requires_gateway_non_dev_auth_and_auth_service(self) -> None:
        docs = gate.load_production_manifest(gate.DEFAULT_MANIFEST)

        gate.validate_production_manifest(docs)

    def test_production_manifest_rejects_dev_gateway_auth_mode(self) -> None:
        docs = gate.load_production_manifest(gate.DEFAULT_MANIFEST)
        gateway = next(doc for doc in docs if doc.get("kind") == "Deployment" and doc.get("metadata", {}).get("name") == "ani-gateway")
        env = gateway["spec"]["template"]["spec"]["containers"][0]["env"]
        for item in env:
            if item.get("name") == "ANI_AUTH_MODE":
                item["value"] = "dev"

        with self.assertRaises(SystemExit) as raised:
            gate.validate_production_manifest(docs)

        self.assertIn("Gateway ANI_AUTH_MODE must not be dev", str(raised.exception))

    def test_production_manifest_requires_dex_static_client_secret_from_secret(self) -> None:
        docs = gate.load_production_manifest(gate.DEFAULT_MANIFEST)
        dex_config = next(doc for doc in docs if doc.get("kind") == "ConfigMap" and doc.get("metadata", {}).get("name") == "ani-dex-production-config")
        dex_config["data"]["config.yaml"] = dex_config["data"]["config.yaml"].replace("$ANI_DEX_CLIENT_SECRET", "literal-secret")

        with self.assertRaises(SystemExit) as raised:
            gate.validate_production_manifest(docs)

        self.assertIn("Dex static client secret must come from environment substitution", str(raised.exception))

    def test_production_manifest_requires_dex_runtime_secret_rendering(self) -> None:
        docs = gate.load_production_manifest(gate.DEFAULT_MANIFEST)
        dex = next(doc for doc in docs if doc.get("kind") == "Deployment" and doc.get("metadata", {}).get("name") == "ani-dex")
        container = dex["spec"]["template"]["spec"]["containers"][0]
        container["command"] = ["dex", "serve", "/etc/dex/config.yaml"]
        container.pop("args", None)

        with self.assertRaises(SystemExit) as raised:
            gate.validate_production_manifest(docs)

        self.assertIn("Dex container must render client secret from Secret before startup", str(raised.exception))

    def test_production_manifest_exposes_dex_nodeport_for_live_oidc_flow(self) -> None:
        docs = gate.load_production_manifest(gate.DEFAULT_MANIFEST)
        dex_service = next(doc for doc in docs if doc.get("kind") == "Service" and doc.get("metadata", {}).get("name") == "ani-dex")

        self.assertEqual("NodePort", dex_service["spec"].get("type"))
        self.assertEqual(30556, dex_service["spec"]["ports"][0].get("nodePort"))

    def test_live_runner_rewrites_internal_issuer_urls_to_public_transport(self) -> None:
        internal = "http://ani-dex.ani-system.svc.cluster.local:5556/dex"
        public = "http://100.64.128.1:30556/dex"

        rewritten = live.rewrite_internal_issuer_url(
            internal + "/auth?client_id=ani-console",
            expected_issuer=internal,
            public_issuer=public,
        )

        self.assertEqual(public + "/auth?client_id=ani-console", rewritten)

    def test_live_runner_reads_password_from_file_without_requiring_cli_literal(self) -> None:
        with tempfile.TemporaryDirectory() as tmp:
            password_path = Path(tmp) / "password"
            password_path.write_text("fixture-password\n", encoding="utf-8")
            args = type("Args", (), {"password": "", "password_file": str(password_path)})()

            self.assertEqual("fixture-password", live.resolve_password(args))

    def test_evidence_requires_production_auth_proof(self) -> None:
        evidence = {
            "status": "passed",
            "auth_dex_production_shape": {
                "status": "passed",
                "gateway_auth_mode": "auth_service",
                "proof_items": [
                    "gateway_non_dev_auth",
                    "dex_discovery_and_jwks",
                    "gateway_rejects_anonymous",
                    "gateway_accepts_dex_oidc_token",
                    "gateway_refresh_token",
                    "auth_service_rbac_check",
                ],
                "anonymous_status": 401,
                "oidc_begin_status": 200,
                "oidc_complete_status": 200,
                "authorized_status": 200,
                "refresh_status": 200,
            },
        }

        gate.validate_evidence_payload(evidence)

    def test_evidence_rejects_dev_auth_mode(self) -> None:
        evidence = {
            "status": "passed",
            "auth_dex_production_shape": {
                "status": "passed",
                "gateway_auth_mode": "dev",
                "proof_items": [
                    "gateway_non_dev_auth",
                    "dex_discovery_and_jwks",
                    "gateway_rejects_anonymous",
                    "gateway_accepts_dex_oidc_token",
                    "gateway_refresh_token",
                    "auth_service_rbac_check",
                ],
                "anonymous_status": 401,
                "oidc_begin_status": 200,
                "oidc_complete_status": 200,
                "authorized_status": 200,
                "refresh_status": 200,
            },
        }

        with self.assertRaises(SystemExit) as raised:
            gate.validate_evidence_payload(evidence)

        self.assertIn("gateway_auth_mode must not be dev", str(raised.exception))

    def test_cli_validates_evidence_file(self) -> None:
        evidence = {
            "status": "passed",
            "auth_dex_production_shape": {
                "status": "passed",
                "gateway_auth_mode": "auth_service",
                "proof_items": [
                    "gateway_non_dev_auth",
                    "dex_discovery_and_jwks",
                    "gateway_rejects_anonymous",
                    "gateway_accepts_dex_oidc_token",
                    "gateway_refresh_token",
                    "auth_service_rbac_check",
                ],
                "anonymous_status": 401,
                "oidc_begin_status": 200,
                "oidc_complete_status": 200,
                "authorized_status": 200,
                "refresh_status": 200,
            },
        }
        with tempfile.TemporaryDirectory() as tmp:
            evidence_path = Path(tmp) / "evidence.json"
            evidence_path.write_text(json.dumps(evidence) + "\n", encoding="utf-8")
            with (
                patch("sys.argv", ["validate_auth_dex_production_gate.py", "--evidence", str(evidence_path)]),
                patch.object(gate, "validate_docs"),
            ):
                gate.main()


if __name__ == "__main__":
    unittest.main()
