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


if __name__ == "__main__":
    unittest.main()
