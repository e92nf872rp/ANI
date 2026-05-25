#!/usr/bin/env python3
"""Tests for the Sprint 5 REAL-K8S-LAB-A profile gate."""

from __future__ import annotations

import json
import tempfile
import unittest
from pathlib import Path
from unittest.mock import patch

import validate_real_k8s_profile as gate


REQUIRED_CONTRACT_GATE_IDS = {
    "vcluster-live-gate",
    "vcluster-upgrade-live-gate",
    "k8s-node-pool-live-gate",
    "kubeovn-network-live-gate",
    "kubevirt-vm-live-gate",
    "reconcile-ha-live-gate",
    "kms-sm4-live-gate",
    "secrets-live-gate",
}


class RealK8sProfileGateTest(unittest.TestCase):
    def component_live_env(self) -> dict[str, str]:
        return {
            "ANI_GATEWAY_URL": "http://127.0.0.1:3000/api/v1",
            "ANI_BEARER_TOKEN": "ani-token",
            "ANI_LIVE_K8S_CLUSTER_ID": "k8sclu-live",
            "KUBECONFIG": "/tmp/real-lab.kubeconfig",
            "DATABASE_URL": "postgres://ani:ani@127.0.0.1:5432/ani",
            "RECONCILE_WORKER_METRICS_URL": "http://127.0.0.1:9090/metrics",
            "KMS_PROVIDER_BASE_URL": "http://127.0.0.1:8081",
            "KMS_PROVIDER_BEARER_TOKEN": "kms-token",
            "OBJECTSTORE_LIVE_PUT_URL": "http://127.0.0.1:9000/put",
            "OBJECTSTORE_LIVE_GET_URL": "http://127.0.0.1:9000/get",
        }

    def test_profile_indexes_all_component_contract_gates(self) -> None:
        profile = gate.load_profile(gate.PROFILE)

        gate.validate_contract(profile)

        contract_gates = profile.get("contract_gates")
        self.assertIsInstance(contract_gates, list)
        gate_ids = {entry["id"] for entry in contract_gates}
        self.assertEqual(REQUIRED_CONTRACT_GATE_IDS, gate_ids)
        for entry in contract_gates:
            self.assertTrue(entry["command"].startswith("make validate-"))
            self.assertTrue((gate.ROOT / entry["manifest"]).exists())
            self.assertTrue((gate.ROOT / entry["validator_script"]).exists())

    def test_profile_documents_required_env_for_each_component_gate(self) -> None:
        profile = gate.load_profile(gate.PROFILE)

        gate.validate_contract(profile)

        gates = {entry["id"]: entry for entry in profile["contract_gates"]}
        for gate_id, entry in gates.items():
            self.assertIsInstance(entry.get("required_env"), list, gate_id)
            self.assertTrue(entry["required_env"], gate_id)
            self.assertTrue(all(isinstance(name, str) and name for name in entry["required_env"]), gate_id)
        self.assertIn("ANI_GATEWAY_URL", gates["vcluster-live-gate"]["required_env"])
        self.assertIn("KUBECONFIG", gates["kubeovn-network-live-gate"]["required_env"])
        self.assertIn("DATABASE_URL", gates["reconcile-ha-live-gate"]["required_env"])
        self.assertIn("OBJECTSTORE_LIVE_PUT_URL", gates["kms-sm4-live-gate"]["required_env"])

    def test_contract_validation_rejects_missing_contract_gate(self) -> None:
        profile = gate.load_profile(gate.PROFILE)
        profile["contract_gates"] = [
            entry for entry in profile.get("contract_gates", []) if entry.get("id") != "kubevirt-vm-live-gate"
        ]

        with self.assertRaises(SystemExit):
            gate.validate_contract(profile)

    def test_contract_validation_rejects_component_gate_without_required_env(self) -> None:
        profile = gate.load_profile(gate.PROFILE)
        profile["contract_gates"][0].pop("required_env", None)

        with self.assertRaises(SystemExit):
            gate.validate_contract(profile)

    def test_live_validation_returns_structured_evidence_for_required_checks(self) -> None:
        profile = gate.load_profile(gate.PROFILE)

        with patch.object(gate.shutil, "which", return_value="/usr/bin/kubectl"):
            with patch.object(gate, "condition_passed", return_value=True):
                evidence = gate.validate_live(profile, "/tmp/real-lab.kubeconfig")

        self.assertIsInstance(evidence, dict)
        self.assertEqual("REAL-K8S-LAB-A", evidence["profile"])
        self.assertEqual("live", evidence["status"])
        self.assertTrue(evidence["kubeconfig_provided"])
        self.assertEqual(3, evidence["minimum_nodes"])
        checks = evidence["checks"]
        self.assertTrue(all(check["passed"] for check in checks))
        check_ids = {(check["component"], check["id"]) for check in checks}
        self.assertIn(("kubernetes", "kubernetes-nodes-ready"), check_ids)
        self.assertIn(("kube_ovn", "kubeovn-vpc-crd"), check_ids)
        self.assertIn(("kubevirt", "kubevirt-vm-crd"), check_ids)
        self.assertIn(("vcluster", "vcluster-workloads"), check_ids)
        self.assertNotIn(("kms_sm4_and_secret", "kms-provider-config"), check_ids)

    def test_main_writes_live_evidence_json_when_requested(self) -> None:
        fake_evidence = {
            "profile": "REAL-K8S-LAB-A",
            "status": "live",
            "checks": [{"component": "kubernetes", "id": "kubernetes-nodes-ready", "passed": True}],
        }
        with tempfile.TemporaryDirectory() as tmpdir:
            output = Path(tmpdir) / "real-k8s-live-evidence.json"

            with patch.object(gate, "validate_live", return_value=fake_evidence):
                with patch(
                    "sys.argv",
                    [
                        "validate_real_k8s_profile.py",
                        "--live",
                        "--kubeconfig",
                        "/tmp/real-lab.kubeconfig",
                        "--evidence-output",
                        str(output),
                    ],
                ):
                    gate.main()

            self.assertEqual(fake_evidence, json.loads(output.read_text(encoding="utf-8")))

    def test_component_live_runner_executes_indexed_validators_with_evidence_outputs(self) -> None:
        profile = gate.load_profile(gate.PROFILE)
        commands: list[list[str]] = []

        def fake_runner(command: list[str]) -> gate.subprocess.CompletedProcess[str]:
            commands.append(command)
            Path(command[-1]).write_text('{"passed": true}\n', encoding="utf-8")
            return gate.subprocess.CompletedProcess(command, 0, stdout="ok\n", stderr="")

        with tempfile.TemporaryDirectory() as tmpdir:
            evidence = gate.validate_component_live_gates(
                profile,
                Path(tmpdir),
                runner=fake_runner,
                env=self.component_live_env(),
            )

        self.assertEqual("REAL-K8S-LAB-A", evidence["profile"])
        self.assertEqual("component_live", evidence["status"])
        component_gates = evidence["component_gates"]
        self.assertEqual(len(REQUIRED_CONTRACT_GATE_IDS), len(component_gates))
        gate_ids = {entry["id"] for entry in component_gates}
        self.assertEqual(REQUIRED_CONTRACT_GATE_IDS, gate_ids)
        self.assertTrue(all(entry["passed"] for entry in component_gates))
        self.assertTrue(all(str(entry["evidence_output"]).endswith(".json") for entry in component_gates))
        self.assertTrue(all("--live" in command for command in commands))
        self.assertTrue(all("--evidence-output" in command for command in commands))
        self.assertTrue(
            any(command[-3:] == ["--live", "--evidence-output", str(Path(tmpdir) / "secrets-live-gate.json")] for command in commands)
        )

    def test_component_live_runner_collects_all_failures_before_reporting(self) -> None:
        profile = gate.load_profile(gate.PROFILE)
        commands: list[list[str]] = []
        failed_gate_ids = {"kubeovn-network-live-gate", "secrets-live-gate"}

        def fake_runner(command: list[str]) -> gate.subprocess.CompletedProcess[str]:
            commands.append(command)
            gate_id = Path(command[-1]).stem
            if gate_id in failed_gate_ids:
                return gate.subprocess.CompletedProcess(command, 1, stdout="", stderr=f"{gate_id} unavailable\n")
            Path(command[-1]).write_text('{"passed": true}\n', encoding="utf-8")
            return gate.subprocess.CompletedProcess(command, 0, stdout="ok\n", stderr="")

        with tempfile.TemporaryDirectory() as tmpdir:
            evidence = gate.validate_component_live_gates(
                profile,
                Path(tmpdir),
                runner=fake_runner,
                env=self.component_live_env(),
            )

        self.assertEqual(len(REQUIRED_CONTRACT_GATE_IDS), len(commands))
        self.assertEqual("component_live_failed", evidence["status"])
        self.assertFalse(evidence["passed"])
        self.assertEqual(
            {"total": len(REQUIRED_CONTRACT_GATE_IDS), "passed": len(REQUIRED_CONTRACT_GATE_IDS) - 2, "failed": 2},
            evidence["summary"],
        )
        failed_entries = {entry["id"]: entry for entry in evidence["component_gates"] if not entry["passed"]}
        self.assertEqual(failed_gate_ids, set(failed_entries))
        self.assertEqual(1, failed_entries["kubeovn-network-live-gate"]["returncode"])
        self.assertIn("unavailable", failed_entries["secrets-live-gate"]["error"])

    def test_component_live_runner_fails_successful_validator_without_evidence_file(self) -> None:
        profile = gate.select_component_contract_gates(gate.load_profile(gate.PROFILE), ["secrets-live-gate"])
        commands: list[list[str]] = []

        def fake_runner(command: list[str]) -> gate.subprocess.CompletedProcess[str]:
            commands.append(command)
            return gate.subprocess.CompletedProcess(command, 0, stdout="ok\n", stderr="")

        with tempfile.TemporaryDirectory() as tmpdir:
            evidence = gate.validate_component_live_gates(
                profile,
                Path(tmpdir),
                runner=fake_runner,
                env=self.component_live_env(),
            )

        self.assertEqual(1, len(commands))
        self.assertEqual("component_live_failed", evidence["status"])
        self.assertFalse(evidence["passed"])
        self.assertEqual({"total": 1, "passed": 0, "failed": 1}, evidence["summary"])
        gate_result = evidence["component_gates"][0]
        self.assertFalse(gate_result["passed"])
        self.assertEqual(0, gate_result["returncode"])
        self.assertIn("missing evidence output", gate_result["error"])

    def test_component_live_runner_fails_successful_validator_with_invalid_evidence_json(self) -> None:
        profile = gate.select_component_contract_gates(gate.load_profile(gate.PROFILE), ["secrets-live-gate"])

        def fake_runner(command: list[str]) -> gate.subprocess.CompletedProcess[str]:
            Path(command[-1]).write_text("not-json\n", encoding="utf-8")
            return gate.subprocess.CompletedProcess(command, 0, stdout="ok\n", stderr="")

        with tempfile.TemporaryDirectory() as tmpdir:
            evidence = gate.validate_component_live_gates(
                profile,
                Path(tmpdir),
                runner=fake_runner,
                env=self.component_live_env(),
            )

        self.assertEqual("component_live_failed", evidence["status"])
        self.assertFalse(evidence["passed"])
        self.assertEqual({"total": 1, "passed": 0, "failed": 1}, evidence["summary"])
        gate_result = evidence["component_gates"][0]
        self.assertFalse(gate_result["passed"])
        self.assertEqual(0, gate_result["returncode"])
        self.assertIn("invalid evidence JSON", gate_result["error"])

    def test_component_live_runner_fails_successful_validator_with_nonpassing_evidence_json(self) -> None:
        profile = gate.select_component_contract_gates(gate.load_profile(gate.PROFILE), ["secrets-live-gate"])

        def fake_runner(command: list[str]) -> gate.subprocess.CompletedProcess[str]:
            Path(command[-1]).write_text('{"status": "failed"}\n', encoding="utf-8")
            return gate.subprocess.CompletedProcess(command, 0, stdout="ok\n", stderr="")

        with tempfile.TemporaryDirectory() as tmpdir:
            evidence = gate.validate_component_live_gates(
                profile,
                Path(tmpdir),
                runner=fake_runner,
                env=self.component_live_env(),
            )

        self.assertEqual("component_live_failed", evidence["status"])
        self.assertFalse(evidence["passed"])
        self.assertEqual({"total": 1, "passed": 0, "failed": 1}, evidence["summary"])
        gate_result = evidence["component_gates"][0]
        self.assertFalse(gate_result["passed"])
        self.assertEqual(0, gate_result["returncode"])
        self.assertIn("non-passing evidence JSON", gate_result["error"])

    def test_component_live_runner_preflights_required_env_before_running_validators(self) -> None:
        profile = gate.load_profile(gate.PROFILE)
        commands: list[list[str]] = []

        def fake_runner(command: list[str]) -> gate.subprocess.CompletedProcess[str]:
            commands.append(command)
            return gate.subprocess.CompletedProcess(command, 0, stdout="ok\n", stderr="")

        with tempfile.TemporaryDirectory() as tmpdir:
            evidence = gate.validate_component_live_gates(
                profile,
                Path(tmpdir),
                runner=fake_runner,
                env={"ANI_GATEWAY_URL": "http://127.0.0.1:3000/api/v1"},
            )

        self.assertEqual([], commands)
        self.assertEqual("component_live_preflight_failed", evidence["status"])
        self.assertFalse(evidence["passed"])
        self.assertEqual(0, evidence["summary"]["passed"])
        self.assertEqual(len(REQUIRED_CONTRACT_GATE_IDS), evidence["summary"]["blocked"])
        blocked = {entry["id"]: entry for entry in evidence["component_gates"]}
        self.assertIn("ANI_BEARER_TOKEN", blocked["vcluster-live-gate"]["missing_env"])
        self.assertIn("KUBECONFIG", blocked["kubevirt-vm-live-gate"]["missing_env"])
        self.assertIn("KMS_PROVIDER_BASE_URL", blocked["kms-sm4-live-gate"]["missing_env"])

    def test_component_live_runner_honors_explicit_empty_env(self) -> None:
        profile = gate.load_profile(gate.PROFILE)
        commands: list[list[str]] = []

        def fake_runner(command: list[str]) -> gate.subprocess.CompletedProcess[str]:
            commands.append(command)
            return gate.subprocess.CompletedProcess(command, 0, stdout="ok\n", stderr="")

        with tempfile.TemporaryDirectory() as tmpdir:
            evidence = gate.validate_component_live_gates(
                profile,
                Path(tmpdir),
                runner=fake_runner,
                env={},
            )

        self.assertEqual([], commands)
        self.assertEqual("component_live_preflight_failed", evidence["status"])
        self.assertEqual(len(REQUIRED_CONTRACT_GATE_IDS), evidence["summary"]["blocked"])
        blocked = {entry["id"]: entry for entry in evidence["component_gates"]}
        self.assertIn("KUBECONFIG", blocked["vcluster-live-gate"]["missing_env"])

    def test_component_env_template_lists_unique_required_env_without_secret_values(self) -> None:
        profile = gate.load_profile(gate.PROFILE)

        template = gate.component_env_template(profile)

        self.assertIn("# REAL-K8S-LAB-A component live required environment", template)
        self.assertIn("--component-preflight", template)
        self.assertEqual(1, template.count('export KUBECONFIG=""'))
        self.assertIn('export ANI_GATEWAY_URL=""', template)
        self.assertIn('export KMS_PROVIDER_BEARER_TOKEN=""', template)
        self.assertIn("# vcluster-live-gate: KUBECONFIG, ANI_GATEWAY_URL, ANI_BEARER_TOKEN", template)
        self.assertIn("# kms-sm4-live-gate: ANI_GATEWAY_URL, ANI_BEARER_TOKEN, KMS_PROVIDER_BASE_URL", template)
        self.assertNotIn("ani-token", template)
        self.assertNotIn("kms-token", template)

    def test_main_writes_component_env_template_when_requested(self) -> None:
        with tempfile.TemporaryDirectory() as tmpdir:
            output = Path(tmpdir) / "real-k8s-component-live.env"

            with patch(
                "sys.argv",
                [
                    "validate_real_k8s_profile.py",
                    "--component-env-template-output",
                    str(output),
                ],
            ):
                gate.main()

            template = output.read_text(encoding="utf-8")
            self.assertIn('export OBJECTSTORE_LIVE_PUT_URL=""', template)
            self.assertIn("# secrets-live-gate: KUBECONFIG, ANI_GATEWAY_URL, ANI_BEARER_TOKEN", template)

    def test_component_env_file_loader_parses_export_template_without_shell_execution(self) -> None:
        with tempfile.TemporaryDirectory() as tmpdir:
            env_file = Path(tmpdir) / "real-k8s-component-live.env"
            env_file.write_text(
                "\n".join(
                    [
                        "# filled REAL-K8S-LAB-A component env",
                        'export KUBECONFIG="/tmp/lab kubeconfig.yaml"',
                        "ANI_GATEWAY_URL='http://127.0.0.1:3000/api/v1'",
                        'export ANI_BEARER_TOKEN="token value"',
                        "",
                    ]
                ),
                encoding="utf-8",
            )

            env = gate.load_component_env_file(env_file)

        self.assertEqual("/tmp/lab kubeconfig.yaml", env["KUBECONFIG"])
        self.assertEqual("http://127.0.0.1:3000/api/v1", env["ANI_GATEWAY_URL"])
        self.assertEqual("token value", env["ANI_BEARER_TOKEN"])

    def test_component_env_file_loader_rejects_non_assignment_lines(self) -> None:
        with tempfile.TemporaryDirectory() as tmpdir:
            env_file = Path(tmpdir) / "real-k8s-component-live.env"
            env_file.write_text("export KUBECONFIG=/tmp/lab\nsource /tmp/secret.env\n", encoding="utf-8")

            with self.assertRaises(SystemExit):
                gate.load_component_env_file(env_file)

    def test_main_component_live_merges_component_env_file_with_process_env(self) -> None:
        profile = gate.load_profile(gate.PROFILE)
        with tempfile.TemporaryDirectory() as tmpdir:
            env_file = Path(tmpdir) / "real-k8s-component-live.env"
            env_file.write_text(
                "\n".join(f'export {name}="{name.lower()}-from-file"' for name in gate.component_required_env_names(profile)),
                encoding="utf-8",
            )

            with patch.object(gate, "validate_component_live_gates", return_value={"component_gates": [], "passed": True}) as validate:
                with patch.dict(gate.os.environ, {"ANI_GATEWAY_URL": "http://from-process.example/api/v1"}, clear=True):
                    with patch(
                        "sys.argv",
                        [
                            "validate_real_k8s_profile.py",
                            "--component-live",
                            "--component-env-file",
                            str(env_file),
                            "--component-evidence-dir",
                            str(Path(tmpdir) / "components"),
                        ],
                    ):
                        gate.main()

        merged_env = validate.call_args.kwargs["env"]
        self.assertEqual("ani_gateway_url-from-file", merged_env["ANI_GATEWAY_URL"])
        self.assertEqual("kubeconfig-from-file", merged_env["KUBECONFIG"])

    def test_component_live_preflight_reports_complete_env_without_running_validators(self) -> None:
        profile = gate.load_profile(gate.PROFILE)

        evidence = gate.validate_component_live_preflight(profile, self.component_live_env())

        self.assertEqual("component_live_preflight_passed", evidence["status"])
        self.assertTrue(evidence["passed"])
        self.assertEqual(len(REQUIRED_CONTRACT_GATE_IDS), evidence["summary"]["total"])
        self.assertEqual(len(REQUIRED_CONTRACT_GATE_IDS), evidence["summary"]["passed"])
        self.assertEqual(0, evidence["summary"]["blocked"])
        self.assertTrue(all(entry["passed"] for entry in evidence["component_gates"]))
        self.assertTrue(all(entry["missing_env"] == [] for entry in evidence["component_gates"]))

    def test_component_live_preflight_reports_missing_env_without_running_validators(self) -> None:
        profile = gate.load_profile(gate.PROFILE)

        evidence = gate.validate_component_live_preflight(profile, {"ANI_GATEWAY_URL": "http://127.0.0.1:3000/api/v1"})

        self.assertEqual("component_live_preflight_failed", evidence["status"])
        self.assertFalse(evidence["passed"])
        self.assertEqual(len(REQUIRED_CONTRACT_GATE_IDS), evidence["summary"]["total"])
        self.assertEqual(len(REQUIRED_CONTRACT_GATE_IDS), evidence["summary"]["blocked"])
        blocked = {entry["id"]: entry for entry in evidence["component_gates"]}
        self.assertIn("KUBECONFIG", blocked["vcluster-live-gate"]["missing_env"])
        self.assertIn("OBJECTSTORE_LIVE_GET_URL", blocked["kms-sm4-live-gate"]["missing_env"])

    def test_select_component_contract_gates_filters_requested_gate_ids(self) -> None:
        profile = gate.load_profile(gate.PROFILE)

        selected = gate.select_component_contract_gates(profile, ["secrets-live-gate"])

        self.assertEqual(["secrets-live-gate"], [entry["id"] for entry in selected["contract_gates"]])
        self.assertEqual(profile["profile"], selected["profile"])
        self.assertEqual(len(REQUIRED_CONTRACT_GATE_IDS), len(profile["contract_gates"]))

    def test_select_component_contract_gates_rejects_unknown_gate_id(self) -> None:
        profile = gate.load_profile(gate.PROFILE)

        with self.assertRaises(SystemExit):
            gate.select_component_contract_gates(profile, ["missing-live-gate"])

    def test_main_component_preflight_uses_env_file_without_running_live_gates(self) -> None:
        profile = gate.load_profile(gate.PROFILE)
        fake_evidence = {
            "profile": "REAL-K8S-LAB-A",
            "status": "component_live_preflight_passed",
            "passed": True,
            "summary": {"total": len(REQUIRED_CONTRACT_GATE_IDS), "passed": len(REQUIRED_CONTRACT_GATE_IDS), "blocked": 0},
            "component_gates": [],
        }
        with tempfile.TemporaryDirectory() as tmpdir:
            env_file = Path(tmpdir) / "real-k8s-component-live.env"
            env_file.write_text(
                "\n".join(f'export {name}="{name.lower()}-from-file"' for name in gate.component_required_env_names(profile)),
                encoding="utf-8",
            )
            output = Path(tmpdir) / "component-preflight-summary.json"

            with patch.object(gate, "validate_component_live_preflight", return_value=fake_evidence) as preflight:
                with patch.object(gate, "validate_component_live_gates") as live_gates:
                    with patch(
                        "sys.argv",
                        [
                            "validate_real_k8s_profile.py",
                            "--component-preflight",
                            "--component-env-file",
                            str(env_file),
                            "--evidence-output",
                            str(output),
                        ],
                    ):
                        gate.main()

            live_gates.assert_not_called()
            merged_env = preflight.call_args.args[1]
            self.assertEqual("kubeconfig-from-file", merged_env["KUBECONFIG"])
            self.assertEqual(fake_evidence, json.loads(output.read_text(encoding="utf-8")))

    def test_main_component_preflight_filters_selected_gate(self) -> None:
        profile = gate.load_profile(gate.PROFILE)
        fake_evidence = {
            "profile": "REAL-K8S-LAB-A",
            "status": "component_live_preflight_passed",
            "passed": True,
            "summary": {"total": 1, "passed": 1, "blocked": 0},
            "component_gates": [{"id": "secrets-live-gate", "passed": True}],
        }
        with tempfile.TemporaryDirectory() as tmpdir:
            env_file = Path(tmpdir) / "real-k8s-component-live.env"
            env_file.write_text(
                "\n".join(f'export {name}="{name.lower()}-from-file"' for name in gate.component_required_env_names(profile)),
                encoding="utf-8",
            )

            with patch.object(gate, "validate_component_live_preflight", return_value=fake_evidence) as preflight:
                with patch(
                    "sys.argv",
                    [
                        "validate_real_k8s_profile.py",
                        "--component-preflight",
                        "--component-gate",
                        "secrets-live-gate",
                        "--component-env-file",
                        str(env_file),
                    ],
                ):
                    gate.main()

            selected_profile = preflight.call_args.args[0]
            self.assertEqual(["secrets-live-gate"], [entry["id"] for entry in selected_profile["contract_gates"]])

    def test_component_summary_report_classifies_failed_and_blocked_gates(self) -> None:
        profile = gate.load_profile(gate.PROFILE)
        evidence = {
            "profile": "REAL-K8S-LAB-A",
            "status": "component_live_preflight_failed",
            "summary": {"total": 3, "passed": 1, "failed": 1, "blocked": 1},
            "component_gates": [
                {"id": "vcluster-live-gate", "passed": True, "missing_env": []},
                {
                    "id": "kms-sm4-live-gate",
                    "passed": False,
                    "missing_env": ["KMS_PROVIDER_BASE_URL"],
                },
                {
                    "id": "secrets-live-gate",
                    "passed": False,
                    "missing_env": [],
                    "returncode": 1,
                    "error": "pod env missing",
                },
            ],
        }

        report = gate.component_summary_report(
            profile,
            evidence,
            component_env_file="/tmp/ani-real-k8s.env",
            component_evidence_dir="/tmp/component-gates",
        )

        self.assertEqual("component_report", report["status"])
        self.assertFalse(report["passed"])
        self.assertEqual(["kms-sm4-live-gate", "secrets-live-gate"], report["unresolved_gates"])
        self.assertEqual("component_live_preflight_failed", report["source_status"])
        self.assertEqual(["secrets-live-gate"], report["failed_gates"])
        self.assertEqual(["kms-sm4-live-gate"], report["blocked_gates"])
        commands = {entry["id"]: entry for entry in report["next_commands"]}
        self.assertIn("--component-gate secrets-live-gate", commands["secrets-live-gate"]["live"])
        self.assertIn("--component-env-file /tmp/ani-real-k8s.env", commands["kms-sm4-live-gate"]["preflight"])
        self.assertIn("--component-evidence-dir /tmp/component-gates", commands["secrets-live-gate"]["live"])
        details = {entry["id"]: entry for entry in report["gate_details"]}
        self.assertEqual("blocked", details["kms-sm4-live-gate"]["status"])
        self.assertEqual(["KMS_PROVIDER_BASE_URL"], details["kms-sm4-live-gate"]["missing_env"])
        self.assertEqual("failed", details["secrets-live-gate"]["status"])
        self.assertEqual(1, details["secrets-live-gate"]["returncode"])
        self.assertEqual("pod env missing", details["secrets-live-gate"]["error"])

    def test_component_summary_report_rejects_unknown_gate_ids_from_stale_summary(self) -> None:
        profile = gate.load_profile(gate.PROFILE)
        evidence = {
            "profile": "REAL-K8S-LAB-A",
            "status": "component_live_failed",
            "component_gates": [
                {"id": "stale-live-gate", "passed": False, "missing_env": [], "returncode": 1},
            ],
        }

        with self.assertRaises(SystemExit):
            gate.component_summary_report(profile, evidence)

    def test_component_summary_report_fails_passed_gate_with_missing_evidence_output(self) -> None:
        profile = gate.load_profile(gate.PROFILE)
        with tempfile.TemporaryDirectory() as tmpdir:
            evidence = {
                "profile": "REAL-K8S-LAB-A",
                "status": "component_live",
                "summary": {"total": 1, "passed": 1, "failed": 0},
                "component_gates": [
                    {
                        "id": "secrets-live-gate",
                        "passed": True,
                        "missing_env": [],
                        "evidence_output": str(Path(tmpdir) / "secrets-live-gate.json"),
                    },
                ],
            }

            report = gate.component_summary_report(profile, evidence)

        self.assertEqual(["secrets-live-gate"], report["failed_gates"])
        self.assertEqual([], report["blocked_gates"])
        self.assertEqual(["secrets-live-gate"], [entry["id"] for entry in report["next_commands"]])
        self.assertEqual(1, len(report["gate_details"]))
        detail = report["gate_details"][0]
        self.assertEqual("secrets-live-gate", detail["id"])
        self.assertEqual("failed", detail["status"])
        self.assertIn("missing evidence output", detail["error"])

    def test_component_summary_report_fails_passed_gate_with_invalid_evidence_output(self) -> None:
        profile = gate.load_profile(gate.PROFILE)
        with tempfile.TemporaryDirectory() as tmpdir:
            evidence_output = Path(tmpdir) / "secrets-live-gate.json"
            evidence_output.write_text("not-json\n", encoding="utf-8")
            evidence = {
                "profile": "REAL-K8S-LAB-A",
                "status": "component_live",
                "summary": {"total": 1, "passed": 1, "failed": 0},
                "component_gates": [
                    {
                        "id": "secrets-live-gate",
                        "passed": True,
                        "missing_env": [],
                        "evidence_output": str(evidence_output),
                    },
                ],
            }

            report = gate.component_summary_report(profile, evidence)

        self.assertEqual(["secrets-live-gate"], report["failed_gates"])
        self.assertEqual(1, len(report["gate_details"]))
        self.assertIn("invalid evidence JSON", report["gate_details"][0]["error"])

    def test_main_component_report_reads_summary_and_writes_report(self) -> None:
        summary = {
            "profile": "REAL-K8S-LAB-A",
            "status": "component_live_failed",
            "summary": {"total": 2, "passed": 1, "failed": 1},
            "component_gates": [
                {"id": "vcluster-live-gate", "passed": True, "missing_env": []},
                {"id": "secrets-live-gate", "passed": False, "missing_env": [], "returncode": 1},
            ],
        }
        with tempfile.TemporaryDirectory() as tmpdir:
            summary_path = Path(tmpdir) / "component-live-summary.json"
            report_path = Path(tmpdir) / "component-report.json"
            summary_path.write_text(json.dumps(summary), encoding="utf-8")

            with patch(
                "sys.argv",
                [
                    "validate_real_k8s_profile.py",
                    "--component-report",
                    str(summary_path),
                    "--component-env-file",
                    "/tmp/ani-real-k8s.env",
                    "--component-evidence-dir",
                    str(Path(tmpdir) / "components"),
                    "--evidence-output",
                    str(report_path),
                ],
            ):
                with self.assertRaises(SystemExit):
                    gate.main()

            report = json.loads(report_path.read_text(encoding="utf-8"))
            self.assertEqual("component_report", report["status"])
            self.assertEqual(["secrets-live-gate"], report["failed_gates"])
            self.assertEqual([], report["blocked_gates"])
            self.assertEqual(["secrets-live-gate"], [entry["id"] for entry in report["next_commands"]])

    def test_main_component_report_exits_zero_when_no_unresolved_gates(self) -> None:
        summary = {
            "profile": "REAL-K8S-LAB-A",
            "status": "component_live",
            "summary": {"total": 1, "passed": 1, "failed": 0},
            "component_gates": [
                {"id": "secrets-live-gate", "passed": True, "missing_env": []},
            ],
        }
        with tempfile.TemporaryDirectory() as tmpdir:
            summary_path = Path(tmpdir) / "component-live-summary.json"
            report_path = Path(tmpdir) / "component-report.json"
            summary_path.write_text(json.dumps(summary), encoding="utf-8")

            with patch(
                "sys.argv",
                [
                    "validate_real_k8s_profile.py",
                    "--component-report",
                    str(summary_path),
                    "--evidence-output",
                    str(report_path),
                ],
            ):
                gate.main()

            report = json.loads(report_path.read_text(encoding="utf-8"))
            self.assertTrue(report["passed"])
            self.assertEqual([], report["unresolved_gates"])
            self.assertEqual([], report["failed_gates"])
            self.assertEqual([], report["blocked_gates"])
            self.assertEqual([], report["next_commands"])

    def test_main_component_report_rejects_stale_summary_gate_ids(self) -> None:
        summary = {
            "profile": "REAL-K8S-LAB-A",
            "status": "component_live_failed",
            "summary": {"total": 1, "passed": 0, "failed": 1},
            "component_gates": [
                {"id": "stale-live-gate", "passed": False, "missing_env": [], "returncode": 1},
            ],
        }
        with tempfile.TemporaryDirectory() as tmpdir:
            summary_path = Path(tmpdir) / "component-live-summary.json"
            report_path = Path(tmpdir) / "component-report.json"
            summary_path.write_text(json.dumps(summary), encoding="utf-8")

            with patch(
                "sys.argv",
                [
                    "validate_real_k8s_profile.py",
                    "--component-report",
                    str(summary_path),
                    "--evidence-output",
                    str(report_path),
                ],
            ):
                with self.assertRaises(SystemExit):
                    gate.main()

            self.assertFalse(report_path.exists())

    def test_main_writes_component_live_summary_when_requested(self) -> None:
        fake_evidence = {
            "profile": "REAL-K8S-LAB-A",
            "status": "component_live",
            "passed": True,
            "summary": {"total": 1, "passed": 1, "failed": 0},
            "component_gates": [{"id": "secrets-live-gate", "passed": True}],
        }
        with tempfile.TemporaryDirectory() as tmpdir:
            output = Path(tmpdir) / "component-live-summary.json"

            with patch.object(gate, "validate_component_live_gates", return_value=fake_evidence):
                with patch(
                    "sys.argv",
                    [
                        "validate_real_k8s_profile.py",
                        "--component-live",
                        "--component-evidence-dir",
                        str(Path(tmpdir) / "components"),
                        "--evidence-output",
                        str(output),
                    ],
                ):
                    gate.main()

            self.assertEqual(fake_evidence, json.loads(output.read_text(encoding="utf-8")))

    def test_main_writes_component_live_failure_summary_before_exiting(self) -> None:
        fake_evidence = {
            "profile": "REAL-K8S-LAB-A",
            "status": "component_live_failed",
            "passed": False,
            "summary": {"total": 2, "passed": 1, "failed": 1},
            "component_gates": [
                {"id": "vcluster-live-gate", "passed": True},
                {"id": "secrets-live-gate", "passed": False, "error": "kubectl unavailable"},
            ],
        }
        with tempfile.TemporaryDirectory() as tmpdir:
            output = Path(tmpdir) / "component-live-summary.json"

            with patch.object(gate, "validate_component_live_gates", return_value=fake_evidence):
                with patch(
                    "sys.argv",
                    [
                        "validate_real_k8s_profile.py",
                        "--component-live",
                        "--component-evidence-dir",
                        str(Path(tmpdir) / "components"),
                        "--evidence-output",
                        str(output),
                    ],
                ):
                    with self.assertRaises(SystemExit):
                        gate.main()

            self.assertEqual(fake_evidence, json.loads(output.read_text(encoding="utf-8")))


if __name__ == "__main__":
    unittest.main()
