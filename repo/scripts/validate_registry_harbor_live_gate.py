#!/usr/bin/env python3
"""Validate Harbor-backed Registry P0 closure live gate through ANI Gateway."""

from __future__ import annotations

import argparse
import base64
import datetime as dt
import hashlib
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
DEFAULT_GATE = ROOT / "deploy/real-k8s-lab/registry-harbor-live-gate.yaml"
PROFILE = "REGISTRY-P0-CLOSURE-A"
GATE_ID = "registry-p0-closure-live-gate"
REQUIRED_CHECKS = {
    "gateway-registry-project-create",
    "gateway-registry-project-list",
    "gateway-registry-push-instructions",
    "gateway-registry-artifact-push",
    "gateway-registry-pull-secret",
    "gateway-registry-images-purpose",
    "gateway-registry-scan-status",
    "gateway-registry-scan-report",
    "gateway-registry-instance-reference",
    "gateway-registry-delete-tag-blocked",
}
REQUIRED_DOC_TOKENS = [
    "REGISTRY-P0-CLOSURE-A",
    "validate-registry-harbor-live-gate",
    "Harbor",
]
COMMAND_TIMEOUT_SECONDS = 180
SCAN_WAIT_ATTEMPTS = 36
SCAN_WAIT_INTERVAL_SECONDS = 5


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


def response_hash(document: dict[str, Any]) -> str:
    raw = json.dumps(document, sort_keys=True, ensure_ascii=False).encode("utf-8")
    return hashlib.sha256(raw).hexdigest()[:16]


@dataclass(frozen=True)
class LiveConfig:
    gateway_url: str
    ani_bearer_token: str
    tenant_id: str
    project: str
    namespace: str
    pull_secret_name: str
    idempotency_key: str
    repository: str
    tag: str
    purpose: str = "container"
    kubeconfig: str = ""
    kubectl_binary: str = "kubectl"
    source_image: str = ""
    instance_name: str = "ani-registry-p0-live"
    production_shaped: bool = False
    cleanup: bool = False


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
        headers = {
            "Accept": "application/json",
            "Authorization": f"Bearer {token}",
            "X-Tenant-ID": tenant_id,
        }
        if body is not None:
            payload = json.dumps(body).encode("utf-8")
            headers["Content-Type"] = "application/json"
        request = urllib.request.Request(url, data=payload, headers=headers, method=method)
        try:
            with urllib.request.urlopen(request, timeout=COMMAND_TIMEOUT_SECONDS) as response:
                raw = response.read().decode("utf-8")
                document = json.loads(raw) if raw.strip() else {}
                if not isinstance(document, dict):
                    fail(f"{method} {url} must return a JSON object")
                return response.status, document
        except urllib.error.HTTPError as err:
            raw = err.read().decode("utf-8", errors="replace")
            try:
                document = json.loads(raw) if raw.strip() else {}
            except json.JSONDecodeError:
                document = {"raw": raw}
            if not isinstance(document, dict):
                document = {"raw": raw}
            return err.code, document
        except urllib.error.URLError as err:
            raise RuntimeError(f"{method} {url} failed: {err.reason}") from err


def gateway_url(config: LiveConfig, path: str, query: dict[str, str] | None = None) -> str:
    base = config.gateway_url.rstrip("/")
    if not base.endswith("/api/v1"):
        base += "/api/v1"
    value = base + path
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
    if isinstance(profile, dict) and profile.get("provider") == "harbor" and profile.get("real_provider") is True:
        return
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
        "repository": config.repository,
        "tag": config.tag,
    }
    missing = [name for name, value in required.items() if not str(value).strip()]
    if missing:
        fail(f"live mode requires {', '.join(missing)}")
    if config.production_shaped and is_local_transport(config.gateway_url):
        fail("production-shaped live mode requires a non-local Gateway URL")
    if shutil.which(config.kubectl_binary) is None:
        fail(f"{config.kubectl_binary} is required for --live")


def maybe_push_artifact(config: LiveConfig, push: dict[str, Any], runner: LiveRunner) -> dict[str, object]:
    if not config.source_image.strip():
        return {"status": "preseeded", "source": "repository/tag supplied without --source-image"}
    if shutil.which("docker") is None:
        fail("docker is required when --source-image is supplied")
    registry = str(push.get("registry") or "").strip()
    if not registry:
        fail("push instructions missing registry host")
    target = f"{registry}/{config.project}/{config.repository}:{config.tag}"
    username = os.getenv("HARBOR_USERNAME", "").strip()
    password = os.getenv("HARBOR_PASSWORD", "").strip()
    if not username or not password:
        fail("source-image push requires HARBOR_USERNAME and HARBOR_PASSWORD in the environment")
    # Login/password never enter evidence.
    runner.run(["docker", "login", registry, "-u", username, "--password-stdin"], input_text=password + "\n")
    runner.run(["docker", "pull", config.source_image])
    runner.run(["docker", "tag", config.source_image, target])
    runner.run(["docker", "push", target])
    return {"status": "pushed", "target_repository": config.repository, "target_tag": config.tag}


def wait_for_scan(
    config: LiveConfig,
    http_client: HTTPClient,
    image_ref: str,
) -> dict[str, Any]:
    last: dict[str, Any] = {}
    for _ in range(SCAN_WAIT_ATTEMPTS):
        status, document = http_client.request(
            "GET",
            gateway_url(config, "/registry/images/scan-result", {"image": image_ref}),
            config.ani_bearer_token,
            config.tenant_id,
        )
        if status != 200:
            fail(f"scan-result must return 200, got {status}: {document}")
        require_real_provider(document, "scan-result")
        last = document
        scan_status = str(document.get("status") or "")
        # P0 closure requires a terminal Harbor/Trivy result, not a stuck pending/running.
        if scan_status in {"complete", "failed"}:
            return document
        time.sleep(SCAN_WAIT_INTERVAL_SECONDS)
    fail(f"scan-result did not reach terminal status complete/failed, last={last}")


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
        fail(f"registry project create must return 201 or 409, got {create_status}: {project}")
    if create_status == 201:
        require_real_provider(project, "project create")

    list_status, projects = http_client.request(
        "GET",
        gateway_url(config, "/registry/projects"),
        config.ani_bearer_token,
        config.tenant_id,
    )
    if list_status != 200:
        fail(f"registry project list must return 200, got {list_status}")
    require_real_provider(projects, "project list")
    if not any(item.get("name") == config.project for item in list_items(projects, "project list")):
        fail("registry project list must include the tenant project")

    push_status, push = http_client.request(
        "GET",
        gateway_url(config, f"/registry/projects/{urllib.parse.quote(config.project)}/push-instructions"),
        config.ani_bearer_token,
        config.tenant_id,
    )
    if push_status != 200 or not push.get("registry"):
        fail(f"push instructions must return registry, got {push_status}: {push}")
    require_real_provider(push, "push instructions")
    push_probe = maybe_push_artifact(config, push, runner)

    runner.run(kubectl(config, ["create", "namespace", config.namespace, "--dry-run=client", "-o", "yaml"]))
    runner.run(kubectl(config, ["apply", "-f", "-"]), input_text=json_to_namespace_manifest(config.namespace))
    secret_status, secret = http_client.request(
        "POST",
        gateway_url(config, f"/registry/projects/{urllib.parse.quote(config.project)}/pull-secret"),
        config.ani_bearer_token,
        config.tenant_id,
        {
            "idempotency_key": config.idempotency_key + "-pull-secret",
            "name": config.pull_secret_name,
            "namespace": config.namespace,
        },
    )
    if secret_status != 201:
        fail(f"pull-secret create must return 201, got {secret_status}: {secret}")
    require_real_provider(secret, "pull-secret")
    observed_secret = json.loads(
        runner.run(kubectl(config, ["-n", config.namespace, "get", "secret", config.pull_secret_name, "-o", "json"]))
    )
    if observed_secret.get("type") != "kubernetes.io/dockerconfigjson":
        fail("pull-secret must create a dockerconfigjson Kubernetes Secret")
    docker_config = observed_secret.get("data", {}).get(".dockerconfigjson", "")
    if not docker_config:
        fail("pull-secret must contain .dockerconfigjson")
    base64.b64decode(docker_config)

    repo_status, repositories = http_client.request(
        "GET",
        gateway_url(config, f"/registry/projects/{urllib.parse.quote(config.project)}/repositories"),
        config.ani_bearer_token,
        config.tenant_id,
    )
    if repo_status != 200:
        fail(f"repository list must return 200, got {repo_status}")
    require_real_provider(repositories, "repository list")
    if not any(item.get("name") == config.repository for item in list_items(repositories, "repository list")):
        fail("repository list must include closure repository")

    artifact_status, artifacts = http_client.request(
        "GET",
        gateway_url(
            config,
            f"/registry/projects/{urllib.parse.quote(config.project)}/repositories/{urllib.parse.quote(config.repository)}/artifacts",
        ),
        config.ani_bearer_token,
        config.tenant_id,
    )
    if artifact_status != 200:
        fail(f"artifact list must return 200, got {artifact_status}: {artifacts}")
    require_real_provider(artifacts, "artifact list")
    if not any(config.tag in item.get("tags", []) for item in list_items(artifacts, "artifact list")):
        fail("artifact list must include closure tag")

    image_status, images = http_client.request(
        "GET",
        gateway_url(
            config,
            "/registry/images",
            {
                "project": config.project,
                "repository": config.repository,
                "tag": config.tag,
                "purpose": config.purpose,
            },
        ),
        config.ani_bearer_token,
        config.tenant_id,
    )
    if image_status != 200:
        fail(f"image list must return 200, got {image_status}: {images}")
    require_real_provider(images, "image list")
    image_items = list_items(images, "image list")
    if not image_items:
        fail("image list must include supplied repository/tag/purpose")
    image = image_items[0]
    if image.get("purpose") != config.purpose:
        fail(f"image purpose = {image.get('purpose')!r}, want {config.purpose!r}")
    image_ref = str(image.get("image") or f"{config.project}/{config.repository}:{config.tag}")
    image_digest = str(image.get("digest") or "")
    scan_from_list = image.get("scan_status") if isinstance(image.get("scan_status"), dict) else {}

    scan_result = wait_for_scan(config, http_client, image_ref)
    scan_status_value = str(scan_result.get("status") or "")
    if scan_status_value not in {"complete", "failed"}:
        fail(f"scan-result status must be complete or failed, got {scan_result}")

    report_status, report = http_client.request(
        "GET",
        gateway_url(config, f"/registry/projects/{urllib.parse.quote(config.project)}/scan-report"),
        config.ani_bearer_token,
        config.tenant_id,
    )
    if report_status != 200:
        fail(f"scan report must return 200, got {report_status}")
    require_real_provider(report, "scan report")

    instance_image_id = f"{config.project}/{config.repository}:{config.tag}"
    create_instance_status, created = http_client.request(
        "POST",
        gateway_url(config, "/instances"),
        config.ani_bearer_token,
        config.tenant_id,
        {
            "idempotency_key": config.idempotency_key + "-instance",
            "name": config.instance_name,
            "kind": "container",
            "image_id": instance_image_id,
            "auto_start": False,
        },
    )
    if create_instance_status not in {201, 409}:
        fail(f"instance create must return 201 or 409 for reference proof, got {create_instance_status}: {created}")
    instance = created.get("instance") if isinstance(created.get("instance"), dict) else created
    instance_id = str(instance.get("id") or "")
    if not instance_id:
        fail(f"instance create missing id: {created}")

    ref_status, references = http_client.request(
        "GET",
        gateway_url(
            config,
            f"/registry/projects/{urllib.parse.quote(config.project)}/repositories/{urllib.parse.quote(config.repository)}/tags/{urllib.parse.quote(config.tag)}/references",
        ),
        config.ani_bearer_token,
        config.tenant_id,
    )
    if ref_status != 200:
        fail(f"tag references must return 200, got {ref_status}: {references}")
    require_real_provider(references, "tag references")
    ref_items = list_items(references, "tag references")
    if not any(item.get("id") == instance_id or instance_id in str(item.get("route") or "") for item in ref_items):
        fail(f"tag references must include created instance {instance_id}: {references}")

    delete_status, deleted = http_client.request(
        "DELETE",
        gateway_url(
            config,
            f"/registry/projects/{urllib.parse.quote(config.project)}/repositories/{urllib.parse.quote(config.repository)}/tags/{urllib.parse.quote(config.tag)}",
        ),
        config.ani_bearer_token,
        config.tenant_id,
    )
    if delete_status != 409:
        fail(f"delete tag must return 409 while referenced, got {delete_status}: {deleted}")

    cleanup: dict[str, object] = {"status": "not_requested"}
    if config.cleanup:
        lifecycle_status, lifecycle = http_client.request(
            "POST",
            gateway_url(config, f"/instances/{urllib.parse.quote(instance_id)}/lifecycle"),
            config.ani_bearer_token,
            config.tenant_id,
            {"action": "delete", "idempotency_key": config.idempotency_key + "-instance-delete"},
        )
        if lifecycle_status not in {200, 202}:
            fail(f"instance delete lifecycle must return 200/202, got {lifecycle_status}: {lifecycle}")
        runner.run(kubectl(config, ["-n", config.namespace, "delete", "secret", config.pull_secret_name, "--ignore-not-found"]))
        cleanup = {
            "status": "deleted",
            "resources": [
                f"instance/{instance_id}",
                f"secret/{config.namespace}/{config.pull_secret_name}",
            ],
        }

    evidence: dict[str, object] = {
        "status": "passed",
        "project_create_status": create_status,
        "project_list_status": list_status,
        "project_found": True,
        "push_instructions_status": push_status,
        "registry_host_observed": True,
        "artifact_push": push_probe,
        "pull_secret_status": secret_status,
        "pull_secret_ref": f"{config.namespace}/{config.pull_secret_name}",
        "kubernetes_secret_type": observed_secret.get("type"),
        "purpose_filter": config.purpose,
        "image_digest_prefix": image_digest[:19] if image_digest else "",
        "scan_list_status": str(scan_from_list.get("status") or ""),
        "scan_result_status": scan_status_value,
        "scan_critical": int(scan_result.get("critical") or 0),
        "scan_high": int(scan_result.get("high") or 0),
        "scan_report_status": report_status,
        "scan_report_provider": report.get("provider_id"),
        "instance_create_status": create_instance_status,
        "instance_id": instance_id,
        "references_count": len(ref_items),
        "delete_tag_status": delete_status,
        "response_hashes": {
            "images": response_hash(images),
            "scan_result": response_hash(scan_result),
            "references": response_hash(references),
        },
        "cleanup": cleanup,
    }
    if config.production_shaped:
        evidence["production_shape"] = {
            "status": "passed",
            "transport_profile": "production_gateway_to_harbor_scan_reference_and_kubernetes_secret",
            "missing_items": [],
            "proof_items": [
                "production_gateway",
                "harbor_real_provider",
                "purpose_filter",
                "scan_status",
                "instance_reference",
                "delete_tag_409",
                "kubernetes_pull_secret",
            ],
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
    serialized = json.dumps(identified, ensure_ascii=False, indent=2, sort_keys=True)
    if '"ani_bearer_token"' in serialized or "Bearer " in serialized or "eyJhbGci" in serialized:
        fail("evidence must not contain bearer/JWT material")
    if '"password"' in serialized.lower() or ".dockerconfigjson" in serialized:
        fail("evidence must not contain password or dockerconfig payloads")
    path.write_text(serialized + "\n", encoding="utf-8")


def main() -> int:
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("--gate", default=str(DEFAULT_GATE))
    parser.add_argument("--live", action="store_true")
    parser.add_argument("--gateway-url", default=os.getenv("ANI_GATEWAY_URL", ""))
    parser.add_argument("--ani-bearer-token", default=os.getenv("ANI_BEARER_TOKEN", ""))
    parser.add_argument("--tenant-id", default=os.getenv("ANI_LIVE_TENANT_ID", "11111111-1111-1111-1111-111111111111"))
    parser.add_argument("--project", default=os.getenv("ANI_REGISTRY_LIVE_PROJECT", os.getenv("ANI_LIVE_TENANT_ID", "11111111-1111-1111-1111-111111111111")))
    parser.add_argument("--namespace", default=os.getenv("ANI_REGISTRY_LIVE_NAMESPACE", "ani-registry-live"))
    parser.add_argument("--pull-secret-name", default=os.getenv("ANI_REGISTRY_LIVE_PULL_SECRET", "ani-registry-pull-live"))
    parser.add_argument("--idempotency-key", default=os.getenv("ANI_REGISTRY_LIVE_IDEMPOTENCY_KEY", "registry-p0-closure-live"))
    parser.add_argument("--repository", default=os.getenv("ANI_REGISTRY_LIVE_REPOSITORY", ""))
    parser.add_argument("--tag", default=os.getenv("ANI_REGISTRY_LIVE_TAG", ""))
    parser.add_argument("--purpose", default=os.getenv("ANI_REGISTRY_LIVE_PURPOSE", "container"))
    parser.add_argument("--source-image", default=os.getenv("ANI_REGISTRY_LIVE_SOURCE_IMAGE", ""))
    parser.add_argument("--instance-name", default=os.getenv("ANI_REGISTRY_LIVE_INSTANCE_NAME", "ani-registry-p0-live"))
    parser.add_argument("--kubeconfig", default=os.getenv("KUBECONFIG", ""))
    parser.add_argument("--production-shaped", action="store_true")
    parser.add_argument("--cleanup", action="store_true")
    parser.add_argument("--evidence-output", default=os.getenv("ANI_REGISTRY_HARBOR_LIVE_EVIDENCE_OUTPUT") or None)
    args = parser.parse_args()

    document = load_gate(Path(args.gate))
    validate_contract(document)
    validate_docs()
    if not args.live:
        print("REGISTRY-P0-CLOSURE-A contract valid; use --live against a Harbor-backed Gateway")
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
            repository=args.repository,
            tag=args.tag,
            purpose=args.purpose,
            source_image=args.source_image,
            instance_name=args.instance_name,
            kubeconfig=args.kubeconfig,
            production_shaped=args.production_shaped,
            cleanup=args.cleanup,
        )
    )
    if args.evidence_output is not None:
        write_evidence(Path(args.evidence_output), evidence)
        print(f"REGISTRY-P0-CLOSURE-A live checks valid; evidence written to {args.evidence_output}")
    else:
        print(f"REGISTRY-P0-CLOSURE-A live checks valid: {json.dumps(evidence, sort_keys=True)}")
    return 0


if __name__ == "__main__":
    raise SystemExit(main())
