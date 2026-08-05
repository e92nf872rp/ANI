#!/usr/bin/env python3
"""Tests for the Sandbox stateless live-gate runner."""

from __future__ import annotations

import unittest
from unittest.mock import patch

import run_instance_sandbox_stateless_live_gate as runner


class SandboxStatelessRunnerTest(unittest.TestCase):
    def test_postgres_count_passes_values_as_psql_variables(self) -> None:
        payload = "00000000-0000-0000-0000-000000000000'; DROP TABLE async_tasks; --"
        with patch.object(runner, "run_kubectl", return_value="1\n") as run_kubectl:
            self.assertEqual(1, runner.postgres_count("kubeconfig", "async_tasks", payload, "id", payload))
        command = run_kubectl.call_args.args[1]
        sql = command[-1]
        self.assertNotIn(payload, sql)
        self.assertIn(":'tenant_id'", sql)
        self.assertIn(":'key'", sql)
        self.assertIn(f"tenant_id={payload}", command)
        self.assertIn(f"key={payload}", command)


if __name__ == "__main__":
    unittest.main()
