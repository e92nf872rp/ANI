#!/usr/bin/env python3
"""Validate Sprint 5 M1-K8S-LIVE-A vCluster live gate."""

from __future__ import annotations

import argparse
import json
import os
import shutil
import subprocess
import sys
import tempfile
import urllib.error
import urllib.request
from dataclasses import dataclass
from pathlib import Path
from typing import Any

import yaml


ROOT = Path(__file__).resolve().parents[1]
DOC_ROOT = ROOT.parent
DEFAULT_GATE = ROOT / "deploy/real-k8s-lab/vcluster-live-gate.yaml"
REQUIRED_CHECKS = {"helm-install", "vcluster-kubeconfig", "kubectl-version", "core-proxy-version"}
REQUIRED_DOC_TOKENS = ["M1-K8S-LIVE-A", "validate-vcluster-live-gate", "vCluster", "live proxy"]


def fail(message: str) -> None:
    raise SystemExit(f"vCluster live gate invalid: {message}")


def load_gate(path: Path) -> dict[str, Any]:
    if not path.exists():
        fail(f"missing {path.relative_to(ROOT)}")
    with path.open(encoding="utf-8") as handle:
        data = yaml.safe_load(handle)
    if not isinstance(data, dict):
        fail(f"{path.relative_to(ROOT)} must be a YAML object")
    return data


def validate_contract(document: dict[str, Any]) -> None:
    if document.get("profile") != "M1-K8S-LIVE-A":
        fail("profile must be M1-K8S-LIVE-A")
    if document.get("status") not in {"contract", "live", "production_like"}:
        fail("status must be contract, live or production_like")
    tools = document.get("required_tools")
    if not isinstance(tools, list) or {"helm", "vcluster", "kubectl"} - set(tools):
        fail("required_tools must include helm, vcluster and kubectl")
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
        content = path.read_text(encoding="utf-8")
        for token in REQUIRED_DOC_TOKENS:
            if token not in content:
                fail(f"{label} must reference {token}")


@dataclass(frozen=True)
class LiveConfig:
    tenant_id: str
    cluster_id: str
    gateway_url: str
    ani_bearer_token: str
    vcluster_server: str = ""
    helm_binary: str = "helm"
    vcluster_binary: str = "vcluster"
    kubectl_binary: str = "kubectl"
    chart_name: str = "vcluster"
    chart_repo: str = "https://charts.loft.sh"
    work_dir: Path | None = None


class CommandRunner:
    def run(self, command: list[str], env: dict[str, str] | None = None) -> str:
        result = subprocess.run(command, env=env, text=True, capture_output=True, check=False)
        if result.returncode != 0:
            detail = result.stderr.strip() or result.stdout.strip()
            raise RuntimeError(f"{' '.join(command)} failed: {detail}")
        return result.stdout

    def post_json(self, url: str, payload: dict[str, object], bearer_token: str) -> dict[str, object]:
        body = json.dumps(payload).encode("utf-8")
        request = urllib.request.Request(
            url,
            data=body,
            method="POST",
            headers={
                "content-type": "application/json",
                "authorization": "Bearer " + bearer_token,
            },
        )
        try:
            with urllib.request.urlopen(request, timeout=30) as response:
                response_body = response.read().decode("utf-8")
                return {
                    "status_code": response.status,
                    "headers": dict(response.headers.items()),
                    "body": json.loads(response_body) if response_body else {},
                }
        except urllib.error.HTTPError as err:
            detail = err.read().decode("utf-8", errors="replace")
            raise RuntimeError(f"Core proxy request failed: HTTP {err.code}: {detail}") from err


def tenant_namespace(tenant_id: str) -> str:
    return "ani-tenant-" + tenant_id


def validate_live_config(config: LiveConfig) -> None:
    required = {
        "tenant_id": config.tenant_id,
        "cluster_id": config.cluster_id,
        "gateway_url": config.gateway_url,
        "ani_bearer_token": config.ani_bearer_token,
    }
    missing = [name for name, value in required.items() if not value.strip()]
    if missing:
        fail(f"live mode requires {', '.join(missing)}")
    for binary in (config.helm_binary, config.vcluster_binary, config.kubectl_binary):
        if shutil.which(binary) is None:
            fail(f"{binary} is required for --live")


def helm_install_command(config: LiveConfig) -> list[str]:
    return [
        config.helm_binary,
        "upgrade",
        "--install",
        config.cluster_id,
        config.chart_name,
        "--repo",
        config.chart_repo,
        "--namespace",
        tenant_namespace(config.tenant_id),
        "--create-namespace",
        "--repository-config=",
        "--set",
        "sync.toHost.service.enabled=true",
    ]


def vcluster_connect_command(config: LiveConfig) -> list[str]:
    command = [
        config.vcluster_binary,
        "connect",
        config.cluster_id,
        "--namespace",
        tenant_namespace(config.tenant_id),
        "--print",
    ]
    if config.vcluster_server.strip():
        command.extend(["--server", config.vcluster_server.strip()])
    return command


def kubeconfig_path(config: LiveConfig) -> Path:
    if config.work_dir is not None:
        return config.work_dir / f"{config.cluster_id}.kubeconfig"
    return Path(tempfile.gettempdir()) / f"{config.cluster_id}.kubeconfig"


def run_live(config: LiveConfig, runner: CommandRunner | None = None) -> dict[str, object]:
    runner = runner or CommandRunner()
    runner.run(helm_install_command(config))
    kubeconfig = runner.run(vcluster_connect_command(config))
    if "apiVersion:" not in kubeconfig or "clusters:" not in kubeconfig:
        fail("vcluster connect did not print a kubeconfig")

    path = kubeconfig_path(config)
    path.write_text(kubeconfig, encoding="utf-8")
    version_output = runner.run([config.kubectl_binary, "--kubeconfig", str(path), "get", "--raw", "/version"])
    try:
        version = json.loads(version_output)
    except json.JSONDecodeError as err:
        fail(f"kubectl /version did not return JSON: {err}")
    if not isinstance(version, dict) or not (version.get("gitVersion") or version.get("major")):
        fail("kubectl /version response missing Kubernetes version")

    payload = {
        "idempotency_key": f"live-proxy-{config.cluster_id}-version",
        "method": "GET",
        "path": "/version",
        "query": {},
        "body": {},
    }
    proxy_response = runner.post_json(
        config.gateway_url.rstrip("/") + f"/k8s-clusters/{config.cluster_id}/proxy",
        payload,
        config.ani_bearer_token,
    )
    status_code = int(proxy_response.get("status_code", 0))
    if status_code < 200 or status_code >= 300:
        fail(f"Core proxy returned HTTP {status_code}")
    return {"status": "passed", "kubeconfig": str(path), "proxy_status": status_code}


def write_live_evidence(path: Path, evidence: dict[str, object]) -> None:
    path.parent.mkdir(parents=True, exist_ok=True)
    path.write_text(json.dumps(evidence, ensure_ascii=False, indent=2, sort_keys=True) + "\n", encoding="utf-8")


def main() -> int:
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("--gate", default=str(DEFAULT_GATE), help="vCluster live gate YAML")
    parser.add_argument("--live", action="store_true", help="run live Helm/vCluster/kubectl/Core proxy checks")
    parser.add_argument("--tenant-id", default=os.getenv("ANI_LIVE_TENANT_ID", "tenant-a"))
    parser.add_argument("--cluster-id", default=os.getenv("ANI_LIVE_K8S_CLUSTER_ID", "k8sclu-live"))
    parser.add_argument("--gateway-url", default=os.getenv("ANI_GATEWAY_URL", ""))
    parser.add_argument("--ani-bearer-token", default=os.getenv("ANI_BEARER_TOKEN", ""))
    parser.add_argument("--vcluster-server", default=os.getenv("VCLUSTER_LIVE_SERVER", ""))
    parser.add_argument(
        "--evidence-output",
        default=os.getenv("ANI_VCLUSTER_LIVE_EVIDENCE_OUTPUT", ""),
        help="write --live evidence JSON to this path",
    )
    args = parser.parse_args()

    document = load_gate(Path(args.gate))
    validate_contract(document)
    validate_docs()
    if args.live:
        config = LiveConfig(
            tenant_id=args.tenant_id,
            cluster_id=args.cluster_id,
            gateway_url=args.gateway_url,
            ani_bearer_token=args.ani_bearer_token,
            vcluster_server=args.vcluster_server,
        )
        validate_live_config(config)
        result = run_live(config)
        if args.evidence_output:
            write_live_evidence(Path(args.evidence_output), result)
            print(f"M1-K8S-LIVE-A live checks valid; evidence written to {args.evidence_output}")
        else:
            print(f"M1-K8S-LIVE-A live checks valid: {json.dumps(result, sort_keys=True)}")
    else:
        print("M1-K8S-LIVE-A contract valid; use --live with ANI_GATEWAY_URL and ANI_BEARER_TOKEN")
    return 0


if __name__ == "__main__":
    raise SystemExit(main())
