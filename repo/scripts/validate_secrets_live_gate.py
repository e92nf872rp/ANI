#!/usr/bin/env python3
"""Validate Sprint 5 M1-SECRETS-LIVE-A Secret live gate."""

from __future__ import annotations

import argparse
import base64
import json
import os
import shutil
import subprocess
import time
import urllib.error
import urllib.request
from dataclasses import dataclass
from pathlib import Path
from typing import Any

import yaml


ROOT = Path(__file__).resolve().parents[1]
DOC_ROOT = ROOT.parent
DEFAULT_GATE = ROOT / "deploy/real-k8s-lab/secrets-live-gate.yaml"
REQUIRED_CHECKS = {
    "core-create-kubernetes-secret",
    "core-bind-secret-env",
    "core-bind-secret-file",
    "kubectl-read-secret",
    "pod-secret-env-visible",
    "pod-secret-file-visible",
    "kubevirt-vm-secret-volume-accepted",
    "kubevirt-vm-secret-volume-guest-visible",
}
REQUIRED_ENV = {"KUBECONFIG", "ANI_GATEWAY_URL", "ANI_BEARER_TOKEN"}
REQUIRED_DOC_TOKENS = [
    "M1-SECRETS-LIVE-A",
    "validate-secrets-live-gate",
    "Kubernetes Secret",
    "env/file/VM",
]
PROFILE = "M1-SECRETS-LIVE-A"
GATE_ID = "secrets-live-gate"


def fail(message: str) -> None:
    raise SystemExit(f"Secret live gate invalid: {message}")


def gate_path_label(path: Path) -> str:
    try:
        return str(path.relative_to(ROOT))
    except ValueError:
        return str(path)


def load_gate(path: Path) -> dict[str, Any]:
    label = gate_path_label(path)
    if not path.exists():
        fail(f"missing {label}")
    try:
        with path.open(encoding="utf-8") as handle:
            data = yaml.safe_load(handle)
    except OSError:
        fail(f"unreadable {label}")
    except yaml.YAMLError:
        fail(f"malformed {label}")
    if not isinstance(data, dict):
        fail(f"{label} must be a YAML object")
    return data


def validate_contract(document: dict[str, Any]) -> None:
    if document.get("profile") != PROFILE:
        fail(f"profile must be {PROFILE}")
    if document.get("status") not in {"contract", "live", "production_like"}:
        fail("status must be contract, live or production_like")
    tools = document.get("required_tools")
    if not isinstance(tools, list) or "kubectl" not in tools:
        fail("required_tools must include kubectl")
    endpoints = document.get("required_endpoints")
    required_endpoints = {"ani_gateway_api_v1", "kubernetes_api", "kubevirt_api"}
    if not isinstance(endpoints, list) or required_endpoints - set(endpoints):
        fail("required_endpoints must include gateway, Kubernetes API and KubeVirt API")
    checks = document.get("live_checks")
    if not isinstance(checks, list):
        fail("live_checks must be a list")
    check_ids = set()
    for check in checks:
        if not isinstance(check, dict):
            fail("live check must be an object")
        for field in ("id", "command", "pass_condition"):
            if not check.get(field):
                fail(f"live check missing {field}")
        check_ids.add(check["id"])
    missing = REQUIRED_CHECKS - check_ids
    if missing:
        fail(f"missing live checks: {', '.join(sorted(missing))}")


def validate_docs() -> None:
    docs = {
        "ANI-DOCS-INDEX.md": DOC_ROOT / "ANI-DOCS-INDEX.md",
        "ANI-06-开发计划.md": DOC_ROOT / "ANI-06-开发计划.md",
        "CURRENT-SPRINT.md": ROOT / "CURRENT-SPRINT.md",
        "development-records/README.md": ROOT / "development-records/README.md",
    }
    for label, path in docs.items():
        try:
            content = path.read_text(encoding="utf-8")
        except FileNotFoundError:
            fail(f"missing doc {label}")
        except OSError:
            fail(f"unreadable doc {label}")
        except UnicodeError:
            fail(f"malformed doc {label}")
        for token in REQUIRED_DOC_TOKENS:
            if token not in content:
                fail(f"{label} must reference {token}")


@dataclass(frozen=True)
class LiveConfig:
    tenant_id: str
    gateway_url: str
    ani_bearer_token: str
    secret_name: str = "live-secret"
    pod_name: str = "ani-secret-live-pod"
    vm_name: str = "ani-secret-live-vm"
    password_value: str = "ani-live-password"
    token_value: str = "ani-live-token"
    vm_wait_timeout: str = "180s"
    kubeconfig: str = ""
    kubectl_binary: str = "kubectl"


class LiveRunner:
    def post_json(
        self,
        url: str,
        payload: dict[str, object],
        bearer_token: str,
        tenant_id: str = "",
    ) -> dict[str, object]:
        body = json.dumps(payload).encode("utf-8")
        headers = {
            "content-type": "application/json",
            "authorization": "Bearer " + bearer_token,
        }
        if tenant_id:
            headers["x-dev-tenant-id"] = tenant_id
        request = urllib.request.Request(
            url,
            data=body,
            method="POST",
            headers=headers,
        )
        try:
            with urllib.request.urlopen(request, timeout=30) as response:
                response_body = response.read().decode("utf-8")
                return json.loads(response_body) if response_body else {}
        except urllib.error.HTTPError as err:
            detail = err.read().decode("utf-8", errors="replace")
            raise RuntimeError(f"{url} returned HTTP {err.code}: {detail}") from err

    def run(self, command: list[str], input_text: str | None = None) -> str:
        result = subprocess.run(
            command,
            input=input_text,
            text=True,
            capture_output=True,
            check=False,
        )
        if result.returncode != 0:
            detail = result.stderr.strip() or result.stdout.strip()
            raise RuntimeError(f"{' '.join(command)} failed: {detail}")
        return result.stdout


def tenant_namespace(tenant_id: str) -> str:
    return "ani-tenant-" + tenant_id.replace("_", "-")


def validate_live_fields(config: LiveConfig) -> None:
    required = {
        "tenant_id": config.tenant_id,
        "gateway_url": config.gateway_url,
        "ani_bearer_token": config.ani_bearer_token,
        "password_value": config.password_value,
        "token_value": config.token_value,
        "vm_wait_timeout": config.vm_wait_timeout,
    }
    missing = [name for name, value in required.items() if not value.strip()]
    if missing:
        fail(f"live mode requires {', '.join(missing)}")
    whitespace = [name for name, value in required.items() if value != value.strip()]
    if whitespace:
        fail(f"{', '.join(whitespace)} must not contain surrounding whitespace")


def validate_live_config(config: LiveConfig) -> None:
    validate_live_fields(config)
    if shutil.which(config.kubectl_binary) is None:
        fail(f"{config.kubectl_binary} is required for --live")


def kubectl(config: LiveConfig, args: list[str]) -> list[str]:
    command = [config.kubectl_binary]
    if config.kubeconfig.strip():
        command.extend(["--kubeconfig", config.kubeconfig.strip()])
    command.extend(args)
    return command


def decode_secret_value(secret: dict[str, Any], key: str) -> str:
    data = secret.get("data")
    if not isinstance(data, dict) or key not in data:
        fail(f"Kubernetes Secret missing data.{key}")
    try:
        return base64.b64decode(str(data[key])).decode("utf-8")
    except Exception as err:  # noqa: BLE001 - convert malformed live data to gate failure
        fail(f"Kubernetes Secret data.{key} is not valid base64 text: {err}")


def pod_manifest(namespace: str, pod_name: str, secret_id: str) -> str:
    return json.dumps(
        {
            "apiVersion": "v1",
            "kind": "Pod",
            "metadata": {"name": pod_name, "namespace": namespace},
            "spec": {
                "restartPolicy": "Never",
                "containers": [
                    {
                        "name": "secret-check",
                        "image": "dockerproxy.net/library/busybox:1.36",
                        "command": [
                            "sh",
                            "-c",
                            "printf 'env:%s\\n' \"$LIVE_password\"; printf 'file:%s\\n' \"$(cat /etc/ani-secret/token)\"",
                        ],
                        "envFrom": [{"prefix": "LIVE_", "secretRef": {"name": secret_id}}],
                        "volumeMounts": [{"name": "secret-file", "mountPath": "/etc/ani-secret", "readOnly": True}],
                    }
                ],
                "volumes": [{"name": "secret-file", "secret": {"secretName": secret_id}}],
            },
        },
        indent=2,
        sort_keys=True,
    )


def vm_manifest(namespace: str, vm_name: str, secret_id: str) -> str:
    return json.dumps(
        {
            "apiVersion": "kubevirt.io/v1",
            "kind": "VirtualMachine",
            "metadata": {"name": vm_name, "namespace": namespace},
            "spec": {
                "running": False,
                "template": {
                    "metadata": {
                        "annotations": {"ani.kubercloud.io/vm-secret-mounts": f"{secret_id}:/etc/ani-secret"}
                    },
                    "spec": {
                        "domain": {
                            "devices": {
                                "disks": [
                                    {"name": "containerdisk", "disk": {"bus": "virtio"}},
                                    {"name": "secret-file", "disk": {"bus": "virtio"}},
                                ]
                            },
                            "resources": {"requests": {"memory": "256Mi"}},
                        },
                        "volumes": [
                            {
                                "name": "containerdisk",
                                "containerDisk": {"image": "quay.io/containerdisks/cirros:latest"},
                            },
                            {"name": "secret-file", "secret": {"secretName": secret_id}},
                        ],
                    },
                },
            },
        },
        indent=2,
        sort_keys=True,
    )


def vm_guest_probe_script() -> str:
    return """#!/bin/sh
echo ANI_VM_SECRET_GUEST_PROBE_START >/dev/console
mkdir -p /mnt/ani-secret
for dev in /dev/vdb /dev/vdc /dev/vdd /dev/sr0 /dev/sr1 /dev/sr2; do
  echo ANI_VM_SECRET_GUEST_TRY=$dev >/dev/console
  mount -o ro $dev /mnt/ani-secret >/dev/console 2>&1 || continue
  ls -la /mnt/ani-secret >/dev/console 2>&1 || true
  if [ -f /mnt/ani-secret/password ]; then
    printf 'ANI_VM_SECRET_GUEST_PASSWORD=%s\\n' "$(cat /mnt/ani-secret/password)" >/dev/console
  fi
  if [ -f /mnt/ani-secret/token ]; then
    printf 'ANI_VM_SECRET_GUEST_TOKEN=%s\\n' "$(cat /mnt/ani-secret/token)" >/dev/console
  fi
  umount /mnt/ani-secret >/dev/console 2>&1 || true
done
echo ANI_VM_SECRET_GUEST_PROBE_DONE >/dev/console
"""


def vm_guest_probe_manifest(namespace: str, vm_name: str, secret_id: str, tenant_id: str) -> str:
    return yaml.safe_dump(
        {
            "apiVersion": "kubevirt.io/v1",
            "kind": "VirtualMachine",
            "metadata": {
                "name": vm_name,
                "namespace": namespace,
                "labels": {
                    "app.kubernetes.io/part-of": "ani-platform",
                    "app.kubernetes.io/managed-by": "ani-core",
                    "ani.kubercloud.io/live-gate": "m1-secrets-live-a",
                },
            },
            "spec": {
                "running": True,
                "template": {
                    "metadata": {
                        "labels": {
                            "kubevirt.io/domain": vm_name,
                            "app.kubernetes.io/part-of": "ani-platform",
                            "ani.kubercloud.io/live-gate": "m1-secrets-live-a",
                            "ani.kubercloud.io/tenant-id": tenant_id,
                        }
                    },
                    "spec": {
                        "terminationGracePeriodSeconds": 0,
                        "domain": {
                            "devices": {
                                "disks": [
                                    {"name": "containerdisk", "disk": {"bus": "virtio"}},
                                    {"name": "secret-file", "disk": {"bus": "virtio"}},
                                    {"name": "cloudinitdisk", "disk": {"bus": "virtio"}},
                                ]
                            },
                            "resources": {"requests": {"memory": "256Mi"}},
                        },
                        "volumes": [
                            {
                                "name": "containerdisk",
                                "containerDisk": {
                                    "image": "quay.io/kubevirt/cirros-container-disk-demo:v1.8.2"
                                },
                            },
                            {
                                "name": "secret-file",
                                "secret": {"secretName": secret_id, "volumeLabel": "ANISECRET"},
                            },
                            {"name": "cloudinitdisk", "cloudInitNoCloud": {"userData": vm_guest_probe_script()}},
                        ],
                    },
                },
            },
        },
        sort_keys=True,
    )


def parse_seconds(timeout: str) -> int:
    value = timeout.strip()
    if value.endswith("s"):
        value = value[:-1]
    try:
        seconds = int(value)
    except ValueError:
        fail("vm_wait_timeout must be an integer number of seconds, optionally suffixed with s")
    if seconds <= 0:
        fail("vm_wait_timeout must be positive")
    return seconds


def find_virt_launcher_pod(config: LiveConfig, runner: LiveRunner, namespace: str, vm_name: str) -> str:
    raw_pods = runner.run(
        kubectl(config, ["get", "pod", "-n", namespace, "-l", f"kubevirt.io/domain={vm_name}", "-o", "json"])
    )
    try:
        pods = json.loads(raw_pods)
    except json.JSONDecodeError as err:
        fail(f"virt-launcher pod lookup did not return JSON: {err}")
    items = pods.get("items")
    if not isinstance(items, list) or not items:
        fail("KubeVirt guest Secret probe did not create a virt-launcher pod")
    pod = items[0]
    metadata = pod.get("metadata", {}) if isinstance(pod, dict) else {}
    name = metadata.get("name") if isinstance(metadata, dict) else None
    if not isinstance(name, str) or not name:
        fail("virt-launcher pod metadata.name missing")
    return name


def wait_for_guest_secret_probe(config: LiveConfig, runner: LiveRunner, namespace: str, launcher_pod: str) -> str:
    deadline = time.monotonic() + parse_seconds(config.vm_wait_timeout)
    logs = ""
    while time.monotonic() <= deadline:
        logs = runner.run(
            kubectl(
                config,
                ["logs", f"pod/{launcher_pod}", "-n", namespace, "--all-containers=true", "--tail=500"],
            )
        )
        if "ANI_VM_SECRET_GUEST_PROBE_DONE" in logs:
            return logs
        time.sleep(2)
    fail("KubeVirt guest Secret probe did not finish before timeout")


def assert_guest_secret_values(logs: str, password_value: str, token_value: str) -> None:
    if "ANI_VM_SECRET_GUEST_PROBE_START" not in logs or "ANI_VM_SECRET_GUEST_PROBE_DONE" not in logs:
        fail("KubeVirt guest Secret probe markers missing from virt-launcher logs")
    if "ANISECRET" not in logs:
        fail("KubeVirt guest Secret disk label ANISECRET missing from guest logs")
    if f"ANI_VM_SECRET_GUEST_PASSWORD={password_value}" not in logs:
        fail("KubeVirt guest logs do not include expected Secret password value")
    if f"ANI_VM_SECRET_GUEST_TOKEN={token_value}" not in logs:
        fail("KubeVirt guest logs do not include expected Secret token value")


def run_vm_guest_secret_probe(
    config: LiveConfig,
    runner: LiveRunner,
    namespace: str,
    secret_id: str,
) -> dict[str, object]:
    runner.run(kubectl(config, ["delete", "vm", config.vm_name, "-n", namespace, "--ignore-not-found=true", "--wait=false"]))
    try:
        runner.run(
            kubectl(config, ["apply", "-f", "-"]),
            input_text=vm_guest_probe_manifest(namespace, config.vm_name, secret_id, config.tenant_id),
        )
        runner.run(
            kubectl(
                config,
                [
                    "wait",
                    "--for=condition=Ready",
                    f"vmi/{config.vm_name}",
                    "-n",
                    namespace,
                    f"--timeout={config.vm_wait_timeout}",
                ],
            )
        )
        raw_vmi = runner.run(kubectl(config, ["get", "vmi", config.vm_name, "-n", namespace, "-o", "json"]))
        try:
            vmi = json.loads(raw_vmi)
        except json.JSONDecodeError as err:
            fail(f"kubectl get vmi did not return JSON: {err}")
        launcher_pod = find_virt_launcher_pod(config, runner, namespace, config.vm_name)
        logs = wait_for_guest_secret_probe(config, runner, namespace, launcher_pod)
        assert_guest_secret_values(logs, config.password_value, config.token_value)
        status = vmi.get("status", {}) if isinstance(vmi, dict) else {}
        return {
            "guest_secret_volume_visible": True,
            "launcher_pod": launcher_pod,
            "node": status.get("nodeName", ""),
            "secret_disk_label": "ANISECRET",
        }
    finally:
        runner.run(kubectl(config, ["delete", "vm", config.vm_name, "-n", namespace, "--ignore-not-found=true", "--wait=false"]))


def run_live(config: LiveConfig, runner: LiveRunner | None = None) -> dict[str, object]:
    validate_live_fields(config)
    runner = runner or LiveRunner()
    gateway = config.gateway_url.rstrip("/")
    namespace = tenant_namespace(config.tenant_id)

    secret = runner.post_json(
        gateway + "/secrets",
        {
            "idempotency_key": f"secrets-live-{config.tenant_id}-{config.secret_name}",
            "name": config.secret_name,
            "type": "opaque",
            "data": {"password": config.password_value, "token": config.token_value},
        },
        config.ani_bearer_token,
        config.tenant_id,
    )
    secret_id = str(secret.get("id") or secret.get("secret_id") or "")
    if not secret_id:
        fail("Core create secret response missing secret id")
    profile = secret.get("dev_profile", {})
    if not isinstance(profile, dict) or profile.get("mode") != "real" or not profile.get("real_provider"):
        fail("Core create secret response must be Kubernetes provider-backed real dev profile")

    for payload in (
        {"target_type": "container", "target_id": config.pod_name, "env_prefix": "LIVE_"},
        {"target_type": "container", "target_id": config.pod_name, "mount_path": "/etc/ani-secret"},
    ):
        binding = runner.post_json(
            gateway + f"/secrets/{secret_id}/bindings",
            payload,
            config.ani_bearer_token,
            config.tenant_id,
        )
        if not binding.get("id") or binding.get("state") != "bound":
            fail("Core secret binding response must be bound")

    raw_secret = runner.run(kubectl(config, ["get", "secret", secret_id, "-n", namespace, "-o", "json"]))
    try:
        kubernetes_secret = json.loads(raw_secret)
    except json.JSONDecodeError as err:
        fail(f"kubectl get secret did not return JSON: {err}")
    if decode_secret_value(kubernetes_secret, "password") != config.password_value:
        fail("Kubernetes Secret password value does not match Core request")
    if decode_secret_value(kubernetes_secret, "token") != config.token_value:
        fail("Kubernetes Secret token value does not match Core request")

    runner.run(kubectl(config, ["delete", "pod", config.pod_name, "-n", namespace, "--ignore-not-found=true"]))
    runner.run(kubectl(config, ["apply", "-f", "-"]), input_text=pod_manifest(namespace, config.pod_name, secret_id))
    runner.run(kubectl(config, ["wait", "--for=condition=Ready", f"pod/{config.pod_name}", "-n", namespace, "--timeout=60s"]))
    logs = runner.run(kubectl(config, ["logs", config.pod_name, "-n", namespace]))
    if f"env:{config.password_value}" not in logs:
        fail("Pod logs do not include expected env Secret value")
    if f"file:{config.token_value}" not in logs:
        fail("Pod logs do not include expected file Secret value")

    vm_apply = runner.run(
        kubectl(config, ["apply", "--dry-run=server", "-f", "-"]),
        input_text=vm_manifest(namespace, config.vm_name, secret_id),
    )
    if config.vm_name not in vm_apply:
        fail("KubeVirt VM Secret volume manifest was not accepted")
    guest_probe = run_vm_guest_secret_probe(config, runner, namespace, secret_id)

    return {
        "status": "passed",
        "tenant_id": config.tenant_id,
        "gateway_url": gateway,
        "secret_id": secret_id,
        "namespace": namespace,
        "pod": config.pod_name,
        "vm": config.vm_name,
        "vm_guest_secret": guest_probe,
    }


def write_live_evidence(path: Path, evidence: dict[str, object]) -> None:
    try:
        path.parent.mkdir(parents=True, exist_ok=True)
        identified = {**evidence, "id": GATE_ID, "profile": PROFILE}
        path.write_text(json.dumps(identified, ensure_ascii=False, indent=2, sort_keys=True) + "\n", encoding="utf-8")
    except OSError as err:
        fail(f"evidence output unusable: {err}")


def validate_gate_path(path: str) -> None:
    if not path.strip():
        fail("gate path must not be empty")
    if path != path.strip():
        fail("gate path must not contain surrounding whitespace")


def validate_evidence_output(path: str) -> None:
    if not path.strip():
        fail("evidence_output must not be empty")
    if path != path.strip():
        fail("evidence_output must not contain surrounding whitespace")
    output_path = Path(path)
    if output_path.is_dir():
        fail("evidence_output must be a file path")
    if output_path.parent.exists() and not output_path.parent.is_dir():
        fail("evidence_output parent must be a directory")
    try:
        output_path.parent.mkdir(parents=True, exist_ok=True)
    except OSError:
        fail("evidence_output parent must be a directory")
    try:
        if output_path.parent.stat().st_mode & 0o222 == 0:
            fail("evidence_output parent must be writable")
        if output_path.exists() and output_path.stat().st_mode & 0o222 == 0:
            fail("evidence_output must be writable")
    except OSError:
        fail("evidence_output must be writable")


def main() -> int:
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("--gate", default=str(DEFAULT_GATE), help="Secret live gate YAML")
    parser.add_argument("--live", action="store_true", help="run live Core/Kubernetes/KubeVirt checks")
    parser.add_argument("--tenant-id", default=os.getenv("ANI_LIVE_TENANT_ID", "tenant-a"))
    parser.add_argument("--gateway-url", default=os.getenv("ANI_GATEWAY_URL", ""))
    parser.add_argument("--ani-bearer-token", default=os.getenv("ANI_BEARER_TOKEN", ""))
    parser.add_argument("--secret-name", default=os.getenv("ANI_LIVE_SECRET_NAME", "live-secret"))
    parser.add_argument("--pod-name", default=os.getenv("ANI_LIVE_SECRET_POD_NAME", "ani-secret-live-pod"))
    parser.add_argument("--vm-name", default=os.getenv("ANI_LIVE_SECRET_VM_NAME", "ani-secret-live-vm"))
    parser.add_argument("--password-value", default=os.getenv("ANI_LIVE_SECRET_PASSWORD", "ani-live-password"))
    parser.add_argument("--token-value", default=os.getenv("ANI_LIVE_SECRET_TOKEN", "ani-live-token"))
    parser.add_argument("--vm-wait-timeout", default=os.getenv("ANI_LIVE_SECRET_VM_WAIT_TIMEOUT", "180s"))
    parser.add_argument("--kubeconfig", default=os.getenv("KUBECONFIG", ""))
    parser.add_argument(
        "--evidence-output",
        default=os.getenv("ANI_SECRETS_LIVE_EVIDENCE_OUTPUT") or None,
        help="optional JSON evidence output path for --live",
    )
    args = parser.parse_args()

    validate_gate_path(args.gate)
    document = load_gate(Path(args.gate))
    validate_contract(document)
    validate_docs()
    if args.live:
        config = LiveConfig(
            tenant_id=args.tenant_id,
            gateway_url=args.gateway_url,
            ani_bearer_token=args.ani_bearer_token,
            secret_name=args.secret_name,
            pod_name=args.pod_name,
            vm_name=args.vm_name,
            password_value=args.password_value,
            token_value=args.token_value,
            vm_wait_timeout=args.vm_wait_timeout,
            kubeconfig=args.kubeconfig,
        )
        validate_live_config(config)
        if args.evidence_output is not None:
            validate_evidence_output(args.evidence_output)
        result = run_live(config)
        if args.evidence_output is not None:
            write_live_evidence(Path(args.evidence_output), result)
            print(f"M1-SECRETS-LIVE-A live checks valid; evidence written to {args.evidence_output}")
        else:
            print(f"M1-SECRETS-LIVE-A live checks valid: {json.dumps(result, sort_keys=True)}")
    else:
        print("M1-SECRETS-LIVE-A contract valid; use --live with ANI_GATEWAY_URL, ANI_BEARER_TOKEN and KUBECONFIG")
    return 0


if __name__ == "__main__":
    raise SystemExit(main())
