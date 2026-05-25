#!/usr/bin/env python3
"""Tests for documentation entrypoint boundary validation."""

from __future__ import annotations

import unittest

import validate_doc_entrypoints as docs


class DocEntrypointValidationTest(unittest.TestCase):
    def test_rke_stale_pattern_matches_token_not_worker_env_names(self) -> None:
        self.assertTrue(docs.contains_stale_pattern("RKE"))
        self.assertTrue(docs.contains_stale_pattern("Use RKE for bootstrap"))
        self.assertFalse(docs.contains_stale_pattern("RECONCILE_WORKER_METRICS_URL"))


if __name__ == "__main__":
    unittest.main()
