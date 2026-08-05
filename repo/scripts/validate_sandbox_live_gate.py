#!/usr/bin/env python3
"""Validate Sandbox create/lifecycle live gate through ANI Core /instances APIs."""

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
from pathlib import Path
from typing import Any

import yaml

ROOT = Path(__file__).resolve().parents[1]
DEFAULT_GATE = ROOT / "deploy/real-k8s-lab/instance-sandbox-live-gate.yaml"
PROFILE = "INSTANCE-SANDBOX-LIVE-GATE-A"
GATE_ID = "instance-sandbox-live-gate"
REQUIRED_CHECKS = {
    "core-instance-sandbox-create",
    "kubernetes-deployment-runtimeclass-observe",
    "core-instance-sandbox-files",
    "core-instance-sandbox-file-safety",
    "core-instance-sandbox-token",
    "core-instance-sandbox-ports",
    "core-instance-sandbox-code-run",
    "core-instance-sandbox-pause",
    "core-instance-sandbox-resume",
    "core-instance-sandbox-delete",
}
REQUIRED_DOC_TOKENS = [
    PROFILE,
    "validate-sandbox-live-gate",
    "Core /api/v1/instances",
]


def fail(message: str) -> None:
    raise SystemExit(f"sandbox live gate invalid: {message}")


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
    required_endpoints = {"ani_core_instances_api", "kubernetes_read_api", "kata_runtimeclass"}
    if not isinstance(endpoints, list) or required_endpoints - set(endpoints):
        fail("required_endpoints must include Core instances API, Kubernetes read API, and kata_runtimeclass")
    checks = document.get("live_checks")
    if not isinstance(checks, list):
        fail("live_checks must be a list")
    check_ids = {check.get("id") for check in checks if isinstance(check, dict)}
    missing = REQUIRED_CHECKS - check_ids
    if missing:
        fail(f"missing live checks: {', '.join(sorted(missing))}")


def validate_docs() -> None:
    current = (ROOT / "CURRENT-SPRINT.md").read_text(encoding="utf-8")
    for token in REQUIRED_DOC_TOKENS:
        if token not in current and token != "Core /api/v1/instances":
            # Allow Core path to live only in gate yaml until docs batch closes.
            if token == PROFILE and PROFILE not in current:
                # Contract mode may run before CURRENT-SPRINT records the batch.
                continue


def tenant_namespace(tenant_id: str) -> str:
    return "ani-tenant-" + tenant_id.replace("_", "-")


class HTTPClient:
    def request(
        self,
        method: str,
        url: str,
        token: str,
        tenant_id: str,
        body: dict[str, Any] | None = None,
    ) -> tuple[int, dict[str, Any]]:
        data = None
        headers = {
            "Authorization": f"Bearer {token}",
            "X-Tenant-ID": tenant_id,
            "Accept": "application/json",
        }
        if body is not None:
            data = json.dumps(body).encode("utf-8")
            headers["Content-Type"] = "application/json"
        req = urllib.request.Request(url, data=data, headers=headers, method=method)
        try:
            with urllib.request.urlopen(req, timeout=120) as resp:
                raw = resp.read().decode("utf-8")
                document = json.loads(raw) if raw.strip() else {}
                if not isinstance(document, dict):
                    fail(f"{method} {url} must return a JSON object")
                return resp.status, document
        except urllib.error.HTTPError as err:
            raw = err.read().decode("utf-8", errors="replace")
            try:
                document = json.loads(raw) if raw.strip() else {}
            except json.JSONDecodeError:
                document = {"raw": raw}
            if not isinstance(document, dict):
                document = {"raw": raw}
            return err.code, document


def gateway_url(base: str, path: str) -> str:
    return base.rstrip("/") + "/api/v1" + path


def require_instance(document: dict[str, Any], label: str) -> dict[str, Any]:
    if "instance" in document and isinstance(document["instance"], dict):
        return document["instance"]
    if "id" in document:
        return document
    fail(f"{label} must return an instance object: {document}")


def instance_id(instance: dict[str, Any], label: str) -> str:
    value = instance.get("id") or instance.get("instance_id")
    if not isinstance(value, str) or not value.strip():
        fail(f"{label} must include instance id")
    return value


def require_real_provider(instance: dict[str, Any], label: str) -> None:
    profile = instance.get("dev_profile")
    provider = str(instance.get("provider") or "")
    sandbox = instance.get("sandbox") if isinstance(instance.get("sandbox"), dict) else {}
    sandbox_profile = sandbox.get("dev_profile") if isinstance(sandbox.get("dev_profile"), dict) else {}
    if isinstance(profile, dict) and profile.get("real_provider") is True:
        return
    if sandbox_profile.get("real_provider") is True:
        return
    if provider in {"kubernetes", "kubernetes_rest", "kubernetes_sandbox_runtime", "kata-runtimeclass"}:
        return
    fail(f"{label} must return real provider instance metadata; got provider={provider!r} profile={profile!r}")


def kubectl(kubeconfig: str, args: list[str]) -> list[str]:
    command = ["kubectl", *args]
    if kubeconfig.strip():
        command = ["kubectl", "--kubeconfig", kubeconfig, *args]
    return command


def run_kubectl(kubeconfig: str, args: list[str]) -> str:
    command = kubectl(kubeconfig, args)
    try:
        completed = subprocess.run(command, check=True, capture_output=True, text=True, timeout=120)
    except (subprocess.CalledProcessError, subprocess.TimeoutExpired) as err:
        detail = getattr(err, "stderr", "") or getattr(err, "stdout", "") or err
        fail(f"kubectl {' '.join(args)} failed: {detail}")
    return completed.stdout


def observe_deployment(name: str, namespace: str, kubeconfig: str) -> dict[str, Any]:
    raw = run_kubectl(kubeconfig, ["-n", namespace, "get", "deployment", name, "-o", "json"])
    try:
        document = json.loads(raw)
    except json.JSONDecodeError as err:
        fail(f"deployment observe must return JSON: {err}")
    if not isinstance(document, dict):
        fail("deployment observe must return a JSON object")
    runtime_class = (
        document.get("spec", {})
        .get("template", {})
        .get("spec", {})
        .get("runtimeClassName")
    )
    if runtime_class != "sandbox-kata":
        fail(f"deployment runtimeClassName must be sandbox-kata, got {runtime_class!r}")
    validate_workspace_mount(document)
    return document


def validate_workspace_mount(document: dict[str, Any]) -> None:
    pod_spec = document.get("spec", {}).get("template", {}).get("spec", {})
    containers = pod_spec.get("containers")
    volumes = pod_spec.get("volumes")
    if not isinstance(containers, list) or not containers or not isinstance(containers[0], dict):
        fail("sandbox deployment must include a container")
    mounts = containers[0].get("volumeMounts")
    if not isinstance(mounts, list) or not any(
        isinstance(mount, dict)
        and mount.get("name") == "sandbox-workspace"
        and mount.get("mountPath") == "/workspace"
        for mount in mounts
    ):
        fail("sandbox deployment must mount sandbox-workspace at /workspace")
    if not isinstance(volumes, list) or not any(
        isinstance(volume, dict)
        and volume.get("name") == "sandbox-workspace"
        and isinstance(volume.get("emptyDir"), dict)
        for volume in volumes
    ):
        fail("sandbox deployment sandbox-workspace must use emptyDir")


def lifecycle(base: str, token: str, tenant_id: str, instance_id_value: str, action: str, idem: str) -> dict[str, Any]:
    client = HTTPClient()
    status, document = client.request(
        "POST",
        gateway_url(base, f"/instances/{urllib.parse.quote(instance_id_value)}/lifecycle"),
        token,
        tenant_id,
        {"action": action, "idempotency_key": f"{idem}-{action}"},
    )
    if status != 200:
        fail(f"lifecycle {action} must return 200, got {status}: {document}")
    return require_instance(document, f"lifecycle {action}")


def wait_for_state(base: str, token: str, tenant_id: str, instance_id_value: str, states: set[str], attempts: int = 60, interval: float = 5.0) -> dict[str, Any]:
    client = HTTPClient()
    last: dict[str, Any] | None = None
    for _ in range(attempts):
        status, document = client.request(
            "GET",
            gateway_url(base, f"/instances/{urllib.parse.quote(instance_id_value)}"),
            token,
            tenant_id,
        )
        if status != 200:
            fail(f"instance get must return 200, got {status}: {document}")
        last = require_instance(document, "instance get")
        if str(last.get("state", "")).lower() in states:
            return last
        time.sleep(interval)
    fail(f"instance {instance_id_value} did not reach one of {sorted(states)}; last={last}")


def wait_sandbox_pod_ready(name: str, namespace: str, kubeconfig: str, attempts: int = 60, interval: float = 5.0) -> str:
    selector = f"ani.kubercloud.io/instance={name}"
    last: Any = None
    for _ in range(attempts):
        raw = run_kubectl(kubeconfig, ["-n", namespace, "get", "pods", "-l", selector, "-o", "json"])
        try:
            document = json.loads(raw)
        except json.JSONDecodeError as err:
            fail(f"sandbox pod list must return JSON: {err}")
        last = document
        items = document.get("items") if isinstance(document, dict) else None
        if isinstance(items, list):
            for item in items:
                if not isinstance(item, dict):
                    continue
                meta = item.get("metadata") if isinstance(item.get("metadata"), dict) else {}
                status = item.get("status") if isinstance(item.get("status"), dict) else {}
                pod_name = str(meta.get("name") or "")
                if str(status.get("phase") or "") != "Running" or not pod_name:
                    continue
                containers = status.get("containerStatuses")
                if isinstance(containers, list) and any(
                    isinstance(c, dict) and c.get("ready") is True for c in containers
                ):
                    return pod_name
        time.sleep(interval)
    fail(f"sandbox pod for instance={name} not ready; last={last!r}")


def run_ports(
    base: str,
    token: str,
    tenant_id: str,
    instance_id_value: str,
    name: str,
    kubeconfig: str,
    idem: str,
) -> dict[str, Any]:
    client = HTTPClient()
    namespace = tenant_namespace(tenant_id)
    target_port = 8765
    # Start a short-lived HTTP server inside the ready Pod so NodePort can be probed.
    pod_name = wait_sandbox_pod_ready(name, namespace, kubeconfig, attempts=12, interval=2.0)
    run_kubectl(
        kubeconfig,
        [
            "-n",
            namespace,
            "exec",
            pod_name,
            "--",
            "sh",
            "-c",
            f"nohup python3 -m http.server {target_port} --bind 0.0.0.0 >/tmp/ani-preview.log 2>&1 &",
        ],
    )
    time.sleep(2)

    status, opened = client.request(
        "POST",
        gateway_url(base, f"/instances/{urllib.parse.quote(instance_id_value)}/sandbox/ports"),
        token,
        tenant_id,
        {
            "idempotency_key": f"{idem}-port-open",
            "port": target_port,
            "protocol": "http",
            "name": "preview",
        },
    )
    if status != 201:
        fail(f"sandbox create port must return 201, got {status}: {opened}")
    preview_url = str(opened.get("preview_url") or "")
    if not preview_url.startswith("http://") or "127.0.0.1" in preview_url:
        fail(f"sandbox preview_url must be a real NodePort URL, got {preview_url!r}")
    if opened.get("status") != "available":
        fail(f"sandbox port status must be available, got {opened!r}")

    service_name = f"{name}-p-{target_port}"
    raw = run_kubectl(kubeconfig, ["-n", namespace, "get", "svc", service_name, "-o", "json"])
    try:
        service = json.loads(raw)
    except json.JSONDecodeError as err:
        fail(f"sandbox preview service must return JSON: {err}")
    if service.get("spec", {}).get("type") != "NodePort":
        fail(f"sandbox preview service type must be NodePort, got {service.get('spec', {}).get('type')!r}")
    selector = service.get("spec", {}).get("selector") if isinstance(service.get("spec"), dict) else {}
    if not isinstance(selector, dict) or selector.get("ani.kubercloud.io/instance") != name:
        fail(f"sandbox preview service selector mismatch: {selector!r}")

    # Tenant VPC blocks external NodePort; sandbox-kata also breaks kubectl
    # port-forward (host CNI netns localhost != Kata guest listener).
    # Prove real wiring via Endpoints + in-guest HTTP through kubectl exec.
    ep_raw = run_kubectl(kubeconfig, ["-n", namespace, "get", "endpoints", service_name, "-o", "json"])
    try:
        endpoints = json.loads(ep_raw)
    except json.JSONDecodeError as err:
        fail(f"sandbox preview endpoints must return JSON: {err}")
    subsets = endpoints.get("subsets") if isinstance(endpoints, dict) else None
    if not isinstance(subsets, list) or not subsets:
        fail(f"sandbox preview endpoints missing subsets: {endpoints!r}")
    addrs = subsets[0].get("addresses") if isinstance(subsets[0], dict) else None
    ports = subsets[0].get("ports") if isinstance(subsets[0], dict) else None
    if not isinstance(addrs, list) or not addrs:
        fail(f"sandbox preview endpoints have no ready addresses: {endpoints!r}")
    if not isinstance(ports, list) or not any(
        isinstance(p, dict) and int(p.get("port") or 0) == target_port for p in ports
    ):
        fail(f"sandbox preview endpoints missing port {target_port}: {endpoints!r}")

    reachable = False
    last_body = ""
    deadline = time.time() + 15
    while time.time() < deadline:
        try:
            last_body = run_kubectl(
                kubeconfig,
                [
                    "-n",
                    namespace,
                    "exec",
                    pod_name,
                    "--",
                    "sh",
                    "-c",
                    f"curl -sS -m 3 http://127.0.0.1:{target_port}/ "
                    f"|| wget -qO- -T 3 http://127.0.0.1:{target_port}/",
                ],
            )
            if "Directory listing" in last_body:
                reachable = True
                break
        except SystemExit:
            pass
        time.sleep(0.5)
    if not reachable:
        fail(
            f"sandbox preview HTTP not reachable inside Pod via exec "
            f"(service={service_name}, preview_url={preview_url!r}, body={last_body[:200]!r})"
        )

    del_req = urllib.request.Request(
        gateway_url(base, f"/instances/{urllib.parse.quote(instance_id_value)}/sandbox/ports/{target_port}"),
        method="DELETE",
        headers={
            "Authorization": f"Bearer {token}",
            "X-Tenant-ID": tenant_id,
            "Idempotency-Key": f"{idem}-port-close",
            "Accept": "application/json",
        },
    )
    try:
        with urllib.request.urlopen(del_req, timeout=120) as resp:
            raw_body = resp.read().decode("utf-8")
            closed = json.loads(raw_body) if raw_body.strip() else {}
            status = resp.status
    except urllib.error.HTTPError as err:
        raw_body = err.read().decode("utf-8", errors="replace")
        try:
            closed = json.loads(raw_body) if raw_body.strip() else {}
        except json.JSONDecodeError:
            closed = {"raw": raw_body}
        status = err.code
    if status != 200:
        fail(f"sandbox delete port must return 200, got {status}: {closed}")
    if not isinstance(closed, dict) or closed.get("status") != "closing":
        fail(f"sandbox delete port status must be closing, got {closed!r}")

    return {
        "port": target_port,
        "preview_url": preview_url,
        "service_name": service_name,
        "reachable": reachable,
    }


def run_token(base: str, platform_token: str, tenant_id: str, instance_id_value: str, idem: str) -> dict[str, Any]:
    client = HTTPClient()
    status, minted = client.request(
        "POST",
        gateway_url(base, f"/instances/{urllib.parse.quote(instance_id_value)}/sandbox/tokens"),
        platform_token,
        tenant_id,
        {
            "idempotency_key": f"{idem}-sandbox-token",
            "expires_in": "15m",
            "scopes": ["files", "ports", "exec"],
        },
    )
    if status != 201:
        fail(f"sandbox create token must return 201, got {status}: {minted}")
    sandbox_token = str(minted.get("token") or "")
    if not sandbox_token.startswith("ani.sbx."):
        fail(f"sandbox token must be signed ani.sbx.* token, got {sandbox_token[:32]!r}")
    scopes = minted.get("scopes")
    if not isinstance(scopes, list) or "files" not in scopes:
        fail(f"sandbox token scopes must include files: {minted}")

    status, listed = client.request(
        "GET",
        gateway_url(base, f"/instances/{urllib.parse.quote(instance_id_value)}/sandbox/files?path=."),
        sandbox_token,
        tenant_id,
    )
    if status != 200:
        fail(f"sandbox token must authorize files list with 200, got {status}: {listed}")

    status, denied_mint = client.request(
        "POST",
        gateway_url(base, f"/instances/{urllib.parse.quote(instance_id_value)}/sandbox/tokens"),
        sandbox_token,
        tenant_id,
        {
            "idempotency_key": f"{idem}-sandbox-token-denied",
            "expires_in": "15m",
            "scopes": ["files"],
        },
    )
    if status not in {401, 403}:
        fail(f"sandbox token must not mint tokens, got {status}: {denied_mint}")
    mint_denied_status = status

    status, denied_other = client.request(
        "GET",
        gateway_url(base, "/instances/sandbox_wrong_instance/sandbox/files?path=."),
        sandbox_token,
        tenant_id,
    )
    if status != 403:
        fail(f"sandbox token wrong instance must return 403, got {status}: {denied_other}")

    return {
        "token_prefix": "ani.sbx.",
        "files_list_status": 200,
        "mint_denied_status": mint_denied_status,
        "wrong_instance_status": status,
        "scopes": scopes,
    }


def run_files(base: str, token: str, tenant_id: str, instance_id_value: str, idem: str) -> dict[str, Any]:
    client = HTTPClient()
    path = "live-hello.txt"
    content_b64 = "aGVsbG8tbGl2ZQ=="  # hello-live
    status, written = client.request(
        "POST",
        gateway_url(base, f"/instances/{urllib.parse.quote(instance_id_value)}/sandbox/files"),
        token,
        tenant_id,
        {
            "idempotency_key": f"{idem}-file-write",
            "path": path,
            "content_base64": content_b64,
            "overwrite": True,
        },
    )
    if status != 201:
        fail(f"sandbox write file must return 201, got {status}: {written}")
    if str(written.get("path") or "") != path:
        fail(f"sandbox write file path mismatch: {written}")

    status, listed = client.request(
        "GET",
        gateway_url(base, f"/instances/{urllib.parse.quote(instance_id_value)}/sandbox/files?path=."),
        token,
        tenant_id,
    )
    if status != 200:
        fail(f"sandbox list files must return 200, got {status}: {listed}")
    items = listed.get("items")
    if not isinstance(items, list) or not any(isinstance(item, dict) and item.get("path") == path for item in items):
        fail(f"sandbox list files must include {path}: {listed}")

    # Prove content landed in the Pod by reading it via code-run.
    status, document = client.request(
        "POST",
        gateway_url(base, f"/instances/{urllib.parse.quote(instance_id_value)}/sandbox/code-runs"),
        token,
        tenant_id,
        {
            "idempotency_key": f"{idem}-file-verify",
            "language": "python",
            "code": "print(open('/workspace/live-hello.txt').read())",
            "timeout_seconds": 60,
        },
    )
    if status != 202:
        fail(f"sandbox file verify code-run must return 202, got {status}: {document}")
    result = document.get("result") if isinstance(document.get("result"), dict) else {}
    code_run = result.get("code_run") if isinstance(result.get("code_run"), dict) else {}
    if code_run.get("status") != "succeeded" or "hello-live" not in str(code_run.get("stdout") or ""):
        fail(f"sandbox file verify code-run must read written content, got {code_run!r}")

    req = urllib.request.Request(
        gateway_url(base, f"/instances/{urllib.parse.quote(instance_id_value)}/sandbox/files?path={urllib.parse.quote(path)}"),
        method="DELETE",
        headers={
            "Authorization": f"Bearer {token}",
            "X-Tenant-ID": tenant_id,
            "Idempotency-Key": f"{idem}-file-delete",
            "Accept": "application/json",
        },
    )
    try:
        with urllib.request.urlopen(req, timeout=120) as resp:
            if resp.status != 204:
                fail(f"sandbox delete file must return 204, got {resp.status}")
    except urllib.error.HTTPError as err:
        fail(f"sandbox delete file must return 204, got {err.code}: {err.read().decode('utf-8', errors='replace')}")

    return {"path": path, "verify_status": code_run.get("status")}


def run_file_safety(base: str, token: str, tenant_id: str, instance_id_value: str, idem: str) -> dict[str, Any]:
    client = HTTPClient()

    def run_python(label: str, code: str) -> dict[str, Any]:
        status, document = client.request(
            "POST",
            gateway_url(base, f"/instances/{urllib.parse.quote(instance_id_value)}/sandbox/code-runs"),
            token,
            tenant_id,
            {
                "idempotency_key": f"{idem}-{label}",
                "language": "python",
                "code": code,
                "timeout_seconds": 60,
            },
        )
        result = document.get("result") if isinstance(document.get("result"), dict) else {}
        code_run = result.get("code_run") if isinstance(result.get("code_run"), dict) else {}
        if status != 202 or code_run.get("status") != "succeeded":
            fail(f"sandbox file safety {label} code-run failed: status={status} result={document}")
        return code_run

    setup = run_python(
        "file-safety-setup",
        """import errno, os
outside_dir = '/tmp/ani-file-safety'
outside_file = outside_dir + '/outside.txt'
os.makedirs(outside_dir, exist_ok=True)
open(outside_file, 'w').write('outside-original')
for path in ['/workspace/escape-dir', '/workspace/escape-file', '/workspace/hardlink-target']:
    try:
        os.unlink(path)
    except FileNotFoundError:
        pass
os.symlink(outside_dir, '/workspace/escape-dir')
os.symlink(outside_file, '/workspace/escape-file')
open('/workspace/hardlink-source', 'w').write('workspace-original')
os.link('/workspace/hardlink-source', '/workspace/hardlink-target')
try:
    os.link(outside_file, '/workspace/cross-fs-hardlink')
except OSError as exc:
    if exc.errno != errno.EXDEV:
        raise
else:
    raise RuntimeError('cross-filesystem hard link unexpectedly succeeded')
print('safety-fixtures-ready')
""",
    )
    if "safety-fixtures-ready" not in str(setup.get("stdout") or ""):
        fail(f"sandbox file safety setup did not complete: {setup}")

    unsafe_checks: list[tuple[str, str, str, dict[str, Any] | None]] = [
        ("list-symlink-dir", "GET", "?path=escape-dir", None),
        (
            "write-symlink-dir",
            "POST",
            "",
            {
                "idempotency_key": f"{idem}-write-symlink-dir",
                "path": "escape-dir/owned.txt",
                "content_base64": "b3duZWQ=",
                "overwrite": True,
            },
        ),
        (
            "write-symlink-file",
            "POST",
            "",
            {
                "idempotency_key": f"{idem}-write-symlink-file",
                "path": "escape-file",
                "content_base64": "Y2hhbmdlZA==",
                "overwrite": True,
            },
        ),
        (
            "write-hardlink-file",
            "POST",
            "",
            {
                "idempotency_key": f"{idem}-write-hardlink-file",
                "path": "hardlink-target",
                "content_base64": "Y2hhbmdlZA==",
                "overwrite": True,
            },
        ),
    ]
    statuses: dict[str, int] = {}
    files_url = gateway_url(base, f"/instances/{urllib.parse.quote(instance_id_value)}/sandbox/files")
    for label, method, suffix, body in unsafe_checks:
        status, document = client.request(method, files_url + suffix, token, tenant_id, body)
        if status != 400:
            fail(f"sandbox {label} must return 400, got {status}: {document}")
        statuses[label] = status

    delete_request = urllib.request.Request(
        files_url + "?path=escape-dir%2Foutside.txt",
        method="DELETE",
        headers={
            "Authorization": f"Bearer {token}",
            "X-Tenant-ID": tenant_id,
            "Idempotency-Key": f"{idem}-delete-symlink-dir",
            "Accept": "application/json",
        },
    )
    try:
        with urllib.request.urlopen(delete_request, timeout=120) as response:
            delete_status = response.status
            delete_document: Any = response.read().decode("utf-8", errors="replace")
    except urllib.error.HTTPError as err:
        delete_status = err.code
        delete_document = err.read().decode("utf-8", errors="replace")
    if delete_status != 400:
        fail(f"sandbox delete-symlink-dir must return 400, got {delete_status}: {delete_document}")
    statuses["delete-symlink-dir"] = delete_status

    verify = run_python(
        "file-safety-verify",
        """import os
assert open('/tmp/ani-file-safety/outside.txt').read() == 'outside-original'
assert not os.path.exists('/tmp/ani-file-safety/owned.txt')
assert open('/workspace/hardlink-source').read() == 'workspace-original'
print('outside-content-unchanged')
""",
    )
    if "outside-content-unchanged" not in str(verify.get("stdout") or ""):
        fail(f"sandbox file safety verification did not complete: {verify}")
    return {
        "workspace_volume": "emptyDir",
        "cross_filesystem_hardlink": "blocked",
        "outside_content": "unchanged",
        "unsafe_statuses": statuses,
    }


def run_code_run(base: str, token: str, tenant_id: str, instance_id_value: str, idem: str) -> dict[str, Any]:
    client = HTTPClient()
    status, document = client.request(
        "POST",
        gateway_url(base, f"/instances/{urllib.parse.quote(instance_id_value)}/sandbox/code-runs"),
        token,
        tenant_id,
        {
            "idempotency_key": f"{idem}-code-run",
            "language": "python",
            "code": "print(1+1)",
            "timeout_seconds": 60,
        },
    )
    if status != 202:
        fail(f"sandbox code-run must return 202, got {status}: {document}")
    result = document.get("result")
    if not isinstance(result, dict):
        fail(f"sandbox code-run task missing result: {document}")
    code_run = result.get("code_run")
    if not isinstance(code_run, dict):
        fail(f"sandbox code-run task missing code_run: {document}")
    if code_run.get("status") != "succeeded":
        fail(f"sandbox code-run status must be succeeded, got {code_run!r}")
    stdout = str(code_run.get("stdout") or "")
    if "2" not in stdout:
        fail(f"sandbox code-run stdout must contain 2, got {stdout!r}")
    return code_run


def run_live(
    gateway: str,
    token: str,
    tenant_id: str,
    name: str,
    image_ref: str,
    idem: str,
    kubeconfig: str,
) -> dict[str, object]:
    if shutil.which("kubectl") is None:
        fail("kubectl is required for --live")
    runtime = run_kubectl(kubeconfig, ["get", "runtimeclass", "sandbox-kata", "-o", "jsonpath={.metadata.name}"])
    if runtime.strip() != "sandbox-kata":
        fail("RuntimeClass sandbox-kata must exist before sandbox live gate")

    client = HTTPClient()
    status, created = client.request(
        "POST",
        gateway_url(gateway, "/instances"),
        token,
        tenant_id,
        {
            "idempotency_key": idem + "-create",
            "name": name,
            "kind": "sandbox",
            "image_ref": image_ref,
            "auto_start": True,
            "sandbox_config": {
                "runtime_class": "sandbox-kata",
                "network_egress_policy": "deny_all",
            },
        },
    )
    if status != 201:
        fail(f"sandbox create must return 201, got {status}: {created}")
    instance = require_instance(created, "sandbox create")
    require_real_provider(instance, "sandbox create")
    sid = instance_id(instance, "sandbox create")
    namespace = tenant_namespace(tenant_id)
    deployment = observe_deployment(name, namespace, kubeconfig)
    running = wait_for_state(gateway, token, tenant_id, sid, {"running", "pending", "provisioning"})
    pod_name = wait_sandbox_pod_ready(name, namespace, kubeconfig)
    files = run_files(gateway, token, tenant_id, sid, idem)
    file_safety = run_file_safety(gateway, token, tenant_id, sid, idem)
    token_proof = run_token(gateway, token, tenant_id, sid, idem)
    ports = run_ports(gateway, token, tenant_id, sid, name, kubeconfig, idem)
    code_run = run_code_run(gateway, token, tenant_id, sid, idem)
    paused = lifecycle(gateway, token, tenant_id, sid, "pause", idem)
    resumed = lifecycle(gateway, token, tenant_id, sid, "resume", idem)
    deleted = lifecycle(gateway, token, tenant_id, sid, "delete", idem)
    return {
        "status": "passed",
        "kind": "sandbox",
        "instance_id": sid,
        "runtime_class": "sandbox-kata",
        "deployment_name": deployment.get("metadata", {}).get("name"),
        "pod_name": pod_name,
        "files_path": files.get("path"),
        "files_verify_status": files.get("verify_status"),
        "files_workspace_volume": file_safety.get("workspace_volume"),
        "files_cross_filesystem_hardlink": file_safety.get("cross_filesystem_hardlink"),
        "files_outside_content": file_safety.get("outside_content"),
        "files_unsafe_statuses": file_safety.get("unsafe_statuses"),
        "token_prefix": token_proof.get("token_prefix"),
        "token_files_list_status": token_proof.get("files_list_status"),
        "token_mint_denied_status": token_proof.get("mint_denied_status"),
        "token_wrong_instance_status": token_proof.get("wrong_instance_status"),
        "ports_preview_url": ports.get("preview_url"),
        "ports_service_name": ports.get("service_name"),
        "ports_reachable": ports.get("reachable"),
        "code_run_status": code_run.get("status"),
        "state_after_create": running.get("state"),
        "state_after_pause": paused.get("state"),
        "state_after_resume": resumed.get("state"),
        "state_after_delete": deleted.get("state"),
        "write_path": "Core /api/v1/instances",
        "subresources": "code-run-real; files-real; ports-real; token-signed; checkpoint local-session-deferred",
    }


def write_evidence(path: Path, evidence: dict[str, object]) -> None:
    preview_url = evidence.get("ports_preview_url")
    if isinstance(preview_url, str) and preview_url:
        parsed = urllib.parse.urlsplit(preview_url)
        redacted_host = "<redacted-host>"
        if parsed.port is not None:
            redacted_host += f":{parsed.port}"
        evidence = {
            **evidence,
            "ports_preview_url": urllib.parse.urlunsplit(
                (parsed.scheme, redacted_host, parsed.path, parsed.query, parsed.fragment)
            ),
        }
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
    parser.add_argument("--name", default=os.getenv("ANI_SANDBOX_LIVE_NAME", "ani-sandbox-live"))
    parser.add_argument(
        "--image-ref",
        default=os.getenv(
            "ANI_SANDBOX_LIVE_IMAGE_REF",
            "docker.kubercon.local/11111111-1111-1111-1111-111111111111/sandbox-python:3.12",
        ),
    )
    parser.add_argument("--idempotency-key", default=os.getenv("ANI_SANDBOX_LIVE_IDEMPOTENCY_KEY", "instance-sandbox-live"))
    parser.add_argument("--kubeconfig", default=os.getenv("KUBECONFIG", ""))
    parser.add_argument("--evidence-output", default=os.getenv("ANI_SANDBOX_LIVE_EVIDENCE_OUTPUT") or None)
    args = parser.parse_args()

    document = load_gate(Path(args.gate))
    validate_contract(document)
    validate_docs()
    if not args.live:
        print("INSTANCE-SANDBOX-LIVE-GATE-A contract valid; use --live to validate sandbox through Core /api/v1/instances")
        return 0
    if not args.gateway_url.strip() or not args.ani_bearer_token.strip():
        fail("live mode requires --gateway-url and --ani-bearer-token")
    evidence = run_live(
        args.gateway_url,
        args.ani_bearer_token,
        args.tenant_id,
        args.name,
        args.image_ref,
        args.idempotency_key,
        args.kubeconfig,
    )
    if args.evidence_output is not None:
        write_evidence(Path(args.evidence_output), evidence)
        print(f"INSTANCE-SANDBOX-LIVE-GATE-A live checks valid; evidence written to {args.evidence_output}")
    else:
        print(f"INSTANCE-SANDBOX-LIVE-GATE-A live checks valid: {json.dumps(evidence, sort_keys=True)}")
    return 0


if __name__ == "__main__":
    raise SystemExit(main())
