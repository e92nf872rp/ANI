#!/usr/bin/env python3
"""Fixture tests for the Services semantic contract gate."""

from __future__ import annotations

import copy
import tempfile
import unittest
from pathlib import Path

import yaml

import validate_services_contract as validator


ROOT = Path(__file__).resolve().parents[1]


class ServicesContractTest(unittest.TestCase):
    @classmethod
    def setUpClass(cls) -> None:
        cls.spec = validator.load_yaml(validator.SPEC_PATH)
        cls.baseline = validator.load_baseline(validator.BASELINE_PATH)

    def validate_fixture(self, spec: dict, baseline: dict | None = None) -> validator.Result:
        return validator.validate(spec, self.baseline if baseline is None else baseline)

    @staticmethod
    def add_operation(spec: dict, path: str, method: str, operation: dict) -> None:
        spec.setdefault("paths", {}).setdefault(path, {})[method] = operation

    def test_current_contract_only_reports_accepted_baselines(self) -> None:
        result = self.validate_fixture(copy.deepcopy(self.spec))
        self.assertFalse(result.errors)
        self.assertGreater(len(result.warnings), 0)

    def test_new_write_missing_idempotency_is_blocked(self) -> None:
        spec = copy.deepcopy(self.spec)
        self.add_operation(
            spec,
            "/new-write",
            "post",
            {
                "operationId": "newWrite",
                "security": [{"BearerAuth": []}],
                "requestBody": {
                    "required": True,
                    "content": {"application/json": {"schema": {"required": ["name"], "properties": {"name": {"type": "string"}}}}},
                },
                "responses": {"201": {"description": "created"}},
            },
        )
        result = self.validate_fixture(spec)
        self.assertTrue(any("newWrite/write_requires_idempotency" in error for error in result.errors))

    def test_exact_existing_idempotency_exception_is_warning_only(self) -> None:
        result = self.validate_fixture(copy.deepcopy(self.spec))
        self.assertTrue(any("uploadKnowledgeBaseDocument/write_requires_idempotency" in warning for warning in result.warnings))
        self.assertFalse(any("uploadKnowledgeBaseDocument/write_requires_idempotency" in error for error in result.errors))

    def test_new_operation_without_security_is_blocked(self) -> None:
        spec = copy.deepcopy(self.spec)
        self.add_operation(
            spec,
            "/new-unsecured",
            "get",
            {"operationId": "newUnsecured", "responses": {"200": {"description": "ok"}}},
        )
        result = self.validate_fixture(spec)
        self.assertTrue(any("newUnsecured/operation_security" in error for error in result.errors))

    def test_new_202_with_wrong_schema_is_blocked(self) -> None:
        spec = copy.deepcopy(self.spec)
        self.add_operation(
            spec,
            "/new-async",
            "post",
            {
                "operationId": "newAsync",
                "security": [{"BearerAuth": []}],
                "requestBody": {
                    "required": True,
                    "content": {"application/json": {"schema": {"required": ["idempotency_key"], "properties": {"idempotency_key": {"type": "string"}}}}},
                },
                "responses": {"202": {"description": "accepted", "content": {"application/json": {"schema": {"$ref": "#/components/schemas/Model"}}}}},
            },
        )
        result = self.validate_fixture(spec)
        self.assertTrue(any("newAsync/async_202_requires_async_task" in error for error in result.errors))

    def test_stale_baseline_is_blocked(self) -> None:
        baseline = copy.deepcopy(self.baseline)
        baseline[("noLongerExists", "operation_security")] = validator.BaselineEntry(
            operation_id="noLongerExists",
            rule="operation_security",
            status=validator.BASELINE_STATUS,
            owner="test",
            reason="fixture",
        )
        result = self.validate_fixture(copy.deepcopy(self.spec), baseline)
        self.assertTrue(any("stale Services contract baseline" in error for error in result.errors))

    def test_baseline_file_round_trips(self) -> None:
        with tempfile.TemporaryDirectory() as directory:
            path = Path(directory) / "baseline.yaml"
            path.write_text(yaml.safe_dump({"version": 1, "exceptions": []}, allow_unicode=True), encoding="utf-8")
            self.assertEqual(validator.load_baseline(path), {})


if __name__ == "__main__":
    unittest.main()
