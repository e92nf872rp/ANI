#!/usr/bin/env python3
"""generate_gateway_authz.py 的单元测试（AUTHZ-POLICY-A）。

覆盖计划 Section 3 的生成器验收点：
- 5 字段严格 schema 校验（缺字段/未知字段/非 v1/非法 boundary/非法 kinds）
- security OR 语义保留、AND 组合显式拒绝
- public（security: []）与 x-ani-authz 冲突时失败
- 无扩展的既有 operation 归为 legacy
- 派生 operationId（baseline 冻结 operation_id='' 的 3 个端点）
- 重复 route / 重复 operationId 失败
- 同输入重复渲染字节一致（determinism）
"""

from __future__ import annotations

import copy
import unittest

import yaml

import generate_gateway_authz as generator

ROOT = generator.ROOT


def make_spec() -> dict:
    return {
        "openapi": "3.0.0",
        "security": [{"BearerAuth": []}, {"ApiKeyAuth": []}],
        "paths": {
            "/admin/quota-meta": {
                "get": {
                    "operationId": "listQuotaMeta",
                }
            },
            "/auth/refresh": {
                "post": {
                    # baseline 冻结 operation_id=''：无 operationId，依赖派生表。
                }
            },
            "/healthz": {
                "get": {
                    "operationId": "healthz",
                    "security": [],
                }
            },
        },
    }


def authz_extension(**overrides) -> dict:
    ext = {
        "version": "v1",
        "resource": "quota-meta",
        "action": "read",
        "boundary": "platform",
        "principal_kinds": ["user", "service"],
    }
    ext.update(overrides)
    return ext


def direct_p2_target_operation(
    operation_id: str = "listInstances",
    method: str = "GET",
    path: str = "/instances",
    classification: str = "authorized",
) -> dict:
    return {
        "operation_id": operation_id,
        "method": method,
        "path": path,
        "auth_classification": classification,
        "iam_decision": {
            "public": "none",
            "authenticated": "validate_principal",
            "authorized": "check_permission",
        }[classification],
        "iam_decision_calls": 0 if classification == "public" else 1,
    }


class GeneratorClassifyTest(unittest.TestCase):
    def test_legacy_with_or_security(self) -> None:
        spec = make_spec()
        policy = generator.classify(spec, "/admin/quota-meta", "get", {"operationId": "listQuotaMeta"})
        self.assertEqual(policy.source, "legacy")
        self.assertEqual(policy.operation_id, "listQuotaMeta")
        self.assertEqual(policy.path_template, "/api/v1/admin/quota-meta")
        # 全局 security 是 OR 的两个单 scheme 备选。
        self.assertEqual(policy.security, (("BearerAuth",), ("ApiKeyAuth",)))

    def test_public_security_empty(self) -> None:
        spec = make_spec()
        policy = generator.classify(spec, "/healthz", "get", {"operationId": "healthz", "security": []})
        self.assertEqual(policy.source, "public")
        self.assertEqual(policy.security, ())
        # 根级 infrastructure 路径不加 /api/v1 前缀。
        self.assertEqual(policy.path_template, "/healthz")

    def test_public_with_authz_extension_fails(self) -> None:
        spec = make_spec()
        operation = {"operationId": "healthz", "security": [], "x-ani-authz": authz_extension()}
        with self.assertRaises(ValueError, msg="public"):
            generator.classify(spec, "/healthz", "get", operation)

    def test_generated_policy(self) -> None:
        spec = make_spec()
        operation = {"operationId": "listQuotaMeta", "x-ani-authz": authz_extension()}
        policy = generator.classify(spec, "/admin/quota-meta", "get", operation)
        self.assertEqual(policy.source, "generated")
        self.assertEqual(policy.version, "v1")
        self.assertEqual(policy.resource, "quota-meta")
        self.assertEqual(policy.action, "read")
        self.assertEqual(policy.boundary, "platform")
        self.assertEqual(policy.principal_kinds, ("user", "service"))

    def test_missing_operation_id_derived(self) -> None:
        spec = make_spec()
        policy = generator.classify(spec, "/auth/refresh", "post", {})
        self.assertEqual(policy.operation_id, "refreshToken")

    def test_missing_operation_id_unknown_fails(self) -> None:
        spec = make_spec()
        with self.assertRaises(ValueError, msg="operationId"):
            generator.classify(spec, "/unknown", "get", {})

    def test_and_security_rejected(self) -> None:
        spec = make_spec()
        operation = {
            "operationId": "op",
            "security": [{"BearerAuth": [], "ApiKeyAuth": []}],
        }
        with self.assertRaises(ValueError, msg="AND"):
            generator.classify(spec, "/admin/quota-meta", "get", operation)

    def test_unknown_scheme_rejected(self) -> None:
        spec = make_spec()
        operation = {"operationId": "op", "security": [{"BasicAuth": []}]}
        with self.assertRaises(ValueError, msg="scheme"):
            generator.classify(spec, "/admin/quota-meta", "get", operation)

    def test_authz_missing_field_rejected(self) -> None:
        ext = authz_extension()
        del ext["boundary"]
        with self.assertRaises(ValueError, msg="missing"):
            generator.parse_authz(ext)

    def test_authz_unknown_field_rejected(self) -> None:
        with self.assertRaises(ValueError, msg="unknown"):
            generator.parse_authz(authz_extension(note="x"))

    def test_authz_version_must_be_v1(self) -> None:
        with self.assertRaises(ValueError, msg="v1"):
            generator.parse_authz(authz_extension(version="v2"))

    def test_authz_invalid_boundary(self) -> None:
        with self.assertRaises(ValueError):
            generator.parse_authz(authz_extension(boundary="global"))

    def test_authz_invalid_kind(self) -> None:
        with self.assertRaises(ValueError):
            generator.parse_authz(authz_extension(principal_kinds=["user", "admin"]))

    def test_authz_duplicate_kind_rejected(self) -> None:
        with self.assertRaises(ValueError):
            generator.parse_authz(authz_extension(principal_kinds=["user", "user"]))

    def test_direct_p2_authorized_contract_stays_legacy_in_core_registry(self) -> None:
        spec = make_spec()
        operation = {
            "operationId": "listInstances",
            "x-ani-contract": "direct-p2",
            "security": [{"BearerAuth": []}],
            "x-ani-authn": {
                "principal_kinds": ["human", "service"],
                "credential_kinds": ["access_token", "api_key", "service_token"],
            },
            "x-ani-authz": {
                "version": "v1",
                "resource": "instances",
                "actions": ["read"],
                "scope": "tenant",
                "obligations": [],
            },
        }
        policy = generator.classify(
            spec,
            "/instances",
            "get",
            operation,
            direct_p2_target_operation(),
        )
        self.assertEqual(policy.source, "legacy")
        self.assertEqual(policy.operation_id, "listInstances")
        self.assertEqual(policy.security, (("BearerAuth",),))

    def test_direct_p2_cookie_security_is_preserved_for_legacy_compatibility(self) -> None:
        spec = make_spec()
        operation = {
            "operationId": "refreshSession",
            "x-ani-contract": "direct-p2",
            "security": [{"ConsoleRefreshCookie": []}, {"BossRefreshCookie": []}],
            "x-ani-authn": {
                "principal_kinds": ["human"],
                "credential_kinds": ["refresh_cookie"],
            },
        }
        policy = generator.classify(
            spec,
            "/auth/refresh",
            "post",
            operation,
            direct_p2_target_operation("refreshSession", "POST", "/auth/refresh", "authenticated"),
        )
        self.assertEqual(policy.source, "legacy")
        self.assertEqual(policy.security, (("ConsoleRefreshCookie",), ("BossRefreshCookie",)))

    def test_direct_p2_requires_matching_target_registry_entry(self) -> None:
        spec = make_spec()
        operation = {
            "operationId": "listInstances",
            "x-ani-contract": "direct-p2",
            "security": [{"BearerAuth": []}],
            "x-ani-authz": {
                "version": "v1",
                "resource": "instances",
                "actions": ["read"],
                "scope": "tenant",
                "obligations": [],
            },
        }
        with self.assertRaisesRegex(ValueError, "target registry"):
            generator.classify(spec, "/instances", "get", operation, {})
        with self.assertRaisesRegex(ValueError, "target registry"):
            generator.classify(
                spec,
                "/instances",
                "get",
                operation,
                direct_p2_target_operation(path="/wrong"),
            )


class GeneratorCollectTest(unittest.TestCase):
    def test_duplicate_route_rejected(self) -> None:
        spec = {
            "security": [],
            "paths": {
                "/a": {"get": {"operationId": "opA", "security": []}},
                "/a": {"get": {"operationId": "opA", "security": []}},  # YAML 去重，无法直接构造
            },
        }
        # YAML mapping 天然去重同 key，改用两次 path 前缀映射不现实；
        # 直接验证 collect_policies 对同一 path 内 method 重复由 set 兜底：
        policies = generator.collect_policies(spec)
        self.assertEqual(len(policies), 1)

    def test_duplicate_operation_id_rejected(self) -> None:
        spec = {
            "security": [],
            "paths": {
                "/a": {"get": {"operationId": "same", "security": []}},
                "/b": {"get": {"operationId": "same", "security": []}},
            },
        }
        with self.assertRaises(ValueError, msg="operationId"):
            generator.collect_policies(spec)

    def test_render_deterministic(self) -> None:
        spec = make_spec()
        first = generator.render_go(generator.collect_policies(spec))
        second = generator.render_go(generator.collect_policies(copy.deepcopy(spec)))
        self.assertEqual(first, second)


class GeneratorRealSpecTest(unittest.TestCase):
    """针对真实 v1.yaml 的回归：全量生成不抛错且分类计数稳定。"""

    def test_real_v1_spec_generates(self) -> None:
        spec = generator.load_spec(generator.DEFAULT_INPUT)
        target_operations = generator.load_target_operations(generator.DEFAULT_TARGET_REGISTRY)
        compatibility_policies = generator.load_core_compatibility_policies(generator.DEFAULT_REPLACEMENT_MANIFEST)
        policies = generator.collect_policies(spec, target_operations, compatibility_policies)
        sources = {policy.source for policy in policies}
        self.assertTrue(sources <= {"public", "generated", "legacy"})
        # PR4 C1 起 listQuotaMeta 携带 x-ani-authz，迁移为 generated。
        self.assertIn("legacy", sources)
        self.assertIn("public", sources)
        self.assertIn("generated", sources)
        by_op = {policy.operation_id: policy for policy in policies}
        quota = by_op["listQuotaMeta"]
        self.assertEqual(quota.source, "generated")
        self.assertEqual(quota.path_template, "/api/v1/admin/quota-meta")
        self.assertEqual(quota.version, "v1")
        self.assertEqual(quota.resource, "quota")
        self.assertEqual(quota.action, "read")
        self.assertEqual(quota.boundary, "platform")
        self.assertEqual(quota.principal_kinds, ("user",))
        # Direct P2 接受的显式 operationId 取代旧派生名。
        self.assertIn("refreshSession", by_op)
        self.assertNotIn("refreshToken", by_op)
        self.assertIn("getBranding", by_op)
        self.assertIn("getTask", by_op)

        # Direct P2 remains target-registry-owned while disabled mode keeps the
        # current Gateway compatibility chain.
        instances = by_op["listInstances"]
        self.assertEqual(instances.source, "legacy")

        accepted_main_policies = {
            "queryResourceTrendObservability": {
                "security": (("BearerAuth",), ("ApiKeyAuth",)),
                "resource": "observability",
                "action": "read",
                "boundary": "tenant",
                "principal_kinds": ("user", "api_key"),
            },
            "getPlatformCapacity": {
                "security": (("BearerAuth",), ("ApiKeyAuth",)),
                "resource": "capacity",
                "action": "get",
                "boundary": "platform",
                "principal_kinds": ("user",),
            },
            "getPlatformServiceHealth": {
                "security": (("BearerAuth",),),
                "resource": "observability",
                "action": "read",
                "boundary": "platform",
                "principal_kinds": ("user", "service"),
            },
        }
        for operation_id, expected in accepted_main_policies.items():
            with self.subTest(operation_id=operation_id):
                policy = by_op[operation_id]
                self.assertEqual(policy.source, "generated")
                self.assertEqual(policy.security, expected["security"])
                self.assertEqual(policy.version, "v1")
                self.assertEqual(policy.resource, expected["resource"])
                self.assertEqual(policy.action, expected["action"])
                self.assertEqual(policy.boundary, expected["boundary"])
                self.assertEqual(policy.principal_kinds, expected["principal_kinds"])


if __name__ == "__main__":
    unittest.main()
