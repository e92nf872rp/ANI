#!/usr/bin/env python3
from __future__ import annotations

import unittest

import validate_inference_access_policy_contract as validator


class InferenceAccessPolicyContractTest(unittest.TestCase):
    def test_requires_policy_paths(self) -> None:
        spec = validator.load_spec("api/openapi/services/v1.yaml")
        paths = spec["paths"]
        self.assertIn("/inference-policies", paths)
        self.assertIn("/inference-policies/{policy_id}", paths)
        self.assertIn("/inference-services/{service_id}/policies", paths)
        self.assertIn("/inference-policy-events", paths)

    def test_service_policy_path_is_not_feature_not_available(self) -> None:
        spec = validator.load_spec("api/openapi/services/v1.yaml")
        operation = spec["paths"]["/inference-services/{service_id}/policies"]["put"]
        self.assertNotIn("501", operation["responses"])
        self.assertNotIn("FEATURE_NOT_AVAILABLE", operation.get("description", ""))

    def test_required_policy_schemas_exist(self) -> None:
        schemas = validator.load_spec("api/openapi/services/v1.yaml")["components"]["schemas"]
        for name in validator.REQUIRED_SCHEMAS:
            self.assertIn(name, schemas)

    def test_mutating_operations_require_idempotency_key(self) -> None:
        spec = validator.load_spec("api/openapi/services/v1.yaml")
        validator.validate_mutating_idempotency(spec)

    def test_policy_operations_use_v1_authz_format(self) -> None:
        spec = validator.load_spec("api/openapi/services/v1.yaml")
        for method, path in validator.REQUIRED_OPERATIONS:
            operation = spec["paths"][path][method]
            self.assertEqual(operation.get("security"), validator.REQUIRED_SECURITY)
            self.assertEqual(
                operation.get("x-ani-authz"),
                {
                    "version": "v1",
                    "resource": "inference_policy",
                    "action": validator.REQUIRED_AUTHZ_ACTIONS[(method, path)],
                    "boundary": "tenant",
                    "principal_kinds": ["user"],
                },
            )


if __name__ == "__main__":
    unittest.main()
