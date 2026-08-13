"""Tests for kb_messages repository (SPEC §6.1, US-010).

Covers:
- create_session ON CONFLICT (id) DO NOTHING: a repeated call with the same
  session_id does not insert a duplicate row (B1 fix — multi-turn Query RPCs
  reuse session_id and must not create duplicate kb_sessions rows).
- insert_message writes a kb_messages row with the expected fields.
"""
import os
import sys
import uuid
from contextlib import asynccontextmanager
from unittest.mock import AsyncMock, MagicMock

import pytest

_SERVICE_ROOT = os.path.abspath(os.path.join(os.path.dirname(__file__), ".."))
sys.path.insert(0, _SERVICE_ROOT)

from app.repositories import message as message_repo

TENANT_ID = "11111111-1111-1111-1111-111111111111"
KB_ID = "22222222-2222-2222-2222-222222222222"
SESSION_ID = "33333333-3333-3333-3333-333333333333"


class _RecordingConn:
    """Records fetchrow SQL + args; lets us assert ON CONFLICT is present."""

    def __init__(self, *, returns_row=True):
        self._returns_row = returns_row
        self.fetchrow_calls: list[tuple] = []
        self.execute_calls: list[tuple] = []

    def transaction(self):
        @asynccontextmanager
        async def _tx():
            yield self
        return _tx()

    async def fetchrow(self, sql, *args):
        self.fetchrow_calls.append((sql, args))
        if "kb_sessions" in sql:
            # ON CONFLICT DO NOTHING: when session_id provided and row exists,
            # RETURNING yields no row (None). Otherwise return a new id.
            if "ON CONFLICT" in sql and not self._returns_row:
                return None
            return {"id": uuid.UUID(SESSION_ID)}
        if "kb_messages" in sql:
            return {
                "id": uuid.uuid4(), "session_id": uuid.UUID(SESSION_ID),
                "tenant_id": uuid.UUID(TENANT_ID), "role": "user",
                "content": "hi", "source_chunks": None, "input_tokens": 0,
                "output_tokens": 0, "duration_ms": None, "created_at": None,
            }
        return None

    async def execute(self, sql, *args):
        self.execute_calls.append((sql, args))
        return "UPDATE 1"


@pytest.mark.asyncio
async def test_create_session_uses_on_conflict_do_nothing():
    """B1: create_session SQL contains ON CONFLICT (id) DO NOTHING so a
    repeated call with the same session_id doesn't insert a duplicate row."""
    conn = _RecordingConn(returns_row=True)
    sid = await message_repo.create_session(
        conn, tenant_id=TENANT_ID, kb_id=KB_ID, session_id=SESSION_ID,
    )
    sql = conn.fetchrow_calls[0][0]
    assert "ON CONFLICT (id) DO NOTHING" in sql
    assert sid == SESSION_ID


@pytest.mark.asyncio
async def test_create_session_returns_existing_id_on_conflict():
    """When ON CONFLICT DO NOTHING returns no row (existing session_id),
    create_session returns the provided session_id as-is (B1 fix)."""
    conn = _RecordingConn(returns_row=False)  # existing row → no RETURNING
    sid = await message_repo.create_session(
        conn, tenant_id=TENANT_ID, kb_id=KB_ID, session_id=SESSION_ID,
    )
    assert sid == SESSION_ID  # returns the provided id, not a new one


@pytest.mark.asyncio
async def test_insert_message_writes_row():
    """insert_message writes a kb_messages row and returns it as a dict."""
    conn = _RecordingConn()
    row = await message_repo.insert_message(
        conn, tenant_id=TENANT_ID, session_id=SESSION_ID,
        role="user", content="hi",
    )
    assert row["role"] == "user"
    assert row["content"] == "hi"
    assert row["session_id"] == uuid.UUID(SESSION_ID)
