"""Redis session cache for the Query RPC (SPEC §6.1 Query, FR-9, US-010).

SPEC §6.1 Query algorithm — the Redis steps:

    4. Redis: RPUSH ani:prod:session:kb:{session_id} <user_msg>;
              EXPIRE 24h; LTRIM 0 19
    ...
    7. Redis: RPUSH <assistant_msg>; LTRIM 20

Key convention (FR-9):  `ani:prod:session:kb:{session_id}`
TTL:                    24 hours (86400 seconds)
Cap:                    LTRIM keeps the most recent 20 entries (indices 0..19)

Redis is a best-effort layer (SPEC §7.3): if it is unavailable, Query degrades
to DB-only persistence (kb_messages) and still returns a correct answer. All
cache operations swallow errors and log a warning rather than failing the
Query RPC.
"""
from __future__ import annotations

import json
import logging
from typing import Any

logger = logging.getLogger(__name__)

# FR-9 / SPEC §6.1 constants.
KEY_PREFIX = "ani:prod:session:kb:"
SESSION_TTL_SECONDS = 24 * 60 * 60  # 24h
SESSION_MAX_ENTRIES = 20  # LTRIM keeps 0..19 (most recent 20)


def _session_key(session_id: str) -> str:
    return f"{KEY_PREFIX}{session_id}"


def _encode_message(role: str, content: str, **extra: Any) -> str:
    """Serialize a message to the cache list entry format (JSON line)."""
    payload: dict[str, Any] = {"role": role, "content": content}
    payload.update(extra)
    return json.dumps(payload, default=str, ensure_ascii=False)


class SessionCache:
    """Redis-backed session cache for kb-service Query.

    Uses redis-py asyncio. All methods are best-effort: they catch RedisError
    and log a warning, never raising to the caller (SPEC §7.3 degradation).
    """

    def __init__(self, *, redis: Any, ttl_seconds: int = SESSION_TTL_SECONDS,
                 max_entries: int = SESSION_MAX_ENTRIES) -> None:
        self._redis = redis
        self._ttl = ttl_seconds
        self._max = max_entries

    async def append_message(
        self, *, session_id: str, role: str, content: str, **extra: Any
    ) -> None:
        """RPUSH a message, EXPIRE 24h, LTRIM to the most recent N entries.

        Implements SPEC §6.1 steps 4 & 7 in one call. Best-effort: on Redis
        failure, logs and returns (Query continues with DB persistence only).
        """
        try:
            key = _session_key(session_id)
            entry = _encode_message(role, content, **extra)
            pipe = self._redis.pipeline()
            pipe.rpush(key, entry)
            pipe.expire(key, self._ttl)
            pipe.ltrim(key, -self._max, -1)
            await pipe.execute()
        except Exception as e:  # noqa: BLE001 — best-effort cache
            logger.warning("session cache append failed (degrading to DB-only): %s", e)

    async def list_messages(self, *, session_id: str, limit: int = 20) -> list[dict[str, Any]]:
        """Read the cached session messages (most recent first up to `limit`).

        Best-effort: on Redis failure, returns an empty list so the caller can
        fall back to kb_messages DB history.
        """
        try:
            key = _session_key(session_id)
            raw = await self._redis.lrange(key, 0, limit - 1)
        except Exception as e:  # noqa: BLE001 — best-effort cache
            logger.warning("session cache read failed (falling back to DB): %s", e)
            return []
        out: list[dict[str, Any]] = []
        for item in raw:
            try:
                if isinstance(item, (bytes, bytearray)):
                    item = item.decode("utf-8")
                out.append(json.loads(item))
            except (json.JSONDecodeError, TypeError):
                continue
        return out
