#!/usr/bin/env python3
"""Validate Sprint 5 REAL-K8S-LAB-A real provider lab gate."""

from __future__ import annotations

import argparse
import json
import os
import re
import shlex
import shutil
import subprocess
import sys
from collections.abc import Mapping
from pathlib import Path
from typing import Any

import yaml


ROOT = Path(__file__).resolve().parents[1]
DOC_ROOT = ROOT.parent
PROFILE = ROOT / "deploy/real-k8s-lab/profile.yaml"
REQUIRED_COMPONENTS = {"kubernetes", "kube_ovn", "kubevirt", "vcluster"}
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
REQUIRED_DOC_TOKENS = [
    "REAL-K8S-LAB-A",
    "validate-real-k8s-profile",
    "Kube-OVN",
    "KubeVirt",
    "vCluster",
    "local profile",
]
ENV_NAME_PATTERN = re.compile(r"^[A-Z][A-Z0-9_]*$")


def fail(message: str) -> None:
    raise SystemExit(f"real k8s profile invalid: {message}")


def read(path: Path) -> str:
    return path.read_text(encoding="utf-8")


def load_profile(path: Path) -> dict[str, Any]:
    if not path.exists():
        fail(f"missing {path.relative_to(ROOT)}")
    with path.open(encoding="utf-8") as handle:
        data = yaml.safe_load(handle)
    if not isinstance(data, dict):
        fail(f"{path.relative_to(ROOT)} must be a YAML object")
    return data


def validate_contract(profile: dict[str, Any]) -> None:
    if profile.get("profile") != "REAL-K8S-LAB-A":
        fail("profile must be REAL-K8S-LAB-A")
    if profile.get("status") not in {"contract", "live", "production_like"}:
        fail("status must be contract, live or production_like")
    if int(profile.get("minimum_nodes", 0)) < 3:
        fail("minimum_nodes must be at least 3")
    components = profile.get("components")
    if not isinstance(components, dict):
        fail("components must be an object")
    missing = REQUIRED_COMPONENTS - set(components)
    if missing:
        fail(f"missing required components: {', '.join(sorted(missing))}")
    for name, component in components.items():
        if not isinstance(component, dict):
            fail(f"{name} component must be an object")
        if "purpose" not in component:
            fail(f"{name} must document purpose")
        checks = component.get("live_checks")
        if not isinstance(checks, list) or not checks:
            fail(f"{name} must define live_checks")
        for check in checks:
            if not isinstance(check, dict):
                fail(f"{name} live check must be an object")
            for field in ("id", "command", "pass_condition"):
                if not check.get(field):
                    fail(f"{name} live check missing {field}")
    contract_gates = profile.get("contract_gates")
    if not isinstance(contract_gates, list):
        fail("contract_gates must be a list")
    observed_gate_ids = set()
    makefile = read(ROOT / "Makefile")
    for gate in contract_gates:
        if not isinstance(gate, dict):
            fail("contract gate must be an object")
        for field in ("id", "profile", "command", "manifest", "validator_script"):
            if not gate.get(field):
                fail(f"contract gate missing {field}")
        required_env = gate.get("required_env")
        if not isinstance(required_env, list) or not required_env:
            fail(f"{gate['id']} required_env must be a non-empty list")
        for env_name in required_env:
            if not isinstance(env_name, str) or not env_name.strip():
                fail(f"{gate['id']} required_env entries must be non-empty strings")
        observed_gate_ids.add(gate["id"])
        command = gate["command"]
        if not isinstance(command, str) or not command.startswith("make validate-"):
            fail(f"{gate['id']} command must be a make validate-* target")
        target = command.removeprefix("make ").strip()
        if f"{target}:" not in makefile:
            fail(f"{gate['id']} command target {target} missing from Makefile")
        for field in ("manifest", "validator_script"):
            path = ROOT / str(gate[field])
            if not path.exists():
                fail(f"{gate['id']} {field} missing: {gate[field]}")
    missing_gates = REQUIRED_CONTRACT_GATE_IDS - observed_gate_ids
    if missing_gates:
        fail(f"missing contract gates: {', '.join(sorted(missing_gates))}")


def validate_docs() -> None:
    docs = {
        "CLAUDE.md": DOC_ROOT / "CLAUDE.md",
        "ANI-DOCS-INDEX.md": DOC_ROOT / "ANI-DOCS-INDEX.md",
        "ANI-06-开发计划.md": DOC_ROOT / "ANI-06-开发计划.md",
        "CURRENT-SPRINT.md": ROOT / "CURRENT-SPRINT.md",
        "development-records/README.md": ROOT / "development-records/README.md",
    }
    for label, path in docs.items():
        content = read(path)
        for token in REQUIRED_DOC_TOKENS:
            if token not in content:
                fail(f"{label} must reference {token}")


def kubectl(args: list[str], kubeconfig: str | None) -> subprocess.CompletedProcess[str]:
    command = ["kubectl", *args]
    env = os.environ.copy()
    if kubeconfig:
        env["KUBECONFIG"] = kubeconfig
    return subprocess.run(command, env=env, text=True, capture_output=True, check=False)


def run_json_check(command: str, kubeconfig: str | None) -> Any:
    if not command.startswith("kubectl "):
        fail(f"live command must start with kubectl: {command}")
    result = kubectl(command.split()[1:], kubeconfig)
    if result.returncode != 0:
        fail(f"{command} failed: {result.stderr.strip() or result.stdout.strip()}")
    try:
        return json.loads(result.stdout)
    except json.JSONDecodeError as err:
        fail(f"{command} did not return JSON: {err}")


def condition_passed(condition: str, command: str, kubeconfig: str | None, minimum_nodes: int) -> bool:
    if condition == "stdout_yes":
        result = kubectl(command.split()[1:], kubeconfig)
        return result.returncode == 0 and result.stdout.strip().lower() == "yes"

    data = run_json_check(command, kubeconfig)
    if condition == "at_least_minimum_nodes_ready":
        items = data.get("items", [])
        ready = 0
        for node in items:
            conditions = node.get("status", {}).get("conditions", [])
            if any(item.get("type") == "Ready" and item.get("status") == "True" for item in conditions):
                ready += 1
        return ready >= minimum_nodes
    if condition == "at_least_one_storageclass":
        return len(data.get("items", [])) >= 1
    if condition == "crd_exists":
        return bool(data.get("metadata", {}).get("name"))
    if condition == "at_least_one_item":
        return len(data.get("items", [])) >= 1
    fail(f"unsupported pass_condition {condition}")


def validate_live(profile: dict[str, Any], kubeconfig: str | None) -> dict[str, Any]:
    if shutil.which("kubectl") is None:
        fail("kubectl is required for --live")
    minimum_nodes = int(profile.get("minimum_nodes", 3))
    evidence: dict[str, Any] = {
        "profile": profile["profile"],
        "status": "live",
        "minimum_nodes": minimum_nodes,
        "kubeconfig_provided": bool(kubeconfig),
        "checks": [],
    }
    for component_name, component in profile["components"].items():
        if not component.get("required", False):
            continue
        for check in component.get("live_checks", []):
            if not condition_passed(check["pass_condition"], check["command"], kubeconfig, minimum_nodes):
                fail(f"{component_name}/{check['id']} did not satisfy {check['pass_condition']}")
            evidence["checks"].append(
                {
                    "component": component_name,
                    "id": check["id"],
                    "command": check["command"],
                    "pass_condition": check["pass_condition"],
                    "passed": True,
                }
            )
    return evidence


def component_gate_evidence_error(path: Path) -> str:
    try:
        data = json.loads(path.read_text(encoding="utf-8"))
    except json.JSONDecodeError as err:
        return f"invalid evidence JSON: {err}"
    if not isinstance(data, dict):
        return "invalid evidence JSON: root must be an object"
    if data.get("status") == "passed" or data.get("passed") is True:
        return ""
    return "non-passing evidence JSON: expected status=passed or passed=true"


def validate_component_live_gates(
    profile: dict[str, Any],
    evidence_dir: Path,
    runner: Any | None = None,
    env: Mapping[str, str] | None = None,
) -> dict[str, Any]:
    if env is None:
        env = os.environ
    command_env = os.environ.copy()
    command_env.update({name: str(value) for name, value in env.items()})
    runner = runner or (
        lambda command: subprocess.run(command, env=command_env, text=True, capture_output=True, check=False)
    )
    evidence_dir.mkdir(parents=True, exist_ok=True)
    evidence: dict[str, Any] = {
        "profile": profile["profile"],
        "status": "component_live",
        "passed": True,
        "summary": {"total": 0, "passed": 0, "failed": 0},
        "component_gates": [],
    }
    for gate in profile.get("contract_gates", []):
        gate_id = str(gate["id"])
        validator = ROOT / str(gate["validator_script"])
        gate_evidence = evidence_dir / f"{gate_id}.json"
        command = [sys.executable, str(validator), "--live", "--evidence-output", str(gate_evidence)]
        evidence["summary"]["total"] += 1
        required_env = [str(name) for name in gate.get("required_env", [])]
        missing_env = [name for name in required_env if not env.get(name, "").strip()]
        gate_result: dict[str, Any] = {
            "id": gate_id,
            "profile": gate["profile"],
            "validator_script": gate["validator_script"],
            "evidence_output": str(gate_evidence),
            "required_env": required_env,
            "missing_env": missing_env,
            "passed": False,
        }
        evidence["component_gates"].append(gate_result)
    blocked = [gate for gate in evidence["component_gates"] if gate["missing_env"]]
    if blocked:
        evidence["passed"] = False
        evidence["status"] = "component_live_preflight_failed"
        evidence["summary"] = {
            "total": evidence["summary"]["total"],
            "passed": 0,
            "failed": 0,
            "blocked": len(blocked),
        }
        return evidence

    for gate_result, gate in zip(evidence["component_gates"], profile.get("contract_gates", []), strict=True):
        gate_id = str(gate["id"])
        gate_evidence = evidence_dir / f"{gate_id}.json"
        validator = ROOT / str(gate["validator_script"])
        command = [sys.executable, str(validator), "--live", "--evidence-output", str(gate_evidence)]
        result = runner(command)
        if result.returncode != 0:
            detail = (result.stderr or result.stdout or "").strip()
            evidence["passed"] = False
            evidence["status"] = "component_live_failed"
            evidence["summary"]["failed"] += 1
            gate_result["returncode"] = result.returncode
            gate_result["error"] = detail
        elif not gate_evidence.exists():
            evidence["passed"] = False
            evidence["status"] = "component_live_failed"
            evidence["summary"]["failed"] += 1
            gate_result["returncode"] = result.returncode
            gate_result["error"] = f"missing evidence output: {gate_evidence}"
        elif evidence_error := component_gate_evidence_error(gate_evidence):
            evidence["passed"] = False
            evidence["status"] = "component_live_failed"
            evidence["summary"]["failed"] += 1
            gate_result["returncode"] = result.returncode
            gate_result["error"] = evidence_error
        else:
            gate_result["passed"] = True
            evidence["summary"]["passed"] += 1
    return evidence


def validate_component_live_preflight(profile: dict[str, Any], env: Mapping[str, str] | None = None) -> dict[str, Any]:
    if env is None:
        env = os.environ
    evidence: dict[str, Any] = {
        "profile": profile["profile"],
        "status": "component_live_preflight_passed",
        "passed": True,
        "summary": {"total": 0, "passed": 0, "blocked": 0},
        "component_gates": [],
    }
    for gate in profile.get("contract_gates", []):
        required_env = [str(name) for name in gate.get("required_env", [])]
        missing_env = [name for name in required_env if not env.get(name, "").strip()]
        gate_passed = not missing_env
        evidence["summary"]["total"] += 1
        if gate_passed:
            evidence["summary"]["passed"] += 1
        else:
            evidence["passed"] = False
            evidence["status"] = "component_live_preflight_failed"
            evidence["summary"]["blocked"] += 1
        evidence["component_gates"].append(
            {
                "id": str(gate["id"]),
                "profile": gate["profile"],
                "required_env": required_env,
                "missing_env": missing_env,
                "passed": gate_passed,
            }
        )
    return evidence


def failed_component_gate_ids(evidence: dict[str, Any]) -> list[str]:
    return [str(gate["id"]) for gate in evidence.get("component_gates", []) if not gate.get("passed")]


def component_gate_command(
    mode: str,
    gate_id: str,
    component_env_file: str = "",
    component_evidence_dir: str = "",
) -> str:
    command = ["python", "scripts/validate_real_k8s_profile.py", mode, "--component-gate", gate_id]
    if component_env_file:
        command.extend(["--component-env-file", component_env_file])
    if mode == "--component-live" and component_evidence_dir:
        command.extend(["--component-evidence-dir", component_evidence_dir])
    suffix = "preflight-summary" if mode == "--component-preflight" else "live-summary"
    command.extend(["--evidence-output", f"repo/development-records/live/{gate_id}-{suffix}.json"])
    return shlex.join(command)


def validate_component_summary_gate_ids(profile: dict[str, Any], evidence: dict[str, Any]) -> None:
    known_gate_ids = {str(gate["id"]) for gate in profile.get("contract_gates", [])}
    unknown_gate_ids: list[str] = []
    component_gates = evidence.get("component_gates", [])
    if not isinstance(component_gates, list):
        fail("component summary component_gates must be a list")
    for gate in component_gates:
        if not isinstance(gate, dict):
            fail("component summary gate entries must be objects")
        gate_id = str(gate.get("id", ""))
        if not gate_id:
            fail("component summary gate missing id")
        if gate_id not in known_gate_ids:
            unknown_gate_ids.append(gate_id)
    if unknown_gate_ids:
        fail(f"component summary references unknown gate ids: {', '.join(sorted(set(unknown_gate_ids)))}")


def component_summary_report(
    profile: dict[str, Any],
    evidence: dict[str, Any],
    component_env_file: str = "",
    component_evidence_dir: str = "",
) -> dict[str, Any]:
    validate_component_summary_gate_ids(profile, evidence)
    failed_gates: list[str] = []
    blocked_gates: list[str] = []
    gate_details: list[dict[str, Any]] = []
    for gate in evidence.get("component_gates", []):
        gate_id = str(gate["id"])
        if gate.get("passed"):
            evidence_output = str(gate.get("evidence_output", "")).strip()
            if not evidence_output:
                continue
            evidence_path = Path(evidence_output)
            if not evidence_path.exists():
                evidence_error = f"missing evidence output: {evidence_path}"
            else:
                evidence_error = component_gate_evidence_error(evidence_path)
            if not evidence_error:
                continue
            failed_gates.append(gate_id)
            detail = {
                "id": gate_id,
                "status": "failed",
                "missing_env": [str(name) for name in gate.get("missing_env", [])],
                "error": evidence_error,
            }
            if "returncode" in gate:
                detail["returncode"] = gate["returncode"]
            gate_details.append(detail)
        elif gate.get("missing_env"):
            blocked_gates.append(gate_id)
            detail = {
                "id": gate_id,
                "status": "blocked",
                "missing_env": [str(name) for name in gate.get("missing_env", [])],
            }
            if "returncode" in gate:
                detail["returncode"] = gate["returncode"]
            if gate.get("error"):
                detail["error"] = str(gate["error"])
            gate_details.append(detail)
        else:
            failed_gates.append(gate_id)
            detail = {
                "id": gate_id,
                "status": "failed",
                "missing_env": [str(name) for name in gate.get("missing_env", [])],
            }
            if "returncode" in gate:
                detail["returncode"] = gate["returncode"]
            if gate.get("error"):
                detail["error"] = str(gate["error"])
            gate_details.append(detail)
    next_gate_ids = list(dict.fromkeys([*blocked_gates, *failed_gates]))
    return {
        "profile": evidence.get("profile", "REAL-K8S-LAB-A"),
        "status": "component_report",
        "passed": not next_gate_ids,
        "source_status": evidence.get("status", ""),
        "summary": evidence.get("summary", {}),
        "failed_gates": failed_gates,
        "blocked_gates": blocked_gates,
        "unresolved_gates": next_gate_ids,
        "gate_details": gate_details,
        "next_commands": [
            {
                "id": gate_id,
                "preflight": component_gate_command(
                    "--component-preflight",
                    gate_id,
                    component_env_file=component_env_file,
                ),
                "live": component_gate_command(
                    "--component-live",
                    gate_id,
                    component_env_file=component_env_file,
                    component_evidence_dir=component_evidence_dir,
                ),
            }
            for gate_id in next_gate_ids
        ],
    }


def load_component_summary(path: Path) -> dict[str, Any]:
    if not path.exists():
        fail(f"component summary missing: {path}")
    try:
        data = json.loads(path.read_text(encoding="utf-8"))
    except json.JSONDecodeError as err:
        fail(f"component summary is not valid JSON: {err}")
    if not isinstance(data, dict):
        fail("component summary must be a JSON object")
    return data


def write_live_evidence(path: Path, evidence: dict[str, Any]) -> None:
    path.parent.mkdir(parents=True, exist_ok=True)
    path.write_text(json.dumps(evidence, ensure_ascii=False, indent=2, sort_keys=True) + "\n", encoding="utf-8")


def select_component_contract_gates(profile: dict[str, Any], gate_ids: list[str] | None) -> dict[str, Any]:
    if not gate_ids:
        return profile
    selected_ids = list(dict.fromkeys(gate_ids))
    gates_by_id = {str(gate["id"]): gate for gate in profile.get("contract_gates", [])}
    missing = [gate_id for gate_id in selected_ids if gate_id not in gates_by_id]
    if missing:
        fail(f"unknown component gate: {', '.join(missing)}")
    selected_profile = dict(profile)
    selected_profile["contract_gates"] = [gates_by_id[gate_id] for gate_id in selected_ids]
    return selected_profile


def component_required_env_names(profile: dict[str, Any]) -> list[str]:
    names: set[str] = set()
    for gate in profile.get("contract_gates", []):
        for name in gate.get("required_env", []):
            names.add(str(name))
    return sorted(names)


def component_env_template(profile: dict[str, Any]) -> str:
    lines = [
        "# REAL-K8S-LAB-A component live required environment",
        "# Fill these values before running:",
        "# python scripts/validate_real_k8s_profile.py --component-preflight --component-env-file <this-file> --evidence-output repo/development-records/live/real-k8s-component-preflight.json",
        "# python scripts/validate_real_k8s_profile.py --component-live --component-env-file <this-file> --component-evidence-dir repo/development-records/live/component-gates --evidence-output repo/development-records/live/real-k8s-component-summary.json",
        "",
    ]
    for name in component_required_env_names(profile):
        lines.append(f'export {name}=""')
    lines.extend(["", "# Component gate env mapping"])
    for gate in profile.get("contract_gates", []):
        required_env = ", ".join(str(name) for name in gate.get("required_env", []))
        lines.append(f"# {gate['id']}: {required_env}")
    return "\n".join(lines) + "\n"


def write_component_env_template(path: Path, profile: dict[str, Any]) -> None:
    path.parent.mkdir(parents=True, exist_ok=True)
    path.write_text(component_env_template(profile), encoding="utf-8")


def parse_component_env_assignment(line: str, path: Path, line_number: int) -> tuple[str, str] | None:
    stripped = line.strip()
    if not stripped or stripped.startswith("#"):
        return None
    if stripped.startswith("export "):
        stripped = stripped.removeprefix("export ").strip()
    if "=" not in stripped:
        fail(f"{path}:{line_number} must be an env assignment")
    name, raw_value = stripped.split("=", 1)
    name = name.strip()
    raw_value = raw_value.strip()
    if not ENV_NAME_PATTERN.match(name):
        fail(f"{path}:{line_number} has invalid env name {name!r}")
    if raw_value == "":
        return name, ""
    try:
        tokens = shlex.split(raw_value, posix=True)
    except ValueError as err:
        fail(f"{path}:{line_number} has invalid quoted value: {err}")
    if len(tokens) != 1:
        fail(f"{path}:{line_number} env value must be one shell-quoted token")
    return name, tokens[0]


def load_component_env_file(path: Path) -> dict[str, str]:
    if not path.exists():
        fail(f"component env file missing: {path}")
    env: dict[str, str] = {}
    for line_number, line in enumerate(path.read_text(encoding="utf-8").splitlines(), 1):
        assignment = parse_component_env_assignment(line, path, line_number)
        if assignment is None:
            continue
        name, value = assignment
        env[name] = value
    return env


def merged_component_env(component_env_file: str) -> dict[str, str]:
    env = os.environ.copy()
    if component_env_file:
        env.update(load_component_env_file(Path(component_env_file)))
    return env


def main() -> int:
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("--profile", default=str(PROFILE), help="real lab profile YAML")
    parser.add_argument("--live", action="store_true", help="run live kubectl checks")
    parser.add_argument("--component-live", action="store_true", help="run indexed component live gates")
    parser.add_argument("--component-preflight", action="store_true", help="check component live required env without running live gates")
    parser.add_argument(
        "--component-evidence-dir",
        default=os.getenv("ANI_REAL_K8S_COMPONENT_EVIDENCE_DIR", str(ROOT / "development-records/live/component-gates")),
        help="directory for per-component --live evidence JSON files",
    )
    parser.add_argument("--kubeconfig", default=os.getenv("KUBECONFIG"), help="kubeconfig for live checks")
    parser.add_argument(
        "--evidence-output",
        default=os.getenv("ANI_REAL_K8S_EVIDENCE_OUTPUT", ""),
        help="write --live evidence JSON to this path",
    )
    parser.add_argument(
        "--component-env-template-output",
        default=os.getenv("ANI_REAL_K8S_COMPONENT_ENV_TEMPLATE_OUTPUT", ""),
        help="write a fillable shell env template for --component-live",
    )
    parser.add_argument(
        "--component-env-file",
        default=os.getenv("ANI_REAL_K8S_COMPONENT_ENV_FILE", ""),
        help="load a filled component env template for --component-live without shell sourcing it",
    )
    parser.add_argument(
        "--component-gate",
        action="append",
        default=[],
        help="limit --component-live or --component-preflight to one indexed contract gate id; repeat for multiple gates",
    )
    parser.add_argument(
        "--component-report",
        default="",
        help="read a component live/preflight summary JSON and output failed/blocked gate rerun commands",
    )
    args = parser.parse_args()

    profile = load_profile(Path(args.profile))
    validate_contract(profile)
    validate_docs()
    selected_modes = [
        bool(args.live),
        bool(args.component_live),
        bool(args.component_preflight),
        bool(args.component_env_template_output),
        bool(args.component_report),
    ]
    if sum(selected_modes) > 1:
        fail("--live, --component-live, --component-preflight, --component-env-template-output and --component-report must be run separately")
    if args.component_env_file and not (args.component_live or args.component_preflight or args.component_report):
        fail("--component-env-file requires --component-live, --component-preflight or --component-report")
    if args.component_gate and not (args.component_live or args.component_preflight):
        fail("--component-gate requires --component-live or --component-preflight")
    if args.live:
        evidence = validate_live(profile, args.kubeconfig)
        if args.evidence_output:
            write_live_evidence(Path(args.evidence_output), evidence)
            print(f"REAL-K8S-LAB-A live checks valid; evidence written to {args.evidence_output}")
        else:
            print("REAL-K8S-LAB-A live checks valid")
    elif args.component_preflight:
        component_profile = select_component_contract_gates(profile, args.component_gate)
        evidence = validate_component_live_preflight(component_profile, merged_component_env(args.component_env_file))
        if args.evidence_output:
            write_live_evidence(Path(args.evidence_output), evidence)
            print(f"REAL-K8S-LAB-A component live preflight evidence written to {args.evidence_output}")
        else:
            print(json.dumps(evidence, ensure_ascii=False, sort_keys=True))
        failed_gates = failed_component_gate_ids(evidence)
        if failed_gates:
            fail(f"component live preflight failed: {', '.join(failed_gates)}")
        print("REAL-K8S-LAB-A component live preflight valid")
    elif args.component_live:
        component_profile = select_component_contract_gates(profile, args.component_gate)
        evidence = validate_component_live_gates(
            component_profile,
            Path(args.component_evidence_dir),
            env=merged_component_env(args.component_env_file),
        )
        if args.evidence_output:
            write_live_evidence(Path(args.evidence_output), evidence)
            print(f"REAL-K8S-LAB-A component live gates evidence written to {args.evidence_output}")
        else:
            print(json.dumps(evidence, ensure_ascii=False, sort_keys=True))
        failed_gates = failed_component_gate_ids(evidence)
        if failed_gates:
            fail(f"component live gates failed: {', '.join(failed_gates)}")
        print("REAL-K8S-LAB-A component live gates valid")
    elif args.component_env_template_output:
        write_component_env_template(Path(args.component_env_template_output), profile)
        print(f"REAL-K8S-LAB-A component env template written to {args.component_env_template_output}")
    elif args.component_report:
        report = component_summary_report(
            profile,
            load_component_summary(Path(args.component_report)),
            component_env_file=args.component_env_file,
            component_evidence_dir=args.component_evidence_dir,
        )
        if args.evidence_output:
            write_live_evidence(Path(args.evidence_output), report)
            print(f"REAL-K8S-LAB-A component report written to {args.evidence_output}")
        else:
            print(json.dumps(report, ensure_ascii=False, sort_keys=True))
        unresolved_gates = report["unresolved_gates"]
        if unresolved_gates:
            fail(f"component report has unresolved gates: {', '.join(unresolved_gates)}")
    else:
        print("REAL-K8S-LAB-A contract valid; use --live with KUBECONFIG to verify a real lab")
    return 0


if __name__ == "__main__":
    raise SystemExit(main())
