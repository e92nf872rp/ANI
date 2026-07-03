#!/usr/bin/env python3
"""Validate Sprint 13 instance observability Prometheus live gate contract."""

from __future__ import annotations

import argparse
import datetime
import json
import subprocess
import time
import urllib.error
import urllib.parse
import urllib.request
from pathlib import Path
from typing import Any, Callable

import yaml


ROOT = Path(__file__).resolve().parents[1]
DOC_ROOT = ROOT.parent
DEFAULT_GATE = ROOT / "deploy/real-k8s-lab/instance-observability-live-gate.yaml"
DEFAULT_EVIDENCE = ROOT / "development-records/live-evidence/sprint13-instance-observability-prometheus-live-evidence.json"
PROFILE = "SPRINT13-INSTANCE-OBSERVABILITY-PROMETHEUS-A"
REQUIRED_CHECKS = {
    "prometheus-health-ready",
    "core-instance-logs-list",
    "core-instance-events-list",
    "core-instance-metrics-get",
    "core-instance-security-events-list",
    "core-instance-exec-session-create",
}
REQUIRED_DOC_TOKENS = [
    "SPRINT13-INSTANCE-OBSERVABILITY-PROMETHEUS-A-TRACK",
    "validate-instance-observability-live-gate",
    "Prometheus",
    "kubelet",
    "LIVE PENDING",
]
PROOF_ITEMS = [
    "production_gateway",
    "production_prometheus_service_or_query",
    "production_kubelet_or_kubernetes_api_access",
]


class LiveArgs:
    def __init__(
        self,
        *,
        gateway_url: str,
        ani_bearer_token: str,
        prometheus_url: str,
        kubeconfig: str,
        namespace: str,
        instance_name: str,
        evidence_output: str,
        production_shaped: bool,
        cleanup: bool,
    ) -> None:
        self.gateway_url = gateway_url
        self.ani_bearer_token = ani_bearer_token
        self.prometheus_url = prometheus_url
        self.kubeconfig = kubeconfig
        self.namespace = namespace
        self.instance_name = instance_name
        self.evidence_output = evidence_output
        self.production_shaped = production_shaped
        self.cleanup = cleanup


def fail(message: str) -> None:
    raise SystemExit(f"instance observability live gate invalid: {message}")


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
    if document.get("status") not in {"contract", "live", "passed"}:
        fail("status must be contract, live or passed")
    tools = document.get("required_tools")
    if not isinstance(tools, list) or "curl" not in tools:
        fail("required_tools must include curl")
    checks = document.get("live_checks")
    if not isinstance(checks, list):
        fail("live_checks must be a list")
    check_ids = set()
    for check in checks:
        if not isinstance(check, dict):
            fail("live check must be an object")
        for field in ("id", "command", "pass_condition"):
            value = check.get(field)
            if not isinstance(value, str) or not value.strip():
                fail(f"live check {field} must be a non-empty string")
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


def validate_gate_path(path: str) -> None:
    if not path.strip():
        fail("gate path must not be empty")
    if path != path.strip():
        fail("gate path must not contain surrounding whitespace")


def trim_url(value: str) -> str:
    return value.strip().rstrip("/")


def tenant_namespace(tenant_id: str) -> str:
    tenant = tenant_id.strip().replace("_", "-")
    if not tenant:
        fail("Core instance create response must include tenant_id for namespace resolution")
    return "ani-tenant-" + tenant


def target_namespace(configured_namespace: str, tenant_id: str) -> str:
    namespace = configured_namespace.strip()
    if namespace and namespace.lower() != "auto":
        return namespace
    return tenant_namespace(tenant_id)


def is_local_transport(value: str) -> bool:
    lowered = value.strip().lower()
    return (
        "127.0.0.1" in lowered
        or "localhost" in lowered
        or "kubectl proxy" in lowered
        or "kubectl-proxy" in lowered
        or "port-forward" in lowered
    )


def validate_production_shaped_live_args(args: LiveArgs) -> None:
    if is_local_transport(args.gateway_url):
        fail("production-shaped live mode requires a non-local production gateway URL")
    if is_local_transport(args.prometheus_url):
        fail("production-shaped live mode requires a non-local Prometheus URL")
    if not args.ani_bearer_token.strip():
        fail("production-shaped live mode requires --ani-bearer-token")


def default_json_requester(
    method: str,
    url: str,
    bearer_token: str,
    payload: dict[str, Any] | None = None,
) -> tuple[int, dict[str, Any]]:
    data = None
    headers = {"Accept": "application/json"}
    if payload is not None:
        data = json.dumps(payload).encode("utf-8")
        headers["Content-Type"] = "application/json"
    if bearer_token.strip():
        headers["Authorization"] = f"Bearer {bearer_token.strip()}"
    request = urllib.request.Request(url, data=data, headers=headers, method=method)
    try:
        with urllib.request.urlopen(request, timeout=30) as response:
            status = int(getattr(response, "status", response.getcode()))
            body = response.read().decode("utf-8")
    except urllib.error.HTTPError as exc:
        status = int(exc.code)
        body = exc.read().decode("utf-8", errors="replace")
    except OSError as exc:
        fail(f"could not call instance observability live API: {exc}")
    try:
        parsed = json.loads(body) if body.strip() else {}
    except json.JSONDecodeError:
        fail("instance observability live API did not return JSON")
    if not isinstance(parsed, dict):
        fail("instance observability live API must return a JSON object")
    return status, parsed


def default_text_requester(url: str) -> tuple[int, str]:
    try:
        with urllib.request.urlopen(url, timeout=20) as response:
            return int(getattr(response, "status", response.getcode())), response.read().decode("utf-8", errors="replace")
    except urllib.error.HTTPError as exc:
        return int(exc.code), exc.read().decode("utf-8", errors="replace")
    except OSError as exc:
        fail(f"could not call Prometheus live API: {exc}")


def require_status(status: int, expected: int, label: str) -> None:
    if status != expected:
        fail(f"{label} returned HTTP {status}, want {expected}")


def require_non_empty_string(payload: dict[str, Any], field: str, label: str) -> str:
    value = payload.get(field)
    if not isinstance(value, str) or not value.strip():
        fail(f"{label} must include non-empty {field}")
    return value.strip()


def run_kubectl(args: list[str], *, kubeconfig: str, input_text: str | None = None) -> None:
    cmd = ["kubectl"]
    if kubeconfig.strip():
        cmd.extend(["--kubeconfig", kubeconfig])
    cmd.extend(args)
    try:
        completed = subprocess.run(cmd, input=input_text, text=True, capture_output=True, check=False, timeout=90)
    except OSError as exc:
        fail(f"kubectl failed to start: {exc}")
    except subprocess.TimeoutExpired:
        fail("kubectl command timed out")
    if completed.returncode != 0:
        fail(f"kubectl command failed: {completed.stderr.strip() or completed.stdout.strip()}")


def target_manifest(namespace: str, name: str) -> str:
    event_timestamp = datetime.datetime.now(datetime.UTC).replace(microsecond=0).isoformat().replace("+00:00", "Z")
    return f"""apiVersion: v1
kind: Namespace
metadata:
  name: {namespace}
---
apiVersion: v1
kind: Pod
metadata:
  name: {name}
  namespace: {namespace}
  labels:
    app.kubernetes.io/name: sprint13-observability-live
    ani.kubercloud.io/instance: {name}
spec:
  restartPolicy: Always
  containers:
    - name: main
      image: dockerproxy.net/library/busybox:1.36
      imagePullPolicy: IfNotPresent
      command: ["/bin/sh", "-c"]
      args:
        - while true; do echo "info sprint13 observability live"; sleep 5; done
      resources:
        requests:
          cpu: 10m
          memory: 16Mi
        limits:
          cpu: 100m
          memory: 64Mi
---
apiVersion: v1
kind: Event
metadata:
  name: {name}.sprint13-warning
  namespace: {namespace}
type: Warning
reason: Sprint13LiveGate
message: Sprint 13 observability live gate synthetic warning event
involvedObject:
  apiVersion: v1
  kind: Pod
  name: {name}
  namespace: {namespace}
source:
  component: sprint13-live-gate
firstTimestamp: "{event_timestamp}"
lastTimestamp: "{event_timestamp}"
count: 1
"""


def ensure_target_pod(args: LiveArgs) -> None:
    run_kubectl(["apply", "-f", "-"], kubeconfig=args.kubeconfig, input_text=target_manifest(args.namespace, args.instance_name))
    run_kubectl(["-n", args.namespace, "wait", "--for=condition=Ready", f"pod/{args.instance_name}", "--timeout=120s"], kubeconfig=args.kubeconfig)


def cleanup_target_pod(args: LiveArgs) -> int:
    try:
        run_kubectl(["-n", args.namespace, "delete", "pod", args.instance_name, "--ignore-not-found=true"], kubeconfig=args.kubeconfig)
        run_kubectl(["-n", args.namespace, "delete", "event", f"{args.instance_name}.sprint13-warning", "--ignore-not-found=true"], kubeconfig=args.kubeconfig)
    except SystemExit:
        return 500
    return 200


def validate_live(
    args: LiveArgs,
    json_requester: Callable[[str, str, str, dict[str, Any] | None], tuple[int, dict[str, Any]]] = default_json_requester,
    text_requester: Callable[[str], tuple[int, str]] = default_text_requester,
) -> None:
    if not args.gateway_url.strip():
        fail("live mode requires --gateway-url")
    if not args.prometheus_url.strip():
        fail("live mode requires --prometheus-url")
    if not args.instance_name.strip():
        fail("live mode requires --instance-name")
    if args.production_shaped:
        validate_production_shaped_live_args(args)

    base_url = trim_url(args.gateway_url)
    prometheus_status, _ = text_requester(trim_url(args.prometheus_url) + "/-/ready")
    require_status(prometheus_status, 200, "Prometheus readiness")

    idempotency = "sprint13-instance-observability-create-" + str(int(time.time()))
    create_status, created = json_requester(
        "POST",
        f"{base_url}/instances",
        args.ani_bearer_token,
        {
            "kind": "container",
            "name": args.instance_name,
            "image": "dockerproxy.net/library/busybox:1.36",
            "cpu": "100m",
            "memory": "64Mi",
            "idempotency_key": idempotency,
        },
    )
    require_status(create_status, 201, "Core instance create")
    instance = created.get("instance")
    if not isinstance(instance, dict):
        fail("Core instance create response must include instance")
    instance_id = require_non_empty_string(instance, "id", "Core instance create response")
    instance_tenant_id = require_non_empty_string(instance, "tenant_id", "Core instance create response")
    args.namespace = target_namespace(args.namespace, instance_tenant_id)
    ensure_target_pod(args)

    logs_status, logs = json_requester("GET", f"{base_url}/instances/{instance_id}/logs?limit=20", args.ani_bearer_token, None)
    require_status(logs_status, 200, "Core instance logs")
    if not isinstance(logs.get("items"), list) or logs.get("total", 0) < 1:
        fail("Core instance logs must include at least one log item")

    events_status, events = json_requester("GET", f"{base_url}/instances/{instance_id}/events?type=Warning", args.ani_bearer_token, None)
    require_status(events_status, 200, "Core instance events")
    if not isinstance(events.get("items"), list) or events.get("total", 0) < 1:
        fail("Core instance events must include at least one warning event")

    metrics_status = 0
    metrics: dict[str, Any] = {}
    for attempt in range(1, 13):
        metrics_status, metrics = json_requester("GET", f"{base_url}/instances/{instance_id}/metrics", args.ani_bearer_token, None)
        if metrics_status == 200 and isinstance(metrics.get("cpu_utilization_pct"), (int, float)):
            break
        if attempt < 12:
            time.sleep(5)
    require_status(metrics_status, 200, "Core instance metrics")
    if not isinstance(metrics.get("cpu_utilization_pct"), (int, float)):
        fail("Core instance metrics must include cpu_utilization_pct")

    security_status, security = json_requester("GET", f"{base_url}/instances/{instance_id}/security-events?severity=warning", args.ani_bearer_token, None)
    require_status(security_status, 200, "Core instance security events")
    if not isinstance(security.get("items"), list) or security.get("total", 0) < 1:
        fail("Core instance security events must include at least one warning event")

    exec_status, exec_session = json_requester(
        "POST",
        f"{base_url}/instances/{instance_id}/exec",
        args.ani_bearer_token,
        {"idempotency_key": "sprint13-instance-observability-exec", "command": ["/bin/sh"], "tty": True},
    )
    require_status(exec_status, 200, "Core instance exec session")
    ws_url = require_non_empty_string(exec_session, "ws_url", "Core instance exec session response")
    if exec_session.get("token"):
        fail("Core instance exec session response must not expose a long-lived token")

    cleanup_status = 0
    if args.cleanup:
        cleanup_status = cleanup_target_pod(args)
        if cleanup_status != 200:
            fail("cleanup failed")

    evidence: dict[str, Any] = {
        "id": "instance-observability-live-gate",
        "profile": PROFILE,
        "status": "passed",
        "prometheus_health_status": prometheus_status,
        "instance_create_status": create_status,
        "logs_status": logs_status,
        "logs_count": logs.get("total", 0),
        "events_status": events_status,
        "events_count": events.get("total", 0),
        "metrics_status": metrics_status,
        "metrics_cpu_present": True,
        "security_events_status": security_status,
        "security_events_count": security.get("total", 0),
        "exec_status": exec_status,
        "exec_ws_url_present": bool(ws_url),
        "exec_token_exposed": False,
        "cleanup_enabled": args.cleanup,
        "cleanup_status": cleanup_status,
    }
    if args.production_shaped:
        evidence["production_shape"] = {
            "status": "passed",
            "transport_profile": "production_gateway_prometheus_and_kubernetes_api",
            "missing_items": [],
            "proof_items": PROOF_ITEMS,
        }
    if args.evidence_output.strip():
        output = Path(args.evidence_output)
        output.parent.mkdir(parents=True, exist_ok=True)
        output.write_text(json.dumps(evidence, indent=2, sort_keys=True) + "\n", encoding="utf-8")


def main() -> int:
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("--gate", default=str(DEFAULT_GATE), help="instance observability live gate YAML")
    parser.add_argument("--live", action="store_true", help="execute the human-gated live checks")
    parser.add_argument("--production-shaped", action="store_true", help="require production-shaped transport and proof_items")
    parser.add_argument("--gateway-url", default="", help="ANI Core API base URL ending at /api/v1")
    parser.add_argument("--ani-bearer-token", default="", help="ANI Core bearer token")
    parser.add_argument("--prometheus-url", default="", help="approved Prometheus HTTP endpoint")
    parser.add_argument("--kubeconfig", default="", help="management kubeconfig for temporary target pod setup")
    parser.add_argument("--namespace", default="auto", help="tenant namespace for the temporary target pod; auto derives it from Core tenant_id")
    parser.add_argument("--instance-name", default="sprint13-observability-live", help="Kubernetes-safe Core instance name / target pod name")
    parser.add_argument("--evidence-output", default=str(DEFAULT_EVIDENCE), help="write non-sensitive evidence JSON")
    parser.add_argument("--cleanup", action="store_true", help="delete temporary target pod/event after live check")
    args = parser.parse_args()

    validate_gate_path(args.gate)
    document = load_gate(Path(args.gate))
    validate_contract(document)
    validate_docs()
    if args.live:
        validate_live(
            LiveArgs(
                gateway_url=args.gateway_url,
                ani_bearer_token=args.ani_bearer_token,
                prometheus_url=args.prometheus_url,
                kubeconfig=args.kubeconfig,
                namespace=args.namespace,
                instance_name=args.instance_name,
                evidence_output=args.evidence_output,
                production_shaped=args.production_shaped,
                cleanup=args.cleanup,
            )
        )
        print("SPRINT13-INSTANCE-OBSERVABILITY-PROMETHEUS-A live checks passed")
        return 0
    print("SPRINT13-INSTANCE-OBSERVABILITY-PROMETHEUS-A contract valid; use --live for human-gated execution")
    return 0


if __name__ == "__main__":
    raise SystemExit(main())
