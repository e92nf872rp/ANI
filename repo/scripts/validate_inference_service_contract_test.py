#!/usr/bin/env python3
"""Contract tests for the approved InferenceService Stage B surface."""

from __future__ import annotations

import unittest
import copy
from pathlib import Path

import yaml

import gen_sdk_alpha
import validate_inference_service_contract as validator
import validate_sdk_alpha


ROOT = Path(__file__).resolve().parents[1]
SPEC_PATH = ROOT / "api/openapi/services/v1.yaml"


def load_spec() -> dict:
    with SPEC_PATH.open(encoding="utf-8") as handle:
        return yaml.safe_load(handle)


class InferenceServiceContractTest(unittest.TestCase):
    @classmethod
    def setUpClass(cls) -> None:
        cls.spec = load_spec()

    def test_stage_b_operations_are_declared(self) -> None:
        paths = self.spec["paths"]
        expected = {
            ("/inference-services/{service_id}", "patch"),
            ("/inference-services/{service_id}/lifecycle", "post"),
            ("/inference-operations/{operation_id}", "get"),
        }
        missing = sorted(
            f"{method.upper()} {path}"
            for path, method in expected
            if method not in paths.get(path, {})
        )
        self.assertEqual(missing, [], f"missing Stage B operations: {missing}")

    def test_current_contract_satisfies_stage_b_semantics(self) -> None:
        self.assertEqual(validator.validate(copy.deepcopy(self.spec)), ())

    def test_missing_security_is_rejected(self) -> None:
        spec = copy.deepcopy(self.spec)
        spec["paths"]["/inference-services/{service_id}/lifecycle"]["post"].pop("security")
        errors = validator.validate(spec)
        self.assertIn("POST /inference-services/{service_id}/lifecycle must require authentication", errors)

    def test_internal_runtime_fields_are_rejected(self) -> None:
        spec = copy.deepcopy(self.spec)
        spec["components"]["schemas"]["InferenceService"]["properties"]["runtime_endpoint"] = {
            "type": "string"
        }
        errors = validator.validate(spec)
        self.assertIn("InferenceService must not expose runtime_endpoint", errors)

    def test_policy_501_semantics_are_required(self) -> None:
        spec = copy.deepcopy(self.spec)
        spec["paths"]["/inference-services/{service_id}/policies"]["put"]["responses"].pop("501")
        errors = validator.validate(spec)
        self.assertIn("updateInferenceServicePolicies must declare 501 FEATURE_NOT_AVAILABLE", errors)

    def test_runtime_test_declares_stable_failure_responses(self) -> None:
        responses = self.spec["paths"]["/inference-services/{service_id}/test"]["post"]["responses"]
        missing = sorted({"422", "502", "503", "504"} - set(responses))
        self.assertEqual(missing, [], f"testInferenceService missing responses: {missing}")

    def test_sdk_metadata_contains_stable_inference_error_codes(self) -> None:
        expected = {
            "INVALID_ARGUMENT",
            "NAME_CONFLICT",
            "IDEMPOTENCY_CONFLICT",
            "OPERATION_IN_PROGRESS",
            "MODEL_NOT_READY",
            "MODEL_INCOMPATIBLE",
            "ACCELERATOR_SPEC_UNAVAILABLE",
            "INSUFFICIENT_CAPACITY",
            "UNSUPPORTED_TOPOLOGY",
            "INVALID_STATE_TRANSITION",
            "IMAGE_UNAVAILABLE",
            "FEATURE_NOT_AVAILABLE",
            "RUNTIME_ERROR",
            "DEPENDENCY_UNAVAILABLE",
            "RUNTIME_TIMEOUT",
        }
        actual = set(gen_sdk_alpha.collect_error_codes(self.spec))
        self.assertEqual(sorted(expected - actual), [], "stable inference error codes missing from SDK metadata")

    def test_sdk_generator_and_validator_collect_the_same_error_codes(self) -> None:
        self.assertEqual(
            validate_sdk_alpha.collect_error_codes(self.spec),
            gen_sdk_alpha.collect_error_codes(self.spec),
        )

    def test_create_accepts_registry_or_manual_image(self) -> None:
        schema = self.spec["components"]["schemas"]["CreateInferenceServiceRequest"]
        self.assertEqual(set(schema["required"]), {"idempotency_key", "name", "model"})
        for field in ("image_id", "image_ref"):
            self.assertEqual(schema["properties"][field]["type"], "string")
            self.assertEqual(schema["properties"][field]["minLength"], 1)
            self.assertNotIn(field, schema["required"])
        resource = self.spec["components"]["schemas"]["InferenceService"]["properties"]
        self.assertIn("image_id", resource)
        self.assertIn("image_ref", resource)

    def test_legacy_optional_create_fields_remain_additive(self) -> None:
        properties = self.spec["components"]["schemas"]["CreateInferenceServiceRequest"]["properties"]
        for field in ("name", "model"):
            self.assertNotIn("minLength", properties[field], f"{field} must not tighten the existing v1 input")
        for field in ("replicas", "gpu_count_per_pod", "max_concurrency"):
            self.assertNotIn("minimum", properties[field], f"{field} must not tighten the existing v1 input")
        for field in ("replicas", "placement_mode", "gpu_count_per_pod", "max_concurrency", "image_id", "image_ref", "engine"):
            self.assertNotIn("default", properties[field], f"{field} must remain optional in generated clients")

    def test_generated_types_keep_legacy_create_fields_optional(self) -> None:
        generated = (ROOT / "frontends/console/src/api/schema.d.ts").read_text(encoding="utf-8")
        block = generated.split("CreateInferenceServiceRequest: {", 1)[1].split("\n        };", 1)[0]
        for field in ("replicas", "placement_mode", "gpu_count_per_pod", "max_concurrency", "image_id", "image_ref", "engine"):
            self.assertIn(f"{field}?:", block)

    def test_create_freezes_optional_engine_env_and_command(self) -> None:
        schemas = self.spec["components"]["schemas"]
        self.assertEqual(
            schemas["CreateInferenceServiceRequest"]["properties"]["engine"]["$ref"],
            "#/components/schemas/InferenceServiceEngine",
        )
        self.assertEqual(
            schemas["InferenceService"]["properties"]["engine"]["$ref"],
            "#/components/schemas/InferenceServiceEngine",
        )
        engine = schemas["InferenceServiceEngine"]
        self.assertFalse(engine["additionalProperties"])
        self.assertNotIn("extra_args", engine["properties"])
        self.assertNotIn("args", engine["properties"])
        self.assertNotIn("InferenceServiceEngineArg", schemas)
        self.assertNotIn("x-ani-reserved-engine-arg-names", engine)
        self.assertEqual(
            engine["x-ani-reserved-engine-env-names"],
            [
                "CUDA_VISIBLE_DEVICES",
                "NVIDIA_VISIBLE_DEVICES",
                "NVIDIA_DRIVER_CAPABILITIES",
                "PYTHONPATH",
                "PATH",
                "LD_PRELOAD",
                "LD_LIBRARY_PATH",
                "RAY_EXPERIMENTAL_NOSET_CUDA_VISIBLE_DEVICES",
            ],
        )
        env = engine["properties"]["env"]
        self.assertEqual(env["maxItems"], 32)
        env_var = schemas["InferenceServiceEngineEnvVar"]
        self.assertEqual(env_var["required"], ["name", "value"])
        self.assertFalse(env_var["additionalProperties"])
        self.assertEqual(env_var["properties"]["name"]["pattern"], "^[A-Za-z_][A-Za-z0-9_]*$")
        command = engine["properties"]["command"]
        self.assertEqual(command["minItems"], 1)
        self.assertEqual(command["maxItems"], 64)
        self.assertEqual(command["items"]["type"], "string")
        self.assertEqual(command["items"]["minLength"], 1)
        self.assertEqual(
            set(schemas["UpdateInferenceServiceRequest"]["properties"]),
            {"idempotency_key", "replicas"},
        )

    def test_authenticated_reads_declare_auth_failures(self) -> None:
        paths = self.spec["paths"]
        for path, method in (
            ("/inference-services", "get"),
            ("/inference-services/{service_id}", "get"),
        ):
            responses = paths[path][method]["responses"]
            self.assertEqual(sorted({"401", "403"} - set(responses)), [])

    def test_operations_freeze_inference_task_semantics(self) -> None:
        paths = self.spec["paths"]
        expected = {
            ("/inference-services", "post"): ["inference_service.create"],
            ("/inference-services/{service_id}", "patch"): ["inference_service.scale"],
            ("/inference-services/{service_id}", "delete"): ["inference_service.delete"],
            ("/inference-services/{service_id}/lifecycle", "post"): [
                "inference_service.start",
                "inference_service.stop",
                "inference_service.restart",
            ],
            ("/inference-operations/{operation_id}", "get"): [
                "inference_service.create",
                "inference_service.scale",
                "inference_service.start",
                "inference_service.stop",
                "inference_service.restart",
                "inference_service.delete",
            ],
        }
        for (path, method), task_types in expected.items():
            operation = paths[path][method]
            self.assertEqual(operation.get("x-ani-async-task-types"), task_types)
            self.assertEqual(operation.get("x-ani-async-resource-type"), "inference_service")

    def test_handler_status_mismatches_are_exactly_baselined(self) -> None:
        source = validator.HANDLER_PATH.read_text(encoding="utf-8")
        warnings, errors = validator.validate_handler_statuses(
            self.spec,
            source,
            validator.load_handler_baseline(),
        )
        self.assertEqual(errors, ())
        self.assertEqual(len(warnings), 3)

    def test_resolved_handler_status_makes_baseline_stale(self) -> None:
        source = validator.HANDLER_PATH.read_text(encoding="utf-8")
        old_body = validator.function_body(source, "createInferenceService")
        new_body = old_body.replace("http.StatusOK", "http.StatusAccepted")
        source = source.replace(old_body, new_body)
        _, errors = validator.validate_handler_statuses(
            self.spec,
            source,
            validator.load_handler_baseline(),
        )
        self.assertIn("stale inference handler baseline: createInferenceService", errors)


if __name__ == "__main__":
    unittest.main()
