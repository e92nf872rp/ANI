"""Tests for kb-service configuration (issue-006 / SPEC §2.4).

Verifies the Settings model loads from the shared `.env` without crashing
on unrelated env vars (MINIO_*, AUTH_*, GATEWAY_PORT, etc. belong to other
ANI services), and that the field names map to the project .env conventions
(DATABASE_URL / NATS_URL / REDIS_URL / ANI_GATEWAY_INTERNAL_URL).
"""
import os
import sys

import pytest

_SERVICE_ROOT = os.path.abspath(os.path.join(os.path.dirname(__file__), ".."))
sys.path.insert(0, _SERVICE_ROOT)

from app.core.config import Settings


def test_settings_instantiate_with_extra_env_vars_ignored():
    """Shared .env has many unrelated keys; kb-service must not crash (extra=ignore)."""
    # Simulate a shared .env with unrelated keys plus kb-service keys.
    env = {
        "MINIO_ENDPOINT": "localhost:9000",      # not for kb-service
        "AUTH_JWT_ISSUER": "ani-dev",             # not for kb-service
        "GATEWAY_PORT": "8080",                   # not for kb-service
        "GRPC_PORT": "50199",                     # kb-service
        "DATABASE_URL": "postgres://u:p@h:5432/d",  # kb-service
        "NATS_URL": "nats://h:4222",              # kb-service
        "REDIS_URL": "redis://h:6379/0",          # kb-service
    }
    with _patch_env(env):
        s = Settings()
    assert s.grpc_port == 50199
    assert s.database_url == "postgres://u:p@h:5432/d"
    assert s.nats_url == "nats://h:4222"
    assert s.redis_url == "redis://h:6379/0"


def test_core_api_base_url_derived_from_gateway_internal_url():
    s = Settings(ani_gateway_internal_url="http://gw:8080")
    assert s.core_api_base_url == "http://gw:8080/api/v1"
    # trailing slash on the internal url is stripped
    s2 = Settings(ani_gateway_internal_url="http://gw:8080/")
    assert s2.core_api_base_url == "http://gw:8080/api/v1"


def test_default_core_api_base_url_points_to_core_api_v1():
    s = Settings()
    assert s.core_api_base_url.endswith("/api/v1")
    assert "ani-gateway" in s.core_api_base_url


import contextlib


@contextlib.contextmanager
def _patch_env(env):
    old = {}
    for k, v in env.items():
        old[k] = os.environ.get(k)
        os.environ[k] = v
    try:
        yield
    finally:
        for k, v in old.items():
            if v is None:
                os.environ.pop(k, None)
            else:
                os.environ[k] = v
