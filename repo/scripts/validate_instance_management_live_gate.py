#!/usr/bin/env python3
"""Validate instance management live gate through ANI Core /instances APIs."""

from __future__ import annotations

import argparse
import datetime as dt
import json
import os
import shutil
import subprocess
import time
import urllib.error
import urllib.parse
import urllib.request
from dataclasses import dataclass
from pathlib import Path
from typing import Any

import yaml


ROOT = Path(__file__).resolve().parents[1]
DOC_ROOT = ROOT.parent
DEFAULT_GATE = ROOT / "deploy/real-k8s-lab/instance-management-live-gate.yaml"
PROFILE = "INSTANCE-MANAGEMENT-LIVE-GATE-A"
GATE_ID = "instance-management-live-gate"
COMMAND_TIMEOUT_SECONDS = 120
REQUIRED_CHECKS = {
    "core-instance-vm-create",
    "core-instance-vm-running",
    "kubevirt-vm-read-observe",
    "kubevirt-vmi-read-observe",
    "core-instance-vm-console-vnc",
    "core-instance-vm-stop",
    "core-instance-vm-start",
    "core-instance-vm-delete",
}
REQUIRED_DOC_TOKENS = [
    PROFILE,
    "validate-instance-management-live-gate",
    "Core /api/v1/instances",
]


def fail(message: str) -> None:
    raise SystemExit(f"instance management live gate invalid: {message}")


def load_gate(path: Path) -> dict[str, Any]:
    if not path.exists():
        fail(f"missing {path}")
    try:
        data = yaml.safe_load(path.read_text(encoding="utf-8"))
    except yaml.YAMLError as err:
        fail(f"malformed {path}: {err}")
    if not isinstance(data, dict):
        fail(f"{path} must be a YAML object")
    return data


def validate_contract(document: dict[str, Any]) -> None:
    if document.get("profile") != PROFILE:
        fail(f"profile must be {PROFILE}")
    if document.get("status") not in {"contract", "live"}:
        fail("status must be contract or live")
    tools = document.get("required_tools")
    if not isinstance(tools, list) or "kubectl" not in tools:
        fail("required_tools must include kubectl")
    endpoints = document.get("required_endpoints")
    required_endpoints = {"ani_core_instances_api", "kubernetes_read_api", "kubevirt_read_api"}
    if not isinstance(endpoints, list) or required_endpoints - set(endpoints):
        fail("required_endpoints must include Core instances API and read-only Kubernetes/KubeVirt APIs")
    checks = document.get("live_checks")
    if not isinstance(checks, list):
        fail("live_checks must be a list")
    check_ids = {check.get("id") for check in checks if isinstance(check, dict)}
    missing = REQUIRED_CHECKS - check_ids
    if missing:
        fail(f"missing live checks: {', '.join(sorted(missing))}")
    for check in checks:
        if not isinstance(check, dict):
            fail("live check must be an object")
        for field in ("id", "command", "pass_condition"):
            value = check.get(field)
            if not isinstance(value, str) or not value.strip():
                fail(f"live check {field} must be a non-empty string")


def validate_docs() -> None:
    docs = {
        "CURRENT-SPRINT.md": ROOT / "CURRENT-SPRINT.md",
        "ANI-06-开发计划.md": DOC_ROOT / "ANI-06-开发计划.md",
        "development-records/README.md": ROOT / "development-records/README.md",
    }
    for label, path in docs.items():
        try:
            content = path.read_text(encoding="utf-8")
        except OSError:
            fail(f"unreadable doc {label}")
        for token in REQUIRED_DOC_TOKENS:
            if token not in content:
                fail(f"{label} must reference {token}")


def validate_evidence_output(path: str) -> None:
    if not path.strip() or path != path.strip():
        fail("evidence_output must be a non-empty path without surrounding whitespace")
    output = Path(path)
    if output.is_dir():
        fail("evidence_output must be a file path")
    if output.parent.exists() and not output.parent.is_dir():
        fail("evidence_output parent must be a directory")
    output.parent.mkdir(parents=True, exist_ok=True)


@dataclass(frozen=True)
class LiveConfig:
    gateway_url: str
    ani_bearer_token: str
    tenant_id: str
    kind: str = "vm"
    name: str = "ani-instance-vm-live"
    image_ref: str = ""
    idempotency_key: str = "instance-management-vm-live"
    namespace: str = ""
    cpu: str = "1"
    memory: str = "512Mi"
    ssh_username: str = "ubuntu"
    kubeconfig: str = ""
    kubectl_binary: str = "kubectl"
    poll_attempts: int = 30
    poll_interval_seconds: float = 5.0


class LiveRunner:
    def run(self, command: list[str], input_text: str | None = None) -> str:
        result = subprocess.run(
            command,
            input=input_text,
            text=True,
            capture_output=True,
            check=False,
            timeout=COMMAND_TIMEOUT_SECONDS,
        )
        if result.returncode != 0:
            detail = result.stderr.strip() or result.stdout.strip()
            raise RuntimeError(f"{' '.join(command)} failed: {detail}")
        return result.stdout


class HTTPClient:
    def request(
        self,
        method: str,
        url: str,
        token: str,
        tenant_id: str,
        body: dict[str, object] | None = None,
    ) -> tuple[int, dict[str, Any]]:
        payload = None
        headers = {"Accept": "application/json", "Authorization": f"Bearer {token}", "X-Dev-Tenant-ID": tenant_id}
        if body is not None:
            payload = json.dumps(body).encode("utf-8")
            headers["Content-Type"] = "application/json"
        request = urllib.request.Request(url, data=payload, headers=headers, method=method)
        try:
            with urllib.request.urlopen(request, timeout=COMMAND_TIMEOUT_SECONDS) as response:
                raw = response.read().decode("utf-8")
                return response.status, json.loads(raw) if raw else {}
        except urllib.error.HTTPError as err:
            raw = err.read().decode("utf-8", errors="replace")
            raise RuntimeError(f"{method} {url} failed: HTTP {err.code} {raw or err.reason}") from err
        except urllib.error.URLError as err:
            raise RuntimeError(f"{method} {url} failed: {err.reason}") from err


def gateway_url(config: LiveConfig, path: str) -> str:
    return config.gateway_url.rstrip("/") + path


def kubectl(config: LiveConfig, args: list[str]) -> list[str]:
    command = [config.kubectl_binary]
    if config.kubeconfig.strip():
        command.extend(["--kubeconfig", config.kubeconfig.strip()])
    command.extend(args)
    return command


def tenant_namespace(config: LiveConfig) -> str:
    if config.namespace.strip():
        return config.namespace.strip()
    return "ani-tenant-" + config.tenant_id.replace("_", "-")


def validate_live_config(config: LiveConfig) -> None:
    required = {
        "gateway_url": config.gateway_url,
        "ani_bearer_token": config.ani_bearer_token,
        "tenant_id": config.tenant_id,
        "kind": config.kind,
        "name": config.name,
        "image_ref": config.image_ref,
        "idempotency_key": config.idempotency_key,
    }
    display_names = {
        "gateway_url": "gateway-url",
        "ani_bearer_token": "ani-bearer-token",
        "tenant_id": "tenant-id",
        "image_ref": "image-ref",
        "idempotency_key": "idempotency-key",
    }
    missing = [display_names.get(name, name) for name, value in required.items() if not str(value).strip()]
    if missing:
        fail(f"live mode requires {', '.join(missing)}")
    if config.kind != "vm":
        fail("this live gate currently supports kind=vm only; sandbox is intentionally separate")
    if shutil.which(config.kubectl_binary) is None:
        fail(f"{config.kubectl_binary} is required for --live")


def require_instance(document: dict[str, Any], label: str) -> dict[str, Any]:
    if "instance" in document and isinstance(document["instance"], dict):
        return document["instance"]
    if "id" in document:
        return document
    fail(f"{label} must return an instance object")


def instance_id(instance: dict[str, Any], label: str) -> str:
    value = instance.get("id") or instance.get("instance_id")
    if not isinstance(value, str) or not value.strip():
        fail(f"{label} must include instance id")
    return value


def require_real_provider(instance: dict[str, Any], label: str) -> None:
    profile = instance.get("dev_profile")
    provider = instance.get("provider")
    if isinstance(profile, dict) and profile.get("real_provider") is True:
        return
    if provider in {"kubernetes", "kubernetes_rest", "kubevirt"}:
        return
    fail(f"{label} must return real provider instance metadata")


def wait_for_state(
    config: LiveConfig,
    http_client: HTTPClient,
    instance_id_value: str,
    states: set[str],
) -> dict[str, Any]:
    last: dict[str, Any] | None = None
    for _ in range(max(1, config.poll_attempts)):
        status, document = http_client.request(
            "GET",
            gateway_url(config, f"/instances/{urllib.parse.quote(instance_id_value)}"),
            config.ani_bearer_token,
            config.tenant_id,
        )
        if status != 200:
            fail("instance get must return 200")
        last = require_instance(document, "instance get")
        if str(last.get("state", "")).lower() in states:
            return last
        time.sleep(config.poll_interval_seconds)
    fail(f"instance {instance_id_value} did not reach one of {sorted(states)}; last={last}")


def operation_id(document: dict[str, Any]) -> str:
    value = document.get("operation_id")
    return value if isinstance(value, str) else ""


def observe_kubevirt(config: LiveConfig, runner: LiveRunner, _instance_id_value: str) -> tuple[dict[str, Any], dict[str, Any]]:
    # KubeVirt object name follows the Core instance name, not the opaque instance_id.
    namespace = tenant_namespace(config)
    vm_name = config.name.strip()
    if not vm_name:
        fail("KubeVirt read observe requires instance name")
    vm_raw = runner.run(kubectl(config, ["-n", namespace, "get", "virtualmachines", vm_name, "-o", "json"]))
    vmi_raw = runner.run(kubectl(config, ["-n", namespace, "get", "virtualmachineinstances", vm_name, "-o", "json"]))
    try:
        vm = json.loads(vm_raw)
        vmi = json.loads(vmi_raw)
    except json.JSONDecodeError as err:
        fail(f"KubeVirt read observe must return JSON: {err}")
    if not isinstance(vm, dict) or not isinstance(vmi, dict):
        fail("KubeVirt read observe must return JSON objects")
    if not vmi.get("status", {}).get("phase"):
        fail("VMI observe must include status.phase")
    return vm, vmi


def console_probe(config: LiveConfig, http_client: HTTPClient, instance_id_value: str, protocol: str) -> dict[str, Any]:
    status, document = http_client.request(
        "POST",
        gateway_url(config, f"/instances/{urllib.parse.quote(instance_id_value)}/console"),
        config.ani_bearer_token,
        config.tenant_id,
        {"protocol": protocol},
    )
    if status != 200:
        fail(f"{protocol} console session must return 200")
    if not document.get("connect_url") or not document.get("url"):
        fail(f"{protocol} console session must return connect_url and url")
    return document


def lifecycle(
    config: LiveConfig,
    http_client: HTTPClient,
    instance_id_value: str,
    action: str,
) -> tuple[dict[str, Any], str]:
    status, document = http_client.request(
        "POST",
        gateway_url(config, f"/instances/{urllib.parse.quote(instance_id_value)}/lifecycle"),
        config.ani_bearer_token,
        config.tenant_id,
        {"action": action, "idempotency_key": f"{config.idempotency_key}-{action}"},
    )
    if status != 200:
        fail(f"lifecycle {action} must return 200")
    return require_instance(document, f"lifecycle {action}"), operation_id(document)


def run_live(
    config: LiveConfig,
    http_client: HTTPClient | None = None,
    runner: LiveRunner | None = None,
) -> dict[str, object]:
    validate_live_config(config)
    http_client = http_client or HTTPClient()
    runner = runner or LiveRunner()
    create_status, created = http_client.request(
        "POST",
        gateway_url(config, "/instances"),
        config.ani_bearer_token,
        config.tenant_id,
        {
            "idempotency_key": config.idempotency_key + "-create",
            "name": config.name,
            "kind": "vm",
            "image_ref": config.image_ref,
            "cpu": config.cpu,
            "memory": config.memory,
            "auto_start": True,
            "vm_config": {"ssh_username": config.ssh_username},
        },
    )
    if create_status != 201:
        fail("VM create must return 201")
    instance = require_instance(created, "VM create")
    require_real_provider(instance, "VM create")
    vm_id = instance_id(instance, "VM create")
    create_operation = operation_id(created)
    running = wait_for_state(config, http_client, vm_id, {"running"})
    vm, vmi = observe_kubevirt(config, runner, vm_id)
    vnc = console_probe(config, http_client, vm_id, "vnc")
    serial = console_probe(config, http_client, vm_id, "console")
    stopped, stop_operation = lifecycle(config, http_client, vm_id, "stop")
    started, start_operation = lifecycle(config, http_client, vm_id, "start")
    deleted, delete_operation = lifecycle(config, http_client, vm_id, "delete")
    return {
        "status": "passed",
        "kind": "vm",
        "instance_id": vm_id,
        "create_operation_id": create_operation,
        "state_after_create": running.get("state"),
        "state_after_stop": stopped.get("state"),
        "state_after_start": started.get("state"),
        "state_after_delete": deleted.get("state"),
        "stop_operation_id": stop_operation,
        "start_operation_id": start_operation,
        "delete_operation_id": delete_operation,
        "console_protocols": [vnc.get("protocol"), serial.get("protocol")],
        "kubevirt_vm_observed": bool(vm.get("metadata", {}).get("name")),
        "kubevirt_vmi_phase": vmi.get("status", {}).get("phase"),
        "kubevirt_vmi_node_observed": bool(vmi.get("status", {}).get("nodeName")),
        "write_path": "Core /api/v1/instances",
        "kubernetes_write_observe": "read-only",
    }


def write_evidence(path: Path, evidence: dict[str, object]) -> None:
    identified = {
        "id": GATE_ID,
        "profile": PROFILE,
        "generated_at": dt.datetime.now(dt.timezone.utc).isoformat().replace("+00:00", "Z"),
        **evidence,
    }
    path.write_text(json.dumps(identified, ensure_ascii=False, indent=2, sort_keys=True) + "\n", encoding="utf-8")


def main() -> int:
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("--gate", default=str(DEFAULT_GATE))
    parser.add_argument("--live", action="store_true")
    parser.add_argument("--kind", default=os.getenv("ANI_INSTANCE_LIVE_KIND", "vm"))
    parser.add_argument("--gateway-url", default=os.getenv("ANI_GATEWAY_URL", ""))
    parser.add_argument("--ani-bearer-token", default=os.getenv("ANI_BEARER_TOKEN", ""))
    parser.add_argument("--tenant-id", default=os.getenv("ANI_LIVE_TENANT_ID", "tenant-a"))
    parser.add_argument("--name", default=os.getenv("ANI_INSTANCE_LIVE_NAME", "ani-instance-vm-live"))
    parser.add_argument("--image-ref", default=os.getenv("ANI_INSTANCE_LIVE_VM_IMAGE_REF", ""))
    parser.add_argument("--idempotency-key", default=os.getenv("ANI_INSTANCE_LIVE_IDEMPOTENCY_KEY", "instance-management-vm-live"))
    parser.add_argument("--namespace", default=os.getenv("ANI_INSTANCE_LIVE_NAMESPACE", ""))
    parser.add_argument("--cpu", default=os.getenv("ANI_INSTANCE_LIVE_VM_CPU", "1"))
    parser.add_argument("--memory", default=os.getenv("ANI_INSTANCE_LIVE_VM_MEMORY", "512Mi"))
    parser.add_argument("--ssh-username", default=os.getenv("ANI_INSTANCE_LIVE_VM_SSH_USERNAME", "ubuntu"))
    parser.add_argument("--kubeconfig", default=os.getenv("KUBECONFIG", ""))
    parser.add_argument("--evidence-output", default=os.getenv("ANI_INSTANCE_MANAGEMENT_LIVE_EVIDENCE_OUTPUT") or None)
    args = parser.parse_args()

    document = load_gate(Path(args.gate))
    validate_contract(document)
    validate_docs()
    if not args.live:
        print("INSTANCE-MANAGEMENT-LIVE-GATE-A contract valid; use --live to validate VM through Core /api/v1/instances")
        return 0
    if args.evidence_output is not None:
        validate_evidence_output(args.evidence_output)
    evidence = run_live(
        LiveConfig(
            gateway_url=args.gateway_url,
            ani_bearer_token=args.ani_bearer_token,
            tenant_id=args.tenant_id,
            kind=args.kind,
            name=args.name,
            image_ref=args.image_ref,
            idempotency_key=args.idempotency_key,
            namespace=args.namespace,
            cpu=args.cpu,
            memory=args.memory,
            ssh_username=args.ssh_username,
            kubeconfig=args.kubeconfig,
        )
    )
    if args.evidence_output is not None:
        write_evidence(Path(args.evidence_output), evidence)
        print(f"INSTANCE-MANAGEMENT-LIVE-GATE-A live checks valid; evidence written to {args.evidence_output}")
    else:
        print(f"INSTANCE-MANAGEMENT-LIVE-GATE-A live checks valid: {json.dumps(evidence, sort_keys=True)}")
    return 0


if __name__ == "__main__":
    raise SystemExit(main())
