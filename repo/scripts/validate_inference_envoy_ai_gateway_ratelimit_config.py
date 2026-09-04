#!/usr/bin/env python3
"""Validate the non-secret Envoy Gateway Redis rate-limit configuration fragment."""

from __future__ import annotations

from pathlib import Path
from typing import Any

import yaml

ROOT = Path(__file__).resolve().parents[1]
DEFAULT_CONFIG = ROOT / "deploy/real-k8s-lab/inference-envoy-ai-gateway-ratelimit-config.yaml"


def fail(message: str) -> None:
    raise SystemExit(f"inference envoy ai gateway rate-limit config invalid: {message}")


def validate(document: dict[str, Any]) -> None:
    if document.get("apiVersion") != "v1":
        fail("fragment must use v1")
    if document.get("kind") != "ConfigMap":
        fail("fragment kind must be ConfigMap")
    metadata = document.get("metadata", {})
    if metadata.get("namespace") != "envoy-gateway-system":
        fail("fragment must be in envoy-gateway-system")
    embedded = document.get("data", {}).get("envoy-gateway.yaml")
    if not isinstance(embedded, str):
        fail("fragment must contain data.envoy-gateway.yaml")
    try:
        gateway = yaml.safe_load(embedded)
    except yaml.YAMLError as err:
        fail(f"embedded EnvoyGateway YAML is malformed: {err}")
    expected = {
        "type": "Redis",
        "redis": {"urlRef": {"secretKeyRef": {"name": "ani-envoy-ratelimit-redis", "key": "REDIS_ENDPOINT"}}},
    }
    if not isinstance(gateway, dict) or gateway.get("apiVersion") != "gateway.envoyproxy.io/v1alpha1" or gateway.get("kind") != "EnvoyGateway":
        fail("embedded document must be an EnvoyGateway v1alpha1 object")
    if gateway.get("rateLimit", {}).get("backend") != expected:
        fail("backend must reference the Redis endpoint Secret without embedding credentials")
    rendered = str(document)
    if "redis://" in rendered.lower() or "password" in rendered.lower() or "ani_dev_" in rendered.lower() or "ani_prod_" in rendered.lower():
        fail("fragment must not contain plaintext connection or API-key material")


def main() -> None:
    try:
        document = yaml.safe_load(DEFAULT_CONFIG.read_text(encoding="utf-8"))
    except yaml.YAMLError as err:
        fail(f"malformed YAML: {err}")
    if not isinstance(document, dict):
        fail("fragment must be a YAML object")
    validate(document)
    print("inference envoy ai gateway rate-limit config valid")


if __name__ == "__main__":
    main()
