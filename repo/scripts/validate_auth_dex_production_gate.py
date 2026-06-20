#!/usr/bin/env python3
"""Validate Sprint 13 Auth/Dex production-shaped gate contract and evidence."""

from __future__ import annotations

import argparse
import json
from pathlib import Path
from typing import Any

import yaml


ROOT = Path(__file__).resolve().parents[1]
DOC_ROOT = ROOT.parent
DEFAULT_GATE = ROOT / "deploy/real-k8s-lab/auth-dex-production-gate.yaml"
DEFAULT_MANIFEST = ROOT / "deploy/real-k8s-lab/sprint13-production-auth-dex.yaml"
DEFAULT_EVIDENCE = ROOT / "development-records/live-evidence/sprint13-auth-dex-production-evidence.json"
LIVE_RUNNER = ROOT / "scripts/validate_auth_dex_production_live.py"
DB_INIT_SQL = ROOT / "deploy/real-k8s-lab/auth-dex-production-db-init.sql"
PROFILE = "SPRINT13-AUTH-DEX-PRODUCTION-GATE"
REQUIRED_CHECKS = {
    "dex-discovery-ready",
    "dex-jwks-ready",
    "auth-service-grpc-ready",
    "gateway-protected-route-rejects-anonymous",
    "gateway-oidc-auth-code-flow",
    "gateway-protected-route-accepts-dex-token",
    "gateway-refresh-token-flow",
}
REQUIRED_TOOLS = {"kubectl", "curl"}
REQUIRED_PROOF_ITEMS = {
    "gateway_non_dev_auth",
    "dex_discovery_and_jwks",
    "gateway_rejects_anonymous",
    "gateway_accepts_dex_oidc_token",
    "gateway_refresh_token",
    "auth_service_rbac_check",
}
REQUIRED_DOC_TOKENS = [
    "SPRINT13-AUTH-DEX-PRODUCTION-GATE",
    "validate-auth-dex-production-gate",
    "Auth/Dex production gate",
    "ANI_AUTH_MODE=auth_service",
    "production ready",
]


def fail(message: str) -> None:
    raise SystemExit(f"auth dex production gate invalid: {message}")


def path_label(path: Path) -> str:
    try:
        return str(path.relative_to(ROOT))
    except ValueError:
        try:
            return str(path.relative_to(DOC_ROOT))
        except ValueError:
            return str(path)


def load_yaml(path: Path) -> Any:
    label = path_label(path)
    if not path.exists():
        fail(f"missing {label}")
    try:
        with path.open(encoding="utf-8") as handle:
            return yaml.safe_load(handle)
    except OSError:
        fail(f"unreadable {label}")
    except yaml.YAMLError:
        fail(f"malformed {label}")


def load_gate(path: Path) -> dict[str, Any]:
    data = load_yaml(path)
    if not isinstance(data, dict):
        fail(f"{path_label(path)} must be a YAML object")
    return data


def load_production_manifest(path: Path) -> list[dict[str, Any]]:
    label = path_label(path)
    if not path.exists():
        fail(f"missing {label}")
    try:
        docs = [doc for doc in yaml.safe_load_all(path.read_text(encoding="utf-8")) if isinstance(doc, dict)]
    except OSError:
        fail(f"unreadable {label}")
    except yaml.YAMLError as exc:
        fail(f"malformed {label}: {exc}")
    if not docs:
        fail(f"{label} must contain Kubernetes resources")
    return docs


def validate_contract(document: dict[str, Any]) -> None:
    if not LIVE_RUNNER.exists():
        fail(f"missing {path_label(LIVE_RUNNER)}")
    if not DB_INIT_SQL.exists():
        fail(f"missing {path_label(DB_INIT_SQL)}")
    if document.get("profile") != PROFILE:
        fail(f"profile must be {PROFILE}")
    if document.get("status") not in {"contract", "live", "passed"}:
        fail("status must be contract, live or passed")
    tools = document.get("required_tools")
    if not isinstance(tools, list) or not REQUIRED_TOOLS.issubset(set(tools)):
        fail("required_tools must include kubectl and curl")
    components = document.get("required_components")
    if not isinstance(components, list) or {"dex", "auth-service", "ani-gateway"} - set(components):
        fail("required_components must include dex, auth-service and ani-gateway")
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
        check_ids.add(str(check["id"]))
    missing = REQUIRED_CHECKS - check_ids
    if missing:
        fail(f"missing live checks: {', '.join(sorted(missing))}")


def find_deployment(docs: list[dict[str, Any]], name: str) -> dict[str, Any]:
    for doc in docs:
        if doc.get("kind") == "Deployment" and doc.get("metadata", {}).get("name") == name:
            return doc
    fail(f"manifest missing Deployment {name}")


def find_resource(docs: list[dict[str, Any]], kind: str, name: str) -> dict[str, Any]:
    for doc in docs:
        if doc.get("kind") == kind and doc.get("metadata", {}).get("name") == name:
            return doc
    fail(f"manifest missing {kind} {name}")


def container_env(deployment: dict[str, Any], container_name: str) -> dict[str, dict[str, Any]]:
    containers = deployment.get("spec", {}).get("template", {}).get("spec", {}).get("containers", [])
    if not isinstance(containers, list):
        fail(f"Deployment {deployment.get('metadata', {}).get('name')} must define containers")
    for container in containers:
        if isinstance(container, dict) and container.get("name") == container_name:
            env = container.get("env")
            if not isinstance(env, list):
                fail(f"container {container_name} must define env")
            return {item.get("name"): item for item in env if isinstance(item, dict)}
    fail(f"Deployment {deployment.get('metadata', {}).get('name')} missing container {container_name}")


def find_container(deployment: dict[str, Any], container_name: str) -> dict[str, Any]:
    containers = deployment.get("spec", {}).get("template", {}).get("spec", {}).get("containers", [])
    if not isinstance(containers, list):
        fail(f"Deployment {deployment.get('metadata', {}).get('name')} must define containers")
    for container in containers:
        if isinstance(container, dict) and container.get("name") == container_name:
            return container
    fail(f"Deployment {deployment.get('metadata', {}).get('name')} missing container {container_name}")


def require_env_value(env: dict[str, dict[str, Any]], name: str, value: str) -> None:
    item = env.get(name)
    if not isinstance(item, dict) or item.get("value") != value:
        fail(f"{name} must be {value}")


def require_env_secret(env: dict[str, dict[str, Any]], name: str) -> None:
    item = env.get(name)
    value_from = item.get("valueFrom") if isinstance(item, dict) else None
    secret_key_ref = value_from.get("secretKeyRef") if isinstance(value_from, dict) else None
    if not isinstance(secret_key_ref, dict) or not secret_key_ref.get("name") or not secret_key_ref.get("key"):
        fail(f"{name} must come from secretKeyRef")


def validate_production_manifest(docs: list[dict[str, Any]]) -> None:
    dex_config = find_resource(docs, "ConfigMap", "ani-dex-production-config")
    dex_config_text = dex_config.get("data", {}).get("config.yaml")
    if not isinstance(dex_config_text, str) or "issuer:" not in dex_config_text:
        fail("Dex ConfigMap must contain config.yaml with issuer")
    if "$ANI_DEX_CLIENT_SECRET" not in dex_config_text:
        fail("Dex static client secret must come from environment substitution")
    if "enablePasswordDB: true" not in dex_config_text:
        fail("Dex production-shaped gate must enable controlled password DB fixture")

    dex = find_deployment(docs, "ani-dex")
    dex_env = container_env(dex, "dex")
    require_env_secret(dex_env, "ANI_DEX_CLIENT_SECRET")
    dex_container = find_container(dex, "dex")
    dex_command = dex_container.get("command")
    dex_args = dex_container.get("args")
    rendered_config = "\n".join(str(item) for item in (dex_args or []))
    if dex_command != ["/bin/sh", "-ec"] or "ANI_DEX_CLIENT_SECRET" not in rendered_config or "/tmp/dex-config.yaml" not in rendered_config:
        fail("Dex container must render client secret from Secret before startup")
    dex_service = find_resource(docs, "Service", "ani-dex")
    dex_service_spec = dex_service.get("spec", {})
    if dex_service_spec.get("type") != "NodePort":
        fail("Dex Service must be NodePort for non-local OIDC live gate access")
    dex_ports = dex_service_spec.get("ports")
    if not isinstance(dex_ports, list) or not dex_ports or dex_ports[0].get("nodePort") != 30556:
        fail("Dex Service must expose nodePort 30556 for Auth/Dex production live gate")

    auth = find_deployment(docs, "ani-auth-service")
    find_resource(docs, "ServiceAccount", "ani-auth-service")
    auth_pod_spec = auth.get("spec", {}).get("template", {}).get("spec", {})
    if auth_pod_spec.get("serviceAccountName") != "ani-auth-service":
        fail("Auth service Deployment must use ani-auth-service ServiceAccount")
    auth_env = container_env(auth, "auth-service")
    for name in (
        "DATABASE_URL",
        "NATS_URL",
        "REDIS_URL",
        "AUTH_JWT_PUBLIC_KEY_PEM",
        "AUTH_JWT_PRIVATE_KEY_PEM",
        "AUTH_OIDC_CLIENT_SECRET",
    ):
        require_env_secret(auth_env, name)
    require_env_value(auth_env, "AUTH_OIDC_CLIENT_ID", "ani-console")
    if not auth_env.get("AUTH_OIDC_ISSUER_URL", {}).get("value", "").startswith("http://ani-dex.ani-system.svc.cluster.local:5556/dex"):
        fail("AUTH_OIDC_ISSUER_URL must point at in-cluster Dex service")

    gateway = find_deployment(docs, "ani-gateway")
    gateway_env = container_env(gateway, "ani-gateway")
    auth_mode = gateway_env.get("ANI_AUTH_MODE", {}).get("value")
    if auth_mode == "dev":
        fail("Gateway ANI_AUTH_MODE must not be dev")
    if auth_mode != "auth_service":
        fail("Gateway ANI_AUTH_MODE must be auth_service")
    require_env_value(gateway_env, "AUTH_SERVICE_ADDR", "ani-auth-service.ani-system.svc.cluster.local:9101")

    for service_name in ("ani-auth-service", "ani-gateway"):
        find_resource(docs, "Service", service_name)


def validate_evidence_payload(payload: dict[str, Any]) -> None:
    if payload.get("status") != "passed":
        fail("evidence status must be passed")
    shape = payload.get("auth_dex_production_shape")
    if not isinstance(shape, dict):
        fail("evidence must include auth_dex_production_shape")
    if shape.get("status") != "passed":
        fail("auth_dex_production_shape.status must be passed")
    if shape.get("gateway_auth_mode") == "dev":
        fail("gateway_auth_mode must not be dev")
    if shape.get("gateway_auth_mode") != "auth_service":
        fail("gateway_auth_mode must be auth_service")
    proof_items = shape.get("proof_items")
    if not isinstance(proof_items, list):
        fail("auth_dex_production_shape.proof_items must be a list")
    missing = REQUIRED_PROOF_ITEMS - {str(item) for item in proof_items}
    if missing:
        fail(f"auth_dex_production_shape proof_items missing {', '.join(sorted(missing))}")
    expected_statuses = {
        "anonymous_status": 401,
        "oidc_begin_status": 200,
        "oidc_complete_status": 200,
        "authorized_status": 200,
        "refresh_status": 200,
    }
    for field, expected in expected_statuses.items():
        if shape.get(field) != expected:
            fail(f"auth_dex_production_shape requires {field}={expected}")


def load_evidence(path: Path) -> dict[str, Any]:
    if not path.exists():
        fail(f"missing evidence {path_label(path)}")
    try:
        payload = json.loads(path.read_text(encoding="utf-8"))
    except json.JSONDecodeError as exc:
        fail(f"malformed evidence {path_label(path)}: {exc}")
    if not isinstance(payload, dict):
        fail("evidence must be a JSON object")
    return payload


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


def main() -> int:
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("--gate", default=str(DEFAULT_GATE), help="Auth/Dex production gate YAML")
    parser.add_argument("--manifest", default=str(DEFAULT_MANIFEST), help="production Auth/Dex manifest")
    parser.add_argument("--evidence", default="", help="optional Auth/Dex production evidence JSON")
    args = parser.parse_args()

    document = load_gate(Path(args.gate))
    validate_contract(document)
    validate_production_manifest(load_production_manifest(Path(args.manifest)))
    validate_docs()
    if args.evidence:
        validate_evidence_payload(load_evidence(Path(args.evidence)))
    print("SPRINT13-AUTH-DEX-PRODUCTION-GATE contract valid")
    return 0


if __name__ == "__main__":
    raise SystemExit(main())
