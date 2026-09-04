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

    def test_go_toolchain_must_meet_security_floor(self) -> None:
        self.workflow["env"]["GO_VERSION"] = "1.25.12"
        errors = validator.validate(self.workflow, self.makefile)
        self.assertTrue(any("Go security floor" in error for error in errors))

    def test_go_builder_images_must_match_ci_toolchain(self) -> None:
        dockerfiles = {
            "services/ani-gateway/Dockerfile": "FROM golang:1.25.12-alpine AS build\n",
        }
        errors = validator.validate(self.workflow, self.makefile, dockerfiles)
        self.assertTrue(any("must match CI GO_VERSION" in error for error in errors))

    def test_mutable_latest_tool_reference_is_blocked(self) -> None:
        self.workflow["jobs"]["go-ci"]["steps"][0]["uses"] = "example/tool@latest"
        errors = validator.validate(self.workflow, self.makefile)
        self.assertTrue(any("mutable @latest" in error for error in errors))

    def test_frontend_high_severity_audit_is_required(self) -> None:
        frontend = self.workflow["jobs"]["frontend-ci"]
        frontend["steps"] = [
            step for step in frontend["steps"] if step.get("name") != "Dependency audit"
        ]
        errors = validator.validate(self.workflow, self.makefile)
        self.assertTrue(any("high and critical npm audit" in error for error in errors))

    def test_runtime_observability_contract_runs_with_base_sha(self) -> None:
        services_text = str(self.workflow["jobs"]["services-pr-gate"])
        self.assertIn("make validate-service-runtime-observability", services_text)
        self.assertIn("BASE_SHA", services_text)

    def test_runtime_observability_base_sha_has_full_git_history(self) -> None:
        checkout = next(
            step for step in self.workflow["jobs"]["services-pr-gate"]["steps"]
            if str(step.get("uses", "")).startswith("actions/checkout@")
        )
        checkout["with"] = {"fetch-depth": 1}
        errors = validator.validate(self.workflow, self.makefile)
        self.assertTrue(any("full Git history" in error for error in errors), errors)


if __name__ == "__main__":
    unittest.main()
