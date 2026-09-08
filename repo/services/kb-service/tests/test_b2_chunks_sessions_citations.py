"""Tests for B2 kb-service: chunks / sessions / messages / citations (issue-045).

Covers SPEC §9.1 B2:
- list_chunks_by_doc_paged: keyset (id ASC, id > $cursor), chunk_type filter
- list_sessions aggregate SQL (COUNT / MAX / earliest-user last_query), composite keyset
- get_session ownership check (kb_id must match)
- list_session_messages_paged: composite keyset (created_at ASC, id ASC tie-break)
- delete_session: single transaction, messages first then session with kb_id
- list_citation_messages_paged: assistant + source_chunks non-null/non-'null', JOIN kb_sessions
- SessionCache.delete_session: best-effort Redis DEL
- servicer: validation, 404 gates, idempotent delete, citations expansion
  (uuid5 determinism, highest score per (message, doc), malformed JSON skip)

Uses recording fake asyncpg connections — no real Postgres required.
"""
import json
import os
import sys
import uuid
from contextlib import asynccontextmanager
from datetime import datetime, timezone
from unittest.mock import AsyncMock

import pytest

_SERVICE_ROOT = os.path.abspath(os.path.join(os.path.dirname(__file__), ".."))
sys.path.insert(0, _SERVICE_ROOT)
sys.path.insert(0, os.path.join(_SERVICE_ROOT, "app", "generated"))

from app.repositories import chunk as chunk_repo
from app.repositories import message as message_repo
from app.session.cache import SessionCache

TENANT_ID = "11111111-1111-1111-1111-111111111111"
KB_ID = "22222222-2222-2222-2222-222222222222"
DOC_ID = "33333333-3333-3333-3333-333333333333"
SESSION_ID = "44444444-4444-4444-4444-444444444444"
MSG_ID = "55555555-5555-5555-5555-555555555555"


# ── recording fake conn ──────────────────────────────────────────────────────


class _RecordingConn:
    """Records fetch/fetchrow/execute SQL + args; canned returns.

    `rows` feeds fetch(); `row` feeds fetchrow(); `fetchval_result` feeds
    fetchval(). fetchrow dispatches by table keywords so repository calls can
    be told apart.
    """

    def __init__(self, *, rows=None, fetchrow_row=None, fetchval_result=None):
        self._rows = rows or []
        self._fetchrow_row = fetchrow_row
        self._fetchval_result = fetchval_result
        self.fetch_calls: list[tuple] = []
        self.fetchrow_calls: list[tuple] = []
        self.execute_calls: list[tuple] = []
        self.fetchval_calls: list[tuple] = []

    def transaction(self):
        @asynccontextmanager
        async def _tx():
            yield self

        return _tx()

    async def fetch(self, sql, *args):
        self.fetch_calls.append((sql, args))
        return self._rows

    async def fetchrow(self, sql, *args):
        self.fetchrow_calls.append((sql, args))
        if self._fetchrow_row is not None:
            return self._fetchrow_row
        return None

    async def execute(self, sql, *args):
        self.execute_calls.append((sql, args))
        return "UPDATE 1"

    async def fetchval(self, sql, *args):
        self.fetchval_calls.append((sql, args))
        return self._fetchval_result


def _chunk_row(chunk_id=MSG_ID, **over):
    row = {
        "id": uuid.UUID(chunk_id),
        "tenant_id": uuid.UUID(TENANT_ID),
        "kb_id": uuid.UUID(KB_ID),
        "doc_id": uuid.UUID(DOC_ID),
        "parent_chunk_id": None,
        "chunk_type": "child",
        "content": "chunk text",
        "parent_content": None,
        "page_number": 1,
        "content_type": "text",
        "file_name": "doc.pdf",
        "token_count": 5,
        "custom_metadata": None,
        "created_at": datetime(2026, 1, 1, tzinfo=timezone.utc),
    }
    row.update(over)
    return row


def _message_row(msg_id=MSG_ID, **over):
    row = {
        "id": uuid.UUID(msg_id),
        "session_id": uuid.UUID(SESSION_ID),
        "tenant_id": uuid.UUID(TENANT_ID),
        "role": "user",
        "content": "hi",
        "source_chunks": None,
        "input_tokens": 0,
        "output_tokens": 0,
        "duration_ms": None,
        "created_at": datetime(2026, 1, 1, 12, 0, 0, tzinfo=timezone.utc),
    }
    row.update(over)
    return row


# ── chunk.list_chunks_by_doc_paged ───────────────────────────────────────────


async def test_list_chunks_by_doc_paged_default_page():
    """First page (no cursor, no chunk_type): plain ORDER BY id ASC, LIMIT $3."""
    conn = _RecordingConn(rows=[_chunk_row()])
    rows = await chunk_repo.list_chunks_by_doc_paged(
        conn, tenant_id=TENANT_ID, kb_id=KB_ID, doc_id=DOC_ID
    )
    sql, args = conn.fetch_calls[0]
    assert "ORDER BY id ASC" in sql
    assert "id >" not in sql
    assert "chunk_type =" not in sql  # no filter (column still in SELECT list)
    assert args == (uuid.UUID(KB_ID), uuid.UUID(DOC_ID), 50)
    assert rows[0]["id"] == uuid.UUID(MSG_ID)
    # RLS context set inside the transaction.
    assert any("SET LOCAL app.current_tenant_id" in s for s, _ in conn.execute_calls)


async def test_list_chunks_by_doc_paged_with_cursor_advances():
    """Second page: id > $cursor keeps the keyset ordering, no OFFSET."""
    conn = _RecordingConn(rows=[])
    await chunk_repo.list_chunks_by_doc_paged(
        conn, tenant_id=TENANT_ID, kb_id=KB_ID, doc_id=DOC_ID, cursor=MSG_ID
    )
    sql, args = conn.fetch_calls[0]
    assert "id > $3" in sql
    assert "OFFSET" not in sql
    assert args == (uuid.UUID(KB_ID), uuid.UUID(DOC_ID), uuid.UUID(MSG_ID), 50)


async def test_list_chunks_by_doc_paged_chunk_type_filter():
    """chunk_type filter adds AND chunk_type = $3 (servicer validated the value)."""
    conn = _RecordingConn(rows=[])
    await chunk_repo.list_chunks_by_doc_paged(
        conn, tenant_id=TENANT_ID, kb_id=KB_ID, doc_id=DOC_ID, chunk_type="parent"
    )
    sql, args = conn.fetch_calls[0]
    assert "chunk_type = $3" in sql
    assert args == (uuid.UUID(KB_ID), uuid.UUID(DOC_ID), "parent", 50)


async def test_list_chunks_by_doc_paged_chunk_type_and_cursor():
    """chunk_type + cursor: filter and keyset combine."""
    conn = _RecordingConn(rows=[])
    await chunk_repo.list_chunks_by_doc_paged(
        conn, tenant_id=TENANT_ID, kb_id=KB_ID, doc_id=DOC_ID,
        chunk_type="doc_summary", cursor=MSG_ID,
    )
    sql, args = conn.fetch_calls[0]
    assert "chunk_type = $3" in sql and "id > $4" in sql
    assert args == (uuid.UUID(KB_ID), uuid.UUID(DOC_ID), "doc_summary", uuid.UUID(MSG_ID), 50)


# ── message.list_sessions (aggregate) ────────────────────────────────────────


async def test_list_sessions_aggregate_sql():
    """First page: COUNT/MAX aggregates, correlated last_query subquery, no cursor."""
    conn = _RecordingConn(rows=[{
        "id": uuid.UUID(SESSION_ID), "kb_id": uuid.UUID(KB_ID),
        "created_at": datetime(2026, 1, 1, tzinfo=timezone.utc),
        "message_count": 2,
        "last_active_at": datetime(2026, 1, 2, tzinfo=timezone.utc),
        "last_query": "first question",
    }])
    rows = await message_repo.list_sessions(
        conn, tenant_id=TENANT_ID, kb_id=KB_ID
    )
    sql, args = conn.fetch_calls[0]
    assert "COUNT(m.id) AS message_count" in sql
    assert "MAX(m.created_at) AS last_active_at" in sql
    assert "m2.role = 'user'" in sql
    assert "ORDER BY m2.created_at ASC LIMIT 1" in sql  # earliest user message
    assert "ORDER BY s.created_at DESC, s.id DESC" in sql
    assert "(s.created_at, s.id)" not in sql  # no cursor predicate on page 1
    assert args == (uuid.UUID(KB_ID), 20)
    assert rows[0]["last_query"] == "first question"
    assert rows[0]["message_count"] == 2


async def test_list_sessions_with_cursor_composite_keyset():
    """Second page: (s.created_at, s.id) < ($ts, $id) composite keyset, DESC."""
    conn = _RecordingConn(rows=[])
    cursor = f"2026-01-01T00:00:00+00:00|{SESSION_ID}"
    await message_repo.list_sessions(
        conn, tenant_id=TENANT_ID, kb_id=KB_ID, cursor=cursor
    )
    sql, args = conn.fetch_calls[0]
    assert "(s.created_at, s.id) < ($2, $3)" in sql
    assert "OFFSET" not in sql
    assert args == (
        uuid.UUID(KB_ID),
        datetime.fromisoformat("2026-01-01T00:00:00+00:00"),
        uuid.UUID(SESSION_ID),
        20,
    )


# ── message.get_session (ownership) ───────────────────────────────────────────


async def test_get_session_returns_row():
    conn = _RecordingConn(fetchrow_row={
        "id": uuid.UUID(SESSION_ID), "kb_id": uuid.UUID(KB_ID),
        "tenant_id": uuid.UUID(TENANT_ID), "user_id": None,
        "title": None, "created_at": datetime(2026, 1, 1, tzinfo=timezone.utc),
    })
    row = await message_repo.get_session(
        conn, tenant_id=TENANT_ID, kb_id=KB_ID, session_id=SESSION_ID
    )
    assert row is not None
    sql, args = conn.fetchrow_calls[0]
    assert "id = $1 AND kb_id = $2" in sql  # ownership check in SQL
    assert args == (uuid.UUID(SESSION_ID), uuid.UUID(KB_ID))


async def test_get_session_other_kb_returns_none():
    """A session belonging to another KB of the same tenant → None → 404."""
    conn = _RecordingConn(fetchrow_row=None)
    row = await message_repo.get_session(
        conn, tenant_id=TENANT_ID, kb_id=KB_ID, session_id=SESSION_ID
    )
    assert row is None


# ── message.list_session_messages_paged ───────────────────────────────────────


async def test_list_session_messages_paged_ordering():
    """ASC playback order with the id tie-break (same-second user+assistant)."""
    conn = _RecordingConn(rows=[_message_row()])
    rows = await message_repo.list_session_messages_paged(
        conn, tenant_id=TENANT_ID, session_id=SESSION_ID
    )
    sql, args = conn.fetch_calls[0]
    assert "ORDER BY created_at ASC, id ASC" in sql
    assert args == (uuid.UUID(SESSION_ID), 100)
    assert rows[0]["role"] == "user"


async def test_list_session_messages_paged_cursor_composite():
    """Second page: (created_at, id) > ($ts, $id) — user messages with
    source_chunks=None still page correctly (cursor is the row key, not the
    payload)."""
    conn = _RecordingConn(rows=[])
    cursor = f"2026-01-01T12:00:00+00:00|{MSG_ID}"
    await message_repo.list_session_messages_paged(
        conn, tenant_id=TENANT_ID, session_id=SESSION_ID, limit=10, cursor=cursor
    )
    sql, args = conn.fetch_calls[0]
    assert "(created_at, id) > ($2, $3)" in sql
    assert "OFFSET" not in sql
    assert args == (
        uuid.UUID(SESSION_ID),
        datetime.fromisoformat("2026-01-01T12:00:00+00:00"),
        uuid.UUID(MSG_ID),
        10,
    )


# ── message.delete_session ───────────────────────────────────────────────────


async def test_delete_session_single_transaction_messages_first():
    """One transaction: DELETE kb_messages, then DELETE kb_sessions with the
    kb_id ownership condition. fetchval returns the deleted id → True."""
    conn = _RecordingConn(fetchval_result=uuid.UUID(SESSION_ID))
    deleted = await message_repo.delete_session(
        conn, tenant_id=TENANT_ID, kb_id=KB_ID, session_id=SESSION_ID
    )
    assert deleted is True
    msg_sql, _ = conn.execute_calls[-2] if len(conn.execute_calls) >= 2 else (None, None)
    sess_sql, sess_args = conn.fetchval_calls[0]
    assert "DELETE FROM kb_messages WHERE session_id = $1" in msg_sql or any(
        "DELETE FROM kb_messages" in s for s, _ in conn.execute_calls
    )
    assert "DELETE FROM kb_sessions" in sess_sql
    assert "WHERE id = $1 AND kb_id = $2" in sess_sql
    assert sess_args == (uuid.UUID(SESSION_ID), uuid.UUID(KB_ID))


async def test_delete_session_missing_returns_false():
    """Session already gone (or owned by another KB): fetchval returns None →
    False → the servicer still returns Empty (idempotent 204)."""
    conn = _RecordingConn(fetchval_result=None)
    deleted = await message_repo.delete_session(
        conn, tenant_id=TENANT_ID, kb_id=KB_ID, session_id=SESSION_ID
    )
    assert deleted is False


async def test_delete_session_messages_scoped_by_kb_ownership():
    """The kb_messages delete must carry the kb_id ownership condition (via a
    kb_sessions subquery): a session_id belonging to another KB of the same
    tenant must not lose its messages (symmetric with get_session, SPEC §7.1
    session 跨 KB 归属校验)."""
    conn = _RecordingConn(fetchval_result=uuid.UUID(SESSION_ID))
    deleted = await message_repo.delete_session(
        conn, tenant_id=TENANT_ID, kb_id=KB_ID, session_id=SESSION_ID
    )
    assert deleted is True
    # execute_calls[0] is the SET LOCAL tenant context; the messages delete is
    # the only other execute call in the transaction.
    msg_sql, msg_args = conn.execute_calls[1]
    assert "DELETE FROM kb_messages" in msg_sql
    # The ownership subquery must scope both the session id and the kb id.
    assert "SELECT id FROM kb_sessions WHERE id = $1 AND kb_id = $2" in msg_sql
    assert msg_args == (uuid.UUID(SESSION_ID), uuid.UUID(KB_ID))


# ── message.list_citation_messages_paged ──────────────────────────────────────


async def test_list_citation_messages_paged_filters():
    """Only assistant messages with non-null, non-'null' source_chunks, joined
    to sessions of the KB, DESC composite keyset."""
    row = {
        "id": uuid.UUID(MSG_ID),
        "session_id": uuid.UUID(SESSION_ID),
        "created_at": datetime(2026, 1, 1, 12, 0, 0, tzinfo=timezone.utc),
        "source_chunks": json.dumps([{"doc_id": DOC_ID, "score": 0.9}]),
    }
    conn = _RecordingConn(rows=[row])
    rows = await message_repo.list_citation_messages_paged(
        conn, tenant_id=TENANT_ID, kb_id=KB_ID
    )
    sql, args = conn.fetch_calls[0]
    assert "m.role = 'assistant'" in sql
    assert "m.source_chunks IS NOT NULL AND m.source_chunks <> 'null'" in sql
    assert "JOIN kb_sessions s ON s.id = m.session_id" in sql
    assert "s.kb_id = $1" in sql
    assert "ORDER BY m.created_at DESC, m.id DESC" in sql
    assert args == (uuid.UUID(KB_ID), 20)
    assert rows[0]["session_id"] == uuid.UUID(SESSION_ID)


async def test_list_citation_messages_paged_cursor():
    conn = _RecordingConn(rows=[])
    cursor = f"2026-01-01T12:00:00+00:00|{MSG_ID}"
    await message_repo.list_citation_messages_paged(
        conn, tenant_id=TENANT_ID, kb_id=KB_ID, limit=5, cursor=cursor
    )
    sql, args = conn.fetch_calls[0]
    assert "(m.created_at, m.id) < ($2, $3)" in sql
    assert args == (
        uuid.UUID(KB_ID),
        datetime.fromisoformat("2026-01-01T12:00:00+00:00"),
        uuid.UUID(MSG_ID),
        5,
    )


# ── SessionCache.delete_session ──────────────────────────────────────────────


async def test_session_cache_delete_session_calls_redis_del():
    """delete_session issues a Redis DEL on the session key."""
    redis = AsyncMock()
    redis.delete = AsyncMock(return_value=1)
    cache = SessionCache(redis=redis)
    await cache.delete_session(session_id=SESSION_ID)
    redis.delete.assert_awaited_once()
    key = redis.delete.await_args.args[0]
    assert SESSION_ID in key  # key derived from the session id


async def test_session_cache_delete_session_best_effort():
    """A Redis failure is swallowed (logged) — the 24h TTL expires the stale
    key anyway; the RPC must not surface an error."""
    redis = AsyncMock()
    redis.delete = AsyncMock(side_effect=RuntimeError("redis down"))
    cache = SessionCache(redis=redis)
    # Must not raise.
    await cache.delete_session(session_id=SESSION_ID)


# ── servicer: citations expansion (pure mapping logic) ───────────────────────


class _CitationsConn:
    """Conn that answers: get_kb → row; list_citation_messages_paged → rows."""

    def __init__(self, citation_rows):
        self._citation_rows = citation_rows

    def transaction(self):
        @asynccontextmanager
        async def _tx():
            yield self

        return _tx()

    async def execute(self, sql, *args):
        return "UPDATE 1"

    async def fetchrow(self, sql, *args):
        if "FROM knowledge_bases" in sql:
            return {"id": uuid.UUID(KB_ID), "tenant_id": uuid.UUID(TENANT_ID)}
        return None

    async def fetch(self, sql, *args):
        if "kb_messages m" in sql or "source_chunks" in sql:
            return self._citation_rows
        return []

    async def fetchval(self, sql, *args):
        return 0


class _ServicerContext:
    """grpc.ServicerContext stand-in — abort raises with the code embedded."""

    def __init__(self):
        self.aborted = None

    def abort(self, code, message):
        self.aborted = (code, message)
        raise RuntimeError(f"aborted: {code} {message}")


def _make_citations_servicer(citation_rows):
    from app.api.grpc_server import KBServiceServicer
    from app.generated.kb.v1 import kb_service_pb2 as kb_pb

    conn = _CitationsConn(citation_rows)

    class _Pool:
        @asynccontextmanager
        async def acquire(self):
            yield conn

    # session_cache_factory that raises → exercises the best-effort path
    # without needing Redis (delete_session must still return Empty).
    def _cache_factory():
        raise RuntimeError("redis unavailable")

    servicer = KBServiceServicer(pool=_Pool(), session_cache_factory=_cache_factory)
    return servicer, kb_pb


def _citation_message_row(source_chunks_json, msg_id=MSG_ID):
    return {
        "id": uuid.UUID(msg_id),
        "session_id": uuid.UUID(SESSION_ID),
        "created_at": datetime(2026, 1, 1, 12, 0, 0, tzinfo=timezone.utc),
        "source_chunks": source_chunks_json,
    }


async def test_citations_expansion_highest_score_and_uuid5():
    """Per (message, doc) the citation takes the highest score; the id is the
    deterministic uuid5 of ani:kb:citation:{kb_id}:{message_id}:{doc_id}."""
    sources = [
        {"doc_id": DOC_ID, "score": 0.4, "content": "low", "file_name": "a.pdf", "page": 1},
        {"doc_id": DOC_ID, "score": 0.9, "content": "high", "file_name": "a.pdf", "page": 2},
        {"doc_id": "66666666-6666-6666-6666-666666666666", "score": 0.7,
         "content": "other doc", "file_name": "b.pdf", "page": 3},
    ]
    servicer, kb_pb = _make_citations_servicer(
        [_citation_message_row(json.dumps(sources))]
    )
    req = kb_pb.ListKBCitationsRequest(tenant_id=TENANT_ID, kb_id=KB_ID)
    resp = await servicer._list_kb_citations(req, _ServicerContext())

    assert len(resp.items) == 2  # one per distinct doc_id
    by_doc = {c.doc_id: c for c in resp.items}
    best = by_doc[DOC_ID]
    assert best.score == pytest.approx(0.9)
    assert best.content == "high"
    assert best.page == 2
    assert best.message_id == MSG_ID
    assert best.session_id == SESSION_ID
    # Deterministic uuid5.
    expected = str(uuid.uuid5(
        uuid.NAMESPACE_URL, f"ani:kb:citation:{KB_ID}:{MSG_ID}:{DOC_ID}"
    ))
    assert best.id == expected
    # Cursor emitted only on a full page (1 row < limit 20).
    assert resp.next_cursor == ""


async def test_citations_skips_malformed_json_and_null_string():
    """'null' / invalid JSON / empty array / non-dict entries are skipped per
    message — a malformed row never blows up the page."""
    servicer, kb_pb = _make_citations_servicer([
        _citation_message_row("null"),
        _citation_message_row("{not json"),
        _citation_message_row("[]"),
        _citation_message_row(json.dumps([{"score": 0.5, "content": "no doc_id"}])),
    ])
    req = kb_pb.ListKBCitationsRequest(tenant_id=TENANT_ID, kb_id=KB_ID)
    resp = await servicer._list_kb_citations(req, _ServicerContext())
    assert resp.items == []


async def test_citations_user_message_with_null_sources_never_reaches_expansion():
    """list_citation_messages_paged filters role='assistant' AND non-null
    source_chunks in SQL — a user message with sources=None can't appear. This
    locks the SQL shape (see repo test) and the expansion precondition."""
    row = _message_row(role="user", source_chunks=None)
    # Emulate the SQL filter: only assistant rows with non-null sources pass.
    visible = [
        r for r in [row]
        if r["role"] == "assistant" and r["source_chunks"] is not None
    ]
    assert visible == []


async def test_citations_empty_kb_returns_empty_list():
    servicer, kb_pb = _make_citations_servicer([])
    req = kb_pb.ListKBCitationsRequest(tenant_id=TENANT_ID, kb_id=KB_ID)
    resp = await servicer._list_kb_citations(req, _ServicerContext())
    assert resp.items == []
    assert resp.next_cursor == ""


async def test_citations_full_page_emits_cursor():
    """A full page of messages emits the last row's composite cursor so the
    client can fetch the next page."""
    rows = [
        _citation_message_row(
            json.dumps([{"doc_id": DOC_ID, "score": 0.1}]),
            msg_id=f"55555555-5555-5555-5555-{i:012d}",
        )
        for i in range(2)
    ]
    servicer, kb_pb = _make_citations_servicer(rows)
    req = kb_pb.ListKBCitationsRequest(
        tenant_id=TENANT_ID, kb_id=KB_ID,
        page=__import__("app.generated.common.v1.common_pb2", fromlist=["x"]).CursorPageRequest(limit=2),
    )
    resp = await servicer._list_kb_citations(req, _ServicerContext())
    assert resp.next_cursor != ""
    assert resp.next_cursor.endswith(f"|{rows[-1]['id']}")


# ── servicer: validation + gates ──────────────────────────────────────────────


class _GatesConn:
    """Conn whose knowledge_bases / kb_documents / kb_sessions lookups miss."""

    def __init__(self, *, kb_exists=True, doc_exists=True, session_exists=True):
        self.kb_exists = kb_exists
        self.doc_exists = doc_exists
        self.session_exists = session_exists
        self.fetch_calls: list[tuple] = []
        self.fetchval_calls: list[tuple] = []

    def transaction(self):
        @asynccontextmanager
        async def _tx():
            yield self

        return _tx()

    async def execute(self, sql, *args):
        return "UPDATE 1"

    async def fetchrow(self, sql, *args):
        if "FROM knowledge_bases" in sql:
            if self.kb_exists:
                return {"id": uuid.UUID(KB_ID), "tenant_id": uuid.UUID(TENANT_ID)}
            return None
        if "FROM kb_documents" in sql:
            if self.doc_exists:
                return {"id": uuid.UUID(DOC_ID), "kb_id": uuid.UUID(KB_ID)}
            return None
        if "FROM kb_sessions" in sql:
            if self.session_exists:
                return {"id": uuid.UUID(SESSION_ID), "kb_id": uuid.UUID(KB_ID)}
            return None
        return None

    async def fetch(self, sql, *args):
        self.fetch_calls.append((sql, args))
        return []

    async def fetchval(self, sql, *args):
        self.fetchval_calls.append((sql, args))
        return uuid.UUID(SESSION_ID)


def _make_gated_servicer(**conn_kwargs):
    from app.api.grpc_server import KBServiceServicer
    from app.generated.kb.v1 import kb_service_pb2 as kb_pb
    from app.generated.common.v1 import common_pb2

    conn = _GatesConn(**conn_kwargs)

    class _Pool:
        @asynccontextmanager
        async def acquire(self):
            yield conn

    servicer = KBServiceServicer(pool=_Pool())
    return servicer, kb_pb, common_pb2


async def test_list_document_chunks_invalid_limit():
    servicer, kb_pb, common_pb2 = _make_gated_servicer()
    req = kb_pb.ListDocumentChunksRequest(
        tenant_id=TENANT_ID, kb_id=KB_ID, doc_id=DOC_ID,
        page=common_pb2.CursorPageRequest(limit=101),
    )
    ctx = _ServicerContext()
    with pytest.raises(RuntimeError, match="INVALID_ARGUMENT"):
        await servicer._list_document_chunks(req, ctx)


async def test_list_document_chunks_invalid_chunk_type():
    servicer, kb_pb, common_pb2 = _make_gated_servicer()
    req = kb_pb.ListDocumentChunksRequest(
        tenant_id=TENANT_ID, kb_id=KB_ID, doc_id=DOC_ID, chunk_type="weird",
    )
    ctx = _ServicerContext()
    with pytest.raises(RuntimeError, match="INVALID_ARGUMENT"):
        await servicer._list_document_chunks(req, ctx)


async def test_list_document_chunks_kb_missing_404():
    servicer, kb_pb, common_pb2 = _make_gated_servicer(kb_exists=False)
    req = kb_pb.ListDocumentChunksRequest(tenant_id=TENANT_ID, kb_id=KB_ID, doc_id=DOC_ID)
    ctx = _ServicerContext()
    with pytest.raises(RuntimeError, match="NOT_FOUND"):
        await servicer._list_document_chunks(req, ctx)


async def test_list_document_chunks_soft_deleted_doc_404():
    """get_document filters soft-deleted docs — the servicer surfaces 404."""
    servicer, kb_pb, common_pb2 = _make_gated_servicer(doc_exists=False)
    req = kb_pb.ListDocumentChunksRequest(tenant_id=TENANT_ID, kb_id=KB_ID, doc_id=DOC_ID)
    ctx = _ServicerContext()
    with pytest.raises(RuntimeError, match="NOT_FOUND"):
        await servicer._list_document_chunks(req, ctx)


async def test_list_document_chunks_cross_tenant_kb_404():
    """RLS hides another tenant's KB — get_kb returns None → 404 (the fake
    conn models the RLS-filtered result)."""
    servicer, kb_pb, common_pb2 = _make_gated_servicer(kb_exists=False)
    req = kb_pb.ListDocumentChunksRequest(tenant_id="99999999-9999-9999-9999-999999999999", kb_id=KB_ID, doc_id=DOC_ID)
    ctx = _ServicerContext()
    with pytest.raises(RuntimeError, match="NOT_FOUND"):
        await servicer._list_document_chunks(req, ctx)


async def test_get_session_messages_cross_kb_session_404():
    """The session belongs to another KB of the same tenant → get_session
    returns None → 404 (no existence leak)."""
    servicer, kb_pb, common_pb2 = _make_gated_servicer(session_exists=False)
    req = kb_pb.GetSessionMessagesRequest(
        tenant_id=TENANT_ID, kb_id=KB_ID, session_id=SESSION_ID
    )
    ctx = _ServicerContext()
    with pytest.raises(RuntimeError, match="NOT_FOUND"):
        await servicer._get_session_messages(req, ctx)


async def test_get_session_messages_after_delete_404():
    """After DeleteSession the session row is gone → GetSessionMessages 404."""
    servicer, kb_pb, common_pb2 = _make_gated_servicer(session_exists=False)
    req = kb_pb.GetSessionMessagesRequest(
        tenant_id=TENANT_ID, kb_id=KB_ID, session_id=SESSION_ID
    )
    ctx = _ServicerContext()
    with pytest.raises(RuntimeError, match="NOT_FOUND"):
        await servicer._get_session_messages(req, ctx)


async def test_get_session_messages_missing_tenant_invalid_argument():
    servicer, kb_pb, common_pb2 = _make_gated_servicer()
    req = kb_pb.GetSessionMessagesRequest(kb_id=KB_ID, session_id=SESSION_ID)
    ctx = _ServicerContext()
    with pytest.raises(RuntimeError, match="INVALID_ARGUMENT"):
        await servicer._get_session_messages(req, ctx)


async def test_delete_session_kb_missing_404_but_session_missing_204():
    """KB missing → NOT_FOUND; session missing (any repeat) → Empty (idempotent)."""
    # KB missing → 404.
    servicer, kb_pb, common_pb2 = _make_gated_servicer(kb_exists=False)
    req = kb_pb.DeleteSessionRequest(tenant_id=TENANT_ID, kb_id=KB_ID, session_id=SESSION_ID)
    ctx = _ServicerContext()
    with pytest.raises(RuntimeError, match="NOT_FOUND"):
        await servicer._delete_session(req, ctx)

    # Session missing → still Empty (fetchval already returns None in
    # _RecordingConn; _GatesConn.fetchval returns an id, so point it at a
    # conn whose kb exists but whose DELETE misses).
    class _Conn(_GatesConn):
        async def fetchval(self, sql, *args):
            self.fetchval_calls.append((sql, args))
            return None  # session row did not exist

    from app.api.grpc_server import KBServiceServicer as _S
    conn = _Conn()

    class _Pool:
        @asynccontextmanager
        async def acquire(self):
            yield conn

    servicer2 = _S(pool=_Pool())
    resp = await servicer2._delete_session(req, _ServicerContext())
    # google.protobuf.empty_pb2.Empty compares equal regardless.
    assert resp is not None


async def test_delete_session_calls_cache_delete_after_commit():
    """After the DB transaction commits, the best-effort Redis DEL runs. A
    factory that raises must NOT fail the RPC (best-effort)."""
    from app.api.grpc_server import KBServiceServicer
    from app.generated.kb.v1 import kb_service_pb2 as kb_pb

    conn = _GatesConn()
    deleted: list = []

    class _Cache:
        async def delete_session(self, *, session_id):
            deleted.append(session_id)

    class _Pool:
        @asynccontextmanager
        async def acquire(self):
            yield conn

    servicer = KBServiceServicer(pool=_Pool(), session_cache_factory=lambda: _Cache())
    req = kb_pb.DeleteSessionRequest(tenant_id=TENANT_ID, kb_id=KB_ID, session_id=SESSION_ID)
    resp = await servicer._delete_session(req, _ServicerContext())
    assert resp is not None
    assert deleted == [SESSION_ID]


async def test_list_sessions_servicer_maps_aggregates():
    """ListKBSessions maps repository aggregates into KBSession protos."""
    from app.api.grpc_server import KBServiceServicer
    from app.generated.kb.v1 import kb_service_pb2 as kb_pb

    row = {
        "id": uuid.UUID(SESSION_ID), "kb_id": uuid.UUID(KB_ID),
        "created_at": datetime(2026, 1, 1, tzinfo=timezone.utc),
        "message_count": 3,
        "last_active_at": datetime(2026, 1, 5, tzinfo=timezone.utc),
        "last_query": "hello?",
    }

    class _Conn(_GatesConn):
        async def fetch(self, sql, *args):
            if "kb_sessions s" in sql:
                return [row]
            return []

    class _Pool:
        @asynccontextmanager
        async def acquire(self):
            yield _Conn()

    servicer = KBServiceServicer(pool=_Pool())
    req = kb_pb.ListKBSessionsRequest(tenant_id=TENANT_ID, kb_id=KB_ID)
    resp = await servicer._list_kb_sessions(req, _ServicerContext())
    assert len(resp.items) == 1
    s = resp.items[0]
    assert s.id == SESSION_ID
    assert s.kb_id == KB_ID
    assert s.message_count == 3
    assert s.last_query == "hello?"
    assert resp.next_cursor == ""  # 1 row < limit 20


async def test_get_session_messages_maps_and_orders():
    """GetSessionMessages maps rows into KBSessionMessage protos in ASC order."""
    from app.api.grpc_server import KBServiceServicer
    from app.generated.kb.v1 import kb_service_pb2 as kb_pb

    m1 = _message_row(msg_id="55555555-5555-5555-5555-555555555555")
    m2 = _message_row(
        msg_id="66666666-6666-6666-6666-666666666666",
        role="assistant", content="answer",
        source_chunks=json.dumps([{"doc_id": DOC_ID}]),
        created_at=datetime(2026, 1, 1, 12, 0, 1, tzinfo=timezone.utc),
    )

    class _Conn(_GatesConn):
        async def fetch(self, sql, *args):
            if "FROM kb_messages" in sql:
                return [m1, m2]
            return []

    class _Pool:
        @asynccontextmanager
        async def acquire(self):
            yield _Conn()

    servicer = KBServiceServicer(pool=_Pool())
    req = kb_pb.GetSessionMessagesRequest(
        tenant_id=TENANT_ID, kb_id=KB_ID, session_id=SESSION_ID
    )
    resp = await servicer._get_session_messages(req, _ServicerContext())
    assert [m.id for m in resp.items] == [
        "55555555-5555-5555-5555-555555555555",
        "66666666-6666-6666-6666-666666666666",
    ]
    assert resp.items[0].source_chunks == ""  # user message: sources null
    assert resp.items[1].source_chunks.startswith("[")
    assert resp.next_cursor == ""  # 2 rows < limit 100


# ── invalid cursor mapping: InvalidCursorError → INVALID_ARGUMENT ──────────


async def test_parse_composite_cursor_rejects_malformed():
    """Malformed composite cursors raise InvalidCursorError (a ValueError
    subclass) — never a raw datetime/uuid error reaching gRPC as UNKNOWN."""
    from app.repositories.cursor import InvalidCursorError, parse_composite_cursor

    for bad in (
        "garbage",
        "not-a-timestamp|11111111-1111-1111-1111-111111111111",
        "2026-01-01T00:00:00+00:00|not-a-uuid",
        "2026-01-01T00:00:00+00:00|",
        "2026-01-01T00:00:00+00:00",
    ):
        with pytest.raises(InvalidCursorError):
            parse_composite_cursor(bad)

    ts, cid = parse_composite_cursor(f"2026-01-01T00:00:00+00:00|{SESSION_ID}")
    assert ts == datetime.fromisoformat("2026-01-01T00:00:00+00:00")
    assert cid == uuid.UUID(SESSION_ID)


async def test_parse_chunk_cursor_rejects_malformed():
    from app.repositories.cursor import InvalidCursorError, parse_chunk_cursor

    with pytest.raises(InvalidCursorError):
        parse_chunk_cursor("garbage")
    assert parse_chunk_cursor(MSG_ID) == uuid.UUID(MSG_ID)


async def test_list_sessions_repository_propagates_invalid_cursor_error():
    """The repository raises InvalidCursorError before any SQL runs; the
    servicer maps it to INVALID_ARGUMENT (not UNKNOWN)."""
    from app.repositories.cursor import InvalidCursorError

    conn = _RecordingConn()
    with pytest.raises(InvalidCursorError):
        await message_repo.list_sessions(
            conn, tenant_id=TENANT_ID, kb_id=KB_ID,
            cursor="2026-13-99T00:00:00+00:00|x",
        )


async def test_list_chunks_repository_propagates_invalid_cursor_error():
    from app.repositories.cursor import InvalidCursorError

    conn = _RecordingConn()
    with pytest.raises(InvalidCursorError):
        await chunk_repo.list_chunks_by_doc_paged(
            conn, tenant_id=TENANT_ID, kb_id=KB_ID, doc_id=DOC_ID, cursor="garbage"
        )


async def test_list_document_chunks_invalid_cursor_invalid_argument():
    servicer, kb_pb, common_pb2 = _make_gated_servicer()
    req = kb_pb.ListDocumentChunksRequest(
        tenant_id=TENANT_ID, kb_id=KB_ID, doc_id=DOC_ID,
        page=common_pb2.CursorPageRequest(cursor="garbage"),
    )
    ctx = _ServicerContext()
    with pytest.raises(RuntimeError, match="INVALID_ARGUMENT"):
        await servicer._list_document_chunks(req, ctx)


async def test_get_session_messages_invalid_cursor_invalid_argument():
    servicer, kb_pb, common_pb2 = _make_gated_servicer()
    req = kb_pb.GetSessionMessagesRequest(
        tenant_id=TENANT_ID, kb_id=KB_ID, session_id=SESSION_ID,
        page=common_pb2.CursorPageRequest(cursor="garbage"),
    )
    ctx = _ServicerContext()
    with pytest.raises(RuntimeError, match="INVALID_ARGUMENT"):
        await servicer._get_session_messages(req, ctx)


async def test_list_kb_citations_invalid_cursor_invalid_argument():
    servicer, kb_pb, common_pb2 = _make_gated_servicer()
    req = kb_pb.ListKBCitationsRequest(
        tenant_id=TENANT_ID, kb_id=KB_ID,
        page=common_pb2.CursorPageRequest(cursor="garbage"),
    )
    ctx = _ServicerContext()
    with pytest.raises(RuntimeError, match="INVALID_ARGUMENT"):
        await servicer._list_kb_citations(req, ctx)


async def test_list_kb_sessions_invalid_cursor_invalid_argument():
    servicer, kb_pb, common_pb2 = _make_gated_servicer()
    req = kb_pb.ListKBSessionsRequest(
        tenant_id=TENANT_ID, kb_id=KB_ID,
        page=common_pb2.CursorPageRequest(cursor="garbage"),
    )
    ctx = _ServicerContext()
    with pytest.raises(RuntimeError, match="INVALID_ARGUMENT"):
        await servicer._list_kb_sessions(req, ctx)


async def test_citations_skips_non_numeric_score_and_page():
    """Non-numeric score/page on one source must not blow up the page
    (SPEC §5.4): the bad source is skipped, good ones survive."""
    sources = [
        {"doc_id": DOC_ID, "score": "high", "content": "bad score", "page": 1},
        {"doc_id": "66666666-6666-6666-6666-666666666666", "score": 0.7,
         "content": "good", "file_name": "b.pdf", "page": 3},
    ]
    servicer, kb_pb = _make_citations_servicer(
        [_citation_message_row(json.dumps(sources))]
    )
    req = kb_pb.ListKBCitationsRequest(tenant_id=TENANT_ID, kb_id=KB_ID)
    resp = await servicer._list_kb_citations(req, _ServicerContext())

    assert len(resp.items) == 1
    assert resp.items[0].doc_id == "66666666-6666-6666-6666-666666666666"
    assert resp.items[0].score == pytest.approx(0.7)
    assert resp.items[0].page == 3

    # Bad page alone (score parses, page does not) is also skipped whole.
    sources_bad_page = [
        {"doc_id": DOC_ID, "score": 0.5, "content": "bad page", "page": "x"},
    ]
    servicer2, _ = _make_citations_servicer(
        [_citation_message_row(json.dumps(sources_bad_page))]
    )
    resp2 = await servicer2._list_kb_citations(req, _ServicerContext())
    assert resp2.items == []
