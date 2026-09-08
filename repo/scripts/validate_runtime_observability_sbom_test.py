import json
import sys
import tempfile
import unittest
from pathlib import Path


sys.path.insert(0, str(Path(__file__).resolve().parent))
import validate_runtime_observability_sbom as validator


def write_bom(path: Path, module_name: str, *, application: bool) -> None:
    root_ref = f"pkg:golang/{module_name}?type=module"
    components = [
        {
            "bom-ref": f"pkg:golang/{name}@{version}?type=module",
            "type": "library",
            "name": name,
            "version": version,
        }
        for name, version in validator.PINNED_COMPONENT_VERSIONS.items()
    ]
    if application:
        components.append(
            {
                "bom-ref": "pkg:golang/github.com/kubercloud/ani/runtimeadmin?type=module",
                "type": "library",
                "name": validator.RUNTIMEADMIN_MODULE,
            }
        )
    path.parent.mkdir(parents=True, exist_ok=True)
    path.write_text(
        json.dumps(
            {
                "bomFormat": "CycloneDX",
                "specVersion": "1.6",
                "metadata": {
                    "component": {
                        "bom-ref": root_ref,
                        "type": "application" if application else "library",
                        "name": module_name,
                    }
                },
                "components": components,
                "dependencies": [{"ref": root_ref, "dependsOn": []}],
            }
        ),
        encoding="utf-8",
    )


class ValidateRuntimeObservabilitySbomTest(unittest.TestCase):
    def populate_valid_boms(self, output_dir: Path) -> None:
        for module_path, module_name in validator.LIBRARY_MODULES.items():
            write_bom(
                output_dir / validator.sbom_filename(module_path),
                module_name,
                application=False,
            )
        for module_path, module_name in validator.APPLICATION_MODULES.items():
            write_bom(
                output_dir / validator.sbom_filename(module_path),
                module_name,
                application=True,
            )

    def test_exact_ten_sboms_and_fixed_versions_pass(self) -> None:
        with tempfile.TemporaryDirectory() as temp_dir:
            output_dir = Path(temp_dir)
            self.populate_valid_boms(output_dir)
            self.assertEqual([], validator.validate_output_dir(output_dir))

    def test_root_component_drift_is_rejected(self) -> None:
        with tempfile.TemporaryDirectory() as temp_dir:
            output_dir = Path(temp_dir)
            self.populate_valid_boms(output_dir)
            path = output_dir / validator.sbom_filename("runtimeadmin")
            bom = json.loads(path.read_text(encoding="utf-8"))
            bom["metadata"]["component"]["name"] = "wrong/module"
            path.write_text(json.dumps(bom), encoding="utf-8")
            errors = validator.validate_output_dir(output_dir)
            self.assertTrue(any("root component" in error for error in errors), errors)

    def test_fixed_dependency_version_drift_is_rejected(self) -> None:
        with tempfile.TemporaryDirectory() as temp_dir:
            output_dir = Path(temp_dir)
            self.populate_valid_boms(output_dir)
            path = output_dir / validator.sbom_filename("runtimeadmin")
            bom = json.loads(path.read_text(encoding="utf-8"))
            component = next(
                component
                for component in bom["components"]
                if component["name"] == "go.opentelemetry.io/otel"
            )
            component["version"] = "v1.44.1"
            path.write_text(json.dumps(bom), encoding="utf-8")
            errors = validator.validate_output_dir(output_dir)
            self.assertTrue(any("go.opentelemetry.io/otel" in error for error in errors), errors)

    def test_application_without_runtimeadmin_is_rejected(self) -> None:
        with tempfile.TemporaryDirectory() as temp_dir:
            output_dir = Path(temp_dir)
            self.populate_valid_boms(output_dir)
            path = output_dir / validator.sbom_filename("services/tenant-service")
            bom = json.loads(path.read_text(encoding="utf-8"))
            bom["components"] = [
                component
                for component in bom["components"]
                if component["name"] != validator.RUNTIMEADMIN_MODULE
            ]
            path.write_text(json.dumps(bom), encoding="utf-8")
            errors = validator.validate_output_dir(output_dir)
            self.assertTrue(any("must include" in error for error in errors), errors)


if __name__ == "__main__":
    unittest.main()
