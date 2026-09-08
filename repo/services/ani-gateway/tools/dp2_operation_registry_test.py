#!/usr/bin/env python3

from __future__ import annotations

import copy
import sys
import unittest
from pathlib import Path

sys.path.insert(0, str(Path(__file__).resolve().parent))

import dp2_operation_registry as registry


def spec() -> dict:
    return {
        "openapi": "3.1.0",
        "components": {
            "securitySchemes": {
                "BearerAuth": {"type": "http", "scheme": "bearer"},
                "ApiKeyAuth": {"type": "apiKey", "in": "header", "name": "X-API-Key"},
                "ConsoleRefreshCookie": {"type": "apiKey", "in": "cookie", "name": "refresh"},
                "BossRefreshCookie": {"type": "apiKey", "in": "cookie", "name": "boss_refresh"},
            },
            "parameters": {
                "IAMIdempotencyKey": {
                    "name": "Idempotency-Key",
                    "in": "header",
                    "required": True,
                    "schema": {"type": "string"},
                }
            },
            "schemas": {"ErrorResponse": {"type": "object", "required": ["code", "message", "request_id"]}},
            "responses": {
                name: {
                    "x-ani-error-codes": {
                        "Unauthorized": ["CREDENTIAL_INVALID"],
                        "Forbidden": ["PERMISSION_DENIED"],
                        "Conflict": ["IDEMPOTENCY_CONFLICT"],
                        "RateLimitExceeded": ["AUTH_RATE_LIMITED"],
                        "ServiceUnavailable": ["AUTHZ_OPERATION_UNREGISTERED", "AUTHZ_POLICY_MISMATCH", "IAM_UNAVAILABLE", "TENANT_IAM_NOT_READY"],
                        "GatewayTimeout": ["IAM_TIMEOUT"],
                    }[name],
                    "content": {"application/json": {"schema": {"$ref": "#/components/schemas/ErrorResponse"}}},
                }
                for name in ("Unauthorized", "Forbidden", "Conflict", "RateLimitExceeded", "ServiceUnavailable", "GatewayTimeout")
            },
        },
        "security": [{"BearerAuth": []}],
        "paths": {
            "/healthz": {
                "get": {
                    "operationId": "liveness",
                    "security": [],
                    "x-ani-handler": "gateway.liveness",
                    "x-ani-owner": "gateway",
                    "x-ani-auth-classification": "public",
                }
            },
            "/instances/{instance_id}": {
                "get": {
                    "operationId": "getInstance",
                    "x-ani-handler": "gateway.getInstance",
                    "x-ani-owner": "core-control",
                    "x-ani-exposure": "internal",
                    "x-ani-auth-classification": "authorized",
                    "x-ani-contract": "direct-p2",
                    "x-ani-authn": {
                        "principal_kinds": ["human", "service"],
                        "credential_kinds": ["access_token", "api_key", "service_token"],
                    },
                    "x-ani-authz": {
                        "version": "v1",
                        "resource": "instances",
                        "actions": ["read"],
                        "scope": "tenant",
                        "obligations": [
                            {"type": "resource_tenant_match", "handler": "core.resource_tenant"}
                        ],
                    },
                    "responses": {
                        "401": {"$ref": "#/components/responses/Unauthorized"},
                        "403": {"$ref": "#/components/responses/Forbidden"},
                        "503": {"$ref": "#/components/responses/ServiceUnavailable"},
                        "504": {"$ref": "#/components/responses/GatewayTimeout"},
                    },
                }
            },
        },
    }


def policy() -> dict:
    return {
        "schema_version": registry.SCHEMA_VERSION,
        "error_contract": {
            "401": {"response": "Unauthorized", "default_code": "CREDENTIAL_INVALID", "reason_codes": ["CREDENTIAL_INVALID"]},
            "403": {"response": "Forbidden", "default_code": "PERMISSION_DENIED", "reason_codes": ["PERMISSION_DENIED"]},
            "409": {"response": "Conflict", "default_code": "IDEMPOTENCY_CONFLICT", "reason_codes": ["IDEMPOTENCY_CONFLICT"]},
            "429": {"response": "RateLimitExceeded", "default_code": "AUTH_RATE_LIMITED", "reason_codes": ["AUTH_RATE_LIMITED"]},
            "503": {
                "response": "ServiceUnavailable",
                "default_code": "IAM_UNAVAILABLE",
                "reason_codes": ["AUTHZ_OPERATION_UNREGISTERED", "AUTHZ_POLICY_MISMATCH", "IAM_UNAVAILABLE", "TENANT_IAM_NOT_READY"],
            },
            "504": {"response": "GatewayTimeout", "default_code": "IAM_TIMEOUT", "reason_codes": ["IAM_TIMEOUT"]},
        },
        "trusted_context": {
            "strip_client_prefix": "x-ani-",
            "inject_headers": list(registry.TRUSTED_CONTEXT_HEADERS),
        },
        "obligation_handlers": [{"name": "core.resource_tenant", "owner": "core-control"}],
    }


class ContractTest(unittest.TestCase):
    def test_expands_complete_contract_and_is_deterministic(self) -> None:
        first = registry.expand(spec(), policy())
        second = registry.expand(copy.deepcopy(spec()), copy.deepcopy(policy()))
        self.assertEqual(first, second)
        self.assertEqual(first["operations"][0]["iam_decision_calls"], 1)
        self.assertEqual(first["operations"][1]["iam_decision_calls"], 0)
        self.assertTrue(first["policy_revision"].startswith("sha256:"))

    def test_missing_operation_fails_closed(self) -> None:
        candidate = spec()
        del candidate["paths"]["/instances/{instance_id}"]["get"]["x-ani-authz"]
        with self.assertRaisesRegex(registry.ContractError, "missing x-ani-authz"):
            registry.expand(candidate, policy())

    def test_duplicate_operation_fails_closed(self) -> None:
        candidate = spec()
        candidate["paths"]["/instances/{instance_id}"]["get"]["operationId"] = "liveness"
        with self.assertRaisesRegex(registry.ContractError, "duplicate operationId"):
            registry.expand(candidate, policy())

    def test_unknown_obligation_handler_fails_closed(self) -> None:
        candidate = spec()
        candidate["paths"]["/instances/{instance_id}"]["get"]["x-ani-authz"]["obligations"][0]["handler"] = "missing"
        with self.assertRaisesRegex(registry.ContractError, "unknown obligation handler"):
            registry.expand(candidate, policy())

    def test_public_cannot_request_iam_or_permission(self) -> None:
        candidate = spec()
        candidate["paths"]["/healthz"]["get"]["x-ani-authz"] = {
            "version": "v1", "resource": "x", "actions": ["read"], "scope": "tenant", "obligations": []
        }
        with self.assertRaisesRegex(registry.ContractError, "public operation"):
            registry.expand(candidate, policy())

    def test_stable_errors_must_use_error_response(self) -> None:
        candidate = spec()
        candidate["components"]["responses"]["GatewayTimeout"] = {"description": "wrong"}
        with self.assertRaisesRegex(registry.ContractError, "must use ErrorResponse"):
            registry.expand(candidate, policy())

    def test_trusted_context_rejects_roles_and_permissions(self) -> None:
        candidate = policy()
        candidate["trusted_context"]["inject_headers"].append("x-ani-roles")
        with self.assertRaisesRegex(registry.ContractError, "fixed allowlist"):
            registry.expand(spec(), candidate)

    def test_unknown_owner_fails_closed(self) -> None:
        candidate_spec = spec()
        operation = candidate_spec["paths"]["/instances/{instance_id}"]["get"]
        operation["x-ani-owner"] = "unknown"
        with self.assertRaisesRegex(registry.ContractError, "unsupported owner"):
            registry.expand(candidate_spec, policy())

    def test_every_operation_requires_openapi_owner_and_handler(self) -> None:
        candidate = spec()
        candidate["paths"]["/healthz"]["get"].update(
            {
                "x-ani-owner": "gateway",
                "x-ani-handler": "gateway.liveness",
                "x-ani-auth-classification": "public",
            }
        )
        candidate["paths"]["/instances/{instance_id}"]["get"].update(
            {
                "x-ani-owner": "core-control",
                "x-ani-handler": "gateway.getInstance",
                "x-ani-auth-classification": "authorized",
                "x-ani-authn": {
                    "principal_kinds": ["human", "service"],
                    "credential_kinds": ["access_token", "api_key", "service_token"],
                },
                "x-ani-authz": {
                    "version": "v1",
                    "resource": "instances",
                    "actions": ["read"],
                    "scope": "tenant",
                    "obligations": [
                        {"type": "resource_tenant_match", "handler": "core.resource_tenant"}
                    ],
                },
            }
        )
        del candidate["paths"]["/healthz"]["get"]["x-ani-owner"]
        with self.assertRaisesRegex(registry.ContractError, "missing x-ani-owner"):
            registry.expand(candidate, policy())

    def test_duplicate_openapi_gateway_handler_fails_closed(self) -> None:
        candidate = spec()
        for path, handler in (("/healthz", "gateway.shared"), ("/instances/{instance_id}", "gateway.shared")):
            operation = candidate["paths"][path]["get"]
            operation["x-ani-handler"] = handler
        with self.assertRaisesRegex(registry.ContractError, "duplicate Gateway handler"):
            registry.expand(candidate, policy())

    def test_decision_rpc_is_explicit_and_single_call(self) -> None:
        expanded = registry.expand(spec(), policy())
        by_id = {item["operation_id"]: item for item in expanded["operations"]}
        self.assertEqual(by_id["liveness"]["iam_decision"], "none")
        self.assertEqual(by_id["getInstance"]["iam_decision"], "check_permission")
        self.assertEqual(by_id["getInstance"]["iam_decision_calls"], 1)

    def test_503_contract_has_policy_fail_closed_reasons(self) -> None:
        expanded = registry.expand(spec(), policy())
        self.assertEqual(
            expanded["error_contract"]["503"]["reason_codes"],
            ["AUTHZ_OPERATION_UNREGISTERED", "AUTHZ_POLICY_MISMATCH", "IAM_UNAVAILABLE", "TENANT_IAM_NOT_READY"],
        )

    def test_public_operation_must_override_global_security(self) -> None:
        candidate = spec()
        del candidate["paths"]["/healthz"]["get"]["security"]
        with self.assertRaisesRegex(registry.ContractError, "public operation liveness must set security to an empty array"):
            registry.expand(candidate, policy())

    def test_protected_operation_rejects_legacy_api_key_scheme(self) -> None:
        candidate = spec()
        candidate["paths"]["/instances/{instance_id}"]["get"]["security"] = [{"ApiKeyAuth": []}]
        with self.assertRaisesRegex(registry.ContractError, "cannot accept ApiKeyAuth"):
            registry.expand(candidate, policy())

    def test_protected_operation_rejects_multi_scheme_and_requirement(self) -> None:
        candidate = spec()
        candidate["paths"]["/instances/{instance_id}"]["get"]["security"] = [
            {"BearerAuth": [], "ConsoleRefreshCookie": []}
        ]
        with self.assertRaisesRegex(registry.ContractError, "AND security requirements"):
            registry.expand(candidate, policy())

    def test_target_operation_requires_stable_error_responses(self) -> None:
        candidate = spec()
        operation = candidate["paths"]["/instances/{instance_id}"]["get"]
        operation["x-ani-owner"] = "core-control"
        operation["x-ani-auth-classification"] = "authorized"
        operation["x-ani-authz"] = {
            "version": "v1",
            "resource": "instances",
            "actions": ["read"],
            "scope": "tenant",
            "obligations": [{"type": "resource_tenant_match", "handler": "core.resource_tenant"}],
        }
        del operation["responses"]["504"]
        with self.assertRaisesRegex(registry.ContractError, "missing stable responses.*504"):
            registry.expand(candidate, policy())

    def test_target_mutation_requires_idempotency_key(self) -> None:
        candidate = spec()
        candidate["paths"]["/instances/{instance_id}"] = {
            "post": {
                "operationId": "getInstance",
                "security": [{"BearerAuth": []}],
                "x-ani-handler": "gateway.getInstance",
                "responses": {
                    status: {"$ref": f"#/components/responses/{name}"}
                    for status, name in {
                        "401": "Unauthorized",
                        "403": "Forbidden",
                        "409": "Conflict",
                        "429": "RateLimitExceeded",
                        "503": "ServiceUnavailable",
                        "504": "GatewayTimeout",
                    }.items()
                },
                "x-ani-owner": "core-control",
                "x-ani-auth-classification": "authorized",
                "x-ani-contract": "direct-p2",
                "x-ani-authn": {
                    "principal_kinds": ["human", "service"],
                    "credential_kinds": ["access_token", "api_key", "service_token"],
                },
                "x-ani-authz": {
                    "version": "v1",
                    "resource": "instances",
                    "actions": ["read"],
                    "scope": "tenant",
                    "obligations": [{"type": "resource_tenant_match", "handler": "core.resource_tenant"}],
                },
            }
        }
        with self.assertRaisesRegex(registry.ContractError, "missing required Idempotency-Key"):
            registry.expand(candidate, policy())

    def test_client_x_ani_header_parameter_is_forbidden(self) -> None:
        candidate = spec()
        candidate["paths"]["/instances/{instance_id}"]["get"]["parameters"] = [
            {"name": "X-Ani-Tenant-Id", "in": "header", "schema": {"type": "string"}}
        ]
        with self.assertRaisesRegex(registry.ContractError, "client-controlled x-ani"):
            registry.expand(candidate, policy())

    def test_accepted_main_operations_use_direct_p2_semantics(self) -> None:
        candidate = registry.load_yaml(registry.DEFAULT_OPENAPI)
        expected = {
            "/platform/services/health": {
                "operation_id": "getPlatformServiceHealth",
                "resource": "observability",
                "actions": ["read"],
                "scope": "platform",
            },
            "/observability/resource_trend": {
                "operation_id": "queryResourceTrendObservability",
                "resource": "observability",
                "actions": ["read"],
                "scope": "tenant",
            },
            "/platform/capacity": {
                "operation_id": "getPlatformCapacity",
                "resource": "capacity",
                "actions": ["get"],
                "scope": "platform",
            },
        }
        for path, contract in expected.items():
            with self.subTest(path=path):
                operation = candidate["paths"][path]["get"]
                operation_id = contract["operation_id"]
                self.assertEqual(operation["operationId"], operation_id)
                self.assertEqual(operation["x-ani-handler"], f"gateway.{operation_id}")
                self.assertEqual(operation["x-ani-owner"], "core-control")
                self.assertEqual(operation["x-ani-auth-classification"], "authorized")
                self.assertEqual(
                    operation["x-ani-authn"],
                    {
                        "principal_kinds": ["human", "service"],
                        "credential_kinds": ["access_token", "api_key", "service_token"],
                    },
                )
                self.assertEqual(
                    operation["x-ani-authz"],
                    {
                        "version": "v1",
                        "resource": contract["resource"],
                        "actions": contract["actions"],
                        "scope": contract["scope"],
                        "obligations": [],
                    },
                )
                self.assertEqual(operation.get("security", candidate["security"]), [{"BearerAuth": []}])
                self.assertNotIn("x-ani-contract", operation)

    def test_real_contract_is_complete(self) -> None:
        # Contract-first RED: this fails until the target policy source and all
        # explicit operationIds are present.
        expanded = registry.expand(registry.load_yaml(registry.DEFAULT_OPENAPI), registry.load_yaml(registry.DEFAULT_POLICY))
        self.assertGreater(len(expanded["operations"]), 0)

    def test_real_replacement_manifest_is_complete(self) -> None:
        current = registry.collect_routes(registry.load_yaml(registry.DEFAULT_OPENAPI))
        result = registry.validate_replacement_manifest(
            registry.load_yaml(registry.DEFAULT_REPLACEMENTS), current
        )
        self.assertEqual(result["operation_counts"], {"core-v1": 28, "services-v1": 31})
        self.assertEqual(result["deletion_ids"], [f"D{number:03d}" for number in range(1, 29)])


if __name__ == "__main__":
    unittest.main()
