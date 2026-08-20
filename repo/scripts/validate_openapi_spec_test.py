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

    def test_registry_p0_scan_reference_and_delete_contract_is_frozen(self) -> None:
        spec = yaml.safe_load((ROOT / "api/openapi/v1.yaml").read_text(encoding="utf-8"))
        schemas = spec["components"]["schemas"]

        scan_result = schemas["RegistryScanResult"]
        self.assertEqual(
            set(scan_result["required"]),
            {"image", "status", "critical", "high", "medium", "low"},
        )
        self.assertEqual(
            scan_result["properties"]["status"]["enum"],
            ["not_scanned", "pending", "running", "complete", "failed"],
        )

        registry_image = schemas["RegistryImage"]
        self.assertEqual(
            registry_image["properties"]["scan_status"]["$ref"],
            "#/components/schemas/RegistryScanResult",
        )

        scan_report = spec["paths"]["/registry/projects/{project}/scan-report"]["get"]
        self.assertEqual(scan_report["operationId"], "getRegistryProjectScanReport")
        self.assertEqual(
            scan_report["responses"]["200"]["content"]["application/json"]["schema"]["$ref"],
            "#/components/schemas/RegistryProjectScanReport",
        )

        scan_result_operation = spec["paths"]["/registry/images/scan-result"]["get"]
        scan_result_parameters = {
            parameter["name"]: parameter
            for parameter in scan_result_operation["parameters"]
        }
        self.assertTrue(scan_result_parameters["image"]["required"])
        self.assertEqual(
            scan_result_operation["responses"]["200"]["content"]["application/json"]["schema"]["$ref"],
            "#/components/schemas/RegistryScanResult",
        )

        references = spec["paths"][
            "/registry/projects/{project}/repositories/{repository}/tags/{tag}/references"
        ]["get"]
        self.assertEqual(references["operationId"], "listRegistryRepositoryTagReferences")
        self.assertEqual(
            references["responses"]["200"]["content"]["application/json"]["schema"]["$ref"],
            "#/components/schemas/RegistryImageReferenceListResponse",
        )

        delete_tag = spec["paths"][
            "/registry/projects/{project}/repositories/{repository}/tags/{tag}"
        ]["delete"]
        self.assertEqual(delete_tag["operationId"], "deleteRegistryRepositoryTag")
        self.assertIn("409", delete_tag["responses"])
        self.assertEqual(
            delete_tag["responses"]["200"]["content"]["application/json"]["schema"]["$ref"],
            "#/components/schemas/RegistryDeletedTag",
        )

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

    def test_vector_document_insert_async_contract_is_pollable(self) -> None:
        spec = yaml.safe_load((ROOT / "api/openapi/v1.yaml").read_text(encoding="utf-8"))
        schemas = spec["components"]["schemas"]

        accepted = spec["paths"]["/vector-stores/{vector_store_id}/documents"]["post"][
            "responses"
        ]["202"]
        self.assertEqual(
            accepted["content"]["application/json"]["schema"]["$ref"],
            "#/components/schemas/VectorStoreDocumentInsertResponse",
        )
        self.assertIn("Location", accepted.get("headers", {}))
        self.assertIn(
            "vector_store.document.insert",
            schemas["AsyncTask"]["properties"]["task_type"]["enum"],
        )

    def test_platform_workload_service_contract_is_frozen(self) -> None:
        spec = yaml.safe_load((ROOT / "api/openapi/v1.yaml").read_text(encoding="utf-8"))
        schemas = spec["components"]["schemas"]
        paths = spec["paths"]

        expected_operations = {
            ("/platform-workload-capabilities", "get"): (
                "getPlatformWorkloadCapabilities",
                "scope:platform-workloads:read",
            ),
            ("/platform-workloads", "post"): (
                "createPlatformWorkload",
                "scope:platform-workloads:write",
            ),
            ("/platform-workloads/{workload_id}", "get"): (
                "getPlatformWorkload",
                "scope:platform-workloads:read",
            ),
            ("/platform-workloads/{workload_id}", "patch"): (
                "updatePlatformWorkload",
                "scope:platform-workloads:write",
            ),
            ("/platform-workloads/{workload_id}", "delete"): (
                "deletePlatformWorkload",
                "scope:platform-workloads:write",
            ),
            ("/platform-workloads/{workload_id}/lifecycle", "post"): (
                "applyPlatformWorkloadLifecycle",
                "scope:platform-workloads:write",
            ),
            ("/platform-workloads/{workload_id}/logs", "get"): (
                "getPlatformWorkloadLogs",
                "scope:platform-workloads:read",
            ),
        }
        for (path, method), (operation_id, scope) in expected_operations.items():
            with self.subTest(path=path, method=method):
                operation = paths[path][method]
                self.assertEqual(operation["operationId"], operation_id)
                self.assertEqual(operation["x-ani-rbac-scope"], scope)
                self.assertTrue(operation["x-ani-service-only"])
                self.assertEqual(operation["x-ani-principal-kind"], "service")
                self.assertEqual(operation["x-ani-exposure"], "internal")
                self.assertEqual(operation["security"], [{"BearerAuth": []}])

        for schema_name in (
            "PlatformWorkloadAcceleratorCapability",
            "PlatformWorkloadCapabilities",
            "PlatformWorkloadAcceleratorResources",
            "PlatformWorkloadResources",
            "PlatformWorkloadRole",
            "PlatformWorkloadTopology",
            "PlatformWorkloadScheduling",
            "PlatformWorkloadNetwork",
            "PlatformWorkloadArtifact",
            "PlatformWorkloadSecretBinding",
            "PlatformWorkloadHealthCheck",
            "PlatformWorkloadMetadata",
            "PlatformWorkloadCreateRequest",
            "PlatformWorkloadUpdateRequest",
            "PlatformWorkloadLifecycleRequest",
            "PlatformWorkload",
            "PlatformWorkloadLogEntry",
            "PlatformWorkloadLogListResponse",
        ):
            self.assertIn(schema_name, schemas)

        def assert_property_descriptions(node: dict, path: str) -> None:
            for property_name, property_schema in node.get("properties", {}).items():
                property_path = f"{path}.{property_name}"
                with self.subTest(property=property_path):
                    self.assertIsInstance(property_schema, dict)
                    self.assertTrue(
                        str(property_schema.get("description", "")).strip(),
                        f"{property_path} must define a non-empty description",
                    )
                assert_property_descriptions(property_schema, property_path)
                items = property_schema.get("items")
                if isinstance(items, dict):
                    assert_property_descriptions(items, f"{property_path}.items")

        for schema_name, schema in schemas.items():
            if not schema_name.startswith("PlatformWorkload"):
                continue
            with self.subTest(schema=schema_name):
                self.assertTrue(
                    str(schema.get("description", "")).strip(),
                    f"{schema_name} must define a non-empty description",
                )
            assert_property_descriptions(schema, schema_name)

        create = schemas["PlatformWorkloadCreateRequest"]
        self.assertEqual(
            set(create["required"]),
            {
                "idempotency_key",
                "name",
                "workload_class",
                "runtime_kind",
                "image_ref",
                "command",
                "replicas",
                "resources",
                "topology",
                "scheduling",
                "network",
                "health_check",
                "metadata",
            },
        )
        accelerator = schemas["PlatformWorkloadAcceleratorResources"]
        self.assertEqual(set(accelerator["required"]), {"spec_id", "count"})
        self.assertEqual(accelerator["properties"]["count"]["minimum"], 1)
        self.assertNotIn("memory", accelerator["required"])
        self.assertEqual(accelerator["properties"]["memory"]["type"], "integer")
        self.assertEqual(accelerator["properties"]["memory"]["minimum"], 1)
        self.assertNotIn("gpu_mode", accelerator["properties"])
        capability = schemas["PlatformWorkloadAcceleratorCapability"]
        self.assertEqual(
            set(capability["required"]),
            {"spec_id", "available", "max_single_node_count"},
        )
        for field in (
            "whole_card_available",
            "vgpu_available",
            "memory_min_mb",
            "memory_max_mb",
            "memory_step_mb",
            "gpu_mode",
        ):
            self.assertNotIn(field, capability["properties"])

        cpu_example = create["example"]
        self.assertNotIn("accelerator", cpu_example["resources"])
        self.assertEqual(cpu_example["topology"]["mode"], "single_node")
        self.assertEqual(cpu_example["network"]["exposure"], "cluster_internal")
        self.assertIn("@sha256:", cpu_example["image_ref"])

        topology = schemas["PlatformWorkloadTopology"]
        self.assertEqual(topology["properties"]["mode"]["enum"], ["single_node", "leader_worker"])
        self.assertIn("leader", topology["properties"])
        self.assertIn("workers", topology["properties"])
        self.assertEqual(len(topology["allOf"]), 2)
        self.assertEqual(len(create["allOf"]), 2)
        self.assertEqual(
            create["properties"]["image_ref"]["pattern"],
            r"^.+@sha256:[a-f0-9]{64}$",
        )

        workload = schemas["PlatformWorkload"]
        self.assertEqual(
            workload["properties"]["runtime_shape"]["enum"],
            ["deployment", "leader_worker_set"],
        )
        self.assertIn("service identity", workload["properties"]["internal_endpoint"]["description"])

        task_types = schemas["AsyncTask"]["properties"]["task_type"]["enum"]
        for task_type in (
            "platform_workload.create",
            "platform_workload.scale",
            "platform_workload.start",
            "platform_workload.stop",
            "platform_workload.restart",
            "platform_workload.delete",
        ):
            self.assertIn(task_type, task_types)
        self.assertIn(
            "platform_workload",
            schemas["AsyncTask"]["properties"]["resource_type"]["enum"],
        )

        for path, method in (
            ("/platform-workloads", "post"),
            ("/platform-workloads/{workload_id}", "patch"),
            ("/platform-workloads/{workload_id}", "delete"),
            ("/platform-workloads/{workload_id}/lifecycle", "post"),
        ):
            accepted = paths[path][method]["responses"]["202"]
            self.assertEqual(
                accepted["content"]["application/json"]["schema"]["$ref"],
                "#/components/schemas/AsyncTask",
            )
            self.assertIn("Location", accepted["headers"])

    def test_storage_p0_keeps_existing_v1_without_contract_changes(self) -> None:
        """STORAGE-CONTROL-PLANE-STATE-A / B1: reuse current Core v1; no additive fields."""
        spec = yaml.safe_load((ROOT / "api/openapi/v1.yaml").read_text(encoding="utf-8"))
        schemas = spec["components"]["schemas"]

        # P0 control-plane persistence must not invent VectorStore.description or similar.
        self.assertNotIn("description", schemas["CreateVectorStoreRequest"]["properties"])
        self.assertNotIn("description", schemas["VectorStore"]["properties"])

        # Text-to-vector stays out of Core; search continues to accept vector only.
        search_props = schemas["VectorStoreSearchRequest"]["properties"]
        self.assertIn("vector", search_props)
        self.assertNotIn("text", search_props)
        self.assertNotIn("query", search_props)
        self.assertEqual(schemas["VectorStoreSearchRequest"]["required"], ["vector"])

        # Filesystem NFS client/CIDR ACL and SMB remain out of P0 contract surface.
        for schema_name in (
            "CreateStorageFilesystemRequest",
            "StorageFilesystem",
            "CreateStorageVolumeRequest",
            "StorageVolume",
        ):
            props = schemas[schema_name]["properties"]
            for forbidden in (
                "client_cidrs",
                "nfs_acl",
                "smb_enabled",
                "acl_rules",
                "static_website",
            ):
                self.assertNotIn(forbidden, props, msg=f"{schema_name}.{forbidden}")

        # Existing storage/vector resource surfaces required by P0 remain present.
        for schema_name in (
            "StorageVolume",
            "VolumeSnapshotRecord",
            "StorageFilesystem",
            "FilesystemMountTarget",
            "StorageBucketRecord",
            "StorageObject",
            "VectorStore",
            "VectorStoreKnowledgeBaseRef",
        ):
            self.assertIn(schema_name, schemas)

    def test_network_p0_contract_adds_non_lb_fields(self) -> None:
        """NETWORK-P0-CONTRACT-A / C1: additive VPC, subnet, SG, and route fields."""
        spec = yaml.safe_load((ROOT / "api/openapi/v1.yaml").read_text(encoding="utf-8"))
        schemas = spec["components"]["schemas"]
        paths = spec["paths"]

        self.assertIn("description", schemas["CreateNetworkVPCRequest"]["properties"])
        self.assertIn("description", schemas["NetworkVPC"]["properties"])
        self.assertIn("subnet_count", schemas["NetworkVPC"]["properties"])
        self.assertNotIn("subnet_count", schemas["CreateNetworkVPCRequest"]["properties"])

        self.assertIn("zone", schemas["CreateNetworkSubnetRequest"]["properties"])
        self.assertIn("zone", schemas["NetworkSubnet"]["properties"])
        self.assertIn("available_ip_count", schemas["NetworkSubnet"]["properties"])
        self.assertNotIn("available_ip_count", schemas["CreateNetworkSubnetRequest"]["properties"])

        security_group = schemas["NetworkSecurityGroup"]
        self.assertIn("vpc_id", security_group["properties"])
        self.assertNotIn("vpc_id", security_group["required"])
        for field in ("rule_count", "bound_instance_count"):
            self.assertIn(field, security_group["properties"])
            self.assertTrue(security_group["properties"][field]["readOnly"])

        create_security_group = schemas["CreateNetworkSecurityGroupRequest"]
        self.assertIn("vpc_id", create_security_group["properties"])
        self.assertNotIn("vpc_id", create_security_group["required"])
        security_group_filters = {
            param["name"]: param for param in paths["/networks/security-groups"]["get"]["parameters"]
        }
        self.assertIn("vpc_id", security_group_filters)

        for schema_name in (
            "NetworkSecurityGroupRule",
            "NetworkSecurityGroupRuleResource",
            "CreateNetworkSecurityGroupRuleRequest",
            "UpdateNetworkSecurityGroupRuleRequest",
        ):
            props = schemas[schema_name]["properties"]
            self.assertIn("cidr", props, msg=f"{schema_name} must keep historical cidr")
            self.assertNotIn(
                "peer_security_group_id",
                props,
                msg=f"{schema_name} must not prebuild peers absent from the 7.29 prototype",
            )
        for schema_name in (
            "NetworkSecurityGroupRule",
            "NetworkSecurityGroupRuleResource",
            "CreateNetworkSecurityGroupRuleRequest",
        ):
            self.assertIn("cidr", schemas[schema_name]["required"])

        self.assertIn("priority", schemas["NetworkRoute"]["properties"])
        self.assertIn("priority", schemas["CreateNetworkRouteRequest"]["properties"])
        self.assertEqual(
            schemas["NetworkRoute"]["properties"]["next_hop_type"]["enum"],
            ["gateway", "instance", "nat", "local"],
        )
        self.assertEqual(
            schemas["CreateNetworkRouteRequest"]["properties"]["next_hop_type"]["enum"],
            ["gateway", "instance", "nat"],
        )
        route_filters = {
            param["name"]: param for param in paths["/networks/routes"]["get"]["parameters"]
        }
        self.assertEqual(
            route_filters["next_hop_type"]["schema"]["enum"],
            ["gateway", "instance", "nat", "local"],
        )

    def test_async_accepted_responses_declare_polling_location(self) -> None:
        spec = yaml.safe_load((ROOT / "api/openapi/v1.yaml").read_text(encoding="utf-8"))
        schemas = spec["components"]["schemas"]

        for path, path_item in spec["paths"].items():
            for method in ("get", "post", "put", "patch", "delete"):
                operation = path_item.get(method)
                if operation is None or "202" not in operation.get("responses", {}):
                    continue
                accepted = operation["responses"]["202"]
                schema_ref = (
                    accepted.get("content", {})
                    .get("application/json", {})
                    .get("schema", {})
                    .get("$ref", "")
                )
                schema_name = schema_ref.rsplit("/", 1)[-1]
                response_schema = schemas.get(schema_name, {})
                exposes_task = schema_name == "AsyncTask" or "task_id" in response_schema.get(
                    "required", []
                )
                if exposes_task:
                    with self.subTest(method=method, path=path):
                        self.assertIn("Location", accepted.get("headers", {}))


if __name__ == "__main__":
    unittest.main()
