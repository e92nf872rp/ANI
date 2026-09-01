#!/usr/bin/env python3
from __future__ import annotations

import unittest

import validate_model_repository_contract as validator


class ModelRepositoryContractTest(unittest.TestCase):
    @classmethod
    def setUpClass(cls) -> None:
        cls.spec = validator.load_spec("api/openapi/services/v1.yaml")
        cls.schemas = cls.spec["components"]["schemas"]
        cls.paths = cls.spec["paths"]

    def test_model_schemas_expose_p0_fields(self) -> None:
        model = self.schemas["Model"]["properties"]
        version = self.schemas["ModelVersion"]["properties"]
        self.assertIn("description", model)
        self.assertIn("updated_at", model)
        self.assertEqual(model["versions"]["items"]["$ref"], "#/components/schemas/ModelVersion")
        self.assertIn("checksum_sha256", version)
        self.assertIn("storage_path", version)

    def test_model_list_exposes_p0_filters(self) -> None:
        parameters = self.paths["/models"]["get"]["parameters"]
        names = {parameter["name"] for parameter in parameters}
        self.assertTrue({"keyword", "source", "capability"}.issubset(names))

    def test_p0_paths_exist(self) -> None:
        self.assertIn("get", self.paths["/models/{model_id}/versions"])
        self.assertIn("post", self.paths["/models/{model_id}/upload-url"])
        self.assertIn("get", self.paths["/model-import-tasks/{task_id}"])

    def test_upload_contract_is_bounded(self) -> None:
        validator.validate_upload_contract(self.spec)

    def test_import_task_returns_async_task(self) -> None:
        self.assertIn("/model-import-tasks/{task_id}", self.paths)
        response = self.paths["/model-import-tasks/{task_id}"]["get"]["responses"]["200"]
        schema = response["content"]["application/json"]["schema"]
        self.assertEqual(schema["$ref"], "#/components/schemas/AsyncTask")

    def test_delete_declares_model_in_use_conflict(self) -> None:
        self.assertIn("409", self.paths["/models/{model_id}"]["delete"]["responses"])
        conflict = self.paths["/models/{model_id}"]["delete"]["responses"]["409"]
        self.assertIn("MODEL_IN_USE", conflict["description"])

    def test_all_model_operations_use_v1_authz(self) -> None:
        validator.validate_authz_format(self.spec)

    def test_planned_model_features_stay_out_of_p0(self) -> None:
        forbidden_prefixes = validator.FORBIDDEN_SCHEMA_PREFIXES
        unexpected = sorted(
            name for name in self.schemas if any(name.startswith(prefix) for prefix in forbidden_prefixes)
        )
        self.assertEqual(unexpected, [])


if __name__ == "__main__":
    unittest.main()
