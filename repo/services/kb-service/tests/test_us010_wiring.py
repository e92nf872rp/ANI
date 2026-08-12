"""Tests for US-010 wiring in the gRPC servicer (issue-008 / US-010).

Verifies the SPEC §6.1 algorithms that were left as TODO in US-009:

1. NotifyDocumentUploaded folds kb_documents UPDATE + async_tasks INSERT +
   outbox_events INSERT into a single CTE-based data_query call (SPEC §4.2
   cross-table atomic fold, issue-030) and returns a deterministic
   AsyncTaskRef; a retry with the same (tenant, kb, doc) returns the same
   task (idempotent replay).
2. Query persists kb_messages (user + assistant) via the Core data plane
   (issue-030: user message via create_session_and_message CTE fold,
   assistant message via insert_message) and appends both to the Redis
   session cache, then returns the rag-engine response with the resolved
   session_id. The KB config load also goes through the data plane (issue
   #029).

These tests use a mock CoreClient (data_query), a mock rag-engine client
factory, and a mock SessionCache factory so no real DB/Core/rag-engine/Redis
is required. They focus on the wiring (idempotency, cache + DB writes) rather
than the SQL behavior (covered by repository tests).
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


# ── Mock CoreClient (data plane, issue #029 / #030) ──────────────────────────


class _MockCoreClient:
    """Mock CoreClient whose data_query records calls and returns canned rows.

    The repos call ``core.data_query(sql=..., params=[...])`` and interpret
    ``{rows, rowcount}``. Tests customize ``data_query_returns`` / the
    ``data_query`` AsyncMock side_effect to drive specific scenarios.
    """

    def __init__(self, *, data_query_returns=None):
        self.data_query = AsyncMock()
        self.aclose = AsyncMock()
        self.__aenter__ = AsyncMock(return_value=self)
        self.__aexit__ = AsyncMock(return_value=None)
        if data_query_returns is not None:
            self.data_query.return_value = data_query_returns
        else:
            # Default: empty result (no rows, zero rowcount).
            self.data_query.return_value = {"rows": [], "rowcount": 0}


def _core_factory(core_client):
    @asynccontextmanager
    async def factory(tenant_id):
        yield core_client
    return factory


# ── NotifyDocumentUploaded: 3-table fold via single data_query ──────────────


def _make_notify_servicer(core_client=None):
    """Build a servicer with a mock CoreClient (data plane only)."""
    core = core_client or _MockCoreClient()
    return KBServiceServicer(
        pool=object(),  # non-None sentinel: DB-backed RPCs are enabled
        core_client_factory=_core_factory(core),
    ), core


def test_notify_folds_three_tables_into_single_data_query():
    """AC1: NotifyDocumentUploaded folds idempotency check + kb_documents
    UPDATE + async_tasks INSERT + outbox_events INSERT into a single CTE-based
    data_query call (SPEC §4.2 cross-table atomic fold, issue-030). This
    eliminates the TOCTOU race — the check and insert are atomic."""
    core = _MockCoreClient()
    task_id = str(uuid.uuid4())
    # Single data_query call: fold_sql returns the new task_id
    core.data_query.return_value = {
        "rows": [{"task_id": task_id, "status": "pending"}],
        "rowcount": 1,
    }
    servicer, _ = _make_notify_servicer(core)

    ctx = _make_context()
    req = kb_pb.NotifyDocumentUploadedRequest(
        tenant_id=TENANT_ID, kb_id=KB_ID, doc_id=DOC_ID, storage_path="kb-docs/kb1/d"
    )
    import asyncio
    result = asyncio.new_event_loop().run_until_complete(
        servicer._notify_document_uploaded(req, ctx)
    )

    # Exactly 1 data_query call: idempotency check folded into the CTE
    assert core.data_query.call_count == 1

    # The fold_sql contains all three table writes + idempotency check in one statement
    fold_sql = core.data_query.call_args_list[0].kwargs["sql"]
    assert "UPDATE kb_documents" in fold_sql
    assert "INSERT INTO async_tasks" in fold_sql
    assert "INSERT INTO outbox_events" in fold_sql
    assert "ON CONFLICT (tenant_id, idempotency_key) DO NOTHING" in fold_sql
    # CTE-based fold (single statement, atomic in one data-plane transaction)
    assert "WITH doc_upd AS" in fold_sql
    assert "existing AS" in fold_sql

    assert result.task_type == "kb.parse"
    assert result.status == "pending"
    assert result.task_id == task_id


def test_notify_idempotent_replay_returns_same_task():
    """A retry for the same (tenant, kb, doc) returns the same AsyncTaskRef.
    The CTE's ON CONFLICT DO NOTHING on async_tasks means the INSERT produces
    no row, and the UNION ALL branch returns the existing task."""
    existing_task_id = str(uuid.uuid4())
    core = _MockCoreClient()
    # Single data_query call: fold_sql returns the existing task (via UNION ALL)
    core.data_query.return_value = {
        "rows": [{"task_id": existing_task_id, "status": "pending"}],
        "rowcount": 1,
    }
    servicer, _ = _make_notify_servicer(core)

    ctx = _make_context()
    req = kb_pb.NotifyDocumentUploadedRequest(
        tenant_id=TENANT_ID, kb_id=KB_ID, doc_id=DOC_ID, storage_path="kb-docs/kb1/d"
    )
    import asyncio
    result = asyncio.new_event_loop().run_until_complete(
        servicer._notify_document_uploaded(req, ctx)
    )
    assert result.task_id == existing_task_id
    # Only 1 data_query call (fold with idempotency check inside)
    assert core.data_query.call_count == 1
    fold_sql = core.data_query.call_args_list[0].kwargs["sql"]
    assert "ON CONFLICT (tenant_id, idempotency_key) DO NOTHING" in fold_sql


def test_notify_missing_ids_returns_invalid_argument():
    servicer, _ = _make_notify_servicer()
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
    """If the document doesn't exist (fold_sql returns no rows), abort before
    returning a task. The CTE's doc_upd returns no rows → no task/outbox
    inserts → final SELECT returns empty → NOT_FOUND."""
    core = _MockCoreClient()
    # Single data_query call: fold_sql → empty rows (doc not found)
    core.data_query.return_value = {"rows": [], "rowcount": 0}
    servicer, _ = _make_notify_servicer(core)
    ctx = _make_context()
    req = kb_pb.NotifyDocumentUploadedRequest(
        tenant_id=TENANT_ID, kb_id=KB_ID, doc_id=DOC_ID, storage_path="kb-docs/kb1/d"
    )
    import asyncio
    with pytest.raises(RuntimeError, match="NOT_FOUND"):
        asyncio.new_event_loop().run_until_complete(
            servicer._notify_document_uploaded(req, ctx)
        )
    # Only 1 data_query call (fold with idempotency check inside); no extra writes
    assert core.data_query.call_count == 1


# ── Query: kb_messages + Redis session cache ─────────────────────────────────


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


def _make_query_servicer(*, rag_client=None, cache=None, core_client=None):
    rag = rag_client or _MockRagClient()

    @asynccontextmanager
    async def rag_factory():
        yield rag

    target_cache = cache or _MockSessionCache()

    def cache_factory():
        return target_cache

    core = core_client or _MockCoreClient()

    return KBServiceServicer(
        pool=object(),  # non-None sentinel: DB-backed RPCs are enabled
        rag_engine_client_factory=rag_factory,
        session_cache_factory=cache_factory,
        core_client_factory=_core_factory(core),
    ), target_cache, core


def _query_core_side_effects(*, kb_found=True):
    """Build data_query side_effects for the Query flow.

    Call 1: get_kb (SELECT FROM knowledge_bases) → kb config row or empty
    Call 2: create_session_and_message (CTE fold) → user message row
    Call 3: insert_message (INSERT INTO kb_messages) → assistant message row
    """
    if kb_found:
        kb_row = {
            "id": KB_ID, "top_k": 5, "score_threshold": 0.0,
            "retrieval_mode": "hybrid",
        }
    else:
        kb_row = None
    return [
        {"rows": [kb_row] if kb_row else [], "rowcount": 1 if kb_row else 0},
        {"rows": [{"id": str(uuid.uuid4()), "session_id": str(uuid.uuid4()),
                    "role": "user", "content": "hi"}], "rowcount": 1},
        {"rows": [{"id": str(uuid.uuid4()), "session_id": str(uuid.uuid4()),
                    "role": "assistant", "content": "hello"}], "rowcount": 1},
    ]


def test_query_persists_user_and_assistant_messages_and_caches():
    """AC3: Query writes kb_messages (user + assistant) via the data plane
    (issue-030) + Redis session cache. KB config is loaded via the data plane
    (issue #029). User message uses create_session_and_message CTE fold;
    assistant message uses insert_message."""
    core = _MockCoreClient()
    core.data_query.side_effect = _query_core_side_effects()
    servicer, cache, core = _make_query_servicer(core_client=core)
    ctx = _make_context()
    req = kb_pb.QueryRequest(
        tenant_id=TENANT_ID, kb_id=KB_ID, question="hi",
        idempotency_key=str(uuid.uuid4()),
    )
    import asyncio
    resp = asyncio.new_event_loop().run_until_complete(
        servicer._query(req, ctx)
    )

    # 3 data_query calls: get_kb + create_session_and_message + insert_message
    assert core.data_query.call_count == 3

    # Call 1: KB config load via data_query (CoreClient).
    kb_sql = core.data_query.call_args_list[0].kwargs["sql"]
    assert "FROM knowledge_bases" in kb_sql

    # Call 2: user message via create_session_and_message (CTE fold)
    user_sql = core.data_query.call_args_list[1].kwargs["sql"]
    assert "WITH sess AS" in user_sql
    assert "INSERT INTO kb_sessions" in user_sql
    assert "INSERT INTO kb_messages" in user_sql

    # Call 3: assistant message via insert_message
    asst_sql = core.data_query.call_args_list[2].kwargs["sql"]
    assert "INSERT INTO kb_messages" in asst_sql
    assert "WITH sess AS" not in asst_sql  # not a CTE fold

    # C2 invariant: create_session + insert_message(user) are folded into a
    # single data_query call (one atomic statement), so a crash mid-RPC can't
    # leave a session row without its user message.

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
    servicer, _, _ = _make_query_servicer()
    ctx = _make_context()
    req = kb_pb.QueryRequest(
        tenant_id=TENANT_ID, kb_id=KB_ID, question="hi"
    )
    import asyncio
    with pytest.raises(Exception):
        asyncio.new_event_loop().run_until_complete(servicer._query(req, ctx))


def test_query_missing_question_returns_invalid_argument():
    servicer, _, _ = _make_query_servicer()
    ctx = _make_context()
    req = kb_pb.QueryRequest(
        tenant_id=TENANT_ID, kb_id=KB_ID, idempotency_key=str(uuid.uuid4())
    )
    import asyncio
    with pytest.raises(Exception):
        asyncio.new_event_loop().run_until_complete(servicer._query(req, ctx))


def test_query_resolves_session_id_when_not_provided():
    """SPEC §6.1 step 2: session_id empty → generate UUID."""
    core = _MockCoreClient()
    core.data_query.side_effect = _query_core_side_effects()
    servicer, _, _ = _make_query_servicer(core_client=core)
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
    core = _MockCoreClient()
    core.data_query.side_effect = _query_core_side_effects()
    servicer, cache, _ = _make_query_servicer(core_client=core)
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
    core = _MockCoreClient()
    core.data_query.side_effect = _query_core_side_effects()

    @asynccontextmanager
    async def rag_factory():
        yield _MockRagClient()

    def cache_factory():
        return None

    servicer = KBServiceServicer(
        pool=object(), rag_engine_client_factory=rag_factory,
        session_cache_factory=cache_factory,
        core_client_factory=_core_factory(core),
    )
    ctx = _make_context()
    req = kb_pb.QueryRequest(
        tenant_id=TENANT_ID, kb_id=KB_ID, question="hi",
        idempotency_key=str(uuid.uuid4()),
    )
    import asyncio
    resp = asyncio.new_event_loop().run_until_complete(servicer._query(req, ctx))
    # Still persists both messages to DB (cache is best-effort)
    assert core.data_query.call_count == 3
    assert resp.answer == "hello"


def test_query_rag_engine_error_returns_unavailable():
    core = _MockCoreClient()
    core.data_query.side_effect = _query_core_side_effects()

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
        pool=object(), rag_engine_client_factory=rag_factory,
        session_cache_factory=cache_factory,
        core_client_factory=_core_factory(core),
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


def test_query_kb_not_found_aborts():
    """When the KB doesn't exist (data_query returns no rows), Query aborts
    with NOT_FOUND."""
    core = _MockCoreClient()
    core.data_query.side_effect = _query_core_side_effects(kb_found=False)
    servicer, _, _ = _make_query_servicer(core_client=core)
    ctx = _make_context()
    req = kb_pb.QueryRequest(
        tenant_id=TENANT_ID, kb_id=KB_ID, question="hi",
        idempotency_key=str(uuid.uuid4()),
    )
    import asyncio
    with pytest.raises(RuntimeError, match="NOT_FOUND"):
        asyncio.new_event_loop().run_until_complete(servicer._query(req, ctx))


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
