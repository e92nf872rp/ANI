#!/usr/bin/env python3
"""Tests for the Services OpenAPI/Gateway route surface gate."""

from __future__ import annotations

import copy
import tempfile
import unittest
from pathlib import Path

import yaml

import validate_services_route_contract as validator


class ServicesRouteContractTest(unittest.TestCase):
    @classmethod
    def setUpClass(cls) -> None:
        cls.spec_routes = validator.openapi_routes(validator.load_yaml(validator.SPEC_PATH))
        cls.code_routes = validator.gateway_routes()
        cls.baseline = validator.load_baseline()

    def test_current_routes_only_report_exact_baselines(self) -> None:
        result = validator.validate(self.spec_routes, self.code_routes, self.baseline)
        self.assertFalse(result.errors)
        self.assertGreater(len(result.warnings), 0)

    def test_new_code_route_is_blocked(self) -> None:
        route = validator.Route("GET", "/new-service")
        result = validator.validate(self.spec_routes, self.code_routes | {route}, self.baseline)
        self.assertTrue(any("code_not_in_spec GET /new-service" in error for error in result.errors))

    def test_new_spec_operation_is_blocked(self) -> None:
        route = validator.Route("POST", "/new-service")
        result = validator.validate(self.spec_routes | {route}, self.code_routes, self.baseline)
        self.assertTrue(any("spec_not_in_code POST /new-service" in error for error in result.errors))

    def test_stale_baseline_is_blocked(self) -> None:
        baseline = copy.copy(self.baseline)
        baseline[("code_not_in_spec", ("GET", "/removed"))] = validator.BaselineEntry(
            kind="code_not_in_spec",
            method="GET",
            path="/removed",
            status="accepted_baseline",
            owner="test",
            reason="fixture",
        )
        result = validator.validate(self.spec_routes, self.code_routes, baseline)
        self.assertTrue(any("stale Services route baseline" in error for error in result.errors))

    def test_go_path_parameters_normalize_to_openapi_style(self) -> None:
        self.assertEqual(validator.normalize_path("/models/:model_id/versions"), "/models/{model_id}/versions")

    def test_gateway_routes_scan_router_go_and_other_non_test_files(self) -> None:
        with tempfile.TemporaryDirectory() as directory:
            router_dir = Path(directory)
            (router_dir / "router.go").write_text(
                'func register(svc *route.RouterGroup) { services := svc; services.GET("/router-level", handler) }\n',
                encoding="utf-8",
            )
            (router_dir / "router_test.go").write_text(
                'svc.GET("/test-only", handler)\n',
                encoding="utf-8",
            )
            self.assertEqual(validator.gateway_routes(router_dir), {validator.Route("GET", "/router-level")})

    def test_baseline_file_round_trips(self) -> None:
        with tempfile.TemporaryDirectory() as directory:
            path = Path(directory) / "baseline.yaml"
            path.write_text(yaml.safe_dump({"version": 1, "exceptions": []}, allow_unicode=True), encoding="utf-8")
            self.assertEqual(validator.load_baseline(path), {})


if __name__ == "__main__":
    unittest.main()
