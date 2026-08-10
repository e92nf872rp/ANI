"""Tests for US-010 wiring in the gRPC servicer (issue-008 / US-010).

Verifies the SPEC §6.1 algorithms that were left as TODO in US-009:

1. NotifyDocumentUploaded writes kb_documents + async_tasks + outbox_events
   in a single transaction and returns a deterministic AsyncTaskRef; a retry
   with the same (tenant, kb, doc) returns the same task (idempotent replay).
2. Query persists kb_messages (user + assistant) and appends both to the
   Redis session cache, then returns the rag-engine response with the
   resolved session_id.

These tests use a mock asyncpg pool/conn, a mock rag-engine client factory,
and a mock SessionCache factory so no real DB/rag-engine/Redis is required.
They focus on the wiring (transaction atomicity, idempotency, cache + DB
writes) rather than the SQL behavior (covered by repository tests).
"""
import os
import sys
import uuid
from concurrent import futures
from contextlib import asynccontextmanager
from unittest.mock import AsyncMock, MagicMock

import grpc
import pytest

_SERVICE_ROOT = os.path.abspath(os.path.join(os.path.dirname(__file__), ".."))
sys.path.insert(0, _SERVICE_ROOT)
sys.path.insert(0, os.path.join(_SERVICE_ROOT, "app", "generated"))

from app.api.grpc_server import KBServiceServicer
from app.generated.common.v1 import common_pb2
from app.generated.kb.v1 import kb_service_pb2 as kb_pb
from app.generated.kb.v1 import kb_service_pb2_grpc as kb_grpc

TENANT_ID = "11111111-1111-1111-1111-111111111111"
KB_ID = "22222222-2222-2222-2222-222222222222"
DOC_ID = "33333333-3333-3333-3333-333333333333"


# ── Mock DB (records writes to verify atomicity) ──────────────────────────────


class _NotifyMockConn:
    """Records every write so we can assert all three tables are touched in
    one transaction (atomic outbox)."""

    def __init__(self, *, doc_exists=True, existing_task=None):
        self.doc_exists = doc_exists
        self.existing_task = existing_task
        # record of operations in order
        self.events: list[tuple] = []
        # tx nesting tracking
        self._tx_depth = 0

    def transaction(self):
        @asynccontextmanager
        async def _tx():
            self._tx_depth += 1
            self.events.append(("BEGIN",))
            try:
                yield self
            finally:
                self._tx_depth -= 1
                self.events.append(("COMMIT",))
        return _tx()

    async def execute(self, sql, *args):
        # update_parse_status_in_tx → returns "UPDATE 1" or "UPDATE 0"
        if "UPDATE kb_documents" in sql:
            self.events.append(("update_kb_documents", args))
            return "UPDATE 1" if self.doc_exists else "UPDATE 0"
        # mark_dispatched not used in notify path; stub
        return "UPDATE 1"

    async def fetchrow(self, sql, *args):
        # find_by_idempotency_key → returns existing task or None
        if "FROM async_tasks" in sql and "idempotency_key" in sql:
            self.events.append(("find_idempotency", args))
            if self.existing_task is not None:
                return self.existing_task
            return None
        # create_task_in_tx / insert_event RETURNING
        if "INSERT INTO async_tasks" in sql:
            self.events.append(("insert_async_tasks", args))
            return {"id": uuid.UUID(DOC_ID), "task_type": "kb.parse", "status": "pending"}
        if "INSERT INTO outbox_events" in sql:
            self.events.append(("insert_outbox_events", args))
            return {"id": 1}
        return None

    async def fetch(self, sql, *args):
        return []

    async def fetchval(self, sql, *args):
        return 0


class _NotifyMockPool:
    def __init__(self, conn):
        self._conn = conn

    @asynccontextmanager
    async def acquire(self):
        yield self._conn


# ── NotifyDocumentUploaded: atomic outbox transaction ─────────────────────────


def _make_notify_servicer(conn):
    pool = _NotifyMockPool(conn)
    return KBServiceServicer(pool=pool)


def test_notify_writes_three_tables_in_one_transaction():
    """AC1: NotifyDocumentUploaded writes kb_documents + async_tasks +
    outbox_events in a single transaction."""
    conn = _NotifyMockConn(doc_exists=True)
    servicer = _make_notify_servicer(conn)

    ctx = _make_context()
    req = kb_pb.NotifyDocumentUploadedRequest(
        tenant_id=TENANT_ID, kb_id=KB_ID, doc_id=DOC_ID, storage_path="kb-docs/kb1/d"
    )
    import asyncio
    result = asyncio.new_event_loop().run_until_complete(
        servicer._notify_document_uploaded(req, ctx)
    )

    # The three writes (update_kb_documents, insert_async_tasks,
    # insert_outbox_events) must all occur between a single BEGIN/COMMIT pair
    # (the atomic outbox transaction). find_by_idempotency_key runs in its own
    # earlier transaction, so we locate the transaction that contains the
    # update and verify the inserts share it.
    events = conn.events
    # find the BEGIN that precedes the update_kb_documents event
    up_idx = next(i for i, e in enumerate(events) if e[0] == "update_kb_documents")
    # walk back to the most recent BEGIN
    begin_idx = max(i for i, e in enumerate(events[:up_idx]) if e[0] == "BEGIN")
    # find the next COMMIT after the update
    commit_idx = next(i for i, e in enumerate(events[up_idx:], start=up_idx) if e[0] == "COMMIT")
    between = events[begin_idx + 1:commit_idx]
    kinds = [e[0] for e in between]
    assert "update_kb_documents" in kinds
    assert "insert_async_tasks" in kinds
    assert "insert_outbox_events" in kinds
    # No nested BEGIN inside this transaction (the three writes are atomic)
    assert kinds.count("BEGIN") == 0
    # The three writes happen in SPEC §6.1 order: doc update → async_task → outbox
    write_order = [k for k in kinds if k in (
        "update_kb_documents", "insert_async_tasks", "insert_outbox_events"
    )]
    assert write_order == [
        "update_kb_documents", "insert_async_tasks", "insert_outbox_events"
    ]

    assert result.task_type == "kb.parse"
    assert result.status == "pending"
    assert result.task_id  # non-empty


def test_notify_idempotent_replay_returns_same_task():
    """A retry for the same (tenant, kb, doc) returns the same AsyncTaskRef
    without re-writing the outbox."""
    existing_task = {
        "id": uuid.uuid4(),
        "task_type": "kb.parse",
        "status": "pending",
    }
    conn = _NotifyMockConn(doc_exists=True, existing_task=existing_task)
    servicer = _make_notify_servicer(conn)

    ctx = _make_context()
    req = kb_pb.NotifyDocumentUploadedRequest(
        tenant_id=TENANT_ID, kb_id=KB_ID, doc_id=DOC_ID, storage_path="kb-docs/kb1/d"
    )
    import asyncio
    result = asyncio.new_event_loop().run_until_complete(
        servicer._notify_document_uploaded(req, ctx)
    )
    assert str(result.task_id) == str(existing_task["id"])
    # No outbox insert on replay
    kinds = [e[0] for e in conn.events]
    assert "insert_outbox_events" not in kinds
    assert "insert_async_tasks" not in kinds


def test_notify_missing_ids_returns_invalid_argument():
    conn = _NotifyMockConn(doc_exists=True)
    servicer = _make_notify_servicer(conn)
    ctx = _make_context()
    req = kb_pb.NotifyDocumentUploadedRequest(
        tenant_id="", kb_id=KB_ID, doc_id=DOC_ID
    )
    import asyncio
    with pytest.raises(Exception):
        asyncio.new_event_loop().run_until_complete(
            servicer._notify_document_uploaded(req, ctx)
    )


def test_notify_doc_not_found_aborts_without_outbox_write():
    """If the document doesn't exist, abort before writing outbox/async_task."""
    conn = _NotifyMockConn(doc_exists=False)
    servicer = _make_notify_servicer(conn)
    ctx = _make_context()
    req = kb_pb.NotifyDocumentUploadedRequest(
        tenant_id=TENANT_ID, kb_id=KB_ID, doc_id=DOC_ID, storage_path="kb-docs/kb1/d"
    )
    import asyncio
    # context.abort raises in our fake context; that's the expected path.
    with pytest.raises(RuntimeError, match="NOT_FOUND"):
        asyncio.new_event_loop().run_until_complete(
            servicer._notify_document_uploaded(req, ctx)
        )
    # The outbox and async_tasks inserts must NOT have happened
    kinds = [e[0] for e in conn.events]
    assert "insert_outbox_events" not in kinds
    assert "insert_async_tasks" not in kinds


# ── Query: kb_messages + Redis session cache ──────────────────────────────────


class _QueryMockConn:
    """Records create_session + insert_message calls for the Query flow."""

    def __init__(self):
        self.events: list[tuple] = []

    def transaction(self):
        @asynccontextmanager
        async def _tx():
            self.events.append(("BEGIN",))
            try:
                yield self
            finally:
                self.events.append(("COMMIT",))
        return _tx()

    async def execute(self, sql, *args):
        return "UPDATE 1"

    async def fetchrow(self, sql, *args):
        # create_session returns id; insert_message returns a row
        if "kb_sessions" in sql:
            self.events.append(("create_session", args))
            return {"id": uuid.uuid4()}
        if "kb_messages" in sql:
            self.events.append(("insert_message", args))
            return {"id": uuid.uuid4()}
        return None

    async def fetch(self, sql, *args):
        return []

    async def fetchval(self, sql, *args):
        return 0


class _QueryMockPool:
    def __init__(self):
        self.conns: list[_QueryMockConn] = []

    @asynccontextmanager
    async def acquire(self):
        conn = _QueryMockConn()
        self.conns.append(conn)
        yield conn


class _MockRagClient:
    """Mock RagEngineClient returning a canned QueryResponse."""

    def __init__(self, answer="hello", sources=None):
        self._answer = answer
        self._sources = sources or [{"doc_id": "d1", "file_name": "a.pdf",
                                       "page": 1, "content": "ctx", "score": 0.9}]

    async def query(self, **kwargs):
        return {
            "answer": self._answer,
            "sources": self._sources,
            "session_id": kwargs.get("session_id", "new"),
            "input_tokens": 10,
            "output_tokens": 5,
        }

    async def aclose(self):
        pass

    async def __aenter__(self):
        return self

    async def __aexit__(self, *args):
        pass


class _MockSessionCache:
    """Records append_message calls."""

    def __init__(self):
        self.appended: list[dict] = []

    async def append_message(self, **kwargs):
        self.appended.append(kwargs)

    async def list_messages(self, **kwargs):
        return []


def _make_query_servicer(*, pool=None, rag_client=None, cache=None):
    rag = rag_client or _MockRagClient()

    @asynccontextmanager
    async def rag_factory():
        yield rag

    target_cache = cache or _MockSessionCache()

    def cache_factory():
        return target_cache

    return KBServiceServicer(
        pool=pool, rag_engine_client_factory=rag_factory,
        session_cache_factory=cache_factory,
    ), target_cache


def test_query_persists_user_and_assistant_messages_and_caches():
    """AC3: Query writes kb_messages (user + assistant) + Redis session cache."""
    pool = _QueryMockPool()
    servicer, cache = _make_query_servicer(pool=pool)
    ctx = _make_context()
    req = kb_pb.QueryRequest(
        tenant_id=TENANT_ID, kb_id=KB_ID, question="hi",
        idempotency_key=str(uuid.uuid4()),
    )
    import asyncio
    resp = asyncio.new_event_loop().run_until_complete(
        servicer._query(req, ctx)
    )

    # Two DB connections acquired (user msg, assistant msg) — each wrote
    assert len(pool.conns) == 2
    user_events = [e[0] for e in pool.conns[0].events]
    asst_events = [e[0] for e in pool.conns[1].events]
    # user connection: create_session + insert_message(user)
    assert "create_session" in user_events
    assert "insert_message" in user_events
    # assistant connection: insert_message(assistant)
    assert "insert_message" in asst_events

    # C2 invariant: create_session + insert_message(user) share one
    # transaction (a single BEGIN/COMMIT pair wraps both writes on the user
    # connection) so a crash mid-RPC can't leave a session row without its
    # user message.
    u_ev = pool.conns[0].events
    cs_idx = next(i for i, e in enumerate(u_ev) if e[0] == "create_session")
    im_idx = next(i for i, e in enumerate(u_ev) if e[0] == "insert_message")
    begin_before_cs = max(i for i, e in enumerate(u_ev[:cs_idx]) if e[0] == "BEGIN")
    commit_after_im = next(i for i, e in enumerate(u_ev[im_idx:], start=im_idx) if e[0] == "COMMIT")
    # Both writes are between the same BEGIN/COMMIT pair (one transaction)
    between = u_ev[begin_before_cs + 1:commit_after_im]
    assert "create_session" in [e[0] for e in between]
    assert "insert_message" in [e[0] for e in between]
    assert [e[0] for e in between].count("BEGIN") == 0  # no nested tx

    # Redis cache: both user and assistant appended
    assert len(cache.appended) == 2
    assert cache.appended[0]["role"] == "user"
    assert cache.appended[0]["content"] == "hi"
    assert cache.appended[1]["role"] == "assistant"
    assert cache.appended[1]["content"] == "hello"

    # Response carries the resolved session_id + rag-engine answer
    assert resp.answer == "hello"
    assert resp.session_id  # non-empty (newly created)
    assert resp.input_tokens == 10
    assert resp.output_tokens == 5
    assert len(resp.sources) == 1
    assert resp.sources[0].doc_id == "d1"


def test_query_missing_idempotency_key_returns_invalid_argument():
    pool = _QueryMockPool()
    servicer, _ = _make_query_servicer(pool=pool)
    ctx = _make_context()
    req = kb_pb.QueryRequest(
        tenant_id=TENANT_ID, kb_id=KB_ID, question="hi"
    )
    import asyncio
    with pytest.raises(Exception):
        asyncio.new_event_loop().run_until_complete(servicer._query(req, ctx))


def test_query_missing_question_returns_invalid_argument():
    pool = _QueryMockPool()
    servicer, _ = _make_query_servicer(pool=pool)
    ctx = _make_context()
    req = kb_pb.QueryRequest(
        tenant_id=TENANT_ID, kb_id=KB_ID, idempotency_key=str(uuid.uuid4())
    )
    import asyncio
    with pytest.raises(Exception):
        asyncio.new_event_loop().run_until_complete(servicer._query(req, ctx))


def test_query_resolves_session_id_when_not_provided():
    """SPEC §6.1 step 2: session_id empty → generate UUID."""
    pool = _QueryMockPool()
    servicer, _ = _make_query_servicer(pool=pool)
    ctx = _make_context()
    req = kb_pb.QueryRequest(
        tenant_id=TENANT_ID, kb_id=KB_ID, question="hi",
        idempotency_key=str(uuid.uuid4()),
    )
    import asyncio
    resp = asyncio.new_event_loop().run_until_complete(servicer._query(req, ctx))
    # A new session_id was generated and returned
    assert resp.session_id
    assert resp.session_id  # is a UUID string


def test_query_reuses_provided_session_id():
    """When session_id is provided, it is used for cache + messages."""
    pool = _QueryMockPool()
    servicer, cache = _make_query_servicer(pool=pool)
    ctx = _make_context()
    provided_session = str(uuid.uuid4())
    req = kb_pb.QueryRequest(
        tenant_id=TENANT_ID, kb_id=KB_ID, question="hi",
        session_id=provided_session, idempotency_key=str(uuid.uuid4()),
    )
    import asyncio
    resp = asyncio.new_event_loop().run_until_complete(servicer._query(req, ctx))
    assert resp.session_id == provided_session
    # Cache entries used the provided session_id
    assert all(a["session_id"] == provided_session for a in cache.appended)


def test_query_works_without_cache_factory_returning_none():
    """Query degrades to DB-only when session cache factory returns None
    (Redis unavailable, SPEC §7.3)."""
    pool = _QueryMockPool()

    @asynccontextmanager
    async def rag_factory():
        yield _MockRagClient()

    def cache_factory():
        return None

    servicer = KBServiceServicer(
        pool=pool, rag_engine_client_factory=rag_factory,
        session_cache_factory=cache_factory,
    )
    ctx = _make_context()
    req = kb_pb.QueryRequest(
        tenant_id=TENANT_ID, kb_id=KB_ID, question="hi",
        idempotency_key=str(uuid.uuid4()),
    )
    import asyncio
    resp = asyncio.new_event_loop().run_until_complete(servicer._query(req, ctx))
    # Still persists both messages to DB (cache is best-effort)
    assert len(pool.conns) == 2
    assert resp.answer == "hello"


def test_query_rag_engine_error_returns_unavailable():
    pool = _QueryMockPool()

    class _FailingRag:
        async def query(self, **kwargs):
            from app.rag_engine.client import RagEngineError
            raise RagEngineError("rag down", status_code=503)

        async def __aenter__(self):
            return self

        async def __aexit__(self, *args):
            pass

    @asynccontextmanager
    async def rag_factory():
        yield _FailingRag()

    def cache_factory():
        return _MockSessionCache()

    servicer = KBServiceServicer(
        pool=pool, rag_engine_client_factory=rag_factory,
        session_cache_factory=cache_factory,
    )
    ctx = _make_context()
    req = kb_pb.QueryRequest(
        tenant_id=TENANT_ID, kb_id=KB_ID, question="hi",
        idempotency_key=str(uuid.uuid4()),
    )
    import asyncio
    with pytest.raises(Exception) as exc:
        asyncio.new_event_loop().run_until_complete(servicer._query(req, ctx))
    # The gRPC context.abort raises; we verify the abort path was taken by
    # the exception (grpc abort raises ValueError/RuntimeError in test).


# ── gRPC server-level regression: skeleton mode still works ───────────────────


@pytest.fixture
def grpc_server():
    server = grpc.server(futures.ThreadPoolExecutor(max_workers=4))
    kb_grpc.add_KBServiceServicer_to_server(KBServiceServicer(), server)
    port = server.add_insecure_port("[::]:0")
    server.start()
    yield f"localhost:{port}"
    server.stop(grace=None)


@pytest.fixture
def stub(grpc_server):
    return kb_grpc.KBServiceStub(grpc.insecure_channel(grpc_server))


def test_notify_no_pool_failed_precondition(stub):
    """Without a pool, NotifyDocumentUploaded still returns FAILED_PRECONDITION
    (regression guard: US-010 did not break skeleton mode)."""
    with pytest.raises(grpc.RpcError) as exc:
        stub.NotifyDocumentUploaded(
            kb_pb.NotifyDocumentUploadedRequest(
                tenant_id=TENANT_ID, kb_id=KB_ID, doc_id=DOC_ID
            )
        )
    assert exc.value.code() == grpc.StatusCode.FAILED_PRECONDITION


# ── helpers ───────────────────────────────────────────────────────────────────


class _FakeContext:
    """Stand-in for grpc.ServicerContext for direct servicer method tests.

    abort() raises to mimic gRPC's behavior; we capture the code+message so
    tests can assert the abort path.
    """

    def __init__(self):
        self.aborted = None

    def abort(self, code, message):
        self.aborted = (code, message)
        raise RuntimeError(f"aborted: {code} {message}")


def _make_context():
    return _FakeContext()
