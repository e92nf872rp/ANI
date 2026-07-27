#!/usr/bin/env python3
"""Validate Harbor-backed Registry live gate through ANI Gateway."""

from __future__ import annotations

import argparse
import base64
import datetime as dt
import json
import os
import shutil
import subprocess
import urllib.error
import urllib.parse
import urllib.request
from dataclasses import dataclass
from pathlib import Path
from typing import Any

import yaml


ROOT = Path(__file__).resolve().parents[1]
DOC_ROOT = ROOT.parent
DEFAULT_GATE = ROOT / "deploy/real-k8s-lab/registry-harbor-live-gate.yaml"
PROFILE = "SPRINT13-REGISTRY-HARBOR-LIVE-A"
GATE_ID = "registry-harbor-live-gate"
REQUIRED_CHECKS = {
    "gateway-registry-project-create",
    "gateway-registry-project-list",
    "gateway-registry-push-instructions",
    "gateway-registry-pull-secret",
    "gateway-registry-images-purpose",
    "gateway-registry-scan-report",
}
REQUIRED_DOC_TOKENS = [
    "SPRINT13-REGISTRY-HARBOR-LIVE-A",
    "validate-registry-harbor-live-gate",
    "Harbor",
]
COMMAND_TIMEOUT_SECONDS = 120


def fail(message: str) -> None:
    raise SystemExit(f"registry Harbor live gate invalid: {message}")


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
    if not isinstance(tools, list) or {"curl", "kubectl"} - set(tools):
        fail("required_tools must include curl and kubectl")
    checks = document.get("live_checks")
    if not isinstance(checks, list):
        fail("live_checks must be a list")
    check_ids = {check.get("id") for check in checks if isinstance(check, dict)}
    missing = REQUIRED_CHECKS - check_ids
    if missing:
        fail(f"missing live checks: {', '.join(sorted(missing))}")


def validate_docs() -> None:
    docs = {
        "CURRENT-SPRINT.md": ROOT / "CURRENT-SPRINT.md",
        "ANI-06-开发计划.md": DOC_ROOT / "ANI-06-开发计划.md",
        "development-records/README.md": ROOT / "development-records/README.md",
    }
    for label, path in docs.items():
        content = path.read_text(encoding="utf-8")
        for token in REQUIRED_DOC_TOKENS:
            if token not in content:
                fail(f"{label} must reference {token}")


def validate_output(path: str) -> None:
    if not path.strip() or path != path.strip():
        fail("evidence_output must be a non-empty path without surrounding whitespace")
    output = Path(path)
    if output.is_dir():
        fail("evidence_output must be a file path")
    output.parent.mkdir(parents=True, exist_ok=True)


def is_local_transport(value: str) -> bool:
    lowered = value.strip().lower()
    return any(marker in lowered for marker in ("127.0.0.1", "localhost", "port-forward", "kubectl-proxy", "kubectl proxy"))


@dataclass(frozen=True)
class LiveConfig:
    gateway_url: str
    ani_bearer_token: str
    tenant_id: str
    project: str
    namespace: str
    pull_secret_name: str
    idempotency_key: str
    kubeconfig: str = ""
    kubectl_binary: str = "kubectl"
    repository: str = ""
    tag: str = ""
    purpose: str = "container"
    production_shaped: bool = False
    cleanup: bool = False


class LiveRunner:
    def run(self, command: list[str], input_text: str | None = None) -> str:
        result = subprocess.run(command, input=input_text, text=True, capture_output=True, check=False, timeout=COMMAND_TIMEOUT_SECONDS)
        if result.returncode != 0:
            detail = result.stderr.strip() or result.stdout.strip()
            raise RuntimeError(f"{' '.join(command)} failed: {detail}")
        return result.stdout


class HTTPClient:
    def request(self, method: str, url: str, token: str, tenant_id: str, body: dict[str, object] | None = None) -> tuple[int, dict[str, Any]]:
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
            if method == "POST" and err.code == 409:
                return err.code, {"conflict": True}
            raise RuntimeError(f"{method} {url} failed: HTTP {err.code} {raw or err.reason}") from err
        except urllib.error.URLError as err:
            raise RuntimeError(f"{method} {url} failed: {err.reason}") from err


def gateway_url(config: LiveConfig, path: str, query: dict[str, str] | None = None) -> str:
    value = config.gateway_url.rstrip("/") + path
    if query:
        value += "?" + urllib.parse.urlencode(query)
    return value


def kubectl(config: LiveConfig, args: list[str]) -> list[str]:
    command = [config.kubectl_binary]
    if config.kubeconfig.strip():
        command.extend(["--kubeconfig", config.kubeconfig.strip()])
    command.extend(args)
    return command


def require_real_provider(document: dict[str, Any], label: str) -> None:
    profile = document.get("dev_profile")
    if not isinstance(profile, dict) or profile.get("provider") != "harbor" or profile.get("real_provider") is not True:
        items = document.get("items")
        if isinstance(items, list):
            for item in items:
                if not isinstance(item, dict):
                    continue
                profile = item.get("dev_profile")
                if isinstance(profile, dict) and profile.get("provider") == "harbor" and profile.get("real_provider") is True:
                    return
        fail(f"{label} must return Harbor real-provider dev_profile")


def list_items(document: dict[str, Any], label: str) -> list[dict[str, Any]]:
    items = document.get("items")
    if not isinstance(items, list):
        fail(f"{label} must return items")
    return [item for item in items if isinstance(item, dict)]


def validate_live_config(config: LiveConfig) -> None:
    required = {
        "gateway_url": config.gateway_url,
        "ani_bearer_token": config.ani_bearer_token,
        "tenant_id": config.tenant_id,
        "project": config.project,
        "namespace": config.namespace,
        "pull_secret_name": config.pull_secret_name,
        "idempotency_key": config.idempotency_key,
    }
    missing = [name for name, value in required.items() if not value.strip()]
    if missing:
        fail(f"live mode requires {', '.join(missing)}")
    if config.production_shaped and is_local_transport(config.gateway_url):
        fail("production-shaped live mode requires a non-local Gateway URL")
    if shutil.which(config.kubectl_binary) is None:
        fail(f"{config.kubectl_binary} is required for --live")


def run_live(config: LiveConfig, http_client: HTTPClient | None = None, runner: LiveRunner | None = None) -> dict[str, object]:
    validate_live_config(config)
    http_client = http_client or HTTPClient()
    runner = runner or LiveRunner()

    create_status, project = http_client.request(
        "POST",
        gateway_url(config, "/registry/projects"),
        config.ani_bearer_token,
        config.tenant_id,
        {"idempotency_key": config.idempotency_key + "-project", "name": config.project, "public": False},
    )
    if create_status not in {201, 409}:
        fail("registry project create must return 201 or 409")
    if create_status == 201:
        require_real_provider(project, "project create")

    list_status, projects = http_client.request("GET", gateway_url(config, "/registry/projects"), config.ani_bearer_token, config.tenant_id)
    if list_status != 200:
        fail("registry project list must return 200")
    require_real_provider(projects, "project list")
    project_items = list_items(projects, "project list")
    if not any(item.get("name") == config.project for item in project_items):
        fail("registry project list must include the tenant project")

    push_status, push = http_client.request("GET", gateway_url(config, f"/registry/projects/{urllib.parse.quote(config.project)}/push-instructions"), config.ani_bearer_token, config.tenant_id)
    if push_status != 200 or not push.get("registry"):
        fail("push instructions must return registry")
    require_real_provider(push, "push instructions")

    runner.run(kubectl(config, ["create", "namespace", config.namespace, "--dry-run=client", "-o", "yaml"]))
    runner.run(kubectl(config, ["apply", "-f", "-"]), input_text=json_to_namespace_manifest(config.namespace))
    secret_status, secret = http_client.request(
        "POST",
        gateway_url(config, f"/registry/projects/{urllib.parse.quote(config.project)}/pull-secret"),
        config.ani_bearer_token,
        config.tenant_id,
        {"idempotency_key": config.idempotency_key + "-pull-secret", "name": config.pull_secret_name, "namespace": config.namespace},
    )
    if secret_status != 201:
        fail("pull-secret create must return 201")
    require_real_provider(secret, "pull-secret")
    observed_secret_raw = runner.run(kubectl(config, ["-n", config.namespace, "get", "secret", config.pull_secret_name, "-o", "json"]))
    observed_secret = json.loads(observed_secret_raw)
    if observed_secret.get("type") != "kubernetes.io/dockerconfigjson":
        fail("pull-secret must create a dockerconfigjson Kubernetes Secret")
    docker_config = observed_secret.get("data", {}).get(".dockerconfigjson", "")
    if not docker_config:
        fail("pull-secret must contain .dockerconfigjson")
    base64.b64decode(docker_config)

    artifact_probe: dict[str, object] = {"status": "skipped", "reason": "repository/tag not supplied"}
    if config.repository and config.tag:
        repo_status, repositories = http_client.request("GET", gateway_url(config, f"/registry/projects/{urllib.parse.quote(config.project)}/repositories"), config.ani_bearer_token, config.tenant_id)
        if repo_status != 200:
            fail("repository list must return 200")
        require_real_provider(repositories, "repository list")
        repo_items = list_items(repositories, "repository list")
        if not any(item.get("name") == config.repository for item in repo_items):
            fail("repository list must include supplied repository")

        artifact_status, artifacts = http_client.request("GET", gateway_url(config, f"/registry/projects/{urllib.parse.quote(config.project)}/repositories/{urllib.parse.quote(config.repository)}/artifacts"), config.ani_bearer_token, config.tenant_id)
        if artifact_status != 200:
            fail("artifact list must return 200")
        require_real_provider(artifacts, "artifact list")
        artifact_items = list_items(artifacts, "artifact list")
        if not any(config.tag in item.get("tags", []) for item in artifact_items):
            fail("artifact list must include supplied tag")

        image_status, images = http_client.request("GET", gateway_url(config, "/registry/images", {"project": config.project, "repository": config.repository, "tag": config.tag, "purpose": config.purpose}), config.ani_bearer_token, config.tenant_id)
        if image_status != 200:
            fail("image list must return 200")
        require_real_provider(images, "image list")
        image_items = list_items(images, "image list")
        if not image_items:
            fail("image list must include supplied repository/tag/purpose")
        artifact_probe = {"status": "passed", "repository": config.repository, "tag": config.tag, "purpose": config.purpose, "images": len(image_items)}

    report_status, report = http_client.request("GET", gateway_url(config, f"/registry/projects/{urllib.parse.quote(config.project)}/scan-report"), config.ani_bearer_token, config.tenant_id)
    if report_status != 200:
        fail("scan report must return 200")
    require_real_provider(report, "scan report")

    cleanup: dict[str, object] = {"status": "not_requested"}
    if config.cleanup:
        runner.run(kubectl(config, ["-n", config.namespace, "delete", "secret", config.pull_secret_name, "--ignore-not-found"]))
        cleanup = {"status": "deleted", "resources": [f"secret/{config.namespace}/{config.pull_secret_name}"]}

    evidence: dict[str, object] = {
        "status": "passed",
        "project_create_status": create_status,
        "project_list_status": list_status,
        "project_found": True,
        "push_instructions_status": push_status,
        "registry_host_observed": True,
        "pull_secret_status": secret_status,
        "pull_secret_ref": f"{config.namespace}/{config.pull_secret_name}",
        "kubernetes_secret_type": observed_secret.get("type"),
        "scan_report_status": report_status,
        "scan_report_provider": report.get("provider_id"),
        "artifact_probe": artifact_probe,
        "cleanup": cleanup,
    }
    if config.production_shaped:
        evidence["production_shape"] = {
            "status": "passed",
            "transport_profile": "production_gateway_to_harbor_and_kubernetes_secret",
            "missing_items": [] if artifact_probe["status"] == "passed" else ["artifact_push_or_preseeded_tag"],
            "proof_items": ["production_gateway", "harbor_real_provider", "kubernetes_pull_secret"],
        }
    return evidence


def json_to_namespace_manifest(namespace: str) -> str:
    return json.dumps({"apiVersion": "v1", "kind": "Namespace", "metadata": {"name": namespace}}) + "\n"


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
    parser.add_argument("--gateway-url", default=os.getenv("ANI_GATEWAY_URL", ""))
    parser.add_argument("--ani-bearer-token", default=os.getenv("ANI_BEARER_TOKEN", ""))
    parser.add_argument("--tenant-id", default=os.getenv("ANI_LIVE_TENANT_ID", "tenant-a"))
    parser.add_argument("--project", default=os.getenv("ANI_REGISTRY_LIVE_PROJECT", os.getenv("ANI_LIVE_TENANT_ID", "tenant-a")))
    parser.add_argument("--namespace", default=os.getenv("ANI_REGISTRY_LIVE_NAMESPACE", "ani-registry-live"))
    parser.add_argument("--pull-secret-name", default=os.getenv("ANI_REGISTRY_LIVE_PULL_SECRET", "ani-registry-pull-live"))
    parser.add_argument("--idempotency-key", default=os.getenv("ANI_REGISTRY_LIVE_IDEMPOTENCY_KEY", "registry-harbor-live"))
    parser.add_argument("--repository", default=os.getenv("ANI_REGISTRY_LIVE_REPOSITORY", ""))
    parser.add_argument("--tag", default=os.getenv("ANI_REGISTRY_LIVE_TAG", ""))
    parser.add_argument("--purpose", default=os.getenv("ANI_REGISTRY_LIVE_PURPOSE", "container"))
    parser.add_argument("--kubeconfig", default=os.getenv("KUBECONFIG", ""))
    parser.add_argument("--production-shaped", action="store_true")
    parser.add_argument("--cleanup", action="store_true")
    parser.add_argument("--evidence-output", default=os.getenv("ANI_REGISTRY_HARBOR_LIVE_EVIDENCE_OUTPUT") or None)
    args = parser.parse_args()

    document = load_gate(Path(args.gate))
    validate_contract(document)
    validate_docs()
    if not args.live:
        print("SPRINT13-REGISTRY-HARBOR-LIVE-A contract valid; use --live against a Harbor-backed Gateway")
        return 0
    if args.evidence_output is not None:
        validate_output(args.evidence_output)
    evidence = run_live(
        LiveConfig(
            gateway_url=args.gateway_url,
            ani_bearer_token=args.ani_bearer_token,
            tenant_id=args.tenant_id,
            project=args.project,
            namespace=args.namespace,
            pull_secret_name=args.pull_secret_name,
            idempotency_key=args.idempotency_key,
            kubeconfig=args.kubeconfig,
            repository=args.repository,
            tag=args.tag,
            purpose=args.purpose,
            production_shaped=args.production_shaped,
            cleanup=args.cleanup,
        )
    )
    if args.evidence_output is not None:
        write_evidence(Path(args.evidence_output), evidence)
        print(f"SPRINT13-REGISTRY-HARBOR-LIVE-A live checks valid; evidence written to {args.evidence_output}")
    else:
        print(f"SPRINT13-REGISTRY-HARBOR-LIVE-A live checks valid: {json.dumps(evidence, sort_keys=True)}")
    return 0


if __name__ == "__main__":
    raise SystemExit(main())
