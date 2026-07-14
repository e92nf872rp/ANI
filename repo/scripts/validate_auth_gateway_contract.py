#!/usr/bin/env python3
"""Validate the M2.2 Auth Gateway REST contract.

This is a local, non-Docker guard. It keeps the Core API contract, Gateway route
registrations, and auth middleware public/protected boundary aligned.
"""

from __future__ import annotations

import sys
from pathlib import Path

import yaml


EXPECTED_PATHS = {
    "/auth/oidc/begin": "beginOIDCLogin",
    "/auth/token": "completeOIDCLogin",
    "/auth/refresh": None,
    "/auth/logout": "logout",
    "/auth/api-keys": None,
    "/auth/api-keys/{key_id}": "revokeAPIKey",
}

EXPECTED_AUTH_ROUTES = {
    'v1.POST("/auth/oidc/begin", api.beginOIDC)',
    'v1.POST("/auth/token", api.completeOIDC)',
    'v1.POST("/auth/refresh", api.refresh)',
    'v1.POST("/auth/logout", api.logout)',
    'v1.GET("/auth/api-keys", api.listAPIKeys)',
    'v1.POST("/auth/api-keys", api.createAPIKey)',
    'v1.DELETE("/auth/api-keys/:key_id", api.revokeAPIKey)',
}

PUBLIC_PATHS = {
    '"/api/v1/auth/oidc/begin"',
    '"/api/v1/auth/token"',
    '"/api/v1/auth/refresh"',
}

PROTECTED_PATHS = {
    '"/api/v1/auth/logout"',
    '"/api/v1/auth/api-keys"',
}

EXPECTED_SCHEMAS = {
    "BeginOIDCLoginRequest",
    "BeginOIDCLoginResponse",
    "CompleteOIDCLoginRequest",
    "TokenPairResponse",
    "RefreshAccessTokenRequest",
    "RefreshAccessTokenResponse",
    "LogoutRequest",
    "RevokeStatusResponse",
    "CreateAPIKeyRequest",
    "CreateAPIKeyResponse",
    "APIKeyInfo",
    "ListAPIKeysResponse",
}


def fail(errors: list[str]) -> None:
    if errors:
        for error in errors:
            print(f"ERROR: {error}", file=sys.stderr)
        raise SystemExit(1)


def load_yaml(path: Path) -> dict:
    with path.open("r", encoding="utf-8") as handle:
        data = yaml.safe_load(handle)
    if not isinstance(data, dict):
        raise SystemExit(f"ERROR: {path} did not parse to an object")
    return data


def validate_api_contract(root: Path, errors: list[str]) -> None:
    spec = load_yaml(root / "api/openapi/v1.yaml")
    paths = spec.get("paths", {})
    schemas = spec.get("components", {}).get("schemas", {})
    for path, operation_id in EXPECTED_PATHS.items():
        if path not in paths:
            errors.append(f"api/openapi/v1.yaml missing {path}")
            continue
        if operation_id is None:
            continue
        operations = [value for value in paths[path].values() if isinstance(value, dict)]
        if not any(operation.get("operationId") == operation_id for operation in operations):
            errors.append(f"{path} missing operationId {operation_id}")
    for schema in EXPECTED_SCHEMAS:
        if schema not in schemas:
            errors.append(f"api/openapi/v1.yaml missing schema {schema}")
    create_api_key = schemas.get("CreateAPIKeyRequest", {})
    create_api_key_required = set(create_api_key.get("required", []))
    create_api_key_props = create_api_key.get("properties", {})
    if "idempotency_key" not in create_api_key_required or "idempotency_key" not in create_api_key_props:
        errors.append("CreateAPIKeyRequest must require idempotency_key")


def validate_gateway_routes(root: Path, errors: list[str]) -> None:
    auth_go = (root / "services/ani-gateway/internal/router/auth.go").read_text(encoding="utf-8")
    for route in EXPECTED_AUTH_ROUTES:
        if route not in auth_go:
            errors.append(f"router/auth.go missing route registration: {route}")
    if "notImplemented" in auth_go:
        errors.append("router/auth.go must not route Auth endpoints to notImplemented")

    stubs_go = (root / "services/ani-gateway/internal/router/stubs.go").read_text(encoding="utf-8")
    if "registerAuth" in stubs_go or '"/auth/' in stubs_go:
        errors.append("router/stubs.go must not contain Auth route stubs")


def validate_auth_middleware(root: Path, errors: list[str]) -> None:
    auth_go = (root / "services/ani-gateway/internal/middleware/auth.go").read_text(encoding="utf-8")
    for path in PUBLIC_PATHS:
        if path not in auth_go:
            errors.append(f"middleware/auth.go public path missing {path}")
    for path in PROTECTED_PATHS:
        if path in auth_go:
            errors.append(f"middleware/auth.go must keep {path} protected")


def main() -> None:
    root = Path(__file__).resolve().parents[1]
    errors: list[str] = []
    validate_api_contract(root, errors)
    validate_gateway_routes(root, errors)
    validate_auth_middleware(root, errors)
    fail(errors)
    print("auth gateway contract valid")


if __name__ == "__main__":
    main()
