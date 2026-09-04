import copy
import subprocess
import sys
import tempfile
import unittest
from pathlib import Path
from unittest import mock

import yaml

sys.path.insert(0, str(Path(__file__).resolve().parent))
import validate_service_runtime_observability as validator


ROOT = Path(__file__).resolve().parents[1]


class ValidateServiceRuntimeObservabilityTest(unittest.TestCase):
    def test_repository_contract_passes_without_running_docker(self) -> None:
        with mock.patch.object(validator, "changed_paths", return_value=set()):
            errors = validator.validate_repository(ROOT, run_promtool=False, base=None)
        self.assertEqual([], errors)

    def test_duplicate_management_port_across_pods_is_allowed(self) -> None:
        inventory = validator.load_inventory(ROOT)
        ports = {
            service["name"]: next(port["port"] for port in service["ports"] if port["name"] == "health")
            for service in inventory["services"]
        }
        self.assertEqual(9204, ports["task-service"])
        self.assertEqual(9204, ports["inference-service"])
        self.assertEqual([], validator.validate_inventory(inventory))

    def test_gateway_probe_uses_public_root_health_contract(self) -> None:
        inventory = validator.load_inventory(ROOT)
        gateway = next(service for service in inventory["services"] if service["name"] == "ani-gateway")
        self.assertEqual("/healthz", gateway["probe"]["httpGet"]["path"])

    def test_inventory_rejects_gateway_probe_path_drift(self) -> None:
        inventory = validator.load_inventory(ROOT)
        gateway = next(service for service in inventory["services"] if service["name"] == "ani-gateway")
        gateway["probe"]["httpGet"]["path"] = "/api/v1/healthz"
        errors = validator.validate_inventory(inventory)
        self.assertTrue(any("probe path" in error for error in errors), errors)

    def test_prometheus_contract_rejects_second_ani_components_job(self) -> None:
        config = validator.load_prometheus_config(ROOT)
        config["scrape_configs"].append(copy.deepcopy(next(
            job for job in config["scrape_configs"] if job["job_name"] == "ani-components"
        )))
        errors = validator.validate_prometheus_config(config)
        self.assertTrue(any("exactly one" in error for error in errors), errors)

    def test_forbidden_service_changes_are_rejected(self) -> None:
        changed = {
            "repo/services/reconcile-worker/main.go",
            "repo/services/envoy-authz-adapter/Dockerfile",
            "repo/services/kb-service/go.mod",
        }
        errors = validator.validate_forbidden_changes(changed)
        self.assertEqual(3, len(errors))

    def test_exact_p0_allowlist_accepts_known_paths_and_fixed_input(self) -> None:
        changed = {
            ".github/workflows/ci.yml",
            "ANI-06-开发计划.md",
            "repo/api/core-v1-compatibility-baseline.yaml",
            "repo/deploy/docker/README.md",
            "repo/deploy/helm/ani-platform/README.md",
            "repo/development-records/README.md",
            "repo/development-records/live-evidence/service-runtime-observability-p0-live-20260904.json",
            "repo/development-records/service-runtime-observability-p0.md",
            "repo/docs/operations/service-runtime-observability.md",
            "repo/runtimeadmin/runtime.go",
            validator.FIXED_PLAN_GIT_PATH,
        }
        self.assertEqual([], validator.validate_changed_path_allowlist(changed))

    def test_exact_p0_allowlist_rejects_other_development_record(self) -> None:
        errors = validator.validate_changed_path_allowlist(
            {"repo/development-records/unrelated-batch.md"}
        )
        self.assertEqual(
            [
                "outside OBS-RUNTIME-P0 exact allowlist: "
                "repo/development-records/unrelated-batch.md"
            ],
            errors,
        )

    def test_exact_p0_allowlist_rejects_deprecated_frontend_changes(self) -> None:
        frontend_paths = {
            "repo/frontends/boss/src/features/platform-service-health/PlatformServiceHealthCard.tsx",
            "repo/frontends/console/src/api/core-schema.d.ts",
        }
        errors = validator.validate_changed_path_allowlist(frontend_paths)
        self.assertEqual(
            [
                "outside OBS-RUNTIME-P0 exact allowlist: "
                "repo/frontends/boss/src/features/platform-service-health/PlatformServiceHealthCard.tsx",
                "outside OBS-RUNTIME-P0 exact allowlist: repo/frontends/console/src/api/core-schema.d.ts",
            ],
            errors,
        )

    def test_exact_p0_allowlist_rejects_unknown_service_change(self) -> None:
        errors = validator.validate_changed_path_allowlist(
            {"repo/services/auth-service/main.go"}
        )
        self.assertEqual(
            ["outside OBS-RUNTIME-P0 exact allowlist: repo/services/auth-service/main.go"],
            errors,
        )

    def test_first_party_session_api_license_exception_is_exact_and_version_bound(self) -> None:
        makefile = (ROOT / "Makefile").read_text(encoding="utf-8")
        self.assertIn("GO_LICENSES_VERSION := v2.0.1", makefile)
        self.assertIn(
            "SESSION_API_LICENSE_EXCEPTION_MODULE := "
            "github.com/zhangzhe-ctrl/ani-session-gateway/api",
            makefile,
        )
        self.assertIn("SESSION_API_LICENSE_EXCEPTION_VERSION := v0.1.0", makefile)
        self.assertIn(
            "SESSION_API_LICENSE_EXCEPTION_SUM := "
            "h1:x38aXUxXlJFcLtsPDtNN2aW8vloUbS88DScWdcLuLMo=",
            makefile,
        )
        self.assertIn("validate-service-runtime-observability-licenses:", makefile)
        self.assertIn("--ignore $(SESSION_API_LICENSE_EXCEPTION_MODULE)", makefile)
        self.assertNotIn("--ignore github.com/zhangzhe-ctrl ", makefile)
        observability_target = makefile.split(
            "validate-service-runtime-observability:", 1
        )[1].split("\n\n", 1)[0]
        self.assertIn(
            "$(MAKE) validate-service-runtime-observability-licenses",
            observability_target,
        )

    def test_affected_go_modules_have_a_pinned_vulnerability_gate(self) -> None:
        makefile = (ROOT / "Makefile").read_text(encoding="utf-8")
        self.assertIn("GOVULNCHECK_VERSION := v1.6.0", makefile)
        self.assertIn(
            "validate-service-runtime-observability-vulnerabilities:",
            makefile,
        )
        vulnerability_target = makefile.split(
            "validate-service-runtime-observability-vulnerabilities:", 1
        )[1].split("\n\n", 1)[0]
        self.assertIn("for module in $(RUNTIME_OBSERVABILITY_GO_MODULES)", vulnerability_target)
        self.assertIn(
            "golang.org/x/vuln/cmd/govulncheck@$(GOVULNCHECK_VERSION)",
            vulnerability_target,
        )
        observability_target = makefile.split(
            "validate-service-runtime-observability:", 1
        )[1].split("\n\n", 1)[0]
        self.assertIn(
            "$(MAKE) validate-service-runtime-observability-vulnerabilities",
            observability_target,
        )

    def test_runtime_deliverables_have_a_pinned_sbom_gate(self) -> None:
        makefile = (ROOT / "Makefile").read_text(encoding="utf-8")
        self.assertIn("CYCLONEDX_GOMOD_VERSION := v1.10.0", makefile)
        self.assertIn("validate-service-runtime-observability-sbom:", makefile)
        sbom_target = makefile.split(
            "validate-service-runtime-observability-sbom:", 1
        )[1].split("\n\n", 1)[0]
        self.assertIn(
            "python scripts/validate_runtime_observability_sbom_test.py",
            sbom_target,
        )
        self.assertIn(
            "python scripts/validate_runtime_observability_sbom.py --generate",
            sbom_target,
        )
        observability_target = makefile.split(
            "validate-service-runtime-observability:", 1
        )[1].split("\n\n", 1)[0]
        self.assertIn(
            "$(MAKE) validate-service-runtime-observability-sbom",
            observability_target,
        )

    def test_fixed_plan_hash_rejects_drift(self) -> None:
        with tempfile.TemporaryDirectory() as temp_dir:
            root = Path(temp_dir)
            plan = root / validator.FIXED_PLAN_REPO_PATH
            plan.parent.mkdir(parents=True)
            plan.write_text("drift\n", encoding="utf-8")
            errors = validator.validate_fixed_plan(root)
            self.assertEqual(1, len(errors))
            self.assertIn(validator.FIXED_PLAN_SHA256, errors[0])

    def test_fixed_plan_is_optional_in_clean_checkout(self) -> None:
        with tempfile.TemporaryDirectory() as temp_dir:
            self.assertEqual([], validator.validate_fixed_plan(Path(temp_dir)))

    def test_changed_paths_are_always_git_root_relative(self) -> None:
        with tempfile.TemporaryDirectory() as temp_dir:
            git_root = Path(temp_dir)
            repo_root = git_root / "repo"
            repo_root.mkdir()
            subprocess.run(["git", "init", "--quiet"], cwd=git_root, check=True)
            subprocess.run(["git", "config", "user.email", "obs-runtime@example.invalid"], cwd=git_root, check=True)
            subprocess.run(["git", "config", "user.name", "OBS Runtime Test"], cwd=git_root, check=True)
            tracked = repo_root / "tracked.txt"
            tracked.write_text("before\n", encoding="utf-8")
            subprocess.run(["git", "add", "repo/tracked.txt"], cwd=git_root, check=True)
            subprocess.run(["git", "commit", "--quiet", "-m", "fixture"], cwd=git_root, check=True)
            tracked.write_text("after\n", encoding="utf-8")
            (repo_root / "untracked.txt").write_text("new\n", encoding="utf-8")
            unicode_path = git_root / "ANI-06-开发计划.md"
            unicode_path.write_text("before\n", encoding="utf-8")
            subprocess.run(["git", "add", unicode_path.name], cwd=git_root, check=True)
            subprocess.run(["git", "commit", "--quiet", "-m", "unicode fixture"], cwd=git_root, check=True)
            unicode_path.write_text("after\n", encoding="utf-8")

            self.assertEqual(
                {"ANI-06-开发计划.md", "repo/tracked.txt", "repo/untracked.txt"},
                validator.changed_paths(repo_root, base=None),
            )


if __name__ == "__main__":
    unittest.main()
