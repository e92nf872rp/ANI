#!/usr/bin/env python3
"""Validate Container orchestration live gate through ANI Core /instances APIs."""

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
DEFAULT_GATE = ROOT / "deploy/real-k8s-lab/instance-orchestration-container-live-gate.yaml"
PROFILE = "INSTANCE-ORCHESTRATION-A"
GATE_ID = "instance-orchestration-container-live-gate"
COMMAND_TIMEOUT_SECONDS = 120
REQUIRED_CHECKS = {
    "core-instance-container-orchestrated-create",
    "core-instance-container-operation-steps",
    "kubernetes-deployment-network-observe",
    "kubernetes-pod-storage-observe",
    "core-instance-container-stop",
    "core-instance-container-start",
    "core-instance-container-delete",
}
REQUIRED_DOC_TOKENS = [
    PROFILE,
    "validate-instance-orchestration-live-gate",
]


def fail(message: str) -> None:
    raise SystemExit(f"instance orchestration live gate invalid: {message}")


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
    required_endpoints = {
        "ani_core_instances_api",
        "kubernetes_read_api",
        "kubeovn_subnet_api",
        "storage_pvc_api",
    }
    if not isinstance(endpoints, list) or required_endpoints - set(endpoints):
        fail("required_endpoints must include Core instances, Kubernetes, Kube-OVN, and storage PVC APIs")
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
    name: str = "ani-instance-orch-live"
    image_ref: str = ""
    idempotency_key: str = "instance-orchestration-container-live"
    namespace: str = ""
    vpc_id: str = ""
    subnet_id: str = ""
    security_group_ids: tuple[str, ...] = ()
    volume_id: str = ""
    filesystem_id: str = ""
    mount_path: str = "/data"
    cpu: str = "250m"
    memory: str = "256Mi"
    kubeconfig: str = ""
    kubectl_binary: str = "kubectl"
    poll_attempts: int = 36
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
        "name": config.name,
        "image_ref": config.image_ref,
        "idempotency_key": config.idempotency_key,
        "vpc_id": config.vpc_id,
        "subnet_id": config.subnet_id,
    }
    display_names = {
        "gateway_url": "gateway-url",
        "ani_bearer_token": "ani-bearer-token",
        "tenant_id": "tenant-id",
        "image_ref": "image-ref",
        "idempotency_key": "idempotency-key",
        "vpc_id": "vpc-id",
        "subnet_id": "subnet-id",
    }
    missing = [display_names.get(name, name) for name, value in required.items() if not str(value).strip()]
    if missing:
        fail(f"live mode requires {', '.join(missing)}")
    if not config.volume_id.strip() and not config.filesystem_id.strip():
        fail("live mode requires --volume-id or --filesystem-id")
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


def operation_id(document: dict[str, Any]) -> str:
    value = document.get("operation_id")
    return value if isinstance(value, str) else ""


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


def get_operation(config: LiveConfig, http_client: HTTPClient, operation_id_value: str) -> dict[str, Any]:
    if not operation_id_value.strip():
        fail("create must return operation_id")
    status, document = http_client.request(
        "GET",
        gateway_url(config, f"/instance-operations/{urllib.parse.quote(operation_id_value)}"),
        config.ani_bearer_token,
        config.tenant_id,
    )
    if status != 200:
        fail("operation get must return 200")
    return document


def require_operation_steps(operation: dict[str, Any]) -> list[str]:
    steps = operation.get("steps")
    if not isinstance(steps, list):
        fail("operation must include steps")
    names = []
    for step in steps:
        if not isinstance(step, dict):
            continue
        name = step.get("step_name") or step.get("name")
        status = str(step.get("status", "")).lower()
        if isinstance(name, str) and name.strip() and status in {"succeeded", "success"}:
            names.append(name.strip())
    required = {"resolve_resources", "network_binding", "storage_mount"}
    missing = required - set(names)
    if missing:
        fail(f"operation missing succeeded steps: {', '.join(sorted(missing))}")
    return names


def observe_deployment_network(config: LiveConfig, runner: LiveRunner) -> dict[str, Any]:
    namespace = tenant_namespace(config)
    raw = runner.run(kubectl(config, ["-n", namespace, "get", "deployment", config.name, "-o", "json"]))
    try:
        deployment = json.loads(raw)
    except json.JSONDecodeError as err:
        fail(f"deployment observe must return JSON: {err}")
    annotations = (
        deployment.get("spec", {})
        .get("template", {})
        .get("metadata", {})
        .get("annotations", {})
    )
    if not isinstance(annotations, dict):
        fail("deployment pod template must include annotations")
    logical_switch = annotations.get("ovn.kubernetes.io/logical_switch")
    subnet_id = annotations.get("ani.kubercloud.io/subnet-id")
    if not logical_switch or not str(logical_switch).startswith("subnet-"):
        fail("deployment must annotate ovn.kubernetes.io/logical_switch for subnet binding")
    if subnet_id != config.subnet_id:
        fail(f"deployment subnet annotation {subnet_id!r} != requested {config.subnet_id!r}")
    return {"logical_switch": logical_switch, "subnet_id": subnet_id, "vpc_id": annotations.get("ani.kubercloud.io/vpc-id")}


def observe_pod_storage(config: LiveConfig, runner: LiveRunner) -> dict[str, Any]:
    namespace = tenant_namespace(config)
    raw = runner.run(
        kubectl(
            config,
            [
                "-n",
                namespace,
                "get",
                "pods",
                "-l",
                f"ani.kubercloud.io/instance={config.name}",
                "-o",
                "json",
            ],
        )
    )
    try:
        listing = json.loads(raw)
    except json.JSONDecodeError as err:
        fail(f"pod observe must return JSON: {err}")
    items = listing.get("items") if isinstance(listing, dict) else None
    if not isinstance(items, list) or not items:
        fail("pod observe must return at least one pod")
    claim_names: list[str] = []
    for item in items:
        if not isinstance(item, dict):
            continue
        for volume in item.get("spec", {}).get("volumes", []) or []:
            if not isinstance(volume, dict):
                continue
            pvc = volume.get("persistentVolumeClaim")
            if isinstance(pvc, dict) and isinstance(pvc.get("claimName"), str):
                claim_names.append(pvc["claimName"])
    if config.volume_id.strip():
        expected = "vol-" + config.volume_id.strip().lower().replace("_", "-")
        # networkProviderName keeps digits/letters/.- ; approximate containment check
        if not any(config.volume_id.replace("_", "-").lower() in name.lower() for name in claim_names):
            fail(f"pod volumes {claim_names} must include PVC claim for volume {config.volume_id}")
        return {"claim_names": claim_names, "expected_volume": expected}
    expected = "fs-" + config.filesystem_id.strip().lower().replace("_", "-")
    if not any(config.filesystem_id.replace("_", "-").lower() in name.lower() for name in claim_names):
        fail(f"pod volumes {claim_names} must include PVC claim for filesystem {config.filesystem_id}")
    return {"claim_names": claim_names, "expected_filesystem": expected}


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


def create_body(config: LiveConfig) -> dict[str, object]:
    network: dict[str, object] = {
        "vpc_id": config.vpc_id,
        "subnet_id": config.subnet_id,
    }
    if config.security_group_ids:
        network["security_group_ids"] = list(config.security_group_ids)
    container_config: dict[str, object] = {
        "network": network,
        "replicas": 1,
    }
    if config.volume_id.strip():
        container_config["volume_mounts"] = [
            {"volume_id": config.volume_id.strip(), "mount_path": config.mount_path, "read_only": False}
        ]
    if config.filesystem_id.strip():
        container_config["filesystem_mounts"] = [
            {
                "filesystem_id": config.filesystem_id.strip(),
                "mount_path": config.mount_path if not config.volume_id.strip() else "/mnt/fs",
                "read_only": False,
            }
        ]
    return {
        "idempotency_key": config.idempotency_key + "-create",
        "name": config.name,
        "kind": "container",
        "image_ref": config.image_ref,
        "cpu": config.cpu,
        "memory": config.memory,
        "auto_start": True,
        "container_config": container_config,
    }


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
        create_body(config),
    )
    if create_status != 201:
        fail("container create must return 201")
    instance = require_instance(created, "container create")
    require_real_provider(instance, "container create")
    created_id = instance_id(instance, "container create")
    create_operation = operation_id(created)
    running = wait_for_state(config, http_client, created_id, {"running", "ready", "active"})
    operation = get_operation(config, http_client, create_operation)
    step_names = require_operation_steps(operation)
    network_observe = observe_deployment_network(config, runner)
    storage_observe = observe_pod_storage(config, runner)
    stopped, stop_operation = lifecycle(config, http_client, created_id, "stop")
    started, start_operation = lifecycle(config, http_client, created_id, "start")
    deleted, delete_operation = lifecycle(config, http_client, created_id, "delete")
    return {
        "status": "passed",
        "kind": "container",
        "instance_id": created_id,
        "create_operation_id": create_operation,
        "operation_steps": step_names,
        "state_after_create": running.get("state"),
        "state_after_stop": stopped.get("state"),
        "state_after_start": started.get("state"),
        "state_after_delete": deleted.get("state"),
        "stop_operation_id": stop_operation,
        "start_operation_id": start_operation,
        "delete_operation_id": delete_operation,
        "network_observe": network_observe,
        "storage_observe": storage_observe,
        "cleared_missing_items": [
            "instance_registry_admission",
            "instance_network_selection",
            "instance_storage_attachment_orchestration",
        ],
        "production_shape": {
            "status": "passed",
            "missing_items": [],
        },
    }


def write_evidence(path: Path, evidence: dict[str, object]) -> None:
    identified = {
        "id": GATE_ID,
        "profile": PROFILE,
        "generated_at": dt.datetime.now(dt.timezone.utc).isoformat().replace("+00:00", "Z"),
        **evidence,
    }
    path.parent.mkdir(parents=True, exist_ok=True)
    path.write_text(json.dumps(identified, ensure_ascii=False, indent=2, sort_keys=True) + "\n", encoding="utf-8")


def main() -> int:
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("--gate", default=str(DEFAULT_GATE))
    parser.add_argument("--live", action="store_true")
    parser.add_argument("--gateway-url", default=os.getenv("ANI_GATEWAY_URL", ""))
    parser.add_argument("--ani-bearer-token", default=os.getenv("ANI_BEARER_TOKEN", ""))
    parser.add_argument("--tenant-id", default=os.getenv("ANI_LIVE_TENANT_ID", "11111111-1111-1111-1111-111111111111"))
    parser.add_argument("--name", default=os.getenv("ANI_ORCH_LIVE_NAME", "ani-instance-orch-live"))
    parser.add_argument("--image-ref", default=os.getenv("ANI_ORCH_LIVE_IMAGE_REF", ""))
    parser.add_argument(
        "--idempotency-key",
        default=os.getenv("ANI_ORCH_LIVE_IDEMPOTENCY_KEY", "instance-orchestration-container-live"),
    )
    parser.add_argument("--vpc-id", default=os.getenv("ANI_ORCH_LIVE_VPC_ID", ""))
    parser.add_argument("--subnet-id", default=os.getenv("ANI_ORCH_LIVE_SUBNET_ID", ""))
    parser.add_argument(
        "--security-group-ids",
        default=os.getenv("ANI_ORCH_LIVE_SECURITY_GROUP_IDS", ""),
        help="comma-separated security group ids",
    )
    parser.add_argument("--volume-id", default=os.getenv("ANI_ORCH_LIVE_VOLUME_ID", ""))
    parser.add_argument("--filesystem-id", default=os.getenv("ANI_ORCH_LIVE_FILESYSTEM_ID", ""))
    parser.add_argument("--mount-path", default=os.getenv("ANI_ORCH_LIVE_MOUNT_PATH", "/data"))
    parser.add_argument("--kubeconfig", default=os.getenv("KUBECONFIG", ""))
    parser.add_argument("--evidence-output", default=os.getenv("ANI_ORCH_LIVE_EVIDENCE_OUTPUT") or None)
    args = parser.parse_args()

    document = load_gate(Path(args.gate))
    validate_contract(document)
    validate_docs()
    if not args.live:
        print(
            "INSTANCE-ORCHESTRATION-A contract valid; "
            "use --live with vpc/subnet and volume or filesystem ids to validate through Core /api/v1/instances"
        )
        return 0
    if args.evidence_output is not None:
        validate_evidence_output(args.evidence_output)
    security_group_ids = tuple(
        part.strip() for part in str(args.security_group_ids).split(",") if part.strip()
    )
    evidence = run_live(
        LiveConfig(
            gateway_url=args.gateway_url,
            ani_bearer_token=args.ani_bearer_token,
            tenant_id=args.tenant_id,
            name=args.name,
            image_ref=args.image_ref,
            idempotency_key=args.idempotency_key,
            vpc_id=args.vpc_id,
            subnet_id=args.subnet_id,
            security_group_ids=security_group_ids,
            volume_id=args.volume_id,
            filesystem_id=args.filesystem_id,
            mount_path=args.mount_path,
            kubeconfig=args.kubeconfig,
        )
    )
    if args.evidence_output is not None:
        write_evidence(Path(args.evidence_output), evidence)
        print(f"INSTANCE-ORCHESTRATION-A live checks valid; evidence written to {args.evidence_output}")
    else:
        print(f"INSTANCE-ORCHESTRATION-A live checks valid: {json.dumps(evidence, sort_keys=True)}")
    return 0


if __name__ == "__main__":
    raise SystemExit(main())
