#!/usr/bin/env python3
"""Tests for Sprint 4 SDK Beta Core/Services SDK separation."""

from __future__ import annotations

import unittest

import validate_sdk_beta as beta


class SDKBetaValidationTest(unittest.TestCase):
    def test_services_idempotency_may_contain_services_operations(self) -> None:
        core_metadata = {"idempotencyOperations": ["createInstance", "createNetworkRoute"]}
        services_metadata = {"idempotencyOperations": ["createSandbox", "updateTenantRole"]}

        beta.validate_services_idempotency_separation(core_metadata, services_metadata)

    def test_services_idempotency_rejects_core_operations(self) -> None:
        core_metadata = {"idempotencyOperations": ["createInstance", "createNetworkRoute"]}
        services_metadata = {"idempotencyOperations": ["createSandbox", "createNetworkRoute"]}

        with self.assertRaises(SystemExit) as raised:
            beta.validate_services_idempotency_separation(core_metadata, services_metadata)

        self.assertIn("Core idempotency operations", str(raised.exception))
        self.assertIn("createNetworkRoute", str(raised.exception))


if __name__ == "__main__":
    unittest.main()
