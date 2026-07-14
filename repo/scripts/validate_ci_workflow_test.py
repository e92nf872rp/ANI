#!/usr/bin/env python3
"""Tests for the CI workflow contract."""

from __future__ import annotations

import copy
import unittest

import validate_ci_workflow as validator


class CIWorkflowContractTest(unittest.TestCase):
    def setUp(self) -> None:
        self.workflow = copy.deepcopy(validator.load_workflow())
        self.makefile = validator.MAKEFILE_PATH.read_text(encoding="utf-8")

    def test_current_workflow_is_fail_closed(self) -> None:
        self.assertEqual(validator.validate(self.workflow, self.makefile), [])

    def test_missing_required_job_is_blocked(self) -> None:
        self.workflow["jobs"].pop("services-pr-gate")
        errors = validator.validate(self.workflow, self.makefile)
        self.assertTrue(any("missing required jobs" in error for error in errors))

    def test_aggregate_without_always_is_blocked(self) -> None:
        self.workflow["jobs"]["required-gates"]["if"] = "${{ success() }}"
        errors = validator.validate(self.workflow, self.makefile)
        self.assertTrue(any("always()" in error for error in errors))

    def test_services_gate_cannot_install_runtime_requirements(self) -> None:
        self.workflow["jobs"]["services-pr-gate"]["steps"][0]["run"] = (
            "pip install -r ai/rag-engine/requirements.txt"
        )
        errors = validator.validate(self.workflow, self.makefile)
        self.assertTrue(any("RAG runtime requirements" in error for error in errors))

    def test_non_portable_go_cache_is_blocked(self) -> None:
        errors = validator.validate(self.workflow, "GOCACHE=/private/tmp/ani-go-build")
        self.assertTrue(any("/private/tmp" in error for error in errors))

    def test_non_portable_gate_script_is_blocked(self) -> None:
        errors = validator.validate(
            self.workflow,
            "GOCACHE=$(CURDIR)/.cache/go-build",
            {"scripts/validate_sdk_alpha.py": "GOCACHE=/private/tmp/ani-go-build"},
        )
        self.assertTrue(any("validate_sdk_alpha.py" in error for error in errors))

    def test_go_lint_must_use_workspace_module_discovery(self) -> None:
        lint_step = next(
            step for step in self.workflow["jobs"]["go-ci"]["steps"]
            if step.get("name") == "Lint (golangci-lint)"
        )
        lint_step["run"] = "golangci-lint run ./..."
        errors = validator.validate(self.workflow, self.makefile)
        self.assertTrue(any("multi-module root" in error for error in errors))

    def test_dependency_scan_must_not_use_static_module_list(self) -> None:
        scan_step = next(
            step for step in self.workflow["jobs"]["dependency-scan"]["steps"]
            if step.get("name") == "Scan Go dependencies (govulncheck)"
        )
        scan_step["run"] = (
            "for module in cli/ani pkg; do (cd $module && govulncheck ./...); done"
        )
        errors = validator.validate(self.workflow, self.makefile)
        self.assertTrue(any("static Go module list" in error for error in errors))

    def test_mutable_latest_tool_reference_is_blocked(self) -> None:
        self.workflow["jobs"]["go-ci"]["steps"][0]["uses"] = "example/tool@latest"
        errors = validator.validate(self.workflow, self.makefile)
        self.assertTrue(any("mutable @latest" in error for error in errors))


if __name__ == "__main__":
    unittest.main()
