"""Tests for kb_messages repository (SPEC §6.1, US-010, §4.2 data plane).

Covers:
- create_session ON CONFLICT (id) DO NOTHING: a repeated call with the same
  session_id does not insert a duplicate row (B1 fix — multi-turn Query RPCs
  reuse session_id and must not create duplicate kb_sessions rows).
- insert_message writes a kb_messages row with the expected fields.
- create_session_and_message folds session + user message into one CTE-based
  data_query call (SPEC §4.2 cross-table atomic fold).

Uses a mock CoreClient whose `data_query` records calls and returns canned
responses so no real Core gateway / Postgres is required.
"""
import os
import sys
import uuid
from unittest.mock import AsyncMock, MagicMock

import pytest

_SERVICE_ROOT = os.path.abspath(os.path.join(os.path.dirname(__file__), ".."))
sys.path.insert(0, _SERVICE_ROOT)

from app.repositories import message as message_repo

TENANT_ID = "11111111-1111-1111-1111-111111111111"
KB_ID = "22222222-2222-2222-2222-222222222222"
SESSION_ID = "33333333-3333-3333-3333-333333333333"


def _mock_core(*, query_return=None):
    """Build a mock CoreClient whose data_query returns query_return."""
    core = MagicMock()
    core.data_query = AsyncMock()
    core.aclose = AsyncMock()
    core.__aenter__ = AsyncMock(return_value=core)
    core.__aexit__ = AsyncMock(return_value=None)
    if isinstance(query_return, list):
        core.data_query.side_effect = query_return
    else:
        core.data_query.return_value = query_return or {"rows": [], "rowcount": 0}
    return core


@pytest.mark.asyncio
async def test_create_session_uses_on_conflict_do_nothing():
    """B1: create_session SQL contains ON CONFLICT (id) DO NOTHING so a
    repeated call with the same session_id doesn't insert a duplicate row."""
    core = _mock_core(
        query_return={"rows": [{"id": SESSION_ID}], "rowcount": 1}
    )

    sid = await message_repo.create_session(
        core, tenant_id=TENANT_ID, kb_id=KB_ID, session_id=SESSION_ID,
    )

    sql = core.data_query.call_args.kwargs["sql"]
    assert "ON CONFLICT (id) DO NOTHING" in sql
    assert sid == SESSION_ID
    params = core.data_query.call_args.kwargs["params"]
    assert params[0] == SESSION_ID
    assert params[1] == KB_ID
    assert params[2] == TENANT_ID


@pytest.mark.asyncio
async def test_create_session_returns_existing_id_on_conflict():
    """When ON CONFLICT DO NOTHING returns no row (existing session_id),
    create_session returns the provided session_id as-is (B1 fix)."""
    core = _mock_core(query_return={"rows": [], "rowcount": 0})

    sid = await message_repo.create_session(
        core, tenant_id=TENANT_ID, kb_id=KB_ID, session_id=SESSION_ID,
    )

    assert sid == SESSION_ID  # returns the provided id, not a new one


@pytest.mark.asyncio
async def test_insert_message_writes_row():
    """insert_message writes a kb_messages row and returns it as a dict."""
    row = {
        "id": str(uuid.uuid4()), "session_id": SESSION_ID,
        "tenant_id": TENANT_ID, "role": "user",
        "content": "hi", "source_chunks": None, "input_tokens": 0,
        "output_tokens": 0, "duration_ms": None, "created_at": None,
    }
    core = _mock_core(query_return={"rows": [row], "rowcount": 1})

    result = await message_repo.insert_message(
        core, tenant_id=TENANT_ID, session_id=SESSION_ID,
        role="user", content="hi",
    )

    assert result["role"] == "user"
    assert result["content"] == "hi"
    assert result["session_id"] == SESSION_ID
    sql = core.data_query.call_args.kwargs["sql"]
    assert "INSERT INTO kb_messages" in sql
    assert "RETURNING" in sql


@pytest.mark.asyncio
async def test_create_session_and_message_folds_session_and_message():
    """AC: create_session_and_message uses a single CTE-based data_query call
    that folds the session insert + message insert (SPEC §4.2 cross-table
    atomic fold). The SQL contains a WITH clause and both table inserts."""
    row = {
        "id": str(uuid.uuid4()), "session_id": SESSION_ID,
        "tenant_id": TENANT_ID, "role": "user",
        "content": "hi", "source_chunks": None, "input_tokens": 0,
        "output_tokens": 0, "duration_ms": None, "created_at": None,
    }
    core = _mock_core(query_return={"rows": [row], "rowcount": 1})

    result = await message_repo.create_session_and_message(
        core, tenant_id=TENANT_ID, kb_id=KB_ID, session_id=SESSION_ID,
        role="user", content="hi",
    )

    assert result["role"] == "user"
    assert result["content"] == "hi"
    # Single data_query call (atomic fold)
    assert core.data_query.call_count == 1
    sql = core.data_query.call_args.kwargs["sql"]
    # CTE-based fold: WITH sess AS (...) , effective_session AS (...) INSERT INTO kb_messages
    assert "WITH sess AS" in sql
    assert "INSERT INTO kb_sessions" in sql
    assert "INSERT INTO kb_messages" in sql
    assert "ON CONFLICT (id) DO NOTHING" in sql
    # Params: session_id, kb_id, tenant_id, user_id, title, role, content, chunks, input, output, duration
    params = core.data_query.call_args.kwargs["params"]
    assert params[0] == SESSION_ID  # session_id
    assert params[1] == KB_ID       # kb_id
    assert params[2] == TENANT_ID    # tenant_id
    assert params[5] == "user"       # role
    assert params[6] == "hi"         # content


@pytest.mark.asyncio
async def test_list_session_messages():
    """list_session_messages returns rows from data_query."""
    rows = [{"id": "m1", "role": "user", "content": "hi"}]
    core = _mock_core(query_return={"rows": rows})

    result = await message_repo.list_session_messages(
        core, tenant_id=TENANT_ID, session_id=SESSION_ID, limit=10,
    )

    assert result == rows
    sql = core.data_query.call_args.kwargs["sql"]
    assert "FROM kb_messages" in sql
    assert "WHERE session_id = $1" in sql
    assert "ORDER BY created_at ASC" in sql
    assert core.data_query.call_args.kwargs["params"] == [SESSION_ID, 10]


# ── cross-cutting: rls.py not imported ────────────────────────────────────────


def test_message_repo_does_not_import_rls():
    """AC: rls.py is no longer used by message.py (SPEC §4.2)."""
    import app.repositories.message as m
    assert not hasattr(m, "set_tenant_context")
    assert "asyncpg" not in dir(m)
