#!/usr/bin/env python3
"""Tests for documentation entrypoint boundary validation."""

from __future__ import annotations

import unittest

import validate_doc_entrypoints as docs


class DocEntrypointValidationTest(unittest.TestCase):
    def test_controlled_unfreeze_current_markers_are_declared(self) -> None:
        required = {
            "Services 受控并行 PR 阶段",
            "CODEOWNERS 共同审查",
            "Services boundary gate",
            "repo/CURRENT-SPRINT.md",
        }

        self.assertTrue(required.issubset(set(docs.CURRENT_SERVICES_GOVERNANCE_MARKERS)))

    def test_rke_stale_pattern_matches_token_not_worker_env_names(self) -> None:
        self.assertTrue(docs.contains_stale_pattern("RKE"))
        self.assertTrue(docs.contains_stale_pattern("Use RKE for bootstrap"))
        self.assertFalse(docs.contains_stale_pattern("RECONCILE_WORKER_METRICS_URL"))
        self.assertFalse(docs.contains_stale_pattern("SERVICE_RKE_TOKEN_BOUNDARY"))
        self.assertFalse(docs.contains_stale_pattern("覆盖 RKE token 边界"))

    def test_services_api_prefix_is_not_rejected_as_core_stale_path(self) -> None:
        self.assertFalse(docs.contains_stale_pattern("/api/v1/svc/models"))
        self.assertFalse(docs.contains_stale_pattern("POST /api/v1/svc/models/import"))


if __name__ == "__main__":
    unittest.main()
