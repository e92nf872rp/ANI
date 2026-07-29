#!/usr/bin/env python3
"""Tests for repository-owned OpenAPI validation."""

from __future__ import annotations

import tempfile
import unittest
from pathlib import Path
from unittest.mock import patch

import yaml

import validate_openapi_spec as validator

ROOT = Path(__file__).resolve().parents[1]


class OpenAPISpecValidatorTest(unittest.TestCase):
    def test_default_specs_are_the_core_and_services_contracts(self) -> None:
        self.assertEqual(
            validator.DEFAULT_SPECS,
            (
                Path("api/openapi/v1.yaml"),
                Path("api/openapi/services/v1.yaml"),
            ),
        )

    @patch("validate_openapi_spec.subprocess.run")
    def test_validate_spec_invokes_python_module_validator(self, run) -> None:
        with tempfile.TemporaryDirectory() as directory:
            path = Path(directory) / "spec.yaml"
            path.write_text("openapi: 3.0.0\n", encoding="utf-8")
            validator.validate_spec(path)
        run.assert_called_once_with(
            [validator.sys.executable, "-m", "openapi_spec_validator", str(path)],
            check=True,
        )

    def test_missing_spec_fails_before_invoking_validator(self) -> None:
        with self.assertRaises(FileNotFoundError):
            validator.validate_spec(Path("/tmp/ani-missing-openapi.yaml"))

    def test_registry_console_flow_contract_is_frozen(self) -> None:
        spec = yaml.safe_load((ROOT / "api/openapi/v1.yaml").read_text(encoding="utf-8"))
        schemas = spec["components"]["schemas"]

        registry_image = schemas["RegistryImage"]
        self.assertEqual(
            registry_image["properties"]["purpose"]["enum"],
            ["container", "gpu", "sandbox", "system"],
        )

        image_filters = {
            param["name"]: param
            for param in spec["paths"]["/registry/images"]["get"]["parameters"]
        }
        self.assertEqual(
            image_filters["purpose"]["schema"]["enum"],
            ["container", "gpu", "sandbox", "system"],
        )

        reference_kind = schemas["RegistryImageReference"]["properties"]["kind"]
        self.assertEqual(
            reference_kind["enum"],
            ["vm_instance", "container_instance", "gpu_container_instance", "sandbox_instance"],
        )

        create_instance_422 = spec["paths"]["/instances"]["post"]["responses"]["422"]["description"]
        for code in (
            "ImageNotFound",
            "ImageScanning",
            "ImageVulnerabilityBlocked",
            "ImagePurposeMismatch",
        ):
            self.assertIn(code, create_instance_422)

    def test_gpu_spec_selection_contract_is_frozen_without_quota_semantics(self) -> None:
        spec = yaml.safe_load((ROOT / "api/openapi/v1.yaml").read_text(encoding="utf-8"))
        schemas = spec["components"]["schemas"]

        self.assertIn("GPUSpecSummary", schemas)
        gpu_spec = schemas["GPUSpecSummary"]
        self.assertEqual(
            set(gpu_spec["required"]),
            {"id", "name", "gpu_type", "shares", "mb_per_share", "available"},
        )
        self.assertNotIn("quota", gpu_spec["properties"])
        self.assertNotIn("used_count", gpu_spec["properties"])

        list_operation = spec["paths"]["/gpu-specs"]["get"]
        self.assertEqual(list_operation["operationId"], "listGPUSpecs")
        self.assertEqual(
            list_operation["responses"]["200"]["content"]["application/json"]["schema"]["$ref"],
            "#/components/schemas/GPUSpecListResponse",
        )

        get_operation = spec["paths"]["/gpu-specs/{spec_id}"]["get"]
        self.assertEqual(get_operation["operationId"], "getGPUSpec")
        self.assertIn("404", get_operation["responses"])
        self.assertNotIn("422", get_operation["responses"])

        create_instance_422 = spec["paths"]["/instances"]["post"]["responses"]["422"]["description"]
        for code in ("GPUSpecNotFound", "GPUSpecUnavailable", "GPUSpecInventoryMismatch"):
            self.assertIn(code, create_instance_422)

        gpu_config = schemas["CreateGPUContainerInstanceConfig"]["properties"]["gpu"]
        self.assertIn("spec_id", gpu_config["properties"])
        self.assertTrue(gpu_config["properties"]["vendor"]["deprecated"])
        self.assertTrue(gpu_config["properties"]["model"]["deprecated"])
        self.assertTrue(gpu_config["properties"]["count"]["deprecated"])
        self.assertTrue(gpu_config["properties"]["allocation_mode"]["deprecated"])

    def test_instance_management_create_and_detail_contract_is_frozen(self) -> None:
        spec = yaml.safe_load((ROOT / "api/openapi/v1.yaml").read_text(encoding="utf-8"))
        schemas = spec["components"]["schemas"]

        create_properties = schemas["CreateInstanceRequest"]["properties"]
        for field in ("description", "labels", "image_id", "image_ref"):
            self.assertIn(field, create_properties)

        for schema_name in (
            "InstanceNetworkConfig",
            "InstanceDiskSpec",
            "InstanceVolumeMount",
            "InstanceFilesystemMount",
            "InstancePortSpec",
            "InstanceEnvVar",
            "InstanceImageSummary",
            "InstanceComputeSummary",
            "InstanceNetworkSummary",
            "InstanceAccessSummary",
            "InstanceStorageAttachment",
        ):
            self.assertIn(schema_name, schemas)

        instance_properties = schemas["InstanceRecord"]["properties"]
        for field in (
            "description",
            "labels",
            "image",
            "compute",
            "network",
            "access",
            "storage_attachments",
        ):
            self.assertIn(field, instance_properties)

        for config_name in (
            "CreateVMInstanceConfig",
            "CreateContainerInstanceConfig",
            "CreateGPUContainerInstanceConfig",
        ):
            self.assertIn("network", schemas[config_name]["properties"])

        for field in (
            "template_id",
            "idle_timeout",
            "on_timeout",
            "egress_allowlist",
            "env",
            "initial_ports",
        ):
            self.assertIn(field, schemas["SandboxConfig"]["properties"])

    def test_instance_management_list_and_observation_pagination_is_frozen(self) -> None:
        spec = yaml.safe_load((ROOT / "api/openapi/v1.yaml").read_text(encoding="utf-8"))

        list_parameters = {
            parameter["name"]: parameter
            for parameter in spec["paths"]["/instances"]["get"]["parameters"]
        }
        for parameter in (
            "kind",
            "state",
            "keyword",
            "created_after",
            "created_before",
            "spec_id",
            "image_id",
            "node_name",
            "rollout_status",
            "gpu_model",
            "queue_name",
            "scheduling_state",
            "template_id",
            "session_state",
            "limit",
            "cursor",
            "sort",
        ):
            self.assertIn(parameter, list_parameters)

        for path in (
            "/instances/{instance_id}/events",
            "/instances/{instance_id}/security-events",
        ):
            parameters = {
                parameter["name"]: parameter
                for parameter in spec["paths"][path]["get"]["parameters"]
            }
            self.assertIn("cursor", parameters)

    def test_instance_management_lifecycle_contract_is_frozen(self) -> None:
        spec = yaml.safe_load((ROOT / "api/openapi/v1.yaml").read_text(encoding="utf-8"))
        schemas = spec["components"]["schemas"]

        lifecycle = schemas["InstanceLifecycleRequest"]
        lifecycle_actions = set(lifecycle["properties"]["action"]["enum"])
        for action in (
            "attach_filesystem",
            "detach_filesystem",
            "scale",
            "update_image",
            "bind_secret",
            "unbind_secret",
            "change_security_groups",
            "set_termination_protection",
            "pause",
            "resume",
            "extend",
            "touch_idle",
        ):
            self.assertIn(action, lifecycle_actions)

        for field in (
            "snapshot_id",
            "mount_path",
            "read_only",
            "filesystem_id",
            "replicas",
            "image_id",
            "strategy",
            "secret_id",
            "binding_type",
            "env_name",
            "security_group_ids",
            "enabled",
            "duration",
        ):
            self.assertIn(field, lifecycle["properties"])

        operation_actions = set(schemas["InstanceOperation"]["properties"]["operation"]["enum"])
        self.assertTrue(lifecycle_actions.issubset(operation_actions))

        operation_step = schemas["InstanceOperation"]["properties"]["steps"]["items"]
        for field in ("task_id", "resource_type", "resource_id"):
            self.assertIn(field, operation_step["properties"])

    def test_instance_async_task_center_contract_is_frozen(self) -> None:
        spec = yaml.safe_load((ROOT / "api/openapi/v1.yaml").read_text(encoding="utf-8"))
        paths = spec["paths"]
        schemas = spec["components"]["schemas"]

        async_task = schemas["AsyncTask"]
        task_types = set(async_task["properties"]["task_type"]["enum"])
        for task_type in (
            "instance.create",
            "instance.start",
            "instance.stop",
            "instance.restart",
            "instance.resize",
            "instance.rebuild",
            "instance.delete",
            "instance.snapshot.create",
            "instance.rollback",
            "instance.scale",
            "instance.image.update",
            "instance.pause",
            "instance.resume",
            "instance.extend",
            "sandbox.checkpoint.clone",
        ):
            self.assertIn(task_type, task_types)

        self.assertIn("instance", async_task["properties"]["resource_type"]["enum"])
        for field in ("resource_name", "instance_id", "operation_id", "started_at"):
            self.assertIn(field, async_task["properties"])

        instance_async_task = schemas["InstanceAsyncTask"]["allOf"][1]
        self.assertEqual(
            set(instance_async_task["required"]),
            {"instance_id", "operation_id"},
        )

        list_tasks = paths["/tasks"]["get"]
        self.assertEqual(list_tasks["operationId"], "listTasks")
        self.assertEqual(
            list_tasks["responses"]["200"]["content"]["application/json"]["schema"]["$ref"],
            "#/components/schemas/AsyncTaskListResponse",
        )
        list_parameters = {parameter["name"] for parameter in list_tasks["parameters"]}
        for parameter in (
            "status",
            "task_type",
            "resource_type",
            "instance_id",
            "created_after",
            "created_before",
            "limit",
            "cursor",
            "sort",
        ):
            self.assertIn(parameter, list_parameters)

        cancel_task = paths["/tasks/{task_id}/cancel"]["post"]
        self.assertEqual(cancel_task["operationId"], "cancelTask")
        self.assertEqual(
            cancel_task["requestBody"]["content"]["application/json"]["schema"]["$ref"],
            "#/components/schemas/CancelAsyncTaskRequest",
        )
        self.assertEqual(
            cancel_task["responses"]["200"]["content"]["application/json"]["schema"]["$ref"],
            "#/components/schemas/AsyncTask",
        )
        self.assertIn("409", cancel_task["responses"])

        for path, success_code in (
            ("/instances", "202"),
            ("/instances/{instance_id}/lifecycle", "202"),
            ("/instances/{instance_id}/sandbox/checkpoints/{checkpoint_id}/clone", "202"),
        ):
            response = paths[path]["post"]["responses"][success_code]
            self.assertEqual(
                response["content"]["application/json"]["schema"]["$ref"],
                "#/components/schemas/InstanceAsyncTask",
            )
            self.assertIn("Location", response["headers"])

    def test_sandbox_subresource_contract_is_frozen(self) -> None:
        spec = yaml.safe_load((ROOT / "api/openapi/v1.yaml").read_text(encoding="utf-8"))
        paths = spec["paths"]
        schemas = spec["components"]["schemas"]

        operations = {
            ("/instances/{instance_id}/sandbox/tokens", "post"): "createSandboxToken",
            ("/instances/{instance_id}/sandbox/ports", "post"): "createSandboxPort",
            ("/instances/{instance_id}/sandbox/ports/{port}", "delete"): "deleteSandboxPort",
            ("/instances/{instance_id}/sandbox/files", "get"): "listSandboxFiles",
            ("/instances/{instance_id}/sandbox/files", "post"): "writeSandboxFile",
            ("/instances/{instance_id}/sandbox/files", "delete"): "deleteSandboxFile",
            ("/instances/{instance_id}/sandbox/checkpoints", "get"): "listSandboxCheckpoints",
            ("/instances/{instance_id}/sandbox/checkpoints", "post"): "createSandboxCheckpoint",
            (
                "/instances/{instance_id}/sandbox/checkpoints/{checkpoint_id}/restore",
                "post",
            ): "restoreSandboxCheckpoint",
            (
                "/instances/{instance_id}/sandbox/checkpoints/{checkpoint_id}/clone",
                "post",
            ): "cloneSandboxCheckpoint",
            ("/instances/{instance_id}/sandbox/code-runs", "post"): "createSandboxCodeRun",
        }
        for (path, method), operation_id in operations.items():
            self.assertIn(path, paths)
            self.assertIn(method, paths[path])
            self.assertEqual(paths[path][method]["operationId"], operation_id)

        for schema_name in (
            "CreateSandboxTokenRequest",
            "SandboxTokenResponse",
            "CreateSandboxPortRequest",
            "SandboxPort",
            "SandboxFile",
            "SandboxFileListResponse",
            "WriteSandboxFileRequest",
            "CreateSandboxCheckpointRequest",
            "SandboxCheckpoint",
            "SandboxCheckpointListResponse",
            "SandboxCheckpointActionRequest",
            "CloneSandboxCheckpointRequest",
            "CreateSandboxCodeRunRequest",
            "SandboxCodeRun",
        ):
            self.assertIn(schema_name, schemas)

        for path, method in (
            ("/instances/{instance_id}/sandbox/ports/{port}", "delete"),
            ("/instances/{instance_id}/sandbox/files", "delete"),
        ):
            parameters = {
                parameter["name"]: parameter
                for parameter in paths[path][method]["parameters"]
            }
            self.assertEqual(parameters["Idempotency-Key"]["in"], "header")
            self.assertTrue(parameters["Idempotency-Key"]["required"])

        code_run_response = paths["/instances/{instance_id}/sandbox/code-runs"]["post"][
            "responses"
        ]["202"]
        self.assertEqual(
            code_run_response["content"]["application/json"]["schema"]["$ref"],
            "#/components/schemas/AsyncTask",
        )
        self.assertIn("Location", code_run_response["headers"])

        task_type = schemas["AsyncTask"]["properties"]["task_type"]["enum"]
        resource_type = schemas["AsyncTask"]["properties"]["resource_type"]["enum"]
        self.assertIn("sandbox.code_run.create", task_type)
        self.assertIn("sandbox_code_run", resource_type)


if __name__ == "__main__":
    unittest.main()
