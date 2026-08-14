"""Tests for the Redis session cache (issue-008 / US-010, SPEC §6.1, FR-9).

Verifies:
- append_message issues RPUSH + EXPIRE(24h) + LTRIM(20) in a pipeline.
- list_messages reads and JSON-decodes the cached list entries.
- The cache is best-effort: Redis failures are swallowed (Query still works).

Uses a mock Redis client so no real Redis is required.
"""
import asyncio
import json
import os
import sys
from unittest.mock import AsyncMock, MagicMock

import pytest

_SERVICE_ROOT = os.path.abspath(os.path.join(os.path.dirname(__file__), ".."))
sys.path.insert(0, _SERVICE_ROOT)
sys.path.insert(0, os.path.join(_SERVICE_ROOT, "app", "generated"))

from app.session.cache import (
    KEY_PREFIX,
    SESSION_MAX_ENTRIES,
    SESSION_TTL_SECONDS,
    SessionCache,
)


SESSION_ID = "sess-1234"


class _MockPipeline:
    """Records rpush/expire/ltrim calls; execute() returns ordered results."""

    def __init__(self):
        self.ops: list[tuple[str, tuple]] = []
        self._results: list = []

    def rpush(self, key, entry):
        self.ops.append(("rpush", (key, entry)))
        return self

    def expire(self, key, ttl):
        self.ops.append(("expire", (key, ttl)))
        return self

    def ltrim(self, key, start, stop):
        self.ops.append(("ltrim", (key, start, stop)))
        return self

    def __await__(self):
        async def _run():
            return self._results
        return _run().__await__()

    async def execute(self):
        return self._results


class _MockRedis:
    """Mock redis.asyncio client with pipeline() + lrange()."""

    def __init__(self):
        self.pipeline_obj = _MockPipeline()
        self.lrange_result: list = []
        self._lrange_calls: list[tuple] = []
        self._raise_on = None  # method name that should raise

    def pipeline(self):
        return self.pipeline_obj

    async def lrange(self, key, start, stop):
        self._lrange_calls.append((key, start, stop))
        if self._raise_on == "lrange":
            raise RuntimeError("redis down")
        return self.lrange_result


# ── append_message ────────────────────────────────────────────────────────────


@pytest.mark.asyncio
async def test_append_message_issues_rpush_expire_ltrim():
    redis = _MockRedis()
    cache = SessionCache(redis=redis)
    await cache.append_message(session_id=SESSION_ID, role="user", content="hi")

    ops = redis.pipeline_obj.ops
    assert ("rpush", (f"{KEY_PREFIX}{SESSION_ID}", json.dumps({"role": "user", "content": "hi"}))) in ops
    assert ("expire", (f"{KEY_PREFIX}{SESSION_ID}", SESSION_TTL_SECONDS)) in ops
    # LTRIM keeps most recent 20: -20..-1
    assert ("ltrim", (f"{KEY_PREFIX}{SESSION_ID}", -SESSION_MAX_ENTRIES, -1)) in ops


@pytest.mark.asyncio
async def test_append_message_ttl_is_24h():
    assert SESSION_TTL_SECONDS == 24 * 60 * 60


@pytest.mark.asyncio
async def test_append_message_includes_extra_fields():
    redis = _MockRedis()
    cache = SessionCache(redis=redis)
    await cache.append_message(
        session_id=SESSION_ID, role="assistant", content="answer",
        sources=[{"doc_id": "d1"}], input_tokens=10, output_tokens=5,
    )
    entry = redis.pipeline_obj.ops[0][1][1]
    decoded = json.loads(entry)
    assert decoded["role"] == "assistant"
    assert decoded["content"] == "answer"
    assert decoded["sources"] == [{"doc_id": "d1"}]
    assert decoded["input_tokens"] == 10


@pytest.mark.asyncio
async def test_append_message_swallows_redis_error():
    """Redis failure must not propagate to the caller (best-effort cache)."""
    redis = _MockRedis()
    # Make pipeline.execute raise
    redis.pipeline_obj.execute = AsyncMock(side_effect=RuntimeError("redis down"))
    cache = SessionCache(redis=redis)
    # Should not raise
    await cache.append_message(session_id=SESSION_ID, role="user", content="hi")


# ── list_messages ─────────────────────────────────────────────────────────────


@pytest.mark.asyncio
async def test_list_messages_decodes_json_entries():
    redis = _MockRedis()
    redis.lrange_result = [
        json.dumps({"role": "user", "content": "q"}).encode("utf-8"),
        json.dumps({"role": "assistant", "content": "a"}).encode("utf-8"),
    ]
    cache = SessionCache(redis=redis)
    msgs = await cache.list_messages(session_id=SESSION_ID, limit=20)
    assert len(msgs) == 2
    assert msgs[0]["role"] == "user"
    assert msgs[1]["content"] == "a"
    # lrange called with key + 0..limit-1
    assert redis._lrange_calls[0][0] == f"{KEY_PREFIX}{SESSION_ID}"
    assert redis._lrange_calls[0][1] == 0


@pytest.mark.asyncio
async def test_list_messages_returns_empty_on_redis_error():
    redis = _MockRedis()
    redis._raise_on = "lrange"
    cache = SessionCache(redis=redis)
    msgs = await cache.list_messages(session_id=SESSION_ID)
    assert msgs == []


@pytest.mark.asyncio
async def test_list_messages_skips_invalid_json_entries():
    redis = _MockRedis()
    redis.lrange_result = [
        json.dumps({"role": "user", "content": "ok"}).encode("utf-8"),
        b"not-json",
        json.dumps({"role": "assistant", "content": "a"}).encode("utf-8"),
    ]
    cache = SessionCache(redis=redis)
    msgs = await cache.list_messages(session_id=SESSION_ID)
    assert len(msgs) == 2  # invalid entry skipped


@pytest.mark.asyncio
async def test_list_messages_handles_string_entries():
    """If redis returns str (decode_responses=True), entries still decode."""
    redis = _MockRedis()
    redis.lrange_result = [
        json.dumps({"role": "user", "content": "q"}),
    ]
    cache = SessionCache(redis=redis)
    msgs = await cache.list_messages(session_id=SESSION_ID)
    assert msgs[0]["content"] == "q"


# ── key format ─────────────────────────────────────────────────────────────────


def test_key_prefix_matches_spec():
    # FR-9: ani:prod:session:kb:{session_id}
    assert KEY_PREFIX == "ani:prod:session:kb:"


def test_max_entries_is_20():
    assert SESSION_MAX_ENTRIES == 20
