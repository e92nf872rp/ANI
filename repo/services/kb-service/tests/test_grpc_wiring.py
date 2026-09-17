"""Tests for gRPC server wiring (issue-007 / US-009).

Verifies:
- CreateKB calls Core POST /vector-stores and writes knowledge_bases + async_tasks.
- DeleteKB soft-deletes KB and calls Core DELETE /vector-stores/{id}.
- The 10 P0 RPCs are still declared on the servicer (regression of issue-006).
- The 3 P1 RPCs still return UNIMPLEMENTED.

These tests use a mock asyncpg pool and a mock CoreClient factory so no real
DB or Core gateway is required. They focus on the wiring logic (Core API call
happens, correct vector_store_id derived, errors mapped) rather than the SQL
behavior (covered by the repository layer + integration tests).
"""
import os
import sys
import uuid
from concurrent import futures
from contextlib import asynccontextmanager
from unittest.mock import AsyncMock, MagicMock

import asyncpg
import grpc
import httpx
import pytest

_SERVICE_ROOT = os.path.abspath(os.path.join(os.path.dirname(__file__), ".."))
sys.path.insert(0, _SERVICE_ROOT)
sys.path.insert(0, os.path.join(_SERVICE_ROOT, "app", "generated"))

from app.api.grpc_server import KBServiceServicer, _vector_store_name
from app.core_api.client import CoreAPIError, CoreClient
from app.generated.common.v1 import common_pb2
from app.generated.kb.v1 import kb_service_pb2 as kb_pb
from app.generated.kb.v1 import kb_service_pb2_grpc as kb_grpc


TENANT_ID = "11111111-1111-1111-1111-111111111111"
KB_ID = "22222222-2222-2222-2222-222222222222"


# ── regression: all 13 RPCs declared ──────────────────────────────────────────

P0_RPCS = [
    "CreateKB", "GetKB", "ListKBs", "DeleteKB",
    "GetDocumentUploadURL", "NotifyDocumentUploaded",
    "GetDocument", "ListDocuments", "DeleteDocument", "Query",
]
P1_RPCS = ["ListKBCitations", "ListKBSessions", "UpdateKBPermissions", "RebuildKB"]


def test_servicer_still_declares_all_rpcs():
    servicer = KBServiceServicer()
    for name in P0_RPCS + P1_RPCS:
        assert hasattr(servicer, name), f"servicer missing RPC {name}"


def test_vector_store_name_derivation():
    name = _vector_store_name(KB_ID)
    assert name == f"kb_{KB_ID.replace('-', '')}"
    assert "-" not in name


# ── mock helpers ──────────────────────────────────────────────────────────────


class _MockConn:
    """Minimal asyncpg.Connection mock for repository calls."""

    def __init__(self):
        self._rows: dict = {}
        self._next_id = 0

    def transaction(self):
        # Return an async context manager
        @asynccontextmanager
        async def _tx():
            yield self
        return _tx()

    async def execute(self, sql, *args):
        return "UPDATE 1"

    async def fetchrow(self, sql, *args):
        return None

    async def fetch(self, sql, *args):
        return []

    async def fetchval(self, sql, *args):
        return 0


class _MockPool:
    """Minimal asyncpg.Pool mock returning _MockConn on acquire."""

    @asynccontextmanager
    async def acquire(self):
        yield _MockConn()


class _MockCoreClient:
    """Mock CoreClient that records calls and returns canned responses."""

    def __init__(self):
        self.calls: list[tuple[str, dict]] = []
        self._fail = False

    def fail_next(self):
        self._fail = True

    async def create_vector_store(self, **kwargs):
        self.calls.append(("create_vector_store", kwargs))
        if self._fail:
            raise CoreAPIError("core down", status_code=503, code="UNAVAILABLE")
        return {"id": _vector_store_name(KB_ID), "name": kwargs["name"]}

    async def set_knowledge_base_link(self, **kwargs):
        self.calls.append(("set_knowledge_base_link", kwargs))
        return {"id": kwargs["vector_store_id"]}

    async def delete_vector_store(self, **kwargs):
        self.calls.append(("delete_vector_store", kwargs))
        return {"id": kwargs["vector_store_id"]}

    async def delete_vector_store_documents(self, **kwargs):
        self.calls.append(("delete_vector_store_documents", kwargs))
        return {"deleted_count": 0}

    async def request_upload_url(self, **kwargs):
        self.calls.append(("request_upload_url", kwargs))
        return {"upload_url": "http://minio.test/put", "object_id": str(uuid.uuid4())}

    async def aclose(self):
        pass

    async def __aenter__(self):
        return self

    async def __aexit__(self, *args):
        pass


def _make_servicer(core_client: _MockCoreClient | None = None):
    mock_core = core_client or _MockCoreClient()

    @asynccontextmanager
    def factory(tenant_id):
        yield mock_core

    pool = _MockPool()
    return KBServiceServicer(pool=pool, core_client_factory=factory), mock_core


# ── gRPC server fixtures ──────────────────────────────────────────────────────


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


# ── skeleton mode: no pool → FAILED_PRECONDITION ─────────────────────────────


def test_get_kb_without_pool_returns_failed_precondition(stub):
    """Without a DB pool, GetKB returns FAILED_PRECONDITION (not UNIMPLEMENTED)."""
    with pytest.raises(grpc.RpcError) as exc:
        stub.GetKB(kb_pb.GetKBRequest(tenant_id=TENANT_ID, kb_id=KB_ID))
    # US-009 changes skeleton from UNIMPLEMENTED to FAILED_PRECONDITION when
    # the servicer is constructed without a pool (test/skeleton mode).
    assert exc.value.code() == grpc.StatusCode.FAILED_PRECONDITION


def test_list_kbs_without_pool_returns_failed_precondition(stub):
    with pytest.raises(grpc.RpcError) as exc:
        stub.ListKBs(kb_pb.ListKBsRequest(tenant_id=TENANT_ID, page=common_pb2.CursorPageRequest(limit=20)))
    assert exc.value.code() == grpc.StatusCode.FAILED_PRECONDITION


def test_delete_kb_without_pool_returns_failed_precondition(stub):
    with pytest.raises(grpc.RpcError) as exc:
        stub.DeleteKB(kb_pb.DeleteKBRequest(tenant_id=TENANT_ID, kb_id=KB_ID))
    assert exc.value.code() == grpc.StatusCode.FAILED_PRECONDITION


# ── P1 RPCs: B2 RPCs wired; permissions pair wired in B4 ─────────────────────


def test_update_kb_permissions_b4_wired_not_unimplemented(stub):
    """B4 (kb-p1-plan §2.5): UpdateKBPermissions is implemented. Constructed
    without a pool it must return FAILED_PRECONDITION — never UNIMPLEMENTED
    (that would mean the P1 stub still shadows the servicer)."""
    with pytest.raises(grpc.RpcError) as exc:
        stub.UpdateKBPermissions(
            kb_pb.UpdateKBPermissionsRequest(
                tenant_id=TENANT_ID, kb_id=KB_ID, idempotency_key=str(uuid.uuid4())
            )
        )
    assert exc.value.code() != grpc.StatusCode.UNIMPLEMENTED
    assert exc.value.code() == grpc.StatusCode.FAILED_PRECONDITION


def test_get_kb_permissions_b4_wired_not_unimplemented(stub):
    """B4 (kb-p1-plan §2.5): GetKBPermissions is implemented; without a pool
    it returns FAILED_PRECONDITION — never UNIMPLEMENTED."""
    with pytest.raises(grpc.RpcError) as exc:
        stub.GetKBPermissions(kb_pb.GetKBPermissionsRequest(tenant_id=TENANT_ID, kb_id=KB_ID))
    assert exc.value.code() != grpc.StatusCode.UNIMPLEMENTED
    assert exc.value.code() == grpc.StatusCode.FAILED_PRECONDITION


def test_b2_rpcs_wired_not_unimplemented(stub):
    """ListKBCitations/ListKBSessions are implemented in B2 (issue-045).

    Constructed without a pool they must return FAILED_PRECONDITION — never
    UNIMPLEMENTED (that would mean the P1 stub still shadows the servicer).
    """
    for rpc, req in [
        ("ListKBCitations", kb_pb.ListKBCitationsRequest(tenant_id=TENANT_ID, kb_id=KB_ID)),
        ("ListKBSessions", kb_pb.ListKBSessionsRequest(tenant_id=TENANT_ID, kb_id=KB_ID)),
        ("ListDocumentChunks", kb_pb.ListDocumentChunksRequest(tenant_id=TENANT_ID, kb_id=KB_ID, doc_id=str(uuid.uuid4()))),
        ("GetSessionMessages", kb_pb.GetSessionMessagesRequest(tenant_id=TENANT_ID, kb_id=KB_ID, session_id=str(uuid.uuid4()))),
        ("DeleteSession", kb_pb.DeleteSessionRequest(tenant_id=TENANT_ID, kb_id=KB_ID, session_id=str(uuid.uuid4()))),
    ]:
        with pytest.raises(grpc.RpcError) as exc:
            getattr(stub, rpc)(req)
        assert exc.value.code() != grpc.StatusCode.UNIMPLEMENTED
        assert exc.value.code() == grpc.StatusCode.FAILED_PRECONDITION


def test_b2_rpcs_wired_not_unimplemented(stub):
    """ListKBCitations/ListKBSessions are implemented in B2 (issue-045).

    Constructed without a pool they must return FAILED_PRECONDITION — never
    UNIMPLEMENTED (that would mean the P1 stub still shadows the servicer).
    """
    for rpc, req in [
        ("ListKBCitations", kb_pb.ListKBCitationsRequest(tenant_id=TENANT_ID, kb_id=KB_ID)),
        ("ListKBSessions", kb_pb.ListKBSessionsRequest(tenant_id=TENANT_ID, kb_id=KB_ID)),
        ("ListDocumentChunks", kb_pb.ListDocumentChunksRequest(tenant_id=TENANT_ID, kb_id=KB_ID, doc_id=str(uuid.uuid4()))),
        ("GetSessionMessages", kb_pb.GetSessionMessagesRequest(tenant_id=TENANT_ID, kb_id=KB_ID, session_id=str(uuid.uuid4()))),
        ("DeleteSession", kb_pb.DeleteSessionRequest(tenant_id=TENANT_ID, kb_id=KB_ID, session_id=str(uuid.uuid4()))),
    ]:
        with pytest.raises(grpc.RpcError) as exc:
            getattr(stub, rpc)(req)
        assert exc.value.code() != grpc.StatusCode.UNIMPLEMENTED
        assert exc.value.code() == grpc.StatusCode.FAILED_PRECONDITION


# ── CreateKB validation ───────────────────────────────────────────────────────


def test_create_kb_missing_tenant_returns_invalid_argument(stub):
    with pytest.raises(grpc.RpcError) as exc:
        stub.CreateKB(kb_pb.CreateKBRequest(name="kb1"))
    assert exc.value.code() == grpc.StatusCode.INVALID_ARGUMENT


def test_create_kb_missing_name_returns_invalid_argument(stub):
    with pytest.raises(grpc.RpcError) as exc:
        stub.CreateKB(kb_pb.CreateKBRequest(tenant_id=TENANT_ID))
    assert exc.value.code() == grpc.StatusCode.INVALID_ARGUMENT


# ── CreateKB name conflict → ALREADY_EXISTS ───────────────────────────────────


def test_create_kb_name_conflict_returns_already_exists():
    """CreateKB hits UNIQUE(tenant_id, name) on the knowledge_bases INSERT.
    Two paths: (a) a genuine name conflict with an active KB, or (b) a retry
    whose prior attempt committed the kb INSERT but crashed before the
    async_tasks record was written (replay check finds nothing, so this
    retry re-inserts). Both must surface ALREADY_EXISTS, not UNKNOWN."""
    class _ConflictConn:
        """Minimal conn: the replay find returns nothing; the kb INSERT
        raises UniqueViolationError (SQLSTATE 23505)."""

        def __init__(self):
            self.events: list[str] = []

        def transaction(self):
            @asynccontextmanager
            async def _tx():
                yield self
            return _tx()

        async def execute(self, sql, *args):
            return "UPDATE 1"

        async def fetchrow(self, sql, *args):
            if "INSERT INTO knowledge_bases" in sql:
                self.events.append("insert_kb")
                raise asyncpg.UniqueViolationError(
                    'duplicate key value violates unique constraint '
                    '"knowledge_bases_tenant_id_name_key"'
                )
            if "FROM async_tasks" in sql and "idempotency_key" in sql:
                self.events.append("find_idempotency")
                return None  # no replay record (crash window / fresh conflict)
            return None

        async def fetch(self, sql, *args):
            return []

        async def fetchval(self, sql, *args):
            return 0

    class _SingleConnPool:
        @asynccontextmanager
        async def acquire(self):
            yield _ConflictConn()

    @asynccontextmanager
    async def core_factory(tenant_id):
        yield core

    core = _MockCoreClient()
    servicer = KBServiceServicer(pool=_SingleConnPool(), core_client_factory=core_factory)
    req = kb_pb.CreateKBRequest(tenant_id=TENANT_ID, name="kb-taken")

    import asyncio

    with pytest.raises(RuntimeError, match="ALREADY_EXISTS"):
        asyncio.new_event_loop().run_until_complete(
            servicer._create_kb(req, _StubContext())
        )
    # The abort happens before the Core step and before any async_tasks
    # write — a 409 path must stay side-effect free.
    assert core.calls == []


# ── CreateKB poison-key self-heal ────────────────────────────────────────────


def test_create_kb_poison_key_self_heals():
    """A prior CreateKB attempt crashed between create_task and complete_task,
    leaving an async_tasks row (pending, result=NULL). The replay check skips
    it (no result), the KB insert + Core vector-store call re-run, and the
    task INSERT now violates UNIQUE(tenant_id, idempotency_key). The servicer
    must reuse the pending row, complete it, and return success."""
    poison_task_id = uuid.uuid4()

    class _PoisonConn:
        """Minimal conn for the CreateKB poison-key flow.

        find_by_idempotency_key: first call returns the stale pending row
        (no result → no replay); the post-violation re-find returns it too.
        create_task INSERT raises UniqueViolationError.
        """

        def __init__(self):
            self.events: list[str] = []

        def transaction(self):
            @asynccontextmanager
            async def _tx():
                yield self
            return _tx()

        async def execute(self, sql, *args):
            return "UPDATE 1"

        async def fetchrow(self, sql, *args):
            if "INSERT INTO knowledge_bases" in sql:
                self.events.append("insert_kb")
                return {
                    "id": uuid.uuid4(),
                    "tenant_id": uuid.UUID(TENANT_ID),
                    "name": "kb-poison",
                    "description": "",
                    "embedding_model": "bge-m3",
                    "chunk_size": 1024,
                    "top_k": 5,
                    "score_threshold": 0.0,
                    "retrieval_mode": "hybrid",
                    "status": "active",
                    "doc_count": 0,
                }
            if "FROM async_tasks" in sql and "idempotency_key" in sql:
                self.events.append("find_idempotency")
                return {
                    "id": poison_task_id,
                    "status": "pending",  # stale: never completed
                    "result": None,
                }
            if "INSERT INTO async_tasks" in sql:
                self.events.append("insert_async_tasks")
                raise asyncpg.UniqueViolationError(
                    'duplicate key value violates unique constraint '
                    '"async_tasks_tenant_id_idempotency_key_key"'
                )
            if "INSERT INTO kb_audit_log" in sql:
                self.events.append("insert_audit")
                return {"id": uuid.uuid4()}
            return None

        async def fetch(self, sql, *args):
            return []

        async def fetchval(self, sql, *args):
            return 0

    class _SingleConnPool:
        @asynccontextmanager
        async def acquire(self):
            yield _PoisonConn()

    @asynccontextmanager
    async def core_factory(tenant_id):
        yield _MockCoreClient()

    servicer = KBServiceServicer(pool=_SingleConnPool(), core_client_factory=core_factory)
    req = kb_pb.CreateKBRequest(tenant_id=TENANT_ID, name="kb-poison")

    import asyncio

    result = asyncio.new_event_loop().run_until_complete(
        servicer._create_kb(req, _StubContext())
    )

    # Retry self-heals: returns the KB row instead of UNKNOWN.
    assert result.name == "kb-poison"


class _StubContext:
    """grpc.ServicerContext stand-in (abort raises)."""

    def abort(self, code, message):
        raise RuntimeError(f"aborted: {code} {message}")


# ── CreateKB: replay of a soft-deleted KB must re-create (B-fix) ─────────────


def test_create_kb_replay_of_deleted_kb_recreates():
    """Bug: delete → same-name re-create returned the deleted KB's old id
    and the new KB never appeared in the list. The name-fallback key
    create_kb:{tenant}:{name} is shared by every same-name create in the
    tenant, so the replay check finds the deleted KB's completed task row.
    The guard must skip the stale replay (get_kb hides deleted rows →
    None) and take the INSERT path: a NEW kb id, not the deleted KB's
    snapshot. The Core idempotency keys must also be derived per-request
    (create_vs:{tenant}:{kb_id}), never the shared fallback key."""
    new_kb_id = uuid.uuid4()

    class _ReplayDeletedConn:
        """find_by_idempotency_key → a completed task whose result is the
        DELETED KB's snapshot (id = the OLD kb id); get_kb → None
        (soft-deleted rows are hidden); the kb INSERT → a fresh row."""

        def __init__(self):
            self.events: list[str] = []

        def transaction(self):
            @asynccontextmanager
            async def _tx():
                yield self
            return _tx()

        async def execute(self, sql, *args):
            return "UPDATE 1"

        async def fetchrow(self, sql, *args):
            if "FROM async_tasks" in sql and "idempotency_key" in sql:
                self.events.append("find_idempotency")
                return {
                    "id": uuid.uuid4(),
                    "status": "completed",
                    # JSONB snapshot of the deleted KB (OLD id).
                    "result": {
                        "id": KB_ID,
                        "tenant_id": TENANT_ID,
                        "name": "kb-replay",
                        "status": "deleted",
                    },
                }
            if "INSERT INTO knowledge_bases" in sql:
                self.events.append("insert_kb")
                return {
                    "id": new_kb_id,
                    "tenant_id": uuid.UUID(TENANT_ID),
                    "name": "kb-replay",
                    "description": "",
                    "embedding_model": "bge-m3",
                    "chunk_size": 1024,
                    "top_k": 5,
                    "score_threshold": 0.0,
                    "retrieval_mode": "hybrid",
                    "status": "active",
                    "doc_count": 0,
                }
            if "INSERT INTO kb_audit_log" in sql:
                self.events.append("insert_audit")
                return {"id": uuid.uuid4()}
            if "INSERT INTO async_tasks" in sql:
                self.events.append("insert_async_tasks")
                return {"id": uuid.uuid4(), "status": "pending"}
            if "FROM knowledge_bases" in sql:
                # get_kb guard: the recorded KB is soft-deleted → hidden.
                self.events.append("get_kb_deleted")
                return None
            return None

        async def fetch(self, sql, *args):
            return []

        async def fetchval(self, sql, *args):
            return 0

    conn = _ReplayDeletedConn()

    class _SharedPool:
        @asynccontextmanager
        async def acquire(self):
            yield conn

    @asynccontextmanager
    async def core_factory(tenant_id):
        yield core

    core = _MockCoreClient()
    servicer = KBServiceServicer(
        pool=_SharedPool(), core_client_factory=core_factory
    )
    req = kb_pb.CreateKBRequest(tenant_id=TENANT_ID, name="kb-replay")

    import asyncio

    result = asyncio.new_event_loop().run_until_complete(
        servicer._create_kb(req, _StubContext())
    )

    # The re-create took the INSERT path — not the deleted KB's snapshot.
    assert "insert_kb" in conn.events
    assert str(result.id) != KB_ID
    assert str(result.id) == str(new_kb_id)
    assert result.name == "kb-replay"
    # Core idempotency keys are derived per-request (never the shared
    # fallback key, which would replay the deleted KB's vector store).
    create_calls = [c for c in core.calls if c[0] == "create_vector_store"]
    assert len(create_calls) == 1
    assert create_calls[0][1]["idempotency_key"] == (
        f"create_vs:{TENANT_ID}:{new_kb_id}"
    )
    link_calls = [c for c in core.calls if c[0] == "set_knowledge_base_link"]
    assert len(link_calls) == 1
    assert link_calls[0][1]["idempotency_key"] == (
        f"create_vs:{TENANT_ID}:{new_kb_id}:kblink"
    )


# ── DeleteKB: kb.create replay-record cleanup (C-fix) ────────────────────────


def test_delete_kb_cleans_create_replay_records():
    """DeleteKB must drop the deleted KB's kb.create async_tasks replay
    rows (C-fix): the create fallback key create_kb:{tenant}:{name} is
    shared by every same-name create, so a surviving row replays the
    deleted KB's snapshot on the next same-name create. The cleanup runs
    after the soft-delete, is best-effort, and does not block the Core
    vector-store cleanup."""
    class _DeleteConn:
        def __init__(self):
            self.execute_calls: list[tuple] = []
            self._kb_row = {
                "id": uuid.UUID(KB_ID),
                "tenant_id": uuid.UUID(TENANT_ID),
                "name": "kb",
                "description": "desc",
                "embedding_model": "bge-m3",
                "chunk_size": 512,
                "top_k": 5,
                "score_threshold": 0.3,
                "retrieval_mode": "hybrid",
                "status": "active",
                "doc_count": 0,
                "default_inference_service": None,
                "vector_store_id": "77777777-7777-7777-7777-777777777777",
            }

        def transaction(self):
            @asynccontextmanager
            async def _tx():
                yield self
            return _tx()

        async def execute(self, sql, *args):
            self.execute_calls.append((sql, args))
            if sql.startswith("SET LOCAL"):
                return None
            if "DELETE FROM async_tasks" in sql:
                return "DELETE 1"
            return "UPDATE 1"

        async def fetchrow(self, sql, *args):
            if "INSERT INTO kb_audit_log" in sql:
                return {"id": uuid.uuid4()}
            if "FROM knowledge_bases" in sql:
                return self._kb_row
            return None

        async def fetch(self, sql, *args):
            return []

        async def fetchval(self, sql, *args):
            return 0

    conn = _DeleteConn()

    class _SharedPool:
        @asynccontextmanager
        async def acquire(self):
            yield conn

    @asynccontextmanager
    async def core_factory(tenant_id):
        yield core

    core = _MockCoreClient()
    servicer = KBServiceServicer(
        pool=_SharedPool(), core_client_factory=core_factory
    )
    req = kb_pb.DeleteKBRequest(tenant_id=TENANT_ID, kb_id=KB_ID)

    import asyncio

    result = asyncio.new_event_loop().run_until_complete(
        servicer._delete_kb(req, _StubContext())
    )
    assert result is not None

    # The soft-delete ran…
    soft_delete = [
        (s, a) for s, a in conn.execute_calls
        if "UPDATE knowledge_bases" in s and "status = 'deleted'" in s
    ]
    assert len(soft_delete) == 1
    # …and the kb.create replay rows were deleted afterwards.
    replay_delete = [
        (s, a) for s, a in conn.execute_calls
        if "DELETE FROM async_tasks" in s
    ]
    assert len(replay_delete) == 1
    sql, args = replay_delete[0]
    assert "task_type = 'kb.create'" in sql
    assert args == (uuid.UUID(TENANT_ID), uuid.UUID(KB_ID))
    assert conn.execute_calls.index(soft_delete[0]) < conn.execute_calls.index(
        replay_delete[0]
    )
    # The Core best-effort vector cleanup still runs.
    assert any(c[0] == "delete_vector_store" for c in core.calls)
