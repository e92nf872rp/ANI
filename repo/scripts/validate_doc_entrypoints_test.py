#!/usr/bin/env python3
"""Tests for documentation entrypoint boundary validation."""

from __future__ import annotations

from pathlib import Path
import tempfile
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

    def test_services_subresource_path_is_not_rejected_as_base_stale_path(self) -> None:
        self.assertTrue(docs.contains_stale_pattern("POST /knowledge-bases"))
        self.assertFalse(docs.contains_stale_pattern("POST /knowledge-bases/{kb_id}/documents"))

    def test_markdown_paths_ignore_execution_scratch_directories(self) -> None:
        original_project_root = docs.PROJECT_ROOT
        with tempfile.TemporaryDirectory() as temp_dir:
            project_root = Path(temp_dir)
            included = project_root / "README.md"
            ignored_superpowers = project_root / ".superpowers" / "sdd" / "task-report.md"
            ignored_worktree = project_root / ".worktrees" / "task" / "README.md"
            ignored_node_modules = project_root / "node_modules" / "package" / "README.md"

            for path in (included, ignored_superpowers, ignored_worktree, ignored_node_modules):
                path.parent.mkdir(parents=True, exist_ok=True)
                path.write_text("# test\n", encoding="utf-8")

            try:
                docs.PROJECT_ROOT = project_root
                self.assertEqual(docs.markdown_paths(), [included])
            finally:
                docs.PROJECT_ROOT = original_project_root


if __name__ == "__main__":
    unittest.main()
