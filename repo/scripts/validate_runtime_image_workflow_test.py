import copy
import sys
import unittest
from pathlib import Path

sys.path.insert(0, str(Path(__file__).resolve().parent))
import validate_runtime_image_workflow as validator


class RuntimeImageWorkflowContractTest(unittest.TestCase):
    def setUp(self) -> None:
        self.workflow = copy.deepcopy(validator.load_workflow())
        self.makefile = validator.load_makefile()

    def test_current_workflow_is_controlled(self) -> None:
        self.assertEqual([], validator.validate(self.workflow))

    def test_current_local_runtime_image_targets_emit_attestations(self) -> None:
        self.assertEqual([], validator.validate_local_makefile(self.makefile))

    def test_local_runtime_image_targets_reject_missing_sbom(self) -> None:
        makefile = self.makefile.replace(
            "RUNTIME_IMAGE_BUILD_FLAGS := --load --sbom=true --provenance=mode=max",
            "RUNTIME_IMAGE_BUILD_FLAGS := --load --provenance=mode=max",
        )
        self.assertTrue(
            any("SBOM" in error for error in validator.validate_local_makefile(makefile))
        )

    def test_latest_tag_is_rejected(self) -> None:
        build = validator.runtime_build_step(self.workflow)
        build["with"]["tags"] += "\nharbor.ani.internal/ani/example:latest"
        self.assertTrue(any("single SHA tag" in error for error in validator.validate(self.workflow)))

    def test_missing_digest_artifact_is_rejected(self) -> None:
        job = self.workflow["jobs"]["runtime-services"]
        job["steps"] = [step for step in job["steps"] if step.get("name") != "Upload digest evidence"]
        self.assertTrue(any("digest artifact" in error for error in validator.validate(self.workflow)))

    def test_harbor_login_uses_registry_host_without_project_path(self) -> None:
        self.workflow["env"]["REGISTRY_HOST"] = "harbor.ani.internal/ani"
        self.assertTrue(any("registry host" in error for error in validator.validate(self.workflow)))


if __name__ == "__main__":
    unittest.main()
